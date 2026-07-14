package generation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type fixedRenderer struct {
	result Result
	err    error
}

func (r fixedRenderer) Render(context.Context, Input) (Result, error) {
	return r.result, r.err
}

func TestExecutorPublishesSchemaValidArtifactsAndReplaysIdempotently(t *testing.T) {
	targetRoot, artifactRoot := separateTestRoots(t)
	writeTestFile(t, targetRoot, "src/custom/workbench.ts", []byte("export const custom = true;\n"))
	output := artifactTestOutput("src/generated/account.ts")
	rendered := testRenderedFile(output, []byte("export const account = true;\n"))
	request, plan := artifactTestRequest(t, targetRoot, []RenderedFile{rendered})
	registry := artifactTestRegistry(t)
	requestRaw, marshalErr := json.Marshal(request)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err := registry.Validate("generator-request", requestRaw); err != nil {
		t.Fatalf("generator request: %v", err)
	}
	store, err := NewArtifactStore(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewExecutor(fixedRenderer{result: Result{Files: []RenderedFile{rendered}}}, NewFileCommitter(), store, registry)
	if err != nil {
		t.Fatal(err)
	}
	input := artifactTestInput(request, plan)
	first, err := executor.Execute(context.Background(), targetRoot, input, ProjectLock{}, PreviousArtifacts{})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Published || !first.Commit.AtomicCommitCompleted {
		t.Fatalf("first outcome = %#v", first)
	}
	assertTestFile(t, targetRoot, output.Path, string(rendered.Bytes))
	assertTestFile(t, targetRoot, "src/custom/workbench.ts", "export const custom = true;\n")
	for name, artifactPath := range map[string]string{
		"assembly-manifest":        request.ArtifactContext.Paths.AssemblyManifestPath,
		"generated-project-lock":   request.ArtifactContext.Paths.GeneratedLockPath,
		"generator-rollback-point": request.ArtifactContext.Paths.RollbackPointPath,
		"generator-commit-journal": request.ArtifactContext.Paths.CommitJournalPath,
		"generator-result":         request.ArtifactContext.Paths.ResultPath,
	} {
		raw := []byte(readTestFile(t, artifactRoot, artifactPath))
		if err := registry.Validate(name, raw); err != nil {
			t.Fatalf("%s validation: %v", name, err)
		}
	}
	var journal commitJournalDocument
	if err := json.Unmarshal([]byte(readTestFile(t, artifactRoot, request.ArtifactContext.Paths.CommitJournalPath)), &journal); err != nil || journal.State != "committed" {
		t.Fatalf("journal = %#v, %v", journal, err)
	}
	var rollback rollbackPointDocument
	if err := json.Unmarshal([]byte(readTestFile(t, artifactRoot, request.ArtifactContext.Paths.RollbackPointPath)), &rollback); err != nil || rollback.PreviousState != "absent" || len(rollback.Files) != 1 || rollback.Files[0].Action != "created" {
		t.Fatalf("rollback = %#v, %v", rollback, err)
	}

	replayed, err := executor.Execute(context.Background(), targetRoot, input, ProjectLock{}, PreviousArtifacts{})
	if err != nil || !replayed.Published || !replayed.Commit.TargetUnchanged {
		t.Fatalf("replay = %#v, %v", replayed, err)
	}
	writeTestFile(t, targetRoot, output.Path, []byte("tampered\n"))
	if _, err := executor.Execute(context.Background(), targetRoot, input, ProjectLock{}, PreviousArtifacts{}); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("tampered replay error = %v", err)
	}
}

func TestExecutorPersistsFailureEvidenceAfterCommitRollback(t *testing.T) {
	targetRoot, artifactRoot := separateTestRoots(t)
	firstOutput := artifactTestOutput("src/generated/a.ts")
	secondOutput := artifactTestOutput("src/generated/b.ts")
	firstRendered := testRenderedFile(firstOutput, []byte("export const a = true;\n"))
	secondRendered := testRenderedFile(secondOutput, []byte("export const b = true;\n"))
	request, plan := artifactTestRequest(t, targetRoot, []RenderedFile{firstRendered, secondRendered})
	registry := artifactTestRegistry(t)
	requestRaw, marshalErr := json.Marshal(request)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err := registry.Validate("generator-request", requestRaw); err != nil {
		t.Fatalf("generator request: %v", err)
	}
	store, err := NewArtifactStore(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	committer := NewFileCommitter()
	committer.beforeReplace = func(index int, _ FileChange) error {
		if index == 1 {
			return errors.New("injected failure")
		}
		return nil
	}
	executor, err := NewExecutor(fixedRenderer{result: Result{Files: []RenderedFile{secondRendered, firstRendered}}}, committer, store, registry)
	if err != nil {
		t.Fatal(err)
	}
	input := artifactTestInput(request, plan)
	outcome, err := executor.Execute(context.Background(), targetRoot, input, ProjectLock{}, PreviousArtifacts{})
	if !errors.Is(err, ErrCommitFailed) || !outcome.Published || !outcome.Commit.RollbackAttempted || !outcome.Commit.RollbackCompleted || !outcome.Commit.TargetUnchanged {
		t.Fatalf("outcome = %#v, error = %v", outcome, err)
	}
	for _, output := range []OutputSpec{firstOutput, secondOutput} {
		if _, statErr := os.Stat(filepath.Join(targetRoot, filepath.FromSlash(output.Path))); !os.IsNotExist(statErr) {
			t.Fatalf("rolled back output %s still exists: %v", output.Path, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(artifactRoot, filepath.FromSlash(request.ArtifactContext.Paths.AssemblyManifestPath))); !os.IsNotExist(statErr) {
		t.Fatalf("failed transaction published a manifest: %v", statErr)
	}
	var journal commitJournalDocument
	journalRaw := []byte(readTestFile(t, artifactRoot, request.ArtifactContext.Paths.CommitJournalPath))
	if err := json.Unmarshal(journalRaw, &journal); err != nil || journal.State != "rolled_back" || !journal.RollbackAttempted || !journal.RollbackCompleted {
		t.Fatalf("journal = %#v, %v", journal, err)
	}
	if err := registry.Validate("generator-commit-journal", journalRaw); err != nil {
		t.Fatal(err)
	}
	var result generatorResultDocument
	resultRaw := []byte(readTestFile(t, artifactRoot, request.ArtifactContext.Paths.ResultPath))
	if err := json.Unmarshal(resultRaw, &result); err != nil || result.Status != "failed" || len(result.DiagnosticIDs) != 1 {
		t.Fatalf("result = %#v, %v", result, err)
	}
	if err := registry.Validate("generator-result", resultRaw); err != nil {
		t.Fatal(err)
	}
	diagnosticPath := filepath.ToSlash(filepath.Join(request.ArtifactContext.Paths.DiagnosticDirectory, result.DiagnosticIDs[0]+".json"))
	if err := registry.Validate("generator-diagnostic", []byte(readTestFile(t, artifactRoot, diagnosticPath))); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitRollbackRestoresCommittedTargetAndIsIdempotent(t *testing.T) {
	targetRoot, artifactRoot := separateTestRoots(t)
	writeTestFile(t, targetRoot, "src/custom/workbench.ts", []byte("custom\n"))
	output := artifactTestOutput("src/generated/account.ts")
	rendered := testRenderedFile(output, []byte("generated\n"))
	request, plan := artifactTestRequest(t, targetRoot, []RenderedFile{rendered})
	registry := artifactTestRegistry(t)
	store, err := NewArtifactStore(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewExecutor(fixedRenderer{result: Result{Files: []RenderedFile{rendered}}}, NewFileCommitter(), store, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), targetRoot, artifactTestInput(request, plan), ProjectLock{}, PreviousArtifacts{}); err != nil {
		t.Fatal(err)
	}
	rollback, err := NewRollbackExecutor(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	first, err := rollback.Rollback(context.Background(), targetRoot, request.ArtifactContext.Paths.RollbackPointPath, request.ArtifactContext.Paths.CommitJournalPath)
	if err != nil || first.Checksum == "" {
		t.Fatalf("first rollback = %#v, %v", first, err)
	}
	if _, statErr := os.Stat(filepath.Join(targetRoot, filepath.FromSlash(output.Path))); !os.IsNotExist(statErr) {
		t.Fatalf("generated output remains after rollback: %v", statErr)
	}
	assertTestFile(t, targetRoot, "src/custom/workbench.ts", "custom\n")
	second, err := rollback.Rollback(context.Background(), targetRoot, request.ArtifactContext.Paths.RollbackPointPath, request.ArtifactContext.Paths.CommitJournalPath)
	if err != nil || second.Checksum != first.Checksum {
		t.Fatalf("replayed rollback = %#v, %v", second, err)
	}
	var journal commitJournalDocument
	journalRaw := []byte(readTestFile(t, artifactRoot, request.ArtifactContext.Paths.CommitJournalPath))
	if json.Unmarshal(journalRaw, &journal) != nil || journal.State != "rolled_back" || !journal.RollbackCompleted {
		t.Fatalf("journal = %#v", journal)
	}
	if err := registry.Validate("generator-commit-journal", journalRaw); err != nil {
		t.Fatal(err)
	}
}

func TestEjectPlanRecordsOwnershipAndCurrentDriftWithoutOverwriting(t *testing.T) {
	targetRoot, artifactRoot := separateTestRoots(t)
	writeTestFile(t, targetRoot, "src/custom/workbench.ts", []byte("custom\n"))
	output := artifactTestOutput("src/generated/account.ts")
	rendered := testRenderedFile(output, []byte("generated\n"))
	request, plan := artifactTestRequest(t, targetRoot, []RenderedFile{rendered})
	registry := artifactTestRegistry(t)
	store, err := NewArtifactStore(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewExecutor(fixedRenderer{result: Result{Files: []RenderedFile{rendered}}}, NewFileCommitter(), store, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), targetRoot, artifactTestInput(request, plan), ProjectLock{}, PreviousArtifacts{}); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, targetRoot, output.Path, []byte("product fork\n"))
	lockRaw := []byte(readTestFile(t, artifactRoot, request.ArtifactContext.Paths.GeneratedLockPath))
	ejectRaw, err := BuildEjectPlan(targetRoot, lockRaw, []string{output.Path})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate("generator-eject-plan", ejectRaw); err != nil {
		t.Fatal(err)
	}
	var eject EjectPlan
	if json.Unmarshal(ejectRaw, &eject) != nil || len(eject.Files) != 1 || eject.Files[0].NewOwnership != "forked" || !eject.Files[0].ModifiedFromBaseline {
		t.Fatalf("eject plan = %#v", eject)
	}
	if _, err := BuildEjectPlan(targetRoot, lockRaw, []string{"src/custom/workbench.ts"}); !errors.Is(err, ErrOwnershipConflict) {
		t.Fatalf("custom eject error = %v", err)
	}
	assertTestFile(t, targetRoot, output.Path, "product fork\n")
}

func TestUpgradePersistsPreviousArtifactsAndExplicitRollbackRestoresOldBytes(t *testing.T) {
	targetRoot, artifactRoot := separateTestRoots(t)
	output := artifactTestOutput("src/generated/account.ts")
	oldRendered := testRenderedFile(output, []byte("old\n"))
	firstRequest, firstPlan := artifactTestRequest(t, targetRoot, []RenderedFile{oldRendered})
	registry := artifactTestRegistry(t)
	store, err := NewArtifactStore(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	firstExecutor, err := NewExecutor(fixedRenderer{result: Result{Files: []RenderedFile{oldRendered}}}, NewFileCommitter(), store, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := firstExecutor.Execute(context.Background(), targetRoot, artifactTestInput(firstRequest, firstPlan), ProjectLock{}, PreviousArtifacts{}); err != nil {
		t.Fatal(err)
	}
	previousLockRaw := []byte(readTestFile(t, artifactRoot, firstRequest.ArtifactContext.Paths.GeneratedLockPath))
	previousLock, err := DecodeProjectLock(previousLockRaw)
	if err != nil {
		t.Fatal(err)
	}
	var planValue map[string]any
	if json.Unmarshal(firstPlan, &planValue) != nil {
		t.Fatal("decode first plan")
	}
	planValue["plan_id"] = "plan.artifact-upgrade"
	planValue["plan_checksum"] = rawDigest([]byte("upgrade-plan"))
	upgradePlan, err := json.Marshal(planValue)
	if err != nil {
		t.Fatal(err)
	}
	upgradeInput, previous, err := BuildRequest(targetRoot, RequestSpec{
		WorkspaceRef: "workspace.test", RunID: "run.artifact-upgrade", RunCreatedAt: time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC),
		Product:           ArtifactProduct{ProductID: "product.test", OfficialTenantID: "tenant.official", Applications: []ArtifactApplication{{PlanApplicationID: "application.web", ApplicationID: "app.web"}}},
		Blueprint:         ArtifactBlueprint{BlueprintID: "bp_test-product", Version: 1, Checksum: rawDigest([]byte("blueprint"))},
		BlueprintDocument: json.RawMessage(`{"product":{"name":"Demo","symbol":"Demo"}}`), PlanDocument: upgradePlan,
		PreviousLock: previousLock, PreviousManifestPath: firstRequest.ArtifactContext.Paths.AssemblyManifestPath, PreviousLockPath: firstRequest.ArtifactContext.Paths.GeneratedLockPath,
	})
	if err != nil || upgradeInput.Request.Operation != "upgrade" || previous.LockSHA256 != previousLock.LockChecksum {
		t.Fatalf("upgrade request previous=%#v error=%v", previous, err)
	}
	newRendered := testRenderedFile(output, []byte("new\n"))
	upgradeExecutor, err := NewExecutor(fixedRenderer{result: Result{Files: []RenderedFile{newRendered}}}, NewFileCommitter(), store, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upgradeExecutor.Execute(context.Background(), targetRoot, upgradeInput, previousLock, previous); err != nil {
		t.Fatal(err)
	}
	assertTestFile(t, targetRoot, output.Path, "new\n")
	var rollbackPoint rollbackPointDocument
	if json.Unmarshal([]byte(readTestFile(t, artifactRoot, upgradeInput.Request.ArtifactContext.Paths.RollbackPointPath)), &rollbackPoint) != nil || rollbackPoint.PreviousState != "present" || rollbackPoint.ManifestPath != firstRequest.ArtifactContext.Paths.AssemblyManifestPath || len(rollbackPoint.Files) != 1 || rollbackPoint.Files[0].Action != "updated" {
		t.Fatalf("upgrade rollback point = %#v", rollbackPoint)
	}
	rollback, err := NewRollbackExecutor(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rollback.Rollback(context.Background(), targetRoot, upgradeInput.Request.ArtifactContext.Paths.RollbackPointPath, upgradeInput.Request.ArtifactContext.Paths.CommitJournalPath); err != nil {
		t.Fatal(err)
	}
	assertTestFile(t, targetRoot, output.Path, "old\n")
}

func artifactTestRequest(t *testing.T, targetRoot string, rendered []RenderedFile) (Request, json.RawMessage) {
	t.Helper()
	outputs := make([]OutputSpec, 0, len(rendered))
	for _, file := range rendered {
		outputs = append(outputs, file.OutputSpec)
	}
	planValue := map[string]any{
		"plan_id": "plan.artifact-test", "plan_checksum": rawDigest([]byte("artifact-plan")), "blueprint_id": "bp_test-product", "blueprint_version": 1,
		"catalog_snapshot": map[string]any{"checksum": rawDigest([]byte("catalog"))},
		"generator":        map[string]any{"generator_id": "platform.generator", "version": "1.0.0", "checksum": rawDigest([]byte("generator"))},
		"expected_outputs": outputs, "required_secret_refs": []any{},
		"packages":     []any{map[string]any{"package_id": "package.account", "version": "1.0.0", "checksum": rawDigest([]byte("package"))}},
		"applications": []any{map[string]any{"application_id": "application.web", "template": map[string]any{"template_id": "standard-web", "version": "1.0.0", "checksum": rawDigest([]byte("template"))}}},
		"sdks":         []any{map[string]any{"sdk_id": "sdk.typescript", "version": "1.0.0", "checksum": rawDigest([]byte("sdk"))}},
	}
	planRaw, err := json.Marshal(planValue)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := InspectTarget(targetRoot, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	request := testFileRequest(snapshot, rendered)
	request.PlanChecksum = planValue["plan_checksum"].(string)
	request.Generator = Tool{GeneratorID: "platform.generator", Version: "1.0.0", Checksum: rawDigest([]byte("generator"))}
	request.RequestID = "request.artifact-test"
	request.WorkspaceRef = "workspace.test"
	request.StagingPath = ".runtime/generator/request-artifact-test"
	request.RollbackPointPath = "artifacts/assembly/assembly-test/rollback-point.json"
	request.ArtifactContext = ArtifactContext{
		AssemblyID: "assembly.test", LockID: "lock.test", RollbackID: "rollback.test", RunID: "run.test",
		Product:         ArtifactProduct{ProductID: "product.test", OfficialTenantID: "tenant.official", Applications: []ArtifactApplication{{PlanApplicationID: "application.web", ApplicationID: "app.web"}}},
		Blueprint:       ArtifactBlueprint{BlueprintID: "bp_test-product", Version: 1, Checksum: rawDigest([]byte("blueprint"))},
		CatalogChecksum: rawDigest([]byte("catalog")),
		Evidence:        []Evidence{{EvidenceID: "evidence.contract", Type: "contract_report", Status: "passed", Path: "artifacts/assembly/assembly-test/reports/contract.json", SHA256: rawDigest([]byte("contract report\n"))}},
		CreatedAt:       "2026-07-14T08:00:00Z",
		Paths: ArtifactPaths{
			ArtifactStagingPath: ".runtime/assembly/request-artifact-test", AssemblyManifestPath: "artifacts/assembly/assembly-test/assembly-manifest.json",
			GeneratedLockPath: "artifacts/assembly/assembly-test/generated-project-lock.json", RollbackPointPath: "artifacts/assembly/assembly-test/rollback-point.json",
			CommitJournalPath: "artifacts/assembly/assembly-test/commit-journal.json", ResultPath: "artifacts/assembly/assembly-test/generator-result.json",
			DiagnosticDirectory: "artifacts/assembly/assembly-test/diagnostics",
		},
	}
	return request, planRaw
}

func artifactTestOutput(outputPath string) OutputSpec {
	return OutputSpec{
		Path: outputPath, Ownership: "generated", SourceID: "package.account", SourceVersion: "1.0.0",
		SourcePath: "content/account.ts.tmpl", SourceSHA256: rawDigest([]byte(outputPath)), RenderStrategy: "strict_template", ContentType: "text",
	}
}

func artifactTestInput(request Request, plan json.RawMessage) Input {
	report := []byte("contract report\n")
	return Input{
		Request: request, Blueprint: json.RawMessage(`{"product":{"name":"Demo","symbol":"Demo"}}`), Plan: plan,
		EvidenceDocuments: map[string][]byte{"artifacts/assembly/assembly-test/reports/contract.json": report},
	}
}

func artifactTestRegistry(t *testing.T) *machinecontract.Registry {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", "..", "..", ".."))
	registry, err := machinecontract.LoadDirectory(filepath.Join(root, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func separateTestRoots(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "target")
	artifacts := filepath.Join(root, "artifact-store")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	return target, artifacts
}
