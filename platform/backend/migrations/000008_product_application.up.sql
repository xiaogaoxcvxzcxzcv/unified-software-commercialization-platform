BEGIN;

CREATE SCHEMA product_application;

CREATE TABLE product_application.product_applications (
    application_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    application_code TEXT NOT NULL CHECK (application_code ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    platform TEXT NOT NULL CHECK (platform IN ('windows', 'macos', 'linux', 'web', 'h5', 'android', 'ios', 'wechat_miniprogram', 'other')),
    distribution_channel TEXT NOT NULL CHECK (distribution_channel ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$'),
    release_track TEXT NOT NULL CHECK (release_track IN ('stable', 'beta', 'internal', 'custom')),
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended')),
    context_version BIGINT NOT NULL DEFAULT 1 CHECK (context_version > 0),
    current_redirect_policy_version BIGINT NOT NULL DEFAULT 0 CHECK (current_redirect_policy_version >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (product_id, application_code),
    UNIQUE (product_id, application_id)
);

CREATE TABLE product_application.application_client_bindings (
    binding_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    application_id TEXT NOT NULL,
    client_id TEXT NOT NULL UNIQUE,
    environment TEXT NOT NULL CHECK (environment IN ('local', 'test', 'production')),
    status TEXT NOT NULL CHECK (status IN ('active', 'suspended')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (product_id, application_id, client_id, environment)
);

CREATE TABLE product_application.redirect_policy_versions (
    policy_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    application_id TEXT NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    content_sha256 TEXT NOT NULL CHECK (content_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (application_id, version)
);

CREATE TABLE product_application.redirect_policy_entries (
    policy_id TEXT NOT NULL REFERENCES product_application.redirect_policy_versions(policy_id),
    entry_type TEXT NOT NULL CHECK (entry_type IN ('web_redirect', 'origin', 'deep_link')),
    value TEXT NOT NULL,
    PRIMARY KEY (policy_id, entry_type, value)
);

CREATE TABLE product_application.idempotency_records (
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

CREATE TABLE product_application.outbox_events (
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

CREATE INDEX product_application_outbox_pending_idx ON product_application.outbox_events (next_attempt_at, occurred_at) WHERE published_at IS NULL AND dead = FALSE;

CREATE FUNCTION product_application.reject_application_identity_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.application_id <> OLD.application_id OR NEW.product_id <> OLD.product_id OR NEW.application_code <> OLD.application_code THEN
        RAISE EXCEPTION 'product application identity is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER product_applications_identity_immutable
BEFORE UPDATE ON product_application.product_applications
FOR EACH ROW EXECUTE FUNCTION product_application.reject_application_identity_update();

COMMIT;
