#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

go test ./...
go build -trimpath -ldflags="-s -w" -o telegram-stop-reply-bot ./cmd/bot
