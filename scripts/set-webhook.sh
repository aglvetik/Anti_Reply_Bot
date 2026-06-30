#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ ! -f .env ]]; then
    echo "Missing ./.env. Copy .env.example to .env and fill it in first."
    exit 1
fi

set -a
# shellcheck disable=SC1091
source .env
set +a

: "${BOT_TOKEN:?BOT_TOKEN is required}"
: "${WEBHOOK_SECRET:?WEBHOOK_SECRET is required}"
: "${PUBLIC_WEBHOOK_URL:?PUBLIC_WEBHOOK_URL is required}"

WEBHOOK_MAX_CONNECTIONS="${WEBHOOK_MAX_CONNECTIONS:-40}"
DROP_PENDING_UPDATES="${DROP_PENDING_UPDATES:-true}"
WEBHOOK_ENDPOINT="${PUBLIC_WEBHOOK_URL%/}/tg/webhook"

payload=$(cat <<JSON
{
  "url": "${WEBHOOK_ENDPOINT}",
  "secret_token": "${WEBHOOK_SECRET}",
  "allowed_updates": ["message"],
  "max_connections": ${WEBHOOK_MAX_CONNECTIONS},
  "drop_pending_updates": ${DROP_PENDING_UPDATES}
}
JSON
)

echo "Setting webhook to ${WEBHOOK_ENDPOINT}"

curl -fsS "https://api.telegram.org/bot${BOT_TOKEN}/setWebhook" \
  -H "Content-Type: application/json" \
  -d "${payload}"

echo
echo "Webhook info:"
curl -fsS "https://api.telegram.org/bot${BOT_TOKEN}/getWebhookInfo"
echo
