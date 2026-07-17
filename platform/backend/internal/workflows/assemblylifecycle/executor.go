package assemblylifecycle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/assembly/generation"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

var ErrLifecycleFinalizeRetryable = errors.New("assembly lifecycle finalization can be retried")

type GenerationLifecycleExecutor struct {
	resolver  *TrustedContextResolver
	planner   *CatalogLifecyclePlanBuilder
	contracts *machinecontract.Registry
	now       func() time.Time
}

func NewGenerationLifecycleExecutor(resolver *TrustedContextResolver, planner *CatalogLifecyclePlanBuilder, contracts *machinecontract.Registry, now func() time.Time) (*GenerationLifecycleExecutor, error) {
	if resolver == nil || planner == nil || contracts == nil {
		return nil, ErrExecutorUnavailable
	}
	if now == nil {
		now = time.Now
	}
	return &GenerationLifecycleExecutor{resolver: resolver, planner: planner, contracts: contracts, now: now}, nil
}

func (e *GenerationLifecycleExecutor) ExecuteUpgrade(ctx context.Context, operation core.LifecycleOperation, plan core.LifecyclePlan, transition core.LifecycleArtifactTransition) (ExecutionResult, error) {
	if e == nil || transition.OperationID != operation.OperationID || transition.Source != operation.Source {
		return ExecutionResult{}, core.ErrConflict
	}
	resolved, input, previous, err := e.planner.BuildUpgradeInput(ctx, plan, operation)
	resumingCommittedOutput := false
	if err != nil {
		resolved, input, previous, err = e.planner.BuildUpgradeResumeInput(ctx, plan, operation)
		if err != nil {
			return ExecutionResult{}, err
		}
		resumingCommittedOutput = true
	}
	store, err := generation.NewArtifactStore(resolved.Workspace.ArtifactRoot)
	if err != nil {
		return ExecutionResult{}, err
	}
	executor, err := generation.NewExecutor(generation.NewPureRenderer(resolved.Catalog), generation.NewFileCommitter(), store, e.contracts)
	if err != nil {
		return ExecutionResult{}, err
	}
	if operation.Status != core.LifecycleExecuting {
		return ExecutionResult{}, core.ErrConflict
	}
	var outcome generation.ExecutionOutcome
	if resumingCommittedOutput {
		var found bool
		outcome, found, err = executor.Recover(ctx, resolved.Workspace.TargetRoot, input, operation.Source.TargetSnapshotChecksum)
		if errors.Is(err, generation.ErrTargetChanged) {
			return ExecutionResult{}, fmt.Errorf("%w: %w", ErrLifecycleFinalizeRetryable, err)
		}
		if err == nil && !found {
			err = core.ErrConflict
		}
	} else {
		outcome, err = executor.Execute(ctx, resolved.Workspace.TargetRoot, input, resolved.ProjectLock, previous)
	}
	if err != nil {
		return ExecutionResult{}, err
	}
	target, err := e.projectSuccessor(operation, outcome.Bundle.AssemblyManifest, outcome.Bundle.GeneratedLock)
	if err != nil {
		return ExecutionResult{}, err
	}
	if err = publishLifecycleDocuments(store, target.ManifestID, outcome.Bundle.AssemblyManifest, outcome.Bundle.GeneratedLock); err != nil {
		return ExecutionResult{}, err
	}
	journal, err := lifecycleRollbackJournal("upgrade", resolved, input.Request.ArtifactContext.Paths.RollbackPointPath, input.Request.ArtifactContext.Paths.CommitJournalPath)
	if err != nil {
		return ExecutionResult{}, err
	}
	completedAt := e.completedAt(operation)
	return ExecutionResult{
		Target: &target,
		Transition: &core.LifecycleArtifactTransition{
			OperationID: operation.OperationID, Source: operation.Source, Target: &target,
			TargetManifestDocument: outcome.Bundle.AssemblyManifest, TargetLockDocument: outcome.Bundle.GeneratedLock,
			RollbackJournal: journal, CreatedAt: transition.CreatedAt, CompletedAt: &completedAt,
		},
		Reports: []core.RunReport{lifecycleReport("report.lifecycle-upgrade", "lifecycle_upgrade", "Lifecycle upgrade artifacts and target snapshot were committed", target.LockChecksum, completedAt)},
	}, nil
}

func (e *GenerationLifecycleExecutor) ExecuteEject(ctx context.Context, operation core.LifecycleOperation, plan core.LifecyclePlan, transition core.LifecycleArtifactTransition) (ExecutionResult, error) {
	if e == nil || operation.Kind != core.LifecycleEject || transition.OperationID != operation.OperationID || transition.Source != operation.Source {
		return ExecutionResult{}, core.ErrConflict
	}
	resolved, err := e.resolver.ResolveCurrent(ctx, operation.AssemblyID, operation.Source)
	if err != nil {
		return ExecutionResult{}, err
	}
	if err = e.planner.Revalidate(ctx, plan, resolved.Manifest, resolved.Lock); err != nil {
		return ExecutionResult{}, err
	}
	var document struct {
		EjectPaths []string `json:"eject_paths"`
	}
	if json.Unmarshal(plan.Document, &document) != nil || len(document.EjectPaths) == 0 {
		return ExecutionResult{}, core.ErrDocumentInvalid
	}
	ejector, err := generation.NewEjectExecutor(e.contracts)
	if err != nil {
		return ExecutionResult{}, err
	}
	manifestID, lockID := lifecycleArtifactIDs(operation.OperationID)
	outcome, err := ejector.Execute(ctx, generation.EjectExecutionInput{
		TargetRoot: resolved.Workspace.TargetRoot, ManifestDocument: resolved.Manifest.Document, LockDocument: resolved.Lock.Document,
		OperationID: operation.OperationID, NewAssemblyID: manifestID, NewLockID: lockID, Paths: document.EjectPaths,
		CreatedAt: operation.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return ExecutionResult{}, err
	}
	target, err := e.projectSuccessor(operation, outcome.AssemblyManifest, outcome.GeneratedLock)
	if err != nil {
		return ExecutionResult{}, err
	}
	store, err := generation.NewArtifactStore(resolved.Workspace.ArtifactRoot)
	if err != nil {
		return ExecutionResult{}, err
	}
	if err = publishLifecycleDocuments(store, target.ManifestID, outcome.AssemblyManifest, outcome.GeneratedLock); err != nil {
		return ExecutionResult{}, err
	}
	journal, err := lifecycleRollbackJournal("eject", resolved, "", "")
	if err != nil {
		return ExecutionResult{}, err
	}
	completedAt := e.completedAt(operation)
	return ExecutionResult{
		Target: &target,
		Transition: &core.LifecycleArtifactTransition{
			OperationID: operation.OperationID, Source: operation.Source, Target: &target,
			TargetManifestDocument: outcome.AssemblyManifest, TargetLockDocument: outcome.GeneratedLock,
			RollbackJournal: journal, CreatedAt: transition.CreatedAt, CompletedAt: &completedAt,
		},
		Reports: []core.RunReport{lifecycleReport("report.lifecycle-eject", "lifecycle_eject", "Selected generated ownership was transferred without changing file contents", target.LockChecksum, completedAt)},
	}, nil
}

func (e *GenerationLifecycleExecutor) ExecuteRollback(ctx context.Context, operation core.LifecycleOperation, transition core.LifecycleArtifactTransition) (ExecutionResult, error) {
	if e == nil || operation.Kind != core.LifecycleRollback || transition.OperationID != operation.OperationID || transition.Source != operation.Source {
		return ExecutionResult{}, core.ErrConflict
	}
	resolved, err := e.resolver.ResolveCurrent(ctx, operation.AssemblyID, operation.Source)
	if err != nil {
		resolved, err = e.resolver.ResolveForResume(ctx, operation.AssemblyID, operation.Source)
		if err != nil {
			return ExecutionResult{}, err
		}
	}
	journal, err := decodeLifecycleRollbackJournal(transition.RollbackJournal, e.contracts)
	if err != nil {
		return ExecutionResult{}, err
	}
	if journal.Kind == "upgrade" {
		store, storeErr := generation.NewArtifactStore(resolved.Workspace.ArtifactRoot)
		if storeErr != nil {
			return ExecutionResult{}, storeErr
		}
		rollback, rollbackErr := generation.NewRollbackExecutor(store, e.contracts)
		if rollbackErr != nil {
			return ExecutionResult{}, rollbackErr
		}
		if _, rollbackErr = rollback.Rollback(ctx, resolved.Workspace.TargetRoot, journal.RollbackPointPath, journal.CommitJournalPath); rollbackErr != nil {
			return ExecutionResult{}, rollbackErr
		}
	} else if journal.Kind != "eject" {
		return ExecutionResult{}, core.ErrDocumentInvalid
	}
	predecessorLock, err := generation.DecodeProjectLock(journal.SourceLockDocument)
	if err != nil {
		return ExecutionResult{}, err
	}
	snapshot, err := generation.InspectTarget(resolved.Workspace.TargetRoot, predecessorLock)
	if err != nil {
		return ExecutionResult{}, err
	}
	manifestID, lockID := lifecycleArtifactIDs(operation.OperationID)
	manifestRaw, lockRaw, err := reissueLifecycleArtifacts(journal.SourceManifestDocument, journal.SourceLockDocument, operation.OperationID, manifestID, lockID, snapshot, operation.CreatedAt.UTC(), e.contracts)
	if err != nil {
		return ExecutionResult{}, err
	}
	target, err := e.projectSuccessor(operation, manifestRaw, lockRaw)
	if err != nil {
		return ExecutionResult{}, err
	}
	store, err := generation.NewArtifactStore(resolved.Workspace.ArtifactRoot)
	if err != nil {
		return ExecutionResult{}, err
	}
	if err = publishLifecycleDocuments(store, target.ManifestID, manifestRaw, lockRaw); err != nil {
		return ExecutionResult{}, err
	}
	completedAt := e.completedAt(operation)
	return ExecutionResult{
		Target: &target,
		Transition: &core.LifecycleArtifactTransition{
			OperationID: operation.OperationID, Source: operation.Source, Target: &target,
			TargetManifestDocument: manifestRaw, TargetLockDocument: lockRaw,
			RollbackJournal: json.RawMessage(`{"kind":"rollback","state":"completed"}`), CreatedAt: transition.CreatedAt, CompletedAt: &completedAt,
		},
		Reports: []core.RunReport{lifecycleReport("report.lifecycle-rollback", "lifecycle_rollback", "Lifecycle rollback restored the predecessor file and ownership state", target.LockChecksum, completedAt)},
	}, nil
}

func publishLifecycleDocuments(store *generation.ArtifactStore, assemblyID string, manifest, lock json.RawMessage) error {
	err := store.PublishLifecycleDocuments(assemblyID, manifest, lock)
	if err == nil || !errors.Is(err, generation.ErrArtifactStore) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrLifecycleFinalizeRetryable, err)
}

type rollbackJournalDocument struct {
	Kind                   string          `json:"kind"`
	WorkspaceRef           string          `json:"workspace_ref"`
	RollbackPointPath      string          `json:"rollback_point_path,omitempty"`
	CommitJournalPath      string          `json:"commit_journal_path,omitempty"`
	SourceManifestDocument json.RawMessage `json:"source_manifest_document"`
	SourceLockDocument     json.RawMessage `json:"source_lock_document"`
}

func lifecycleRollbackJournal(kind string, resolved ResolvedLifecycleContext, rollbackPointPath, commitJournalPath string) (json.RawMessage, error) {
	value := rollbackJournalDocument{Kind: kind, WorkspaceRef: resolved.Run.OutputTargetRef, RollbackPointPath: rollbackPointPath, CommitJournalPath: commitJournalPath, SourceManifestDocument: resolved.Manifest.Document, SourceLockDocument: resolved.Lock.Document}
	return json.Marshal(value)
}

func decodeLifecycleRollbackJournal(raw json.RawMessage, contracts *machinecontract.Registry) (rollbackJournalDocument, error) {
	var value rollbackJournalDocument
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || (value.Kind != "upgrade" && value.Kind != "eject") || value.WorkspaceRef == "" || len(value.SourceManifestDocument) == 0 || len(value.SourceLockDocument) == 0 {
		return rollbackJournalDocument{}, core.ErrDocumentInvalid
	}
	if value.Kind == "upgrade" && (machinecontract.ValidateSafeRelativePath(value.RollbackPointPath) != nil || machinecontract.ValidateSafeRelativePath(value.CommitJournalPath) != nil) {
		return rollbackJournalDocument{}, core.ErrDocumentInvalid
	}
	if contracts.Validate("assembly-manifest", value.SourceManifestDocument) != nil || contracts.Validate("generated-project-lock", value.SourceLockDocument) != nil {
		return rollbackJournalDocument{}, core.ErrDocumentInvalid
	}
	return value, nil
}

func (e *GenerationLifecycleExecutor) projectSuccessor(operation core.LifecycleOperation, manifestRaw, lockRaw json.RawMessage) (core.LifecycleArtifactState, error) {
	if e.contracts.Validate("assembly-manifest", manifestRaw) != nil || e.contracts.Validate("generated-project-lock", lockRaw) != nil {
		return core.LifecycleArtifactState{}, core.ErrDocumentInvalid
	}
	var manifest struct {
		AssemblyID, LifecycleOperationID, ManifestChecksum string
	}
	var lock struct {
		LockID, LifecycleOperationID, AssemblyManifestChecksum, CatalogChecksum, TargetSnapshotChecksum, LockChecksum string
	}
	var manifestFields map[string]json.RawMessage
	var lockFields map[string]json.RawMessage
	if json.Unmarshal(manifestRaw, &manifestFields) != nil || json.Unmarshal(lockRaw, &lockFields) != nil ||
		decodeStringFields(manifestFields, map[string]*string{"assembly_id": &manifest.AssemblyID, "lifecycle_operation_id": &manifest.LifecycleOperationID, "manifest_checksum": &manifest.ManifestChecksum}) != nil ||
		decodeStringFields(lockFields, map[string]*string{"lock_id": &lock.LockID, "lifecycle_operation_id": &lock.LifecycleOperationID, "assembly_manifest_checksum": &lock.AssemblyManifestChecksum, "catalog_checksum": &lock.CatalogChecksum, "target_snapshot_checksum": &lock.TargetSnapshotChecksum, "lock_checksum": &lock.LockChecksum}) != nil {
		return core.LifecycleArtifactState{}, core.ErrDocumentInvalid
	}
	manifestChecksum, manifestErr := machinecontract.DigestWithoutTopLevelField(manifestRaw, "manifest_checksum")
	lockChecksum, lockErr := machinecontract.DigestWithoutTopLevelField(lockRaw, "lock_checksum")
	if manifestErr != nil || lockErr != nil || manifest.LifecycleOperationID != operation.OperationID || lock.LifecycleOperationID != operation.OperationID || !equalDigest(manifest.ManifestChecksum, manifestChecksum) || !equalDigest(lock.LockChecksum, lockChecksum) || !equalDigest(lock.AssemblyManifestChecksum, manifestChecksum) {
		return core.LifecycleArtifactState{}, core.ErrDocumentInvalid
	}
	return core.LifecycleArtifactState{ManifestID: manifest.AssemblyID, ManifestChecksum: manifestChecksum, LockID: lock.LockID, LockChecksum: lockChecksum, CatalogChecksum: lock.CatalogChecksum, TargetSnapshotChecksum: lock.TargetSnapshotChecksum}, nil
}

func reissueLifecycleArtifacts(sourceManifest, sourceLock json.RawMessage, operationID, manifestID, lockID string, snapshot generation.TargetSnapshot, createdAt time.Time, contracts *machinecontract.Registry) (json.RawMessage, json.RawMessage, error) {
	if contracts.Validate("assembly-manifest", sourceManifest) != nil || contracts.Validate("generated-project-lock", sourceLock) != nil {
		return nil, nil, core.ErrDocumentInvalid
	}
	var manifest map[string]any
	var lock map[string]any
	if json.Unmarshal(sourceManifest, &manifest) != nil || json.Unmarshal(sourceLock, &lock) != nil {
		return nil, nil, core.ErrDocumentInvalid
	}
	locked, err := generation.DecodeProjectLock(sourceLock)
	if err != nil {
		return nil, nil, core.ErrDocumentInvalid
	}
	current := make(map[string]generation.ExistingFile, len(snapshot.Files))
	for _, file := range snapshot.Files {
		current[strings.ToLower(file.Path)] = file
	}
	outputs, ok := manifest["outputs"].([]any)
	if !ok || len(outputs) != len(locked.Files) {
		return nil, nil, core.ErrDocumentInvalid
	}
	outputsByPath := make(map[string]map[string]any, len(outputs))
	for _, value := range outputs {
		output, valid := value.(map[string]any)
		outputPath, pathValid := output["path"].(string)
		if !valid || !pathValid || outputPath == "" || outputsByPath[strings.ToLower(outputPath)] != nil {
			return nil, nil, core.ErrDocumentInvalid
		}
		outputsByPath[strings.ToLower(outputPath)] = output
	}
	for index := range locked.Files {
		file := &locked.Files[index]
		actual, ok := current[strings.ToLower(file.Path)]
		output := outputsByPath[strings.ToLower(file.Path)]
		outputOwnership, ownershipOK := output["ownership"].(string)
		if !ok || output == nil || !ownershipOK || outputOwnership != file.Ownership {
			return nil, nil, generation.ErrOwnershipConflict
		}
		switch file.Ownership {
		case "generated", "integration":
			if !equalDigest(actual.SHA256, file.SHA256) {
				return nil, nil, generation.ErrOwnershipConflict
			}
		case "custom", "forked":
			file.SHA256 = actual.SHA256
			output["sha256"] = actual.SHA256
		default:
			return nil, nil, core.ErrDocumentInvalid
		}
	}
	lock["files"] = locked.Files
	delete(manifest, "run_id")
	manifest["assembly_id"] = manifestID
	manifest["lifecycle_operation_id"] = operationID
	manifest["created_at"] = createdAt.Format(time.RFC3339Nano)
	manifest["manifest_checksum"] = "sha256:" + strings.Repeat("0", 64)
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		return nil, nil, err
	}
	manifestChecksum, err := machinecontract.DigestWithoutTopLevelField(manifestRaw, "manifest_checksum")
	if err != nil {
		return nil, nil, err
	}
	manifest["manifest_checksum"] = manifestChecksum
	manifestRaw, err = json.Marshal(manifest)
	if err != nil {
		return nil, nil, err
	}
	lock["lock_id"] = lockID
	delete(lock, "run_id")
	lock["lifecycle_operation_id"] = operationID
	lock["assembly_manifest_checksum"] = manifestChecksum
	lock["target_snapshot_checksum"] = snapshot.Checksum
	lock["created_at"] = createdAt.Format(time.RFC3339Nano)
	lock["lock_checksum"] = "sha256:" + strings.Repeat("0", 64)
	lockRaw, err := json.Marshal(lock)
	if err != nil {
		return nil, nil, err
	}
	lockChecksum, err := machinecontract.DigestWithoutTopLevelField(lockRaw, "lock_checksum")
	if err != nil {
		return nil, nil, err
	}
	lock["lock_checksum"] = lockChecksum
	lockRaw, err = json.Marshal(lock)
	if err != nil || contracts.Validate("assembly-manifest", manifestRaw) != nil || contracts.Validate("generated-project-lock", lockRaw) != nil {
		return nil, nil, core.ErrDocumentInvalid
	}
	return manifestRaw, lockRaw, nil
}

func lifecycleArtifactIDs(operationID string) (string, string) {
	digest := sha256.Sum256([]byte(operationID + "\x00lifecycle-artifacts"))
	seed := hex.EncodeToString(digest[:])[:24]
	return "assembly." + seed, "lock." + seed
}

func lifecycleReport(id, reportType, summary, checksum string, createdAt time.Time) core.RunReport {
	return core.RunReport{ReportID: id, ReportType: reportType, Status: "passed", Summary: summary, Checksum: checksum, CreatedAt: createdAt}
}

func (e *GenerationLifecycleExecutor) completedAt(operation core.LifecycleOperation) time.Time {
	value := e.now().UTC()
	if !value.After(operation.UpdatedAt) {
		value = operation.UpdatedAt.Add(time.Nanosecond)
	}
	return value
}

func decodeStringFields(values map[string]json.RawMessage, targets map[string]*string) error {
	for key, target := range targets {
		raw, ok := values[key]
		if !ok || json.Unmarshal(raw, target) != nil || *target == "" {
			return core.ErrDocumentInvalid
		}
	}
	return nil
}
