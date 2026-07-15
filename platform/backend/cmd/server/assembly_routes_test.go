package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	assemblyhttp "platform.local/capability-platform/backend/internal/modules/assembly/httptransport"
)

const assemblyTestDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type assemblyCoreStub struct {
	plan         core.Plan
	confirmed    core.Plan
	run          core.Run
	confirmCalls int
	startCalls   int
}

func (*assemblyCoreStub) CreateBlueprint(context.Context, core.CreateBlueprintCommand) (core.Blueprint, error) {
	return core.Blueprint{}, nil
}
func (*assemblyCoreStub) GetBlueprint(context.Context, string, int64) (core.Blueprint, error) {
	return core.Blueprint{}, nil
}
func (*assemblyCoreStub) CreatePlan(context.Context, core.CreatePlanCommand) (core.Plan, error) {
	return core.Plan{}, nil
}
func (s *assemblyCoreStub) GetPlan(context.Context, string) (core.Plan, error) { return s.plan, nil }
func (s *assemblyCoreStub) ConfirmPlan(_ context.Context, command core.ConfirmPlanCommand) (core.Plan, error) {
	s.confirmCalls++
	if command.ConfirmationChecksum != assemblyTestDigest {
		return core.Plan{}, core.ErrConflict
	}
	return s.confirmed, nil
}
func (s *assemblyCoreStub) StartAssembly(_ context.Context, command core.StartAssemblyCommand) (core.Run, error) {
	s.startCalls++
	if command.ExpectedPlanVersion != s.confirmed.Version {
		return core.Run{}, core.ErrVersionConflict
	}
	if command.OutputTargetRef != "workspace.default" {
		return core.Run{}, core.ErrConflict
	}
	return s.run, nil
}
func (*assemblyCoreStub) GetRun(context.Context, string) (core.Run, error) { return core.Run{}, nil }
func (*assemblyCoreStub) GetManifest(context.Context, string) (core.Manifest, error) {
	return core.Manifest{}, nil
}
func (*assemblyCoreStub) GetLock(context.Context, string) (core.GeneratedProjectLock, error) {
	return core.GeneratedProjectLock{}, nil
}

func TestAssemblyAdminAdapterRejectsUnregisteredOutputBeforeConfirmation(t *testing.T) {
	service := &assemblyCoreStub{plan: core.Plan{PlanID: "plan-1", BlueprintID: "bp-1", Version: 1, Environment: "production", PlanSHA256: assemblyTestDigest}}
	adapter := newAssemblyAdminAdapter(service, assemblyOutputTarget("workspace.default", "production", true))
	_, err := adapter.StartAssembly(context.Background(), assemblyhttp.StartAssemblyCommand{
		BlueprintID: "bp-1", PlanID: "plan-1", ExpectedPlanVersion: 1, PlanChecksum: assemblyTestDigest,
		ConfirmationChecksum: assemblyTestDigest, OutputTargetRef: "workspace.untrusted",
	})
	if !errors.Is(err, assemblyhttp.ErrOutputTargetUnavailable) || service.confirmCalls != 0 || service.startCalls != 0 {
		t.Fatalf("StartAssembly() error=%v confirm=%d start=%d", err, service.confirmCalls, service.startCalls)
	}
}

func TestAssemblyAdminAdapterRejectsOutputTargetEnvironmentBeforeConfirmation(t *testing.T) {
	service := &assemblyCoreStub{plan: core.Plan{PlanID: "plan-1", BlueprintID: "bp-1", Version: 1, Environment: "production", PlanSHA256: assemblyTestDigest}}
	adapter := newAssemblyAdminAdapter(service, assemblyOutputTarget("workspace.default", "test", true))
	_, err := adapter.StartAssembly(context.Background(), assemblyhttp.StartAssemblyCommand{
		BlueprintID: "bp-1", PlanID: "plan-1", ExpectedPlanVersion: 1, PlanChecksum: assemblyTestDigest,
		ConfirmationChecksum: assemblyTestDigest, OutputTargetRef: "workspace.default",
	})
	if !errors.Is(err, assemblyhttp.ErrOutputTargetUnavailable) || service.confirmCalls != 0 || service.startCalls != 0 {
		t.Fatalf("StartAssembly() error=%v confirm=%d start=%d", err, service.confirmCalls, service.startCalls)
	}
}

func TestAssemblyAdminAdapterListsTargetsInStableOrderAndExplicitDefault(t *testing.T) {
	adapter := newAssemblyAdminAdapter(&assemblyCoreStub{},
		assemblyOutputTarget("workspace.zeta", "test", false),
		assemblyOutputTarget("workspace.alpha", "production", true),
		assemblyOutputTarget("workspace.beta", "production", false),
	)
	result, err := adapter.ListOutputTargets(context.Background(), assemblyhttp.ListOutputTargetsCommand{Environment: "production", ActorID: "admin-1"})
	if err != nil || len(result.Items) != 2 || result.Items[0].OutputTargetRef != "workspace.alpha" || result.Items[1].OutputTargetRef != "workspace.beta" || result.DefaultOutputTargetRef == nil || *result.DefaultOutputTargetRef != "workspace.alpha" {
		t.Fatalf("ListOutputTargets() = %#v, %v", result, err)
	}
	withoutDefault, err := adapter.ListOutputTargets(context.Background(), assemblyhttp.ListOutputTargetsCommand{Environment: "test"})
	if err != nil || len(withoutDefault.Items) != 1 || withoutDefault.DefaultOutputTargetRef != nil {
		t.Fatalf("ListOutputTargets(test) = %#v, %v", withoutDefault, err)
	}
}

func TestAssemblyAdminAdapterConfirmsLockedPlanBeforeStarting(t *testing.T) {
	service := &assemblyCoreStub{
		plan:      core.Plan{PlanID: "plan-1", BlueprintID: "bp-1", Version: 1, Environment: "production", PlanSHA256: assemblyTestDigest},
		confirmed: core.Plan{PlanID: "plan-1", BlueprintID: "bp-1", Version: 2, PlanSHA256: assemblyTestDigest},
		run:       core.Run{RunID: "run-1", PlanID: "plan-1", PlanVersion: 2},
	}
	adapter := newAssemblyAdminAdapter(service, assemblyOutputTarget("workspace.default", "production", true))
	run, err := adapter.StartAssembly(context.Background(), assemblyhttp.StartAssemblyCommand{
		BlueprintID: "bp-1", PlanID: "plan-1", ExpectedPlanVersion: 1, PlanChecksum: assemblyTestDigest,
		ConfirmationChecksum: assemblyTestDigest, OutputTargetRef: "workspace.default",
		ActorID: "admin-1", IdempotencyKey: "assembly-idempotency-1", TraceID: "trace-1",
	})
	if err != nil || run.RunID != "run-1" || service.confirmCalls != 1 || service.startCalls != 1 {
		t.Fatalf("StartAssembly() = %#v, %v confirm=%d start=%d", run, err, service.confirmCalls, service.startCalls)
	}
}

func TestAssemblyAdminAdapterRejectsPlanIdentityOrChecksumBeforeConfirmation(t *testing.T) {
	service := &assemblyCoreStub{plan: core.Plan{PlanID: "plan-1", BlueprintID: "bp-other", Version: 1, Environment: "production", PlanSHA256: assemblyTestDigest}}
	adapter := newAssemblyAdminAdapter(service, assemblyOutputTarget("workspace.default", "production", true))
	_, err := adapter.StartAssembly(context.Background(), assemblyhttp.StartAssemblyCommand{
		BlueprintID: "bp-1", PlanID: "plan-1", ExpectedPlanVersion: 1, PlanChecksum: assemblyTestDigest,
		ConfirmationChecksum: assemblyTestDigest, OutputTargetRef: "workspace.default",
	})
	if !errors.Is(err, assemblyhttp.ErrConflict) || service.confirmCalls != 0 {
		t.Fatalf("StartAssembly() error=%v confirm=%d", err, service.confirmCalls)
	}
}

func TestAssemblyAdminAdapterResumesAfterPlanWasAlreadyConfirmed(t *testing.T) {
	now := time.Now().UTC()
	confirmed := core.Plan{PlanID: "plan-1", BlueprintID: "bp-1", Version: 2, Environment: "production", PlanSHA256: assemblyTestDigest, ConfirmedAt: &now}
	service := &assemblyCoreStub{
		plan: confirmed, confirmed: confirmed,
		run: core.Run{RunID: "run-1", PlanID: "plan-1", PlanVersion: 2, OutputTargetRef: "workspace.default"},
	}
	adapter := newAssemblyAdminAdapter(service, assemblyOutputTarget("workspace.default", "production", true))
	run, err := adapter.StartAssembly(context.Background(), assemblyhttp.StartAssemblyCommand{
		BlueprintID: "bp-1", PlanID: "plan-1", ExpectedPlanVersion: 1, PlanChecksum: assemblyTestDigest,
		ConfirmationChecksum: assemblyTestDigest, OutputTargetRef: "workspace.default",
		ActorID: "admin-1", IdempotencyKey: "assembly-idempotency-1", TraceID: "trace-1",
	})
	if err != nil || run.RunID != "run-1" || service.confirmCalls != 0 || service.startCalls != 1 {
		t.Fatalf("StartAssembly() = %#v, %v confirm=%d start=%d", run, err, service.confirmCalls, service.startCalls)
	}
}

func TestAssemblyRunExposesAPIURLsWithoutArtifactPaths(t *testing.T) {
	value := assemblyRun(core.Run{
		RunID: "run-1", PlanID: "plan-1", ManifestID: "assembly-1", LockID: "lock-1",
	})
	if value.ManifestURL != "/api/v1/admin/assembly-manifests/assembly-1" || value.LockURL != "/api/v1/admin/generated-project-locks/lock-1" {
		t.Fatalf("assemblyRun() = %#v", value)
	}
}

func assemblyOutputTarget(reference, environment string, isDefault bool) assemblyhttp.OutputTarget {
	return assemblyhttp.OutputTarget{OutputTargetRef: reference, Environment: environment, DisplayName: reference, Summary: "Server-managed output target", IsDefault: isDefault}
}
