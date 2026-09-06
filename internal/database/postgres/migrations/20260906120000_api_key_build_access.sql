-- +goose Up
-- Existing tokens retain all Updates access they had before empty rule lists
-- became deny-by-default. Scoped tokens keep their existing rules unchanged.
INSERT INTO api_key_branch_rules (api_key_id, pattern, actions)
SELECT k.id, '*', ARRAY['read', 'publish', 'rollback']
FROM api_keys k
WHERE NOT EXISTS (SELECT 1 FROM api_key_branch_rules r WHERE r.api_key_id = k.id);

-- Build is a separate opt-in permission domain, including for existing keys.
ALTER TABLE api_keys ADD COLUMN build_actions TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down
-- The previous code interprets no rules as full access. Refuse to roll back
-- while a token has no Updates access: that would silently widen its rights.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM api_keys k
        WHERE k.revoked_at IS NULL
          AND NOT EXISTS (SELECT 1 FROM api_key_branch_rules r WHERE r.api_key_id = k.id)
    ) THEN
        RAISE EXCEPTION 'Cannot downgrade token permissions while live tokens have no Updates access';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE api_keys DROP COLUMN build_actions;
