package run

import (
	"log"

	"github.com/bromigos-org/pc-principal/internal/discordevent"
	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

func ingestStartupSnapshot(s *discordgo.Session, event *discordgo.Ready) {
	if s == nil || event == nil {
		return
	}
	normalizer := liveNormalizer(s, discordevent.SourceMarkerBackfill)
	for _, readyGuild := range event.Guilds {
		guild, err := startupGuild(s, readyGuild)
		if err != nil {
			log.Printf("agents-memory startup snapshot guild %s unavailable: %v", readyGuild.ID, err)
			continue
		}
		ingestGuildSnapshot(normalizer, guild)
	}
}

func startupGuild(s *discordgo.Session, readyGuild *discordgo.Guild) (*discordgo.Guild, error) {
	if readyGuild == nil {
		return nil, discordgo.ErrStateNotFound
	}
	if len(readyGuild.Channels) > 0 || len(readyGuild.Threads) > 0 || len(readyGuild.Roles) > 0 || len(readyGuild.Members) > 0 {
		return readyGuild, nil
	}
	return s.State.Guild(readyGuild.ID)
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
