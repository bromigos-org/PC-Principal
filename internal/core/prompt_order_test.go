package core

import (
	"strings"
	"testing"
)

func assertPromptOrder(t *testing.T, prompt string, ordered []string) {
	t.Helper()
	previousIndex := -1
	for _, want := range ordered {
		index := strings.Index(prompt, want)
		if index == -1 {
			t.Fatalf("expected assistant prompt to contain %q, got %q", want, prompt)
		}
		if index <= previousIndex {
			t.Fatalf("expected %q to preserve service ordering in prompt %q", want, prompt)
		}
		previousIndex = index
	}
}
