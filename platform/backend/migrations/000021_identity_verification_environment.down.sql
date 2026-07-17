BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM identity.external_identity_proofs)
        OR EXISTS (
            SELECT 1 FROM identity.registration_verification_challenges
            WHERE environment IS NOT NULL
        ) THEN
        RAISE EXCEPTION 'migration 000021 rollback refused: Identity verification environment facts exist';
    END IF;
END;
$$;

DROP INDEX IF EXISTS identity.external_identity_proofs_environment_scope_active_idx;
DROP INDEX IF EXISTS identity.registration_verification_environment_scope_idx;

CREATE OR REPLACE FUNCTION identity.enforce_external_proof_flow_scope() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM identity.external_auth_flows f
        WHERE f.flow_id = NEW.flow_id
          AND f.product_id = NEW.product_id
          AND f.application_id = NEW.application_id
          AND f.tenant_id IS NOT DISTINCT FROM NEW.tenant_id
          AND f.provider = NEW.provider
          AND f.provider_application_ref = NEW.provider_application_ref
    ) THEN
        RAISE EXCEPTION 'external identity proof scope must match its flow';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE identity.registration_verification_challenges
    DROP CONSTRAINT IF EXISTS registration_verification_environment_valid,
    DROP COLUMN IF EXISTS environment;

ALTER TABLE identity.external_identity_proofs
    DROP CONSTRAINT IF EXISTS external_identity_proofs_environment_valid,
    DROP COLUMN IF EXISTS environment;

COMMIT;
