BEGIN;

CREATE TABLE hosted_interaction.self_service_flows (
    interaction_id TEXT PRIMARY KEY REFERENCES hosted_interaction.interactions(interaction_id),
    flow_kind TEXT NOT NULL CHECK (flow_kind IN ('registration_verification', 'recovery_verification')),
    protected_key_ref TEXT NOT NULL CHECK (char_length(protected_key_ref) BETWEEN 1 AND 128),
    protected_ciphertext BYTEA NOT NULL CHECK (octet_length(protected_ciphertext) BETWEEN 32 AND 8192),
    protected_digest BYTEA NOT NULL CHECK (octet_length(protected_digest) = 32),
    identifier_hint TEXT NOT NULL CHECK (char_length(identifier_hint) BETWEEN 1 AND 160),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    CHECK (updated_at >= created_at),
    CHECK (expires_at > updated_at)
);

CREATE INDEX hosted_self_service_flows_expiry_idx
    ON hosted_interaction.self_service_flows (expires_at, interaction_id);

ALTER TABLE hosted_interaction.idempotency_records
    DROP CONSTRAINT idempotency_records_operation_check;

ALTER TABLE hosted_interaction.idempotency_records
    ADD CONSTRAINT idempotency_records_operation_check
    CHECK (operation IN ('create', 'account_complete', 'auth_flow_reset', 'cancel'));

COMMIT;
