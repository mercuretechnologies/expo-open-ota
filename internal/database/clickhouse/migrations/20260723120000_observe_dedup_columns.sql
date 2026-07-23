-- +goose Up

-- content_hash fingerprints one row (FNV-1a over its wire content, computed
-- by the flattener): published SDKs re-send a whole batch after ANY non-2xx,
-- so duplicate rows are a certainty, not an edge case. Dedup happens at query
-- time (uniqExact(content_hash), argMax) on the tolerant-fact-table model;
-- the sorting keys stay lean and no FINAL is ever needed on the hot path.
ALTER TABLE observe_metrics ADD COLUMN IF NOT EXISTS content_hash UInt64 DEFAULT 0;
ALTER TABLE observe_logs ADD COLUMN IF NOT EXISTS content_hash UInt64 DEFAULT 0;

-- Leftover point attributes as sorted JSON. setGlobalAttributes merges
-- arbitrary user keys into every metric, not only logs; without this column
-- those keys were silently dropped on the metrics side.
ALTER TABLE observe_metrics ADD COLUMN IF NOT EXISTS attributes String DEFAULT '' AFTER custom_params;

-- +goose Down
ALTER TABLE observe_metrics DROP COLUMN IF EXISTS attributes;
ALTER TABLE observe_logs DROP COLUMN IF EXISTS content_hash;
ALTER TABLE observe_metrics DROP COLUMN IF EXISTS content_hash;
