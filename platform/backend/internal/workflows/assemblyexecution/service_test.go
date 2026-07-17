package assemblyexecution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/assembly/generation"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/workflows/productprovisioning"
)

type assemblyStub struct {
	run       core.Run
	plan      core.Plan
	blueprint core.Blueprint
	contracts *machinecontract.Registry
	updates   []core.RunStatus
}

func (s *assemblyStub) GetRun(context.Context, string) (core.Run, error)   { return s.run, nil }
func (s *assemblyStub) GetPlan(context.Context, string) (core.Plan, error) { return s.plan, nil }
func (s *assemblyStub) GetBlueprint(context.Context, string, int64) (core.Blueprint, error) {
	return s.blueprint, nil
}
func (s *assemblyStub) BindProduct(_ context.Context, command core.BindProductCommand) (core.Run, error) {
	if command.ExpectedVersion != s.run.Version {
		return core.Run{}, core.ErrVersionConflict
	}
	s.run.ProductID = command.ProductID
	s.plan.ProductID = command.ProductID
	s.blueprint.ProductID = command.ProductID
	s.run.Version++
	return s.run, nil
}
func (s *assemblyStub) UpdateRun(_ context.Context, command core.UpdateRunCommand) (core.Run, error) {
	if command.ExpectedVersion != s.run.Version {
		return core.Run{}, core.ErrVersionConflict
	}
	var document runMachineDocument
	if err := json.Unmarshal(command.Document, &document); err != nil {
		return core.Run{}, err
	}
	s.run.Version++
	s.run.Status, s.run.Steps, s.run.Recovery = document.Status, document.Steps, document.Recovery
	s.run.DiagnosticIDs = document.DiagnosticIDs
	s.run.CurrentStepID = ""
	if document.CurrentStepID != nil {
		s.run.CurrentStepID = *document.CurrentStepID
	}
	s.run.UpdatedAt, s.run.CompletedAt, s.run.Document = document.UpdatedAt, document.CompletedAt, append(json.RawMessage(nil), command.Document...)
	s.updates = append(s.updates, document.Status)
	return s.run, nil
}
func (s *assemblyStub) CompleteAssembly(_ context.Context, command core.CompleteAssemblyCommand) (core.Run, error) {
	if command.ExpectedVersion != s.run.Version {
		return core.Run{}, core.ErrVersionConflict
	}
	if err := s.contracts.Validate("assembly-manifest", command.ManifestDocument); err != nil {
		return core.Run{}, err
	}
	if err := s.contracts.Validate("generated-project-lock", command.LockDocument); err != nil {
		return core.Run{}, err
	}
	var document runMachineDocument
	var manifest struct {
		AssemblyID string `json:"assembly_id"`
	}
	var lock struct {
		LockID string `json:"lock_id"`
	}
	if json.Unmarshal(command.RunDocument, &document) != nil || json.Unmarshal(command.ManifestDocument, &manifest) != nil || json.Unmarshal(command.LockDocument, &lock) != nil {
		return core.Run{}, errors.New("invalid completion documents")
	}
	s.run.Version++
	s.run.Status, s.run.Steps, s.run.Recovery = document.Status, document.Steps, document.Recovery
	s.run.ManifestID, s.run.LockID, s.run.CompletedAt = manifest.AssemblyID, lock.LockID, document.CompletedAt
	s.updates = append(s.updates, document.Status)
	return s.run, nil
}

type productProvisionerStub struct{ calls int }

func (s *productProvisionerStub) CreateProduct(context.Context, productprovisioning.CreateCommand) (product.Product, error) {
	s.calls++
	return product.Product{ProductID: "product.runtime", OfficialTenantID: "tenant.official", ProvisioningState: "ready"}, nil
}

type applicationServiceStub struct{ calls int }

func (s *applicationServiceStub) CreateApplication(_ context.Context, command productapplication.CreateCommand) (productapplication.Application, error) {
	s.calls++
	return productapplication.Application{ApplicationID: "app.runtime", ProductID: command.Product.ProductID, ApplicationCode: command.ApplicationCode}, nil
}

type capabilityServiceStub struct{ replaceCalls int }

func (s *capabilityServiceStub) CurrentCapabilitySet(context.Context, string) (product.CapabilitySet, error) {
	return product.CapabilitySet{}, product.ErrNotFound
}
func (s *capabilityServiceStub) ReplaceCapabilitySet(_ context.Context, command product.ReplaceCapabilitySetCommand) (product.CapabilitySet, error) {
	s.replaceCalls++
	return product.CapabilitySet{ProductID: command.Plan.ProductID, Version: 1, SourcePlanID: command.Plan.SourcePlanID}, nil
}

type rendererStub struct{ result generation.Result }

func (s rendererStub) Render(context.Context, generation.Input) (generation.Result, error) {
	return s.result, nil
}

func TestExecuteRunsProvisionGenerationAndCompletionChain(t *testing.T) {
	root := t.TempDir()
	targetRoot, artifactRoot := filepath.Join(root, "target"), filepath.Join(root, "artifacts")
	if err := os.Mkdir(targetRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(artifactRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	workspaces, err := generation.NewWorkspaceCatalog([]generation.Workspace{{Reference: "workspace.default", TargetRoot: targetRoot, ArtifactRoot: artifactRoot}})
	if err != nil {
		t.Fatal(err)
	}
	output := generation.OutputSpec{
		Path: "generated/account.ts", Ownership: "generated", SourceID: "package.account", SourceVersion: "1.0.0",
		SourcePath: "content/account.ts.tmpl", SourceSHA256: testDigest("source"), RenderStrategy: "strict_template", ContentType: "text",
	}
	content := []byte("export const account = true;\n")
	rendered := generation.RenderedFile{OutputSpec: output, Bytes: content, SHA256: testDigestBytes(content), GeneratedSHA256: testDigestBytes(content)}
	planRaw, err := json.Marshal(map[string]any{
		"schema_version": "1.0.0", "plan_id": "plan.execution", "plan_checksum": testDigest("plan"),
		"blueprint_id": "bp_execution", "blueprint_version": 1, "environment": "test",
		"catalog_snapshot": map[string]any{"revision": "catalog-r1", "scope": "ordinary", "checksum": testDigest("catalog")},
		"packages":         []any{map[string]any{"package_id": "package.account", "version": "1.0.0", "checksum": testDigest("package")}},
		"applications":     []any{map[string]any{"application_id": "application.web", "target": "web", "channel": "official", "environment": "test", "template": map[string]any{"template_id": "standard-web", "version": "1.0.0", "checksum": testDigest("template")}}},
		"generator":        map[string]any{"generator_id": "platform.generator", "version": "1.0.0", "checksum": testDigest("generator")},
		"sdks":             []any{map[string]any{"sdk_id": "sdk.typescript", "version": "1.0.0", "checksum": testDigest("sdk")}},
		"expected_outputs": []generation.OutputSpec{output}, "required_secret_refs": []generation.SecretRef{},
	})
	if err != nil {
		t.Fatal(err)
	}
	blueprintRaw := json.RawMessage(`{"schema_version":"1.0.0","product":{"code":"execution-demo","name":"Execution Demo"}}`)
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	registry := executionRegistry(t)
	assembly := &assemblyStub{
		contracts: registry,
		blueprint: core.Blueprint{BlueprintID: "bp_execution", Revision: 1, ContentSHA256: testDigest("blueprint"), Document: blueprintRaw},
		plan:      core.Plan{PlanID: "plan.execution", BlueprintID: "bp_execution", BlueprintRevision: 1, Version: 2, PlanSHA256: testDigest("plan"), CatalogRevision: "catalog-r1", CatalogSnapshotSHA256: testDigest("catalog"), Document: planRaw, Executable: true},
		run: core.Run{
			RunID: "run.execution", PlanID: "plan.execution", PlanVersion: 2, Version: 1, PlanSHA256: testDigest("plan"), IdempotencyKeyDigest: testDigest("idempotency"),
			OutputTargetRef: "workspace.default", Status: core.RunStatusPlanned, CreatedAt: now, UpdatedAt: now,
			Steps: []core.RunStep{
				{StepID: "step.provision", Kind: "provision", Status: "pending", CompensationStatus: "pending"},
				{StepID: "step.enable-capability", Kind: "enable_capability", Status: "pending", CompensationStatus: "pending"},
				{StepID: "step.generate", Kind: "generate", Status: "pending", CompensationStatus: "pending"},
				{StepID: "step.validate", Kind: "validate", Status: "pending", CompensationStatus: "not_required"},
				{StepID: "step.commit", Kind: "commit", Status: "pending", CompensationStatus: "pending"},
			},
		},
	}
	products := &productProvisionerStub{}
	applications := &applicationServiceStub{}
	capabilities := &capabilityServiceStub{}
	service := New(assembly, products, applications, capabilities, workspaces, rendererStub{result: generation.Result{Files: []generation.RenderedFile{rendered}}}, registry, func() time.Time { return now })
	completed, err := service.Execute(context.Background(), Command{RunID: "run.execution", ActorID: "admin.execution", IdempotencyKey: "0123456789abcdef", TraceID: "trace.execution"})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != core.RunStatusCompleted || completed.ManifestID == "" || completed.LockID == "" || products.calls != 1 || applications.calls != 1 || capabilities.replaceCalls != 1 {
		t.Fatalf("completed=%#v products=%d applications=%d capabilities=%d", completed, products.calls, applications.calls, capabilities.replaceCalls)
	}
	wantStatuses := []core.RunStatus{core.RunStatusProvisioning, core.RunStatusGenerating, core.RunStatusValidating, core.RunStatusCompleted}
	if len(assembly.updates) != len(wantStatuses) {
		t.Fatalf("statuses = %v", assembly.updates)
	}
	for index := range wantStatuses {
		if assembly.updates[index] != wantStatuses[index] {
			t.Fatalf("statuses = %v", assembly.updates)
		}
	}
	if actual, err := os.ReadFile(filepath.Join(targetRoot, filepath.FromSlash(output.Path))); err != nil || string(actual) != string(content) {
		t.Fatalf("generated output = %q, %v", actual, err)
	}
}

func TestGeneratorFailureEvidenceProjectsValidatedSafeFields(t *testing.T) {
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	diagnostic := []byte(`{"diagnostic_id":"diagnostic.safe","code":"GENERATOR_TARGET_CHANGED","severity":"error","category":"conflict","message":"the target changed after inspection","blocking":true,"retryable":true,"path":"generated/app.ts","related_paths":[],"remediation":["inspect target changes"]}`)
	result := []byte(`{"status":"conflict","result_checksum":"` + testDigest("result") + `"}`)
	diagnostics, reports, err := generatorFailureEvidence(generation.FailureArtifacts{Diagnostics: map[string][]byte{"diagnostics/safe.json": diagnostic}, Result: result}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != "generator.target.changed" || len(diagnostics[0].RelatedPaths) != 1 || diagnostics[0].RelatedPaths[0] != "generated/app.ts" {
		t.Fatalf("diagnostics=%+v", diagnostics)
	}
	if len(reports) != 1 || reports[0].ReportType != "generator_result" || reports[0].Checksum != testDigest("result") {
		t.Fatalf("reports=%+v", reports)
	}
}

func executionRegistry(t *testing.T) *machinecontract.Registry {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", "..", ".."))
	registry, err := machinecontract.LoadDirectory(filepath.Join(root, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func testDigest(value string) string { return testDigestBytes([]byte(value)) }
func testDigestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}
