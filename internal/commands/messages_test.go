package commands

import (
	"strings"
	"testing"
)

func TestCommandLookupCaseInsensitive(t *testing.T) {
	register("pctest", "", "", nil)

	cases := []string{"pctest", "PCTEST", "PcTest", "PCTest"}
	for _, input := range cases {
		lower := strings.ToLower(input)
		found := false
		for _, cmd := range registry {
			if cmd.Name == lower {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("command %q not found after ToLower → %q", input, lower)
		}
	}
}
