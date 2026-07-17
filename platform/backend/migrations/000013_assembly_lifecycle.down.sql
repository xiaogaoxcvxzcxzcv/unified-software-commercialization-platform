BEGIN;

DELETE FROM access_control.admin_role_permissions
WHERE permission_code IN ('assembly.lifecycle.plan', 'assembly.lifecycle.execute');
DELETE FROM access_control.admin_permissions
WHERE permission_code IN ('assembly.lifecycle.plan', 'assembly.lifecycle.execute');

DROP TRIGGER IF EXISTS lifecycle_reports_immutable ON assembly.lifecycle_reports;
DROP TRIGGER IF EXISTS lifecycle_diagnostics_immutable ON assembly.lifecycle_diagnostics;
DROP TRIGGER IF EXISTS lifecycle_artifact_transitions_delete_immutable ON assembly.lifecycle_artifact_transitions;
DROP TRIGGER IF EXISTS lifecycle_artifact_transitions_contract_immutable ON assembly.lifecycle_artifact_transitions;
DROP TRIGGER IF EXISTS lifecycle_operations_delete_immutable ON assembly.lifecycle_operations;
DROP TRIGGER IF EXISTS lifecycle_operations_contract_immutable ON assembly.lifecycle_operations;
DROP TRIGGER IF EXISTS lifecycle_operations_insert_valid ON assembly.lifecycle_operations;
DROP TRIGGER IF EXISTS lifecycle_plans_immutable ON assembly.lifecycle_plans;
DROP TRIGGER IF EXISTS lifecycle_heads_delete_immutable ON assembly.lifecycle_heads;
DROP TRIGGER IF EXISTS lifecycle_heads_contract_immutable ON assembly.lifecycle_heads;
DROP FUNCTION IF EXISTS assembly.validate_lifecycle_head_update();
DROP FUNCTION IF EXISTS assembly.validate_lifecycle_transition_update();
DROP FUNCTION IF EXISTS assembly.reject_lifecycle_operation_update();
DROP FUNCTION IF EXISTS assembly.validate_lifecycle_operation_insert();
DROP FUNCTION IF EXISTS assembly.reject_lifecycle_plan_mutation();

DROP TABLE IF EXISTS assembly.lifecycle_reports;
DROP TABLE IF EXISTS assembly.lifecycle_diagnostics;
DROP TABLE IF EXISTS assembly.lifecycle_artifact_transitions;
DROP TABLE IF EXISTS assembly.lifecycle_dispatches;
DROP TABLE IF EXISTS assembly.lifecycle_heads;

DROP TRIGGER IF EXISTS generated_project_locks_lifecycle_source_valid ON assembly.generated_project_locks;
DROP TRIGGER IF EXISTS assembly_manifests_lifecycle_source_valid ON assembly.assembly_manifests;
DROP FUNCTION IF EXISTS assembly.validate_lifecycle_lock_insert();
DROP FUNCTION IF EXISTS assembly.validate_lifecycle_manifest_insert();
ALTER TABLE assembly.generated_project_locks
    DROP CONSTRAINT generated_project_locks_lifecycle_operation_id_fkey;
ALTER TABLE assembly.assembly_manifests
    DROP CONSTRAINT assembly_manifests_lifecycle_operation_id_fkey;
DROP TABLE IF EXISTS assembly.lifecycle_operations;
DROP TABLE IF EXISTS assembly.lifecycle_plans;

DROP TRIGGER IF EXISTS assembly_generated_locks_immutable ON assembly.generated_project_locks;
DROP TRIGGER IF EXISTS assembly_manifests_immutable ON assembly.assembly_manifests;
DELETE FROM assembly.generated_project_locks WHERE lifecycle_operation_id IS NOT NULL;
DELETE FROM assembly.assembly_manifests WHERE lifecycle_operation_id IS NOT NULL;
ALTER TABLE assembly.generated_project_locks
    DROP CONSTRAINT generated_project_locks_single_source_check,
    DROP COLUMN lifecycle_operation_id,
    ALTER COLUMN run_id SET NOT NULL;
ALTER TABLE assembly.assembly_manifests
    DROP CONSTRAINT assembly_manifests_single_source_check,
    DROP COLUMN lifecycle_operation_id,
    ALTER COLUMN run_id SET NOT NULL;

ALTER TABLE assembly.generated_project_locks
    DROP CONSTRAINT generated_project_locks_assembly_product_fkey,
    DROP CONSTRAINT generated_project_locks_id_product_unique;
ALTER TABLE assembly.assembly_manifests
    DROP CONSTRAINT assembly_manifests_id_product_unique;

CREATE TRIGGER assembly_manifests_immutable
BEFORE UPDATE OR DELETE ON assembly.assembly_manifests
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();
CREATE TRIGGER assembly_generated_locks_immutable
BEFORE UPDATE OR DELETE ON assembly.generated_project_locks
FOR EACH ROW EXECUTE FUNCTION assembly.reject_all_mutation();

ALTER TABLE assembly.assembly_run_dispatches
    DROP CONSTRAINT assembly_run_dispatches_state_check,
    ADD CONSTRAINT assembly_run_dispatches_state_check CHECK (state IN ('pending','leased','completed','dead'));

ALTER TABLE assembly.assembly_runs
    DROP CONSTRAINT assembly_runs_status_check,
    ADD CONSTRAINT assembly_runs_status_check CHECK (status IN ('planned','provisioning','generating','validating','completed','failed','rolling_back','rolled_back'));

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

COMMIT;
