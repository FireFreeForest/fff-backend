CREATE TABLE IF NOT EXISTS mcp_api_keys (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    key_prefix text NOT NULL,
    key_hash bytea NOT NULL,
    role text NOT NULL CHECK (role IN ('admin', 'petugas', 'viewer')),
    actor_id uuid REFERENCES users (id) ON DELETE SET NULL,
    is_active boolean NOT NULL DEFAULT true,
    expires_at timestamptz,
    last_used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS mcp_api_keys_prefix_uq ON mcp_api_keys (key_prefix);
CREATE INDEX IF NOT EXISTS mcp_api_keys_active_idx ON mcp_api_keys (is_active) WHERE is_active;
