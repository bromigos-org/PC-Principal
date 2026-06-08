package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/bromigos-org/pc-principal/internal/store"
	"github.com/bwmarrin/discordgo"
)

func init() {
	register("chat", "@PC-Principal chat <topic>", "Start a persistent threaded conversation with PC Principal on a topic.", Chat)
}

// Chat creates a Discord thread named after the topic and begins a persistent
// multi-turn conversation backed by DragonflyDB.
func Chat(s *discordgo.Session, m *discordgo.MessageCreate) {
	parts := strings.Fields(m.Content)
	if len(parts) < 3 {
		s.ChannelMessageSend(m.ChannelID, "BRO. You need to give me a TOPIC. Example: `@PC-Principal chat kubernetes`")
		return
	}
	topic := strings.Join(parts[2:], " ")

	thread, err := s.MessageThreadStartComplex(m.ChannelID, m.ID, &discordgo.ThreadStart{
		Name:                topic,
		AutoArchiveDuration: 60,
	})
	if err != nil {
		fmt.Printf("chat: thread creation error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, "I CANNOT create a thread right now. This is a BLATANT infrastructure failure.")
		return
	}

	history := []store.Message{
		{Role: "system", Content: pcPrincipalSystemPrompt},
		{Role: "user", Content: topic},
	}

	reply, err := callLiteLLM(history)
	if err != nil {
		fmt.Printf("chat: LiteLLM error: %v\n", err)
		s.ChannelMessageSend(thread.ID, "LiteLLM is NOT responding. I am livid. Check the logs.")
		return
	}

	s.ChannelMessageSend(thread.ID, reply)

	history = append(history, store.Message{Role: "assistant", Content: reply})
	if err := store.Save(context.Background(), thread.ID, history); err != nil {
		fmt.Printf("chat: store error: %v\n", err)
	}
}
