BEGIN;

ALTER TABLE assembly.assembly_runs
    ADD COLUMN root_run_id TEXT,
    ADD COLUMN retry_of_run_id TEXT,
    ADD COLUMN attempt_number INTEGER NOT NULL DEFAULT 1 CHECK (attempt_number > 0);

UPDATE assembly.assembly_runs SET root_run_id = run_id;

ALTER TABLE assembly.assembly_runs
    ALTER COLUMN root_run_id SET NOT NULL,
    ADD CONSTRAINT assembly_runs_root_fk FOREIGN KEY (root_run_id) REFERENCES assembly.assembly_runs(run_id),
    ADD CONSTRAINT assembly_runs_retry_parent_fk FOREIGN KEY (retry_of_run_id) REFERENCES assembly.assembly_runs(run_id),
    ADD CONSTRAINT assembly_runs_retry_shape_check CHECK (
        (attempt_number = 1 AND retry_of_run_id IS NULL AND root_run_id = run_id)
        OR (attempt_number > 1 AND retry_of_run_id IS NOT NULL AND root_run_id <> run_id)
    ),
    ADD CONSTRAINT assembly_runs_root_attempt_unique UNIQUE (root_run_id, attempt_number),
    ADD CONSTRAINT assembly_runs_retry_parent_unique UNIQUE (retry_of_run_id);

CREATE INDEX assembly_runs_list_idx
    ON assembly.assembly_runs (created_at DESC, run_id DESC);
CREATE INDEX assembly_runs_status_list_idx
    ON assembly.assembly_runs (status, created_at DESC, run_id DESC);
CREATE INDEX assembly_runs_product_list_idx
    ON assembly.assembly_runs (product_id, created_at DESC, run_id DESC)
    WHERE product_id IS NOT NULL;

CREATE FUNCTION assembly.validate_run_retry_chain() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    parent_root TEXT;
    parent_attempt INTEGER;
    parent_status TEXT;
    parent_recovery JSONB;
    root_actor TEXT;
BEGIN
    IF NEW.attempt_number = 1 THEN
        RETURN NEW;
    END IF;
    SELECT root_run_id, attempt_number, status, recovery
      INTO parent_root, parent_attempt, parent_status, parent_recovery
      FROM assembly.assembly_runs
     WHERE run_id = NEW.retry_of_run_id
     FOR SHARE;
    IF NOT FOUND
       OR parent_status <> 'failed'
       OR COALESCE((parent_recovery->>'retryable')::boolean, FALSE) IS NOT TRUE
       OR COALESCE((parent_recovery->>'rollback_required')::boolean, TRUE) IS NOT FALSE
       OR NEW.root_run_id <> parent_root
       OR NEW.attempt_number <> parent_attempt + 1 THEN
        RAISE EXCEPTION 'assembly retry chain is invalid';
    END IF;
    SELECT created_by INTO root_actor
      FROM assembly.assembly_runs
     WHERE run_id = NEW.root_run_id AND attempt_number = 1
     FOR SHARE;
    IF NOT FOUND OR NEW.created_by <> root_actor THEN
        RAISE EXCEPTION 'assembly retry workflow actor is invalid';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER assembly_runs_retry_chain_valid
BEFORE INSERT ON assembly.assembly_runs
FOR EACH ROW EXECUTE FUNCTION assembly.validate_run_retry_chain();

CREATE TABLE assembly.assembly_run_dispatches (
    run_id TEXT PRIMARY KEY REFERENCES assembly.assembly_runs(run_id),
    state TEXT NOT NULL CHECK (state IN ('pending','leased','completed','dead')),
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

CREATE INDEX assembly_run_dispatches_claim_idx
    ON assembly.assembly_run_dispatches (available_at, created_at, run_id)
    WHERE state IN ('pending','leased');

CREATE FUNCTION assembly.safe_text_array(value JSONB, maximum_items INTEGER, maximum_length INTEGER) RETURNS BOOLEAN
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    item JSONB;
    text_value TEXT;
BEGIN
    IF jsonb_typeof(value) <> 'array' OR jsonb_array_length(value) > maximum_items THEN
        RETURN FALSE;
    END IF;
    FOR item IN SELECT element FROM jsonb_array_elements(value) AS elements(element) LOOP
        IF jsonb_typeof(item) <> 'string' THEN
            RETURN FALSE;
        END IF;
        text_value := item #>> '{}';
        IF char_length(text_value) NOT BETWEEN 1 AND maximum_length OR text_value ~ E'[\\r\\n\\t]' THEN
            RETURN FALSE;
        END IF;
    END LOOP;
    RETURN TRUE;
END;
$$;

CREATE FUNCTION assembly.safe_relative_path_array(value JSONB) RETURNS BOOLEAN
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    item JSONB;
    path_value TEXT;
BEGIN
    IF jsonb_typeof(value) <> 'array' OR jsonb_array_length(value) > 100 THEN
        RETURN FALSE;
    END IF;
    FOR item IN SELECT element FROM jsonb_array_elements(value) AS elements(element) LOOP
        IF jsonb_typeof(item) <> 'string' THEN
            RETURN FALSE;
        END IF;
        path_value := item #>> '{}';
        IF char_length(path_value) NOT BETWEEN 1 AND 500
           OR path_value !~ '^[A-Za-z0-9._/-]+$'
           OR path_value ~ '^[A-Za-z]:'
           OR path_value ~ '^/'
           OR path_value ~ '(^|/)\.\.(/|$)' THEN
            RETURN FALSE;
        END IF;
    END LOOP;
    RETURN TRUE;
END;
$$;

CREATE TABLE assembly.assembly_run_diagnostics (
    run_id TEXT NOT NULL REFERENCES assembly.assembly_runs(run_id),
    diagnostic_id TEXT NOT NULL,
    code TEXT NOT NULL CHECK (code ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$'),
    severity TEXT NOT NULL CHECK (severity IN ('info','warning','error')),
    category TEXT NOT NULL CHECK (category ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$'),
    message TEXT NOT NULL CHECK (char_length(message) BETWEEN 1 AND 500 AND message !~ E'[\\r\\n\\t]'),
    blocking BOOLEAN NOT NULL,
    retryable BOOLEAN NOT NULL,
    remediation JSONB NOT NULL CHECK (assembly.safe_text_array(remediation, 20, 300)),
    related_paths JSONB NOT NULL CHECK (assembly.safe_relative_path_array(related_paths)),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (run_id, diagnostic_id)
);

CREATE TABLE assembly.assembly_run_reports (
    run_id TEXT NOT NULL REFERENCES assembly.assembly_runs(run_id),
    report_id TEXT NOT NULL,
    report_type TEXT NOT NULL CHECK (report_type ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$'),
    status TEXT NOT NULL CHECK (status IN ('passed','failed','partial')),
    summary TEXT NOT NULL CHECK (char_length(summary) BETWEEN 1 AND 500 AND summary !~ E'[\\r\\n\\t]'),
    checksum TEXT CHECK (checksum IS NULL OR checksum ~ '^sha256:[a-f0-9]{64}$'),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (run_id, report_id)
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
       OR OLD.status IN ('completed','failed','rolled_back')
       OR NEW.version <> OLD.version + 1
       OR NEW.updated_at <= OLD.updated_at
       OR (OLD.completed_at IS NOT NULL AND NEW.completed_at IS DISTINCT FROM OLD.completed_at)
       OR (NEW.completed_at IS NOT NULL AND NEW.status NOT IN ('completed','failed','rolled_back'))
       OR (NEW.status = 'completed' AND (NEW.manifest_id IS NULL OR NEW.lock_id IS NULL OR NEW.completed_at IS NULL))
       OR (NEW.status IN ('failed','rolled_back') AND NEW.completed_at IS NULL)
       OR NOT (
            NEW.status = OLD.status
            OR (OLD.status = 'planned' AND NEW.status IN ('provisioning','failed'))
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

CREATE FUNCTION assembly.reject_run_step_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.run_id IS DISTINCT FROM OLD.run_id
       OR NEW.step_id IS DISTINCT FROM OLD.step_id
       OR NEW.ordinal IS DISTINCT FROM OLD.ordinal
       OR NEW.kind IS DISTINCT FROM OLD.kind
       OR NEW.attempt < OLD.attempt
       OR (OLD.started_at IS NOT NULL AND NEW.started_at IS DISTINCT FROM OLD.started_at)
       OR (OLD.finished_at IS NOT NULL AND NEW.finished_at IS DISTINCT FROM OLD.finished_at)
       OR NOT (NEW.diagnostic_ids @> OLD.diagnostic_ids)
       OR NOT (
            NEW.status = OLD.status
            OR (OLD.status = 'pending' AND NEW.status IN ('running','completed','failed','compensated','skipped'))
            OR (OLD.status = 'running' AND NEW.status IN ('completed','failed','compensated','skipped'))
            OR (OLD.status IN ('completed','failed') AND NEW.status = 'compensated')
       )
       OR NOT (
            NEW.compensation_status = OLD.compensation_status
            OR (OLD.compensation_status = 'not_required' AND NEW.compensation_status IN ('pending','completed','failed'))
            OR (OLD.compensation_status = 'pending' AND NEW.compensation_status IN ('completed','failed'))
       ) THEN
        RAISE EXCEPTION 'assembly run step evolution is invalid';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER assembly_run_steps_contract_immutable
BEFORE UPDATE ON assembly.assembly_run_steps
FOR EACH ROW EXECUTE FUNCTION assembly.reject_run_step_update();

CREATE TRIGGER assembly_runs_delete_immutable
BEFORE DELETE ON assembly.assembly_runs
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();

CREATE TRIGGER assembly_run_steps_delete_immutable
BEFORE DELETE ON assembly.assembly_run_steps
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();

CREATE TRIGGER assembly_run_diagnostics_immutable
BEFORE UPDATE OR DELETE ON assembly.assembly_run_diagnostics
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();

CREATE TRIGGER assembly_run_reports_immutable
BEFORE UPDATE OR DELETE ON assembly.assembly_run_reports
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();

INSERT INTO assembly.assembly_run_dispatches(run_id,state,attempt_count,available_at,created_at,updated_at)
SELECT run_id,
       CASE WHEN status = 'planned' THEN 'pending' ELSE 'completed' END,
       0,
       updated_at,
       created_at,
       updated_at
FROM assembly.assembly_runs;

COMMIT;
