package core

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

var ErrLifecycleUnavailable = errors.New("assembly lifecycle service is unavailable")

type LifecycleKind string

const (
	LifecycleUpgrade  LifecycleKind = "upgrade"
	LifecycleEject    LifecycleKind = "eject"
	LifecycleRollback LifecycleKind = "rollback"
)

type LifecycleStatus string

const (
	LifecyclePlanned        LifecycleStatus = "planned"
	LifecycleExecuting      LifecycleStatus = "executing"
	LifecycleCompleted      LifecycleStatus = "completed"
	LifecycleFailed         LifecycleStatus = "failed"
	LifecycleCancelled      LifecycleStatus = "cancelled"
	LifecycleRollingBack    LifecycleStatus = "rolling_back"
	LifecycleRolledBack     LifecycleStatus = "rolled_back"
	LifecycleRollbackFailed LifecycleStatus = "rollback_failed"
)

type LifecycleVersionRef struct {
	ID, Version string
}

type LifecycleTargetVersions struct {
	Packages, Templates, SDKs []LifecycleVersionRef
	Generator                 LifecycleVersionRef
}

type LifecycleArtifactState struct {
	ManifestID             string `json:"manifest_id"`
	ManifestChecksum       string `json:"manifest_checksum"`
	LockID                 string `json:"lock_id"`
	LockChecksum           string `json:"lock_checksum"`
	CatalogChecksum        string `json:"catalog_checksum"`
	TargetSnapshotChecksum string `json:"target_snapshot_checksum"`
}

type LifecycleChange struct {
	Path, Action, Ownership, BeforeChecksum, AfterChecksum, SourceID, SourceVersion string
}

type LifecycleConflict struct {
	ConflictID, Code, Category, Message string
	Blocking                            bool
	Paths, Remediation                  []string
}

type LifecycleMigration struct {
	MigrationID, Kind, Reversibility, Summary string
}

type LifecycleRollbackPolicy struct {
	Strategy                                             string
	Automatic                                            bool
	PredecessorManifestChecksum, PredecessorLockChecksum string
}

type LifecyclePlan struct {
	LifecyclePlanID, AssemblyID, ProductID string
	Operation                              LifecycleKind
	Version                                int64
	SchemaVersion                          string
	Document                               json.RawMessage
	Source                                 LifecycleArtifactState
	TargetSnapshotChecksum                 string
	Changes                                []LifecycleChange
	Migrations                             []LifecycleMigration
	Conflicts                              []LifecycleConflict
	RegressionTests, Statements            []string
	Rollback                               LifecycleRollbackPolicy
	BlockingConflictCount                  int
	Executable                             bool
	ConfirmationChecksum, PlanChecksum     string
	CreatedBy                              string
	CreatedAt                              time.Time
	AuditID                                string
}

type LifecycleRecovery struct {
	Retryable         bool `json:"retryable"`
	RollbackAvailable bool `json:"rollback_available"`
	CancelAllowed     bool `json:"cancel_allowed"`
}

type LifecycleOperation struct {
	OperationID, RootOperationID, RollbackOfOperationID, LifecyclePlanID string
	AssemblyID, ProductID                                                string
	Kind                                                                 LifecycleKind
	Version                                                              int64
	Status                                                               LifecycleStatus
	CurrentStep                                                          string
	SchemaVersion                                                        string
	Document                                                             json.RawMessage
	Source                                                               LifecycleArtifactState
	Target                                                               *LifecycleArtifactState
	Recovery                                                             LifecycleRecovery
	Diagnostics                                                          []RunDiagnostic
	Reports                                                              []RunReport
	OperationChecksum, IdempotencyKeyDigest, CreatedBy, AuditID          string
	CreatedAt, UpdatedAt                                                 time.Time
	CompletedAt                                                          *time.Time
}

type LifecycleArtifactTransition struct {
	OperationID                                string
	Source                                     LifecycleArtifactState
	TargetManifestDocument, TargetLockDocument json.RawMessage
	Target                                     *LifecycleArtifactState
	RollbackJournal                            json.RawMessage
	CreatedAt                                  time.Time
	CompletedAt                                *time.Time
}

type LifecycleDispatch struct {
	OperationID, RootOperationID, CreatedBy string
	Kind                                    LifecycleKind
	AttemptCount                            int
}

type CreateLifecyclePlanRecord struct {
	Plan        LifecyclePlan
	Idempotency Idempotency
	Event       OutboxEvent
}

type StartLifecycleOperationRecord struct {
	Operation   LifecycleOperation
	Idempotency Idempotency
	Event       OutboxEvent
	Transition  LifecycleArtifactTransition
}

type UpdateLifecycleOperationRecord struct {
	Operation       LifecycleOperation
	ExpectedVersion int64
	Diagnostics     []RunDiagnostic
	Reports         []RunReport
	Transition      *LifecycleArtifactTransition
	Event           OutboxEvent
}

type CancelRunRecord struct {
	Run             Run
	ExpectedVersion int64
	Idempotency     Idempotency
	Event           OutboxEvent
}

type LifecycleRepository interface {
	GetLifecycleSource(context.Context, string) (Manifest, GeneratedProjectLock, error)
	GetCurrentLock(context.Context, string, string) (GeneratedProjectLock, error)
	CreateLifecyclePlan(context.Context, CreateLifecyclePlanRecord) (LifecyclePlan, error)
	GetLifecyclePlan(context.Context, string) (LifecyclePlan, error)
	StartLifecycleOperation(context.Context, StartLifecycleOperationRecord) (LifecycleOperation, error)
	GetLifecycleOperation(context.Context, string) (LifecycleOperation, error)
	GetLifecycleTransition(context.Context, string) (LifecycleArtifactTransition, error)
	CancelLifecycleOperation(context.Context, UpdateLifecycleOperationRecord, Idempotency) (LifecycleOperation, error)
	StartRollbackOperation(context.Context, StartLifecycleOperationRecord, int64) (LifecycleOperation, error)
	CancelRun(context.Context, CancelRunRecord) (Run, error)
}

type LifecycleWorkerRepository interface {
	ClaimLifecycleDispatch(context.Context, string, time.Time, time.Duration) (LifecycleDispatch, error)
	RenewLifecycleDispatch(context.Context, string, string, time.Time, time.Duration) error
	CompleteLifecycleDispatch(context.Context, string, string, time.Time) error
	RequeueLifecycleDispatch(context.Context, string, string, string, time.Time, time.Time, bool) error
	UpdateLifecycleOperation(context.Context, UpdateLifecycleOperationRecord) (LifecycleOperation, error)
}

type LifecyclePlanBuilder interface {
	BuildUpgradePlan(context.Context, string, Manifest, GeneratedProjectLock, LifecycleTargetVersions) (json.RawMessage, error)
	BuildEjectPlan(context.Context, string, Manifest, GeneratedProjectLock, []string) (json.RawMessage, error)
	Revalidate(context.Context, LifecyclePlan, Manifest, GeneratedProjectLock) error
}

type LifecycleService struct {
	repository  LifecycleRepository
	validator   DocumentValidator
	planner     LifecyclePlanBuilder
	idGenerator IDGenerator
	now         func() time.Time
}

func NewLifecycleService(repository LifecycleRepository, validator DocumentValidator, planner LifecyclePlanBuilder, idGenerator IDGenerator, now func() time.Time) *LifecycleService {
	if now == nil {
		now = time.Now
	}
	return &LifecycleService{repository: repository, validator: validator, planner: planner, idGenerator: idGenerator, now: now}
}

type CreateUpgradePlanCommand struct {
	AssemblyID, ExpectedManifestChecksum, ExpectedLockChecksum, ActorID, IdempotencyKey, TraceID string
	Target                                                                                       LifecycleTargetVersions
}

type CreateEjectPlanCommand struct {
	AssemblyID, ExpectedManifestChecksum, ExpectedLockChecksum, ActorID, IdempotencyKey, TraceID string
	Paths                                                                                        []string
}

func (s *LifecycleService) CreateUpgradePlan(ctx context.Context, command CreateUpgradePlanCommand) (LifecyclePlan, error) {
	if !s.ready() || command.AssemblyID == "" || !digestPattern.MatchString(command.ExpectedManifestChecksum) || !digestPattern.MatchString(command.ExpectedLockChecksum) || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" || !validTargetVersions(command.Target) {
		return LifecyclePlan{}, s.unavailableOrInvalid()
	}
	manifest, lock, err := s.loadSource(ctx, command.AssemblyID, command.ExpectedManifestChecksum, command.ExpectedLockChecksum)
	if err != nil {
		return LifecyclePlan{}, err
	}
	document, err := s.planner.BuildUpgradePlan(ctx, command.AssemblyID, manifest, lock, command.Target)
	if err != nil {
		return LifecyclePlan{}, ErrPlanUnavailable
	}
	return s.persistPlan(ctx, LifecycleUpgrade, command.AssemblyID, manifest, lock, document, command.ActorID, command.IdempotencyKey, command.TraceID, command.Target)
}

func (s *LifecycleService) CreateEjectPlan(ctx context.Context, command CreateEjectPlanCommand) (LifecyclePlan, error) {
	if !s.ready() || command.AssemblyID == "" || !digestPattern.MatchString(command.ExpectedManifestChecksum) || !digestPattern.MatchString(command.ExpectedLockChecksum) || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" || !validLifecyclePaths(command.Paths) {
		return LifecyclePlan{}, s.unavailableOrInvalid()
	}
	manifest, lock, err := s.loadSource(ctx, command.AssemblyID, command.ExpectedManifestChecksum, command.ExpectedLockChecksum)
	if err != nil {
		return LifecyclePlan{}, err
	}
	document, err := s.planner.BuildEjectPlan(ctx, command.AssemblyID, manifest, lock, append([]string(nil), command.Paths...))
	if err != nil {
		return LifecyclePlan{}, ErrPlanUnavailable
	}
	return s.persistPlan(ctx, LifecycleEject, command.AssemblyID, manifest, lock, document, command.ActorID, command.IdempotencyKey, command.TraceID, append([]string(nil), command.Paths...))
}

func (s *LifecycleService) ready() bool {
	return s != nil && s.repository != nil && s.validator != nil && s.planner != nil && s.idGenerator != nil
}
func (s *LifecycleService) unavailableOrInvalid() error {
	if s == nil || s.repository == nil || s.validator == nil || s.planner == nil || s.idGenerator == nil {
		return ErrLifecycleUnavailable
	}
	return ErrInvalidCommand
}

func (s *LifecycleService) loadSource(ctx context.Context, assemblyID, manifestChecksum, lockChecksum string) (Manifest, GeneratedProjectLock, error) {
	manifest, lock, err := s.repository.GetLifecycleSource(ctx, assemblyID)
	if err != nil {
		return Manifest{}, GeneratedProjectLock{}, err
	}
	if manifest.AssemblyID == "" || lock.AssemblyID != manifest.AssemblyID || lock.ProductID != manifest.ProductID || !digestsEqual(manifest.ManifestSHA256, manifestChecksum) || !digestsEqual(lock.LockSHA256, lockChecksum) {
		return Manifest{}, GeneratedProjectLock{}, ErrConflict
	}
	return manifest, lock, nil
}

func (s *LifecycleService) GetCurrentSource(ctx context.Context, assemblyID string) (LifecycleArtifactState, error) {
	if s == nil || s.repository == nil || assemblyID == "" {
		return LifecycleArtifactState{}, ErrInvalidCommand
	}
	manifest, lock, err := s.repository.GetLifecycleSource(ctx, assemblyID)
	if err != nil {
		return LifecycleArtifactState{}, err
	}
	return projectLifecycleSource(manifest, lock)
}

func projectLifecycleSource(manifest Manifest, lock GeneratedProjectLock) (LifecycleArtifactState, error) {
	if manifest.AssemblyID == "" || manifest.ProductID == "" || lock.LockID == "" || lock.AssemblyID != manifest.AssemblyID || lock.ProductID != manifest.ProductID {
		return LifecycleArtifactState{}, ErrDocumentInvalid
	}
	manifestChecksum, manifestErr := machinecontract.DigestWithoutTopLevelField(manifest.Document, "manifest_checksum")
	manifestDocumentChecksum, manifestDocumentErr := machinecontract.Digest(manifest.Document)
	lockChecksum, lockErr := machinecontract.DigestWithoutTopLevelField(lock.Document, "lock_checksum")
	lockDocumentChecksum, lockDocumentErr := machinecontract.Digest(lock.Document)
	var lockHeader struct {
		LockID                   string `json:"lock_id"`
		AssemblyManifestChecksum string `json:"assembly_manifest_checksum"`
		CatalogChecksum          string `json:"catalog_checksum"`
		TargetSnapshotChecksum   string `json:"target_snapshot_checksum"`
		LockChecksum             string `json:"lock_checksum"`
	}
	if manifestErr != nil || manifestDocumentErr != nil || lockErr != nil || lockDocumentErr != nil || json.Unmarshal(lock.Document, &lockHeader) != nil ||
		lockHeader.LockID != lock.LockID || !digestsEqual(manifestChecksum, manifest.ManifestSHA256) || !digestsEqual("sha256:"+manifestDocumentChecksum, manifest.DocumentSHA256) ||
		!digestsEqual(lockChecksum, lock.LockSHA256) || !digestsEqual("sha256:"+lockDocumentChecksum, lock.DocumentSHA256) || !digestsEqual(lockHeader.LockChecksum, lockChecksum) ||
		!digestsEqual(lockHeader.AssemblyManifestChecksum, manifestChecksum) || !digestPattern.MatchString(lockHeader.CatalogChecksum) || !digestPattern.MatchString(lockHeader.TargetSnapshotChecksum) {
		return LifecycleArtifactState{}, ErrDocumentInvalid
	}
	return LifecycleArtifactState{ManifestID: manifest.AssemblyID, ManifestChecksum: manifestChecksum, LockID: lock.LockID, LockChecksum: lockChecksum, CatalogChecksum: lockHeader.CatalogChecksum, TargetSnapshotChecksum: lockHeader.TargetSnapshotChecksum}, nil
}

func (s *LifecycleService) persistPlan(ctx context.Context, kind LifecycleKind, rootAssemblyID string, manifest Manifest, lock GeneratedProjectLock, document json.RawMessage, actorID, key, traceID string, request any) (LifecyclePlan, error) {
	validated, err := s.validator.Validate("assembly-lifecycle-plan", document)
	if err != nil {
		return LifecyclePlan{}, err
	}
	plan, err := parseLifecyclePlan(validated.CanonicalJSON)
	if err != nil {
		return LifecyclePlan{}, err
	}
	if plan.Operation != kind || plan.AssemblyID != rootAssemblyID || plan.ProductID != manifest.ProductID || plan.Version != 1 || !digestsEqual(plan.Source.ManifestChecksum, manifest.ManifestSHA256) || !digestsEqual(plan.Source.LockChecksum, lock.LockSHA256) || plan.Source.ManifestID != manifest.AssemblyID || plan.Source.LockID != lock.LockID {
		return LifecyclePlan{}, ErrDocumentInvalid
	}
	plan.SchemaVersion, plan.Document, plan.CreatedBy = validated.SchemaVersion, validated.CanonicalJSON, actorID
	now := s.now().UTC()
	if plan.CreatedAt.IsZero() || plan.CreatedAt.After(now.Add(time.Minute)) {
		return LifecyclePlan{}, ErrDocumentInvalid
	}
	auditID, eventID, err := s.newIDs()
	if err != nil {
		return LifecyclePlan{}, err
	}
	plan.AuditID = auditID
	idem, err := makeIdempotency("assembly.lifecycle.plan."+string(kind), actorID, manifest.AssemblyID, key, request, now)
	if err != nil {
		return LifecyclePlan{}, err
	}
	event := assemblyEvent(eventID, auditID, "assembly.lifecycle_planned.v1", "assembly.lifecycle.planned", "assembly_lifecycle_plan", plan.LifecyclePlanID, plan.ProductID, actorID, traceID, "assembly.lifecycle.plan", now, "normal", map[string]any{"assembly_id": plan.AssemblyID, "operation": kind, "executable": plan.Executable, "plan_checksum": plan.PlanChecksum})
	return s.repository.CreateLifecyclePlan(ctx, CreateLifecyclePlanRecord{Plan: plan, Idempotency: idem, Event: event})
}

func (s *LifecycleService) GetPlan(ctx context.Context, planID string) (LifecyclePlan, error) {
	if s == nil || s.repository == nil || planID == "" {
		return LifecyclePlan{}, ErrInvalidCommand
	}
	return s.repository.GetLifecyclePlan(ctx, planID)
}

type ExecuteLifecyclePlanCommand struct {
	LifecyclePlanID, PlanChecksum, ConfirmationChecksum, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                                                                       int64
}

func (s *LifecycleService) ExecutePlan(ctx context.Context, command ExecuteLifecyclePlanCommand) (LifecycleOperation, error) {
	if !s.ready() || command.LifecyclePlanID == "" || command.ExpectedVersion < 1 || !digestPattern.MatchString(command.PlanChecksum) || !digestPattern.MatchString(command.ConfirmationChecksum) || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return LifecycleOperation{}, s.unavailableOrInvalid()
	}
	plan, err := s.repository.GetLifecyclePlan(ctx, command.LifecyclePlanID)
	if err != nil {
		return LifecycleOperation{}, err
	}
	if plan.Version != command.ExpectedVersion {
		return LifecycleOperation{}, ErrVersionConflict
	}
	if !plan.Executable {
		return LifecycleOperation{}, ErrPlanNotExecutable
	}
	if !digestsEqual(plan.PlanChecksum, command.PlanChecksum) || !digestsEqual(plan.ConfirmationChecksum, command.ConfirmationChecksum) {
		return LifecycleOperation{}, ErrConflict
	}
	manifest, lock, err := s.loadSource(ctx, plan.AssemblyID, plan.Source.ManifestChecksum, plan.Source.LockChecksum)
	if err != nil {
		return LifecycleOperation{}, err
	}
	if err := s.planner.Revalidate(ctx, plan, manifest, lock); err != nil {
		return LifecycleOperation{}, ErrConflict
	}
	now := s.now().UTC()
	operation, err := s.newOperation(plan, command.ActorID, command.IdempotencyKey, now)
	if err != nil {
		return LifecycleOperation{}, err
	}
	auditID, eventID, err := s.newIDs()
	if err != nil {
		return LifecycleOperation{}, err
	}
	operation.AuditID = auditID
	idem, err := makeIdempotency("assembly.lifecycle.execute", command.ActorID, plan.LifecyclePlanID, command.IdempotencyKey, struct {
		ExpectedVersion                    int64
		PlanChecksum, ConfirmationChecksum string
	}{command.ExpectedVersion, command.PlanChecksum, command.ConfirmationChecksum}, now)
	if err != nil {
		return LifecycleOperation{}, err
	}
	event := assemblyEvent(eventID, auditID, "assembly.lifecycle_started.v1", "assembly.lifecycle.started", "assembly_lifecycle_operation", operation.OperationID, operation.ProductID, command.ActorID, command.TraceID, "assembly.lifecycle.execute", now, "high", map[string]any{"lifecycle_plan_id": plan.LifecyclePlanID, "operation": plan.Operation})
	transition := LifecycleArtifactTransition{OperationID: operation.OperationID, Source: plan.Source, RollbackJournal: json.RawMessage(`{}`), CreatedAt: now}
	return s.repository.StartLifecycleOperation(ctx, StartLifecycleOperationRecord{Operation: operation, Idempotency: idem, Event: event, Transition: transition})
}

func (s *LifecycleService) GetOperation(ctx context.Context, operationID string) (LifecycleOperation, error) {
	if s == nil || s.repository == nil || operationID == "" {
		return LifecycleOperation{}, ErrInvalidCommand
	}
	return s.repository.GetLifecycleOperation(ctx, operationID)
}

type CancelLifecycleOperationCommand struct {
	OperationID, Reason, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                                       int64
}

func (s *LifecycleService) CancelOperation(ctx context.Context, command CancelLifecycleOperationCommand) (LifecycleOperation, error) {
	if !s.ready() || command.OperationID == "" || command.ExpectedVersion < 1 || !validReason(command.Reason) || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return LifecycleOperation{}, s.unavailableOrInvalid()
	}
	current, err := s.repository.GetLifecycleOperation(ctx, command.OperationID)
	if err != nil {
		return LifecycleOperation{}, err
	}
	if current.Version != command.ExpectedVersion {
		return LifecycleOperation{}, ErrVersionConflict
	}
	if current.Status != LifecyclePlanned {
		return LifecycleOperation{}, ErrConflict
	}
	now := s.now().UTC()
	next, err := EvolveLifecycleOperation(current, LifecycleCancelled, "", nil, LifecycleRecovery{}, nil, nil, now)
	if err != nil {
		return LifecycleOperation{}, err
	}
	auditID, eventID, err := s.newIDs()
	if err != nil {
		return LifecycleOperation{}, err
	}
	next.AuditID = auditID
	idem, err := makeIdempotency("assembly.lifecycle.cancel", command.ActorID, current.OperationID, command.IdempotencyKey, struct {
		ExpectedVersion int64
		Reason          string
	}{command.ExpectedVersion, command.Reason}, now)
	if err != nil {
		return LifecycleOperation{}, err
	}
	event := assemblyEvent(eventID, auditID, "assembly.lifecycle_cancelled.v1", "assembly.lifecycle.cancelled", "assembly_lifecycle_operation", current.OperationID, current.ProductID, command.ActorID, command.TraceID, "assembly.lifecycle.execute", now, "high", map[string]any{"reason": command.Reason})
	return s.repository.CancelLifecycleOperation(ctx, UpdateLifecycleOperationRecord{Operation: next, ExpectedVersion: current.Version, Event: event}, idem)
}

type RollbackLifecycleOperationCommand struct {
	OperationID, Reason, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                                       int64
}

func (s *LifecycleService) RollbackOperation(ctx context.Context, command RollbackLifecycleOperationCommand) (LifecycleOperation, error) {
	if !s.ready() || command.OperationID == "" || command.ExpectedVersion < 1 || !validReason(command.Reason) || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return LifecycleOperation{}, s.unavailableOrInvalid()
	}
	predecessor, err := s.repository.GetLifecycleOperation(ctx, command.OperationID)
	if err != nil {
		return LifecycleOperation{}, err
	}
	if predecessor.Version != command.ExpectedVersion {
		return LifecycleOperation{}, ErrVersionConflict
	}
	if predecessor.Status != LifecycleCompleted || !predecessor.Recovery.RollbackAvailable {
		return LifecycleOperation{}, ErrConflict
	}
	transition, err := s.repository.GetLifecycleTransition(ctx, predecessor.OperationID)
	if err != nil || transition.Target == nil || transition.CompletedAt == nil {
		return LifecycleOperation{}, ErrConflict
	}
	now := s.now().UTC()
	operationID, err := s.idGenerator("operation_")
	if err != nil {
		return LifecycleOperation{}, err
	}
	operation := LifecycleOperation{OperationID: operationID, RootOperationID: predecessor.RootOperationID, RollbackOfOperationID: predecessor.OperationID, AssemblyID: predecessor.AssemblyID, ProductID: predecessor.ProductID, Kind: LifecycleRollback, Version: 1, Status: LifecyclePlanned, Source: *transition.Target, Recovery: LifecycleRecovery{Retryable: true, CancelAllowed: true}, IdempotencyKeyDigest: digestString(command.IdempotencyKey), CreatedBy: command.ActorID, CreatedAt: now, UpdatedAt: now}
	if err := rebuildLifecycleOperationDocument(&operation); err != nil {
		return LifecycleOperation{}, err
	}
	auditID, eventID, err := s.newIDs()
	if err != nil {
		return LifecycleOperation{}, err
	}
	operation.AuditID = auditID
	idem, err := makeIdempotency("assembly.lifecycle.rollback", command.ActorID, predecessor.OperationID, command.IdempotencyKey, struct {
		ExpectedVersion int64
		Reason          string
	}{command.ExpectedVersion, command.Reason}, now)
	if err != nil {
		return LifecycleOperation{}, err
	}
	event := assemblyEvent(eventID, auditID, "assembly.lifecycle_started.v1", "assembly.lifecycle.rollback_started", "assembly_lifecycle_operation", operation.OperationID, operation.ProductID, command.ActorID, command.TraceID, "assembly.lifecycle.execute", now, "high", map[string]any{"rollback_of_operation_id": predecessor.OperationID, "reason": command.Reason})
	record := StartLifecycleOperationRecord{Operation: operation, Idempotency: idem, Event: event, Transition: LifecycleArtifactTransition{OperationID: operation.OperationID, Source: operation.Source, RollbackJournal: transition.RollbackJournal, CreatedAt: now}}
	return s.repository.StartRollbackOperation(ctx, record, predecessor.Version)
}

type CancelRunCommand struct {
	RunID, Reason, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                                 int64
}

func (s *Service) CancelRun(ctx context.Context, command CancelRunCommand) (Run, error) {
	repository, ok := s.repository.(LifecycleRepository)
	if !ok || s.idGenerator == nil || command.RunID == "" || command.ExpectedVersion < 1 || !validReason(command.Reason) || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return Run{}, ErrInvalidCommand
	}
	current, err := s.repository.GetRun(ctx, "", command.RunID)
	if err != nil {
		return Run{}, err
	}
	if current.Version != command.ExpectedVersion {
		return Run{}, ErrVersionConflict
	}
	if current.Status != RunStatusPlanned {
		return Run{}, ErrConflict
	}
	now := s.now().UTC()
	next := current
	next.Version++
	next.Status = RunStatusCancelled
	next.Recovery = RunRecovery{}
	next.UpdatedAt = now
	next.CompletedAt = &now
	next.CurrentStepID = ""
	for index := range next.Steps {
		if next.Steps[index].Status == "pending" {
			next.Steps[index].Status = "skipped"
			next.Steps[index].FinishedAt = &now
		}
	}
	document, err := runDocumentFrom(next)
	if err != nil {
		return Run{}, err
	}
	validated, err := s.validator.Validate("assembly-run", document)
	if err != nil {
		return Run{}, err
	}
	next.Document, next.DocumentSHA256 = validated.CanonicalJSON, validated.SHA256
	auditID, eventID, err := s.newAuditAndEventIDs()
	if err != nil {
		return Run{}, err
	}
	next.AuditID = auditID
	idem, err := makeIdempotency("assembly.cancel_run", command.ActorID, current.RunID, command.IdempotencyKey, struct {
		ExpectedVersion int64
		Reason          string
	}{command.ExpectedVersion, command.Reason}, now)
	if err != nil {
		return Run{}, err
	}
	event := assemblyEvent(eventID, auditID, "assembly.cancelled.v1", "assembly.cancelled", "assembly_run", current.RunID, current.ProductID, command.ActorID, command.TraceID, "assembly.lifecycle.execute", now, "high", map[string]any{"reason": command.Reason})
	return repository.CancelRun(ctx, CancelRunRecord{Run: next, ExpectedVersion: current.Version, Idempotency: idem, Event: event})
}

func (s *LifecycleService) newOperation(plan LifecyclePlan, actorID, key string, now time.Time) (LifecycleOperation, error) {
	id, err := s.idGenerator("operation_")
	if err != nil {
		return LifecycleOperation{}, err
	}
	operation := LifecycleOperation{OperationID: id, RootOperationID: id, LifecyclePlanID: plan.LifecyclePlanID, AssemblyID: plan.AssemblyID, ProductID: plan.ProductID, Kind: plan.Operation, Version: 1, Status: LifecyclePlanned, Source: plan.Source, Recovery: LifecycleRecovery{Retryable: true, CancelAllowed: true}, IdempotencyKeyDigest: digestString(key), CreatedBy: actorID, CreatedAt: now, UpdatedAt: now}
	return operation, rebuildLifecycleOperationDocument(&operation)
}

func (s *LifecycleService) newIDs() (string, string, error) {
	auditID, err := s.idGenerator("aud_")
	if err != nil {
		return "", "", err
	}
	eventID, err := s.idGenerator("evt_")
	return auditID, eventID, err
}

func EvolveLifecycleOperation(current LifecycleOperation, status LifecycleStatus, step string, target *LifecycleArtifactState, recovery LifecycleRecovery, diagnostics []RunDiagnostic, reports []RunReport, now time.Time) (LifecycleOperation, error) {
	allowed := map[LifecycleStatus]map[LifecycleStatus]bool{LifecyclePlanned: {LifecycleExecuting: true, LifecycleCancelled: true, LifecycleFailed: true}, LifecycleExecuting: {LifecycleCompleted: true, LifecycleFailed: true, LifecycleRollingBack: true}, LifecycleRollingBack: {LifecycleRolledBack: true, LifecycleRollbackFailed: true}}
	if !allowed[current.Status][status] || !now.After(current.UpdatedAt) {
		return LifecycleOperation{}, ErrConflict
	}
	next := current
	next.Version++
	next.Status = status
	next.CurrentStep = step
	next.Target = target
	next.Recovery = recovery
	next.UpdatedAt = now.UTC()
	next.Diagnostics = append([]RunDiagnostic(nil), diagnostics...)
	next.Reports = append([]RunReport(nil), reports...)
	if status == LifecycleCompleted || status == LifecycleFailed || status == LifecycleCancelled || status == LifecycleRolledBack || status == LifecycleRollbackFailed {
		completed := now.UTC()
		next.CompletedAt = &completed
	}
	if err := rebuildLifecycleOperationDocument(&next); err != nil {
		return LifecycleOperation{}, err
	}
	return next, nil
}

func rebuildLifecycleOperationDocument(value *LifecycleOperation) error {
	var target any
	if value.Target != nil {
		target = artifactStateDocument(*value.Target)
	}
	var rollbackOf, planID, step, completed any
	if value.RollbackOfOperationID != "" {
		rollbackOf = value.RollbackOfOperationID
	}
	if value.LifecyclePlanID != "" {
		planID = value.LifecyclePlanID
	}
	if value.CurrentStep != "" {
		step = value.CurrentStep
	}
	if value.CompletedAt != nil {
		completed = value.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	diagnosticIDs := make([]string, len(value.Diagnostics))
	for i := range value.Diagnostics {
		diagnosticIDs[i] = value.Diagnostics[i].DiagnosticID
	}
	reportIDs := make([]string, len(value.Reports))
	for i := range value.Reports {
		reportIDs[i] = value.Reports[i].ReportID
	}
	body := map[string]any{"schema_version": "1.0.0", "operation_id": value.OperationID, "root_operation_id": value.RootOperationID, "rollback_of_operation_id": rollbackOf, "lifecycle_plan_id": planID, "assembly_id": value.AssemblyID, "product_id": value.ProductID, "kind": value.Kind, "version": value.Version, "status": value.Status, "current_step": step, "source": artifactStateDocument(value.Source), "target": target, "recovery": map[string]any{"retryable": value.Recovery.Retryable, "rollback_available": value.Recovery.RollbackAvailable, "cancel_allowed": value.Recovery.CancelAllowed}, "diagnostic_ids": diagnosticIDs, "report_ids": reportIDs, "created_by": value.CreatedBy, "created_at": value.CreatedAt.UTC().Format(time.RFC3339Nano), "updated_at": value.UpdatedAt.UTC().Format(time.RFC3339Nano), "completed_at": completed, "operation_checksum": digestBytes(nil)}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	checksum, err := machinecontract.DigestWithoutTopLevelField(raw, "operation_checksum")
	if err != nil {
		return err
	}
	body["operation_checksum"] = checksum
	raw, err = json.Marshal(body)
	if err != nil {
		return err
	}
	canonical, err := machinecontract.Canonicalize(raw)
	if err != nil {
		return err
	}
	value.SchemaVersion = "1.0.0"
	value.Document = canonical
	value.OperationChecksum = checksum
	return nil
}

func parseLifecyclePlan(document json.RawMessage) (LifecyclePlan, error) {
	var body struct {
		SchemaVersion, LifecyclePlanID, AssemblyID, ProductID string
		Operation                                             LifecycleKind
		Source                                                struct{ ManifestID, ManifestChecksum, LockID, LockChecksum, CatalogChecksum, TargetSnapshotChecksum string }
		TargetSnapshotChecksum                                string
		Changes                                               []struct {
			Path, Action, Ownership       string
			BeforeChecksum, AfterChecksum *string
			SourceID, SourceVersion       string
		}
		Conflicts             []LifecycleConflict
		RegressionTests       []string `json:"regression_tests"`
		BlockingConflictCount int      `json:"blocking_conflict_count"`
		Executable            bool
		Confirmation          struct {
			Statements      []string
			SummaryChecksum string `json:"summary_checksum"`
		}
		CreatedAt    time.Time `json:"created_at"`
		PlanChecksum string    `json:"plan_checksum"`
		Version      int64
	}
	if json.Unmarshal(document, &body) != nil {
		return LifecyclePlan{}, ErrDocumentInvalid
	}
	// Struct field tags are required for snake_case fields not handled by encoding/json.
	var raw map[string]json.RawMessage
	if json.Unmarshal(document, &raw) != nil {
		return LifecyclePlan{}, ErrDocumentInvalid
	}
	var header struct {
		SchemaVersion          string                 `json:"schema_version"`
		LifecyclePlanID        string                 `json:"lifecycle_plan_id"`
		AssemblyID             string                 `json:"assembly_id"`
		ProductID              string                 `json:"product_id"`
		Operation              LifecycleKind          `json:"operation"`
		Source                 LifecycleArtifactState `json:"source"`
		TargetSnapshotChecksum string                 `json:"target_snapshot_checksum"`
		Changes                []struct {
			Path           string  `json:"path"`
			Action         string  `json:"action"`
			Ownership      string  `json:"ownership"`
			BeforeChecksum *string `json:"before_checksum"`
			AfterChecksum  *string `json:"after_checksum"`
			SourceID       string  `json:"source_id"`
			SourceVersion  string  `json:"source_version"`
		} `json:"changes"`
		Migrations []struct {
			MigrationID   string `json:"migration_id"`
			Kind          string `json:"kind"`
			Reversibility string `json:"reversibility"`
			Summary       string `json:"summary"`
		} `json:"migrations"`
		Conflicts []struct {
			ConflictID  string   `json:"conflict_id"`
			Code        string   `json:"code"`
			Category    string   `json:"category"`
			Blocking    bool     `json:"blocking"`
			Message     string   `json:"message"`
			Paths       []string `json:"paths"`
			Remediation []string `json:"remediation"`
		} `json:"conflicts"`
		Rollback struct {
			Strategy                    string `json:"strategy"`
			Automatic                   bool   `json:"automatic"`
			PredecessorManifestChecksum string `json:"predecessor_manifest_checksum"`
			PredecessorLockChecksum     string `json:"predecessor_lock_checksum"`
		} `json:"rollback"`
		RegressionTests       []string `json:"regression_tests"`
		BlockingConflictCount int      `json:"blocking_conflict_count"`
		Executable            bool     `json:"executable"`
		Confirmation          struct {
			Statements      []string `json:"statements"`
			SummaryChecksum string   `json:"summary_checksum"`
		} `json:"confirmation"`
		CreatedAt    time.Time `json:"created_at"`
		PlanChecksum string    `json:"plan_checksum"`
	}
	if json.Unmarshal(document, &header) != nil {
		return LifecyclePlan{}, ErrDocumentInvalid
	}
	checksum, err := machinecontract.DigestWithoutTopLevelField(document, "plan_checksum")
	if err != nil || !digestsEqual(checksum, header.PlanChecksum) {
		return LifecyclePlan{}, ErrDocumentInvalid
	}
	changes := make([]LifecycleChange, len(header.Changes))
	for i, v := range header.Changes {
		changes[i] = LifecycleChange{Path: v.Path, Action: v.Action, Ownership: v.Ownership, SourceID: v.SourceID, SourceVersion: v.SourceVersion}
		if v.BeforeChecksum != nil {
			changes[i].BeforeChecksum = *v.BeforeChecksum
		}
		if v.AfterChecksum != nil {
			changes[i].AfterChecksum = *v.AfterChecksum
		}
	}
	migrations := make([]LifecycleMigration, len(header.Migrations))
	for i, v := range header.Migrations {
		migrations[i] = LifecycleMigration{MigrationID: v.MigrationID, Kind: v.Kind, Reversibility: v.Reversibility, Summary: v.Summary}
	}
	conflicts := make([]LifecycleConflict, len(header.Conflicts))
	for i, v := range header.Conflicts {
		conflicts[i] = LifecycleConflict{ConflictID: v.ConflictID, Code: v.Code, Category: v.Category, Blocking: v.Blocking, Message: v.Message, Paths: v.Paths, Remediation: v.Remediation}
	}
	if header.LifecyclePlanID == "" || header.AssemblyID == "" || header.ProductID == "" || (header.Operation != LifecycleUpgrade && header.Operation != LifecycleEject) || header.BlockingConflictCount < 0 || header.Executable != (header.BlockingConflictCount == 0) || !digestPattern.MatchString(header.Confirmation.SummaryChecksum) {
		return LifecyclePlan{}, ErrDocumentInvalid
	}
	return LifecyclePlan{LifecyclePlanID: header.LifecyclePlanID, AssemblyID: header.AssemblyID, ProductID: header.ProductID, Operation: header.Operation, Version: 1, SchemaVersion: header.SchemaVersion, Document: document, Source: header.Source, TargetSnapshotChecksum: header.TargetSnapshotChecksum, Changes: changes, Migrations: migrations, Conflicts: conflicts, RegressionTests: header.RegressionTests, BlockingConflictCount: header.BlockingConflictCount, Executable: header.Executable, ConfirmationChecksum: header.Confirmation.SummaryChecksum, Statements: header.Confirmation.Statements, Rollback: LifecycleRollbackPolicy{Strategy: header.Rollback.Strategy, Automatic: header.Rollback.Automatic, PredecessorManifestChecksum: header.Rollback.PredecessorManifestChecksum, PredecessorLockChecksum: header.Rollback.PredecessorLockChecksum}, PlanChecksum: header.PlanChecksum, CreatedAt: header.CreatedAt}, nil
}

func ProjectLifecyclePlanDocument(document json.RawMessage) (LifecyclePlan, error) {
	return parseLifecyclePlan(document)
}

func artifactStateDocument(value LifecycleArtifactState) map[string]any {
	return map[string]any{"manifest_id": value.ManifestID, "manifest_checksum": value.ManifestChecksum, "lock_id": value.LockID, "lock_checksum": value.LockChecksum, "catalog_checksum": value.CatalogChecksum, "target_snapshot_checksum": value.TargetSnapshotChecksum}
}

func validTargetVersions(value LifecycleTargetVersions) bool {
	if !validVersionRefs(value.Templates) || len(value.Templates) == 0 || !validVersionRefs(value.Packages) || !validVersionRefs(value.SDKs) || !validVersionRef(value.Generator) {
		return false
	}
	return true
}
func validVersionRefs(values []LifecycleVersionRef) bool {
	seen := map[string]bool{}
	for _, v := range values {
		if !validVersionRef(v) || seen[v.ID] {
			return false
		}
		seen[v.ID] = true
	}
	return true
}

var (
	lifecycleIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	lifecycleSemverPattern     = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
)

func validVersionRef(value LifecycleVersionRef) bool {
	return lifecycleIdentifierPattern.MatchString(value.ID) && lifecycleSemverPattern.MatchString(value.Version)
}
func validLifecyclePaths(values []string) bool {
	if len(values) == 0 || len(values) > 500 {
		return false
	}
	copyValues := append([]string(nil), values...)
	sort.Strings(copyValues)
	for i, v := range copyValues {
		if machinecontract.ValidateSafeRelativePath(v) != nil || (i > 0 && strings.EqualFold(copyValues[i-1], v)) {
			return false
		}
	}
	return true
}
func validReason(value string) bool {
	return value != "" && len(value) <= 500 && !strings.ContainsAny(value, "\r\n\t") && strings.TrimSpace(value) == value
}

func runDocumentFrom(run Run) (json.RawMessage, error) {
	steps := make([]map[string]any, len(run.Steps))
	for i, v := range run.Steps {
		steps[i] = map[string]any{"step_id": v.StepID, "kind": v.Kind, "status": v.Status, "attempt": v.Attempt, "compensation_status": v.CompensationStatus, "diagnostic_ids": v.DiagnosticIDs}
		if v.StartedAt != nil {
			steps[i]["started_at"] = v.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		if v.FinishedAt != nil {
			steps[i]["finished_at"] = v.FinishedAt.UTC().Format(time.RFC3339Nano)
		}
	}
	body := map[string]any{"schema_version": run.SchemaVersion, "run_id": run.RunID, "root_run_id": run.RootRunID, "retry_of_run_id": nil, "attempt_number": run.AttemptNumber, "plan_id": run.PlanID, "plan_checksum": run.PlanSHA256, "idempotency_key_digest": run.IdempotencyKeyDigest, "output_target_ref": run.OutputTargetRef, "status": run.Status, "steps": steps, "current_step_id": nil, "diagnostic_ids": run.DiagnosticIDs, "recovery": map[string]any{"retryable": run.Recovery.Retryable, "rollback_required": run.Recovery.RollbackRequired}, "created_at": run.CreatedAt.UTC().Format(time.RFC3339Nano), "updated_at": run.UpdatedAt.UTC().Format(time.RFC3339Nano), "completed_at": run.CompletedAt.UTC().Format(time.RFC3339Nano)}
	if run.RetryOfRunID != "" {
		body["retry_of_run_id"] = run.RetryOfRunID
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return machinecontract.Canonicalize(raw)
}
