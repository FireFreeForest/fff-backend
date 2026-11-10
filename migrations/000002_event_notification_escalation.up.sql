ALTER TABLE events ADD COLUMN IF NOT EXISTS reminder_sent_at timestamptz;
ALTER TABLE events ADD COLUMN IF NOT EXISTS escalation_sent_at timestamptz;
