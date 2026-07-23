BEGIN;

CREATE SCHEMA entitlement;

CREATE TABLE entitlement.features (
    feature_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    feature_code TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('boolean', 'limit', 'quota', 'device_policy')),
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 120),
    status TEXT NOT NULL CHECK (status IN ('active', 'deprecated', 'disabled')),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (product_id, feature_code),
    CHECK (char_length(feature_code) BETWEEN 1 AND 128),
    CHECK (feature_code !~ '[[:cntrl:]]')
);

CREATE TABLE entitlement.policies (
    policy_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    policy_code TEXT NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    status TEXT NOT NULL CHECK (status IN ('draft', 'active', 'retired')),
    features JSONB NOT NULL CHECK (jsonb_typeof(features) = 'array'),
    validity_rule TEXT NOT NULL CHECK (validity_rule IN ('fixed_duration', 'fixed_end', 'lifetime')),
    validity_seconds BIGINT CHECK (validity_seconds IS NULL OR validity_seconds > 0),
    fixed_valid_until TIMESTAMPTZ,
    stacking_rule TEXT NOT NULL CHECK (stacking_rule IN ('union_latest_expiry', 'replace_same_group', 'reject_conflict')),
    mutual_exclusion_group TEXT CHECK (mutual_exclusion_group IS NULL OR (char_length(mutual_exclusion_group) BETWEEN 1 AND 128 AND mutual_exclusion_group !~ '[[:cntrl:]]')),
    priority INTEGER NOT NULL DEFAULT 0,
    revoke_scope TEXT NOT NULL CHECK (revoke_scope IN ('source_only', 'conclusion_group', 'all_user_entitlements')),
    offline_grace_max_seconds BIGINT NOT NULL DEFAULT 0 CHECK (offline_grace_max_seconds >= 0),
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (product_id, tenant_id, policy_code, version),
    CHECK (char_length(policy_code) BETWEEN 1 AND 128),
    CHECK (policy_code !~ '[[:cntrl:]]'),
    CHECK (
        (validity_rule = 'fixed_duration' AND validity_seconds IS NOT NULL AND fixed_valid_until IS NULL)
        OR (validity_rule = 'fixed_end' AND validity_seconds IS NULL AND fixed_valid_until IS NOT NULL)
        OR (validity_rule = 'lifetime' AND validity_seconds IS NULL AND fixed_valid_until IS NULL)
    ),
    CHECK ((status = 'active') = (published_at IS NOT NULL))
);

CREATE TABLE entitlement.grants (
    grant_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    policy_id TEXT NOT NULL,
    policy_version BIGINT NOT NULL CHECK (policy_version > 0),
    effect TEXT NOT NULL CHECK (effect IN ('grant', 'extend', 'replace', 'revoke', 'expire')),
    source_type TEXT NOT NULL CHECK (source_type IN ('admin', 'trial', 'gift', 'order', 'license')),
    source_id TEXT NOT NULL,
    source_effect_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    valid_from TIMESTAMPTZ NOT NULL,
    valid_until TIMESTAMPTZ,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('admin', 'system', 'user')),
    actor_id TEXT NOT NULL,
    reason_code TEXT NOT NULL CHECK (char_length(reason_code) BETWEEN 1 AND 64),
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (product_id, tenant_id, user_id, source_type, source_id, source_effect_id),
    UNIQUE (product_id, tenant_id, user_id, idempotency_key),
    CHECK (char_length(source_id) BETWEEN 1 AND 160),
    CHECK (char_length(source_effect_id) BETWEEN 1 AND 160),
    CHECK (char_length(idempotency_key) BETWEEN 1 AND 160),
    CHECK (valid_until IS NULL OR valid_until > valid_from)
);

CREATE TABLE entitlement.revisions (
    revision_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    decision_hash BYTEA NOT NULL CHECK (octet_length(decision_hash) = 32),
    effective_features JSONB NOT NULL CHECK (jsonb_typeof(effective_features) = 'object'),
    plan_code TEXT,
    valid_until TIMESTAMPTZ,
    offline_grace_until TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (product_id, tenant_id, user_id),
    CHECK (plan_code IS NULL OR (char_length(plan_code) BETWEEN 1 AND 128 AND plan_code !~ '[[:cntrl:]]')),
    CHECK (offline_grace_until IS NULL OR valid_until IS NULL OR offline_grace_until >= valid_until)
);

CREATE TABLE entitlement.ledger (
    ledger_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    operation_type TEXT NOT NULL CHECK (operation_type IN ('grant', 'extend', 'replace', 'revoke', 'expire', 'policy_publish', 'policy_conflict', 'idempotency_replay')),
    operation_id TEXT NOT NULL,
    source_type TEXT CHECK (source_type IS NULL OR source_type IN ('admin', 'trial', 'gift', 'order', 'license')),
    source_id TEXT,
    grant_id TEXT,
    before_revision BIGINT CHECK (before_revision IS NULL OR before_revision > 0),
    after_revision BIGINT CHECK (after_revision IS NULL OR after_revision > 0),
    before_decision_hash BYTEA CHECK (before_decision_hash IS NULL OR octet_length(before_decision_hash) = 32),
    after_decision_hash BYTEA CHECK (after_decision_hash IS NULL OR octet_length(after_decision_hash) = 32),
    audit_id TEXT,
    trace_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (char_length(operation_id) BETWEEN 1 AND 160),
    CHECK (after_revision IS NULL OR before_revision IS NULL OR after_revision >= before_revision)
);

CREATE INDEX entitlement_ledger_scope_idx
    ON entitlement.ledger (product_id, tenant_id, user_id, created_at, ledger_id);

CREATE TABLE entitlement.idempotency_records (
    product_id TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    operation TEXT NOT NULL CHECK (operation IN ('grant', 'extend', 'replace', 'revoke', 'expire')),
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    result_grant_id TEXT,
    result_revision BIGINT CHECK (result_revision IS NULL OR result_revision > 0),
    response_document JSONB NOT NULL CHECK (jsonb_typeof(response_document) = 'object'),
    state TEXT NOT NULL CHECK (state IN ('pending', 'completed', 'failed')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (product_id, tenant_id, user_id, idempotency_key),
    CHECK (char_length(idempotency_key) BETWEEN 1 AND 160)
);

CREATE TABLE entitlement.outbox_events (
    event_id TEXT PRIMARY KEY,
    aggregate_id TEXT NOT NULL,
    event_type TEXT NOT NULL CHECK (event_type IN (
        'entitlement.granted.v1',
        'entitlement.extended.v1',
        'entitlement.replaced.v1',
        'entitlement.revoked.v1',
        'entitlement.expired.v1',
        'entitlement.policy-published.v1'
    )),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    dead BOOLEAN NOT NULL DEFAULT FALSE,
    last_error TEXT
);

CREATE INDEX entitlement_outbox_pending_idx
    ON entitlement.outbox_events (next_attempt_at, occurred_at)
    WHERE published_at IS NULL AND dead = FALSE;

CREATE FUNCTION entitlement.reject_scope_identity_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'features' THEN
        IF NEW.product_id <> OLD.product_id OR NEW.feature_code <> OLD.feature_code THEN
            RAISE EXCEPTION 'entitlement feature scope identity is immutable';
        END IF;
    ELSIF TG_TABLE_NAME = 'policies' THEN
        IF NEW.product_id <> OLD.product_id OR NEW.tenant_id <> OLD.tenant_id OR NEW.policy_code <> OLD.policy_code OR NEW.version <> OLD.version THEN
            RAISE EXCEPTION 'entitlement policy scope identity is immutable';
        END IF;
    ELSIF TG_TABLE_NAME = 'revisions' THEN
        IF NEW.product_id <> OLD.product_id OR NEW.tenant_id <> OLD.tenant_id OR NEW.user_id <> OLD.user_id THEN
            RAISE EXCEPTION 'entitlement revision scope identity is immutable';
        END IF;
        IF NEW.version <= OLD.version THEN
            RAISE EXCEPTION 'entitlement revision version must increase';
        END IF;
    ELSE
        IF NEW.product_id <> OLD.product_id OR NEW.tenant_id <> OLD.tenant_id OR NEW.user_id <> OLD.user_id OR NEW.idempotency_key <> OLD.idempotency_key THEN
            RAISE EXCEPTION 'entitlement idempotency scope identity is immutable';
        END IF;
        IF NEW.request_hash <> OLD.request_hash THEN
            RAISE EXCEPTION 'entitlement idempotency request hash is immutable';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION entitlement.reject_append_only_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'entitlement append-only relation cannot be updated or deleted';
END;
$$;

CREATE TRIGGER entitlement_features_identity_immutable
BEFORE UPDATE ON entitlement.features
FOR EACH ROW EXECUTE FUNCTION entitlement.reject_scope_identity_update();

CREATE TRIGGER entitlement_policies_identity_immutable
BEFORE UPDATE ON entitlement.policies
FOR EACH ROW EXECUTE FUNCTION entitlement.reject_scope_identity_update();

CREATE TRIGGER entitlement_revisions_identity_immutable
BEFORE UPDATE ON entitlement.revisions
FOR EACH ROW EXECUTE FUNCTION entitlement.reject_scope_identity_update();

CREATE TRIGGER entitlement_idempotency_identity_immutable
BEFORE UPDATE ON entitlement.idempotency_records
FOR EACH ROW EXECUTE FUNCTION entitlement.reject_scope_identity_update();

CREATE TRIGGER entitlement_grants_append_only
BEFORE UPDATE OR DELETE ON entitlement.grants
FOR EACH ROW EXECUTE FUNCTION entitlement.reject_append_only_change();

CREATE TRIGGER entitlement_ledger_append_only
BEFORE UPDATE OR DELETE ON entitlement.ledger
FOR EACH ROW EXECUTE FUNCTION entitlement.reject_append_only_change();

COMMIT;
