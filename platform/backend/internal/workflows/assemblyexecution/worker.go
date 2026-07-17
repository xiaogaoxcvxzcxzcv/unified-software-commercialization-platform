package assemblyexecution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
)

type DispatchRepository interface {
	ClaimDispatch(context.Context, string, time.Time, time.Duration) (core.Dispatch, error)
	RenewDispatch(context.Context, string, string, time.Time, time.Duration) error
	CompleteDispatch(context.Context, string, string, time.Time) error
	RequeueDispatch(context.Context, string, string, string, time.Time, time.Time, bool) error
}

type RunExecutor interface {
	Execute(context.Context, Command) (core.Run, error)
}
type RunReader interface {
	GetRun(context.Context, string) (core.Run, error)
}

type Worker struct {
	repository       DispatchRepository
	executor         RunExecutor
	runs             RunReader
	workerID         string
	now              func() time.Time
	lease            time.Duration
	executionTimeout time.Duration
	poll             time.Duration
}

func NewWorker(repository DispatchRepository, executor RunExecutor, runs RunReader, workerID string, now func() time.Time) *Worker {
	if now == nil {
		now = time.Now
	}
	return &Worker{repository: repository, executor: executor, runs: runs, workerID: workerID, now: now, lease: time.Minute, executionTimeout: 15 * time.Minute, poll: 500 * time.Millisecond}
}

func (w *Worker) Run(ctx context.Context) {
	if w == nil || w.repository == nil || w.executor == nil || w.runs == nil || w.workerID == "" {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		worked := w.runOne(ctx)
		delay := time.Duration(0)
		if !worked {
			delay = w.poll
		}
		timer.Reset(delay)
	}
}

func (w *Worker) runOne(ctx context.Context) bool {
	now := w.now().UTC()
	dispatch, err := w.repository.ClaimDispatch(ctx, w.workerID, now, w.lease)
	if errors.Is(err, core.ErrNotFound) {
		return false
	}
	if err != nil {
		return false
	}
	executionCtx, cancel := context.WithTimeout(ctx, w.executionTimeout)
	defer cancel()
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(w.lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-executionCtx.Done():
				return
			case <-ticker.C:
				if w.repository.RenewDispatch(executionCtx, dispatch.RunID, w.workerID, w.now().UTC(), w.lease) != nil {
					cancel()
					return
				}
			}
		}
	}()
	run, executeErr := w.executor.Execute(executionCtx, Command{RunID: dispatch.RunID, ActorID: dispatch.CreatedBy, IdempotencyKey: executionKey(dispatch.RootRunID), TraceID: fmt.Sprintf("dispatch:%s:%d", dispatch.RunID, dispatch.AttemptCount)})
	cancel()
	<-heartbeatDone
	if run.RunID == "" || !terminalRun(run.Status) {
		readCtx, readCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		current, readErr := w.runs.GetRun(readCtx, dispatch.RunID)
		readCancel()
		if readErr == nil {
			run = current
		}
	}
	finishedAt := w.now().UTC()
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	if terminalRun(run.Status) {
		_ = w.repository.CompleteDispatch(finishCtx, dispatch.RunID, w.workerID, finishedAt)
		return true
	}
	delay := time.Duration(1<<min(dispatch.AttemptCount-1, 6)) * time.Second
	errorCode := "assembly.dispatch_execution_failed"
	if executeErr == nil {
		errorCode = "assembly.dispatch_incomplete"
	}
	_ = w.repository.RequeueDispatch(finishCtx, dispatch.RunID, w.workerID, errorCode, finishedAt, finishedAt.Add(delay), false)
	return true
}

func terminalRun(status core.RunStatus) bool {
	return status == core.RunStatusCompleted || status == core.RunStatusFailed || status == core.RunStatusCancelled || status == core.RunStatusRolledBack
}
