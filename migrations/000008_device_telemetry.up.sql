CREATE TABLE IF NOT EXISTS telemetry_device (
    id bigserial PRIMARY KEY,
    device_id uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    firmware_version text NOT NULL DEFAULT '',
    wifi_rssi integer,
    battery_voltage double precision,
    battery_percentage integer,
    recorded_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS telemetry_device_device_time_idx
    ON telemetry_device (device_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS telemetry_device_received_idx
    ON telemetry_device (received_at);
