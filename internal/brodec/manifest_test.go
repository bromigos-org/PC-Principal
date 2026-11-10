package brodec

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
	"unicode/utf8"
)

// manifestCommand is a command as the developer portal takes it in the
// commands manifest.
type manifestCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Options     []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Type        string `json:"type"`
		Required    bool   `json:"required"`
		Choices     []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"choices"`
	} `json:"options"`
	Where      string `json:"where"`
	AdminsOnly bool   `json:"admins_only"`
}

var commandName = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// brodec/commands.json is pasted into the developer portal by hand. It must
// be something the portal accepts, and it must list the commands the bot
// has: the apps offer what the manifest says, the bot answers what the code
// says.
func TestCommandsManifest_is_valid_and_lists_the_bots_commands(t *testing.T) {
	// Given
	content, err := os.ReadFile("../../brodec/commands.json")
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	var manifest struct {
		Commands []manifestCommand `json:"commands"`
	}
	decoder := json.NewDecoder(bytesReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}
	h := newHarness(t)

	// Then: the platform's rules for a registry
	if len(manifest.Commands) == 0 || len(manifest.Commands) > 100 {
		t.Fatalf("a registry holds 1 to 100 commands, got %d", len(manifest.Commands))
	}
	seen := map[string]bool{}
	for _, command := range manifest.Commands {
		if !commandName.MatchString(command.Name) {
			t.Fatalf("/%s: names are 1 to 32 of a-z, 0-9, _ and -", command.Name)
		}
		if seen[command.Name] {
			t.Fatalf("/%s is there twice", command.Name)
		}
		seen[command.Name] = true
		if n := utf8.RuneCountInString(command.Description); n < 1 || n > 100 {
			t.Fatalf("/%s: the description is 1 to 100 characters, got %d", command.Name, n)
		}
		switch command.Where {
		case "", "everywhere", "groups", "dms":
		default:
			t.Fatalf("/%s: where is everywhere, groups or dms, got %q", command.Name, command.Where)
		}
		if len(command.Options) > 25 {
			t.Fatalf("/%s: at most 25 options", command.Name)
		}
		optionSeen := map[string]bool{}
		optionalSeen := false
		for _, option := range command.Options {
			if !commandName.MatchString(option.Name) || optionSeen[option.Name] {
				t.Fatalf("/%s %s: a bad or repeated option name", command.Name, option.Name)
			}
			optionSeen[option.Name] = true
			switch option.Type {
			case "text", "number", "duration", "choice", "user":
			default:
				t.Fatalf("/%s %s: unknown type %q", command.Name, option.Name, option.Type)
			}
			if n := utf8.RuneCountInString(option.Description); n < 1 || n > 100 {
				t.Fatalf("/%s %s: the description is 1 to 100 characters, got %d", command.Name, option.Name, n)
			}
			if option.Required && optionalSeen {
				t.Fatalf("/%s %s: required options come first", command.Name, option.Name)
			}
			optionalSeen = optionalSeen || !option.Required
			if (option.Type == "choice") != (len(option.Choices) > 0) {
				t.Fatalf("/%s %s: choices go with the choice type, and only with it", command.Name, option.Name)
			}
		}
	}

	// and the same commands, with the same words, as the bot has
	commands := h.bot.commands()
	if len(commands) != len(manifest.Commands) {
		t.Fatalf("the bot has %d commands, the manifest lists %d", len(commands), len(manifest.Commands))
	}
	for i, command := range commands {
		listed := manifest.Commands[i]
		if command.Name != listed.Name || command.Description != listed.Description {
			t.Fatalf("command %d: the bot has /%s (%q), the manifest lists /%s (%q)", i+1, command.Name, command.Description, listed.Name, listed.Description)
		}
		if command.Run == nil {
			t.Fatalf("/%s does nothing", command.Name)
		}
		// The help card shows the description as a field's value.
		if n := utf8.RuneCountInString(command.Description); n > cardFieldValueMax {
			t.Fatalf("/%s: the description is %d characters, more than a card's field holds (%d)", command.Name, n, cardFieldValueMax)
		}
	}
}
