package commands

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
)

func TestCombinedMemoryPromptRendersSharedContractFixture(t *testing.T) {
	// Given
	fixtureBytes, err := os.ReadFile("testdata/memory_context_enriched_contract.json")
	if err != nil {
		t.Fatalf("read memory context contract fixture: %v", err)
	}
	var response memory.MemoryContextResponse
	if err := json.Unmarshal(fixtureBytes, &response); err != nil {
		t.Fatalf("decode memory context contract fixture: %v", err)
	}

	// When
	prompt := combinedMemoryPrompt(response)

	// Then
	ordered := []string{
		"Relevant reviewed memory context:",
		"Source: short_term",
		"Short-term continuity:",
		"Source: long_term",
		"Long-term durable knowledge:",
		"Source: entities",
		"Entity enrichment:",
		"Source: preferences",
		"Durable preference:",
		"Source: dedup_notice",
		"Dedup notice:",
		"Source: reasoning",
		"Reasoning recall:",
		"Source: similar_traces",
		"Similar traces:",
	}
	assertPromptOrder(t, prompt, ordered)
	for _, want := range []string{
		"summary=Continue the current memory-upgrade conversation without re-asking for context.",
		"summary=agents-memory is the sole SDK and Neo4j gateway for Bromigos agents.",
		"summary=Validate fixture decoding on both sides before claiming integration coverage.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}
}
