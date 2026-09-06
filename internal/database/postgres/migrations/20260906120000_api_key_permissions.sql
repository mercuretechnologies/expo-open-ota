-- +goose NO TRANSACTION
-- +goose Up
-- Build the parent indexes without blocking authentication reads and writes.
-- Drop unfinished indexes before retrying an interrupted migration.
DROP INDEX CONCURRENTLY IF EXISTS idx_api_keys_id_app;
CREATE UNIQUE INDEX CONCURRENTLY idx_api_keys_id_app ON api_keys (id, app_id);
DROP INDEX CONCURRENTLY IF EXISTS idx_app_identifiers_id_app;
CREATE UNIQUE INDEX CONCURRENTLY idx_app_identifiers_id_app ON app_identifiers (id, app_id);

-- Goose sends this block in one pgx Exec; PostgreSQL applies its statements
-- in one implicit transaction, so a failure rolls back the schema changes.
-- +goose StatementBegin
ALTER TABLE IF EXISTS api_key_branch_rules RENAME TO api_key_update_rules;
DO $$
BEGIN
    -- This constraint is renamed in the same atomic block as the data migration.
    -- Its old name marks the first run; retries must not grant new tokens access.
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'api_key_update_rules'::regclass AND conname = 'uq_api_key_branch_rule') THEN
        INSERT INTO api_key_update_rules (api_key_id, pattern, actions)
        SELECT k.id, '*', ARRAY['read', 'publish', 'rollback']
        FROM api_keys k
        WHERE NOT EXISTS (SELECT 1 FROM api_key_update_rules r WHERE r.api_key_id = k.id);

        ALTER TABLE api_key_update_rules RENAME CONSTRAINT uq_api_key_branch_rule TO uq_api_key_update_rule;
        ALTER TABLE api_key_update_rules RENAME CONSTRAINT fk_api_key_branch_rules_api_key TO fk_api_key_update_rules_api_key;
        ALTER TABLE api_key_update_rules RENAME CONSTRAINT api_key_branch_rules_pkey TO api_key_update_rules_pkey;
        ALTER SEQUENCE api_key_branch_rules_id_seq RENAME TO api_key_update_rules_id_seq;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'api_keys'::regclass AND conname = 'uq_api_keys_app_id') THEN
        ALTER TABLE api_keys ADD CONSTRAINT uq_api_keys_app_id UNIQUE USING INDEX idx_api_keys_id_app;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_identifiers'::regclass AND conname = 'uq_app_identifiers_app_id') THEN
        ALTER TABLE app_identifiers ADD CONSTRAINT uq_app_identifiers_app_id UNIQUE USING INDEX idx_app_identifiers_id_app;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS api_key_build_rules (
    api_key_id BIGINT NOT NULL,
    app_id UUID NOT NULL,
    app_identifier_id UUID NOT NULL,
    actions TEXT[] NOT NULL CHECK (cardinality(actions) > 0 AND array_position(actions, NULL) IS NULL AND actions <@ ARRAY['create']::TEXT[]),
    PRIMARY KEY (api_key_id, app_identifier_id),
    FOREIGN KEY (api_key_id, app_id) REFERENCES api_keys(id, app_id) ON DELETE CASCADE,
    FOREIGN KEY (app_identifier_id, app_id) REFERENCES app_identifiers(id, app_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_api_key_build_rules_identifier ON api_key_build_rules(app_identifier_id, app_id);
CREATE INDEX IF NOT EXISTS idx_api_key_build_rules_app ON api_key_build_rules(app_id);

CREATE TABLE IF NOT EXISTS api_key_submit_rules (
    api_key_id BIGINT NOT NULL,
    app_id UUID NOT NULL,
    app_identifier_id UUID NOT NULL,
    destination TEXT NOT NULL CHECK (destination IN ('internal', 'alpha', 'beta', 'production', 'testflight')),
    actions TEXT[] NOT NULL CHECK (cardinality(actions) > 0 AND array_position(actions, NULL) IS NULL AND actions <@ ARRAY['upload']::TEXT[]),
    PRIMARY KEY (api_key_id, app_identifier_id, destination),
    FOREIGN KEY (api_key_id, app_id) REFERENCES api_keys(id, app_id) ON DELETE CASCADE,
    FOREIGN KEY (app_identifier_id, app_id) REFERENCES app_identifiers(id, app_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_api_key_submit_rules_identifier ON api_key_submit_rules(app_identifier_id, app_id);
CREATE INDEX IF NOT EXISTS idx_api_key_submit_rules_app ON api_key_submit_rules(app_id);

-- +goose StatementEnd

-- USING INDEX renames attached indexes to the constraint names. On a retry
-- after the schema committed, only the redundant unattached indexes remain.
DROP INDEX CONCURRENTLY IF EXISTS idx_api_keys_id_app;
DROP INDEX CONCURRENTLY IF EXISTS idx_app_identifiers_id_app;

-- +goose Down
-- Refuse a downgrade that would widen Updates access or discard native grants.
-- +goose StatementBegin
DO $$
BEGIN
    -- A previous Down may have committed before Goose recorded its version.
    IF to_regclass('api_key_update_rules') IS NULL THEN
        RETURN;
    END IF;
    IF EXISTS (
        SELECT 1 FROM api_keys k
        WHERE k.revoked_at IS NULL
          AND NOT EXISTS (SELECT 1 FROM api_key_update_rules r WHERE r.api_key_id = k.id)
    ) THEN
        RAISE EXCEPTION 'Cannot downgrade token permissions while live tokens have no Updates access';
    END IF;
    IF EXISTS (SELECT 1 FROM api_key_build_rules r JOIN api_keys k ON k.id = r.api_key_id WHERE k.revoked_at IS NULL)
       OR EXISTS (SELECT 1 FROM api_key_submit_rules r JOIN api_keys k ON k.id = r.api_key_id WHERE k.revoked_at IS NULL) THEN
        RAISE EXCEPTION 'Cannot downgrade while live tokens have Build or Submit rules; revoke those grants first';
    END IF;
    DROP TABLE api_key_submit_rules;
    DROP TABLE api_key_build_rules;
    ALTER TABLE app_identifiers DROP CONSTRAINT uq_app_identifiers_app_id;
    ALTER TABLE api_keys DROP CONSTRAINT uq_api_keys_app_id;
    ALTER TABLE api_key_update_rules RENAME CONSTRAINT uq_api_key_update_rule TO uq_api_key_branch_rule;
    ALTER TABLE api_key_update_rules RENAME CONSTRAINT fk_api_key_update_rules_api_key TO fk_api_key_branch_rules_api_key;
    ALTER TABLE api_key_update_rules RENAME CONSTRAINT api_key_update_rules_pkey TO api_key_branch_rules_pkey;
    ALTER SEQUENCE api_key_update_rules_id_seq RENAME TO api_key_branch_rules_id_seq;
    ALTER TABLE api_key_update_rules RENAME TO api_key_branch_rules;
END $$;
-- +goose StatementEnd
