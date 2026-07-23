package g2a05acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	assemblymachine "platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

func TestAcceptancePlannerPlanMatchesCurrentAssemblySchema(t *testing.T) {
	root := contractRepositoryRoot(t)
	contracts, err := assemblymachine.LoadDirectory(filepath.Join(root, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	planned, err := acceptancePlanner{}.BuildPlan(context.Background(), core.Blueprint{BlueprintID: fixtureBlueprintID, Revision: 1}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("assembly-plan", planned.Document); err != nil {
		t.Fatal(err)
	}
	var value struct {
		CatalogSnapshot struct {
			Scope string `json:"scope"`
		} `json:"catalog_snapshot"`
	}
	if err := json.Unmarshal(planned.Document, &value); err != nil {
		t.Fatal(err)
	}
	if value.CatalogSnapshot.Scope != "ordinary" {
		t.Fatalf("acceptance fixture plan scope=%q, want ordinary CreatePlan path", value.CatalogSnapshot.Scope)
	}
}

func contractRepositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for current := workingDirectory; ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(current, "platform", "backend", "go.mod")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("repository root not found")
		}
	}
}
