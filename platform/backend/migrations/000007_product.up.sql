BEGIN;

CREATE SCHEMA product;

CREATE TABLE product.products (
    product_id TEXT PRIMARY KEY,
    product_code TEXT NOT NULL UNIQUE CHECK (product_code ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended')),
    provisioning_state TEXT NOT NULL CHECK (provisioning_state IN ('pending', 'ready', 'failed')),
    official_tenant_id TEXT,
    context_version BIGINT NOT NULL DEFAULT 1 CHECK (context_version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE product.product_environments (
    product_id TEXT NOT NULL,
    environment TEXT NOT NULL CHECK (environment IN ('local', 'test', 'production')),
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended')),
    context_version BIGINT NOT NULL DEFAULT 1 CHECK (context_version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (product_id, environment)
);

CREATE TABLE product.product_clients (
    client_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    environment TEXT NOT NULL CHECK (environment IN ('local', 'test', 'production')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'active', 'suspended')),
    context_version BIGINT NOT NULL DEFAULT 1 CHECK (context_version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (product_id, client_id)
);

CREATE INDEX product_clients_scope_idx ON product.product_clients (product_id, environment, status);

CREATE TABLE product.product_client_credentials (
    credential_id TEXT PRIMARY KEY,
    client_id TEXT NOT NULL,
    product_id TEXT NOT NULL,
    proof_type TEXT NOT NULL CHECK (proof_type IN ('hmac_sha256_v1', 'ed25519_signature_v1')),
    proof_digest TEXT,
    public_key TEXT,
    generation INTEGER NOT NULL CHECK (generation > 0),
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked')),
    not_before TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK ((proof_type = 'hmac_sha256_v1' AND proof_digest IS NOT NULL AND public_key IS NULL) OR (proof_type = 'ed25519_signature_v1' AND proof_digest IS NULL AND public_key IS NOT NULL)),
    CHECK (expires_at > not_before),
    UNIQUE (client_id, generation),
    UNIQUE (product_id, client_id, credential_id)
);

CREATE INDEX product_client_credentials_active_idx ON product.product_client_credentials (client_id, status, expires_at);

CREATE TABLE product.client_proof_nonces (
    client_id TEXT NOT NULL,
    nonce_digest TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    session_id TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (client_id, nonce_digest)
);

CREATE TABLE product.client_sessions (
    session_id TEXT PRIMARY KEY,
    token_digest TEXT NOT NULL UNIQUE,
    product_id TEXT NOT NULL,
    environment TEXT NOT NULL CHECK (environment IN ('local', 'test', 'production')),
    application_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    client_id TEXT NOT NULL,
    credential_id TEXT NOT NULL,
    client_version TEXT NOT NULL,
    product_context_version BIGINT NOT NULL CHECK (product_context_version > 0),
    application_context_version BIGINT NOT NULL CHECK (application_context_version > 0),
    tenant_context_version BIGINT NOT NULL CHECK (tenant_context_version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    CHECK (expires_at > created_at)
);

CREATE INDEX client_sessions_active_idx ON product.client_sessions (client_id, expires_at) WHERE revoked_at IS NULL;

CREATE TABLE product.product_capability_sets (
    capability_set_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    source_plan_id TEXT NOT NULL,
    catalog_revision TEXT NOT NULL,
    catalog_snapshot_sha256 TEXT NOT NULL CHECK (catalog_snapshot_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    content_sha256 TEXT NOT NULL CHECK (content_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (product_id, version)
);

CREATE TABLE product.product_capability_items (
    capability_set_id TEXT NOT NULL REFERENCES product.product_capability_sets(capability_set_id),
    product_id TEXT NOT NULL,
    capability_id TEXT NOT NULL,
    enabled BOOLEAN NOT NULL,
    policy JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_package_id TEXT NOT NULL,
    source_package_version TEXT NOT NULL,
    PRIMARY KEY (capability_set_id, capability_id)
);

CREATE TABLE product.idempotency_records (
    operation TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    scope_id TEXT NOT NULL,
    key_digest TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    resource_id TEXT,
    state TEXT NOT NULL CHECK (state IN ('pending', 'completed', 'failed')),
    response_json JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (operation, actor_id, scope_id, key_digest)
);

CREATE TABLE product.outbox_events (
    event_id TEXT PRIMARY KEY,
    aggregate_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    dead BOOLEAN NOT NULL DEFAULT FALSE,
    last_error TEXT
);

CREATE INDEX product_outbox_pending_idx ON product.outbox_events (next_attempt_at, occurred_at) WHERE published_at IS NULL AND dead = FALSE;

CREATE FUNCTION product.reject_product_identity_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.product_id <> OLD.product_id OR NEW.product_code <> OLD.product_code THEN
        RAISE EXCEPTION 'product identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER products_identity_immutable
BEFORE UPDATE ON product.products
FOR EACH ROW EXECUTE FUNCTION product.reject_product_identity_update();

COMMIT;
