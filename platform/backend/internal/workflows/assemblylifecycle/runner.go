package assemblylifecycle

import (
	"context"
	"errors"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
)

var ErrExecutorUnavailable = errors.New("trusted assembly lifecycle executor is unavailable")

type ExecutionResult struct {
	Target      *core.LifecycleArtifactState
	Transition  *core.LifecycleArtifactTransition
	Diagnostics []core.RunDiagnostic
	Reports     []core.RunReport
}

// Executor is the production trust boundary. Its implementation resolves the
// server-owned workspace and artifact paths and invokes the existing generation
// Executor, eject planner/committer, or RollbackExecutor. Browser values never
// cross this interface.
type Executor interface {
	ExecuteUpgrade(context.Context, core.LifecycleOperation, core.LifecyclePlan, core.LifecycleArtifactTransition) (ExecutionResult, error)
	ExecuteEject(context.Context, core.LifecycleOperation, core.LifecyclePlan, core.LifecycleArtifactTransition) (ExecutionResult, error)
	ExecuteRollback(context.Context, core.LifecycleOperation, core.LifecycleArtifactTransition) (ExecutionResult, error)
}

type Runner struct {
	repository core.LifecycleWorkerRepository
	reader     interface {
		GetLifecycleOperation(context.Context, string) (core.LifecycleOperation, error)
		GetLifecyclePlan(context.Context, string) (core.LifecyclePlan, error)
		GetLifecycleTransition(context.Context, string) (core.LifecycleArtifactTransition, error)
	}
	executor         Executor
	idGenerator      core.IDGenerator
	now              func() time.Time
	lease            time.Duration
	pollInterval     time.Duration
	executionTimeout time.Duration
	workerID         string
	onIterationError func()
}

type RunnerOption func(*Runner)

func WithPollInterval(value time.Duration) RunnerOption {
	return func(r *Runner) {
		if value > 0 {
			r.pollInterval = value
		}
	}
}
func WithExecutionTimeout(value time.Duration) RunnerOption {
	return func(r *Runner) {
		if value > 0 {
			r.executionTimeout = value
		}
	}
}
func WithIterationErrorHandler(handler func()) RunnerOption {
	return func(r *Runner) { r.onIterationError = handler }
}

func NewRunner(repository core.LifecycleWorkerRepository, reader interface {
	GetLifecycleOperation(context.Context, string) (core.LifecycleOperation, error)
	GetLifecyclePlan(context.Context, string) (core.LifecyclePlan, error)
	GetLifecycleTransition(context.Context, string) (core.LifecycleArtifactTransition, error)
}, executor Executor, idGenerator core.IDGenerator, now func() time.Time, workerID string, lease time.Duration, options ...RunnerOption) (*Runner, error) {
	if repository == nil || reader == nil || executor == nil || idGenerator == nil || workerID == "" || lease <= 0 {
		return nil, ErrExecutorUnavailable
	}
	if now == nil {
		now = time.Now
	}
	runner := &Runner{repository: repository, reader: reader, executor: executor, idGenerator: idGenerator, now: now, workerID: workerID, lease: lease, pollInterval: time.Second, executionTimeout: 4 * lease}
	for _, option := range options {
		if option != nil {
			option(runner)
		}
	}
	return runner, nil
}

func (r *Runner) Run(ctx context.Context) error {
	if r == nil {
		return ErrExecutorUnavailable
	}
	for {
		err := r.RunOnce(ctx)
		if err == nil || errors.Is(err, core.ErrNotFound) {
			if err == nil {
				continue
			}
		} else if ctx.Err() != nil {
			return ctx.Err()
		} else if r.onIterationError != nil {
			r.onIterationError()
		}
		timer := time.NewTimer(r.pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
			continue
		}
	}
}

func (r *Runner) RunOnce(ctx context.Context) error {
	if r == nil || r.repository == nil || r.reader == nil || r.executor == nil {
		return ErrExecutorUnavailable
	}
	now := r.now().UTC()
	dispatch, err := r.repository.ClaimLifecycleDispatch(ctx, r.workerID, now, r.lease)
	if err != nil {
		return err
	}
	executionCtx, cancel := context.WithTimeout(ctx, r.executionTimeout)
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	heartbeatError := make(chan error, 1)
	go r.heartbeat(executionCtx, cancel, dispatch.OperationID, stopHeartbeat, heartbeatDone, heartbeatError)
	err = r.runClaimed(executionCtx, dispatch)
	close(stopHeartbeat)
	<-heartbeatDone
	executionErr := executionCtx.Err()
	cancel()
	select {
	case renewErr := <-heartbeatError:
		// Completing the dispatch and renewing its lease can cross in flight.
		// A nil run error proves this worker already committed both the terminal
		// operation and the completed dispatch, so the late renewal conflict is
		// benign. A failed run still treats any heartbeat error as lease loss.
		if err == nil {
			return nil
		}
		return r.finalizeInterrupted(ctx, dispatch.OperationID, "assembly.lifecycle_lease_lost", errors.Join(err, renewErr))
	default:
	}
	if executionErr != nil {
		if errors.Is(executionErr, context.Canceled) && ctx.Err() != nil {
			return r.requeueInterrupted(ctx, dispatch.OperationID, "assembly.lifecycle_worker_shutdown", errors.Join(err, executionErr))
		}
		code := "assembly.lifecycle_execution_cancelled"
		if errors.Is(executionErr, context.DeadlineExceeded) {
			code = "assembly.lifecycle_execution_timeout"
		}
		return r.finalizeInterrupted(ctx, dispatch.OperationID, code, errors.Join(err, executionErr))
	}
	return err
}

func (r *Runner) requeueInterrupted(parent context.Context, operationID, code string, cause error) error {
	bound := r.lease
	if bound > 10*time.Second {
		bound = 10 * time.Second
	}
	if bound < time.Second {
		bound = time.Second
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), bound)
	defer cancel()
	now := r.now().UTC()
	err := r.repository.RequeueLifecycleDispatch(ctx, operationID, r.workerID, code, now, now, false)
	return errors.Join(cause, err)
}

func (r *Runner) runClaimed(ctx context.Context, dispatch core.LifecycleDispatch) error {
	now := r.now().UTC()
	operation, err := r.reader.GetLifecycleOperation(ctx, dispatch.OperationID)
	if err != nil {
		return r.dead(ctx, dispatch.OperationID, "assembly.lifecycle_state_unavailable", now, err)
	}
	if isTerminalLifecycleStatus(operation.Status) {
		return r.repository.CompleteLifecycleDispatch(ctx, operation.OperationID, r.workerID, now)
	}
	if operation.Status != core.LifecyclePlanned && operation.Status != core.LifecycleExecuting && operation.Status != core.LifecycleRollingBack {
		return r.dead(ctx, dispatch.OperationID, "assembly.lifecycle_state_conflict", now, core.ErrConflict)
	}
	executing := operation
	if operation.Status == core.LifecyclePlanned {
		executing, err = core.EvolveLifecycleOperation(operation, core.LifecycleExecuting, "step.prepare", nil, core.LifecycleRecovery{}, nil, nil, r.nextTime(operation.UpdatedAt))
		if err != nil {
			return r.dead(ctx, dispatch.OperationID, "assembly.lifecycle_state_conflict", now, err)
		}
		if _, err = r.repository.UpdateLifecycleOperation(ctx, core.UpdateLifecycleOperationRecord{Operation: executing, ExpectedVersion: operation.Version, Event: r.event(executing, "assembly.lifecycle_started.v1", "assembly.lifecycle.executing", "success", "")}); err != nil {
			return r.retryActive(ctx, dispatch.OperationID, "assembly.lifecycle_update_retry", now, err)
		}
	}
	transition, err := r.reader.GetLifecycleTransition(ctx, operation.OperationID)
	if err != nil {
		return r.fail(ctx, executing, transition, err)
	}
	var result ExecutionResult
	if operation.Kind == core.LifecycleRollback {
		if executing.Status == core.LifecycleExecuting {
			rolling, rollErr := core.EvolveLifecycleOperation(executing, core.LifecycleRollingBack, "step.rollback", nil, core.LifecycleRecovery{}, nil, nil, r.nextTime(executing.UpdatedAt))
			if rollErr != nil {
				return r.fail(ctx, executing, transition, rollErr)
			}
			if _, rollErr = r.repository.UpdateLifecycleOperation(ctx, core.UpdateLifecycleOperationRecord{Operation: rolling, ExpectedVersion: executing.Version}); rollErr != nil {
				return r.retryActive(ctx, operation.OperationID, "assembly.lifecycle_update_retry", now, rollErr)
			}
			executing = rolling
		} else if executing.Status != core.LifecycleRollingBack {
			return r.dead(ctx, operation.OperationID, "assembly.lifecycle_state_conflict", now, core.ErrConflict)
		}
		result, err = r.executor.ExecuteRollback(ctx, executing, transition)
	} else {
		if executing.Status != core.LifecycleExecuting {
			return r.dead(ctx, operation.OperationID, "assembly.lifecycle_state_conflict", now, core.ErrConflict)
		}
		plan, planErr := r.reader.GetLifecyclePlan(ctx, operation.LifecyclePlanID)
		if planErr != nil {
			return r.fail(ctx, executing, transition, planErr)
		}
		if operation.Kind == core.LifecycleUpgrade {
			result, err = r.executor.ExecuteUpgrade(ctx, executing, plan, transition)
		} else if operation.Kind == core.LifecycleEject {
			result, err = r.executor.ExecuteEject(ctx, executing, plan, transition)
		} else {
			err = core.ErrInvalidCommand
		}
	}
	if err != nil {
		if errors.Is(err, ErrLifecycleFinalizeRetryable) {
			return r.retryActive(ctx, operation.OperationID, "assembly.lifecycle_finalize_retry", now, err)
		}
		return r.failWithEvidence(ctx, executing, transition, result, err)
	}
	if result.Target == nil || result.Transition == nil || result.Transition.Target == nil || result.Transition.CompletedAt == nil {
		return r.failWithEvidence(ctx, executing, transition, result, core.ErrDocumentInvalid)
	}
	status, eventType, action := core.LifecycleCompleted, "assembly.lifecycle_completed.v1", "assembly.lifecycle.completed"
	if operation.Kind == core.LifecycleRollback {
		status, eventType, action = core.LifecycleRolledBack, "assembly.lifecycle_rolled_back.v1", "assembly.lifecycle.rolled_back"
	}
	recovery := core.LifecycleRecovery{RollbackAvailable: operation.Kind != core.LifecycleRollback}
	completed, err := core.EvolveLifecycleOperation(executing, status, "", result.Target, recovery, result.Diagnostics, result.Reports, r.nextTime(executing.UpdatedAt))
	if err != nil {
		return r.dead(ctx, operation.OperationID, "assembly.lifecycle_state_conflict", now, err)
	}
	record := core.UpdateLifecycleOperationRecord{Operation: completed, ExpectedVersion: executing.Version, Diagnostics: result.Diagnostics, Reports: result.Reports, Transition: result.Transition, Event: r.event(completed, eventType, action, "success", "")}
	if _, err = r.repository.UpdateLifecycleOperation(ctx, record); err != nil {
		return r.retryActive(ctx, operation.OperationID, "assembly.lifecycle_update_retry", now, err)
	}
	return r.repository.CompleteLifecycleDispatch(ctx, operation.OperationID, r.workerID, r.now().UTC())
}

func (r *Runner) heartbeat(ctx context.Context, cancel context.CancelFunc, operationID string, stop <-chan struct{}, done chan<- struct{}, result chan<- error) {
	defer close(done)
	interval := r.lease / 3
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCtx, renewCancel := context.WithTimeout(ctx, interval)
			err := r.repository.RenewLifecycleDispatch(renewCtx, operationID, r.workerID, r.now().UTC(), r.lease)
			renewCancel()
			if err != nil {
				select {
				case <-stop:
					return
				default:
				}
				select {
				case result <- err:
				default:
				}
				cancel()
				return
			}
		}
	}
}

func (r *Runner) finalizeInterrupted(parent context.Context, operationID, code string, cause error) error {
	bound := r.lease
	if bound > 10*time.Second {
		bound = 10 * time.Second
	}
	if bound < time.Second {
		bound = time.Second
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), bound)
	defer cancel()
	operation, readErr := r.reader.GetLifecycleOperation(ctx, operationID)
	var persistErr error
	if readErr == nil && operation.Status != core.LifecycleCompleted && operation.Status != core.LifecycleFailed && operation.Status != core.LifecycleCancelled && operation.Status != core.LifecycleRolledBack && operation.Status != core.LifecycleRollbackFailed {
		status := core.LifecycleFailed
		if operation.Status == core.LifecycleRollingBack {
			status = core.LifecycleRollbackFailed
		}
		diagnostic := core.RunDiagnostic{DiagnosticID: r.mustID("diagnostic_"), Code: code, Severity: "error", Category: "generation", Message: "Lifecycle execution stopped before the worker lease could be safely maintained", Blocking: true, Retryable: false, Remediation: []string{"Inspect worker health and create a new lifecycle plan"}, RelatedPaths: []string{}, CreatedAt: r.now().UTC()}
		failed, evolveErr := core.EvolveLifecycleOperation(operation, status, "", nil, core.LifecycleRecovery{}, []core.RunDiagnostic{diagnostic}, nil, r.nextTime(operation.UpdatedAt))
		if evolveErr == nil {
			_, persistErr = r.repository.UpdateLifecycleOperation(ctx, core.UpdateLifecycleOperationRecord{Operation: failed, ExpectedVersion: operation.Version, Diagnostics: []core.RunDiagnostic{diagnostic}, Event: r.event(failed, "assembly.lifecycle_failed.v1", "assembly.lifecycle.failed", "failure", code)})
		} else {
			persistErr = evolveErr
		}
	}
	requeueErr := r.repository.RequeueLifecycleDispatch(ctx, operationID, r.workerID, code, r.now().UTC(), r.now().UTC(), true)
	return errors.Join(cause, readErr, persistErr, requeueErr)
}

func (r *Runner) fail(ctx context.Context, current core.LifecycleOperation, transition core.LifecycleArtifactTransition, cause error) error {
	return r.failWithEvidence(ctx, current, transition, ExecutionResult{}, cause)
}
func (r *Runner) failWithEvidence(ctx context.Context, current core.LifecycleOperation, transition core.LifecycleArtifactTransition, result ExecutionResult, cause error) error {
	status := core.LifecycleFailed
	if current.Kind == core.LifecycleRollback && current.Status == core.LifecycleRollingBack {
		status = core.LifecycleRollbackFailed
	}
	diagnostic := core.RunDiagnostic{DiagnosticID: r.mustID("diagnostic_"), Code: "assembly.lifecycle_execution_failed", Severity: "error", Category: "generation", Message: "Lifecycle execution stopped; inspect trusted server diagnostics", Blocking: true, Retryable: false, Remediation: []string{"Review the lifecycle operation evidence and create a new plan"}, RelatedPaths: []string{}, CreatedAt: r.now().UTC()}
	if len(result.Diagnostics) == 0 {
		result.Diagnostics = []core.RunDiagnostic{diagnostic}
	}
	failed, err := core.EvolveLifecycleOperation(current, status, "", nil, core.LifecycleRecovery{}, result.Diagnostics, result.Reports, r.nextTime(current.UpdatedAt))
	if err != nil {
		return r.dead(ctx, current.OperationID, "assembly.lifecycle_state_conflict", r.now().UTC(), errors.Join(cause, err))
	}
	if _, err = r.repository.UpdateLifecycleOperation(ctx, core.UpdateLifecycleOperationRecord{Operation: failed, ExpectedVersion: current.Version, Diagnostics: result.Diagnostics, Reports: result.Reports, Event: r.event(failed, "assembly.lifecycle_failed.v1", "assembly.lifecycle.failed", "failure", "assembly.lifecycle_execution_failed")}); err != nil {
		return r.dead(ctx, current.OperationID, "assembly.lifecycle_update_failed", r.now().UTC(), errors.Join(cause, err))
	}
	if err = r.repository.CompleteLifecycleDispatch(ctx, current.OperationID, r.workerID, r.now().UTC()); err != nil {
		return errors.Join(cause, err)
	}
	// The operation failure is durable and its dispatch is closed. It is a job
	// outcome, not a worker infrastructure failure, so the runner can continue
	// claiming independent lifecycle work.
	return nil
}

func (r *Runner) dead(ctx context.Context, operationID, code string, now time.Time, cause error) error {
	err := r.repository.RequeueLifecycleDispatch(ctx, operationID, r.workerID, code, now, now, true)
	return errors.Join(cause, err)
}
func (r *Runner) retryActive(ctx context.Context, operationID, code string, now time.Time, cause error) error {
	err := r.repository.RequeueLifecycleDispatch(ctx, operationID, r.workerID, code, now, now.Add(r.pollInterval), false)
	if err != nil {
		return errors.Join(cause, err)
	}
	return nil
}
func isTerminalLifecycleStatus(status core.LifecycleStatus) bool {
	switch status {
	case core.LifecycleCompleted, core.LifecycleFailed, core.LifecycleCancelled, core.LifecycleRolledBack, core.LifecycleRollbackFailed:
		return true
	default:
		return false
	}
}
func (r *Runner) nextTime(previous time.Time) time.Time {
	now := r.now().UTC()
	if !now.After(previous) {
		return previous.Add(time.Nanosecond)
	}
	return now
}
func (r *Runner) mustID(prefix string) string {
	value, err := r.idGenerator(prefix)
	if err != nil {
		return prefix + "unavailable"
	}
	return value
}
func (r *Runner) event(operation core.LifecycleOperation, eventType, action, result, reason string) core.OutboxEvent {
	now := r.now().UTC()
	auditID := r.mustID("aud_")
	eventID := r.mustID("evt_")
	payload := core.EventPayload{AuditID: auditID, OccurredAt: now, ActorID: operation.CreatedBy, Permission: "assembly.lifecycle.execute", ScopeType: "product", ScopeID: operation.ProductID, ProductID: operation.ProductID, Action: action, TargetType: "assembly_lifecycle_operation", TargetID: operation.OperationID, Result: result, ReasonCode: reason, TraceID: "worker:" + r.workerID, RiskLevel: "high", RedactedSummary: map[string]any{"kind": operation.Kind, "status": operation.Status}}
	return core.OutboxEvent{EventID: eventID, AggregateID: operation.OperationID, EventType: eventType, Payload: payload, OccurredAt: now}
}
