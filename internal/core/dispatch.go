package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/bromigos-org/pc-principal/internal/ambient"
)

const (
	mentionOnlyFallback = "Bro, I'm here. Say what you need after the mention."
	modelFailureLine    = "Bro, LiteLLM is not cooperating right now. Totally unacceptable."
	// fillerWord may open what is said to the bot ("@PC-Principal hey ...").
	// It is never a command, whatever is registered under it.
	fillerWord = "hey"
)

// Route names the handler a message went to.
type Route int

const (
	// RouteNone: no handler took the message. The bot stays silent.
	RouteNone Route = iota
	// RouteCommand: the message addressed the bot with a command word.
	RouteCommand
	// RouteConversation: the message addressed the bot without a command.
	RouteConversation
	// RouteFollowUp: the message was written in a session's room.
	RouteFollowUp
	// RouteAmbient: the message continues a conversation the sender was
	// just having with the bot, without addressing it.
	RouteAmbient
	// RouteButton: a button on one of the bot's cards was pressed.
	RouteButton
)

func (r Route) String() string {
	switch r {
	case RouteNone:
		return "none"
	case RouteCommand:
		return "command"
	case RouteConversation:
		return "conversation"
	case RouteFollowUp:
		return "follow-up"
	case RouteAmbient:
		return "ambient"
	case RouteButton:
		return "button"
	default:
		return fmt.Sprintf("Route(%d)", int(r))
	}
}

// Command is something the bot does when it is addressed with a word.
type Command struct {
	// Name is the word, in lower case.
	Name string
	// Description says what the command does, in one line, for a list of
	// commands. A front end that keeps its own list leaves it empty.
	Description string
	// Run carries the command out. It answers through the Responder, or by
	// whatever means the front end that registered it has.
	Run func(ctx context.Context, in Inbound, r Responder)
}

// Button is what the bot does when a button on one of its cards is pressed.
type Button struct {
	// Prefix is how the ids of the buttons it handles begin.
	Prefix string
	// Run handles the press. It answers through the Responder.
	Run func(ctx context.Context, in Inbound, r Responder)
}

// Dispatcher decides which handler a message goes to, and runs it.
type Dispatcher struct {
	// Conversation is the brain's conversation. Required.
	Conversation *Conversation
	// Ambient keeps the short sessions in which the bot goes on answering
	// someone it was just talking to. Nil means ambient replies are off.
	Ambient *ambient.Manager
	// Commands are the commands a message can address, in the order they
	// are looked up.
	Commands []Command
	// Buttons handle the presses of the buttons on the bot's cards, in the
	// order they are looked up.
	Buttons []Button
}

// Dispatch routes a message to exactly one handler and reports which:
//
//   - a typed command goes to the command of that name, and a button press
//     to the handler of that button; neither ever reaches the conversation
//   - a message that addresses the bot goes to the command its command word
//     names, or else to the conversation; it never falls through to the
//     handlers below, even when it is answered with silence
//   - otherwise a message in a session's room goes to the session
//   - otherwise it may be answered as part of an ambient session
//
// The front end checks that it would answer the sender at all before it
// calls Dispatch.
func (d *Dispatcher) Dispatch(ctx context.Context, in Inbound, desc Describer, r Responder) Route {
	switch in.Kind {
	case KindCommand:
		return d.HandleCommand(ctx, in, r)
	case KindButton:
		return d.HandleButton(ctx, in, r)
	}
	if in.Addressed {
		return d.HandleAddressed(ctx, in, desc, r)
	}
	if route := d.HandleFollowUp(ctx, in, desc, r); route != RouteNone {
		return route
	}
	return d.HandleAmbient(ctx, in, desc, r)
}

// HandleCommand runs the command a typed command names. An unknown name
// goes nowhere: the model is never asked what a command might have meant.
func (d *Dispatcher) HandleCommand(ctx context.Context, in Inbound, r Responder) Route {
	for _, cmd := range d.Commands {
		if cmd.Name == in.Command {
			cmd.Run(ctx, in, r)
			return RouteCommand
		}
	}
	return RouteNone
}

// HandleButton runs the handler of the button that was pressed.
func (d *Dispatcher) HandleButton(ctx context.Context, in Inbound, r Responder) Route {
	for _, button := range d.Buttons {
		if strings.HasPrefix(in.ButtonID, button.Prefix) {
			button.Run(ctx, in, r)
			return RouteButton
		}
	}
	return RouteNone
}

// HandleAddressed handles a message that addresses the bot: it runs the
// command the message names, or answers in conversation. A message that
// tells the bot to stop ends the room's ambient session and gets no answer.
func (d *Dispatcher) HandleAddressed(ctx context.Context, in Inbound, desc Describer, r Responder) Route {
	if d.Ambient != nil {
		decision, err := d.Ambient.Decide(ctx, ambientMessage(in, true))
		if err != nil {
			fmt.Printf("ambient: state error: %v\n", err)
		}
		if decision.Stop {
			return RouteNone
		}
	}

	if in.Command != "" {
		for _, cmd := range d.Commands {
			if cmd.Name == in.Command && in.Command != fillerWord {
				r.Acknowledge(ctx)
				cmd.Run(ctx, in, r)
				return RouteCommand
			}
		}
	}

	r.Acknowledge(ctx)
	if err := d.AddressedConversation(ctx, in, desc, r); err != nil {
		fmt.Printf("mention: conversation error: %v\n", err)
		r.Say(ctx, modelFailureLine)
	}
	return RouteConversation
}

// AddressedConversation answers, in the room's running conversation, a
// message that addresses the bot, and starts the room's ambient session
// when the answer went out. It does nothing when the bot's address is not
// in the text, and says so when the address is all there is.
func (d *Dispatcher) AddressedConversation(ctx context.Context, in Inbound, desc Describer, r Responder) error {
	userMessage, ok := strings.TrimSpace(in.Text), true
	if !in.Plain {
		userMessage, ok = AddressedText(in.Text, in.AddressForms)
	}
	if !ok {
		return nil
	}
	if userMessage == "" {
		return r.Reply(ctx, mentionOnlyFallback)
	}
	if err := d.Conversation.Respond(ctx, in, userMessage, desc, r); err != nil {
		return err
	}
	if d.Ambient == nil {
		return nil
	}
	return d.Ambient.Activate(ctx, in.RoomID, in.SpaceID, in.SenderID)
}

// HandleFollowUp answers a message written in a session's room.
func (d *Dispatcher) HandleFollowUp(ctx context.Context, in Inbound, desc Describer, r Responder) Route {
	if d.Conversation.FollowUp(ctx, in, desc, r) {
		return RouteFollowUp
	}
	return RouteNone
}

// HandleAmbient answers a message that does not address the bot when the
// room's ambient session says the bot should keep talking.
func (d *Dispatcher) HandleAmbient(ctx context.Context, in Inbound, desc Describer, r Responder) Route {
	if d.Ambient == nil || in.Addressed {
		return RouteNone
	}
	decision, err := d.Ambient.Decide(ctx, ambientMessage(in, false))
	if err != nil {
		fmt.Printf("ambient: state error: %v\n", err)
		return RouteNone
	}
	if !decision.Reply {
		return RouteNone
	}
	if err := d.ambientConversation(ctx, in, desc, r); err != nil {
		fmt.Printf("ambient: conversation error: %v\n", err)
		r.Say(ctx, modelFailureLine)
	}
	return RouteAmbient
}

func (d *Dispatcher) ambientConversation(ctx context.Context, in Inbound, desc Describer, r Responder) error {
	userMessage := strings.TrimSpace(in.Text)
	if userMessage == "" {
		return nil
	}
	if err := d.Conversation.Respond(ctx, in, userMessage, desc, r); err != nil {
		return err
	}
	return d.Ambient.RecordReply(ctx, in.RoomID)
}

func ambientMessage(in Inbound, botMentioned bool) ambient.Message {
	return ambient.Message{
		ChannelID:     in.RoomID,
		GuildID:       in.SpaceID,
		UserID:        in.SenderID,
		Content:       in.Text,
		BotMentioned:  botMentioned,
		ReferencesBot: in.ReplyTo != nil && in.ReplyTo.FromBot,
	}
}
