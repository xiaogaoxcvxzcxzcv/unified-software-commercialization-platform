package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
	"platform.local/capability-platform/backend/internal/modules/product"
)

const (
	testDigestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testDigestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type validatorStub struct{ err error }

func (v validatorStub) Validate(name string, document json.RawMessage) (ValidatedDocument, error) {
	if v.err != nil {
		return ValidatedDocument{}, v.err
	}
	return ValidatedDocument{SchemaName: name, SchemaVersion: "1.0.0", CanonicalJSON: append(json.RawMessage(nil), document...), SHA256: testDigestA}, nil
}

type plannerStub struct {
	document PlannedDocument
	err      error
}

func (p plannerStub) BuildPlan(context.Context, Blueprint, string) (PlannedDocument, error) {
	return p.document, p.err
}

type repositoryStub struct {
	blueprint                                                                                   Blueprint
	plan                                                                                        Plan
	run                                                                                         Run
	createBlueprintCalls, createPlanCalls, confirmCalls, startCalls, updateCalls, completeCalls int
	lastUpdate                                                                                  UpdateRunRecord
	lastComplete                                                                                CompleteRunRecord
}

func (r *repositoryStub) CreateBlueprint(_ context.Context, record CreateBlueprintRecord) (Blueprint, error) {
	r.createBlueprintCalls++
	r.blueprint = record.Blueprint
	return record.Blueprint, nil
}
func (r *repositoryStub) GetBlueprint(context.Context, string, string, int64) (Blueprint, error) {
	if r.blueprint.BlueprintID == "" {
		return Blueprint{}, ErrNotFound
	}
	return r.blueprint, nil
}
func (r *repositoryStub) CreatePlan(_ context.Context, record CreatePlanRecord) (Plan, error) {
	r.createPlanCalls++
	r.plan = record.Plan
	return record.Plan, nil
}
func (r *repositoryStub) GetPlan(context.Context, string, string) (Plan, error) {
	if r.plan.PlanID == "" {
		return Plan{}, ErrNotFound
	}
	return r.plan, nil
}
func (r *repositoryStub) ConfirmPlan(_ context.Context, record ConfirmPlanRecord) (Plan, error) {
	r.confirmCalls++
	p := r.plan
	p.Version++
	p.ConfirmedAt = &record.ConfirmedAt
	return p, nil
}
func (r *repositoryStub) StartRun(_ context.Context, record StartRunRecord) (Run, error) {
	r.startCalls++
	r.run = record.Run
	return record.Run, nil
}
func (r *repositoryStub) GetRun(context.Context, string, string) (Run, error) {
	if r.run.RunID == "" {
		return Run{}, ErrNotFound
	}
	return r.run, nil
}
func (r *repositoryStub) BindProduct(context.Context, BindProductRecord) (Run, error) {
	return Run{}, nil
}
func (r *repositoryStub) UpdateRun(_ context.Context, record UpdateRunRecord) (Run, error) {
	r.updateCalls++
	r.lastUpdate = record
	return record.Run, nil
}
func (r *repositoryStub) CompleteRun(_ context.Context, record CompleteRunRecord) (Run, error) {
	r.completeCalls++
	r.lastComplete = record
	return record.Run, nil
}
func (r *repositoryStub) GetManifest(context.Context, string, string) (Manifest, error) {
	return Manifest{}, nil
}
func (r *repositoryStub) GetLock(context.Context, string, string) (GeneratedProjectLock, error) {
	return GeneratedProjectLock{}, nil
}
func (r *repositoryStub) ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error) {
	return nil, nil
}
func (r *repositoryStub) MarkOutboxPublished(context.Context, string, time.Time) error { return nil }
func (r *repositoryStub) MarkOutboxFailed(context.Context, string, string, time.Time, bool) error {
	return nil
}

func TestCreateBlueprintValidatesBeforePersistenceAndUsesPlatformScope(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository, validatorStub{err: ErrDocumentInvalid}, nil, sequenceIDs(), fixedClock())
	_, err := service.CreateBlueprint(context.Background(), CreateBlueprintCommand{Document: json.RawMessage(`{}`), ActorID: "admin", IdempotencyKey: "blueprint-key-0001", TraceID: "trace"})
	if !errors.Is(err, ErrDocumentInvalid) || repository.createBlueprintCalls != 0 {
		t.Fatalf("invalid blueprint err=%v calls=%d", err, repository.createBlueprintCalls)
	}
	service.validator = validatorStub{}
	created, err := service.CreateBlueprint(context.Background(), CreateBlueprintCommand{Document: json.RawMessage(`{"blueprint_id":"bp_service-test","version":"1.0.0"}`), ActorID: "admin", IdempotencyKey: "blueprint-key-0002", TraceID: "trace"})
	if err != nil || created.ProductID != "" || created.BlueprintID != "bp_service-test" {
		t.Fatalf("created blueprint=%+v err=%v", created, err)
	}
}

func TestCreatePlanFailsClosedAndRejectsZeroBlueprintRevision(t *testing.T) {
	repository := &repositoryStub{blueprint: Blueprint{BlueprintID: "bp_service-test", Revision: 1, ContentSHA256: testDigestA}}
	service := NewService(repository, validatorStub{}, nil, sequenceIDs(), fixedClock())
	_, err := service.CreatePlan(context.Background(), CreatePlanCommand{BlueprintID: "bp_service-test", BlueprintVersion: 1, Environment: "test", ActorID: "admin", IdempotencyKey: "plan-create-key-01", TraceID: "trace"})
	if !errors.Is(err, ErrPlanUnavailable) {
		t.Fatalf("nil planner error=%v", err)
	}
	service.planner = plannerStub{}
	_, err = service.CreatePlan(context.Background(), CreatePlanCommand{BlueprintID: "bp_service-test", BlueprintVersion: 0, Environment: "test", ActorID: "admin", IdempotencyKey: "plan-create-key-02", TraceID: "trace"})
	if !errors.Is(err, ErrInvalidCommand) || repository.createPlanCalls != 0 {
		t.Fatalf("zero revision error=%v calls=%d", err, repository.createPlanCalls)
	}
}

func TestConfirmAndStartRequireLockedConfirmationChecksum(t *testing.T) {
	now := fixedClock()()
	confirmationChecksum, err := ConfirmationSummaryChecksum(0, 1, []string{"Confirm locked plan."})
	if err != nil {
		t.Fatal(err)
	}
	planDocument := json.RawMessage(`{"conflicts":[],"risks":[{"risk_id":"risk.locked-plan"}],"confirmation":{"required":true,"blocking_conflict_count":0,"risk_count":1,"statements":["Confirm locked plan."],"summary_checksum":"` + confirmationChecksum + `"}}`)
	repository := &repositoryStub{plan: Plan{PlanID: "plan.service-test", Version: 1, Executable: true, PlanSHA256: testDigestB, Document: planDocument, ConfirmedAt: &now}}
	service := NewService(repository, validatorStub{}, plannerStub{}, sequenceIDs(), fixedClock(), WithOutputTargetVerifier(OutputTargetVerifierFunc(
		func(context.Context, string, string) error { return nil },
	)))
	_, err = service.ConfirmPlan(context.Background(), ConfirmPlanCommand{PlanID: repository.plan.PlanID, ConfirmationChecksum: testDigestB, ExpectedVersion: 1, ActorID: "admin", IdempotencyKey: "confirm-plan-key1", TraceID: "trace"})
	if !errors.Is(err, ErrConflict) || repository.confirmCalls != 0 {
		t.Fatalf("confirmation mismatch err=%v calls=%d", err, repository.confirmCalls)
	}
	_, err = service.ConfirmPlan(context.Background(), ConfirmPlanCommand{PlanID: repository.plan.PlanID, ConfirmationChecksum: confirmationChecksum, ExpectedVersion: 1, ActorID: "admin", IdempotencyKey: "confirm-plan-key2", TraceID: "trace"})
	if err != nil || repository.confirmCalls != 1 {
		t.Fatalf("confirm err=%v calls=%d", err, repository.confirmCalls)
	}
	_, err = service.StartAssembly(context.Background(), StartAssemblyCommand{PlanID: repository.plan.PlanID, PlanChecksum: testDigestB, ConfirmationChecksum: testDigestB, OutputTargetRef: "workspace.default", ExpectedPlanVersion: 1, ActorID: "admin", IdempotencyKey: "start-assembly-01", TraceID: "trace"})
	if !errors.Is(err, ErrConflict) || repository.startCalls != 0 {
		t.Fatalf("start confirmation mismatch err=%v calls=%d", err, repository.startCalls)
	}
}

func TestStartAssemblyFailsClosedThroughServerOutputTargetVerifier(t *testing.T) {
	now := fixedClock()()
	confirmationChecksum, err := ConfirmationSummaryChecksum(0, 0, []string{"Confirm locked plan."})
	if err != nil {
		t.Fatal(err)
	}
	planDocument := json.RawMessage(`{"conflicts":[],"risks":[],"confirmation":{"required":true,"blocking_conflict_count":0,"risk_count":0,"statements":["Confirm locked plan."],"summary_checksum":"` + confirmationChecksum + `"}}`)
	newRepository := func() *repositoryStub {
		return &repositoryStub{plan: Plan{
			PlanID: "plan.output-target", Version: 2, Environment: "production", Executable: true,
			PlanSHA256: testDigestB, Document: planDocument, ConfirmedAt: &now,
		}}
	}
	command := StartAssemblyCommand{
		PlanID: "plan.output-target", PlanChecksum: testDigestB, ConfirmationChecksum: confirmationChecksum,
		OutputTargetRef: "workspace.default", ExpectedPlanVersion: 2, ActorID: "admin",
		IdempotencyKey: "start-output-target-01", TraceID: "trace",
	}

	repository := newRepository()
	service := NewService(repository, validatorStub{}, plannerStub{}, sequenceIDs(), fixedClock())
	if _, err := service.StartAssembly(context.Background(), command); !errors.Is(err, ErrOutputTargetUnavailable) || repository.startCalls != 0 {
		t.Fatalf("missing verifier error=%v starts=%d", err, repository.startCalls)
	}

	repository = newRepository()
	service = NewService(repository, validatorStub{}, plannerStub{}, sequenceIDs(), fixedClock(), WithOutputTargetVerifier(OutputTargetVerifierFunc(
		func(_ context.Context, environment, outputTargetRef string) error {
			if environment != "production" || outputTargetRef != "workspace.default" {
				return ErrOutputTargetUnavailable
			}
			return nil
		},
	)))
	if _, err := service.StartAssembly(context.Background(), command); err != nil || repository.startCalls != 1 {
		t.Fatalf("verified target error=%v starts=%d", err, repository.startCalls)
	}
}

func TestPlanConfirmationChecksumRejectsCountsThatDoNotMatchPlan(t *testing.T) {
	checksum, err := ConfirmationSummaryChecksum(0, 1, []string{"Confirm locked plan."})
	if err != nil {
		t.Fatal(err)
	}
	document := json.RawMessage(`{"conflicts":[{"blocking":true}],"risks":[],"confirmation":{"required":true,"blocking_conflict_count":0,"risk_count":1,"statements":["Confirm locked plan."],"summary_checksum":"` + checksum + `"}}`)
	if _, err := planConfirmationChecksum(document); !errors.Is(err, ErrDocumentInvalid) {
		t.Fatalf("planConfirmationChecksum() error=%v, want %v", err, ErrDocumentInvalid)
	}
}

func TestPlanConfirmationChecksumRejectsExecutableBlockingPlan(t *testing.T) {
	checksum, err := ConfirmationSummaryChecksum(1, 0, []string{"Resolve conflict."})
	if err != nil {
		t.Fatal(err)
	}
	document := json.RawMessage(`{"conflicts":[{"blocking":true}],"risks":[],"executable":true,"confirmation":{"required":true,"blocking_conflict_count":1,"risk_count":0,"statements":["Resolve conflict."],"summary_checksum":"` + checksum + `"}}`)
	if _, err := planConfirmationChecksum(document); !errors.Is(err, ErrDocumentInvalid) {
		t.Fatalf("planConfirmationChecksum() error=%v, want %v", err, ErrDocumentInvalid)
	}
}

func TestFailedRunUsesServerAuditTimeAndFailureResult(t *testing.T) {
	serverNow := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	documentTime := serverNow.Add(-24 * time.Hour)
	repository := &repositoryStub{run: Run{RunID: "run.service-test", PlanID: "plan.service-test", PlanVersion: 2, Version: 1, PlanSHA256: testDigestA, IdempotencyKeyDigest: testDigestB, OutputTargetRef: "workspace.default", Status: RunStatusProvisioning, Steps: []RunStep{{StepID: "step.provision", Kind: "provision", Status: "running", Attempt: 1, CompensationStatus: "pending"}}, CreatedAt: documentTime, UpdatedAt: documentTime}}
	service := NewService(repository, validatorStub{}, nil, sequenceIDs(), func() time.Time { return serverNow })
	document := json.RawMessage(fmt.Sprintf(`{"run_id":"run.service-test","plan_id":"plan.service-test","plan_checksum":"%s","idempotency_key_digest":"%s","output_target_ref":"workspace.default","status":"failed","steps":[{"step_id":"step.provision","kind":"provision","status":"failed","attempt":1,"compensation_status":"pending","diagnostic_ids":["diagnostic.safe"]}],"current_step_id":"step.provision","diagnostic_ids":["diagnostic.safe"],"recovery":{"retryable":true,"rollback_required":true,"resume_from_step_id":"step.provision"},"created_at":"%s","updated_at":"%s","completed_at":"%s"}`, testDigestA, testDigestB, documentTime.Format(time.RFC3339), documentTime.Format(time.RFC3339), documentTime.Format(time.RFC3339)))
	_, err := service.UpdateRun(context.Background(), UpdateRunCommand{RunID: repository.run.RunID, ExpectedVersion: 1, Document: document, ActorID: "admin", IdempotencyKey: "update-failed-run1", TraceID: "trace"})
	if err != nil {
		t.Fatal(err)
	}
	if repository.lastUpdate.Event.OccurredAt != serverNow || repository.lastUpdate.Event.Payload.Result != "failure" || repository.lastUpdate.Event.Payload.ReasonCode != "assembly.run_failed" {
		t.Fatalf("failed event=%+v", repository.lastUpdate.Event)
	}
}

func TestResolveProductCapabilityChangeUsesPersistedBoundScope(t *testing.T) {
	now := fixedClock()()
	capabilities := []product.CapabilityItem{{CapabilityID: "identity.login", Enabled: true, Policy: json.RawMessage(`{}`), SourcePackageID: "package.account", SourcePackageVersion: "1.0.0"}}
	repository := &repositoryStub{plan: Plan{PlanID: "plan.bound", ProductID: "product-a", Executable: true, ConfirmedAt: &now, CatalogRevision: "catalog-r1", CatalogSnapshotSHA256: testDigestA, Document: json.RawMessage(`{"capabilities":[{"capability_id":"identity.login","enabled":true,"policy":{},"source_package_id":"package.account","source_package_version":"1.0.0"}]}`), Capabilities: capabilities}}
	service := NewService(repository, validatorStub{}, nil, sequenceIDs(), fixedClock())
	resolved, err := service.ResolveProductCapabilityChange(context.Background(), product.TrustedCapabilityChangePlan{ProductID: "product-a", SourcePlanID: "plan.bound", CatalogRevision: "catalog-r1", CatalogSnapshotSHA256: testDigestA})
	if err != nil || len(resolved.Items) != 1 {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	_, err = service.ResolveProductCapabilityChange(context.Background(), product.TrustedCapabilityChangePlan{ProductID: "product-b", SourcePlanID: "plan.bound", CatalogRevision: "catalog-r1", CatalogSnapshotSHA256: testDigestA})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("cross product verifier error=%v", err)
	}
}

func TestCompleteAssemblyRequiresManifestAndLockToMatchConfirmedPlan(t *testing.T) {
	now := fixedClock()()
	createdAt := now.Add(-2 * time.Minute)
	updatedAt := now.Add(-time.Minute)
	planDocument := json.RawMessage(`{"blueprint_id":"bp_complete","blueprint_version":1,"catalog_snapshot":{"checksum":"` + testDigestA + `"},"packages":[{"package_id":"package.account","version":"1.0.0","checksum":"` + testDigestB + `"}],"applications":[{"application_id":"application.web","template":{"template_id":"standard-web","version":"1.0.0","checksum":"` + testDigestA + `"}}],"generator":{"generator_id":"platform.generator","version":"1.0.0","checksum":"` + testDigestB + `"},"sdks":[{"sdk_id":"platform.sdk","version":"1.0.0","checksum":"` + testDigestA + `"}],"required_secret_refs":[],"expected_outputs":[{"path":"generated/account.ts","ownership":"generated","source_id":"package.account","source_version":"1.0.0","source_path":"content/account.ts.tmpl","source_sha256":"` + testDigestA + `","render_strategy":"strict_template","content_type":"text"}]}`)
	repository := &repositoryStub{
		blueprint: Blueprint{BlueprintID: "bp_complete", ProductID: "product-a", Revision: 1, ContentSHA256: testDigestB},
		plan:      Plan{PlanID: "plan.complete", ProductID: "product-a", BlueprintID: "bp_complete", BlueprintRevision: 1, Version: 2, PlanSHA256: testDigestA, BlueprintSHA256: testDigestB, CatalogSnapshotSHA256: testDigestA, Executable: true, ConfirmedAt: &createdAt, Document: planDocument},
		run:       Run{RunID: "run.complete", ProductID: "product-a", PlanID: "plan.complete", PlanVersion: 2, Version: 3, PlanSHA256: testDigestA, IdempotencyKeyDigest: testDigestB, OutputTargetRef: "workspace.default", Status: RunStatusValidating, Steps: []RunStep{{StepID: "step.validate", Kind: "validate", Status: "running", Attempt: 1, CompensationStatus: "not_required"}}, CreatedAt: createdAt, UpdatedAt: updatedAt},
	}
	runDocument := json.RawMessage(fmt.Sprintf(`{"run_id":"run.complete","plan_id":"plan.complete","plan_checksum":"%s","idempotency_key_digest":"%s","output_target_ref":"workspace.default","status":"completed","steps":[{"step_id":"step.validate","kind":"validate","status":"completed","attempt":1,"compensation_status":"not_required"}],"current_step_id":null,"diagnostic_ids":[],"manifest_path":"artifacts/assembly-manifest.json","lock_path":"artifacts/generated-project-lock.json","recovery":{"retryable":false,"rollback_required":false},"created_at":"%s","updated_at":"%s","completed_at":"%s"}`, testDigestA, testDigestB, createdAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)))
	manifest := jsonWithDigest(t, map[string]any{
		"schema_version": "1.0.0", "assembly_id": "assembly.complete", "run_id": "run.complete",
		"product":   map[string]any{"product_id": "product-a", "official_tenant_id": "tenant.official", "applications": []any{map[string]any{"plan_application_id": "application.web", "application_id": "app.web"}}},
		"blueprint": map[string]any{"blueprint_id": "bp_complete", "version": 1, "checksum": testDigestB}, "catalog_checksum": testDigestA,
		"packages":  []any{map[string]any{"package_id": "package.account", "version": "1.0.0", "checksum": testDigestB}},
		"templates": []any{map[string]any{"template_id": "standard-web", "version": "1.0.0", "checksum": testDigestA}},
		"sdks":      []any{map[string]any{"sdk_id": "platform.sdk", "version": "1.0.0", "checksum": testDigestA}},
		"outputs": []any{map[string]any{
			"path": "generated/account.ts", "ownership": "generated", "sha256": testDigestB, "source_id": "package.account",
			"source_version": "1.0.0", "source_path": "content/account.ts.tmpl", "source_sha256": testDigestA,
			"render_strategy": "strict_template", "content_type": "text",
		}},
		"evidence":    []any{map[string]any{"evidence_id": "evidence.contract", "type": "contract_report", "status": "passed", "path": "artifacts/contract.json", "sha256": testDigestA}},
		"secret_refs": []any{}, "created_at": now.Format(time.RFC3339Nano), "manifest_checksum": testDigestA,
	}, "manifest_checksum")
	manifestChecksum, err := verifiedEmbeddedDigest(manifest, "manifest_checksum")
	if err != nil {
		t.Fatal(err)
	}
	lockValue := map[string]any{
		"schema_version": "1.0.0", "lock_id": "lock.complete", "assembly_manifest_checksum": manifestChecksum,
		"blueprint_checksum": testDigestB, "catalog_checksum": testDigestA, "target_snapshot_checksum": testDigestA,
		"generator": map[string]any{"generator_id": "platform.generator", "version": "1.0.0", "checksum": testDigestB},
		"packages":  []any{map[string]any{"id": "package.account", "version": "1.0.0", "checksum": testDigestB}},
		"templates": []any{map[string]any{"id": "standard-web", "version": "1.0.0", "checksum": testDigestA}},
		"sdks":      []any{map[string]any{"id": "platform.sdk", "version": "1.0.0", "checksum": testDigestA}},
		"files": []any{map[string]any{
			"path": "generated/account.ts", "ownership": "generated", "sha256": testDigestB, "generated_sha256": testDigestB,
			"source_id": "package.account", "source_version": "1.0.0", "source_path": "content/account.ts.tmpl",
			"source_sha256": testDigestA, "render_strategy": "strict_template", "content_type": "text", "update_policy": "replace_generated",
		}},
		"created_at": now.Format(time.RFC3339Nano), "lock_checksum": testDigestA,
	}
	lock := jsonWithDigest(t, lockValue, "lock_checksum")
	service := NewService(repository, validatorStub{}, nil, sequenceIDs(), fixedClock())
	result, err := service.CompleteAssembly(context.Background(), CompleteAssemblyCommand{ProductID: "product-a", RunID: "run.complete", ExpectedVersion: 3, RunDocument: runDocument, ManifestDocument: manifest, LockDocument: lock, ActorID: "admin", IdempotencyKey: "complete-assembly-001", TraceID: "trace"})
	if err != nil || result.ManifestID != "assembly.complete" || result.LockID != "lock.complete" || repository.completeCalls != 1 {
		t.Fatalf("CompleteAssembly() result=%+v error=%v calls=%d", result, err, repository.completeCalls)
	}
	lockValue["generator"].(map[string]any)["checksum"] = testDigestA
	tamperedLock := jsonWithDigest(t, lockValue, "lock_checksum")
	repository.completeCalls = 0
	_, err = service.CompleteAssembly(context.Background(), CompleteAssemblyCommand{ProductID: "product-a", RunID: "run.complete", ExpectedVersion: 3, RunDocument: runDocument, ManifestDocument: manifest, LockDocument: tamperedLock, ActorID: "admin", IdempotencyKey: "complete-assembly-002", TraceID: "trace"})
	if !errors.Is(err, ErrDocumentInvalid) || repository.completeCalls != 0 {
		t.Fatalf("tampered lock error=%v calls=%d", err, repository.completeCalls)
	}
}

func TestRunEvolutionRejectsLockedFieldDrift(t *testing.T) {
	now := fixedClock()()
	current := Run{RunID: "run.locked", PlanID: "plan.locked", PlanSHA256: testDigestA, IdempotencyKeyDigest: testDigestB, OutputTargetRef: "workspace.default", Status: RunStatusGenerating, Steps: []RunStep{{StepID: "step.generate", Kind: "generate", Status: "running", Attempt: 1}}, CreatedAt: now, UpdatedAt: now}
	next := current
	next.OutputTargetRef = "workspace.other"
	if !errors.Is(validateRunEvolution(current, next), ErrInvalidRunTransition) {
		t.Fatal("output target drift was accepted")
	}
	next = current
	next.CreatedAt = now.Add(time.Second)
	if !errors.Is(validateRunEvolution(current, next), ErrInvalidRunTransition) {
		t.Fatal("created_at drift was accepted")
	}
}

func jsonWithDigest(t *testing.T, value map[string]any, field string) json.RawMessage {
	t.Helper()
	value[field] = testDigestA
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := machinecontract.DigestWithoutTopLevelField(raw, field)
	if err != nil {
		t.Fatal(err)
	}
	value[field] = digest
	raw, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func sequenceIDs() IDGenerator {
	var n atomic.Uint64
	return func(prefix string) (string, error) { return fmt.Sprintf("%s%032x", prefix, n.Add(1)), nil }
}
func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC) }
}
