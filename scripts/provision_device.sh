#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 2 ]; then
    echo "usage: $0 <device_id> <password>" >&2
    exit 1
fi

DEVICE_ID="$1"
PASSWORD="$2"

if ! [[ "$DEVICE_ID" =~ ^[A-Za-z0-9._-]{3,64}$ ]]; then
    echo "device_id must be 3-64 characters (letters, digits, dot, underscore, dash)" >&2
    exit 1
fi

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

"${COMPOSE[@]}" exec -T -u 1883 mosquitto mosquitto_passwd -b /mosquitto/data/passwd "$DEVICE_ID" "$PASSWORD"
"${COMPOSE[@]}" exec -T -u 1883 mosquitto sh -c 'chmod 600 /mosquitto/data/passwd /mosquitto/data/acl'
"${COMPOSE[@]}" kill -s HUP mosquitto

PREFIX="${MQTT_TOPIC_PREFIX:-fire-free-forest}"

echo "provisioned MQTT user $DEVICE_ID"
echo "topics:"
echo "  $PREFIX/$DEVICE_ID/telemetry/environment"
echo "  $PREFIX/$DEVICE_ID/telemetry/fire"
echo "  $PREFIX/$DEVICE_ID/event"
echo "  $PREFIX/$DEVICE_ID/command/ack"
echo "  $PREFIX/$DEVICE_ID/command (subscribe)"
