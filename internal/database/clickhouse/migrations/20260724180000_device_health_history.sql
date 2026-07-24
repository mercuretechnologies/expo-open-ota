-- +goose Up

-- Immutable device facts delivered from PostgreSQL's transactional outbox.
-- outbox_id is globally unique in the control-plane database. Replacing makes
-- the unavoidable "ClickHouse accepted, PostgreSQL delete crashed" retry
-- idempotent; analytical queries group by outbox_id rather than relying on
-- background merges having completed.
CREATE TABLE IF NOT EXISTS device_health_events (
    outbox_id         UInt64,
    event_type        LowCardinality(String),
    app_id            UUID,
    eas_client_id     UUID,
    update_id         UUID,
    previous_update_id Nullable(UUID),
    failure_type      LowCardinality(String) DEFAULT '',
    fatal_error       String DEFAULT '',
    occurred_at       DateTime64(3, 'UTC'),
    ingested_at       DateTime64(3, 'UTC') DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(ingested_at)
PARTITION BY toYYYYMM(occurred_at)
ORDER BY (app_id, outbox_id);

-- Query-ready one-minute projection of PostgreSQL's instant-T health. Raw
-- events above remain the reconstructible history; this small absolute-value
-- series keeps dashboard graphs cheap and deterministic.
CREATE TABLE IF NOT EXISTS update_health_snapshots (
    app_id             UUID,
    update_id          UUID,
    bucket             DateTime('UTC'),
    captured_at        DateTime64(3, 'UTC'),
    role               LowCardinality(String),
    devices_on_update  UInt64,
    successful_devices UInt64,
    faulty_devices     UInt64,
    update_issues      UInt64,
    runtime_issues     UInt64
)
ENGINE = ReplacingMergeTree(captured_at)
PARTITION BY toYYYYMM(bucket)
ORDER BY (app_id, update_id, bucket);

-- +goose Down
DROP TABLE IF EXISTS update_health_snapshots;
DROP TABLE IF EXISTS device_health_events;
