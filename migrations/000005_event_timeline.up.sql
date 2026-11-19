ALTER TABLE events ADD COLUMN IF NOT EXISTS resolved_by uuid REFERENCES users (id) ON DELETE SET NULL;

UPDATE events e
SET resolved_by = a.actor_id
FROM (
    SELECT DISTINCT ON (entity_id) entity_id, actor_id
    FROM audit_logs
    WHERE action = 'event.resolve' AND entity = 'event' AND actor_id IS NOT NULL
    ORDER BY entity_id, created_at DESC
) a
WHERE e.resolved_by IS NULL AND e.id::text = a.entity_id;

CREATE TABLE IF NOT EXISTS event_timeline (
    id bigserial PRIMARY KEY,
    event_id uuid NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN (
        'CREATED', 'NOTIFICATION', 'REMINDER', 'ESCALATION',
        'ACKNOWLEDGED', 'RESOLVED', 'AUTO_RESOLVED'
    )),
    outcome text CHECK (outcome IS NULL OR outcome IN ('sent', 'partial', 'failed', 'skipped')),
    actor_type text NOT NULL CHECK (actor_type IN ('system', 'user')),
    actor_id uuid REFERENCES users (id) ON DELETE SET NULL,
    actor_name text NOT NULL DEFAULT 'Sistem',
    actor_role text NOT NULL DEFAULT '',
    title text NOT NULL DEFAULT '',
    message text NOT NULL DEFAULT '',
    level text NOT NULL DEFAULT 'info' CHECK (level IN ('info', 'success', 'warning', 'error', 'critical', 'muted')),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    dedupe_key text
);
CREATE INDEX IF NOT EXISTS event_timeline_event_time_idx
    ON event_timeline (event_id, occurred_at, id);
CREATE UNIQUE INDEX IF NOT EXISTS event_timeline_dedupe_key_uq
    ON event_timeline (dedupe_key) WHERE dedupe_key IS NOT NULL;

INSERT INTO event_timeline (
    event_id, kind, outcome, actor_type, actor_id, actor_name, actor_role,
    title, message, level, metadata, occurred_at, created_at
)
SELECT
    e.id,
    'CREATED',
    NULL,
    'system',
    NULL,
    'Sistem',
    '',
    CASE e.event_type
        WHEN 'FIRE_DETECTED' THEN 'Api Terdeteksi'
        WHEN 'SMOKE_DETECTED' THEN 'Asap Terdeteksi'
        WHEN 'TEMPERATURE_HIGH' THEN 'Suhu Tinggi'
        WHEN 'SOIL_DRY' THEN 'Tanah Kering'
        WHEN 'DEVICE_OFFLINE' THEN 'Perangkat Offline'
        WHEN 'SENSOR_ERROR' THEN 'Error Sensor'
        ELSE 'Event Terdeteksi'
    END,
    COALESCE(NULLIF(e.message, ''), 'Event terdeteksi oleh sistem.'),
    CASE e.severity
        WHEN 'critical' THEN 'critical'
        WHEN 'error' THEN 'error'
        ELSE 'warning'
    END,
    jsonb_build_object(
        'event_type', e.event_type,
        'severity', e.severity,
        'source', e.source,
        'status', e.status
    ),
    e.triggered_at,
    COALESCE(e.created_at, e.triggered_at)
FROM events e
WHERE NOT EXISTS (
    SELECT 1 FROM event_timeline t WHERE t.event_id = e.id AND t.kind = 'CREATED'
);

INSERT INTO event_timeline (
    event_id, kind, outcome, actor_type, actor_id, actor_name, actor_role,
    title, message, level, metadata, occurred_at, created_at
)
SELECT
    e.id,
    'ACKNOWLEDGED',
    NULL,
    CASE WHEN e.acknowledged_by IS NULL THEN 'system' ELSE 'user' END,
    e.acknowledged_by,
    COALESCE(u.name, 'Sistem'),
    COALESCE(u.role, ''),
    'Event Ditangani',
    CASE WHEN u.name IS NULL
        THEN 'Event ditandai ditangani oleh sistem.'
        ELSE u.name || ' menandai event sebagai ditangani.'
    END,
    'success',
    jsonb_build_object('from_status', 'active', 'to_status', 'acknowledged'),
    e.acknowledged_at,
    e.acknowledged_at
FROM events e
LEFT JOIN users u ON u.id = e.acknowledged_by
WHERE e.acknowledged_at IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM event_timeline t WHERE t.event_id = e.id AND t.kind = 'ACKNOWLEDGED'
  );

INSERT INTO event_timeline (
    event_id, kind, outcome, actor_type, actor_id, actor_name, actor_role,
    title, message, level, metadata, occurred_at, created_at
)
SELECT
    e.id,
    CASE WHEN e.resolved_by IS NULL THEN 'AUTO_RESOLVED' ELSE 'RESOLVED' END,
    NULL,
    CASE WHEN e.resolved_by IS NULL THEN 'system' ELSE 'user' END,
    e.resolved_by,
    COALESCE(u.name, 'Sistem'),
    COALESCE(u.role, ''),
    CASE WHEN e.resolved_by IS NULL THEN 'Event Diselesaikan Otomatis' ELSE 'Event Diselesaikan' END,
    CASE WHEN u.name IS NULL
        THEN 'Event ditutup otomatis oleh sistem.'
        ELSE u.name || ' menandai event sebagai selesai.'
    END,
    'success',
    jsonb_build_object(
        'from_status', e.status,
        'to_status', 'resolved',
        'reason', CASE WHEN e.resolved_by IS NULL THEN 'condition_normal' ELSE 'manual' END
    ),
    e.resolved_at,
    e.resolved_at
FROM events e
LEFT JOIN users u ON u.id = e.resolved_by
WHERE e.resolved_at IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM event_timeline t
      WHERE t.event_id = e.id AND t.kind IN ('RESOLVED', 'AUTO_RESOLVED')
  );

INSERT INTO event_timeline (
    event_id, kind, outcome, actor_type, actor_id, actor_name, actor_role,
    title, message, level, metadata, occurred_at, created_at
)
SELECT
    e.id,
    'REMINDER',
    'sent',
    'system',
    NULL,
    'Sistem',
    '',
    'Peringatan Ulang Dikirim',
    'Peringatan ulang berhasil dikirim.',
    'info',
    jsonb_build_object('historical', true, 'notification_type', 'fire_reminder'),
    e.reminder_sent_at,
    e.reminder_sent_at
FROM events e
WHERE e.reminder_sent_at IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM event_timeline t WHERE t.event_id = e.id AND t.kind = 'REMINDER'
  );

INSERT INTO event_timeline (
    event_id, kind, outcome, actor_type, actor_id, actor_name, actor_role,
    title, message, level, metadata, occurred_at, created_at
)
SELECT
    e.id,
    'ESCALATION',
    'sent',
    'system',
    NULL,
    'Sistem',
    '',
    'Eskalasi Darurat Dikirim',
    'Eskalasi darurat berhasil dikirim.',
    'info',
    jsonb_build_object('historical', true, 'notification_type', 'fire_escalation'),
    e.escalation_sent_at,
    e.escalation_sent_at
FROM events e
WHERE e.escalation_sent_at IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM event_timeline t WHERE t.event_id = e.id AND t.kind = 'ESCALATION'
  );
