package core

import (
	"strings"
	"unicode/utf8"
)

// BlankReply is what the bot says when the model answered with nothing.
const BlankReply = "Bro, my brain totally blanked. Try that again."

// SplitReply fits an answer of any length to a platform that takes at most
// limit bytes in one message: it trims the answer and cuts it into pieces of
// at most limit bytes, never inside a character. A blank answer becomes
// BlankReply.
func SplitReply(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{BlankReply}
	}
	if len(text) <= limit {
		return []string{text}
	}

	chunks := make([]string, 0, len(text)/limit+1)
	for len(text) > 0 {
		cut := chunkSize(text, limit)
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	return chunks
}

// chunkSize is how many bytes of text the next piece takes: limit, or fewer
// when limit falls inside a character.
func chunkSize(text string, limit int) int {
	if len(text) <= limit {
		return len(text)
	}
	cut := limit
	for cut > 0 && !utf8.ValidString(text[:cut]) {
		cut--
	}
	if cut == 0 {
		// The text is not UTF-8 at all; cut it where the limit says.
		return limit
	}
	return cut
}
