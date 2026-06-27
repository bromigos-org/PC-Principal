package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultTimeout = 5 * time.Second
	defaultTenant  = "bromigos"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

type Visibility string

const (
	VisibilityPrivateUser  Visibility = "private_user"
	VisibilityAgentPrivate Visibility = "agent_private"
	VisibilityAgentShared  Visibility = "agent_shared"
	VisibilityChannel      Visibility = "channel"
	VisibilityGuild        Visibility = "guild"
	VisibilityTenant       Visibility = "tenant"
	VisibilityGlobal       Visibility = "global"
)

type Scope struct {
	TenantID   string     `json:"tenant_id"`
	SpaceID    string     `json:"space_id"`
	AgentID    string     `json:"agent_id"`
	SessionID  string     `json:"session_id"`
	UserID     string     `json:"user_id"`
	Visibility Visibility `json:"visibility"`
	GuildID    string     `json:"guild_id,omitempty"`
	ChannelID  string     `json:"channel_id,omitempty"`
}

type Config struct {
	Enabled  bool
	BaseURL  string
	Token    string
	TenantID string
}

type ContextQuery struct {
	Scope Scope
	Query string
	Limit int
}

type Message struct {
	Scope   Scope
	Role    Role
	Content string
}

type Client interface {
	GetContext(ctx context.Context, query ContextQuery) (string, error)
	AddMessage(ctx context.Context, message Message) error
}

type HTTPClient struct {
	httpClient *http.Client
	baseURL    string
	token      string
	enabled    bool
}

type contextRequest struct {
	Scope Scope  `json:"scope"`
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type contextResponse struct {
	Context string `json:"context"`
}

type messageWriteRequest struct {
	Scope   Scope  `json:"scope"`
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

type messageWriteResponse struct {
	Accepted bool `json:"accepted"`
}

func LoadConfigFromEnv() Config {
	return Config{
		Enabled:  strings.EqualFold(os.Getenv("MEMORY_ENABLED"), "true"),
		BaseURL:  os.Getenv("MEMORY_SERVICE_URL"),
		Token:    os.Getenv("MEMORY_SERVICE_TOKEN"),
		TenantID: getenvDefault("MEMORY_TENANT_ID", defaultTenant),
	}
}

func NewClient(config Config, httpClient *http.Client) *HTTPClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &HTTPClient{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(config.BaseURL, "/"),
		token:      config.Token,
		enabled:    config.Enabled && config.BaseURL != "" && config.Token != "",
	}
}

func (c *HTTPClient) GetContext(ctx context.Context, query ContextQuery) (string, error) {
	if !c.enabled {
		return "", nil
	}
	request := contextRequest{Scope: query.Scope, Query: query.Query, Limit: query.Limit}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode context request: %w", err)
	}
	responseBytes, err := c.post(ctx, "/v1/context", requestBytes)
	if err != nil {
		return "", fmt.Errorf("get memory context: %w", err)
	}
	var response contextResponse
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return "", fmt.Errorf("decode context response: %w", err)
	}
	return response.Context, nil
}

func (c *HTTPClient) AddMessage(ctx context.Context, message Message) error {
	if !c.enabled {
		return nil
	}
	request := messageWriteRequest{Scope: message.Scope, Role: message.Role, Content: message.Content}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode message request: %w", err)
	}
	responseBytes, err := c.post(ctx, "/v1/messages", requestBytes)
	if err != nil {
		return fmt.Errorf("write memory message: %w", err)
	}
	var response messageWriteResponse
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return fmt.Errorf("decode message response: %w", err)
	}
	if !response.Accepted {
		return fmt.Errorf("write memory message: not accepted")
	}
	return nil
}

func (c *HTTPClient) post(ctx context.Context, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agents-memory returned %d", resp.StatusCode)
	}
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return respBytes, nil
}

func getenvDefault(name string, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}
