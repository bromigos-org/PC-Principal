package ambient

import (
	"context"
	"strings"
	"time"
)

type Clock interface {
	Now() time.Time
}

type Store interface {
	Load(ctx context.Context, key string) (State, bool, error)
	Save(ctx context.Context, key string, state State, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

type State struct {
	ChannelID             string    `json:"channel_id"`
	GuildID               string    `json:"guild_id"`
	ActiveUserID          string    `json:"active_user_id"`
	UserTurns             int       `json:"user_turns"`
	ConsecutiveBotReplies int       `json:"consecutive_bot_replies"`
	LastChannelReplyAt    time.Time `json:"last_channel_reply_at"`
	LastUserReplyAt       time.Time `json:"last_user_reply_at"`
	ExpiresAt             time.Time `json:"expires_at"`
}

type Message struct {
	ChannelID     string
	GuildID       string
	UserID        string
	Content       string
	BotMentioned  bool
	ReferencesBot bool
}

type Decision struct {
	Reply bool
	Stop  bool
}

type Manager struct {
	config Config
	store  Store
	clock  Clock
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

func NewManager(config Config, store Store, clock Clock) *Manager {
	if clock == nil {
		clock = realClock{}
	}
	return &Manager{config: config.withDefaults(), store: store, clock: clock}
}

func (m *Manager) Enabled() bool {
	return m != nil && m.config.Enabled && m.store != nil
}

func Key(channelID string) string {
	return "ambient:discord:channel:" + channelID
}

func (m *Manager) Activate(ctx context.Context, channelID string, guildID string, userID string) error {
	if !m.Enabled() {
		return nil
	}
	now := m.clock.Now()
	state := State{ChannelID: channelID, GuildID: guildID, ActiveUserID: userID, ExpiresAt: now.Add(m.config.SessionTTL)}
	return m.store.Save(ctx, Key(channelID), state, m.config.SessionTTL)
}

func (m *Manager) Decide(ctx context.Context, message Message) (Decision, error) {
	if !m.Enabled() {
		return Decision{}, nil
	}
	key := Key(message.ChannelID)
	state, ok, err := m.store.Load(ctx, key)
	if err != nil || !ok {
		return Decision{}, err
	}
	now := m.clock.Now()
	if !now.Before(state.ExpiresAt) {
		return Decision{}, m.store.Delete(ctx, key)
	}
	if isStop(message.Content, message.BotMentioned) {
		return Decision{Stop: true}, m.store.Delete(ctx, key)
	}
	if message.UserID != state.ActiveUserID && !message.ReferencesBot {
		return Decision{}, nil
	}
	if state.UserTurns >= m.config.MaxUserTurns || state.ConsecutiveBotReplies >= m.config.MaxConsecutiveReplies {
		return Decision{}, m.store.Delete(ctx, key)
	}
	if now.Sub(state.LastChannelReplyAt) < m.config.ChannelReplyCooldown || now.Sub(state.LastUserReplyAt) < m.config.UserReplyCooldown {
		return Decision{}, nil
	}
	return Decision{Reply: true}, nil
}

func (m *Manager) RecordReply(ctx context.Context, channelID string) error {
	if !m.Enabled() {
		return nil
	}
	key := Key(channelID)
	state, ok, err := m.store.Load(ctx, key)
	if err != nil || !ok {
		return err
	}
	now := m.clock.Now()
	state.UserTurns++
	state.ConsecutiveBotReplies++
	state.LastChannelReplyAt = now
	state.LastUserReplyAt = now
	state.ExpiresAt = now.Add(m.config.SessionTTL)
	return m.store.Save(ctx, key, state, m.config.SessionTTL)
}

func isStop(content string, botMentioned bool) bool {
	text := strings.ToLower(strings.TrimSpace(content))
	text = strings.ReplaceAll(text, ",", "")
	text = strings.ReplaceAll(text, ".", "")
	if text == "stop" || text == "nevermind" || text == "shut up pc principal" {
		return true
	}
	return botMentioned && strings.Contains(text, "stop")
}
