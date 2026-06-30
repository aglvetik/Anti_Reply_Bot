# telegram-stop-reply-bot

`telegram-stop-reply-bot` is a small Go webhook bot for Telegram groups and supergroups. It lets a user block unwanted replies and pings from a specific person with the Russian command `Бот стоп`.

The bot is intentionally narrow:

- It only handles `message` updates.
- It stores rules in SQLite.
- It keeps active rules in memory for O(1) checks during normal message processing.
- It deletes violating messages as fast as Telegram allows after the webhook update arrives.
- It does not mute, ban, kick, or restrict users.

## What the bot does

Rules are directional and scoped per chat.

Rule key:

`chat_id + protected_user_id + blocked_user_id`

Example flow:

1. User A replies to User B with `Бот стоп`.
2. A new rule `B -> A` is enabled in the current chat.
3. User B can no longer reply to User A.
4. User B can no longer mention or text-mention User A.
5. If User A repeats the same reply command to User B, only the same direction `B -> A` is disabled.
6. If User B later replies to User A with `Бот стоп`, that creates or toggles the reverse rule `A -> B` independently.

Important behavior:

- A valid `Бот стоп` command is always processed before violation detection.
- This means a user who is currently blocked from replying can still reply with `Бот стоп` to create or toggle the reverse direction.
- The command only works for text messages that normalize to `бот стоп`.
- The command must be a reply.
- The sender and reply target must both exist, must be different users, and must not be bots.

Normalization rules:

- Trim leading and trailing spaces.
- Compare case-insensitively.
- Collapse multiple internal spaces to one.

Valid examples:

- `Бот стоп`
- `бот стоп`
- `БОТ СТОП`
- `  бот   стоп  `

## Immune users

Immune user IDs are configured through `IMMUNE_USER_IDS`.

Default example:

`IMMUNE_USER_IDS=5300889569`

Rules for immune users:

- Restrictions never apply to them.
- They can always reply to anyone.
- They can always mention or text-mention anyone.
- Attempts to create a rule against an immune user are ignored.
- Stored rules whose `blocked_user_id` is immune are ignored on startup and during matching.
- Immune users can still use `Бот стоп` against other users.

## Telegram limitations and required permissions

Telegram does not let bots prevent a message before it is sent. The practical flow is:

1. Telegram sends the webhook update.
2. The bot checks the in-memory rules.
3. The bot immediately calls `deleteMessage` when a violation is detected.

For this to work well:

- The bot must be an admin in the group or supergroup.
- The bot must have permission to delete messages.
- The bot must receive group messages.
- Privacy mode should be disabled in BotFather, or the bot must otherwise be able to receive the messages you care about.

## Configuration

Copy `.env.example` to `.env` and fill it in.

| Variable | Required | Default | Notes |
| --- | --- | --- | --- |
| `BOT_TOKEN` | yes | none | Telegram bot token from BotFather. |
| `WEBHOOK_SECRET` | yes | none | Must match `X-Telegram-Bot-Api-Secret-Token`. |
| `PUBLIC_WEBHOOK_URL` | for webhook setup script | none | Public HTTPS base URL, for example `https://bot.example.com`. The script appends `/tg/webhook`. |
| `HTTP_ADDR` | no | `127.0.0.1:8080` | Local HTTP listen address. |
| `SQLITE_PATH` | no | `./data/bot.sqlite` | SQLite database file. |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn`, or `error`. |
| `WARNING_TTL_SECONDS` | no | `5` | Temporary warning and confirmation message TTL. |
| `WEBHOOK_MAX_CONNECTIONS` | no | `40` | Used by `scripts/set-webhook.sh`. |
| `IMMUNE_USER_IDS` | no | `5300889569` | Comma-separated Telegram user IDs. |
| `DROP_PENDING_UPDATES` | for webhook setup script | `true` | Used by `scripts/set-webhook.sh`. |

## Build

```bash
go test ./...
go build -trimpath -ldflags="-s -w" -o telegram-stop-reply-bot ./cmd/bot
```

Or use:

```bash
./scripts/build.sh
```

## Run locally

```bash
cp .env.example .env
./scripts/run-local.sh
```

The local process only starts the webhook server. To receive real Telegram updates locally, expose the server through a public HTTPS endpoint such as an SSH tunnel, reverse proxy, or tunnel service, then set the webhook to that public URL.

## Deploy to a VPS

Example target layout:

- Working directory: `/opt/telegram-stop-reply-bot`
- Binary: `/opt/telegram-stop-reply-bot/telegram-stop-reply-bot`
- Env file: `/opt/telegram-stop-reply-bot/.env`

Suggested deploy flow:

1. Clone the repository on the VPS.
2. Copy `.env.example` to `.env` and fill in real values.
3. Build the binary with `./scripts/build.sh`.
4. Copy or install the binary and `.env` into `/opt/telegram-stop-reply-bot`.
5. Install the systemd service with `./scripts/install-service.sh`.
6. Put the Nginx config from `deploy/nginx/telegram-stop-reply-bot.conf` in place and adapt `server_name` and certificate paths.
7. Reload Nginx.
8. Set the webhook with `./scripts/set-webhook.sh`.

## Nginx

An example reverse proxy config is provided at:

`deploy/nginx/telegram-stop-reply-bot.conf`

What to customize:

- `server_name`
- TLS certificate paths
- Upstream address if you change `HTTP_ADDR`

After updating the file on the VPS:

```bash
sudo nginx -t
sudo systemctl reload nginx
```

## systemd

The example service file is:

`deploy/systemd/telegram-stop-reply-bot.service`

It uses:

- `WorkingDirectory=/opt/telegram-stop-reply-bot`
- `ExecStart=/opt/telegram-stop-reply-bot/telegram-stop-reply-bot`
- `EnvironmentFile=/opt/telegram-stop-reply-bot/.env`
- `Restart=always`

Install it with:

```bash
./scripts/install-service.sh
```

## Set the webhook

Use:

```bash
./scripts/set-webhook.sh
```

The script sends:

- `allowed_updates=["message"]`
- `max_connections` from `.env`
- `drop_pending_updates` from `.env`
- the configured webhook secret token

The webhook endpoint is:

`POST /tg/webhook`

If the `X-Telegram-Bot-Api-Secret-Token` header does not match `WEBHOOK_SECRET`, the bot returns `401 Unauthorized`.

## Logs

When running under systemd:

```bash
sudo journalctl -u telegram-stop-reply-bot -f
```

Recent logs:

```bash
sudo journalctl -u telegram-stop-reply-bot -n 100 --no-pager
```

The bot uses structured `slog` JSON logs and intentionally does not log full message texts.

## Storage and performance notes

- Active rules are loaded into memory at startup.
- Normal message processing does not query SQLite.
- Rule checks are in-memory lookups.
- SQLite is used for startup loading, rule toggles, and known user persistence.
- SQLite runs in WAL mode.
- Username mention matching uses Telegram message entities and UTF-16-safe extraction.

## Testing

Unit tests cover:

- command normalization
- valid and invalid `Бот стоп` command handling
- toggle enable and disable behavior
- reverse-direction independence
- reply, username mention, and text mention violations
- command bypass of violation detection
- immune user behavior
- bot-message ignore behavior

Run:

```bash
go test ./...
```
