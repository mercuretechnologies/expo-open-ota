-- +goose Up
CREATE TABLE build_log_chunks (
    build_id UUID NOT NULL REFERENCES builds(id) ON DELETE CASCADE,
    byte_offset INTEGER NOT NULL CHECK (byte_offset >= 0),
    content TEXT NOT NULL CHECK (octet_length(content) BETWEEN 1 AND 32768),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (build_id, byte_offset),
    CHECK (byte_offset + octet_length(content) <= 10485760)
);

-- +goose Down
DROP TABLE build_log_chunks;
