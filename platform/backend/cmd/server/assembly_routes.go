package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"sort"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	assemblyhttp "platform.local/capability-platform/backend/internal/modules/assembly/httptransport"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecatalog"
)

type assemblyAdminAdapter struct {
	service             assemblyCoreService
	lifecycle           *core.LifecycleService
	outputTarget        map[string]assemblyhttp.OutputTarget
	ordinaryCatalog     *machinecatalog.Catalog
	experimentalCatalog *machinecatalog.Catalog
}

func (a assemblyAdminAdapter) withLifecycle(service *core.LifecycleService) assemblyAdminAdapter {
	a.lifecycle = service
	return a
}

func (a assemblyAdminAdapter) GetLifecycleSource(ctx context.Context, command assemblyhttp.GetLifecycleSourceCommand) (assemblyhttp.LifecycleArtifactState, error) {
	if a.lifecycle == nil {
		return assemblyhttp.LifecycleArtifactState{}, assemblyhttp.ErrLifecycleUnavailable
	}
	value, err := a.lifecycle.GetCurrentSource(ctx, command.AssemblyID)
	return assemblyLifecycleArtifact(value), mapAssemblyError(err)
}

type assemblyCoreService interface {
	CreateBlueprint(context.Context, core.CreateBlueprintCommand) (core.Blueprint, error)
	GetBlueprint(context.Context, string, int64) (core.Blueprint, error)
	CreatePlan(context.Context, core.CreatePlanCommand) (core.Plan, error)
	GetPlan(context.Context, string) (core.Plan, error)
	ConfirmPlan(context.Context, core.ConfirmPlanCommand) (core.Plan, error)
	StartAssembly(context.Context, core.StartAssemblyCommand) (core.Run, error)
	GetRun(context.Context, string) (core.Run, error)
	GetManifest(context.Context, string) (core.Manifest, error)
	GetLock(context.Context, string) (core.GeneratedProjectLock, error)
}

type assemblyRecoveryService interface {
	ListRuns(context.Context, core.RunListFilter) (core.RunPage, error)
	RetryRun(context.Context, core.RetryRunCommand) (core.Run, error)
}

func newAssemblyAdminAdapter(service assemblyCoreService, outputTargets ...assemblyhttp.OutputTarget) assemblyAdminAdapter {
	return newAssemblyAdminAdapterWithCatalogs(service, nil, nil, outputTargets...)
}

func newAssemblyAdminAdapterWithCatalogs(service assemblyCoreService, ordinaryCatalog, experimentalCatalog *machinecatalog.Catalog, outputTargets ...assemblyhttp.OutputTarget) assemblyAdminAdapter {
	targets := make(map[string]assemblyhttp.OutputTarget, len(outputTargets))
	for _, target := range outputTargets {
		if target.OutputTargetRef != "" && target.Environment != "" {
			targets[outputTargetKey(target.Environment, target.OutputTargetRef)] = target
		}
	}
	return assemblyAdminAdapter{service: service, outputTarget: targets, ordinaryCatalog: ordinaryCatalog, experimentalCatalog: experimentalCatalog}
}

func (a assemblyAdminAdapter) ListCatalogOptions(_ context.Context, command assemblyhttp.ListCatalogOptionsCommand) (assemblyhttp.CatalogOptions, error) {
	return catalogOptionsFrom(a.ordinaryCatalog, command)
}

func (a assemblyAdminAdapter) ListExperimentalCatalogOptions(_ context.Context, command assemblyhttp.ListCatalogOptionsCommand) (assemblyhttp.CatalogOptions, error) {
	return catalogOptionsFrom(a.experimentalCatalog, command)
}

func catalogOptionsFrom(catalog *machinecatalog.Catalog, command assemblyhttp.ListCatalogOptionsCommand) (assemblyhttp.CatalogOptions, error) {
	if catalog == nil {
		return assemblyhttp.CatalogOptions{}, assemblyhttp.ErrPlanUnavailable
	}
	value, err := catalog.Options(command.Target, command.DeliveryMode, command.Environment)
	if err != nil {
		return assemblyhttp.CatalogOptions{}, err
	}
	result := assemblyhttp.CatalogOptions{CatalogScope: value.CatalogScope, CatalogRevision: value.CatalogRevision, Target: value.Target, DeliveryMode: value.DeliveryMode, Environment: value.Environment,
		Packages: make([]assemblyhttp.CatalogPackageOption, len(value.Packages)), Templates: make([]assemblyhttp.CatalogTemplateOption, len(value.Templates)), Generators: mapToolOptions(value.Generators), SDKs: mapToolOptions(value.SDKs)}
	for i, item := range value.Packages {
		result.Packages[i] = assemblyhttp.CatalogPackageOption{PackageID: item.PackageID, Version: item.Version, Name: item.Name, UserValue: item.UserValue,
			Dependencies: mapRequirements(item.Dependencies), Conflicts: mapRequirements(item.Conflicts), CompatibleTemplateRefs: mapVersionRefs(item.CompatibleTemplateRefs)}
	}
	for i, item := range value.Templates {
		result.Templates[i] = assemblyhttp.CatalogTemplateOption{TemplateID: item.TemplateID, Version: item.Version, Name: item.Name, SupportedBlocks: item.SupportedBlocks}
	}
	return result, nil
}

func mapRequirements(values []machinecatalog.Requirement) []assemblyhttp.CatalogRequirement {
	result := make([]assemblyhttp.CatalogRequirement, len(values))
	for i, v := range values {
		result[i] = assemblyhttp.CatalogRequirement{PackageID: v.PackageID, VersionRange: v.VersionRange}
	}
	return result
}
func mapVersionRefs(values []machinecatalog.VersionRef) []assemblyhttp.CatalogVersionRef {
	result := make([]assemblyhttp.CatalogVersionRef, len(values))
	for i, v := range values {
		result[i] = assemblyhttp.CatalogVersionRef{ID: v.ID, Version: v.Version}
	}
	return result
}
func mapToolOptions(values []machinecatalog.ToolOption) []assemblyhttp.CatalogToolOption {
	result := make([]assemblyhttp.CatalogToolOption, len(values))
	for i, v := range values {
		result[i] = assemblyhttp.CatalogToolOption{ID: v.ID, Version: v.Version, Name: v.Name}
	}
	return result
}

func (a assemblyAdminAdapter) ListOutputTargets(_ context.Context, command assemblyhttp.ListOutputTargetsCommand) (assemblyhttp.OutputTargetList, error) {
	items := make([]assemblyhttp.OutputTarget, 0, len(a.outputTarget))
	for _, target := range a.outputTarget {
		if target.Environment == command.Environment {
			items = append(items, target)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].OutputTargetRef < items[j].OutputTargetRef })
	var defaultRef *string
	for _, item := range items {
		if item.IsDefault {
			value := item.OutputTargetRef
			defaultRef = &value
			break
		}
	}
	return assemblyhttp.OutputTargetList{Environment: command.Environment, DefaultOutputTargetRef: defaultRef, Items: items}, nil
}

func (a assemblyAdminAdapter) CreateBlueprint(ctx context.Context, command assemblyhttp.CreateBlueprintCommand) (assemblyhttp.Blueprint, error) {
	value, err := a.service.CreateBlueprint(ctx, core.CreateBlueprintCommand{
		Document: command.Document, ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID,
	})
	return assemblyBlueprint(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) GetBlueprint(ctx context.Context, command assemblyhttp.GetBlueprintCommand) (assemblyhttp.Blueprint, error) {
	value, err := a.service.GetBlueprint(ctx, command.BlueprintID, 0)
	return assemblyBlueprint(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) CreatePlan(ctx context.Context, command assemblyhttp.CreatePlanCommand) (assemblyhttp.Plan, error) {
	value, err := a.service.CreatePlan(ctx, core.CreatePlanCommand{
		BlueprintID: command.BlueprintID, BlueprintVersion: command.BlueprintVersion, Environment: command.Environment,
		CatalogScope: command.CatalogScope, ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID,
	})
	return assemblyPlan(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) GetPlan(ctx context.Context, command assemblyhttp.GetPlanCommand) (assemblyhttp.Plan, error) {
	value, err := a.service.GetPlan(ctx, command.PlanID)
	return assemblyPlan(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) StartAssembly(ctx context.Context, command assemblyhttp.StartAssemblyCommand) (assemblyhttp.Run, error) {
	plan, err := a.service.GetPlan(ctx, command.PlanID)
	if err != nil {
		return assemblyhttp.Run{}, mapAssemblyError(err)
	}
	if plan.BlueprintID != command.BlueprintID || !constantTimeEqual(plan.PlanSHA256, command.PlanChecksum) {
		return assemblyhttp.Run{}, assemblyhttp.ErrConflict
	}
	if _, allowed := a.outputTarget[outputTargetKey(plan.Environment, command.OutputTargetRef)]; !allowed {
		return assemblyhttp.Run{}, assemblyhttp.ErrOutputTargetUnavailable
	}
	confirmed := plan
	if plan.Version == command.ExpectedPlanVersion {
		confirmed, err = a.service.ConfirmPlan(ctx, core.ConfirmPlanCommand{
			PlanID: command.PlanID, ConfirmationChecksum: command.ConfirmationChecksum, ExpectedVersion: command.ExpectedPlanVersion,
			ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID,
		})
		if err != nil {
			return assemblyhttp.Run{}, mapAssemblyError(err)
		}
	} else if plan.Version != command.ExpectedPlanVersion+1 || plan.ConfirmedAt == nil {
		return assemblyhttp.Run{}, assemblyhttp.ErrVersionConflict
	}
	run, err := a.service.StartAssembly(ctx, core.StartAssemblyCommand{
		PlanID: command.PlanID, PlanChecksum: command.PlanChecksum, ConfirmationChecksum: command.ConfirmationChecksum,
		OutputTargetRef: command.OutputTargetRef, ExpectedPlanVersion: confirmed.Version,
		ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID,
	})
	return assemblyRun(run), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) GetRun(ctx context.Context, command assemblyhttp.GetRunCommand) (assemblyhttp.Run, error) {
	value, err := a.service.GetRun(ctx, command.RunID)
	return assemblyRun(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) ListRuns(ctx context.Context, command assemblyhttp.ListRunsCommand) (assemblyhttp.RunPage, error) {
	service, ok := a.service.(assemblyRecoveryService)
	if !ok {
		return assemblyhttp.RunPage{}, assemblyhttp.ErrOperationInProgress
	}
	value, err := service.ListRuns(ctx, core.RunListFilter{PageSize: command.PageSize, Cursor: command.Cursor, Status: core.RunStatus(command.Status), ProductID: command.ProductID})
	result := assemblyhttp.RunPage{Items: make([]assemblyhttp.RunSummary, len(value.Items)), NextCursor: value.NextCursor}
	for i, item := range value.Items {
		result.Items[i] = assemblyhttp.RunSummary{RunID: item.RunID, ProductID: item.ProductID, PlanID: item.PlanID, Version: item.Version, RootRunID: item.RootRunID, RetryOfRunID: item.RetryOfRunID, AttemptNumber: item.AttemptNumber, Status: string(item.Status), CurrentStepID: item.CurrentStepID, DiagnosticCount: item.DiagnosticCount, ReportCount: item.ReportCount, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, CompletedAt: item.CompletedAt}
	}
	return result, mapAssemblyError(err)
}

func (a assemblyAdminAdapter) RetryRun(ctx context.Context, command assemblyhttp.RetryRunCommand) (assemblyhttp.Run, error) {
	service, ok := a.service.(assemblyRecoveryService)
	if !ok {
		return assemblyhttp.Run{}, assemblyhttp.ErrOperationInProgress
	}
	value, err := service.RetryRun(ctx, core.RetryRunCommand{RunID: command.RunID, ExpectedVersion: command.ExpectedVersion, ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID})
	return assemblyRun(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) GetManifest(ctx context.Context, command assemblyhttp.GetManifestCommand) (assemblyhttp.Manifest, error) {
	value, err := a.service.GetManifest(ctx, command.AssemblyID)
	return assemblyhttp.Manifest{
		AssemblyID: value.AssemblyID, ProductID: value.ProductID, RunID: value.RunID, LifecycleOperationID: value.LifecycleOperationID, SchemaVersion: value.SchemaVersion,
		Document: value.Document, DocumentChecksum: value.DocumentSHA256, Checksum: value.ManifestSHA256, CreatedAt: value.CreatedAt,
	}, mapAssemblyError(err)
}

func (a assemblyAdminAdapter) GetLock(ctx context.Context, command assemblyhttp.GetLockCommand) (assemblyhttp.GeneratedProjectLock, error) {
	value, err := a.service.GetLock(ctx, command.LockID)
	return assemblyhttp.GeneratedProjectLock{
		LockID: value.LockID, ProductID: value.ProductID, RunID: value.RunID, LifecycleOperationID: value.LifecycleOperationID, AssemblyID: value.AssemblyID,
		SchemaVersion: value.SchemaVersion, Document: value.Document, DocumentChecksum: value.DocumentSHA256,
		Checksum: value.LockSHA256, CreatedAt: value.CreatedAt,
	}, mapAssemblyError(err)
}

func (a assemblyAdminAdapter) CreateUpgradePlan(ctx context.Context, command assemblyhttp.CreateUpgradePlanCommand) (assemblyhttp.LifecyclePlan, error) {
	if a.lifecycle == nil {
		return assemblyhttp.LifecyclePlan{}, assemblyhttp.ErrLifecycleUnavailable
	}
	value, err := a.lifecycle.CreateUpgradePlan(ctx, core.CreateUpgradePlanCommand{
		AssemblyID: command.AssemblyID, ExpectedManifestChecksum: command.ExpectedManifestChecksum, ExpectedLockChecksum: command.ExpectedLockChecksum,
		Target: coreLifecycleTarget(command.Target), ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID,
	})
	return assemblyLifecyclePlan(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) CreateEjectPlan(ctx context.Context, command assemblyhttp.CreateEjectPlanCommand) (assemblyhttp.LifecyclePlan, error) {
	if a.lifecycle == nil {
		return assemblyhttp.LifecyclePlan{}, assemblyhttp.ErrLifecycleUnavailable
	}
	value, err := a.lifecycle.CreateEjectPlan(ctx, core.CreateEjectPlanCommand{
		AssemblyID: command.AssemblyID, ExpectedManifestChecksum: command.ExpectedManifestChecksum, ExpectedLockChecksum: command.ExpectedLockChecksum,
		Paths: command.Paths, ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID,
	})
	return assemblyLifecyclePlan(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) GetLifecyclePlan(ctx context.Context, command assemblyhttp.GetLifecyclePlanCommand) (assemblyhttp.LifecyclePlan, error) {
	if a.lifecycle == nil {
		return assemblyhttp.LifecyclePlan{}, assemblyhttp.ErrLifecycleUnavailable
	}
	value, err := a.lifecycle.GetPlan(ctx, command.LifecyclePlanID)
	return assemblyLifecyclePlan(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) ExecuteLifecyclePlan(ctx context.Context, command assemblyhttp.ExecuteLifecyclePlanCommand) (assemblyhttp.LifecycleOperation, error) {
	if a.lifecycle == nil {
		return assemblyhttp.LifecycleOperation{}, assemblyhttp.ErrLifecycleUnavailable
	}
	value, err := a.lifecycle.ExecutePlan(ctx, core.ExecuteLifecyclePlanCommand{
		LifecyclePlanID: command.LifecyclePlanID, ExpectedVersion: command.ExpectedVersion, PlanChecksum: command.PlanChecksum,
		ConfirmationChecksum: command.ConfirmationChecksum, ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID,
	})
	return assemblyLifecycleOperation(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) GetLifecycleOperation(ctx context.Context, command assemblyhttp.GetLifecycleOperationCommand) (assemblyhttp.LifecycleOperation, error) {
	if a.lifecycle == nil {
		return assemblyhttp.LifecycleOperation{}, assemblyhttp.ErrLifecycleUnavailable
	}
	value, err := a.lifecycle.GetOperation(ctx, command.OperationID)
	return assemblyLifecycleOperation(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) CancelLifecycleOperation(ctx context.Context, command assemblyhttp.CancelLifecycleOperationCommand) (assemblyhttp.LifecycleOperation, error) {
	if a.lifecycle == nil {
		return assemblyhttp.LifecycleOperation{}, assemblyhttp.ErrLifecycleUnavailable
	}
	value, err := a.lifecycle.CancelOperation(ctx, core.CancelLifecycleOperationCommand{
		OperationID: command.OperationID, ExpectedVersion: command.ExpectedVersion, Reason: command.Reason,
		ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID,
	})
	return assemblyLifecycleOperation(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) RollbackLifecycleOperation(ctx context.Context, command assemblyhttp.RollbackLifecycleOperationCommand) (assemblyhttp.LifecycleOperation, error) {
	if a.lifecycle == nil {
		return assemblyhttp.LifecycleOperation{}, assemblyhttp.ErrLifecycleUnavailable
	}
	value, err := a.lifecycle.RollbackOperation(ctx, core.RollbackLifecycleOperationCommand{
		OperationID: command.OperationID, ExpectedVersion: command.ExpectedVersion, Reason: command.Reason,
		ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID,
	})
	return assemblyLifecycleOperation(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) CancelRun(ctx context.Context, command assemblyhttp.CancelRunCommand) (assemblyhttp.Run, error) {
	service, ok := a.service.(interface {
		CancelRun(context.Context, core.CancelRunCommand) (core.Run, error)
	})
	if !ok {
		return assemblyhttp.Run{}, assemblyhttp.ErrLifecycleUnavailable
	}
	value, err := service.CancelRun(ctx, core.CancelRunCommand{RunID: command.RunID, ExpectedVersion: command.ExpectedVersion, Reason: command.Reason, ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID})
	return assemblyRun(value), mapAssemblyError(err)
}

func coreLifecycleTarget(value assemblyhttp.LifecycleTargetVersions) core.LifecycleTargetVersions {
	return core.LifecycleTargetVersions{Packages: coreLifecycleVersionRefs(value.Packages), Templates: coreLifecycleVersionRefs(value.Templates), Generator: core.LifecycleVersionRef{ID: value.Generator.ID, Version: value.Generator.Version}, SDKs: coreLifecycleVersionRefs(value.SDKs)}
}

func coreLifecycleVersionRefs(values []assemblyhttp.LifecycleVersionRef) []core.LifecycleVersionRef {
	result := make([]core.LifecycleVersionRef, len(values))
	for index, value := range values {
		result[index] = core.LifecycleVersionRef{ID: value.ID, Version: value.Version}
	}
	return result
}

func assemblyLifecyclePlan(value core.LifecyclePlan) assemblyhttp.LifecyclePlan {
	changes := make([]assemblyhttp.LifecycleChange, len(value.Changes))
	for index, item := range value.Changes {
		changes[index] = assemblyhttp.LifecycleChange{Path: item.Path, Action: item.Action, Ownership: item.Ownership, BeforeChecksum: item.BeforeChecksum, AfterChecksum: item.AfterChecksum, SourceID: item.SourceID, SourceVersion: item.SourceVersion}
	}
	migrations := make([]assemblyhttp.LifecycleMigration, len(value.Migrations))
	for index, item := range value.Migrations {
		migrations[index] = assemblyhttp.LifecycleMigration{MigrationID: item.MigrationID, Kind: item.Kind, Reversibility: item.Reversibility, Summary: item.Summary}
	}
	conflicts := make([]assemblyhttp.LifecycleConflict, len(value.Conflicts))
	for index, item := range value.Conflicts {
		conflicts[index] = assemblyhttp.LifecycleConflict{ConflictID: item.ConflictID, Code: item.Code, Category: item.Category, Blocking: item.Blocking, Message: item.Message, Paths: item.Paths, Remediation: item.Remediation}
	}
	return assemblyhttp.LifecyclePlan{LifecyclePlanID: value.LifecyclePlanID, AssemblyID: value.AssemblyID, ProductID: value.ProductID, Operation: string(value.Operation), Version: value.Version, Source: assemblyLifecycleArtifact(value.Source), TargetSnapshotChecksum: value.TargetSnapshotChecksum, Changes: changes, Migrations: migrations, Conflicts: conflicts, RegressionTests: value.RegressionTests, Rollback: assemblyhttp.LifecycleRollbackPolicy{Strategy: value.Rollback.Strategy, Automatic: value.Rollback.Automatic, PredecessorManifestChecksum: value.Rollback.PredecessorManifestChecksum, PredecessorLockChecksum: value.Rollback.PredecessorLockChecksum}, BlockingConflictCount: value.BlockingConflictCount, Executable: value.Executable, ConfirmationChecksum: value.ConfirmationChecksum, Statements: value.Statements, PlanChecksum: value.PlanChecksum, Document: value.Document, CreatedAt: value.CreatedAt, AuditID: value.AuditID}
}

func assemblyLifecycleOperation(value core.LifecycleOperation) assemblyhttp.LifecycleOperation {
	var target *assemblyhttp.LifecycleArtifactState
	if value.Target != nil {
		mapped := assemblyLifecycleArtifact(*value.Target)
		target = &mapped
	}
	manifestURL, lockURL := "", ""
	if target != nil {
		manifestURL = "/api/v1/admin/assembly-manifests/" + target.ManifestID
		lockURL = "/api/v1/admin/generated-project-locks/" + target.LockID
	}
	return assemblyhttp.LifecycleOperation{OperationID: value.OperationID, RootOperationID: value.RootOperationID, RollbackOfOperationID: value.RollbackOfOperationID, LifecyclePlanID: value.LifecyclePlanID, AssemblyID: value.AssemblyID, ProductID: value.ProductID, Kind: string(value.Kind), Version: value.Version, Status: string(value.Status), CurrentStep: value.CurrentStep, Source: assemblyLifecycleArtifact(value.Source), Target: target, Recovery: assemblyhttp.LifecycleRecovery{Retryable: value.Recovery.Retryable, RollbackAvailable: value.Recovery.RollbackAvailable, CancelAllowed: value.Recovery.CancelAllowed}, Diagnostics: mapRunDiagnostics(value.Diagnostics), Reports: mapRunReports(value.Reports), Document: value.Document, ManifestURL: manifestURL, LockURL: lockURL, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, CompletedAt: value.CompletedAt, AuditID: value.AuditID}
}

func assemblyLifecycleArtifact(value core.LifecycleArtifactState) assemblyhttp.LifecycleArtifactState {
	return assemblyhttp.LifecycleArtifactState{ManifestID: value.ManifestID, ManifestChecksum: value.ManifestChecksum, LockID: value.LockID, LockChecksum: value.LockChecksum, CatalogChecksum: value.CatalogChecksum, TargetSnapshotChecksum: value.TargetSnapshotChecksum}
}

func assemblyBlueprint(value core.Blueprint) assemblyhttp.Blueprint {
	return assemblyhttp.Blueprint{
		BlueprintID: value.BlueprintID, Version: value.Revision, SchemaVersion: value.SchemaVersion,
		Document: value.Document, Checksum: value.ContentSHA256, Environments: value.Environments, CreatedAt: value.CreatedAt, UpdatedAt: value.CreatedAt, AuditID: value.AuditID,
	}
}

func assemblyPlan(value core.Plan) assemblyhttp.Plan {
	return assemblyhttp.Plan{
		PlanID: value.PlanID, Version: value.Version, BlueprintID: value.BlueprintID, BlueprintVersion: value.BlueprintRevision,
		SchemaVersion: value.SchemaVersion, Environment: value.Environment, Document: value.Document, Checksum: value.PlanSHA256, ConfirmationChecksum: value.ConfirmationChecksum, Review: mapPlanReview(value.Review),
		Executable: value.Executable, Confirmed: value.ConfirmedAt != nil, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, AuditID: value.AuditID,
	}
}

func mapPlanReview(value core.PlanReview) assemblyhttp.PlanReview {
	result := assemblyhttp.PlanReview{Packages: make([]assemblyhttp.PlanReviewPackage, len(value.Packages)), Applications: make([]assemblyhttp.PlanReviewApplication, len(value.Applications)), Risks: make([]assemblyhttp.PlanReviewRisk, len(value.Risks)), BlockingConflictCount: value.BlockingConflictCount, Statements: value.Statements}
	for i, v := range value.Packages {
		result.Packages[i] = assemblyhttp.PlanReviewPackage{PackageID: v.PackageID, Version: v.Version}
	}
	for i, v := range value.Applications {
		result.Applications[i] = assemblyhttp.PlanReviewApplication{ApplicationID: v.ApplicationID, Target: v.Target, Channel: v.Channel, DeliveryMode: v.DeliveryMode, TemplateID: v.TemplateID, TemplateVersion: v.TemplateVersion}
	}
	for i, v := range value.Risks {
		result.Risks[i] = assemblyhttp.PlanReviewRisk{RiskID: v.RiskID, Level: v.Level, Category: v.Category, Summary: v.Summary, RequiresConfirmation: v.RequiresConfirmation}
	}
	return result
}

func assemblyRun(value core.Run) assemblyhttp.Run {
	manifestURL, lockURL := "", ""
	if value.ManifestID != "" {
		manifestURL = "/api/v1/admin/assembly-manifests/" + value.ManifestID
	}
	if value.LockID != "" {
		lockURL = "/api/v1/admin/generated-project-locks/" + value.LockID
	}
	return assemblyhttp.Run{
		RunID: value.RunID, ProductID: value.ProductID, Version: value.Version, RootRunID: value.RootRunID, RetryOfRunID: value.RetryOfRunID, AttemptNumber: value.AttemptNumber, PlanID: value.PlanID, PlanVersion: value.PlanVersion, PlanChecksum: value.PlanSHA256,
		OutputTargetRef: value.OutputTargetRef, Status: string(value.Status), Document: value.Document, ManifestURL: manifestURL, LockURL: lockURL,
		CurrentStepID: value.CurrentStepID, Steps: mapRunSteps(value.Steps), Recovery: assemblyhttp.RunRecovery{Retryable: value.Recovery.Retryable, RollbackRequired: value.Recovery.RollbackRequired, ResumeFromStepID: value.Recovery.ResumeFromStepID}, Diagnostics: mapRunDiagnostics(value.Diagnostics), Reports: mapRunReports(value.Reports),
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, CompletedAt: value.CompletedAt, AuditID: value.AuditID,
	}
}

func mapRunSteps(values []core.RunStep) []assemblyhttp.RunStep {
	result := make([]assemblyhttp.RunStep, len(values))
	for i, v := range values {
		result[i] = assemblyhttp.RunStep{StepID: v.StepID, Kind: v.Kind, Status: v.Status, Attempt: v.Attempt, CompensationStatus: v.CompensationStatus, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt, DiagnosticIDs: v.DiagnosticIDs}
	}
	return result
}
func mapRunDiagnostics(values []core.RunDiagnostic) []assemblyhttp.RunDiagnostic {
	result := make([]assemblyhttp.RunDiagnostic, len(values))
	for i, v := range values {
		result[i] = assemblyhttp.RunDiagnostic{DiagnosticID: v.DiagnosticID, Code: v.Code, Severity: v.Severity, Category: v.Category, Message: v.Message, Blocking: v.Blocking, Retryable: v.Retryable, Remediation: v.Remediation, RelatedPaths: v.RelatedPaths, CreatedAt: v.CreatedAt}
	}
	return result
}
func mapRunReports(values []core.RunReport) []assemblyhttp.RunReport {
	result := make([]assemblyhttp.RunReport, len(values))
	for i, v := range values {
		result[i] = assemblyhttp.RunReport{ReportID: v.ReportID, ReportType: v.ReportType, Status: v.Status, Summary: v.Summary, Checksum: v.Checksum, CreatedAt: v.CreatedAt}
	}
	return result
}

func outputTargetKey(environment, reference string) string { return environment + "\x00" + reference }

func newCoreOutputTargetVerifier(outputTargets ...assemblyhttp.OutputTarget) core.OutputTargetVerifier {
	allowed := make(map[string]struct{}, len(outputTargets))
	for _, target := range outputTargets {
		if target.Environment != "" && target.OutputTargetRef != "" {
			allowed[outputTargetKey(target.Environment, target.OutputTargetRef)] = struct{}{}
		}
	}
	return core.OutputTargetVerifierFunc(func(_ context.Context, environment, outputTargetRef string) error {
		if _, ok := allowed[outputTargetKey(environment, outputTargetRef)]; !ok {
			return core.ErrOutputTargetUnavailable
		}
		return nil
	})
}

func mapAssemblyError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, core.ErrInvalidCommand):
		return assemblyhttp.ErrInvalidCommand
	case errors.Is(err, core.ErrDocumentInvalid):
		return assemblyhttp.ErrDocumentInvalid
	case errors.Is(err, core.ErrNotFound):
		return assemblyhttp.ErrNotFound
	case errors.Is(err, core.ErrConflict):
		return assemblyhttp.ErrConflict
	case errors.Is(err, core.ErrVersionConflict):
		return assemblyhttp.ErrVersionConflict
	case errors.Is(err, core.ErrIdempotencyConflict):
		return assemblyhttp.ErrIdempotencyConflict
	case errors.Is(err, core.ErrOperationInProgress):
		return assemblyhttp.ErrOperationInProgress
	case errors.Is(err, core.ErrPlanUnavailable):
		return assemblyhttp.ErrPlanUnavailable
	case errors.Is(err, core.ErrPlanNotExecutable):
		return assemblyhttp.ErrPlanNotExecutable
	case errors.Is(err, core.ErrPlanNotConfirmed):
		return assemblyhttp.ErrPlanNotConfirmed
	case errors.Is(err, core.ErrOutputTargetUnavailable):
		return assemblyhttp.ErrOutputTargetUnavailable
	case errors.Is(err, core.ErrLifecycleUnavailable):
		return assemblyhttp.ErrLifecycleUnavailable
	case errors.Is(err, core.ErrInvalidRunTransition):
		return assemblyhttp.ErrConflict
	default:
		return err
	}
}

func constantTimeEqual(first, second string) bool {
	return len(first) == len(second) && subtle.ConstantTimeCompare([]byte(first), []byte(second)) == 1
}
