package run

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bromigos-org/pc-principal/internal/discordevent"
)

func configureLiveAttachmentCopy(ctx context.Context) {
	config := liveAttachmentCopyConfigFromEnv()
	if !config.Enabled {
		liveAttachmentCopyConfig = config
		return
	}
	endpoint := strings.TrimSpace(os.Getenv("DISCORD_ATTACHMENT_S3_ENDPOINT"))
	accessKeyID := envFirst("DISCORD_ATTACHMENT_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")
	secretAccessKey := envFirst("DISCORD_ATTACHMENT_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")
	if endpoint == "" || accessKeyID == "" || secretAccessKey == "" {
		log.Println("Discord attachment copy enabled but S3/RustFS endpoint or credentials are missing; attachment events will degrade to metadata-only failures")
		liveAttachmentCopyConfig = config
		return
	}
	store, err := discordevent.NewS3AttachmentStore(ctx, discordevent.S3StoreConfig{
		Endpoint:        endpoint,
		Region:          envFirst("DISCORD_ATTACHMENT_S3_REGION", "AWS_REGION"),
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		Provider:        envString("DISCORD_ATTACHMENT_STORAGE_PROVIDER", "rustfs"),
		UsePathStyle:    envBool("DISCORD_ATTACHMENT_S3_PATH_STYLE", true),
	})
	if err != nil {
		log.Printf("Discord attachment S3/RustFS store unavailable; attachment events will degrade to metadata-only failures: %v", err)
		liveAttachmentCopyConfig = config
		return
	}
	config.Store = store
	liveAttachmentCopyConfig = config
}

func liveAttachmentCopyConfigFromEnv() discordevent.AttachmentCopyConfig {
	return discordevent.AttachmentCopyConfig{
		Enabled:              envBool("DISCORD_ATTACHMENT_COPY_ENABLED", false),
		Bucket:               envString("DISCORD_ATTACHMENT_COPY_BUCKET", "pc-principal-discord-media"),
		MaxSizeBytes:         envInt("DISCORD_ATTACHMENT_COPY_MAX_SIZE_BYTES", 25_000_000),
		ContentTypeAllowlist: envCSV("DISCORD_ATTACHMENT_COPY_CONTENT_TYPES", []string{"image/png", "image/jpeg", "image/gif", "image/webp", "video/mp4", "video/webm", "application/pdf", "text/plain"}),
		Timeout:              envDuration("DISCORD_ATTACHMENT_COPY_TIMEOUT", 10*time.Second),
	}
}

func envFirst(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func envString(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envCSV(name string, fallback []string) []string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return fallback
	}
	return result
}
