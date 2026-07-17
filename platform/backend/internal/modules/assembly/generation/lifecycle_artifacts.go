package generation

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"path/filepath"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

// PublishLifecycleDocuments atomically publishes the immutable manifest and
// lock pair created by a lifecycle operation. A retry is idempotent only when
// both previously published documents are byte-identical.
func (s *ArtifactStore) PublishLifecycleDocuments(assemblyID string, manifest, lock json.RawMessage) error {
	if s == nil || s.root == "" || assemblyID == "" || len(manifest) == 0 || len(lock) == 0 {
		return ErrArtifactStore
	}
	finalRelative := path.Join("artifacts", "assembly", assemblyID)
	if path.Base(finalRelative) != assemblyID || machinecontract.ValidateSafeRelativePath(finalRelative) != nil {
		return ErrInvalidInput
	}
	manifestRelative := path.Join(finalRelative, "assembly-manifest.json")
	lockRelative := path.Join(finalRelative, "generated-project-lock.json")
	finalRoot := filepath.Join(s.root, filepath.FromSlash(finalRelative))
	if err := ensurePathInsideRoot(s.root, finalRoot); err != nil {
		return err
	}
	if _, err := os.Lstat(finalRoot); err == nil {
		actualManifest, manifestErr := readSafeWorkspaceFile(s.root, manifestRelative)
		actualLock, lockErr := readSafeWorkspaceFile(s.root, lockRelative)
		if manifestErr == nil && lockErr == nil && bytes.Equal(actualManifest, manifest) && bytes.Equal(actualLock, lock) {
			return nil
		}
		return ErrArtifactConflict
	} else if !os.IsNotExist(err) {
		return ErrArtifactStore
	}

	runtimeRoot := filepath.Join(s.root, ".runtime", "assembly")
	if err := createSafeDirectory(s.root, runtimeRoot, 0o700); err != nil {
		return err
	}
	stageRoot, err := os.MkdirTemp(runtimeRoot, ".lifecycle-")
	if err != nil {
		return ErrArtifactStore
	}
	defer os.RemoveAll(stageRoot)
	if err := writeNewFile(s.root, filepath.Join(stageRoot, "assembly-manifest.json"), manifest, 0o600); err != nil {
		return err
	}
	if err := writeNewFile(s.root, filepath.Join(stageRoot, "generated-project-lock.json"), lock, 0o600); err != nil {
		return err
	}
	if _, err := ensureSafeTargetParent(s.root, finalRoot); err != nil {
		return err
	}
	if err := os.Rename(stageRoot, finalRoot); err != nil {
		if _, statErr := os.Lstat(finalRoot); statErr == nil {
			actualManifest, manifestErr := readSafeWorkspaceFile(s.root, manifestRelative)
			actualLock, lockErr := readSafeWorkspaceFile(s.root, lockRelative)
			if manifestErr == nil && lockErr == nil && bytes.Equal(actualManifest, manifest) && bytes.Equal(actualLock, lock) {
				return nil
			}
		}
		return ErrArtifactConflict
	}
	return nil
}
