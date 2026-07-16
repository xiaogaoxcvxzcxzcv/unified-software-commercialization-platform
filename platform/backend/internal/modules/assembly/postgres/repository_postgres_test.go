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
	repository := assemblypostgres.NewWithCursorKey(database.Pool, []byte("assembly-repository-postgres-test-cursor-key"))
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

	run := core.Run{RunID: "run.repository-test", RootRunID: "run.repository-test", AttemptNumber: 1, PlanID: plan.PlanID, PlanVersion: confirmed.Version, Version: 1, PlanSHA256: plan.PlanSHA256, SchemaVersion: "1.0.0", Document: json.RawMessage(`{"schema_version":"1.0.0"}`), DocumentSHA256: digestA, IdempotencyKeyDigest: digestB, OutputTargetRef: "workspace.default", Status: core.RunStatusPlanned, Steps: []core.RunStep{{StepID: "step.provision", Kind: "provision", Status: "pending", CompensationStatus: "pending", DiagnosticIDs: []string{}}}, Recovery: core.RunRecovery{Retryable: true, ResumeFromStepID: "step.provision"}, CreatedBy: "admin-1", CreatedAt: now, UpdatedAt: now, AuditID: "aud-run"}
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
	generating := updated
	generating.Status = core.RunStatusGenerating
	generating.Document = json.RawMessage(`{"schema_version":"1.0.0","status":"generating"}`)
	generating.DocumentSHA256 = digestC
	generating.UpdatedAt = now.Add(5 * time.Minute)
	generating.Steps[0].Status = "completed"
	generating.Steps[0].FinishedAt = &generating.UpdatedAt
	generating, err = repository.UpdateRun(ctx, core.UpdateRunRecord{Run: generating, ExpectedVersion: 3, Idempotency: idem("assembly.update_run", "admin-1", run.RunID, digestB, digestC, generating.UpdatedAt), Event: event("evt-generating", "aud-generating", "assembly.progressed.v1", run.RunID, "product-a")})
	if err != nil {
		t.Fatal(err)
	}
	validating := generating
	validating.Status = core.RunStatusValidating
	validating.Document = json.RawMessage(`{"schema_version":"1.0.0","status":"validating"}`)
	validating.DocumentSHA256 = digestA
	validating.UpdatedAt = now.Add(5*time.Minute + 30*time.Second)
	validating, err = repository.UpdateRun(ctx, core.UpdateRunRecord{Run: validating, ExpectedVersion: 4, Idempotency: idem("assembly.update_run", "admin-1", run.RunID, digestC, digestA, validating.UpdatedAt), Event: event("evt-validating", "aud-validating", "assembly.progressed.v1", run.RunID, "product-a")})
	if err != nil {
		t.Fatal(err)
	}

	completedAt := now.Add(6 * time.Minute)
	completed := validating
	completed.Status = core.RunStatusCompleted
	completed.Document = json.RawMessage(`{"schema_version":"1.0.0","status":"completed"}`)
	completed.DocumentSHA256 = digestC
	completed.UpdatedAt = completedAt
	completed.CompletedAt = &completedAt
	completed.Steps[0].Status = "completed"
	manifest := core.Manifest{AssemblyID: "assembly.repository-test", ProductID: "product-a", RunID: run.RunID, SchemaVersion: "1.0.0", Document: json.RawMessage(`{"schema_version":"1.0.0","assembly_id":"assembly.repository-test"}`), DocumentSHA256: digestA, ManifestSHA256: digestB, CreatedAt: completedAt}
	lock := core.GeneratedProjectLock{LockID: "lock.repository-test", ProductID: "product-a", RunID: run.RunID, AssemblyID: manifest.AssemblyID, SchemaVersion: "1.0.0", Document: json.RawMessage(`{"schema_version":"1.0.0","lock_id":"lock.repository-test"}`), DocumentSHA256: digestB, LockSHA256: digestC, CreatedAt: completedAt}
	completed, err = repository.CompleteRun(ctx, core.CompleteRunRecord{Run: completed, ExpectedVersion: 5, Manifest: manifest, Lock: lock, Idempotency: idem("assembly.complete", "admin-1", run.RunID, digestC, digestB, completedAt), Event: event("evt-completed", "aud-completed", "assembly.completed.v1", run.RunID, "product-a")})
	if err != nil || completed.Version != 6 || completed.ManifestID != manifest.AssemblyID || completed.LockID != lock.LockID {
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
	completedDetail, err := repository.GetRun(ctx, "product-a", run.RunID)
	if err != nil || len(completedDetail.Reports) != 1 || completedDetail.Reports[0].Checksum != manifest.ManifestSHA256 {
		t.Fatalf("completed reports=%+v err=%v", completedDetail.Reports, err)
	}

	failedRun := run
	failedRun.RunID = "run.repository-failed"
	failedRun.RootRunID = failedRun.RunID
	failedRun.ProductID = "product-a"
	failedRun.CreatedAt = now.Add(7 * time.Minute)
	failedRun.UpdatedAt = failedRun.CreatedAt
	failedRun.Document = json.RawMessage(`{"schema_version":"1.0.0","run_id":"run.repository-failed"}`)
	failedRun.DocumentSHA256 = digestB
	failedRun.IdempotencyKeyDigest = digestA
	failedRun.Document = json.RawMessage(`{"schema_version":"1.0.0","run_id":"run.repository-failed","idempotency_key_digest":"` + digestA + `"}`)
	failedRun.Steps = []core.RunStep{{StepID: "step.provision", Kind: "provision", Status: "pending", CompensationStatus: "pending"}}
	if _, err = repository.StartRun(ctx, core.StartRunRecord{Run: failedRun, Idempotency: idem("assembly.start", "admin-1", plan.PlanID, digestA, digestB, failedRun.CreatedAt), Event: event("evt-failed-start", "aud-failed-start", "assembly.started.v1", failedRun.RunID, "product-a")}); err != nil {
		t.Fatal(err)
	}
	failedAt := now.Add(8 * time.Minute)
	failedRun.Status = core.RunStatusFailed
	failedRun.Version = 2
	failedRun.UpdatedAt = failedAt
	failedRun.CompletedAt = &failedAt
	failedRun.DiagnosticIDs = []string{"diagnostic.transient"}
	failedRun.Recovery = core.RunRecovery{Retryable: true, ResumeFromStepID: "step.provision"}
	failedRun.Steps[0].Status = "failed"
	failedRun.Steps[0].Attempt = 1
	failedRun.Steps[0].DiagnosticIDs = []string{"diagnostic.transient"}
	failedRun, err = repository.UpdateRun(ctx, core.UpdateRunRecord{Run: failedRun, ExpectedVersion: 1, Diagnostics: []core.RunDiagnostic{{DiagnosticID: "diagnostic.transient", Code: "assembly.transient", Severity: "error", Category: "assembly", Message: "Transient prerequisite failed", Blocking: true, Retryable: true, Remediation: []string{"Retry later"}, RelatedPaths: []string{}, CreatedAt: failedAt}}, Reports: []core.RunReport{{ReportID: "report.generator-result", ReportType: "generator_result", Status: "failed", Summary: "Generator failed", Checksum: digestC, CreatedAt: failedAt}}, Idempotency: idem("assembly.update_run", "admin-1", failedRun.RunID, digestB, digestC, failedAt), Event: event("evt-failed", "aud-failed", "assembly.failed.v1", failedRun.RunID, "product-a")})
	if err != nil {
		t.Fatal(err)
	}
	failedDetail, err := repository.GetRun(ctx, "product-a", failedRun.RunID)
	if err != nil || len(failedDetail.Diagnostics) != 1 || !failedDetail.Diagnostics[0].Retryable {
		t.Fatalf("diagnostics=%+v err=%v", failedDetail.Diagnostics, err)
	}
	retryAt := now.Add(9 * time.Minute)
	retry := failedRun
	retry.RunID = "run.repository-retry"
	retry.RootRunID = failedRun.RootRunID
	retry.RetryOfRunID = failedRun.RunID
	retry.AttemptNumber = 2
	retry.Version = 1
	retry.Status = core.RunStatusPlanned
	retry.CreatedBy = "admin-1"
	retry.CreatedAt = retryAt
	retry.UpdatedAt = retryAt
	retry.CompletedAt = nil
	retry.DiagnosticIDs = nil
	retry.Diagnostics = nil
	retry.Reports = nil
	retry.IdempotencyKeyDigest = digestC
	retry.Document = json.RawMessage(`{"schema_version":"1.0.0","run_id":"run.repository-retry","root_run_id":"run.repository-failed","retry_of_run_id":"run.repository-failed","attempt_number":2,"idempotency_key_digest":"` + digestC + `"}`)
	retry.Steps = []core.RunStep{{StepID: "step.provision", Kind: "provision", Status: "pending", CompensationStatus: "pending"}}
	retried, err := repository.RetryRun(ctx, core.RetryRunRecord{ParentRun: failedRun, Run: retry, ExpectedVersion: failedRun.Version, Idempotency: idem("assembly.retry_run", "admin-2", failedRun.RunID, digestC, digestA, retryAt), Event: event("evt-retry", "aud-retry", "assembly.retried.v1", retry.RunID, "product-a")})
	if err != nil || retried.CreatedBy != "admin-1" || retried.AttemptNumber != 2 {
		t.Fatalf("retried=%+v err=%v", retried, err)
	}
	page, err := repository.ListRuns(ctx, core.RunListFilter{PageSize: 1, Status: core.RunStatusFailed, ProductID: "product-a"})
	if err != nil || len(page.Items) != 1 || page.Items[0].DiagnosticCount != 1 || page.Items[0].ReportCount != 1 {
		t.Fatalf("failed page=%+v err=%v", page, err)
	}
	if _, err := repository.ListRuns(ctx, core.RunListFilter{PageSize: 1, Cursor: page.NextCursor + "tampered", Status: core.RunStatusFailed, ProductID: "product-a"}); !errors.Is(err, core.ErrInvalidCommand) {
		t.Fatalf("tampered cursor err=%v", err)
	}
	allPage, err := repository.ListRuns(ctx, core.RunListFilter{PageSize: 1, ProductID: "product-a"})
	if err != nil || allPage.NextCursor == "" {
		t.Fatalf("all page=%+v err=%v", allPage, err)
	}
	nextPage, err := repository.ListRuns(ctx, core.RunListFilter{PageSize: 1, Cursor: allPage.NextCursor, ProductID: "product-a"})
	if err != nil || len(nextPage.Items) != 1 || nextPage.Items[0].RunID == allPage.Items[0].RunID {
		t.Fatalf("next page=%+v err=%v", nextPage, err)
	}

	// A terminal dispatch remains recoverable until the durable worker marks it complete.
	for {
		claimed, claimErr := repository.ClaimDispatch(ctx, "worker.repository", now.Add(20*time.Minute), time.Minute)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if claimed.RunID == failedRun.RunID {
			if err := repository.RequeueDispatch(ctx, claimed.RunID, "worker.repository", "assembly.dispatch_retry", now.Add(20*time.Minute), now.Add(21*time.Minute), false); err != nil {
				t.Fatal(err)
			}
			break
		}
		if err := repository.CompleteDispatch(ctx, claimed.RunID, "worker.repository", now.Add(20*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	other, err := repository.ClaimDispatch(ctx, "worker.repository", now.Add(20*time.Minute), time.Minute)
	if err != nil || other.RunID != retry.RunID {
		t.Fatalf("other dispatch=%+v err=%v", other, err)
	}
	if err := repository.CompleteDispatch(ctx, other.RunID, "worker.repository", now.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := repository.ClaimDispatch(ctx, "worker.repository", now.Add(22*time.Minute), time.Minute)
	if err != nil || reclaimed.RunID != failedRun.RunID || reclaimed.AttemptCount != 2 {
		t.Fatalf("reclaimed=%+v err=%v", reclaimed, err)
	}
	if err := repository.CompleteDispatch(ctx, reclaimed.RunID, "worker.repository", now.Add(22*time.Minute)); err != nil {
		t.Fatal(err)
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
