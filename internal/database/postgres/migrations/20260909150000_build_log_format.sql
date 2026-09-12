-- +goose Up
ALTER TABLE build_log_chunks ADD COLUMN format TEXT NOT NULL DEFAULT 'text'
    CHECK (format IN ('text', 'ndjson'));

-- +goose Down
ALTER TABLE build_log_chunks DROP COLUMN format;
