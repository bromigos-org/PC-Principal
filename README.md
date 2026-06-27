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
| `MEMORY_SERVICE_URL` | Helm value | Base URL for `agents-memory` |
| `MEMORY_SERVICE_TOKEN` | Vault `secret/homelab/agents-memory:token` | Bearer token for `agents-memory`; optional secret ref in k8s |
| `MEMORY_TENANT_ID` | Helm value | Memory tenant ID, defaults to `bromigos` |
| `ALLOWED_ROLES` | Helm value | Comma-separated Discord role IDs that may use privileged commands (e.g. `mdelete`) |

## Deployment

Deployed to the Bromigos homelab k8s cluster via ArgoCD (`gitops/argocd-apps/pc-principal.yaml` watches `helm/pc-principal`). The Docker image is built and pushed to `ghcr.io/bromigos-org/pc-principal` on every merge to `main`.

The `DISCORD_BOT_TOKEN` and `LITELLM_API_KEY` are stored in HashiCorp Vault under `secret/homelab/pc-principal` and synced into the cluster via External Secrets Operator.

To redeploy after a code change:

```bash
git push origin main                       # triggers CI image build
# ArgoCD picks up the new image within ~3 minutes (image-updater)
argocd app sync pc-principal --prune      # optional force
```

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
