package generation

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEjectExecutorLeavesBodiesUnchangedAndClosesSuccessorDigests(t *testing.T) {
	executor, input, sourceManifest, sourceLock := ejectExecutorFixture(t, "generated")
	selectedPath := input.Paths[0]
	before, err := os.ReadFile(filepath.Join(input.TargetRoot, filepath.FromSlash(selectedPath)))
	if err != nil {
		t.Fatal(err)
	}

	outcome, err := executor.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(input.TargetRoot, filepath.FromSlash(selectedPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("eject modified target file contents")
	}
	if err := executor.contracts.Validate("generator-eject-plan", outcome.Plan); err != nil {
		t.Fatalf("plan validation: %v", err)
	}
	if err := executor.contracts.Validate("assembly-manifest", outcome.AssemblyManifest); err != nil {
		t.Fatalf("manifest validation: %v", err)
	}
	if err := executor.contracts.Validate("generated-project-lock", outcome.GeneratedLock); err != nil {
		t.Fatalf("lock validation: %v", err)
	}

	var manifest assemblyManifestDocument
	var lock generatedLockDocument
	if err := jsonUnmarshalStrict(outcome.AssemblyManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := jsonUnmarshalStrict(outcome.GeneratedLock, &lock); err != nil {
		t.Fatal(err)
	}
	if manifest.AssemblyID != input.NewAssemblyID || manifest.RunID != "" || manifest.LifecycleOperationID != input.OperationID {
		t.Fatalf("manifest successor identity = %#v", manifest)
	}
	if lock.LockID != input.NewLockID || lock.LifecycleOperationID != input.OperationID {
		t.Fatalf("lock successor identity = %#v", lock)
	}
	if !digestEqual(lock.AssemblyManifestChecksum, manifest.ManifestChecksum) ||
		!digestEqual(outcome.ManifestChecksum, manifest.ManifestChecksum) ||
		!digestEqual(outcome.LockChecksum, lock.LockChecksum) {
		t.Fatal("successor digest closure is broken")
	}
	if !digestEqual(lock.TargetSnapshotChecksum, outcome.TargetSnapshot.Checksum) {
		t.Fatal("successor lock does not close over returned target snapshot")
	}
	if digestEqual(outcome.TargetSnapshot.Checksum, outcome.RollbackEvidence.TargetSnapshotChecksum) {
		t.Fatal("ownership-only eject reused the predecessor target snapshot checksum")
	}
	if manifest.Outputs[0].Ownership != "forked" || lock.Files[0].Ownership != "forked" || lock.Files[0].UpdatePolicy != "diff_only" {
		t.Fatalf("selected ownership was not ejected: manifest=%q lock=%q policy=%q", manifest.Outputs[0].Ownership, lock.Files[0].Ownership, lock.Files[0].UpdatePolicy)
	}
	if manifest.Outputs[1] != sourceManifest.Outputs[1] || !reflect.DeepEqual(lock.Files[1], sourceLock.Files[1]) {
		t.Fatal("unselected facts changed")
	}
	if !bytes.Equal(outcome.RollbackEvidence.ManifestDocument, input.ManifestDocument) ||
		!bytes.Equal(outcome.RollbackEvidence.LockDocument, input.LockDocument) ||
		!digestEqual(outcome.RollbackEvidence.ManifestChecksum, sourceManifest.ManifestChecksum) ||
		!digestEqual(outcome.RollbackEvidence.LockChecksum, sourceLock.LockChecksum) {
		t.Fatal("rollback evidence does not preserve the predecessor documents")
	}
}

func TestEjectExecutorRejectsCustomOwnership(t *testing.T) {
	executor, input, _, _ := ejectExecutorFixture(t, "custom")
	if _, err := executor.Execute(context.Background(), input); !errors.Is(err, ErrOwnershipConflict) {
		t.Fatalf("Execute error = %v, want ErrOwnershipConflict", err)
	}
}

func TestEjectExecutorAcceptsSelectedFileDriftWithoutWriting(t *testing.T) {
	executor, input, sourceManifest, sourceLock := ejectExecutorFixture(t, "generated")
	selected := filepath.Join(input.TargetRoot, filepath.FromSlash(input.Paths[0]))
	drifted := []byte("product-owned edit\n")
	if err := os.WriteFile(selected, drifted, 0o644); err != nil {
		t.Fatal(err)
	}
	outcome, err := executor.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(selected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, drifted) {
		t.Fatal("eject changed drifted file")
	}
	var manifest assemblyManifestDocument
	var lock generatedLockDocument
	if err := jsonUnmarshalStrict(outcome.AssemblyManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := jsonUnmarshalStrict(outcome.GeneratedLock, &lock); err != nil {
		t.Fatal(err)
	}
	currentChecksum := digestBytes(drifted)
	if manifest.Outputs[0].Ownership != "forked" || !digestEqual(manifest.Outputs[0].SHA256, currentChecksum) ||
		lock.Files[0].Ownership != "forked" || lock.Files[0].UpdatePolicy != "diff_only" || !digestEqual(lock.Files[0].SHA256, currentChecksum) {
		t.Fatal("drifted successor facts do not describe the current forked file")
	}
	if !digestEqual(lock.Files[0].GeneratedSHA256, sourceLock.Files[0].GeneratedSHA256) ||
		lock.Files[0].SourceID != sourceLock.Files[0].SourceID || !digestEqual(manifest.Outputs[0].SourceSHA256, sourceManifest.Outputs[0].SourceSHA256) {
		t.Fatal("eject discarded the upstream generation baseline")
	}
	if !digestEqual(lock.AssemblyManifestChecksum, manifest.ManifestChecksum) ||
		!digestEqual(outcome.ManifestChecksum, manifest.ManifestChecksum) || !digestEqual(outcome.LockChecksum, lock.LockChecksum) {
		t.Fatal("drifted successor digest closure is broken")
	}
}

func TestEjectExecutorRejectsUnselectedArtifactMismatch(t *testing.T) {
	executor, input, _, sourceLock := ejectExecutorFixture(t, "generated")
	sourceLock.Files[1].SourcePath = "content/mismatched.ts.tmpl"
	lockRaw, _, err := marshalWithEmbeddedDigest(sourceLock, "lock_checksum")
	if err != nil {
		t.Fatal(err)
	}
	input.LockDocument = lockRaw
	if _, err := executor.Execute(context.Background(), input); !errors.Is(err, ErrPlanMismatch) {
		t.Fatalf("Execute error = %v, want ErrPlanMismatch", err)
	}
}

func TestEjectExecutorRejectsBrokenSourceDigestClosure(t *testing.T) {
	executor, input, _, sourceLock := ejectExecutorFixture(t, "generated")
	sourceLock.AssemblyManifestChecksum = digestBytes([]byte("different manifest"))
	lockRaw, _, err := marshalWithEmbeddedDigest(sourceLock, "lock_checksum")
	if err != nil {
		t.Fatal(err)
	}
	input.LockDocument = lockRaw
	if _, err := executor.Execute(context.Background(), input); !errors.Is(err, ErrPlanMismatch) {
		t.Fatalf("Execute error = %v, want ErrPlanMismatch", err)
	}
}

func TestEjectExecutorReplayIsDeterministic(t *testing.T) {
	executor, input, _, _ := ejectExecutorFixture(t, "integration")
	first, err := executor.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := executor.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("identical eject input did not produce an identical outcome")
	}
}

func ejectExecutorFixture(t *testing.T, selectedOwnership string) (*EjectExecutor, EjectExecutionInput, assemblyManifestDocument, generatedLockDocument) {
	t.Helper()
	registry := artifactTestRegistry(t)
	executor, err := NewEjectExecutor(registry)
	if err != nil {
		t.Fatal(err)
	}
	targetRoot := t.TempDir()
	selectedPath := "src/selected.ts"
	otherPath := "src/other.ts"
	selectedBody := []byte("export const selected = true;\n")
	otherBody := []byte("export const other = true;\n")
	for filePath, contents := range map[string][]byte{selectedPath: selectedBody, otherPath: otherBody} {
		fullPath := filepath.Join(targetRoot, filepath.FromSlash(filePath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, contents, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	selectedMerge := (*MergeSpec)(nil)
	selectedRenderStrategy := "strict_template"
	if selectedOwnership == "integration" {
		selectedMerge = &MergeSpec{Strategy: "generated_region_v1", RegionID: "account-client", CommentPrefix: "//"}
		selectedRenderStrategy = "generated_region"
	}
	selectedOutput := manifestOutput{
		Path: selectedPath, Ownership: selectedOwnership, SHA256: digestBytes(selectedBody), SourceID: "package.account",
		SourceVersion: "1.0.0", SourcePath: "content/selected.ts.tmpl", SourceSHA256: digestBytes([]byte("selected source")),
		RenderStrategy: selectedRenderStrategy, ContentType: "text", Merge: selectedMerge,
	}
	otherOutput := manifestOutput{
		Path: otherPath, Ownership: "generated", SHA256: digestBytes(otherBody), SourceID: "package.account",
		SourceVersion: "1.0.0", SourcePath: "content/other.ts.tmpl", SourceSHA256: digestBytes([]byte("other source")),
		RenderStrategy: "strict_template", ContentType: "text",
	}
	manifest := assemblyManifestDocument{
		SchemaVersion: "1.0.0", AssemblyID: "assembly.eject-source", RunID: "run.eject-source",
		Product:         ArtifactProduct{ProductID: "product.eject-test", OfficialTenantID: "tenant.official", Applications: []ArtifactApplication{{PlanApplicationID: "application.web", ApplicationID: "app.web"}}},
		Blueprint:       ArtifactBlueprint{BlueprintID: "bp_eject-test", Version: 1, Checksum: digestBytes([]byte("blueprint"))},
		CatalogChecksum: digestBytes([]byte("catalog")), Generator: Tool{GeneratorID: "generator.platform", Version: "1.0.0", Checksum: digestBytes([]byte("generator"))},
		Packages:  []manifestPackage{{PackageID: "package.account", Version: "1.0.0", Checksum: digestBytes([]byte("package"))}},
		Templates: []manifestTemplate{{TemplateID: "standard-a", Version: "1.0.0", Checksum: digestBytes([]byte("template"))}},
		SDKs:      []manifestSDK{}, Outputs: []manifestOutput{selectedOutput, otherOutput},
		Evidence:   []Evidence{{EvidenceID: "evidence.eject-contract", Type: "contract_report", Status: "passed", Path: "artifacts/eject/contract.json", SHA256: digestBytes([]byte("evidence"))}},
		SecretRefs: []SecretRef{}, CreatedAt: "2026-07-15T08:00:00Z", ManifestChecksum: digestBytes(nil),
	}
	manifestRaw, manifestChecksum, err := marshalWithEmbeddedDigest(manifest, "manifest_checksum")
	if err != nil {
		t.Fatal(err)
	}
	manifest.ManifestChecksum = manifestChecksum
	selectedPolicy := updatePolicy(selectedOwnership)
	selectedGeneratedSHA := digestBytes(selectedBody)
	if selectedOwnership == "custom" {
		selectedGeneratedSHA = ""
	}
	lock := generatedLockDocument{
		SchemaVersion: "1.0.0", LockID: "lock.eject-source", RunID: "run.eject-source", AssemblyManifestChecksum: manifestChecksum,
		BlueprintChecksum: manifest.Blueprint.Checksum, CatalogChecksum: manifest.CatalogChecksum,
		RollbackPointPath: "artifacts/assembly/assembly.eject-source/rollback-point.json", Generator: manifest.Generator,
		Packages:  []LockedDependency{{ID: "package.account", Version: "1.0.0", Checksum: manifest.Packages[0].Checksum}},
		Templates: []LockedDependency{{ID: "standard-a", Version: "1.0.0", Checksum: manifest.Templates[0].Checksum}}, SDKs: []LockedDependency{},
		Files: []LockedFile{
			{Path: selectedPath, Ownership: selectedOwnership, SHA256: selectedOutput.SHA256, GeneratedSHA256: selectedGeneratedSHA, SourceID: selectedOutput.SourceID, SourceVersion: selectedOutput.SourceVersion, SourcePath: selectedOutput.SourcePath, SourceSHA256: selectedOutput.SourceSHA256, RenderStrategy: selectedOutput.RenderStrategy, ContentType: selectedOutput.ContentType, Merge: selectedMerge, UpdatePolicy: selectedPolicy},
			{Path: otherPath, Ownership: "generated", SHA256: otherOutput.SHA256, GeneratedSHA256: otherOutput.SHA256, SourceID: otherOutput.SourceID, SourceVersion: otherOutput.SourceVersion, SourcePath: otherOutput.SourcePath, SourceSHA256: otherOutput.SourceSHA256, RenderStrategy: otherOutput.RenderStrategy, ContentType: otherOutput.ContentType, UpdatePolicy: "replace_generated"},
		},
		CreatedAt: "2026-07-15T08:00:00Z", LockChecksum: digestBytes(nil),
	}
	snapshot, err := InspectTarget(targetRoot, ProjectLock{Files: lock.Files})
	if err != nil {
		t.Fatal(err)
	}
	lock.TargetSnapshotChecksum = snapshot.Checksum
	lockRaw, lockChecksum, err := marshalWithEmbeddedDigest(lock, "lock_checksum")
	if err != nil {
		t.Fatal(err)
	}
	lock.LockChecksum = lockChecksum
	if err := registry.Validate("assembly-manifest", manifestRaw); err != nil {
		t.Fatalf("fixture manifest: %v", err)
	}
	if err := registry.Validate("generated-project-lock", lockRaw); err != nil {
		t.Fatalf("fixture lock: %v", err)
	}
	return executor, EjectExecutionInput{
		TargetRoot: targetRoot, ManifestDocument: manifestRaw, LockDocument: lockRaw,
		OperationID: "operation.eject-test", NewAssemblyID: "assembly.eject-successor", NewLockID: "lock.eject-successor",
		Paths: []string{selectedPath}, CreatedAt: "2026-07-16T09:30:00Z",
	}, manifest, lock
}
