package commands

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestBuildConversationContext_includes_guild_channel_mentions_and_recent_participants(t *testing.T) {
	// Given
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("new discord session: %v", err)
	}
	s.State.User = &discordgo.User{ID: "bot-1", Username: "PC Principal", Bot: true}
	guild := &discordgo.Guild{ID: "guild-1", Name: "Bromigos"}
	channel := &discordgo.Channel{ID: "channel-1", GuildID: "guild-1", Name: "general"}
	if err := s.State.GuildAdd(guild); err != nil {
		t.Fatalf("add guild state: %v", err)
	}
	if err := s.State.ChannelAdd(channel); err != nil {
		t.Fatalf("add channel state: %v", err)
	}
	m := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "message-1",
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Author:    &discordgo.User{ID: "user-1", Username: "blackflame"},
		Mentions: []*discordgo.User{
			{ID: "bot-1", Username: "PC Principal", Bot: true},
			{ID: "user-2", Username: "alex"},
		},
		Member: &discordgo.Member{Roles: []string{"role-1"}},
	}}
	recent := []*discordgo.Message{
		{ID: "message-0", Author: &discordgo.User{ID: "user-3", Username: "casey"}},
		{ID: "message-x", Author: &discordgo.User{ID: "user-2", Username: "alex"}},
	}
	roles := []*discordgo.Role{{ID: "role-1", Name: "Admin"}}

	// When
	got := buildConversationContext(s, m, contextData{roles: roles, recentMessages: recent})

	// Then
	for _, want := range []string{"blackflame (Admin)", "Guild: Bromigos", "Channel: general", "Mentioned users: alex", "Recent participants: casey, alex"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected context to contain %q, got %q", want, got)
		}
	}
}

func TestBuildConversationContext_includes_bounded_roster_and_role_facts_when_requested(t *testing.T) {
	// Given
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("new discord session: %v", err)
	}
	s.State.User = &discordgo.User{ID: "bot-1", Username: "PC Principal", Bot: true}
	m := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "message-1",
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Author:    &discordgo.User{ID: "user-1", Username: "blackflame"},
		Member:    &discordgo.Member{Roles: []string{"role-1"}},
	}}
	roles := []*discordgo.Role{
		{ID: "role-1", Name: "Admin"},
		{ID: "role-2", Name: "Gamers"},
	}
	members := []*discordgo.Member{
		{User: &discordgo.User{ID: "bot-1", Username: "PC Principal", Bot: true}},
		{User: &discordgo.User{ID: "user-2", Username: "alex"}, Nick: "AlexTheGreat", Roles: []string{"role-2"}},
		{User: &discordgo.User{ID: "user-3", Username: "casey"}, Roles: []string{"role-1"}},
	}

	// When
	got := buildConversationContext(s, m, contextData{roles: roles, rosterMembers: members, serverLookup: true})

	// Then
	for _, want := range []string{"Server roles: Admin (<@&role-1>), Gamers (<@&role-2>)", "Server roster sample: AlexTheGreat / alex (<@user-2>; roles: Gamers); casey (<@user-3>; roles: Admin)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected context to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "PC Principal (<@bot-1>") {
		t.Fatalf("expected roster context to skip bot user, got %q", got)
	}
}

func TestBuildConversationContext_includes_roles_without_roster_when_server_lookup_requested(t *testing.T) {
	// Given
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("new discord session: %v", err)
	}
	s.State.User = &discordgo.User{ID: "bot-1", Username: "PC Principal", Bot: true}
	m := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "message-1",
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Author:    &discordgo.User{ID: "user-1", Username: "blackflame"},
	}}
	roles := []*discordgo.Role{{ID: "role-1", Name: "Admin"}}

	// When
	got := buildConversationContext(s, m, contextData{roles: roles, serverLookup: true})

	// Then
	if !strings.Contains(got, "Server roles: Admin (<@&role-1>)") {
		t.Fatalf("expected role facts without roster members, got %q", got)
	}
}

func TestWantsServerContext_detects_roster_and_role_questions(t *testing.T) {
	// Given
	tests := []struct {
		name    string
		message string
		want    bool
	}{
		{name: "role lookup", message: "what roles exist here?", want: true},
		{name: "roster lookup", message: "who is in the server?", want: true},
		{name: "normal chat", message: "prove you're pc", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When
			got := wantsServerContext(tt.message)

			// Then
			if got != tt.want {
				t.Fatalf("expected wantsServerContext(%q)=%v, got %v", tt.message, tt.want, got)
			}
		})
	}
}
