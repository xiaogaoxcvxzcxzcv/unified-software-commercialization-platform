BEGIN;

ALTER TABLE identity.external_identity_proofs
    ADD COLUMN environment TEXT;

UPDATE identity.external_identity_proofs p
SET environment = f.environment
FROM identity.external_auth_flows f
WHERE f.flow_id = p.flow_id;

ALTER TABLE identity.external_identity_proofs
    ALTER COLUMN environment SET NOT NULL,
    ADD CONSTRAINT external_identity_proofs_environment_valid
        CHECK (environment IN ('local', 'test', 'production'));

ALTER TABLE identity.registration_verification_challenges
    ADD COLUMN environment TEXT,
    ADD CONSTRAINT registration_verification_environment_valid
        CHECK (environment IS NULL OR environment IN ('local', 'test', 'production'));

CREATE INDEX external_identity_proofs_environment_scope_active_idx
    ON identity.external_identity_proofs (environment, product_id, application_id, provider, expires_at)
    WHERE consumed_at IS NULL;

CREATE INDEX registration_verification_environment_scope_idx
    ON identity.registration_verification_challenges (environment, product_id, application_id, identifier_type, identifier_digest, expires_at);

CREATE OR REPLACE FUNCTION identity.enforce_external_proof_flow_scope() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM identity.external_auth_flows f
        WHERE f.flow_id = NEW.flow_id
          AND f.product_id = NEW.product_id
          AND f.application_id = NEW.application_id
          AND f.tenant_id IS NOT DISTINCT FROM NEW.tenant_id
          AND f.environment = NEW.environment
          AND f.provider = NEW.provider
          AND f.provider_application_ref = NEW.provider_application_ref
    ) THEN
        RAISE EXCEPTION 'external identity proof scope must match its flow';
    END IF;
    RETURN NEW;
END;
$$;

COMMIT;
