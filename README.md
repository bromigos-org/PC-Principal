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

The bot talks to a Gemma 4 12B model through the in-cluster LiteLLM proxy. Two flavours:

- **`hey`** — `@PC-Principal hey <message>` — single-turn reply in the current channel. Stateless.
- **`chat`** — `@PC-Principal chat <topic>` — opens a Discord thread named after the topic and runs a persistent multi-turn conversation. Message history is stored in DragonflyDB under `conversation:<threadID>` with a 24-hour TTL. The thread `AutoArchiveDuration` is set to 60 minutes.

Both use the same system prompt (`pcPrincipalSystemPrompt` in `internal/commands/hey.go`) that keeps the bot in character as South Park's PC Principal — short, punchy, full of "bro"/"sweet"/"totally", quick to ask "You PC, bro?" and call out anyone not being a decent human.

## Architecture

```
Discord (gateway)
    │
    ▼
PC-Principal (this repo, Go + discordgo)
    │   ├─→ HTTP server on :8080
    │   │     └─→ /health, /healthz  (Discord gateway state + DragonflyDB ping)
    │   ├─→ LiteLLM proxy  (chat completions, model: gemma4)
    │   └─→ DragonflyDB    (conversation history for `chat` threads)
    │
    ▼
Vault (DISCORD_BOT_TOKEN, LITELLM_API_KEY) → External Secrets Operator → k8s Secret
```

- **Go 1.26** + [discordgo](https://github.com/bwmarrin/discordgo) for the gateway
- **DragonflyDB** (Redis-compatible) at `dragonfly.dragonfly.svc.cluster.local:6379` for thread state
- **LiteLLM** at `http://litellm.litellm.svc.cluster.local:4000/v1` for LLM calls

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
    hey.go               # in-channel stateless chat
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
  utils/
    log.go               # structured JSON logging helper
```

## License

MIT — see [LICENSE](LICENSE).
