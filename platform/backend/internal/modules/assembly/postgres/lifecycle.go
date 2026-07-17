package postgres

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

func (r *Repository) GetCurrentLock(ctx context.Context, productID, assemblyID string) (core.GeneratedProjectLock, error) {
	var value core.GeneratedProjectLock
	err := r.pool.QueryRow(ctx, `SELECT lock_id,product_id,COALESCE(run_id,''),assembly_id,schema_version,document,document_sha256,lock_sha256,created_at FROM assembly.generated_project_locks WHERE assembly_id=$2 AND ($1='' OR product_id=$1) ORDER BY created_at DESC,lock_id DESC LIMIT 1`, productID, assemblyID).Scan(&value.LockID, &value.ProductID, &value.RunID, &value.AssemblyID, &value.SchemaVersion, &value.Document, &value.DocumentSHA256, &value.LockSHA256, &value.CreatedAt)
	return value, mapNotFound(err)
}

func (r *Repository) GetLifecycleSource(ctx context.Context, rootAssemblyID string) (core.Manifest, core.GeneratedProjectLock, error) {
	var manifest core.Manifest
	var lock core.GeneratedProjectLock
	err := r.pool.QueryRow(ctx, `SELECT
		manifest.assembly_id,manifest.product_id,COALESCE(manifest.run_id,''),COALESCE(manifest.lifecycle_operation_id,''),manifest.schema_version,manifest.document,manifest.document_sha256,manifest.manifest_sha256,manifest.created_at,
		lock.lock_id,lock.product_id,COALESCE(lock.run_id,''),COALESCE(lock.lifecycle_operation_id,''),lock.assembly_id,lock.schema_version,lock.document,lock.document_sha256,lock.lock_sha256,lock.created_at
      FROM assembly.lifecycle_heads head
      JOIN assembly.assembly_manifests manifest ON manifest.assembly_id=head.current_manifest_id
      JOIN assembly.generated_project_locks lock ON lock.lock_id=head.current_lock_id
      WHERE head.root_assembly_id=$1 AND manifest.product_id=head.product_id AND lock.product_id=head.product_id AND lock.assembly_id=manifest.assembly_id`, rootAssemblyID).Scan(
		&manifest.AssemblyID, &manifest.ProductID, &manifest.RunID, &manifest.LifecycleOperationID, &manifest.SchemaVersion, &manifest.Document, &manifest.DocumentSHA256, &manifest.ManifestSHA256, &manifest.CreatedAt,
		&lock.LockID, &lock.ProductID, &lock.RunID, &lock.LifecycleOperationID, &lock.AssemblyID, &lock.SchemaVersion, &lock.Document, &lock.DocumentSHA256, &lock.LockSHA256, &lock.CreatedAt,
	)
	if err != nil {
		return core.Manifest{}, core.GeneratedProjectLock{}, mapNotFound(err)
	}
	return manifest, lock, nil
}

func (r *Repository) CreateLifecyclePlan(ctx context.Context, record core.CreateLifecyclePlanRecord) (core.LifecyclePlan, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.LifecyclePlan{}, err
	}
	defer rollback(tx, ctx)
	if replay, reserved, err := reserveIdempotency[core.LifecyclePlan](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	p := record.Plan
	_, err = tx.Exec(ctx, `INSERT INTO assembly.lifecycle_plans(lifecycle_plan_id,assembly_id,product_id,operation,version,schema_version,document,source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,source_catalog_checksum,source_target_snapshot_checksum,target_snapshot_checksum,blocking_conflict_count,executable,confirmation_checksum,plan_checksum,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`, p.LifecyclePlanID, p.AssemblyID, p.ProductID, p.Operation, p.Version, p.SchemaVersion, string(p.Document), p.Source.ManifestID, p.Source.ManifestChecksum, p.Source.LockID, p.Source.LockChecksum, p.Source.CatalogChecksum, p.Source.TargetSnapshotChecksum, p.TargetSnapshotChecksum, p.BlockingConflictCount, p.Executable, p.ConfirmationChecksum, p.PlanChecksum, p.CreatedBy, p.CreatedAt)
	if err != nil {
		return core.LifecyclePlan{}, mapWriteError(err)
	}
	if err = insertOutbox(ctx, tx, record.Event); err != nil {
		return core.LifecyclePlan{}, err
	}
	if err = completeIdempotency(ctx, tx, record.Idempotency, p.LifecyclePlanID, p); err != nil {
		return core.LifecyclePlan{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return core.LifecyclePlan{}, err
	}
	return p, nil
}

func (r *Repository) GetLifecyclePlan(ctx context.Context, planID string) (core.LifecyclePlan, error) {
	return scanLifecyclePlan(r.pool.QueryRow(ctx, lifecyclePlanSelect+` WHERE p.lifecycle_plan_id=$1`, planID))
}

func (r *Repository) StartLifecycleOperation(ctx context.Context, record core.StartLifecycleOperationRecord) (core.LifecycleOperation, error) {
	return r.startLifecycleOperation(ctx, record, 0, "")
}

func (r *Repository) StartRollbackOperation(ctx context.Context, record core.StartLifecycleOperationRecord, expectedPredecessorVersion int64) (core.LifecycleOperation, error) {
	return r.startLifecycleOperation(ctx, record, expectedPredecessorVersion, record.Operation.RollbackOfOperationID)
}

func (r *Repository) startLifecycleOperation(ctx context.Context, record core.StartLifecycleOperationRecord, expectedPredecessorVersion int64, predecessorID string) (core.LifecycleOperation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.LifecycleOperation{}, err
	}
	defer rollback(tx, ctx)
	if replay, reserved, err := reserveIdempotency[core.LifecycleOperation](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	if predecessorID != "" {
		var version int64
		var status string
		err = tx.QueryRow(ctx, `SELECT version,status FROM assembly.lifecycle_operations WHERE operation_id=$1 FOR UPDATE`, predecessorID).Scan(&version, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return core.LifecycleOperation{}, core.ErrNotFound
		}
		if err != nil {
			return core.LifecycleOperation{}, err
		}
		if version != expectedPredecessorVersion {
			return core.LifecycleOperation{}, core.ErrVersionConflict
		}
		if status != "completed" {
			return core.LifecycleOperation{}, core.ErrConflict
		}
	}
	if err = lockAndValidateLifecycleHead(ctx, tx, record.Operation); err != nil {
		return core.LifecycleOperation{}, err
	}
	if err = insertLifecycleOperation(ctx, tx, record.Operation); err != nil {
		return core.LifecycleOperation{}, err
	}
	now := record.Operation.CreatedAt
	_, err = tx.Exec(ctx, `INSERT INTO assembly.lifecycle_dispatches(operation_id,state,attempt_count,available_at,created_at,updated_at) VALUES($1,'pending',0,$2,$2,$2)`, record.Operation.OperationID, now)
	if err != nil {
		return core.LifecycleOperation{}, mapWriteError(err)
	}
	t := record.Transition
	_, err = tx.Exec(ctx, `INSERT INTO assembly.lifecycle_artifact_transitions(operation_id,source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,rollback_journal,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, t.OperationID, t.Source.ManifestID, t.Source.ManifestChecksum, t.Source.LockID, t.Source.LockChecksum, t.RollbackJournal, t.CreatedAt)
	if err != nil {
		return core.LifecycleOperation{}, mapWriteError(err)
	}
	if err = insertOutbox(ctx, tx, record.Event); err != nil {
		return core.LifecycleOperation{}, err
	}
	if err = completeIdempotency(ctx, tx, record.Idempotency, record.Operation.OperationID, record.Operation); err != nil {
		return core.LifecycleOperation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return core.LifecycleOperation{}, err
	}
	return record.Operation, nil
}

func insertLifecycleOperation(ctx context.Context, tx pgx.Tx, o core.LifecycleOperation) error {
	source, err := json.Marshal(o.Source)
	if err != nil {
		return err
	}
	var target any
	if o.Target != nil {
		raw, e := json.Marshal(o.Target)
		if e != nil {
			return e
		}
		target = raw
	}
	recovery, err := json.Marshal(o.Recovery)
	if err != nil {
		return err
	}
	diagnostics, _ := json.Marshal([]string{})
	reports, _ := json.Marshal([]string{})
	_, err = tx.Exec(ctx, `INSERT INTO assembly.lifecycle_operations(operation_id,root_operation_id,rollback_of_operation_id,lifecycle_plan_id,assembly_id,product_id,kind,version,status,current_step,schema_version,document,source_state,target_state,recovery,diagnostic_ids,report_ids,operation_checksum,idempotency_key_digest,created_by,created_at,updated_at,completed_at) VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`, o.OperationID, o.RootOperationID, o.RollbackOfOperationID, o.LifecyclePlanID, o.AssemblyID, o.ProductID, o.Kind, o.Version, o.Status, o.CurrentStep, o.SchemaVersion, string(o.Document), source, target, recovery, diagnostics, reports, o.OperationChecksum, o.IdempotencyKeyDigest, o.CreatedBy, o.CreatedAt, o.UpdatedAt, o.CompletedAt)
	return mapWriteError(err)
}

func (r *Repository) GetLifecycleOperation(ctx context.Context, operationID string) (core.LifecycleOperation, error) {
	value, err := scanLifecycleOperation(r.pool.QueryRow(ctx, lifecycleOperationSelect+` WHERE o.operation_id=$1`, operationID))
	if err != nil {
		return core.LifecycleOperation{}, err
	}
	if err = loadLifecycleEvidence(ctx, r.pool, &value); err != nil {
		return core.LifecycleOperation{}, err
	}
	return value, nil
}

func (r *Repository) GetLifecycleTransition(ctx context.Context, operationID string) (core.LifecycleArtifactTransition, error) {
	var value core.LifecycleArtifactTransition
	var sourceManifestID, sourceManifestChecksum, sourceLockID, sourceLockChecksum string
	var targetManifestID, targetManifestChecksum, targetLockID, targetLockChecksum *string
	var targetManifestDocument, targetLockDocument []byte
	var sourceState, targetState []byte
	err := r.pool.QueryRow(ctx, `SELECT t.operation_id,t.source_manifest_id,t.source_manifest_checksum,t.source_lock_id,t.source_lock_checksum,t.target_manifest_id,t.target_manifest_checksum,t.target_manifest_document,t.target_lock_id,t.target_lock_checksum,t.target_lock_document,t.rollback_journal,t.created_at,t.completed_at,o.source_state,o.target_state FROM assembly.lifecycle_artifact_transitions t JOIN assembly.lifecycle_operations o ON o.operation_id=t.operation_id WHERE t.operation_id=$1`, operationID).Scan(&value.OperationID, &sourceManifestID, &sourceManifestChecksum, &sourceLockID, &sourceLockChecksum, &targetManifestID, &targetManifestChecksum, &targetManifestDocument, &targetLockID, &targetLockChecksum, &targetLockDocument, &value.RollbackJournal, &value.CreatedAt, &value.CompletedAt, &sourceState, &targetState)
	if err != nil {
		return core.LifecycleArtifactTransition{}, mapNotFound(err)
	}
	if json.Unmarshal(sourceState, &value.Source) != nil {
		return core.LifecycleArtifactTransition{}, core.ErrDocumentInvalid
	}
	if targetManifestID != nil && targetLockID != nil {
		var target core.LifecycleArtifactState
		if json.Unmarshal(targetState, &target) != nil {
			return core.LifecycleArtifactTransition{}, core.ErrDocumentInvalid
		}
		value.Target = &target
		value.TargetManifestDocument = targetManifestDocument
		value.TargetLockDocument = targetLockDocument
	}
	return value, nil
}

func (r *Repository) CancelLifecycleOperation(ctx context.Context, record core.UpdateLifecycleOperationRecord, idem core.Idempotency) (core.LifecycleOperation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.LifecycleOperation{}, err
	}
	defer rollback(tx, ctx)
	if replay, reserved, err := reserveIdempotency[core.LifecycleOperation](ctx, tx, idem); err != nil || !reserved {
		return replay, err
	}
	var version int64
	var status, dispatchState string
	err = tx.QueryRow(ctx, `SELECT o.version,o.status,d.state FROM assembly.lifecycle_operations o JOIN assembly.lifecycle_dispatches d ON d.operation_id=o.operation_id WHERE o.operation_id=$1 FOR UPDATE OF o,d`, record.Operation.OperationID).Scan(&version, &status, &dispatchState)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.LifecycleOperation{}, core.ErrNotFound
	}
	if err != nil {
		return core.LifecycleOperation{}, err
	}
	if version != record.ExpectedVersion {
		return core.LifecycleOperation{}, core.ErrVersionConflict
	}
	if status != "planned" || dispatchState != "pending" {
		return core.LifecycleOperation{}, core.ErrConflict
	}
	if err = updateLifecycleOperation(ctx, tx, record); err != nil {
		return core.LifecycleOperation{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE assembly.lifecycle_dispatches SET state='cancelled',updated_at=$2 WHERE operation_id=$1`, record.Operation.OperationID, record.Operation.UpdatedAt)
	if err != nil {
		return core.LifecycleOperation{}, err
	}
	if err = insertOutbox(ctx, tx, record.Event); err != nil {
		return core.LifecycleOperation{}, err
	}
	if err = completeIdempotency(ctx, tx, idem, record.Operation.OperationID, record.Operation); err != nil {
		return core.LifecycleOperation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return core.LifecycleOperation{}, err
	}
	return record.Operation, nil
}

func (r *Repository) CancelRun(ctx context.Context, record core.CancelRunRecord) (core.Run, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.Run{}, err
	}
	defer rollback(tx, ctx)
	if replay, reserved, err := reserveIdempotency[core.Run](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	var version int64
	var status, dispatchState string
	err = tx.QueryRow(ctx, `SELECT r.version,r.status,d.state FROM assembly.assembly_runs r JOIN assembly.assembly_run_dispatches d ON d.run_id=r.run_id WHERE r.run_id=$1 FOR UPDATE OF r,d`, record.Run.RunID).Scan(&version, &status, &dispatchState)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Run{}, core.ErrNotFound
	}
	if err != nil {
		return core.Run{}, err
	}
	if version != record.ExpectedVersion {
		return core.Run{}, core.ErrVersionConflict
	}
	if status != "planned" || dispatchState != "pending" {
		return core.Run{}, core.ErrConflict
	}
	run := record.Run
	diagnosticsRaw, _ := json.Marshal(run.DiagnosticIDs)
	recoveryRaw, _ := json.Marshal(run.Recovery)
	_, err = tx.Exec(ctx, `UPDATE assembly.assembly_runs SET version=$2,schema_version=$3,document=$4,document_sha256=$5,status=$6,current_step_id=NULL,diagnostic_ids=$7,recovery=$8,updated_at=$9,completed_at=$10 WHERE run_id=$1`, run.RunID, run.Version, run.SchemaVersion, string(run.Document), run.DocumentSHA256, run.Status, diagnosticsRaw, recoveryRaw, run.UpdatedAt, run.CompletedAt)
	if err != nil {
		return core.Run{}, mapWriteError(err)
	}
	_, err = tx.Exec(ctx, `UPDATE assembly.assembly_run_steps SET status='skipped',finished_at=$2 WHERE run_id=$1 AND status='pending'`, run.RunID, run.UpdatedAt)
	if err != nil {
		return core.Run{}, mapWriteError(err)
	}
	_, err = tx.Exec(ctx, `UPDATE assembly.assembly_run_dispatches SET state='cancelled',updated_at=$2 WHERE run_id=$1`, run.RunID, run.UpdatedAt)
	if err != nil {
		return core.Run{}, err
	}
	if err = insertOutbox(ctx, tx, record.Event); err != nil {
		return core.Run{}, err
	}
	if err = completeIdempotency(ctx, tx, record.Idempotency, run.RunID, run); err != nil {
		return core.Run{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return core.Run{}, err
	}
	return run, nil
}

func (r *Repository) ClaimLifecycleDispatch(ctx context.Context, workerID string, now time.Time, lease time.Duration) (core.LifecycleDispatch, error) {
	if workerID == "" || lease <= 0 {
		return core.LifecycleDispatch{}, core.ErrInvalidCommand
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.LifecycleDispatch{}, err
	}
	defer rollback(tx, ctx)
	var item core.LifecycleDispatch
	err = tx.QueryRow(ctx, `SELECT d.operation_id,o.root_operation_id,o.created_by,o.kind,d.attempt_count FROM assembly.lifecycle_dispatches d JOIN assembly.lifecycle_operations o ON o.operation_id=d.operation_id WHERE (d.state='pending' AND d.available_at<=$1) OR (d.state='leased' AND d.lease_until<=$1) ORDER BY d.available_at,d.created_at,d.operation_id LIMIT 1 FOR UPDATE OF d SKIP LOCKED`, now).Scan(&item.OperationID, &item.RootOperationID, &item.CreatedBy, &item.Kind, &item.AttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.LifecycleDispatch{}, core.ErrNotFound
	}
	if err != nil {
		return core.LifecycleDispatch{}, err
	}
	item.AttemptCount++
	_, err = tx.Exec(ctx, `UPDATE assembly.lifecycle_dispatches SET state='leased',attempt_count=attempt_count+1,lease_owner=$2,lease_until=$3,updated_at=$1 WHERE operation_id=$4`, now, workerID, now.Add(lease), item.OperationID)
	if err != nil {
		return core.LifecycleDispatch{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return core.LifecycleDispatch{}, err
	}
	return item, nil
}

func (r *Repository) RenewLifecycleDispatch(ctx context.Context, operationID, workerID string, now time.Time, lease time.Duration) error {
	tag, err := r.pool.Exec(ctx, `UPDATE assembly.lifecycle_dispatches SET lease_until=$4,updated_at=$3 WHERE operation_id=$1 AND state='leased' AND lease_owner=$2 AND lease_until>$3`, operationID, workerID, now, now.Add(lease))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return core.ErrConflict
	}
	return nil
}
func (r *Repository) CompleteLifecycleDispatch(ctx context.Context, operationID, workerID string, now time.Time) error {
	tag, err := r.pool.Exec(ctx, `UPDATE assembly.lifecycle_dispatches SET state='completed',lease_owner=NULL,lease_until=NULL,last_error_code=NULL,updated_at=$3 WHERE operation_id=$1 AND state='leased' AND lease_owner=$2`, operationID, workerID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return core.ErrConflict
	}
	return nil
}
func (r *Repository) RequeueLifecycleDispatch(ctx context.Context, operationID, workerID, errorCode string, now, availableAt time.Time, dead bool) error {
	state := "pending"
	if dead {
		state = "dead"
	}
	tag, err := r.pool.Exec(ctx, `UPDATE assembly.lifecycle_dispatches SET state=$3,available_at=$4,lease_owner=NULL,lease_until=NULL,last_error_code=$5,updated_at=$6 WHERE operation_id=$1 AND state='leased' AND lease_owner=$2`, operationID, workerID, state, availableAt, errorCode, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return core.ErrConflict
	}
	return nil
}

func (r *Repository) UpdateLifecycleOperation(ctx context.Context, record core.UpdateLifecycleOperationRecord) (core.LifecycleOperation, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return core.LifecycleOperation{}, err
	}
	defer rollback(tx, ctx)
	success := record.Operation.Status == core.LifecycleCompleted || record.Operation.Status == core.LifecycleRolledBack
	if success != (record.Transition != nil && record.Transition.Target != nil) {
		return core.LifecycleOperation{}, core.ErrDocumentInvalid
	}
	var successorManifest core.Manifest
	var successorLock core.GeneratedProjectLock
	if record.Transition != nil && record.Transition.Target != nil {
		successorManifest, successorLock, err = lifecycleSuccessorArtifacts(record)
		if err != nil {
			return core.LifecycleOperation{}, err
		}
		if err = lockAndValidateLifecycleHead(ctx, tx, record.Operation); err != nil {
			return core.LifecycleOperation{}, err
		}
	}
	if err = updateLifecycleOperation(ctx, tx, record); err != nil {
		return core.LifecycleOperation{}, err
	}
	for _, d := range record.Diagnostics {
		remediation, _ := json.Marshal(d.Remediation)
		paths, _ := json.Marshal(d.RelatedPaths)
		_, err = tx.Exec(ctx, `INSERT INTO assembly.lifecycle_diagnostics(operation_id,diagnostic_id,code,severity,category,message,blocking,retryable,remediation,related_paths,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT DO NOTHING`, record.Operation.OperationID, d.DiagnosticID, d.Code, d.Severity, d.Category, d.Message, d.Blocking, d.Retryable, remediation, paths, d.CreatedAt)
		if err != nil {
			return core.LifecycleOperation{}, err
		}
	}
	for _, report := range record.Reports {
		_, err = tx.Exec(ctx, `INSERT INTO assembly.lifecycle_reports(operation_id,report_id,report_type,status,summary,checksum,created_at) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7) ON CONFLICT DO NOTHING`, record.Operation.OperationID, report.ReportID, report.ReportType, report.Status, report.Summary, report.Checksum, report.CreatedAt)
		if err != nil {
			return core.LifecycleOperation{}, err
		}
	}
	if record.Transition != nil && record.Transition.Target != nil {
		t := record.Transition
		if _, err = tx.Exec(ctx, `INSERT INTO assembly.assembly_manifests(assembly_id,product_id,run_id,lifecycle_operation_id,schema_version,document,document_sha256,manifest_sha256,created_at) VALUES($1,$2,NULL,$3,$4,$5,$6,$7,$8)`, successorManifest.AssemblyID, successorManifest.ProductID, record.Operation.OperationID, successorManifest.SchemaVersion, string(successorManifest.Document), successorManifest.DocumentSHA256, successorManifest.ManifestSHA256, successorManifest.CreatedAt); err != nil {
			return core.LifecycleOperation{}, mapWriteError(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO assembly.generated_project_locks(lock_id,product_id,run_id,lifecycle_operation_id,assembly_id,schema_version,document,document_sha256,lock_sha256,created_at) VALUES($1,$2,NULL,$3,$4,$5,$6,$7,$8,$9)`, successorLock.LockID, successorLock.ProductID, record.Operation.OperationID, successorLock.AssemblyID, successorLock.SchemaVersion, string(successorLock.Document), successorLock.DocumentSHA256, successorLock.LockSHA256, successorLock.CreatedAt); err != nil {
			return core.LifecycleOperation{}, mapWriteError(err)
		}
		var tag pgconn.CommandTag
		tag, err = tx.Exec(ctx, `UPDATE assembly.lifecycle_artifact_transitions SET target_manifest_id=$2,target_manifest_checksum=$3,target_manifest_document=$4,target_lock_id=$5,target_lock_checksum=$6,target_lock_document=$7,rollback_journal=$8,completed_at=$9 WHERE operation_id=$1 AND completed_at IS NULL`, t.OperationID, t.Target.ManifestID, t.Target.ManifestChecksum, t.TargetManifestDocument, t.Target.LockID, t.Target.LockChecksum, t.TargetLockDocument, t.RollbackJournal, t.CompletedAt)
		if err != nil {
			return core.LifecycleOperation{}, err
		}
		if tag.RowsAffected() != 1 {
			return core.LifecycleOperation{}, core.ErrConflict
		}
		tag, err = tx.Exec(ctx, `UPDATE assembly.lifecycle_heads SET current_manifest_id=$2,current_lock_id=$3,version=version+1,updated_by_operation_id=$4,updated_at=$5 WHERE root_assembly_id=$1`, record.Operation.AssemblyID, successorManifest.AssemblyID, successorLock.LockID, record.Operation.OperationID, record.Operation.UpdatedAt)
		if err != nil {
			return core.LifecycleOperation{}, err
		}
		if tag.RowsAffected() != 1 {
			return core.LifecycleOperation{}, core.ErrConflict
		}
	}
	if record.Event.EventID != "" {
		if err = insertOutbox(ctx, tx, record.Event); err != nil {
			return core.LifecycleOperation{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return core.LifecycleOperation{}, err
	}
	return record.Operation, nil
}

func lockAndValidateLifecycleHead(ctx context.Context, tx pgx.Tx, operation core.LifecycleOperation) error {
	var headProductID, currentManifestID, currentLockID string
	err := tx.QueryRow(ctx, `SELECT product_id,current_manifest_id,current_lock_id FROM assembly.lifecycle_heads WHERE root_assembly_id=$1 FOR UPDATE`, operation.AssemblyID).
		Scan(&headProductID, &currentManifestID, &currentLockID)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ErrNotFound
	}
	if err != nil {
		return err
	}
	if headProductID != operation.ProductID || currentManifestID != operation.Source.ManifestID || currentLockID != operation.Source.LockID {
		return core.ErrConflict
	}
	var currentManifestChecksum, currentLockChecksum, catalogChecksum, targetSnapshotChecksum string
	err = tx.QueryRow(ctx, `SELECT manifest.manifest_sha256,lock.lock_sha256,lock.document::jsonb->>'catalog_checksum',lock.document::jsonb->>'target_snapshot_checksum'
		FROM assembly.assembly_manifests manifest
		JOIN assembly.generated_project_locks lock ON lock.lock_id=$2 AND lock.product_id=$3
		WHERE manifest.assembly_id=$1 AND manifest.product_id=$3`, currentManifestID, currentLockID, headProductID).
		Scan(&currentManifestChecksum, &currentLockChecksum, &catalogChecksum, &targetSnapshotChecksum)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ErrConflict
	}
	if err != nil {
		return err
	}
	if !secureDigestEqual(currentManifestChecksum, operation.Source.ManifestChecksum) || !secureDigestEqual(currentLockChecksum, operation.Source.LockChecksum) ||
		!secureDigestEqual(catalogChecksum, operation.Source.CatalogChecksum) || !secureDigestEqual(targetSnapshotChecksum, operation.Source.TargetSnapshotChecksum) {
		return core.ErrConflict
	}
	return nil
}

func lifecycleSuccessorArtifacts(record core.UpdateLifecycleOperationRecord) (core.Manifest, core.GeneratedProjectLock, error) {
	t := record.Transition
	if t == nil || t.OperationID != record.Operation.OperationID || t.Source != record.Operation.Source || t.Target == nil || record.Operation.Target == nil || t.CompletedAt == nil || len(t.TargetManifestDocument) == 0 || len(t.TargetLockDocument) == 0 || *record.Operation.Target != *t.Target {
		return core.Manifest{}, core.GeneratedProjectLock{}, core.ErrDocumentInvalid
	}
	var manifestHeader struct {
		SchemaVersion        string `json:"schema_version"`
		AssemblyID           string `json:"assembly_id"`
		LifecycleOperationID string `json:"lifecycle_operation_id"`
		ManifestChecksum     string `json:"manifest_checksum"`
		Product              struct {
			ProductID string `json:"product_id"`
		} `json:"product"`
		CreatedAt time.Time `json:"created_at"`
	}
	var lockHeader struct {
		SchemaVersion            string    `json:"schema_version"`
		LockID                   string    `json:"lock_id"`
		LifecycleOperationID     string    `json:"lifecycle_operation_id"`
		AssemblyManifestChecksum string    `json:"assembly_manifest_checksum"`
		CatalogChecksum          string    `json:"catalog_checksum"`
		TargetSnapshotChecksum   string    `json:"target_snapshot_checksum"`
		LockChecksum             string    `json:"lock_checksum"`
		CreatedAt                time.Time `json:"created_at"`
	}
	if json.Unmarshal(t.TargetManifestDocument, &manifestHeader) != nil || json.Unmarshal(t.TargetLockDocument, &lockHeader) != nil {
		return core.Manifest{}, core.GeneratedProjectLock{}, core.ErrDocumentInvalid
	}
	manifestChecksum, manifestErr := machinecontract.DigestWithoutTopLevelField(t.TargetManifestDocument, "manifest_checksum")
	lockChecksum, lockErr := machinecontract.DigestWithoutTopLevelField(t.TargetLockDocument, "lock_checksum")
	manifestDocumentChecksum, manifestDocumentErr := machinecontract.Digest(t.TargetManifestDocument)
	lockDocumentChecksum, lockDocumentErr := machinecontract.Digest(t.TargetLockDocument)
	if manifestErr != nil || lockErr != nil || manifestDocumentErr != nil || lockDocumentErr != nil ||
		manifestHeader.SchemaVersion == "" || manifestHeader.AssemblyID != t.Target.ManifestID || manifestHeader.Product.ProductID != record.Operation.ProductID || manifestHeader.LifecycleOperationID != record.Operation.OperationID ||
		lockHeader.SchemaVersion == "" || lockHeader.LockID != t.Target.LockID || lockHeader.LifecycleOperationID != record.Operation.OperationID ||
		!secureDigestEqual(manifestChecksum, manifestHeader.ManifestChecksum) || !secureDigestEqual(manifestChecksum, t.Target.ManifestChecksum) ||
		!secureDigestEqual(lockChecksum, lockHeader.LockChecksum) || !secureDigestEqual(lockChecksum, t.Target.LockChecksum) ||
		!secureDigestEqual(lockHeader.AssemblyManifestChecksum, manifestChecksum) || !secureDigestEqual(lockHeader.CatalogChecksum, t.Target.CatalogChecksum) ||
		!secureDigestEqual(lockHeader.TargetSnapshotChecksum, t.Target.TargetSnapshotChecksum) || manifestHeader.CreatedAt.IsZero() || lockHeader.CreatedAt.IsZero() {
		return core.Manifest{}, core.GeneratedProjectLock{}, core.ErrDocumentInvalid
	}
	manifest := core.Manifest{AssemblyID: manifestHeader.AssemblyID, ProductID: record.Operation.ProductID, LifecycleOperationID: record.Operation.OperationID, SchemaVersion: manifestHeader.SchemaVersion, Document: t.TargetManifestDocument, DocumentSHA256: "sha256:" + manifestDocumentChecksum, ManifestSHA256: manifestChecksum, CreatedAt: manifestHeader.CreatedAt}
	lock := core.GeneratedProjectLock{LockID: lockHeader.LockID, ProductID: record.Operation.ProductID, LifecycleOperationID: record.Operation.OperationID, AssemblyID: manifestHeader.AssemblyID, SchemaVersion: lockHeader.SchemaVersion, Document: t.TargetLockDocument, DocumentSHA256: "sha256:" + lockDocumentChecksum, LockSHA256: lockChecksum, CreatedAt: lockHeader.CreatedAt}
	return manifest, lock, nil
}

func secureDigestEqual(left, right string) bool {
	return len(left) == len(right) && hmac.Equal([]byte(left), []byte(right))
}

func updateLifecycleOperation(ctx context.Context, tx pgx.Tx, record core.UpdateLifecycleOperationRecord) error {
	o := record.Operation
	var target any
	if o.Target != nil {
		raw, err := json.Marshal(o.Target)
		if err != nil {
			return err
		}
		target = raw
	}
	recovery, err := json.Marshal(o.Recovery)
	if err != nil {
		return err
	}
	diagnosticIDs := make([]string, len(o.Diagnostics))
	for i := range o.Diagnostics {
		diagnosticIDs[i] = o.Diagnostics[i].DiagnosticID
	}
	reportIDs := make([]string, len(o.Reports))
	for i := range o.Reports {
		reportIDs[i] = o.Reports[i].ReportID
	}
	diagnostics, _ := json.Marshal(diagnosticIDs)
	reports, _ := json.Marshal(reportIDs)
	tag, err := tx.Exec(ctx, `UPDATE assembly.lifecycle_operations SET version=$2,status=$3,current_step=NULLIF($4,''),document=$5,target_state=$6,recovery=$7,diagnostic_ids=$8,report_ids=$9,operation_checksum=$10,updated_at=$11,completed_at=$12 WHERE operation_id=$1 AND version=$13`, o.OperationID, o.Version, o.Status, o.CurrentStep, string(o.Document), target, recovery, diagnostics, reports, o.OperationChecksum, o.UpdatedAt, o.CompletedAt, record.ExpectedVersion)
	if err != nil {
		return mapWriteError(err)
	}
	if tag.RowsAffected() != 1 {
		return core.ErrVersionConflict
	}
	return nil
}

const lifecyclePlanSelect = `SELECT p.lifecycle_plan_id,p.assembly_id,p.product_id,p.operation,p.version,p.schema_version,p.document,p.source_manifest_id,p.source_manifest_checksum,p.source_lock_id,p.source_lock_checksum,p.source_catalog_checksum,p.source_target_snapshot_checksum,p.target_snapshot_checksum,p.blocking_conflict_count,p.executable,p.confirmation_checksum,p.plan_checksum,p.created_by,p.created_at,COALESCE((SELECT e.payload->>'audit_id' FROM assembly.outbox_events e WHERE e.aggregate_id=p.lifecycle_plan_id ORDER BY e.occurred_at DESC LIMIT 1),'') FROM assembly.lifecycle_plans p`
const lifecycleOperationSelect = `SELECT o.operation_id,o.root_operation_id,COALESCE(o.rollback_of_operation_id,''),COALESCE(o.lifecycle_plan_id,''),o.assembly_id,o.product_id,o.kind,o.version,o.status,COALESCE(o.current_step,''),o.schema_version,o.document,o.source_state,o.target_state,o.recovery,o.operation_checksum,o.idempotency_key_digest,o.created_by,o.created_at,o.updated_at,o.completed_at,COALESCE((SELECT e.payload->>'audit_id' FROM assembly.outbox_events e WHERE e.aggregate_id=o.operation_id ORDER BY e.occurred_at DESC LIMIT 1),'') FROM assembly.lifecycle_operations o`

func scanLifecyclePlan(row scanner) (core.LifecyclePlan, error) {
	var p core.LifecyclePlan
	var document []byte
	err := row.Scan(&p.LifecyclePlanID, &p.AssemblyID, &p.ProductID, &p.Operation, &p.Version, &p.SchemaVersion, &document, &p.Source.ManifestID, &p.Source.ManifestChecksum, &p.Source.LockID, &p.Source.LockChecksum, &p.Source.CatalogChecksum, &p.Source.TargetSnapshotChecksum, &p.TargetSnapshotChecksum, &p.BlockingConflictCount, &p.Executable, &p.ConfirmationChecksum, &p.PlanChecksum, &p.CreatedBy, &p.CreatedAt, &p.AuditID)
	if err != nil {
		return core.LifecyclePlan{}, mapNotFound(err)
	}
	projected, err := core.ProjectLifecyclePlanDocument(document)
	if err != nil {
		return core.LifecyclePlan{}, err
	}
	projected.CreatedBy = p.CreatedBy
	projected.AuditID = p.AuditID
	return projected, nil
}
func scanLifecycleOperation(row scanner) (core.LifecycleOperation, error) {
	var o core.LifecycleOperation
	var source, target, recovery []byte
	err := row.Scan(&o.OperationID, &o.RootOperationID, &o.RollbackOfOperationID, &o.LifecyclePlanID, &o.AssemblyID, &o.ProductID, &o.Kind, &o.Version, &o.Status, &o.CurrentStep, &o.SchemaVersion, &o.Document, &source, &target, &recovery, &o.OperationChecksum, &o.IdempotencyKeyDigest, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt, &o.CompletedAt, &o.AuditID)
	if err != nil {
		return core.LifecycleOperation{}, mapNotFound(err)
	}
	if json.Unmarshal(source, &o.Source) != nil || json.Unmarshal(recovery, &o.Recovery) != nil {
		return core.LifecycleOperation{}, core.ErrDocumentInvalid
	}
	if len(target) > 0 && string(target) != "null" {
		var value core.LifecycleArtifactState
		if json.Unmarshal(target, &value) != nil {
			return core.LifecycleOperation{}, core.ErrDocumentInvalid
		}
		o.Target = &value
	}
	return o, nil
}
func loadLifecycleEvidence(ctx context.Context, q queryer, o *core.LifecycleOperation) error {
	diagnostics, err := q.Query(ctx, `SELECT diagnostic_id,code,severity,category,message,blocking,retryable,remediation,related_paths,created_at FROM assembly.lifecycle_diagnostics WHERE operation_id=$1 ORDER BY created_at,diagnostic_id`, o.OperationID)
	if err != nil {
		return err
	}
	defer diagnostics.Close()
	for diagnostics.Next() {
		var d core.RunDiagnostic
		var remediation, paths []byte
		if err = diagnostics.Scan(&d.DiagnosticID, &d.Code, &d.Severity, &d.Category, &d.Message, &d.Blocking, &d.Retryable, &remediation, &paths, &d.CreatedAt); err != nil {
			return err
		}
		if json.Unmarshal(remediation, &d.Remediation) != nil || json.Unmarshal(paths, &d.RelatedPaths) != nil {
			return core.ErrDocumentInvalid
		}
		o.Diagnostics = append(o.Diagnostics, d)
	}
	if err = diagnostics.Err(); err != nil {
		return err
	}
	reports, err := q.Query(ctx, `SELECT report_id,report_type,status,summary,COALESCE(checksum,''),created_at FROM assembly.lifecycle_reports WHERE operation_id=$1 ORDER BY created_at,report_id`, o.OperationID)
	if err != nil {
		return err
	}
	defer reports.Close()
	for reports.Next() {
		var report core.RunReport
		if err = reports.Scan(&report.ReportID, &report.ReportType, &report.Status, &report.Summary, &report.Checksum, &report.CreatedAt); err != nil {
			return err
		}
		o.Reports = append(o.Reports, report)
	}
	return reports.Err()
}
