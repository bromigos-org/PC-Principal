package commands

import (
	"context"
	"fmt"

	"github.com/bromigos-org/pc-principal/internal/store"
	"github.com/bwmarrin/discordgo"
)

// HeyThreadHandler continues a PC Principal conversation inside a tracked thread.
// Every message in a thread seeded by the hey command gets a response.
func HeyThreadHandler(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	if !hasAllowedRole(m.Member) {
		return
	}

	ctx := context.Background()

	exists, err := store.Exists(ctx, m.ChannelID)
	if err != nil {
		fmt.Printf("hey_thread: store error: %v\n", err)
		return
	}
	if !exists {
		return
	}

	history, err := store.Get(ctx, m.ChannelID)
	if err != nil {
		fmt.Printf("hey_thread: get history error: %v\n", err)
		return
	}

	history = append(history, store.Message{Role: "user", Content: m.Content})

	reply, err := callLiteLLM(history)
	if err != nil {
		fmt.Printf("hey_thread: LiteLLM error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, "LiteLLM is DOWN right now. I am VERY upset. Someone fix the infrastructure.")
		return
	}

	history = append(history, store.Message{Role: "assistant", Content: reply})

	if err := store.Save(ctx, m.ChannelID, history); err != nil {
		fmt.Printf("hey_thread: save error: %v\n", err)
	}

	s.ChannelMessageSend(m.ChannelID, reply)
}
