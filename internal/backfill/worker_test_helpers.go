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
	return &fakeDiscordClient{channels: map[string][]*discordgo.Channel{}, threads: map[string][]*discordgo.Channel{}, messages: map[string][][]*discordgo.Message{}, messageErrs: map[string]error{}}
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
	result := make([]*discordgo.Message, 0, count)
	for id := newest; id > newest-count; id-- {
		result = append(result, message("m-"+strconv.Itoa(id)))
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
