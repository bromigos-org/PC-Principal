package backfill

import (
	"context"
	"fmt"
	"time"

	"github.com/bromigos-org/pc-principal/internal/discordevent"
	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

func (w Worker) guildTopology(ctx context.Context, guildID string) ([]*discordgo.Channel, []*discordgo.Channel, error) {
	channels, err := w.deps.Discord.GuildChannels(ctx, guildID)
	if err != nil {
		return nil, nil, fmt.Errorf("list guild %s channels: %w", guildID, err)
	}
	threads, err := w.deps.Discord.GuildThreadsActive(ctx, guildID)
	if err != nil {
		return nil, nil, fmt.Errorf("list guild %s active threads: %w", guildID, err)
	}
	return channels, threads, nil
}

func (w Worker) ingestGuildTopology(ctx context.Context, guildID string, channels []*discordgo.Channel, threads []*discordgo.Channel) error {
	normalizer := discordevent.New(discordevent.Config{TenantID: w.config.TenantID, AgentID: w.config.AgentID, SourceMarker: discordevent.SourceMarkerBackfill, ObservedAt: time.Now().UTC(), Snapshot: discordevent.Snapshot{Channels: channelMap(channels, threads)}})
	events := make([]memory.ClientEvent, 0, len(channels)+len(threads))
	for _, channel := range channels {
		events = append(events, normalizer.NormalizeChannelCreate(channel)...)
	}
	for _, thread := range threads {
		events = append(events, normalizer.NormalizeThreadCreate(thread)...)
	}
	roles, err := w.deps.Discord.GuildRoles(ctx, guildID)
	if err != nil {
		return fmt.Errorf("list guild %s roles: %w", guildID, err)
	}
	for _, role := range roles {
		events = append(events, normalizer.NormalizeRoleCreate(guildID, role)...)
	}
	members, err := w.guildMembers(ctx, guildID)
	if err != nil {
		return err
	}
	for _, member := range members {
		events = append(events, normalizer.NormalizeMemberUpdate(member)...)
	}
	return w.ingestEvents(ctx, events)
}

func (w Worker) guildMembers(ctx context.Context, guildID string) ([]*discordgo.Member, error) {
	var result []*discordgo.Member
	afterID := ""
	for {
		members, err := w.deps.Discord.GuildMembers(ctx, guildID, afterID, discordPageLimit)
		if err != nil {
			return nil, fmt.Errorf("list guild %s members: %w", guildID, err)
		}
		if len(members) == 0 {
			return result, nil
		}
		result = append(result, members...)
		afterID = members[len(members)-1].User.ID
		if len(members) < discordPageLimit {
			return result, nil
		}
	}
}

func textHistoryChannels(channels []*discordgo.Channel) []*discordgo.Channel {
	result := make([]*discordgo.Channel, 0, len(channels))
	for _, channel := range channels {
		if supportsHistory(channel) {
			result = append(result, channel)
		}
	}
	return result
}

func supportsHistory(channel *discordgo.Channel) bool {
	if channel == nil {
		return false
	}
	switch channel.Type {
	case discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews, discordgo.ChannelTypeGuildPublicThread, discordgo.ChannelTypeGuildPrivateThread, discordgo.ChannelTypeGuildNewsThread:
		return true
	default:
		return false
	}
}

func channelMap(channels []*discordgo.Channel, threads []*discordgo.Channel) map[string]*discordgo.Channel {
	result := make(map[string]*discordgo.Channel, len(channels)+len(threads))
	for _, channel := range channels {
		result[channel.ID] = channel
	}
	for _, thread := range threads {
		result[thread.ID] = thread
	}
	return result
}
