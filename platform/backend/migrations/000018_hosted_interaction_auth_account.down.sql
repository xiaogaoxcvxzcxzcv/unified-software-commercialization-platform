BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM hosted_interaction.interactions)
        OR EXISTS (SELECT 1 FROM hosted_interaction.browser_sessions)
        OR EXISTS (SELECT 1 FROM hosted_interaction.completion_grants)
        OR EXISTS (SELECT 1 FROM hosted_interaction.idempotency_records)
        OR EXISTS (SELECT 1 FROM hosted_interaction.outbox_events)
        OR EXISTS (SELECT 1 FROM identity.hosted_auth_proofs)
        OR EXISTS (SELECT 1 FROM identity.hosted_grant_redemptions) THEN
        RAISE EXCEPTION 'migration 000018 rollback refused: durable hosted interaction or identity grant state exists';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS identity_hosted_grant_redemption_immutable ON identity.hosted_grant_redemptions;
DROP FUNCTION IF EXISTS identity.reject_hosted_grant_redemption_mutation();
DROP TRIGGER IF EXISTS identity_hosted_auth_proof_one_way ON identity.hosted_auth_proofs;
DROP FUNCTION IF EXISTS identity.enforce_hosted_auth_proof_transition();
DROP TABLE IF EXISTS identity.hosted_grant_redemptions;
DROP TABLE IF EXISTS identity.hosted_auth_proofs;

DROP SCHEMA hosted_interaction CASCADE;

COMMIT;
