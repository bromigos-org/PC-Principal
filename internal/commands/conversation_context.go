package commands

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

const (
	recentMessageLimit     = 12
	recentParticipantLimit = 6
	rosterMemberLimit      = 25
)

type contextData struct {
	roles          []*discordgo.Role
	recentMessages []*discordgo.Message
	rosterMembers  []*discordgo.Member
	serverLookup   bool
}

func fetchContextData(s *discordgo.Session, m *discordgo.MessageCreate, userMessage string) contextData {
	data := contextData{serverLookup: wantsServerContext(userMessage)}
	if m.GuildID != "" {
		if roles, err := s.GuildRoles(m.GuildID); err == nil {
			data.roles = roles
		}
		if data.serverLookup {
			if members, err := s.GuildMembers(m.GuildID, "", rosterMemberLimit); err == nil {
				data.rosterMembers = members
			}
		}
	}
	if messages, err := s.ChannelMessages(m.ChannelID, recentMessageLimit, m.ID, "", ""); err == nil {
		data.recentMessages = messages
	}
	return data
}

func wantsServerContext(message string) bool {
	text := strings.ToLower(message)
	queries := []string{"who is", "who's", "members", "roster", "server", "guild", "role", "roles"}
	for _, query := range queries {
		if strings.Contains(text, query) {
			return true
		}
	}
	return false
}

func buildConversationContext(s *discordgo.Session, m *discordgo.MessageCreate, data contextData) string {
	parts := []string{memberContextFromRoles(m, data.roles)}
	if guildName := guildName(s, m.GuildID); guildName != "" {
		parts = append(parts, "Guild: "+guildName)
	}
	if channelName := channelName(s, m.ChannelID); channelName != "" {
		parts = append(parts, "Channel: "+channelName)
	}
	if mentioned := mentionedUserNames(m, s.State.User.ID); len(mentioned) > 0 {
		parts = append(parts, "Mentioned users: "+strings.Join(mentioned, ", "))
	}
	if recent := recentParticipantNames(data.recentMessages, s.State.User.ID); len(recent) > 0 {
		parts = append(parts, "Recent participants: "+strings.Join(recent, ", "))
	}
	if roleFacts := roleFacts(data.roles); data.serverLookup && roleFacts != "" {
		parts = append(parts, "Server roles: "+roleFacts)
	}
	if roster := rosterFacts(data.rosterMembers, data.roles, s.State.User.ID); roster != "" {
		parts = append(parts, "Server roster sample: "+roster)
	}
	return strings.Join(parts, "; ")
}

func memberContextFromRoles(m *discordgo.MessageCreate, roles []*discordgo.Role) string {
	username := m.Author.Username
	if m.Member == nil {
		return username
	}
	roleMap := roleNameMap(roles)
	names := make([]string, 0, len(m.Member.Roles))
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

func guildName(s *discordgo.Session, guildID string) string {
	if guildID == "" {
		return ""
	}
	if guild, err := s.State.Guild(guildID); err == nil {
		return guild.Name
	}
	if guild, err := s.Guild(guildID); err == nil {
		return guild.Name
	}
	return ""
}

func channelName(s *discordgo.Session, channelID string) string {
	if channel, err := s.State.Channel(channelID); err == nil {
		return channel.Name
	}
	if channel, err := s.Channel(channelID); err == nil {
		return channel.Name
	}
	return ""
}

func mentionedUserNames(m *discordgo.MessageCreate, botID string) []string {
	names := make([]string, 0, len(m.Mentions))
	for _, user := range m.Mentions {
		if user.ID != botID {
			names = append(names, user.Username)
		}
	}
	return names
}

func recentParticipantNames(messages []*discordgo.Message, botID string) []string {
	seen := map[string]struct{}{}
	names := make([]string, 0, recentParticipantLimit)
	for _, message := range messages {
		if message.Author == nil || message.Author.ID == botID || message.Author.Bot {
			continue
		}
		if _, ok := seen[message.Author.ID]; ok {
			continue
		}
		seen[message.Author.ID] = struct{}{}
		names = append(names, message.Author.Username)
		if len(names) == recentParticipantLimit {
			return names
		}
	}
	return names
}

func roleFacts(roles []*discordgo.Role) string {
	facts := make([]string, 0, len(roles))
	for _, role := range roles {
		facts = append(facts, fmt.Sprintf("%s (<@&%s>)", role.Name, role.ID))
	}
	return strings.Join(facts, ", ")
}

func rosterFacts(members []*discordgo.Member, roles []*discordgo.Role, botID string) string {
	roleMap := roleNameMap(roles)
	facts := make([]string, 0, len(members))
	for _, member := range members {
		if member.User == nil || member.User.ID == botID || member.User.Bot {
			continue
		}
		facts = append(facts, memberFact(member, roleMap))
		if len(facts) == rosterMemberLimit {
			return strings.Join(facts, "; ")
		}
	}
	return strings.Join(facts, "; ")
}

func memberFact(member *discordgo.Member, roleMap map[string]string) string {
	name := member.User.Username
	if member.Nick != "" {
		name = member.Nick + " / " + name
	}
	names := make([]string, 0, len(member.Roles))
	for _, id := range member.Roles {
		if role, ok := roleMap[id]; ok {
			names = append(names, role)
		}
	}
	if len(names) == 0 {
		return fmt.Sprintf("%s (<@%s>)", name, member.User.ID)
	}
	return fmt.Sprintf("%s (<@%s>; roles: %s)", name, member.User.ID, strings.Join(names, ", "))
}

func roleNameMap(roles []*discordgo.Role) map[string]string {
	roleMap := make(map[string]string, len(roles))
	for _, role := range roles {
		roleMap[role.ID] = role.Name
	}
	return roleMap
}
