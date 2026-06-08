package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"
)

var allowedRoles map[string]struct{}

func init() {
	allowedRoles = make(map[string]struct{})
	for _, id := range strings.Split(os.Getenv("ALLOWED_ROLES"), ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			allowedRoles[id] = struct{}{}
		}
	}
}

// hasAllowedRole returns true if the member holds at least one allowed role,
// or if no ALLOWED_ROLES are configured (open to everyone).
func hasAllowedRole(member *discordgo.Member) bool {
	if len(allowedRoles) == 0 {
		return true
	}
	if member == nil {
		return false
	}
	for _, r := range member.Roles {
		if _, ok := allowedRoles[r]; ok {
			return true
		}
	}
	return false
}

// memberContext returns a string like "blackflame (Admin, Moderator)" by
// resolving the member's role IDs to names via the Discord API.
func memberContext(s *discordgo.Session, m *discordgo.MessageCreate) string {
	username := m.Author.Username
	if m.Member == nil || m.GuildID == "" {
		return username
	}

	guildRoles, err := s.GuildRoles(m.GuildID)
	if err != nil {
		return username
	}

	roleMap := make(map[string]string, len(guildRoles))
	for _, r := range guildRoles {
		roleMap[r.ID] = r.Name
	}

	var names []string
	for _, id := range m.Member.Roles {
		if name, ok := roleMap[id]; ok {
			names = append(names, name)
		}
	}

	if len(names) == 0 {
		return username
	}
	return fmt.Sprintf("%s (%s)", username, strings.Join(names, ", "))
}
