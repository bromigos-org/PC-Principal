package commands

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/core"
	"github.com/bwmarrin/discordgo"
)

func TestInboundFromMessage_maps_a_guild_mention(t *testing.T) {
	// Given
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" PING now")

	// When
	got := inboundFromMessage(s, m)

	// Then
	want := core.Inbound{
		Platform:     "discord",
		MessageID:    "message-1",
		SpaceID:      "guild-1",
		RoomID:       "channel-1",
		SenderID:     "user-1",
		SenderName:   "blackflame",
		Text:         "<@bot-1> PING now",
		Direct:       false,
		Addressed:    true,
		AddressForms: []string{"<@bot-1>", "<@!bot-1>"},
		Command:      "ping",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected inbound:\n got %#v\nwant %#v", got, want)
	}
}

func TestInboundFromMessage_maps_a_direct_message_without_a_mention(t *testing.T) {
	// Given
	s, m := mentionSessionAndMessage(t, "hello")
	m.GuildID = ""
	m.ChannelID = "dm-channel-1"
	m.Mentions = nil
	m.Member = nil

	// When
	got := inboundFromMessage(s, m)

	// Then
	if !got.Direct || got.SpaceID != "" || got.RoomID != "dm-channel-1" {
		t.Fatalf("expected a direct message in its DM channel, got %#v", got)
	}
	if got.Addressed || got.Command != "" || got.ReplyTo != nil {
		t.Fatalf("expected no address, no command word and no reference, got %#v", got)
	}
}

func TestInboundFromMessage_maps_what_the_message_replies_to(t *testing.T) {
	// Given
	s, toBot := mentionSessionAndMessage(t, "tell me more")
	toBot.ReferencedMessage = &discordgo.Message{ID: "message-0", Author: &discordgo.User{ID: "bot-1", Bot: true}}
	_, toMember := mentionSessionAndMessage(t, "tell me more")
	toMember.ReferencedMessage = &discordgo.Message{ID: "message-0", Author: &discordgo.User{ID: "user-2"}}
	_, toUnknown := mentionSessionAndMessage(t, "tell me more")
	toUnknown.ReferencedMessage = &discordgo.Message{ID: "message-0"}

	// When
	gotBot := inboundFromMessage(s, toBot).ReplyTo
	gotMember := inboundFromMessage(s, toMember).ReplyTo
	gotUnknown := inboundFromMessage(s, toUnknown).ReplyTo

	// Then
	if gotBot == nil || *gotBot != (core.Reference{MessageID: "message-0", SenderID: "bot-1", FromBot: true}) {
		t.Fatalf("expected a reference to the bot's message, got %#v", gotBot)
	}
	if gotMember == nil || *gotMember != (core.Reference{MessageID: "message-0", SenderID: "user-2"}) {
		t.Fatalf("expected a reference to a member's message, got %#v", gotMember)
	}
	if gotUnknown == nil || *gotUnknown != (core.Reference{MessageID: "message-0"}) {
		t.Fatalf("expected a reference without a sender, got %#v", gotUnknown)
	}
}

func TestCommandWord_is_the_second_word_in_lower_case(t *testing.T) {
	// Given
	tests := map[string]string{
		"<@bot-1> ping":           "ping",
		"<@bot-1>   MDelete 5":    "mdelete",
		"<@user-2> ping <@bot-1>": "ping",
		"hello <@bot-1> ping":     "<@bot-1>",
		"<@bot-1>":                "",
		"":                        "",
	}

	for content, want := range tests {
		// When
		got := commandWord(content)

		// Then
		if got != want {
			t.Fatalf("commandWord(%q) = %q, want %q", content, got, want)
		}
	}
}

func TestDiscordResponder_reply_splits_long_text_and_say_sends_it_as_is(t *testing.T) {
	// Given
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" hello")
	s.Client = recorder.httpClient(t)
	responder := respondTo(s, m)
	long := strings.Repeat("a", discordSafeMessageLimit+10)

	// When
	replyErr := responder.Reply(context.Background(), long)
	sayErr := responder.Say(context.Background(), "  Pong!  ")

	// Then
	if replyErr != nil || sayErr != nil {
		t.Fatalf("expected both sends to succeed, got %v and %v", replyErr, sayErr)
	}
	if len(recorder.sent) != 3 {
		t.Fatalf("expected two reply messages and one said message, got %#v", recorder.sent)
	}
	if recorder.sent[0].Content+recorder.sent[1].Content != long || recorder.sent[0].AllowedMentions == nil || recorder.sent[1].AllowedMentions == nil {
		t.Fatalf("expected the reply split in two with its mention settings, got %#v", recorder.sent[:2])
	}
	if recorder.sent[2].Content != "  Pong!  " || recorder.sent[2].AllowedMentions != nil {
		t.Fatalf("expected the said line untouched and without mention settings, got %#v", recorder.sent[2])
	}
}

func TestDiscordResponder_acknowledge_reacts_only_when_there_is_a_message(t *testing.T) {
	// Given
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" hello")
	s.Client = recorder.httpClient(t)

	// When
	withMessageErr := respondTo(s, m).Acknowledge(context.Background())
	withoutMessageErr := discordResponder{session: s, channelID: "thread-1"}.Acknowledge(context.Background())

	// Then
	if withMessageErr != nil || withoutMessageErr != nil {
		t.Fatalf("expected acknowledging never to fail, got %v and %v", withMessageErr, withoutMessageErr)
	}
	if len(recorder.reactions) != 1 {
		t.Fatalf("expected one :PC: reaction, got %#v", recorder.reactions)
	}
}

func TestDiscordResponder_has_no_cards_edits_or_private_answers(t *testing.T) {
	// Given
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" hello")
	var responder core.Responder = respondTo(s, m)
	ctx := context.Background()

	// When
	_, cardErr := responder.Card(ctx, core.Card{Title: "Poll"})
	editErr := responder.Edit(ctx, "card-1", core.Card{Title: "Poll"})
	answerErr := responder.Answer(ctx, "only you see this")

	// Then
	for verb, err := range map[string]error{"Card": cardErr, "Edit": editErr, "Answer": answerErr} {
		if !errors.Is(err, core.ErrUnsupported) {
			t.Fatalf("expected %s to be unsupported on Discord, got %v", verb, err)
		}
	}
}
