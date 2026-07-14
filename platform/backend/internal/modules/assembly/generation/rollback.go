package generation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type RollbackExecutor struct {
	artifacts *ArtifactStore
	contracts ArtifactContractValidator
}

func NewRollbackExecutor(artifacts *ArtifactStore, contracts ArtifactContractValidator) (*RollbackExecutor, error) {
	if artifacts == nil || contracts == nil {
		return nil, ErrInvalidInput
	}
	return &RollbackExecutor{artifacts: artifacts, contracts: contracts}, nil
}

func (e *RollbackExecutor) Rollback(ctx context.Context, targetRoot, rollbackPointPath, commitJournalPath string) (TargetSnapshot, error) {
	if e == nil || rootsOverlap(targetRoot, e.artifacts.Root()) || machineSafeRelative(rollbackPointPath) != nil || machineSafeRelative(commitJournalPath) != nil {
		return TargetSnapshot{}, ErrInvalidInput
	}
	rollbackRaw, err := readSafeWorkspaceFile(e.artifacts.root, rollbackPointPath)
	if err != nil || e.contracts.Validate("generator-rollback-point", rollbackRaw) != nil || decodeRollbackChecksum(rollbackRaw, "rollback_checksum") != nil {
		return TargetSnapshot{}, ErrArtifactStore
	}
	journalRaw, err := readSafeWorkspaceFile(e.artifacts.root, commitJournalPath)
	if err != nil || e.contracts.Validate("generator-commit-journal", journalRaw) != nil || decodeRollbackChecksum(journalRaw, "journal_checksum") != nil {
		return TargetSnapshot{}, ErrArtifactStore
	}
	var rollback rollbackPointDocument
	var journal commitJournalDocument
	if jsonUnmarshalStrict(rollbackRaw, &rollback) != nil || jsonUnmarshalStrict(journalRaw, &journal) != nil ||
		!digestEqual(rollback.TargetSnapshotChecksum, journal.TargetSnapshotChecksum) || len(rollback.Files) != len(journal.Changes) {
		return TargetSnapshot{}, ErrArtifactConflict
	}
	if journal.State == "rolled_back" {
		return verifyRollbackSnapshot(targetRoot, rollback, journal)
	}
	if journal.State != "committed" {
		return TargetSnapshot{}, ErrArtifactConflict
	}
	select {
	case <-ctx.Done():
		return TargetSnapshot{}, ctx.Err()
	default:
	}
	if err := verifyCommittedChanges(targetRoot, journal); err != nil {
		return TargetSnapshot{}, err
	}
	recoveryRelative := ".runtime/generator/rollback-" + strings.TrimPrefix(digestBytes([]byte(rollback.RollbackID)), "sha256:")[:24]
	recoveryRoot, err := resolveNonexistentPath(targetRoot, recoveryRelative)
	if err != nil || createSafeDirectory(targetRoot, recoveryRoot, 0o700) != nil {
		return TargetSnapshot{}, ErrRollbackFailed
	}
	defer os.RemoveAll(recoveryRoot)
	rollbackByPath := make(map[string]rollbackFile, len(rollback.Files))
	for _, item := range rollback.Files {
		rollbackByPath[item.Path] = item
	}
	applied := make([]appliedChange, 0, len(journal.Changes))
	for _, change := range journal.Changes {
		if change.Action == "unchanged" {
			continue
		}
		item := appliedChange{change: FileChange{Path: change.Path, Ownership: change.Ownership, Action: change.Action, SHA256: change.AfterSHA256, PreviousSHA256: change.BeforeSHA256}, mode: 0o644}
		targetPath := filepath.Join(targetRoot, filepath.FromSlash(change.Path))
		if info, statErr := os.Stat(targetPath); statErr == nil {
			item.mode = info.Mode().Perm()
		}
		if change.Action == "updated" {
			rollbackItem := rollbackByPath[change.Path]
			backup, readErr := readSafeWorkspaceFile(e.artifacts.root, rollbackItem.BackupPath)
			if readErr != nil || !digestEqual(digestBytes(backup), change.BeforeSHA256) || !digestEqual(rollbackItem.SHA256, change.BeforeSHA256) {
				_ = e.markRollbackJournal(commitJournalPath, false)
				return TargetSnapshot{}, ErrRollbackFailed
			}
			item.backupPath = filepath.Join(recoveryRoot, filepath.FromSlash(change.Path))
			if err := writeNewFile(targetRoot, item.backupPath, backup, item.mode); err != nil {
				_ = e.markRollbackJournal(commitJournalPath, false)
				return TargetSnapshot{}, ErrRollbackFailed
			}
		}
		applied = append(applied, item)
	}
	if err := rollbackApplied(targetRoot, applied, nil); err != nil {
		_ = e.markRollbackJournal(commitJournalPath, false)
		return TargetSnapshot{}, ErrRollbackFailed
	}
	snapshot, err := verifyRollbackSnapshot(targetRoot, rollback, journal)
	if err != nil {
		_ = e.markRollbackJournal(commitJournalPath, false)
		return TargetSnapshot{}, err
	}
	if err := e.markRollbackJournal(commitJournalPath, true); err != nil {
		return TargetSnapshot{}, err
	}
	return snapshot, nil
}

func verifyCommittedChanges(targetRoot string, journal commitJournalDocument) error {
	for _, change := range journal.Changes {
		content, err := readSafeWorkspaceFile(targetRoot, change.Path)
		if err != nil || !digestEqual(digestBytes(content), change.AfterSHA256) {
			return ErrTargetChanged
		}
	}
	return nil
}

func verifyRollbackSnapshot(targetRoot string, rollback rollbackPointDocument, journal commitJournalDocument) (TargetSnapshot, error) {
	lock := ProjectLock{Files: make([]LockedFile, 0, len(journal.Changes))}
	for _, change := range journal.Changes {
		if change.Action == "created" {
			continue
		}
		before := change.BeforeSHA256
		if change.Action == "unchanged" && before == "" {
			before = change.AfterSHA256
		}
		lock.Files = append(lock.Files, LockedFile{Path: change.Path, Ownership: change.Ownership, SHA256: before, UpdatePolicy: updatePolicy(change.Ownership)})
	}
	snapshot, err := InspectTarget(targetRoot, lock)
	if err != nil || !digestEqual(snapshot.Checksum, rollback.TargetSnapshotChecksum) {
		return TargetSnapshot{}, ErrRollbackFailed
	}
	return snapshot, nil
}

func (e *RollbackExecutor) markRollbackJournal(relative string, completed bool) error {
	absolute := filepath.Join(e.artifacts.root, filepath.FromSlash(relative))
	if err := ensurePathInsideRoot(e.artifacts.root, absolute); err != nil {
		return err
	}
	state := "rollback_failed"
	if completed {
		state = "rolled_back"
	}
	return replaceJournalState(e.artifacts.root, absolute, state, true, completed)
}

func machineSafeRelative(value string) error {
	if machinecontract.ValidateSafeRelativePath(value) != nil {
		return ErrInvalidInput
	}
	return nil
}

func decodeRollbackChecksum(raw []byte, field string) error {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return ErrInvalidInput
	}
	var claimed string
	if json.Unmarshal(value[field], &claimed) != nil {
		return ErrInvalidInput
	}
	actual, err := machinecontract.DigestWithoutTopLevelField(raw, field)
	if err != nil || !digestEqual(claimed, actual) {
		return errors.New("embedded checksum mismatch")
	}
	return nil
}
