-- +goose Up
ALTER TABLE app_identifiers ADD CONSTRAINT app_identifiers_app_id_id UNIQUE (app_id,id);
CREATE TABLE builds (
    id UUID PRIMARY KEY,
    app_id UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    app_identifier_id UUID NOT NULL REFERENCES app_identifiers(id) ON DELETE CASCADE,
    platform TEXT NOT NULL CHECK (platform IN ('android', 'ios')),
    application_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('building', 'uploading', 'ready', 'failed')),
    artifact_type TEXT NOT NULL CHECK (artifact_type IN ('apk', 'aab', 'ipa')),
    size BIGINT NOT NULL DEFAULT 0 CHECK (size >= 0 AND size <= 2147483648),
    sha256 TEXT NOT NULL DEFAULT '' CHECK (sha256 = '' OR sha256 ~ '^[0-9a-f]{64}$'),
    artifact_key TEXT NOT NULL UNIQUE,
    metadata JSONB NOT NULL,
    actor_type TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    actor_display TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    duration_ms BIGINT CHECK (duration_ms IS NULL OR duration_ms >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ready_at TIMESTAMPTZ,
    FOREIGN KEY (app_id,app_identifier_id) REFERENCES app_identifiers(app_id,id) ON DELETE CASCADE,
    CONSTRAINT builds_artifact_platform CHECK ((platform = 'android') = (artifact_type IN ('apk', 'aab'))),
    CONSTRAINT builds_artifact_declared CHECK ((size > 0) = (sha256 <> '')),
    CONSTRAINT builds_artifact_required CHECK (status NOT IN ('uploading', 'ready') OR size > 0),
    CONSTRAINT builds_finished CHECK ((status = 'building') = (finished_at IS NULL)),
    CONSTRAINT builds_duration CHECK ((finished_at IS NULL) = (duration_ms IS NULL)),
    CONSTRAINT builds_ready_at CHECK ((status = 'ready') = (ready_at IS NOT NULL))
);
CREATE INDEX builds_app_created ON builds(app_id, created_at DESC, id DESC);
CREATE INDEX builds_identifier ON builds(app_identifier_id);

-- +goose Down
DROP TABLE builds;
ALTER TABLE app_identifiers DROP CONSTRAINT app_identifiers_app_id_id;
