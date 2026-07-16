package core

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
	"platform.local/capability-platform/backend/internal/modules/product"
)

var (
	digestPattern     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	stableCodePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	drivePathPattern  = regexp.MustCompile(`^[A-Za-z]:`)
)

type IDGenerator func(string) (string, error)

type Service struct {
	repository    Repository
	validator     DocumentValidator
	planner       Planner
	outputTargets OutputTargetVerifier
	idGenerator   IDGenerator
	now           func() time.Time
}

type ServiceOption func(*Service)

func WithOutputTargetVerifier(verifier OutputTargetVerifier) ServiceOption {
	return func(service *Service) { service.outputTargets = verifier }
}

func NewService(repository Repository, validator DocumentValidator, planner Planner, idGenerator IDGenerator, now func() time.Time, options ...ServiceOption) *Service {
	if now == nil {
		now = time.Now
	}
	service := &Service{repository: repository, validator: validator, planner: planner, idGenerator: idGenerator, now: now}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

type CreateBlueprintCommand struct {
	ActorID, IdempotencyKey, TraceID string
	Document                         json.RawMessage
}

func (s *Service) CreateBlueprint(ctx context.Context, command CreateBlueprintCommand) (Blueprint, error) {
	if s.repository == nil || s.validator == nil || s.idGenerator == nil || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return Blueprint{}, ErrInvalidCommand
	}
	validated, err := s.validator.Validate("product-blueprint", command.Document)
	if err != nil {
		return Blueprint{}, err
	}
	var header struct {
		BlueprintID  string `json:"blueprint_id"`
		Version      string `json:"version"`
		Applications []struct {
			Environment string `json:"environment"`
		} `json:"applications"`
	}
	if err := json.Unmarshal(validated.CanonicalJSON, &header); err != nil || header.BlueprintID == "" || header.Version == "" {
		return Blueprint{}, ErrDocumentInvalid
	}
	now := s.now().UTC()
	auditID, eventID, err := s.newAuditAndEventIDs()
	if err != nil {
		return Blueprint{}, err
	}
	environments, err := projectBlueprintEnvironments(header.Applications)
	if err != nil {
		return Blueprint{}, err
	}
	blueprint := Blueprint{BlueprintID: header.BlueprintID, Revision: 1, DocumentVersion: header.Version, SchemaVersion: validated.SchemaVersion, Document: validated.CanonicalJSON, ContentSHA256: validated.SHA256, Environments: environments, CreatedBy: command.ActorID, CreatedAt: now, AuditID: auditID}
	idem, err := makeIdempotency("assembly.create_blueprint", command.ActorID, "platform", command.IdempotencyKey, struct {
		BlueprintSHA256 string `json:"blueprint_sha256"`
	}{validated.SHA256}, now)
	if err != nil {
		return Blueprint{}, err
	}
	event := assemblyEvent(eventID, auditID, "assembly.blueprint_created.v1", "assembly.blueprint.created", "product_blueprint", header.BlueprintID, blueprint.ProductID, command.ActorID, command.TraceID, "assembly.blueprint.manage", now, "normal", map[string]any{"blueprint_sha256": validated.SHA256, "revision": 1})
	return s.repository.CreateBlueprint(ctx, CreateBlueprintRecord{Blueprint: blueprint, Idempotency: idem, Event: event})
}

func (s *Service) GetBlueprint(ctx context.Context, blueprintID string, revision int64) (Blueprint, error) {
	if s.repository == nil || blueprintID == "" || revision < 0 {
		return Blueprint{}, ErrInvalidCommand
	}
	value, err := s.repository.GetBlueprint(ctx, "", blueprintID, revision)
	if err != nil {
		return Blueprint{}, err
	}
	var body struct {
		Applications []struct {
			Environment string `json:"environment"`
		} `json:"applications"`
	}
	if json.Unmarshal(value.Document, &body) != nil {
		return Blueprint{}, ErrDocumentInvalid
	}
	value.Environments, err = projectBlueprintEnvironments(body.Applications)
	if err != nil {
		return Blueprint{}, err
	}
	return value, nil
}

type CreatePlanCommand struct {
	BlueprintID, Environment, ActorID, IdempotencyKey, TraceID string
	BlueprintVersion                                           int64
}

func (s *Service) CreatePlan(ctx context.Context, command CreatePlanCommand) (Plan, error) {
	if s.repository == nil || s.validator == nil || s.planner == nil || command.BlueprintID == "" || command.BlueprintVersion < 1 || command.Environment == "" || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		if s.planner == nil {
			return Plan{}, ErrPlanUnavailable
		}
		return Plan{}, ErrInvalidCommand
	}
	blueprint, err := s.repository.GetBlueprint(ctx, "", command.BlueprintID, command.BlueprintVersion)
	if err != nil {
		return Plan{}, err
	}
	planned, err := s.planner.BuildPlan(ctx, blueprint, command.Environment)
	if err != nil {
		return Plan{}, fmt.Errorf("%w: %v", ErrPlanUnavailable, err)
	}
	validated, err := s.validator.Validate("assembly-plan", planned.Document)
	if err != nil {
		return Plan{}, err
	}
	planChecksum, err := verifiedEmbeddedDigest(validated.CanonicalJSON, "plan_checksum")
	if err != nil {
		return Plan{}, err
	}
	confirmationChecksum, err := planConfirmationChecksum(validated.CanonicalJSON)
	if err != nil {
		return Plan{}, err
	}
	var body struct {
		PlanID           string `json:"plan_id"`
		BlueprintID      string `json:"blueprint_id"`
		BlueprintVersion int64  `json:"blueprint_version"`
		Environment      string `json:"environment"`
		CatalogSnapshot  struct {
			Revision string `json:"revision"`
			Checksum string `json:"checksum"`
		} `json:"catalog_snapshot"`
		Capabilities []product.CapabilityItem `json:"capabilities"`
		Executable   bool                     `json:"executable"`
	}
	if err := json.Unmarshal(validated.CanonicalJSON, &body); err != nil || body.PlanID == "" || body.BlueprintID != blueprint.BlueprintID || body.BlueprintVersion != blueprint.Revision || body.Environment != command.Environment || body.CatalogSnapshot.Revision == "" || !digestPattern.MatchString(body.CatalogSnapshot.Checksum) {
		return Plan{}, ErrDocumentInvalid
	}
	capabilities, err := normalizeCapabilities(planned.Capabilities)
	if err != nil {
		return Plan{}, err
	}
	documentCapabilities, err := normalizeCapabilities(body.Capabilities)
	if err != nil || !capabilitySetsEqual(capabilities, documentCapabilities) {
		return Plan{}, ErrDocumentInvalid
	}
	capabilities = documentCapabilities
	review, err := projectPlanReview(validated.CanonicalJSON)
	if err != nil {
		return Plan{}, err
	}
	now := s.now().UTC()
	auditID, eventID, err := s.newAuditAndEventIDs()
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{PlanID: body.PlanID, ProductID: blueprint.ProductID, BlueprintID: blueprint.BlueprintID, BlueprintRevision: blueprint.Revision, Version: 1, Environment: body.Environment, SchemaVersion: validated.SchemaVersion, Document: validated.CanonicalJSON, BlueprintSHA256: blueprint.ContentSHA256, CatalogRevision: body.CatalogSnapshot.Revision, CatalogSnapshotSHA256: body.CatalogSnapshot.Checksum, PlanSHA256: planChecksum, ConfirmationChecksum: confirmationChecksum, Review: review, Executable: body.Executable, Capabilities: capabilities, CreatedBy: command.ActorID, CreatedAt: now, UpdatedAt: now, AuditID: auditID}
	idem, err := makeIdempotency("assembly.create_plan", command.ActorID, blueprint.BlueprintID, command.IdempotencyKey, struct{ BlueprintSHA256, Environment string }{blueprint.ContentSHA256, command.Environment}, now)
	if err != nil {
		return Plan{}, err
	}
	event := assemblyEvent(eventID, auditID, "assembly.planned.v1", "assembly.plan.created", "assembly_plan", plan.PlanID, plan.ProductID, command.ActorID, command.TraceID, "assembly.plan", now, "normal", map[string]any{"blueprint_id": plan.BlueprintID, "catalog_revision": plan.CatalogRevision, "executable": plan.Executable})
	return s.repository.CreatePlan(ctx, CreatePlanRecord{Plan: plan, Idempotency: idem, Event: event})
}

func (s *Service) GetPlan(ctx context.Context, planID string) (Plan, error) {
	if s.repository == nil || planID == "" {
		return Plan{}, ErrInvalidCommand
	}
	value, err := s.repository.GetPlan(ctx, "", planID)
	if err != nil {
		return Plan{}, err
	}
	value.ConfirmationChecksum, err = planConfirmationChecksum(value.Document)
	if err != nil {
		return Plan{}, err
	}
	value.Review, err = projectPlanReview(value.Document)
	if err != nil {
		return Plan{}, err
	}
	return value, nil
}

func projectBlueprintEnvironments(applications []struct {
	Environment string `json:"environment"`
}) ([]string, error) {
	seen := map[string]bool{}
	for _, app := range applications {
		switch app.Environment {
		case "development", "test", "staging", "production":
			seen[app.Environment] = true
		default:
			return nil, ErrDocumentInvalid
		}
	}
	order := []string{"development", "test", "staging", "production"}
	result := make([]string, 0, len(seen))
	for _, value := range order {
		if seen[value] {
			result = append(result, value)
		}
	}
	if len(result) == 0 {
		return nil, ErrDocumentInvalid
	}
	return result, nil
}

func projectPlanReview(document json.RawMessage) (PlanReview, error) {
	var body struct {
		Packages []struct {
			PackageID string `json:"package_id"`
			Version   string `json:"version"`
		} `json:"packages"`
		Applications []struct {
			ApplicationID string `json:"application_id"`
			Target        string `json:"target"`
			Channel       string `json:"channel"`
			DeliveryMode  string `json:"delivery_mode"`
			Template      struct {
				TemplateID string `json:"template_id"`
				Version    string `json:"version"`
			} `json:"template"`
		} `json:"applications"`
		Risks []struct {
			RiskID               string `json:"risk_id"`
			Level                string `json:"level"`
			Category             string `json:"category"`
			Summary              string `json:"summary"`
			RequiresConfirmation bool   `json:"requires_confirmation"`
		} `json:"risks"`
		Conflicts []struct {
			Blocking bool `json:"blocking"`
		} `json:"conflicts"`
		Confirmation struct {
			Statements []string `json:"statements"`
		} `json:"confirmation"`
	}
	if json.Unmarshal(document, &body) != nil || len(body.Applications) == 0 || len(body.Confirmation.Statements) == 0 {
		return PlanReview{}, ErrDocumentInvalid
	}
	result := PlanReview{Packages: make([]PlanReviewPackage, len(body.Packages)), Applications: make([]PlanReviewApplication, len(body.Applications)), Risks: make([]PlanReviewRisk, len(body.Risks)), Statements: append([]string(nil), body.Confirmation.Statements...)}
	for i, v := range body.Packages {
		if v.PackageID == "" || v.Version == "" {
			return PlanReview{}, ErrDocumentInvalid
		}
		result.Packages[i] = PlanReviewPackage{v.PackageID, v.Version}
	}
	for i, v := range body.Applications {
		if v.ApplicationID == "" || v.Target == "" || v.Channel == "" || v.DeliveryMode == "" || v.Template.TemplateID == "" || v.Template.Version == "" {
			return PlanReview{}, ErrDocumentInvalid
		}
		result.Applications[i] = PlanReviewApplication{v.ApplicationID, v.Target, v.Channel, v.DeliveryMode, v.Template.TemplateID, v.Template.Version}
	}
	for i, v := range body.Risks {
		if v.RiskID == "" || v.Summary == "" {
			return PlanReview{}, ErrDocumentInvalid
		}
		result.Risks[i] = PlanReviewRisk{v.RiskID, v.Level, v.Category, v.Summary, v.RequiresConfirmation}
	}
	for _, v := range body.Conflicts {
		if v.Blocking {
			result.BlockingConflictCount++
		}
	}
	return result, nil
}

type ConfirmPlanCommand struct {
	PlanID, ConfirmationChecksum, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                                                int64
}

func (s *Service) ConfirmPlan(ctx context.Context, command ConfirmPlanCommand) (Plan, error) {
	if s.repository == nil || s.idGenerator == nil || command.PlanID == "" || !digestPattern.MatchString(command.ConfirmationChecksum) || command.ExpectedVersion < 1 || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return Plan{}, ErrInvalidCommand
	}
	plan, err := s.repository.GetPlan(ctx, "", command.PlanID)
	if err != nil {
		return Plan{}, err
	}
	if !plan.Executable {
		return Plan{}, ErrPlanNotExecutable
	}
	confirmationChecksum, err := planConfirmationChecksum(plan.Document)
	if err != nil || !digestsEqual(confirmationChecksum, command.ConfirmationChecksum) {
		return Plan{}, ErrConflict
	}
	now := s.now().UTC()
	auditID, eventID, err := s.newAuditAndEventIDs()
	if err != nil {
		return Plan{}, err
	}
	idem, err := makeIdempotency("assembly.confirm_plan", command.ActorID, plan.PlanID, command.IdempotencyKey, struct {
		PlanSHA256, ConfirmationChecksum string
		ExpectedVersion                  int64
	}{plan.PlanSHA256, command.ConfirmationChecksum, command.ExpectedVersion}, now)
	if err != nil {
		return Plan{}, err
	}
	event := assemblyEvent(eventID, auditID, "assembly.plan_confirmed.v1", "assembly.plan.confirmed", "assembly_plan", plan.PlanID, plan.ProductID, command.ActorID, command.TraceID, "assembly.execute", now, "high", map[string]any{"plan_sha256": plan.PlanSHA256, "expected_version": command.ExpectedVersion})
	confirmed, err := s.repository.ConfirmPlan(ctx, ConfirmPlanRecord{PlanID: plan.PlanID, ConfirmedBy: command.ActorID, ExpectedVersion: command.ExpectedVersion, ConfirmedAt: now, Idempotency: idem, Event: event})
	if err == nil {
		confirmed.AuditID = auditID
	}
	return confirmed, err
}

type StartAssemblyCommand struct {
	PlanID, PlanChecksum, ConfirmationChecksum, OutputTargetRef, ActorID, IdempotencyKey, TraceID string
	ExpectedPlanVersion                                                                           int64
}

func (s *Service) StartAssembly(ctx context.Context, command StartAssemblyCommand) (Run, error) {
	if s.repository == nil || s.validator == nil || s.idGenerator == nil || command.PlanID == "" || command.ExpectedPlanVersion < 1 || !digestPattern.MatchString(command.PlanChecksum) || !digestPattern.MatchString(command.ConfirmationChecksum) || len(command.OutputTargetRef) < 3 || len(command.OutputTargetRef) > 128 || !identifierPattern.MatchString(command.OutputTargetRef) || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return Run{}, ErrInvalidCommand
	}
	plan, err := s.repository.GetPlan(ctx, "", command.PlanID)
	if err != nil {
		return Run{}, err
	}
	if plan.Version != command.ExpectedPlanVersion {
		return Run{}, ErrVersionConflict
	}
	if !digestsEqual(plan.PlanSHA256, command.PlanChecksum) {
		return Run{}, ErrConflict
	}
	if !plan.Executable {
		return Run{}, ErrPlanNotExecutable
	}
	if plan.ConfirmedAt == nil {
		return Run{}, ErrPlanNotConfirmed
	}
	if s.outputTargets == nil || s.outputTargets.VerifyOutputTarget(ctx, plan.Environment, command.OutputTargetRef) != nil {
		return Run{}, ErrOutputTargetUnavailable
	}
	confirmationChecksum, err := planConfirmationChecksum(plan.Document)
	if err != nil || !digestsEqual(confirmationChecksum, command.ConfirmationChecksum) {
		return Run{}, ErrConflict
	}
	runID, err := s.idGenerator("run_")
	if err != nil {
		return Run{}, err
	}
	now := s.now().UTC()
	keyDigest := digestString(command.IdempotencyKey)
	runDocument, err := initialRunDocument(runID, plan, keyDigest, command.OutputTargetRef, now)
	if err != nil {
		return Run{}, err
	}
	validated, err := s.validator.Validate("assembly-run", runDocument)
	if err != nil {
		return Run{}, err
	}
	auditID, eventID, err := s.newAuditAndEventIDs()
	if err != nil {
		return Run{}, err
	}
	run, err := parseRunDocument(validated, plan.ProductID, plan.Version, command.ActorID, auditID)
	if err != nil {
		return Run{}, err
	}
	idem, err := makeIdempotency("assembly.start", command.ActorID, plan.PlanID, command.IdempotencyKey, struct {
		PlanSHA256, ConfirmationChecksum, OutputTargetRef string
		PlanVersion                                       int64
	}{plan.PlanSHA256, command.ConfirmationChecksum, command.OutputTargetRef, plan.Version}, now)
	if err != nil {
		return Run{}, err
	}
	event := assemblyEvent(eventID, auditID, "assembly.started.v1", "assembly.started", "assembly_run", run.RunID, run.ProductID, command.ActorID, command.TraceID, "assembly.execute", now, "high", map[string]any{"plan_id": plan.PlanID, "plan_sha256": plan.PlanSHA256, "output_target_ref": command.OutputTargetRef})
	return s.repository.StartRun(ctx, StartRunRecord{Run: run, Idempotency: idem, Event: event})
}

func (s *Service) GetRun(ctx context.Context, runID string) (Run, error) {
	if s.repository == nil || runID == "" {
		return Run{}, ErrInvalidCommand
	}
	return s.repository.GetRun(ctx, "", runID)
}

func (s *Service) ListRuns(ctx context.Context, filter RunListFilter) (RunPage, error) {
	if s.repository == nil || filter.PageSize < 1 || filter.PageSize > 100 {
		return RunPage{}, ErrInvalidCommand
	}
	switch filter.Status {
	case "", RunStatusPlanned, RunStatusProvisioning, RunStatusGenerating, RunStatusValidating, RunStatusCompleted, RunStatusFailed, RunStatusRollingBack, RunStatusRolledBack:
	default:
		return RunPage{}, ErrInvalidCommand
	}
	repository, ok := s.repository.(RecoveryRepository)
	if !ok {
		return RunPage{}, ErrOperationInProgress
	}
	return repository.ListRuns(ctx, filter)
}

type RetryRunCommand struct {
	RunID, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                         int64
}

func (s *Service) RetryRun(ctx context.Context, command RetryRunCommand) (Run, error) {
	if s.repository == nil || s.validator == nil || s.idGenerator == nil || command.RunID == "" || command.ActorID == "" || command.ExpectedVersion < 1 || command.IdempotencyKey == "" || command.TraceID == "" {
		return Run{}, ErrInvalidCommand
	}
	parent, err := s.repository.GetRun(ctx, "", command.RunID)
	if err != nil {
		return Run{}, err
	}
	if parent.Version != command.ExpectedVersion {
		return Run{}, ErrVersionConflict
	}
	if parent.Status != RunStatusFailed || !parent.Recovery.Retryable || parent.Recovery.RollbackRequired {
		return Run{}, ErrConflict
	}
	runID, err := s.idGenerator("run_")
	if err != nil {
		return Run{}, err
	}
	now := s.now().UTC()
	keyDigest := digestString(command.IdempotencyKey)
	document, err := retryRunDocument(runID, parent, keyDigest, now)
	if err != nil {
		return Run{}, err
	}
	validated, err := s.validator.Validate("assembly-run", document)
	if err != nil {
		return Run{}, err
	}
	auditID, eventID, err := s.newAuditAndEventIDs()
	if err != nil {
		return Run{}, err
	}
	run, err := parseRunDocument(validated, parent.ProductID, parent.PlanVersion, parent.CreatedBy, auditID)
	if err != nil {
		return Run{}, err
	}
	run.RootRunID, run.RetryOfRunID, run.AttemptNumber = parent.RootRunID, parent.RunID, parent.AttemptNumber+1
	idem, err := makeIdempotency("assembly.retry_run", command.ActorID, parent.RunID, command.IdempotencyKey, struct {
		ExpectedVersion int64
		RootRunID       string
	}{command.ExpectedVersion, parent.RootRunID}, now)
	if err != nil {
		return Run{}, err
	}
	event := assemblyEvent(eventID, auditID, "assembly.retried.v1", "assembly.retried", "assembly_run", run.RunID, run.ProductID, command.ActorID, command.TraceID, "assembly.execute", now, "high", map[string]any{"retry_of_run_id": parent.RunID, "root_run_id": parent.RootRunID, "attempt_number": run.AttemptNumber})
	repository, ok := s.repository.(RecoveryRepository)
	if !ok {
		return Run{}, ErrOperationInProgress
	}
	return repository.RetryRun(ctx, RetryRunRecord{ParentRun: parent, Run: run, ExpectedVersion: command.ExpectedVersion, Idempotency: idem, Event: event})
}

type BindProductCommand struct {
	ProductID, RunID, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                                    int64
}

func (s *Service) BindProduct(ctx context.Context, command BindProductCommand) (Run, error) {
	if s.repository == nil || s.idGenerator == nil || command.ProductID == "" || command.RunID == "" || command.ExpectedVersion < 1 || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return Run{}, ErrInvalidCommand
	}
	now := s.now().UTC()
	auditID, eventID, err := s.newAuditAndEventIDs()
	if err != nil {
		return Run{}, err
	}
	idem, err := makeIdempotency("assembly.bind_product", command.ActorID, command.RunID, command.IdempotencyKey, struct {
		ProductID       string
		ExpectedVersion int64
	}{command.ProductID, command.ExpectedVersion}, now)
	if err != nil {
		return Run{}, err
	}
	event := assemblyEvent(eventID, auditID, "assembly.product_bound.v1", "assembly.product.bound", "assembly_run", command.RunID, command.ProductID, command.ActorID, command.TraceID, "assembly.execute", now, "high", map[string]any{"product_id": command.ProductID})
	return s.repository.BindProduct(ctx, BindProductRecord{ProductID: command.ProductID, RunID: command.RunID, ExpectedVersion: command.ExpectedVersion, BoundAt: now, Idempotency: idem, Event: event})
}

type UpdateRunCommand struct {
	RunID, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                         int64
	Document                                json.RawMessage
	Diagnostics                             []RunDiagnostic
	Reports                                 []RunReport
}

func (s *Service) UpdateRun(ctx context.Context, command UpdateRunCommand) (Run, error) {
	if s.repository == nil || s.validator == nil || s.idGenerator == nil || command.RunID == "" || command.ExpectedVersion < 1 || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return Run{}, ErrInvalidCommand
	}
	current, err := s.repository.GetRun(ctx, "", command.RunID)
	if err != nil {
		return Run{}, err
	}
	validated, err := s.validator.Validate("assembly-run", command.Document)
	if err != nil {
		return Run{}, err
	}
	auditID, eventID, err := s.newAuditAndEventIDs()
	if err != nil {
		return Run{}, err
	}
	next, err := parseRunDocument(validated, current.ProductID, current.PlanVersion, command.ActorID, auditID)
	if err != nil {
		return Run{}, err
	}
	if err := validateRunEvolution(current, next); err != nil {
		return Run{}, ErrInvalidRunTransition
	}
	diagnostics, reports, err := validateRunEvidence(next, command.Diagnostics, command.Reports, s.now().UTC())
	if err != nil {
		return Run{}, err
	}
	next.Version = current.Version + 1
	now := s.now().UTC()
	idem, err := makeIdempotency("assembly.update_run", command.ActorID, current.RunID, command.IdempotencyKey, struct {
		DocumentSHA256  string
		ExpectedVersion int64
	}{validated.SHA256, command.ExpectedVersion}, now)
	if err != nil {
		return Run{}, err
	}
	eventType, action, risk := "assembly.progressed.v1", "assembly.progressed", "normal"
	if next.Status == RunStatusFailed {
		eventType, action, risk = "assembly.failed.v1", "assembly.failed", "high"
	}
	event := assemblyEvent(eventID, auditID, eventType, action, "assembly_run", next.RunID, next.ProductID, command.ActorID, command.TraceID, "assembly.execute", now, risk, map[string]any{"status": next.Status, "resume_from_step_id": next.Recovery.ResumeFromStepID})
	if next.Status == RunStatusFailed {
		event.Payload.Result = "failure"
		event.Payload.ReasonCode = "assembly.run_failed"
	}
	return s.repository.UpdateRun(ctx, UpdateRunRecord{Run: next, ExpectedVersion: command.ExpectedVersion, Diagnostics: diagnostics, Reports: reports, Idempotency: idem, Event: event})
}

func validateRunEvidence(run Run, diagnostics []RunDiagnostic, reports []RunReport, now time.Time) ([]RunDiagnostic, []RunReport, error) {
	if run.Status == RunStatusFailed && len(diagnostics) == 0 {
		diagnostics = make([]RunDiagnostic, len(run.DiagnosticIDs))
		for i, id := range run.DiagnosticIDs {
			code := strings.TrimPrefix(strings.ReplaceAll(id, "-", "_"), "diagnostic.")
			diagnostics[i] = RunDiagnostic{DiagnosticID: id, Code: "assembly." + code, Severity: "error", Category: "assembly", Message: "Assembly step failed; resolve the prerequisite before retrying", Blocking: true, Retryable: run.Recovery.Retryable, Remediation: []string{"Resolve the reported prerequisite", "Retry the failed assembly run"}, RelatedPaths: []string{}, CreatedAt: now}
		}
	}
	ids := map[string]bool{}
	for _, id := range run.DiagnosticIDs {
		ids[id] = true
	}
	seen := map[string]bool{}
	for i := range diagnostics {
		d := &diagnostics[i]
		if !ids[d.DiagnosticID] || seen[d.DiagnosticID] || !identifierPattern.MatchString(d.DiagnosticID) || !stableCodePattern.MatchString(d.Code) || !stableCodePattern.MatchString(d.Category) || (d.Severity != "info" && d.Severity != "warning" && d.Severity != "error") || len(d.Message) < 1 || len(d.Message) > 500 || strings.ContainsAny(d.Message, "\r\n\t") {
			return nil, nil, ErrDocumentInvalid
		}
		seen[d.DiagnosticID] = true
		if d.CreatedAt.IsZero() {
			d.CreatedAt = now
		}
		if d.Remediation == nil {
			d.Remediation = []string{}
		}
		if d.RelatedPaths == nil {
			d.RelatedPaths = []string{}
		}
		if len(d.Remediation) > 20 || len(d.RelatedPaths) > 100 {
			return nil, nil, ErrDocumentInvalid
		}
		for _, item := range d.Remediation {
			if len(item) < 1 || len(item) > 300 || strings.ContainsAny(item, "\r\n\t") {
				return nil, nil, ErrDocumentInvalid
			}
		}
		pathSeen := map[string]bool{}
		for _, p := range d.RelatedPaths {
			if pathSeen[p] || !safeRelativeEvidencePath(p) {
				return nil, nil, ErrDocumentInvalid
			}
			pathSeen[p] = true
		}
	}
	if run.Status == RunStatusFailed && len(seen) != len(ids) {
		return nil, nil, ErrDocumentInvalid
	}
	reportSeen := map[string]bool{}
	for i := range reports {
		r := &reports[i]
		if reportSeen[r.ReportID] || !identifierPattern.MatchString(r.ReportID) || !stableCodePattern.MatchString(r.ReportType) || (r.Status != "passed" && r.Status != "failed" && r.Status != "partial") || len(r.Summary) < 1 || len(r.Summary) > 500 || strings.ContainsAny(r.Summary, "\r\n\t") || (r.Checksum != "" && !digestPattern.MatchString(r.Checksum)) {
			return nil, nil, ErrDocumentInvalid
		}
		if r.CreatedAt.IsZero() {
			r.CreatedAt = now
		}
		reportSeen[r.ReportID] = true
	}
	return diagnostics, reports, nil
}

func safeRelativeEvidencePath(value string) bool {
	return value != "" && !strings.Contains(value, "\\") && !strings.HasPrefix(value, "/") && !drivePathPattern.MatchString(value) && !strings.Contains("/"+value+"/", "/../")
}

type CompleteAssemblyCommand struct {
	ProductID, RunID, ActorID, IdempotencyKey, TraceID string
	ExpectedVersion                                    int64
	RunDocument, ManifestDocument, LockDocument        json.RawMessage
}

func (s *Service) CompleteAssembly(ctx context.Context, command CompleteAssemblyCommand) (Run, error) {
	if s.repository == nil || s.validator == nil || s.idGenerator == nil || command.ProductID == "" || command.RunID == "" || command.ExpectedVersion < 1 || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return Run{}, ErrInvalidCommand
	}
	current, err := s.repository.GetRun(ctx, command.ProductID, command.RunID)
	if err != nil {
		return Run{}, err
	}
	runDoc, err := s.validator.Validate("assembly-run", command.RunDocument)
	if err != nil {
		return Run{}, err
	}
	manifestDoc, err := s.validator.Validate("assembly-manifest", command.ManifestDocument)
	if err != nil {
		return Run{}, err
	}
	lockDoc, err := s.validator.Validate("generated-project-lock", command.LockDocument)
	if err != nil {
		return Run{}, err
	}
	manifestChecksum, err := verifiedEmbeddedDigest(manifestDoc.CanonicalJSON, "manifest_checksum")
	if err != nil {
		return Run{}, err
	}
	lockChecksum, err := verifiedEmbeddedDigest(lockDoc.CanonicalJSON, "lock_checksum")
	if err != nil {
		return Run{}, err
	}
	auditID, eventID, err := s.newAuditAndEventIDs()
	if err != nil {
		return Run{}, err
	}
	next, err := parseRunDocument(runDoc, current.ProductID, current.PlanVersion, command.ActorID, auditID)
	if err != nil {
		return Run{}, err
	}
	if err := validateRunEvolution(current, next); err != nil || next.Status != RunStatusCompleted {
		return Run{}, ErrInvalidRunTransition
	}
	plan, err := s.repository.GetPlan(ctx, command.ProductID, current.PlanID)
	if err != nil {
		return Run{}, err
	}
	if plan.Version != current.PlanVersion || plan.ConfirmedAt == nil || !plan.Executable || !digestsEqual(plan.PlanSHA256, current.PlanSHA256) {
		return Run{}, ErrConflict
	}
	blueprint, err := s.repository.GetBlueprint(ctx, command.ProductID, plan.BlueprintID, plan.BlueprintRevision)
	if err != nil {
		return Run{}, err
	}
	identity, err := validateCompletedArtifacts(plan, blueprint, current, manifestDoc.CanonicalJSON, lockDoc.CanonicalJSON, manifestChecksum, lockChecksum)
	if err != nil {
		return Run{}, err
	}
	next.Version = current.Version + 1
	next.ManifestID = identity.AssemblyID
	next.LockID = identity.LockID
	manifest := Manifest{AssemblyID: identity.AssemblyID, ProductID: command.ProductID, RunID: current.RunID, SchemaVersion: manifestDoc.SchemaVersion, Document: manifestDoc.CanonicalJSON, DocumentSHA256: manifestDoc.SHA256, ManifestSHA256: manifestChecksum, CreatedAt: identity.ManifestCreatedAt}
	lock := GeneratedProjectLock{LockID: identity.LockID, ProductID: command.ProductID, RunID: current.RunID, AssemblyID: manifest.AssemblyID, SchemaVersion: lockDoc.SchemaVersion, Document: lockDoc.CanonicalJSON, DocumentSHA256: lockDoc.SHA256, LockSHA256: lockChecksum, CreatedAt: identity.LockCreatedAt}
	now := s.now().UTC()
	idem, err := makeIdempotency("assembly.complete", command.ActorID, current.RunID, command.IdempotencyKey, struct {
		RunSHA256, ManifestSHA256, LockSHA256 string
		ExpectedVersion                       int64
	}{runDoc.SHA256, manifestChecksum, lockChecksum, command.ExpectedVersion}, now)
	if err != nil {
		return Run{}, err
	}
	event := assemblyEvent(eventID, auditID, "assembly.completed.v1", "assembly.completed", "assembly_run", current.RunID, command.ProductID, command.ActorID, command.TraceID, "assembly.execute", now, "high", map[string]any{"assembly_id": manifest.AssemblyID, "manifest_sha256": manifestChecksum, "lock_sha256": lockChecksum})
	return s.repository.CompleteRun(ctx, CompleteRunRecord{Run: next, ExpectedVersion: command.ExpectedVersion, Manifest: manifest, Lock: lock, Idempotency: idem, Event: event})
}

func (s *Service) GetManifest(ctx context.Context, assemblyID string) (Manifest, error) {
	if s.repository == nil || assemblyID == "" {
		return Manifest{}, ErrInvalidCommand
	}
	return s.repository.GetManifest(ctx, "", assemblyID)
}
func (s *Service) GetLock(ctx context.Context, lockID string) (GeneratedProjectLock, error) {
	if s.repository == nil || lockID == "" {
		return GeneratedProjectLock{}, ErrInvalidCommand
	}
	return s.repository.GetLock(ctx, "", lockID)
}

func (s *Service) ResolveProductCapabilityChange(ctx context.Context, requested product.TrustedCapabilityChangePlan) (product.TrustedCapabilityChangePlan, error) {
	if s.repository == nil || requested.ProductID == "" || requested.SourcePlanID == "" || requested.CatalogRevision == "" || !digestPattern.MatchString(requested.CatalogSnapshotSHA256) {
		return product.TrustedCapabilityChangePlan{}, ErrInvalidCommand
	}
	plan, err := s.repository.GetPlan(ctx, requested.ProductID, requested.SourcePlanID)
	if err != nil {
		return product.TrustedCapabilityChangePlan{}, err
	}
	if plan.ProductID != requested.ProductID || !plan.Executable || plan.ConfirmedAt == nil || plan.CatalogRevision != requested.CatalogRevision || !digestsEqual(plan.CatalogSnapshotSHA256, requested.CatalogSnapshotSHA256) {
		return product.TrustedCapabilityChangePlan{}, ErrConflict
	}
	lockedCapabilities, err := capabilitiesFromPlanDocument(plan.Document)
	if err != nil {
		return product.TrustedCapabilityChangePlan{}, err
	}
	projectedCapabilities, err := normalizeCapabilities(plan.Capabilities)
	if err != nil || !capabilitySetsEqual(lockedCapabilities, projectedCapabilities) {
		return product.TrustedCapabilityChangePlan{}, ErrConflict
	}
	return product.TrustedCapabilityChangePlan{ProductID: plan.ProductID, SourcePlanID: plan.PlanID, CatalogRevision: plan.CatalogRevision, CatalogSnapshotSHA256: plan.CatalogSnapshotSHA256, Items: lockedCapabilities}, nil
}

func (s *Service) ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]ClaimedOutboxEvent, error) {
	if s.repository == nil || limit < 1 || limit > 200 {
		return nil, ErrInvalidCommand
	}
	return s.repository.ClaimOutbox(ctx, now.UTC(), limit)
}
func (s *Service) MarkOutboxPublished(ctx context.Context, eventID string, now time.Time) error {
	if s.repository == nil || eventID == "" {
		return ErrInvalidCommand
	}
	return s.repository.MarkOutboxPublished(ctx, eventID, now.UTC())
}
func (s *Service) MarkOutboxFailed(ctx context.Context, eventID, summary string, next time.Time, dead bool) error {
	if s.repository == nil || eventID == "" || summary == "" || len(summary) > 512 {
		return ErrInvalidCommand
	}
	return s.repository.MarkOutboxFailed(ctx, eventID, summary, next.UTC(), dead)
}

func (s *Service) newAuditAndEventIDs() (string, string, error) {
	auditID, err := s.idGenerator("aud_")
	if err != nil {
		return "", "", err
	}
	eventID, err := s.idGenerator("evt_")
	return auditID, eventID, err
}

func makeIdempotency(operation, actorID, scopeID, key string, request any, now time.Time) (Idempotency, error) {
	if operation == "" || actorID == "" || scopeID == "" || len(key) < 16 || len(key) > 128 {
		return Idempotency{}, ErrInvalidCommand
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return Idempotency{}, err
	}
	return Idempotency{Operation: operation, ActorID: actorID, ScopeID: scopeID, KeyDigest: digestString(key), RequestDigest: digestBytes(raw), Now: now.UTC()}, nil
}

func digestString(value string) string { return digestBytes([]byte(value)) }
func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func digestsEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func normalizeCapabilities(items []product.CapabilityItem) ([]product.CapabilityItem, error) {
	result := append([]product.CapabilityItem(nil), items...)
	seen := map[string]struct{}{}
	for i := range result {
		if result[i].CapabilityID == "" || result[i].SourcePackageID == "" || result[i].SourcePackageVersion == "" {
			return nil, ErrDocumentInvalid
		}
		if _, duplicate := seen[result[i].CapabilityID]; duplicate {
			return nil, ErrDocumentInvalid
		}
		seen[result[i].CapabilityID] = struct{}{}
		if len(result[i].Policy) == 0 {
			result[i].Policy = json.RawMessage(`{}`)
		}
		var policy any
		if json.Unmarshal(result[i].Policy, &policy) != nil {
			return nil, ErrDocumentInvalid
		}
		canonical, err := machinecontract.Canonicalize(result[i].Policy)
		if err != nil {
			return nil, ErrDocumentInvalid
		}
		result[i].Policy = canonical
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CapabilityID < result[j].CapabilityID })
	return result, nil
}

func capabilitySetsEqual(left, right []product.CapabilityItem) bool {
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftDigest, leftErr := machinecontract.Digest(leftRaw)
	rightDigest, rightErr := machinecontract.Digest(rightRaw)
	return leftErr == nil && rightErr == nil && digestsEqual(leftDigest, rightDigest)
}

func capabilitiesFromPlanDocument(document json.RawMessage) ([]product.CapabilityItem, error) {
	var body struct {
		Capabilities []product.CapabilityItem `json:"capabilities"`
	}
	if err := json.Unmarshal(document, &body); err != nil {
		return nil, ErrDocumentInvalid
	}
	return normalizeCapabilities(body.Capabilities)
}

func planConfirmationChecksum(document json.RawMessage) (string, error) {
	var body struct {
		Conflicts []struct {
			Blocking bool `json:"blocking"`
		} `json:"conflicts"`
		Risks        []json.RawMessage `json:"risks"`
		Executable   bool              `json:"executable"`
		Confirmation struct {
			Required              bool     `json:"required"`
			BlockingConflictCount int      `json:"blocking_conflict_count"`
			RiskCount             int      `json:"risk_count"`
			Statements            []string `json:"statements"`
			SummaryChecksum       string   `json:"summary_checksum"`
		} `json:"confirmation"`
	}
	if err := json.Unmarshal(document, &body); err != nil || !digestPattern.MatchString(body.Confirmation.SummaryChecksum) {
		return "", ErrDocumentInvalid
	}
	blockingConflicts := 0
	for _, conflict := range body.Conflicts {
		if conflict.Blocking {
			blockingConflicts++
		}
	}
	if !body.Confirmation.Required || body.Confirmation.BlockingConflictCount != blockingConflicts || body.Confirmation.RiskCount != len(body.Risks) || (blockingConflicts > 0 && body.Executable) {
		return "", ErrDocumentInvalid
	}
	expected, err := ConfirmationSummaryChecksum(body.Confirmation.BlockingConflictCount, body.Confirmation.RiskCount, body.Confirmation.Statements)
	if err != nil || !digestsEqual(expected, body.Confirmation.SummaryChecksum) {
		return "", ErrDocumentInvalid
	}
	return body.Confirmation.SummaryChecksum, nil
}

func ConfirmationSummaryChecksum(blockingConflictCount, riskCount int, statements []string) (string, error) {
	if blockingConflictCount < 0 || riskCount < 0 || len(statements) == 0 {
		return "", ErrDocumentInvalid
	}
	raw, err := json.Marshal(struct {
		BlockingConflictCount int      `json:"blocking_conflict_count"`
		RiskCount             int      `json:"risk_count"`
		Statements            []string `json:"statements"`
	}{blockingConflictCount, riskCount, statements})
	if err != nil {
		return "", err
	}
	digest, err := machinecontract.Digest(raw)
	if err != nil {
		return "", ErrDocumentInvalid
	}
	return "sha256:" + digest, nil
}

func initialRunDocument(runID string, plan Plan, keyDigest, outputTargetRef string, now time.Time) (json.RawMessage, error) {
	steps := []map[string]any{
		{"step_id": "step.provision", "kind": "provision", "status": "pending", "attempt": 0, "compensation_status": "pending", "diagnostic_ids": []string{}},
		{"step_id": "step.enable-capability", "kind": "enable_capability", "status": "pending", "attempt": 0, "compensation_status": "pending", "diagnostic_ids": []string{}},
		{"step_id": "step.generate", "kind": "generate", "status": "pending", "attempt": 0, "compensation_status": "pending", "diagnostic_ids": []string{}},
		{"step_id": "step.validate", "kind": "validate", "status": "pending", "attempt": 0, "compensation_status": "not_required", "diagnostic_ids": []string{}},
		{"step_id": "step.commit", "kind": "commit", "status": "pending", "attempt": 0, "compensation_status": "pending", "diagnostic_ids": []string{}},
	}
	document := map[string]any{"schema_version": "1.0.0", "run_id": runID, "root_run_id": runID, "attempt_number": 1, "plan_id": plan.PlanID, "plan_checksum": plan.PlanSHA256, "idempotency_key_digest": keyDigest, "output_target_ref": outputTargetRef, "status": "planned", "steps": steps, "current_step_id": nil, "diagnostic_ids": []string{}, "recovery": map[string]any{"retryable": true, "rollback_required": false, "resume_from_step_id": "step.provision"}, "created_at": now.Format(time.RFC3339Nano), "updated_at": now.Format(time.RFC3339Nano)}
	return json.Marshal(document)
}

func retryRunDocument(runID string, parent Run, keyDigest string, now time.Time) (json.RawMessage, error) {
	var body map[string]any
	if err := json.Unmarshal(parent.Document, &body); err != nil {
		return nil, ErrDocumentInvalid
	}
	steps, ok := body["steps"].([]any)
	if !ok {
		return nil, ErrDocumentInvalid
	}
	for _, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok {
			return nil, ErrDocumentInvalid
		}
		step["status"], step["attempt"], step["diagnostic_ids"] = "pending", 0, []string{}
		delete(step, "started_at")
		delete(step, "finished_at")
		if step["kind"] == "validate" {
			step["compensation_status"] = "not_required"
		} else {
			step["compensation_status"] = "pending"
		}
	}
	body["run_id"], body["root_run_id"], body["retry_of_run_id"] = runID, parent.RootRunID, parent.RunID
	body["attempt_number"], body["idempotency_key_digest"] = parent.AttemptNumber+1, keyDigest
	body["status"], body["current_step_id"], body["diagnostic_ids"] = "planned", nil, []string{}
	body["recovery"] = map[string]any{"retryable": true, "rollback_required": false, "resume_from_step_id": "step.provision"}
	body["created_at"], body["updated_at"] = now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)
	delete(body, "completed_at")
	delete(body, "manifest_path")
	delete(body, "lock_path")
	return json.Marshal(body)
}

func parseRunDocument(validated ValidatedDocument, productID string, planVersion int64, actorID, auditID string) (Run, error) {
	var body struct {
		RunID                string      `json:"run_id"`
		RootRunID            string      `json:"root_run_id"`
		RetryOfRunID         string      `json:"retry_of_run_id"`
		AttemptNumber        int         `json:"attempt_number"`
		PlanID               string      `json:"plan_id"`
		PlanChecksum         string      `json:"plan_checksum"`
		IdempotencyKeyDigest string      `json:"idempotency_key_digest"`
		OutputTargetRef      string      `json:"output_target_ref"`
		Status               RunStatus   `json:"status"`
		CurrentStepID        *string     `json:"current_step_id"`
		Steps                []RunStep   `json:"steps"`
		DiagnosticIDs        []string    `json:"diagnostic_ids"`
		Recovery             RunRecovery `json:"recovery"`
		CreatedAt            time.Time   `json:"created_at"`
		UpdatedAt            time.Time   `json:"updated_at"`
		CompletedAt          *time.Time  `json:"completed_at"`
	}
	if err := json.Unmarshal(validated.CanonicalJSON, &body); err != nil || body.RunID == "" || body.PlanID == "" || !digestPattern.MatchString(body.PlanChecksum) || !digestPattern.MatchString(body.IdempotencyKeyDigest) || len(body.OutputTargetRef) < 3 || len(body.OutputTargetRef) > 128 || !identifierPattern.MatchString(body.OutputTargetRef) {
		return Run{}, ErrDocumentInvalid
	}
	currentStep := ""
	if body.CurrentStepID != nil {
		currentStep = *body.CurrentStepID
	}
	if body.RootRunID == "" {
		body.RootRunID = body.RunID
	}
	if body.AttemptNumber == 0 {
		body.AttemptNumber = 1
	}
	return Run{RunID: body.RunID, ProductID: productID, RootRunID: body.RootRunID, RetryOfRunID: body.RetryOfRunID, AttemptNumber: body.AttemptNumber, PlanID: body.PlanID, PlanVersion: planVersion, Version: 1, PlanSHA256: body.PlanChecksum, SchemaVersion: validated.SchemaVersion, Document: validated.CanonicalJSON, DocumentSHA256: validated.SHA256, IdempotencyKeyDigest: body.IdempotencyKeyDigest, OutputTargetRef: body.OutputTargetRef, Status: body.Status, CurrentStepID: currentStep, Steps: body.Steps, DiagnosticIDs: body.DiagnosticIDs, Recovery: body.Recovery, CreatedBy: actorID, CreatedAt: body.CreatedAt.UTC(), UpdatedAt: body.UpdatedAt.UTC(), CompletedAt: body.CompletedAt, AuditID: auditID}, nil
}

func validTransition(current, next RunStatus) bool {
	if current == next && current != RunStatusCompleted && current != RunStatusRolledBack {
		return true
	}
	allowed := map[RunStatus]map[RunStatus]bool{
		RunStatusPlanned:      {RunStatusProvisioning: true, RunStatusFailed: true},
		RunStatusProvisioning: {RunStatusGenerating: true, RunStatusFailed: true},
		RunStatusGenerating:   {RunStatusValidating: true, RunStatusFailed: true},
		RunStatusValidating:   {RunStatusCompleted: true, RunStatusFailed: true},
		RunStatusFailed:       {RunStatusProvisioning: true, RunStatusRollingBack: true},
		RunStatusRollingBack:  {RunStatusRolledBack: true, RunStatusFailed: true},
	}
	return allowed[current][next]
}

func validateRunEvolution(current, next Run) error {
	if current.RootRunID == "" {
		current.RootRunID = current.RunID
	}
	if current.AttemptNumber == 0 {
		current.AttemptNumber = 1
	}
	if next.RootRunID == "" {
		next.RootRunID = next.RunID
	}
	if next.AttemptNumber == 0 {
		next.AttemptNumber = 1
	}
	if current.Status == RunStatusCompleted || current.Status == RunStatusRolledBack ||
		next.RunID != current.RunID || next.PlanID != current.PlanID ||
		next.RootRunID != current.RootRunID || next.RetryOfRunID != current.RetryOfRunID || next.AttemptNumber != current.AttemptNumber ||
		!digestsEqual(next.PlanSHA256, current.PlanSHA256) ||
		!digestsEqual(next.IdempotencyKeyDigest, current.IdempotencyKeyDigest) ||
		next.OutputTargetRef != current.OutputTargetRef || !next.CreatedAt.Equal(current.CreatedAt) ||
		next.UpdatedAt.Before(current.UpdatedAt) || !validTransition(current.Status, next.Status) ||
		len(next.Steps) != len(current.Steps) {
		return ErrInvalidRunTransition
	}
	terminal := next.Status == RunStatusCompleted || next.Status == RunStatusFailed || next.Status == RunStatusRolledBack
	if terminal != (next.CompletedAt != nil) {
		return ErrInvalidRunTransition
	}
	for index := range current.Steps {
		before, after := current.Steps[index], next.Steps[index]
		if before.StepID != after.StepID || before.Kind != after.Kind || after.Attempt < before.Attempt ||
			!validStepTransition(before.Status, after.Status) || !stableTime(before.StartedAt, after.StartedAt) ||
			!stableTime(before.FinishedAt, after.FinishedAt) || !validCompensationTransition(before.CompensationStatus, after.CompensationStatus) ||
			(after.Status != "pending" && after.Status != "skipped" && after.Attempt < 1) ||
			(after.StartedAt != nil && after.FinishedAt != nil && after.FinishedAt.Before(*after.StartedAt)) {
			return ErrInvalidRunTransition
		}
	}
	return nil
}

func validCompensationTransition(current, next string) bool {
	if current == next {
		return true
	}
	allowed := map[string]map[string]bool{
		"pending": {"completed": true, "failed": true},
		"failed":  {"pending": true, "completed": true},
	}
	return allowed[current][next]
}

func validStepTransition(current, next string) bool {
	if current == next {
		return current != "completed" && current != "compensated" && current != "skipped"
	}
	allowed := map[string]map[string]bool{
		"pending":   {"running": true, "completed": true, "failed": true, "skipped": true},
		"running":   {"completed": true, "failed": true},
		"failed":    {"running": true, "compensated": true},
		"completed": {"compensated": true},
	}
	return allowed[current][next]
}

func stableTime(current, next *time.Time) bool {
	if current == nil {
		return true
	}
	return next != nil && current.Equal(*next)
}

func assemblyEvent(eventID, auditID, eventType, action, targetType, targetID, productID, actorID, traceID, permission string, now time.Time, risk string, summary map[string]any) OutboxEvent {
	scopeType, scopeID := "platform", ""
	if productID != "" {
		scopeType, scopeID = "product", productID
	}
	return OutboxEvent{EventID: eventID, AggregateID: targetID, EventType: eventType, OccurredAt: now, Payload: EventPayload{AuditID: auditID, OccurredAt: now, ActorID: actorID, Permission: permission, ScopeType: scopeType, ScopeID: scopeID, ProductID: productID, Action: action, TargetType: targetType, TargetID: targetID, Result: "success", TraceID: traceID, RiskLevel: risk, RedactedSummary: summary}}
}
