package discordevent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

type recordingAttachmentStore struct {
	err     error
	objects []AttachmentObject
}

func (s *recordingAttachmentStore) StoreAttachment(ctx context.Context, object AttachmentObject) (AttachmentObjectPointer, error) {
	s.objects = append(s.objects, object)
	if s.err != nil {
		return AttachmentObjectPointer{}, s.err
	}
	return AttachmentObjectPointer{
		Bucket:      object.Bucket,
		Key:         object.Key,
		Provider:    "fake-s3",
		Endpoint:    "http://rustfs.test",
		ContentType: object.ContentType,
		Size:        object.Size,
		SHA256:      object.SHA256,
	}, nil
}

func TestAttachmentCopy_keeps_metadata_only_payload_when_disabled(t *testing.T) {
	// Given
	store := &recordingAttachmentStore{}
	normalizer := New(Config{TenantID: "tenant-1", AgentID: "agent-1", SourceMarker: SourceMarkerLive, ObservedAt: fixedObservedAt(), AttachmentCopy: AttachmentCopyConfig{Store: store}})
	message := messageWithAttachment("https://cdn.example.test/a.png", "image/png", 12)

	// When
	events := normalizer.NormalizeMessageCreateWithContext(context.Background(), message)

	// Then
	attachment := eventOfType(events, memory.EventTypeAttachmentDiscovered)
	if len(store.objects) != 0 {
		t.Fatalf("expected disabled copy to skip storage, got %#v", store.objects)
	}
	if attachment.Payload["filename"] != "report final.pdf" || attachment.Payload["url"] != "https://cdn.example.test/a.png" {
		t.Fatalf("expected existing metadata payload to remain, got %#v", attachment.Payload)
	}
	if _, ok := attachment.Payload["copy_status"]; ok {
		t.Fatalf("expected disabled copy to preserve metadata-only behavior, got %#v", attachment.Payload)
	}
}

func TestAttachmentCopy_stores_allowed_attachment_pointer_when_enabled(t *testing.T) {
	// Given
	body := "stored bytes"
	server := attachmentServer(t, http.StatusOK, "application/pdf", body)
	store := &recordingAttachmentStore{}
	normalizer := New(Config{TenantID: "tenant-1", AgentID: "agent-1", SourceMarker: SourceMarkerLive, ObservedAt: fixedObservedAt(), AttachmentCopy: AttachmentCopyConfig{
		Enabled:              true,
		Bucket:               "pc-principal-discord-media",
		MaxSizeBytes:         1_000,
		ContentTypeAllowlist: []string{"application/pdf"},
		Timeout:              time.Second,
		Store:                store,
	}})
	message := messageWithAttachment(server.URL+"/attachment", "application/pdf", len(body))

	// When
	events := normalizer.NormalizeMessageCreateWithContext(context.Background(), message)

	// Then
	attachment := eventOfType(events, memory.EventTypeAttachmentDiscovered)
	if len(store.objects) != 1 {
		t.Fatalf("expected one stored object, got %#v", store.objects)
	}
	if string(store.objects[0].Body) != body {
		t.Fatalf("expected downloaded bytes to be uploaded, got %q", string(store.objects[0].Body))
	}
	wantChecksum := sha256Hex([]byte(body))
	wantKey := "guilds/guild-1/channels/channel-1/messages/message-1/attachments/attachment-1/report-final.pdf"
	if store.objects[0].Key != wantKey || attachment.Payload["storage_key"] != wantKey {
		t.Fatalf("expected deterministic object key %q, got object=%#v payload=%#v", wantKey, store.objects[0], attachment.Payload)
	}
	if attachment.Payload["copy_status"] != "stored" || attachment.Payload["storage_bucket"] != "pc-principal-discord-media" || attachment.Payload["storage_provider"] != "fake-s3" || attachment.Payload["storage_endpoint"] != "http://rustfs.test" {
		t.Fatalf("expected stored pointer metadata, got %#v", attachment.Payload)
	}
	if attachment.Payload["storage_content_type"] != "application/pdf" || attachment.Payload["storage_size"] != len(body) || attachment.Payload["storage_sha256"] != wantChecksum {
		t.Fatalf("expected content metadata and checksum, got %#v", attachment.Payload)
	}
}

func TestAttachmentCopy_degrades_to_metadata_when_attachment_disallowed(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		size        int
		wantReason  string
	}{
		{name: "content type blocked", contentType: "application/x-msdownload", size: 10, wantReason: "content_type_not_allowed"},
		{name: "size blocked", contentType: "application/pdf", size: 2_000, wantReason: "size_exceeds_limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			store := &recordingAttachmentStore{}
			normalizer := New(Config{TenantID: "tenant-1", AgentID: "agent-1", SourceMarker: SourceMarkerLive, ObservedAt: fixedObservedAt(), AttachmentCopy: AttachmentCopyConfig{
				Enabled:              true,
				MaxSizeBytes:         1_000,
				ContentTypeAllowlist: []string{"application/pdf"},
				Store:                store,
			}})
			message := messageWithAttachment("https://cdn.example.test/file", tt.contentType, tt.size)

			// When
			events := normalizer.NormalizeMessageCreateWithContext(context.Background(), message)

			// Then
			attachment := eventOfType(events, memory.EventTypeAttachmentDiscovered)
			if len(store.objects) != 0 {
				t.Fatalf("expected disallowed attachment to skip storage, got %#v", store.objects)
			}
			if attachment.Payload["copy_status"] != "skipped" || attachment.Payload["copy_error"] != tt.wantReason {
				t.Fatalf("expected clear skip metadata, got %#v", attachment.Payload)
			}
		})
	}
}

func TestAttachmentCopy_degrades_to_metadata_when_download_or_upload_fails(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		storeErr  error
		wantError string
	}{
		{name: "download fails", status: http.StatusBadGateway, wantError: "download_failed"},
		{name: "upload fails", status: http.StatusOK, storeErr: errors.New("rustfs offline"), wantError: "upload_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			server := attachmentServer(t, tt.status, "application/pdf", "body")
			store := &recordingAttachmentStore{err: tt.storeErr}
			normalizer := New(Config{TenantID: "tenant-1", AgentID: "agent-1", SourceMarker: SourceMarkerLive, ObservedAt: fixedObservedAt(), AttachmentCopy: AttachmentCopyConfig{
				Enabled:              true,
				MaxSizeBytes:         1_000,
				ContentTypeAllowlist: []string{"application/pdf"},
				Timeout:              time.Second,
				Store:                store,
			}})
			message := messageWithAttachment(server.URL+"/attachment", "application/pdf", 4)

			// When
			events := normalizer.NormalizeMessageCreateWithContext(context.Background(), message)

			// Then
			attachment := eventOfType(events, memory.EventTypeAttachmentDiscovered)
			if attachment.Payload["copy_status"] != "failed" {
				t.Fatalf("expected failed copy status, got %#v", attachment.Payload)
			}
			copyError, _ := attachment.Payload["copy_error"].(string)
			if !strings.Contains(copyError, tt.wantError) {
				t.Fatalf("expected %q in copy error, got %#v", tt.wantError, attachment.Payload)
			}
			if _, ok := attachment.Payload["storage_key"]; ok {
				t.Fatalf("expected no object pointer on failure, got %#v", attachment.Payload)
			}
		})
	}
}

func attachmentServer(t *testing.T, status int, contentType string, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write test attachment: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func messageWithAttachment(rawURL string, contentType string, size int) *discordgo.Message {
	return &discordgo.Message{
		ID:        "message-1",
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Content:   "with attachment",
		Timestamp: fixedObservedAt(),
		Author:    &discordgo.User{ID: "user-1", Username: "blackflame"},
		Attachments: []*discordgo.MessageAttachment{{
			ID:          "attachment-1",
			URL:         rawURL,
			ProxyURL:    rawURL,
			Filename:    "report final.pdf",
			ContentType: contentType,
			Size:        size,
		}},
	}
}

func fixedObservedAt() time.Time {
	return time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
