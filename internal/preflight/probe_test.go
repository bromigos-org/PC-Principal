package preflight

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestProbe_ReturnsWarningsAndSkips_whenChannelPermissionsMissing(t *testing.T) {
	// Given
	client := &fakeDiscordProbeClient{
		currentUser: &discordgo.User{ID: "bot-1"},
		guilds:      []*discordgo.UserGuild{{ID: "guild-1", Name: "Bromigos"}},
		channels: map[string][]*discordgo.Channel{
			"guild-1": {{ID: "channel-1", GuildID: "guild-1", Name: "general", Type: discordgo.ChannelTypeGuildText}},
		},
		permissions: map[string]int64{
			"channel-1": discordgo.PermissionViewChannel | discordgo.PermissionAddReactions,
		},
	}
	probe := NewProbe(client, Config{Intents: RequiredIntents()})

	// When
	report := probe.Run(context.Background())

	// Then
	if len(report.Warnings) != 1 {
		t.Fatalf("expected one warning, got %#v", report.Warnings)
	}
	warning := report.Warnings[0]
	if warning.Code != WarningMissingChannelPermission || warning.Permission != "Read Message History" {
		t.Fatalf("expected missing history warning, got %#v", warning)
	}
	if report.ChannelDecisions[0].ReadHistory {
		t.Fatalf("expected history backfill to be skipped, got %#v", report.ChannelDecisions[0])
	}
	if !report.ChannelDecisions[0].Visible || !report.ChannelDecisions[0].ReadMessages || !report.ChannelDecisions[0].AddReactions {
		t.Fatalf("expected remaining channel surfaces allowed, got %#v", report.ChannelDecisions[0])
	}
}

func TestProbe_ReturnsWarningsAndSkips_whenPrivilegedIntentsMissing(t *testing.T) {
	// Given
	client := &fakeDiscordProbeClient{
		currentUser: &discordgo.User{ID: "bot-1"},
		guilds:      []*discordgo.UserGuild{{ID: "guild-1", Name: "Bromigos"}},
		channels: map[string][]*discordgo.Channel{
			"guild-1": {{ID: "channel-1", GuildID: "guild-1", Name: "general", Type: discordgo.ChannelTypeGuildText}},
		},
		permissions: map[string]int64{
			"channel-1": discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory | discordgo.PermissionAddReactions,
		},
	}
	probe := NewProbe(client, Config{Intents: discordgo.IntentGuilds | discordgo.IntentGuildMessages | discordgo.IntentGuildMessageReactions})

	// When
	report := probe.Run(context.Background())

	// Then
	if len(report.Warnings) != 2 {
		t.Fatalf("expected message content and member intent warnings, got %#v", report.Warnings)
	}
	if report.MessageContentAvailable {
		t.Fatalf("expected message content unavailable")
	}
	if report.MemberAccessAvailable {
		t.Fatalf("expected member access unavailable")
	}
	if report.ChannelDecisions[0].ReadMessages {
		t.Fatalf("expected message ingestion to be skipped without message content, got %#v", report.ChannelDecisions[0])
	}
}

func TestProbe_Continues_whenOneGuildCannotBeRead(t *testing.T) {
	// Given
	client := &fakeDiscordProbeClient{
		currentUser: &discordgo.User{ID: "bot-1"},
		guilds: []*discordgo.UserGuild{
			{ID: "guild-1", Name: "Bromigos"},
			{ID: "guild-2", Name: "Locked"},
		},
		channels: map[string][]*discordgo.Channel{
			"guild-1": {{ID: "channel-1", GuildID: "guild-1", Name: "general", Type: discordgo.ChannelTypeGuildText}},
		},
		channelsErr: map[string]error{"guild-2": errors.New("forbidden")},
		permissions: map[string]int64{
			"channel-1": discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory | discordgo.PermissionAddReactions,
		},
	}
	probe := NewProbe(client, Config{Intents: RequiredIntents()})

	// When
	report := probe.Run(context.Background())

	// Then
	if len(report.ChannelDecisions) != 1 {
		t.Fatalf("expected readable guild decision to remain, got %#v", report.ChannelDecisions)
	}
	if len(report.Warnings) != 1 || report.Warnings[0].Code != WarningGuildChannelsUnavailable {
		t.Fatalf("expected guild channel warning, got %#v", report.Warnings)
	}
}

type fakeDiscordProbeClient struct {
	currentUser *discordgo.User
	guilds      []*discordgo.UserGuild
	channels    map[string][]*discordgo.Channel
	permissions map[string]int64
	rolesErr    map[string]error
	membersErr  map[string]error
	channelsErr map[string]error
}

func (c *fakeDiscordProbeClient) CurrentUser(ctx context.Context) (*discordgo.User, error) {
	return c.currentUser, nil
}

func (c *fakeDiscordProbeClient) Guilds(ctx context.Context) ([]*discordgo.UserGuild, error) {
	return c.guilds, nil
}

func (c *fakeDiscordProbeClient) GuildChannels(ctx context.Context, guildID string) ([]*discordgo.Channel, error) {
	if err, ok := c.channelsErr[guildID]; ok {
		return nil, err
	}
	return c.channels[guildID], nil
}

func (c *fakeDiscordProbeClient) UserChannelPermissions(ctx context.Context, userID string, channelID string) (int64, error) {
	return c.permissions[channelID], nil
}

func (c *fakeDiscordProbeClient) GuildRoles(ctx context.Context, guildID string) error {
	if err, ok := c.rolesErr[guildID]; ok {
		return err
	}
	return nil
}

func (c *fakeDiscordProbeClient) GuildMembers(ctx context.Context, guildID string) error {
	if err, ok := c.membersErr[guildID]; ok {
		return err
	}
	return nil
}
