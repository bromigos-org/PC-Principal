# PC-Principal

A Discord bot for the Bromigos server, built in Go on top of [discordgo](https://github.com/bwmarrin/discordgo).

## Features

- **Vent Anonymously** — Post to `#vent-anonymously` and your identity is stripped. Replies in the thread are also anonymized.
- **Temp Channels** — Join `=Join To Start Game` to get a personal voice channel. It's automatically deleted when you leave and it's empty. React with the `bromigo` emoji to rename it.
- **Mdelete** — `@PC-Principal mdelete <n>` — bulk-delete the last N messages in a channel (admin only).
- **Mpost** — `@PC-Principal mpost #channel <message>` — post as the bot to any channel from `#moderator-only`.

## Deployment

Deployed to the Bromigos homelab k8s cluster via ArgoCD. The Docker image is built and pushed to `ghcr.io/bromigos-org/pc-principal` on every merge to `main`.

The `DISCORD_BOT_TOKEN` is stored in HashiCorp Vault (`secret/homelab/pc-principal`) and synced into the cluster via External Secrets Operator.

## Development

```bash
go run ./cmd/pc-principal/
```

Requires `DISCORD_BOT_TOKEN` set in your environment (or a `.env` file).
