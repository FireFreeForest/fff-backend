DROP TABLE IF EXISTS event_timeline;
ALTER TABLE events DROP COLUMN IF EXISTS resolved_by;
