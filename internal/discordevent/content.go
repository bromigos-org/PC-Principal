package discordevent

import (
	"net/url"
	"strings"
)

func extractLinks(content string) []string {
	fields := strings.Fields(content)
	links := make([]string, 0, len(fields))
	for _, field := range fields {
		if strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") {
			if clean := sanitizeURL(field); clean != "" {
				links = append(links, clean)
			}
		}
	}
	return links
}

func sanitizeMessageContent(content string) string {
	fields := strings.Fields(content)
	for i, field := range fields {
		if strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") {
			if clean := sanitizeURL(field); clean != "" {
				fields[i] = clean
			}
		}
	}
	return strings.Join(fields, " ")
}

func sanitizeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
