UPDATE commands SET origin = 'manual' WHERE origin = 'mcp';
ALTER TABLE commands DROP CONSTRAINT IF EXISTS commands_origin_check;
ALTER TABLE commands ADD CONSTRAINT commands_origin_check
    CHECK (origin IN ('manual', 'system'));
