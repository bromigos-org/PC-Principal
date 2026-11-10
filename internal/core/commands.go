package core

import "context"

// Ping is the command that answers "Pong!": a way to see that the bot is
// there without asking the model anything.
func Ping() Command {
	return Command{
		Name:        "ping",
		Description: "Check that I'm awake.",
		Run: func(ctx context.Context, _ Inbound, r Responder) {
			_ = r.Reply(ctx, "Pong!")
		},
	}
}

// HelpCard lists commands on a card, one field each: the name as it is
// typed after prefix ("/" on a platform with typed commands) and what the
// command does. The front end adds whatever else its help shows.
func HelpCard(title string, prefix string, commands []Command) Card {
	card := Card{Title: title}
	for _, cmd := range commands {
		card.Fields = append(card.Fields, CardField{Label: prefix + cmd.Name, Value: cmd.Description})
	}
	return card
}
