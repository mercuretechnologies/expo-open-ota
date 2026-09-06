-- +goose Up
ALTER TABLE api_key_branch_rules RENAME TO api_key_update_rules;
ALTER TABLE api_key_update_rules RENAME CONSTRAINT uq_api_key_branch_rule TO uq_api_key_update_rule;
ALTER TABLE api_key_update_rules RENAME CONSTRAINT fk_api_key_branch_rules_api_key TO fk_api_key_update_rules_api_key;
ALTER TABLE api_key_update_rules RENAME CONSTRAINT api_key_branch_rules_pkey TO api_key_update_rules_pkey;
ALTER SEQUENCE api_key_branch_rules_id_seq RENAME TO api_key_update_rules_id_seq;
ALTER TABLE api_keys ADD CONSTRAINT uq_api_keys_app_id UNIQUE (id, app_id);
ALTER TABLE app_identifiers ADD CONSTRAINT uq_app_identifiers_app_id UNIQUE (id, app_id);

CREATE TABLE api_key_build_rules (
    api_key_id BIGINT NOT NULL,
    app_id UUID NOT NULL,
    app_identifier_id UUID NOT NULL,
    actions TEXT[] NOT NULL CHECK (cardinality(actions) > 0 AND array_position(actions, NULL) IS NULL AND actions <@ ARRAY['read', 'create', 'cancel']::TEXT[]),
    PRIMARY KEY (api_key_id, app_identifier_id),
    FOREIGN KEY (api_key_id, app_id) REFERENCES api_keys(id, app_id) ON DELETE CASCADE,
    FOREIGN KEY (app_identifier_id, app_id) REFERENCES app_identifiers(id, app_id) ON DELETE CASCADE
);
CREATE INDEX idx_api_key_build_rules_identifier ON api_key_build_rules(app_identifier_id, app_id);
CREATE INDEX idx_api_key_build_rules_app ON api_key_build_rules(app_id);

CREATE TABLE api_key_submit_rules (
    api_key_id BIGINT NOT NULL,
    app_id UUID NOT NULL,
    app_identifier_id UUID NOT NULL,
    destination TEXT NOT NULL CHECK (destination IN ('internal', 'alpha', 'beta', 'production', 'testflight', 'app-store')),
    actions TEXT[] NOT NULL CHECK (cardinality(actions) > 0 AND array_position(actions, NULL) IS NULL AND actions <@ ARRAY['read', 'upload', 'review', 'release']::TEXT[]),
    PRIMARY KEY (api_key_id, app_identifier_id, destination),
    FOREIGN KEY (api_key_id, app_id) REFERENCES api_keys(id, app_id) ON DELETE CASCADE,
    FOREIGN KEY (app_identifier_id, app_id) REFERENCES app_identifiers(id, app_id) ON DELETE CASCADE,
    CHECK (
        (destination = 'testflight' AND actions <@ ARRAY['read', 'upload', 'review', 'release']::TEXT[])
        OR (destination = 'app-store' AND actions <@ ARRAY['read', 'review', 'release']::TEXT[])
        OR (destination IN ('internal', 'alpha', 'beta', 'production') AND actions <@ ARRAY['read', 'upload', 'release']::TEXT[])
    )
);
CREATE INDEX idx_api_key_submit_rules_identifier ON api_key_submit_rules(app_identifier_id, app_id);
CREATE INDEX idx_api_key_submit_rules_app ON api_key_submit_rules(app_id);

-- Preserve existing grants for identifiers already declared, never future ones.
INSERT INTO api_key_build_rules (api_key_id, app_id, app_identifier_id, actions)
SELECT k.id, k.app_id, ai.id, k.build_actions
FROM api_keys k JOIN app_identifiers ai ON ai.app_id = k.app_id
WHERE cardinality(k.build_actions) > 0;
ALTER TABLE api_keys DROP COLUMN build_actions;

-- +goose Down
-- Identifier-scoped grants cannot be represented by the old app-wide model.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM api_key_build_rules r JOIN api_keys k ON k.id = r.api_key_id WHERE k.revoked_at IS NULL)
       OR EXISTS (SELECT 1 FROM api_key_submit_rules r JOIN api_keys k ON k.id = r.api_key_id WHERE k.revoked_at IS NULL) THEN
        RAISE EXCEPTION 'Cannot downgrade while live tokens have Build or Submit rules; revoke those grants first';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE api_keys ADD COLUMN build_actions TEXT[] NOT NULL DEFAULT '{}';
DROP TABLE api_key_submit_rules;
DROP TABLE api_key_build_rules;
ALTER TABLE app_identifiers DROP CONSTRAINT uq_app_identifiers_app_id;
ALTER TABLE api_keys DROP CONSTRAINT uq_api_keys_app_id;
ALTER TABLE api_key_update_rules RENAME CONSTRAINT uq_api_key_update_rule TO uq_api_key_branch_rule;
ALTER TABLE api_key_update_rules RENAME CONSTRAINT fk_api_key_update_rules_api_key TO fk_api_key_branch_rules_api_key;
ALTER TABLE api_key_update_rules RENAME CONSTRAINT api_key_update_rules_pkey TO api_key_branch_rules_pkey;
ALTER SEQUENCE api_key_update_rules_id_seq RENAME TO api_key_branch_rules_id_seq;
ALTER TABLE api_key_update_rules RENAME TO api_key_branch_rules;
