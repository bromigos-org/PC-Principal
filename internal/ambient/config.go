package ambient

import (
	"os"
	"strings"
	"time"
)

const (
	defaultSessionTTL            = 20 * time.Minute
	defaultMaxUserTurns          = 6
	defaultMaxConsecutiveReplies = 2
	defaultChannelReplyCooldown  = 90 * time.Second
	defaultUserReplyCooldown     = 30 * time.Second
)

type Config struct {
	Enabled               bool
	SessionTTL            time.Duration
	MaxUserTurns          int
	MaxConsecutiveReplies int
	ChannelReplyCooldown  time.Duration
	UserReplyCooldown     time.Duration
}

func DefaultConfig() Config {
	return Config{
		Enabled:               false,
		SessionTTL:            defaultSessionTTL,
		MaxUserTurns:          defaultMaxUserTurns,
		MaxConsecutiveReplies: defaultMaxConsecutiveReplies,
		ChannelReplyCooldown:  defaultChannelReplyCooldown,
		UserReplyCooldown:     defaultUserReplyCooldown,
	}
}

func LoadConfigFromEnv() Config {
	config := DefaultConfig()
	config.Enabled = strings.EqualFold(os.Getenv("DISCORD_AMBIENT_REPLIES_ENABLED"), "true")
	return config
}

func (c Config) withDefaults() Config {
	defaults := DefaultConfig()
	if c.SessionTTL <= 0 {
		c.SessionTTL = defaults.SessionTTL
	}
	if c.MaxUserTurns <= 0 {
		c.MaxUserTurns = defaults.MaxUserTurns
	}
	if c.MaxConsecutiveReplies <= 0 {
		c.MaxConsecutiveReplies = defaults.MaxConsecutiveReplies
	}
	if c.ChannelReplyCooldown <= 0 {
		c.ChannelReplyCooldown = defaults.ChannelReplyCooldown
	}
	if c.UserReplyCooldown <= 0 {
		c.UserReplyCooldown = defaults.UserReplyCooldown
	}
	return c
}
