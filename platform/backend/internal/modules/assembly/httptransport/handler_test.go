package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

const testChecksum = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type serviceStub struct {
	lifecycleSource         LifecycleArtifactState
	lifecycleSourceErr      error
	getLifecycleSource      GetLifecycleSourceCommand
	getLifecycleSourceCalls int
	catalogOptions          CatalogOptions
	catalogOptionsErr       error
	listCatalog             ListCatalogOptionsCommand
	listCatalogCalls        int
	listExperimentalCalls   int
	outputTargets           OutputTargetList
	outputTargetsErr        error
	listTargets             ListOutputTargetsCommand
	listTargetCalls         int
	blueprint               Blueprint
	blueprintErr            error
	createBlueprint         CreateBlueprintCommand
	getBlueprint            GetBlueprintCommand
	createCalls             int
	getCalls                int
	plan                    Plan
	planErr                 error
	createPlan              CreatePlanCommand
	getPlan                 GetPlanCommand
	createPlanCalls         int
	getPlanCalls            int
	run                     Run
	runErr                  error
	start                   StartAssemblyCommand
	getRun                  GetRunCommand
	startCalls              int
	getRunCalls             int
	manifest                Manifest
	manifestErr             error
	getManifest             GetManifestCommand
	getManifestCalls        int
	lock                    GeneratedProjectLock
	lockErr                 error
	getLock                 GetLockCommand
	getLockCalls            int
}
type recoveryServiceStub struct {
	*serviceStub
	page RunPage
}

func (s *recoveryServiceStub) ListRuns(context.Context, ListRunsCommand) (RunPage, error) {
	return s.page, nil
}
func (s *recoveryServiceStub) RetryRun(context.Context, RetryRunCommand) (Run, error) {
	return Run{}, ErrNotFound
}

func (s *serviceStub) ListCatalogOptions(_ context.Context, command ListCatalogOptionsCommand) (CatalogOptions, error) {
	s.listCatalogCalls++
	s.listCatalog = command
	return s.catalogOptions, s.catalogOptionsErr
}
func (s *serviceStub) ListExperimentalCatalogOptions(_ context.Context, command ListCatalogOptionsCommand) (CatalogOptions, error) {
	s.listExperimentalCalls++
	s.listCatalog = command
	return s.catalogOptions, s.catalogOptionsErr
}

func (s *serviceStub) ListOutputTargets(_ context.Context, command ListOutputTargetsCommand) (OutputTargetList, error) {
	s.listTargetCalls++
	s.listTargets = command
	return s.outputTargets, s.outputTargetsErr
}

func TestHandlerListsOrdinaryAndExperimentalCatalogOptionsThroughSeparatePermissions(t *testing.T) {
	service := &serviceStub{catalogOptions: CatalogOptions{CatalogScope: "ordinary", CatalogRevision: "catalog-123456789abc", Target: "web", DeliveryMode: "generated_source", Environment: "test", Packages: []CatalogPackageOption{}, Templates: []CatalogTemplateOption{}, Generators: []CatalogToolOption{}, SDKs: []CatalogToolOption{}}}
	handler, _, authorization := allowedHandler(service)
	response := perform(handler, http.MethodGet, "/api/v1/admin/assembly-catalog-options?target=web&delivery_mode=generated_source&environment=test", "", nil)
	if response.Code != http.StatusOK || authorization.permission != assemblyPlanPermission || service.listCatalogCalls != 1 || service.listExperimentalCalls != 0 {
		t.Fatalf("ordinary response=%d permission=%q calls=%d/%d body=%s", response.Code, authorization.permission, service.listCatalogCalls, service.listExperimentalCalls, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["catalog_scope"] != "ordinary" || len(body) != 9 {
		t.Fatalf("ordinary body=%#v", body)
	}

	service.catalogOptions.CatalogScope = "experimental"
	response = perform(handler, http.MethodGet, "/api/v1/admin/experimental/assembly-catalog-options?environment=test&delivery_mode=generated_source&target=web", "", nil)
	if response.Code != http.StatusOK || authorization.permission != assemblyExperimentalPermission || service.listExperimentalCalls != 1 {
		t.Fatalf("experimental response=%d permission=%q calls=%d body=%s", response.Code, authorization.permission, service.listExperimentalCalls, response.Body.String())
	}
}

func TestHandlerRejectsCatalogScopeInjectionAndMalformedQueriesBeforeService(t *testing.T) {
	service := &serviceStub{}
	handler, _, _ := allowedHandler(service)
	tests := []struct {
		target, body string
		headers      http.Header
	}{
		{"/api/v1/admin/assembly-catalog-options?target=web&delivery_mode=generated_source&environment=test&catalog_scope=experimental", "", nil},
		{"/api/v1/admin/assembly-catalog-options?target=web&target=h5&delivery_mode=generated_source&environment=test", "", nil},
		{"/api/v1/admin/assembly-catalog-options?target=invalid&delivery_mode=generated_source&environment=test", "", nil},
		{"/api/v1/admin/assembly-catalog-options?target=web&delivery_mode=generated_source&environment=test", `{}`, http.Header{"Content-Type": []string{"application/json"}}},
		{"/api/v1/admin/assembly-catalog-options?target=web&delivery_mode=generated_source&environment=test", "", http.Header{"X-Assembly-Catalog-Scope": []string{"experimental"}}},
	}
	for _, test := range tests {
		response := perform(handler, http.MethodGet, test.target, test.body, test.headers)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s => %d body=%s", test.target, response.Code, response.Body.String())
		}
	}
	if service.listCatalogCalls != 0 || service.listExperimentalCalls != 0 {
		t.Fatalf("service called %d/%d", service.listCatalogCalls, service.listExperimentalCalls)
	}
}

func TestCatalogDisplayAllowsProseSlashAndRejectsHostPaths(t *testing.T) {
	for _, value := range []string{"Web / H5 capability", "Account and profile"} {
		if !validCatalogDisplay(value, 120) {
			t.Fatalf("valid prose rejected: %q", value)
		}
	}
	for _, value := range []string{"D:/private/source", "/var/private", `\\server\share`, "../outside", "file:///private/source", "name\x00hidden"} {
		if validCatalogDisplay(value, 120) {
			t.Fatalf("path-like catalog display accepted: %q", value)
		}
	}
}

func TestHandlerFailsClosedBeforeSerializingPathLikeCatalogMetadata(t *testing.T) {
	service := &serviceStub{catalogOptions: CatalogOptions{
		CatalogScope: "ordinary", CatalogRevision: "catalog-123456789abc", Target: "web", DeliveryMode: "generated_source", Environment: "test",
		Packages:  []CatalogPackageOption{{PackageID: "package.account", Version: "1.0.0", Name: "D:/private/source", UserValue: "Account capability", Dependencies: []CatalogRequirement{}, Conflicts: []CatalogRequirement{}, CompatibleTemplateRefs: []CatalogVersionRef{{ID: "standard-a", Version: "1.0.0"}}}},
		Templates: []CatalogTemplateOption{}, Generators: []CatalogToolOption{}, SDKs: []CatalogToolOption{},
	}}
	handler, _, _ := allowedHandler(service)
	response := perform(handler, http.MethodGet, "/api/v1/admin/assembly-catalog-options?target=web&delivery_mode=generated_source&environment=test", "", nil)
	assertProblem(t, response, http.StatusInternalServerError, "internal_error")
	if strings.Contains(response.Body.String(), "private/source") {
		t.Fatalf("path-like metadata leaked: %s", response.Body.String())
	}
}

func TestHandlerRequiresExplicitExperimentalCatalogPermission(t *testing.T) {
	service := &serviceStub{}
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}}
	authorization := &authorizerStub{decision: adminrequest.Decision{Allowed: false, ReasonCode: "permission_denied"}}
	handler := New(service, adminrequest.New(auth, authorization, nil))
	response := perform(handler, http.MethodGet, "/api/v1/admin/experimental/assembly-catalog-options?target=web&delivery_mode=generated_source&environment=test", "", nil)
	assertProblem(t, response, http.StatusForbidden, "admin_auth.permission_denied")
	if authorization.permission != assemblyExperimentalPermission || service.listExperimentalCalls != 0 || auth.proof {
		t.Fatalf("permission=%q calls=%d proof=%v", authorization.permission, service.listExperimentalCalls, auth.proof)
	}
}

func (s *serviceStub) CreateBlueprint(_ context.Context, command CreateBlueprintCommand) (Blueprint, error) {
	s.createCalls++
	s.createBlueprint = command
	return s.blueprint, s.blueprintErr
}

func (s *serviceStub) GetBlueprint(_ context.Context, command GetBlueprintCommand) (Blueprint, error) {
	s.getCalls++
	s.getBlueprint = command
	return s.blueprint, s.blueprintErr
}

func (s *serviceStub) CreatePlan(_ context.Context, command CreatePlanCommand) (Plan, error) {
	s.createPlanCalls++
	s.createPlan = command
	return s.plan, s.planErr
}

func (s *serviceStub) GetPlan(_ context.Context, command GetPlanCommand) (Plan, error) {
	s.getPlanCalls++
	s.getPlan = command
	return s.plan, s.planErr
}

func (s *serviceStub) StartAssembly(_ context.Context, command StartAssemblyCommand) (Run, error) {
	s.startCalls++
	s.start = command
	return s.run, s.runErr
}

func (s *serviceStub) GetRun(_ context.Context, command GetRunCommand) (Run, error) {
	s.getRunCalls++
	s.getRun = command
	return s.run, s.runErr
}

func (s *serviceStub) GetManifest(_ context.Context, command GetManifestCommand) (Manifest, error) {
	s.getManifestCalls++
	s.getManifest = command
	return s.manifest, s.manifestErr
}

func (s *serviceStub) GetLock(_ context.Context, command GetLockCommand) (GeneratedProjectLock, error) {
	s.getLockCalls++
	s.getLock = command
	return s.lock, s.lockErr
}

type authenticatorStub struct {
	principal adminrequest.Principal
	err       error
	proof     bool
	calls     int
}

func (s *authenticatorStub) Authenticate(_ context.Context, _ *http.Request, proof bool) (adminrequest.Principal, error) {
	s.calls++
	s.proof = proof
	return s.principal, s.err
}

type authorizerStub struct {
	decision   adminrequest.Decision
	err        error
	principal  adminrequest.Principal
	permission string
	target     adminrequest.TargetScope
	calls      int
}

func (s *authorizerStub) Authorize(_ context.Context, principal adminrequest.Principal, permission string, target adminrequest.TargetScope) (adminrequest.Decision, error) {
	s.calls++
	s.principal = principal
	s.permission = permission
	s.target = target
	return s.decision, s.err
}

func TestHandlerCreatesPreProductBlueprintInPlatformScope(t *testing.T) {
	service := &serviceStub{blueprint: validBlueprintResult("bp-video")}
	handler, auth, authorization := allowedHandler(service)
	headers := writeHeaders("blueprint-create-0001")
	body := `{"schema_version":"1.0.0","blueprint_id":"bp-video","version":"1.0.0","product":{"code":"video","name":"Video"},"packages":[],"applications":[],"provider_refs":[],"extensions":[],"generator":{"id":"platform.generator","version":"1.0.0"},"sdk":{"id":"platform.sdk","version":"1.0.0"},"output_root":"generated/video"}`
	recorder := perform(handler, http.MethodPost, "https://api.example.test/api/v1/admin/blueprints", body, headers)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	command := service.createBlueprint
	if service.createCalls != 1 || command.ActorID != "admin-1" || command.TraceID != "request-assembly-0001" || command.IdempotencyKey != "blueprint-create-0001" || !json.Valid(command.Document) {
		t.Fatalf("calls=%d command=%+v", service.createCalls, command)
	}
	if !auth.proof || authorization.permission != blueprintManagePermission || authorization.target.Type != "platform" || authorization.target.ProductID != "" {
		t.Fatalf("proof=%v permission=%q target=%+v", auth.proof, authorization.permission, authorization.target)
	}
}

func TestHandlerListsOnlyRedactedOutputTargetsWithExplicitDefault(t *testing.T) {
	defaultRef := "workspace.default"
	service := &serviceStub{outputTargets: OutputTargetList{
		Environment: "production", DefaultOutputTargetRef: &defaultRef,
		Items: []OutputTarget{
			{OutputTargetRef: "workspace.default", Environment: "production", DisplayName: "Production workspace", Summary: "Managed production source and evidence", IsDefault: true},
			{OutputTargetRef: "workspace.secondary", Environment: "production", DisplayName: "Secondary workspace", Summary: "Managed secondary destination"},
		},
	}}
	handler, auth, authorization := allowedHandler(service)
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/assembly-output-targets?environment=production", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if strings.Contains(body, "target_root") || strings.Contains(body, "artifact_root") || strings.Contains(body, `C:\\`) || strings.Contains(body, `D:/`) {
		t.Fatalf("response leaked a host path: %s", body)
	}
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["environment"] != "production" || decoded["default_policy"] != "explicit" || decoded["default_output_target_ref"] != defaultRef || len(decoded["items"].([]any)) != 2 {
		t.Fatalf("response=%v", decoded)
	}
	if service.listTargetCalls != 1 || service.listTargets.Environment != "production" || service.listTargets.ActorID != "admin-1" || auth.proof || authorization.permission != assemblyPlanPermission || authorization.target.Type != "platform" {
		t.Fatalf("command=%+v proof=%v permission=%q target=%+v", service.listTargets, auth.proof, authorization.permission, authorization.target)
	}
}

func TestHandlerReturnsNullWhenOutputTargetHasNoExplicitDefault(t *testing.T) {
	service := &serviceStub{outputTargets: OutputTargetList{Environment: "test", Items: []OutputTarget{}}}
	handler, _, _ := allowedHandler(service)
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/assembly-output-targets?environment=test", "", nil)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"default_output_target_ref":null`) || !strings.Contains(recorder.Body.String(), `"items":[]`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandlerFailsClosedWhenOutputTargetDisplayMetadataLooksLikeAHostPath(t *testing.T) {
	service := &serviceStub{outputTargets: OutputTargetList{
		Environment: "production",
		Items:       []OutputTarget{{OutputTargetRef: "workspace.default", Environment: "production", DisplayName: "D:/private/source", Summary: "Managed output"}},
	}}
	handler, _, _ := allowedHandler(service)
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/assembly-output-targets?environment=production", "", nil)
	assertProblem(t, recorder, http.StatusInternalServerError, "internal_error")
	if strings.Contains(recorder.Body.String(), "D:/private") {
		t.Fatalf("internal response leaked display metadata: %s", recorder.Body.String())
	}
}

func TestHandlerFailsClosedWhenOutputTargetDisplayMetadataContainsControlCharacter(t *testing.T) {
	service := &serviceStub{outputTargets: OutputTargetList{
		Environment: "production",
		Items:       []OutputTarget{{OutputTargetRef: "workspace.default", Environment: "production", DisplayName: "Local\x00workspace", Summary: "Managed output"}},
	}}
	handler, _, _ := allowedHandler(service)
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/assembly-output-targets?environment=production", "", nil)
	assertProblem(t, recorder, http.StatusInternalServerError, "internal_error")
	if strings.Contains(recorder.Body.String(), "Local") {
		t.Fatalf("internal response leaked display metadata: %s", recorder.Body.String())
	}
}

func TestHandlerRejectsOutputTargetListWithoutPlanPermission(t *testing.T) {
	service := &serviceStub{}
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}}
	authorization := &authorizerStub{decision: adminrequest.Decision{Allowed: false, ReasonCode: "permission_denied"}}
	handler := New(service, adminrequest.New(auth, authorization, nil))
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/assembly-output-targets?environment=test", "", nil)
	assertProblem(t, recorder, http.StatusForbidden, "admin_auth.permission_denied")
	if service.listTargetCalls != 0 || authorization.permission != assemblyPlanPermission || auth.proof {
		t.Fatalf("list calls=%d permission=%q proof=%v", service.listTargetCalls, authorization.permission, auth.proof)
	}
}

func TestHandlerCreatesServerGeneratedPlanWithoutClientPlanDocument(t *testing.T) {
	service := &serviceStub{plan: validPlanResult("plan-1", "bp-video")}
	handler, _, authorization := allowedHandler(service)
	recorder := perform(handler, http.MethodPost, "https://api.example.test/api/v1/admin/blueprints/bp-video/plan", `{"blueprint_version":3,"environment":"test"}`, writeHeaders("assembly-plan-key-0001"))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	command := service.createPlan
	if service.createPlanCalls != 1 || command.BlueprintID != "bp-video" || command.BlueprintVersion != 3 || command.Environment != "test" || command.ActorID != "admin-1" || command.TraceID != "request-assembly-0001" {
		t.Fatalf("calls=%d command=%+v", service.createPlanCalls, command)
	}
	if command.CatalogScope != "ordinary" {
		t.Fatalf("catalog scope=%q", command.CatalogScope)
	}
	if authorization.permission != assemblyPlanPermission || authorization.target.Type != "platform" {
		t.Fatalf("permission=%q target=%+v", authorization.permission, authorization.target)
	}
}

func TestHandlerCreatesExperimentalPlanThroughSeparatePermission(t *testing.T) {
	service := &serviceStub{plan: validPlanResult("plan-1", "bp-video")}
	handler, _, authorization := allowedHandler(service)
	recorder := perform(handler, http.MethodPost, "https://api.example.test/api/v1/admin/experimental/blueprints/bp-video/plan", `{"blueprint_version":3,"environment":"test"}`, writeHeaders("assembly-plan-key-0001"))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	command := service.createPlan
	if service.createPlanCalls != 1 || command.BlueprintID != "bp-video" || command.CatalogScope != "experimental" {
		t.Fatalf("calls=%d command=%+v", service.createPlanCalls, command)
	}
	if authorization.permission != assemblyExperimentalPermission || authorization.target.Type != "platform" {
		t.Fatalf("permission=%q target=%+v", authorization.permission, authorization.target)
	}
}

func TestHandlerRejectsExperimentalPlanScopeInjectionOnOrdinaryRoute(t *testing.T) {
	service := &serviceStub{plan: validPlanResult("plan-1", "bp-video")}
	handler, _, _ := allowedHandler(service)
	recorder := perform(handler, http.MethodPost, "https://api.example.test/api/v1/admin/blueprints/bp-video/plan", `{"blueprint_version":3,"environment":"test","catalog_scope":"experimental"}`, writeHeaders("assembly-plan-key-0001"))
	assertProblem(t, recorder, http.StatusBadRequest, "assembly.invalid_request")
	if service.createPlanCalls != 0 {
		t.Fatalf("client catalog scope reached service: %+v", service.createPlan)
	}
}

func TestHandlerStartsConfirmedAssemblyAsHighRisk(t *testing.T) {
	service := &serviceStub{run: validRunResult("run-1", "plan-1")}
	handler, auth, authorization := allowedHandler(service)
	body := `{"plan_id":"plan-1","expected_plan_version":2,"plan_checksum":"` + testChecksum + `","confirmation":{"accepted":true,"summary_checksum":"` + testChecksum + `"},"output_target_ref":"workspace.video"}`
	recorder := perform(handler, http.MethodPost, "https://api.example.test/api/v1/admin/blueprints/bp-video/assemble", body, writeHeaders("assembly-start-key-0001"))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"output_target_ref":"workspace.video"`) {
		t.Fatalf("response does not expose locked output target: %s", recorder.Body.String())
	}
	command := service.start
	if service.startCalls != 1 || command.BlueprintID != "bp-video" || command.PlanID != "plan-1" || command.ExpectedPlanVersion != 2 || command.PlanChecksum != testChecksum || command.ConfirmationChecksum != testChecksum || command.OutputTargetRef != "workspace.video" || command.ActorID != "admin-1" {
		t.Fatalf("calls=%d command=%+v", service.startCalls, command)
	}
	if !auth.proof || authorization.permission != assemblyExecutePermission || authorization.target.Type != "platform" {
		t.Fatalf("proof=%v permission=%q target=%+v", auth.proof, authorization.permission, authorization.target)
	}
}

func TestHandlerRequiresReauthenticationForAssemblyExecution(t *testing.T) {
	service := &serviceStub{}
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}}
	authorization := &authorizerStub{decision: adminrequest.Decision{Allowed: true, ReauthenticationRequired: true}}
	handler := New(service, adminrequest.New(auth, authorization, nil))
	body := `{"plan_id":"plan-1","expected_plan_version":2,"plan_checksum":"` + testChecksum + `","confirmation":{"accepted":true,"summary_checksum":"` + testChecksum + `"},"output_target_ref":"workspace.video"}`
	recorder := perform(handler, http.MethodPost, "https://api.example.test/api/v1/admin/blueprints/bp-video/assemble", body, writeHeaders("assembly-start-key-0001"))
	assertProblem(t, recorder, http.StatusForbidden, "admin_auth.reauthentication_required")
	if service.startCalls != 0 || !auth.proof {
		t.Fatalf("calls=%d proof=%v", service.startCalls, auth.proof)
	}
}

func TestHandlerReadsBlueprintPlanRunManifestAndLockStatus(t *testing.T) {
	service := &serviceStub{
		blueprint: validBlueprintResult("bp-video"),
		plan:      validPlanResult("plan-1", "bp-video"),
		run:       validRunResult("run-1", "plan-1"),
		manifest:  validManifestResult("assembly-1", "run-1"),
		lock:      validLockResult("lock-1", "assembly-1", "run-1"),
	}
	handler, auth, authorization := allowedHandler(service)
	for _, target := range []string{
		"https://api.example.test/api/v1/admin/blueprints/bp-video",
		"https://api.example.test/api/v1/admin/assembly-plans/plan-1",
		"https://api.example.test/api/v1/admin/assembly-runs/run-1",
		"https://api.example.test/api/v1/admin/assembly-manifests/assembly-1",
		"https://api.example.test/api/v1/admin/generated-project-locks/lock-1",
	} {
		recorder := perform(handler, http.MethodGet, target, "", nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("target=%s status=%d body=%s", target, recorder.Code, recorder.Body.String())
		}
	}
	if service.getBlueprint.BlueprintID != "bp-video" || service.getPlan.PlanID != "plan-1" || service.getRun.RunID != "run-1" || service.getManifest.AssemblyID != "assembly-1" || service.getLock.LockID != "lock-1" {
		t.Fatalf("blueprint=%+v plan=%+v run=%+v manifest=%+v lock=%+v", service.getBlueprint, service.getPlan, service.getRun, service.getManifest, service.getLock)
	}
	if auth.proof || authorization.permission != assemblyReadPermission || authorization.target.Type != "platform" {
		t.Fatalf("proof=%v permission=%q target=%+v", auth.proof, authorization.permission, authorization.target)
	}
}

func TestRunResponseUsesStrictSnakeCaseNestedContract(t *testing.T) {
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	value := normalizeRunProjection(Run{RunID: "run-contract", PlanID: "plan-contract", PlanVersion: 2, PlanChecksum: testChecksum, OutputTargetRef: "workspace.default", Status: "failed", Document: json.RawMessage(`{"schema_version":"1.0.0"}`), CreatedAt: now, UpdatedAt: now, CompletedAt: &now, AuditID: "audit-contract", Steps: []RunStep{{StepID: "step.provision", Kind: "provision", Status: "failed", Attempt: 1, CompensationStatus: "pending", DiagnosticIDs: []string{"diagnostic.transient"}}}, Recovery: RunRecovery{Retryable: true, ResumeFromStepID: "step.provision"}, Diagnostics: []RunDiagnostic{{DiagnosticID: "diagnostic.transient", Code: "diagnostic.transient", Severity: "error", Category: "assembly", Message: "Transient assembly prerequisite failed", Blocking: true, Retryable: true, Remediation: []string{"Retry later"}, RelatedPaths: []string{}}}, Reports: []RunReport{{ReportID: "report.validation", ReportType: "assembly_validation", Status: "failed", Summary: "Validation did not complete", CreatedAt: now}}})
	raw, err := json.Marshal(runResponseFrom(value))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{"StepID", "Retryable\"", "ReportType", "created_at\":\"2026-07-16T08:00:00Z\",\"diagnostic"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("forbidden key %q in %s", forbidden, text)
		}
	}
	for _, required := range []string{"\"step_id\"", "\"retryable\"", "\"type\":\"assembly_validation\"", "\"checksum\":null", "\"started_at\":null", "\"finished_at\":null"} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing %q in %s", required, text)
		}
	}
}

func TestHandlerListsAssemblyRunSummariesWithoutDocuments(t *testing.T) {
	now := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	service := &recoveryServiceStub{serviceStub: &serviceStub{}, page: RunPage{Items: []RunSummary{{RunID: "run-summary", PlanID: "plan-summary", Version: 3, RootRunID: "run-summary", AttemptNumber: 1, Status: "failed", DiagnosticCount: 2, ReportCount: 1, CreatedAt: now, UpdatedAt: now, CompletedAt: &now}}, NextCursor: "cursor-next"}}
	handler, _, _ := allowedHandler(service)
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/assembly-runs?page_size=25", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, required := range []string{`"diagnostic_count":2`, `"report_count":1`, `"next_cursor":"cursor-next"`} {
		if !strings.Contains(body, required) {
			t.Fatalf("missing %s in %s", required, body)
		}
	}
	if strings.Contains(body, "document") {
		t.Fatalf("summary leaked document: %s", body)
	}
}

func TestBlueprintAndPlanResponsesExposeRecoveryProjections(t *testing.T) {
	blueprintRaw, err := json.Marshal(blueprintResponseFrom(validBlueprintResult("bp-recovery")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blueprintRaw), `"environments":["test"]`) {
		t.Fatalf("blueprint=%s", blueprintRaw)
	}
	planRaw, err := json.Marshal(planResponseFrom(validPlanResult("plan-recovery", "bp-recovery")))
	if err != nil {
		t.Fatal(err)
	}
	text := string(planRaw)
	for _, required := range []string{`"confirmation_checksum":"` + testChecksum + `"`, `"review":`, `"application_id":"application.web"`, `"template_version":"1.0.0"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing %s in %s", required, text)
		}
	}
}

func TestHandlerReturnsManifestAndLockRecoveryMetadata(t *testing.T) {
	service := &serviceStub{
		manifest: validManifestResult("assembly-1", "run-1"),
		lock:     validLockResult("lock-1", "assembly-1", "run-1"),
	}
	handler, _, _ := allowedHandler(service)
	tests := []struct {
		target, idField, id string
	}{
		{"https://api.example.test/api/v1/admin/assembly-manifests/assembly-1", "assembly_id", "assembly-1"},
		{"https://api.example.test/api/v1/admin/generated-project-locks/lock-1", "lock_id", "lock-1"},
	}
	for _, test := range tests {
		recorder := perform(handler, http.MethodGet, test.target, "", nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("target=%s status=%d body=%s", test.target, recorder.Code, recorder.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body[test.idField] != test.id || body["product_id"] != "prod-video" || body["run_id"] != "run-1" || body["document_checksum"] != testChecksum || body["checksum"] != testChecksum || body["created_at"] == "" {
			t.Fatalf("response=%v", body)
		}
		if _, ok := body["document"].(map[string]any); !ok {
			t.Fatalf("document is not an object: %T", body["document"])
		}
	}
}

func TestHandlerRejectsClientPlanDocumentAndUnconfirmedExecution(t *testing.T) {
	service := &serviceStub{plan: validPlanResult("plan-1", "bp-video"), run: validRunResult("run-1", "plan-1")}
	handler, _, _ := allowedHandler(service)
	recorder := perform(handler, http.MethodPost, "https://api.example.test/api/v1/admin/blueprints/bp-video/plan", `{"blueprint_version":1,"environment":"test","document":{"executable":true}}`, writeHeaders("assembly-plan-key-0001"))
	assertProblem(t, recorder, http.StatusBadRequest, "assembly.invalid_request")
	if service.createPlanCalls != 0 {
		t.Fatal("client-supplied plan reached service")
	}
	body := `{"plan_id":"plan-1","expected_plan_version":2,"plan_checksum":"` + testChecksum + `","confirmation":{"accepted":false,"summary_checksum":"` + testChecksum + `"},"output_target_ref":"workspace.video"}`
	recorder = perform(handler, http.MethodPost, "https://api.example.test/api/v1/admin/blueprints/bp-video/assemble", body, writeHeaders("assembly-start-key-0001"))
	assertProblem(t, recorder, http.StatusBadRequest, "assembly.invalid_request")
	if service.startCalls != 0 {
		t.Fatal("unconfirmed execution reached service")
	}
}

func TestHandlerRejectsQueriesNonCanonicalPathsAndMethodsBeforeAuthorization(t *testing.T) {
	service := &serviceStub{}
	handler, auth, _ := allowedHandler(service)
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/blueprints/bp-video?version=1", "", nil)
	assertProblem(t, recorder, http.StatusBadRequest, "assembly.invalid_query")
	if auth.calls != 0 {
		t.Fatal("invalid query reached authentication")
	}
	for _, target := range []string{
		"https://api.example.test/api/v1/admin/assembly-output-targets",
		"https://api.example.test/api/v1/admin/assembly-output-targets?environment=invalid",
		"https://api.example.test/api/v1/admin/assembly-output-targets?environment=test&environment=production",
		"https://api.example.test/api/v1/admin/assembly-output-targets?environment=test&extra=value",
	} {
		recorder = perform(handler, http.MethodGet, target, "", nil)
		assertProblem(t, recorder, http.StatusBadRequest, "assembly.invalid_query")
	}
	for _, target := range []string{
		"https://api.example.test/api/v1/admin/blueprints/",
		"https://api.example.test/api/v1/admin/blueprints/%62p-video",
		"https://api.example.test/api/v1/admin/blueprints/bp-video/plan/",
		"https://api.example.test/api/v1/admin/assembly-runs/run-1/other",
		"https://api.example.test/api/v1/admin/assembly-manifests/assembly-1/other",
		"https://api.example.test/api/v1/admin/generated-project-locks/%6cock-1",
	} {
		recorder = perform(handler, http.MethodGet, target, "", nil)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("target=%q status=%d body=%s", target, recorder.Code, recorder.Body.String())
		}
	}
	recorder = perform(handler, http.MethodDelete, "https://api.example.test/api/v1/admin/blueprints/bp-video", "", nil)
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("status=%d allow=%q", recorder.Code, recorder.Header().Get("Allow"))
	}
}

func TestHandlerEnforcesStrictJSONMediaTypeSizeAndIdempotency(t *testing.T) {
	service := &serviceStub{}
	handler, _, _ := allowedHandler(service)
	tests := []struct {
		name    string
		body    string
		headers http.Header
		status  int
		code    string
	}{
		{name: "missing media type", body: `{}`, headers: http.Header{"Idempotency-Key": []string{"blueprint-create-0001"}}, status: http.StatusUnsupportedMediaType, code: "assembly.unsupported_media_type"},
		{name: "two values", body: `{}` + ` {}`, headers: writeHeaders("blueprint-create-0001"), status: http.StatusBadRequest, code: "assembly.invalid_request"},
		{name: "non object blueprint", body: `[]`, headers: writeHeaders("blueprint-create-0001"), status: http.StatusBadRequest, code: "assembly.invalid_request"},
		{name: "too large", body: `{"value":"` + strings.Repeat("x", maxRequestBody) + `"}`, headers: writeHeaders("blueprint-create-0001"), status: http.StatusRequestEntityTooLarge, code: "assembly.request_too_large"},
		{name: "missing idempotency", body: `{}`, headers: http.Header{"Content-Type": []string{"application/json"}}, status: http.StatusBadRequest, code: "assembly.invalid_idempotency_key"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := perform(handler, http.MethodPost, "https://api.example.test/api/v1/admin/blueprints", test.body, test.headers)
			assertProblem(t, recorder, test.status, test.code)
		})
	}
}

func TestHandlerRejectsMismatchedServiceResults(t *testing.T) {
	service := &serviceStub{blueprint: validBlueprintResult("bp-other")}
	handler, _, _ := allowedHandler(service)
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/blueprints/bp-video", "", nil)
	assertProblem(t, recorder, http.StatusInternalServerError, "internal_error")
}

func TestHandlerRejectsMismatchedManifestAndLockResults(t *testing.T) {
	service := &serviceStub{
		manifest: validManifestResult("assembly-other", "run-1"),
		lock:     validLockResult("lock-other", "assembly-1", "run-1"),
	}
	handler, _, _ := allowedHandler(service)
	for _, target := range []string{
		"https://api.example.test/api/v1/admin/assembly-manifests/assembly-1",
		"https://api.example.test/api/v1/admin/generated-project-locks/lock-1",
	} {
		recorder := perform(handler, http.MethodGet, target, "", nil)
		assertProblem(t, recorder, http.StatusInternalServerError, "internal_error")
	}
}

func TestHandlerMapsStableAssemblyErrors(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{ErrInvalidCommand, http.StatusBadRequest, "assembly.invalid_request"},
		{ErrDocumentInvalid, http.StatusBadRequest, "assembly.invalid_request"},
		{ErrNotFound, http.StatusNotFound, "assembly.not_found"},
		{ErrConflict, http.StatusConflict, "assembly.conflict"},
		{ErrVersionConflict, http.StatusConflict, "assembly.version_conflict"},
		{ErrIdempotencyConflict, http.StatusConflict, "assembly.idempotency_conflict"},
		{ErrOperationInProgress, http.StatusConflict, "assembly.operation_in_progress"},
		{ErrPlanUnavailable, http.StatusServiceUnavailable, "assembly.planner_unavailable"},
		{ErrPlanNotExecutable, http.StatusUnprocessableEntity, "assembly.plan_not_executable"},
		{ErrPlanNotConfirmed, http.StatusUnprocessableEntity, "assembly.plan_not_confirmed"},
		{ErrOutputTargetUnavailable, http.StatusUnprocessableEntity, "assembly.output_target_unavailable"},
		{errors.New("database unavailable"), http.StatusInternalServerError, "internal_error"},
	}
	for _, test := range tests {
		service := &serviceStub{blueprintErr: test.err}
		handler, _, _ := allowedHandler(service)
		recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/blueprints/bp-video", "", nil)
		assertProblem(t, recorder, test.status, test.code)
	}
}

func allowedHandler(service Service) (*Handler, *authenticatorStub, *authorizerStub) {
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}}
	authorization := &authorizerStub{decision: adminrequest.Decision{Allowed: true}}
	return New(service, adminrequest.New(auth, authorization, nil)), auth, authorization
}

func validBlueprintResult(id string) Blueprint {
	now := time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC)
	return Blueprint{BlueprintID: id, Version: 1, SchemaVersion: "1.0.0", Environments: []string{"test"}, Document: json.RawMessage(`{"schema_version":"1.0.0"}`), Checksum: testChecksum, CreatedAt: now, UpdatedAt: now, AuditID: "audit-blueprint"}
}

func validPlanResult(id, blueprintID string) Plan {
	now := time.Date(2026, 7, 13, 20, 1, 0, 0, time.UTC)
	return Plan{PlanID: id, Version: 1, BlueprintID: blueprintID, BlueprintVersion: 1, SchemaVersion: "1.0.0", Environment: "test", ConfirmationChecksum: testChecksum, Review: PlanReview{Applications: []PlanReviewApplication{{ApplicationID: "application.web", Target: "web", Channel: "official", DeliveryMode: "generated_source", TemplateID: "template.web", TemplateVersion: "1.0.0"}}, Statements: []string{"Confirm assembly"}}, Document: json.RawMessage(`{"schema_version":"1.0.0","applications":[]}`), Checksum: testChecksum, Executable: true, CreatedAt: now, UpdatedAt: now, AuditID: "audit-plan"}
}

func validRunResult(id, planID string) Run {
	now := time.Date(2026, 7, 13, 20, 2, 0, 0, time.UTC)
	return Run{RunID: id, PlanID: planID, PlanVersion: 2, PlanChecksum: testChecksum, OutputTargetRef: "workspace.video", Status: "planned", Document: json.RawMessage(`{"schema_version":"1.0.0","status":"planned"}`), CreatedAt: now, UpdatedAt: now, AuditID: "audit-run"}
}

func validManifestResult(id, runID string) Manifest {
	now := time.Date(2026, 7, 14, 0, 10, 0, 0, time.UTC)
	return Manifest{AssemblyID: id, ProductID: "prod-video", RunID: runID, SchemaVersion: "1.0.0",
		Document:         json.RawMessage(`{"schema_version":"1.0.0","assembly_id":"assembly-1"}`),
		DocumentChecksum: testChecksum, Checksum: testChecksum, CreatedAt: now}
}

func validLockResult(id, assemblyID, runID string) GeneratedProjectLock {
	now := time.Date(2026, 7, 14, 0, 11, 0, 0, time.UTC)
	return GeneratedProjectLock{LockID: id, ProductID: "prod-video", RunID: runID, AssemblyID: assemblyID,
		SchemaVersion: "1.0.0", Document: json.RawMessage(`{"schema_version":"1.0.0","lock_id":"lock-1"}`),
		DocumentChecksum: testChecksum, Checksum: testChecksum, CreatedAt: now}
}

func writeHeaders(idempotencyKey string) http.Header {
	return http.Header{
		"Content-Type":    []string{"application/json"},
		"Idempotency-Key": []string{idempotencyKey},
		requestid.Header:  []string{"request-assembly-0001"},
	}
}

func perform(handler http.Handler, method, target, body string, headers http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	recorder := httptest.NewRecorder()
	requestid.Middleware(handler).ServeHTTP(recorder, request)
	return recorder
}

func assertProblem(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, status, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != code || body["request_id"] == "" {
		t.Fatalf("problem=%v", body)
	}
}
