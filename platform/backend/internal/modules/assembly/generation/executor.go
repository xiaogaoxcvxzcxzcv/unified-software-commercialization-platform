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
	if recovered, found, err := e.recover(ctx, targetRoot, input, input.Request.TargetSnapshotChecksum); err != nil {
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
	return e.recover(ctx, targetRoot, input, expectedSourceSnapshotChecksum)
}

func (e *Executor) recover(ctx context.Context, targetRoot string, input Input, expectedSourceSnapshotChecksum string) (ExecutionOutcome, bool, error) {
	if err := ctx.Err(); err != nil {
		return ExecutionOutcome{}, false, err
	}
	if published, found, err := e.artifacts.LoadPublished(input.Request); err != nil {
		return ExecutionOutcome{}, found, err
	} else if found {
		if err := e.validateSuccessBundle(published); err != nil || verifyFinalSnapshot(targetRoot, published.FinalSnapshot) != nil {
			return ExecutionOutcome{}, true, ErrArtifactConflict
		}
		return ExecutionOutcome{Commit: CommitResult{AtomicCommitCompleted: true, StagingCleanupCompleted: true, TargetUnchanged: true}, Bundle: published, Published: true}, true, nil
	}
	if staged, transaction, found, err := e.artifacts.loadRecoverableStaged(input.Request, expectedSourceSnapshotChecksum); err != nil {
		return ExecutionOutcome{}, found, err
	} else if found {
		if err := e.validateSuccessBundle(staged); err != nil || verifyFinalSnapshot(targetRoot, staged.FinalSnapshot) != nil {
			return ExecutionOutcome{}, true, ErrArtifactConflict
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
