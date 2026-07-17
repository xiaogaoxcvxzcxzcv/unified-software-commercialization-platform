BEGIN;

CREATE SCHEMA product_user_access;

CREATE TABLE product_user_access.product_access (
    product_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended')),
    access_version BIGINT NOT NULL DEFAULT 1 CHECK (access_version > 0),
    reason_code TEXT NOT NULL,
    operator_note TEXT,
    status_changed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (product_id, user_id),
    CHECK (char_length(reason_code) BETWEEN 1 AND 64),
    CHECK (operator_note IS NULL OR (char_length(operator_note) BETWEEN 1 AND 500 AND operator_note !~ '[[:cntrl:]]'))
);

CREATE TABLE product_user_access.tenant_access (
    product_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended')),
    access_version BIGINT NOT NULL DEFAULT 1 CHECK (access_version > 0),
    reason_code TEXT NOT NULL,
    operator_note TEXT,
    status_changed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (product_id, tenant_id, user_id),
    CHECK (char_length(reason_code) BETWEEN 1 AND 64),
    CHECK (operator_note IS NULL OR (char_length(operator_note) BETWEEN 1 AND 500 AND operator_note !~ '[[:cntrl:]]'))
);

CREATE INDEX tenant_access_scope_status_idx
    ON product_user_access.tenant_access (product_id, tenant_id, status, user_id);

CREATE TABLE product_user_access.idempotency_records (
    operation TEXT NOT NULL,
    scope_type TEXT NOT NULL CHECK (scope_type IN ('product', 'tenant')),
    product_id TEXT NOT NULL,
    scope_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    key_digest BYTEA NOT NULL,
    request_digest BYTEA NOT NULL,
    result_version BIGINT,
    state TEXT NOT NULL CHECK (state IN ('pending', 'completed', 'failed')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (operation, scope_type, product_id, scope_id, user_id, key_digest),
    CHECK ((scope_type = 'product' AND scope_id = product_id) OR scope_type = 'tenant'),
    CHECK (octet_length(key_digest) = 32),
    CHECK (octet_length(request_digest) = 32),
    CHECK (result_version IS NULL OR result_version > 0)
);

CREATE TABLE product_user_access.outbox_events (
    event_id TEXT PRIMARY KEY,
    aggregate_id TEXT NOT NULL,
    event_type TEXT NOT NULL CHECK (event_type IN ('product-user-access.status-changed.v1', 'product-user-access.session-revocation-requested.v1')),
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    dead BOOLEAN NOT NULL DEFAULT FALSE,
    last_error TEXT
);

CREATE INDEX product_user_access_outbox_pending_idx
    ON product_user_access.outbox_events (next_attempt_at, occurred_at)
    WHERE published_at IS NULL AND dead = FALSE;

CREATE FUNCTION product_user_access.reject_scope_identity_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'product_access' THEN
        IF NEW.product_id <> OLD.product_id OR NEW.user_id <> OLD.user_id THEN
            RAISE EXCEPTION 'product access scope identity is immutable';
        END IF;
    ELSE
        IF NEW.product_id <> OLD.product_id OR NEW.tenant_id <> OLD.tenant_id OR NEW.user_id <> OLD.user_id THEN
            RAISE EXCEPTION 'tenant access scope identity is immutable';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER product_access_scope_identity_immutable
BEFORE UPDATE ON product_user_access.product_access
FOR EACH ROW EXECUTE FUNCTION product_user_access.reject_scope_identity_update();

CREATE TRIGGER tenant_access_scope_identity_immutable
BEFORE UPDATE ON product_user_access.tenant_access
FOR EACH ROW EXECUTE FUNCTION product_user_access.reject_scope_identity_update();

COMMIT;
