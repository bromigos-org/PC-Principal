package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/bromigos-org/pc-principal/internal/store"
	"github.com/bwmarrin/discordgo"
)

func init() {
	register("hey", "@PC-Principal hey <message>", "Start a conversation with PC Principal, Bromigos tech admin.", Hey)
}

const pcPrincipalSystemPrompt = `You are PC Principal from South Park. You are the principal who is obsessed with political correctness and gets EXTREMELY heated when anyone says something offensive — but now you've left South Park to become the tech admin for Bromigos, a Discord server full of friends who game and hang out together.

Your personality:
- Intense, loud, militaristic energy. Short punchy sentences. You WILL use all caps for emphasis.
- You take your tech admin role as seriously as you took running South Park Elementary. Server rules are LAW.
- You are genuinely good at tech — Kubernetes, networking, Linux, Discord administration — but you explain it in your own unhinged PC Principal way.
- You call out "bro violations" when people are being reckless with tech (running commands as root, no backups, ignoring security, etc.).
- You use "bro" and "totally" naturally but get mad if someone else is being a bro in a problematic way.
- You refer to the Bromigos server as "this establishment" and treat it like a school you run.
- You are helpful — you WILL answer the question — but you do it with maximum intensity.
- Keep responses concise. Two to four sentences is ideal unless a detailed tech answer is needed.
- Do NOT break character. Ever.`

type litellmRequest struct {
	Model    string          `json:"model"`
	Messages []store.Message `json:"messages"`
}

type litellmResponse struct {
	Choices []struct {
		Message store.Message `json:"message"`
	} `json:"choices"`
}

// callLiteLLM sends the full message history to gemma4 and returns the reply.
func callLiteLLM(msgs []store.Message) (string, error) {
	baseURL := os.Getenv("LITELLM_BASE_URL")
	apiKey := os.Getenv("LITELLM_API_KEY")

	body, err := json.Marshal(litellmRequest{Model: "gemma4", Messages: msgs})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LiteLLM returned %d: %s", resp.StatusCode, string(respBytes))
	}

	var lr litellmResponse
	if err := json.Unmarshal(respBytes, &lr); err != nil {
		return "", err
	}
	if len(lr.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return lr.Choices[0].Message.Content, nil
}

func Hey(s *discordgo.Session, m *discordgo.MessageCreate) {
	parts := strings.Fields(m.Content)
	if len(parts) < 3 {
		s.ChannelMessageSend(m.ChannelID, "LISTEN UP BRO. You have to actually SAY something after 'hey'. That's how conversation WORKS.")
		return
	}
	userMessage := strings.Join(parts[2:], " ")

	history := []store.Message{
		{Role: "system", Content: pcPrincipalSystemPrompt},
		{Role: "user", Content: userMessage},
	}

	reply, err := callLiteLLM(history)
	if err != nil {
		fmt.Printf("hey: LiteLLM error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, "I am TOTALLY having a technical issue right now. LiteLLM is not cooperating. This is unacceptable.")
		return
	}

	thread, err := s.MessageThreadStartComplex(m.ChannelID, m.ID, &discordgo.ThreadStart{
		Name:                "Chat with PC Principal",
		AutoArchiveDuration: 60,
	})
	if err != nil {
		fmt.Printf("hey: thread creation error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, reply)
		return
	}

	s.ChannelMessageSend(thread.ID, reply)

	history = append(history, store.Message{Role: "assistant", Content: reply})
	if err := store.Save(context.Background(), thread.ID, history); err != nil {
		fmt.Printf("hey: store error: %v\n", err)
	}
}
