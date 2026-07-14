BEGIN;

CREATE TABLE identity.admin_auth_clients (
    client_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 128),
    client_type TEXT NOT NULL CHECK (client_type IN ('cli', 'automation')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    disabled_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    CHECK (client_id ~ '^acli_[a-z0-9][a-z0-9_-]{7,119}$'),
    CHECK (expires_at IS NULL OR expires_at > created_at),
    CHECK ((status = 'active' AND disabled_at IS NULL) OR (status = 'disabled' AND disabled_at IS NOT NULL))
);

CREATE TABLE identity.admin_auth_client_credentials (
    credential_id TEXT PRIMARY KEY,
    client_id TEXT NOT NULL REFERENCES identity.admin_auth_clients(client_id) ON DELETE RESTRICT,
    proof_type TEXT NOT NULL CHECK (proof_type = 'shared_secret_v1'),
    secret_digest BYTEA NOT NULL UNIQUE CHECK (octet_length(secret_digest) = 32),
    digest_version SMALLINT NOT NULL DEFAULT 1 CHECK (digest_version = 1),
    created_at TIMESTAMPTZ NOT NULL,
    not_before TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    CHECK (credential_id ~ '^acred_[a-z0-9][a-z0-9_-]{7,119}$'),
    CHECK (not_before >= created_at),
    CHECK (expires_at IS NULL OR expires_at > not_before),
    UNIQUE (client_id, credential_id)
);

CREATE INDEX admin_auth_client_credentials_active_idx
    ON identity.admin_auth_client_credentials (client_id, not_before, expires_at)
    WHERE revoked_at IS NULL;

ALTER TABLE identity.admin_sessions
    ADD COLUMN controlled_client_id TEXT,
    ADD COLUMN controlled_client_credential_id TEXT;

WITH legacy_families AS (
    UPDATE identity.admin_sessions
       SET revoked_at = COALESCE(revoked_at, CURRENT_TIMESTAMP),
           revoke_reason = COALESCE(revoke_reason, 'bearer_client_binding_required'),
           last_seen_at = CURRENT_TIMESTAMP
     WHERE transport = 'bearer'
       AND controlled_client_id IS NULL
    RETURNING token_family_id, revoked_at
)
UPDATE identity.admin_session_tokens AS token
   SET revoked_at = COALESCE(token.revoked_at, legacy.revoked_at)
  FROM legacy_families AS legacy
 WHERE token.token_family_id = legacy.token_family_id;

ALTER TABLE identity.admin_sessions
    ADD CONSTRAINT admin_sessions_controlled_client_fk
        FOREIGN KEY (controlled_client_id, controlled_client_credential_id)
        REFERENCES identity.admin_auth_client_credentials(client_id, credential_id)
        ON DELETE RESTRICT,
    ADD CONSTRAINT admin_sessions_controlled_client_binding_check
        CHECK (
            (transport = 'cookie'
                AND controlled_client_id IS NULL
                AND controlled_client_credential_id IS NULL)
            OR
            (transport = 'bearer'
                AND (
                    (controlled_client_id IS NOT NULL
                        AND controlled_client_credential_id IS NOT NULL)
                    OR
                    (revoked_at IS NOT NULL
                        AND controlled_client_id IS NULL
                        AND controlled_client_credential_id IS NULL)
                ))
        );

CREATE INDEX admin_sessions_controlled_client_active_idx
    ON identity.admin_sessions (controlled_client_id, controlled_client_credential_id, refresh_expires_at)
    WHERE transport = 'bearer' AND revoked_at IS NULL;

COMMIT;
