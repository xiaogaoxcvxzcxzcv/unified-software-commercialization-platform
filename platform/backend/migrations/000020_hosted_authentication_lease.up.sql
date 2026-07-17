BEGIN;

ALTER TABLE hosted_interaction.interactions
    ADD COLUMN authentication_lease_digest BYTEA,
    ADD COLUMN authentication_started_at TIMESTAMPTZ,
    ADD COLUMN authentication_lease_expires_at TIMESTAMPTZ;

UPDATE hosted_interaction.interactions
SET status = 'opened', version = version + 1
WHERE status = 'authenticating';

ALTER TABLE hosted_interaction.interactions
    ADD CONSTRAINT hosted_interactions_authentication_lease_shape CHECK (
        (status = 'authenticating'
            AND authentication_lease_digest IS NOT NULL
            AND octet_length(authentication_lease_digest) = 32
            AND authentication_started_at IS NOT NULL
            AND authentication_lease_expires_at IS NOT NULL
            AND authentication_lease_expires_at > authentication_started_at)
        OR
        (status <> 'authenticating'
            AND authentication_lease_digest IS NULL
            AND authentication_started_at IS NULL
            AND authentication_lease_expires_at IS NULL)
    );

COMMIT;
