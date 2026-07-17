BEGIN;

DROP TRIGGER IF EXISTS identity_hosted_grant_redemption_immutable ON identity.hosted_grant_redemptions;
DROP FUNCTION IF EXISTS identity.reject_hosted_grant_redemption_mutation();
DROP TRIGGER IF EXISTS identity_hosted_auth_proof_one_way ON identity.hosted_auth_proofs;
DROP FUNCTION IF EXISTS identity.enforce_hosted_auth_proof_transition();
DROP TABLE IF EXISTS identity.hosted_grant_redemptions;
DROP TABLE IF EXISTS identity.hosted_auth_proofs;

DROP SCHEMA hosted_interaction CASCADE;

COMMIT;
