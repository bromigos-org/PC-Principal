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
	guilds, err := w.deps.Discord.UserGuilds(ctx, guildPageLimit(w.config.MaxChannelsPerRun), "")
	if err != nil {
		return summary, fmt.Errorf("list discord guilds: %w", err)
	}
	for _, guild := range guilds {
		channels, threads, err := w.guildTopology(ctx, guild.ID)
		if err != nil {
			return summary, err
		}
		if err := w.ingestGuildTopology(ctx, guild.ID, channels, threads); err != nil {
			return summary, err
		}
		guildChannels := channelsWithGuildID(guild.ID, channels)
		guildThreads := channelsWithGuildID(guild.ID, threads)
		for _, channel := range textHistoryChannels(append(guildChannels, guildThreads...)) {
			if w.reachedChannelLimit(summary.ChannelsVisited) {
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

func (w Worker) backfillChannel(ctx context.Context, channel *discordgo.Channel) (int, int, int, error) {
	key := CursorKey{GuildID: channel.GuildID, ChannelID: channel.ID}
	beforeID, err := w.deps.Cursors.Get(ctx, key)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("load backfill cursor guild %s channel %s: %w", channel.GuildID, channel.ID, err)
	}
	fetched := 0
	ingested := 0
	for !w.reachedMessageLimit(fetched) {
		limit := pageLimit(w.remainingMessageLimit(fetched))
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
		if err := w.ingestMessages(ctx, channel.GuildID, messages); err != nil {
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

func (w Worker) ingestMessages(ctx context.Context, guildID string, messages []*discordgo.Message) error {
	normalizer := discordevent.New(discordevent.Config{TenantID: w.config.TenantID, AgentID: w.config.AgentID, SourceMarker: discordevent.SourceMarkerBackfill, ObservedAt: time.Now().UTC()})
	events := make([]memory.ClientEvent, 0, len(messages))
	for _, message := range messages {
		messageCopy := *message
		messageCopy.GuildID = guildID
		events = append(events, normalizer.NormalizeMessageCreate(&messageCopy)...)
	}
	return w.ingestEvents(ctx, events)
}

func (w Worker) ingestEvents(ctx context.Context, events []memory.ClientEvent) error {
	if len(events) == 0 {
		return nil
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

func (w Worker) reachedChannelLimit(visited int) bool {
	return w.config.MaxChannelsPerRun > 0 && visited >= w.config.MaxChannelsPerRun
}

func (w Worker) reachedMessageLimit(fetched int) bool {
	return w.config.MaxMessagesPerChannel > 0 && fetched >= w.config.MaxMessagesPerChannel
}

func (w Worker) remainingMessageLimit(fetched int) int {
	if w.config.MaxMessagesPerChannel == 0 {
		return discordPageLimit
	}
	return w.config.MaxMessagesPerChannel - fetched
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

func pageLimit(remaining int) int {
	if remaining <= 0 {
		return discordPageLimit
	}
	if remaining < discordPageLimit {
		return remaining
	}
	return discordPageLimit
}

func guildPageLimit(maxChannels int) int {
	if maxChannels > 0 && maxChannels < discordPageLimit {
		return maxChannels
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
