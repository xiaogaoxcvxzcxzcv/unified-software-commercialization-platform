package assemblylifecycle

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
)

type runnerFixture struct {
	mu                                           sync.Mutex
	operation                                    core.LifecycleOperation
	claim                                        func(context.Context) (core.LifecycleDispatch, error)
	renew                                        func(context.Context) error
	complete                                     func(context.Context) error
	update                                       func(core.UpdateLifecycleOperationRecord) error
	claims, renews, updates, requeues, completes int
}

func (f *runnerFixture) ClaimLifecycleDispatch(ctx context.Context, _ string, _ time.Time, _ time.Duration) (core.LifecycleDispatch, error) {
	f.mu.Lock()
	f.claims++
	fn := f.claim
	f.mu.Unlock()
	return fn(ctx)
}
func (f *runnerFixture) RenewLifecycleDispatch(ctx context.Context, _, _ string, _ time.Time, _ time.Duration) error {
	f.mu.Lock()
	f.renews++
	fn := f.renew
	f.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ctx)
}
func (f *runnerFixture) CompleteLifecycleDispatch(ctx context.Context, _, _ string, _ time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	f.completes++
	fn := f.complete
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return nil
}
func (f *runnerFixture) RequeueLifecycleDispatch(ctx context.Context, _, _, _ string, _, _ time.Time, _ bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	f.requeues++
	f.mu.Unlock()
	return nil
}
func (f *runnerFixture) UpdateLifecycleOperation(ctx context.Context, record core.UpdateLifecycleOperationRecord) (core.LifecycleOperation, error) {
	if err := ctx.Err(); err != nil {
		return core.LifecycleOperation{}, err
	}
	f.mu.Lock()
	f.updates++
	fn := f.update
	if fn != nil {
		if err := fn(record); err != nil {
			f.mu.Unlock()
			return core.LifecycleOperation{}, err
		}
	}
	f.operation = record.Operation
	f.mu.Unlock()
	return record.Operation, nil
}
func (f *runnerFixture) GetLifecycleOperation(ctx context.Context, _ string) (core.LifecycleOperation, error) {
	if err := ctx.Err(); err != nil {
		return core.LifecycleOperation{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.operation, nil
}
func (f *runnerFixture) GetLifecyclePlan(context.Context, string) (core.LifecyclePlan, error) {
	return core.LifecyclePlan{LifecyclePlanID: "lifecycle.test", Operation: core.LifecycleUpgrade}, nil
}
func (f *runnerFixture) GetLifecycleTransition(context.Context, string) (core.LifecycleArtifactTransition, error) {
	return core.LifecycleArtifactTransition{OperationID: "operation.test"}, nil
}

type blockingExecutor struct{ cancelled chan struct{} }

type successfulExecutor struct{ renewStarted <-chan struct{} }

type failingExecutor struct{ err error }

type postCommitCancellationExecutor struct{}

func (e *failingExecutor) ExecuteUpgrade(context.Context, core.LifecycleOperation, core.LifecyclePlan, core.LifecycleArtifactTransition) (ExecutionResult, error) {
	return ExecutionResult{}, e.err
}
func (e *failingExecutor) ExecuteEject(context.Context, core.LifecycleOperation, core.LifecyclePlan, core.LifecycleArtifactTransition) (ExecutionResult, error) {
	return ExecutionResult{}, e.err
}
func (e *failingExecutor) ExecuteRollback(context.Context, core.LifecycleOperation, core.LifecycleArtifactTransition) (ExecutionResult, error) {
	return ExecutionResult{}, e.err
}

func postCommitResult(ctx context.Context, operation core.LifecycleOperation) (ExecutionResult, error) {
	<-ctx.Done()
	completedAt := operation.UpdatedAt.Add(time.Second)
	target := operation.Source
	target.ManifestID = "assembly.post-commit"
	target.LockID = "lock.post-commit"
	return ExecutionResult{Target: &target, Transition: &core.LifecycleArtifactTransition{OperationID: operation.OperationID, Source: operation.Source, Target: &target, TargetManifestDocument: []byte(`{}`), TargetLockDocument: []byte(`{}`), RollbackJournal: []byte(`{}`), CompletedAt: &completedAt}}, nil
}

func (e *postCommitCancellationExecutor) ExecuteUpgrade(ctx context.Context, operation core.LifecycleOperation, _ core.LifecyclePlan, _ core.LifecycleArtifactTransition) (ExecutionResult, error) {
	return postCommitResult(ctx, operation)
}
func (e *postCommitCancellationExecutor) ExecuteEject(ctx context.Context, operation core.LifecycleOperation, _ core.LifecyclePlan, _ core.LifecycleArtifactTransition) (ExecutionResult, error) {
	return postCommitResult(ctx, operation)
}
func (e *postCommitCancellationExecutor) ExecuteRollback(ctx context.Context, operation core.LifecycleOperation, _ core.LifecycleArtifactTransition) (ExecutionResult, error) {
	return postCommitResult(ctx, operation)
}

func (e *successfulExecutor) result(ctx context.Context, operation core.LifecycleOperation) (ExecutionResult, error) {
	select {
	case <-ctx.Done():
		return ExecutionResult{}, ctx.Err()
	case <-e.renewStarted:
	}
	completedAt := operation.UpdatedAt.Add(time.Second)
	target := core.LifecycleArtifactState{
		ManifestID: "assembly.successor", ManifestChecksum: operation.Source.ManifestChecksum,
		LockID: "lock.successor", LockChecksum: operation.Source.LockChecksum,
		CatalogChecksum: operation.Source.CatalogChecksum, TargetSnapshotChecksum: operation.Source.TargetSnapshotChecksum,
	}
	return ExecutionResult{Target: &target, Transition: &core.LifecycleArtifactTransition{OperationID: operation.OperationID, Source: operation.Source, Target: &target, TargetManifestDocument: []byte(`{}`), TargetLockDocument: []byte(`{}`), RollbackJournal: []byte(`{}`), CompletedAt: &completedAt}}, nil
}
func (e *successfulExecutor) ExecuteUpgrade(ctx context.Context, operation core.LifecycleOperation, _ core.LifecyclePlan, _ core.LifecycleArtifactTransition) (ExecutionResult, error) {
	return e.result(ctx, operation)
}
func (e *successfulExecutor) ExecuteEject(ctx context.Context, operation core.LifecycleOperation, _ core.LifecyclePlan, _ core.LifecycleArtifactTransition) (ExecutionResult, error) {
	return e.result(ctx, operation)
}
func (e *successfulExecutor) ExecuteRollback(ctx context.Context, operation core.LifecycleOperation, _ core.LifecycleArtifactTransition) (ExecutionResult, error) {
	return e.result(ctx, operation)
}

func (e *blockingExecutor) wait(ctx context.Context) (ExecutionResult, error) {
	<-ctx.Done()
	select {
	case <-e.cancelled:
	default:
		close(e.cancelled)
	}
	return ExecutionResult{}, ctx.Err()
}
func (e *blockingExecutor) ExecuteUpgrade(ctx context.Context, _ core.LifecycleOperation, _ core.LifecyclePlan, _ core.LifecycleArtifactTransition) (ExecutionResult, error) {
	return e.wait(ctx)
}
func (e *blockingExecutor) ExecuteEject(ctx context.Context, _ core.LifecycleOperation, _ core.LifecyclePlan, _ core.LifecycleArtifactTransition) (ExecutionResult, error) {
	return e.wait(ctx)
}
func (e *blockingExecutor) ExecuteRollback(ctx context.Context, _ core.LifecycleOperation, _ core.LifecycleArtifactTransition) (ExecutionResult, error) {
	return e.wait(ctx)
}

func lifecycleOperationFixture() core.LifecycleOperation {
	now := time.Now().UTC().Add(-time.Second)
	return core.LifecycleOperation{OperationID: "operation.test", RootOperationID: "operation.test", LifecyclePlanID: "lifecycle.test", AssemblyID: "assembly.test", ProductID: "product.test", Kind: core.LifecycleUpgrade, Version: 1, Status: core.LifecyclePlanned, Source: core.LifecycleArtifactState{ManifestID: "assembly.test", ManifestChecksum: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LockID: "lock.test", LockChecksum: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", CatalogChecksum: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TargetSnapshotChecksum: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, Recovery: core.LifecycleRecovery{CancelAllowed: true}, CreatedBy: "admin.test", CreatedAt: now, UpdatedAt: now}
}
func testID(prefix string) (string, error) { return prefix + "test", nil }

func TestNewRunnerFailsClosedWithoutTrustedExecutor(t *testing.T) {
	runner, err := NewRunner(nil, nil, nil, nil, time.Now, "worker.test", time.Minute)
	if err != ErrExecutorUnavailable || runner != nil {
		t.Fatalf("runner=%v err=%v", runner, err)
	}
}

func TestRunnerPollsWhenQueueIsEmpty(t *testing.T) {
	fixture := &runnerFixture{claim: func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{}, core.ErrNotFound
	}}
	executor := &blockingExecutor{cancelled: make(chan struct{})}
	runner, err := NewRunner(fixture, fixture, executor, testID, time.Now, "worker.test", 30*time.Millisecond, WithPollInterval(5*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Millisecond)
	defer cancel()
	if err = runner.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected error: %v", err)
	}
	fixture.mu.Lock()
	claims := fixture.claims
	fixture.mu.Unlock()
	if claims < 3 {
		t.Fatalf("expected repeated polling, claims=%d", claims)
	}
}

func TestRunnerHeartbeatFailureCancelsExecutorAndRequeues(t *testing.T) {
	fixture := &runnerFixture{operation: lifecycleOperationFixture()}
	fixture.claim = func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{OperationID: "operation.test"}, nil
	}
	fixture.renew = func(context.Context) error {
		fixture.mu.Lock()
		count := fixture.renews
		fixture.mu.Unlock()
		if count >= 2 {
			return errors.New("lease lost")
		}
		return nil
	}
	executor := &blockingExecutor{cancelled: make(chan struct{})}
	runner, err := NewRunner(fixture, fixture, executor, testID, time.Now, "worker.test", 30*time.Millisecond, WithExecutionTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.RunOnce(context.Background()); err == nil {
		t.Fatal("expected lease loss")
	}
	select {
	case <-executor.cancelled:
	default:
		t.Fatal("executor context was not cancelled")
	}
	fixture.mu.Lock()
	renews, requeues, updates, status := fixture.renews, fixture.requeues, fixture.updates, fixture.operation.Status
	fixture.mu.Unlock()
	if renews < 2 || requeues != 1 || updates != 1 || status != core.LifecycleExecuting {
		t.Fatalf("renews=%d requeues=%d updates=%d status=%s", renews, requeues, updates, status)
	}
}

func TestRunnerLeaseLossAfterFilesCommitPreservesActiveOperationForRecovery(t *testing.T) {
	fixture := &runnerFixture{operation: lifecycleOperationFixture()}
	fixture.claim = func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{OperationID: "operation.test"}, nil
	}
	fixture.renew = func(context.Context) error { return errors.New("lease lost after product commit") }
	runner, err := NewRunner(fixture, fixture, &postCommitCancellationExecutor{}, testID, time.Now, "worker.test", 9*time.Millisecond, WithExecutionTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.RunOnce(context.Background()); err == nil {
		t.Fatal("expected lease-loss infrastructure error")
	}
	fixture.mu.Lock()
	status, updates, requeues, completes := fixture.operation.Status, fixture.updates, fixture.requeues, fixture.completes
	fixture.mu.Unlock()
	if status != core.LifecycleExecuting || updates != 1 || requeues != 1 || completes != 0 {
		t.Fatalf("status=%s updates=%d requeues=%d completes=%d", status, updates, requeues, completes)
	}
}

func TestRunnerTimeoutAfterFilesCommitPreservesActiveOperationForRecovery(t *testing.T) {
	fixture := &runnerFixture{operation: lifecycleOperationFixture(), claim: func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{OperationID: "operation.test"}, nil
	}}
	runner, err := NewRunner(fixture, fixture, &postCommitCancellationExecutor{}, testID, time.Now, "worker.test", time.Second, WithExecutionTimeout(5*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.RunOnce(context.Background()); err == nil {
		t.Fatal("expected timeout infrastructure error")
	}
	fixture.mu.Lock()
	status, updates, requeues := fixture.operation.Status, fixture.updates, fixture.requeues
	fixture.mu.Unlock()
	if status != core.LifecycleExecuting || updates != 1 || requeues != 1 {
		t.Fatalf("status=%s updates=%d requeues=%d", status, updates, requeues)
	}
}

func TestRunnerIgnoresRenewalConflictAfterDispatchCompleted(t *testing.T) {
	renewStarted := make(chan struct{})
	dispatchCompleted := make(chan struct{})
	fixture := &runnerFixture{operation: lifecycleOperationFixture()}
	fixture.claim = func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{OperationID: "operation.test"}, nil
	}
	var renewOnce sync.Once
	fixture.renew = func(context.Context) error {
		renewOnce.Do(func() { close(renewStarted) })
		<-dispatchCompleted
		return core.ErrConflict
	}
	var completeOnce sync.Once
	fixture.complete = func(context.Context) error {
		completeOnce.Do(func() { close(dispatchCompleted) })
		return nil
	}
	runner, err := NewRunner(fixture, fixture, &successfulExecutor{renewStarted: renewStarted}, testID, time.Now, "worker.test", 15*time.Millisecond, WithExecutionTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("completed dispatch was treated as lease loss: %v", err)
	}
	fixture.mu.Lock()
	renews, requeues, completes := fixture.renews, fixture.requeues, fixture.completes
	fixture.mu.Unlock()
	if renews != 1 || requeues != 0 || completes != 1 {
		t.Fatalf("renews=%d requeues=%d completes=%d", renews, requeues, completes)
	}
}

func TestRunnerSuccessfulDispatchDoesNotCancelItself(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	fixture := &runnerFixture{operation: lifecycleOperationFixture()}
	fixture.claim = func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{OperationID: "operation.test"}, nil
	}
	runner, err := NewRunner(fixture, fixture, &successfulExecutor{renewStarted: ready}, testID, time.Now, "worker.test", time.Second, WithExecutionTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("successful dispatch returned an error: %v", err)
	}
	fixture.mu.Lock()
	status, completes, requeues := fixture.operation.Status, fixture.completes, fixture.requeues
	fixture.mu.Unlock()
	if status != core.LifecycleCompleted || completes != 1 || requeues != 0 {
		t.Fatalf("status=%s completes=%d requeues=%d", status, completes, requeues)
	}
}

func TestRunnerContinuesAfterDurablyRecordedOperationFailure(t *testing.T) {
	fixture := &runnerFixture{operation: lifecycleOperationFixture()}
	fixture.claim = func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{OperationID: "operation.test"}, nil
	}
	runner, err := NewRunner(fixture, fixture, &failingExecutor{err: errors.New("generator rejected input")}, testID, time.Now, "worker.test", time.Second, WithExecutionTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("durably recorded job failure stopped the runner: %v", err)
	}
	fixture.mu.Lock()
	status, completes, requeues := fixture.operation.Status, fixture.completes, fixture.requeues
	fixture.mu.Unlock()
	if status != core.LifecycleFailed || completes != 1 || requeues != 0 {
		t.Fatalf("status=%s completes=%d requeues=%d", status, completes, requeues)
	}
}

func TestRunnerResumesExecutingOperationAfterLeaseReclaim(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	operation := lifecycleOperationFixture()
	operation.Status = core.LifecycleExecuting
	operation.Version = 2
	operation.CurrentStep = "step.prepare"
	fixture := &runnerFixture{operation: operation, claim: func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{OperationID: operation.OperationID}, nil
	}}
	runner, err := NewRunner(fixture, fixture, &successfulExecutor{renewStarted: ready}, testID, time.Now, "worker.test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("resume error: %v", err)
	}
	fixture.mu.Lock()
	status, updates, completes := fixture.operation.Status, fixture.updates, fixture.completes
	fixture.mu.Unlock()
	if status != core.LifecycleCompleted || updates != 1 || completes != 1 {
		t.Fatalf("status=%s updates=%d completes=%d", status, updates, completes)
	}
}

func TestRunnerResumesRollingBackOperationAfterLeaseReclaim(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	operation := lifecycleOperationFixture()
	operation.Kind = core.LifecycleRollback
	operation.LifecyclePlanID = ""
	operation.RollbackOfOperationID = "operation.predecessor"
	operation.Status = core.LifecycleRollingBack
	operation.Version = 3
	operation.CurrentStep = "step.rollback"
	fixture := &runnerFixture{operation: operation, claim: func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{OperationID: operation.OperationID}, nil
	}}
	runner, err := NewRunner(fixture, fixture, &successfulExecutor{renewStarted: ready}, testID, time.Now, "worker.test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("resume rollback error: %v", err)
	}
	fixture.mu.Lock()
	status, updates, completes := fixture.operation.Status, fixture.updates, fixture.completes
	fixture.mu.Unlock()
	if status != core.LifecycleRolledBack || updates != 1 || completes != 1 {
		t.Fatalf("status=%s updates=%d completes=%d", status, updates, completes)
	}
}

func TestRunnerCompletesDispatchStrandedAfterTerminalCommit(t *testing.T) {
	operation := lifecycleOperationFixture()
	operation.Status = core.LifecycleCompleted
	operation.Version = 3
	completedAt := operation.UpdatedAt.Add(time.Second)
	operation.CompletedAt = &completedAt
	fixture := &runnerFixture{operation: operation, claim: func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{OperationID: operation.OperationID}, nil
	}}
	runner, err := NewRunner(fixture, fixture, &failingExecutor{err: errors.New("must not execute")}, testID, time.Now, "worker.test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("terminal dispatch recovery error: %v", err)
	}
	fixture.mu.Lock()
	updates, completes := fixture.updates, fixture.completes
	fixture.mu.Unlock()
	if updates != 0 || completes != 1 {
		t.Fatalf("updates=%d completes=%d", updates, completes)
	}
}

func TestRunnerRetriesInfrastructureFailureWithoutStoppingService(t *testing.T) {
	transient := errors.New("temporary database failure")
	fixture := &runnerFixture{}
	fixture.claim = func(context.Context) (core.LifecycleDispatch, error) {
		fixture.mu.Lock()
		claims := fixture.claims
		fixture.mu.Unlock()
		if claims == 1 {
			return core.LifecycleDispatch{}, transient
		}
		return core.LifecycleDispatch{}, core.ErrNotFound
	}
	runner, err := NewRunner(fixture, fixture, &failingExecutor{err: transient}, testID, time.Now, "worker.test", time.Second, WithPollInterval(5*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Millisecond)
	defer cancel()
	if err = runner.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runner stopped on transient error: %v", err)
	}
	fixture.mu.Lock()
	claims := fixture.claims
	fixture.mu.Unlock()
	if claims < 3 {
		t.Fatalf("runner did not retry after transient error: claims=%d", claims)
	}
}

func TestRunnerRequeuesActiveOperationWhenArtifactFinalizationIsRetryable(t *testing.T) {
	fixture := &runnerFixture{operation: lifecycleOperationFixture(), claim: func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{OperationID: "operation.test"}, nil
	}}
	runner, err := NewRunner(fixture, fixture, &failingExecutor{err: ErrLifecycleFinalizeRetryable}, testID, time.Now, "worker.test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("retryable finalization error escaped runner: %v", err)
	}
	fixture.mu.Lock()
	status, requeues, completes := fixture.operation.Status, fixture.requeues, fixture.completes
	fixture.mu.Unlock()
	if status != core.LifecycleExecuting || requeues != 1 || completes != 0 {
		t.Fatalf("status=%s requeues=%d completes=%d", status, requeues, completes)
	}
}

func TestRunnerRequeuesActiveOperationWhenTerminalDatabaseCommitFails(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	transient := errors.New("terminal transaction unavailable")
	fixture := &runnerFixture{operation: lifecycleOperationFixture(), claim: func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{OperationID: "operation.test"}, nil
	}}
	fixture.update = func(record core.UpdateLifecycleOperationRecord) error {
		if record.Operation.Status == core.LifecycleCompleted {
			return transient
		}
		return nil
	}
	runner, err := NewRunner(fixture, fixture, &successfulExecutor{renewStarted: ready}, testID, time.Now, "worker.test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("terminal commit failure escaped runner: %v", err)
	}
	fixture.mu.Lock()
	status, requeues, completes := fixture.operation.Status, fixture.requeues, fixture.completes
	fixture.mu.Unlock()
	if status != core.LifecycleExecuting || requeues != 1 || completes != 0 {
		t.Fatalf("status=%s requeues=%d completes=%d", status, requeues, completes)
	}
}

func TestRunnerReclaimsOperationAfterPostFilesDatabaseFailure(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	transient := errors.New("terminal transaction unavailable")
	fixture := &runnerFixture{operation: lifecycleOperationFixture(), claim: func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{OperationID: "operation.test"}, nil
	}}
	failedOnce := false
	fixture.update = func(record core.UpdateLifecycleOperationRecord) error {
		if record.Operation.Status == core.LifecycleCompleted && !failedOnce {
			failedOnce = true
			return transient
		}
		return nil
	}
	runner, err := NewRunner(fixture, fixture, &successfulExecutor{renewStarted: ready}, testID, time.Now, "worker.test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("first finalization failure escaped: %v", err)
	}
	fixture.mu.Lock()
	firstStatus, firstRequeues := fixture.operation.Status, fixture.requeues
	fixture.mu.Unlock()
	if firstStatus != core.LifecycleExecuting || firstRequeues != 1 {
		t.Fatalf("after failure status=%s requeues=%d", firstStatus, firstRequeues)
	}
	if err = runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("reclaim finalization: %v", err)
	}
	fixture.mu.Lock()
	status, completes := fixture.operation.Status, fixture.completes
	fixture.mu.Unlock()
	if status != core.LifecycleCompleted || completes != 1 {
		t.Fatalf("after reclaim status=%s completes=%d", status, completes)
	}
}

func TestRunnerParentCancellationUsesDetachedFinalizer(t *testing.T) {
	fixture := &runnerFixture{operation: lifecycleOperationFixture()}
	fixture.claim = func(context.Context) (core.LifecycleDispatch, error) {
		return core.LifecycleDispatch{OperationID: "operation.test"}, nil
	}
	executor := &blockingExecutor{cancelled: make(chan struct{})}
	runner, err := NewRunner(fixture, fixture, executor, testID, time.Now, "worker.test", time.Second, WithExecutionTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.RunOnce(ctx) }()
	time.Sleep(15 * time.Millisecond)
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected error: %v", err)
	}
	fixture.mu.Lock()
	requeues, status := fixture.requeues, fixture.operation.Status
	fixture.mu.Unlock()
	if requeues < 1 || status != core.LifecycleExecuting {
		t.Fatalf("cancelled execution was not preserved for restart: requeues=%d status=%s", requeues, status)
	}
}
