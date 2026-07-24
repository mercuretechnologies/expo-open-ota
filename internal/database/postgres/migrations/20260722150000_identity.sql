-- +goose Up
-- +goose StatementBegin

-- Device identity (ee/identity). Maps an expo-eas-client install UUID to
-- operator-defined metadata (userId, tenant, ...) fed by `identify` log
-- events on the observe ingestion route, plus GeoIP columns resolved from
-- the request IP. EE-licensed code, but NOT license-gated: the feature works
-- on community deployments too.

-- Trigram index support for the metadata value autocomplete. Ships with the
-- postgres contrib package (present in the official Docker images and on the
-- managed offerings: RDS, Cloud SQL, Neon, Supabase). Operators on a build
-- without contrib get a clear migration failure here, not a silent one later.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- The allowlist managed in the dashboard "Identity" section. Ingestion drops
-- every metadata key that is not declared here with a matching value type,
-- which is what makes hostile payloads (thousands of keys, megabyte values)
-- a non-issue by construction rather than by heuristics.
CREATE TABLE identity_schema (
    app_id UUID NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value_type TEXT NOT NULL CHECK (value_type IN ('string', 'number', 'boolean')),
    -- Applies to string values only. A string over the limit is dropped, not
    -- truncated: a truncated userId would silently corrupt the mapping.
    max_length INT NOT NULL DEFAULT 256,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (app_id, key)
);

-- One row per install. eas_client_id is the persistent per-install UUID from
-- expo-eas-client (sent as the expo.eas_client.id resource attribute).
-- metadata only ever contains allowlisted keys, so its size is bounded by the
-- schema, and the GIN index serves equality/containment lookups on any key
-- (metadata @> '{"userId": "X"}') without per-key indexes.
CREATE TABLE device_identity (
    app_id UUID NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    eas_client_id UUID NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- GeoIP enrichment, city-level accuracy (MaxMind GeoLite2). Nullable:
    -- resolution is best-effort and optional. COALESCE semantics on update:
    -- a request that resolves no geo never erases a previously known one.
    country_code TEXT,
    city TEXT,
    lat DOUBLE PRECISION,
    lng DOUBLE PRECISION,
    first_seen_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (app_id, eas_client_id)
);

CREATE INDEX idx_device_identity_metadata ON device_identity USING GIN (metadata jsonb_path_ops);
CREATE INDEX idx_device_identity_country ON device_identity (app_id, country_code);
CREATE INDEX idx_device_identity_last_seen ON device_identity (app_id, last_seen_at);

-- Distinct-value stats powering searchMetadata (dashboard autocomplete and
-- segmentation dropdowns). Cardinality is the number of distinct values, not
-- the number of devices, so lookups stay cheap at any fleet size. Counts are
-- maintained transactionally with device updates but treated as approximate
-- ranking signals, not billing-grade numbers.
CREATE TABLE identity_value_stats (
    app_id UUID NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    device_count BIGINT NOT NULL DEFAULT 0,
    last_seen_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (app_id, key, value)
);

CREATE INDEX idx_identity_value_stats_trgm ON identity_value_stats USING GIN (value gin_trgm_ops);

-- Serves the empty-search autocomplete (dropdown open, the most common call)
-- as an index-only top-N scan. Without it, every call top-N sorts all distinct
-- values of the key, which is O(user count) for a userId-style key.
CREATE INDEX idx_identity_value_stats_top ON identity_value_stats (app_id, key, device_count DESC, value ASC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS identity_value_stats;
DROP TABLE IF EXISTS device_identity;
DROP TABLE IF EXISTS identity_schema;
-- +goose StatementEnd
