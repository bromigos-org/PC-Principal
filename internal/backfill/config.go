package backfill

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTenantID              = "bromigos"
	defaultAgentID               = "pc-principal"
	defaultMaxChannelsPerRun     = 25
	defaultMaxMessagesPerChannel = 500
	defaultMemoryBatchSize       = 50
	defaultRequestDelay          = 250 * time.Millisecond
	defaultBackoff               = time.Second
	defaultMaxAttempts           = 3
)

type Config struct {
	Enabled               bool
	TenantID              string
	AgentID               string
	MaxChannelsPerRun     int
	MaxMessagesPerChannel int
	MemoryBatchSize       int
	RequestDelay          time.Duration
	Backoff               time.Duration
	MaxAttempts           int
}

func DefaultConfig() Config {
	return Config{
		Enabled:               false,
		TenantID:              defaultTenantID,
		AgentID:               defaultAgentID,
		MaxChannelsPerRun:     defaultMaxChannelsPerRun,
		MaxMessagesPerChannel: defaultMaxMessagesPerChannel,
		MemoryBatchSize:       defaultMemoryBatchSize,
		RequestDelay:          defaultRequestDelay,
		Backoff:               defaultBackoff,
		MaxAttempts:           defaultMaxAttempts,
	}
}

func LoadConfigFromEnv() Config {
	config := DefaultConfig()
	config.Enabled = strings.EqualFold(os.Getenv("DISCORD_HISTORY_BACKFILL_ENABLED"), "true")
	config.TenantID = envString("GNOSIS_TENANT_ID", config.TenantID)
	config.AgentID = envString("DISCORD_HISTORY_BACKFILL_AGENT_ID", config.AgentID)
	config.MaxChannelsPerRun = envInt("DISCORD_HISTORY_BACKFILL_MAX_CHANNELS", config.MaxChannelsPerRun)
	config.MaxMessagesPerChannel = envInt("DISCORD_HISTORY_BACKFILL_MAX_MESSAGES_PER_CHANNEL", config.MaxMessagesPerChannel)
	config.MemoryBatchSize = envInt("DISCORD_HISTORY_BACKFILL_GNOSIS_BATCH_SIZE", config.MemoryBatchSize)
	config.RequestDelay = envDuration("DISCORD_HISTORY_BACKFILL_REQUEST_DELAY", config.RequestDelay)
	config.Backoff = envDuration("DISCORD_HISTORY_BACKFILL_BACKOFF", config.Backoff)
	config.MaxAttempts = envInt("DISCORD_HISTORY_BACKFILL_MAX_ATTEMPTS", config.MaxAttempts)
	return config.withDefaults()
}

func (c Config) withDefaults() Config {
	defaults := DefaultConfig()
	if c.TenantID == "" {
		c.TenantID = defaults.TenantID
	}
	if c.AgentID == "" {
		c.AgentID = defaults.AgentID
	}
	if c.MaxChannelsPerRun < 0 {
		c.MaxChannelsPerRun = defaults.MaxChannelsPerRun
	}
	if c.MaxMessagesPerChannel < 0 {
		c.MaxMessagesPerChannel = defaults.MaxMessagesPerChannel
	}
	if c.MemoryBatchSize <= 0 {
		c.MemoryBatchSize = defaults.MemoryBatchSize
	}
	if c.Backoff <= 0 {
		c.Backoff = defaults.Backoff
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = defaults.MaxAttempts
	}
	return c
}

func envString(name string, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}

func envInt(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}
