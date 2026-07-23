package assemblyexecution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/assembly/generation"
	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/workflows/productprovisioning"
)

var (
	ErrUnavailable        = errors.New("assembly execution workflow is unavailable")
	ErrPrecondition       = errors.New("assembly execution precondition failed")
	ErrEnvironment        = errors.New("assembly execution environment is unsupported")
	ErrInvalidRunDocument = errors.New("assembly execution run document is invalid")
)

type AssemblyService interface {
	GetRun(context.Context, string) (core.Run, error)
	GetPlan(context.Context, string) (core.Plan, error)
	GetBlueprint(context.Context, string, int64) (core.Blueprint, error)
	BindProduct(context.Context, core.BindProductCommand) (core.Run, error)
	UpdateRun(context.Context, core.UpdateRunCommand) (core.Run, error)
	CompleteAssembly(context.Context, core.CompleteAssemblyCommand) (core.Run, error)
}

type ProductProvisioner interface {
	CreateProduct(context.Context, productprovisioning.CreateCommand) (product.Product, error)
}

type ApplicationService interface {
	CreateApplication(context.Context, productapplication.CreateCommand) (productapplication.Application, error)
}

type CapabilityService interface {
	ReplaceCapabilitySet(context.Context, product.ReplaceCapabilitySetCommand) (product.CapabilitySet, error)
	CurrentCapabilitySet(context.Context, string) (product.CapabilitySet, error)
}

type Service struct {
	assembly             AssemblyService
	products             ProductProvisioner
	applications         ApplicationService
	capabilities         CapabilityService
	workspaces           *generation.WorkspaceCatalog
	renderer             generation.Renderer
	experimentalRenderer generation.Renderer
	contracts            generation.ArtifactContractValidator
	now                  func() time.Time
}

type Command struct {
	RunID          string
	ActorID        string
	IdempotencyKey string
	TraceID        string
}

type blueprintDocument struct {
	Product struct {
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"product"`
}

type planDocument struct {
	PlanID           string `json:"plan_id"`
	BlueprintID      string `json:"blueprint_id"`
	BlueprintVersion int64  `json:"blueprint_version"`
	Environment      string `json:"environment"`
	CatalogSnapshot  struct {
		Revision string `json:"revision"`
		Scope    string `json:"scope"`
		Checksum string `json:"checksum"`
	} `json:"catalog_snapshot"`
	Applications []struct {
		ApplicationID string `json:"application_id"`
		Target        string `json:"target"`
		Channel       string `json:"channel"`
		Environment   string `json:"environment"`
	} `json:"applications"`
}

type Option func(*Service)

func WithExperimentalRenderer(renderer generation.Renderer) Option {
	return func(s *Service) {
		s.experimentalRenderer = renderer
	}
}

func New(assembly AssemblyService, products ProductProvisioner, applications ApplicationService, capabilities CapabilityService, workspaces *generation.WorkspaceCatalog, renderer generation.Renderer, contracts generation.ArtifactContractValidator, now func() time.Time, options ...Option) *Service {
	if now == nil {
		now = time.Now
	}
	service := &Service{assembly: assembly, products: products, applications: applications, capabilities: capabilities, workspaces: workspaces, renderer: renderer, contracts: contracts, now: now}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func (s *Service) Execute(ctx context.Context, command Command) (core.Run, error) {
	if s == nil || s.assembly == nil || s.products == nil || s.applications == nil || s.capabilities == nil || s.workspaces == nil || s.renderer == nil || s.contracts == nil ||
		command.RunID == "" || command.ActorID == "" || command.TraceID == "" || len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 128 {
		return core.Run{}, ErrUnavailable
	}
	run, err := s.assembly.GetRun(ctx, command.RunID)
	if err != nil {
		return core.Run{}, err
	}
	if run.Status == core.RunStatusCompleted {
		return run, nil
	}
	if run.Status != core.RunStatusPlanned {
		return run, ErrPrecondition
	}
	// Recovery attempts must address downstream services with the immutable
	// root operation identity; the retrying administrator is recorded only by
	// the retry audit event.
	command.ActorID = run.CreatedBy
	command.IdempotencyKey = executionKey(run.RootRunID)
	plan, err := s.assembly.GetPlan(ctx, run.PlanID)
	if err != nil {
		return core.Run{}, err
	}
	blueprint, err := s.assembly.GetBlueprint(ctx, plan.BlueprintID, plan.BlueprintRevision)
	if err != nil {
		return core.Run{}, err
	}
	var blueprintValue blueprintDocument
	var planValue planDocument
	if json.Unmarshal(blueprint.Document, &blueprintValue) != nil || json.Unmarshal(plan.Document, &planValue) != nil ||
		planValue.PlanID != plan.PlanID || planValue.BlueprintID != blueprint.BlueprintID || planValue.BlueprintVersion != blueprint.Revision || len(planValue.Applications) == 0 {
		return run, ErrPrecondition
	}
	environment, applicationEnvironment, err := executionEnvironment(planValue.Environment)
	if err != nil {
		return s.failRun(ctx, command, run, "step.provision", "diagnostic.environment-unsupported", false, err)
	}

	run, err = s.updateRun(ctx, command, run, transitionSpec{Status: core.RunStatusProvisioning, CurrentStepID: "step.provision", Running: []string{"step.provision"}, Recovery: core.RunRecovery{Retryable: true, ResumeFromStepID: "step.provision"}}, "provisioning-started")
	if err != nil {
		return run, err
	}
	created, err := s.products.CreateProduct(ctx, productprovisioning.CreateCommand{
		ProductCode: blueprintValue.Product.Code, Name: blueprintValue.Product.Name, Status: "active", Environments: []string{environment},
		ActorID: command.ActorID, IdempotencyKey: derivedKey(command.IdempotencyKey, "product"), TraceID: command.TraceID,
	})
	if err != nil {
		return s.failRun(ctx, command, run, "step.provision", "diagnostic.product-provisioning", true, err)
	}
	run, err = s.assembly.BindProduct(ctx, core.BindProductCommand{
		ProductID: created.ProductID, RunID: run.RunID, ExpectedVersion: run.Version, ActorID: command.ActorID,
		IdempotencyKey: derivedKey(command.IdempotencyKey, "bind-product"), TraceID: command.TraceID,
	})
	if err != nil {
		return run, err
	}
	applications := make([]generation.ArtifactApplication, 0, len(planValue.Applications))
	for _, planned := range planValue.Applications {
		if planned.Environment != planValue.Environment {
			return s.failRun(ctx, command, run, "step.provision", "diagnostic.application-environment", false, ErrPrecondition)
		}
		createdApplication, createErr := s.applications.CreateApplication(ctx, productapplication.CreateCommand{
			Product:         productapplication.ProductContext{ProductID: created.ProductID, Environment: applicationEnvironment},
			ApplicationCode: planned.ApplicationID, Name: applicationName(blueprintValue.Product.Name, planned.ApplicationID), Platform: applicationPlatform(planned.Target),
			DistributionChannel: planned.Channel, ReleaseTrack: releaseTrack(applicationEnvironment), Status: productapplication.StatusActive,
			ActorID: command.ActorID, TraceID: command.TraceID, IdempotencyKey: derivedKey(command.IdempotencyKey, "application:"+planned.ApplicationID),
		})
		if createErr != nil {
			return s.failRun(ctx, command, run, "step.provision", "diagnostic.application-provisioning", true, createErr)
		}
		applications = append(applications, generation.ArtifactApplication{PlanApplicationID: planned.ApplicationID, ApplicationID: createdApplication.ApplicationID})
	}
	currentSet, currentErr := s.capabilities.CurrentCapabilitySet(ctx, created.ProductID)
	expectedCapabilityVersion := int64(0)
	if currentErr == nil {
		if currentSet.SourcePlanID == plan.PlanID && currentSet.CatalogRevision == plan.CatalogRevision && currentSet.CatalogSnapshotSHA256 == plan.CatalogSnapshotSHA256 {
			expectedCapabilityVersion = -1
		} else {
			expectedCapabilityVersion = currentSet.Version
		}
	} else if !errors.Is(currentErr, product.ErrNotFound) {
		return s.failRun(ctx, command, run, "step.enable-capability", "diagnostic.capability-read", true, currentErr)
	}
	if expectedCapabilityVersion >= 0 {
		_, err = s.capabilities.ReplaceCapabilitySet(ctx, product.ReplaceCapabilitySetCommand{
			Plan:            product.TrustedCapabilityChangePlan{ProductID: created.ProductID, SourcePlanID: plan.PlanID, CatalogRevision: plan.CatalogRevision, CatalogSnapshotSHA256: plan.CatalogSnapshotSHA256},
			ExpectedVersion: expectedCapabilityVersion, ActorID: command.ActorID, IdempotencyKey: derivedKey(command.IdempotencyKey, "capabilities"), TraceID: command.TraceID,
		})
		if err != nil {
			return s.failRun(ctx, command, run, "step.enable-capability", "diagnostic.capability-enable", true, err)
		}
	}

	run, err = s.updateRun(ctx, command, run, transitionSpec{
		Status: core.RunStatusGenerating, CurrentStepID: "step.generate", Completed: []string{"step.provision", "step.enable-capability"}, Running: []string{"step.generate"},
		Recovery: core.RunRecovery{Retryable: true, ResumeFromStepID: "step.generate"},
	}, "generation-started")
	if err != nil {
		return run, err
	}
	workspace, err := s.workspaces.Resolve(run.OutputTargetRef)
	if err != nil {
		return s.failRun(ctx, command, run, "step.generate", "diagnostic.workspace-unavailable", false, err)
	}
	request, previous, err := generation.BuildRequest(workspace.TargetRoot, generation.RequestSpec{
		WorkspaceRef: run.OutputTargetRef, RunID: run.RunID, RunCreatedAt: run.CreatedAt,
		Product:           generation.ArtifactProduct{ProductID: created.ProductID, OfficialTenantID: created.OfficialTenantID, Applications: applications},
		Blueprint:         generation.ArtifactBlueprint{BlueprintID: blueprint.BlueprintID, Version: blueprint.Revision, Checksum: blueprint.ContentSHA256},
		BlueprintDocument: blueprint.Document, PlanDocument: plan.Document,
	})
	if err != nil {
		return s.failRun(ctx, command, run, "step.generate", "diagnostic.generator-request", false, err)
	}
	artifactStore, err := generation.NewArtifactStore(workspace.ArtifactRoot)
	if err != nil {
		return s.failRun(ctx, command, run, "step.generate", "diagnostic.artifact-store", false, err)
	}
	renderer, err := s.rendererForScope(planValue.CatalogSnapshot.Scope)
	if err != nil {
		return s.failRun(ctx, command, run, "step.generate", "diagnostic.generator-unavailable", false, err)
	}
	executor, err := generation.NewExecutor(renderer, generation.NewFileCommitter(), artifactStore, s.contracts)
	if err != nil {
		return s.failRun(ctx, command, run, "step.generate", "diagnostic.generator-unavailable", false, err)
	}
	outcome, executionErr := executor.Execute(ctx, workspace.TargetRoot, request, generation.ProjectLock{}, previous)
	if executionErr != nil {
		diagnosticIDs := failureDiagnosticIDs(outcome.Failure.Result)
		if len(diagnosticIDs) == 0 {
			diagnosticIDs = []string{"diagnostic.generator-failed"}
		}
		diagnostics, reports, projectionErr := generatorFailureEvidence(outcome.Failure, s.now().UTC())
		if projectionErr != nil {
			return s.failRun(ctx, command, run, "step.generate", "diagnostic.generator-evidence", false, errors.Join(executionErr, projectionErr))
		}
		return s.failRunWithEvidence(ctx, command, run, "step.generate", diagnosticIDs, outcome.Commit.TargetUnchanged, diagnostics, reports, executionErr)
	}
	run, err = s.updateRun(ctx, command, run, transitionSpec{
		Status: core.RunStatusValidating, CurrentStepID: "step.validate", Completed: []string{"step.generate"}, Running: []string{"step.validate"},
		Recovery: core.RunRecovery{Retryable: true, ResumeFromStepID: "step.validate"},
	}, "validation-started")
	if err != nil {
		return run, err
	}
	completedDocument, err := nextRunDocument(run, s.now().UTC(), transitionSpec{
		Status: core.RunStatusCompleted, Completed: []string{"step.validate", "step.commit"}, ManifestPath: request.Request.ArtifactContext.Paths.AssemblyManifestPath,
		LockPath: request.Request.ArtifactContext.Paths.GeneratedLockPath, Recovery: core.RunRecovery{Retryable: false, RollbackRequired: false}, Terminal: true,
	})
	if err != nil {
		return run, err
	}
	return s.assembly.CompleteAssembly(ctx, core.CompleteAssemblyCommand{
		ProductID: created.ProductID, RunID: run.RunID, ExpectedVersion: run.Version, RunDocument: completedDocument,
		ManifestDocument: outcome.Bundle.AssemblyManifest, LockDocument: outcome.Bundle.GeneratedLock,
		ActorID: command.ActorID, IdempotencyKey: derivedKey(command.IdempotencyKey, "complete"), TraceID: command.TraceID,
	})
}

func (s *Service) rendererForScope(scope string) (generation.Renderer, error) {
	switch scope {
	case "ordinary":
		if s.renderer == nil {
			return nil, ErrUnavailable
		}
		return s.renderer, nil
	case "experimental":
		if s.experimentalRenderer == nil {
			return nil, ErrUnavailable
		}
		return s.experimentalRenderer, nil
	default:
		return nil, ErrPrecondition
	}
}

type transitionSpec struct {
	Status        core.RunStatus
	CurrentStepID string
	Running       []string
	Completed     []string
	Failed        []string
	DiagnosticIDs []string
	Recovery      core.RunRecovery
	ManifestPath  string
	LockPath      string
	Terminal      bool
}

func (s *Service) updateRun(ctx context.Context, command Command, run core.Run, spec transitionSpec, key string) (core.Run, error) {
	document, err := nextRunDocument(run, s.now().UTC(), spec)
	if err != nil {
		return run, err
	}
	return s.assembly.UpdateRun(ctx, core.UpdateRunCommand{
		RunID: run.RunID, ExpectedVersion: run.Version, Document: document, ActorID: command.ActorID,
		IdempotencyKey: derivedKey(command.IdempotencyKey, key), TraceID: command.TraceID,
	})
}

func (s *Service) failRun(ctx context.Context, command Command, run core.Run, stepID, diagnosticID string, retryable bool, cause error) (core.Run, error) {
	failed, err := s.failRunWithDiagnostics(ctx, command, run, stepID, []string{diagnosticID}, retryable, cause)
	if err != nil {
		return failed, err
	}
	if !retryable {
		return failed, cause
	}
	return failed, cause
}

func (s *Service) failRunWithDiagnostics(ctx context.Context, command Command, run core.Run, stepID string, diagnosticIDs []string, targetUnchanged bool, cause error) (core.Run, error) {
	return s.failRunWithEvidence(ctx, command, run, stepID, diagnosticIDs, targetUnchanged, nil, nil, cause)
}

func (s *Service) failRunWithEvidence(ctx context.Context, command Command, run core.Run, stepID string, diagnosticIDs []string, targetUnchanged bool, diagnostics []core.RunDiagnostic, reports []core.RunReport, cause error) (core.Run, error) {
	document, err := nextRunDocument(run, s.now().UTC(), transitionSpec{
		Status: core.RunStatusFailed, CurrentStepID: stepID, Failed: []string{stepID}, DiagnosticIDs: diagnosticIDs,
		Recovery: core.RunRecovery{Retryable: targetUnchanged, RollbackRequired: !targetUnchanged, ResumeFromStepID: stepID}, Terminal: true,
	})
	if err != nil {
		return run, errors.Join(cause, err)
	}
	updated, updateErr := s.assembly.UpdateRun(ctx, core.UpdateRunCommand{
		RunID: run.RunID, ExpectedVersion: run.Version, Document: document, ActorID: command.ActorID,
		IdempotencyKey: derivedKey(command.IdempotencyKey, "failed:"+stepID), TraceID: command.TraceID, Diagnostics: diagnostics, Reports: reports,
	})
	return updated, errors.Join(cause, updateErr)
}

func generatorFailureEvidence(failure generation.FailureArtifacts, now time.Time) ([]core.RunDiagnostic, []core.RunReport, error) {
	diagnostics := make([]core.RunDiagnostic, 0, len(failure.Diagnostics))
	for _, raw := range failure.Diagnostics {
		var value struct {
			DiagnosticID string   `json:"diagnostic_id"`
			Code         string   `json:"code"`
			Severity     string   `json:"severity"`
			Category     string   `json:"category"`
			Message      string   `json:"message"`
			Blocking     bool     `json:"blocking"`
			Retryable    bool     `json:"retryable"`
			Path         string   `json:"path"`
			RelatedPaths []string `json:"related_paths"`
			Remediation  []string `json:"remediation"`
		}
		if json.Unmarshal(raw, &value) != nil {
			return nil, nil, ErrInvalidRunDocument
		}
		code := strings.ToLower(strings.ReplaceAll(value.Code, "_", "."))
		category := strings.ToLower(strings.ReplaceAll(value.Category, "_", "."))
		paths := append([]string(nil), value.RelatedPaths...)
		if value.Path != "" {
			paths = append(paths, value.Path)
		}
		diagnostics = append(diagnostics, core.RunDiagnostic{DiagnosticID: value.DiagnosticID, Code: code, Severity: value.Severity, Category: category, Message: value.Message, Blocking: value.Blocking, Retryable: value.Retryable, Remediation: append([]string(nil), value.Remediation...), RelatedPaths: paths, CreatedAt: now})
	}
	sort.Slice(diagnostics, func(i, j int) bool { return diagnostics[i].DiagnosticID < diagnostics[j].DiagnosticID })
	var result struct {
		Status         string `json:"status"`
		ResultChecksum string `json:"result_checksum"`
	}
	if json.Unmarshal(failure.Result, &result) != nil || result.ResultChecksum == "" {
		return nil, nil, ErrInvalidRunDocument
	}
	report := core.RunReport{ReportID: "report.generator-result", ReportType: "generator_result", Status: "failed", Summary: "Generator execution failed; validated failure evidence is available", Checksum: result.ResultChecksum, CreatedAt: now}
	return diagnostics, []core.RunReport{report}, nil
}

type runMachineDocument struct {
	SchemaVersion        string           `json:"schema_version"`
	RunID                string           `json:"run_id"`
	RootRunID            string           `json:"root_run_id"`
	RetryOfRunID         string           `json:"retry_of_run_id,omitempty"`
	AttemptNumber        int              `json:"attempt_number"`
	PlanID               string           `json:"plan_id"`
	PlanChecksum         string           `json:"plan_checksum"`
	IdempotencyKeyDigest string           `json:"idempotency_key_digest"`
	OutputTargetRef      string           `json:"output_target_ref"`
	Status               core.RunStatus   `json:"status"`
	Steps                []core.RunStep   `json:"steps"`
	CurrentStepID        *string          `json:"current_step_id"`
	DiagnosticIDs        []string         `json:"diagnostic_ids"`
	ManifestPath         string           `json:"manifest_path,omitempty"`
	LockPath             string           `json:"lock_path,omitempty"`
	Recovery             core.RunRecovery `json:"recovery"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
	CompletedAt          *time.Time       `json:"completed_at,omitempty"`
}

func nextRunDocument(run core.Run, now time.Time, spec transitionSpec) (json.RawMessage, error) {
	steps := append([]core.RunStep(nil), run.Steps...)
	for index := range steps {
		step := &steps[index]
		switch {
		case contains(spec.Running, step.StepID):
			if step.Status == "pending" || step.Status == "failed" {
				step.Status = "running"
				step.Attempt++
				started := now
				step.StartedAt = &started
			}
		case contains(spec.Completed, step.StepID):
			if step.Status == "pending" {
				step.Attempt++
				started := now
				step.StartedAt = &started
			}
			step.Status = "completed"
			finished := now
			step.FinishedAt = &finished
		case contains(spec.Failed, step.StepID):
			if step.Status == "pending" {
				step.Attempt++
				started := now
				step.StartedAt = &started
			}
			step.Status = "failed"
			step.DiagnosticIDs = append([]string{}, spec.DiagnosticIDs...)
			finished := now
			step.FinishedAt = &finished
		}
	}
	var current *string
	if spec.CurrentStepID != "" {
		value := spec.CurrentStepID
		current = &value
	}
	document := runMachineDocument{
		SchemaVersion: "1.0.0", RunID: run.RunID, RootRunID: run.RootRunID, RetryOfRunID: run.RetryOfRunID, AttemptNumber: run.AttemptNumber, PlanID: run.PlanID, PlanChecksum: run.PlanSHA256,
		IdempotencyKeyDigest: run.IdempotencyKeyDigest, OutputTargetRef: run.OutputTargetRef, Status: spec.Status,
		Steps: steps, CurrentStepID: current, DiagnosticIDs: append([]string{}, spec.DiagnosticIDs...),
		ManifestPath: spec.ManifestPath, LockPath: spec.LockPath, Recovery: spec.Recovery,
		CreatedAt: run.CreatedAt.UTC(), UpdatedAt: maxTime(run.UpdatedAt.UTC(), now.UTC()),
	}
	if spec.Terminal {
		completed := document.UpdatedAt
		document.CompletedAt = &completed
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, ErrInvalidRunDocument
	}
	return raw, nil
}

func executionKey(rootRunID string) string {
	digest := sha256.Sum256([]byte("assembly-execution-root:" + rootRunID))
	return "dispatch_" + hex.EncodeToString(digest[:])
}

func executionEnvironment(value string) (string, productapplication.Environment, error) {
	switch value {
	case "development":
		return "local", productapplication.EnvironmentLocal, nil
	case "test":
		return "test", productapplication.EnvironmentTest, nil
	case "production":
		return "production", productapplication.EnvironmentProduction, nil
	default:
		return "", "", ErrEnvironment
	}
}

func applicationPlatform(target string) productapplication.Platform {
	switch target {
	case "web":
		return productapplication.PlatformWeb
	case "h5":
		return productapplication.PlatformH5
	case "wechat_miniprogram":
		return productapplication.PlatformWechatMiniProgram
	default:
		return productapplication.PlatformOther
	}
}

func releaseTrack(environment productapplication.Environment) productapplication.ReleaseTrack {
	if environment == productapplication.EnvironmentProduction {
		return productapplication.ReleaseTrackStable
	}
	return productapplication.ReleaseTrackInternal
}

func applicationName(productName, applicationID string) string {
	value := strings.TrimSpace(productName + " " + applicationID)
	runes := []rune(value)
	if len(runes) > 120 {
		value = string(runes[:120])
	}
	return value
}

func failureDiagnosticIDs(raw []byte) []string {
	var document struct {
		DiagnosticIDs []string `json:"diagnostic_ids"`
	}
	if json.Unmarshal(raw, &document) != nil {
		return nil
	}
	sort.Strings(document.DiagnosticIDs)
	return document.DiagnosticIDs
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func maxTime(first, second time.Time) time.Time {
	if first.After(second) {
		return first
	}
	return second
}

func derivedKey(root, step string) string {
	digest := sha256.Sum256([]byte(root + "\x00" + step))
	return hex.EncodeToString(digest[:])
}
