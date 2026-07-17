BEGIN;

ALTER TABLE hosted_interaction.interactions
    DISABLE TRIGGER hosted_interaction_one_way;

UPDATE hosted_interaction.interactions
SET initiator_client_session_id = NULL
WHERE initiator_kind = 'user';

ALTER TABLE hosted_interaction.interactions
    ENABLE TRIGGER hosted_interaction_one_way;

ALTER TABLE hosted_interaction.interactions
    ALTER COLUMN initiator_client_session_id DROP NOT NULL,
    DROP CONSTRAINT IF EXISTS interactions_initiator_client_session_id_check,
    ADD CONSTRAINT hosted_interactions_actor_session_shape CHECK (
        (initiator_kind = 'client'
            AND route_id = 'hosted.auth'
            AND initiator_client_session_id IS NOT NULL
            AND char_length(initiator_client_session_id) BETWEEN 1 AND 160
            AND initiator_user_id IS NULL
            AND initiator_user_session_id IS NULL)
        OR
        (initiator_kind = 'user'
            AND route_id = 'hosted.account'
            AND initiator_client_session_id IS NULL
            AND initiator_user_id IS NOT NULL
            AND initiator_user_session_id IS NOT NULL)
    );

CREATE OR REPLACE FUNCTION hosted_interaction.enforce_interaction_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'hosted interactions are immutable facts';
    END IF;
    IF NEW.interaction_id <> OLD.interaction_id
        OR NEW.route_id <> OLD.route_id
        OR NEW.product_id <> OLD.product_id
        OR NEW.application_id <> OLD.application_id
        OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.environment <> OLD.environment
        OR NEW.channel <> OLD.channel
        OR NEW.initiator_kind <> OLD.initiator_kind
        OR NEW.initiator_client_session_id IS DISTINCT FROM OLD.initiator_client_session_id
        OR NEW.initiator_user_id IS DISTINCT FROM OLD.initiator_user_id
        OR NEW.initiator_user_session_id IS DISTINCT FROM OLD.initiator_user_session_id
        OR NEW.return_target_code <> OLD.return_target_code
        OR NEW.return_target_uri <> OLD.return_target_uri
        OR NEW.return_target_policy_version <> OLD.return_target_policy_version
        OR NEW.state_protector_key_ref <> OLD.state_protector_key_ref
        OR NEW.state_ciphertext <> OLD.state_ciphertext
        OR NEW.state_digest <> OLD.state_digest
        OR NEW.nonce_digest IS DISTINCT FROM OLD.nonce_digest
        OR NEW.pkce_challenge_digest IS DISTINCT FROM OLD.pkce_challenge_digest
        OR NEW.pkce_method IS DISTINCT FROM OLD.pkce_method
        OR NEW.locale IS DISTINCT FROM OLD.locale
        OR NEW.theme_variant IS DISTINCT FROM OLD.theme_variant
        OR NEW.trace_id <> OLD.trace_id
        OR NEW.created_at <> OLD.created_at
        OR NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'hosted interaction scope and security facts are immutable';
    END IF;
    IF NEW.version <> OLD.version + 1 THEN
        RAISE EXCEPTION 'hosted interaction version must advance once';
    END IF;
    IF OLD.status IN ('exchanged', 'cancelled', 'failed', 'expired') THEN
        RAISE EXCEPTION 'hosted interaction terminal state is immutable';
    END IF;
    IF NOT (
        (OLD.status = 'created' AND NEW.status IN ('opened', 'cancelled', 'failed', 'expired'))
        OR (OLD.status = 'opened' AND NEW.status IN ('opened', 'authenticating', 'completed', 'cancelled', 'failed', 'expired'))
        OR (OLD.status = 'authenticating' AND NEW.status IN ('opened', 'completed', 'cancelled', 'failed', 'expired'))
        OR (OLD.status = 'completed' AND NEW.status IN ('exchanged', 'expired'))
    ) THEN
        RAISE EXCEPTION 'invalid hosted interaction transition % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;

COMMIT;
