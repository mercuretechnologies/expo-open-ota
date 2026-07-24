-- +goose NO TRANSACTION
-- +goose Up

-- One eoas publish produces one update row per platform (iOS then Android),
-- each with its own id and uuid; nothing ties them together. The CLI now mints
-- a single UUID per publish run and sends it with each per-platform call
-- (group republishes get a server-minted one), stored here so consumers
-- (dashboard grouping, per-publish health) can treat the set as one publish.
-- NULL for rows created by older CLIs, rollback markers (branch-level
-- operations, never grouped) and stateless mode, which degrade to the
-- ungrouped per-platform display.
ALTER TABLE updates ADD COLUMN IF NOT EXISTS publish_group UUID;

-- Serves the group-republish member resolution (and future group-scoped
-- reads). Partial on both predicates so the index only holds grouped, served
-- rows; ungrouped history costs nothing.
DROP INDEX CONCURRENTLY IF EXISTS idx_updates_publish_group;
CREATE INDEX CONCURRENTLY idx_updates_publish_group ON updates (publish_group)
    WHERE publish_group IS NOT NULL AND checked_at IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_updates_publish_group;
ALTER TABLE updates DROP COLUMN IF EXISTS publish_group;
