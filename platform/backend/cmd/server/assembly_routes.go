package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"sort"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	assemblyhttp "platform.local/capability-platform/backend/internal/modules/assembly/httptransport"
	"platform.local/capability-platform/backend/internal/workflows/assemblyexecution"
)

type assemblyAdminAdapter struct {
	service      assemblyCoreService
	executor     assemblyRunExecutor
	outputTarget map[string]assemblyhttp.OutputTarget
}

type assemblyRunExecutor interface {
	Execute(context.Context, assemblyexecution.Command) (core.Run, error)
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

func newAssemblyAdminAdapter(service assemblyCoreService, outputTargets ...assemblyhttp.OutputTarget) assemblyAdminAdapter {
	return newAssemblyAdminAdapterWithExecutor(service, nil, outputTargets...)
}

func newAssemblyAdminAdapterWithExecutor(service assemblyCoreService, executor assemblyRunExecutor, outputTargets ...assemblyhttp.OutputTarget) assemblyAdminAdapter {
	targets := make(map[string]assemblyhttp.OutputTarget, len(outputTargets))
	for _, target := range outputTargets {
		if target.OutputTargetRef != "" && target.Environment != "" {
			targets[outputTargetKey(target.Environment, target.OutputTargetRef)] = target
		}
	}
	return assemblyAdminAdapter{service: service, executor: executor, outputTarget: targets}
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
		ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID,
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
	if err == nil && a.executor != nil {
		executed, executionErr := a.executor.Execute(ctx, assemblyexecution.Command{
			RunID: run.RunID, ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID,
		})
		if executed.RunID != "" {
			run = executed
		}
		if executionErr != nil && executed.RunID == "" {
			return assemblyhttp.Run{}, executionErr
		}
	}
	return assemblyRun(run), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) GetRun(ctx context.Context, command assemblyhttp.GetRunCommand) (assemblyhttp.Run, error) {
	value, err := a.service.GetRun(ctx, command.RunID)
	return assemblyRun(value), mapAssemblyError(err)
}

func (a assemblyAdminAdapter) GetManifest(ctx context.Context, command assemblyhttp.GetManifestCommand) (assemblyhttp.Manifest, error) {
	value, err := a.service.GetManifest(ctx, command.AssemblyID)
	return assemblyhttp.Manifest{
		AssemblyID: value.AssemblyID, ProductID: value.ProductID, RunID: value.RunID, SchemaVersion: value.SchemaVersion,
		Document: value.Document, DocumentChecksum: value.DocumentSHA256, Checksum: value.ManifestSHA256, CreatedAt: value.CreatedAt,
	}, mapAssemblyError(err)
}

func (a assemblyAdminAdapter) GetLock(ctx context.Context, command assemblyhttp.GetLockCommand) (assemblyhttp.GeneratedProjectLock, error) {
	value, err := a.service.GetLock(ctx, command.LockID)
	return assemblyhttp.GeneratedProjectLock{
		LockID: value.LockID, ProductID: value.ProductID, RunID: value.RunID, AssemblyID: value.AssemblyID,
		SchemaVersion: value.SchemaVersion, Document: value.Document, DocumentChecksum: value.DocumentSHA256,
		Checksum: value.LockSHA256, CreatedAt: value.CreatedAt,
	}, mapAssemblyError(err)
}

func assemblyBlueprint(value core.Blueprint) assemblyhttp.Blueprint {
	return assemblyhttp.Blueprint{
		BlueprintID: value.BlueprintID, Version: value.Revision, SchemaVersion: value.SchemaVersion,
		Document: value.Document, Checksum: value.ContentSHA256, CreatedAt: value.CreatedAt, UpdatedAt: value.CreatedAt, AuditID: value.AuditID,
	}
}

func assemblyPlan(value core.Plan) assemblyhttp.Plan {
	return assemblyhttp.Plan{
		PlanID: value.PlanID, Version: value.Version, BlueprintID: value.BlueprintID, BlueprintVersion: value.BlueprintRevision,
		SchemaVersion: value.SchemaVersion, Environment: value.Environment, Document: value.Document, Checksum: value.PlanSHA256,
		Executable: value.Executable, Confirmed: value.ConfirmedAt != nil, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, AuditID: value.AuditID,
	}
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
		RunID: value.RunID, PlanID: value.PlanID, PlanVersion: value.PlanVersion, PlanChecksum: value.PlanSHA256,
		OutputTargetRef: value.OutputTargetRef, Status: string(value.Status), Document: value.Document, ManifestURL: manifestURL, LockURL: lockURL,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, CompletedAt: value.CompletedAt, AuditID: value.AuditID,
	}
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
	case errors.Is(err, core.ErrInvalidRunTransition):
		return assemblyhttp.ErrConflict
	default:
		return err
	}
}

func constantTimeEqual(first, second string) bool {
	return len(first) == len(second) && subtle.ConstantTimeCompare([]byte(first), []byte(second)) == 1
}
