package backfill

import "context"

type CursorKey struct {
	GuildID   string
	ChannelID string
}

type CursorStore interface {
	Get(ctx context.Context, key CursorKey) (string, error)
	Save(ctx context.Context, key CursorKey, beforeID string) error
}
