package commands

import (
	"context"
	"strings"

	"github.com/bromigos-org/pc-principal/internal/ambient"
	"github.com/bwmarrin/discordgo"
)

var ambientManager *ambient.Manager

func ConfigureAmbient(manager *ambient.Manager) {
	ambientManager = manager
}

func handleAmbientConversation(s *discordgo.Session, m *discordgo.MessageCreate) error {
	userMessage := strings.TrimSpace(m.Content)
	if userMessage == "" {
		return nil
	}
	if err := handleConversation(s, conversationRequest{Message: m, UserMessage: userMessage}); err != nil {
		return err
	}
	return recordAmbientReply(m.ChannelID)
}

func recordAmbientActivation(m *discordgo.MessageCreate) error {
	if ambientManager == nil {
		return nil
	}
	return ambientManager.Activate(context.Background(), m.ChannelID, m.GuildID, m.Author.ID)
}

func recordAmbientReply(channelID string) error {
	if ambientManager == nil {
		return nil
	}
	return ambientManager.RecordReply(context.Background(), channelID)
}
