package backfill

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/bromigos-org/pc-principal/internal/discordevent"
	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

const discordPageLimit = 100

type MemoryClient interface {
	IngestEvents(ctx context.Context, events []memory.ClientEvent) (memory.ClientEventBatchResponse, error)
}

type WorkerDeps struct {
	Discord DiscordClient
	Memory  MemoryClient
	Cursors CursorStore
}

type Worker struct {
	deps   WorkerDeps
	config Config
}

type Summary struct {
	ChannelsVisited  int
	ChannelsSkipped  int
	MessagesIngested int
}

func NewWorker(deps WorkerDeps, config Config) Worker {
	return Worker{deps: deps, config: config.withDefaults()}
}

func (w Worker) Run(ctx context.Context) (Summary, error) {
	var summary Summary
	if !w.config.Enabled {
		return summary, nil
	}
	guilds, err := w.deps.Discord.UserGuilds(ctx, w.config.MaxChannelsPerRun, "")
	if err != nil {
		return summary, fmt.Errorf("list discord guilds: %w", err)
	}
	for _, guild := range guilds {
		channels, err := w.visibleChannels(ctx, guild.ID)
		if err != nil {
			return summary, err
		}
		for _, channel := range channels {
			if summary.ChannelsVisited >= w.config.MaxChannelsPerRun {
				return summary, nil
			}
			visited, skipped, ingested, err := w.backfillChannel(ctx, channel)
			summary.ChannelsVisited += visited
			summary.ChannelsSkipped += skipped
			summary.MessagesIngested += ingested
			if err != nil {
				return summary, err
			}
		}
	}
	return summary, nil
}

func (w Worker) visibleChannels(ctx context.Context, guildID string) ([]*discordgo.Channel, error) {
	channels, err := w.deps.Discord.GuildChannels(ctx, guildID)
	if err != nil {
		return nil, fmt.Errorf("list guild %s channels: %w", guildID, err)
	}
	threads, err := w.deps.Discord.GuildThreadsActive(ctx, guildID)
	if err != nil {
		return nil, fmt.Errorf("list guild %s active threads: %w", guildID, err)
	}
	return textHistoryChannels(append(channels, threads...)), nil
}

func (w Worker) backfillChannel(ctx context.Context, channel *discordgo.Channel) (int, int, int, error) {
	key := CursorKey{GuildID: channel.GuildID, ChannelID: channel.ID}
	beforeID, err := w.deps.Cursors.Get(ctx, key)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("load backfill cursor guild %s channel %s: %w", channel.GuildID, channel.ID, err)
	}
	fetched := 0
	ingested := 0
	for fetched < w.config.MaxMessagesPerChannel {
		limit := pageLimit(w.config.MaxMessagesPerChannel - fetched)
		messages, err := w.fetchMessages(ctx, channel.ID, limit, beforeID)
		if isPermissionDenied(err) {
			return 0, 1, ingested, nil
		}
		if err != nil {
			return 1, 0, ingested, fmt.Errorf("fetch channel %s messages: %w", channel.ID, err)
		}
		if len(messages) == 0 {
			return 1, 0, ingested, nil
		}
		oldestID := messages[len(messages)-1].ID
		if err := w.ingestMessages(ctx, messages); err != nil {
			return 1, 0, ingested, fmt.Errorf("ingest channel %s messages: %w", channel.ID, err)
		}
		if err := w.deps.Cursors.Save(ctx, key, oldestID); err != nil {
			return 1, 0, ingested, fmt.Errorf("save backfill cursor guild %s channel %s: %w", channel.GuildID, channel.ID, err)
		}
		fetched += len(messages)
		ingested += len(messages)
		beforeID = oldestID
		if len(messages) < limit {
			return 1, 0, ingested, nil
		}
		if err := w.wait(ctx, w.config.RequestDelay); err != nil {
			return 1, 0, ingested, err
		}
	}
	return 1, 0, ingested, nil
}

func (w Worker) fetchMessages(ctx context.Context, channelID string, limit int, beforeID string) ([]*discordgo.Message, error) {
	var messages []*discordgo.Message
	err := w.withRetry(ctx, func() error {
		var err error
		messages, err = w.deps.Discord.ChannelMessages(ctx, channelID, limit, beforeID)
		return err
	})
	return messages, err
}

func (w Worker) ingestMessages(ctx context.Context, messages []*discordgo.Message) error {
	normalizer := discordevent.New(discordevent.Config{TenantID: w.config.TenantID, AgentID: w.config.AgentID, SourceMarker: discordevent.SourceMarkerBackfill, ObservedAt: time.Now().UTC()})
	events := make([]memory.ClientEvent, 0, len(messages))
	for _, message := range messages {
		events = append(events, normalizer.NormalizeMessageCreate(message)...)
	}
	for start := 0; start < len(events); start += w.config.MemoryBatchSize {
		end := min(start+w.config.MemoryBatchSize, len(events))
		batch := events[start:end]
		if err := w.withRetry(ctx, func() error {
			_, err := w.deps.Memory.IngestEvents(ctx, batch)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w Worker) withRetry(ctx context.Context, operation func() error) error {
	var err error
	for attempt := 1; attempt <= w.config.MaxAttempts; attempt++ {
		err = operation()
		if err == nil || !isTransient(err) || attempt == w.config.MaxAttempts {
			return err
		}
		if waitErr := w.wait(ctx, w.config.Backoff*time.Duration(attempt)); waitErr != nil {
			return waitErr
		}
	}
	return err
}

func (w Worker) wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
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

func pageLimit(remaining int) int {
	if remaining < discordPageLimit {
		return remaining
	}
	return discordPageLimit
}

func isPermissionDenied(err error) bool {
	var restErr *discordgo.RESTError
	return errors.As(err, &restErr) && restErr.Response != nil && (restErr.Response.StatusCode == http.StatusForbidden || restErr.Response.StatusCode == http.StatusNotFound)
}

func isTransient(err error) bool {
	var memoryErr *memory.Error
	if errors.As(err, &memoryErr) {
		return memoryErr.Kind == memory.ErrorKindTimeout || memoryErr.Kind == memory.ErrorKindServer || memoryErr.Kind == memory.ErrorKindUnexpected
	}
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Response != nil {
		return restErr.Response.StatusCode == http.StatusTooManyRequests || restErr.Response.StatusCode >= http.StatusInternalServerError
	}
	return true
}
