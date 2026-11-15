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

if [ ! -f "$DATA/passwd" ]; then
    touch "$DATA/passwd"
    chown 1883:1883 "$DATA/passwd"
    chmod 600 "$DATA/passwd"
    run_passwd backend "${MQTT_BACKEND_PASSWORD:-change-me-backend}"
    run_passwd "${MQTT_DEVICE_USERNAME:-FFF-VIRTUAL-DEVICE}" "${MQTT_DEVICE_PASSWORD:-device123}"
    echo "created MQTT password file"
else
    # keep provisioned device users, but keep the configured service and demo users in sync
    run_passwd backend "${MQTT_BACKEND_PASSWORD:-change-me-backend}"
    if [ -n "${MQTT_DEVICE_USERNAME:-}" ]; then
        run_passwd "$MQTT_DEVICE_USERNAME" "${MQTT_DEVICE_PASSWORD:-device123}"
    fi
    echo "updated backend and configured device MQTT users; other device users kept"
fi

PREFIX="${MQTT_TOPIC_PREFIX:-fire-free-forest}"

cat > "$DATA/acl" <<EOF
user backend
topic readwrite $PREFIX/#
topic read \$SYS/#

pattern write $PREFIX/%u/telemetry/#
pattern write $PREFIX/%u/event
pattern write $PREFIX/%u/command/ack
pattern read $PREFIX/%u/command
EOF

chown 1883:1883 "$DATA" "$DATA/passwd" "$DATA/acl"
chmod 600 "$DATA/passwd" "$DATA/acl"
