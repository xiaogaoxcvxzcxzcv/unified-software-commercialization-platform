BEGIN;

DROP TRIGGER IF EXISTS assembly_run_reports_immutable ON assembly.assembly_run_reports;
DROP TRIGGER IF EXISTS assembly_run_diagnostics_immutable ON assembly.assembly_run_diagnostics;
DROP TRIGGER IF EXISTS assembly_run_steps_delete_immutable ON assembly.assembly_run_steps;
DROP TRIGGER IF EXISTS assembly_runs_delete_immutable ON assembly.assembly_runs;
DROP TRIGGER IF EXISTS assembly_run_steps_contract_immutable ON assembly.assembly_run_steps;
DROP FUNCTION IF EXISTS assembly.reject_run_step_update();
DROP TRIGGER IF EXISTS assembly_runs_retry_chain_valid ON assembly.assembly_runs;
DROP FUNCTION IF EXISTS assembly.validate_run_retry_chain();

DROP TABLE IF EXISTS assembly.assembly_run_reports;
DROP TABLE IF EXISTS assembly.assembly_run_diagnostics;
DROP TABLE IF EXISTS assembly.assembly_run_dispatches;
DROP FUNCTION IF EXISTS assembly.safe_relative_path_array(JSONB);
DROP FUNCTION IF EXISTS assembly.safe_text_array(JSONB, INTEGER, INTEGER);

CREATE OR REPLACE FUNCTION assembly.reject_run_contract_update() RETURNS trigger
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

DROP INDEX IF EXISTS assembly.assembly_runs_product_list_idx;
DROP INDEX IF EXISTS assembly.assembly_runs_status_list_idx;
DROP INDEX IF EXISTS assembly.assembly_runs_list_idx;

ALTER TABLE assembly.assembly_runs
    DROP CONSTRAINT IF EXISTS assembly_runs_retry_parent_unique,
    DROP CONSTRAINT IF EXISTS assembly_runs_root_attempt_unique,
    DROP CONSTRAINT IF EXISTS assembly_runs_retry_shape_check,
    DROP CONSTRAINT IF EXISTS assembly_runs_retry_parent_fk,
    DROP CONSTRAINT IF EXISTS assembly_runs_root_fk,
    DROP COLUMN IF EXISTS attempt_number,
    DROP COLUMN IF EXISTS retry_of_run_id,
    DROP COLUMN IF EXISTS root_run_id;

COMMIT;
