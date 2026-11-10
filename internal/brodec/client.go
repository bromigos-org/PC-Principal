package brodec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// gatewayTimeout bounds one call to the gateway's local API. The gateway
// answers once its sends have gone out, and a send over a room limit waits
// its turn there, so this is generous.
const gatewayTimeout = 90 * time.Second

// gatewayError is the gateway refusing a call.
type gatewayError struct {
	Status  int
	Message string
}

func (e *gatewayError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("the gateway answered %d", e.Status)
	}
	return fmt.Sprintf("the gateway answered %d: %s", e.Status, e.Message)
}

// refused reports whether the gateway could not carry the actions out, as
// opposed to not being reachable or not letting the bot in.
func refused(err error) bool {
	var gatewayErr *gatewayError
	return errors.As(err, &gatewayErr) && gatewayErr.Status == http.StatusBadRequest
}

// client talks to the gateway's local API: the way to act on a delivery
// after the three seconds the gateway waits for an answer to it.
type client struct {
	baseURL   string
	tokenPath string
	http      *http.Client

	mu    sync.Mutex
	token string
}

func newClient(baseURL string, tokenPath string, httpClient *http.Client) *client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: gatewayTimeout}
	}
	return &client{baseURL: strings.TrimRight(baseURL, "/"), tokenPath: tokenPath, http: httpClient}
}

// bearer returns the local API's token, reading the gateway's token file
// when it is not in hand or when fresh is asked for.
func (c *client) bearer(fresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && !fresh {
		return c.token, nil
	}
	text, err := os.ReadFile(c.tokenPath)
	if err != nil {
		return "", fmt.Errorf("read the gateway's token file: %w", err)
	}
	c.token = strings.TrimSpace(string(text))
	if c.token == "" {
		return "", errors.New("the gateway's token file is empty")
	}
	return c.token, nil
}

// send asks the gateway to carry out actions for a delivery it made.
func (c *client) send(ctx context.Context, d Delivery, actions []Action) error {
	body, err := encodeJSON(localActions{Event: d.raw, Actions: actions})
	if err != nil {
		return fmt.Errorf("encode actions: %w", err)
	}
	err = c.post(ctx, body, false)
	var gatewayErr *gatewayError
	if errors.As(err, &gatewayErr) && gatewayErr.Status == http.StatusUnauthorized {
		// The gateway makes a new token when its data directory is new.
		err = c.post(ctx, body, true)
	}
	return err
}

func (c *client) post(ctx context.Context, body []byte, freshToken bool) error {
	token, err := c.bearer(freshToken)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/actions", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("reach the gateway: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 == 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
		return nil
	}
	return &gatewayError{Status: response.StatusCode, Message: errorMessage(response.Body)}
}

// errorMessage reads the gateway's {"error": "..."} body.
func errorMessage(body io.Reader) string {
	var decoded struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 1<<16)).Decode(&decoded); err != nil {
		return ""
	}
	return decoded.Error
}

// publicKey asks the gateway for its public key. The gateway serves it only
// once it has signed in and the endpoint has passed its first check, so an
// answer means the gateway is up and delivering.
func (c *client) publicKey(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/public-key", nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("reach the gateway: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", &gatewayError{Status: response.StatusCode}
	}
	text, err := io.ReadAll(io.LimitReader(response.Body, 1<<10))
	if err != nil {
		return "", fmt.Errorf("read the gateway's answer: %w", err)
	}
	return strings.TrimSpace(string(text)), nil
}
