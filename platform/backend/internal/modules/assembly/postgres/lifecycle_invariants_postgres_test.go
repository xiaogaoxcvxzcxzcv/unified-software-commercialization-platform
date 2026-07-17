package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
	assemblypostgres "platform.local/capability-platform/backend/internal/modules/assembly/postgres"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

const lifecycleInvariantDigestD = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

func TestLifecyclePlanRejectsCrossProductSourceGraph(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	root := seedLifecycleInvariantRoot(t, database.Pool, "cross-product", "product.source", time.Date(2026, 7, 16, 17, 0, 0, 0, time.UTC))

	_, err := database.Pool.Exec(ctx, `INSERT INTO assembly.lifecycle_plans(
		lifecycle_plan_id,assembly_id,product_id,operation,version,schema_version,document,
		source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,
		source_catalog_checksum,source_target_snapshot_checksum,target_snapshot_checksum,
		blocking_conflict_count,executable,confirmation_checksum,plan_checksum,created_by,created_at
	) VALUES($1,$2,$3,'upgrade',1,'1.0.0','{}',$2,$4,$5,$6,$4,$4,$4,0,TRUE,$4,$7,'admin.test',$8)`,
		"lifecycle.cross-product", root.assemblyID, "product.other", root.manifestChecksum, root.lockID, root.lockChecksum, digestC, root.now.Add(time.Minute))
	if err == nil {
		t.Fatal("cross-product lifecycle plan unexpectedly accepted")
	}
}

func TestLifecycleHeadRejectsSuccessorFromPlannedOperation(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	root := seedLifecycleInvariantRoot(t, database.Pool, "planned-head", "product.planned-head", time.Date(2026, 7, 16, 18, 0, 0, 0, time.UTC))
	planID := insertLifecycleInvariantPlan(t, database.Pool, root, "lifecycle.planned-head", digestC)
	operationID := "operation.planned-head"
	insertLifecycleInvariantOperation(t, database.Pool, root, planID, operationID, "planned", root.now.Add(2*time.Minute), nil)

	successorManifestID := "assembly.planned-successor"
	successorLockID := "lock.planned-successor"
	_, manifestErr := database.Pool.Exec(ctx, `INSERT INTO assembly.assembly_manifests(assembly_id,product_id,run_id,lifecycle_operation_id,schema_version,document,document_sha256,manifest_sha256,created_at) VALUES($1,$2,NULL,$3,'1.0.0','{}',$4,$5,$6)`, successorManifestID, root.productID, operationID, digestA, digestB, root.now.Add(3*time.Minute))
	var lockErr, updateErr error
	if manifestErr == nil {
		_, lockErr = database.Pool.Exec(ctx, `INSERT INTO assembly.generated_project_locks(lock_id,product_id,run_id,lifecycle_operation_id,assembly_id,schema_version,document,document_sha256,lock_sha256,created_at) VALUES($1,$2,NULL,$3,$4,'1.0.0','{}',$5,$6,$7)`, successorLockID, root.productID, operationID, successorManifestID, digestB, digestC, root.now.Add(3*time.Minute))
	}
	if manifestErr == nil && lockErr == nil {
		_, updateErr = database.Pool.Exec(ctx, `UPDATE assembly.lifecycle_heads SET current_manifest_id=$2,current_lock_id=$3,version=version+1,updated_by_operation_id=$4,updated_at=$5 WHERE root_assembly_id=$1`, root.assemblyID, successorManifestID, successorLockID, operationID, root.now.Add(4*time.Minute))
	}
	if manifestErr == nil && lockErr == nil && updateErr == nil {
		t.Error("planned lifecycle operation unexpectedly advanced the head")
	}
	var manifestID, lockID string
	var version int64
	if err := database.Pool.QueryRow(ctx, `SELECT current_manifest_id,current_lock_id,version FROM assembly.lifecycle_heads WHERE root_assembly_id=$1`, root.assemblyID).Scan(&manifestID, &lockID, &version); err != nil {
		t.Fatal(err)
	}
	if manifestID != root.assemblyID || lockID != root.lockID || version != 1 {
		t.Fatalf("head changed after rejected planned successor: manifest=%s lock=%s version=%d", manifestID, lockID, version)
	}
}

func TestCompletedLifecycleTransitionRejectsFurtherMutation(t *testing.T) {
	database := testpostgres.Open(t)
	repository := assemblypostgres.New(database.Pool)
	ctx := context.Background()
	root := seedLifecycleInvariantRoot(t, database.Pool, "transition-mutation", "product.transition-mutation", time.Date(2026, 7, 16, 19, 0, 0, 0, time.UTC))
	planID := insertLifecycleInvariantPlan(t, database.Pool, root, "lifecycle.transition-mutation", digestC)
	operationID := "operation.transition-mutation"
	createdAt := root.now.Add(2 * time.Minute)
	completedAt := root.now.Add(3 * time.Minute)
	insertLifecycleInvariantOperation(t, database.Pool, root, planID, operationID, "executing", createdAt, nil)
	insertLifecycleInvariantTransition(t, database.Pool, root, operationID, createdAt)
	manifestRaw, lockRaw, target := lifecycleSuccessorDocuments(t, operationID, "assembly.transition-target", "lock.transition-target", root.productID, digestB, digestC, completedAt)
	operation := lifecycleInvariantCompletedOperation(root, planID, operationID, target, createdAt, completedAt)
	transition := core.LifecycleArtifactTransition{OperationID: operationID, Source: lifecycleInvariantSource(root), Target: &target, TargetManifestDocument: manifestRaw, TargetLockDocument: lockRaw, RollbackJournal: json.RawMessage(`{"state":"committed"}`), CreatedAt: createdAt, CompletedAt: &completedAt}
	if _, err := repository.UpdateLifecycleOperation(ctx, core.UpdateLifecycleOperationRecord{Operation: operation, ExpectedVersion: 1, Transition: &transition}); err != nil {
		t.Fatal(err)
	}

	_, updateErr := database.Pool.Exec(ctx, `UPDATE assembly.lifecycle_artifact_transitions SET target_manifest_document=jsonb_build_object('tampered',TRUE) WHERE operation_id=$1`, operationID)
	if updateErr == nil {
		t.Error("completed lifecycle transition unexpectedly accepted a second update")
	}
	var tampered bool
	if err := database.Pool.QueryRow(ctx, `SELECT target_manifest_document ? 'tampered' FROM assembly.lifecycle_artifact_transitions WHERE operation_id=$1`, operationID).Scan(&tampered); err != nil {
		t.Fatal(err)
	}
	if tampered {
		t.Fatal("completed lifecycle transition was mutated")
	}
}

func TestLifecycleRepositoryRejectsCrossOperationTransitionAtomically(t *testing.T) {
	database := testpostgres.Open(t)
	repository := assemblypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 20, 0, 0, 0, time.UTC)
	rootA := seedLifecycleInvariantRoot(t, database.Pool, "operation-a", "product.operation-a", now)
	rootB := seedLifecycleInvariantRootWithChecksums(t, database.Pool, "operation-b", "product.operation-b", digestC, lifecycleInvariantDigestD, now.Add(time.Second))
	planA := insertLifecycleInvariantPlan(t, database.Pool, rootA, "lifecycle.operation-a", digestC)
	planB := insertLifecycleInvariantPlan(t, database.Pool, rootB, "lifecycle.operation-b", digestA)
	operationAID := "operation.invariant-a"
	operationBID := "operation.invariant-b"
	insertLifecycleInvariantOperation(t, database.Pool, rootA, planA, operationAID, "executing", now.Add(2*time.Minute), nil)
	insertLifecycleInvariantOperation(t, database.Pool, rootB, planB, operationBID, "executing", now.Add(2*time.Minute), nil)
	insertLifecycleInvariantTransition(t, database.Pool, rootA, operationAID, now.Add(2*time.Minute))
	insertLifecycleInvariantTransition(t, database.Pool, rootB, operationBID, now.Add(2*time.Minute))

	completedAt := now.Add(3 * time.Minute)
	manifestRaw, lockRaw, target := lifecycleSuccessorDocuments(t, operationAID, "assembly.invariant-target", "lock.invariant-target", rootA.productID, digestB, digestC, completedAt)
	operation := core.LifecycleOperation{
		OperationID: operationAID, RootOperationID: operationAID, LifecyclePlanID: planA,
		AssemblyID: rootA.assemblyID, ProductID: rootA.productID, Kind: core.LifecycleUpgrade,
		Version: 2, Status: core.LifecycleCompleted, SchemaVersion: "1.0.0", Document: json.RawMessage(`{}`),
		Source: lifecycleInvariantSource(rootA), Target: &target, Recovery: core.LifecycleRecovery{RollbackAvailable: true},
		OperationChecksum: digestB, IdempotencyKeyDigest: digestC, CreatedBy: "admin.test",
		CreatedAt: now.Add(2 * time.Minute), UpdatedAt: completedAt, CompletedAt: &completedAt,
	}
	transition := core.LifecycleArtifactTransition{
		OperationID: operationBID, Source: lifecycleInvariantSource(rootB), Target: &target,
		TargetManifestDocument: manifestRaw, TargetLockDocument: lockRaw,
		RollbackJournal: json.RawMessage(`{"state":"committed"}`), CreatedAt: now.Add(2 * time.Minute), CompletedAt: &completedAt,
	}
	if _, err := repository.UpdateLifecycleOperation(ctx, core.UpdateLifecycleOperationRecord{Operation: operation, ExpectedVersion: 1, Transition: &transition}); err == nil {
		t.Error("repository accepted a transition owned by another operation")
	}

	var operationVersion int64
	var operationStatus, headManifestID, headLockID string
	var headVersion int64
	if err := database.Pool.QueryRow(ctx, `SELECT version,status FROM assembly.lifecycle_operations WHERE operation_id=$1`, operationAID).Scan(&operationVersion, &operationStatus); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT current_manifest_id,current_lock_id,version FROM assembly.lifecycle_heads WHERE root_assembly_id=$1`, rootA.assemblyID).Scan(&headManifestID, &headLockID, &headVersion); err != nil {
		t.Fatal(err)
	}
	var artifactCount int
	if err := database.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM assembly.assembly_manifests WHERE assembly_id=$1)+(SELECT count(*) FROM assembly.generated_project_locks WHERE lock_id=$2)`, target.ManifestID, target.LockID).Scan(&artifactCount); err != nil {
		t.Fatal(err)
	}
	var transitionCompletedAt *time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT completed_at FROM assembly.lifecycle_artifact_transitions WHERE operation_id=$1`, operationBID).Scan(&transitionCompletedAt); err != nil {
		t.Fatal(err)
	}
	if operationVersion != 1 || operationStatus != "executing" || headManifestID != rootA.assemblyID || headLockID != rootA.lockID || headVersion != 1 || artifactCount != 0 || transitionCompletedAt != nil {
		t.Fatalf("cross-operation rejection partially committed: operation=%d/%s head=%s/%s/%d artifacts=%d transition_completed=%v", operationVersion, operationStatus, headManifestID, headLockID, headVersion, artifactCount, transitionCompletedAt)
	}
}

func TestLifecycleHeadRejectsRewindToHistoricalSuccessor(t *testing.T) {
	database := testpostgres.Open(t)
	repository := assemblypostgres.New(database.Pool)
	ctx := context.Background()
	root := seedLifecycleInvariantRoot(t, database.Pool, "historical-rewind", "product.historical-rewind", time.Date(2026, 7, 16, 21, 0, 0, 0, time.UTC))

	first := completeLifecycleInvariantGeneration(t, database.Pool, repository, root, lifecycleInvariantSource(root), "first", digestC, digestB, digestC, root.now.Add(2*time.Minute))
	second := completeLifecycleInvariantGeneration(t, database.Pool, repository, root, first, "second", lifecycleInvariantDigestD, digestC, lifecycleInvariantDigestD, root.now.Add(5*time.Minute))

	_, err := database.Pool.Exec(ctx, `UPDATE assembly.lifecycle_heads SET current_manifest_id=$2,current_lock_id=$3,version=version+1,updated_by_operation_id=$4,updated_at=$5 WHERE root_assembly_id=$1`,
		root.assemblyID, first.ManifestID, first.LockID, "operation.historical-rewind.first", root.now.Add(8*time.Minute))
	if err == nil {
		t.Fatal("lifecycle head unexpectedly rewound from the current successor to a historical successor")
	}

	var manifestID, lockID string
	var version int64
	if err := database.Pool.QueryRow(ctx, `SELECT current_manifest_id,current_lock_id,version FROM assembly.lifecycle_heads WHERE root_assembly_id=$1`, root.assemblyID).Scan(&manifestID, &lockID, &version); err != nil {
		t.Fatal(err)
	}
	if manifestID != second.ManifestID || lockID != second.LockID || version != 3 {
		t.Fatalf("head changed after rejected historical rewind: manifest=%s lock=%s version=%d", manifestID, lockID, version)
	}
}

func TestLifecycleOperationRejectsPlanFromAnotherProduct(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 22, 0, 0, 0, time.UTC)
	rootA := seedLifecycleInvariantRoot(t, database.Pool, "plan-product-a", "product.plan-a", now)
	rootB := seedLifecycleInvariantRootWithChecksums(t, database.Pool, "plan-product-b", "product.plan-b", digestC, lifecycleInvariantDigestD, now.Add(time.Second))
	planA := insertLifecycleInvariantPlan(t, database.Pool, rootA, "lifecycle.plan-product-a", digestC)

	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO assembly.lifecycle_operations(
		operation_id,root_operation_id,lifecycle_plan_id,assembly_id,product_id,kind,version,status,schema_version,document,
		source_state,recovery,diagnostic_ids,report_ids,operation_checksum,idempotency_key_digest,created_by,created_at,updated_at
	) VALUES($1,$1,$2,$3,$4,'upgrade',1,'planned','1.0.0','{}',$5,'{}','[]','[]',$6,$7,'admin.test',$8,$8)`,
		"operation.plan-product-mismatch", planA, rootB.assemblyID, rootB.productID,
		mustLifecycleInvariantJSON(t, lifecycleInvariantSource(rootB)), digestA, digestB, now.Add(2*time.Minute))
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err == nil {
		t.Fatal("lifecycle operation unexpectedly accepted a lifecycle plan owned by another product")
	}
}

func TestLifecycleOperationRejectsStaleCurrentSource(t *testing.T) {
	database := testpostgres.Open(t)
	repository := assemblypostgres.New(database.Pool)
	ctx := context.Background()
	root := seedLifecycleInvariantRoot(t, database.Pool, "stale-operation-source", "product.stale-operation-source", time.Date(2026, 7, 16, 23, 0, 0, 0, time.UTC))
	completeLifecycleInvariantGeneration(t, database.Pool, repository, root, lifecycleInvariantSource(root), "advance", digestC, digestB, digestC, root.now.Add(2*time.Minute))

	stalePlanID := insertLifecycleInvariantPlan(t, database.Pool, root, "lifecycle.stale-operation-source", lifecycleInvariantDigestD)
	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO assembly.lifecycle_operations(
		operation_id,root_operation_id,lifecycle_plan_id,assembly_id,product_id,kind,version,status,schema_version,document,
		source_state,recovery,diagnostic_ids,report_ids,operation_checksum,idempotency_key_digest,created_by,created_at,updated_at
	) VALUES($1,$1,$2,$3,$4,'upgrade',1,'planned','1.0.0','{}',$5,'{}','[]','[]',$6,$7,'admin.test',$8,$8)`,
		"operation.stale-operation-source", stalePlanID, root.assemblyID, root.productID,
		mustLifecycleInvariantJSON(t, lifecycleInvariantSource(root)), digestA, digestB, root.now.Add(6*time.Minute))
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err == nil {
		t.Fatal("lifecycle operation unexpectedly accepted a source that is no longer the current head")
	}
}

func TestLifecycleOperationStartRevalidatesSourceAfterConcurrentHeadAdvance(t *testing.T) {
	database := testpostgres.Open(t)
	repository := assemblypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	root := seedLifecycleInvariantRoot(t, database.Pool, "start-toctou", "product.start-toctou", now)
	firstPlanID := insertLifecycleInvariantPlan(t, database.Pool, root, "lifecycle.start-toctou-first", digestC)
	firstOperationID := "operation.start-toctou-first"
	firstCreatedAt := now.Add(2 * time.Minute)
	insertLifecycleInvariantOperation(t, database.Pool, root, firstPlanID, firstOperationID, "executing", firstCreatedAt, nil)
	insertLifecycleInvariantTransition(t, database.Pool, root, firstOperationID, firstCreatedAt)
	secondPlanID := insertLifecycleInvariantPlan(t, database.Pool, root, "lifecycle.start-toctou-second", lifecycleInvariantDigestD)

	advanceTx, err := database.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer advanceTx.Rollback(ctx)
	if _, err = advanceTx.Exec(ctx, `SELECT 1 FROM assembly.lifecycle_heads WHERE root_assembly_id=$1 FOR UPDATE`, root.assemblyID); err != nil {
		t.Fatal(err)
	}

	secondCreatedAt := now.Add(3 * time.Minute)
	secondOperation := core.LifecycleOperation{
		OperationID: "operation.start-toctou-second", RootOperationID: "operation.start-toctou-second", LifecyclePlanID: secondPlanID,
		AssemblyID: root.assemblyID, ProductID: root.productID, Kind: core.LifecycleUpgrade,
		Version: 1, Status: core.LifecyclePlanned, SchemaVersion: "1.0.0", Document: json.RawMessage(`{}`),
		Source: lifecycleInvariantSource(root), Recovery: core.LifecycleRecovery{}, OperationChecksum: digestA,
		IdempotencyKeyDigest: lifecycleInvariantDigestD, CreatedBy: "admin.test", CreatedAt: secondCreatedAt, UpdatedAt: secondCreatedAt,
	}
	startResult := make(chan error, 1)
	go func() {
		_, startErr := repository.StartLifecycleOperation(ctx, core.StartLifecycleOperationRecord{
			Operation:   secondOperation,
			Idempotency: idem("assembly.lifecycle.execute", "admin.test", secondPlanID, lifecycleInvariantDigestD, digestC, secondCreatedAt),
			Event:       event("event.start-toctou-second", "audit.start-toctou-second", "assembly.lifecycle_started.v1", secondOperation.OperationID, root.productID),
			Transition:  core.LifecycleArtifactTransition{OperationID: secondOperation.OperationID, Source: secondOperation.Source, RollbackJournal: json.RawMessage(`{}`), CreatedAt: secondCreatedAt},
		})
		startResult <- startErr
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked bool
		err = database.Pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM pg_stat_activity
			WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock'
			  AND query LIKE '%FROM assembly.lifecycle_heads WHERE root_assembly_id%'
			  AND query LIKE '%FOR UPDATE%'
		)`).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("concurrent lifecycle start did not block on the locked lifecycle head")
		}
		time.Sleep(10 * time.Millisecond)
	}

	completedAt := now.Add(4 * time.Minute)
	manifestRaw, lockRaw, target := lifecycleSuccessorDocuments(t, firstOperationID, "assembly.start-toctou-next", "lock.start-toctou-next", root.productID, digestB, digestC, completedAt)
	targetRaw := mustLifecycleInvariantJSON(t, target)
	manifestDocumentDigest, err := machinecontract.Digest(manifestRaw)
	if err != nil {
		t.Fatal(err)
	}
	lockDocumentDigest, err := machinecontract.Digest(lockRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = advanceTx.Exec(ctx, `UPDATE assembly.lifecycle_operations SET version=2,status='completed',current_step=NULL,target_state=$2,recovery='{"rollback_available":true}',operation_checksum=$3,updated_at=$4,completed_at=$4 WHERE operation_id=$1`, firstOperationID, targetRaw, digestB, completedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = advanceTx.Exec(ctx, `INSERT INTO assembly.assembly_manifests(assembly_id,product_id,run_id,lifecycle_operation_id,schema_version,document,document_sha256,manifest_sha256,created_at) VALUES($1,$2,NULL,$3,'1.0.0',$4,$5,$6,$7)`, target.ManifestID, root.productID, firstOperationID, manifestRaw, "sha256:"+manifestDocumentDigest, target.ManifestChecksum, completedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = advanceTx.Exec(ctx, `INSERT INTO assembly.generated_project_locks(lock_id,product_id,run_id,lifecycle_operation_id,assembly_id,schema_version,document,document_sha256,lock_sha256,created_at) VALUES($1,$2,NULL,$3,$4,'1.0.0',$5,$6,$7,$8)`, target.LockID, root.productID, firstOperationID, target.ManifestID, lockRaw, "sha256:"+lockDocumentDigest, target.LockChecksum, completedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = advanceTx.Exec(ctx, `UPDATE assembly.lifecycle_artifact_transitions SET target_manifest_id=$2,target_manifest_checksum=$3,target_manifest_document=$4,target_lock_id=$5,target_lock_checksum=$6,target_lock_document=$7,rollback_journal='{"state":"committed"}',completed_at=$8 WHERE operation_id=$1`, firstOperationID, target.ManifestID, target.ManifestChecksum, manifestRaw, target.LockID, target.LockChecksum, lockRaw, completedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = advanceTx.Exec(ctx, `UPDATE assembly.lifecycle_heads SET current_manifest_id=$2,current_lock_id=$3,version=version+1,updated_by_operation_id=$4,updated_at=$5 WHERE root_assembly_id=$1`, root.assemblyID, target.ManifestID, target.LockID, firstOperationID, completedAt); err != nil {
		t.Fatal(err)
	}
	if err = advanceTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case err = <-startResult:
		if !errors.Is(err, core.ErrConflict) {
			t.Fatalf("concurrent stale lifecycle start error = %v, want ErrConflict", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent stale lifecycle start did not return after head advance")
	}
	var secondOperationCount, secondDispatchCount, secondTransitionCount int
	if err = database.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM assembly.lifecycle_operations WHERE operation_id=$1),
		(SELECT count(*) FROM assembly.lifecycle_dispatches WHERE operation_id=$1),
		(SELECT count(*) FROM assembly.lifecycle_artifact_transitions WHERE operation_id=$1)`, secondOperation.OperationID).Scan(&secondOperationCount, &secondDispatchCount, &secondTransitionCount); err != nil {
		t.Fatal(err)
	}
	if secondOperationCount != 0 || secondDispatchCount != 0 || secondTransitionCount != 0 {
		t.Fatalf("stale lifecycle start partially committed: operation=%d dispatch=%d transition=%d", secondOperationCount, secondDispatchCount, secondTransitionCount)
	}
}

func TestLifecycleRepositoryRejectsSuccessfulTerminalStateWithoutTransitionAtomically(t *testing.T) {
	database := testpostgres.Open(t)
	repository := assemblypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 1, 0, 0, 0, time.UTC)
	root := seedLifecycleInvariantRoot(t, database.Pool, "terminal-without-transition", "product.terminal-without-transition", now)
	planID := insertLifecycleInvariantPlan(t, database.Pool, root, "lifecycle.terminal-without-transition", digestC)
	operationID := "operation.terminal-without-transition"
	createdAt := now.Add(2 * time.Minute)
	completedAt := now.Add(3 * time.Minute)
	insertLifecycleInvariantOperation(t, database.Pool, root, planID, operationID, "executing", createdAt, nil)
	insertLifecycleInvariantTransition(t, database.Pool, root, operationID, createdAt)
	_, _, target := lifecycleSuccessorDocuments(t, operationID, "assembly.terminal-without-transition-next", "lock.terminal-without-transition-next", root.productID, digestB, digestC, completedAt)
	operation := lifecycleInvariantCompletedOperation(root, planID, operationID, target, createdAt, completedAt)

	_, err := repository.UpdateLifecycleOperation(ctx, core.UpdateLifecycleOperationRecord{Operation: operation, ExpectedVersion: 1})
	if !errors.Is(err, core.ErrDocumentInvalid) {
		t.Fatalf("successful terminal update without transition error = %v, want ErrDocumentInvalid", err)
	}
	var version, headVersion int64
	var status, headManifestID, headLockID string
	var completedTransitionAt *time.Time
	if err = database.Pool.QueryRow(ctx, `SELECT version,status FROM assembly.lifecycle_operations WHERE operation_id=$1`, operationID).Scan(&version, &status); err != nil {
		t.Fatal(err)
	}
	if err = database.Pool.QueryRow(ctx, `SELECT current_manifest_id,current_lock_id,version FROM assembly.lifecycle_heads WHERE root_assembly_id=$1`, root.assemblyID).Scan(&headManifestID, &headLockID, &headVersion); err != nil {
		t.Fatal(err)
	}
	if err = database.Pool.QueryRow(ctx, `SELECT completed_at FROM assembly.lifecycle_artifact_transitions WHERE operation_id=$1`, operationID).Scan(&completedTransitionAt); err != nil {
		t.Fatal(err)
	}
	var successorCount int
	if err = database.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM assembly.assembly_manifests WHERE lifecycle_operation_id=$1)+(SELECT count(*) FROM assembly.generated_project_locks WHERE lifecycle_operation_id=$1)`, operationID).Scan(&successorCount); err != nil {
		t.Fatal(err)
	}
	if version != 1 || status != "executing" || headManifestID != root.assemblyID || headLockID != root.lockID || headVersion != 1 || completedTransitionAt != nil || successorCount != 0 {
		t.Fatalf("rejected successful terminal update partially committed: operation=%d/%s head=%s/%s/v%d transition=%v successors=%d", version, status, headManifestID, headLockID, headVersion, completedTransitionAt, successorCount)
	}
}

func TestLifecycleRollbackCanRetryOnlyAfterRollbackFailed(t *testing.T) {
	database := testpostgres.Open(t)
	repository := assemblypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 2, 0, 0, 0, time.UTC)
	root := seedLifecycleInvariantRoot(t, database.Pool, "rollback-retry", "product.rollback-retry", now)
	source := completeLifecycleInvariantGeneration(t, database.Pool, repository, root, lifecycleInvariantSource(root), "upgrade", digestC, digestB, digestC, now.Add(2*time.Minute))
	predecessorID := "operation.rollback-retry.upgrade"

	insertRollback := func(operationID, status string, createdAt time.Time, completedAt *time.Time) error {
		_, err := database.Pool.Exec(ctx, `INSERT INTO assembly.lifecycle_operations(
			operation_id,root_operation_id,rollback_of_operation_id,lifecycle_plan_id,assembly_id,product_id,kind,
			version,status,schema_version,document,source_state,target_state,recovery,diagnostic_ids,report_ids,
			operation_checksum,idempotency_key_digest,created_by,created_at,updated_at,completed_at
		) VALUES($1,$2,$2,NULL,$3,$4,'rollback',1,$5,'1.0.0','{}',$6,NULL,'{}','[]','[]',$7,$8,'admin.test',$9,$9,$10)`,
			operationID, predecessorID, root.assemblyID, root.productID, status, mustLifecycleInvariantJSON(t, source),
			digestA, digestB, createdAt, completedAt)
		return err
	}
	type rollbackUpdate struct {
		version  int
		status   string
		at       time.Time
		terminal bool
	}
	advanceRollback := func(operationID string, updates []rollbackUpdate) {
		for _, update := range updates {
			var completedAt *time.Time
			if update.terminal {
				completedAt = &update.at
			}
			_, err := database.Pool.Exec(ctx, `UPDATE assembly.lifecycle_operations
				SET version=$2,status=$3,operation_checksum=$4,updated_at=$5,completed_at=$6
				WHERE operation_id=$1`, operationID, update.version, update.status, digestC, update.at, completedAt)
			if err != nil {
				t.Fatalf("advance %s to %s: %v", operationID, update.status, err)
			}
		}
	}

	if err := insertRollback("operation.rollback-retry.first", "planned", now.Add(4*time.Minute), nil); err != nil {
		t.Fatalf("insert first rollback operation: %v", err)
	}
	if err := insertRollback("operation.rollback-retry.before-failure", "planned", now.Add(5*time.Minute), nil); err == nil {
		t.Fatal("concurrent rollback before failure unexpectedly accepted")
	}
	advanceRollback("operation.rollback-retry.first", []rollbackUpdate{
		{version: 2, status: "executing", at: now.Add(5 * time.Minute)},
		{version: 3, status: "rolling_back", at: now.Add(6 * time.Minute)},
		{version: 4, status: "rollback_failed", at: now.Add(7 * time.Minute), terminal: true},
	})

	if err := insertRollback("operation.rollback-retry.second", "planned", now.Add(8*time.Minute), nil); err != nil {
		t.Fatalf("rollback_failed operation prevented retry: %v", err)
	}
	if err := insertRollback("operation.rollback-retry.concurrent", "planned", now.Add(9*time.Minute), nil); err == nil {
		t.Fatal("concurrent rollback retry unexpectedly accepted")
	}
	advanceRollback("operation.rollback-retry.second", []rollbackUpdate{
		{version: 2, status: "executing", at: now.Add(9 * time.Minute)},
		{version: 3, status: "rolling_back", at: now.Add(10 * time.Minute)},
		{version: 4, status: "rolled_back", at: now.Add(11 * time.Minute), terminal: true},
	})
	if err := insertRollback("operation.rollback-retry.after-success", "planned", now.Add(12*time.Minute), nil); err == nil {
		t.Fatal("successful rollback unexpectedly allowed a repeated rollback")
	}

	var failedHistory, successfulRollback int
	if err := database.Pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status='rollback_failed'),
		count(*) FILTER (WHERE status='rolled_back')
		FROM assembly.lifecycle_operations WHERE rollback_of_operation_id=$1`, predecessorID).Scan(&failedHistory, &successfulRollback); err != nil {
		t.Fatal(err)
	}
	if failedHistory != 1 || successfulRollback != 1 {
		t.Fatalf("rollback history failed=%d rolled_back=%d", failedHistory, successfulRollback)
	}
}

type lifecycleInvariantRoot struct {
	assemblyID, lockID, productID, manifestChecksum, lockChecksum string
	now                                                           time.Time
}

func seedLifecycleInvariantRoot(t *testing.T, pool *pgxpool.Pool, suffix, productID string, now time.Time) lifecycleInvariantRoot {
	t.Helper()
	return seedLifecycleInvariantRootWithChecksums(t, pool, suffix, productID, digestA, digestB, now)
}

func seedLifecycleInvariantRootWithChecksums(t *testing.T, pool *pgxpool.Pool, suffix, productID, manifestChecksum, lockChecksum string, now time.Time) lifecycleInvariantRoot {
	t.Helper()
	ctx := context.Background()
	blueprintID := "bp." + suffix
	planID := "plan." + suffix
	runID := "run." + suffix
	assemblyID := "assembly." + suffix
	lockID := "lock." + suffix
	queries := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO assembly.product_blueprints(blueprint_id,revision,document_version,schema_version,document,content_sha256,created_by,created_at) VALUES($1,1,'1.0.0','1.0.0','{}',$2,'admin.test',$3)`, []any{blueprintID, digestA, now}},
		{`INSERT INTO assembly.assembly_plans(plan_id,blueprint_id,blueprint_revision,version,environment,schema_version,document,blueprint_sha256,catalog_revision,catalog_snapshot_sha256,plan_sha256,executable,created_by,created_at,updated_at) VALUES($1,$2,1,1,'test','1.0.0','{}',$3,'catalog.test',$3,$3,TRUE,'admin.test',$4,$4)`, []any{planID, blueprintID, digestA, now}},
		{`INSERT INTO assembly.assembly_runs(run_id,root_run_id,attempt_number,plan_id,plan_version,version,plan_sha256,schema_version,document,document_sha256,idempotency_key_digest,output_target_ref,status,diagnostic_ids,recovery,created_by,created_at,updated_at) VALUES($1,$1,1,$2,1,1,$3,'1.0.0','{}',$3,$4,'workspace.default','planned','[]','{}','admin.test',$5,$5)`, []any{runID, planID, digestA, digestB, now}},
		{`INSERT INTO assembly.assembly_manifests(assembly_id,product_id,run_id,schema_version,document,document_sha256,manifest_sha256,created_at) VALUES($1,$2,$3,'1.0.0','{}',$4,$4,$5)`, []any{assemblyID, productID, runID, manifestChecksum, now}},
		{`INSERT INTO assembly.generated_project_locks(lock_id,product_id,run_id,assembly_id,schema_version,document,document_sha256,lock_sha256,created_at) VALUES($1,$2,$3,$4,'1.0.0',$5,$6,$6,$7)`, []any{lockID, productID, runID, assemblyID, `{"catalog_checksum":"` + digestA + `","target_snapshot_checksum":"` + digestA + `"}`, lockChecksum, now}},
		{`INSERT INTO assembly.lifecycle_heads(root_assembly_id,product_id,current_manifest_id,current_lock_id,version,updated_at) VALUES($1,$2,$1,$3,1,$4)`, []any{assemblyID, productID, lockID, now}},
	}
	for _, item := range queries {
		if _, err := pool.Exec(ctx, item.query, item.args...); err != nil {
			t.Fatal(err)
		}
	}
	return lifecycleInvariantRoot{assemblyID: assemblyID, lockID: lockID, productID: productID, manifestChecksum: manifestChecksum, lockChecksum: lockChecksum, now: now}
}

func insertLifecycleInvariantPlan(t *testing.T, pool *pgxpool.Pool, root lifecycleInvariantRoot, planID, planChecksum string) string {
	t.Helper()
	source := lifecycleInvariantSource(root)
	_, err := pool.Exec(context.Background(), `INSERT INTO assembly.lifecycle_plans(
		lifecycle_plan_id,assembly_id,product_id,operation,version,schema_version,document,
		source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,
		source_catalog_checksum,source_target_snapshot_checksum,target_snapshot_checksum,
		blocking_conflict_count,executable,confirmation_checksum,plan_checksum,created_by,created_at
	) VALUES($1,$2,$3,'upgrade',1,'1.0.0','{}',$4,$5,$6,$7,$8,$9,$9,0,TRUE,$10,$11,'admin.test',$12)`,
		planID, root.assemblyID, root.productID, source.ManifestID, source.ManifestChecksum, source.LockID, source.LockChecksum,
		source.CatalogChecksum, source.TargetSnapshotChecksum, digestA, planChecksum, root.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return planID
}

func insertLifecycleInvariantOperation(t *testing.T, pool *pgxpool.Pool, root lifecycleInvariantRoot, planID, operationID, status string, createdAt time.Time, completedAt *time.Time) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO assembly.lifecycle_operations(
		operation_id,root_operation_id,lifecycle_plan_id,assembly_id,product_id,kind,version,status,schema_version,document,
		source_state,recovery,diagnostic_ids,report_ids,operation_checksum,idempotency_key_digest,created_by,created_at,updated_at,completed_at
	) VALUES($1,$1,$2,$3,$4,'upgrade',1,$5,'1.0.0','{}',$6,'{}','[]','[]',$7,$8,'admin.test',$9,$9,$10)`,
		operationID, planID, root.assemblyID, root.productID, status, mustLifecycleInvariantJSON(t, lifecycleInvariantSource(root)), digestA, digestC, createdAt, completedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func insertLifecycleInvariantTransition(t *testing.T, pool *pgxpool.Pool, root lifecycleInvariantRoot, operationID string, createdAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO assembly.lifecycle_artifact_transitions(operation_id,source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,rollback_journal,created_at) VALUES($1,$2,$3,$4,$5,'{}',$6)`, operationID, root.assemblyID, root.manifestChecksum, root.lockID, root.lockChecksum, createdAt); err != nil {
		t.Fatal(err)
	}
}

func lifecycleInvariantSource(root lifecycleInvariantRoot) core.LifecycleArtifactState {
	return core.LifecycleArtifactState{ManifestID: root.assemblyID, ManifestChecksum: root.manifestChecksum, LockID: root.lockID, LockChecksum: root.lockChecksum, CatalogChecksum: digestA, TargetSnapshotChecksum: digestA}
}

func lifecycleInvariantCompletedOperation(root lifecycleInvariantRoot, planID, operationID string, target core.LifecycleArtifactState, createdAt, completedAt time.Time) core.LifecycleOperation {
	return core.LifecycleOperation{
		OperationID: operationID, RootOperationID: operationID, LifecyclePlanID: planID,
		AssemblyID: root.assemblyID, ProductID: root.productID, Kind: core.LifecycleUpgrade,
		Version: 2, Status: core.LifecycleCompleted, SchemaVersion: "1.0.0", Document: json.RawMessage(`{}`),
		Source: lifecycleInvariantSource(root), Target: &target, Recovery: core.LifecycleRecovery{RollbackAvailable: true},
		OperationChecksum: digestB, IdempotencyKeyDigest: digestC, CreatedBy: "admin.test",
		CreatedAt: createdAt, UpdatedAt: completedAt, CompletedAt: &completedAt,
	}
}

func completeLifecycleInvariantGeneration(t *testing.T, pool *pgxpool.Pool, repository *assemblypostgres.Repository, root lifecycleInvariantRoot, source core.LifecycleArtifactState, suffix, planChecksum, manifestChecksum, lockChecksum string, createdAt time.Time) core.LifecycleArtifactState {
	t.Helper()
	ctx := context.Background()
	planID := "lifecycle." + root.assemblyID + "." + suffix
	operationID := "operation." + root.assemblyID[len("assembly."):] + "." + suffix
	manifestID := "assembly." + root.assemblyID[len("assembly."):] + "." + suffix
	lockID := "lock." + root.assemblyID[len("assembly."):] + "." + suffix
	completedAt := createdAt.Add(time.Minute)

	_, err := pool.Exec(ctx, `INSERT INTO assembly.lifecycle_plans(
		lifecycle_plan_id,assembly_id,product_id,operation,version,schema_version,document,
		source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,
		source_catalog_checksum,source_target_snapshot_checksum,target_snapshot_checksum,
		blocking_conflict_count,executable,confirmation_checksum,plan_checksum,created_by,created_at
	) VALUES($1,$2,$3,'upgrade',1,'1.0.0','{}',$4,$5,$6,$7,$8,$9,$9,0,TRUE,$10,$11,'admin.test',$12)`,
		planID, root.assemblyID, root.productID, source.ManifestID, source.ManifestChecksum, source.LockID, source.LockChecksum,
		source.CatalogChecksum, source.TargetSnapshotChecksum, digestA, planChecksum, createdAt.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO assembly.lifecycle_operations(
		operation_id,root_operation_id,lifecycle_plan_id,assembly_id,product_id,kind,version,status,schema_version,document,
		source_state,recovery,diagnostic_ids,report_ids,operation_checksum,idempotency_key_digest,created_by,created_at,updated_at
	) VALUES($1,$1,$2,$3,$4,'upgrade',1,'executing','1.0.0','{}',$5,'{}','[]','[]',$6,$7,'admin.test',$8,$8)`,
		operationID, planID, root.assemblyID, root.productID, mustLifecycleInvariantJSON(t, source), digestA, planChecksum, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO assembly.lifecycle_artifact_transitions(operation_id,source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,rollback_journal,created_at) VALUES($1,$2,$3,$4,$5,'{}',$6)`, operationID, source.ManifestID, source.ManifestChecksum, source.LockID, source.LockChecksum, createdAt); err != nil {
		t.Fatal(err)
	}

	manifestRaw, lockRaw, target := lifecycleSuccessorDocuments(t, operationID, manifestID, lockID, root.productID, manifestChecksum, lockChecksum, completedAt)
	operation := lifecycleInvariantCompletedOperation(root, planID, operationID, target, createdAt, completedAt)
	operation.Source = source
	operation.OperationChecksum = planChecksum
	transition := core.LifecycleArtifactTransition{OperationID: operationID, Source: source, Target: &target, TargetManifestDocument: manifestRaw, TargetLockDocument: lockRaw, RollbackJournal: json.RawMessage(`{"state":"committed"}`), CreatedAt: createdAt, CompletedAt: &completedAt}
	if _, err = repository.UpdateLifecycleOperation(ctx, core.UpdateLifecycleOperationRecord{Operation: operation, ExpectedVersion: 1, Transition: &transition}); err != nil {
		t.Fatal(err)
	}
	return target
}

func mustLifecycleInvariantJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
