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
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

func TestCatalogLifecyclePlanBuilderCreatesSchemaValidEjectAndRevalidatesSnapshot(t *testing.T) {
	fixture := newContextFixture(t)
	now := time.Date(2026, 7, 16, 9, 0, 0, 123456789, time.UTC)
	builder, err := NewCatalogLifecyclePlanBuilder(fixture.resolver, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	document, err := builder.BuildEjectPlan(context.Background(), "assembly.root", fixture.repository.manifest, fixture.repository.lock, []string{"src/generated/app.txt"})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := machinecontract.LoadDirectory(filepath.Join("..", "..", "..", "..", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate("assembly-lifecycle-plan", document); err != nil {
		t.Fatalf("lifecycle plan schema: %v\n%s", err, document)
	}
	plan, err := core.ProjectLifecyclePlanDocument(document)
	if err != nil || plan.Operation != core.LifecycleEject || !plan.Executable || len(plan.Changes) != 1 || plan.Changes[0].Ownership != "forked" {
		t.Fatalf("projected plan = %#v, %v", plan, err)
	}
	// PostgreSQL timestamps round-trip at microsecond precision. Revalidation
	// must remain byte-stable after the persisted plan is read back.
	plan.CreatedAt = plan.CreatedAt.Truncate(time.Microsecond)
	if err := builder.Revalidate(context.Background(), plan, fixture.repository.manifest, fixture.repository.lock); err != nil {
		t.Fatalf("Revalidate() = %v", err)
	}
	managed := filepath.Join(fixture.resolver.workspacesRootForTest(t), "src", "generated", "app.txt")
	if err := os.WriteFile(managed, []byte("operator drift\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := builder.Revalidate(context.Background(), plan, fixture.repository.manifest, fixture.repository.lock); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("drift revalidation error = %v", err)
	}
}

func TestCatalogLifecyclePlanBuilderRejectsAmbiguousSDKTargetBeforeDryRun(t *testing.T) {
	fixture := newContextFixture(t)
	builder, err := NewCatalogLifecyclePlanBuilder(fixture.resolver, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = builder.BuildUpgradePlan(context.Background(), "assembly.root", fixture.repository.manifest, fixture.repository.lock, core.LifecycleTargetVersions{
		Generator: core.LifecycleVersionRef{ID: "generator.root", Version: "1.0.0"},
		Templates: []core.LifecycleVersionRef{{ID: "template.root", Version: "1.0.0"}},
	})
	if !errors.Is(err, core.ErrDocumentInvalid) {
		t.Fatalf("ambiguous SDK error = %v", err)
	}
}

func TestMarshalLifecyclePlanBlocksManualMigrationAndManualRollback(t *testing.T) {
	registry, err := machinecontract.LoadDirectory(filepath.Join("..", "..", "..", "..", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	digestA := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tests := []struct {
		name             string
		migrations       []lifecycleMigrationDocument
		rollback         lifecycleRollbackDocument
		conflictCategory string
	}{
		{
			name:             "manual migration",
			migrations:       []lifecycleMigrationDocument{{MigrationID: "migration.account-v2", Kind: "database", Reversibility: "manual", Summary: "Requires an operator-owned data restore"}},
			rollback:         lifecycleRollbackDocument{Strategy: "restore_predecessor", Automatic: true, PredecessorManifestChecksum: digestA, PredecessorLockChecksum: digestB},
			conflictCategory: "migration",
		},
		{
			name:             "manual rollback strategy",
			migrations:       []lifecycleMigrationDocument{},
			rollback:         lifecycleRollbackDocument{Strategy: "manual", Automatic: false, PredecessorManifestChecksum: digestA, PredecessorLockChecksum: digestB},
			conflictCategory: "rollback",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, marshalErr := marshalLifecyclePlan(lifecyclePlanDocument{
				SchemaVersion: "1.0.0", LifecyclePlanID: "lifecycle.safety-test", AssemblyID: "assembly.safety-test", ProductID: "product.safety-test", Operation: "eject",
				Source:     lifecycleSourceDocument{ManifestID: "manifest.safety-test", ManifestChecksum: digestA, LockID: "lock.safety-test", LockChecksum: digestB, CatalogChecksum: digestA, TargetSnapshotChecksum: digestB},
				EjectPaths: []string{"generated/safety.txt"}, TargetSnapshotChecksum: digestA, Changes: []lifecycleChangeDocument{}, Migrations: test.migrations,
				RegressionTests: []string{"assembly.lifecycle.contract"}, Conflicts: []lifecycleConflictDocument{}, Rollback: test.rollback,
				Confirmation: lifecycleConfirmationDocument{Statements: []string{"Confirm lifecycle safety"}}, CreatedAt: time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC), PlanChecksum: emptyDigest,
			})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if validationErr := registry.Validate("assembly-lifecycle-plan", document); validationErr != nil {
				t.Fatalf("blocked plan violates schema: %v\n%s", validationErr, document)
			}
			var projected lifecyclePlanDocument
			if err := json.Unmarshal(document, &projected); err != nil {
				t.Fatal(err)
			}
			if projected.Executable || projected.BlockingConflictCount < 1 {
				t.Fatalf("unsafe plan remained executable: %+v", projected)
			}
			found := false
			for _, conflict := range projected.Conflicts {
				found = found || (conflict.Blocking && conflict.Category == test.conflictCategory)
			}
			if !found {
				t.Fatalf("missing blocking %s conflict: %+v", test.conflictCategory, projected.Conflicts)
			}
		})
	}
}

func (r *TrustedContextResolver) workspacesRootForTest(t *testing.T) string {
	t.Helper()
	workspace, err := r.workspaces.Resolve("workspace.root")
	if err != nil {
		t.Fatal(err)
	}
	return workspace.TargetRoot
}
