package memory

type Visibility string

const (
	VisibilityPrivateUser  Visibility = "private_user"
	VisibilityAgentPrivate Visibility = "agent_private"
	VisibilityAgentShared  Visibility = "agent_shared"
	VisibilityChannel      Visibility = "channel"
	VisibilityGuild        Visibility = "guild"
	VisibilityTenant       Visibility = "tenant"
	VisibilityGlobal       Visibility = "global"
)

type SourceClient string

const (
	SourceClientDiscord SourceClient = "discord"
)

type EventType string

const (
	EventTypeMessageCreated       EventType = "message_created"
	EventTypeMessageUpdated       EventType = "message_updated"
	EventTypeMessageDeleted       EventType = "message_deleted"
	EventTypeReactionAdded        EventType = "reaction_added"
	EventTypeReactionRemoved      EventType = "reaction_removed"
	EventTypeChannelCreated       EventType = "channel_created"
	EventTypeChannelUpdated       EventType = "channel_updated"
	EventTypeChannelDeleted       EventType = "channel_deleted"
	EventTypeThreadCreated        EventType = "thread_created"
	EventTypeThreadUpdated        EventType = "thread_updated"
	EventTypeThreadDeleted        EventType = "thread_deleted"
	EventTypeRoleCreated          EventType = "role_created"
	EventTypeRoleUpdated          EventType = "role_updated"
	EventTypeRoleDeleted          EventType = "role_deleted"
	EventTypeMemberUpdated        EventType = "member_updated"
	EventTypeAttachmentDiscovered EventType = "attachment_discovered"
	EventTypeLinkDiscovered       EventType = "link_discovered"
	EventTypeTopicUpdated         EventType = "topic_updated"
	EventTypeSkillProposed        EventType = "skill_proposed"
	EventTypeSkillApproved        EventType = "skill_approved"
	EventTypeSkillUsed            EventType = "skill_used"
)

type EventIngestStatus string

const (
	EventIngestStatusAccepted  EventIngestStatus = "accepted"
	EventIngestStatusDuplicate EventIngestStatus = "duplicate"
	EventIngestStatusRejected  EventIngestStatus = "rejected"
	EventIngestStatusFailed    EventIngestStatus = "failed"
)

type SkillStatus string

const (
	SkillStatusProposed SkillStatus = "proposed"
	SkillStatusApproved SkillStatus = "approved"
	SkillStatusRejected SkillStatus = "rejected"
	SkillStatusDisabled SkillStatus = "disabled"
)

type Scope struct {
	TenantID   string     `json:"tenant_id"`
	SpaceID    string     `json:"space_id"`
	AgentID    string     `json:"agent_id"`
	SessionID  string     `json:"session_id"`
	UserID     string     `json:"user_id"`
	Visibility Visibility `json:"visibility"`
	GuildID    string     `json:"guild_id,omitempty"`
	ChannelID  string     `json:"channel_id,omitempty"`
}

type JsonValue interface{}

type JsonObject map[string]JsonValue

type ClientEventActor struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	IsBot       bool   `json:"is_bot"`
}

type ClientEventSubject struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	ParentID string `json:"parent_id,omitempty"`
}

type DiscordEventContext struct {
	GuildID   string `json:"guild_id,omitempty"`
	ChannelID string `json:"channel_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
}

type ClientEvent struct {
	TenantID       string              `json:"tenant_id"`
	SourceClient   SourceClient        `json:"source_client"`
	AgentID        string              `json:"agent_id"`
	EventID        string              `json:"event_id"`
	EventType      EventType           `json:"event_type"`
	OccurredAt     string              `json:"occurred_at"`
	ObservedAt     string              `json:"observed_at"`
	IdempotencyKey string              `json:"idempotency_key"`
	Scope          Scope               `json:"scope"`
	Actor          ClientEventActor    `json:"actor"`
	Subject        ClientEventSubject  `json:"subject"`
	Payload        JsonObject          `json:"payload"`
	Discord        DiscordEventContext `json:"discord,omitempty"`
}

type ClientEventBatchRequest struct {
	Events []ClientEvent `json:"events"`
}

type EventIngestResult struct {
	EventID string            `json:"event_id"`
	Status  EventIngestStatus `json:"status"`
	Reason  string            `json:"reason,omitempty"`
}

type ClientEventBatchResponse struct {
	Results []EventIngestResult `json:"results"`
}

type GraphContextRequest struct {
	Scope           Scope  `json:"scope"`
	Query           string `json:"query"`
	Limit           int    `json:"limit"`
	IncludeTopology bool   `json:"include_topology"`
	IncludeSkills   bool   `json:"include_skills"`
}

type GraphContextResponse struct {
	Context string       `json:"context"`
	Facts   []JsonObject `json:"facts"`
}

type SkillRecord struct {
	SkillID     string      `json:"skill_id"`
	TenantID    string      `json:"tenant_id"`
	AgentID     string      `json:"agent_id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Status      SkillStatus `json:"status"`
	Scope       Visibility  `json:"scope"`
	Metadata    JsonObject  `json:"metadata"`
}

type SkillListRequest struct {
	TenantID string `json:"tenant_id"`
	AgentID  string `json:"agent_id"`
}

type SkillListResponse struct {
	Skills []SkillRecord `json:"skills"`
}

type SkillProposal struct {
	ProposalID  string     `json:"proposal_id"`
	TenantID    string     `json:"tenant_id"`
	AgentID     string     `json:"agent_id"`
	ProposedBy  string     `json:"proposed_by"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Scope       Visibility `json:"scope"`
	Metadata    JsonObject `json:"metadata"`
}

type SkillUsage struct {
	SkillID  string     `json:"skill_id"`
	TenantID string     `json:"tenant_id"`
	AgentID  string     `json:"agent_id"`
	UsedBy   string     `json:"used_by"`
	UsedAt   string     `json:"used_at"`
	Scope    Visibility `json:"scope"`
	Metadata JsonObject `json:"metadata"`
}

type skillUsageResponse struct {
	Accepted bool `json:"accepted"`
}
