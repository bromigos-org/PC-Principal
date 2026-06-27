package run

import (
	"context"
	"log"

	"github.com/bromigos-org/pc-principal/internal/backfill"
	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bromigos-org/pc-principal/internal/store"
	"github.com/bwmarrin/discordgo"
)

func runHistoryBackfill(session *discordgo.Session, memoryClient memory.Client) {
	config := backfill.LoadConfigFromEnv()
	if !config.Enabled {
		return
	}
	redisClient := store.Client()
	if redisClient == nil {
		log.Println("Discord history backfill enabled but DRAGONFLY_ADDR is not configured; skipping")
		return
	}
	worker := backfill.NewWorker(backfill.WorkerDeps{Discord: backfill.SessionClient{Session: session}, Memory: memoryClient, Cursors: backfill.NewRedisCursorStore(redisClient)}, config)
	summary, err := worker.Run(context.Background())
	if err != nil {
		log.Printf("Discord history backfill failed: %v", err)
		return
	}
	log.Printf("Discord history backfill complete: channels=%d skipped=%d messages=%d", summary.ChannelsVisited, summary.ChannelsSkipped, summary.MessagesIngested)
}
