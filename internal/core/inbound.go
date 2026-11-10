package core

import "context"

// PlatformDiscord and PlatformBRODEC are the values of Inbound.Platform for
// the two front ends.
const (
	PlatformDiscord = "discord"
	PlatformBRODEC  = "brodec"
)

// Kind says what an Inbound is.
type Kind int

const (
	// KindMessage is something a person wrote. It is the zero value.
	KindMessage Kind = iota
	// KindCommand is a command the platform parsed for the bot (a typed
	// "/poll ..."). Command holds its name and Args what followed it.
	KindCommand
	// KindButton is a press of a button on one of the bot's cards. ButtonID
	// holds the id the card gave the button.
	KindButton
)

// Inbound is one message as the brain sees it, whatever platform it came
// from.
type Inbound struct {
	// Platform names the front end that received the message.
	Platform string
	// Kind says whether this is a message, a typed command or a button
	// press.
	Kind Kind
	// MessageID is the platform's id of the message.
	MessageID string
	// SpaceID is the community the room belongs to (a Discord guild). It is
	// empty for a direct message.
	SpaceID string
	// RoomID is where the message was written (a Discord channel, thread or
	// DM channel). Answers go back to the same room.
	RoomID string
	// SenderID and SenderName identify who wrote the message.
	SenderID   string
	SenderName string
	// Text is the message as it was written, the bot's address included.
	Text string
	// Direct is true for a private conversation between one person and the
	// bot.
	Direct bool
	// ReplyTo is the message this one answers, or nil.
	ReplyTo *Reference
	// Addressed is true when the platform marked the message as meant for
	// the bot (a mention).
	Addressed bool
	// AddressForms are the ways the bot's address may be spelled inside Text
	// (on Discord, "<@id>" and "<@!id>"). The brain cuts the first one it
	// finds out of the text before it treats the rest as what was said. A
	// message can be Addressed and contain none of them (a Discord reply
	// that pings the bot); the conversation then stays silent.
	AddressForms []string
	// Plain is true when Text is already what was said to the bot: the
	// front end cut the bot's address out, or the platform never writes one
	// into the text. AddressForms is then not looked at.
	Plain bool
	// Command is the word that would name a command if one is registered
	// under it, in lower case, or empty. The front end decides where a
	// command word sits in a message. For a KindCommand it is the command's
	// name.
	Command string
	// Args is what was typed after the name of a KindCommand.
	Args string
	// ButtonID is the id of the button a KindButton pressed.
	ButtonID string
}

// Reference points at the message an Inbound replies to.
type Reference struct {
	MessageID string
	SenderID  string
	// FromBot is true when the referenced message was written by this bot.
	FromBot bool
	// Text is what the referenced message said, when the platform hands it
	// over with the reply. The conversation shows it to the model for the
	// one answer and does not keep it.
	Text string
}

// Describer tells the brain who is speaking and where, in the words that go
// into the prompt. A front end implements it; the brain calls it only at the
// moment it needs the description, because building one may cost the
// platform a lookup.
type Describer interface {
	// Sender describes the person alone, for example "blackflame (Admin)".
	Sender(ctx context.Context, in Inbound) string
	// Scene describes the person and their surroundings (the community, the
	// room, who else is around) for a message that said the given words.
	Scene(ctx context.Context, in Inbound, said string) string
}

// describeSender and describeScene fall back to the sender's name when a
// front end has nothing more to say.
func describeSender(ctx context.Context, d Describer, in Inbound) string {
	if d == nil {
		return in.SenderName
	}
	return d.Sender(ctx, in)
}

func describeScene(ctx context.Context, d Describer, in Inbound, said string) string {
	if d == nil {
		return in.SenderName
	}
	return d.Scene(ctx, in, said)
}
