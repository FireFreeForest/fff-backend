CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL,
    password_hash text NOT NULL,
    name text NOT NULL DEFAULT '',
    role text NOT NULL DEFAULT 'viewer' CHECK (role IN ('admin', 'petugas', 'viewer')),
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_uq ON users (lower(email));

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    user_agent text NOT NULL DEFAULT '',
    ip text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS refresh_tokens_user_idx ON refresh_tokens (user_id);

CREATE TABLE IF NOT EXISTS devices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id text NOT NULL UNIQUE,
    name text NOT NULL DEFAULT '',
    location_name text NOT NULL DEFAULT '',
    latitude double precision,
    longitude double precision,
    status text NOT NULL DEFAULT 'offline' CHECK (status IN ('online', 'offline')),
    firmware_version text NOT NULL DEFAULT '',
    last_seen_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS telemetry_environment (
    id bigserial PRIMARY KEY,
    device_id uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    temperature double precision NOT NULL,
    humidity double precision NOT NULL,
    soil_moisture double precision NOT NULL,
    recorded_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS telemetry_environment_device_time_idx
    ON telemetry_environment (device_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS telemetry_environment_received_idx
    ON telemetry_environment (received_at);

CREATE TABLE IF NOT EXISTS telemetry_fire (
    id bigserial PRIMARY KEY,
    device_id uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    flame_detected boolean NOT NULL DEFAULT false,
    smoke_level integer NOT NULL DEFAULT 0,
    pump boolean NOT NULL DEFAULT false,
    buzzer boolean NOT NULL DEFAULT false,
    recorded_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS telemetry_fire_device_time_idx
    ON telemetry_fire (device_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS telemetry_fire_received_idx
    ON telemetry_fire (received_at);

CREATE TABLE IF NOT EXISTS events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    event_type text NOT NULL,
    severity text NOT NULL CHECK (severity IN ('error', 'warning', 'critical')),
    source text NOT NULL DEFAULT '',
    message text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'acknowledged', 'resolved')),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    triggered_at timestamptz NOT NULL,
    acknowledged_by uuid REFERENCES users (id) ON DELETE SET NULL,
    acknowledged_at timestamptz,
    resolved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS events_device_time_idx ON events (device_id, triggered_at DESC);
CREATE INDEX IF NOT EXISTS events_status_severity_idx ON events (status, severity);
CREATE INDEX IF NOT EXISTS events_triggered_idx ON events (triggered_at DESC);

CREATE TABLE IF NOT EXISTS commands (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    command_id text NOT NULL UNIQUE,
    device_id uuid NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    action text NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'sent', 'success', 'failed', 'timeout')),
    origin text NOT NULL DEFAULT 'manual' CHECK (origin IN ('manual', 'system')),
    issued_by uuid REFERENCES users (id) ON DELETE SET NULL,
    issued_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    acked_at timestamptz,
    ack_message text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS commands_device_time_idx ON commands (device_id, issued_at DESC);
CREATE INDEX IF NOT EXISTS commands_status_idx ON commands (status);

CREATE TABLE IF NOT EXISTS outbox (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    topic text NOT NULL,
    payload jsonb NOT NULL,
    qos smallint NOT NULL DEFAULT 1,
    aggregate_type text NOT NULL DEFAULT '',
    aggregate_id text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz
);
CREATE INDEX IF NOT EXISTS outbox_pending_idx ON outbox (status, next_attempt_at);

CREATE TABLE IF NOT EXISTS push_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token text NOT NULL UNIQUE,
    platform text NOT NULL DEFAULT 'android',
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS push_tokens_user_idx ON push_tokens (user_id);

CREATE TABLE IF NOT EXISTS audit_logs (
    id bigserial PRIMARY KEY,
    actor_id uuid REFERENCES users (id) ON DELETE SET NULL,
    action text NOT NULL,
    entity text NOT NULL DEFAULT '',
    entity_id text NOT NULL DEFAULT '',
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_logs_created_idx ON audit_logs (created_at DESC);
