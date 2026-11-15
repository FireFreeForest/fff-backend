#!/bin/sh
set -eu

DATA=/mosquitto/data
mkdir -p "$DATA"
chown 1883:1883 "$DATA"

run_passwd() {
    if command -v su >/dev/null 2>&1; then
        MQTT_PW_FILE="$DATA/passwd" MQTT_PW_USER="$1" MQTT_PW_PASS="$2" \
            su -s /bin/sh mosquitto -c 'mosquitto_passwd -b "$MQTT_PW_FILE" "$MQTT_PW_USER" "$MQTT_PW_PASS"'
    else
        mosquitto_passwd -b "$DATA/passwd" "$1" "$2"
    fi
}

DEVICE_USERNAME="${MQTT_DEVICE_USERNAME:-}"
DEVICE_PASSWORD="${MQTT_DEVICE_PASSWORD:-}"
if [ -z "$DEVICE_USERNAME" ] || [ -z "$DEVICE_PASSWORD" ]; then
    echo "MQTT_DEVICE_USERNAME and MQTT_DEVICE_PASSWORD must be set" >&2
    exit 1
fi

if [ ! -f "$DATA/passwd" ]; then
    touch "$DATA/passwd"
    chown 1883:1883 "$DATA/passwd"
    chmod 600 "$DATA/passwd"
    run_passwd backend "${MQTT_BACKEND_PASSWORD:-change-me-backend}"
    run_passwd "$DEVICE_USERNAME" "$DEVICE_PASSWORD"
    echo "created MQTT password file"
else
    # keep the service and shared device accounts in sync; existing per-device users are left untouched
    run_passwd backend "${MQTT_BACKEND_PASSWORD:-change-me-backend}"
    run_passwd "$DEVICE_USERNAME" "$DEVICE_PASSWORD"
    echo "updated backend and shared device MQTT users"
fi

PREFIX="${MQTT_TOPIC_PREFIX:-fire-free-forest}"

cat > "$DATA/acl" <<EOF
user backend
topic readwrite $PREFIX/#
topic read \$SYS/#

pattern write $PREFIX/%c/telemetry/#
pattern write $PREFIX/%c/event
pattern write $PREFIX/%c/command/ack
pattern read $PREFIX/%c/command
EOF

chown 1883:1883 "$DATA" "$DATA/passwd" "$DATA/acl"
chmod 600 "$DATA/passwd" "$DATA/acl"
