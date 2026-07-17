package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (s *serviceStub) GetLifecycleSource(_ context.Context, command GetLifecycleSourceCommand) (LifecycleArtifactState, error) {
	s.getLifecycleSourceCalls++
	s.getLifecycleSource = command
	return s.lifecycleSource, s.lifecycleSourceErr
}
func (s *serviceStub) CreateUpgradePlan(context.Context, CreateUpgradePlanCommand) (LifecyclePlan, error) {
	return LifecyclePlan{}, ErrPlanUnavailable
}
func (s *serviceStub) CreateEjectPlan(context.Context, CreateEjectPlanCommand) (LifecyclePlan, error) {
	return LifecyclePlan{}, ErrPlanUnavailable
}
func (s *serviceStub) GetLifecyclePlan(context.Context, GetLifecyclePlanCommand) (LifecyclePlan, error) {
	return LifecyclePlan{}, ErrNotFound
}
func (s *serviceStub) ExecuteLifecyclePlan(context.Context, ExecuteLifecyclePlanCommand) (LifecycleOperation, error) {
	return LifecycleOperation{}, ErrNotFound
}
func (s *serviceStub) GetLifecycleOperation(context.Context, GetLifecycleOperationCommand) (LifecycleOperation, error) {
	return LifecycleOperation{}, ErrNotFound
}
func (s *serviceStub) CancelLifecycleOperation(context.Context, CancelLifecycleOperationCommand) (LifecycleOperation, error) {
	return LifecycleOperation{}, ErrNotFound
}
func (s *serviceStub) RollbackLifecycleOperation(context.Context, RollbackLifecycleOperationCommand) (LifecycleOperation, error) {
	return LifecycleOperation{}, ErrNotFound
}
func (s *serviceStub) CancelRun(context.Context, CancelRunCommand) (Run, error) {
	return Run{}, ErrNotFound
}

func TestParseLifecycleRoutes(t *testing.T) {
	cases := map[string]routeKind{
		"/api/v1/admin/assemblies/assembly.test/lifecycle-source":             routeLifecycleSource,
		"/api/v1/admin/assemblies/assembly.test/upgrade-plans":                routeUpgradePlan,
		"/api/v1/admin/assemblies/assembly.test/eject-plans":                  routeEjectPlan,
		"/api/v1/admin/assembly-lifecycle-plans/lifecycle.test":               routeLifecyclePlan,
		"/api/v1/admin/assembly-lifecycle-plans/lifecycle.test/execute":       routeExecuteLifecyclePlan,
		"/api/v1/admin/assembly-lifecycle-operations/operation.test":          routeLifecycleOperation,
		"/api/v1/admin/assembly-lifecycle-operations/operation.test/cancel":   routeCancelLifecycleOperation,
		"/api/v1/admin/assembly-lifecycle-operations/operation.test/rollback": routeRollbackLifecycleOperation,
		"/api/v1/admin/assembly-runs/run.test/cancel":                         routeCancelRun,
	}
	for target, want := range cases {
		t.Run(target, func(t *testing.T) {
			route, ok := parseRoute(httptest.NewRequest("GET", target, nil))
			if !ok || route.kind != want {
				t.Fatalf("route=%+v ok=%v want=%v", route, ok, want)
			}
		})
	}
}

func TestLifecycleTargetRejectsClientChecksum(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"packages":[],"templates":[{"id":"template.standard-a","version":"1.0.0","checksum":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],"generator":{"id":"generator.platform","version":"1.0.0"},"sdks":[]}`))
	request.Header.Set("Content-Type", "application/json")
	var target lifecycleTargetRequest
	if decodeJSON(recorder, request, &target) {
		t.Fatal("client-supplied catalog checksum was accepted")
	}
	if recorder.Code != 400 {
		t.Fatalf("status=%d want=400", recorder.Code)
	}
}

func TestLifecycleResponsesDoNotExposeMachineDocuments(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	planRaw, err := json.Marshal(lifecyclePlanResponseFrom(LifecyclePlan{Document: json.RawMessage(`{"host_path":"C:/secret"}`), Changes: []LifecycleChange{}, Conflicts: []LifecycleConflict{}, RegressionTests: []string{}, Statements: []string{}, CreatedAt: now}))
	if err != nil {
		t.Fatal(err)
	}
	operationRaw, err := json.Marshal(lifecycleOperationResponseFrom(LifecycleOperation{Document: json.RawMessage(`{"rollback_point_path":"artifacts/private.json"}`), Diagnostics: []RunDiagnostic{}, Reports: []RunReport{}, CreatedAt: now, UpdatedAt: now}))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{planRaw, operationRaw} {
		var response map[string]any
		if json.Unmarshal(raw, &response) != nil {
			t.Fatal("response is not JSON")
		}
		if _, exposed := response["document"]; exposed {
			t.Fatalf("machine document exposed: %s", raw)
		}
	}
}

func TestLifecyclePathsRejectHostAndTraversalInputs(t *testing.T) {
	for _, paths := range [][]string{{"../secret"}, {"/etc/passwd"}, {"C:/secret"}, {"generated/../custom"}, {"generated/main.go", "GENERATED/main.go"}, {"generated\\main.go"}} {
		if validLifecyclePaths(paths) {
			t.Fatalf("unsafe paths accepted: %#v", paths)
		}
	}
	if !validLifecyclePaths([]string{"generated/main.go", "integration/routes.ts"}) {
		t.Fatal("safe relative paths rejected")
	}
}

func TestLifecycleBrowserProjectionRejectsPathsAndUnsafeEvidence(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	state := LifecycleArtifactState{ManifestID: "assembly.test", ManifestChecksum: strings.Replace(testChecksum, "b", "a", 64), LockID: "lock.test", LockChecksum: testChecksum, CatalogChecksum: testChecksum, TargetSnapshotChecksum: testChecksum}
	plan := LifecyclePlan{LifecyclePlanID: "lifecycle.test", AssemblyID: "assembly.test", ProductID: "product.test", Operation: "upgrade", Version: 1, Source: state, TargetSnapshotChecksum: testChecksum, Changes: []LifecycleChange{{Path: "generated/main.go", Action: "update", Ownership: "generated", BeforeChecksum: testChecksum, AfterChecksum: testChecksum, SourceID: "template.test", SourceVersion: "1.0.0"}}, Migrations: []LifecycleMigration{}, Conflicts: []LifecycleConflict{}, RegressionTests: []string{"assembly.lifecycle.contract"}, Rollback: LifecycleRollbackPolicy{Strategy: "restore_predecessor", Automatic: true, PredecessorManifestChecksum: testChecksum, PredecessorLockChecksum: testChecksum}, BlockingConflictCount: 0, Executable: true, ConfirmationChecksum: testChecksum, Statements: []string{"Confirm the locked lifecycle changes"}, PlanChecksum: testChecksum, Document: json.RawMessage(`{}`), CreatedAt: now, AuditID: "audit.test"}
	if !validLifecyclePlan(plan, "assembly.test", "upgrade") {
		t.Fatal("valid lifecycle plan rejected")
	}
	plan.Changes[0].Path = "generated/main.go\nD:/private/source"
	if validLifecyclePlan(plan, "assembly.test", "upgrade") {
		t.Fatal("lifecycle plan with embedded host path was accepted")
	}

	target := state
	operation := LifecycleOperation{OperationID: "operation.test", RootOperationID: "operation.test", LifecyclePlanID: "lifecycle.test", AssemblyID: "assembly.test", ProductID: "product.test", Kind: "upgrade", Version: 3, Status: "completed", Source: state, Target: &target, Recovery: LifecycleRecovery{RollbackAvailable: true}, Diagnostics: []RunDiagnostic{}, Reports: []RunReport{}, Document: json.RawMessage(`{}`), ManifestURL: manifestsPath + "/" + target.ManifestID, LockURL: locksPath + "/" + target.LockID, CreatedAt: now, UpdatedAt: now.Add(time.Second), CompletedAt: timePointer(now.Add(time.Second)), AuditID: "audit.test"}
	if !validLifecycleOperation(operation) {
		t.Fatal("valid lifecycle operation rejected")
	}
	operation.Diagnostics = []RunDiagnostic{{DiagnosticID: "diagnostic.test", Code: "assembly.lifecycle_failed", Severity: "error", Category: "generation", Message: "inspect D:/private/source", Remediation: []string{}, RelatedPaths: []string{}, CreatedAt: now}}
	if validLifecycleOperation(operation) {
		t.Fatal("lifecycle operation with host path evidence was accepted")
	}
}

func TestLifecycleSourceHandlerReturnsOnlyCurrentSafeState(t *testing.T) {
	state := LifecycleArtifactState{ManifestID: "assembly.current", ManifestChecksum: testChecksum, LockID: "lock.current", LockChecksum: testChecksum, CatalogChecksum: testChecksum, TargetSnapshotChecksum: testChecksum}
	service := &serviceStub{lifecycleSource: state}
	handler, auth, authorization := allowedHandler(service)
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/assemblies/assembly.root/lifecycle-source", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.getLifecycleSourceCalls != 1 || service.getLifecycleSource.AssemblyID != "assembly.root" || auth.proof || authorization.permission != assemblyReadPermission || authorization.target.Type != "platform" {
		t.Fatalf("calls=%d command=%+v proof=%v permission=%q target=%+v", service.getLifecycleSourceCalls, service.getLifecycleSource, auth.proof, authorization.permission, authorization.target)
	}
	var response map[string]any
	if json.Unmarshal(recorder.Body.Bytes(), &response) != nil || len(response) != 6 || response["manifest_id"] != "assembly.current" || response["lock_id"] != "lock.current" {
		t.Fatalf("unexpected source projection: %s", recorder.Body.String())
	}

	service.lifecycleSource.ManifestID = "C:/private/source"
	recorder = perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/assemblies/assembly.root/lifecycle-source", "", nil)
	assertProblem(t, recorder, http.StatusInternalServerError, "internal_error")
}

func TestManifestAndLockResponsesExposeExactlyOneProvenanceSource(t *testing.T) {
	service := &serviceStub{manifest: validManifestResult("assembly-successor", ""), lock: validLockResult("lock-successor", "assembly-successor", "")}
	service.manifest.LifecycleOperationID = "operation-successor"
	service.lock.LifecycleOperationID = "operation-successor"
	handler, _, _ := allowedHandler(service)
	for _, target := range []string{
		"https://api.example.test/api/v1/admin/assembly-manifests/assembly-successor",
		"https://api.example.test/api/v1/admin/generated-project-locks/lock-successor",
	} {
		recorder := perform(handler, http.MethodGet, target, "", nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("target=%s status=%d body=%s", target, recorder.Code, recorder.Body.String())
		}
		var response map[string]any
		if json.Unmarshal(recorder.Body.Bytes(), &response) != nil || response["lifecycle_operation_id"] != "operation-successor" {
			t.Fatalf("target=%s response=%s", target, recorder.Body.String())
		}
		if _, exists := response["run_id"]; exists {
			t.Fatalf("target=%s exposed ambiguous provenance: %s", target, recorder.Body.String())
		}
	}
}

func timePointer(value time.Time) *time.Time { return &value }
