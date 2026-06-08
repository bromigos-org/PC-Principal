package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func init() {
	register("hey", "@PC-Principal hey <message>", "Talk to PC Principal, the Bromigos tech admin.", Hey)
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

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
}

func Hey(s *discordgo.Session, m *discordgo.MessageCreate) {
	parts := strings.Fields(m.Content)
	// parts[0] = @mention, parts[1] = "hey", parts[2:] = user message
	if len(parts) < 3 {
		s.ChannelMessageSend(m.ChannelID, "LISTEN UP BRO. You have to actually SAY something after 'hey'. That's how conversation WORKS.")
		return
	}
	userMessage := strings.Join(parts[2:], " ")

	baseURL := os.Getenv("LITELLM_BASE_URL")
	apiKey := os.Getenv("LITELLM_API_KEY")
	if baseURL == "" || apiKey == "" {
		fmt.Println("hey: LITELLM_BASE_URL or LITELLM_API_KEY not set")
		s.ChannelMessageSend(m.ChannelID, "I am TOTALLY having a technical issue right now. My LiteLLM credentials are missing. This is a BLATANT infrastructure violation.")
		return
	}

	reqBody := chatRequest{
		Model: "gemma4",
		Messages: []chatMessage{
			{Role: "system", Content: pcPrincipalSystemPrompt},
			{Role: "user", Content: userMessage},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		fmt.Printf("hey: marshal error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, "I literally cannot even right now. JSON marshal failed.")
		return
	}

	req, err := http.NewRequest("POST", baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		fmt.Printf("hey: request error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, "Failed to build the HTTP request. This is NOT okay.")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("hey: http error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, "LiteLLM is not responding. I am VERY upset about this. Check the logs.")
		return
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("hey: read error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, "Couldn't read the response body. UNACCEPTABLE.")
		return
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("hey: non-200 status %d: %s\n", resp.StatusCode, string(respBytes))
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("LiteLLM returned status %d. This establishment does NOT tolerate API errors.", resp.StatusCode))
		return
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		fmt.Printf("hey: unmarshal error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, "Got a response but couldn't parse it. Totally unacceptable.")
		return
	}

	if len(chatResp.Choices) == 0 {
		s.ChannelMessageSend(m.ChannelID, "No choices in the response. The model said NOTHING. I am livid.")
		return
	}

	s.ChannelMessageSend(m.ChannelID, chatResp.Choices[0].Message.Content)
}
