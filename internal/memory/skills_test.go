package memory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClientGraphAndSkillAPIs(t *testing.T) {
	// Given
	seenPaths := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPaths[r.URL.Path] = true
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/graph/context":
			_, _ = w.Write([]byte(`{"context":"graph facts","facts":[{"kind":"channel"}]}`))
		case "/v1/skills":
			_, _ = w.Write([]byte(`{"skills":[{"skill_id":"skill-1","tenant_id":"bromigos","agent_id":"pc-principal","name":"Summarize","description":"Summarize channels","status":"approved","scope":"agent_shared","metadata":{"reviewed":true}}]}`))
		case "/v1/skills/proposals":
			_, _ = w.Write([]byte(`{"proposal_id":"proposal-1","tenant_id":"bromigos","agent_id":"pc-principal","proposed_by":"user-1","name":"Summarize","description":"Summarize channels","scope":"agent_shared","metadata":{}}`))
		case "/v1/skills/usage":
			_, _ = w.Write([]byte(`{"accepted":true}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, Token: "test-token"}, server.Client())

	// When
	graph, graphErr := client.GetGraphContext(context.Background(), GraphContextRequest{Scope: testScope(), Query: "channel", Limit: 3})
	skills, skillsErr := client.ListSkills(context.Background(), SkillListRequest{TenantID: "bromigos", AgentID: "pc-principal"})
	proposal, proposalErr := client.ProposeSkill(context.Background(), testSkillProposal())
	usageErr := client.RecordSkillUsage(context.Background(), testSkillUsage())

	// Then
	if graphErr != nil || skillsErr != nil || proposalErr != nil || usageErr != nil {
		t.Fatalf("expected graph and skill APIs to succeed, got graph=%v skills=%v proposal=%v usage=%v", graphErr, skillsErr, proposalErr, usageErr)
	}
	if graph.Context != "graph facts" || len(graph.Facts) != 1 {
		t.Fatalf("expected graph context response, got %#v", graph)
	}
	if len(skills.Skills) != 1 || skills.Skills[0].Status != SkillStatusApproved {
		t.Fatalf("expected approved skill, got %#v", skills)
	}
	if proposal.ProposalID != "proposal-1" {
		t.Fatalf("expected proposal response, got %#v", proposal)
	}
	for _, path := range []string{"/v1/graph/context", "/v1/skills", "/v1/skills/proposals", "/v1/skills/usage"} {
		if !seenPaths[path] {
			t.Fatalf("expected path %s to be called", path)
		}
	}
}

func testClientEvent() ClientEvent {
	return ClientEvent{
		TenantID:       "bromigos",
		SourceClient:   SourceClientDiscord,
		AgentID:        "pc-principal",
		EventID:        "event-1",
		EventType:      EventTypeMessageCreated,
		OccurredAt:     "2026-06-27T00:00:00Z",
		ObservedAt:     "2026-06-27T00:00:01Z",
		IdempotencyKey: "discord:message:event-1",
		Scope:          testScope(),
		Actor: ClientEventActor{
			ID:          "user-1",
			DisplayName: "Blackflame",
			IsBot:       false,
		},
		Subject: ClientEventSubject{ID: "message-1", Type: "message"},
		Payload: JsonObject{"content": "Howdy"},
		Discord: DiscordEventContext{
			GuildID:   "guild-1",
			ChannelID: "channel-1",
			MessageID: "message-1",
		},
	}
}

func testSkillProposal() SkillProposal {
	return SkillProposal{
		ProposalID:  "proposal-1",
		TenantID:    "bromigos",
		AgentID:     "pc-principal",
		ProposedBy:  "user-1",
		Name:        "Summarize",
		Description: "Summarize channels",
		Scope:       VisibilityAgentShared,
		Metadata:    JsonObject{"source": "test"},
	}
}

func testSkillUsage() SkillUsage {
	return SkillUsage{
		SkillID:  "skill-1",
		TenantID: "bromigos",
		AgentID:  "pc-principal",
		UsedBy:   "user-1",
		UsedAt:   "2026-06-27T00:00:00Z",
		Scope:    VisibilityAgentShared,
		Metadata: JsonObject{"outcome": "ok"},
	}
}
