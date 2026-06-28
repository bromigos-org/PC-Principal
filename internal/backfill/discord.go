package backfill

import (
	"context"

	"github.com/bwmarrin/discordgo"
)

type DiscordClient interface {
	UserGuilds(ctx context.Context, limit int, beforeID string) ([]*discordgo.UserGuild, error)
	GuildChannels(ctx context.Context, guildID string) ([]*discordgo.Channel, error)
	GuildThreadsActive(ctx context.Context, guildID string) ([]*discordgo.Channel, error)
	GuildRoles(ctx context.Context, guildID string) ([]*discordgo.Role, error)
	GuildMembers(ctx context.Context, guildID string, afterID string, limit int) ([]*discordgo.Member, error)
	ChannelMessages(ctx context.Context, channelID string, limit int, beforeID string) ([]*discordgo.Message, error)
}

type SessionClient struct {
	Session *discordgo.Session
}

func (c SessionClient) UserGuilds(ctx context.Context, limit int, beforeID string) ([]*discordgo.UserGuild, error) {
	return c.Session.UserGuilds(limit, beforeID, "", false, discordgo.WithContext(ctx))
}

func (c SessionClient) GuildChannels(ctx context.Context, guildID string) ([]*discordgo.Channel, error) {
	return c.Session.GuildChannels(guildID, discordgo.WithContext(ctx))
}

func (c SessionClient) GuildThreadsActive(ctx context.Context, guildID string) ([]*discordgo.Channel, error) {
	threads, err := c.Session.GuildThreadsActive(guildID, discordgo.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	return threads.Threads, nil
}

func (c SessionClient) GuildRoles(ctx context.Context, guildID string) ([]*discordgo.Role, error) {
	return c.Session.GuildRoles(guildID, discordgo.WithContext(ctx))
}

func (c SessionClient) GuildMembers(ctx context.Context, guildID string, afterID string, limit int) ([]*discordgo.Member, error) {
	return c.Session.GuildMembers(guildID, afterID, limit, discordgo.WithContext(ctx))
}

func (c SessionClient) ChannelMessages(ctx context.Context, channelID string, limit int, beforeID string) ([]*discordgo.Message, error) {
	return c.Session.ChannelMessages(channelID, limit, beforeID, "", "", discordgo.WithContext(ctx))
}
