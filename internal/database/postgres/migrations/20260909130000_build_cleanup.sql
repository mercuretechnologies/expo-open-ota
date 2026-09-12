-- +goose Up

-- Outbox of build artifacts whose row is gone. No foreign keys: the row is
-- written by the DELETE trigger and must outlive the cascade that fired it.
CREATE TABLE build_artifact_cleanup (
    id BIGSERIAL PRIMARY KEY,
    build_id UUID NOT NULL,
    platform TEXT NOT NULL,
    app_identifier_id UUID NOT NULL,
    artifact_type TEXT NOT NULL,
    due_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '15 minutes',
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX build_artifact_cleanup_due ON build_artifact_cleanup (due_at);

-- +goose StatementBegin
CREATE FUNCTION enqueue_build_artifact_cleanup() RETURNS trigger AS $$
BEGIN
    INSERT INTO build_artifact_cleanup (build_id, platform, app_identifier_id, artifact_type)
    VALUES (OLD.id, OLD.platform, OLD.app_identifier_id, OLD.artifact_type);
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_build_artifact_cleanup
AFTER DELETE ON builds
FOR EACH ROW EXECUTE FUNCTION enqueue_build_artifact_cleanup();

-- Ledger of stale staging sweeps, so a ready build is swept once.
CREATE TABLE build_staging_sweeps (
    build_id UUID PRIMARY KEY REFERENCES builds (id) ON DELETE CASCADE,
    swept_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS build_staging_sweeps;
DROP TRIGGER IF EXISTS trg_build_artifact_cleanup ON builds;
DROP FUNCTION IF EXISTS enqueue_build_artifact_cleanup;
DROP TABLE IF EXISTS build_artifact_cleanup;
