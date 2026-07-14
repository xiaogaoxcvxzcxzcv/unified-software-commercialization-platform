package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type FileCommitter struct {
	beforeReplace func(int, FileChange) error
}

type appliedChange struct {
	change     FileChange
	backupPath string
	mode       os.FileMode
}

type commitJournal struct {
	SchemaVersion          string        `json:"schema_version"`
	RequestID              string        `json:"request_id"`
	TargetSnapshotChecksum string        `json:"target_snapshot_checksum"`
	Changes                []journalItem `json:"changes"`
}

type journalItem struct {
	Path         string `json:"path"`
	Ownership    string `json:"ownership"`
	Action       string `json:"action"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	AfterSHA256  string `json:"after_sha256"`
	StagedPath   string `json:"staged_path"`
	BackupPath   string `json:"backup_path,omitempty"`
}

func NewFileCommitter() *FileCommitter {
	return &FileCommitter{}
}

func (c *FileCommitter) Commit(ctx context.Context, root string, request Request, prepared PreparedChangeSet) (CommitResult, error) {
	result := CommitResult{PreservedFiles: append([]ExistingFile(nil), prepared.Preserved...)}
	if c == nil || len(prepared.Diagnostics) != 0 || (request.Operation != "generate" && request.Operation != "upgrade") {
		return result, ErrOwnershipConflict
	}
	root, err := validateWorkspaceRoot(root)
	if err != nil {
		return result, err
	}
	if err := VerifyPreparedSnapshot(root, prepared.Snapshot); err != nil {
		result.TargetUnchanged = false
		return result, err
	}
	if err := validateCommitPaths(request, prepared.Changes); err != nil {
		return result, err
	}
	stageRoot, err := resolveNonexistentPath(root, request.StagingPath)
	if err != nil {
		return result, err
	}
	if err := createSafeDirectory(root, stageRoot, 0o700); err != nil {
		return result, err
	}
	cleanupStage := func() bool { return os.RemoveAll(stageRoot) == nil }

	changes := append([]FileChange(nil), prepared.Changes...)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	journal := commitJournal{SchemaVersion: "1.0.0", RequestID: request.RequestID, TargetSnapshotChecksum: prepared.Snapshot.Checksum}
	for _, change := range changes {
		if change.Action == "unchanged" {
			result.FilesWritten = append(result.FilesWritten, change)
			continue
		}
		select {
		case <-ctx.Done():
			result.StagingCleanupCompleted = cleanupStage()
			result.TargetUnchanged = true
			return result, ctx.Err()
		default:
		}
		stagedRelative := filepath.ToSlash(filepath.Join("files", filepath.FromSlash(change.Path)))
		stagedPath := filepath.Join(stageRoot, filepath.FromSlash(stagedRelative))
		if err := writeNewFile(root, stagedPath, change.Bytes, 0o600); err != nil {
			result.StagingCleanupCompleted = cleanupStage()
			result.TargetUnchanged = true
			return result, err
		}
		if actual, err := os.ReadFile(stagedPath); err != nil || !digestEqual(digestBytes(actual), change.SHA256) {
			result.StagingCleanupCompleted = cleanupStage()
			result.TargetUnchanged = true
			return result, ErrCommitFailed
		}
		item := journalItem{Path: change.Path, Ownership: change.Ownership, Action: change.Action, BeforeSHA256: change.PreviousSHA256, AfterSHA256: change.SHA256, StagedPath: stagedRelative}
		if change.Action == "updated" {
			item.BackupPath = filepath.ToSlash(filepath.Join("backups", filepath.FromSlash(change.Path)))
			backupPath := filepath.Join(stageRoot, filepath.FromSlash(item.BackupPath))
			targetPath := filepath.Join(root, filepath.FromSlash(change.Path))
			info, statErr := os.Stat(targetPath)
			if statErr != nil || !info.Mode().IsRegular() || copyErr(root, targetPath, backupPath, info.Mode().Perm()) != nil {
				result.StagingCleanupCompleted = cleanupStage()
				result.TargetUnchanged = true
				return result, ErrCommitFailed
			}
		}
		journal.Changes = append(journal.Changes, item)
	}
	journalRaw, err := json.Marshal(journal)
	if err != nil {
		result.StagingCleanupCompleted = cleanupStage()
		result.TargetUnchanged = true
		return result, ErrCommitFailed
	}
	journalRaw, err = machinecontract.Canonicalize(journalRaw)
	if err != nil || writeNewFile(root, filepath.Join(stageRoot, "commit-journal.json"), append(journalRaw, '\n'), 0o600) != nil {
		result.StagingCleanupCompleted = cleanupStage()
		result.TargetUnchanged = true
		return result, ErrCommitFailed
	}

	applied := make([]appliedChange, 0, len(journal.Changes))
	createdTargetDirectories := make([]string, 0)
	for index, item := range journal.Changes {
		select {
		case <-ctx.Done():
			return c.rollbackAfterFailure(root, stageRoot, result, applied, createdTargetDirectories, ctx.Err())
		default:
		}
		change := changeByPath(changes, item.Path)
		if c.beforeReplace != nil {
			if err := c.beforeReplace(index, change); err != nil {
				return c.rollbackAfterFailure(root, stageRoot, result, applied, createdTargetDirectories, err)
			}
		}
		targetPath := filepath.Join(root, filepath.FromSlash(change.Path))
		created, err := ensureSafeTargetParent(root, targetPath)
		createdTargetDirectories = append(createdTargetDirectories, created...)
		if err != nil {
			return c.rollbackAfterFailure(root, stageRoot, result, applied, createdTargetDirectories, err)
		}
		if err := validateTargetForChange(targetPath, change); err != nil {
			return c.rollbackAfterFailure(root, stageRoot, result, applied, createdTargetDirectories, err)
		}
		stagedPath := filepath.Join(stageRoot, filepath.FromSlash(item.StagedPath))
		mode := os.FileMode(0o644)
		backupPath := ""
		if item.BackupPath != "" {
			backupPath = filepath.Join(stageRoot, filepath.FromSlash(item.BackupPath))
			if info, err := os.Stat(targetPath); err != nil {
				return c.rollbackAfterFailure(root, stageRoot, result, applied, createdTargetDirectories, err)
			} else {
				mode = info.Mode().Perm()
			}
		}
		if err := os.Chmod(stagedPath, mode); err != nil {
			return c.rollbackAfterFailure(root, stageRoot, result, applied, createdTargetDirectories, err)
		}
		if err := replaceFile(stagedPath, targetPath); err != nil {
			return c.rollbackAfterFailure(root, stageRoot, result, applied, createdTargetDirectories, err)
		}
		applied = append(applied, appliedChange{change: change, backupPath: backupPath, mode: mode})
		result.FilesWritten = append(result.FilesWritten, change)
	}
	for _, change := range changes {
		if change.Action == "unchanged" {
			continue
		}
		content, err := readSafeWorkspaceFile(root, change.Path)
		if err != nil || !digestEqual(digestBytes(content), change.SHA256) {
			return c.rollbackAfterFailure(root, stageRoot, result, applied, createdTargetDirectories, ErrCommitFailed)
		}
	}
	result.StagingCleanupCompleted = cleanupStage()
	if !result.StagingCleanupCompleted {
		return result, ErrCommitFailed
	}
	result.AtomicCommitCompleted = true
	result.TargetUnchanged = len(applied) == 0
	return result, nil
}

func (c *FileCommitter) rollbackAfterFailure(root, stageRoot string, result CommitResult, applied []appliedChange, createdDirectories []string, cause error) (CommitResult, error) {
	result.RollbackAttempted = len(applied) != 0 || len(createdDirectories) != 0
	rollbackErr := rollbackApplied(root, applied, createdDirectories)
	result.RollbackCompleted = result.RollbackAttempted && rollbackErr == nil
	result.StagingCleanupCompleted = os.RemoveAll(stageRoot) == nil
	result.AtomicCommitCompleted = false
	result.TargetUnchanged = rollbackErr == nil
	result.FilesWritten = nil
	if rollbackErr != nil {
		return result, errors.Join(ErrCommitFailed, ErrRollbackFailed)
	}
	return result, fmt.Errorf("%w: %v", ErrCommitFailed, cause)
}

func rollbackApplied(root string, applied []appliedChange, createdDirectories []string) error {
	var rollbackErr error
	for index := len(applied) - 1; index >= 0; index-- {
		item := applied[index]
		targetPath := filepath.Join(root, filepath.FromSlash(item.change.Path))
		if item.change.Action == "created" {
			if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
				rollbackErr = errors.Join(rollbackErr, err)
			}
			continue
		}
		if item.backupPath == "" {
			rollbackErr = errors.Join(rollbackErr, ErrRollbackFailed)
			continue
		}
		if err := os.Chmod(item.backupPath, item.mode); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
			continue
		}
		if err := replaceFile(item.backupPath, targetPath); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	for index := len(createdDirectories) - 1; index >= 0; index-- {
		if err := os.Remove(createdDirectories[index]); err != nil && !os.IsNotExist(err) {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	return rollbackErr
}

func validateCommitPaths(request Request, changes []FileChange) error {
	if err := machinecontract.ValidateSafeRelativePath(request.StagingPath); err != nil {
		return ErrInvalidInput
	}
	if err := machinecontract.ValidateSafeRelativePath(request.RollbackPointPath); err != nil {
		return ErrInvalidInput
	}
	if pathsOverlap(request.StagingPath, request.RollbackPointPath) {
		return ErrInvalidInput
	}
	for _, change := range changes {
		if change.Action != "created" && change.Action != "updated" && change.Action != "unchanged" {
			return ErrInvalidInput
		}
		if pathsOverlap(change.Path, request.StagingPath) || pathsOverlap(change.Path, request.RollbackPointPath) {
			return ErrInvalidInput
		}
	}
	return nil
}

func resolveNonexistentPath(root, relative string) (string, error) {
	if err := machinecontract.ValidateSafeRelativePath(relative); err != nil {
		return "", ErrInvalidInput
	}
	full := filepath.Join(root, filepath.FromSlash(relative))
	if err := ensurePathInsideRoot(root, full); err != nil {
		return "", err
	}
	if _, err := os.Lstat(full); err == nil || !os.IsNotExist(err) {
		return "", ErrTargetUnsafe
	}
	return full, nil
}

func createSafeDirectory(root, directory string, mode os.FileMode) error {
	if err := ensurePathInsideRoot(root, directory); err != nil {
		return err
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil {
		return ErrTargetUnsafe
	}
	if relative == "." {
		return nil
	}
	current := root
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		info, statErr := os.Lstat(current)
		switch {
		case statErr == nil:
			unsafe, checkErr := isUnsafeFilesystemEntry(current)
			if checkErr != nil || unsafe || !info.IsDir() {
				return ErrTargetUnsafe
			}
		case os.IsNotExist(statErr):
			if err := os.Mkdir(current, mode); err != nil {
				return fmt.Errorf("%w: create staging directory", ErrCommitFailed)
			}
		default:
			return ErrTargetUnsafe
		}
	}
	return nil
}

func ensureSafeTargetParent(root, target string) ([]string, error) {
	if err := ensurePathInsideRoot(root, target); err != nil {
		return nil, err
	}
	parent := filepath.Dir(target)
	relative, err := filepath.Rel(root, parent)
	if err != nil {
		return nil, ErrTargetUnsafe
	}
	if relative == "." {
		return nil, nil
	}
	current := root
	created := make([]string, 0)
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		info, statErr := os.Lstat(current)
		switch {
		case statErr == nil:
			unsafe, checkErr := isUnsafeFilesystemEntry(current)
			if checkErr != nil || unsafe || !info.IsDir() {
				return created, ErrTargetUnsafe
			}
		case os.IsNotExist(statErr):
			if err := os.Mkdir(current, 0o755); err != nil {
				return created, ErrCommitFailed
			}
			created = append(created, current)
		default:
			return created, ErrTargetUnsafe
		}
	}
	return created, nil
}

func validateTargetForChange(target string, change FileChange) error {
	info, err := os.Lstat(target)
	if change.Action == "created" {
		if os.IsNotExist(err) {
			return nil
		}
		return ErrTargetChanged
	}
	if err != nil || !info.Mode().IsRegular() {
		return ErrTargetChanged
	}
	unsafe, err := isUnsafeFilesystemEntry(target)
	if err != nil || unsafe {
		return ErrTargetUnsafe
	}
	content, err := os.ReadFile(target)
	if err != nil || !digestEqual(digestBytes(content), change.PreviousSHA256) {
		return ErrTargetChanged
	}
	return nil
}

func writeNewFile(root, path string, content []byte, mode os.FileMode) error {
	if err := ensurePathInsideRoot(root, path); err != nil {
		return err
	}
	if err := createSafeDirectory(root, filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("%w: create staged file", ErrCommitFailed)
	}
	written, writeErr := file.Write(content)
	if writeErr == nil && written != len(content) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return fmt.Errorf("%w: write staged file", ErrCommitFailed)
	}
	return nil
}

func copyErr(root, source, target string, mode os.FileMode) error {
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return writeNewFile(root, target, content, mode)
}

func changeByPath(changes []FileChange, path string) FileChange {
	for _, change := range changes {
		if change.Path == path {
			return change
		}
	}
	return FileChange{}
}
