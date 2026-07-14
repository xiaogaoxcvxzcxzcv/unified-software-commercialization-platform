package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	assemblypostgres "platform.local/capability-platform/backend/internal/modules/assembly/postgres"
	"platform.local/capability-platform/backend/internal/modules/product"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	digestC = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func TestRepositoryPreProductLifecycleBindingIsolationAndOptimisticConcurrency(t *testing.T) {
	database := testpostgres.Open(t)
	repository := assemblypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 18, 0, 0, 0, time.UTC)

	blueprint := core.Blueprint{BlueprintID: "bp_repository-test", Revision: 1, DocumentVersion: "1.0.0", SchemaVersion: "1.0.0", Document: json.RawMessage(`{"schema_version":"1.0.0"}`), ContentSHA256: digestA, CreatedBy: "admin-1", CreatedAt: now, AuditID: "aud-blueprint"}
	blueprintRecord := core.CreateBlueprintRecord{Blueprint: blueprint, Idempotency: idem("assembly.create_blueprint", "admin-1", "platform", digestA, digestA, now), Event: event("evt-blueprint", "aud-blueprint", "assembly.blueprint_created.v1", blueprint.BlueprintID, "")}
	created, err := repository.CreateBlueprint(ctx, blueprintRecord)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := repository.CreateBlueprint(ctx, blueprintRecord)
	if err != nil || replayed.BlueprintID != created.BlueprintID {
		t.Fatalf("blueprint replay = %+v, err = %v", replayed, err)
	}
	changed := blueprintRecord
	changed.Idempotency.RequestDigest = digestB
	if _, err := repository.CreateBlueprint(ctx, changed); !errors.Is(err, core.ErrIdempotencyConflict) {
		t.Fatalf("changed blueprint replay error = %v", err)
	}
	if created.ProductID != "" {
		t.Fatalf("pre-product blueprint product = %q", created.ProductID)
	}
	var storedBlueprint string
	if err := database.Pool.QueryRow(ctx, `SELECT document FROM assembly.product_blueprints WHERE blueprint_id=$1 AND revision=1`, blueprint.BlueprintID).Scan(&storedBlueprint); err != nil {
		t.Fatal(err)
	}
	if storedBlueprint != string(blueprint.Document) {
		t.Fatalf("stored canonical blueprint = %q, want exact %q", storedBlueprint, blueprint.Document)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE assembly.product_blueprints SET created_by='attacker' WHERE blueprint_id=$1 AND revision=1`, blueprint.BlueprintID); err == nil {
		t.Fatal("immutable blueprint metadata update unexpectedly succeeded")
	}

	plan := core.Plan{PlanID: "plan.repository-test", BlueprintID: blueprint.BlueprintID, BlueprintRevision: 1, Version: 1, Environment: "test", SchemaVersion: "1.0.0", Document: json.RawMessage(`{"schema_version":"1.0.0"}`), BlueprintSHA256: digestA, CatalogRevision: "catalog-revision-1", CatalogSnapshotSHA256: digestB, PlanSHA256: digestC, Executable: true, Capabilities: []product.CapabilityItem{{CapabilityID: "identity.login", Enabled: true, Policy: json.RawMessage(`{}`), SourcePackageID: "package.account", SourcePackageVersion: "1.0.0"}}, CreatedBy: "admin-1", CreatedAt: now, UpdatedAt: now, AuditID: "aud-plan"}
	plan, err = repository.CreatePlan(ctx, core.CreatePlanRecord{Plan: plan, Idempotency: idem("assembly.create_plan", "admin-1", blueprint.BlueprintID, digestB, digestA, now), Event: event("evt-plan", "aud-plan", "assembly.planned.v1", plan.PlanID, "")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE assembly.assembly_plans SET document='{"changed":true}' WHERE plan_id=$1`, plan.PlanID); err == nil {
		t.Fatal("immutable plan document update unexpectedly succeeded")
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE assembly.plan_capabilities SET policy='{"tampered":true}' WHERE plan_id=$1`, plan.PlanID); err == nil {
		t.Fatal("immutable plan capability projection update unexpectedly succeeded")
	}
	confirmed, err := repository.ConfirmPlan(ctx, core.ConfirmPlanRecord{PlanID: plan.PlanID, ExpectedVersion: 1, ConfirmedBy: "admin-1", ConfirmedAt: now.Add(time.Minute), Idempotency: idem("assembly.confirm_plan", "admin-1", plan.PlanID, digestC, digestB, now.Add(time.Minute)), Event: event("evt-confirm", "aud-confirm", "assembly.plan_confirmed.v1", plan.PlanID, "")})
	if err != nil || confirmed.Version != 2 || confirmed.ConfirmedAt == nil {
		t.Fatalf("confirmed plan = %+v, err = %v", confirmed, err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE assembly.assembly_plans SET confirmed_by='attacker' WHERE plan_id=$1`, plan.PlanID); err == nil {
		t.Fatal("confirmed plan metadata rewrite unexpectedly succeeded")
	}
	if _, err := repository.ConfirmPlan(ctx, core.ConfirmPlanRecord{PlanID: plan.PlanID, ExpectedVersion: 1, ConfirmedBy: "admin-1", ConfirmedAt: now.Add(2 * time.Minute), Idempotency: idem("assembly.confirm_plan", "admin-1", plan.PlanID, digestA, digestC, now.Add(2*time.Minute)), Event: event("evt-confirm-stale", "aud-confirm-stale", "assembly.plan_confirmed.v1", plan.PlanID, "")}); !errors.Is(err, core.ErrVersionConflict) {
		t.Fatalf("stale confirmation error = %v", err)
	}

	run := core.Run{RunID: "run.repository-test", PlanID: plan.PlanID, PlanVersion: confirmed.Version, Version: 1, PlanSHA256: plan.PlanSHA256, SchemaVersion: "1.0.0", Document: json.RawMessage(`{"schema_version":"1.0.0"}`), DocumentSHA256: digestA, OutputTargetRef: "workspace.default", Status: core.RunStatusPlanned, Steps: []core.RunStep{{StepID: "step.provision", Kind: "provision", Status: "pending", CompensationStatus: "pending"}}, Recovery: core.RunRecovery{Retryable: true, ResumeFromStepID: "step.provision"}, CreatedBy: "admin-1", CreatedAt: now, UpdatedAt: now, AuditID: "aud-run"}
	runRecord := core.StartRunRecord{Run: run, Idempotency: idem("assembly.start", "admin-1", plan.PlanID, digestB, digestC, now), Event: event("evt-run", "aud-run", "assembly.started.v1", run.RunID, "")}
	started, err := repository.StartRun(ctx, runRecord)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE assembly.assembly_runs SET output_target_ref='workspace.other' WHERE run_id=$1`, run.RunID); err == nil {
		t.Fatal("locked run output target update unexpectedly succeeded")
	}
	replayedRun, err := repository.StartRun(ctx, runRecord)
	if err != nil || replayedRun.RunID != started.RunID {
		t.Fatalf("run replay = %+v, err = %v", replayedRun, err)
	}

	bound, err := repository.BindProduct(ctx, core.BindProductRecord{ProductID: "product-a", RunID: run.RunID, ExpectedVersion: 1, BoundAt: now.Add(3 * time.Minute), Idempotency: idem("assembly.bind_product", "admin-1", run.RunID, digestC, digestA, now.Add(3*time.Minute)), Event: event("evt-bind", "aud-bind", "assembly.product_bound.v1", run.RunID, "product-a")})
	if err != nil || bound.ProductID != "product-a" || bound.Version != 2 {
		t.Fatalf("bound run = %+v, err = %v", bound, err)
	}
	if _, err := repository.GetRun(ctx, "product-b", run.RunID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross-product run error = %v", err)
	}
	boundPlan, err := repository.GetPlan(ctx, "product-a", plan.PlanID)
	if err != nil || boundPlan.ProductID != "product-a" || len(boundPlan.Capabilities) != 1 {
		t.Fatalf("bound plan = %+v, err = %v", boundPlan, err)
	}
	if _, err := repository.GetPlan(ctx, "product-b", plan.PlanID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross-product plan error = %v", err)
	}

	next := bound
	next.Status = core.RunStatusProvisioning
	next.CurrentStepID = "step.provision"
	next.DocumentSHA256 = digestB
	next.Document = json.RawMessage(`{"schema_version":"1.0.0","status":"provisioning"}`)
	next.UpdatedAt = now.Add(4 * time.Minute)
	next.Steps[0].Status = "running"
	next.Steps[0].Attempt = 1
	updated, err := repository.UpdateRun(ctx, core.UpdateRunRecord{Run: next, ExpectedVersion: 2, Idempotency: idem("assembly.update_run", "admin-1", run.RunID, digestA, digestB, now.Add(4*time.Minute)), Event: event("evt-progress", "aud-progress", "assembly.progressed.v1", run.RunID, "product-a")})
	if err != nil || updated.Version != 3 || updated.Status != core.RunStatusProvisioning {
		t.Fatalf("updated run = %+v, err = %v", updated, err)
	}
	stale := updated
	stale.UpdatedAt = now.Add(5 * time.Minute)
	if _, err := repository.UpdateRun(ctx, core.UpdateRunRecord{Run: stale, ExpectedVersion: 2, Idempotency: idem("assembly.update_run", "admin-2", run.RunID, digestB, digestC, now.Add(5*time.Minute)), Event: event("evt-stale", "aud-stale", "assembly.progressed.v1", run.RunID, "product-a")}); !errors.Is(err, core.ErrVersionConflict) {
		t.Fatalf("stale run update error = %v", err)
	}

	completedAt := now.Add(6 * time.Minute)
	completed := updated
	completed.Status = core.RunStatusCompleted
	completed.Document = json.RawMessage(`{"schema_version":"1.0.0","status":"completed"}`)
	completed.DocumentSHA256 = digestC
	completed.UpdatedAt = completedAt
	completed.CompletedAt = &completedAt
	completed.Steps[0].Status = "completed"
	completed.Steps[0].CompensationStatus = "not_required"
	manifest := core.Manifest{AssemblyID: "assembly.repository-test", ProductID: "product-a", RunID: run.RunID, SchemaVersion: "1.0.0", Document: json.RawMessage(`{"schema_version":"1.0.0","assembly_id":"assembly.repository-test"}`), DocumentSHA256: digestA, ManifestSHA256: digestB, CreatedAt: completedAt}
	lock := core.GeneratedProjectLock{LockID: "lock.repository-test", ProductID: "product-a", RunID: run.RunID, AssemblyID: manifest.AssemblyID, SchemaVersion: "1.0.0", Document: json.RawMessage(`{"schema_version":"1.0.0","lock_id":"lock.repository-test"}`), DocumentSHA256: digestB, LockSHA256: digestC, CreatedAt: completedAt}
	completed, err = repository.CompleteRun(ctx, core.CompleteRunRecord{Run: completed, ExpectedVersion: 3, Manifest: manifest, Lock: lock, Idempotency: idem("assembly.complete", "admin-1", run.RunID, digestC, digestB, completedAt), Event: event("evt-completed", "aud-completed", "assembly.completed.v1", run.RunID, "product-a")})
	if err != nil || completed.Version != 4 || completed.ManifestID != manifest.AssemblyID || completed.LockID != lock.LockID {
		t.Fatalf("completed run = %+v, err = %v", completed, err)
	}
	platformManifest, err := repository.GetManifest(ctx, "", manifest.AssemblyID)
	if err != nil || platformManifest.ProductID != "product-a" || string(platformManifest.Document) != string(manifest.Document) {
		t.Fatalf("platform manifest = %+v, err = %v", platformManifest, err)
	}
	platformLock, err := repository.GetLock(ctx, "", lock.LockID)
	if err != nil || platformLock.AssemblyID != manifest.AssemblyID || string(platformLock.Document) != string(lock.Document) {
		t.Fatalf("platform lock = %+v, err = %v", platformLock, err)
	}
	if _, err := repository.GetManifest(ctx, "product-b", manifest.AssemblyID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("cross-product manifest error = %v", err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE assembly.assembly_manifests SET document='{"tampered":true}' WHERE assembly_id=$1`, manifest.AssemblyID); err == nil {
		t.Fatal("immutable manifest update unexpectedly succeeded")
	}
	if _, err := database.Pool.Exec(ctx, `DELETE FROM assembly.generated_project_locks WHERE lock_id=$1`, lock.LockID); err == nil {
		t.Fatal("immutable generated project lock delete unexpectedly succeeded")
	}

	var rawKeyCount int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM assembly.idempotency_records WHERE key_digest='plain-idempotency-key'`).Scan(&rawKeyCount); err != nil {
		t.Fatal(err)
	}
	if rawKeyCount != 0 {
		t.Fatalf("raw idempotency key persisted %d times", rawKeyCount)
	}
}

func TestRepositoryOutboxClaimRetryAndPublish(t *testing.T) {
	database := testpostgres.Open(t)
	repository := assemblypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 19, 0, 0, 0, time.UTC)
	b := core.Blueprint{BlueprintID: "bp_outbox-test", Revision: 1, DocumentVersion: "1.0.0", SchemaVersion: "1.0.0", Document: json.RawMessage(`{}`), ContentSHA256: digestA, CreatedBy: "admin", CreatedAt: now}
	if _, err := repository.CreateBlueprint(ctx, core.CreateBlueprintRecord{Blueprint: b, Idempotency: idem("assembly.create_blueprint", "admin", "platform", digestA, digestA, now), Event: event("evt-outbox", "aud-outbox", "assembly.blueprint_created.v1", b.BlueprintID, "")}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.ClaimOutbox(ctx, now, 10)
	if err != nil || len(claimed) != 1 || claimed[0].AttemptCount != 1 {
		t.Fatalf("claim = %+v, err = %v", claimed, err)
	}
	if err := repository.MarkOutboxFailed(ctx, claimed[0].EventID, "temporary", now.Add(-time.Second), false); err != nil {
		t.Fatal(err)
	}
	retried, err := repository.ClaimOutbox(ctx, now, 10)
	if err != nil || len(retried) != 1 || retried[0].AttemptCount != 2 {
		t.Fatalf("retry = %+v, err = %v", retried, err)
	}
	if err := repository.MarkOutboxPublished(ctx, retried[0].EventID, now); err != nil {
		t.Fatal(err)
	}
	after, err := repository.ClaimOutbox(ctx, now.Add(time.Minute), 10)
	if err != nil || len(after) != 0 {
		t.Fatalf("after publish = %+v, err = %v", after, err)
	}
}

func idem(operation, actor, scope, keyDigest, requestDigest string, now time.Time) core.Idempotency {
	return core.Idempotency{Operation: operation, ActorID: actor, ScopeID: scope, KeyDigest: keyDigest, RequestDigest: requestDigest, Now: now}
}
func event(eventID, auditID, eventType, targetID, productID string) core.OutboxEvent {
	return core.OutboxEvent{EventID: eventID, AggregateID: targetID, EventType: eventType, OccurredAt: time.Date(2026, 7, 13, 18, 0, 0, 0, time.UTC), Payload: core.EventPayload{AuditID: auditID, ActorID: "admin", Permission: "assembly.execute", ScopeType: "platform", ProductID: productID, Action: eventType, TargetType: "assembly", TargetID: targetID, Result: "success", TraceID: "trace-test", RiskLevel: "normal"}}
}
