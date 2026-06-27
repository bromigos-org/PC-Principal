package preflight

import "github.com/bwmarrin/discordgo"

type WarningCode string

const (
	WarningMissingIntent            WarningCode = "missing_intent"
	WarningMissingChannelPermission WarningCode = "missing_channel_permission"
	WarningGuildChannelsUnavailable WarningCode = "guild_channels_unavailable"
	WarningGuildRolesUnavailable    WarningCode = "guild_roles_unavailable"
	WarningGuildMembersUnavailable  WarningCode = "guild_members_unavailable"
)

type Warning struct {
	Code       WarningCode
	GuildID    string
	GuildName  string
	ChannelID  string
	Channel    string
	Intent     string
	Permission string
	Detail     string
}

type ChannelDecision struct {
	GuildID      string
	GuildName    string
	ChannelID    string
	Channel      string
	Visible      bool
	ReadHistory  bool
	ReadMessages bool
	AddReactions bool
}

type Report struct {
	Warnings                []Warning
	ChannelDecisions        []ChannelDecision
	RoleAccessAvailable     bool
	MemberAccessAvailable   bool
	MessageContentAvailable bool
}

type Config struct {
	Intents discordgo.Intent
}

func RequiredIntents() discordgo.Intent {
	return discordgo.IntentGuilds |
		discordgo.IntentGuildMessages |
		discordgo.IntentGuildMessageReactions |
		discordgo.IntentMessageContent |
		discordgo.IntentGuildMembers
}
