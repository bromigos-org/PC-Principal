package commands

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/bromigos-org/pc-principal/internal/core"
	"github.com/bwmarrin/discordgo"
)

const (
	discordMessageLimit     = 2000
	discordSafeMessageLimit = 1900
	emptyAssistantResponse  = "Bro, my brain totally blanked. Try that again."
)

// inboundFromMessage turns a Discord message into what the brain sees. The
// message must have an author.
func inboundFromMessage(s *discordgo.Session, m *discordgo.MessageCreate) core.Inbound {
	in := core.Inbound{
		Platform:     core.PlatformDiscord,
		MessageID:    m.ID,
		SpaceID:      m.GuildID,
		RoomID:       m.ChannelID,
		SenderID:     m.Author.ID,
		SenderName:   m.Author.Username,
		Text:         m.Content,
		Direct:       m.GuildID == "",
		Addressed:    mentionsBot(s, m),
		AddressForms: mentionForms(s.State.User.ID),
		Command:      commandWord(m.Content),
	}
	if referenced := m.ReferencedMessage; referenced != nil {
		in.ReplyTo = &core.Reference{MessageID: referenced.ID, FromBot: referencesBot(s, m)}
		if referenced.Author != nil {
			in.ReplyTo.SenderID = referenced.Author.ID
		}
	}
	return in
}

// mentionForms are the two ways Discord writes a mention of a user in the
// text of a message.
func mentionForms(userID string) []string {
	return []string{"<@" + userID + ">", "<@!" + userID + ">"}
}

// commandWord is the second word of a message, in lower case: where a
// command sits in "@PC-Principal ping". It is empty for a message of fewer
// than two words.
func commandWord(content string) string {
	parts := strings.Fields(content)
	if len(parts) < 2 {
		return ""
	}
	return strings.ToLower(parts[1])
}

func mentionConversationText(content string, botID string) (string, bool) {
	return core.AddressedText(content, mentionForms(botID))
}

// discordDescriber tells the brain who wrote a Discord message and where,
// looking the names up when the brain asks.
type discordDescriber struct {
	session *discordgo.Session
	message *discordgo.MessageCreate
}

func (d discordDescriber) Sender(context.Context, core.Inbound) string {
	return memberContext(d.session, d.message)
}

func (d discordDescriber) Scene(_ context.Context, _ core.Inbound, said string) string {
	return buildConversationContext(d.session, d.message, fetchContextData(d.session, d.message, said))
}

// discordResponder is how the brain answers in one Discord channel. Cards,
// edits and private answers are not built for Discord.
type discordResponder struct {
	core.TextOnly
	session   *discordgo.Session
	channelID string
	// message is the message being answered. It is nil when the brain
	// speaks in a channel nobody wrote in yet (a thread the bot just made).
	message *discordgo.MessageCreate
}

// respondTo answers a message in the channel it was written in.
func respondTo(s *discordgo.Session, m *discordgo.MessageCreate) discordResponder {
	return discordResponder{session: s, channelID: m.ChannelID, message: m}
}

// Acknowledge reacts to the message with the :PC: emoji.
func (r discordResponder) Acknowledge(context.Context) error {
	if r.message != nil {
		reactPC(r.session, r.message)
	}
	return nil
}

// Reply sends text of any length, split into messages Discord accepts.
func (r discordResponder) Reply(_ context.Context, text string) error {
	return sendDiscordResponse(r.session, r.channelID, text)
}

// Say sends one message as is.
func (r discordResponder) Say(_ context.Context, text string) error {
	_, err := r.session.ChannelMessageSend(r.channelID, text)
	return err
}

func sendDiscordResponse(s *discordgo.Session, channelID string, response string) error {
	for _, chunk := range chunkDiscordMessage(response) {
		msg := &discordgo.MessageSend{
			Content: chunk,
			AllowedMentions: &discordgo.MessageAllowedMentions{
				Parse: []discordgo.AllowedMentionType{
					discordgo.AllowedMentionTypeUsers,
					discordgo.AllowedMentionTypeRoles,
					discordgo.AllowedMentionTypeEveryone,
				},
			},
		}
		if _, err := s.ChannelMessageSendComplex(channelID, msg); err != nil {
			return fmt.Errorf("send Discord message: %w", err)
		}
	}
	return nil
}

func chunkDiscordMessage(response string) []string {
	text := strings.TrimSpace(response)
	if text == "" {
		return []string{emptyAssistantResponse}
	}
	if len(text) <= discordSafeMessageLimit {
		return []string{text}
	}

	chunks := make([]string, 0, len(text)/discordSafeMessageLimit+1)
	for len(text) > 0 {
		cut := safeChunkSize(text)
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	return chunks
}

func safeChunkSize(text string) int {
	if len(text) <= discordSafeMessageLimit {
		return len(text)
	}
	cut := discordSafeMessageLimit
	for !utf8.ValidString(text[:cut]) {
		cut--
	}
	return cut
}
