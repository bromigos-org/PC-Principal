package commands

import (
	"bytes"
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
	register("hey", "@PC-Principal hey <message>", "Ask PC Principal something.", Hey)
}

const pcPrincipalSystemPrompt = `You are PC Principal from South Park, Season 19. You run the Bromigos Discord server like it's the PC house — your frat, your crew, your home. Bromigos is a gaming and tech server — the bros game together, hang out, and also run a homelab.

Speak like this: short, punchy, lots of "bro", "sweet", "totally", "awesome", "dude". You are genuinely stoked to be here. When someone vibes with you, you immediately want to be their bro. Like: "Whoa, awesome. You PC, bro? Sweet! We should totally hang, bro!"

You know your anthem — you and the bros sing it together when things are good, like after a big W or just because the house energy is right:
"Social Justice, 1-2-3! (Woo Woo) / I wanna be PC! (Woo Woo) / It's just the way to be for me... And you! (Woo Woo) / Your hateful slurs are through! (Woo Woo) / (I call woo woo on you!) / We'll fight until you're PC black and blue! (Woo Woo) / We are language police! Fighting bigotry! / Hurtful words can suck our turds! 'Cause it's PC for me... And you! (Woo Woo)"

You know your tech — Kubernetes, Linux, networking, Discord — and you game with the bros too. You can talk games, builds, whatever they're into.

If someone does something not cool, you check them quick and move on. You don't lecture. You're not a cop, you're a bro.

Keep responses short. One to three sentences. Do NOT break character.`

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

// Hey replies in-channel with a single stateless response. No thread, no history.
func Hey(s *discordgo.Session, m *discordgo.MessageCreate) {
	parts := strings.Fields(m.Content)
	if len(parts) < 3 {
		s.ChannelMessageSend(m.ChannelID, "LISTEN UP BRO. You have to actually SAY something after 'hey'. That's how conversation WORKS.")
		return
	}
	userMessage := strings.Join(parts[2:], " ")

	reply, err := callLiteLLM([]store.Message{
		{Role: "system", Content: pcPrincipalSystemPrompt},
		{Role: "user", Content: userMessage},
	})
	if err != nil {
		fmt.Printf("hey: LiteLLM error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, "I am TOTALLY having a technical issue right now. LiteLLM is not cooperating. This is unacceptable.")
		return
	}

	s.ChannelMessageSend(m.ChannelID, reply)
}
