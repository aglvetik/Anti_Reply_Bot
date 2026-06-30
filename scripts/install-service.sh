#!/usr/bin/env bash
set -euo pipefail

APP_NAME="telegram-stop-reply-bot"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="/opt/${APP_NAME}"

cd "$ROOT_DIR"

if [[ ! -f "./${APP_NAME}" ]]; then
    echo "Missing ./${APP_NAME}. Run ./scripts/build.sh first."
    exit 1
fi

if [[ ! -f "./.env" ]]; then
    echo "Missing ./.env. Copy .env.example to .env and fill it in first."
    exit 1
fi

sudo install -d -m 755 "${APP_DIR}"
sudo install -m 755 "./${APP_NAME}" "${APP_DIR}/${APP_NAME}"
sudo install -m 600 "./.env" "${APP_DIR}/.env"
sudo install -m 644 "./deploy/systemd/${APP_NAME}.service" "/etc/systemd/system/${APP_NAME}.service"

sudo systemctl daemon-reload
sudo systemctl enable "${APP_NAME}.service"
sudo systemctl restart "${APP_NAME}.service"
sudo systemctl status --no-pager "${APP_NAME}.service"
