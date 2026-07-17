BEGIN;

ALTER TABLE assembly.assembly_runs
    DROP CONSTRAINT assembly_runs_status_check,
    ADD CONSTRAINT assembly_runs_status_check CHECK (status IN ('planned','provisioning','generating','validating','completed','failed','cancelled','rolling_back','rolled_back'));

ALTER TABLE assembly.assembly_run_dispatches
    DROP CONSTRAINT assembly_run_dispatches_state_check,
    ADD CONSTRAINT assembly_run_dispatches_state_check CHECK (state IN ('pending','leased','completed','cancelled','dead'));

ALTER TABLE assembly.assembly_manifests
    ADD CONSTRAINT assembly_manifests_id_product_unique UNIQUE (assembly_id, product_id);
ALTER TABLE assembly.generated_project_locks
    ADD CONSTRAINT generated_project_locks_id_product_unique UNIQUE (lock_id, product_id),
    ADD CONSTRAINT generated_project_locks_assembly_product_fkey
        FOREIGN KEY (assembly_id, product_id) REFERENCES assembly.assembly_manifests(assembly_id, product_id);

CREATE TABLE assembly.lifecycle_plans (
    lifecycle_plan_id TEXT PRIMARY KEY,
    assembly_id TEXT NOT NULL,
    product_id TEXT NOT NULL,
    operation TEXT NOT NULL CHECK (operation IN ('upgrade','eject')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version = 1),
    schema_version TEXT NOT NULL,
    document TEXT NOT NULL CHECK (jsonb_typeof(document::jsonb) = 'object'),
    source_manifest_id TEXT NOT NULL,
    source_manifest_checksum TEXT NOT NULL CHECK (source_manifest_checksum ~ '^sha256:[a-f0-9]{64}$'),
    source_lock_id TEXT NOT NULL,
    source_lock_checksum TEXT NOT NULL CHECK (source_lock_checksum ~ '^sha256:[a-f0-9]{64}$'),
    source_catalog_checksum TEXT NOT NULL CHECK (source_catalog_checksum ~ '^sha256:[a-f0-9]{64}$'),
    source_target_snapshot_checksum TEXT NOT NULL CHECK (source_target_snapshot_checksum ~ '^sha256:[a-f0-9]{64}$'),
    target_snapshot_checksum TEXT NOT NULL CHECK (target_snapshot_checksum ~ '^sha256:[a-f0-9]{64}$'),
    blocking_conflict_count INTEGER NOT NULL CHECK (blocking_conflict_count >= 0),
    executable BOOLEAN NOT NULL,
    confirmation_checksum TEXT NOT NULL CHECK (confirmation_checksum ~ '^sha256:[a-f0-9]{64}$'),
    plan_checksum TEXT NOT NULL UNIQUE CHECK (plan_checksum ~ '^sha256:[a-f0-9]{64}$'),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (executable = (blocking_conflict_count = 0)),
    FOREIGN KEY (assembly_id, product_id) REFERENCES assembly.assembly_manifests(assembly_id, product_id),
    FOREIGN KEY (source_manifest_id, product_id) REFERENCES assembly.assembly_manifests(assembly_id, product_id),
    FOREIGN KEY (source_lock_id, product_id) REFERENCES assembly.generated_project_locks(lock_id, product_id)
);

CREATE INDEX lifecycle_plans_assembly_idx
    ON assembly.lifecycle_plans (assembly_id, created_at DESC, lifecycle_plan_id DESC);
CREATE INDEX lifecycle_plans_product_idx
    ON assembly.lifecycle_plans (product_id, created_at DESC, lifecycle_plan_id DESC);

CREATE TABLE assembly.lifecycle_operations (
    operation_id TEXT PRIMARY KEY,
    root_operation_id TEXT NOT NULL,
    rollback_of_operation_id TEXT,
    lifecycle_plan_id TEXT REFERENCES assembly.lifecycle_plans(lifecycle_plan_id),
    assembly_id TEXT NOT NULL,
    product_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('upgrade','eject','rollback')),
    version BIGINT NOT NULL CHECK (version > 0),
    status TEXT NOT NULL CHECK (status IN ('planned','executing','completed','failed','cancelled','rolling_back','rolled_back','rollback_failed')),
    current_step TEXT,
    schema_version TEXT NOT NULL,
    document TEXT NOT NULL CHECK (jsonb_typeof(document::jsonb) = 'object'),
    source_state JSONB NOT NULL CHECK (jsonb_typeof(source_state) = 'object'),
    target_state JSONB CHECK (target_state IS NULL OR jsonb_typeof(target_state) = 'object'),
    recovery JSONB NOT NULL CHECK (jsonb_typeof(recovery) = 'object'),
    diagnostic_ids JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (assembly.safe_text_array(diagnostic_ids, 100, 128)),
    report_ids JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (assembly.safe_text_array(report_ids, 100, 128)),
    operation_checksum TEXT NOT NULL CHECK (operation_checksum ~ '^sha256:[a-f0-9]{64}$'),
    idempotency_key_digest TEXT NOT NULL CHECK (idempotency_key_digest ~ '^sha256:[a-f0-9]{64}$'),
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    FOREIGN KEY (root_operation_id) REFERENCES assembly.lifecycle_operations(operation_id) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (rollback_of_operation_id) REFERENCES assembly.lifecycle_operations(operation_id),
    FOREIGN KEY (assembly_id, product_id) REFERENCES assembly.assembly_manifests(assembly_id, product_id),
    UNIQUE (lifecycle_plan_id, idempotency_key_digest),
    CHECK (
        (kind IN ('upgrade','eject') AND lifecycle_plan_id IS NOT NULL AND rollback_of_operation_id IS NULL)
        OR (kind = 'rollback' AND lifecycle_plan_id IS NULL AND rollback_of_operation_id IS NOT NULL)
    ),
    CHECK ((operation_id = root_operation_id AND rollback_of_operation_id IS NULL) OR operation_id <> root_operation_id),
    CHECK ((status IN ('completed','failed','cancelled','rolled_back','rollback_failed')) = (completed_at IS NOT NULL))
);

ALTER TABLE assembly.assembly_manifests
    ALTER COLUMN run_id DROP NOT NULL,
    ADD COLUMN lifecycle_operation_id TEXT UNIQUE REFERENCES assembly.lifecycle_operations(operation_id),
    ADD CONSTRAINT assembly_manifests_single_source_check
        CHECK (num_nonnulls(run_id, lifecycle_operation_id) = 1);

ALTER TABLE assembly.generated_project_locks
    ALTER COLUMN run_id DROP NOT NULL,
    ADD COLUMN lifecycle_operation_id TEXT UNIQUE REFERENCES assembly.lifecycle_operations(operation_id),
    ADD CONSTRAINT generated_project_locks_single_source_check
        CHECK (num_nonnulls(run_id, lifecycle_operation_id) = 1);

CREATE TABLE assembly.lifecycle_heads (
    root_assembly_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    current_manifest_id TEXT NOT NULL UNIQUE,
    current_lock_id TEXT NOT NULL UNIQUE,
    version BIGINT NOT NULL CHECK (version > 0),
    updated_by_operation_id TEXT REFERENCES assembly.lifecycle_operations(operation_id),
    updated_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (root_assembly_id, product_id) REFERENCES assembly.assembly_manifests(assembly_id, product_id),
    FOREIGN KEY (current_manifest_id, product_id) REFERENCES assembly.assembly_manifests(assembly_id, product_id),
    FOREIGN KEY (current_lock_id, product_id) REFERENCES assembly.generated_project_locks(lock_id, product_id)
);

INSERT INTO assembly.lifecycle_heads(root_assembly_id,product_id,current_manifest_id,current_lock_id,version,updated_at)
SELECT manifest.assembly_id,manifest.product_id,manifest.assembly_id,lock.lock_id,1,
       GREATEST(manifest.created_at,lock.created_at)
FROM assembly.assembly_manifests manifest
JOIN assembly.generated_project_locks lock ON lock.assembly_id=manifest.assembly_id;

CREATE UNIQUE INDEX lifecycle_operations_rollback_once_idx
    ON assembly.lifecycle_operations (rollback_of_operation_id)
    WHERE rollback_of_operation_id IS NOT NULL
      AND status <> 'rollback_failed';
CREATE INDEX lifecycle_operations_assembly_idx
    ON assembly.lifecycle_operations (assembly_id, created_at DESC, operation_id DESC);
CREATE INDEX lifecycle_operations_product_idx
    ON assembly.lifecycle_operations (product_id, created_at DESC, operation_id DESC);
CREATE UNIQUE INDEX lifecycle_operations_active_assembly_idx
    ON assembly.lifecycle_operations (assembly_id)
    WHERE status IN ('planned','executing','rolling_back');

CREATE TABLE assembly.lifecycle_dispatches (
    operation_id TEXT PRIMARY KEY REFERENCES assembly.lifecycle_operations(operation_id),
    state TEXT NOT NULL CHECK (state IN ('pending','leased','completed','cancelled','dead')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    available_at TIMESTAMPTZ NOT NULL,
    lease_owner TEXT,
    lease_until TIMESTAMPTZ,
    last_error_code TEXT CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$'),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK ((state = 'leased') = (lease_owner IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK (lease_owner IS NULL OR lease_owner ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$')
);

CREATE INDEX lifecycle_dispatches_claim_idx
    ON assembly.lifecycle_dispatches (available_at, created_at, operation_id)
    WHERE state IN ('pending','leased');

CREATE TABLE assembly.lifecycle_artifact_transitions (
    operation_id TEXT PRIMARY KEY REFERENCES assembly.lifecycle_operations(operation_id),
    source_manifest_id TEXT NOT NULL REFERENCES assembly.assembly_manifests(assembly_id),
    source_manifest_checksum TEXT NOT NULL CHECK (source_manifest_checksum ~ '^sha256:[a-f0-9]{64}$'),
    source_lock_id TEXT NOT NULL REFERENCES assembly.generated_project_locks(lock_id),
    source_lock_checksum TEXT NOT NULL CHECK (source_lock_checksum ~ '^sha256:[a-f0-9]{64}$'),
    target_manifest_id TEXT REFERENCES assembly.assembly_manifests(assembly_id),
    target_manifest_checksum TEXT CHECK (target_manifest_checksum IS NULL OR target_manifest_checksum ~ '^sha256:[a-f0-9]{64}$'),
    target_manifest_document JSONB CHECK (target_manifest_document IS NULL OR jsonb_typeof(target_manifest_document) = 'object'),
    target_lock_id TEXT REFERENCES assembly.generated_project_locks(lock_id),
    target_lock_checksum TEXT CHECK (target_lock_checksum IS NULL OR target_lock_checksum ~ '^sha256:[a-f0-9]{64}$'),
    target_lock_document JSONB CHECK (target_lock_document IS NULL OR jsonb_typeof(target_lock_document) = 'object'),
    rollback_journal JSONB NOT NULL CHECK (jsonb_typeof(rollback_journal) = 'object'),
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CHECK ((target_manifest_id IS NULL) = (target_manifest_checksum IS NULL)),
    CHECK ((target_manifest_id IS NULL) = (target_manifest_document IS NULL)),
    CHECK ((target_lock_id IS NULL) = (target_lock_checksum IS NULL)),
    CHECK ((target_lock_id IS NULL) = (target_lock_document IS NULL)),
    CHECK ((target_manifest_id IS NULL) = (target_lock_id IS NULL)),
    CHECK ((completed_at IS NULL) = (target_manifest_id IS NULL))
);

CREATE TABLE assembly.lifecycle_diagnostics (
    operation_id TEXT NOT NULL REFERENCES assembly.lifecycle_operations(operation_id),
    diagnostic_id TEXT NOT NULL,
    code TEXT NOT NULL CHECK (code ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$'),
    severity TEXT NOT NULL CHECK (severity IN ('info','warning','error')),
    category TEXT NOT NULL CHECK (category ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$'),
    message TEXT NOT NULL CHECK (char_length(message) BETWEEN 1 AND 500 AND message !~ E'[\r\n\t]'),
    blocking BOOLEAN NOT NULL,
    retryable BOOLEAN NOT NULL,
    remediation JSONB NOT NULL CHECK (assembly.safe_text_array(remediation, 20, 300)),
    related_paths JSONB NOT NULL CHECK (assembly.safe_relative_path_array(related_paths)),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (operation_id, diagnostic_id)
);

CREATE UNIQUE INDEX lifecycle_artifact_transitions_target_manifest_idx
    ON assembly.lifecycle_artifact_transitions (target_manifest_id)
    WHERE target_manifest_id IS NOT NULL;
CREATE UNIQUE INDEX lifecycle_artifact_transitions_target_lock_idx
    ON assembly.lifecycle_artifact_transitions (target_lock_id)
    WHERE target_lock_id IS NOT NULL;

CREATE TABLE assembly.lifecycle_reports (
    operation_id TEXT NOT NULL REFERENCES assembly.lifecycle_operations(operation_id),
    report_id TEXT NOT NULL,
    report_type TEXT NOT NULL CHECK (report_type ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$'),
    status TEXT NOT NULL CHECK (status IN ('passed','failed','partial')),
    summary TEXT NOT NULL CHECK (char_length(summary) BETWEEN 1 AND 500 AND summary !~ E'[\r\n\t]'),
    checksum TEXT CHECK (checksum IS NULL OR checksum ~ '^sha256:[a-f0-9]{64}$'),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (operation_id, report_id)
);

CREATE FUNCTION assembly.reject_lifecycle_plan_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'validated lifecycle plan is immutable';
END;
$$;

CREATE TRIGGER lifecycle_plans_immutable
BEFORE UPDATE OR DELETE ON assembly.lifecycle_plans
FOR EACH ROW EXECUTE FUNCTION assembly.reject_lifecycle_plan_mutation();

CREATE FUNCTION assembly.validate_lifecycle_operation_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM assembly.lifecycle_heads head
        JOIN assembly.assembly_manifests manifest
          ON manifest.assembly_id=head.current_manifest_id
         AND manifest.product_id=head.product_id
        JOIN assembly.generated_project_locks lock
          ON lock.lock_id=head.current_lock_id
         AND lock.product_id=head.product_id
        WHERE head.root_assembly_id=NEW.assembly_id
          AND head.product_id=NEW.product_id
          AND NEW.source_state->>'manifest_id'=head.current_manifest_id
          AND NEW.source_state->>'manifest_checksum'=manifest.manifest_sha256
          AND NEW.source_state->>'lock_id'=head.current_lock_id
          AND NEW.source_state->>'lock_checksum'=lock.lock_sha256
          AND NEW.source_state->>'catalog_checksum'=lock.document::jsonb->>'catalog_checksum'
          AND NEW.source_state->>'target_snapshot_checksum'=lock.document::jsonb->>'target_snapshot_checksum'
    ) THEN
        RAISE EXCEPTION 'assembly lifecycle operation source is not the current head';
    END IF;

    IF NEW.kind IN ('upgrade','eject') THEN
        IF NEW.operation_id IS DISTINCT FROM NEW.root_operation_id
           OR NOT EXISTS (
                SELECT 1
                FROM assembly.lifecycle_plans plan
                WHERE plan.lifecycle_plan_id=NEW.lifecycle_plan_id
                  AND plan.assembly_id=NEW.assembly_id
                  AND plan.product_id=NEW.product_id
                  AND plan.operation=NEW.kind
                  AND plan.source_manifest_id=NEW.source_state->>'manifest_id'
                  AND plan.source_manifest_checksum=NEW.source_state->>'manifest_checksum'
                  AND plan.source_lock_id=NEW.source_state->>'lock_id'
                  AND plan.source_lock_checksum=NEW.source_state->>'lock_checksum'
                  AND plan.source_catalog_checksum=NEW.source_state->>'catalog_checksum'
                  AND plan.source_target_snapshot_checksum=NEW.source_state->>'target_snapshot_checksum'
           ) THEN
            RAISE EXCEPTION 'assembly lifecycle operation plan provenance is invalid';
        END IF;
    ELSE
        IF NOT EXISTS (
            SELECT 1
            FROM assembly.lifecycle_operations predecessor
            WHERE predecessor.operation_id=NEW.rollback_of_operation_id
              AND predecessor.root_operation_id=NEW.root_operation_id
              AND predecessor.assembly_id=NEW.assembly_id
              AND predecessor.product_id=NEW.product_id
              AND predecessor.status='completed'
              AND predecessor.target_state=NEW.source_state
        ) THEN
            RAISE EXCEPTION 'assembly lifecycle rollback provenance is invalid';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER lifecycle_operations_insert_valid
BEFORE INSERT ON assembly.lifecycle_operations
FOR EACH ROW EXECUTE FUNCTION assembly.validate_lifecycle_operation_insert();

CREATE FUNCTION assembly.reject_lifecycle_operation_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.operation_id IS DISTINCT FROM OLD.operation_id
       OR NEW.root_operation_id IS DISTINCT FROM OLD.root_operation_id
       OR NEW.rollback_of_operation_id IS DISTINCT FROM OLD.rollback_of_operation_id
       OR NEW.lifecycle_plan_id IS DISTINCT FROM OLD.lifecycle_plan_id
       OR NEW.assembly_id IS DISTINCT FROM OLD.assembly_id
       OR NEW.product_id IS DISTINCT FROM OLD.product_id
       OR NEW.kind IS DISTINCT FROM OLD.kind
       OR NEW.schema_version IS DISTINCT FROM OLD.schema_version
       OR NEW.source_state IS DISTINCT FROM OLD.source_state
       OR NEW.idempotency_key_digest IS DISTINCT FROM OLD.idempotency_key_digest
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR OLD.status IN ('completed','failed','cancelled','rolled_back','rollback_failed')
       OR NEW.version <> OLD.version + 1
       OR NEW.updated_at <= OLD.updated_at
       OR NOT (NEW.diagnostic_ids @> OLD.diagnostic_ids)
       OR NOT (NEW.report_ids @> OLD.report_ids)
       OR (OLD.completed_at IS NOT NULL AND NEW.completed_at IS DISTINCT FROM OLD.completed_at)
       OR NOT (
            NEW.status = OLD.status
            OR (OLD.status = 'planned' AND NEW.status IN ('executing','cancelled','failed'))
            OR (OLD.status = 'executing' AND NEW.status IN ('completed','failed','rolling_back'))
            OR (OLD.status = 'rolling_back' AND NEW.status IN ('rolled_back','rollback_failed'))
       ) THEN
        RAISE EXCEPTION 'assembly lifecycle operation evolution is invalid';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER lifecycle_operations_contract_immutable
BEFORE UPDATE ON assembly.lifecycle_operations
FOR EACH ROW EXECUTE FUNCTION assembly.reject_lifecycle_operation_update();

CREATE TRIGGER lifecycle_operations_delete_immutable
BEFORE DELETE ON assembly.lifecycle_operations
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();
CREATE TRIGGER lifecycle_artifact_transitions_delete_immutable
BEFORE DELETE ON assembly.lifecycle_artifact_transitions
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();

CREATE FUNCTION assembly.validate_lifecycle_transition_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.completed_at IS NOT NULL
           OR NEW.target_manifest_id IS NOT NULL
           OR NEW.target_lock_id IS NOT NULL
           OR NOT EXISTS (
                SELECT 1 FROM assembly.lifecycle_operations operation
                WHERE operation.operation_id=NEW.operation_id
                  AND operation.status IN ('planned','executing','rolling_back')
                  AND operation.source_state->>'manifest_id'=NEW.source_manifest_id
                  AND operation.source_state->>'manifest_checksum'=NEW.source_manifest_checksum
                  AND operation.source_state->>'lock_id'=NEW.source_lock_id
                  AND operation.source_state->>'lock_checksum'=NEW.source_lock_checksum
           ) THEN
            RAISE EXCEPTION 'assembly lifecycle artifact transition creation is invalid';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.operation_id IS DISTINCT FROM OLD.operation_id
       OR NEW.source_manifest_id IS DISTINCT FROM OLD.source_manifest_id
       OR NEW.source_manifest_checksum IS DISTINCT FROM OLD.source_manifest_checksum
       OR NEW.source_lock_id IS DISTINCT FROM OLD.source_lock_id
       OR NEW.source_lock_checksum IS DISTINCT FROM OLD.source_lock_checksum
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR OLD.completed_at IS NOT NULL
       OR OLD.target_manifest_id IS NOT NULL
       OR OLD.target_lock_id IS NOT NULL
       OR NEW.completed_at IS NULL
       OR NEW.target_manifest_id IS NULL
       OR NEW.target_lock_id IS NULL
       OR NOT EXISTS (
            SELECT 1 FROM assembly.lifecycle_operations operation
            WHERE operation.operation_id=NEW.operation_id
              AND operation.status IN ('completed','rolled_back')
              AND operation.source_state->>'manifest_id'=NEW.source_manifest_id
              AND operation.source_state->>'manifest_checksum'=NEW.source_manifest_checksum
              AND operation.source_state->>'lock_id'=NEW.source_lock_id
              AND operation.source_state->>'lock_checksum'=NEW.source_lock_checksum
              AND operation.target_state->>'manifest_id'=NEW.target_manifest_id
              AND operation.target_state->>'manifest_checksum'=NEW.target_manifest_checksum
              AND operation.target_state->>'lock_id'=NEW.target_lock_id
              AND operation.target_state->>'lock_checksum'=NEW.target_lock_checksum
       ) THEN
        RAISE EXCEPTION 'assembly lifecycle artifact transition evolution is invalid';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER lifecycle_artifact_transitions_contract_immutable
BEFORE INSERT OR UPDATE ON assembly.lifecycle_artifact_transitions
FOR EACH ROW EXECUTE FUNCTION assembly.validate_lifecycle_transition_update();
CREATE TRIGGER lifecycle_diagnostics_immutable
BEFORE UPDATE OR DELETE ON assembly.lifecycle_diagnostics
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();
CREATE TRIGGER lifecycle_reports_immutable
BEFORE UPDATE OR DELETE ON assembly.lifecycle_reports
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();

CREATE FUNCTION assembly.validate_lifecycle_manifest_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.lifecycle_operation_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM assembly.lifecycle_operations operation
        WHERE operation.operation_id=NEW.lifecycle_operation_id
          AND operation.product_id=NEW.product_id
          AND operation.status IN ('completed','rolled_back')
          AND operation.target_state->>'manifest_id'=NEW.assembly_id
          AND operation.target_state->>'manifest_checksum'=NEW.manifest_sha256
    ) THEN
        RAISE EXCEPTION 'lifecycle manifest provenance is invalid';
    END IF;
    RETURN NEW;
END;
$$;
CREATE FUNCTION assembly.validate_lifecycle_lock_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.lifecycle_operation_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM assembly.lifecycle_operations operation
        WHERE operation.operation_id=NEW.lifecycle_operation_id
          AND operation.product_id=NEW.product_id
          AND operation.status IN ('completed','rolled_back')
          AND operation.target_state->>'manifest_id'=NEW.assembly_id
          AND operation.target_state->>'lock_id'=NEW.lock_id
          AND operation.target_state->>'lock_checksum'=NEW.lock_sha256
    ) THEN
        RAISE EXCEPTION 'lifecycle lock provenance is invalid';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER assembly_manifests_lifecycle_source_valid
BEFORE INSERT ON assembly.assembly_manifests
FOR EACH ROW EXECUTE FUNCTION assembly.validate_lifecycle_manifest_insert();
CREATE TRIGGER generated_project_locks_lifecycle_source_valid
BEFORE INSERT ON assembly.generated_project_locks
FOR EACH ROW EXECUTE FUNCTION assembly.validate_lifecycle_lock_insert();

CREATE FUNCTION assembly.validate_lifecycle_head_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.root_assembly_id IS DISTINCT FROM OLD.root_assembly_id
       OR NEW.product_id IS DISTINCT FROM OLD.product_id
       OR NEW.version <> OLD.version + 1
       OR NEW.updated_at <= OLD.updated_at
       OR NEW.updated_by_operation_id IS NULL
       OR NEW.current_manifest_id = OLD.current_manifest_id
       OR NEW.current_lock_id = OLD.current_lock_id
       OR NOT EXISTS (
            SELECT 1
            FROM assembly.assembly_manifests manifest
            JOIN assembly.generated_project_locks lock ON lock.assembly_id=manifest.assembly_id
            WHERE manifest.assembly_id=NEW.current_manifest_id
              AND lock.lock_id=NEW.current_lock_id
              AND manifest.product_id=NEW.product_id
              AND lock.product_id=NEW.product_id
              AND manifest.lifecycle_operation_id=NEW.updated_by_operation_id
              AND lock.lifecycle_operation_id=NEW.updated_by_operation_id
       ) THEN
        RAISE EXCEPTION 'assembly lifecycle head evolution is invalid';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM assembly.lifecycle_operations operation
        JOIN assembly.lifecycle_artifact_transitions transition ON transition.operation_id=operation.operation_id
        WHERE operation.operation_id=NEW.updated_by_operation_id
          AND operation.assembly_id=NEW.root_assembly_id
          AND operation.product_id=NEW.product_id
          AND operation.status IN ('completed','rolled_back')
          AND operation.source_state->>'manifest_id'=OLD.current_manifest_id
          AND operation.source_state->>'manifest_checksum'=(
                SELECT manifest.manifest_sha256
                FROM assembly.assembly_manifests manifest
                WHERE manifest.assembly_id=OLD.current_manifest_id
                  AND manifest.product_id=OLD.product_id
          )
          AND operation.source_state->>'lock_id'=OLD.current_lock_id
          AND operation.source_state->>'lock_checksum'=(
                SELECT lock.lock_sha256
                FROM assembly.generated_project_locks lock
                WHERE lock.lock_id=OLD.current_lock_id
                  AND lock.product_id=OLD.product_id
          )
          AND operation.target_state->>'manifest_id'=NEW.current_manifest_id
          AND operation.target_state->>'lock_id'=NEW.current_lock_id
          AND transition.source_manifest_id=OLD.current_manifest_id
          AND transition.source_manifest_checksum=operation.source_state->>'manifest_checksum'
          AND transition.source_lock_id=OLD.current_lock_id
          AND transition.source_lock_checksum=operation.source_state->>'lock_checksum'
          AND transition.target_manifest_id=NEW.current_manifest_id
          AND transition.target_lock_id=NEW.current_lock_id
          AND transition.completed_at IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'assembly lifecycle head has no completed transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER lifecycle_heads_contract_immutable
BEFORE UPDATE ON assembly.lifecycle_heads
FOR EACH ROW EXECUTE FUNCTION assembly.validate_lifecycle_head_update();
CREATE TRIGGER lifecycle_heads_delete_immutable
BEFORE DELETE ON assembly.lifecycle_heads
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();

INSERT INTO access_control.admin_permissions (permission_code, description, risk_level)
VALUES
    ('assembly.lifecycle.plan', 'Create assembly upgrade and eject lifecycle plans', 'normal'),
    ('assembly.lifecycle.execute', 'Execute, cancel, and roll back assembly lifecycle operations', 'high')
ON CONFLICT (permission_code) DO UPDATE
SET description = EXCLUDED.description,
    risk_level = EXCLUDED.risk_level;

INSERT INTO access_control.admin_role_permissions (role_id, permission_code)
SELECT role_id, permission_code
FROM access_control.admin_roles
CROSS JOIN (VALUES
    ('assembly.lifecycle.plan'),
    ('assembly.lifecycle.execute')
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

CREATE OR REPLACE FUNCTION assembly.reject_run_contract_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.run_id IS DISTINCT FROM OLD.run_id
       OR NEW.plan_id IS DISTINCT FROM OLD.plan_id
       OR NEW.plan_version IS DISTINCT FROM OLD.plan_version
       OR NEW.plan_sha256 IS DISTINCT FROM OLD.plan_sha256
       OR NEW.idempotency_key_digest IS DISTINCT FROM OLD.idempotency_key_digest
       OR NEW.output_target_ref IS DISTINCT FROM OLD.output_target_ref
       OR NEW.root_run_id IS DISTINCT FROM OLD.root_run_id
       OR NEW.retry_of_run_id IS DISTINCT FROM OLD.retry_of_run_id
       OR NEW.attempt_number IS DISTINCT FROM OLD.attempt_number
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR (OLD.product_id IS NOT NULL AND NEW.product_id IS DISTINCT FROM OLD.product_id)
       OR (OLD.manifest_id IS NOT NULL AND NEW.manifest_id IS DISTINCT FROM OLD.manifest_id)
       OR (OLD.lock_id IS NOT NULL AND NEW.lock_id IS DISTINCT FROM OLD.lock_id)
       OR OLD.status IN ('completed','failed','cancelled','rolled_back')
       OR NEW.version <> OLD.version + 1
       OR NEW.updated_at <= OLD.updated_at
       OR (OLD.completed_at IS NOT NULL AND NEW.completed_at IS DISTINCT FROM OLD.completed_at)
       OR (NEW.completed_at IS NOT NULL AND NEW.status NOT IN ('completed','failed','cancelled','rolled_back'))
       OR (NEW.status = 'completed' AND (NEW.manifest_id IS NULL OR NEW.lock_id IS NULL OR NEW.completed_at IS NULL))
       OR (NEW.status IN ('failed','cancelled','rolled_back') AND NEW.completed_at IS NULL)
       OR NOT (
            NEW.status = OLD.status
            OR (OLD.status = 'planned' AND NEW.status IN ('provisioning','failed','cancelled'))
            OR (OLD.status = 'provisioning' AND NEW.status IN ('generating','failed','rolling_back'))
            OR (OLD.status = 'generating' AND NEW.status IN ('validating','failed','rolling_back'))
            OR (OLD.status = 'validating' AND NEW.status IN ('completed','failed','rolling_back'))
            OR (OLD.status = 'rolling_back' AND NEW.status IN ('rolled_back','failed'))
       ) THEN
        RAISE EXCEPTION 'assembly run contract evolution is invalid';
    END IF;
    RETURN NEW;
END;
$$;

COMMIT;
