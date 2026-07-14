BEGIN;

CREATE SCHEMA assembly;

CREATE TABLE assembly.product_blueprints (
    blueprint_id TEXT NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    product_id TEXT,
    document_version TEXT NOT NULL,
    schema_version TEXT NOT NULL,
    document TEXT NOT NULL CHECK (jsonb_typeof(document::jsonb) = 'object'),
    content_sha256 TEXT NOT NULL CHECK (content_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (blueprint_id, revision)
);

CREATE UNIQUE INDEX product_blueprints_content_idx
    ON assembly.product_blueprints (blueprint_id, content_sha256);
CREATE INDEX product_blueprints_product_idx
    ON assembly.product_blueprints (product_id, created_at) WHERE product_id IS NOT NULL;

CREATE TABLE assembly.assembly_plans (
    plan_id TEXT PRIMARY KEY,
    blueprint_id TEXT NOT NULL,
    blueprint_revision BIGINT NOT NULL,
    product_id TEXT,
    version BIGINT NOT NULL CHECK (version > 0),
    environment TEXT NOT NULL CHECK (environment IN ('development', 'test', 'staging', 'production')),
    schema_version TEXT NOT NULL,
    document TEXT NOT NULL CHECK (jsonb_typeof(document::jsonb) = 'object'),
    blueprint_sha256 TEXT NOT NULL CHECK (blueprint_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    catalog_revision TEXT NOT NULL,
    catalog_snapshot_sha256 TEXT NOT NULL CHECK (catalog_snapshot_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    plan_sha256 TEXT NOT NULL CHECK (plan_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    executable BOOLEAN NOT NULL,
    confirmed_at TIMESTAMPTZ,
    confirmed_by TEXT,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (blueprint_id, blueprint_revision)
        REFERENCES assembly.product_blueprints (blueprint_id, revision),
    CHECK ((confirmed_at IS NULL AND confirmed_by IS NULL) OR (confirmed_at IS NOT NULL AND confirmed_by IS NOT NULL))
);

CREATE INDEX assembly_plans_blueprint_idx
    ON assembly.assembly_plans (blueprint_id, blueprint_revision, created_at);
CREATE INDEX assembly_plans_product_idx
    ON assembly.assembly_plans (product_id, created_at) WHERE product_id IS NOT NULL;

CREATE TABLE assembly.plan_capabilities (
    plan_id TEXT NOT NULL REFERENCES assembly.assembly_plans(plan_id),
    product_id TEXT,
    capability_id TEXT NOT NULL,
    enabled BOOLEAN NOT NULL,
    policy JSONB NOT NULL,
    source_package_id TEXT NOT NULL,
    source_package_version TEXT NOT NULL,
    PRIMARY KEY (plan_id, capability_id)
);

CREATE TABLE assembly.assembly_runs (
    run_id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL REFERENCES assembly.assembly_plans(plan_id),
    plan_version BIGINT NOT NULL CHECK (plan_version > 0),
    product_id TEXT,
    version BIGINT NOT NULL CHECK (version > 0),
    plan_sha256 TEXT NOT NULL CHECK (plan_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    schema_version TEXT NOT NULL,
    document TEXT NOT NULL CHECK (jsonb_typeof(document::jsonb) = 'object'),
    document_sha256 TEXT NOT NULL CHECK (document_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    idempotency_key_digest TEXT NOT NULL CHECK (idempotency_key_digest ~ '^sha256:[a-f0-9]{64}$'),
    output_target_ref TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('planned','provisioning','generating','validating','completed','failed','rolling_back','rolled_back')),
    current_step_id TEXT,
    diagnostic_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    recovery JSONB NOT NULL,
    manifest_id TEXT,
    lock_id TEXT,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ
);

CREATE INDEX assembly_runs_plan_idx ON assembly.assembly_runs (plan_id, created_at);
CREATE INDEX assembly_runs_product_idx ON assembly.assembly_runs (product_id, created_at) WHERE product_id IS NOT NULL;
CREATE UNIQUE INDEX assembly_runs_idempotency_idx ON assembly.assembly_runs (plan_id, idempotency_key_digest);

CREATE TABLE assembly.assembly_run_steps (
    run_id TEXT NOT NULL REFERENCES assembly.assembly_runs(run_id) ON DELETE CASCADE,
    step_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    kind TEXT NOT NULL CHECK (kind IN ('provision','enable_capability','generate','validate','commit','rollback')),
    status TEXT NOT NULL CHECK (status IN ('pending','running','completed','failed','compensated','skipped')),
    attempt INTEGER NOT NULL CHECK (attempt >= 0),
    compensation_status TEXT NOT NULL CHECK (compensation_status IN ('not_required','pending','completed','failed')),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    diagnostic_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    PRIMARY KEY (run_id, step_id),
    UNIQUE (run_id, ordinal)
);

CREATE TABLE assembly.assembly_manifests (
    assembly_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    run_id TEXT NOT NULL UNIQUE REFERENCES assembly.assembly_runs(run_id),
    schema_version TEXT NOT NULL,
    document TEXT NOT NULL CHECK (jsonb_typeof(document::jsonb) = 'object'),
    document_sha256 TEXT NOT NULL CHECK (document_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    manifest_sha256 TEXT NOT NULL UNIQUE CHECK (manifest_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE assembly.generated_project_locks (
    lock_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    run_id TEXT NOT NULL UNIQUE REFERENCES assembly.assembly_runs(run_id),
    assembly_id TEXT NOT NULL UNIQUE REFERENCES assembly.assembly_manifests(assembly_id),
    schema_version TEXT NOT NULL,
    document TEXT NOT NULL CHECK (jsonb_typeof(document::jsonb) = 'object'),
    document_sha256 TEXT NOT NULL CHECK (document_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    lock_sha256 TEXT NOT NULL UNIQUE CHECK (lock_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE assembly.idempotency_records (
    operation TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    scope_id TEXT NOT NULL,
    key_digest TEXT NOT NULL CHECK (key_digest ~ '^sha256:[a-f0-9]{64}$'),
    request_digest TEXT NOT NULL CHECK (request_digest ~ '^sha256:[a-f0-9]{64}$'),
    resource_id TEXT,
    state TEXT NOT NULL CHECK (state IN ('pending','completed','failed')),
    response_json JSONB,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (operation, actor_id, scope_id, key_digest)
);

CREATE TABLE assembly.outbox_events (
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

CREATE INDEX assembly_outbox_pending_idx ON assembly.outbox_events (next_attempt_at, occurred_at)
    WHERE published_at IS NULL AND dead = FALSE;

CREATE FUNCTION assembly.reject_immutable_document_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.blueprint_id IS DISTINCT FROM OLD.blueprint_id
       OR NEW.revision IS DISTINCT FROM OLD.revision
       OR NEW.document_version IS DISTINCT FROM OLD.document_version
       OR NEW.document IS DISTINCT FROM OLD.document
       OR NEW.schema_version <> OLD.schema_version
       OR NEW.content_sha256 <> OLD.content_sha256
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (OLD.product_id IS NOT NULL AND NEW.product_id IS DISTINCT FROM OLD.product_id) THEN
        RAISE EXCEPTION 'validated blueprint document is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER assembly_blueprints_document_immutable
BEFORE UPDATE ON assembly.product_blueprints
FOR EACH ROW EXECUTE FUNCTION assembly.reject_immutable_document_update();

CREATE FUNCTION assembly.reject_plan_contract_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.plan_id <> OLD.plan_id
       OR NEW.blueprint_id <> OLD.blueprint_id
       OR NEW.blueprint_revision <> OLD.blueprint_revision
       OR NEW.environment <> OLD.environment
       OR NEW.schema_version <> OLD.schema_version
       OR NEW.document <> OLD.document
       OR NEW.blueprint_sha256 <> OLD.blueprint_sha256
       OR NEW.catalog_revision <> OLD.catalog_revision
       OR NEW.catalog_snapshot_sha256 <> OLD.catalog_snapshot_sha256
       OR NEW.plan_sha256 <> OLD.plan_sha256
       OR NEW.executable <> OLD.executable
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (OLD.product_id IS NOT NULL AND NEW.product_id IS DISTINCT FROM OLD.product_id)
       OR (OLD.confirmed_at IS NOT NULL AND (NEW.confirmed_at IS DISTINCT FROM OLD.confirmed_at OR NEW.confirmed_by IS DISTINCT FROM OLD.confirmed_by))
       OR ((OLD.confirmed_at IS NULL) <> (OLD.confirmed_by IS NULL))
       OR ((NEW.confirmed_at IS NULL) <> (NEW.confirmed_by IS NULL))
       OR (NEW.version IS DISTINCT FROM OLD.version AND NOT (
            OLD.confirmed_at IS NULL AND NEW.confirmed_at IS NOT NULL AND NEW.version = OLD.version + 1
       ))
       OR (NEW.updated_at IS DISTINCT FROM OLD.updated_at AND NOT (
            OLD.confirmed_at IS NULL AND NEW.confirmed_at IS NOT NULL AND NEW.updated_at = NEW.confirmed_at
       )) THEN
        RAISE EXCEPTION 'validated assembly plan contract is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER assembly_plans_contract_immutable
BEFORE UPDATE ON assembly.assembly_plans
FOR EACH ROW EXECUTE FUNCTION assembly.reject_plan_contract_update();

CREATE FUNCTION assembly.reject_plan_capability_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.plan_id IS DISTINCT FROM OLD.plan_id
       OR NEW.capability_id IS DISTINCT FROM OLD.capability_id
       OR NEW.enabled IS DISTINCT FROM OLD.enabled
       OR NEW.policy IS DISTINCT FROM OLD.policy
       OR NEW.source_package_id IS DISTINCT FROM OLD.source_package_id
       OR NEW.source_package_version IS DISTINCT FROM OLD.source_package_version
       OR (OLD.product_id IS NOT NULL AND NEW.product_id IS DISTINCT FROM OLD.product_id) THEN
        RAISE EXCEPTION 'assembly plan capability projection is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER assembly_plan_capabilities_immutable
BEFORE UPDATE ON assembly.plan_capabilities
FOR EACH ROW EXECUTE FUNCTION assembly.reject_plan_capability_update();

CREATE FUNCTION assembly.reject_run_contract_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.run_id IS DISTINCT FROM OLD.run_id
       OR NEW.plan_id IS DISTINCT FROM OLD.plan_id
       OR NEW.plan_version IS DISTINCT FROM OLD.plan_version
       OR NEW.plan_sha256 IS DISTINCT FROM OLD.plan_sha256
       OR NEW.idempotency_key_digest IS DISTINCT FROM OLD.idempotency_key_digest
       OR NEW.output_target_ref IS DISTINCT FROM OLD.output_target_ref
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (OLD.product_id IS NOT NULL AND NEW.product_id IS DISTINCT FROM OLD.product_id)
       OR (OLD.manifest_id IS NOT NULL AND NEW.manifest_id IS DISTINCT FROM OLD.manifest_id)
       OR (OLD.lock_id IS NOT NULL AND NEW.lock_id IS DISTINCT FROM OLD.lock_id) THEN
        RAISE EXCEPTION 'assembly run locked contract is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER assembly_runs_contract_immutable
BEFORE UPDATE ON assembly.assembly_runs
FOR EACH ROW EXECUTE FUNCTION assembly.reject_run_contract_update();

CREATE FUNCTION assembly.reject_all_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'immutable assembly record cannot be changed or deleted';
END;
$$;

CREATE TRIGGER assembly_manifests_immutable
BEFORE UPDATE OR DELETE ON assembly.assembly_manifests
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();

CREATE TRIGGER assembly_generated_locks_immutable
BEFORE UPDATE OR DELETE ON assembly.generated_project_locks
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();

CREATE TRIGGER assembly_blueprints_delete_immutable
BEFORE DELETE ON assembly.product_blueprints
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();

CREATE TRIGGER assembly_plans_delete_immutable
BEFORE DELETE ON assembly.assembly_plans
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();

CREATE TRIGGER assembly_plan_capabilities_delete_immutable
BEFORE DELETE ON assembly.plan_capabilities
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();

INSERT INTO access_control.admin_permissions (permission_code, description, risk_level)
VALUES
    ('assembly.blueprint.manage', 'Create and manage product assembly blueprints', 'normal'),
    ('assembly.plan', 'Create deterministic product assembly plans', 'normal'),
    ('assembly.execute', 'Execute confirmed product assembly plans', 'high'),
    ('assembly.read', 'Read product assembly blueprints, plans, runs, manifests, and locks', 'normal')
ON CONFLICT (permission_code) DO UPDATE
SET description = EXCLUDED.description,
    risk_level = EXCLUDED.risk_level;

INSERT INTO access_control.admin_role_permissions (role_id, permission_code)
SELECT role_id, permission_code
FROM access_control.admin_roles
CROSS JOIN (VALUES
    ('assembly.blueprint.manage'),
    ('assembly.plan'),
    ('assembly.execute'),
    ('assembly.read')
) AS permissions(permission_code)
WHERE role_code = 'super_admin'
ON CONFLICT DO NOTHING;

UPDATE access_control.admin_authorization_versions
SET authorization_version = authorization_version + 1,
    updated_at = CURRENT_TIMESTAMP
WHERE admin_user_id IN (
    SELECT DISTINCT binding.admin_user_id
    FROM access_control.admin_scope_bindings binding
    JOIN access_control.admin_roles role ON role.role_id = binding.role_id
    WHERE role.role_code = 'super_admin' AND binding.status = 'active'
);

COMMIT;
