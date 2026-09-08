-- +goose Up
ALTER TABLE app_identifiers ALTER COLUMN build_number DROP DEFAULT;
ALTER TABLE app_identifiers ALTER COLUMN build_number TYPE TEXT USING build_number::text;
ALTER TABLE app_identifiers ALTER COLUMN build_number SET DEFAULT '0';

-- +goose Down
-- Refuse rollback if any value cannot be represented exactly as a BIGINT.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM app_identifiers
        WHERE CASE WHEN build_number ~ '^-?(0|[1-9][0-9]*)$' AND length(build_number) <= 20
            THEN build_number::numeric NOT BETWEEN -9223372036854775808 AND 9223372036854775807
            ELSE true END
    ) THEN
        RAISE EXCEPTION 'Cannot restore BIGINT build_number without losing values';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE app_identifiers ALTER COLUMN build_number DROP DEFAULT;
ALTER TABLE app_identifiers ALTER COLUMN build_number TYPE BIGINT USING build_number::bigint;
ALTER TABLE app_identifiers ALTER COLUMN build_number SET DEFAULT 0;
