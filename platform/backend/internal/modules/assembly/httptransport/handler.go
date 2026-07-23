package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

const (
	blueprintsPath                 = "/api/v1/admin/blueprints"
	plansPath                      = "/api/v1/admin/assembly-plans"
	runsPath                       = "/api/v1/admin/assembly-runs"
	manifestsPath                  = "/api/v1/admin/assembly-manifests"
	locksPath                      = "/api/v1/admin/generated-project-locks"
	outputTargetsPath              = "/api/v1/admin/assembly-output-targets"
	catalogOptionsPath             = "/api/v1/admin/assembly-catalog-options"
	experimentalCatalogOptionsPath = "/api/v1/admin/experimental/assembly-catalog-options"
	experimentalBlueprintsPath     = "/api/v1/admin/experimental/blueprints"
	assemblyReadPermission         = "assembly.read"
	blueprintManagePermission      = "assembly.blueprint.manage"
	assemblyPlanPermission         = "assembly.plan"
	assemblyExecutePermission      = "assembly.execute"
	assemblyExperimentalPermission = "assembly.experimental.use"
	maxRequestBody                 = 1 << 20
)

var (
	identifierPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	referencePattern   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	packagePattern     = regexp.MustCompile(`^package\.[a-z][a-z0-9-]*$`)
	semverPattern      = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{15,127}$`)
	checksumPattern    = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// These errors are the HTTP application boundary. The composition adapter
// translates Assembly core errors to them without exposing repositories or
// persistence details to the transport.
var (
	ErrInvalidCommand          = errors.New("assembly command is invalid")
	ErrDocumentInvalid         = errors.New("assembly document is invalid")
	ErrNotFound                = errors.New("assembly resource was not found")
	ErrConflict                = errors.New("assembly resource conflicts with existing state")
	ErrVersionConflict         = errors.New("assembly resource version changed")
	ErrIdempotencyConflict     = errors.New("assembly idempotency key conflicts with an earlier request")
	ErrOperationInProgress     = errors.New("assembly operation is already in progress")
	ErrPlanUnavailable         = errors.New("assembly planner is unavailable")
	ErrPlanNotExecutable       = errors.New("assembly plan is not executable")
	ErrPlanNotConfirmed        = errors.New("assembly plan is not confirmed")
	ErrOutputTargetUnavailable = errors.New("assembly output target is unavailable")
	ErrLifecycleUnavailable    = errors.New("assembly lifecycle service is unavailable")
)

type Service interface {
	CreateBlueprint(context.Context, CreateBlueprintCommand) (Blueprint, error)
	GetBlueprint(context.Context, GetBlueprintCommand) (Blueprint, error)
	CreatePlan(context.Context, CreatePlanCommand) (Plan, error)
	GetPlan(context.Context, GetPlanCommand) (Plan, error)
	StartAssembly(context.Context, StartAssemblyCommand) (Run, error)
	GetRun(context.Context, GetRunCommand) (Run, error)
	GetManifest(context.Context, GetManifestCommand) (Manifest, error)
	GetLock(context.Context, GetLockCommand) (GeneratedProjectLock, error)
	ListOutputTargets(context.Context, ListOutputTargetsCommand) (OutputTargetList, error)
	ListCatalogOptions(context.Context, ListCatalogOptionsCommand) (CatalogOptions, error)
	ListExperimentalCatalogOptions(context.Context, ListCatalogOptionsCommand) (CatalogOptions, error)
}

type runRecoveryService interface {
	ListRuns(context.Context, ListRunsCommand) (RunPage, error)
	RetryRun(context.Context, RetryRunCommand) (Run, error)
}

type ListCatalogOptionsCommand struct {
	Target       string
	DeliveryMode string
	Environment  string
	ActorID      string
}

type CatalogOptions struct {
	CatalogScope    string
	CatalogRevision string
	Target          string
	DeliveryMode    string
	Environment     string
	Packages        []CatalogPackageOption
	Templates       []CatalogTemplateOption
	Generators      []CatalogToolOption
	SDKs            []CatalogToolOption
}

type CatalogRequirement struct {
	PackageID    string
	VersionRange string
}

type CatalogVersionRef struct {
	ID      string
	Version string
}

type CatalogPackageOption struct {
	PackageID              string
	Version                string
	Name                   string
	UserValue              string
	Dependencies           []CatalogRequirement
	Conflicts              []CatalogRequirement
	CompatibleTemplateRefs []CatalogVersionRef
}

type CatalogTemplateOption struct {
	TemplateID      string
	Version         string
	Name            string
	SupportedBlocks []string
}

type CatalogToolOption struct {
	ID      string
	Version string
	Name    string
}

type ListOutputTargetsCommand struct {
	Environment string
	ActorID     string
}

type OutputTarget struct {
	OutputTargetRef string
	Environment     string
	DisplayName     string
	Summary         string
	IsDefault       bool
}

type OutputTargetList struct {
	Environment            string
	DefaultOutputTargetRef *string
	Items                  []OutputTarget
}

type CreateBlueprintCommand struct {
	Document       json.RawMessage
	ActorID        string
	IdempotencyKey string
	TraceID        string
}

type GetBlueprintCommand struct {
	BlueprintID string
}

type CreatePlanCommand struct {
	BlueprintID      string
	BlueprintVersion int64
	Environment      string
	CatalogScope     string
	ActorID          string
	IdempotencyKey   string
	TraceID          string
}

type GetPlanCommand struct {
	PlanID string
}

type StartAssemblyCommand struct {
	BlueprintID          string
	PlanID               string
	ExpectedPlanVersion  int64
	PlanChecksum         string
	ConfirmationChecksum string
	OutputTargetRef      string
	ActorID              string
	IdempotencyKey       string
	TraceID              string
}

type GetRunCommand struct {
	RunID string
}

type ListRunsCommand struct {
	PageSize                  int
	Cursor, Status, ProductID string
}
type RetryRunCommand struct {
	RunID, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                         int64
}

type GetManifestCommand struct {
	AssemblyID string
}

type GetLockCommand struct {
	LockID string
}

type Blueprint struct {
	BlueprintID   string
	Version       int64
	SchemaVersion string
	Document      json.RawMessage
	Checksum      string
	Environments  []string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	AuditID       string
}

type Plan struct {
	PlanID               string
	Version              int64
	BlueprintID          string
	BlueprintVersion     int64
	SchemaVersion        string
	Environment          string
	Document             json.RawMessage
	Checksum             string
	ConfirmationChecksum string
	Review               PlanReview
	Executable           bool
	Confirmed            bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
	AuditID              string
}
type PlanReviewPackage struct{ PackageID, Version string }
type PlanReviewApplication struct{ ApplicationID, Target, Channel, DeliveryMode, TemplateID, TemplateVersion string }
type PlanReviewRisk struct {
	RiskID, Level, Category, Summary string
	RequiresConfirmation             bool
}
type PlanReview struct {
	Packages              []PlanReviewPackage
	Applications          []PlanReviewApplication
	Risks                 []PlanReviewRisk
	BlockingConflictCount int
	Statements            []string
}

type Run struct {
	RunID           string
	ProductID       string
	Version         int64
	RootRunID       string
	RetryOfRunID    string
	AttemptNumber   int
	PlanID          string
	PlanVersion     int64
	PlanChecksum    string
	OutputTargetRef string
	Status          string
	CurrentStepID   string
	Steps           []RunStep
	Recovery        RunRecovery
	Diagnostics     []RunDiagnostic
	Reports         []RunReport
	Document        json.RawMessage
	ManifestURL     string
	LockURL         string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CompletedAt     *time.Time
	AuditID         string
}

type RunStep struct {
	StepID, Kind, Status  string
	Attempt               int
	CompensationStatus    string
	StartedAt, FinishedAt *time.Time
	DiagnosticIDs         []string
}
type RunRecovery struct {
	Retryable, RollbackRequired bool
	ResumeFromStepID            string
}
type RunDiagnostic struct {
	DiagnosticID, Code, Severity, Category, Message string
	Blocking, Retryable                             bool
	Remediation, RelatedPaths                       []string
	CreatedAt                                       time.Time
}
type RunReport struct {
	ReportID, ReportType, Status, Summary, Checksum string
	CreatedAt                                       time.Time
}
type RunPage struct {
	Items      []RunSummary
	NextCursor string
}
type RunSummary struct {
	RunID, ProductID, PlanID, RootRunID, RetryOfRunID string
	Version                                           int64
	AttemptNumber                                     int
	Status, CurrentStepID                             string
	DiagnosticCount, ReportCount                      int
	CreatedAt, UpdatedAt                              time.Time
	CompletedAt                                       *time.Time
}

type Manifest struct {
	AssemblyID           string
	ProductID            string
	RunID                string
	LifecycleOperationID string
	SchemaVersion        string
	Document             json.RawMessage
	DocumentChecksum     string
	Checksum             string
	CreatedAt            time.Time
}

type GeneratedProjectLock struct {
	LockID               string
	ProductID            string
	RunID                string
	LifecycleOperationID string
	AssemblyID           string
	SchemaVersion        string
	Document             json.RawMessage
	DocumentChecksum     string
	Checksum             string
	CreatedAt            time.Time
}

type Handler struct {
	service Service
	guard   *adminrequest.Guard
}

func New(service Service, guard *adminrequest.Guard) *Handler {
	return &Handler{service: service, guard: guard}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.service == nil || h.guard == nil {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	route, ok := parseRoute(r)
	if !ok {
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	if route.kind != routeRuns && route.kind != routeOutputTargets && route.kind != routeCatalogOptions && route.kind != routeExperimentalCatalogOptions && r.URL.RawQuery != "" {
		httpx.Error(w, r, http.StatusBadRequest, "assembly.invalid_query", "query parameters are not supported")
		return
	}
	switch route.kind {
	case routeOutputTargets:
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowed(w, r, http.MethodGet)
			return
		}
		h.listOutputTargets(w, r)
	case routeCatalogOptions:
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowed(w, r, http.MethodGet)
			return
		}
		h.listCatalogOptions(w, r, false)
	case routeExperimentalCatalogOptions:
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowed(w, r, http.MethodGet)
			return
		}
		h.listCatalogOptions(w, r, true)
	case routeBlueprints:
		if r.Method != http.MethodPost {
			httpx.MethodNotAllowed(w, r, http.MethodPost)
			return
		}
		h.createBlueprint(w, r)
	case routeBlueprint:
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowed(w, r, http.MethodGet)
			return
		}
		h.getBlueprint(w, r, route.resourceID)
	case routePlanForBlueprint:
		if r.Method != http.MethodPost {
			httpx.MethodNotAllowed(w, r, http.MethodPost)
			return
		}
		h.createPlan(w, r, route.resourceID, false)
	case routeExperimentalPlanForBlueprint:
		if r.Method != http.MethodPost {
			httpx.MethodNotAllowed(w, r, http.MethodPost)
			return
		}
		h.createPlan(w, r, route.resourceID, true)
	case routeAssembleBlueprint:
		if r.Method != http.MethodPost {
			httpx.MethodNotAllowed(w, r, http.MethodPost)
			return
		}
		h.startAssembly(w, r, route.resourceID)
	case routePlan:
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowed(w, r, http.MethodGet)
			return
		}
		h.getPlan(w, r, route.resourceID)
	case routeRun:
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowed(w, r, http.MethodGet)
			return
		}
		h.getRun(w, r, route.resourceID)
	case routeRuns:
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowed(w, r, http.MethodGet)
			return
		}
		h.listRuns(w, r)
	case routeRetryRun:
		if r.Method != http.MethodPost {
			httpx.MethodNotAllowed(w, r, http.MethodPost)
			return
		}
		h.retryRun(w, r, route.resourceID)
	case routeCancelRun:
		if r.Method != http.MethodPost {
			httpx.MethodNotAllowed(w, r, http.MethodPost)
			return
		}
		h.cancelRun(w, r, route.resourceID)
	case routeUpgradePlan:
		if r.Method != http.MethodPost {
			httpx.MethodNotAllowed(w, r, http.MethodPost)
			return
		}
		h.createUpgradePlan(w, r, route.resourceID)
	case routeLifecycleSource:
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowed(w, r, http.MethodGet)
			return
		}
		h.getLifecycleSource(w, r, route.resourceID)
	case routeEjectPlan:
		if r.Method != http.MethodPost {
			httpx.MethodNotAllowed(w, r, http.MethodPost)
			return
		}
		h.createEjectPlan(w, r, route.resourceID)
	case routeLifecyclePlan:
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowed(w, r, http.MethodGet)
			return
		}
		h.getLifecyclePlan(w, r, route.resourceID)
	case routeExecuteLifecyclePlan:
		if r.Method != http.MethodPost {
			httpx.MethodNotAllowed(w, r, http.MethodPost)
			return
		}
		h.executeLifecyclePlan(w, r, route.resourceID)
	case routeLifecycleOperation:
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowed(w, r, http.MethodGet)
			return
		}
		h.getLifecycleOperation(w, r, route.resourceID)
	case routeCancelLifecycleOperation:
		if r.Method != http.MethodPost {
			httpx.MethodNotAllowed(w, r, http.MethodPost)
			return
		}
		h.cancelLifecycleOperation(w, r, route.resourceID)
	case routeRollbackLifecycleOperation:
		if r.Method != http.MethodPost {
			httpx.MethodNotAllowed(w, r, http.MethodPost)
			return
		}
		h.rollbackLifecycleOperation(w, r, route.resourceID)
	case routeManifest:
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowed(w, r, http.MethodGet)
			return
		}
		h.getManifest(w, r, route.resourceID)
	case routeLock:
		if r.Method != http.MethodGet {
			httpx.MethodNotAllowed(w, r, http.MethodGet)
			return
		}
		h.getLock(w, r, route.resourceID)
	default:
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
	}
}

func (h *Handler) listCatalogOptions(w http.ResponseWriter, r *http.Request, experimental bool) {
	if r.ContentLength > 0 || len(r.TransferEncoding) != 0 ||
		len(r.Header.Values("X-Assembly-Catalog-Scope")) != 0 || len(r.Header.Values("X-Catalog-Scope")) != 0 || len(r.Header.Values("Catalog-Scope")) != 0 {
		httpx.Error(w, r, http.StatusBadRequest, "assembly.invalid_query", "catalog scope and request bodies are not accepted")
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 3 || len(values["target"]) != 1 || len(values["delivery_mode"]) != 1 || len(values["environment"]) != 1 ||
		!validTarget(values["target"][0]) || !validDeliveryMode(values["delivery_mode"][0]) || !validEnvironment(values["environment"][0]) {
		httpx.Error(w, r, http.StatusBadRequest, "assembly.invalid_query", "exactly one supported target, delivery_mode, and environment are required")
		return
	}
	permission := assemblyPlanPermission
	if experimental {
		permission = assemblyExperimentalPermission
	}
	principal, ok := h.authorize(w, r, permission, false)
	if !ok {
		return
	}
	command := ListCatalogOptionsCommand{Target: values["target"][0], DeliveryMode: values["delivery_mode"][0], Environment: values["environment"][0], ActorID: principal.AdminUserID}
	var result CatalogOptions
	if experimental {
		result, err = h.service.ListExperimentalCatalogOptions(r.Context(), command)
	} else {
		result, err = h.service.ListCatalogOptions(r.Context(), command)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	expectedScope := "ordinary"
	if experimental {
		expectedScope = "experimental"
	}
	if !validCatalogOptions(result, expectedScope, command) {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusOK, catalogOptionsResponseFrom(result))
}

func (h *Handler) listOutputTargets(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 1 || len(values["environment"]) != 1 || !validEnvironment(values["environment"][0]) {
		httpx.Error(w, r, http.StatusBadRequest, "assembly.invalid_query", "exactly one supported environment query parameter is required")
		return
	}
	principal, ok := h.authorize(w, r, assemblyPlanPermission, false)
	if !ok {
		return
	}
	result, err := h.service.ListOutputTargets(r.Context(), ListOutputTargetsCommand{Environment: values["environment"][0], ActorID: principal.AdminUserID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validOutputTargetList(result) {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusOK, outputTargetListResponseFrom(result))
}

func (h *Handler) createBlueprint(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authorize(w, r, blueprintManagePermission, false)
	if !ok {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	document, ok := decodeJSONObject(w, r)
	if !ok {
		return
	}
	traceID, ok := requireTraceID(w, r)
	if !ok {
		return
	}
	result, err := h.service.CreateBlueprint(r.Context(), CreateBlueprintCommand{
		Document: document, ActorID: principal.AdminUserID,
		IdempotencyKey: idempotencyKey, TraceID: traceID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validBlueprint(result, "") {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusCreated, blueprintResponseFrom(result))
}

func (h *Handler) getBlueprint(w http.ResponseWriter, r *http.Request, blueprintID string) {
	if _, ok := h.authorize(w, r, assemblyReadPermission, false); !ok {
		return
	}
	result, err := h.service.GetBlueprint(r.Context(), GetBlueprintCommand{BlueprintID: blueprintID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validBlueprint(result, blueprintID) {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusOK, blueprintResponseFrom(result))
}

type createPlanRequest struct {
	BlueprintVersion *int64 `json:"blueprint_version"`
	Environment      string `json:"environment"`
}

func (h *Handler) createPlan(w http.ResponseWriter, r *http.Request, blueprintID string, experimental bool) {
	permission := assemblyPlanPermission
	if experimental {
		permission = assemblyExperimentalPermission
	}
	principal, ok := h.authorize(w, r, permission, false)
	if !ok {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body createPlanRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.BlueprintVersion == nil || *body.BlueprintVersion < 1 || !validEnvironment(body.Environment) {
		writeInvalidRequest(w, r)
		return
	}
	traceID, ok := requireTraceID(w, r)
	if !ok {
		return
	}
	catalogScope := "ordinary"
	if experimental {
		catalogScope = "experimental"
	}
	result, err := h.service.CreatePlan(r.Context(), CreatePlanCommand{
		BlueprintID: blueprintID, BlueprintVersion: *body.BlueprintVersion,
		Environment: body.Environment, CatalogScope: catalogScope, ActorID: principal.AdminUserID,
		IdempotencyKey: idempotencyKey, TraceID: traceID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validPlan(result, blueprintID, "") {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusCreated, planResponseFrom(result))
}

func (h *Handler) getPlan(w http.ResponseWriter, r *http.Request, planID string) {
	if _, ok := h.authorize(w, r, assemblyReadPermission, false); !ok {
		return
	}
	result, err := h.service.GetPlan(r.Context(), GetPlanCommand{PlanID: planID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validPlan(result, "", planID) {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusOK, planResponseFrom(result))
}

type startAssemblyRequest struct {
	PlanID              string                `json:"plan_id"`
	ExpectedPlanVersion *int64                `json:"expected_plan_version"`
	PlanChecksum        string                `json:"plan_checksum"`
	Confirmation        *assemblyConfirmation `json:"confirmation"`
	OutputTargetRef     string                `json:"output_target_ref"`
}

type assemblyConfirmation struct {
	Accepted        bool   `json:"accepted"`
	SummaryChecksum string `json:"summary_checksum"`
}

func (h *Handler) startAssembly(w http.ResponseWriter, r *http.Request, blueprintID string) {
	principal, ok := h.authorize(w, r, assemblyExecutePermission, true)
	if !ok {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body startAssemblyRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if !validIdentifier(body.PlanID) || body.ExpectedPlanVersion == nil || *body.ExpectedPlanVersion < 1 ||
		!checksumPattern.MatchString(body.PlanChecksum) || body.Confirmation == nil || !body.Confirmation.Accepted ||
		!checksumPattern.MatchString(body.Confirmation.SummaryChecksum) || !referencePattern.MatchString(body.OutputTargetRef) {
		writeInvalidRequest(w, r)
		return
	}
	traceID, ok := requireTraceID(w, r)
	if !ok {
		return
	}
	result, err := h.service.StartAssembly(r.Context(), StartAssemblyCommand{
		BlueprintID: blueprintID, PlanID: body.PlanID,
		ExpectedPlanVersion: *body.ExpectedPlanVersion, PlanChecksum: body.PlanChecksum,
		ConfirmationChecksum: body.Confirmation.SummaryChecksum, OutputTargetRef: body.OutputTargetRef,
		ActorID: principal.AdminUserID, IdempotencyKey: idempotencyKey, TraceID: traceID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	result = normalizeRunProjection(result)
	if !validRun(result, body.PlanID, "") {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusAccepted, runResponseFrom(result))
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request, runID string) {
	if _, ok := h.authorize(w, r, assemblyReadPermission, false); !ok {
		return
	}
	result, err := h.service.GetRun(r.Context(), GetRunCommand{RunID: runID})
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

func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r, assemblyReadPermission, false); !ok {
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeInvalidRequest(w, r)
		return
	}
	for key, value := range values {
		if (key != "page_size" && key != "cursor" && key != "status" && key != "product_id") || len(value) != 1 {
			writeInvalidRequest(w, r)
			return
		}
	}
	pageSize := 50
	if raw := values.Get("page_size"); raw != "" {
		var parsed int
		if _, err := fmt.Sscanf(raw, "%d", &parsed); err != nil || parsed < 1 || parsed > 100 || fmt.Sprintf("%d", parsed) != raw {
			writeInvalidRequest(w, r)
			return
		}
		pageSize = parsed
	}
	status, productID := values.Get("status"), values.Get("product_id")
	if status != "" && !validRunStatus(status) {
		writeInvalidRequest(w, r)
		return
	}
	if productID != "" && !validIdentifier(productID) {
		writeInvalidRequest(w, r)
		return
	}
	service, ok := h.service.(runRecoveryService)
	if !ok {
		writeInternalError(w, r)
		return
	}
	result, err := service.ListRuns(r.Context(), ListRunsCommand{PageSize: pageSize, Cursor: values.Get("cursor"), Status: status, ProductID: productID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	items := make([]runSummaryResponse, len(result.Items))
	for i, item := range result.Items {
		if !validRunSummary(item) {
			writeInternalError(w, r)
			return
		}
		items[i] = runSummaryResponseFrom(item)
	}
	var next *string
	if result.NextCursor != "" {
		next = &result.NextCursor
	}
	httpx.JSON(w, http.StatusOK, runPageResponse{Items: items, NextCursor: next})
}

type retryRunRequest struct {
	ExpectedVersion *int64 `json:"expected_version"`
}

func (h *Handler) retryRun(w http.ResponseWriter, r *http.Request, runID string) {
	principal, ok := h.authorize(w, r, assemblyExecutePermission, true)
	if !ok {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body retryRunRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ExpectedVersion == nil || *body.ExpectedVersion < 1 {
		writeInvalidRequest(w, r)
		return
	}
	traceID, ok := requireTraceID(w, r)
	if !ok {
		return
	}
	service, available := h.service.(runRecoveryService)
	if !available {
		writeInternalError(w, r)
		return
	}
	result, err := service.RetryRun(r.Context(), RetryRunCommand{RunID: runID, ExpectedVersion: *body.ExpectedVersion, ActorID: principal.AdminUserID, IdempotencyKey: key, TraceID: traceID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	result = normalizeRunProjection(result)
	if !validRun(result, "", result.RunID) {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusAccepted, runResponseFrom(result))
}

func (h *Handler) getManifest(w http.ResponseWriter, r *http.Request, assemblyID string) {
	if _, ok := h.authorize(w, r, assemblyReadPermission, false); !ok {
		return
	}
	result, err := h.service.GetManifest(r.Context(), GetManifestCommand{AssemblyID: assemblyID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validManifest(result, assemblyID) {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusOK, manifestResponseFrom(result))
}

func (h *Handler) getLock(w http.ResponseWriter, r *http.Request, lockID string) {
	if _, ok := h.authorize(w, r, assemblyReadPermission, false); !ok {
		return
	}
	result, err := h.service.GetLock(r.Context(), GetLockCommand{LockID: lockID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validLock(result, lockID) {
		writeInternalError(w, r)
		return
	}
	httpx.JSON(w, http.StatusOK, lockResponseFrom(result))
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, permission string, highRisk bool) (adminrequest.Principal, bool) {
	return h.guard.Authorize(w, r, permission, adminrequest.TargetScope{Type: "platform"}, highRisk)
}

type blueprintResponse struct {
	BlueprintID   string          `json:"blueprint_id"`
	Version       int64           `json:"version"`
	SchemaVersion string          `json:"schema_version"`
	Document      json.RawMessage `json:"document"`
	Checksum      string          `json:"checksum"`
	Environments  []string        `json:"environments"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	AuditID       string          `json:"audit_id"`
}

type planResponse struct {
	PlanID               string             `json:"plan_id"`
	Version              int64              `json:"version"`
	BlueprintID          string             `json:"blueprint_id"`
	BlueprintVersion     int64              `json:"blueprint_version"`
	SchemaVersion        string             `json:"schema_version"`
	Environment          string             `json:"environment"`
	Document             json.RawMessage    `json:"document"`
	Checksum             string             `json:"checksum"`
	ConfirmationChecksum string             `json:"confirmation_checksum"`
	Review               planReviewResponse `json:"review"`
	Executable           bool               `json:"executable"`
	Confirmed            bool               `json:"confirmed"`
	CreatedAt            time.Time          `json:"created_at"`
	UpdatedAt            time.Time          `json:"updated_at"`
	AuditID              string             `json:"audit_id"`
}
type planReviewPackageResponse struct {
	PackageID string `json:"package_id"`
	Version   string `json:"version"`
}
type planReviewApplicationResponse struct {
	ApplicationID   string `json:"application_id"`
	Target          string `json:"target"`
	Channel         string `json:"channel"`
	DeliveryMode    string `json:"delivery_mode"`
	TemplateID      string `json:"template_id"`
	TemplateVersion string `json:"template_version"`
}
type planReviewRiskResponse struct {
	RiskID               string `json:"risk_id"`
	Level                string `json:"level"`
	Category             string `json:"category"`
	Summary              string `json:"summary"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
}
type planReviewResponse struct {
	Packages              []planReviewPackageResponse     `json:"packages"`
	Applications          []planReviewApplicationResponse `json:"applications"`
	Risks                 []planReviewRiskResponse        `json:"risks"`
	BlockingConflictCount int                             `json:"blocking_conflict_count"`
	Statements            []string                        `json:"statements"`
}

type runResponse struct {
	RunID           string                  `json:"run_id"`
	ProductID       *string                 `json:"product_id"`
	Version         int64                   `json:"version"`
	RootRunID       string                  `json:"root_run_id"`
	RetryOfRunID    *string                 `json:"retry_of_run_id"`
	AttemptNumber   int                     `json:"attempt_number"`
	PlanID          string                  `json:"plan_id"`
	PlanVersion     int64                   `json:"plan_version"`
	PlanChecksum    string                  `json:"plan_checksum"`
	OutputTargetRef string                  `json:"output_target_ref"`
	Status          string                  `json:"status"`
	CurrentStepID   *string                 `json:"current_step_id"`
	Steps           []runStepResponse       `json:"steps"`
	Recovery        runRecoveryResponse     `json:"recovery"`
	Diagnostics     []runDiagnosticResponse `json:"diagnostics"`
	Reports         []runReportResponse     `json:"reports"`
	Document        json.RawMessage         `json:"document"`
	ManifestURL     string                  `json:"manifest_url,omitempty"`
	LockURL         string                  `json:"lock_url,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
	CompletedAt     *time.Time              `json:"completed_at"`
	AuditID         string                  `json:"audit_id"`
}

type runStepResponse struct {
	StepID             string     `json:"step_id"`
	Kind               string     `json:"kind"`
	Status             string     `json:"status"`
	Attempt            int        `json:"attempt"`
	CompensationStatus string     `json:"compensation_status"`
	StartedAt          *time.Time `json:"started_at"`
	FinishedAt         *time.Time `json:"finished_at"`
	DiagnosticIDs      []string   `json:"diagnostic_ids"`
}
type runRecoveryResponse struct {
	Retryable        bool    `json:"retryable"`
	RollbackRequired bool    `json:"rollback_required"`
	ResumeFromStepID *string `json:"resume_from_step_id"`
}
type runDiagnosticResponse struct {
	DiagnosticID string   `json:"diagnostic_id"`
	Code         string   `json:"code"`
	Severity     string   `json:"severity"`
	Category     string   `json:"category"`
	Message      string   `json:"message"`
	Blocking     bool     `json:"blocking"`
	Retryable    bool     `json:"retryable"`
	Remediation  []string `json:"remediation"`
	RelatedPaths []string `json:"related_paths"`
}
type runReportResponse struct {
	ReportID  string    `json:"report_id"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	Summary   string    `json:"summary"`
	Checksum  *string   `json:"checksum"`
	CreatedAt time.Time `json:"created_at"`
}
type runSummaryResponse struct {
	RunID           string     `json:"run_id"`
	ProductID       *string    `json:"product_id"`
	PlanID          string     `json:"plan_id"`
	Version         int64      `json:"version"`
	RootRunID       string     `json:"root_run_id"`
	RetryOfRunID    *string    `json:"retry_of_run_id"`
	AttemptNumber   int        `json:"attempt_number"`
	Status          string     `json:"status"`
	CurrentStepID   *string    `json:"current_step_id"`
	DiagnosticCount int        `json:"diagnostic_count"`
	ReportCount     int        `json:"report_count"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CompletedAt     *time.Time `json:"completed_at"`
}
type runPageResponse struct {
	Items      []runSummaryResponse `json:"items"`
	NextCursor *string              `json:"next_cursor"`
}

type outputTargetResponse struct {
	OutputTargetRef string `json:"output_target_ref"`
	DisplayName     string `json:"display_name"`
	Summary         string `json:"summary"`
	IsDefault       bool   `json:"is_default"`
}

type outputTargetListResponse struct {
	Environment            string                 `json:"environment"`
	DefaultPolicy          string                 `json:"default_policy"`
	DefaultOutputTargetRef *string                `json:"default_output_target_ref"`
	Items                  []outputTargetResponse `json:"items"`
}

type catalogRequirementResponse struct {
	PackageID    string `json:"package_id"`
	VersionRange string `json:"version_range"`
}
type catalogVersionRefResponse struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}
type catalogPackageOptionResponse struct {
	PackageID              string                       `json:"package_id"`
	Version                string                       `json:"version"`
	Name                   string                       `json:"name"`
	UserValue              string                       `json:"user_value"`
	Dependencies           []catalogRequirementResponse `json:"dependencies"`
	Conflicts              []catalogRequirementResponse `json:"conflicts"`
	CompatibleTemplateRefs []catalogVersionRefResponse  `json:"compatible_template_refs"`
}
type catalogTemplateOptionResponse struct {
	TemplateID      string   `json:"template_id"`
	Version         string   `json:"version"`
	Name            string   `json:"name"`
	SupportedBlocks []string `json:"supported_blocks"`
}
type catalogToolOptionResponse struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Name    string `json:"name"`
}
type catalogOptionsResponse struct {
	CatalogScope    string                          `json:"catalog_scope"`
	CatalogRevision string                          `json:"catalog_revision"`
	Target          string                          `json:"target"`
	DeliveryMode    string                          `json:"delivery_mode"`
	Environment     string                          `json:"environment"`
	Packages        []catalogPackageOptionResponse  `json:"packages"`
	Templates       []catalogTemplateOptionResponse `json:"templates"`
	Generators      []catalogToolOptionResponse     `json:"generators"`
	SDKs            []catalogToolOptionResponse     `json:"sdks"`
}

type manifestResponse struct {
	AssemblyID           string          `json:"assembly_id"`
	ProductID            string          `json:"product_id"`
	RunID                string          `json:"run_id,omitempty"`
	LifecycleOperationID string          `json:"lifecycle_operation_id,omitempty"`
	SchemaVersion        string          `json:"schema_version"`
	Document             json.RawMessage `json:"document"`
	DocumentChecksum     string          `json:"document_checksum"`
	Checksum             string          `json:"checksum"`
	CreatedAt            time.Time       `json:"created_at"`
}

type lockResponse struct {
	LockID               string          `json:"lock_id"`
	ProductID            string          `json:"product_id"`
	RunID                string          `json:"run_id,omitempty"`
	LifecycleOperationID string          `json:"lifecycle_operation_id,omitempty"`
	AssemblyID           string          `json:"assembly_id"`
	SchemaVersion        string          `json:"schema_version"`
	Document             json.RawMessage `json:"document"`
	DocumentChecksum     string          `json:"document_checksum"`
	Checksum             string          `json:"checksum"`
	CreatedAt            time.Time       `json:"created_at"`
}

func blueprintResponseFrom(value Blueprint) blueprintResponse {
	return blueprintResponse{BlueprintID: value.BlueprintID, Version: value.Version,
		SchemaVersion: value.SchemaVersion, Document: value.Document, Checksum: value.Checksum, Environments: value.Environments,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, AuditID: value.AuditID}
}

func planResponseFrom(value Plan) planResponse {
	packages := make([]planReviewPackageResponse, len(value.Review.Packages))
	for i, v := range value.Review.Packages {
		packages[i] = planReviewPackageResponse{PackageID: v.PackageID, Version: v.Version}
	}
	applications := make([]planReviewApplicationResponse, len(value.Review.Applications))
	for i, v := range value.Review.Applications {
		applications[i] = planReviewApplicationResponse{ApplicationID: v.ApplicationID, Target: v.Target, Channel: v.Channel, DeliveryMode: v.DeliveryMode, TemplateID: v.TemplateID, TemplateVersion: v.TemplateVersion}
	}
	risks := make([]planReviewRiskResponse, len(value.Review.Risks))
	for i, v := range value.Review.Risks {
		risks[i] = planReviewRiskResponse{RiskID: v.RiskID, Level: v.Level, Category: v.Category, Summary: v.Summary, RequiresConfirmation: v.RequiresConfirmation}
	}
	return planResponse{PlanID: value.PlanID, Version: value.Version,
		BlueprintID: value.BlueprintID, BlueprintVersion: value.BlueprintVersion, SchemaVersion: value.SchemaVersion,
		Environment: value.Environment, Document: value.Document, Checksum: value.Checksum, ConfirmationChecksum: value.ConfirmationChecksum, Review: planReviewResponse{Packages: packages, Applications: applications, Risks: risks, BlockingConflictCount: value.Review.BlockingConflictCount, Statements: nonNilStrings(value.Review.Statements)},
		Executable: value.Executable, Confirmed: value.Confirmed, CreatedAt: value.CreatedAt,
		UpdatedAt: value.UpdatedAt, AuditID: value.AuditID}
}

func runResponseFrom(value Run) runResponse {
	var productID, retryOf, current *string
	if value.ProductID != "" {
		productID = &value.ProductID
	}
	if value.RetryOfRunID != "" {
		retryOf = &value.RetryOfRunID
	}
	if value.CurrentStepID != "" {
		current = &value.CurrentStepID
	}
	steps := make([]runStepResponse, len(value.Steps))
	for i, v := range value.Steps {
		steps[i] = runStepResponse{StepID: v.StepID, Kind: v.Kind, Status: v.Status, Attempt: v.Attempt, CompensationStatus: v.CompensationStatus, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt, DiagnosticIDs: nonNilStrings(v.DiagnosticIDs)}
	}
	var resume *string
	if value.Recovery.ResumeFromStepID != "" {
		resume = &value.Recovery.ResumeFromStepID
	}
	diagnostics := make([]runDiagnosticResponse, len(value.Diagnostics))
	for i, v := range value.Diagnostics {
		diagnostics[i] = runDiagnosticResponse{DiagnosticID: v.DiagnosticID, Code: v.Code, Severity: v.Severity, Category: v.Category, Message: v.Message, Blocking: v.Blocking, Retryable: v.Retryable, Remediation: nonNilStrings(v.Remediation), RelatedPaths: nonNilStrings(v.RelatedPaths)}
	}
	reports := make([]runReportResponse, len(value.Reports))
	for i, v := range value.Reports {
		var checksum *string
		if v.Checksum != "" {
			checksum = &v.Checksum
		}
		reports[i] = runReportResponse{ReportID: v.ReportID, Type: v.ReportType, Status: v.Status, Summary: v.Summary, Checksum: checksum, CreatedAt: v.CreatedAt}
	}
	return runResponse{RunID: value.RunID, PlanID: value.PlanID,
		ProductID: productID, Version: value.Version, RootRunID: value.RootRunID, RetryOfRunID: retryOf, AttemptNumber: value.AttemptNumber,
		PlanVersion: value.PlanVersion, PlanChecksum: value.PlanChecksum, OutputTargetRef: value.OutputTargetRef, Status: value.Status,
		CurrentStepID: current, Steps: steps, Recovery: runRecoveryResponse{Retryable: value.Recovery.Retryable, RollbackRequired: value.Recovery.RollbackRequired, ResumeFromStepID: resume}, Diagnostics: diagnostics, Reports: reports,
		Document: value.Document, ManifestURL: value.ManifestURL, LockURL: value.LockURL,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, CompletedAt: value.CompletedAt, AuditID: value.AuditID}
}

func normalizeRunProjection(value Run) Run {
	if value.RootRunID == "" {
		value.RootRunID = value.RunID
	}
	if value.AttemptNumber == 0 {
		value.AttemptNumber = 1
	}
	if value.Version == 0 {
		value.Version = 1
	}
	if value.Steps == nil {
		value.Steps = []RunStep{}
	}
	if value.Diagnostics == nil {
		value.Diagnostics = []RunDiagnostic{}
	}
	if value.Reports == nil {
		value.Reports = []RunReport{}
	}
	return value
}

func runSummaryResponseFrom(value RunSummary) runSummaryResponse {
	var product, retry, current *string
	if value.ProductID != "" {
		product = &value.ProductID
	}
	if value.RetryOfRunID != "" {
		retry = &value.RetryOfRunID
	}
	if value.CurrentStepID != "" {
		current = &value.CurrentStepID
	}
	return runSummaryResponse{RunID: value.RunID, ProductID: product, PlanID: value.PlanID, Version: value.Version, RootRunID: value.RootRunID, RetryOfRunID: retry, AttemptNumber: value.AttemptNumber, Status: value.Status, CurrentStepID: current, DiagnosticCount: value.DiagnosticCount, ReportCount: value.ReportCount, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, CompletedAt: value.CompletedAt}
}
func validRunSummary(value RunSummary) bool {
	return validIdentifier(value.RunID) && validIdentifier(value.PlanID) && validIdentifier(value.RootRunID) && value.Version > 0 && value.AttemptNumber > 0 && validRunStatus(value.Status) && value.DiagnosticCount >= 0 && value.ReportCount >= 0 && !value.CreatedAt.IsZero() && !value.UpdatedAt.IsZero()
}
func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func outputTargetListResponseFrom(value OutputTargetList) outputTargetListResponse {
	items := make([]outputTargetResponse, len(value.Items))
	for index, item := range value.Items {
		items[index] = outputTargetResponse{OutputTargetRef: item.OutputTargetRef, DisplayName: item.DisplayName, Summary: item.Summary, IsDefault: item.IsDefault}
	}
	return outputTargetListResponse{Environment: value.Environment, DefaultPolicy: "explicit", DefaultOutputTargetRef: value.DefaultOutputTargetRef, Items: items}
}

func catalogOptionsResponseFrom(value CatalogOptions) catalogOptionsResponse {
	result := catalogOptionsResponse{CatalogScope: value.CatalogScope, CatalogRevision: value.CatalogRevision, Target: value.Target, DeliveryMode: value.DeliveryMode, Environment: value.Environment,
		Packages: make([]catalogPackageOptionResponse, len(value.Packages)), Templates: make([]catalogTemplateOptionResponse, len(value.Templates)), Generators: toolOptionResponses(value.Generators), SDKs: toolOptionResponses(value.SDKs)}
	for i, item := range value.Packages {
		result.Packages[i] = catalogPackageOptionResponse{PackageID: item.PackageID, Version: item.Version, Name: item.Name, UserValue: item.UserValue,
			Dependencies: requirementResponses(item.Dependencies), Conflicts: requirementResponses(item.Conflicts), CompatibleTemplateRefs: versionRefResponses(item.CompatibleTemplateRefs)}
	}
	for i, item := range value.Templates {
		result.Templates[i] = catalogTemplateOptionResponse{TemplateID: item.TemplateID, Version: item.Version, Name: item.Name, SupportedBlocks: item.SupportedBlocks}
	}
	return result
}

func requirementResponses(values []CatalogRequirement) []catalogRequirementResponse {
	result := make([]catalogRequirementResponse, len(values))
	for i, v := range values {
		result[i] = catalogRequirementResponse{PackageID: v.PackageID, VersionRange: v.VersionRange}
	}
	return result
}
func versionRefResponses(values []CatalogVersionRef) []catalogVersionRefResponse {
	result := make([]catalogVersionRefResponse, len(values))
	for i, v := range values {
		result[i] = catalogVersionRefResponse{ID: v.ID, Version: v.Version}
	}
	return result
}
func toolOptionResponses(values []CatalogToolOption) []catalogToolOptionResponse {
	result := make([]catalogToolOptionResponse, len(values))
	for i, v := range values {
		result[i] = catalogToolOptionResponse{ID: v.ID, Version: v.Version, Name: v.Name}
	}
	return result
}

func validCatalogOptions(value CatalogOptions, expectedScope string, command ListCatalogOptionsCommand) bool {
	if value.CatalogScope != expectedScope || !referencePattern.MatchString(value.CatalogRevision) || value.Target != command.Target || value.DeliveryMode != command.DeliveryMode || value.Environment != command.Environment || value.Packages == nil || value.Templates == nil || value.Generators == nil || value.SDKs == nil {
		return false
	}
	for i, item := range value.Packages {
		if !packagePattern.MatchString(item.PackageID) || !semverPattern.MatchString(item.Version) || !validCatalogDisplay(item.Name, 120) || !validCatalogDisplay(item.UserValue, 240) || item.Dependencies == nil || item.Conflicts == nil || item.CompatibleTemplateRefs == nil || (i > 0 && value.Packages[i-1].PackageID+"\x00"+value.Packages[i-1].Version >= item.PackageID+"\x00"+item.Version) {
			return false
		}
		if !validRequirements(item.Dependencies) || !validRequirements(item.Conflicts) || !validVersionRefs(item.CompatibleTemplateRefs) {
			return false
		}
	}
	for i, item := range value.Templates {
		if !validIdentifier(item.TemplateID) || !semverPattern.MatchString(item.Version) || !validCatalogDisplay(item.Name, 120) || !validStableStrings(item.SupportedBlocks) || (i > 0 && value.Templates[i-1].TemplateID+"\x00"+value.Templates[i-1].Version >= item.TemplateID+"\x00"+item.Version) {
			return false
		}
	}
	return validTools(value.Generators) && validTools(value.SDKs)
}

func validRequirements(values []CatalogRequirement) bool {
	for i, v := range values {
		if !packagePattern.MatchString(v.PackageID) || v.VersionRange == "" || (i > 0 && values[i-1].PackageID+"\x00"+values[i-1].VersionRange >= v.PackageID+"\x00"+v.VersionRange) {
			return false
		}
	}
	return true
}

func validVersionRefs(values []CatalogVersionRef) bool {
	for i, v := range values {
		if !validIdentifier(v.ID) || !semverPattern.MatchString(v.Version) || (i > 0 && values[i-1].ID+"\x00"+values[i-1].Version >= v.ID+"\x00"+v.Version) {
			return false
		}
	}
	return true
}

func validTools(values []CatalogToolOption) bool {
	for i, v := range values {
		if !validIdentifier(v.ID) || !semverPattern.MatchString(v.Version) || !validCatalogDisplay(v.Name, 120) || (i > 0 && values[i-1].ID+"\x00"+values[i-1].Version >= v.ID+"\x00"+v.Version) {
			return false
		}
	}
	return true
}

func validStableStrings(values []string) bool {
	if values == nil {
		return false
	}
	for index, value := range values {
		if !referencePattern.MatchString(value) || (index > 0 && values[index-1] >= value) {
			return false
		}
	}
	return true
}

func validCatalogDisplay(value string, maximum int) bool {
	if value == "" || value != strings.TrimSpace(value) || utf8.RuneCountInString(value) > maximum || strings.Contains(value, "\\") {
		return false
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.Contains(lower, "file://") || strings.Contains(value, "../") || strings.Contains(value, "/..") {
		return false
	}
	if len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && value[2] == '/' {
		return false
	}
	for _, character := range value {
		if character <= 0x1f || character == 0x7f {
			return false
		}
	}
	return true
}

func validOutputTargetList(value OutputTargetList) bool {
	if !validEnvironment(value.Environment) || value.Items == nil {
		return false
	}
	seen := make(map[string]struct{}, len(value.Items))
	defaultRef := ""
	for index, item := range value.Items {
		if item.Environment != value.Environment || !referencePattern.MatchString(item.OutputTargetRef) ||
			!validRedactedDisplay(item.DisplayName, 120) || !validRedactedDisplay(item.Summary, 240) {
			return false
		}
		if index > 0 && value.Items[index-1].OutputTargetRef >= item.OutputTargetRef {
			return false
		}
		if _, duplicate := seen[item.OutputTargetRef]; duplicate {
			return false
		}
		seen[item.OutputTargetRef] = struct{}{}
		if item.IsDefault {
			if defaultRef != "" {
				return false
			}
			defaultRef = item.OutputTargetRef
		}
	}
	if value.DefaultOutputTargetRef == nil {
		return defaultRef == ""
	}
	return defaultRef != "" && *value.DefaultOutputTargetRef == defaultRef
}

func validRedactedDisplay(value string, maximum int) bool {
	if value == "" || value != strings.TrimSpace(value) || utf8.RuneCountInString(value) > maximum || strings.ContainsAny(value, "/\\") {
		return false
	}
	for _, character := range value {
		if character <= 0x1f || character == 0x7f {
			return false
		}
	}
	return true
}

func manifestResponseFrom(value Manifest) manifestResponse {
	return manifestResponse{AssemblyID: value.AssemblyID, ProductID: value.ProductID, RunID: value.RunID, LifecycleOperationID: value.LifecycleOperationID,
		SchemaVersion: value.SchemaVersion, Document: value.Document, DocumentChecksum: value.DocumentChecksum,
		Checksum: value.Checksum, CreatedAt: value.CreatedAt}
}

func lockResponseFrom(value GeneratedProjectLock) lockResponse {
	return lockResponse{LockID: value.LockID, ProductID: value.ProductID, RunID: value.RunID, LifecycleOperationID: value.LifecycleOperationID,
		AssemblyID: value.AssemblyID, SchemaVersion: value.SchemaVersion, Document: value.Document,
		DocumentChecksum: value.DocumentChecksum, Checksum: value.Checksum, CreatedAt: value.CreatedAt}
}

func validBlueprint(value Blueprint, blueprintID string) bool {
	return validIdentifier(value.BlueprintID) &&
		(blueprintID == "" || value.BlueprintID == blueprintID) && value.Version > 0 &&
		strings.TrimSpace(value.SchemaVersion) != "" && validJSONObject(value.Document) &&
		validEnvironmentList(value.Environments) &&
		checksumPattern.MatchString(value.Checksum) && !value.CreatedAt.IsZero() && !value.UpdatedAt.IsZero() &&
		validIdentifier(value.AuditID)
}

func validPlan(value Plan, blueprintID, planID string) bool {
	return validIdentifier(value.PlanID) &&
		(planID == "" || value.PlanID == planID) && validIdentifier(value.BlueprintID) &&
		(blueprintID == "" || value.BlueprintID == blueprintID) && value.Version > 0 &&
		value.BlueprintVersion > 0 && strings.TrimSpace(value.SchemaVersion) != "" &&
		validEnvironment(value.Environment) && validJSONObject(value.Document) &&
		checksumPattern.MatchString(value.ConfirmationChecksum) &&
		validPlanReview(value.Review) &&
		checksumPattern.MatchString(value.Checksum) && !value.CreatedAt.IsZero() && !value.UpdatedAt.IsZero() &&
		validIdentifier(value.AuditID)
}

func validPlanReview(value PlanReview) bool {
	if len(value.Applications) == 0 || len(value.Statements) == 0 || value.BlockingConflictCount < 0 {
		return false
	}
	for _, item := range value.Packages {
		if !packagePattern.MatchString(item.PackageID) || !semverPattern.MatchString(item.Version) {
			return false
		}
	}
	for _, item := range value.Applications {
		if !validIdentifier(item.ApplicationID) || !validTarget(item.Target) || !validIdentifier(item.Channel) || !validDeliveryMode(item.DeliveryMode) || !validIdentifier(item.TemplateID) || !semverPattern.MatchString(item.TemplateVersion) {
			return false
		}
	}
	for _, item := range value.Risks {
		if !validIdentifier(item.RiskID) || (item.Level != "low" && item.Level != "medium" && item.Level != "high") || !containsString([]string{"security", "data", "compatibility", "provider", "generation", "rollback"}, item.Category) || len(item.Summary) < 1 || len(item.Summary) > 512 || strings.ContainsAny(item.Summary, "\r\n\t") {
			return false
		}
	}
	seen := map[string]bool{}
	for _, statement := range value.Statements {
		if seen[statement] || len(statement) < 1 || len(statement) > 512 || strings.ContainsAny(statement, "\r\n\t") {
			return false
		}
		seen[statement] = true
	}
	return true
}
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validEnvironmentList(values []string) bool {
	if len(values) == 0 {
		return false
	}
	rank := map[string]int{"development": 1, "test": 2, "staging": 3, "production": 4}
	previous := 0
	for _, value := range values {
		current := rank[value]
		if current == 0 || current <= previous {
			return false
		}
		previous = current
	}
	return true
}

func validRun(value Run, planID, runID string) bool {
	return validIdentifier(value.RunID) &&
		validIdentifier(value.RootRunID) && value.AttemptNumber > 0 && value.Version > 0 &&
		(runID == "" || value.RunID == runID) && validIdentifier(value.PlanID) &&
		(planID == "" || value.PlanID == planID) && value.PlanVersion > 0 &&
		checksumPattern.MatchString(value.PlanChecksum) && referencePattern.MatchString(value.OutputTargetRef) && validRunStatus(value.Status) &&
		validJSONObject(value.Document) && !value.CreatedAt.IsZero() && !value.UpdatedAt.IsZero() &&
		validIdentifier(value.AuditID)
}

func validManifest(value Manifest, assemblyID string) bool {
	return validIdentifier(value.AssemblyID) && value.AssemblyID == assemblyID && validIdentifier(value.ProductID) &&
		(validIdentifier(value.RunID) != validIdentifier(value.LifecycleOperationID)) && strings.TrimSpace(value.SchemaVersion) != "" && validJSONObject(value.Document) &&
		checksumPattern.MatchString(value.DocumentChecksum) && checksumPattern.MatchString(value.Checksum) && !value.CreatedAt.IsZero()
}

func validLock(value GeneratedProjectLock, lockID string) bool {
	return validIdentifier(value.LockID) && value.LockID == lockID && validIdentifier(value.ProductID) &&
		(validIdentifier(value.RunID) != validIdentifier(value.LifecycleOperationID)) && validIdentifier(value.AssemblyID) && strings.TrimSpace(value.SchemaVersion) != "" &&
		validJSONObject(value.Document) && checksumPattern.MatchString(value.DocumentChecksum) &&
		checksumPattern.MatchString(value.Checksum) && !value.CreatedAt.IsZero()
}

func validRunStatus(value string) bool {
	switch value {
	case "planned", "provisioning", "generating", "validating", "completed", "failed", "cancelled", "rolling_back", "rolled_back":
		return true
	default:
		return false
	}
}

func validEnvironment(value string) bool {
	return value == "development" || value == "test" || value == "staging" || value == "production"
}

func validTarget(value string) bool {
	switch value {
	case "web", "desktop_webview", "h5", "wechat_miniprogram", "mobile_app":
		return true
	}
	return false
}
func validDeliveryMode(value string) bool {
	return value == "hosted" || value == "package" || value == "generated_source"
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || !idempotencyPattern.MatchString(values[0]) || strings.TrimSpace(values[0]) != values[0] {
		httpx.Error(w, r, http.StatusBadRequest, "assembly.invalid_idempotency_key", "exactly one safe Idempotency-Key of 16 to 128 characters is required")
		return "", false
	}
	return values[0], true
}

func requireTraceID(w http.ResponseWriter, r *http.Request) (string, bool) {
	traceID := requestid.FromContext(r.Context())
	if traceID == "" {
		writeInternalError(w, r)
		return "", false
	}
	return traceID, true
}

func decodeJSONObject(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	var document json.RawMessage
	if !decodeJSON(w, r, &document) {
		return nil, false
	}
	if !validJSONObject(document) {
		writeInvalidRequest(w, r)
		return nil, false
	}
	return append(json.RawMessage(nil), document...), true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		httpx.Error(w, r, http.StatusUnsupportedMediaType, "assembly.unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeDecodeError(w, r, err)
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeDecodeError(w, r, err)
				return false
			}
		}
		writeInvalidRequest(w, r)
		return false
	}
	return true
}

func writeDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		httpx.Error(w, r, http.StatusRequestEntityTooLarge, "assembly.request_too_large", "request body exceeds 1 MiB")
		return
	}
	writeInvalidRequest(w, r)
}

func validJSONObject(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}' && json.Valid(trimmed)
}

func writeInvalidRequest(w http.ResponseWriter, r *http.Request) {
	httpx.Error(w, r, http.StatusBadRequest, "assembly.invalid_request", "assembly request is invalid")
}

func writeInternalError(w http.ResponseWriter, r *http.Request) {
	httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidCommand), errors.Is(err, ErrDocumentInvalid):
		writeInvalidRequest(w, r)
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "assembly.not_found", "assembly resource was not found")
	case errors.Is(err, ErrIdempotencyConflict):
		httpx.Error(w, r, http.StatusConflict, "assembly.idempotency_conflict", "Idempotency-Key was reused with a different request")
	case errors.Is(err, ErrVersionConflict):
		httpx.Error(w, r, http.StatusConflict, "assembly.version_conflict", "assembly resource version changed")
	case errors.Is(err, ErrOperationInProgress):
		retryAfter := 3
		httpx.ErrorWithOptions(w, r, http.StatusConflict, "assembly.operation_in_progress", "assembly operation is already in progress", httpx.ErrorOptions{Retryable: true, RetryAfterSeconds: &retryAfter})
	case errors.Is(err, ErrPlanUnavailable):
		retryAfter := 5
		httpx.ErrorWithOptions(w, r, http.StatusServiceUnavailable, "assembly.planner_unavailable", "assembly planning is temporarily unavailable", httpx.ErrorOptions{Retryable: true, RetryAfterSeconds: &retryAfter})
	case errors.Is(err, ErrPlanNotExecutable):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "assembly.plan_not_executable", "assembly plan is not executable")
	case errors.Is(err, ErrPlanNotConfirmed):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "assembly.plan_not_confirmed", "assembly plan confirmation is invalid or missing")
	case errors.Is(err, ErrOutputTargetUnavailable):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "assembly.output_target_unavailable", "assembly output target is unavailable")
	case errors.Is(err, ErrLifecycleUnavailable):
		writeLifecycleUnavailable(w, r)
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "assembly.conflict", "assembly resource conflicts with existing state")
	default:
		writeInternalError(w, r)
	}
}

type routeKind int

const (
	routeBlueprints routeKind = iota + 1
	routeBlueprint
	routePlanForBlueprint
	routeExperimentalPlanForBlueprint
	routeAssembleBlueprint
	routePlan
	routeRuns
	routeRun
	routeRetryRun
	routeManifest
	routeLock
	routeOutputTargets
	routeCatalogOptions
	routeExperimentalCatalogOptions
	routeCancelRun
	routeLifecycleSource
	routeUpgradePlan
	routeEjectPlan
	routeLifecyclePlan
	routeExecuteLifecyclePlan
	routeLifecycleOperation
	routeCancelLifecycleOperation
	routeRollbackLifecycleOperation
)

type parsedRoute struct {
	kind       routeKind
	resourceID string
}

func parseRoute(r *http.Request) (parsedRoute, bool) {
	if r.URL.RawPath != "" || strings.Contains(r.URL.EscapedPath(), "%") || r.URL.Path != path.Clean(r.URL.Path) {
		return parsedRoute{}, false
	}
	if r.URL.Path == blueprintsPath {
		return parsedRoute{kind: routeBlueprints}, true
	}
	if r.URL.Path == outputTargetsPath {
		return parsedRoute{kind: routeOutputTargets}, true
	}
	if r.URL.Path == catalogOptionsPath {
		return parsedRoute{kind: routeCatalogOptions}, true
	}
	if r.URL.Path == experimentalCatalogOptionsPath {
		return parsedRoute{kind: routeExperimentalCatalogOptions}, true
	}
	if strings.HasPrefix(r.URL.Path, experimentalBlueprintsPath+"/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, experimentalBlueprintsPath+"/"), "/")
		if len(parts) == 2 && validIdentifier(parts[0]) && parts[1] == "plan" {
			return parsedRoute{kind: routeExperimentalPlanForBlueprint, resourceID: parts[0]}, true
		}
		return parsedRoute{}, false
	}
	if r.URL.Path == runsPath {
		return parsedRoute{kind: routeRuns}, true
	}
	if strings.HasPrefix(r.URL.Path, blueprintsPath+"/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, blueprintsPath+"/"), "/")
		if len(parts) == 1 && validIdentifier(parts[0]) {
			return parsedRoute{kind: routeBlueprint, resourceID: parts[0]}, true
		}
		if len(parts) == 2 && validIdentifier(parts[0]) {
			switch parts[1] {
			case "plan":
				return parsedRoute{kind: routePlanForBlueprint, resourceID: parts[0]}, true
			case "assemble":
				return parsedRoute{kind: routeAssembleBlueprint, resourceID: parts[0]}, true
			}
		}
		return parsedRoute{}, false
	}
	if strings.HasPrefix(r.URL.Path, plansPath+"/") {
		value := strings.TrimPrefix(r.URL.Path, plansPath+"/")
		if validIdentifier(value) && !strings.Contains(value, "/") {
			return parsedRoute{kind: routePlan, resourceID: value}, true
		}
	}
	if strings.HasPrefix(r.URL.Path, runsPath+"/") {
		value := strings.TrimPrefix(r.URL.Path, runsPath+"/")
		parts := strings.Split(value, "/")
		if len(parts) == 2 && validIdentifier(parts[0]) && parts[1] == "retry" {
			return parsedRoute{kind: routeRetryRun, resourceID: parts[0]}, true
		}
		if len(parts) == 2 && validIdentifier(parts[0]) && parts[1] == "cancel" {
			return parsedRoute{kind: routeCancelRun, resourceID: parts[0]}, true
		}
		if validIdentifier(value) && !strings.Contains(value, "/") {
			return parsedRoute{kind: routeRun, resourceID: value}, true
		}
	}
	if strings.HasPrefix(r.URL.Path, assembliesPath+"/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, assembliesPath+"/"), "/")
		if len(parts) == 2 && validIdentifier(parts[0]) {
			if parts[1] == "lifecycle-source" {
				return parsedRoute{kind: routeLifecycleSource, resourceID: parts[0]}, true
			}
			if parts[1] == "upgrade-plans" {
				return parsedRoute{kind: routeUpgradePlan, resourceID: parts[0]}, true
			}
			if parts[1] == "eject-plans" {
				return parsedRoute{kind: routeEjectPlan, resourceID: parts[0]}, true
			}
		}
		return parsedRoute{}, false
	}
	if strings.HasPrefix(r.URL.Path, lifecyclePlansPath+"/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, lifecyclePlansPath+"/"), "/")
		if len(parts) == 1 && validIdentifier(parts[0]) {
			return parsedRoute{kind: routeLifecyclePlan, resourceID: parts[0]}, true
		}
		if len(parts) == 2 && validIdentifier(parts[0]) && parts[1] == "execute" {
			return parsedRoute{kind: routeExecuteLifecyclePlan, resourceID: parts[0]}, true
		}
		return parsedRoute{}, false
	}
	if strings.HasPrefix(r.URL.Path, lifecycleOperationsPath+"/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, lifecycleOperationsPath+"/"), "/")
		if len(parts) == 1 && validIdentifier(parts[0]) {
			return parsedRoute{kind: routeLifecycleOperation, resourceID: parts[0]}, true
		}
		if len(parts) == 2 && validIdentifier(parts[0]) {
			if parts[1] == "cancel" {
				return parsedRoute{kind: routeCancelLifecycleOperation, resourceID: parts[0]}, true
			}
			if parts[1] == "rollback" {
				return parsedRoute{kind: routeRollbackLifecycleOperation, resourceID: parts[0]}, true
			}
		}
		return parsedRoute{}, false
	}
	if strings.HasPrefix(r.URL.Path, manifestsPath+"/") {
		value := strings.TrimPrefix(r.URL.Path, manifestsPath+"/")
		if validIdentifier(value) && !strings.Contains(value, "/") {
			return parsedRoute{kind: routeManifest, resourceID: value}, true
		}
	}
	if strings.HasPrefix(r.URL.Path, locksPath+"/") {
		value := strings.TrimPrefix(r.URL.Path, locksPath+"/")
		if validIdentifier(value) && !strings.Contains(value, "/") {
			return parsedRoute{kind: routeLock, resourceID: value}, true
		}
	}
	return parsedRoute{}, false
}

func validIdentifier(value string) bool { return identifierPattern.MatchString(value) }
