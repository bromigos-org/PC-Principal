package run

import (
	"fmt"
	"log"
	"time"

	"github.com/bromigos-org/pc-principal/internal/discordevent"
	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

const startupSnapshotPageLimit = 100

func ingestStartupSnapshot(s *discordgo.Session, event *discordgo.Ready) {
	if s == nil || event == nil {
		return
	}
	for _, readyGuild := range event.Guilds {
		guild, err := startupGuild(s, readyGuild)
		if err != nil {
			log.Printf("gnosis startup snapshot guild %s unavailable: %v", readyGuild.ID, err)
			continue
		}
		normalizer := discordevent.New(discordevent.Config{TenantID: liveMemoryTenantID, AgentID: pcPrincipalAgentID, SourceMarker: discordevent.SourceMarkerBackfill, ObservedAt: time.Now().UTC(), Snapshot: discordevent.Snapshot{Channels: startupChannelMap(guild)}})
		ingestGuildSnapshot(normalizer, guild)
	}
}

func startupGuild(s *discordgo.Session, readyGuild *discordgo.Guild) (*discordgo.Guild, error) {
	if readyGuild == nil {
		return nil, discordgo.ErrStateNotFound
	}
	channels, err := s.GuildChannels(readyGuild.ID)
	if err != nil {
		return nil, fmt.Errorf("list guild %s channels: %w", readyGuild.ID, err)
	}
	threads, err := s.GuildThreadsActive(readyGuild.ID)
	if err != nil {
		return nil, fmt.Errorf("list guild %s active threads: %w", readyGuild.ID, err)
	}
	roles, err := s.GuildRoles(readyGuild.ID)
	if err != nil {
		return nil, fmt.Errorf("list guild %s roles: %w", readyGuild.ID, err)
	}
	members, err := startupGuildMembers(s, readyGuild.ID)
	if err != nil {
		return nil, err
	}
	guild := *readyGuild
	guild.Channels = channels
	guild.Threads = threads.Threads
	guild.Roles = roles
	guild.Members = members
	return &guild, nil
}

func startupGuildMembers(s *discordgo.Session, guildID string) ([]*discordgo.Member, error) {
	var result []*discordgo.Member
	afterID := ""
	for {
		members, err := s.GuildMembers(guildID, afterID, startupSnapshotPageLimit)
		if err != nil {
			return nil, fmt.Errorf("list guild %s members: %w", guildID, err)
		}
		if len(members) == 0 {
			return result, nil
		}
		result = append(result, members...)
		afterID = members[len(members)-1].User.ID
		if len(members) < startupSnapshotPageLimit {
			return result, nil
		}
	}
}

func startupChannelMap(guild *discordgo.Guild) map[string]*discordgo.Channel {
	channels := make(map[string]*discordgo.Channel, len(guild.Channels)+len(guild.Threads))
	for _, channel := range guild.Channels {
		channels[channel.ID] = channel
	}
	for _, thread := range guild.Threads {
		channels[thread.ID] = thread
	}
	return channels
}

func ingestGuildSnapshot(normalizer discordevent.Normalizer, guild *discordgo.Guild) {
	if guild == nil {
		return
	}
	events := make([]memory.ClientEvent, 0, len(guild.Channels)+len(guild.Threads)+len(guild.Roles)+len(guild.Members))
	for _, channel := range guild.Channels {
		events = append(events, normalizer.NormalizeChannelCreate(channel)...)
	}
	for _, thread := range guild.Threads {
		events = append(events, normalizer.NormalizeThreadCreate(thread)...)
	}
	for _, role := range guild.Roles {
		events = append(events, normalizer.NormalizeRoleCreate(guild.ID, role)...)
	}
	for _, member := range guild.Members {
		events = append(events, normalizer.NormalizeMemberUpdate(member)...)
	}
	ingestLiveTopology("startup snapshot", events)
}
