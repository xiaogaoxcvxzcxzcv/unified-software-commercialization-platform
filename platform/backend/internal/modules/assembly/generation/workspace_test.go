package generation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceCatalogResolvesOnlyConfiguredSeparateRoots(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	artifacts := filepath.Join(root, "artifacts")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewWorkspaceCatalog([]Workspace{{Reference: "workspace.default", TargetRoot: target, ArtifactRoot: artifacts}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := catalog.Resolve("workspace.default")
	if err != nil || resolved.TargetRoot != target || resolved.ArtifactRoot != artifacts {
		t.Fatalf("resolved = %#v, %v", resolved, err)
	}
	if _, err := catalog.Resolve("workspace.untrusted"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("untrusted reference error = %v", err)
	}
	if _, err := NewWorkspaceCatalog([]Workspace{{Reference: "workspace.overlap", TargetRoot: root, ArtifactRoot: artifacts}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("overlapping roots error = %v", err)
	}
}
