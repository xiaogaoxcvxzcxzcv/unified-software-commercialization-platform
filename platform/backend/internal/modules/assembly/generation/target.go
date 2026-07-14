package generation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

func InspectTarget(root string, previous ProjectLock) (TargetSnapshot, error) {
	root, err := validateWorkspaceRoot(root)
	if err != nil {
		return TargetSnapshot{}, err
	}
	locked, err := lockedFilesByPath(previous.Files)
	if err != nil {
		return TargetSnapshot{}, err
	}
	files := make([]ExistingFile, 0)
	seen := map[string]string{}
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("%w: inspect target", ErrTargetUnsafe)
		}
		unsafe, checkErr := isUnsafeFilesystemEntry(current)
		if checkErr != nil || unsafe {
			return fmt.Errorf("%w: linked or unreadable target entry", ErrTargetUnsafe)
		}
		if current == root || entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%w: special target entry", ErrTargetUnsafe)
		}
		relative, relErr := filepath.Rel(root, current)
		if relErr != nil {
			return fmt.Errorf("%w: relative target path", ErrTargetUnsafe)
		}
		relative = filepath.ToSlash(relative)
		if err := machinecontract.ValidateSafeRelativePath(relative); err != nil {
			return fmt.Errorf("%w: unsafe target path", ErrTargetUnsafe)
		}
		folded := strings.ToLower(relative)
		if existing := seen[folded]; existing != "" && existing != relative {
			return fmt.Errorf("%w: case-folded target collision", ErrTargetUnsafe)
		}
		seen[folded] = relative
		content, readErr := os.ReadFile(current)
		if readErr != nil {
			return fmt.Errorf("%w: unreadable target file", ErrTargetUnsafe)
		}
		ownership := "custom"
		if value, ok := locked[folded]; ok {
			if value.Path != relative {
				return fmt.Errorf("%w: lock path case drift", ErrTargetUnsafe)
			}
			ownership = value.Ownership
		}
		files = append(files, ExistingFile{Path: relative, Ownership: ownership, SHA256: digestBytes(content)})
		return nil
	})
	if err != nil {
		return TargetSnapshot{}, err
	}
	sortExistingFiles(files)
	checksum, err := snapshotChecksum(files)
	if err != nil {
		return TargetSnapshot{}, err
	}
	return TargetSnapshot{Files: files, Checksum: checksum}, nil
}

func PrepareTarget(root string, request Request, rendered Result, previous ProjectLock) (PreparedChangeSet, error) {
	snapshot, err := InspectTarget(root, previous)
	if err != nil {
		return PreparedChangeSet{}, err
	}
	prepared := PreparedChangeSet{Snapshot: snapshot}
	if err := validateFileRequest(request, snapshot); err != nil {
		prepared.Diagnostics = append(prepared.Diagnostics, diagnosticForTargetChange(snapshot, request))
		return prepared, err
	}
	locked, err := lockedFilesByPath(previous.Files)
	if err != nil {
		return PreparedChangeSet{}, err
	}
	existing := existingFilesByPath(snapshot.Files)
	renderedByPath := make(map[string]RenderedFile, len(rendered.Files))
	for _, file := range rendered.Files {
		folded := strings.ToLower(file.Path)
		if _, duplicate := renderedByPath[folded]; duplicate {
			return PreparedChangeSet{}, ErrDuplicateOutput
		}
		renderedByPath[folded] = file
	}
	if len(renderedByPath) != len(request.DesiredOutputs) {
		return PreparedChangeSet{}, ErrPlanMismatch
	}

	desired := make(map[string]struct{}, len(request.DesiredOutputs))
	var firstConflict error
	for _, output := range request.DesiredOutputs {
		folded := strings.ToLower(output.Path)
		desired[folded] = struct{}{}
		file, ok := renderedByPath[folded]
		if !ok || !outputSetsEqual([]OutputSpec{file.OutputSpec}, []OutputSpec{output}) {
			return PreparedChangeSet{}, ErrPlanMismatch
		}
		if conflictPath := overlappingProtectedPath(output.Path, request.ProtectedPaths); conflictPath != "" {
			prepared.Diagnostics = append(prepared.Diagnostics, ownershipDiagnostic(
				"GENERATOR_PROTECTED_PATH", output.Path, conflictPath, output.Ownership, "custom", "output overlaps a protected product path",
			))
			firstConflict = firstError(firstConflict, ErrOwnershipConflict)
			continue
		}
		if conflictPath := overlappingExistingPath(output.Path, snapshot.Files); conflictPath != "" && !strings.EqualFold(conflictPath, output.Path) {
			prepared.Diagnostics = append(prepared.Diagnostics, ownershipDiagnostic(
				"GENERATOR_PATH_OVERLAP", output.Path, conflictPath, output.Ownership, existing[strings.ToLower(conflictPath)].Ownership, "output overlaps an existing product path",
			))
			firstConflict = firstError(firstConflict, ErrOwnershipConflict)
			continue
		}
		current, exists := existing[folded]
		baseline, wasLocked := locked[folded]
		switch {
		case wasLocked && baseline.Ownership == "forked":
			if exists {
				prepared.Preserved = append(prepared.Preserved, current)
			}
		case exists && current.Ownership == "custom":
			prepared.Diagnostics = append(prepared.Diagnostics, ownershipDiagnostic(
				"GENERATOR_CUSTOM_COLLISION", output.Path, output.Path, output.Ownership, "custom", "output would overwrite an unknown or custom file",
			))
			firstConflict = firstError(firstConflict, ErrOwnershipConflict)
		case output.Ownership == "generated":
			change, diagnostic, changeErr := prepareGeneratedChange(file, current, exists, baseline, wasLocked)
			if diagnostic != nil {
				prepared.Diagnostics = append(prepared.Diagnostics, *diagnostic)
				firstConflict = firstError(firstConflict, changeErr)
				continue
			}
			prepared.Changes = append(prepared.Changes, change)
		case output.Ownership == "integration":
			change, diagnostic, changeErr := prepareIntegrationChange(root, file, current, exists, baseline, wasLocked)
			if diagnostic != nil {
				prepared.Diagnostics = append(prepared.Diagnostics, *diagnostic)
				firstConflict = firstError(firstConflict, changeErr)
				continue
			}
			prepared.Changes = append(prepared.Changes, change)
		default:
			return PreparedChangeSet{}, ErrInvalidInput
		}
	}

	for folded, baseline := range locked {
		if baseline.Ownership != "generated" && baseline.Ownership != "integration" {
			continue
		}
		if _, selected := desired[folded]; selected {
			continue
		}
		prepared.Diagnostics = append(prepared.Diagnostics, Diagnostic{
			Code: "GENERATOR_MANAGED_REMOVAL_UNPLANNED", Category: "ownership", Message: "a previously managed file is absent from desired outputs",
			Path: baseline.Path, ExpectedOwnership: baseline.Ownership, ActualOwnership: baseline.Ownership,
			Remediation: []string{"create an explicit removal plan before changing the managed file set"},
		})
		firstConflict = firstError(firstConflict, ErrOwnershipConflict)
	}

	for _, current := range snapshot.Files {
		if _, selected := desired[strings.ToLower(current.Path)]; selected {
			continue
		}
		if current.Ownership == "custom" || current.Ownership == "forked" {
			prepared.Preserved = append(prepared.Preserved, current)
		}
	}
	sort.Slice(prepared.Changes, func(i, j int) bool { return prepared.Changes[i].Path < prepared.Changes[j].Path })
	sortExistingFiles(prepared.Preserved)
	if firstConflict != nil {
		return prepared, firstConflict
	}
	return prepared, nil
}

func prepareGeneratedChange(file RenderedFile, current ExistingFile, exists bool, baseline LockedFile, wasLocked bool) (FileChange, *Diagnostic, error) {
	if exists {
		if !wasLocked || baseline.Ownership != "generated" {
			diagnostic := ownershipDiagnostic("GENERATOR_OWNERSHIP_MISMATCH", file.Path, file.Path, "generated", current.Ownership, "generated output has no matching lock baseline")
			return FileChange{}, &diagnostic, ErrOwnershipConflict
		}
		if !digestEqual(current.SHA256, baseline.SHA256) {
			diagnostic := checksumDiagnostic("GENERATOR_GENERATED_MODIFIED", file.Path, baseline.SHA256, current.SHA256, "generated file was modified after the last lock")
			return FileChange{}, &diagnostic, ErrGeneratedModified
		}
		action := "updated"
		if digestEqual(current.SHA256, file.SHA256) {
			action = "unchanged"
		}
		return FileChange{Path: file.Path, Ownership: "generated", Action: action, Bytes: append([]byte(nil), file.Bytes...), SHA256: file.SHA256, GeneratedSHA256: file.GeneratedSHA256, PreviousSHA256: current.SHA256}, nil, nil
	}
	if wasLocked {
		diagnostic := checksumDiagnostic("GENERATOR_MANAGED_FILE_MISSING", file.Path, baseline.SHA256, digestBytes(nil), "locked generated file is missing")
		return FileChange{}, &diagnostic, ErrGeneratedModified
	}
	return FileChange{Path: file.Path, Ownership: "generated", Action: "created", Bytes: append([]byte(nil), file.Bytes...), SHA256: file.SHA256, GeneratedSHA256: file.GeneratedSHA256}, nil, nil
}

func prepareIntegrationChange(root string, file RenderedFile, current ExistingFile, exists bool, baseline LockedFile, wasLocked bool) (FileChange, *Diagnostic, error) {
	if !exists {
		if wasLocked {
			diagnostic := checksumDiagnostic("GENERATOR_INTEGRATION_FILE_MISSING", file.Path, baseline.SHA256, digestBytes(nil), "locked integration file is missing")
			return FileChange{}, &diagnostic, ErrIntegrationRegion
		}
		merged, generatedDigest, err := createIntegrationFile(file.Bytes, file.Merge)
		if err != nil {
			return FileChange{}, nil, err
		}
		return FileChange{Path: file.Path, Ownership: "integration", Action: "created", Bytes: merged, SHA256: digestBytes(merged), GeneratedSHA256: generatedDigest}, nil, nil
	}
	if !wasLocked || baseline.Ownership != "integration" || baseline.Merge == nil || file.Merge == nil || *baseline.Merge != *file.Merge {
		diagnostic := ownershipDiagnostic("GENERATOR_INTEGRATION_BASELINE_MISSING", file.Path, file.Path, "integration", current.Ownership, "integration output has no matching generated-region baseline")
		return FileChange{}, &diagnostic, ErrOwnershipConflict
	}
	content, err := readSafeWorkspaceFile(root, file.Path)
	if err != nil || !digestEqual(digestBytes(content), current.SHA256) {
		diagnostic := checksumDiagnostic("GENERATOR_TARGET_CHANGED", file.Path, current.SHA256, digestBytes(content), "integration file changed during preparation")
		return FileChange{}, &diagnostic, ErrTargetChanged
	}
	merged, currentGeneratedDigest, generatedDigest, err := mergeIntegrationRegion(content, file.Bytes, file.Merge)
	if err != nil || !digestEqual(currentGeneratedDigest, baseline.GeneratedSHA256) {
		diagnostic := checksumDiagnostic("GENERATOR_INTEGRATION_REGION_MODIFIED", file.Path, baseline.GeneratedSHA256, currentGeneratedDigest, "integration generated region was modified or is structurally invalid")
		return FileChange{}, &diagnostic, ErrIntegrationRegion
	}
	action := "updated"
	mergedDigest := digestBytes(merged)
	if digestEqual(mergedDigest, current.SHA256) {
		action = "unchanged"
	}
	return FileChange{Path: file.Path, Ownership: "integration", Action: action, Bytes: merged, SHA256: mergedDigest, GeneratedSHA256: generatedDigest, PreviousSHA256: current.SHA256}, nil, nil
}

func validateFileRequest(request Request, snapshot TargetSnapshot) error {
	if request.SchemaVersion != "1.0.0" || request.ConflictPolicy != "stop" ||
		request.Determinism != (Determinism{Timezone: "UTC", Locale: "C", SortOrder: "bytewise"}) ||
		!stableIdentifierPattern.MatchString(request.RequestID) || !stableIdentifierPattern.MatchString(request.WorkspaceRef) ||
		!validDigest(request.PlanChecksum) || !validDigest(request.TargetSnapshotChecksum) ||
		request.StagingPath == "" || request.RollbackPointPath == "" || len(request.DesiredOutputs) == 0 ||
		(request.Operation != "generate" && request.Operation != "upgrade" && request.Operation != "validate") {
		return ErrInvalidInput
	}
	if err := machinecontract.ValidateSafeRelativePath(request.Inputs.BlueprintPath); err != nil {
		return ErrInvalidInput
	}
	if err := machinecontract.ValidateSafeRelativePath(request.Inputs.PlanPath); err != nil {
		return ErrInvalidInput
	}
	if request.Operation == "upgrade" {
		if err := machinecontract.ValidateSafeRelativePath(request.Inputs.PreviousManifestPath); err != nil {
			return ErrInvalidInput
		}
		if err := machinecontract.ValidateSafeRelativePath(request.Inputs.PreviousLockPath); err != nil {
			return ErrInvalidInput
		}
	}
	if err := machinecontract.ValidateSafeRelativePath(request.StagingPath); err != nil {
		return ErrInvalidInput
	}
	if err := machinecontract.ValidateSafeRelativePath(request.RollbackPointPath); err != nil {
		return ErrInvalidInput
	}
	if pathsOverlap(request.StagingPath, request.RollbackPointPath) {
		return ErrInvalidInput
	}
	if !digestEqual(request.TargetSnapshotChecksum, snapshot.Checksum) {
		return ErrTargetChanged
	}
	expected := append([]ExistingFile{}, request.ExistingFiles...)
	sortExistingFiles(expected)
	if len(expected) != len(snapshot.Files) {
		return ErrTargetChanged
	}
	for index := range expected {
		if expected[index] != snapshot.Files[index] {
			return ErrTargetChanged
		}
	}
	for _, path := range request.ProtectedPaths {
		if err := machinecontract.ValidateSafeRelativePath(path); err != nil {
			return ErrInvalidInput
		}
		if pathsOverlap(path, request.StagingPath) || pathsOverlap(path, request.RollbackPointPath) {
			return ErrInvalidInput
		}
	}
	for _, output := range request.DesiredOutputs {
		if err := validateOutput(output); err != nil {
			return err
		}
		if pathsOverlap(output.Path, request.StagingPath) || pathsOverlap(output.Path, request.RollbackPointPath) {
			return ErrInvalidInput
		}
	}
	return nil
}

func VerifyPreparedSnapshot(root string, expected TargetSnapshot) error {
	actual, err := inspectWithKnownOwnership(root, expected.Files)
	if err != nil {
		return err
	}
	if !digestEqual(actual.Checksum, expected.Checksum) {
		return ErrTargetChanged
	}
	return nil
}

func inspectWithKnownOwnership(root string, expected []ExistingFile) (TargetSnapshot, error) {
	lock := ProjectLock{Files: make([]LockedFile, 0, len(expected))}
	for _, file := range expected {
		lock.Files = append(lock.Files, LockedFile{Path: file.Path, Ownership: file.Ownership, SHA256: file.SHA256, UpdatePolicy: updatePolicy(file.Ownership)})
	}
	actual, err := InspectTarget(root, lock)
	if err != nil {
		return TargetSnapshot{}, err
	}
	if len(actual.Files) != len(expected) {
		return TargetSnapshot{}, ErrTargetChanged
	}
	for index := range actual.Files {
		if actual.Files[index] != expected[index] {
			return TargetSnapshot{}, ErrTargetChanged
		}
	}
	return actual, nil
}

func validateWorkspaceRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", ErrTargetUnsafe
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", ErrTargetUnsafe
	}
	for current := abs; ; current = filepath.Dir(current) {
		unsafe, checkErr := isUnsafeFilesystemEntry(current)
		if checkErr != nil || unsafe {
			return "", ErrTargetUnsafe
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return filepath.Clean(abs), nil
}

func readSafeWorkspaceFile(root, relative string) ([]byte, error) {
	if err := machinecontract.ValidateSafeRelativePath(relative); err != nil {
		return nil, ErrTargetUnsafe
	}
	full := filepath.Join(root, filepath.FromSlash(relative))
	if err := ensurePathInsideRoot(root, full); err != nil {
		return nil, err
	}
	for current := full; current != root; current = filepath.Dir(current) {
		unsafe, err := isUnsafeFilesystemEntry(current)
		if err != nil || unsafe {
			return nil, ErrTargetUnsafe
		}
	}
	return os.ReadFile(full)
}

func ensurePathInsideRoot(root, candidate string) error {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return ErrTargetUnsafe
	}
	return nil
}

func lockedFilesByPath(files []LockedFile) (map[string]LockedFile, error) {
	values := make(map[string]LockedFile, len(files))
	for _, file := range files {
		if err := machinecontract.ValidateSafeRelativePath(file.Path); err != nil || !validOwnership(file.Ownership) || file.SHA256 == "" {
			return nil, ErrInvalidInput
		}
		folded := strings.ToLower(file.Path)
		if _, duplicate := values[folded]; duplicate {
			return nil, ErrInvalidInput
		}
		values[folded] = file
	}
	return values, nil
}

func existingFilesByPath(files []ExistingFile) map[string]ExistingFile {
	values := make(map[string]ExistingFile, len(files))
	for _, file := range files {
		values[strings.ToLower(file.Path)] = file
	}
	return values
}

func snapshotChecksum(files []ExistingFile) (string, error) {
	raw, err := json.Marshal(files)
	if err != nil {
		return "", err
	}
	canonical, err := machinecontract.Canonicalize(raw)
	if err != nil {
		return "", err
	}
	return digestBytes(canonical), nil
}

func sortExistingFiles(files []ExistingFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}

func overlappingProtectedPath(output string, protected []string) string {
	for _, path := range protected {
		if pathsOverlap(output, path) {
			return path
		}
	}
	return ""
}

func overlappingExistingPath(output string, existing []ExistingFile) string {
	for _, file := range existing {
		if pathsOverlap(output, file.Path) {
			return file.Path
		}
	}
	return ""
}

func pathsOverlap(left, right string) bool {
	left, right = strings.ToLower(left), strings.ToLower(right)
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func validOwnership(value string) bool {
	return value == "generated" || value == "integration" || value == "custom" || value == "forked"
}

func updatePolicy(ownership string) string {
	switch ownership {
	case "generated":
		return "replace_generated"
	case "integration":
		return "merge_integration"
	case "forked":
		return "diff_only"
	default:
		return "preserve_custom"
	}
}

func firstError(current, candidate error) error {
	if current == nil {
		return candidate
	}
	if errors.Is(candidate, ErrGeneratedModified) || errors.Is(candidate, ErrIntegrationRegion) {
		return candidate
	}
	return current
}

func ownershipDiagnostic(code, path, relatedPath, expected, actual, message string) Diagnostic {
	return Diagnostic{
		Code: code, Category: "ownership", Message: message, Path: path, RelatedPaths: []string{relatedPath},
		ExpectedOwnership: expected, ActualOwnership: actual,
		Remediation: []string{"move product-owned content away from the managed path or create an explicit eject/migration plan"},
	}
}

func checksumDiagnostic(code, path, expected, actual, message string) Diagnostic {
	return Diagnostic{
		Code: code, Category: "checksum", Message: message, Path: path, ExpectedSHA256: expected, ActualSHA256: actual,
		Remediation: []string{"restore the locked baseline or eject the file before regenerating"},
	}
}

func diagnosticForTargetChange(snapshot TargetSnapshot, request Request) Diagnostic {
	return Diagnostic{
		Code: "GENERATOR_TARGET_SNAPSHOT_CHANGED", Category: "conflict", Message: "the target no longer matches the inspected snapshot",
		ExpectedSHA256: request.TargetSnapshotChecksum, ActualSHA256: snapshot.Checksum,
		Remediation: []string{"inspect the target again and create a new generator request"}, Retryable: true,
	}
}
