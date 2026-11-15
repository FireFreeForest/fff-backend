ALTER TABLE telemetry_environment ALTER COLUMN temperature DROP NOT NULL;
ALTER TABLE telemetry_environment ALTER COLUMN humidity DROP NOT NULL;
ALTER TABLE telemetry_fire ADD COLUMN IF NOT EXISTS flame_level integer;
