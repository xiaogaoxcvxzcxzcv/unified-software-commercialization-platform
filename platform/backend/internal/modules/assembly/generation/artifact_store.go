package generation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type ArtifactStore struct {
	root string
}

type ArtifactTransaction struct {
	store         *ArtifactStore
	request       Request
	stageRoot     string
	finalRoot     string
	finalRelative string
	published     bool
}

func NewArtifactStore(root string) (*ArtifactStore, error) {
	validated, err := validateWorkspaceRoot(root)
	if err != nil {
		return nil, err
	}
	return &ArtifactStore{root: validated}, nil
}

func (s *ArtifactStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *ArtifactStore) LoadPublished(request Request) (ArtifactBundle, bool, error) {
	if s == nil || s.root == "" {
		return ArtifactBundle{}, false, ErrArtifactStore
	}
	finalRoot := filepath.Join(s.root, filepath.FromSlash(path.Dir(request.ArtifactContext.Paths.AssemblyManifestPath)))
	if _, err := os.Lstat(finalRoot); os.IsNotExist(err) {
		return ArtifactBundle{}, false, nil
	} else if err != nil {
		return ArtifactBundle{}, false, ErrArtifactStore
	}
	read := func(relative string) ([]byte, error) { return readSafeWorkspaceFile(s.root, relative) }
	manifest, manifestErr := read(request.ArtifactContext.Paths.AssemblyManifestPath)
	lockRaw, lockErr := read(request.ArtifactContext.Paths.GeneratedLockPath)
	rollback, rollbackErr := read(request.ArtifactContext.Paths.RollbackPointPath)
	journal, journalErr := read(request.ArtifactContext.Paths.CommitJournalPath)
	result, resultErr := read(request.ArtifactContext.Paths.ResultPath)
	if errors.Join(manifestErr, lockErr, rollbackErr, journalErr, resultErr) != nil {
		return ArtifactBundle{}, true, ErrArtifactConflict
	}
	var journalDocument commitJournalDocument
	var resultDocument generatorResultDocument
	if jsonUnmarshalStrict(journal, &journalDocument) != nil || jsonUnmarshalStrict(result, &resultDocument) != nil || journalDocument.State != "committed" || resultDocument.Status != "succeeded" {
		return ArtifactBundle{}, true, ErrArtifactConflict
	}
	lock, err := DecodeProjectLock(lockRaw)
	if err != nil {
		return ArtifactBundle{}, true, ErrArtifactConflict
	}
	var manifestDocument assemblyManifestDocument
	if jsonUnmarshalStrict(manifest, &manifestDocument) != nil {
		return ArtifactBundle{}, true, ErrArtifactConflict
	}
	evidenceDocuments := make(map[string][]byte, len(manifestDocument.Evidence))
	for _, evidence := range manifestDocument.Evidence {
		content, err := read(evidence.Path)
		if err != nil || !digestEqual(digestBytes(content), evidence.SHA256) {
			return ArtifactBundle{}, true, ErrArtifactConflict
		}
		evidenceDocuments[evidence.Path] = content
	}
	files := make([]ExistingFile, 0, len(lock.Files))
	for _, file := range lock.Files {
		files = append(files, ExistingFile{Path: file.Path, Ownership: file.Ownership, SHA256: file.SHA256})
	}
	sortExistingFiles(files)
	return ArtifactBundle{
		AssemblyManifest: manifest, GeneratedLock: lockRaw, RollbackPoint: rollback, CommitJournal: journal,
		GeneratorResult: result, EvidenceDocuments: evidenceDocuments, FinalSnapshot: TargetSnapshot{Files: files, Checksum: lock.TargetSnapshotChecksum},
	}, true, nil
}

// loadRecoverableStaged returns only a complete, request-bound success
// transaction. The caller must still validate the artifact contracts and the
// target's final snapshot before committing and publishing it.
func (s *ArtifactStore) loadRecoverableStaged(request Request, expectedSourceSnapshotChecksum string) (ArtifactBundle, *ArtifactTransaction, bool, error) {
	if s == nil || s.root == "" {
		return ArtifactBundle{}, nil, false, ErrArtifactStore
	}
	stageRoot := filepath.Join(s.root, filepath.FromSlash(request.ArtifactContext.Paths.ArtifactStagingPath))
	if err := ensurePathInsideRoot(s.root, stageRoot); err != nil {
		return ArtifactBundle{}, nil, false, err
	}
	info, err := os.Lstat(stageRoot)
	if os.IsNotExist(err) {
		return ArtifactBundle{}, nil, false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ArtifactBundle{}, nil, true, ErrArtifactConflict
	}
	for current := stageRoot; current != s.root; current = filepath.Dir(current) {
		unsafe, checkErr := isUnsafeFilesystemEntry(current)
		if checkErr != nil || unsafe {
			return ArtifactBundle{}, nil, true, ErrArtifactConflict
		}
	}
	finalRelative := path.Dir(request.ArtifactContext.Paths.AssemblyManifestPath)
	finalRoot := filepath.Join(s.root, filepath.FromSlash(finalRelative))
	if _, err := os.Lstat(finalRoot); err == nil {
		return ArtifactBundle{}, nil, true, ErrArtifactConflict
	} else if !os.IsNotExist(err) {
		return ArtifactBundle{}, nil, true, ErrArtifactStore
	}
	transaction := &ArtifactTransaction{store: s, request: request, stageRoot: stageRoot, finalRoot: finalRoot, finalRelative: finalRelative}
	read := func(finalPath string) ([]byte, error) {
		stagedPath, pathErr := transaction.stagePathForFinal(finalPath)
		if pathErr != nil {
			return nil, pathErr
		}
		relative, relErr := filepath.Rel(stageRoot, stagedPath)
		if relErr != nil {
			return nil, ErrArtifactConflict
		}
		return readSafeWorkspaceFile(stageRoot, filepath.ToSlash(relative))
	}
	manifestRaw, manifestErr := read(request.ArtifactContext.Paths.AssemblyManifestPath)
	lockRaw, lockErr := read(request.ArtifactContext.Paths.GeneratedLockPath)
	rollbackRaw, rollbackErr := read(request.ArtifactContext.Paths.RollbackPointPath)
	journalRaw, journalErr := read(request.ArtifactContext.Paths.CommitJournalPath)
	resultRaw, resultErr := read(request.ArtifactContext.Paths.ResultPath)
	if errors.Join(manifestErr, lockErr, rollbackErr, journalErr, resultErr) != nil {
		return ArtifactBundle{}, nil, true, ErrArtifactConflict
	}
	var manifest assemblyManifestDocument
	var lock generatedLockDocument
	var rollback rollbackPointDocument
	var journal commitJournalDocument
	var result generatorResultDocument
	if jsonUnmarshalStrict(manifestRaw, &manifest) != nil || jsonUnmarshalStrict(lockRaw, &lock) != nil ||
		jsonUnmarshalStrict(rollbackRaw, &rollback) != nil || jsonUnmarshalStrict(journalRaw, &journal) != nil ||
		jsonUnmarshalStrict(resultRaw, &result) != nil {
		return ArtifactBundle{}, nil, true, ErrArtifactConflict
	}
	if !recoverableArtifactIdentityMatches(request, expectedSourceSnapshotChecksum, manifest, lock, rollback, journal, result) ||
		!recoverableArtifactClosureMatches(request, manifest, lock, rollback, journal, result) ||
		validateEmbeddedDigest(manifestRaw, "manifest_checksum", manifest.ManifestChecksum) != nil ||
		validateEmbeddedDigest(lockRaw, "lock_checksum", lock.LockChecksum) != nil ||
		validateEmbeddedDigest(rollbackRaw, "rollback_checksum", rollback.RollbackChecksum) != nil ||
		validateEmbeddedDigest(journalRaw, "journal_checksum", journal.JournalChecksum) != nil ||
		validateEmbeddedDigest(resultRaw, "result_checksum", result.ResultChecksum) != nil ||
		!digestEqual(lock.AssemblyManifestChecksum, manifest.ManifestChecksum) {
		return ArtifactBundle{}, nil, true, ErrArtifactConflict
	}

	evidenceDocuments := make(map[string][]byte, len(manifest.Evidence))
	expectedFiles := map[string]struct{}{
		request.ArtifactContext.Paths.AssemblyManifestPath: {}, request.ArtifactContext.Paths.GeneratedLockPath: {},
		request.ArtifactContext.Paths.RollbackPointPath: {}, request.ArtifactContext.Paths.CommitJournalPath: {},
		request.ArtifactContext.Paths.ResultPath: {},
	}
	for _, evidence := range manifest.Evidence {
		content, readErr := read(evidence.Path)
		if readErr != nil || !digestEqual(digestBytes(content), evidence.SHA256) {
			return ArtifactBundle{}, nil, true, ErrArtifactConflict
		}
		evidenceDocuments[evidence.Path] = content
		expectedFiles[evidence.Path] = struct{}{}
	}
	backups := make(map[string][]byte)
	for _, file := range rollback.Files {
		if file.Action != "updated" {
			continue
		}
		content, readErr := read(file.BackupPath)
		if readErr != nil || !digestEqual(digestBytes(content), file.SHA256) {
			return ArtifactBundle{}, nil, true, ErrArtifactConflict
		}
		backups[file.BackupPath] = content
		expectedFiles[file.BackupPath] = struct{}{}
	}
	if !stagedFileSetMatches(stageRoot, finalRelative, expectedFiles) {
		return ArtifactBundle{}, nil, true, ErrArtifactConflict
	}
	files := make([]ExistingFile, 0, len(lock.Files))
	for _, file := range lock.Files {
		files = append(files, ExistingFile{Path: file.Path, Ownership: file.Ownership, SHA256: file.SHA256})
	}
	sortExistingFiles(files)
	bundle := ArtifactBundle{
		AssemblyManifest: manifestRaw, GeneratedLock: lockRaw, RollbackPoint: rollbackRaw, CommitJournal: journalRaw,
		GeneratorResult: resultRaw, Backups: backups, EvidenceDocuments: evidenceDocuments,
		FinalSnapshot: TargetSnapshot{Files: files, Checksum: lock.TargetSnapshotChecksum},
	}
	if validateArtifactBundlePaths(request, bundle) != nil {
		return ArtifactBundle{}, nil, true, ErrArtifactConflict
	}
	return bundle, transaction, true, nil
}

func recoverableArtifactClosureMatches(request Request, manifest assemblyManifestDocument, lock generatedLockDocument, rollback rollbackPointDocument, journal commitJournalDocument, result generatorResultDocument) bool {
	if manifest.Product.ProductID != request.ArtifactContext.Product.ProductID ||
		manifest.Product.OfficialTenantID != request.ArtifactContext.Product.OfficialTenantID ||
		len(manifest.Product.Applications) != len(request.ArtifactContext.Product.Applications) ||
		manifest.Blueprint != request.ArtifactContext.Blueprint ||
		!digestEqual(manifest.CatalogChecksum, request.ArtifactContext.CatalogChecksum) || manifest.Generator != request.Generator ||
		manifest.CreatedAt != request.ArtifactContext.CreatedAt ||
		!digestEqual(lock.BlueprintChecksum, manifest.Blueprint.Checksum) ||
		!digestEqual(lock.CatalogChecksum, manifest.CatalogChecksum) || lock.Generator != manifest.Generator ||
		lock.CreatedAt != manifest.CreatedAt || !digestEqual(lock.TargetSnapshotChecksum, resultTargetChecksum(result)) {
		return false
	}
	for index := range manifest.Product.Applications {
		if manifest.Product.Applications[index] != request.ArtifactContext.Product.Applications[index] {
			return false
		}
	}
	if !recoverableOutputsMatch(request.DesiredOutputs, manifest.Outputs) ||
		!recoverableEvidenceMatches(request.ArtifactContext.Evidence, manifest.Evidence) {
		return false
	}
	lockByPath := make(map[string]LockedFile, len(lock.Files))
	for _, file := range lock.Files {
		if _, duplicate := lockByPath[file.Path]; duplicate {
			return false
		}
		lockByPath[file.Path] = file
	}
	changes := make(map[string]journalFile, len(journal.Changes))
	for _, change := range journal.Changes {
		locked, ok := lockByPath[change.Path]
		if _, duplicate := changes[change.Path]; duplicate || !ok || !digestEqual(change.AfterSHA256, locked.SHA256) || locked.Ownership != change.Ownership {
			return false
		}
		changes[change.Path] = change
	}
	if len(result.FilesWritten) != len(journal.Changes) {
		return false
	}
	for _, written := range result.FilesWritten {
		change, ok := changes[written.Path]
		if !ok || written.Action != change.Action || written.Ownership != change.Ownership || !digestEqual(written.SHA256, change.AfterSHA256) {
			return false
		}
	}
	if len(rollback.Files) != len(journal.Changes) {
		return false
	}
	for _, file := range rollback.Files {
		change, ok := changes[file.Path]
		if !ok || file.Action != change.Action || file.Ownership != change.Ownership || file.BackupPath != change.BackupPath ||
			(file.Action == "updated" && !digestEqual(file.SHA256, change.BeforeSHA256)) {
			return false
		}
	}
	return true
}

func recoverableOutputsMatch(expected []OutputSpec, actual []manifestOutput) bool {
	if len(expected) != len(actual) {
		return false
	}
	byPath := make(map[string]OutputSpec, len(expected))
	for _, output := range expected {
		if _, duplicate := byPath[output.Path]; duplicate {
			return false
		}
		byPath[output.Path] = output
	}
	for _, output := range actual {
		want, ok := byPath[output.Path]
		if !ok || output.Ownership != want.Ownership || output.SourceID != want.SourceID ||
			output.SourceVersion != want.SourceVersion || output.SourcePath != want.SourcePath ||
			!digestEqual(output.SourceSHA256, want.SourceSHA256) || output.RenderStrategy != want.RenderStrategy ||
			output.ContentType != want.ContentType || !equalMergeSpec(output.Merge, want.Merge) {
			return false
		}
		delete(byPath, output.Path)
	}
	return len(byPath) == 0
}

func recoverableEvidenceMatches(expected, actual []Evidence) bool {
	if len(expected) != len(actual) {
		return false
	}
	byID := make(map[string]Evidence, len(expected))
	for _, evidence := range expected {
		if _, duplicate := byID[evidence.EvidenceID]; duplicate {
			return false
		}
		byID[evidence.EvidenceID] = evidence
	}
	for _, evidence := range actual {
		want, ok := byID[evidence.EvidenceID]
		if !ok || evidence != want {
			return false
		}
		delete(byID, evidence.EvidenceID)
	}
	return len(byID) == 0
}

func resultTargetChecksum(result generatorResultDocument) string {
	files := make([]ExistingFile, 0, len(result.FilesWritten)+len(result.PreservedFiles))
	for _, file := range result.FilesWritten {
		files = append(files, ExistingFile{Path: file.Path, Ownership: file.Ownership, SHA256: file.SHA256})
	}
	for _, file := range result.PreservedFiles {
		files = append(files, ExistingFile{Path: file.Path, Ownership: file.Ownership, SHA256: file.SHA256})
	}
	sortExistingFiles(files)
	checksum, err := snapshotChecksum(files)
	if err != nil {
		return ""
	}
	return checksum
}

func recoverableArtifactIdentityMatches(request Request, expectedSourceSnapshotChecksum string, manifest assemblyManifestDocument, lock generatedLockDocument, rollback rollbackPointDocument, journal commitJournalDocument, result generatorResultDocument) bool {
	if journal.State != "prepared" && journal.State != "committed" {
		return false
	}
	return manifest.AssemblyID == request.ArtifactContext.AssemblyID && manifest.RunID == request.ArtifactContext.RunID &&
		manifest.LifecycleOperationID == request.ArtifactContext.LifecycleOperationID && lock.LockID == request.ArtifactContext.LockID &&
		lock.RunID == request.ArtifactContext.RunID && lock.LifecycleOperationID == request.ArtifactContext.LifecycleOperationID &&
		rollback.RollbackID == request.ArtifactContext.RollbackID && rollback.WorkspaceRef == request.WorkspaceRef &&
		journal.RequestID == request.RequestID && journal.WorkspaceRef == request.WorkspaceRef &&
		validDigest(expectedSourceSnapshotChecksum) && digestEqual(journal.TargetSnapshotChecksum, expectedSourceSnapshotChecksum) &&
		digestEqual(rollback.TargetSnapshotChecksum, expectedSourceSnapshotChecksum) &&
		result.RequestID == request.RequestID && result.Status == "succeeded" && result.AtomicCommitCompleted &&
		!result.RollbackAttempted && !result.RollbackCompleted &&
		result.RollbackPointPath == request.ArtifactContext.Paths.RollbackPointPath &&
		result.CommitJournalPath == request.ArtifactContext.Paths.CommitJournalPath &&
		result.AssemblyManifestPath == request.ArtifactContext.Paths.AssemblyManifestPath &&
		result.GeneratedLockPath == request.ArtifactContext.Paths.GeneratedLockPath
}

func validateEmbeddedDigest(raw []byte, field, expected string) error {
	actual, err := machinecontract.DigestWithoutTopLevelField(raw, field)
	if err != nil || !digestEqual(actual, expected) {
		return ErrArtifactConflict
	}
	return nil
}

func stagedFileSetMatches(stageRoot, finalRelative string, expected map[string]struct{}) bool {
	seen := make(map[string]struct{}, len(expected))
	err := filepath.Walk(stageRoot, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.Mode()&os.ModeSymlink != 0 {
			return ErrArtifactConflict
		}
		if info.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(stageRoot, current)
		if err != nil {
			return ErrArtifactConflict
		}
		finalPath := path.Join(finalRelative, filepath.ToSlash(relative))
		if _, ok := expected[finalPath]; !ok {
			return ErrArtifactConflict
		}
		seen[finalPath] = struct{}{}
		return nil
	})
	return err == nil && len(seen) == len(expected)
}

func (s *ArtifactStore) Prepare(request Request, bundle ArtifactBundle) (*ArtifactTransaction, error) {
	if s == nil || s.root == "" {
		return nil, ErrArtifactStore
	}
	if err := validateArtifactBundlePaths(request, bundle); err != nil {
		return nil, err
	}
	stageRoot, err := resolveNonexistentPath(s.root, request.ArtifactContext.Paths.ArtifactStagingPath)
	if err != nil {
		return nil, err
	}
	finalRelative := path.Dir(request.ArtifactContext.Paths.AssemblyManifestPath)
	finalRoot := filepath.Join(s.root, filepath.FromSlash(finalRelative))
	if err := ensurePathInsideRoot(s.root, finalRoot); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(finalRoot); err == nil {
		if artifactSetMatches(s.root, request, bundle) {
			return &ArtifactTransaction{store: s, request: request, finalRoot: finalRoot, finalRelative: finalRelative, published: true}, nil
		}
		return nil, ErrArtifactConflict
	} else if !os.IsNotExist(err) {
		return nil, ErrArtifactStore
	}
	if err := createSafeDirectory(s.root, stageRoot, 0o700); err != nil {
		return nil, err
	}
	transaction := &ArtifactTransaction{store: s, request: request, stageRoot: stageRoot, finalRoot: finalRoot, finalRelative: finalRelative}
	if err := transaction.writeBundle(bundle); err != nil {
		_ = os.RemoveAll(stageRoot)
		return nil, err
	}
	return transaction, nil
}

func (s *ArtifactStore) PublishFailure(request Request, failure FailureArtifacts, transaction *ArtifactTransaction) error {
	if s == nil || len(failure.Result) == 0 || len(failure.Diagnostics) == 0 {
		return ErrArtifactStore
	}
	if transaction == nil {
		stageRoot, err := resolveNonexistentPath(s.root, request.ArtifactContext.Paths.ArtifactStagingPath)
		if err != nil {
			return err
		}
		finalRelative := path.Dir(request.ArtifactContext.Paths.AssemblyManifestPath)
		finalRoot := filepath.Join(s.root, filepath.FromSlash(finalRelative))
		if _, err := os.Lstat(finalRoot); err == nil {
			return ErrArtifactConflict
		} else if !os.IsNotExist(err) {
			return ErrArtifactStore
		}
		if err := createSafeDirectory(s.root, stageRoot, 0o700); err != nil {
			return err
		}
		transaction = &ArtifactTransaction{store: s, request: request, stageRoot: stageRoot, finalRoot: finalRoot, finalRelative: finalRelative}
	}
	if transaction.store != s || transaction.published {
		return ErrArtifactConflict
	}
	for _, successPath := range []string{request.ArtifactContext.Paths.AssemblyManifestPath, request.ArtifactContext.Paths.GeneratedLockPath} {
		absolute, err := transaction.stagePathForFinal(successPath)
		if err == nil {
			_ = os.Remove(absolute)
		}
	}
	resultPath, err := transaction.stagePathForFinal(request.ArtifactContext.Paths.ResultPath)
	if err != nil {
		return err
	}
	if err := replaceArtifactFile(s.root, resultPath, failure.Result); err != nil {
		return err
	}
	diagnosticPaths := make([]string, 0, len(failure.Diagnostics))
	for diagnosticPath := range failure.Diagnostics {
		diagnosticPaths = append(diagnosticPaths, diagnosticPath)
	}
	sort.Strings(diagnosticPaths)
	for _, diagnosticPath := range diagnosticPaths {
		absolute, err := transaction.stagePathForFinal(diagnosticPath)
		if err != nil {
			return err
		}
		if err := writeNewFile(s.root, absolute, failure.Diagnostics[diagnosticPath], 0o600); err != nil {
			return ErrArtifactStore
		}
	}
	return transaction.Publish()
}

func (t *ArtifactTransaction) writeBundle(bundle ArtifactBundle) error {
	documents := map[string][]byte{
		t.request.ArtifactContext.Paths.AssemblyManifestPath: bundle.AssemblyManifest,
		t.request.ArtifactContext.Paths.GeneratedLockPath:    bundle.GeneratedLock,
		t.request.ArtifactContext.Paths.RollbackPointPath:    bundle.RollbackPoint,
		t.request.ArtifactContext.Paths.CommitJournalPath:    bundle.CommitJournal,
		t.request.ArtifactContext.Paths.ResultPath:           bundle.GeneratorResult,
	}
	paths := make([]string, 0, len(documents)+len(bundle.Backups)+len(bundle.EvidenceDocuments))
	for documentPath := range documents {
		paths = append(paths, documentPath)
	}
	for backupPath := range bundle.Backups {
		paths = append(paths, backupPath)
	}
	for evidencePath := range bundle.EvidenceDocuments {
		paths = append(paths, evidencePath)
	}
	sort.Strings(paths)
	for _, finalPath := range paths {
		var content []byte
		if value, ok := documents[finalPath]; ok {
			content = value
		} else if value, ok := bundle.Backups[finalPath]; ok {
			content = value
		} else {
			content = bundle.EvidenceDocuments[finalPath]
		}
		stagePath, err := t.stagePathForFinal(finalPath)
		if err != nil || len(content) == 0 {
			return ErrArtifactStore
		}
		if err := writeNewFile(t.store.root, stagePath, content, 0o600); err != nil {
			return fmt.Errorf("%w: write staged artifact", ErrArtifactStore)
		}
	}
	return nil
}

func (t *ArtifactTransaction) MarkCommitted() error {
	if t == nil || t.store == nil {
		return ErrArtifactStore
	}
	if t.published {
		return nil
	}
	journalPath, err := t.stagePathForFinal(t.request.ArtifactContext.Paths.CommitJournalPath)
	if err != nil {
		return err
	}
	return replaceJournalState(t.store.root, journalPath, "committed", false, false)
}

func (t *ArtifactTransaction) MarkRolledBack(rollbackCompleted bool) error {
	if t == nil || t.store == nil || t.published {
		return ErrArtifactStore
	}
	state := "rollback_failed"
	if rollbackCompleted {
		state = "rolled_back"
	}
	journalPath, err := t.stagePathForFinal(t.request.ArtifactContext.Paths.CommitJournalPath)
	if err != nil {
		return err
	}
	return replaceJournalState(t.store.root, journalPath, state, true, rollbackCompleted)
}

func (t *ArtifactTransaction) Publish() error {
	if t == nil || t.store == nil {
		return ErrArtifactStore
	}
	if t.published {
		return nil
	}
	if _, err := ensureSafeTargetParent(t.store.root, t.finalRoot); err != nil {
		return err
	}
	if err := os.Rename(t.stageRoot, t.finalRoot); err != nil {
		return fmt.Errorf("%w: publish artifact transaction", ErrArtifactStore)
	}
	t.published = true
	return nil
}

func (t *ArtifactTransaction) Cleanup() bool {
	if t == nil || t.stageRoot == "" || t.published {
		return true
	}
	return os.RemoveAll(t.stageRoot) == nil
}

func (t *ArtifactTransaction) ReadBackup(finalPath string) ([]byte, error) {
	if t == nil || t.store == nil {
		return nil, ErrArtifactStore
	}
	var absolute string
	var err error
	if t.published {
		absolute = filepath.Join(t.store.root, filepath.FromSlash(finalPath))
		if err = ensurePathInsideRoot(t.store.root, absolute); err != nil {
			return nil, err
		}
	} else {
		absolute, err = t.stagePathForFinal(finalPath)
		if err != nil {
			return nil, err
		}
	}
	content, err := os.ReadFile(absolute)
	if err != nil {
		return nil, fmt.Errorf("%w: read rollback backup", ErrArtifactStore)
	}
	return content, nil
}

func (t *ArtifactTransaction) stagePathForFinal(finalPath string) (string, error) {
	if machinePathErr := validateFinalArtifactPath(t.finalRelative, finalPath); machinePathErr != nil {
		return "", machinePathErr
	}
	relative := strings.TrimPrefix(finalPath, t.finalRelative+"/")
	if relative == finalPath || relative == "" {
		return "", ErrInvalidInput
	}
	absolute := filepath.Join(t.stageRoot, filepath.FromSlash(relative))
	if err := ensurePathInsideRoot(t.stageRoot, absolute); err != nil {
		return "", err
	}
	return absolute, nil
}

func validateArtifactBundlePaths(request Request, bundle ArtifactBundle) error {
	if len(bundle.AssemblyManifest) == 0 || len(bundle.GeneratedLock) == 0 || len(bundle.RollbackPoint) == 0 || len(bundle.CommitJournal) == 0 || len(bundle.GeneratorResult) == 0 {
		return ErrInvalidInput
	}
	finalRelative := path.Dir(request.ArtifactContext.Paths.AssemblyManifestPath)
	for _, value := range []string{
		request.ArtifactContext.Paths.AssemblyManifestPath, request.ArtifactContext.Paths.GeneratedLockPath,
		request.ArtifactContext.Paths.RollbackPointPath, request.ArtifactContext.Paths.CommitJournalPath,
		request.ArtifactContext.Paths.ResultPath,
	} {
		if err := validateFinalArtifactPath(finalRelative, value); err != nil {
			return err
		}
	}
	for backupPath := range bundle.Backups {
		if err := validateFinalArtifactPath(finalRelative, backupPath); err != nil {
			return err
		}
	}
	for evidencePath := range bundle.EvidenceDocuments {
		if err := validateFinalArtifactPath(finalRelative, evidencePath); err != nil {
			return err
		}
	}
	return nil
}

func validateFinalArtifactPath(finalRoot, value string) error {
	if finalRoot == "." || machinecontract.ValidateSafeRelativePath(value) != nil || (value != finalRoot && !strings.HasPrefix(value, finalRoot+"/")) {
		return ErrInvalidInput
	}
	return nil
}

func artifactSetMatches(root string, request Request, bundle ArtifactBundle) bool {
	expected := map[string][]byte{
		request.ArtifactContext.Paths.AssemblyManifestPath: bundle.AssemblyManifest,
		request.ArtifactContext.Paths.GeneratedLockPath:    bundle.GeneratedLock,
		request.ArtifactContext.Paths.RollbackPointPath:    bundle.RollbackPoint,
		request.ArtifactContext.Paths.ResultPath:           bundle.GeneratorResult,
	}
	for filePath, want := range expected {
		actual, err := readSafeWorkspaceFile(root, filePath)
		if err != nil || !digestEqual(digestBytes(actual), digestBytes(want)) {
			return false
		}
	}
	actualJournal, err := readSafeWorkspaceFile(root, request.ArtifactContext.Paths.CommitJournalPath)
	if err != nil || !committedJournalMatches(actualJournal, bundle.CommitJournal) {
		return false
	}
	for filePath, want := range bundle.Backups {
		actual, err := readSafeWorkspaceFile(root, filePath)
		if err != nil || !digestEqual(digestBytes(actual), digestBytes(want)) {
			return false
		}
	}
	for filePath, want := range bundle.EvidenceDocuments {
		actual, err := readSafeWorkspaceFile(root, filePath)
		if err != nil || !digestEqual(digestBytes(actual), digestBytes(want)) {
			return false
		}
	}
	return true
}

func committedJournalMatches(actual, prepared []byte) bool {
	var actualDocument, preparedDocument commitJournalDocument
	if jsonUnmarshalStrict(actual, &actualDocument) != nil || jsonUnmarshalStrict(prepared, &preparedDocument) != nil || actualDocument.State != "committed" {
		return false
	}
	preparedDocument.State = "committed"
	preparedDocument.RollbackAttempted = false
	preparedDocument.RollbackCompleted = false
	expected, _, err := marshalWithEmbeddedDigest(preparedDocument, "journal_checksum")
	return err == nil && digestEqual(digestBytes(actual), digestBytes(expected))
}

func replaceJournalState(root, journalPath, state string, attempted, completed bool) error {
	raw, err := os.ReadFile(journalPath)
	if err != nil {
		return ErrArtifactStore
	}
	var journal commitJournalDocument
	if err := jsonUnmarshalStrict(raw, &journal); err != nil {
		return ErrArtifactStore
	}
	journal.State, journal.RollbackAttempted, journal.RollbackCompleted = state, attempted, completed
	updated, _, err := marshalWithEmbeddedDigest(journal, "journal_checksum")
	if err != nil {
		return ErrArtifactStore
	}
	temporary := journalPath + ".next"
	if err := writeNewFile(root, temporary, updated, 0o600); err != nil {
		return ErrArtifactStore
	}
	if err := replaceFile(temporary, journalPath); err != nil {
		_ = os.Remove(temporary)
		return ErrArtifactStore
	}
	return nil
}

func replaceArtifactFile(root, target string, content []byte) error {
	if _, err := os.Lstat(target); os.IsNotExist(err) {
		return writeNewFile(root, target, content, 0o600)
	} else if err != nil {
		return ErrArtifactStore
	}
	temporary := target + ".next"
	if err := writeNewFile(root, temporary, content, 0o600); err != nil {
		return ErrArtifactStore
	}
	if err := replaceFile(temporary, target); err != nil {
		_ = os.Remove(temporary)
		return ErrArtifactStore
	}
	return nil
}

func jsonUnmarshalStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidInput
	}
	return nil
}
