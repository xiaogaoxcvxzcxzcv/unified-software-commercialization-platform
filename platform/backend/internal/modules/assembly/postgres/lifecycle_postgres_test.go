package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
	assemblypostgres "platform.local/capability-platform/backend/internal/modules/assembly/postgres"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestCancelRunUpdatesPersistedStepsWithoutSyntheticRunColumn(t *testing.T) {
	database := testpostgres.Open(t)
	repository := assemblypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 14, 0, 0, 0, time.UTC)
	blueprint := core.Blueprint{BlueprintID: "bp.cancel-test", Revision: 1, DocumentVersion: "1.0.0", SchemaVersion: "1.0.0", Document: json.RawMessage(`{}`), ContentSHA256: digestA, CreatedBy: "admin.test", CreatedAt: now}
	if _, err := repository.CreateBlueprint(ctx, core.CreateBlueprintRecord{Blueprint: blueprint, Idempotency: idem("assembly.create_blueprint", "admin.test", "platform", digestA, digestA, now), Event: event("evt-cancel-blueprint", "aud-cancel-blueprint", "assembly.blueprint_created.v1", blueprint.BlueprintID, "")}); err != nil {
		t.Fatal(err)
	}
	plan := core.Plan{PlanID: "plan.cancel-test", BlueprintID: blueprint.BlueprintID, BlueprintRevision: 1, Version: 1, Environment: "test", SchemaVersion: "1.0.0", Document: json.RawMessage(`{}`), BlueprintSHA256: digestA, CatalogRevision: "catalog.test", CatalogSnapshotSHA256: digestB, PlanSHA256: digestC, Executable: true, CreatedBy: "admin.test", CreatedAt: now, UpdatedAt: now}
	if _, err := repository.CreatePlan(ctx, core.CreatePlanRecord{Plan: plan, Idempotency: idem("assembly.create_plan", "admin.test", blueprint.BlueprintID, digestB, digestB, now), Event: event("evt-cancel-plan", "aud-cancel-plan", "assembly.planned.v1", plan.PlanID, "")}); err != nil {
		t.Fatal(err)
	}
	run := core.Run{RunID: "run.cancel-test", RootRunID: "run.cancel-test", AttemptNumber: 1, PlanID: plan.PlanID, PlanVersion: 1, Version: 1, PlanSHA256: digestC, SchemaVersion: "1.0.0", Document: json.RawMessage(`{}`), DocumentSHA256: digestA, IdempotencyKeyDigest: digestC, OutputTargetRef: "workspace.default", Status: core.RunStatusPlanned, Steps: []core.RunStep{{StepID: "step.provision", Kind: "provision", Status: "pending", CompensationStatus: "pending", DiagnosticIDs: []string{}}}, Recovery: core.RunRecovery{Retryable: true, ResumeFromStepID: "step.provision"}, CreatedBy: "admin.test", CreatedAt: now, UpdatedAt: now}
	if _, err := repository.StartRun(ctx, core.StartRunRecord{Run: run, Idempotency: idem("assembly.start", "admin.test", plan.PlanID, digestC, digestC, now), Event: event("evt-cancel-run", "aud-cancel-run", "assembly.started.v1", run.RunID, "")}); err != nil {
		t.Fatal(err)
	}
	cancelledAt := now.Add(time.Minute)
	run.Version = 2
	run.Status = core.RunStatusCancelled
	run.Document = json.RawMessage(`{"status":"cancelled"}`)
	run.DocumentSHA256 = digestB
	run.Recovery = core.RunRecovery{}
	run.UpdatedAt = cancelledAt
	run.CompletedAt = &cancelledAt
	run.Steps[0].Status = "skipped"
	run.Steps[0].FinishedAt = &cancelledAt
	result, err := repository.CancelRun(ctx, core.CancelRunRecord{Run: run, ExpectedVersion: 1, Idempotency: idem("assembly.cancel_run", "admin.test", run.RunID, digestA, digestA, cancelledAt), Event: event("evt-cancelled", "aud-cancelled", "assembly.cancelled.v1", run.RunID, "")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != core.RunStatusCancelled {
		t.Fatalf("status=%s", result.Status)
	}
	var runStatus, stepStatus string
	var finishedAt *time.Time
	if err = database.Pool.QueryRow(ctx, `SELECT r.status,s.status,s.finished_at FROM assembly.assembly_runs r JOIN assembly.assembly_run_steps s ON s.run_id=r.run_id WHERE r.run_id=$1 AND s.step_id='step.provision'`, run.RunID).Scan(&runStatus, &stepStatus, &finishedAt); err != nil {
		t.Fatal(err)
	}
	if runStatus != "cancelled" || stepStatus != "skipped" || finishedAt == nil || !finishedAt.Equal(cancelledAt) {
		t.Fatalf("run=%s step=%s finished=%v", runStatus, stepStatus, finishedAt)
	}
}

func TestLifecycleTransitionMismatchLeavesOperationUnchanged(t *testing.T) {
	database := testpostgres.Open(t)
	repository := assemblypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 15, 0, 0, 0, time.UTC)
	// Minimal trusted predecessor graph for lifecycle foreign keys.
	sourceState := json.RawMessage(`{"manifest_id":"assembly.transition-test","manifest_checksum":"` + digestA + `","lock_id":"lock.transition-test","lock_checksum":"` + digestA + `","catalog_checksum":"` + digestA + `","target_snapshot_checksum":"` + digestA + `"}`)
	setup := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO assembly.product_blueprints(blueprint_id,revision,document_version,schema_version,document,content_sha256,created_by,created_at) VALUES('bp.transition-test',1,'1.0.0','1.0.0','{}',$1,'admin.test',$2)`, []any{digestA, now}},
		{`INSERT INTO assembly.assembly_plans(plan_id,blueprint_id,blueprint_revision,version,environment,schema_version,document,blueprint_sha256,catalog_revision,catalog_snapshot_sha256,plan_sha256,executable,created_by,created_at,updated_at) VALUES('plan.transition-test','bp.transition-test',1,1,'test','1.0.0','{}',$1,'catalog.test',$1,$1,TRUE,'admin.test',$2,$2)`, []any{digestA, now}},
		{`INSERT INTO assembly.assembly_runs(run_id,root_run_id,attempt_number,plan_id,plan_version,version,plan_sha256,schema_version,document,document_sha256,idempotency_key_digest,output_target_ref,status,current_step_id,diagnostic_ids,recovery,created_by,created_at,updated_at) VALUES('run.transition-test','run.transition-test',1,'plan.transition-test',1,1,$1,'1.0.0','{}',$1,$1,'workspace.default','planned',NULL,'[]','{}','admin.test',$2,$2)`, []any{digestA, now}},
		{`INSERT INTO assembly.assembly_manifests(assembly_id,product_id,run_id,schema_version,document,document_sha256,manifest_sha256,created_at) VALUES('assembly.transition-test','product.transition-test','run.transition-test','1.0.0','{}',$1,$1,$2)`, []any{digestA, now}},
		{`INSERT INTO assembly.generated_project_locks(lock_id,product_id,run_id,assembly_id,schema_version,document,document_sha256,lock_sha256,created_at) VALUES('lock.transition-test','product.transition-test','run.transition-test','assembly.transition-test','1.0.0',jsonb_build_object('catalog_checksum',$1::text,'target_snapshot_checksum',$1::text)::text,$1,$1,$2)`, []any{digestA, now}},
		{`INSERT INTO assembly.lifecycle_heads(root_assembly_id,product_id,current_manifest_id,current_lock_id,version,updated_at) VALUES('assembly.transition-test','product.transition-test','assembly.transition-test','lock.transition-test',1,$1)`, []any{now}},
		{`INSERT INTO assembly.lifecycle_plans(lifecycle_plan_id,assembly_id,product_id,operation,version,schema_version,document,source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,source_catalog_checksum,source_target_snapshot_checksum,target_snapshot_checksum,blocking_conflict_count,executable,confirmation_checksum,plan_checksum,created_by,created_at) VALUES('lifecycle.transition-test','assembly.transition-test','product.transition-test','upgrade',1,'1.0.0','{}','assembly.transition-test',$1,'lock.transition-test',$1,$1,$1,$1,0,TRUE,$1,$1,'admin.test',$2)`, []any{digestA, now}},
		{`INSERT INTO assembly.lifecycle_operations(operation_id,root_operation_id,lifecycle_plan_id,assembly_id,product_id,kind,version,status,current_step,schema_version,document,source_state,target_state,recovery,diagnostic_ids,report_ids,operation_checksum,idempotency_key_digest,created_by,created_at,updated_at) VALUES('operation.transition-test','operation.transition-test','lifecycle.transition-test','assembly.transition-test','product.transition-test','upgrade',1,'executing','step.execute','1.0.0','{}',$1,NULL,'{}','[]','[]',$2,$2,'admin.test',$3,$3)`, []any{sourceState, digestA, now}},
		{`INSERT INTO assembly.lifecycle_artifact_transitions(operation_id,source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,rollback_journal,created_at) VALUES('operation.transition-test','assembly.transition-test',$1,'lock.transition-test',$1,'{}',$2)`, []any{digestA, now}},
	}
	for _, item := range setup {
		if _, err := database.Pool.Exec(ctx, item.query, item.args...); err != nil {
			t.Fatal(err)
		}
	}
	completedAt := now.Add(time.Minute)
	manifestRaw, lockRaw, target := lifecycleSuccessorDocuments(t, "operation.transition-test", "assembly.target-next", "lock.target-next", "product.transition-test", digestB, digestB, completedAt)
	operation := core.LifecycleOperation{OperationID: "operation.transition-test", RootOperationID: "operation.transition-test", LifecyclePlanID: "lifecycle.transition-test", AssemblyID: "assembly.transition-test", ProductID: "product.transition-test", Kind: core.LifecycleUpgrade, Version: 2, Status: core.LifecycleCompleted, SchemaVersion: "1.0.0", Document: json.RawMessage(`{}`), Source: core.LifecycleArtifactState{ManifestID: "assembly.transition-test", ManifestChecksum: digestA, LockID: "lock.transition-test", LockChecksum: digestA, CatalogChecksum: digestA, TargetSnapshotChecksum: digestA}, Target: &target, Recovery: core.LifecycleRecovery{RollbackAvailable: true}, OperationChecksum: digestB, IdempotencyKeyDigest: digestA, CreatedBy: "admin.test", CreatedAt: now, UpdatedAt: completedAt, CompletedAt: &completedAt}
	transition := core.LifecycleArtifactTransition{OperationID: operation.OperationID, Source: operation.Source, Target: &target, TargetManifestDocument: manifestRaw, TargetLockDocument: lockRaw, RollbackJournal: json.RawMessage(`{}`), CreatedAt: now, CompletedAt: &completedAt}
	transition.OperationID = "operation.other"
	_, err := repository.UpdateLifecycleOperation(ctx, core.UpdateLifecycleOperationRecord{Operation: operation, ExpectedVersion: 1, Transition: &transition})
	if !errors.Is(err, core.ErrDocumentInvalid) {
		t.Fatalf("error=%v", err)
	}
	var version int64
	var status string
	if err = database.Pool.QueryRow(ctx, `SELECT version,status FROM assembly.lifecycle_operations WHERE operation_id='operation.transition-test'`).Scan(&version, &status); err != nil {
		t.Fatal(err)
	}
	if version != 1 || status != "executing" {
		t.Fatalf("transaction partially committed: version=%d status=%s", version, status)
	}
}

func TestLifecycleCompletionPublishesSuccessorAndMovesHead(t *testing.T) {
	database := testpostgres.Open(t)
	repository := assemblypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 16, 0, 0, 0, time.UTC)
	rootAssemblyID := "assembly.head-test"
	productID := "product.head-test"
	operationID := "operation.head-test"
	source := core.LifecycleArtifactState{ManifestID: rootAssemblyID, ManifestChecksum: digestA, LockID: "lock.head-test", LockChecksum: digestA, CatalogChecksum: digestA, TargetSnapshotChecksum: digestA}
	sourceRaw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	setup := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO assembly.product_blueprints(blueprint_id,revision,document_version,schema_version,document,content_sha256,created_by,created_at) VALUES('bp.head-test',1,'1.0.0','1.0.0','{}',$1,'admin.test',$2)`, []any{digestA, now}},
		{`INSERT INTO assembly.assembly_plans(plan_id,blueprint_id,blueprint_revision,version,environment,schema_version,document,blueprint_sha256,catalog_revision,catalog_snapshot_sha256,plan_sha256,executable,created_by,created_at,updated_at) VALUES('plan.head-test','bp.head-test',1,1,'test','1.0.0','{}',$1,'catalog.test',$1,$1,TRUE,'admin.test',$2,$2)`, []any{digestA, now}},
		{`INSERT INTO assembly.assembly_runs(run_id,root_run_id,attempt_number,plan_id,plan_version,version,plan_sha256,schema_version,document,document_sha256,idempotency_key_digest,output_target_ref,status,current_step_id,diagnostic_ids,recovery,created_by,created_at,updated_at) VALUES('run.head-test','run.head-test',1,'plan.head-test',1,1,$1,'1.0.0','{}',$1,$1,'workspace.default','planned',NULL,'[]','{}','admin.test',$2,$2)`, []any{digestA, now}},
		{`INSERT INTO assembly.assembly_manifests(assembly_id,product_id,run_id,schema_version,document,document_sha256,manifest_sha256,created_at) VALUES($1,$2,'run.head-test','1.0.0','{}',$3,$3,$4)`, []any{rootAssemblyID, productID, digestA, now}},
		{`INSERT INTO assembly.generated_project_locks(lock_id,product_id,run_id,assembly_id,schema_version,document,document_sha256,lock_sha256,created_at) VALUES('lock.head-test',$1,'run.head-test',$2,'1.0.0',jsonb_build_object('catalog_checksum',$3::text,'target_snapshot_checksum',$3::text)::text,$3,$3,$4)`, []any{productID, rootAssemblyID, digestA, now}},
		{`INSERT INTO assembly.lifecycle_heads(root_assembly_id,product_id,current_manifest_id,current_lock_id,version,updated_at) VALUES($1,$2,$1,'lock.head-test',1,$3)`, []any{rootAssemblyID, productID, now}},
		{`INSERT INTO assembly.lifecycle_plans(lifecycle_plan_id,assembly_id,product_id,operation,version,schema_version,document,source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,source_catalog_checksum,source_target_snapshot_checksum,target_snapshot_checksum,blocking_conflict_count,executable,confirmation_checksum,plan_checksum,created_by,created_at) VALUES('lifecycle.head-test',$1,$2,'upgrade',1,'1.0.0','{}',$1,$3,'lock.head-test',$3,$3,$3,$3,0,TRUE,$3,$3,'admin.test',$4)`, []any{rootAssemblyID, productID, digestA, now}},
		{`INSERT INTO assembly.lifecycle_operations(operation_id,root_operation_id,lifecycle_plan_id,assembly_id,product_id,kind,version,status,current_step,schema_version,document,source_state,target_state,recovery,diagnostic_ids,report_ids,operation_checksum,idempotency_key_digest,created_by,created_at,updated_at) VALUES($1,$1,'lifecycle.head-test',$2,$3,'upgrade',1,'planned',NULL,'1.0.0','{}',$4,NULL,'{}','[]','[]',$5,$5,'admin.test',$6,$6)`, []any{operationID, rootAssemblyID, productID, sourceRaw, digestA, now}},
		{`INSERT INTO assembly.lifecycle_artifact_transitions(operation_id,source_manifest_id,source_manifest_checksum,source_lock_id,source_lock_checksum,rollback_journal,created_at) VALUES($1,$2,$3,'lock.head-test',$3,'{}',$4)`, []any{operationID, rootAssemblyID, digestA, now}},
	}
	for _, item := range setup {
		if _, err = database.Pool.Exec(ctx, item.query, item.args...); err != nil {
			t.Fatal(err)
		}
	}
	executingAt := now.Add(time.Second)
	executing := core.LifecycleOperation{OperationID: operationID, RootOperationID: operationID, LifecyclePlanID: "lifecycle.head-test", AssemblyID: rootAssemblyID, ProductID: productID, Kind: core.LifecycleUpgrade, Version: 2, Status: core.LifecycleExecuting, CurrentStep: "step.prepare", SchemaVersion: "1.0.0", Document: json.RawMessage(`{}`), Source: source, Recovery: core.LifecycleRecovery{}, OperationChecksum: digestB, IdempotencyKeyDigest: digestA, CreatedBy: "admin.test", CreatedAt: now, UpdatedAt: executingAt}
	if _, err = repository.UpdateLifecycleOperation(ctx, core.UpdateLifecycleOperationRecord{Operation: executing, ExpectedVersion: 1}); err != nil {
		t.Fatalf("planned to executing update with no target: %v", err)
	}
	var targetState []byte
	if err = database.Pool.QueryRow(ctx, `SELECT target_state FROM assembly.lifecycle_operations WHERE operation_id=$1`, operationID).Scan(&targetState); err != nil {
		t.Fatal(err)
	}
	if targetState != nil {
		t.Fatal("executing operation persisted a JSON target instead of SQL NULL")
	}

	completedAt := now.Add(time.Minute)
	manifestRaw, lockRaw, target := lifecycleSuccessorDocuments(t, operationID, "assembly.head-next", "lock.head-next", productID, digestB, digestC, completedAt)
	operation := core.LifecycleOperation{OperationID: operationID, RootOperationID: operationID, LifecyclePlanID: "lifecycle.head-test", AssemblyID: rootAssemblyID, ProductID: productID, Kind: core.LifecycleUpgrade, Version: 3, Status: core.LifecycleCompleted, SchemaVersion: "1.0.0", Document: json.RawMessage(`{}`), Source: source, Target: &target, Recovery: core.LifecycleRecovery{RollbackAvailable: true}, OperationChecksum: digestB, IdempotencyKeyDigest: digestA, CreatedBy: "admin.test", CreatedAt: now, UpdatedAt: completedAt, CompletedAt: &completedAt}
	transition := core.LifecycleArtifactTransition{OperationID: operationID, Source: source, Target: &target, TargetManifestDocument: manifestRaw, TargetLockDocument: lockRaw, RollbackJournal: json.RawMessage(`{"state":"committed"}`), CreatedAt: now, CompletedAt: &completedAt}
	result, err := repository.UpdateLifecycleOperation(ctx, core.UpdateLifecycleOperationRecord{Operation: operation, ExpectedVersion: 2, Transition: &transition})
	if err != nil || result.Status != core.LifecycleCompleted {
		t.Fatalf("completion result=%+v err=%v", result, err)
	}
	manifest, lock, err := repository.GetLifecycleSource(ctx, rootAssemblyID)
	if err != nil || manifest.AssemblyID != target.ManifestID || lock.LockID != target.LockID || manifest.RunID != "" || lock.RunID != "" || manifest.LifecycleOperationID != operationID || lock.LifecycleOperationID != operationID {
		t.Fatalf("current source manifest=%+v lock=%+v err=%v", manifest, lock, err)
	}
	var manifestOperationID, lockOperationID string
	var headVersion int64
	if err = database.Pool.QueryRow(ctx, `SELECT manifest.lifecycle_operation_id,lock.lifecycle_operation_id,head.version FROM assembly.lifecycle_heads head JOIN assembly.assembly_manifests manifest ON manifest.assembly_id=head.current_manifest_id JOIN assembly.generated_project_locks lock ON lock.lock_id=head.current_lock_id WHERE head.root_assembly_id=$1`, rootAssemblyID).Scan(&manifestOperationID, &lockOperationID, &headVersion); err != nil {
		t.Fatal(err)
	}
	if manifestOperationID != operationID || lockOperationID != operationID || headVersion != 2 {
		t.Fatalf("successor provenance manifest=%s lock=%s headVersion=%d", manifestOperationID, lockOperationID, headVersion)
	}
	if _, err = database.Pool.Exec(ctx, `UPDATE assembly.lifecycle_heads SET current_manifest_id=$2,current_lock_id=$3,version=version+1,updated_by_operation_id=$4,updated_at=$5 WHERE root_assembly_id=$1`, rootAssemblyID, rootAssemblyID, source.LockID, operationID, completedAt.Add(time.Minute)); err == nil {
		t.Fatal("direct lifecycle head rewind unexpectedly succeeded")
	}
}

func lifecycleSuccessorDocuments(t *testing.T, operationID, manifestID, lockID, productID, catalogChecksum, snapshotChecksum string, createdAt time.Time) (json.RawMessage, json.RawMessage, core.LifecycleArtifactState) {
	t.Helper()
	manifest := map[string]any{
		"schema_version": "1.0.0", "assembly_id": manifestID, "lifecycle_operation_id": operationID,
		"product": map[string]any{"product_id": productID}, "created_at": createdAt.Format(time.RFC3339Nano), "manifest_checksum": digestA,
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestChecksum, err := machinecontract.DigestWithoutTopLevelField(manifestRaw, "manifest_checksum")
	if err != nil {
		t.Fatal(err)
	}
	manifest["manifest_checksum"] = manifestChecksum
	manifestRaw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	lock := map[string]any{
		"schema_version": "1.0.0", "lock_id": lockID, "lifecycle_operation_id": operationID,
		"assembly_manifest_checksum": manifestChecksum, "catalog_checksum": catalogChecksum, "target_snapshot_checksum": snapshotChecksum,
		"created_at": createdAt.Format(time.RFC3339Nano), "lock_checksum": digestA,
	}
	lockRaw, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	lockChecksum, err := machinecontract.DigestWithoutTopLevelField(lockRaw, "lock_checksum")
	if err != nil {
		t.Fatal(err)
	}
	lock["lock_checksum"] = lockChecksum
	lockRaw, err = json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	target := core.LifecycleArtifactState{ManifestID: manifestID, ManifestChecksum: manifestChecksum, LockID: lockID, LockChecksum: lockChecksum, CatalogChecksum: catalogChecksum, TargetSnapshotChecksum: snapshotChecksum}
	return manifestRaw, lockRaw, target
}
