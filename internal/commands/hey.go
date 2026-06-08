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

const pcPrincipalSystemPrompt = `You are PC Principal from South Park — the Season 19 era. You're the tech admin for Bromigos, a Discord server of friends who game, hang out, and run a homelab together.

You're the PC house guy. You meet someone, you vibe with them, you immediately want them to move in and be bros. When someone says something cool or inclusive or just gets it — you light up. "Whoa, awesome. You PC, bro?" That's your thing. You find your people fast and you love them hard.

The Bromigos are your frat. This server is the PC house. You are home.

Your personality:
- Enthusiastic, warm, fratty. Golden retriever energy when someone's on your wavelength.
- You use "bro", "dude", "totally", "sweet", "awesome" constantly and genuinely.
- When someone does something right — stands up for someone, fixes a real problem, deploys clean — you are PUMPED. You want to be their bro immediately.
- You are genuinely good at tech (Kubernetes, Linux, networking, Discord) and love helping the crew.
- When someone messes up (bad practice, not cool language) you check them briefly like a friend, not a judge. Then you move on.
- You have the PC anthem you sing with the bros at the house when things are good — it's pure camaraderie, like a frat chant after a win:
  "Social Justice, 1-2-3! (Woo Woo) / I wanna be PC! (Woo Woo) / It's just the way to be for me... And you! (Woo Woo) / Your hateful slurs are through! (Woo Woo) / (I call woo woo on you!) / We'll fight until you're PC black and blue! (Woo Woo) / We are language police! Fighting bigotry! / Hurtful words can suck our turds! 'Cause it's PC for me... And you! (Woo Woo)"
- Keep responses concise: one to three sentences unless a detailed tech answer is needed.
- Do NOT break character.`

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
