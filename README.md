# PC-Principal

PC-Principal is the Bromigos Discord bot. It handles commands, mention and thread conversations, moderation-adjacent helpers, and memory-aware prompting while talking to `agents-memory` over HTTP.

It is intentionally not a Neo4j client and not a Python SDK client.

## What it does

- Runs the Discord bot surface for Bromigos communities.
- Builds conversation prompts with local short-term history plus reviewed memory sections from `agents-memory`.
- Writes user and assistant turns back to the memory gateway when memory is enabled.
- Emits structured Discord events for ingestion, including messages, reactions, topology changes, members, roles, links, and attachments.
- Records reviewed skill context and reasoning trace lifecycle data through the gateway.
- Falls back safely when the combined memory endpoint or optional dependencies are unavailable.

## Architecture

```mermaid
flowchart LR
    Discord[Discord Gateway and APIs]
    Bot[PC-Principal]
    Dragonfly[(DragonflyDB)]
    LiteLLM[LiteLLM]
    Memory[agents-memory HTTP API]
    Neo4j[(Neo4j, behind agents-memory)]
    Vault[Vault and External Secrets]

    Discord --> Bot
    Bot --> Dragonfly
    Bot --> LiteLLM
    Bot --> Memory
    Memory --> Neo4j
    Vault --> Bot
    Vault --> Memory
```

## Integration boundary

- PC-Principal is an HTTP-only client of `agents-memory`.
- It does not connect to Neo4j, Bolt, or `neo4j-agent-memory` directly.
- Memory type routing stays service-owned. The bot reads what the gateway returns and does not invent its own scope policy.
- Combined memory sections prefer `memory_type` labels and fall back to legacy `source` labels only when needed.

## Conversation memory behavior

When `MEMORY_ENABLED=true`, the conversation flow asks `agents-memory` for combined context through `POST /v1/memory/context`.

- If combined recall succeeds, the bot renders service-ordered sections under `Relevant reviewed memory context:`.
- If the returned sections do not include `short_term`, the bot can still prepend bounded local short-term history from Dragonfly or in-memory history.
- If combined recall fails, the bot falls back to legacy `POST /v1/context` and `POST /v1/graph/context`.
- If the memory service is unavailable, conversation handling degrades instead of crashing the bot.

## Prompt assembly flow

```mermaid
sequenceDiagram
    participant U as Discord user
    participant B as PC-Principal
    participant D as Dragonfly
    participant M as agents-memory
    participant L as LiteLLM

    U->>B: Mention or thread message
    B->>D: Read bounded short-term history
    B->>M: POST /v1/memory/context
    alt Combined context succeeds
        M-->>B: sections with memory_type and facts
    else Combined context fails
        B->>M: POST /v1/context
        B->>M: POST /v1/graph/context
        M-->>B: legacy short-term and graph context
    end
    B->>L: Build final prompt with memory and skills
    L-->>B: Assistant reply
    B->>M: Write user and assistant turns, reasoning trace data, optional events
    B-->>U: Discord response
```

## Memory-aware prompting

The bot treats the combined memory response as labeled prompt sections.

- `short_term` covers recent continuity.
- `long_term` covers durable facts, preferences, entities, and graph-backed recall.
- `reasoning` covers prior successful tool-use summaries.
- `facts` attached to a section are rendered as deterministic `key=value` lines.
- Reviewed skills are rendered as a separate system section, not mixed into memory recall.

Reasoning traces are lifecycle records, not hidden prompt dumps. The bot records start, step, tool-call, and completion events through gateway endpoints, while the gateway keeps chain-of-thought style content out of prompt-safe recall.

## Discord ingestion posture

- Mention replies and thread chat are the visible conversation surface.
- Live ingestion is wider than the reply surface. The bot can send message, reaction, channel, thread, role, member, attachment, link, and topic events to `agents-memory`.
- Attachment handling is metadata-first by default.
- Attachment byte copying is optional and stays off unless explicitly enabled.
- Ambient replies are guarded and disabled unless configured.

## Configuration

### Required secrets

- `DISCORD_BOT_TOKEN`
- `LITELLM_API_KEY`
- `MEMORY_SERVICE_TOKEN` when memory integration is enabled

These should come from Vault or another secret manager. Helm values can wire URLs and flags, but they should not contain literal secret values.

### Key environment variables

- `DISCORD_BOT_TOKEN`
- `LITELLM_BASE_URL`
- `LITELLM_API_KEY`
- `DRAGONFLY_ADDR`
- `MEMORY_ENABLED`
- `MEMORY_EVENTS_ENABLED`
- `MEMORY_GRAPH_CONTEXT_ENABLED`
- `MEMORY_SERVICE_URL`
- `MEMORY_SERVICE_TOKEN`
- `MEMORY_TENANT_ID`
- `DISCORD_HISTORY_BACKFILL_ENABLED`
- `DISCORD_BACKFILL_ENABLED`
- `DISCORD_AMBIENT_REPLIES_ENABLED`
- `AMBIENT_REPLIES_ENABLED`
- `DISCORD_ATTACHMENT_METADATA_ENABLED`
- `DISCORD_ATTACHMENT_COPY_ENABLED`
- `DISCORD_ATTACHMENT_COPY_POLICY`

## Local development

```bash
go run ./cmd/pc-principal/
```

The bot can start in degraded local mode when optional services such as Dragonfly or `agents-memory` are missing. Live Discord use still requires a valid `DISCORD_BOT_TOKEN`.

## Testing and verification

```bash
go test ./...
```

Important coverage points in the repo include:

- conversation prompt fallback and combined memory rendering tests under `internal/commands/`
- memory client contract coverage under `internal/memory/`
- event and backfill behavior under `internal/backfill/`, `internal/run/`, and `internal/discordevent/`

## Deployment and operations

PC-Principal is expected to run in Kubernetes with GitOps-driven config changes.

- The bot consumes `agents-memory` over HTTP.
- The memory service remains the only component that talks to Neo4j.
- Homelab deployment follows ClusterIP plus Traefik ingress patterns on the service side, not direct bot access to the database.
- Secrets are expected to flow through Vault and External Secrets Operator.
- ArgoCD is the expected reconciler for rollout and rollback.

### Rollout

1. Land the code or Helm change in Git.
2. Let ArgoCD reconcile the `pc-principal` chart.
3. Enable `MEMORY_ENABLED`, `MEMORY_EVENTS_ENABLED`, and related flags only when the matching `agents-memory` behavior is ready.
4. Keep backfill, ambient replies, and attachment copying off unless the rollout needs them.
5. Verify command handling, mention conversation flow, and one memory-backed request path.

### Rollback

1. Revert the smallest Git or Helm change.
2. Let ArgoCD reconcile.
3. If combined memory recall needs to back out, the bot can fall back to legacy `/v1/context` plus `/v1/graph/context` without changing the integration boundary.

## Health and failure behavior

- `GET /health` and `/healthz` expose bot, Discord, and Dragonfly readiness state.
- Memory failures are logged and treated as degraded dependencies where possible.
- Prompt construction remains usable with fallback context or local short-term history if the primary combined endpoint fails.

## Current guarantees and non-goals

### Current guarantees

- HTTP-only memory integration.
- Combined memory prompt rendering with `memory_type` awareness.
- Safe fallback to legacy context endpoints.
- Structured event ingestion without requiring the bot to reply.
- Reasoning trace lifecycle writes through the gateway.

### Non-goals in this repo

- Direct Neo4j access.
- Direct `neo4j-agent-memory` SDK access.
- Client-owned memory scope filtering.
- Claiming MCP-native memory support.

## Upstream attribution

PC-Principal is a Bromigos-local Discord bot built on [discordgo](https://github.com/bwmarrin/discordgo). Its shared memory integration goes through the Bromigos `agents-memory` gateway, not through direct database or SDK access.
