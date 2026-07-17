package assemblylifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/assembly/generation"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

func TestReissueLifecycleArtifactsRejectsManagedDrift(t *testing.T) {
	registry := executorTestRegistry(t)
	for _, ownership := range []string{"generated", "integration"} {
		t.Run(ownership, func(t *testing.T) {
			files := []executorArtifactFile{{Path: "src/managed.ts", Ownership: ownership, Body: []byte("locked\n")}}
			manifest, lock := executorSourceDocuments(t, registry, files)
			snapshot := generation.TargetSnapshot{
				Files:    []generation.ExistingFile{{Path: files[0].Path, Ownership: ownership, SHA256: rawDigest([]byte("drifted\n"))}},
				Checksum: testDigest("drifted-snapshot-" + ownership),
			}

			_, _, err := reissueLifecycleArtifacts(manifest, lock, "operation.rollback", "assembly.rollback", "lock.rollback", snapshot, time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC), registry)
			if !errors.Is(err, generation.ErrOwnershipConflict) {
				t.Fatalf("reissueLifecycleArtifacts() error = %v, want ErrOwnershipConflict", err)
			}
		})
	}
}

func TestReissueLifecycleArtifactsPreservesCurrentProductOwnedSHAAndDigestClosure(t *testing.T) {
	registry := executorTestRegistry(t)
	files := []executorArtifactFile{
		{Path: "src/custom.ts", Ownership: "custom", Body: []byte("old custom\n")},
		{Path: "src/forked.ts", Ownership: "forked", Body: []byte("old fork\n")},
	}
	manifest, lock := executorSourceDocuments(t, registry, files)
	customSHA := rawDigest([]byte("current custom\n"))
	forkedSHA := rawDigest([]byte("current fork\n"))
	snapshot := generation.TargetSnapshot{
		Files: []generation.ExistingFile{
			{Path: files[0].Path, Ownership: "custom", SHA256: customSHA},
			{Path: files[1].Path, Ownership: "forked", SHA256: forkedSHA},
		},
		Checksum: testDigest("current-product-owned-snapshot"),
	}
	createdAt := time.Date(2026, 7, 16, 10, 30, 0, 0, time.UTC)

	manifestRaw, lockRaw, err := reissueLifecycleArtifacts(manifest, lock, "operation.rollback", "assembly.rollback", "lock.rollback", snapshot, createdAt, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate("assembly-manifest", manifestRaw); err != nil {
		t.Fatalf("successor manifest schema: %v", err)
	}
	if err := registry.Validate("generated-project-lock", lockRaw); err != nil {
		t.Fatalf("successor lock schema: %v", err)
	}
	var manifestDocument struct {
		Outputs []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(manifestRaw, &manifestDocument); err != nil {
		t.Fatal(err)
	}
	if got := manifestDocument.Outputs[0].SHA256; got != customSHA {
		t.Fatalf("custom manifest SHA = %q, want %q", got, customSHA)
	}
	if got := manifestDocument.Outputs[1].SHA256; got != forkedSHA {
		t.Fatalf("forked manifest SHA = %q, want %q", got, forkedSHA)
	}
	projectLock, err := generation.DecodeProjectLock(lockRaw)
	if err != nil {
		t.Fatal(err)
	}
	if got := projectLock.Files[0].SHA256; got != customSHA {
		t.Fatalf("custom SHA = %q, want %q", got, customSHA)
	}
	if got := projectLock.Files[1].SHA256; got != forkedSHA {
		t.Fatalf("forked SHA = %q, want %q", got, forkedSHA)
	}
	if projectLock.TargetSnapshotChecksum != snapshot.Checksum {
		t.Fatalf("target snapshot checksum = %q, want %q", projectLock.TargetSnapshotChecksum, snapshot.Checksum)
	}
	executor := &GenerationLifecycleExecutor{contracts: registry}
	target, err := executor.projectSuccessor(core.LifecycleOperation{OperationID: "operation.rollback"}, manifestRaw, lockRaw)
	if err != nil {
		t.Fatalf("projectSuccessor() rejected digest closure: %v", err)
	}
	if target.ManifestID != "assembly.rollback" || target.LockID != "lock.rollback" || target.TargetSnapshotChecksum != snapshot.Checksum {
		t.Fatalf("projected target = %#v", target)
	}
}

func TestExecuteRollbackPublishConflictReturnsNoCommittableTransition(t *testing.T) {
	fixture := newContextFixture(t)
	registry := executorTestRegistry(t)
	resolved, err := fixture.resolver.Resolve(context.Background(), fixture.repository.rootManifest.AssemblyID, fixture.repository.manifest, fixture.repository.lock)
	if err != nil {
		t.Fatal(err)
	}
	files := []executorArtifactFile{{Path: "src/generated/app.txt", Ownership: "generated", Body: []byte("current\n")}}
	sourceManifest, sourceLock := executorSourceDocuments(t, registry, files)
	now := time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC)
	operation := core.LifecycleOperation{
		OperationID: "operation.rollback-conflict", AssemblyID: fixture.repository.rootManifest.AssemblyID,
		ProductID: fixture.repository.manifest.ProductID, Kind: core.LifecycleRollback,
		Source: core.LifecycleArtifactState{
			ManifestID: resolved.Manifest.AssemblyID, ManifestChecksum: resolved.Manifest.ManifestSHA256,
			LockID: resolved.Lock.LockID, LockChecksum: resolved.Lock.LockSHA256,
			CatalogChecksum: resolved.ProjectLock.CatalogChecksum, TargetSnapshotChecksum: resolved.TargetSnapshot.Checksum,
		},
		CreatedAt: now, UpdatedAt: now,
	}
	journal, err := json.Marshal(rollbackJournalDocument{
		Kind: "eject", WorkspaceRef: resolved.Run.OutputTargetRef,
		SourceManifestDocument: sourceManifest, SourceLockDocument: sourceLock,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestID, _ := lifecycleArtifactIDs(operation.OperationID)
	conflictRoot := filepath.Join(resolved.Workspace.ArtifactRoot, "artifacts", "assembly", manifestID)
	if err := os.MkdirAll(conflictRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conflictRoot, "assembly-manifest.json"), []byte(`{"different":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conflictRoot, "generated-project-lock.json"), []byte(`{"different":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	executor, err := NewGenerationLifecycleExecutor(fixture.resolver, &CatalogLifecyclePlanBuilder{}, registry, func() time.Time { return now.Add(time.Minute) })
	if err != nil {
		t.Fatal(err)
	}

	result, err := executor.ExecuteRollback(context.Background(), operation, core.LifecycleArtifactTransition{
		OperationID: operation.OperationID, Source: operation.Source, RollbackJournal: journal, CreatedAt: now,
	})
	if !errors.Is(err, generation.ErrArtifactConflict) {
		t.Fatalf("ExecuteRollback() error = %v, want ErrArtifactConflict", err)
	}
	if errors.Is(err, ErrLifecycleFinalizeRetryable) {
		t.Fatalf("permanent artifact conflict was classified retryable: %v", err)
	}
	if result.Target != nil || result.Transition != nil || len(result.Diagnostics) != 0 || len(result.Reports) != 0 {
		t.Fatalf("publish conflict returned committable execution evidence: %#v", result)
	}
}

func TestPublishLifecycleDocumentsRetriesOnlyStoreFailure(t *testing.T) {
	if err := publishLifecycleDocuments(nil, "assembly.test", json.RawMessage(`{}`), json.RawMessage(`{}`)); !errors.Is(err, ErrLifecycleFinalizeRetryable) {
		t.Fatalf("store failure classification = %v", err)
	}
	store, err := generation.NewArtifactStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = publishLifecycleDocuments(store, "../unsafe", json.RawMessage(`{}`), json.RawMessage(`{}`))
	if !errors.Is(err, generation.ErrInvalidInput) || errors.Is(err, ErrLifecycleFinalizeRetryable) {
		t.Fatalf("invalid input classification = %v", err)
	}
}

type executorArtifactFile struct {
	Path      string
	Ownership string
	Body      []byte
}

func executorSourceDocuments(t *testing.T, registry *machinecontract.Registry, files []executorArtifactFile) (json.RawMessage, json.RawMessage) {
	t.Helper()
	outputs := make([]any, 0, len(files))
	lockedFiles := make([]any, 0, len(files))
	for _, file := range files {
		sha := rawDigest(file.Body)
		renderStrategy := "strict_template"
		updatePolicy := "replace_generated"
		output := map[string]any{
			"path": file.Path, "ownership": file.Ownership, "sha256": sha,
			"source_id": "template.executor", "source_version": "1.0.0", "source_path": "content/file.tmpl",
			"source_sha256": testDigest("source-" + file.Path), "render_strategy": renderStrategy, "content_type": "text",
		}
		locked := map[string]any{
			"path": file.Path, "ownership": file.Ownership, "sha256": sha,
			"source_id": "template.executor", "source_version": "1.0.0", "source_path": "content/file.tmpl",
			"source_sha256": testDigest("source-" + file.Path), "render_strategy": renderStrategy, "content_type": "text",
		}
		switch file.Ownership {
		case "generated":
			locked["generated_sha256"] = sha
		case "integration":
			updatePolicy = "merge_integration"
			renderStrategy = "generated_region"
			merge := map[string]any{"strategy": "generated_region_v1", "region_id": "executor-region", "comment_prefix": "//"}
			output["render_strategy"] = renderStrategy
			output["merge"] = merge
			locked["generated_sha256"] = sha
			locked["render_strategy"] = renderStrategy
			locked["merge"] = merge
		case "custom":
			updatePolicy = "preserve_custom"
		case "forked":
			updatePolicy = "diff_only"
			locked["generated_sha256"] = sha
		default:
			t.Fatalf("unsupported ownership %q", file.Ownership)
		}
		locked["update_policy"] = updatePolicy
		outputs = append(outputs, output)
		lockedFiles = append(lockedFiles, locked)
	}
	manifest := map[string]any{
		"schema_version": "1.0.0", "assembly_id": "assembly.predecessor", "run_id": "run.predecessor",
		"product":          map[string]any{"product_id": "product.root", "official_tenant_id": "tenant.root", "applications": []any{map[string]any{"plan_application_id": "application.web", "application_id": "application.runtime"}}},
		"blueprint":        map[string]any{"blueprint_id": "bp_executor", "version": 1, "checksum": testDigest("blueprint")},
		"catalog_checksum": testDigest("catalog"),
		"generator":        map[string]any{"generator_id": "generator.executor", "version": "1.0.0", "checksum": testDigest("generator")},
		"packages":         []any{}, "templates": []any{map[string]any{"template_id": "executor-template", "version": "1.0.0", "checksum": testDigest("template")}}, "sdks": []any{},
		"outputs":     outputs,
		"evidence":    []any{map[string]any{"evidence_id": "evidence.executor", "type": "test_report", "status": "passed", "path": "artifacts/executor/report.json", "sha256": testDigest("evidence")}},
		"secret_refs": []any{}, "created_at": "2026-07-16T09:00:00Z", "manifest_checksum": emptyDigest,
	}
	manifestRaw := canonicalJSON(t, manifest)
	manifest["manifest_checksum"] = checksumWithoutField(t, manifestRaw, "manifest_checksum")
	manifestRaw = canonicalJSON(t, manifest)
	lock := map[string]any{
		"schema_version": "1.0.0", "lock_id": "lock.predecessor", "run_id": "run.predecessor", "assembly_manifest_checksum": manifest["manifest_checksum"],
		"blueprint_checksum": testDigest("blueprint"), "catalog_checksum": testDigest("catalog"), "target_snapshot_checksum": testDigest("source-snapshot"),
		"rollback_point_path": "artifacts/assembly/assembly.predecessor/rollback-point.json",
		"generator":           map[string]any{"generator_id": "generator.executor", "version": "1.0.0", "checksum": testDigest("generator")},
		"packages":            []any{}, "templates": []any{map[string]any{"id": "executor-template", "version": "1.0.0", "checksum": testDigest("template")}}, "sdks": []any{},
		"files": lockedFiles, "created_at": "2026-07-16T09:00:00Z", "lock_checksum": emptyDigest,
	}
	lockRaw := canonicalJSON(t, lock)
	lock["lock_checksum"] = checksumWithoutField(t, lockRaw, "lock_checksum")
	lockRaw = canonicalJSON(t, lock)
	if err := registry.Validate("assembly-manifest", manifestRaw); err != nil {
		t.Fatalf("source manifest fixture: %v", err)
	}
	if err := registry.Validate("generated-project-lock", lockRaw); err != nil {
		t.Fatalf("source lock fixture: %v", err)
	}
	return manifestRaw, lockRaw
}

func executorTestRegistry(t *testing.T) *machinecontract.Registry {
	t.Helper()
	registry, err := machinecontract.LoadDirectory(filepath.Join("..", "..", "..", "..", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	return registry
}
