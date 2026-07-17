package assemblylifecycle

import (
	"context"
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
	now := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
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

func (r *TrustedContextResolver) workspacesRootForTest(t *testing.T) string {
	t.Helper()
	workspace, err := r.workspaces.Resolve("workspace.root")
	if err != nil {
		t.Fatal(err)
	}
	return workspace.TargetRoot
}
