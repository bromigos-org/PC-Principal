package preflight

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
)

type DiscordClient interface {
	CurrentUser(ctx context.Context) (*discordgo.User, error)
	Guilds(ctx context.Context) ([]*discordgo.UserGuild, error)
	GuildChannels(ctx context.Context, guildID string) ([]*discordgo.Channel, error)
	UserChannelPermissions(ctx context.Context, userID string, channelID string) (int64, error)
	GuildRoles(ctx context.Context, guildID string) error
	GuildMembers(ctx context.Context, guildID string) error
}

type Probe struct {
	client DiscordClient
	config Config
}

func NewProbe(client DiscordClient, config Config) Probe {
	return Probe{client: client, config: config}
}

func (p Probe) Run(ctx context.Context) Report {
	report := Report{MessageContentAvailable: hasIntent(p.config.Intents, discordgo.IntentMessageContent)}
	report.Warnings = append(report.Warnings, p.intentWarnings()...)

	bot, err := p.client.CurrentUser(ctx)
	if err != nil {
		report.Warnings = append(report.Warnings, Warning{Code: WarningGuildChannelsUnavailable, Detail: fmt.Sprintf("current user unavailable: %v", err)})
		return report
	}
	guilds, err := p.client.Guilds(ctx)
	if err != nil {
		report.Warnings = append(report.Warnings, Warning{Code: WarningGuildChannelsUnavailable, Detail: fmt.Sprintf("guild list unavailable: %v", err)})
		return report
	}

	for _, guild := range guilds {
		report = p.probeGuild(ctx, report, bot.ID, guild)
	}
	return report
}

func (p Probe) intentWarnings() []Warning {
	warnings := make([]Warning, 0, 2)
	if !hasIntent(p.config.Intents, discordgo.IntentMessageContent) {
		warnings = append(warnings, Warning{Code: WarningMissingIntent, Intent: "Message Content", Detail: "live ingestion and backfill will skip message text"})
	}
	if !hasIntent(p.config.Intents, discordgo.IntentGuildMembers) {
		warnings = append(warnings, Warning{Code: WarningMissingIntent, Intent: "Guild Members", Detail: "member and thread-member topology will be incomplete"})
	}
	return warnings
}

func (p Probe) probeGuild(ctx context.Context, report Report, botID string, guild *discordgo.UserGuild) Report {
	rolesAvailable := p.probeRoles(ctx, guild, &report)
	membersAvailable := p.probeMembers(ctx, guild, &report)
	report.RoleAccessAvailable = report.RoleAccessAvailable || rolesAvailable
	report.MemberAccessAvailable = report.MemberAccessAvailable || membersAvailable

	channels, err := p.client.GuildChannels(ctx, guild.ID)
	if err != nil {
		report.Warnings = append(report.Warnings, guildWarning(WarningGuildChannelsUnavailable, guild, fmt.Sprintf("channel list unavailable: %v", err)))
		return report
	}
	for _, channel := range channels {
		if channel.Type != discordgo.ChannelTypeGuildText && channel.Type != discordgo.ChannelTypeGuildPublicThread && channel.Type != discordgo.ChannelTypeGuildPrivateThread {
			continue
		}
		report = p.probeChannel(ctx, report, botID, guild, channel)
	}
	return report
}

func (p Probe) probeRoles(ctx context.Context, guild *discordgo.UserGuild, report *Report) bool {
	if err := p.client.GuildRoles(ctx, guild.ID); err != nil {
		report.Warnings = append(report.Warnings, guildWarning(WarningGuildRolesUnavailable, guild, fmt.Sprintf("role list unavailable: %v", err)))
		return false
	}
	return true
}

func (p Probe) probeMembers(ctx context.Context, guild *discordgo.UserGuild, report *Report) bool {
	if !hasIntent(p.config.Intents, discordgo.IntentGuildMembers) {
		return false
	}
	if err := p.client.GuildMembers(ctx, guild.ID); err != nil {
		report.Warnings = append(report.Warnings, guildWarning(WarningGuildMembersUnavailable, guild, fmt.Sprintf("member list unavailable: %v", err)))
		return false
	}
	return true
}

func (p Probe) probeChannel(ctx context.Context, report Report, botID string, guild *discordgo.UserGuild, channel *discordgo.Channel) Report {
	permissions, err := p.client.UserChannelPermissions(ctx, botID, channel.ID)
	if err != nil {
		report.Warnings = append(report.Warnings, channelWarning(WarningMissingChannelPermission, guild, channel, "View Channel", fmt.Sprintf("channel permissions unavailable: %v", err)))
		report.ChannelDecisions = append(report.ChannelDecisions, ChannelDecision{GuildID: guild.ID, GuildName: guild.Name, ChannelID: channel.ID, Channel: channel.Name})
		return report
	}
	decision := ChannelDecision{GuildID: guild.ID, GuildName: guild.Name, ChannelID: channel.ID, Channel: channel.Name}
	decision.Visible = hasPermission(permissions, discordgo.PermissionViewChannel)
	decision.ReadHistory = hasPermission(permissions, discordgo.PermissionReadMessageHistory)
	decision.AddReactions = hasPermission(permissions, discordgo.PermissionAddReactions)
	decision.ReadMessages = decision.Visible && report.MessageContentAvailable
	report.Warnings = appendMissingPermissionWarnings(report.Warnings, guild, channel, permissions)
	report.ChannelDecisions = append(report.ChannelDecisions, decision)
	return report
}

func appendMissingPermissionWarnings(warnings []Warning, guild *discordgo.UserGuild, channel *discordgo.Channel, permissions int64) []Warning {
	required := []struct {
		name string
		bit  int64
	}{
		{name: "View Channel", bit: discordgo.PermissionViewChannel},
		{name: "Read Message History", bit: discordgo.PermissionReadMessageHistory},
		{name: "Add Reactions", bit: discordgo.PermissionAddReactions},
	}
	for _, permission := range required {
		if !hasPermission(permissions, permission.bit) {
			warnings = append(warnings, channelWarning(WarningMissingChannelPermission, guild, channel, permission.name, "surface will be skipped"))
		}
	}
	return warnings
}

func guildWarning(code WarningCode, guild *discordgo.UserGuild, detail string) Warning {
	return Warning{Code: code, GuildID: guild.ID, GuildName: guild.Name, Detail: detail}
}

func channelWarning(code WarningCode, guild *discordgo.UserGuild, channel *discordgo.Channel, permission string, detail string) Warning {
	return Warning{Code: code, GuildID: guild.ID, GuildName: guild.Name, ChannelID: channel.ID, Channel: channel.Name, Permission: permission, Detail: detail}
}

func hasIntent(intents discordgo.Intent, intent discordgo.Intent) bool {
	return intents&intent == intent
}

func hasPermission(permissions int64, permission int64) bool {
	return permissions&permission == permission
}
