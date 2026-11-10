package brodec

import (
	"context"
	"fmt"
	"strings"

	"github.com/bromigos-org/pc-principal/internal/core"
)

const (
	helpButtonPrefix  = "help:"
	helpButtonPrivacy = helpButtonPrefix + "privacy"
	helpButtonSees    = helpButtonPrefix + "sees"
)

// commands are the commands the bot has on BRODEC. brodec/commands.json at
// the root of the repository registers the same list with the platform.
func (b *Bot) commands() []core.Command {
	list := []core.Command{
		{Name: "help", Description: "What I do here, and what I see."},
		core.Ping(),
		{Name: "poll", Description: "Ask the room: /poll Question | A | B", Run: b.polls.create},
	}
	listed := append([]core.Command(nil), list...)
	list[0].Run = func(ctx context.Context, _ core.Inbound, r core.Responder) {
		_, _ = r.Card(ctx, helpCard(listed))
	}
	return list
}

// buttons are the buttons on the bot's cards.
func (b *Bot) buttons() []core.Button {
	return []core.Button{
		{Prefix: helpButtonPrefix, Run: b.helpButton},
		{Prefix: pollButtonPrefix, Run: b.polls.vote},
	}
}

// helpCard is what /help posts in the room: the commands, and two buttons
// whose answers only the person who taps them sees.
func helpCard(commands []core.Command) core.Card {
	card := core.HelpCard("PC PRINCIPAL", "/", commands)
	card.Text = "Mention me or reply to me and I'll talk. In a direct message, just write."
	card.Buttons = [][]core.CardButton{{
		{ID: helpButtonPrivacy, Label: "PRIVACY", Style: core.ButtonPrimary},
		{ID: helpButtonSees, Label: "WHAT I SEE"},
	}}
	card.Footer = fmt.Sprintf("I only see what you send me, and I forget it after %s. Tap PRIVACY.", contextLifetimeWords())
	return card
}

// helpButton answers a tap on one of the help card's buttons, privately.
func (b *Bot) helpButton(ctx context.Context, in core.Inbound, r core.Responder) {
	switch in.ButtonID {
	case helpButtonPrivacy:
		_ = r.Answer(ctx, privacyText(b.config.AIWhere))
	case helpButtonSees:
		_ = r.Answer(ctx, seesText())
	default:
		if failer, ok := r.(interface {
			Fail(context.Context, string) error
		}); ok {
			_ = failer.Fail(ctx, unknownButtonLine)
		}
	}
}

func contextLifetimeWords() string {
	return fmt.Sprintf("%d hours", int(contextLifetime.Hours()))
}

// privacyText says what the bot keeps. Every number in it is the constant
// the code works with, so the text cannot say one thing while the code does
// another. aiWhere says where the model runs, which only the deployment
// knows.
func privacyText(aiWhere string) string {
	lines := []string{
		"PRIVACY",
		"",
		"• What reaches me: messages that mention me or reply to me, my commands, taps on my buttons, and direct messages to me. Nothing else in a room.",
		fmt.Sprintf("• What you say to me goes to %s, which writes my answer. Nothing is used to train a model.", aiWhere),
		fmt.Sprintf("• I keep a short context so I can follow a conversation: what was said to me in this room and what I answered, at most %d messages, erased %s after the last one. A message you replied to is shown to the model for that one answer and is not kept.", contextMessages, contextLifetimeWords()),
		"• I have no long-term memory here. I store nothing you say anywhere else, nothing crosses from one room to another, and nothing comes over from Discord or goes there.",
		fmt.Sprintf("• A poll keeps its question, its choices, the command that started it, and one scrambled marker per voter (not your ID) so that you can change your vote. I never show who voted for what. A poll and its votes are erased after %d days.", int(pollLifetime.Hours()/24)),
		"",
		"Only you see this.",
	}
	return strings.Join(lines, "\n")
}

// seesText says what the bot receives.
func seesText() string {
	lines := []string{
		"WHAT I SEE",
		"",
		"• In a group: only the messages that mention me or reply to me, my commands, and taps on my buttons. Those are handed to me one at a time. The rest of the room never reaches me, and neither does anything from before I joined.",
		"• In a direct message: every message you send me.",
		"• With each of those I get the room, your ID and the text. With a reply I also get the message you replied to, when it was mine or mentioned me.",
		"• I get no names, no member list, no attachments and no reactions.",
		"",
		"Only you see this.",
	}
	return strings.Join(lines, "\n")
}
