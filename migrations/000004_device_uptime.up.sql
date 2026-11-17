ALTER TABLE devices ADD COLUMN IF NOT EXISTS online_since timestamptz;

UPDATE devices
SET online_since = COALESCE(last_seen_at, updated_at, now())
WHERE status = 'online' AND online_since IS NULL;

CREATE INDEX IF NOT EXISTS devices_online_since_idx
    ON devices (online_since) WHERE status = 'online';
