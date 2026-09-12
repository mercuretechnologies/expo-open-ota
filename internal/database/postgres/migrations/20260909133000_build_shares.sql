-- +goose Up
CREATE TABLE IF NOT EXISTS build_shares (
    id UUID PRIMARY KEY,
    build_id UUID NOT NULL REFERENCES builds(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS build_shares_build ON build_shares(build_id, created_at DESC);

-- +goose Down
DROP TABLE build_shares;
