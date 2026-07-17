BEGIN;

UPDATE hosted_interaction.interactions
SET status = 'opened',
    version = version + 1,
    authentication_lease_digest = NULL,
    authentication_started_at = NULL,
    authentication_lease_expires_at = NULL
WHERE status = 'authenticating'
  AND authentication_lease_expires_at <= clock_timestamp();

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM hosted_interaction.interactions
        WHERE authentication_lease_digest IS NOT NULL
           OR authentication_started_at IS NOT NULL
           OR authentication_lease_expires_at IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'migration 000020 rollback refused: active Hosted authentication lease exists';
    END IF;
END;
$$;

ALTER TABLE hosted_interaction.interactions
    DROP CONSTRAINT IF EXISTS hosted_interactions_authentication_lease_shape,
    DROP COLUMN IF EXISTS authentication_lease_digest,
    DROP COLUMN IF EXISTS authentication_started_at,
    DROP COLUMN IF EXISTS authentication_lease_expires_at;

COMMIT;
