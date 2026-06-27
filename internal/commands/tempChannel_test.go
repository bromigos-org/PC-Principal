package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestMessageReactionAdd_Renames_temp_channel_when_bromigo_reacts(t *testing.T) {
	// Given
	prepareTempChannel(t, "voice-1", "channel-1")
	recorder := &tempChannelDiscordRecorder{}
	s := tempChannelSession(t)
	s.Client = recorder.httpClient(t)
	reaction := &discordgo.MessageReactionAdd{MessageReaction: &discordgo.MessageReaction{
		MessageID: "message-1",
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Emoji:     discordgo.Emoji{Name: "bromigo"},
	}}

	// When
	MessageReactionAdd(s, reaction)

	// Then
	if len(recorder.edits) != 1 {
		t.Fatalf("expected one channel rename, got %#v", recorder.edits)
	}
	if recorder.edits[0].channelID != "voice-1" || recorder.edits[0].name != "squad lobby" {
		t.Fatalf("expected bromigo reaction to rename voice channel, got %#v", recorder.edits[0])
	}
}

func prepareTempChannel(t *testing.T, voiceChannelID string, textChannelID string) {
	t.Helper()
	mutex.Lock()
	previous := botCreatedChannels
	botCreatedChannels = map[string]ChannelInfo{voiceChannelID: {CreatedByBot: true, TextChannelID: textChannelID}}
	mutex.Unlock()
	t.Cleanup(func() {
		mutex.Lock()
		botCreatedChannels = previous
		mutex.Unlock()
	})
}

func tempChannelSession(t *testing.T) *discordgo.Session {
	t.Helper()
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("new discord session: %v", err)
	}
	return s
}

type tempChannelDiscordRecorder struct {
	edits []tempChannelEdit
}

type tempChannelEdit struct {
	channelID string
	name      string
}

func (r *tempChannelDiscordRecorder) httpClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: tempChannelRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		status := http.StatusInternalServerError
		body := `{"message":"test failure"}`
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/channels/channel-1/messages/message-1") {
			status = http.StatusOK
			body = `{"id":"message-1","channel_id":"channel-1","content":"squad lobby"}`
		}
		if req.Method == http.MethodPatch && strings.Contains(req.URL.Path, "/channels/voice-1") {
			var edit discordgo.ChannelEdit
			if err := json.NewDecoder(req.Body).Decode(&edit); err != nil {
				t.Fatalf("decode Discord channel edit: %v", err)
			}
			r.edits = append(r.edits, tempChannelEdit{channelID: "voice-1", name: edit.Name})
			status = http.StatusOK
			body = `{"id":"voice-1","name":"squad lobby","type":2}`
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
}

type tempChannelRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tempChannelRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
