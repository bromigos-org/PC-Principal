package discordevent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

const copyStatusStored = "stored"

var unsafeKeyChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

type AttachmentStore interface {
	StoreAttachment(ctx context.Context, object AttachmentObject) (AttachmentObjectPointer, error)
}

type AttachmentObject struct {
	Bucket      string
	Key         string
	ContentType string
	Size        int
	SHA256      string
	Body        []byte
}

type AttachmentObjectPointer struct {
	Bucket      string
	Key         string
	Provider    string
	Endpoint    string
	ContentType string
	Size        int
	SHA256      string
}

type attachmentNormalizeInput struct {
	ctx        context.Context
	message    *discordgo.Message
	attachment *discordgo.MessageAttachment
	scope      memory.Scope
	occurredAt time.Time
}

type attachmentCopyInput struct {
	ctx        context.Context
	message    *discordgo.Message
	attachment *discordgo.MessageAttachment
	payload    memory.JsonObject
}

func (n Normalizer) normalizeAttachment(input attachmentNormalizeInput) memory.ClientEvent {
	payload := memory.JsonObject{
		"message_id":    input.message.ID,
		"channel_id":    input.message.ChannelID,
		"guild_id":      input.message.GuildID,
		"attachment_id": input.attachment.ID,
		"filename":      input.attachment.Filename,
		"content_type":  input.attachment.ContentType,
		"size":          input.attachment.Size,
		"width":         input.attachment.Width,
		"height":        input.attachment.Height,
		"spoiler":       attachmentSpoiler(input.attachment),
		"url":           sanitizeURL(input.attachment.URL),
		"proxy_url":     sanitizeURL(input.attachment.ProxyURL),
		"source_marker": string(n.config.SourceMarker),
	}
	n.applyAttachmentCopy(attachmentCopyInput{ctx: input.ctx, message: input.message, attachment: input.attachment, payload: payload})
	return n.clientEvent(
		memory.EventTypeAttachmentDiscovered,
		input.occurredAt,
		actorForUser(input.message.Author),
		memory.ClientEventSubject{ID: input.attachment.ID, Type: "attachment", ParentID: input.message.ID},
		payload,
		discordContext(input.message.GuildID, input.message.ChannelID, input.message.ID),
		input.scope,
	)
}

func (n Normalizer) applyAttachmentCopy(input attachmentCopyInput) {
	decision := n.config.AttachmentCopy.decision(input.attachment)
	if !decision.enabled {
		return
	}
	if !decision.allowed {
		input.payload["copy_status"] = "skipped"
		input.payload["copy_error"] = decision.reason
		return
	}
	pointer, err := n.copyAttachment(input.ctx, input.message, input.attachment)
	if err != nil {
		input.payload["copy_status"] = "failed"
		input.payload["copy_error"] = err.Error()
		return
	}
	input.payload["copy_status"] = copyStatusStored
	input.payload["storage_bucket"] = pointer.Bucket
	input.payload["storage_key"] = pointer.Key
	input.payload["storage_provider"] = pointer.Provider
	input.payload["storage_endpoint"] = pointer.Endpoint
	input.payload["storage_content_type"] = pointer.ContentType
	input.payload["storage_size"] = pointer.Size
	input.payload["storage_sha256"] = pointer.SHA256
}

func (n Normalizer) copyAttachment(ctx context.Context, message *discordgo.Message, attachment *discordgo.MessageAttachment) (AttachmentObjectPointer, error) {
	if n.config.AttachmentCopy.Store == nil {
		return AttachmentObjectPointer{}, fmt.Errorf("upload_failed: attachment store is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, n.config.AttachmentCopy.Timeout)
	defer cancel()
	body, err := n.downloadAttachment(ctx, attachment)
	if err != nil {
		return AttachmentObjectPointer{}, err
	}
	checksum := sha256.Sum256(body)
	object := AttachmentObject{
		Bucket:      n.config.AttachmentCopy.Bucket,
		Key:         attachmentObjectKey(message, attachment),
		ContentType: attachment.ContentType,
		Size:        len(body),
		SHA256:      hex.EncodeToString(checksum[:]),
		Body:        body,
	}
	pointer, err := n.config.AttachmentCopy.Store.StoreAttachment(ctx, object)
	if err != nil {
		return AttachmentObjectPointer{}, fmt.Errorf("upload_failed: %w", err)
	}
	return pointer, nil
}

func (n Normalizer) downloadAttachment(ctx context.Context, attachment *discordgo.MessageAttachment) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, attachment.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("download_failed: build request: %w", err)
	}
	client := &http.Client{Timeout: n.config.AttachmentCopy.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download_failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("download_failed: status %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, int64(n.config.AttachmentCopy.MaxSizeBytes)+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("download_failed: read body: %w", err)
	}
	if len(body) > n.config.AttachmentCopy.MaxSizeBytes {
		return nil, fmt.Errorf("download_failed: size_exceeds_limit")
	}
	return body, nil
}

func (n Normalizer) normalizeLink(message *discordgo.Message, link string, scope memory.Scope, occurredAt time.Time) memory.ClientEvent {
	return n.clientEvent(
		memory.EventTypeLinkDiscovered,
		occurredAt,
		actorForUser(message.Author),
		memory.ClientEventSubject{ID: hashID("link", message.ID, link), Type: "link", ParentID: message.ID},
		memory.JsonObject{
			"message_id":    message.ID,
			"channel_id":    message.ChannelID,
			"guild_id":      message.GuildID,
			"url":           link,
			"source_marker": string(n.config.SourceMarker),
		},
		discordContext(message.GuildID, message.ChannelID, message.ID),
		scope,
	)
}

func attachmentSpoiler(attachment *discordgo.MessageAttachment) bool {
	return strings.HasPrefix(attachment.Filename, "SPOILER_")
}

func (c AttachmentCopyConfig) allows(attachment *discordgo.MessageAttachment) bool {
	return c.decision(attachment).allowed
}

type attachmentCopyDecision struct {
	enabled bool
	allowed bool
	reason  string
}

func (c AttachmentCopyConfig) decision(attachment *discordgo.MessageAttachment) attachmentCopyDecision {
	if !c.Enabled {
		return attachmentCopyDecision{enabled: false}
	}
	if attachment.Size > c.MaxSizeBytes {
		return attachmentCopyDecision{enabled: true, reason: "size_exceeds_limit"}
	}
	for _, contentType := range c.ContentTypeAllowlist {
		if contentType == attachment.ContentType {
			return attachmentCopyDecision{enabled: true, allowed: true}
		}
	}
	return attachmentCopyDecision{enabled: true, reason: "content_type_not_allowed"}
}

func attachmentObjectKey(message *discordgo.Message, attachment *discordgo.MessageAttachment) string {
	return "guilds/" + safeObjectKeyPart(message.GuildID) + "/channels/" + safeObjectKeyPart(message.ChannelID) + "/messages/" + safeObjectKeyPart(message.ID) + "/attachments/" + safeObjectKeyPart(attachment.ID) + "/" + safeFilename(attachment.Filename)
}

func safeFilename(filename string) string {
	trimmed := strings.Trim(strings.TrimPrefix(filename, "SPOILER_"), ". ")
	if trimmed == "" {
		return "attachment"
	}
	safe := strings.Trim(unsafeKeyChars.ReplaceAllString(trimmed, "-"), "-")
	if safe == "" {
		return "attachment"
	}
	return safe
}

func safeObjectKeyPart(value string) string {
	if value == "" {
		return "unknown"
	}
	return safeFilename(value)
}
