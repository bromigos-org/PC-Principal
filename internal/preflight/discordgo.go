package preflight

import (
	"context"

	"github.com/bwmarrin/discordgo"
)

type DiscordGoClient struct {
	session *discordgo.Session
}

func NewDiscordGoClient(session *discordgo.Session) DiscordGoClient {
	return DiscordGoClient{session: session}
}

func (c DiscordGoClient) CurrentUser(ctx context.Context) (*discordgo.User, error) {
	return c.session.User("@me", discordgo.WithContext(ctx))
}

func (c DiscordGoClient) Guilds(ctx context.Context) ([]*discordgo.UserGuild, error) {
	return c.session.UserGuilds(200, "", "", false, discordgo.WithContext(ctx))
}

func (c DiscordGoClient) GuildChannels(ctx context.Context, guildID string) ([]*discordgo.Channel, error) {
	return c.session.GuildChannels(guildID, discordgo.WithContext(ctx))
}

func (c DiscordGoClient) UserChannelPermissions(ctx context.Context, userID string, channelID string) (int64, error) {
	return c.session.UserChannelPermissions(userID, channelID, discordgo.WithContext(ctx))
}

func (c DiscordGoClient) GuildRoles(ctx context.Context, guildID string) error {
	_, err := c.session.GuildRoles(guildID, discordgo.WithContext(ctx))
	return err
}

func (c DiscordGoClient) GuildMembers(ctx context.Context, guildID string) error {
	_, err := c.session.GuildMembers(guildID, "", 1, discordgo.WithContext(ctx))
	return err
}
