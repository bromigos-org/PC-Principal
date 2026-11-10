package brodec

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestEndpoint_accepts_the_gateways_ping_and_answers_nothing(t *testing.T) {
	// Given
	h := newHarness(t)

	// When
	actions := h.deliver(fixture(t, "delivery/ping.json"))

	// Then
	if len(actions) != 0 {
		t.Fatalf("expected no actions for a ping, got %v", actions)
	}
	if h.llm.calls() != 0 || len(h.gateway.localCalls()) != 0 {
		t.Fatal("a ping must not reach the model or the gateway's local API")
	}
}

// The gateway sends a request with a bad signature at its start and after
// every 200 deliveries, and stops for good if the endpoint accepts it.
func TestEndpoint_refuses_what_the_gateway_did_not_sign(t *testing.T) {
	_, strangerKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	randomSignature := make([]byte, ed25519.SignatureSize)
	if _, err := rand.Read(randomSignature); err != nil {
		t.Fatal(err)
	}
	body := fixture(t, "delivery/mention.json")

	cases := map[string]func(h *harness, r *http.Request){
		"a random signature, as the gateway's probe sends": func(_ *harness, r *http.Request) {
			r.Header.Set(headerSignature, hex.EncodeToString(randomSignature))
		},
		"a signature by another key": func(_ *harness, r *http.Request) {
			r.Header.Set(headerSignature, sign(strangerKey, r.Header.Get(headerTimestamp), body))
		},
		"a signature for another body": func(h *harness, r *http.Request) {
			r.Header.Set(headerSignature, sign(h.private, r.Header.Get(headerTimestamp), fixture(t, "delivery/ping.json")))
		},
		"a signature for another timestamp": func(h *harness, r *http.Request) {
			r.Header.Set(headerSignature, sign(h.private, "1000", body))
		},
		"a signature that is not hex": func(_ *harness, r *http.Request) {
			r.Header.Set(headerSignature, "zz")
		},
		"no signature header": func(_ *harness, r *http.Request) {
			r.Header.Del(headerSignature)
		},
		"no timestamp header": func(_ *harness, r *http.Request) {
			r.Header.Del(headerTimestamp)
		},
		"a timestamp that is not a number": func(h *harness, r *http.Request) {
			r.Header.Set(headerTimestamp, "now")
			r.Header.Set(headerSignature, sign(h.private, "now", body))
		},
		"a timestamp six minutes old, well signed": func(h *harness, r *http.Request) {
			old := strconv.FormatInt(time.Now().Add(-6*time.Minute).Unix(), 10)
			r.Header.Set(headerTimestamp, old)
			r.Header.Set(headerSignature, sign(h.private, old, body))
		},
		"a timestamp six minutes ahead, well signed": func(h *harness, r *http.Request) {
			ahead := strconv.FormatInt(time.Now().Add(6*time.Minute).Unix(), 10)
			r.Header.Set(headerTimestamp, ahead)
			r.Header.Set(headerSignature, sign(h.private, ahead, body))
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			// Given
			h := newHarness(t)
			request := h.request(body)
			tamper(h, request)

			// When
			status := h.status(request)

			// Then
			if status != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", status)
			}
			if h.llm.calls() != 0 || len(h.gateway.localCalls()) != 0 {
				t.Fatal("a refused delivery must set nothing off")
			}
		})
	}
}

func TestEndpoint_accepts_a_timestamp_inside_the_window(t *testing.T) {
	// Given
	h := newHarness(t)
	body := fixture(t, "delivery/ping.json")
	request := h.request(body)
	fourMinutesAgo := strconv.FormatInt(time.Now().Add(-4*time.Minute).Unix(), 10)
	request.Header.Set(headerTimestamp, fourMinutesAgo)
	request.Header.Set(headerSignature, sign(h.private, fourMinutesAgo, body))

	// When
	status := h.status(request)

	// Then
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
}

func TestEndpoint_takes_only_POST(t *testing.T) {
	// Given
	h := newHarness(t)
	request := h.request(fixture(t, "delivery/ping.json"))
	request.Method = http.MethodGet

	// When
	status := h.status(request)

	// Then
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", status)
	}
}

// The bot may start before the gateway has written its key. Until the key
// is there the endpoint answers 503, which the gateway's first check tries
// again; it must never answer 2xx to something it cannot verify.
func TestEndpoint_waits_for_the_gateways_key_file(t *testing.T) {
	// Given: a bot whose gateway has not written its key yet
	h := newHarness(t, withConfig(func(c *Config) { c.GatewayData = t.TempDir() }))
	body := fixture(t, "delivery/ping.json")

	// When
	before := h.status(h.request(body))
	writeFile(t, filepath.Join(h.config.GatewayData, publicKeyFile), base64.RawStdEncoding.EncodeToString(h.private.Public().(ed25519.PublicKey)))
	after := h.status(h.request(body))

	// Then
	if before != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 while the key is missing, got %d", before)
	}
	if after != http.StatusOK {
		t.Fatalf("expected 200 once the key is there, got %d", after)
	}
}

// A gateway that lost its data directory comes back with a new key and
// writes it to the same file.
func TestEndpoint_follows_a_gateway_that_came_back_with_a_new_key(t *testing.T) {
	// Given: a clock, so that the next read of the key file is not held off
	now := time.Now()
	h := newHarness(t, withClock(func() time.Time { return now }))
	body := fixture(t, "delivery/ping.json")
	if status := h.status(h.request(body)); status != http.StatusOK {
		t.Fatalf("expected 200 with the first key, got %d", status)
	}
	newPublic, newPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	oldPrivate := h.private
	writeFile(t, filepath.Join(h.dataDir, publicKeyFile), base64.StdEncoding.EncodeToString(newPublic))
	h.private = newPrivate
	now = now.Add(2 * keyReloadEvery)

	// When
	withNewKey := h.status(h.request(body))
	h.private = oldPrivate
	withOldKey := h.status(h.request(body))

	// Then
	if withNewKey != http.StatusOK {
		t.Fatalf("expected 200 with the new key, got %d", withNewKey)
	}
	if withOldKey != http.StatusUnauthorized {
		t.Fatalf("expected 401 with the key the gateway no longer has, got %d", withOldKey)
	}
}

func TestEndpoint_takes_the_key_from_the_configuration_over_the_file(t *testing.T) {
	// Given: a key in the configuration, and another one in the file
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, withConfig(func(c *Config) { c.PublicKey = base64.StdEncoding.EncodeToString(public) }))
	body := fixture(t, "delivery/ping.json")
	fileKey := h.private

	// When
	withFileKey := h.status(h.request(body))
	h.private = private
	withConfiguredKey := h.status(h.request(body))
	h.private = fileKey

	// Then
	if withConfiguredKey != http.StatusOK {
		t.Fatalf("expected 200 with the configured key, got %d", withConfiguredKey)
	}
	if withFileKey != http.StatusUnauthorized {
		t.Fatalf("expected 401 with the file's key, got %d", withFileKey)
	}
}

func TestNew_refuses_a_public_key_that_is_not_one(t *testing.T) {
	for name, key := range map[string]string{"not base64": "not a key!", "too short": "c2hvcnQ"} {
		t.Run(name, func(t *testing.T) {
			// Given
			h := newHarness(t)
			config := h.config
			config.PublicKey = key

			// When
			_, err := New(config, Deps{Redis: h.rdb, LLM: h.llm})

			// Then
			if err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
