-- +goose NO TRANSACTION
-- +goose Up

-- Supports the branch/channel dashboard summaries: once the newest runtime for
-- a branch is known, its active rollout or latest checked update is read in
-- descending publication order.
DROP INDEX CONCURRENTLY IF EXISTS idx_updates_branch_runtime_created;
CREATE INDEX CONCURRENTLY idx_updates_branch_runtime_created
    ON updates(branch_id, runtime_version_id, created_at DESC, id DESC)
    INCLUDE (commit_hash, rollout_percentage)
    WHERE checked_at IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_updates_branch_runtime_created;
