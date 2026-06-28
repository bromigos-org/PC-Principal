package backfill

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

type fakeDiscordClient struct {
	guilds       []*discordgo.UserGuild
	channels     map[string][]*discordgo.Channel
	threads      map[string][]*discordgo.Channel
	roles        map[string][]*discordgo.Role
	members      map[string][][]*discordgo.Member
	messages     map[string][][]*discordgo.Message
	messageErrs  map[string]error
	messageCalls []messageCall
	sent         []string
}

type messageCall struct {
	channelID string
	limit     int
	beforeID  string
}

func newFakeDiscordClient() *fakeDiscordClient {
	return &fakeDiscordClient{channels: map[string][]*discordgo.Channel{}, threads: map[string][]*discordgo.Channel{}, roles: map[string][]*discordgo.Role{}, members: map[string][][]*discordgo.Member{}, messages: map[string][][]*discordgo.Message{}, messageErrs: map[string]error{}}
}

func (c *fakeDiscordClient) UserGuilds(ctx context.Context, limit int, beforeID string) ([]*discordgo.UserGuild, error) {
	return c.guilds, nil
}

func (c *fakeDiscordClient) GuildChannels(ctx context.Context, guildID string) ([]*discordgo.Channel, error) {
	return c.channels[guildID], nil
}

func (c *fakeDiscordClient) GuildThreadsActive(ctx context.Context, guildID string) ([]*discordgo.Channel, error) {
	return c.threads[guildID], nil
}

func (c *fakeDiscordClient) GuildRoles(ctx context.Context, guildID string) ([]*discordgo.Role, error) {
	return c.roles[guildID], nil
}

func (c *fakeDiscordClient) GuildMembers(ctx context.Context, guildID string, afterID string, limit int) ([]*discordgo.Member, error) {
	pages := c.members[guildID]
	if len(pages) == 0 {
		return nil, nil
	}
	c.members[guildID] = pages[1:]
	return pages[0], nil
}

func (c *fakeDiscordClient) ChannelMessages(ctx context.Context, channelID string, limit int, beforeID string) ([]*discordgo.Message, error) {
	c.messageCalls = append(c.messageCalls, messageCall{channelID: channelID, limit: limit, beforeID: beforeID})
	if err := c.messageErrs[channelID]; err != nil {
		return nil, err
	}
	pages := c.messages[channelID]
	if len(pages) == 0 {
		return nil, nil
	}
	c.messages[channelID] = pages[1:]
	return pages[0], nil
}

type fakeMemoryClient struct {
	err     error
	batches [][]memory.ClientEvent
}

func (c *fakeMemoryClient) IngestEvents(ctx context.Context, events []memory.ClientEvent) (memory.ClientEventBatchResponse, error) {
	c.batches = append(c.batches, append([]memory.ClientEvent(nil), events...))
	return memory.ClientEventBatchResponse{}, c.err
}

func (c *fakeMemoryClient) events() []memory.ClientEvent {
	var result []memory.ClientEvent
	for _, batch := range c.batches {
		result = append(result, batch...)
	}
	return result
}

type fakeCursorStore struct {
	values map[CursorKey]string
}

func newFakeCursorStore() *fakeCursorStore {
	return &fakeCursorStore{values: map[CursorKey]string{}}
}

func (s *fakeCursorStore) Get(ctx context.Context, key CursorKey) (string, error) {
	return s.values[key], nil
}

func (s *fakeCursorStore) Save(ctx context.Context, key CursorKey, beforeID string) error {
	s.values[key] = beforeID
	return nil
}

func message(id string) *discordgo.Message {
	return messageInChannel("channel-1", id)
}

func messageInChannel(channelID string, id string) *discordgo.Message {
	return &discordgo.Message{ID: id, ChannelID: channelID, GuildID: "guild-1", Content: "hello " + id, Timestamp: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC), Author: &discordgo.User{ID: "user-1", Username: "Alex"}}
}

func messages(count int, newest int) []*discordgo.Message {
	return messagesInChannel("channel-1", count, newest)
}

func messagesInChannel(channelID string, count int, newest int) []*discordgo.Message {
	result := make([]*discordgo.Message, 0, count)
	for id := newest; id > newest-count; id-- {
		result = append(result, messageInChannel(channelID, "m-"+strconv.Itoa(id)))
	}
	return result
}

func calledChannels(calls []messageCall) string {
	ids := make([]string, 0, len(calls))
	for _, call := range calls {
		ids = append(ids, call.channelID)
	}
	return strings.Join(ids, ",")
}
