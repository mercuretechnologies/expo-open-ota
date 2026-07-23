-- +goose Up

-- The manifest-derived update-health state moved to Postgres
-- (device_identity.current_update_id + device_update_failures): instant-T
-- adoption and launch-crash health must work for every DB-mode deployment,
-- with no ClickHouse and no observe SDK. These two tables never shipped a
-- consumer; over-time adoption graphs derive from observe_metrics instead
-- (update_id is a column on every row).
DROP TABLE IF EXISTS device_current_update;
DROP TABLE IF EXISTS update_crashes;

-- +goose Down
CREATE TABLE IF NOT EXISTS device_current_update (
    app_id            UUID,
    eas_client_id     UUID,
    current_update_id UUID,
    branch            LowCardinality(String),
    channel           LowCardinality(String),
    runtime_version   LowCardinality(String),
    platform          LowCardinality(String),
    changed_at        DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(changed_at)
ORDER BY (app_id, eas_client_id);

CREATE TABLE IF NOT EXISTS update_crashes (
    app_id          UUID,
    eas_client_id   UUID,
    update_id       UUID,
    fatal_error     SimpleAggregateFunction(max, String),
    branch          LowCardinality(String),
    channel         LowCardinality(String),
    runtime_version LowCardinality(String),
    platform        LowCardinality(String),
    first_seen_at   SimpleAggregateFunction(min, DateTime('UTC')),
    last_seen_at    SimpleAggregateFunction(max, DateTime('UTC'))
)
ENGINE = AggregatingMergeTree
ORDER BY (app_id, update_id, eas_client_id);
