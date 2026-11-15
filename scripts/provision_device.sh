#!/usr/bin/env bash
set -euo pipefail

# Devices share one MQTT account; identity comes from the MQTT client ID.
# This script only re-syncs the shared account from .env and reloads Mosquitto.
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

COMPOSE=()
if command -v podman-compose >/dev/null 2>&1; then
    COMPOSE=(podman-compose)
elif command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then
    COMPOSE=(podman compose)
elif command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    COMPOSE=(docker compose)
else
    echo "podman-compose, podman compose, or docker compose is required" >&2
    exit 1
fi

"${COMPOSE[@]}" up --force-recreate mosquitto-init
"${COMPOSE[@]}" kill -s HUP mosquitto
echo "shared MQTT device account synced from .env"
