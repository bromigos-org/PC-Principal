package commands

import (
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
