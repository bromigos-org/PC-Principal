package run

import (
	"log"

	"github.com/bromigos-org/pc-principal/internal/discordevent"
	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

const (
	snapshotMemberLimit = 100
	snapshotEventLimit  = 500
)

func ingestStartupSnapshot(s *discordgo.Session, event *discordgo.Ready) {
	if s == nil || event == nil {
		return
	}
	normalizer := liveNormalizer(s, discordevent.SourceMarkerBackfill)
	remaining := snapshotEventLimit
	for _, readyGuild := range event.Guilds {
		guild, err := s.State.Guild(readyGuild.ID)
		if err != nil {
			log.Printf("agents-memory startup snapshot guild %s unavailable: %v", readyGuild.ID, err)
			continue
		}
		remaining = ingestGuildSnapshot(normalizer, guild, remaining)
		if remaining == 0 {
			return
		}
	}
}

func ingestGuildSnapshot(normalizer discordevent.Normalizer, guild *discordgo.Guild, remaining int) int {
	if guild == nil || remaining == 0 {
		return remaining
	}
	events := make([]memory.ClientEvent, 0, min(snapshotEventLimit, len(guild.Channels)+len(guild.Threads)+len(guild.Roles)+min(snapshotMemberLimit, len(guild.Members))))
	for _, channel := range guild.Channels {
		events = appendCapped(events, normalizer.NormalizeChannelCreate(channel), &remaining)
	}
	for _, thread := range guild.Threads {
		events = appendCapped(events, normalizer.NormalizeThreadCreate(thread), &remaining)
	}
	for _, role := range guild.Roles {
		events = appendCapped(events, normalizer.NormalizeRoleCreate(guild.ID, role), &remaining)
	}
	memberCount := 0
	for _, member := range guild.Members {
		if memberCount == snapshotMemberLimit {
			break
		}
		events = appendCapped(events, normalizer.NormalizeMemberUpdate(member), &remaining)
		memberCount++
	}
	ingestLiveTopology("startup snapshot", events)
	return remaining
}

func appendCapped(events []memory.ClientEvent, next []memory.ClientEvent, remaining *int) []memory.ClientEvent {
	if *remaining == 0 {
		return events
	}
	for _, event := range next {
		if *remaining == 0 {
			return events
		}
		events = append(events, event)
		*remaining--
	}
	return events
}
