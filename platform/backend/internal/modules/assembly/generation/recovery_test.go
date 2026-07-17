package generation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestExecutorDiscardsPreparedStageAtExactSourceAndRendersAgain(t *testing.T) {
	fixture := prepareSuccessStage(t, false)
	fixture.executor, _ = NewExecutor(fixedRenderer{result: Result{Files: []RenderedFile{fixture.rendered}}}, NewFileCommitter(), fixture.store, artifactTestRegistry(t))

	outcome, err := fixture.executor.Execute(context.Background(), fixture.targetRoot, fixture.input, ProjectLock{}, PreviousArtifacts{})
	if err != nil || !outcome.Published {
		t.Fatalf("prepared-source recovery outcome=%#v err=%v", outcome, err)
	}
	assertTestFile(t, fixture.targetRoot, fixture.outputPath, string(fixture.rendered.Bytes))
}

func TestExecutorRollsPartialPreparedCommitBackBeforeRerender(t *testing.T) {
	fixture := prepareSuccessStage(t, false)
	writeTestFile(t, fixture.targetRoot, fixture.outputPath, fixture.rendered.Bytes)
	generatorStage := filepath.Join(fixture.targetRoot, filepath.FromSlash(fixture.input.Request.StagingPath))
	if err := os.MkdirAll(generatorStage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generatorStage, "interrupted"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.executor, _ = NewExecutor(fixedRenderer{result: Result{Files: []RenderedFile{fixture.rendered}}}, NewFileCommitter(), fixture.store, artifactTestRegistry(t))

	outcome, err := fixture.executor.Execute(context.Background(), fixture.targetRoot, fixture.input, ProjectLock{}, PreviousArtifacts{})
	if err != nil || !outcome.Published {
		t.Fatalf("partial recovery outcome=%#v err=%v", outcome, err)
	}
	if _, err := os.Stat(generatorStage); !os.IsNotExist(err) {
		t.Fatalf("interrupted generator stage still exists: %v", err)
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

func TestExecutorRecoverAcceptsProductionRequestRebuiltAfterWorkspaceCommit(t *testing.T) {
	targetRoot, artifactRoot := separateTestRoots(t)
	output := artifactTestOutput("src/generated/production-resume.ts")
	rendered := testRenderedFile(output, []byte("export const resumed = true;\n"))
	_, plan := artifactTestRequest(t, targetRoot, []RenderedFile{rendered})
	spec := RequestSpec{
		WorkspaceRef: "workspace.production-resume", LifecycleOperationID: "operation.production-resume",
		RunCreatedAt:      time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC),
		Product:           ArtifactProduct{ProductID: "product.test", OfficialTenantID: "tenant.official", Applications: []ArtifactApplication{{PlanApplicationID: "application.web", ApplicationID: "app.web"}}},
		Blueprint:         ArtifactBlueprint{BlueprintID: "bp_test-product", Version: 1, Checksum: rawDigest([]byte("blueprint"))},
		BlueprintDocument: json.RawMessage(`{"product":{"name":"Demo","symbol":"Demo"}}`), PlanDocument: plan,
	}
	original, previous, err := BuildRequest(targetRoot, spec)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareTarget(targetRoot, original.Request, Result{Files: []RenderedFile{rendered}}, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := BuildArtifactBundle(targetRoot, original.Request, original.Plan, original.EvidenceDocuments, prepared, previous)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := NewArtifactStore(artifactRoot)
	_, err = store.Prepare(original.Request, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewFileCommitter().Commit(context.Background(), targetRoot, original.Request, prepared); err != nil {
		t.Fatal(err)
	}
	rebuilt, _, err := BuildRequest(targetRoot, spec)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Request.ArtifactContext.Evidence[0].SHA256 == original.Request.ArtifactContext.Evidence[0].SHA256 {
		t.Fatal("rebuilt request did not exercise changed workspace evidence")
	}
	executor, _ := NewExecutor(fixedRenderer{err: errors.New("must not render")}, NewFileCommitter(), store, artifactTestRegistry(t))
	outcome, found, err := executor.Recover(context.Background(), targetRoot, rebuilt, original.Request.TargetSnapshotChecksum)
	if err != nil || !found || !outcome.Published {
		t.Fatalf("production rebuilt recovery found=%v outcome=%#v err=%v", found, outcome, err)
	}
	if _, published, err := store.loadPublishedRecoverable(rebuilt.Request, original.Request.TargetSnapshotChecksum); err != nil || !published {
		t.Fatalf("recovered transaction was not durably published: found=%v err=%v", published, err)
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

func TestLoadPublishedRejectsTamperedRollbackDigest(t *testing.T) {
	fixture := prepareInterruptedSuccess(t)
	publishInterruptedFixture(t, fixture)
	rollbackPath := filepath.Join(fixture.store.root, filepath.FromSlash(fixture.input.Request.ArtifactContext.Paths.RollbackPointPath))
	var rollback map[string]any
	if json.Unmarshal(mustRead(t, rollbackPath), &rollback) != nil {
		t.Fatal("decode rollback")
	}
	rollback["rollback_checksum"] = digestBytes([]byte("tampered"))
	raw, _ := json.Marshal(rollback)
	if err := os.WriteFile(rollbackPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := fixture.store.LoadPublished(fixture.input.Request); !found || !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("tampered rollback found=%v err=%v", found, err)
	}
}

func TestLoadPublishedRejectsUnexpectedFileAndIdentityMismatch(t *testing.T) {
	for _, mode := range []string{"unexpected", "identity"} {
		t.Run(mode, func(t *testing.T) {
			fixture := prepareInterruptedSuccess(t)
			publishInterruptedFixture(t, fixture)
			finalRoot := filepath.Join(fixture.store.root, filepath.FromSlash(filepath.Dir(fixture.input.Request.ArtifactContext.Paths.AssemblyManifestPath)))
			if mode == "unexpected" {
				if err := os.WriteFile(filepath.Join(finalRoot, "unexpected"), []byte("unexpected"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				resultPath := filepath.Join(fixture.store.root, filepath.FromSlash(fixture.input.Request.ArtifactContext.Paths.ResultPath))
				var result generatorResultDocument
				if jsonUnmarshalStrict(mustRead(t, resultPath), &result) != nil {
					t.Fatal("decode result")
				}
				result.RequestID = "request.different"
				raw, _, err := marshalWithEmbeddedDigest(result, "result_checksum")
				if err != nil || os.WriteFile(resultPath, raw, 0o600) != nil {
					t.Fatal("rewrite result identity")
				}
			}
			if _, found, err := fixture.store.LoadPublished(fixture.input.Request); !found || !errors.Is(err, ErrArtifactConflict) {
				t.Fatalf("mode=%s found=%v err=%v", mode, found, err)
			}
		})
	}
}

func TestLoadPublishedRejectsMissingRollbackBackup(t *testing.T) {
	fixture := preparePublishedUpgrade(t)
	var rollback rollbackPointDocument
	rollbackPath := filepath.Join(fixture.store.root, filepath.FromSlash(fixture.input.Request.ArtifactContext.Paths.RollbackPointPath))
	if jsonUnmarshalStrict(mustRead(t, rollbackPath), &rollback) != nil || len(rollback.Files) != 1 || rollback.Files[0].BackupPath == "" {
		t.Fatal("upgrade fixture has no rollback backup")
	}
	backupPath := filepath.Join(fixture.store.root, filepath.FromSlash(rollback.Files[0].BackupPath))
	if err := os.Remove(backupPath); err != nil {
		t.Fatal(err)
	}
	if _, found, err := fixture.store.LoadPublished(fixture.input.Request); !found || !errors.Is(err, ErrArtifactConflict) {
		t.Fatalf("missing backup found=%v err=%v", found, err)
	}
}

func TestRollbackResumesAfterWorkspaceRestoreBeforeJournalMark(t *testing.T) {
	fixture := preparePublishedUpgrade(t)
	rollbackPath := fixture.input.Request.ArtifactContext.Paths.RollbackPointPath
	journalPath := fixture.input.Request.ArtifactContext.Paths.CommitJournalPath
	var rollbackPoint rollbackPointDocument
	if jsonUnmarshalStrict(mustRead(t, filepath.Join(fixture.store.root, filepath.FromSlash(rollbackPath))), &rollbackPoint) != nil || len(rollbackPoint.Files) != 1 {
		t.Fatal("decode rollback point")
	}
	item := rollbackPoint.Files[0]
	backup := mustRead(t, filepath.Join(fixture.store.root, filepath.FromSlash(item.BackupPath)))
	writeTestFile(t, fixture.targetRoot, item.Path, backup)
	executor, _ := NewRollbackExecutor(fixture.store, artifactTestRegistry(t))
	if _, err := executor.Rollback(context.Background(), fixture.targetRoot, rollbackPath, journalPath); err != nil {
		t.Fatalf("resume rollback: %v", err)
	}
	if _, err := executor.Rollback(context.Background(), fixture.targetRoot, rollbackPath, journalPath); err != nil {
		t.Fatalf("idempotent rolled_back replay: %v", err)
	}
}

type interruptedSuccessFixture struct {
	targetRoot  string
	outputPath  string
	input       Input
	store       *ArtifactStore
	executor    *Executor
	transaction *ArtifactTransaction
	rendered    RenderedFile
}

func prepareInterruptedSuccess(t *testing.T) interruptedSuccessFixture {
	return prepareSuccessStage(t, true)
}

func prepareSuccessStage(t *testing.T, commit bool) interruptedSuccessFixture {
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
	if commit {
		if _, err := committer.Commit(context.Background(), targetRoot, request, prepared); err != nil {
			t.Fatal(err)
		}
	}
	executor, err := NewExecutor(fixedRenderer{err: errors.New("renderer must not run during recovery")}, committer, store, artifactTestRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	return interruptedSuccessFixture{targetRoot: targetRoot, outputPath: output.Path, input: input, store: store, executor: executor, transaction: transaction, rendered: rendered}
}

func publishInterruptedFixture(t *testing.T, fixture interruptedSuccessFixture) {
	t.Helper()
	if err := fixture.transaction.MarkCommitted(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.transaction.Publish(); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, filename string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type publishedUpgradeFixture struct {
	input      Input
	store      *ArtifactStore
	targetRoot string
}

func preparePublishedUpgrade(t *testing.T) publishedUpgradeFixture {
	t.Helper()
	targetRoot, artifactRoot := separateTestRoots(t)
	output := artifactTestOutput("src/generated/upgrade-recovery.ts")
	oldRendered := testRenderedFile(output, []byte("old\n"))
	firstRequest, firstPlan := artifactTestRequest(t, targetRoot, []RenderedFile{oldRendered})
	store, _ := NewArtifactStore(artifactRoot)
	registry := artifactTestRegistry(t)
	firstExecutor, _ := NewExecutor(fixedRenderer{result: Result{Files: []RenderedFile{oldRendered}}}, NewFileCommitter(), store, registry)
	if _, err := firstExecutor.Execute(context.Background(), targetRoot, artifactTestInput(firstRequest, firstPlan), ProjectLock{}, PreviousArtifacts{}); err != nil {
		t.Fatal(err)
	}
	previousLockRaw := mustRead(t, filepath.Join(artifactRoot, filepath.FromSlash(firstRequest.ArtifactContext.Paths.GeneratedLockPath)))
	previousLock, err := DecodeProjectLock(previousLockRaw)
	if err != nil {
		t.Fatal(err)
	}
	var planValue map[string]any
	if json.Unmarshal(firstPlan, &planValue) != nil {
		t.Fatal("decode plan")
	}
	planValue["plan_id"] = "plan.upgrade-recovery"
	planValue["plan_checksum"] = rawDigest([]byte("upgrade-recovery-plan"))
	upgradePlan, _ := json.Marshal(planValue)
	input, previous, err := BuildRequest(targetRoot, RequestSpec{
		WorkspaceRef: "workspace.upgrade-recovery", LifecycleOperationID: "operation.upgrade-recovery", RunCreatedAt: time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC),
		Product:           ArtifactProduct{ProductID: "product.test", OfficialTenantID: "tenant.official", Applications: []ArtifactApplication{{PlanApplicationID: "application.web", ApplicationID: "app.web"}}},
		Blueprint:         ArtifactBlueprint{BlueprintID: "bp_test-product", Version: 1, Checksum: rawDigest([]byte("blueprint"))},
		BlueprintDocument: json.RawMessage(`{"product":{"name":"Demo","symbol":"Demo"}}`), PlanDocument: upgradePlan,
		PreviousLock: previousLock, PreviousManifestPath: firstRequest.ArtifactContext.Paths.AssemblyManifestPath, PreviousLockPath: firstRequest.ArtifactContext.Paths.GeneratedLockPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	newRendered := testRenderedFile(output, []byte("new\n"))
	executor, _ := NewExecutor(fixedRenderer{result: Result{Files: []RenderedFile{newRendered}}}, NewFileCommitter(), store, registry)
	if _, err := executor.Execute(context.Background(), targetRoot, input, previousLock, previous); err != nil {
		t.Fatal(err)
	}
	return publishedUpgradeFixture{input: input, store: store, targetRoot: targetRoot}
}
