package run

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

func TestIngestStartupSnapshot_fetches_rest_topology_when_ready_guild_is_partial(t *testing.T) {
	// Given
	memoryClient := &fakeLiveMemoryClient{}
	configureLiveMessageIngestion(memoryClient, "tenant-1")
	t.Cleanup(func() { configureLiveMessageIngestion(memory.NewClient(memory.Config{}, nil), "bromigos") })
	s := runSession(t)
	s.Client = startupSnapshotRESTClient(t)
	readyGuild := &discordgo.Guild{
		ID: "guild-1",
		Channels: []*discordgo.Channel{{
			ID:      "stale-channel-1",
			GuildID: "guild-1",
			Name:    "stale-ready-channel",
			Type:    discordgo.ChannelTypeGuildText,
		}},
		Roles: []*discordgo.Role{{ID: "role-old", Name: "stale ready role"}},
		Members: []*discordgo.Member{{
			GuildID: "guild-1",
			User:    &discordgo.User{ID: "user-101", Username: "stale-human", Bot: false},
			Roles:   nil,
		}},
	}

	// When
	ingestStartupSnapshot(s, &discordgo.Ready{Guilds: []*discordgo.Guild{readyGuild}})

	// Then
	categoryEvent := eventByType(memoryClient.events, memory.EventTypeChannelCreated)
	if categoryEvent.Subject.ID != "category-1" || categoryEvent.Payload["category_name"] != "School Board" {
		t.Fatalf("expected authoritative category event from REST, got %#v", categoryEvent)
	}
	channelEvent := channelEventByID(memoryClient.events, "channel-1")
	if channelEvent.Payload["name"] != "announcements" || channelEvent.Payload["parent_id"] != "category-1" || channelEvent.Payload["category_id"] != "category-1" {
		t.Fatalf("expected authoritative child channel/category metadata from REST, got %#v", channelEvent)
	}
	roleEvent := eventByType(memoryClient.events, memory.EventTypeRoleCreated)
	if roleEvent.Subject.ID != "role-current-1" || roleEvent.Payload["name"] != "Project Lead" {
		t.Fatalf("expected authoritative role name from REST, got %#v", roleEvent)
	}
	memberEvent := memberEventByID(memoryClient.events, "user-101")
	if memberEvent.Subject.ID != "user-101" {
		t.Fatalf("expected paginated REST member event, got %#v", memberEvent)
	}
	roles, ok := memberEvent.Payload["roles"].([]string)
	if !ok || len(roles) != 2 || roles[0] != "role-current-1" || roles[1] != "role-current-2" {
		t.Fatalf("expected full authoritative member role snapshot, got %#v", memberEvent.Payload["roles"])
	}
	userEvent := userEventByID(memoryClient.events, "user-101")
	if userEvent.Subject.Type != "bot" || userEvent.Payload["is_bot"] != true || userEvent.Payload["user_type"] != "bot" {
		t.Fatalf("expected REST bot metadata to override stale Ready human, got %#v", userEvent)
	}
	emptyRolesEvent := memberEventByID(memoryClient.events, "user-102")
	emptyRoles, ok := emptyRolesEvent.Payload["roles"].([]string)
	if !ok || len(emptyRoles) != 0 {
		t.Fatalf("expected REST empty role snapshot to be emitted, got %#v", emptyRolesEvent.Payload["roles"])
	}
}

func channelEventByID(events []memory.ClientEvent, channelID string) memory.ClientEvent {
	for _, event := range events {
		if event.EventType == memory.EventTypeChannelCreated && event.Subject.ID == channelID {
			return event
		}
	}
	return memory.ClientEvent{}
}

func memberEventByID(events []memory.ClientEvent, memberID string) memory.ClientEvent {
	for _, event := range events {
		if event.EventType == memory.EventTypeMemberUpdated && event.Subject.ID == memberID {
			return event
		}
	}
	return memory.ClientEvent{}
}

func userEventByID(events []memory.ClientEvent, userID string) memory.ClientEvent {
	for _, event := range events {
		if event.EventType == memory.EventTypeUserDiscovered && event.Subject.ID == userID {
			return event
		}
	}
	return memory.ClientEvent{}
}

func startupSnapshotRESTClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: runRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("unexpected Discord REST method %s %s", req.Method, req.URL.String())
		}
		body := startupSnapshotRESTBody(t, req.URL.Path, req.URL.Query().Get("after"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
}

func startupSnapshotRESTBody(t *testing.T, path string, afterID string) string {
	t.Helper()
	switch path {
	case "/api/v9/guilds/guild-1/channels":
		return encodeJSON(t, []*discordgo.Channel{
			{ID: "category-1", GuildID: "guild-1", Name: "School Board", Type: discordgo.ChannelTypeGuildCategory},
			{ID: "channel-1", GuildID: "guild-1", Name: "announcements", ParentID: "category-1", Type: discordgo.ChannelTypeGuildText},
		})
	case "/api/v9/guilds/guild-1/threads/active":
		return encodeJSON(t, discordgo.ThreadsList{})
	case "/api/v9/guilds/guild-1/roles":
		return encodeJSON(t, []*discordgo.Role{
			{ID: "role-current-1", Name: "Project Lead", Position: 2},
			{ID: "role-current-2", Name: "Incident Commander", Position: 3},
		})
	case "/api/v9/guilds/guild-1/members":
		return startupSnapshotMembersPage(t, afterID)
	default:
		t.Fatalf("unexpected Discord REST path %s", path)
		return "null"
	}
}

func startupSnapshotMembersPage(t *testing.T, afterID string) string {
	t.Helper()
	switch afterID {
	case "":
		members := make([]*discordgo.Member, startupSnapshotPageLimit)
		for i := range members {
			members[i] = &discordgo.Member{User: &discordgo.User{ID: "user-100", Username: "first"}, Roles: []string{"role-current-1"}}
		}
		return encodeJSON(t, members)
	case "user-100":
		return encodeJSON(t, []*discordgo.Member{
			{User: &discordgo.User{ID: "user-101", Username: "alex", Bot: true}, Roles: []string{"role-current-1", "role-current-2"}},
			{User: &discordgo.User{ID: "user-102", Username: "casey"}, Roles: []string{}},
		})
	case "user-102":
		return encodeJSON(t, []*discordgo.Member{})
	default:
		t.Fatalf("unexpected members page after=%s", afterID)
		return "null"
	}
}

func encodeJSON(t *testing.T, value interface{}) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode Discord REST body: %v", err)
	}
	return string(body)
}
