ALTER TABLE telemetry_fire DROP COLUMN IF EXISTS flame_level;
UPDATE telemetry_environment SET temperature = 0 WHERE temperature IS NULL;
UPDATE telemetry_environment SET humidity = 0 WHERE humidity IS NULL;
ALTER TABLE telemetry_environment ALTER COLUMN temperature SET NOT NULL;
ALTER TABLE telemetry_environment ALTER COLUMN humidity SET NOT NULL;
