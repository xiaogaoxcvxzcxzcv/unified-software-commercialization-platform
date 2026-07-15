package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	assemblyhttp "platform.local/capability-platform/backend/internal/modules/assembly/httptransport"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
	assemblypostgres "platform.local/capability-platform/backend/internal/modules/assembly/postgres"
	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/requestid"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

type fixedAssemblyPlanner struct{}

func (fixedAssemblyPlanner) BuildPlan(_ context.Context, blueprint core.Blueprint, environment string) (core.PlannedDocument, error) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "schemas", "fixtures", "assembly-generator", "assembly-plan", "valid.json"))
	if err != nil {
		return core.PlannedDocument{}, err
	}
	var document map[string]any
	if err := json.Unmarshal(fixture, &document); err != nil {
		return core.PlannedDocument{}, err
	}
	document["plan_id"] = "plan.http-foundation"
	document["blueprint_id"] = blueprint.BlueprintID
	document["blueprint_version"] = blueprint.Revision
	document["environment"] = environment
	for _, raw := range document["applications"].([]any) {
		raw.(map[string]any)["environment"] = environment
	}
	confirmation := document["confirmation"].(map[string]any)
	statementsRaw := confirmation["statements"].([]any)
	statements := make([]string, len(statementsRaw))
	for index, statement := range statementsRaw {
		statements[index] = statement.(string)
	}
	confirmationChecksum, err := core.ConfirmationSummaryChecksum(int(confirmation["blocking_conflict_count"].(float64)), int(confirmation["risk_count"].(float64)), statements)
	if err != nil {
		return core.PlannedDocument{}, err
	}
	confirmation["summary_checksum"] = confirmationChecksum
	document["plan_checksum"] = "sha256:" + strings.Repeat("0", 64)
	raw, err := json.Marshal(document)
	if err != nil {
		return core.PlannedDocument{}, err
	}
	checksum, err := machinecontract.DigestWithoutTopLevelField(raw, "plan_checksum")
	if err != nil {
		return core.PlannedDocument{}, err
	}
	document["plan_checksum"] = checksum
	raw, err = json.Marshal(document)
	if err != nil {
		return core.PlannedDocument{}, err
	}
	return core.PlannedDocument{Document: raw, Capabilities: []product.CapabilityItem{{CapabilityID: "identity.user-session", Enabled: true, Policy: json.RawMessage(`{}`), SourcePackageID: "package.account", SourcePackageVersion: "1.0.0"}}}, nil
}

func TestAssemblyHTTPPostgreSQLFoundationFlow(t *testing.T) {
	database := testpostgres.Open(t)
	registry, err := machinecontract.LoadDirectory(filepath.Join("..", "..", "..", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	service := core.NewService(assemblypostgres.New(database.Pool), core.NewRegistryValidator(registry), fixedAssemblyPlanner{}, securevalue.ID, nil)
	guard := adminrequest.New(
		assemblyIntegrationAuthenticator{},
		integrationAuthorizer{},
		nil,
	)
	handler := requestid.Middleware(assemblyhttp.New(newAssemblyAdminAdapter(service, "workspace.default", "workspace.secondary"), guard))

	blueprintDocument, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "schemas", "fixtures", "catalog-blueprint", "product-blueprint.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	blueprint := doJSON(t, handler, http.MethodPost, "/api/v1/admin/blueprints", string(blueprintDocument), "assembly-blueprint-0001")
	blueprintID := stringField(t, blueprint, "blueprint_id")

	plan := doJSON(t, handler, http.MethodPost, "/api/v1/admin/blueprints/"+blueprintID+"/plan", `{"blueprint_version":1,"environment":"production"}`, "assembly-plan-0000001")
	planID := stringField(t, plan, "plan_id")
	planChecksum := stringField(t, plan, "checksum")
	planDocument := plan["document"].(map[string]any)
	confirmation := planDocument["confirmation"].(map[string]any)
	confirmationChecksum := confirmation["summary_checksum"].(string)

	assembleBody := fmt.Sprintf(`{"plan_id":%q,"expected_plan_version":1,"plan_checksum":%q,"confirmation":{"accepted":true,"summary_checksum":%q},"output_target_ref":"workspace.default"}`, planID, planChecksum, confirmationChecksum)
	run := doJSON(t, handler, http.MethodPost, "/api/v1/admin/blueprints/"+blueprintID+"/assemble", assembleBody, "assembly-start-000001")
	runID := stringField(t, run, "run_id")
	if run["status"] != "planned" {
		t.Fatalf("run=%v", run)
	}
	replayed := doJSON(t, handler, http.MethodPost, "/api/v1/admin/blueprints/"+blueprintID+"/assemble", assembleBody, "assembly-start-000001")
	if replayed["run_id"] != runID {
		t.Fatalf("replayed run=%v, want run_id=%s", replayed, runID)
	}
	conflictingBody := strings.Replace(assembleBody, "workspace.default", "workspace.secondary", 1)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/blueprints/"+blueprintID+"/assemble", strings.NewReader(conflictingBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "assembly-start-000001")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"assembly.idempotency_conflict"`) {
		t.Fatalf("conflicting output target status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	read := doJSON(t, handler, http.MethodGet, "/api/v1/admin/assembly-runs/"+runID, "{}", "assembly-read-0000001")
	if read["run_id"] != runID || read["plan_id"] != planID {
		t.Fatalf("read run=%v", read)
	}
	events, err := service.ClaimOutbox(context.Background(), time.Now().UTC().Add(time.Second), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("outbox events=%d, want 4", len(events))
	}
}

type assemblyIntegrationAuthenticator struct{}

func (assemblyIntegrationAuthenticator) Authenticate(context.Context, *http.Request, bool) (adminrequest.Principal, error) {
	return adminrequest.Principal{AdminUserID: "admin-integration", SessionID: "session-integration", AuthTime: time.Now().UTC()}, nil
}
