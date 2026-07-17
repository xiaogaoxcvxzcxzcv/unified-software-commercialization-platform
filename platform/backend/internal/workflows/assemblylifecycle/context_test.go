package assemblylifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/assembly/generation"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecatalog"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type lifecycleContextRepositoryFake struct {
	rootManifest core.Manifest
	manifest     core.Manifest
	lock         core.GeneratedProjectLock
	run          core.Run
	plan         core.Plan
	blueprint    core.Blueprint
}

func (r lifecycleContextRepositoryFake) GetLifecycleSource(context.Context, string) (core.Manifest, core.GeneratedProjectLock, error) {
	return r.manifest, r.lock, nil
}
func (r lifecycleContextRepositoryFake) GetManifest(context.Context, string, string) (core.Manifest, error) {
	return r.rootManifest, nil
}
func (r lifecycleContextRepositoryFake) GetRun(context.Context, string, string) (core.Run, error) {
	return r.run, nil
}
func (r lifecycleContextRepositoryFake) GetPlan(context.Context, string, string) (core.Plan, error) {
	return r.plan, nil
}
func (r lifecycleContextRepositoryFake) GetBlueprint(context.Context, string, string, int64) (core.Blueprint, error) {
	return r.blueprint, nil
}

type lifecyclePermissionCatalog struct{}

func (lifecyclePermissionCatalog) Version() string                            { return "1.0.0" }
func (lifecyclePermissionCatalog) Checksum() string                           { return testDigest("permissions") }
func (lifecyclePermissionCatalog) ValidateRequiredPermissions([]string) error { return nil }

func TestTrustedContextResolverUsesRootRunAndCurrentLifecycleHead(t *testing.T) {
	fixture := newContextFixture(t)
	resolved, err := fixture.resolver.Resolve(context.Background(), fixture.repository.rootManifest.AssemblyID, fixture.repository.manifest, fixture.repository.lock)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.RootAssemblyID != "assembly.root" || resolved.Manifest.AssemblyID != "assembly.current" || resolved.Run.RunID != "run.root" || resolved.CatalogScope != "ordinary" {
		t.Fatalf("resolved context = %#v", resolved)
	}
	if resolved.PreviousManifestPath != "artifacts/assembly/assembly.current/assembly-manifest.json" || resolved.TargetSnapshot.Checksum == "" || len(resolved.TargetSnapshot.Files) != 1 {
		t.Fatalf("resolved paths/snapshot = %#v", resolved)
	}

	expected := core.LifecycleArtifactState{ManifestID: resolved.Manifest.AssemblyID, ManifestChecksum: resolved.Manifest.ManifestSHA256, LockID: resolved.Lock.LockID, LockChecksum: resolved.Lock.LockSHA256, CatalogChecksum: resolved.ProjectLock.CatalogChecksum, TargetSnapshotChecksum: resolved.TargetSnapshot.Checksum}
	current, err := fixture.resolver.ResolveCurrent(context.Background(), resolved.RootAssemblyID, expected)
	if err != nil || current.Manifest.AssemblyID != resolved.Manifest.AssemblyID {
		t.Fatalf("ResolveCurrent() = %#v, %v", current, err)
	}
	expected.TargetSnapshotChecksum = testDigest("stale-target")
	if _, err := fixture.resolver.ResolveCurrent(context.Background(), resolved.RootAssemblyID, expected); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("stale target error = %v", err)
	}
	resumed, err := fixture.resolver.ResolveForResume(context.Background(), resolved.RootAssemblyID, expected)
	if err != nil || resumed.Manifest.AssemblyID != resolved.Manifest.AssemblyID {
		t.Fatalf("ResolveForResume() = %#v, %v", resumed, err)
	}
	expected.LockChecksum = testDigest("wrong-lock")
	if _, err := fixture.resolver.ResolveForResume(context.Background(), resolved.RootAssemblyID, expected); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("resume accepted wrong immutable source: %v", err)
	}
}

func TestTrustedContextResolverRejectsCatalogScopeWithoutConfiguredCatalog(t *testing.T) {
	fixture := newContextFixture(t)
	fixture.repository.plan.Document = replacePlanScope(t, fixture.repository.plan.Document, "experimental")
	fixture.repository.plan.PlanSHA256 = checksumWithoutField(t, fixture.repository.plan.Document, "plan_checksum")
	fixture.repository.run.PlanSHA256 = fixture.repository.plan.PlanSHA256
	fixture.repository.run.Document = canonicalJSON(t, map[string]any{"plan_checksum": fixture.repository.plan.PlanSHA256})
	fixture.repository.run.DocumentSHA256 = documentDigest(t, fixture.repository.run.Document)
	if _, err := fixture.resolver.Resolve(context.Background(), fixture.repository.rootManifest.AssemblyID, fixture.repository.manifest, fixture.repository.lock); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("missing experimental catalog error = %v", err)
	}
}

func TestTrustedContextResolverUsesPlanScopeWithHistoricalCatalogChecksum(t *testing.T) {
	fixture := newContextFixture(t)
	historicalChecksum := testDigest("historical-catalog")
	currentSnapshot, err := fixture.resolver.ordinary.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if equalDigest(historicalChecksum, currentSnapshot.SnapshotSHA256) {
		t.Fatal("test requires the historical and current catalog checksums to differ")
	}

	fixture.repository.rootManifest = rewriteManifestCatalogChecksum(t, fixture.repository.rootManifest, historicalChecksum)
	fixture.repository.manifest = rewriteManifestCatalogChecksum(t, fixture.repository.manifest, historicalChecksum)
	fixture.repository.lock = rewriteLockCatalogChecksum(t, fixture.repository.lock, fixture.repository.manifest.ManifestSHA256, historicalChecksum)
	fixture.repository.plan.Document = rewritePlanCatalogChecksum(t, fixture.repository.plan.Document, historicalChecksum)
	fixture.repository.plan.PlanSHA256 = checksumWithoutField(t, fixture.repository.plan.Document, "plan_checksum")
	fixture.repository.plan.CatalogSnapshotSHA256 = historicalChecksum
	fixture.repository.run.PlanSHA256 = fixture.repository.plan.PlanSHA256
	fixture.repository.run.Document = canonicalJSON(t, map[string]any{"plan_checksum": fixture.repository.plan.PlanSHA256})
	fixture.repository.run.DocumentSHA256 = documentDigest(t, fixture.repository.run.Document)

	resolved, err := fixture.resolver.Resolve(context.Background(), fixture.repository.rootManifest.AssemblyID, fixture.repository.manifest, fixture.repository.lock)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.CatalogScope != "ordinary" || resolved.Catalog != fixture.resolver.ordinary {
		t.Fatalf("resolved catalog scope = %q, catalog = %p", resolved.CatalogScope, resolved.Catalog)
	}
}

type contextFixture struct {
	resolver   *TrustedContextResolver
	repository *lifecycleContextRepositoryFake
}

func newContextFixture(t *testing.T) contextFixture {
	t.Helper()
	targetRoot, artifactRoot := filepath.Join(t.TempDir(), "target"), filepath.Join(t.TempDir(), "artifacts")
	for _, root := range []string{targetRoot, artifactRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	managedPath := filepath.Join(targetRoot, "src", "generated", "app.txt")
	if err := os.MkdirAll(filepath.Dir(managedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("current\n")
	if err := os.WriteFile(managedPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	registry, err := machinecontract.LoadDirectory(filepath.Join("..", "..", "..", "..", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := machinecatalog.NewBlockCatalog("1.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	packageRoot, templateRoot := filepath.Join(t.TempDir(), "packages"), filepath.Join(t.TempDir(), "templates")
	if err := os.MkdirAll(packageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(templateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	catalog, err := machinecatalog.LoadOrdinary(packageRoot, templateRoot, registry, lifecyclePermissionCatalog{}, blocks)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	blueprintDocument := canonicalJSON(t, map[string]any{"blueprint_id": "blueprint.root", "schema_version": "1.0.0"})
	blueprintChecksum := documentDigest(t, blueprintDocument)
	planDocument := lifecycleTestPlanDocument(t, snapshot.SnapshotSHA256, "ordinary")
	planChecksum := checksumWithoutField(t, planDocument, "plan_checksum")
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	confirmedAt := now

	product := generation.ArtifactProduct{ProductID: "product.root", OfficialTenantID: "tenant.root", Applications: []generation.ArtifactApplication{{PlanApplicationID: "application.web", ApplicationID: "application.runtime"}}}
	rootManifest := testManifest(t, "assembly.root", "run.root", product, snapshot.SnapshotSHA256)
	currentManifest := testManifest(t, "assembly.current", "", product, snapshot.SnapshotSHA256)
	lockDocument := lifecycleTestLockDocument(t, "lock.current", currentManifest.ManifestSHA256, snapshot.SnapshotSHA256, "src/generated/app.txt", rawDigest(content))
	lock := core.GeneratedProjectLock{LockID: "lock.current", ProductID: product.ProductID, AssemblyID: currentManifest.AssemblyID, SchemaVersion: "1.0.0", Document: lockDocument, DocumentSHA256: documentDigest(t, lockDocument), LockSHA256: checksumWithoutField(t, lockDocument, "lock_checksum"), CreatedAt: now}
	runDocument := canonicalJSON(t, map[string]any{"plan_checksum": planChecksum})
	repository := lifecycleContextRepositoryFake{
		rootManifest: rootManifest, manifest: currentManifest, lock: lock,
		run:       core.Run{RunID: "run.root", ProductID: product.ProductID, PlanID: "plan.root", PlanVersion: 1, Version: 5, PlanSHA256: planChecksum, SchemaVersion: "1.0.0", Document: runDocument, DocumentSHA256: documentDigest(t, runDocument), OutputTargetRef: "workspace.root", Status: core.RunStatusCompleted, ManifestID: rootManifest.AssemblyID, LockID: "lock.root", CreatedAt: now, UpdatedAt: now, CompletedAt: &now},
		plan:      core.Plan{PlanID: "plan.root", ProductID: product.ProductID, BlueprintID: "blueprint.root", BlueprintRevision: 1, Version: 1, Environment: "test", Document: planDocument, BlueprintSHA256: blueprintChecksum, CatalogSnapshotSHA256: snapshot.SnapshotSHA256, PlanSHA256: planChecksum, ConfirmedAt: &confirmedAt},
		blueprint: core.Blueprint{BlueprintID: "blueprint.root", ProductID: product.ProductID, Revision: 1, Document: blueprintDocument, ContentSHA256: blueprintChecksum},
	}
	workspaces, err := generation.NewWorkspaceCatalog([]generation.Workspace{{Reference: "workspace.root", TargetRoot: targetRoot, ArtifactRoot: artifactRoot}})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewTrustedContextResolver(&repository, workspaces, catalog, nil)
	if err != nil {
		t.Fatal(err)
	}
	return contextFixture{resolver: resolver, repository: &repository}
}

func testManifest(t *testing.T, assemblyID, runID string, product generation.ArtifactProduct, catalogChecksum string) core.Manifest {
	t.Helper()
	body := map[string]any{"schema_version": "1.0.0", "assembly_id": assemblyID, "product": product, "catalog_checksum": catalogChecksum, "manifest_checksum": emptyDigest}
	raw := canonicalJSON(t, body)
	checksum := checksumWithoutField(t, raw, "manifest_checksum")
	body["manifest_checksum"] = checksum
	raw = canonicalJSON(t, body)
	return core.Manifest{AssemblyID: assemblyID, ProductID: product.ProductID, RunID: runID, SchemaVersion: "1.0.0", Document: raw, DocumentSHA256: documentDigest(t, raw), ManifestSHA256: checksum, CreatedAt: time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)}
}

func lifecycleTestLockDocument(t *testing.T, lockID, manifestChecksum, catalogChecksum, managedPath, managedChecksum string) json.RawMessage {
	t.Helper()
	body := map[string]any{"schema_version": "1.0.0", "lock_id": lockID, "assembly_manifest_checksum": manifestChecksum, "blueprint_checksum": testDigest("blueprint"), "catalog_checksum": catalogChecksum, "target_snapshot_checksum": testDigest("snapshot"), "rollback_point_path": "artifacts/rollback/root.json", "generator": map[string]any{"generator_id": "generator.root", "version": "1.0.0", "checksum": testDigest("generator")}, "packages": []any{}, "templates": []any{}, "sdks": []any{}, "files": []any{map[string]any{"path": managedPath, "ownership": "generated", "sha256": managedChecksum, "generated_sha256": managedChecksum, "source_id": "template.root", "source_version": "1.0.0", "source_path": "template/app.txt", "source_sha256": testDigest("source"), "render_strategy": "strict_template", "content_type": "text", "update_policy": "replace_generated"}}, "created_at": "2026-07-16T08:00:00Z", "lock_checksum": emptyDigest}
	raw := canonicalJSON(t, body)
	body["lock_checksum"] = checksumWithoutField(t, raw, "lock_checksum")
	return canonicalJSON(t, body)
}

func lifecycleTestPlanDocument(t *testing.T, catalogChecksum, scope string) json.RawMessage {
	t.Helper()
	body := map[string]any{"catalog_snapshot": map[string]any{"scope": scope, "checksum": catalogChecksum}, "plan_checksum": emptyDigest}
	raw := canonicalJSON(t, body)
	body["plan_checksum"] = checksumWithoutField(t, raw, "plan_checksum")
	return canonicalJSON(t, body)
}

func replacePlanScope(t *testing.T, raw json.RawMessage, scope string) json.RawMessage {
	t.Helper()
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		t.Fatal("decode plan")
	}
	body["catalog_snapshot"].(map[string]any)["scope"] = scope
	body["plan_checksum"] = emptyDigest
	next := canonicalJSON(t, body)
	body["plan_checksum"] = checksumWithoutField(t, next, "plan_checksum")
	return canonicalJSON(t, body)
}

func rewriteManifestCatalogChecksum(t *testing.T, manifest core.Manifest, catalogChecksum string) core.Manifest {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(manifest.Document, &body); err != nil {
		t.Fatal(err)
	}
	body["catalog_checksum"] = catalogChecksum
	body["manifest_checksum"] = emptyDigest
	raw := canonicalJSON(t, body)
	body["manifest_checksum"] = checksumWithoutField(t, raw, "manifest_checksum")
	manifest.Document = canonicalJSON(t, body)
	manifest.ManifestSHA256 = body["manifest_checksum"].(string)
	manifest.DocumentSHA256 = documentDigest(t, manifest.Document)
	return manifest
}

func rewriteLockCatalogChecksum(t *testing.T, lock core.GeneratedProjectLock, manifestChecksum, catalogChecksum string) core.GeneratedProjectLock {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(lock.Document, &body); err != nil {
		t.Fatal(err)
	}
	body["assembly_manifest_checksum"] = manifestChecksum
	body["catalog_checksum"] = catalogChecksum
	body["lock_checksum"] = emptyDigest
	raw := canonicalJSON(t, body)
	body["lock_checksum"] = checksumWithoutField(t, raw, "lock_checksum")
	lock.Document = canonicalJSON(t, body)
	lock.LockSHA256 = body["lock_checksum"].(string)
	lock.DocumentSHA256 = documentDigest(t, lock.Document)
	return lock
}

func rewritePlanCatalogChecksum(t *testing.T, raw json.RawMessage, catalogChecksum string) json.RawMessage {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	body["catalog_snapshot"].(map[string]any)["checksum"] = catalogChecksum
	body["plan_checksum"] = emptyDigest
	next := canonicalJSON(t, body)
	body["plan_checksum"] = checksumWithoutField(t, next, "plan_checksum")
	return canonicalJSON(t, body)
}

func canonicalJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = machinecontract.Canonicalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func checksumWithoutField(t *testing.T, raw json.RawMessage, field string) string {
	t.Helper()
	value, err := machinecontract.DigestWithoutTopLevelField(raw, field)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func documentDigest(t *testing.T, value []byte) string {
	t.Helper()
	digest, err := machinecontract.Digest(value)
	if err != nil {
		t.Fatal(err)
	}
	return "sha256:" + digest
}
func testDigest(value string) string {
	raw, _ := json.Marshal(value)
	digest, _ := machinecontract.Digest(raw)
	return "sha256:" + digest
}
func rawDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:])
}
