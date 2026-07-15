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
