# PC-Principal

A Discord bot for the Bromigos server, written in Go on top of [discordgo](https://github.com/bwmarrin/discordgo). Runs moderation utilities, manages voice channels, and chats with members in character as [PC Principal from South Park](https://southpark.fandom.com/wiki/PC_Principal) by routing through a local LLM (Gemma 4 12B) via [LiteLLM](https://github.com/BerriAI/litellm).

## Features

### Moderation & utilities

- **Vent Anonymously** — Post to `#vent-anonymously` and your identity is stripped. Replies in the thread are also anonymized.
- **Temp Channels** — Join `=Join To Start Game` to get a personal voice channel. It's automatically deleted when you leave and it's empty. React with the `bromigo` emoji to rename it.
- **Mdelete** — `@PC-Principal mdelete <n>` — bulk-delete the last N messages in a channel (gated by the role allow-list in `ALLOWED_ROLES`).
- **Mpost** — `@PC-Principal mpost #channel <message>` — post as the bot to any channel.
- **Help** — `@PC-Principal help` — DMs the user a list of every registered command.

### LLM chat (via LiteLLM)

The bot talks to a Gemma 4 12B model through the in-cluster LiteLLM proxy. Three flavours:

- **Natural mentions** — `@PC-Principal prove you're pc` — direct channel conversation with bounded DragonflyDB short-term memory and optional scoped `agents-memory` long-term recall/write.
- **`hey`** — `@PC-Principal hey <message>` — compatibility form for the same direct channel conversation path.
- **`chat`** — `@PC-Principal chat <topic>` — opens a Discord thread named after the topic and runs a persistent multi-turn conversation. Message history is stored in DragonflyDB under `conversation:<threadID>` with a 24-hour TTL. The thread `AutoArchiveDuration` is set to 60 minutes.

Both use the same system prompt (`pcPrincipalSystemPrompt` in `internal/commands/hey.go`) that keeps the bot in character as South Park's PC Principal — short, punchy, full of "bro"/"sweet"/"totally", quick to ask "You PC, bro?" and call out anyone not being a decent human.

Command precedence is preserved for `ping`, `help`, `chat`, `mdelete`, `mpost`, and `hey`. A mention that does not match one of those commands falls through to the conversation path; a mention with no text gets a short prompt to say more.

### Conversation behavior

- Direct channel mentions use bounded memory keyed by guild and channel (`guild:<guild_id>:channel:<channel_id>`). The bot keeps the system prompt plus the latest 40 non-system turns when DragonflyDB/Redis is available.
- When `MEMORY_ENABLED=true`, direct mentions also call `agents-memory` for scoped long-term recall and write user/assistant turns after each reply. Guild conversations use channel visibility; DMs use private-user visibility.
- Existing `chat` threads keep their current thread-scoped memory behavior.
- If `DRAGONFLY_ADDR` is not set or the store has not initialized, conversation store reads/writes safely become no-ops instead of crashing the bot.
- If `MEMORY_SERVICE_URL` or `MEMORY_SERVICE_TOKEN` is not set, long-term memory safely becomes a no-op instead of crashing the bot.
- Responses are split into multiple Discord messages below Discord's 2,000-character limit. Empty model responses are replaced with a short fallback.
- Prompts include bounded context when available: the author's role names, guild and channel names, explicitly mentioned users, and recent unique channel participants from recent messages.
- Server roster and role awareness is on-demand. Questions about members, the server/guild, or roles add a bounded sample of up to 25 guild members plus role mention IDs so PC Principal can answer roster/role questions and mention users or roles without dumping the full guild into every prompt.
- Model-generated replies may mention users, roles, `@everyone`, or `@here`. Keep `ALLOWED_ROLES` limited to users trusted with that ping authority, and restrict the bot's Discord mention permissions if mass pings should not be allowed.
- Discord API failures degrade gracefully; missing roster/role context does not block a normal conversation reply.

### Discord memory ingestion and reply rules

- Live Discord ingestion is broader than the reply surface. Normal visible messages, channel and thread lifecycle changes, role and member updates, reactions, links, and attachment metadata are ingested silently when memory events are enabled.
- Silent ingestion does not create replies on its own. Non-mention messages stay silent by default, even when they are stored for memory or graph recall.
- Mention handling keeps command precedence. `ping`, `help`, `chat`, `mdelete`, `mpost`, and `hey` are checked first. Only mentions that are not commands fall through to the conversation path.
- Ambient replies are off by default. They are only possible after an explicit successful mention conversation starts an active session, and the same allowed-role checks and command bypass rules still apply.
- Live ingestion failures are non-fatal. If `agents-memory` or graph recall is unavailable, PC Principal logs the failure and continues the normal Discord command or conversation flow.
- Backfill has no Discord reply surface. Historical replay only writes memory events, and never sends channel replies while it catches up.

### Visibility, privacy, and retention

- DMs use `private_user` visibility. Recall from a DM only sees that DM user's private scope, plus any wider tenant or approved agent-shared context intentionally exposed by the memory service.
- Guild mention conversations use `channel` visibility keyed to the current guild and channel. Graph recall does not cross into sibling channels, even inside the same guild.
- Guild topology facts such as channels, roles, and members are stored with `guild` visibility. They can support recall inside that guild, but they still stay inside the tenant and agent boundary.
- Reviewed skill records are `agent_shared` by default. They are visible to the requesting agent within the same tenant, but proposals do not become runnable behavior.
- The memory service also enforces tenant and agent boundaries. PC Principal uses tenant `bromigos` by default and agent ID `pc-principal`, so cross-tenant or other-agent recall is out of scope.
- Dragonfly short-term conversation state is separate from long-term memory. Mention and `chat` history keeps the current 24 hour TTL behavior in Dragonfly, while Discord graph events keep tombstones for deletes and renames instead of hard-deleting the historical fact trail.

### Backfill and ambient controls

- `DISCORD_HISTORY_BACKFILL_ENABLED` and `DISCORD_BACKFILL_ENABLED` are wired to the same Helm value. The feature is disabled by default and is intended for read-only historical ingestion.
- Default backfill caps are bounded: up to 25 channels per run, up to 500 messages per channel, batches of 50 events, 250ms request delay, 1s backoff, and 3 attempts.
- Backfill is cursor-based and resumable. Cursor keys live under `backfill:discord:history:guild:{guild}:channel:{channel}` so they do not collide with live conversation memory.
- Channels without `View Channel`, `Read Message History`, or other required Discord permissions are skipped, and the preflight warnings call that out instead of failing the whole bot.
- `DISCORD_AMBIENT_REPLIES_ENABLED` and `AMBIENT_REPLIES_ENABLED` are both wired to the same guard. Default session limits are 20 minutes, 6 user turns, 2 consecutive bot replies, 90 second per-channel cooldown, and 30 second per-user cooldown.
- Ambient mode only reuses the normal mention conversation path after the bot has already been engaged. It does not change mention command precedence, and it does not make ordinary channel ingestion chatty.

### Skill and attachment policy

- Reviewed skills are context, not executable self-modification. The workflow is observe, propose, ask for approval, save the approved reviewed skill, then include that reviewed skill as non-executable prompt context.
- Unapproved proposals are never runnable. Proposed, rejected, disabled, or unreviewed records do not appear in the prompt section that the assistant uses for conversation context.
- Skill usage tracking is only accepted for approved reviewed skills. Attempting to record usage for an unapproved proposal is rejected by `agents-memory`.
- Attachment handling is metadata-first by default. PC Principal records attachment filename, content type, size, dimensions, spoiler status, and sanitized URLs, plus discovered links with sanitized URLs.
- `DISCORD_ATTACHMENT_METADATA_ENABLED=true` and `DISCORD_ATTACHMENT_COPY_ENABLED=false` is the default rollout posture. `DISCORD_ATTACHMENT_COPY_POLICY=metadata-only` means no attachment bytes are copied or stored during the default deployment.
- RustFS only becomes relevant if a future copy policy is intentionally enabled. The current documented rollout assumes metadata-only behavior, not object copying.

## Architecture

```
Discord (gateway)
    │
    ▼
PC-Principal (this repo, Go + discordgo)
    │   ├─→ HTTP server on :8080
    │   │     └─→ /health, /healthz  (Discord gateway state + DragonflyDB ping)
    │   ├─→ LiteLLM proxy  (chat completions, model: gemma4)
    │   ├─→ DragonflyDB    (conversation history for `chat` threads + direct mentions)
    │   └─→ agents-memory  (optional scoped long-term graph/vector memory)
    │
    ▼
Vault (DISCORD_BOT_TOKEN, LITELLM_API_KEY) → External Secrets Operator → k8s Secret
```

- **Go 1.26** + [discordgo](https://github.com/bwmarrin/discordgo) for the gateway
- **DragonflyDB** (Redis-compatible) at `dragonfly.dragonfly.svc.cluster.local:6379` for thread state
- **LiteLLM** at `http://litellm.litellm.svc.cluster.local:4000/v1` for LLM calls
- **agents-memory** at `http://agents-memory.agents-memory.svc.cluster.local:8080` for optional long-term memory

The HTTP server starts before `Init()` so the `/health` endpoint is reachable during the (sometimes slow) Discord gateway handshake — the homepage dot stays green even while the bot is still connecting.

## Health endpoint

`GET /health` (alias `/healthz`) on port `8080`. Always returns `200 OK` while the HTTP server is reachable, so k8s liveness/readiness probes and the homepage `siteMonitor` both stay green during normal operation. The JSON body carries the deeper state:

```bash
$ curl -s http://pc-principal.pc-principal.svc.cluster.local:8080/health | jq
{
  "status": "ok",                    # "ok" or "degraded"
  "checks": {
    "discord":   "ready",            # "ready" | "not_ready"
    "dragonfly": "ok"                # "ok" | "unreachable: <err>" | "skipped"
  },
  "checkedAt": "2026-06-10T05:40:12Z"
}
```

The Discord flag is flipped by the `onReady` / `onDisconnect` / `onReconnect` handlers in `internal/run/run.go`. The Dragonfly flag is a 1-second `PING` against the client created in `internal/store/conversation.go`.

## Configuration

All config comes from environment variables (set via the Helm chart's `Deployment` + `ExternalSecret`):

| Variable | Source | Purpose |
|---|---|---|
| `DISCORD_BOT_TOKEN` | Vault `secret/homelab/pc-principal:discord_bot_token` | Bot authentication |
| `LITELLM_BASE_URL` | Helm value | LLM proxy URL |
| `LITELLM_API_KEY` | Vault `secret/homelab/pc-principal:litellm_api_key` | LLM proxy auth |
| `DRAGONFLY_ADDR` | Helm value | Dragonfly host:port |
| `MEMORY_ENABLED` | Helm value | Enables the shared `agents-memory` service when set to `true` |
| `MEMORY_EVENTS_ENABLED` | Helm value | Enables silent Discord event ingestion into `agents-memory` |
| `MEMORY_GRAPH_CONTEXT_ENABLED` | Helm value | Enables scoped Discord graph recall during mention conversations |
| `MEMORY_SERVICE_URL` | Helm value | Base URL for `agents-memory` |
| `MEMORY_SERVICE_TOKEN` | Vault `secret/homelab/agents-memory:token` | Bearer token for `agents-memory`; optional secret ref in k8s |
| `MEMORY_TENANT_ID` | Helm value | Memory tenant ID, defaults to `bromigos` |
| `DISCORD_HISTORY_BACKFILL_ENABLED` | Helm value | Enables bounded historical Discord replay into memory |
| `DISCORD_BACKFILL_ENABLED` | Helm value | Alias for the same backfill toggle used by the chart wiring |
| `DISCORD_HISTORY_BACKFILL_AGENT_ID` | Helm value | Agent ID stamped onto backfill events, defaults to `pc-principal` |
| `DISCORD_HISTORY_BACKFILL_MAX_CHANNELS` | Helm value | Max channels visited in one backfill run, default `25` |
| `DISCORD_HISTORY_BACKFILL_MAX_MESSAGES_PER_CHANNEL` | Helm value | Max messages ingested per channel in one run, default `500` |
| `DISCORD_HISTORY_BACKFILL_MEMORY_BATCH_SIZE` | Helm value | Event batch size for backfill writes, default `50` |
| `DISCORD_HISTORY_BACKFILL_REQUEST_DELAY` | Helm value | Delay between Discord history requests, default `250ms` |
| `DISCORD_HISTORY_BACKFILL_BACKOFF` | Helm value | Retry backoff for Discord history requests, default `1s` |
| `DISCORD_HISTORY_BACKFILL_MAX_ATTEMPTS` | Helm value | Retry count for Discord history requests, default `3` |
| `DISCORD_AMBIENT_REPLIES_ENABLED` | Helm value | Enables guarded ambient replies after explicit bot engagement |
| `AMBIENT_REPLIES_ENABLED` | Helm value | Alias for the same ambient toggle used by the chart wiring |
| `DISCORD_AMBIENT_SESSION_TTL` | Helm value | Ambient session lifetime, default `20m` |
| `DISCORD_AMBIENT_MAX_USER_TURNS` | Helm value | Max user turns in one ambient session, default `6` |
| `DISCORD_AMBIENT_MAX_CONSECUTIVE_REPLIES` | Helm value | Max consecutive ambient bot replies, default `2` |
| `DISCORD_AMBIENT_CHANNEL_REPLY_COOLDOWN` | Helm value | Minimum delay between ambient replies in one channel, default `90s` |
| `DISCORD_AMBIENT_USER_REPLY_COOLDOWN` | Helm value | Minimum delay between ambient replies for one user, default `30s` |
| `DISCORD_ATTACHMENT_METADATA_ENABLED` | Helm value | Enables attachment metadata capture |
| `DISCORD_ATTACHMENT_COPY_ENABLED` | Helm value | Enables attachment byte copy only if intentionally turned on |
| `DISCORD_ATTACHMENT_COPY_POLICY` | Helm value | Attachment copy posture, default `metadata-only` |
| `MEMORY_SKILL_REGISTRY_ENABLED` | Helm value | Enables reviewed skill list and proposal endpoints |
| `MEMORY_SKILL_REVIEWED_ONLY` | Helm value | Restricts returned skills to approved reviewed records |
| `MEMORY_SKILL_PROPOSE_ENABLED` | Helm value | Enables skill proposal writes for review |
| `MEMORY_SKILL_USAGE_ENABLED` | Helm value | Enables approved skill usage recording |
| `ALLOWED_ROLES` | Helm value | Comma-separated Discord role IDs that may use privileged commands (e.g. `mdelete`) |

## Deployment

Deployed to the Bromigos homelab k8s cluster via ArgoCD (`gitops/argocd-apps/pc-principal.yaml` watches `helm/pc-principal`). The Docker image is built and pushed to `ghcr.io/bromigos-org/pc-principal` on every merge to `main`.

The `DISCORD_BOT_TOKEN` and `LITELLM_API_KEY` are stored in HashiCorp Vault under `secret/homelab/pc-principal` and synced into the cluster via External Secrets Operator.

To roll out a Discord memory change safely:

```bash
git push origin master
```

Recommended rollout sequence:

1. Land and review the docs and chart changes in Git.
2. Enable core memory plumbing first: `MEMORY_ENABLED`, `MEMORY_EVENTS_ENABLED`, and `MEMORY_GRAPH_CONTEXT_ENABLED`.
3. Keep `DISCORD_BACKFILL_ENABLED`, `DISCORD_AMBIENT_REPLIES_ENABLED`, and `AMBIENT_REPLIES_ENABLED` off for the first rollout unless the change specifically needs them.
4. Keep attachment copy off by default with `DISCORD_ATTACHMENT_COPY_ENABLED=false` and `DISCORD_ATTACHMENT_COPY_POLICY=metadata-only`.
5. Allow ArgoCD to reconcile from Git. Do not bypass GitOps with manual cluster apply commands, manual chart upgrade commands, or manual sync clicks for this tracked service.

Rollback uses the same GitOps path. Revert or flip the smallest necessary flag in Git, push to `master`, and let ArgoCD reconcile. The main independent rollback levers are:

- `MEMORY_EVENTS_ENABLED=false` to stop live Discord event ingestion while keeping the base bot online.
- `MEMORY_GRAPH_CONTEXT_ENABLED=false` to stop graph recall without turning off basic mention conversations.
- `DISCORD_BACKFILL_ENABLED=false` and `DISCORD_HISTORY_BACKFILL_ENABLED=false` to stop historical replay.
- `DISCORD_AMBIENT_REPLIES_ENABLED=false` and `AMBIENT_REPLIES_ENABLED=false` to stop ambient follow-up replies.
- `MEMORY_SKILL_REGISTRY_ENABLED=false`, `MEMORY_SKILL_PROPOSE_ENABLED=false`, or `MEMORY_SKILL_USAGE_ENABLED=false` to disable skill surfaces independently.
- `DISCORD_ATTACHMENT_COPY_ENABLED=false` to force metadata-only attachment behavior again.

### Read-only smoke checks

Run these checks after GitOps reconciliation. They are read-only and safe for operators:

```bash
curl -s http://pc-principal.pc-principal.svc.cluster.local:8080/health | jq
curl -s -H "Authorization: Bearer $MEMORY_SERVICE_TOKEN" \
  -H "Content-Type: application/json" \
  http://agents-memory.agents-memory.svc.cluster.local:8080/v1/skills \
  -d '{"tenant_id":"bromigos","agent_id":"pc-principal"}' | jq
```

What to verify:

- `/health` returns HTTP 200 and a JSON body with Discord `ready` or an explained degraded state.
- Mention conversations still work, command precedence still works, and ordinary channel chatter stays silent when ambient is disabled.
- If backfill is enabled, logs should show bounded progress and permission-denied skips, but no Discord reply traffic.
- Skill list responses should only contain approved reviewed entries, not draft proposals.

## Development

```bash
go run ./cmd/pc-principal/
```

Requires `DISCORD_BOT_TOKEN` set in your environment (or a `.env` file). All other env vars are optional during local dev — the bot will start in "degraded" mode and log a warning if `DRAGONFLY_ADDR` is missing.

### Running tests

```bash
go test ./...
```

There's a small test suite in `internal/commands/messages_test.go` covering case-insensitive command lookup.

### Project layout

```
cmd/pc-principal/        # main entry point
internal/
  run/                   # process lifecycle, HTTP server, health probe
    checkHealth.go       # /health handler
    run.go               # Discord session, event handlers
  commands/              # one file per slash-style command
    registry.go          # Command struct + register()
    hey.go               # in-channel PC Principal chat entry point
    conversation.go      # direct mention conversation + memory recall/write
    conversation_context.go  # Discord role/member/channel context assembly
    hey_thread.go        # thread reply handler
    chat.go              # persistent threaded chat with DragonflyDB backing
    moderator.go         # mpost
    delete.go            # mdelete
    ventAnonymously.go   # anonymous posting
    tempChannel.go       # voice channel lifecycle
    help.go              # DM the user the command list
    auth.go              # role allow-list check
    messages.go          # message context helpers
  store/                 # DragonflyDB client + conversation persistence
    conversation.go      # Init, Client, Exists, Get, Save
  memory/                # agents-memory HTTP client
  llm/                   # LiteLLM client adapter
  utils/
    log.go               # structured JSON logging helper
```

### Discord intents

Enable the bot's privileged intents in the Discord Developer Portal:

- Message Content Intent — required so direct mention text can be read and routed.
- Server Members Intent — used for member/role context when available.
- Presence Intent — currently requested by the bot session along with guild, message, reaction, voice, and DM intents.

### Agent framework status

PC Principal is adapter-first, not ADK-backed yet. The Discord handlers call an internal LLM client interface, and the active implementation still sends direct LiteLLM chat-completions to the configured `gemma4` model.

[ADK-Go](https://github.com/google/adk-go) looks useful for future tool-using agents. Its core packages include `agent/llmagent`, `runner`, `session`, `memory`, `tool`, and `model`; the documented happy path uses Gemini with `llmagent.New`, `runner.New`, and `session.InMemoryService`. A LiteLLM/OpenAI-compatible backend should be proven with a custom `model.LLM` implementation before adding ADK to production.

Future ADK spike checklist:

- Add `internal/agent/adk_litellm_model.go` to wrap the current internal LLM adapter as an ADK `model.LLM`.
- Add `internal/agent/adk_litellm_model_test.go` proving ADK request/response mapping preserves PC Principal prompt context and LiteLLM behavior.
- Add `internal/agent/runner.go` only after the adapter test passes, wiring `llmagent`, `runner`, and a session service.
- Add `google.golang.org/adk` to `go.mod` only in that spike branch/commit.

## License

MIT — see [LICENSE](LICENSE).
