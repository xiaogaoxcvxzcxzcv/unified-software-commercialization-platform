package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/platform/httpx"
)

var safeRelativePathPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
var stableLifecycleCodePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
var hostPathInDisplayPattern = regexp.MustCompile(`(?i)(?:[a-z]:[/\\]|\\\\|(?:^|[[:space:]])/(?:users|home|etc|var|tmp|opt|srv|root|mnt|volumes)(?:/|$))`)

const (
	assembliesPath                     = "/api/v1/admin/assemblies"
	lifecyclePlansPath                 = "/api/v1/admin/assembly-lifecycle-plans"
	lifecycleOperationsPath            = "/api/v1/admin/assembly-lifecycle-operations"
	assemblyLifecyclePlanPermission    = "assembly.lifecycle.plan"
	assemblyLifecycleExecutePermission = "assembly.lifecycle.execute"
)

type lifecycleService interface {
	GetLifecycleSource(context.Context, GetLifecycleSourceCommand) (LifecycleArtifactState, error)
	CreateUpgradePlan(context.Context, CreateUpgradePlanCommand) (LifecyclePlan, error)
	CreateEjectPlan(context.Context, CreateEjectPlanCommand) (LifecyclePlan, error)
	GetLifecyclePlan(context.Context, GetLifecyclePlanCommand) (LifecyclePlan, error)
	ExecuteLifecyclePlan(context.Context, ExecuteLifecyclePlanCommand) (LifecycleOperation, error)
	GetLifecycleOperation(context.Context, GetLifecycleOperationCommand) (LifecycleOperation, error)
	CancelLifecycleOperation(context.Context, CancelLifecycleOperationCommand) (LifecycleOperation, error)
	RollbackLifecycleOperation(context.Context, RollbackLifecycleOperationCommand) (LifecycleOperation, error)
	CancelRun(context.Context, CancelRunCommand) (Run, error)
}

type LifecycleVersionRef struct{ ID, Version string }
type LifecycleTargetVersions struct {
	Packages, Templates, SDKs []LifecycleVersionRef
	Generator                 LifecycleVersionRef
}
type CreateUpgradePlanCommand struct {
	AssemblyID, ExpectedManifestChecksum, ExpectedLockChecksum, ActorID, IdempotencyKey, TraceID string
	Target                                                                                       LifecycleTargetVersions
}
type GetLifecycleSourceCommand struct{ AssemblyID string }
type CreateEjectPlanCommand struct {
	AssemblyID, ExpectedManifestChecksum, ExpectedLockChecksum, ActorID, IdempotencyKey, TraceID string
	Paths                                                                                        []string
}
type GetLifecyclePlanCommand struct{ LifecyclePlanID string }
type ExecuteLifecyclePlanCommand struct {
	LifecyclePlanID, PlanChecksum, ConfirmationChecksum, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                                                                       int64
}
type GetLifecycleOperationCommand struct{ OperationID string }
type CancelLifecycleOperationCommand struct {
	OperationID, Reason, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                                       int64
}
type RollbackLifecycleOperationCommand struct {
	OperationID, Reason, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                                       int64
}
type CancelRunCommand struct {
	RunID, Reason, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                                 int64
}

type LifecycleArtifactState struct{ ManifestID, ManifestChecksum, LockID, LockChecksum, CatalogChecksum, TargetSnapshotChecksum string }
type LifecycleChange struct{ Path, Action, Ownership, BeforeChecksum, AfterChecksum, SourceID, SourceVersion string }
type LifecycleConflict struct {
	ConflictID, Code, Category, Message string
	Blocking                            bool
	Paths, Remediation                  []string
}
type LifecycleMigration struct{ MigrationID, Kind, Reversibility, Summary string }
type LifecycleRollbackPolicy struct {
	Strategy                                             string
	Automatic                                            bool
	PredecessorManifestChecksum, PredecessorLockChecksum string
}
type LifecyclePlan struct {
	LifecyclePlanID, AssemblyID, ProductID, Operation string
	Version                                           int64
	Source                                            LifecycleArtifactState
	TargetSnapshotChecksum                            string
	Changes                                           []LifecycleChange
	Migrations                                        []LifecycleMigration
	Conflicts                                         []LifecycleConflict
	RegressionTests                                   []string
	Rollback                                          LifecycleRollbackPolicy
	BlockingConflictCount                             int
	Executable                                        bool
	ConfirmationChecksum                              string
	Statements                                        []string
	PlanChecksum                                      string
	Document                                          json.RawMessage
	CreatedAt                                         time.Time
	AuditID                                           string
}
type LifecycleRecovery struct{ Retryable, RollbackAvailable, CancelAllowed bool }
type LifecycleOperation struct {
	OperationID, RootOperationID, RollbackOfOperationID, LifecyclePlanID, AssemblyID, ProductID, Kind, Status, CurrentStep string
	Version                                                                                                                int64
	Source                                                                                                                 LifecycleArtifactState
	Target                                                                                                                 *LifecycleArtifactState
	Recovery                                                                                                               LifecycleRecovery
	Diagnostics                                                                                                            []RunDiagnostic
	Reports                                                                                                                []RunReport
	Document                                                                                                               json.RawMessage
	ManifestURL, LockURL                                                                                                   string
	CreatedAt, UpdatedAt                                                                                                   time.Time
	CompletedAt                                                                                                            *time.Time
	AuditID                                                                                                                string
}

type lifecycleVersionRefRequest struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}
type lifecycleTargetRequest struct {
	Packages  []lifecycleVersionRefRequest `json:"packages"`
	Templates []lifecycleVersionRefRequest `json:"templates"`
	Generator lifecycleVersionRefRequest   `json:"generator"`
	SDKs      []lifecycleVersionRefRequest `json:"sdks"`
}

func (h *Handler) lifecycle() (lifecycleService, bool) {
	service, ok := h.service.(lifecycleService)
	return service, ok
}

func (h *Handler) getLifecycleSource(w http.ResponseWriter, r *http.Request, assemblyID string) {
	if _, ok := h.authorize(w, r, assemblyReadPermission, false); !ok {
		return
	}
	service, ok := h.lifecycle()
	if !ok {
		writeLifecycleUnavailable(w, r)
		return
	}
	result, err := service.GetLifecycleSource(r.Context(), GetLifecycleSourceCommand{AssemblyID: assemblyID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validLifecycleArtifactState(result) {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusOK, lifecycleArtifactStateResponseFrom(result))
}

func (h *Handler) createUpgradePlan(w http.ResponseWriter, r *http.Request, assemblyID string) {
	principal, ok := h.authorize(w, r, assemblyLifecyclePlanPermission, false)
	if !ok {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	trace, ok := requireTraceID(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedManifestChecksum string                 `json:"expected_manifest_checksum"`
		ExpectedLockChecksum     string                 `json:"expected_lock_checksum"`
		Target                   lifecycleTargetRequest `json:"target"`
	}
	if !decodeJSON(w, r, &body) || !checksumPattern.MatchString(body.ExpectedManifestChecksum) || !checksumPattern.MatchString(body.ExpectedLockChecksum) {
		writeInvalidRequest(w, r)
		return
	}
	target, valid := lifecycleTargetFrom(body.Target)
	if !valid {
		writeInvalidRequest(w, r)
		return
	}
	service, ok := h.lifecycle()
	if !ok {
		writeLifecycleUnavailable(w, r)
		return
	}
	result, err := service.CreateUpgradePlan(r.Context(), CreateUpgradePlanCommand{AssemblyID: assemblyID, ExpectedManifestChecksum: body.ExpectedManifestChecksum, ExpectedLockChecksum: body.ExpectedLockChecksum, Target: target, ActorID: principal.AdminUserID, IdempotencyKey: key, TraceID: trace})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validLifecyclePlan(result, assemblyID, "upgrade") {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusCreated, lifecyclePlanResponseFrom(result))
}

func (h *Handler) createEjectPlan(w http.ResponseWriter, r *http.Request, assemblyID string) {
	principal, ok := h.authorize(w, r, assemblyLifecyclePlanPermission, false)
	if !ok {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	trace, ok := requireTraceID(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedManifestChecksum string   `json:"expected_manifest_checksum"`
		ExpectedLockChecksum     string   `json:"expected_lock_checksum"`
		Paths                    []string `json:"paths"`
	}
	if !decodeJSON(w, r, &body) || !checksumPattern.MatchString(body.ExpectedManifestChecksum) || !checksumPattern.MatchString(body.ExpectedLockChecksum) || !validLifecyclePaths(body.Paths) {
		writeInvalidRequest(w, r)
		return
	}
	service, ok := h.lifecycle()
	if !ok {
		writeLifecycleUnavailable(w, r)
		return
	}
	result, err := service.CreateEjectPlan(r.Context(), CreateEjectPlanCommand{AssemblyID: assemblyID, ExpectedManifestChecksum: body.ExpectedManifestChecksum, ExpectedLockChecksum: body.ExpectedLockChecksum, Paths: append([]string(nil), body.Paths...), ActorID: principal.AdminUserID, IdempotencyKey: key, TraceID: trace})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validLifecyclePlan(result, assemblyID, "eject") {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusCreated, lifecyclePlanResponseFrom(result))
}

func (h *Handler) getLifecyclePlan(w http.ResponseWriter, r *http.Request, planID string) {
	if _, ok := h.authorize(w, r, assemblyReadPermission, false); !ok {
		return
	}
	service, ok := h.lifecycle()
	if !ok {
		writeLifecycleUnavailable(w, r)
		return
	}
	result, err := service.GetLifecyclePlan(r.Context(), GetLifecyclePlanCommand{LifecyclePlanID: planID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validLifecyclePlan(result, "", "") || result.LifecyclePlanID != planID {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusOK, lifecyclePlanResponseFrom(result))
}

func (h *Handler) executeLifecyclePlan(w http.ResponseWriter, r *http.Request, planID string) {
	principal, ok := h.authorize(w, r, assemblyLifecycleExecutePermission, true)
	if !ok {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	trace, ok := requireTraceID(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion      int64  `json:"expected_version"`
		PlanChecksum         string `json:"plan_checksum"`
		ConfirmationChecksum string `json:"confirmation_checksum"`
	}
	if !decodeJSON(w, r, &body) || body.ExpectedVersion < 1 || !checksumPattern.MatchString(body.PlanChecksum) || !checksumPattern.MatchString(body.ConfirmationChecksum) {
		writeInvalidRequest(w, r)
		return
	}
	service, ok := h.lifecycle()
	if !ok {
		writeLifecycleUnavailable(w, r)
		return
	}
	result, err := service.ExecuteLifecyclePlan(r.Context(), ExecuteLifecyclePlanCommand{LifecyclePlanID: planID, ExpectedVersion: body.ExpectedVersion, PlanChecksum: body.PlanChecksum, ConfirmationChecksum: body.ConfirmationChecksum, ActorID: principal.AdminUserID, IdempotencyKey: key, TraceID: trace})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validLifecycleOperation(result) || result.LifecyclePlanID != planID {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusAccepted, lifecycleOperationResponseFrom(result))
}

func (h *Handler) getLifecycleOperation(w http.ResponseWriter, r *http.Request, operationID string) {
	if _, ok := h.authorize(w, r, assemblyReadPermission, false); !ok {
		return
	}
	service, ok := h.lifecycle()
	if !ok {
		writeLifecycleUnavailable(w, r)
		return
	}
	result, err := service.GetLifecycleOperation(r.Context(), GetLifecycleOperationCommand{OperationID: operationID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validLifecycleOperation(result) || result.OperationID != operationID {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusOK, lifecycleOperationResponseFrom(result))
}

func (h *Handler) cancelLifecycleOperation(w http.ResponseWriter, r *http.Request, operationID string) {
	h.mutateLifecycleOperation(w, r, operationID, false)
}
func (h *Handler) rollbackLifecycleOperation(w http.ResponseWriter, r *http.Request, operationID string) {
	h.mutateLifecycleOperation(w, r, operationID, true)
}
func (h *Handler) mutateLifecycleOperation(w http.ResponseWriter, r *http.Request, operationID string, rollback bool) {
	principal, ok := h.authorize(w, r, assemblyLifecycleExecutePermission, true)
	if !ok {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	trace, ok := requireTraceID(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64  `json:"expected_version"`
		Reason          string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) || body.ExpectedVersion < 1 || !validLifecycleReason(body.Reason) {
		writeInvalidRequest(w, r)
		return
	}
	service, ok := h.lifecycle()
	if !ok {
		writeLifecycleUnavailable(w, r)
		return
	}
	var result LifecycleOperation
	var err error
	if rollback {
		result, err = service.RollbackLifecycleOperation(r.Context(), RollbackLifecycleOperationCommand{OperationID: operationID, ExpectedVersion: body.ExpectedVersion, Reason: body.Reason, ActorID: principal.AdminUserID, IdempotencyKey: key, TraceID: trace})
	} else {
		result, err = service.CancelLifecycleOperation(r.Context(), CancelLifecycleOperationCommand{OperationID: operationID, ExpectedVersion: body.ExpectedVersion, Reason: body.Reason, ActorID: principal.AdminUserID, IdempotencyKey: key, TraceID: trace})
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validLifecycleOperation(result) {
		writeInternalError(w, r)
		return
	}
	status := http.StatusOK
	if rollback {
		status = http.StatusAccepted
	}
	httpx.JSON(w, status, lifecycleOperationResponseFrom(result))
}

func (h *Handler) cancelRun(w http.ResponseWriter, r *http.Request, runID string) {
	principal, ok := h.authorize(w, r, assemblyLifecycleExecutePermission, true)
	if !ok {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	trace, ok := requireTraceID(w, r)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64  `json:"expected_version"`
		Reason          string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) || body.ExpectedVersion < 1 || !validLifecycleReason(body.Reason) {
		writeInvalidRequest(w, r)
		return
	}
	service, ok := h.lifecycle()
	if !ok {
		writeLifecycleUnavailable(w, r)
		return
	}
	result, err := service.CancelRun(r.Context(), CancelRunCommand{RunID: runID, ExpectedVersion: body.ExpectedVersion, Reason: body.Reason, ActorID: principal.AdminUserID, IdempotencyKey: key, TraceID: trace})
	if err != nil {
		writeError(w, r, err)
		return
	}
	result = normalizeRunProjection(result)
	if !validRun(result, "", runID) {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusOK, runResponseFrom(result))
}

type lifecycleArtifactStateResponse struct {
	ManifestID             string `json:"manifest_id"`
	ManifestChecksum       string `json:"manifest_checksum"`
	LockID                 string `json:"lock_id"`
	LockChecksum           string `json:"lock_checksum"`
	CatalogChecksum        string `json:"catalog_checksum"`
	TargetSnapshotChecksum string `json:"target_snapshot_checksum"`
}
type lifecycleChangeResponse struct {
	Path           string  `json:"path"`
	Action         string  `json:"action"`
	Ownership      string  `json:"ownership"`
	BeforeChecksum *string `json:"before_checksum"`
	AfterChecksum  *string `json:"after_checksum"`
	SourceID       string  `json:"source_id"`
	SourceVersion  string  `json:"source_version"`
}
type lifecycleConflictResponse struct {
	ConflictID  string   `json:"conflict_id"`
	Code        string   `json:"code"`
	Category    string   `json:"category"`
	Blocking    bool     `json:"blocking"`
	Message     string   `json:"message"`
	Paths       []string `json:"paths"`
	Remediation []string `json:"remediation"`
}
type lifecycleMigrationResponse struct {
	MigrationID   string `json:"migration_id"`
	Kind          string `json:"kind"`
	Reversibility string `json:"reversibility"`
	Summary       string `json:"summary"`
}
type lifecycleRollbackPolicyResponse struct {
	Strategy                    string `json:"strategy"`
	Automatic                   bool   `json:"automatic"`
	PredecessorManifestChecksum string `json:"predecessor_manifest_checksum"`
	PredecessorLockChecksum     string `json:"predecessor_lock_checksum"`
}
type lifecyclePlanResponse struct {
	LifecyclePlanID        string                          `json:"lifecycle_plan_id"`
	AssemblyID             string                          `json:"assembly_id"`
	ProductID              string                          `json:"product_id"`
	Operation              string                          `json:"operation"`
	Version                int64                           `json:"version"`
	Source                 lifecycleArtifactStateResponse  `json:"source"`
	TargetSnapshotChecksum string                          `json:"target_snapshot_checksum"`
	Changes                []lifecycleChangeResponse       `json:"changes"`
	Migrations             []lifecycleMigrationResponse    `json:"migrations"`
	Conflicts              []lifecycleConflictResponse     `json:"conflicts"`
	RegressionTests        []string                        `json:"regression_tests"`
	Rollback               lifecycleRollbackPolicyResponse `json:"rollback"`
	BlockingConflictCount  int                             `json:"blocking_conflict_count"`
	Executable             bool                            `json:"executable"`
	ConfirmationChecksum   string                          `json:"confirmation_checksum"`
	Statements             []string                        `json:"statements"`
	PlanChecksum           string                          `json:"plan_checksum"`
	CreatedAt              time.Time                       `json:"created_at"`
	AuditID                string                          `json:"audit_id"`
}
type lifecycleRecoveryResponse struct {
	Retryable         bool `json:"retryable"`
	RollbackAvailable bool `json:"rollback_available"`
	CancelAllowed     bool `json:"cancel_allowed"`
}
type lifecycleOperationResponse struct {
	OperationID           string                          `json:"operation_id"`
	RootOperationID       string                          `json:"root_operation_id"`
	RollbackOfOperationID *string                         `json:"rollback_of_operation_id"`
	LifecyclePlanID       *string                         `json:"lifecycle_plan_id"`
	AssemblyID            string                          `json:"assembly_id"`
	ProductID             string                          `json:"product_id"`
	Kind                  string                          `json:"kind"`
	Version               int64                           `json:"version"`
	Status                string                          `json:"status"`
	CurrentStep           *string                         `json:"current_step"`
	Source                lifecycleArtifactStateResponse  `json:"source"`
	Target                *lifecycleArtifactStateResponse `json:"target"`
	Recovery              lifecycleRecoveryResponse       `json:"recovery"`
	Diagnostics           []runDiagnosticResponse         `json:"diagnostics"`
	Reports               []runReportResponse             `json:"reports"`
	ManifestURL           string                          `json:"manifest_url,omitempty"`
	LockURL               string                          `json:"lock_url,omitempty"`
	CreatedAt             time.Time                       `json:"created_at"`
	UpdatedAt             time.Time                       `json:"updated_at"`
	CompletedAt           *time.Time                      `json:"completed_at"`
	AuditID               string                          `json:"audit_id"`
}

func lifecyclePlanResponseFrom(v LifecyclePlan) lifecyclePlanResponse {
	changes := make([]lifecycleChangeResponse, len(v.Changes))
	for i, c := range v.Changes {
		changes[i] = lifecycleChangeResponse{Path: c.Path, Action: c.Action, Ownership: c.Ownership, BeforeChecksum: optional(c.BeforeChecksum), AfterChecksum: optional(c.AfterChecksum), SourceID: c.SourceID, SourceVersion: c.SourceVersion}
	}
	migrations := make([]lifecycleMigrationResponse, len(v.Migrations))
	for i, migration := range v.Migrations {
		migrations[i] = lifecycleMigrationResponse{MigrationID: migration.MigrationID, Kind: migration.Kind, Reversibility: migration.Reversibility, Summary: migration.Summary}
	}
	conflicts := make([]lifecycleConflictResponse, len(v.Conflicts))
	for i, c := range v.Conflicts {
		conflicts[i] = lifecycleConflictResponse{ConflictID: c.ConflictID, Code: c.Code, Category: c.Category, Blocking: c.Blocking, Message: c.Message, Paths: nonNilStrings(c.Paths), Remediation: nonNilStrings(c.Remediation)}
	}
	return lifecyclePlanResponse{LifecyclePlanID: v.LifecyclePlanID, AssemblyID: v.AssemblyID, ProductID: v.ProductID, Operation: v.Operation, Version: v.Version, Source: lifecycleArtifactStateResponseFrom(v.Source), TargetSnapshotChecksum: v.TargetSnapshotChecksum, Changes: changes, Migrations: migrations, Conflicts: conflicts, RegressionTests: nonNilStrings(v.RegressionTests), Rollback: lifecycleRollbackPolicyResponse{Strategy: v.Rollback.Strategy, Automatic: v.Rollback.Automatic, PredecessorManifestChecksum: v.Rollback.PredecessorManifestChecksum, PredecessorLockChecksum: v.Rollback.PredecessorLockChecksum}, BlockingConflictCount: v.BlockingConflictCount, Executable: v.Executable, ConfirmationChecksum: v.ConfirmationChecksum, Statements: nonNilStrings(v.Statements), PlanChecksum: v.PlanChecksum, CreatedAt: v.CreatedAt, AuditID: v.AuditID}
}
func lifecycleOperationResponseFrom(v LifecycleOperation) lifecycleOperationResponse {
	var rollbackOf, planID, step *string
	if v.RollbackOfOperationID != "" {
		rollbackOf = &v.RollbackOfOperationID
	}
	if v.LifecyclePlanID != "" {
		planID = &v.LifecyclePlanID
	}
	if v.CurrentStep != "" {
		step = &v.CurrentStep
	}
	var target *lifecycleArtifactStateResponse
	if v.Target != nil {
		x := lifecycleArtifactStateResponseFrom(*v.Target)
		target = &x
	}
	diagnostics := make([]runDiagnosticResponse, len(v.Diagnostics))
	for i, d := range v.Diagnostics {
		diagnostics[i] = runDiagnosticResponse{DiagnosticID: d.DiagnosticID, Code: d.Code, Severity: d.Severity, Category: d.Category, Message: d.Message, Blocking: d.Blocking, Retryable: d.Retryable, Remediation: nonNilStrings(d.Remediation), RelatedPaths: nonNilStrings(d.RelatedPaths)}
	}
	reports := make([]runReportResponse, len(v.Reports))
	for i, x := range v.Reports {
		reports[i] = runReportResponse{ReportID: x.ReportID, Type: x.ReportType, Status: x.Status, Summary: x.Summary, Checksum: optional(x.Checksum), CreatedAt: x.CreatedAt}
	}
	return lifecycleOperationResponse{OperationID: v.OperationID, RootOperationID: v.RootOperationID, RollbackOfOperationID: rollbackOf, LifecyclePlanID: planID, AssemblyID: v.AssemblyID, ProductID: v.ProductID, Kind: v.Kind, Version: v.Version, Status: v.Status, CurrentStep: step, Source: lifecycleArtifactStateResponseFrom(v.Source), Target: target, Recovery: lifecycleRecoveryResponse{Retryable: v.Recovery.Retryable, RollbackAvailable: v.Recovery.RollbackAvailable, CancelAllowed: v.Recovery.CancelAllowed}, Diagnostics: diagnostics, Reports: reports, ManifestURL: v.ManifestURL, LockURL: v.LockURL, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, CompletedAt: v.CompletedAt, AuditID: v.AuditID}
}
func lifecycleArtifactStateResponseFrom(v LifecycleArtifactState) lifecycleArtifactStateResponse {
	return lifecycleArtifactStateResponse{ManifestID: v.ManifestID, ManifestChecksum: v.ManifestChecksum, LockID: v.LockID, LockChecksum: v.LockChecksum, CatalogChecksum: v.CatalogChecksum, TargetSnapshotChecksum: v.TargetSnapshotChecksum}
}
func optional(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func lifecycleTargetFrom(v lifecycleTargetRequest) (LifecycleTargetVersions, bool) {
	result := LifecycleTargetVersions{Packages: versionRefsFrom(v.Packages), Templates: versionRefsFrom(v.Templates), Generator: LifecycleVersionRef{ID: v.Generator.ID, Version: v.Generator.Version}, SDKs: versionRefsFrom(v.SDKs)}
	return result, validLifecycleVersionRefs(result.Packages, true) && validLifecycleVersionRefs(result.Templates, false) && validLifecycleVersionRefs(result.SDKs, true) && validLifecycleVersionRef(result.Generator)
}
func versionRefsFrom(v []lifecycleVersionRefRequest) []LifecycleVersionRef {
	result := make([]LifecycleVersionRef, len(v))
	for i, x := range v {
		result[i] = LifecycleVersionRef{ID: x.ID, Version: x.Version}
	}
	return result
}
func validLifecycleVersionRefs(values []LifecycleVersionRef, empty bool) bool {
	if !empty && len(values) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range values {
		if !validLifecycleVersionRef(v) || seen[v.ID] {
			return false
		}
		seen[v.ID] = true
	}
	return true
}
func validLifecycleVersionRef(v LifecycleVersionRef) bool {
	return validIdentifier(v.ID) && semverPattern.MatchString(v.Version)
}
func validLifecyclePaths(values []string) bool {
	if len(values) == 0 || len(values) > 500 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range values {
		lower := strings.ToLower(v)
		if len(v) > 500 || !safeRelativePathPattern.MatchString(v) || strings.Contains(v, "\\") || strings.HasPrefix(v, "/") || v == "." || v == ".." || strings.HasPrefix(v, "../") || strings.Contains(v, "/../") || strings.HasSuffix(v, "/..") || seen[lower] {
			return false
		}
		seen[lower] = true
	}
	return true
}
func validLifecycleReason(v string) bool {
	return v != "" && len(v) <= 500 && v == strings.TrimSpace(v) && !strings.ContainsAny(v, "\r\n\t")
}
func validLifecycleArtifactState(v LifecycleArtifactState) bool {
	return validIdentifier(v.ManifestID) && checksumPattern.MatchString(v.ManifestChecksum) && validIdentifier(v.LockID) && checksumPattern.MatchString(v.LockChecksum) && checksumPattern.MatchString(v.CatalogChecksum) && checksumPattern.MatchString(v.TargetSnapshotChecksum)
}
func validLifecyclePlan(v LifecyclePlan, assemblyID, kind string) bool {
	if !validIdentifier(v.LifecyclePlanID) || !validIdentifier(v.AssemblyID) || (assemblyID != "" && v.AssemblyID != assemblyID) || !validIdentifier(v.ProductID) || (kind != "" && v.Operation != kind) || (v.Operation != "upgrade" && v.Operation != "eject") || v.Version < 1 || !validLifecycleArtifactState(v.Source) || !checksumPattern.MatchString(v.TargetSnapshotChecksum) || v.Changes == nil || v.Migrations == nil || v.Conflicts == nil || v.RegressionTests == nil || len(v.Statements) == 0 || v.BlockingConflictCount < 0 || v.Executable != (v.BlockingConflictCount == 0) || !validLifecycleRollbackPolicy(v.Rollback) || !checksumPattern.MatchString(v.ConfirmationChecksum) || !checksumPattern.MatchString(v.PlanChecksum) || !validJSONObject(v.Document) || v.CreatedAt.IsZero() || !validIdentifier(v.AuditID) {
		return false
	}
	for _, change := range v.Changes {
		if !validLifecycleChange(change) {
			return false
		}
	}
	for _, conflict := range v.Conflicts {
		if !validLifecycleConflict(conflict) {
			return false
		}
	}
	for _, migration := range v.Migrations {
		if !validIdentifier(migration.MigrationID) || !containsString([]string{"database", "provider", "configuration"}, migration.Kind) || !containsString([]string{"reversible", "compensatable", "manual"}, migration.Reversibility) || !validLifecycleDisplayText(migration.Summary, 300) {
			return false
		}
	}
	for _, regression := range v.RegressionTests {
		if !validIdentifier(regression) {
			return false
		}
	}
	for _, statement := range v.Statements {
		if !validLifecycleDisplayText(statement, 300) {
			return false
		}
	}
	return true
}

func validLifecycleRollbackPolicy(value LifecycleRollbackPolicy) bool {
	return containsString([]string{"restore_predecessor", "compensate", "manual"}, value.Strategy) &&
		checksumPattern.MatchString(value.PredecessorManifestChecksum) && checksumPattern.MatchString(value.PredecessorLockChecksum)
}
func validLifecycleOperation(v LifecycleOperation) bool {
	if !validIdentifier(v.OperationID) || !validIdentifier(v.RootOperationID) || !validIdentifier(v.AssemblyID) || !validIdentifier(v.ProductID) || v.Version < 1 || !validLifecycleArtifactState(v.Source) || !validJSONObject(v.Document) || v.CreatedAt.IsZero() || v.UpdatedAt.IsZero() || !validIdentifier(v.AuditID) || (v.CurrentStep != "" && !validIdentifier(v.CurrentStep)) || v.Diagnostics == nil || v.Reports == nil {
		return false
	}
	switch v.Kind {
	case "upgrade", "eject":
		if !validIdentifier(v.LifecyclePlanID) || v.RollbackOfOperationID != "" {
			return false
		}
	case "rollback":
		if v.LifecyclePlanID != "" || !validIdentifier(v.RollbackOfOperationID) {
			return false
		}
	default:
		return false
	}
	switch v.Status {
	case "planned", "executing", "completed", "failed", "cancelled", "rolling_back", "rolled_back", "rollback_failed":
	default:
		return false
	}
	terminal := v.Status == "completed" || v.Status == "failed" || v.Status == "cancelled" || v.Status == "rolled_back" || v.Status == "rollback_failed"
	success := v.Status == "completed" || v.Status == "rolled_back"
	if terminal != (v.CompletedAt != nil) || success != (v.Target != nil) {
		return false
	}
	if v.Target == nil {
		if v.ManifestURL != "" || v.LockURL != "" {
			return false
		}
	} else if !validLifecycleArtifactState(*v.Target) || v.ManifestURL != manifestsPath+"/"+v.Target.ManifestID || v.LockURL != locksPath+"/"+v.Target.LockID {
		return false
	}
	for _, diagnostic := range v.Diagnostics {
		if !validLifecycleDiagnostic(diagnostic) {
			return false
		}
	}
	for _, report := range v.Reports {
		if !validLifecycleReport(report) {
			return false
		}
	}
	return true
}

func validLifecycleChange(v LifecycleChange) bool {
	return validLifecyclePath(v.Path) && containsString([]string{"create", "update", "delete", "unchanged", "eject"}, v.Action) && containsString([]string{"generated", "integration", "forked"}, v.Ownership) && (v.BeforeChecksum == "" || checksumPattern.MatchString(v.BeforeChecksum)) && (v.AfterChecksum == "" || checksumPattern.MatchString(v.AfterChecksum)) && validIdentifier(v.SourceID) && semverPattern.MatchString(v.SourceVersion)
}

func validLifecycleConflict(v LifecycleConflict) bool {
	if !validIdentifier(v.ConflictID) || !stableLifecycleCodePattern.MatchString(v.Code) || !containsString([]string{"custom", "generated_drift", "integration", "catalog", "migration", "rollback", "target"}, v.Category) || !validLifecycleDisplayText(v.Message, 500) || !validLifecyclePathList(v.Paths, true) || len(v.Remediation) > 20 {
		return false
	}
	for _, value := range v.Remediation {
		if !validLifecycleDisplayText(value, 300) {
			return false
		}
	}
	return true
}

func validLifecycleDiagnostic(v RunDiagnostic) bool {
	if !validIdentifier(v.DiagnosticID) || !stableLifecycleCodePattern.MatchString(v.Code) || !containsString([]string{"info", "warning", "error"}, v.Severity) || !stableLifecycleCodePattern.MatchString(v.Category) || !validLifecycleDisplayText(v.Message, 500) || len(v.Remediation) > 20 || !validLifecyclePathList(v.RelatedPaths, true) || v.CreatedAt.IsZero() {
		return false
	}
	for _, value := range v.Remediation {
		if !validLifecycleDisplayText(value, 300) {
			return false
		}
	}
	return true
}

func validLifecycleReport(v RunReport) bool {
	return validIdentifier(v.ReportID) && stableLifecycleCodePattern.MatchString(v.ReportType) && containsString([]string{"passed", "failed", "partial"}, v.Status) && validLifecycleDisplayText(v.Summary, 500) && (v.Checksum == "" || checksumPattern.MatchString(v.Checksum)) && !v.CreatedAt.IsZero()
}

func validLifecyclePath(value string) bool {
	return len(value) <= 500 && validLifecyclePathList([]string{value}, false)
}

func validLifecyclePathList(values []string, allowEmpty bool) bool {
	if len(values) == 0 {
		return allowEmpty
	}
	return validLifecyclePaths(values)
}

func validLifecycleDisplayText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\r\n\t") && !hostPathInDisplayPattern.MatchString(value)
}
func writeLifecycleUnavailable(w http.ResponseWriter, r *http.Request) {
	httpx.Error(w, r, http.StatusServiceUnavailable, "assembly.lifecycle_unavailable", "trusted assembly lifecycle services are unavailable")
}
