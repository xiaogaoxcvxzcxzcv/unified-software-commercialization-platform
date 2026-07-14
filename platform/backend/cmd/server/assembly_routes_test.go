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
	service := &assemblyCoreStub{plan: core.Plan{PlanID: "plan-1", BlueprintID: "bp-1", Version: 1, PlanSHA256: assemblyTestDigest}}
	adapter := newAssemblyAdminAdapter(service, "workspace.default")
	_, err := adapter.StartAssembly(context.Background(), assemblyhttp.StartAssemblyCommand{
		BlueprintID: "bp-1", PlanID: "plan-1", ExpectedPlanVersion: 1, PlanChecksum: assemblyTestDigest,
		ConfirmationChecksum: assemblyTestDigest, OutputTargetRef: "workspace.untrusted",
	})
	if !errors.Is(err, assemblyhttp.ErrPlanUnavailable) || service.confirmCalls != 0 || service.startCalls != 0 {
		t.Fatalf("StartAssembly() error=%v confirm=%d start=%d", err, service.confirmCalls, service.startCalls)
	}
}

func TestAssemblyAdminAdapterConfirmsLockedPlanBeforeStarting(t *testing.T) {
	service := &assemblyCoreStub{
		plan:      core.Plan{PlanID: "plan-1", BlueprintID: "bp-1", Version: 1, PlanSHA256: assemblyTestDigest},
		confirmed: core.Plan{PlanID: "plan-1", BlueprintID: "bp-1", Version: 2, PlanSHA256: assemblyTestDigest},
		run:       core.Run{RunID: "run-1", PlanID: "plan-1", PlanVersion: 2},
	}
	adapter := newAssemblyAdminAdapter(service, "workspace.default")
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
	service := &assemblyCoreStub{plan: core.Plan{PlanID: "plan-1", BlueprintID: "bp-other", Version: 1, PlanSHA256: assemblyTestDigest}}
	adapter := newAssemblyAdminAdapter(service, "workspace.default")
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
	confirmed := core.Plan{PlanID: "plan-1", BlueprintID: "bp-1", Version: 2, PlanSHA256: assemblyTestDigest, ConfirmedAt: &now}
	service := &assemblyCoreStub{
		plan: confirmed, confirmed: confirmed,
		run: core.Run{RunID: "run-1", PlanID: "plan-1", PlanVersion: 2, OutputTargetRef: "workspace.default"},
	}
	adapter := newAssemblyAdminAdapter(service, "workspace.default")
	run, err := adapter.StartAssembly(context.Background(), assemblyhttp.StartAssemblyCommand{
		BlueprintID: "bp-1", PlanID: "plan-1", ExpectedPlanVersion: 1, PlanChecksum: assemblyTestDigest,
		ConfirmationChecksum: assemblyTestDigest, OutputTargetRef: "workspace.default",
		ActorID: "admin-1", IdempotencyKey: "assembly-idempotency-1", TraceID: "trace-1",
	})
	if err != nil || run.RunID != "run-1" || service.confirmCalls != 0 || service.startCalls != 1 {
		t.Fatalf("StartAssembly() = %#v, %v confirm=%d start=%d", run, err, service.confirmCalls, service.startCalls)
	}
}
