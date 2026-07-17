package generation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExecutorRecoversSuccessStageBeforeJournalMark(t *testing.T) {
	fixture := prepareInterruptedSuccess(t)

	outcome, err := fixture.executor.Execute(context.Background(), fixture.targetRoot, fixture.input, ProjectLock{}, PreviousArtifacts{})
	if err != nil {
		t.Fatalf("Execute recovery error = %v", err)
	}
	if !outcome.Published || !outcome.Commit.AtomicCommitCompleted || !outcome.Commit.TargetUnchanged {
		t.Fatalf("recovery outcome = %#v", outcome)
	}
	if _, found, err := fixture.store.LoadPublished(fixture.input.Request); err != nil || !found {
		t.Fatalf("LoadPublished after recovery = found %v, err %v", found, err)
	}
}

func TestExecutorRecoversSuccessStageAfterJournalMark(t *testing.T) {
	fixture := prepareInterruptedSuccess(t)
	if err := fixture.transaction.MarkCommitted(); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.executor.Execute(context.Background(), fixture.targetRoot, fixture.input, ProjectLock{}, PreviousArtifacts{}); err != nil {
		t.Fatalf("Execute marked recovery error = %v", err)
	}
}

func TestExecutorRecoverUsesTrustedSourceChecksumWithCurrentWorkspaceRequest(t *testing.T) {
	fixture := prepareInterruptedSuccess(t)
	expectedSource := fixture.input.Request.TargetSnapshotChecksum
	var lock generatedLockDocument
	lockPath, err := fixture.transaction.stagePathForFinal(fixture.input.Request.ArtifactContext.Paths.GeneratedLockPath)
	if err != nil {
		t.Fatal(err)
	}
	lockRaw, err := os.ReadFile(lockPath)
	if err != nil || jsonUnmarshalStrict(lockRaw, &lock) != nil {
		t.Fatalf("read staged lock: %v", err)
	}
	fixture.input.Request.TargetSnapshotChecksum = lock.TargetSnapshotChecksum

	outcome, found, err := fixture.executor.Recover(context.Background(), fixture.targetRoot, fixture.input, expectedSource)
	if err != nil || !found || !outcome.Published {
		t.Fatalf("Recover = found %v, outcome %#v, err %v", found, outcome, err)
	}
}

func TestExecutorRecoverRejectsWrongTrustedSourceChecksum(t *testing.T) {
	fixture := prepareInterruptedSuccess(t)

	_, found, err := fixture.executor.Recover(context.Background(), fixture.targetRoot, fixture.input, digestBytes([]byte("wrong source")))
	if !found || !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("Recover wrong source = found %v, err %v", found, err)
	}
}

func TestExecutorRecoverNeverRendersWhenNoEvidenceExists(t *testing.T) {
	targetRoot, artifactRoot := separateTestRoots(t)
	output := artifactTestOutput("src/generated/absent.ts")
	rendered := testRenderedFile(output, []byte("absent\n"))
	request, plan := artifactTestRequest(t, targetRoot, []RenderedFile{rendered})
	store, err := NewArtifactStore(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewExecutor(fixedRenderer{err: errors.New("must not render")}, NewFileCommitter(), store, artifactTestRegistry(t))
	if err != nil {
		t.Fatal(err)
	}

	_, found, err := executor.Recover(context.Background(), targetRoot, artifactTestInput(request, plan), request.TargetSnapshotChecksum)
	if err != nil || found {
		t.Fatalf("Recover without evidence = found %v, err %v", found, err)
	}
}

func TestExecutorRejectsTamperedSuccessStage(t *testing.T) {
	fixture := prepareInterruptedSuccess(t)
	manifestPath, err := fixture.transaction.stagePathForFinal(fixture.input.Request.ArtifactContext.Paths.AssemblyManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.executor.Execute(context.Background(), fixture.targetRoot, fixture.input, ProjectLock{}, PreviousArtifacts{}); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("tampered stage error = %v", err)
	}
}

func TestExecutorRejectsTamperedTargetDuringStageRecovery(t *testing.T) {
	fixture := prepareInterruptedSuccess(t)
	writeTestFile(t, fixture.targetRoot, fixture.outputPath, []byte("tampered\n"))

	if _, err := fixture.executor.Execute(context.Background(), fixture.targetRoot, fixture.input, ProjectLock{}, PreviousArtifacts{}); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("tampered target error = %v", err)
	}
}

func TestExecutorRejectsUnexpectedFileInSuccessStage(t *testing.T) {
	fixture := prepareInterruptedSuccess(t)
	if err := os.WriteFile(filepath.Join(fixture.transaction.stageRoot, "unexpected"), []byte("unexpected"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.executor.Execute(context.Background(), fixture.targetRoot, fixture.input, ProjectLock{}, PreviousArtifacts{}); !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("unexpected staged file error = %v", err)
	}
}

type interruptedSuccessFixture struct {
	targetRoot  string
	outputPath  string
	input       Input
	store       *ArtifactStore
	executor    *Executor
	transaction *ArtifactTransaction
}

func prepareInterruptedSuccess(t *testing.T) interruptedSuccessFixture {
	t.Helper()
	targetRoot, artifactRoot := separateTestRoots(t)
	output := artifactTestOutput("src/generated/recovered.ts")
	rendered := testRenderedFile(output, []byte("export const recovered = true;\n"))
	request, plan := artifactTestRequest(t, targetRoot, []RenderedFile{rendered})
	input := artifactTestInput(request, plan)
	prepared, err := PrepareTarget(targetRoot, request, Result{Files: []RenderedFile{rendered}}, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := BuildArtifactBundle(targetRoot, request, input.Plan, input.EvidenceDocuments, prepared, PreviousArtifacts{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewArtifactStore(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := store.Prepare(request, bundle)
	if err != nil {
		t.Fatal(err)
	}
	committer := NewFileCommitter()
	if _, err := committer.Commit(context.Background(), targetRoot, request, prepared); err != nil {
		t.Fatal(err)
	}
	executor, err := NewExecutor(fixedRenderer{err: errors.New("renderer must not run during recovery")}, committer, store, artifactTestRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	return interruptedSuccessFixture{targetRoot: targetRoot, outputPath: output.Path, input: input, store: store, executor: executor, transaction: transaction}
}
