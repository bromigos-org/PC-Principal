package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/bromigos-org/pc-principal/internal/store"
)

const (
	defaultLiteLLMModel   = "gemma4"
	defaultRequestTimeout = 30 * time.Second
)

type LiteLLMClient struct {
	httpClient *http.Client
	model      string
}

func NewLiteLLMClient(httpClient *http.Client) *LiteLLMClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultRequestTimeout}
	}
	return &LiteLLMClient{httpClient: httpClient, model: defaultLiteLLMModel}
}

type liteLLMRequest struct {
	Model    string          `json:"model"`
	Messages []store.Message `json:"messages"`
}

type liteLLMResponse struct {
	Choices []struct {
		Message store.Message `json:"message"`
	} `json:"choices"`
}

func (c *LiteLLMClient) Generate(ctx context.Context, messages []store.Message) (string, error) {
	baseURL := os.Getenv("LITELLM_BASE_URL")
	apiKey := os.Getenv("LITELLM_API_KEY")

	body, err := json.Marshal(liteLLMRequest{Model: c.model, Messages: messages})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := c.httpClient.Do(req)
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

	var lr liteLLMResponse
	if err := json.Unmarshal(respBytes, &lr); err != nil {
		return "", err
	}
	if len(lr.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return lr.Choices[0].Message.Content, nil
}
