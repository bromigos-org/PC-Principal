package ambient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

func (s *RedisStore) Load(ctx context.Context, key string) (State, bool, error) {
	value, err := s.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var state State
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		return State{}, false, fmt.Errorf("decode ambient state: %w", err)
	}
	return state, true, nil
}

func (s *RedisStore) Save(ctx context.Context, key string, state State, ttl time.Duration) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode ambient state: %w", err)
	}
	return s.client.Set(ctx, key, payload, ttl).Err()
}

func (s *RedisStore) Delete(ctx context.Context, key string) error {
	return s.client.Del(ctx, key).Err()
}
