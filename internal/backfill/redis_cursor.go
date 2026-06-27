package backfill

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type RedisCursorStore struct {
	client *redis.Client
}

func NewRedisCursorStore(client *redis.Client) RedisCursorStore {
	return RedisCursorStore{client: client}
}

func (s RedisCursorStore) Get(ctx context.Context, key CursorKey) (string, error) {
	value, err := s.client.Get(ctx, cursorRedisKey(key)).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get redis cursor: %w", err)
	}
	return value, nil
}

func (s RedisCursorStore) Save(ctx context.Context, key CursorKey, beforeID string) error {
	if err := s.client.Set(ctx, cursorRedisKey(key), beforeID, 0).Err(); err != nil {
		return fmt.Errorf("set redis cursor: %w", err)
	}
	return nil
}

func cursorRedisKey(key CursorKey) string {
	return "backfill:discord:history:guild:" + key.GuildID + ":channel:" + key.ChannelID
}
