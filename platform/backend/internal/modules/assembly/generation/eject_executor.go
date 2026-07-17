package generation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

// EjectExecutionInput contains the complete, immutable state needed to execute
// an ownership-only eject transition.
type EjectExecutionInput struct {
	TargetRoot       string
	ManifestDocument []byte
	LockDocument     []byte
	OperationID      string
	NewAssemblyID    string
	NewLockID        string
	Paths            []string
	CreatedAt        string
}

// EjectRollbackEvidence restores the predecessor metadata. Eject never changes
// target file bytes, so no content backup is necessary.
type EjectRollbackEvidence struct {
	ManifestDocument       []byte
	LockDocument           []byte
	ManifestChecksum       string
	LockChecksum           string
	TargetSnapshotChecksum string
}

type EjectExecutionOutcome struct {
	Plan             []byte
	TargetSnapshot   TargetSnapshot
	AssemblyManifest []byte
	GeneratedLock    []byte
	ManifestChecksum string
	LockChecksum     string
	RollbackEvidence EjectRollbackEvidence
}

type EjectExecutor struct {
	contracts *machinecontract.Registry
}

func NewEjectExecutor(contracts *machinecontract.Registry) (*EjectExecutor, error) {
	if contracts == nil {
		return nil, ErrInvalidInput
	}
	return &EjectExecutor{contracts: contracts}, nil
}

func (e *EjectExecutor) Execute(ctx context.Context, input EjectExecutionInput) (EjectExecutionOutcome, error) {
	if err := ctx.Err(); err != nil {
		return EjectExecutionOutcome{}, err
	}
	if err := validateEjectExecutionInput(input); err != nil {
		return EjectExecutionOutcome{}, err
	}
	if err := e.contracts.Validate("assembly-manifest", input.ManifestDocument); err != nil {
		return EjectExecutionOutcome{}, fmt.Errorf("%w: source assembly manifest contract: %v", ErrInvalidInput, err)
	}
	if err := e.contracts.Validate("generated-project-lock", input.LockDocument); err != nil {
		return EjectExecutionOutcome{}, fmt.Errorf("%w: source generated lock contract: %v", ErrInvalidInput, err)
	}

	var manifest assemblyManifestDocument
	var lock generatedLockDocument
	if jsonUnmarshalStrict(input.ManifestDocument, &manifest) != nil || jsonUnmarshalStrict(input.LockDocument, &lock) != nil {
		return EjectExecutionOutcome{}, ErrInvalidInput
	}
	if err := validateEjectSourceClosure(input.ManifestDocument, input.LockDocument, manifest, lock); err != nil {
		return EjectExecutionOutcome{}, err
	}

	planRaw, err := BuildEjectPlan(input.TargetRoot, input.LockDocument, input.Paths)
	if err != nil {
		return EjectExecutionOutcome{}, err
	}
	if err := e.contracts.Validate("generator-eject-plan", planRaw); err != nil {
		return EjectExecutionOutcome{}, fmt.Errorf("%w: eject plan contract: %v", ErrInvalidInput, err)
	}
	var plan EjectPlan
	if jsonUnmarshalStrict(planRaw, &plan) != nil {
		return EjectExecutionOutcome{}, ErrInvalidInput
	}
	selected := make(map[string]EjectFile, len(plan.Files))
	for _, file := range plan.Files {
		selected[file.Path] = file
	}
	if err := validateEjectArtifactClosure(manifest, lock); err != nil {
		return EjectExecutionOutcome{}, err
	}

	if err := ctx.Err(); err != nil {
		return EjectExecutionOutcome{}, err
	}
	projectLock, err := DecodeProjectLock(input.LockDocument)
	if err != nil {
		return EjectExecutionOutcome{}, err
	}
	snapshot, err := InspectTarget(input.TargetRoot, projectLock)
	if err != nil {
		return EjectExecutionOutcome{}, err
	}
	if !digestEqual(snapshot.Checksum, plan.TargetSnapshotChecksum) {
		return EjectExecutionOutcome{}, ErrTargetChanged
	}

	successorManifest := manifest
	successorManifest.AssemblyID = input.NewAssemblyID
	successorManifest.RunID = ""
	successorManifest.LifecycleOperationID = input.OperationID
	successorManifest.CreatedAt = input.CreatedAt
	for index := range successorManifest.Outputs {
		if file, ok := selected[successorManifest.Outputs[index].Path]; ok {
			successorManifest.Outputs[index].Ownership = "forked"
			successorManifest.Outputs[index].SHA256 = file.CurrentSHA256
		}
	}
	manifestRaw, manifestChecksum, err := marshalWithEmbeddedDigest(successorManifest, "manifest_checksum")
	if err != nil {
		return EjectExecutionOutcome{}, err
	}
	if err := e.contracts.Validate("assembly-manifest", manifestRaw); err != nil {
		return EjectExecutionOutcome{}, fmt.Errorf("%w: successor assembly manifest contract: %v", ErrInvalidInput, err)
	}

	successorLock := lock
	successorLock.LockID = input.NewLockID
	successorLock.RunID = ""
	successorLock.LifecycleOperationID = input.OperationID
	successorLock.AssemblyManifestChecksum = manifestChecksum
	successorLock.CreatedAt = input.CreatedAt
	for index := range successorLock.Files {
		if file, ok := selected[successorLock.Files[index].Path]; ok {
			successorLock.Files[index].Ownership = "forked"
			successorLock.Files[index].SHA256 = file.CurrentSHA256
			successorLock.Files[index].UpdatePolicy = "diff_only"
		}
	}
	successorProjectLock := projectLock
	successorProjectLock.Files = append([]LockedFile(nil), successorLock.Files...)
	successorSnapshot, err := InspectTarget(input.TargetRoot, successorProjectLock)
	if err != nil {
		return EjectExecutionOutcome{}, err
	}
	successorLock.TargetSnapshotChecksum = successorSnapshot.Checksum
	lockRaw, lockChecksum, err := marshalWithEmbeddedDigest(successorLock, "lock_checksum")
	if err != nil {
		return EjectExecutionOutcome{}, err
	}
	if err := e.contracts.Validate("generated-project-lock", lockRaw); err != nil {
		return EjectExecutionOutcome{}, fmt.Errorf("%w: successor generated lock contract: %v", ErrInvalidInput, err)
	}
	if err := ctx.Err(); err != nil {
		return EjectExecutionOutcome{}, err
	}

	return EjectExecutionOutcome{
		Plan:             append([]byte(nil), planRaw...),
		TargetSnapshot:   cloneTargetSnapshot(successorSnapshot),
		AssemblyManifest: append([]byte(nil), manifestRaw...),
		GeneratedLock:    append([]byte(nil), lockRaw...),
		ManifestChecksum: manifestChecksum,
		LockChecksum:     lockChecksum,
		RollbackEvidence: EjectRollbackEvidence{
			ManifestDocument:       append([]byte(nil), input.ManifestDocument...),
			LockDocument:           append([]byte(nil), input.LockDocument...),
			ManifestChecksum:       manifest.ManifestChecksum,
			LockChecksum:           lock.LockChecksum,
			TargetSnapshotChecksum: snapshot.Checksum,
		},
	}, nil
}

func validateEjectExecutionInput(input EjectExecutionInput) error {
	if input.TargetRoot == "" || len(input.ManifestDocument) == 0 || len(input.LockDocument) == 0 || len(input.Paths) == 0 ||
		!stableIdentifierPattern.MatchString(input.OperationID) || !stableIdentifierPattern.MatchString(input.NewAssemblyID) ||
		!stableIdentifierPattern.MatchString(input.NewLockID) || input.OperationID == input.NewAssemblyID ||
		input.OperationID == input.NewLockID || input.NewAssemblyID == input.NewLockID {
		return ErrInvalidInput
	}
	createdAt, err := time.Parse(time.RFC3339Nano, input.CreatedAt)
	if err != nil || createdAt.Location() != time.UTC {
		return ErrInvalidInput
	}
	return nil
}

func validateEjectSourceClosure(manifestRaw, lockRaw []byte, manifest assemblyManifestDocument, lock generatedLockDocument) error {
	manifestChecksum, err := machinecontract.DigestWithoutTopLevelField(manifestRaw, "manifest_checksum")
	if err != nil || !digestEqual(manifestChecksum, manifest.ManifestChecksum) {
		return ErrInvalidInput
	}
	lockChecksum, err := machinecontract.DigestWithoutTopLevelField(lockRaw, "lock_checksum")
	if err != nil || !digestEqual(lockChecksum, lock.LockChecksum) {
		return ErrInvalidInput
	}
	if !digestEqual(lock.AssemblyManifestChecksum, manifest.ManifestChecksum) ||
		!digestEqual(lock.BlueprintChecksum, manifest.Blueprint.Checksum) ||
		!digestEqual(lock.CatalogChecksum, manifest.CatalogChecksum) || lock.Generator != manifest.Generator {
		return ErrPlanMismatch
	}
	return nil
}

func validateEjectArtifactClosure(manifest assemblyManifestDocument, lock generatedLockDocument) error {
	if len(manifest.Outputs) != len(lock.Files) {
		return ErrPlanMismatch
	}
	outputs := make(map[string]manifestOutput, len(manifest.Outputs))
	for _, output := range manifest.Outputs {
		folded := strings.ToLower(output.Path)
		if _, duplicate := outputs[folded]; duplicate {
			return ErrInvalidInput
		}
		outputs[folded] = output
	}
	for _, file := range lock.Files {
		folded := strings.ToLower(file.Path)
		output, exists := outputs[folded]
		if !exists || output.Path != file.Path || output.Ownership != file.Ownership ||
			!digestEqual(output.SHA256, file.SHA256) || output.SourceID != file.SourceID ||
			output.SourceVersion != file.SourceVersion || output.SourcePath != file.SourcePath ||
			!digestEqual(output.SourceSHA256, file.SourceSHA256) || output.RenderStrategy != file.RenderStrategy ||
			output.ContentType != file.ContentType || !equalMergeSpec(output.Merge, file.Merge) {
			return ErrPlanMismatch
		}
		delete(outputs, folded)
	}
	if len(outputs) != 0 {
		return ErrPlanMismatch
	}
	return nil
}

func equalMergeSpec(left, right *MergeSpec) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func cloneTargetSnapshot(source TargetSnapshot) TargetSnapshot {
	return TargetSnapshot{Files: append([]ExistingFile(nil), source.Files...), Checksum: source.Checksum}
}
