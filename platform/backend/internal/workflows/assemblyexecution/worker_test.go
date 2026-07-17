package assemblyexecution

import (
	"context"
	"errors"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
)

type dispatchRepositoryStub struct {
	dispatch    core.Dispatch
	complete    int
	requeue     int
	dead        bool
	requeuedAt  time.Time
	availableAt time.Time
}

func (s *dispatchRepositoryStub) ClaimDispatch(context.Context, string, time.Time, time.Duration) (core.Dispatch, error) {
	return s.dispatch, nil
}
func (s *dispatchRepositoryStub) RenewDispatch(context.Context, string, string, time.Time, time.Duration) error {
	return nil
}
func (s *dispatchRepositoryStub) CompleteDispatch(context.Context, string, string, time.Time) error {
	s.complete++
	return nil
}
func (s *dispatchRepositoryStub) RequeueDispatch(_ context.Context, _, _, _ string, now, available time.Time, dead bool) error {
	s.requeue++
	s.dead = dead
	s.requeuedAt = now
	s.availableAt = available
	return nil
}

type executorStub struct {
	run core.Run
	err error
}

func (s executorStub) Execute(context.Context, Command) (core.Run, error) { return s.run, s.err }

type readerStub struct {
	run core.Run
	err error
}

func (s readerStub) GetRun(context.Context, string) (core.Run, error) { return s.run, s.err }

func TestWorkerCompletesDispatchWhenExecutionPersistedFailure(t *testing.T) {
	repo := &dispatchRepositoryStub{dispatch: core.Dispatch{RunID: "run_failed", RootRunID: "run_root", CreatedBy: "admin", AttemptCount: 1}}
	failed := core.Run{RunID: "run_failed", Status: core.RunStatusFailed}
	worker := NewWorker(repo, executorStub{run: failed, err: errors.New("business failure")}, readerStub{run: failed}, "worker-1", func() time.Time { return time.Unix(100, 0).UTC() })
	if !worker.runOne(context.Background()) {
		t.Fatal("runOne did not claim work")
	}
	if repo.complete != 1 || repo.requeue != 0 {
		t.Fatalf("complete=%d requeue=%d", repo.complete, repo.requeue)
	}
}

func TestWorkerTreatsCancelledRunAsTerminal(t *testing.T) {
	repo := &dispatchRepositoryStub{dispatch: core.Dispatch{RunID: "run_cancelled", RootRunID: "run_root", CreatedBy: "admin", AttemptCount: 1}}
	cancelled := core.Run{RunID: "run_cancelled", Status: core.RunStatusCancelled}
	worker := NewWorker(repo, executorStub{run: cancelled, err: core.ErrConflict}, readerStub{run: cancelled}, "worker-1", func() time.Time { return time.Unix(100, 0).UTC() })
	if !worker.runOne(context.Background()) {
		t.Fatal("runOne did not claim work")
	}
	if repo.complete != 1 || repo.requeue != 0 {
		t.Fatalf("complete=%d requeue=%d", repo.complete, repo.requeue)
	}
}

func TestWorkerRequeuesOnlyNonTerminalInfrastructureFailure(t *testing.T) {
	repo := &dispatchRepositoryStub{dispatch: core.Dispatch{RunID: "run_pending", RootRunID: "run_root", CreatedBy: "admin", AttemptCount: 2}}
	pending := core.Run{RunID: "run_pending", Status: core.RunStatusPlanned}
	worker := NewWorker(repo, executorStub{err: errors.New("database unavailable")}, readerStub{run: pending}, "worker-1", func() time.Time { return time.Unix(100, 0).UTC() })
	if !worker.runOne(context.Background()) {
		t.Fatal("runOne did not claim work")
	}
	if repo.complete != 0 || repo.requeue != 1 || repo.dead {
		t.Fatalf("complete=%d requeue=%d dead=%t", repo.complete, repo.requeue, repo.dead)
	}
}

func TestWorkerCapsBackoffWithoutStrandingNonTerminalRun(t *testing.T) {
	repo := &dispatchRepositoryStub{dispatch: core.Dispatch{RunID: "run_pending", RootRunID: "run_root", CreatedBy: "admin", AttemptCount: 100}}
	pending := core.Run{RunID: "run_pending", Status: core.RunStatusPlanned}
	worker := NewWorker(repo, executorStub{err: errors.New("database unavailable")}, readerStub{run: pending}, "worker-1", func() time.Time { return time.Unix(100, 0).UTC() })
	worker.runOne(context.Background())
	if repo.requeue != 1 || repo.dead || repo.availableAt.Sub(repo.requeuedAt) != 64*time.Second {
		t.Fatalf("requeue=%d dead=%t backoff=%s", repo.requeue, repo.dead, repo.availableAt.Sub(repo.requeuedAt))
	}
}
