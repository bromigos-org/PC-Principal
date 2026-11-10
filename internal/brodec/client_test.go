package brodec

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"
)

func TestClient_reads_the_gateways_token_again_when_it_is_refused(t *testing.T) {
	// Given: a bot that started while the token file held an old token
	h := newHarness(t)
	writeFile(t, filepath.Join(h.dataDir, tokenFile), "a-token-from-before\n")
	if _, err := h.bot.client.bearer(false); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.dataDir, tokenFile), testToken+"\n")

	// When
	h.deliver(fixture(t, "delivery/mention.json"))

	// Then: the first try is refused, the second goes through
	if refusedTries := h.gateway.refusedTries(); refusedTries != 1 {
		t.Fatalf("expected one refused try, got %d", refusedTries)
	}
	if len(h.gateway.localCalls()) != 1 {
		t.Fatalf("expected the reply to go through with the new token, got %d calls", len(h.gateway.localCalls()))
	}
}

func TestClient_says_so_when_the_token_file_is_missing(t *testing.T) {
	// Given
	h := newHarness(t, withConfig(func(c *Config) { c.GatewayData = t.TempDir() }))
	delivery, err := parseDelivery(fixture(t, "delivery/mention.json"))
	if err != nil {
		t.Fatal(err)
	}

	// When
	err = h.bot.client.send(context.Background(), delivery, []Action{{Type: actionSay, Text: "hello"}})

	// Then
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(h.gateway.localCalls()) != 0 {
		t.Fatal("nothing must be sent without a token")
	}
}

func TestHealth_reports_the_gateways_state(t *testing.T) {
	// Given: a bot beside a gateway that is up
	h := newHarness(t)

	// When
	beforeAnyDelivery := h.bot.Health(context.Background())
	h.deliver(fixture(t, "delivery/ping.json"))
	afterAPing := h.bot.Health(context.Background())

	// Then
	if !beforeAnyDelivery.Ready || beforeAnyDelivery.Gateway != "ready" || beforeAnyDelivery.Key != "loaded" {
		t.Fatalf("expected a ready gateway, got %+v", beforeAnyDelivery)
	}
	if !beforeAnyDelivery.LastDelivery.IsZero() {
		t.Fatal("expected no delivery yet")
	}
	if time.Since(afterAPing.LastDelivery) > time.Minute {
		t.Fatalf("expected the ping to count as a delivery, got %s", afterAPing.LastDelivery)
	}
}

func TestHealth_reports_a_gateway_that_is_down(t *testing.T) {
	// Given
	h := newHarness(t)
	h.gateway.server.Close()

	// When
	health := h.bot.Health(context.Background())

	// Then
	if health.Ready || health.Gateway != "unreachable" {
		t.Fatalf("expected an unreachable gateway, got %+v", health)
	}
}

func TestHealth_reports_a_key_the_gateway_does_not_sign_with(t *testing.T) {
	// Given: the gateway serves another key than the one in the bot's file
	h := newHarness(t)
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	h.gateway.setKey(base64.RawStdEncoding.EncodeToString(other))

	// When
	health := h.bot.Health(context.Background())

	// Then
	if health.Ready || health.Key != "mismatch" {
		t.Fatalf("expected a key mismatch, got %+v", health)
	}
}

func TestHealth_reports_a_missing_key(t *testing.T) {
	// Given: a gateway that has not written its key file
	h := newHarness(t, withConfig(func(c *Config) { c.GatewayData = t.TempDir() }))

	// When
	health := h.bot.Health(context.Background())

	// Then
	if health.Ready || health.Key != "missing" {
		t.Fatalf("expected a missing key, got %+v", health)
	}
}
