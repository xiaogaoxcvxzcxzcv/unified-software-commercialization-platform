package generation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInspectTargetIsDeterministicAndRejectsLinks(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "z/custom.txt", []byte("z\n"))
	writeTestFile(t, root, "a/custom.txt", []byte("a\n"))
	first, err := InspectTarget(root, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	changedTime := time.Date(2035, 2, 3, 4, 5, 6, 0, time.FixedZone("test-zone", 9*60*60))
	if err := os.Chtimes(filepath.Join(root, "a", "custom.txt"), changedTime, changedTime); err != nil {
		t.Fatal(err)
	}
	second, err := InspectTarget(root, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Checksum != second.Checksum || len(first.Files) != 2 || first.Files[0].Path != "a/custom.txt" {
		t.Fatalf("deterministic snapshots = %#v / %#v", first, second)
	}

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation is unavailable in this test environment: %v", err)
	}
	if _, err := InspectTarget(root, ProjectLock{}); !errors.Is(err, ErrTargetUnsafe) {
		t.Fatalf("InspectTarget() link error = %v", err)
	}
}

func TestPrepareAndCommitPreservesCustomAndRepeatsIdempotently(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "src/custom/workbench.ts", []byte("export const custom = true;\n"))
	generated := testRenderedFile(OutputSpec{
		Path: "src/generated/product.ts", Ownership: "generated", SourceID: "package.account", SourceVersion: "1.0.0",
		SourcePath: "content/product.ts.tmpl", SourceSHA256: rawDigest([]byte("source-generated")), RenderStrategy: "strict_template", ContentType: "text",
	}, []byte("export const product = \"demo\";\n"))
	merge := &MergeSpec{Strategy: "generated_region_v1", RegionID: "routes", CommentPrefix: "//"}
	integration := testRenderedFile(OutputSpec{
		Path: "src/integration/routes.ts", Ownership: "integration", SourceID: "template.standard", SourceVersion: "1.0.0",
		SourcePath: "content/routes.ts.tmpl", SourceSHA256: rawDigest([]byte("source-integration")), RenderStrategy: "generated_region", ContentType: "text", Merge: merge,
	}, []byte("export const generatedRoute = \"/account\";\n"))

	initial, err := InspectTarget(root, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	request := testFileRequest(initial, []RenderedFile{generated, integration})
	request.ProtectedPaths = []string{"src/custom/workbench.ts"}
	prepared, err := PrepareTarget(root, request, Result{Files: []RenderedFile{integration, generated}}, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Changes) != 2 || len(prepared.Preserved) != 1 || prepared.Preserved[0].Path != "src/custom/workbench.ts" {
		t.Fatalf("prepared = %#v", prepared)
	}
	committed, err := NewFileCommitter().Commit(context.Background(), root, request, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if !committed.AtomicCommitCompleted || !committed.StagingCleanupCompleted || committed.RollbackAttempted {
		t.Fatalf("commit = %#v", committed)
	}
	assertTestFile(t, root, "src/custom/workbench.ts", "export const custom = true;\n")
	assertTestFile(t, root, generated.Path, string(generated.Bytes))
	integrationContent := readTestFile(t, root, integration.Path)
	if !strings.Contains(integrationContent, "// <platform-generated:routes>\n") || !strings.Contains(integrationContent, "// </platform-generated:routes>\n") {
		t.Fatalf("integration content = %q", integrationContent)
	}

	lock := lockFromChanges(prepared.Changes, prepared.Preserved, map[string]OutputSpec{
		generated.Path: generated.OutputSpec, integration.Path: integration.OutputSpec,
	})
	repeatedSnapshot, err := InspectTarget(root, lock)
	if err != nil {
		t.Fatal(err)
	}
	repeatedRequest := testFileRequest(repeatedSnapshot, []RenderedFile{integration, generated})
	repeatedRequest.ProtectedPaths = request.ProtectedPaths
	repeated, err := PrepareTarget(root, repeatedRequest, Result{Files: []RenderedFile{generated, integration}}, lock)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range repeated.Changes {
		if change.Action != "unchanged" {
			t.Fatalf("repeat action for %s = %s", change.Path, change.Action)
		}
	}
	repeatedCommit, err := NewFileCommitter().Commit(context.Background(), root, repeatedRequest, repeated)
	if err != nil {
		t.Fatal(err)
	}
	if !repeatedCommit.TargetUnchanged || !repeatedCommit.AtomicCommitCompleted {
		t.Fatalf("repeat commit = %#v", repeatedCommit)
	}
}

func TestPrepareRejectsModifiedGeneratedAndCustomCollisionWithoutWriting(t *testing.T) {
	root := t.TempDir()
	output := OutputSpec{
		Path: "src/generated/account.ts", Ownership: "generated", SourceID: "package.account", SourceVersion: "1.0.0",
		SourcePath: "content/account.ts.tmpl", SourceSHA256: rawDigest([]byte("source")), RenderStrategy: "strict_template", ContentType: "text",
	}
	oldContent := []byte("export const version = 1;\n")
	writeTestFile(t, root, output.Path, oldContent)
	lock := ProjectLock{Files: []LockedFile{{
		Path: output.Path, Ownership: "generated", SHA256: rawDigest(oldContent), GeneratedSHA256: rawDigest(oldContent),
		SourceID: output.SourceID, SourceVersion: output.SourceVersion, SourcePath: output.SourcePath, SourceSHA256: output.SourceSHA256,
		RenderStrategy: output.RenderStrategy, ContentType: output.ContentType, UpdatePolicy: "replace_generated",
	}}}
	writeTestFile(t, root, output.Path, []byte("export const manuallyChanged = true;\n"))
	snapshot, err := InspectTarget(root, lock)
	if err != nil {
		t.Fatal(err)
	}
	rendered := testRenderedFile(output, []byte("export const version = 2;\n"))
	request := testFileRequest(snapshot, []RenderedFile{rendered})
	prepared, err := PrepareTarget(root, request, Result{Files: []RenderedFile{rendered}}, lock)
	if !errors.Is(err, ErrGeneratedModified) || len(prepared.Diagnostics) != 1 || prepared.Diagnostics[0].Code != "GENERATOR_GENERATED_MODIFIED" {
		t.Fatalf("PrepareTarget() prepared=%#v err=%v", prepared, err)
	}
	assertTestFile(t, root, output.Path, "export const manuallyChanged = true;\n")

	customRoot := t.TempDir()
	writeTestFile(t, customRoot, output.Path, []byte("product-owned\n"))
	customSnapshot, err := InspectTarget(customRoot, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	customRequest := testFileRequest(customSnapshot, []RenderedFile{rendered})
	customPrepared, err := PrepareTarget(customRoot, customRequest, Result{Files: []RenderedFile{rendered}}, ProjectLock{})
	if !errors.Is(err, ErrOwnershipConflict) || len(customPrepared.Diagnostics) != 1 || customPrepared.Diagnostics[0].Code != "GENERATOR_CUSTOM_COLLISION" {
		t.Fatalf("custom collision prepared=%#v err=%v", customPrepared, err)
	}
	assertTestFile(t, customRoot, output.Path, "product-owned\n")
}

func TestIntegrationMergePreservesProductContentAndRejectsRegionEdits(t *testing.T) {
	root := t.TempDir()
	merge := &MergeSpec{Strategy: "generated_region_v1", RegionID: "navigation", CommentPrefix: "//"}
	output := OutputSpec{
		Path: "src/integration/navigation.ts", Ownership: "integration", SourceID: "template.standard", SourceVersion: "1.0.0",
		SourcePath: "content/navigation.ts.tmpl", SourceSHA256: rawDigest([]byte("source")), RenderStrategy: "generated_region", ContentType: "text", Merge: merge,
	}
	firstRendered := testRenderedFile(output, []byte("export const account = \"/account\";\n"))
	initial, err := InspectTarget(root, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	firstRequest := testFileRequest(initial, []RenderedFile{firstRendered})
	firstPrepared, err := PrepareTarget(root, firstRequest, Result{Files: []RenderedFile{firstRendered}}, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileCommitter().Commit(context.Background(), root, firstRequest, firstPrepared); err != nil {
		t.Fatal(err)
	}
	lock := lockFromChanges(firstPrepared.Changes, nil, map[string]OutputSpec{output.Path: output})
	current := readTestFile(t, root, output.Path)
	writeTestFile(t, root, output.Path, []byte("// product-owned header\n"+current+"// product-owned footer\n"))
	snapshot, err := InspectTarget(root, lock)
	if err != nil {
		t.Fatal(err)
	}
	updatedRendered := testRenderedFile(output, []byte("export const account = \"/profile\";\n"))
	request := testFileRequest(snapshot, []RenderedFile{updatedRendered})
	prepared, err := PrepareTarget(root, request, Result{Files: []RenderedFile{updatedRendered}}, lock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileCommitter().Commit(context.Background(), root, request, prepared); err != nil {
		t.Fatal(err)
	}
	merged := readTestFile(t, root, output.Path)
	if !strings.HasPrefix(merged, "// product-owned header\n") || !strings.HasSuffix(merged, "// product-owned footer\n") || !strings.Contains(merged, "\"/profile\"") {
		t.Fatalf("merged integration = %q", merged)
	}

	updatedLock := lockFromChanges(prepared.Changes, nil, map[string]OutputSpec{output.Path: output})
	tampered := strings.Replace(merged, "\"/profile\"", "\"/tampered\"", 1)
	writeTestFile(t, root, output.Path, []byte(tampered))
	tamperedSnapshot, err := InspectTarget(root, updatedLock)
	if err != nil {
		t.Fatal(err)
	}
	tamperedRequest := testFileRequest(tamperedSnapshot, []RenderedFile{updatedRendered})
	tamperedPrepared, err := PrepareTarget(root, tamperedRequest, Result{Files: []RenderedFile{updatedRendered}}, updatedLock)
	if !errors.Is(err, ErrIntegrationRegion) || len(tamperedPrepared.Diagnostics) != 1 || tamperedPrepared.Diagnostics[0].Code != "GENERATOR_INTEGRATION_REGION_MODIFIED" {
		t.Fatalf("tampered region prepared=%#v err=%v", tamperedPrepared, err)
	}
	assertTestFile(t, root, output.Path, tampered)
}

func TestCommitFailureRollsBackEveryAppliedFile(t *testing.T) {
	root := t.TempDir()
	created := OutputSpec{Path: "aaa/generated/new.ts", Ownership: "generated", SourceID: "package.account", SourceVersion: "1.0.0", SourcePath: "content/new.tmpl", SourceSHA256: rawDigest([]byte("new-source")), RenderStrategy: "strict_template", ContentType: "text"}
	first := OutputSpec{Path: "src/generated/a.ts", Ownership: "generated", SourceID: "package.account", SourceVersion: "1.0.0", SourcePath: "content/a.tmpl", SourceSHA256: rawDigest([]byte("a-source")), RenderStrategy: "strict_template", ContentType: "text"}
	second := OutputSpec{Path: "src/generated/b.ts", Ownership: "generated", SourceID: "package.account", SourceVersion: "1.0.0", SourcePath: "content/b.tmpl", SourceSHA256: rawDigest([]byte("b-source")), RenderStrategy: "strict_template", ContentType: "text"}
	oldFirst, oldSecond := []byte("old-a\n"), []byte("old-b\n")
	writeTestFile(t, root, first.Path, oldFirst)
	writeTestFile(t, root, second.Path, oldSecond)
	lock := ProjectLock{Files: []LockedFile{
		lockedGeneratedFile(first, oldFirst), lockedGeneratedFile(second, oldSecond),
	}}
	snapshot, err := InspectTarget(root, lock)
	if err != nil {
		t.Fatal(err)
	}
	rendered := []RenderedFile{testRenderedFile(created, []byte("created\n")), testRenderedFile(first, []byte("new-a\n")), testRenderedFile(second, []byte("new-b\n"))}
	request := testFileRequest(snapshot, rendered)
	prepared, err := PrepareTarget(root, request, Result{Files: rendered}, lock)
	if err != nil {
		t.Fatal(err)
	}
	committer := NewFileCommitter()
	committer.beforeReplace = func(index int, _ FileChange) error {
		if index == 2 {
			return errors.New("injected commit failure")
		}
		return nil
	}
	result, err := committer.Commit(context.Background(), root, request, prepared)
	if !errors.Is(err, ErrCommitFailed) {
		t.Fatalf("Commit() error = %v", err)
	}
	if !result.RollbackAttempted || !result.RollbackCompleted || !result.TargetUnchanged || !result.StagingCleanupCompleted || result.AtomicCommitCompleted {
		t.Fatalf("rollback result = %#v", result)
	}
	assertTestFile(t, root, first.Path, string(oldFirst))
	assertTestFile(t, root, second.Path, string(oldSecond))
	if _, statErr := os.Stat(filepath.Join(root, "aaa")); !os.IsNotExist(statErr) {
		t.Fatalf("created target directories remained after rollback: %v", statErr)
	}
}

func TestCommitRejectsSnapshotDriftBeforeCreatingStaging(t *testing.T) {
	root := t.TempDir()
	output := OutputSpec{Path: "src/generated/a.ts", Ownership: "generated", SourceID: "package.account", SourceVersion: "1.0.0", SourcePath: "content/a.tmpl", SourceSHA256: rawDigest([]byte("source")), RenderStrategy: "strict_template", ContentType: "text"}
	rendered := testRenderedFile(output, []byte("new-a\n"))
	snapshot, err := InspectTarget(root, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	request := testFileRequest(snapshot, []RenderedFile{rendered})
	prepared, err := PrepareTarget(root, request, Result{Files: []RenderedFile{rendered}}, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "src/custom/late.ts", []byte("late change\n"))
	result, err := NewFileCommitter().Commit(context.Background(), root, request, prepared)
	if !errors.Is(err, ErrTargetChanged) || result.AtomicCommitCompleted {
		t.Fatalf("Commit() result=%#v err=%v", result, err)
	}
	if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(request.StagingPath))); !os.IsNotExist(statErr) {
		t.Fatalf("staging path exists after pre-commit rejection: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(output.Path))); !os.IsNotExist(statErr) {
		t.Fatalf("generated output exists after pre-commit rejection: %v", statErr)
	}
}

func testFileRequest(snapshot TargetSnapshot, rendered []RenderedFile) Request {
	outputs := make([]OutputSpec, 0, len(rendered))
	for _, file := range rendered {
		outputs = append(outputs, file.OutputSpec)
	}
	return Request{
		SchemaVersion: "1.0.0", RequestID: "request.test", Operation: "generate", WorkspaceRef: "workspace.test",
		PlanChecksum: rawDigest([]byte("plan")), TargetSnapshotChecksum: snapshot.Checksum,
		Generator:      Tool{GeneratorID: "platform.generator", Version: "1.0.0", Checksum: rawDigest([]byte("generator"))},
		Inputs:         InputPaths{BlueprintPath: "contracts/blueprint.json", PlanPath: "contracts/plan.json"},
		DesiredOutputs: outputs, ExistingFiles: append([]ExistingFile{}, snapshot.Files...), ProtectedPaths: []string{}, SecretRefs: []SecretRef{},
		StagingPath: ".runtime/generator/request-test", RollbackPointPath: "artifacts/rollback/request-test.json", ConflictPolicy: "stop",
		Determinism: Determinism{Timezone: "UTC", Locale: "C", SortOrder: "bytewise"},
	}
}

func testRenderedFile(output OutputSpec, content []byte) RenderedFile {
	digest := rawDigest(content)
	return RenderedFile{OutputSpec: output, Bytes: append([]byte(nil), content...), SHA256: digest, GeneratedSHA256: digest, SourceManifestSHA256: rawDigest([]byte(output.SourceID + output.SourceVersion))}
}

func lockFromChanges(changes []FileChange, preserved []ExistingFile, outputs map[string]OutputSpec) ProjectLock {
	lock := ProjectLock{SchemaVersion: "1.0.0"}
	for _, change := range changes {
		output := outputs[change.Path]
		lock.Files = append(lock.Files, LockedFile{
			Path: change.Path, Ownership: change.Ownership, SHA256: change.SHA256, GeneratedSHA256: change.GeneratedSHA256,
			SourceID: output.SourceID, SourceVersion: output.SourceVersion, SourcePath: output.SourcePath, SourceSHA256: output.SourceSHA256,
			RenderStrategy: output.RenderStrategy, ContentType: output.ContentType, Merge: output.Merge, UpdatePolicy: updatePolicy(change.Ownership),
		})
	}
	for _, file := range preserved {
		lock.Files = append(lock.Files, LockedFile{Path: file.Path, Ownership: file.Ownership, SHA256: file.SHA256, UpdatePolicy: updatePolicy(file.Ownership)})
	}
	return lock
}

func lockedGeneratedFile(output OutputSpec, content []byte) LockedFile {
	digest := rawDigest(content)
	return LockedFile{
		Path: output.Path, Ownership: "generated", SHA256: digest, GeneratedSHA256: digest,
		SourceID: output.SourceID, SourceVersion: output.SourceVersion, SourcePath: output.SourcePath, SourceSHA256: output.SourceSHA256,
		RenderStrategy: output.RenderStrategy, ContentType: output.ContentType, UpdatePolicy: "replace_generated",
	}
}

func writeTestFile(t *testing.T, root, relative string, content []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, root, relative string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func assertTestFile(t *testing.T, root, relative, expected string) {
	t.Helper()
	if actual := readTestFile(t, root, relative); actual != expected {
		t.Fatalf("%s = %q, want %q", relative, actual, expected)
	}
}
