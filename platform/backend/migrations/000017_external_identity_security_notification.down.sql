BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM identity.external_auth_flows)
        OR EXISTS (SELECT 1 FROM identity.external_identity_proofs)
        OR EXISTS (SELECT 1 FROM identity.registration_verification_challenges)
        OR EXISTS (SELECT 1 FROM identity.end_user_sessions WHERE external_identity_id IS NOT NULL)
        OR EXISTS (SELECT 1 FROM identity.recovery_challenges WHERE delivery_status = 'pending')
        OR EXISTS (SELECT 1 FROM notification.security_deliveries)
        OR EXISTS (SELECT 1 FROM notification.security_delivery_attempts)
        OR EXISTS (SELECT 1 FROM notification.outbox_events)
        OR EXISTS (
            SELECT 1 FROM product_application.redirect_policy_entries
            WHERE entry_type = 'auth_return_target'
        ) THEN
        RAISE EXCEPTION 'migration 000017 rollback refused because durable external identity, auth target, or security delivery state exists';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS security_delivery_attempt_immutable ON notification.security_delivery_attempts;
DROP TRIGGER IF EXISTS security_delivery_one_way ON notification.security_deliveries;
DROP TRIGGER IF EXISTS notification_outbox_one_way ON notification.outbox_events;
DROP FUNCTION IF EXISTS notification.enforce_security_delivery_transition();
DROP FUNCTION IF EXISTS notification.enforce_outbox_transition();
DROP FUNCTION IF EXISTS notification.reject_attempt_update();
DROP TABLE IF EXISTS notification.outbox_events;
DROP TABLE IF EXISTS notification.security_delivery_attempts;
DROP TABLE IF EXISTS notification.security_deliveries;
DROP SCHEMA IF EXISTS notification;

DROP TRIGGER IF EXISTS product_application_outbox_one_way ON product_application.outbox_events;
DROP FUNCTION IF EXISTS product_application.enforce_outbox_transition();
ALTER TABLE product_application.outbox_events
    DROP CONSTRAINT IF EXISTS product_application_outbox_terminal_shape,
    DROP CONSTRAINT IF EXISTS product_application_outbox_lease_shape,
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS lease_token;

DROP TRIGGER IF EXISTS registration_verification_one_way ON identity.registration_verification_challenges;
DROP TRIGGER IF EXISTS external_identity_proof_one_way ON identity.external_identity_proofs;
DROP TRIGGER IF EXISTS external_auth_flow_one_way ON identity.external_auth_flows;
DROP TRIGGER IF EXISTS external_identity_proof_flow_scope ON identity.external_identity_proofs;
DROP TRIGGER IF EXISTS recovery_delivery_one_way ON identity.recovery_challenges;
DROP FUNCTION IF EXISTS identity.enforce_recovery_delivery_transition();
DROP FUNCTION IF EXISTS identity.reject_registration_verification_reuse();
DROP FUNCTION IF EXISTS identity.enforce_external_identity_proof_transition();
DROP FUNCTION IF EXISTS identity.enforce_external_auth_flow_transition();
DROP FUNCTION IF EXISTS identity.enforce_external_proof_flow_scope();
DROP TABLE IF EXISTS identity.registration_verification_challenges;
DROP TABLE IF EXISTS identity.external_identity_proofs;
DROP TABLE IF EXISTS identity.external_auth_flows;

ALTER TABLE identity.recovery_challenges
    DROP COLUMN IF EXISTS delivery_status;

ALTER TABLE identity.end_user_sessions
    DROP CONSTRAINT IF EXISTS end_user_sessions_external_identity_shape,
    DROP COLUMN IF EXISTS external_identity_id;

DROP INDEX IF EXISTS product_application.redirect_policy_entries_auth_target_code_idx;
ALTER TABLE product_application.redirect_policy_entries
    DROP CONSTRAINT IF EXISTS redirect_policy_entries_auth_target_shape,
    DROP CONSTRAINT redirect_policy_entries_entry_type_check,
    DROP COLUMN IF EXISTS target_code,
    ADD CONSTRAINT redirect_policy_entries_entry_type_check
        CHECK (entry_type IN ('web_redirect', 'origin', 'deep_link'));

COMMIT;
