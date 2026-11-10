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
	// defaultTimeout bounds one request. It is short on purpose: recall and
	// write-back sit on the path of a reply, and a slow gnosis must not hold
	// the reply up.
	defaultTimeout = 5 * time.Second
	// eventBatchTimeoutPerEvent is the extra time an event batch gets for
	// every event after the first. gnosis works through a batch one event at
	// a time, so the time a batch needs grows with its size. One event keeps
	// defaultTimeout.
	eventBatchTimeoutPerEvent = time.Second
	// maxEventBatchTimeout caps the time allowed for one event batch.
	maxEventBatchTimeout = 2 * time.Minute
	defaultTenant        = "bromigos"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

type Config struct {
	Enabled  bool
	BaseURL  string
	Token    string
	TenantID string
}

type Message struct {
	Scope   Scope
	Role    Role
	Content string
}

type Client interface {
	AddMessage(ctx context.Context, message Message) error
	IngestEvent(ctx context.Context, event ClientEvent) error
	IngestEvents(ctx context.Context, events []ClientEvent) (ClientEventBatchResponse, error)
	GetMemoryContext(ctx context.Context, request MemoryContextRequest) (MemoryContextResponse, error)
	GetGraphContext(ctx context.Context, request GraphContextRequest) (GraphContextResponse, error)
	StartReasoningTrace(ctx context.Context, request ReasoningTraceStartRequest) (ReasoningTraceStartResponse, error)
	AddReasoningStep(ctx context.Context, request ReasoningStepRequest) (ReasoningStepResponse, error)
	RecordReasoningToolCall(ctx context.Context, request ReasoningToolCallRequest) (ReasoningToolCallResponse, error)
	CompleteReasoningTrace(ctx context.Context, request ReasoningTraceCompleteRequest) (ReasoningTraceCompleteResponse, error)
	GetReasoningContext(ctx context.Context, request ReasoningContextRequest) (ReasoningContextResponse, error)
	ListSkills(ctx context.Context, request SkillListRequest) (SkillListResponse, error)
	ProposeSkill(ctx context.Context, proposal SkillProposal) (SkillProposal, error)
	RecordSkillUsage(ctx context.Context, usage SkillUsage) error
}

type HTTPClient struct {
	httpClient *http.Client
	baseURL    string
	token      string
	enabled    bool
	// requestTimeout and batchTimeoutPerEvent are fields so tests can shorten
	// them; NewClient sets them from the constants above.
	requestTimeout       time.Duration
	batchTimeoutPerEvent time.Duration
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
		Enabled:  strings.EqualFold(os.Getenv("GNOSIS_ENABLED"), "true"),
		BaseURL:  os.Getenv("GNOSIS_SERVICE_URL"),
		Token:    os.Getenv("GNOSIS_SERVICE_TOKEN"),
		TenantID: getenvDefault("GNOSIS_TENANT_ID", defaultTenant),
	}
}

func NewClient(config Config, httpClient *http.Client) *HTTPClient {
	if httpClient == nil {
		// No client-wide timeout: every request carries its own deadline, and
		// an event batch is allowed longer than the other calls.
		httpClient = &http.Client{}
	}
	return &HTTPClient{
		httpClient:           httpClient,
		baseURL:              strings.TrimRight(config.BaseURL, "/"),
		token:                config.Token,
		enabled:              config.Enabled && config.BaseURL != "" && config.Token != "",
		requestTimeout:       defaultTimeout,
		batchTimeoutPerEvent: eventBatchTimeoutPerEvent,
	}
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
	responseBytes, err := c.post(ctx, "/v1/messages", requestBytes, c.requestTimeout)
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

func (c *HTTPClient) IngestEvent(ctx context.Context, event ClientEvent) error {
	_, err := c.IngestEvents(ctx, []ClientEvent{event})
	return err
}

func (c *HTTPClient) IngestEvents(ctx context.Context, events []ClientEvent) (ClientEventBatchResponse, error) {
	var response ClientEventBatchResponse
	if !c.enabled {
		return response, nil
	}
	if err := c.postJSONWithin(ctx, c.eventBatchTimeout(len(events)), "/v1/events/batch", ClientEventBatchRequest{Events: events}, &response); err != nil {
		return response, fmt.Errorf("ingest memory events: %w", err)
	}
	if hasPartialFailure(response.Results) {
		return response, &Error{Kind: ErrorKindPartialBatch, Operation: "ingest memory events", Results: response.Results}
	}
	return response, nil
}

func (c *HTTPClient) GetGraphContext(ctx context.Context, request GraphContextRequest) (GraphContextResponse, error) {
	var response GraphContextResponse
	if !c.enabled {
		return response, nil
	}
	if err := c.postJSON(ctx, "/v1/graph/context", request, &response); err != nil {
		return response, fmt.Errorf("get memory graph context: %w", err)
	}
	return response, nil
}

func (c *HTTPClient) ListSkills(ctx context.Context, request SkillListRequest) (SkillListResponse, error) {
	var response SkillListResponse
	if !c.enabled {
		return response, nil
	}
	if err := c.postJSON(ctx, "/v1/skills", request, &response); err != nil {
		return response, fmt.Errorf("list memory skills: %w", err)
	}
	return response, nil
}

func (c *HTTPClient) ProposeSkill(ctx context.Context, proposal SkillProposal) (SkillProposal, error) {
	var response SkillProposal
	if !c.enabled {
		return response, nil
	}
	if err := c.postJSON(ctx, "/v1/skills/proposals", proposal, &response); err != nil {
		return response, fmt.Errorf("propose memory skill: %w", err)
	}
	return response, nil
}

func (c *HTTPClient) RecordSkillUsage(ctx context.Context, usage SkillUsage) error {
	if !c.enabled {
		return nil
	}
	var response skillUsageResponse
	if err := c.postJSON(ctx, "/v1/skills/usage", usage, &response); err != nil {
		return fmt.Errorf("record memory skill usage: %w", err)
	}
	if !response.Accepted {
		return &Error{Kind: ErrorKindValidation, Operation: "record memory skill usage"}
	}
	return nil
}

// eventBatchTimeout is the time allowed for one batch of the given size.
func (c *HTTPClient) eventBatchTimeout(events int) time.Duration {
	timeout := c.requestTimeout + time.Duration(max(events-1, 0))*c.batchTimeoutPerEvent
	return min(timeout, max(maxEventBatchTimeout, c.requestTimeout))
}

func (c *HTTPClient) post(ctx context.Context, path string, body []byte, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, classifySendError("send request", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, classifyStatus("gnosis request", resp.StatusCode)
	}
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return respBytes, nil
}

func (c *HTTPClient) postJSON(ctx context.Context, path string, request any, response any) error {
	return c.postJSONWithin(ctx, c.requestTimeout, path, request, response)
}

func (c *HTTPClient) postJSONWithin(ctx context.Context, timeout time.Duration, path string, request any, response any) error {
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	responseBytes, err := c.post(ctx, path, requestBytes, timeout)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(responseBytes, response); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func getenvDefault(name string, fallback string) string {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	return value
}
