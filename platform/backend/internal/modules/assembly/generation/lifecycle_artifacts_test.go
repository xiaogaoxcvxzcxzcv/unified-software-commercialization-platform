package generation

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactStorePublishesLifecycleDocumentsAtomicallyAndIdempotently(t *testing.T) {
	root := t.TempDir()
	store, err := NewArtifactStore(root)
	if err != nil {
		t.Fatalf("NewArtifactStore error = %v", err)
	}
	manifest := json.RawMessage(`{"assembly_id":"assembly.successor"}`)
	lock := json.RawMessage(`{"lock_id":"lock.successor"}`)
	if err := store.PublishLifecycleDocuments("assembly.successor", manifest, lock); err != nil {
		t.Fatalf("PublishLifecycleDocuments error = %v", err)
	}
	if err := store.PublishLifecycleDocuments("assembly.successor", manifest, lock); err != nil {
		t.Fatalf("idempotent PublishLifecycleDocuments error = %v", err)
	}
	manifestPath := filepath.Join(root, "artifacts", "assembly", "assembly.successor", "assembly-manifest.json")
	actual, err := os.ReadFile(manifestPath)
	if err != nil || string(actual) != string(manifest) {
		t.Fatalf("published manifest = %q, error = %v", actual, err)
	}
	if err := store.PublishLifecycleDocuments("assembly.successor", json.RawMessage(`{"different":true}`), lock); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("conflicting retry error = %v, want ErrArtifactConflict", err)
	}
}

func TestArtifactStoreRejectsUnsafeLifecycleArtifactIdentity(t *testing.T) {
	store, err := NewArtifactStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewArtifactStore error = %v", err)
	}
	if err := store.PublishLifecycleDocuments("../escape", json.RawMessage(`{}`), json.RawMessage(`{}`)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("PublishLifecycleDocuments error = %v, want ErrInvalidInput", err)
	}
}
