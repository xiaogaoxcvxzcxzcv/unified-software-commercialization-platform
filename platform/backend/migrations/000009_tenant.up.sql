BEGIN;

CREATE SCHEMA tenant;

CREATE TABLE tenant.product_tenants (
    tenant_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    tenant_code TEXT NOT NULL CHECK (tenant_code ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    tenant_type TEXT NOT NULL CHECK (tenant_type IN ('official', 'agent')),
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended')),
    external_agent_ref TEXT,
    context_version BIGINT NOT NULL DEFAULT 1 CHECK (context_version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (product_id, tenant_code),
    UNIQUE (product_id, tenant_id)
);

CREATE UNIQUE INDEX product_tenants_one_official_idx ON tenant.product_tenants (product_id) WHERE tenant_type = 'official';

CREATE TABLE tenant.distribution_bindings (
    binding_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    application_id TEXT,
    channel_code TEXT NOT NULL,
    proof_subject_digest TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended')),
    context_version BIGINT NOT NULL DEFAULT 1 CHECK (context_version > 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (product_id, channel_code, proof_subject_digest)
);

CREATE TABLE tenant.idempotency_records (
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

CREATE TABLE tenant.outbox_events (
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

CREATE INDEX tenant_outbox_pending_idx ON tenant.outbox_events (next_attempt_at, occurred_at) WHERE published_at IS NULL AND dead = FALSE;

CREATE FUNCTION tenant.reject_tenant_identity_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id <> OLD.tenant_id OR NEW.product_id <> OLD.product_id OR NEW.tenant_code <> OLD.tenant_code OR NEW.tenant_type <> OLD.tenant_type THEN
        RAISE EXCEPTION 'tenant identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER product_tenants_identity_immutable
BEFORE UPDATE ON tenant.product_tenants
FOR EACH ROW EXECUTE FUNCTION tenant.reject_tenant_identity_update();

COMMIT;
