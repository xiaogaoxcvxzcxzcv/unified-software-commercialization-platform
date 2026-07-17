package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type ArtifactContractValidator interface {
	Validate(string, []byte) error
}

type Executor struct {
	renderer  Renderer
	committer *FileCommitter
	artifacts *ArtifactStore
	contracts ArtifactContractValidator
}

type ExecutionOutcome struct {
	Commit    CommitResult
	Bundle    ArtifactBundle
	Failure   FailureArtifacts
	Published bool
}

func NewExecutor(renderer Renderer, committer *FileCommitter, artifacts *ArtifactStore, contracts ArtifactContractValidator) (*Executor, error) {
	if renderer == nil || committer == nil || artifacts == nil || contracts == nil {
		return nil, ErrInvalidInput
	}
	return &Executor{renderer: renderer, committer: committer, artifacts: artifacts, contracts: contracts}, nil
}

func (e *Executor) Execute(ctx context.Context, targetRoot string, input Input, previousLock ProjectLock, previousArtifacts PreviousArtifacts) (ExecutionOutcome, error) {
	if e == nil || rootsOverlap(targetRoot, e.artifacts.Root()) {
		return ExecutionOutcome{}, ErrInvalidInput
	}
	requestRaw, err := json.Marshal(input.Request)
	if err != nil || e.contracts.Validate("generator-request", requestRaw) != nil {
		return ExecutionOutcome{}, ErrInvalidInput
	}
	expectedSourceSnapshotChecksum := input.Request.TargetSnapshotChecksum
	if validDigest(previousLock.TargetSnapshotChecksum) {
		expectedSourceSnapshotChecksum = previousLock.TargetSnapshotChecksum
	}
	if recovered, found, err := e.recover(ctx, targetRoot, input, expectedSourceSnapshotChecksum, true); err != nil {
		return ExecutionOutcome{}, err
	} else if found {
		return recovered, nil
	}
	preCommit := CommitResult{StagingCleanupCompleted: true, TargetUnchanged: true}
	rendered, err := e.renderer.Render(ctx, input)
	if err != nil {
		return e.fail(input.Request, PreparedChangeSet{}, preCommit, nil, err)
	}
	prepared, err := PrepareTarget(targetRoot, input.Request, rendered, previousLock)
	if err != nil {
		return e.fail(input.Request, prepared, preCommit, nil, err)
	}
	bundle, err := BuildArtifactBundle(targetRoot, input.Request, input.Plan, input.EvidenceDocuments, prepared, previousArtifacts)
	if err != nil {
		return e.fail(input.Request, prepared, preCommit, nil, err)
	}
	if err := e.validateSuccessBundle(bundle); err != nil {
		return e.fail(input.Request, prepared, preCommit, nil, err)
	}
	transaction, err := e.artifacts.Prepare(input.Request, bundle)
	if err != nil {
		return ExecutionOutcome{}, err
	}
	if transaction.published {
		if err := verifyFinalSnapshot(targetRoot, bundle.FinalSnapshot); err != nil {
			return ExecutionOutcome{}, ErrArtifactConflict
		}
		return ExecutionOutcome{Commit: CommitResult{AtomicCommitCompleted: true, StagingCleanupCompleted: true, TargetUnchanged: true}, Bundle: bundle, Published: true}, nil
	}
	commit, err := e.committer.Commit(ctx, targetRoot, input.Request, prepared)
	if err != nil {
		if commit.RollbackAttempted {
			_ = transaction.MarkRolledBack(commit.RollbackCompleted)
		}
		return e.fail(input.Request, prepared, commit, transaction, err)
	}
	if err := verifyFinalSnapshot(targetRoot, bundle.FinalSnapshot); err != nil {
		return e.rollbackAndFail(targetRoot, input.Request, prepared, bundle, transaction, commit, err)
	}
	if err := transaction.MarkCommitted(); err != nil {
		return e.rollbackAndFail(targetRoot, input.Request, prepared, bundle, transaction, commit, err)
	}
	if err := transaction.Publish(); err != nil {
		return e.rollbackAndFail(targetRoot, input.Request, prepared, bundle, transaction, commit, err)
	}
	return ExecutionOutcome{Commit: commit, Bundle: bundle, Published: true}, nil
}

// Recover completes an already-published or deterministically staged success
// transaction without rendering or writing product files. The expected source
// checksum must come from the durable lifecycle operation, not the current
// workspace, which may already contain the committed successor snapshot.
func (e *Executor) Recover(ctx context.Context, targetRoot string, input Input, expectedSourceSnapshotChecksum string) (ExecutionOutcome, bool, error) {
	if e == nil || rootsOverlap(targetRoot, e.artifacts.Root()) || !validDigest(expectedSourceSnapshotChecksum) {
		return ExecutionOutcome{}, false, ErrInvalidInput
	}
	requestRaw, err := json.Marshal(input.Request)
	if err != nil || e.contracts.Validate("generator-request", requestRaw) != nil {
		return ExecutionOutcome{}, false, ErrInvalidInput
	}
	return e.recover(ctx, targetRoot, input, expectedSourceSnapshotChecksum, false)
}

func (e *Executor) recover(ctx context.Context, targetRoot string, input Input, expectedSourceSnapshotChecksum string, restartInPlace bool) (ExecutionOutcome, bool, error) {
	if err := ctx.Err(); err != nil {
		return ExecutionOutcome{}, false, err
	}
	if published, found, err := e.artifacts.loadPublishedRecoverable(input.Request, expectedSourceSnapshotChecksum); err != nil {
		return ExecutionOutcome{}, found, err
	} else if found {
		if err := e.validateSuccessBundle(published); err != nil || !recoveryBundleMatchesPlan(input, published) || verifyFinalSnapshot(targetRoot, published.FinalSnapshot) != nil {
			return ExecutionOutcome{}, true, ErrArtifactConflict
		}
		return ExecutionOutcome{Commit: CommitResult{AtomicCommitCompleted: true, StagingCleanupCompleted: true, TargetUnchanged: true}, Bundle: published, Published: true}, true, nil
	}
	if staged, transaction, found, err := e.artifacts.loadRecoverableStaged(input.Request, expectedSourceSnapshotChecksum); err != nil {
		return ExecutionOutcome{}, found, err
	} else if found {
		if err := e.validateSuccessBundle(staged); err != nil || !recoveryBundleMatchesPlan(input, staged) {
			return ExecutionOutcome{}, true, ErrArtifactConflict
		}
		if verifyFinalSnapshot(targetRoot, staged.FinalSnapshot) != nil {
			if err := restoreInterruptedStage(targetRoot, input.Request, staged, transaction, expectedSourceSnapshotChecksum); err != nil {
				return ExecutionOutcome{}, true, err
			}
			if restartInPlace {
				return ExecutionOutcome{}, false, nil
			}
			return ExecutionOutcome{}, true, ErrTargetChanged
		}
		if err := transaction.MarkCommitted(); err != nil {
			return ExecutionOutcome{}, true, err
		}
		if err := transaction.Publish(); err != nil {
			return ExecutionOutcome{}, true, err
		}
		return ExecutionOutcome{Commit: CommitResult{AtomicCommitCompleted: true, StagingCleanupCompleted: true, TargetUnchanged: true}, Bundle: staged, Published: true}, true, nil
	}
	return ExecutionOutcome{}, false, nil
}

func recoveryBundleMatchesPlan(input Input, bundle ArtifactBundle) bool {
	var plan Plan
	var manifest assemblyManifestDocument
	if json.Unmarshal(input.Plan, &plan) != nil || jsonUnmarshalStrict(bundle.AssemblyManifest, &manifest) != nil || validateRequestPlan(input.Request, plan) != nil {
		return false
	}
	type dependency struct{ version, checksum string }
	packages := make(map[string]dependency, len(plan.Packages))
	for _, value := range plan.Packages {
		if _, duplicate := packages[value.PackageID]; duplicate {
			return false
		}
		packages[value.PackageID] = dependency{value.Version, value.Checksum}
	}
	templates := make(map[string]dependency, len(plan.Applications))
	for _, application := range plan.Applications {
		value := application.Template
		want := dependency{value.Version, value.Checksum}
		if prior, duplicate := templates[value.TemplateID]; duplicate && prior != want {
			return false
		}
		templates[value.TemplateID] = want
	}
	sdks := make(map[string]dependency, len(plan.SDKs))
	for _, value := range plan.SDKs {
		if _, duplicate := sdks[value.SDKID]; duplicate {
			return false
		}
		sdks[value.SDKID] = dependency{value.Version, value.Checksum}
	}
	compare := func(expected map[string]dependency, actual map[string]dependency) bool {
		if len(expected) != len(actual) {
			return false
		}
		for id, want := range expected {
			got, ok := actual[id]
			if !ok || got.version != want.version || !digestEqual(got.checksum, want.checksum) {
				return false
			}
		}
		return true
	}
	manifestPackages := make(map[string]dependency, len(manifest.Packages))
	for _, value := range manifest.Packages {
		manifestPackages[value.PackageID] = dependency{value.Version, value.Checksum}
	}
	manifestTemplates := make(map[string]dependency, len(manifest.Templates))
	for _, value := range manifest.Templates {
		manifestTemplates[value.TemplateID] = dependency{value.Version, value.Checksum}
	}
	manifestSDKs := make(map[string]dependency, len(manifest.SDKs))
	for _, value := range manifest.SDKs {
		manifestSDKs[value.SDKID] = dependency{value.Version, value.Checksum}
	}
	return compare(packages, manifestPackages) && compare(templates, manifestTemplates) && compare(sdks, manifestSDKs)
}

func restoreInterruptedStage(targetRoot string, request Request, bundle ArtifactBundle, transaction *ArtifactTransaction, expectedSourceSnapshotChecksum string) error {
	var journal commitJournalDocument
	var result generatorResultDocument
	if transaction == nil || jsonUnmarshalStrict(bundle.CommitJournal, &journal) != nil || jsonUnmarshalStrict(bundle.GeneratorResult, &result) != nil ||
		!digestEqual(journal.TargetSnapshotChecksum, expectedSourceSnapshotChecksum) {
		return ErrArtifactConflict
	}
	source, err := sourceSnapshotFromRecovery(journal, result)
	if err != nil || !digestEqual(source.Checksum, expectedSourceSnapshotChecksum) {
		return ErrArtifactConflict
	}
	if err := cleanupGeneratorStage(targetRoot, request.StagingPath); err != nil {
		return err
	}
	if verifyFinalSnapshot(targetRoot, source) != nil {
		applied, classifyErr := classifyInterruptedChanges(targetRoot, journal, transaction)
		if classifyErr != nil {
			return classifyErr
		}
		if rollbackErr := rollbackApplied(targetRoot, applied, nil); rollbackErr != nil || verifyFinalSnapshot(targetRoot, source) != nil {
			return ErrRollbackFailed
		}
	}
	if !transaction.Cleanup() {
		return ErrArtifactStore
	}
	return nil
}

func sourceSnapshotFromRecovery(journal commitJournalDocument, result generatorResultDocument) (TargetSnapshot, error) {
	files := make([]ExistingFile, 0, len(journal.Changes)+len(result.PreservedFiles))
	for _, change := range journal.Changes {
		if change.Action == "created" {
			continue
		}
		before := change.BeforeSHA256
		if change.Action == "unchanged" && before == "" {
			before = change.AfterSHA256
		}
		if !validDigest(before) {
			return TargetSnapshot{}, ErrArtifactConflict
		}
		files = append(files, ExistingFile{Path: change.Path, Ownership: change.Ownership, SHA256: before})
	}
	for _, file := range result.PreservedFiles {
		files = append(files, ExistingFile{Path: file.Path, Ownership: file.Ownership, SHA256: file.SHA256})
	}
	sortExistingFiles(files)
	checksum, err := snapshotChecksum(files)
	if err != nil {
		return TargetSnapshot{}, ErrArtifactConflict
	}
	return TargetSnapshot{Files: files, Checksum: checksum}, nil
}

func classifyInterruptedChanges(targetRoot string, journal commitJournalDocument, transaction *ArtifactTransaction) ([]appliedChange, error) {
	applied := make([]appliedChange, 0, len(journal.Changes))
	for _, change := range journal.Changes {
		content, exists, err := readOptionalSafeFile(targetRoot, change.Path)
		if err != nil {
			return nil, err
		}
		actual := ""
		if exists {
			actual = digestBytes(content)
		}
		switch change.Action {
		case "unchanged":
			if !exists || !digestEqual(actual, change.AfterSHA256) {
				return nil, ErrArtifactConflict
			}
		case "created":
			if !exists {
				continue
			}
			if !digestEqual(actual, change.AfterSHA256) {
				return nil, ErrArtifactConflict
			}
			applied = append(applied, appliedChange{change: FileChange{Path: change.Path, Ownership: change.Ownership, Action: change.Action, SHA256: change.AfterSHA256}, mode: 0o644})
		case "updated":
			if exists && digestEqual(actual, change.BeforeSHA256) {
				continue
			}
			if !exists || !digestEqual(actual, change.AfterSHA256) {
				return nil, ErrArtifactConflict
			}
			backupPath, pathErr := transaction.stagePathForFinal(change.BackupPath)
			if pathErr != nil {
				return nil, pathErr
			}
			info, statErr := os.Stat(filepath.Join(targetRoot, filepath.FromSlash(change.Path)))
			if statErr != nil || !info.Mode().IsRegular() {
				return nil, ErrArtifactConflict
			}
			applied = append(applied, appliedChange{change: FileChange{Path: change.Path, Ownership: change.Ownership, Action: change.Action, SHA256: change.AfterSHA256, PreviousSHA256: change.BeforeSHA256}, backupPath: backupPath, mode: info.Mode().Perm()})
		default:
			return nil, ErrArtifactConflict
		}
	}
	return applied, nil
}

func readOptionalSafeFile(root, relative string) ([]byte, bool, error) {
	if err := machinecontract.ValidateSafeRelativePath(relative); err != nil {
		return nil, false, ErrTargetUnsafe
	}
	full := filepath.Join(root, filepath.FromSlash(relative))
	if err := ensurePathInsideRoot(root, full); err != nil {
		return nil, false, err
	}
	if _, err := os.Lstat(full); os.IsNotExist(err) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, ErrTargetUnsafe
	}
	content, err := readSafeWorkspaceFile(root, relative)
	return content, err == nil, err
}

func cleanupGeneratorStage(root, relative string) error {
	if err := machinecontract.ValidateSafeRelativePath(relative); err != nil {
		return ErrTargetUnsafe
	}
	full := filepath.Join(root, filepath.FromSlash(relative))
	if err := ensurePathInsideRoot(root, full); err != nil {
		return err
	}
	info, err := os.Lstat(full)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrTargetUnsafe
	}
	for current := full; current != root; current = filepath.Dir(current) {
		unsafe, checkErr := isUnsafeFilesystemEntry(current)
		if checkErr != nil || unsafe {
			return ErrTargetUnsafe
		}
	}
	if err := os.RemoveAll(full); err != nil {
		return ErrArtifactStore
	}
	return nil
}

func (e *Executor) rollbackAndFail(targetRoot string, request Request, prepared PreparedChangeSet, bundle ArtifactBundle, transaction *ArtifactTransaction, commit CommitResult, cause error) (ExecutionOutcome, error) {
	commit.AtomicCommitCompleted = false
	commit.RollbackAttempted = true
	rollbackErr := restorePreparedChanges(targetRoot, request, prepared, transaction, bundle.FinalSnapshot)
	commit.RollbackCompleted = rollbackErr == nil
	commit.TargetUnchanged = rollbackErr == nil
	_ = transaction.MarkRolledBack(commit.RollbackCompleted)
	if rollbackErr != nil {
		cause = errors.Join(cause, ErrRollbackFailed)
	}
	return e.fail(request, prepared, commit, transaction, cause)
}

func (e *Executor) fail(request Request, prepared PreparedChangeSet, commit CommitResult, transaction *ArtifactTransaction, cause error) (ExecutionOutcome, error) {
	failure, err := BuildFailureArtifacts(request, prepared, cause, commit)
	if err != nil {
		if transaction != nil {
			transaction.Cleanup()
		}
		return ExecutionOutcome{Commit: commit}, errors.Join(cause, err)
	}
	if err := e.validateFailureArtifacts(failure); err != nil {
		if transaction != nil {
			transaction.Cleanup()
		}
		return ExecutionOutcome{Commit: commit}, errors.Join(cause, err)
	}
	if err := e.artifacts.PublishFailure(request, failure, transaction); err != nil {
		if transaction != nil {
			transaction.Cleanup()
		}
		return ExecutionOutcome{Commit: commit}, errors.Join(cause, err)
	}
	return ExecutionOutcome{Commit: commit, Failure: failure, Published: true}, cause
}

func (e *Executor) validateSuccessBundle(bundle ArtifactBundle) error {
	for name, document := range map[string][]byte{
		"assembly-manifest":        bundle.AssemblyManifest,
		"generated-project-lock":   bundle.GeneratedLock,
		"generator-rollback-point": bundle.RollbackPoint,
		"generator-commit-journal": bundle.CommitJournal,
		"generator-result":         bundle.GeneratorResult,
	} {
		if err := e.contracts.Validate(name, document); err != nil {
			return fmt.Errorf("%w: %s: %v", ErrArtifactStore, name, err)
		}
	}
	return nil
}

func (e *Executor) validateFailureArtifacts(failure FailureArtifacts) error {
	if err := e.contracts.Validate("generator-result", failure.Result); err != nil {
		return ErrArtifactStore
	}
	for _, document := range failure.Diagnostics {
		if err := e.contracts.Validate("generator-diagnostic", document); err != nil {
			return ErrArtifactStore
		}
	}
	return nil
}

func verifyFinalSnapshot(targetRoot string, expected TargetSnapshot) error {
	actual, err := inspectWithKnownOwnership(targetRoot, expected.Files)
	if err != nil || !digestEqual(actual.Checksum, expected.Checksum) {
		return ErrTargetChanged
	}
	return nil
}

func restorePreparedChanges(targetRoot string, request Request, prepared PreparedChangeSet, transaction *ArtifactTransaction, finalSnapshot TargetSnapshot) error {
	if transaction == nil || verifyFinalSnapshot(targetRoot, finalSnapshot) != nil {
		return ErrRollbackFailed
	}
	recoveryRelative := request.StagingPath + "-recovery"
	recoveryRoot, err := resolveNonexistentPath(targetRoot, recoveryRelative)
	if err != nil {
		return ErrRollbackFailed
	}
	if err := createSafeDirectory(targetRoot, recoveryRoot, 0o700); err != nil {
		return ErrRollbackFailed
	}
	defer os.RemoveAll(recoveryRoot)
	backupRoot := filepath.ToSlash(filepath.Join(filepath.Dir(request.ArtifactContext.Paths.RollbackPointPath), "backups"))
	applied := make([]appliedChange, 0, len(prepared.Changes))
	for _, change := range prepared.Changes {
		if change.Action == "unchanged" {
			continue
		}
		item := appliedChange{change: change, mode: 0o644}
		targetPath := filepath.Join(targetRoot, filepath.FromSlash(change.Path))
		if info, statErr := os.Stat(targetPath); statErr == nil {
			item.mode = info.Mode().Perm()
		}
		if change.Action == "updated" {
			backupPath := filepath.ToSlash(filepath.Join(filepath.FromSlash(backupRoot), filepath.FromSlash(change.Path)))
			backup, readErr := transaction.ReadBackup(backupPath)
			if readErr != nil || !digestEqual(digestBytes(backup), change.PreviousSHA256) {
				return ErrRollbackFailed
			}
			item.backupPath = filepath.Join(recoveryRoot, filepath.FromSlash(change.Path))
			if err := writeNewFile(targetRoot, item.backupPath, backup, item.mode); err != nil {
				return ErrRollbackFailed
			}
		}
		applied = append(applied, item)
	}
	if err := rollbackApplied(targetRoot, applied, nil); err != nil {
		return ErrRollbackFailed
	}
	if err := VerifyPreparedSnapshot(targetRoot, prepared.Snapshot); err != nil {
		return ErrRollbackFailed
	}
	return nil
}

func rootsOverlap(first, second string) bool {
	first, errFirst := filepath.Abs(first)
	second, errSecond := filepath.Abs(second)
	if errFirst != nil || errSecond != nil {
		return true
	}
	inside := func(parent, candidate string) bool {
		relative, err := filepath.Rel(parent, candidate)
		return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
	}
	return inside(first, second) || inside(second, first)
}

func DecodeProjectLock(document []byte) (ProjectLock, error) {
	var lock ProjectLock
	if err := json.Unmarshal(document, &lock); err != nil || lock.SchemaVersion != "1.0.0" || !validDigest(lock.LockChecksum) {
		return ProjectLock{}, ErrInvalidInput
	}
	expected, err := machinecontract.DigestWithoutTopLevelField(document, "lock_checksum")
	if err != nil || !digestEqual(expected, lock.LockChecksum) {
		return ProjectLock{}, ErrInvalidInput
	}
	if _, err := lockedFilesByPath(lock.Files); err != nil {
		return ProjectLock{}, err
	}
	return lock, nil
}
