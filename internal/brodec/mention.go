package brodec

import (
	"regexp"
	"sort"
	"strings"
)

// mentionStripper cuts the bot's own mention out of a message before the
// message goes to the model. How the apps write a mention into the text is
// not pinned down, so every form the bot could be written in is handled:
// its handle, its name, its Matrix ID, and a link to that ID.
type mentionStripper struct {
	// anywhere are forms that can only be a mention ("@pc_principal", the
	// Matrix ID). They are cut wherever they stand.
	anywhere *regexp.Regexp
	// leading are the bare handle and name. They are cut only at the start
	// of the message, where they address the bot ("PC Principal, hey").
	// Further in they are part of what was said ("who is PC Principal?").
	leading *regexp.Regexp
}

func newMentionStripper(handle string, name string, userID string) mentionStripper {
	var anywhere, leading []string
	for _, word := range []string{handle, name} {
		word = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(word), "@"))
		if word == "" {
			continue
		}
		anywhere = append(anywhere, "@"+regexp.QuoteMeta(word)+`\b`)
		leading = append(leading, regexp.QuoteMeta(word))
	}
	if userID = strings.TrimSpace(userID); userID != "" {
		quoted := regexp.QuoteMeta(userID)
		// A Markdown link to the ID, the bare link, and the ID itself.
		anywhere = append(anywhere,
			`\[[^\]]*\]\(https://matrix\.to/#/`+quoted+`[^)]*\)`,
			`https://matrix\.to/#/`+quoted+`\b`,
			quoted+`\b`,
		)
	}
	// Longer forms first, so that the longest way the bot is written is the
	// one that is cut.
	sort.SliceStable(anywhere, func(i, j int) bool { return len(anywhere[i]) > len(anywhere[j]) })
	sort.SliceStable(leading, func(i, j int) bool { return len(leading[i]) > len(leading[j]) })

	stripper := mentionStripper{}
	if len(anywhere) > 0 {
		stripper.anywhere = regexp.MustCompile(`(?i)(?:` + strings.Join(anywhere, "|") + `)[:,]?`)
	}
	if len(leading) > 0 {
		stripper.leading = regexp.MustCompile(`(?i)^\s*(?:` + strings.Join(leading, "|") + `)(?:[:,]\s*|\s+|$)`)
	}
	return stripper
}

var runsOfBlanks = regexp.MustCompile(`[ \t]{2,}`)

// strip returns what was said without the bot's mention. A message that
// holds no mention comes back as it was written, trimmed.
func (m mentionStripper) strip(body string) string {
	stripped := body
	if m.anywhere != nil {
		stripped = m.anywhere.ReplaceAllString(stripped, " ")
	}
	if m.leading != nil {
		stripped = m.leading.ReplaceAllString(stripped, "")
	}
	if stripped == body {
		return strings.TrimSpace(body)
	}
	return strings.TrimSpace(runsOfBlanks.ReplaceAllString(stripped, " "))
}
