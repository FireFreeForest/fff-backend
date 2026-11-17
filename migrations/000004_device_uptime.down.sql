DROP INDEX IF EXISTS devices_online_since_idx;
ALTER TABLE devices DROP COLUMN IF EXISTS online_since;
