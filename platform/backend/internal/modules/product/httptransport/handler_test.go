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

	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

type serviceStub struct {
	listed         []product.Product
	listErr        error
	listLimit      int
	listCalls      int
	got            product.Product
	getID          string
	getErr         error
	getCalls       int
	current        product.CapabilitySet
	currentID      string
	currentErr     error
	currentCalls   int
	replaced       product.CapabilitySet
	replaceCommand product.ReplaceCapabilitySetCommand
	replaceErr     error
	replaceCalls   int
}

func (s *serviceStub) ListProducts(_ context.Context, limit int) ([]product.Product, error) {
	s.listCalls++
	s.listLimit = limit
	return s.listed, s.listErr
}

func (s *serviceStub) GetProduct(_ context.Context, productID string) (product.Product, error) {
	s.getCalls++
	s.getID = productID
	return s.got, s.getErr
}

func (s *serviceStub) CurrentCapabilitySet(_ context.Context, productID string) (product.CapabilitySet, error) {
	s.currentCalls++
	s.currentID = productID
	return s.current, s.currentErr
}

func (s *serviceStub) ReplaceCapabilitySet(_ context.Context, command product.ReplaceCapabilitySetCommand) (product.CapabilitySet, error) {
	s.replaceCalls++
	s.replaceCommand = command
	return s.replaced, s.replaceErr
}

type provisionerStub struct {
	command ProvisionCommand
	result  ProvisionedProduct
	err     error
	calls   int
}

func (s *provisionerStub) ProvisionProduct(_ context.Context, command ProvisionCommand) (ProvisionedProduct, error) {
	s.calls++
	s.command = command
	return s.result, s.err
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
	permission string
	target     adminrequest.TargetScope
	calls      int
}

func (s *authorizerStub) Authorize(_ context.Context, _ adminrequest.Principal, permission string, target adminrequest.TargetScope) (adminrequest.Decision, error) {
	s.calls++
	s.permission = permission
	s.target = target
	return s.decision, s.err
}

func TestHandlerListsProductsAtPlatformScope(t *testing.T) {
	service := &serviceStub{listed: []product.Product{readyProduct("prod-a", "video-brain"), readyProduct("prod-b", "image-studio")}}
	provisioner := &provisionerStub{}
	handler, auth, authorization := allowedHandler(service, provisioner)
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/products", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.listCalls != 1 || service.listLimit != maxProductList || auth.proof {
		t.Fatalf("list calls=%d limit=%d proof=%v", service.listCalls, service.listLimit, auth.proof)
	}
	if authorization.permission != productReadPermission || authorization.target.Type != "platform" || authorization.target.ProductID != "" {
		t.Fatalf("authorization=%q %+v", authorization.permission, authorization.target)
	}
	var response productListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 || response.Items[1].ProductID != "prod-b" {
		t.Fatalf("response=%+v", response)
	}
}

func TestHandlerCreatesOnlyThroughReadyProvisioner(t *testing.T) {
	service := &serviceStub{}
	created := readyProduct("prod-a", "video-brain")
	provisioner := &provisionerStub{result: ProvisionedProduct{Product: created}}
	handler, auth, authorization := allowedHandler(service, provisioner)
	headers := http.Header{
		"Content-Type":    []string{"application/json; charset=utf-8"},
		"Idempotency-Key": []string{"product-create-key-0001"},
		requestid.Header:  []string{"request-product-create-0001"},
	}
	recorder := perform(handler, http.MethodPost, "https://api.example.test/api/v1/admin/products", `{"code":"video-brain","name":"Video Brain","status":"active"}`, headers)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if provisioner.calls != 1 || service.getCalls != 0 || service.replaceCalls != 0 {
		t.Fatalf("provisioner=%d service=%+v", provisioner.calls, service)
	}
	if provisioner.command.ActorID != "admin-1" || provisioner.command.TraceID != "request-product-create-0001" || provisioner.command.IdempotencyKey != "product-create-key-0001" || provisioner.command.ProductCode != "video-brain" {
		t.Fatalf("command=%+v", provisioner.command)
	}
	if !auth.proof || authorization.permission != productManagePermission || authorization.target.Type != "platform" {
		t.Fatalf("proof=%v authorization=%q %+v", auth.proof, authorization.permission, authorization.target)
	}
	var response productResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ProductID != "prod-a" || response.ProvisioningState != "ready" || response.OfficialTenantID == "" {
		t.Fatalf("response=%+v", response)
	}
}

func TestHandlerGetsProductAtPathScope(t *testing.T) {
	service := &serviceStub{got: readyProduct("prod-a", "video-brain")}
	handler, auth, authorization := allowedHandler(service, &provisionerStub{})
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/products/prod-a", "", nil)
	if recorder.Code != http.StatusOK || service.getID != "prod-a" || auth.proof {
		t.Fatalf("status=%d get=%q proof=%v body=%s", recorder.Code, service.getID, auth.proof, recorder.Body.String())
	}
	if authorization.permission != productReadPermission || authorization.target.ProductID != "prod-a" || authorization.target.Type != "product" {
		t.Fatalf("authorization=%q %+v", authorization.permission, authorization.target)
	}
}

func TestHandlerGetsSortedCapabilityProjectionAtProductScope(t *testing.T) {
	service := &serviceStub{
		got: readyProduct("prod-a", "video-brain"),
		current: product.CapabilitySet{
			CapabilitySetID: "pcset-private", ProductID: "prod-a", Version: 3,
			SourcePlanID: "plan-1", CatalogRevision: "revision-1",
			CatalogSnapshotSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			ContentSHA256:         "sha256:private", CreatedBy: "admin-private", CreatedAt: time.Now(), AuditID: "audit-1",
			Items: []product.CapabilityItem{
				{CapabilityID: "usage.read", Enabled: true, Policy: json.RawMessage(`{"limit":10}`), SourcePackageID: "package.usage", SourcePackageVersion: "1.2.0"},
				{CapabilityID: "account.manage", Enabled: false, SourcePackageID: "package.account", SourcePackageVersion: "1.0.0"},
			},
		},
	}
	handler, auth, authorization := allowedHandler(service, &provisionerStub{})
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/products/prod-a/capabilities", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.getCalls != 1 || service.currentCalls != 1 || service.currentID != "prod-a" || auth.proof {
		t.Fatalf("get=%d current=%d current id=%q proof=%v", service.getCalls, service.currentCalls, service.currentID, auth.proof)
	}
	if authorization.permission != productReadPermission || authorization.target != productTarget("prod-a") {
		t.Fatalf("authorization=%q %+v", authorization.permission, authorization.target)
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	set, ok := response["capability_set"].(map[string]any)
	if !ok || response["product_id"] != "prod-a" || set["product_id"] != "prod-a" || set["version"] != float64(3) {
		t.Fatalf("response=%v", response)
	}
	items, ok := set["capabilities"].([]any)
	if !ok || len(items) != 2 || items[0].(map[string]any)["capability_id"] != "account.manage" || items[1].(map[string]any)["capability_id"] != "usage.read" {
		t.Fatalf("items=%v", set["capabilities"])
	}
	for _, privateField := range []string{"capability_set_id", "content_sha256", "created_by", "created_at"} {
		if _, exists := set[privateField]; exists {
			t.Fatalf("private field %q leaked in %v", privateField, set)
		}
	}
}

func TestHandlerReturnsNullWhenExistingProductHasNoCapabilitySet(t *testing.T) {
	service := &serviceStub{got: readyProduct("prod-a", "video-brain"), currentErr: product.ErrNotFound}
	handler, _, _ := allowedHandler(service, &provisionerStub{})
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/products/prod-a/capabilities", "", nil)
	if recorder.Code != http.StatusOK || service.getCalls != 1 || service.currentCalls != 1 {
		t.Fatalf("status=%d get=%d current=%d body=%s", recorder.Code, service.getCalls, service.currentCalls, recorder.Body.String())
	}
	var response capabilitySetProjectionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ProductID != "prod-a" || response.CapabilitySet != nil || !strings.Contains(recorder.Body.String(), `"capability_set":null`) {
		t.Fatalf("response=%s", recorder.Body.String())
	}
}

func TestHandlerDoesNotReadCapabilitySetForUnknownProduct(t *testing.T) {
	service := &serviceStub{getErr: product.ErrNotFound}
	handler, _, _ := allowedHandler(service, &provisionerStub{})
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/products/prod-a/capabilities", "", nil)
	assertProblem(t, recorder, http.StatusNotFound, "product.not_found")
	if service.currentCalls != 0 {
		t.Fatalf("current calls=%d", service.currentCalls)
	}
}

func TestHandlerRejectsUnauthorizedCapabilityReadBeforeServiceCalls(t *testing.T) {
	service := &serviceStub{}
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}}
	authorization := &authorizerStub{decision: adminrequest.Decision{Allowed: false}}
	handler := New(service, &provisionerStub{}, adminrequest.New(auth, authorization, nil))
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/products/prod-a/capabilities", "", nil)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.getCalls != 0 || service.currentCalls != 0 || authorization.permission != productReadPermission || authorization.target != productTarget("prod-a") {
		t.Fatalf("service=%+v authorization=%q %+v", service, authorization.permission, authorization.target)
	}
}

func TestHandlerMapsCapabilityReadErrorsAndRejectsInvalidProjection(t *testing.T) {
	t.Run("repository error", func(t *testing.T) {
		service := &serviceStub{got: readyProduct("prod-a", "video-brain"), currentErr: errors.New("database unavailable")}
		handler, _, _ := allowedHandler(service, &provisionerStub{})
		recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/products/prod-a/capabilities", "", nil)
		assertProblem(t, recorder, http.StatusInternalServerError, "internal_error")
	})
	t.Run("invalid set", func(t *testing.T) {
		service := &serviceStub{got: readyProduct("prod-a", "video-brain"), current: product.CapabilitySet{ProductID: "other-product", Version: 1}}
		handler, _, _ := allowedHandler(service, &provisionerStub{})
		recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/products/prod-a/capabilities", "", nil)
		assertProblem(t, recorder, http.StatusInternalServerError, "internal_error")
	})
}

func TestHandlerReplacesCapabilitiesUsingTrustedPathAndPrincipal(t *testing.T) {
	service := &serviceStub{replaced: product.CapabilitySet{CapabilitySetID: "pcset-1", ProductID: "prod-a", Version: 2, SourcePlanID: "plan-1", AuditID: "audit-1"}}
	handler, auth, authorization := allowedHandler(service, &provisionerStub{})
	headers := http.Header{
		"Content-Type":    []string{"application/json"},
		"Idempotency-Key": []string{"capability-change-0001"},
		requestid.Header:  []string{"request-capability-0001"},
	}
	body := `{"expected_version":1,"change_plan":{"assembly_plan_id":"plan-1","catalog_revision":"revision-1","catalog_snapshot_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`
	recorder := perform(handler, http.MethodPut, "https://api.example.test/api/v1/admin/products/prod-a/capabilities", body, headers)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	command := service.replaceCommand
	if service.replaceCalls != 1 || command.Plan.ProductID != "prod-a" || command.Plan.SourcePlanID != "plan-1" || command.ExpectedVersion != 1 || command.ActorID != "admin-1" || command.TraceID != "request-capability-0001" || command.IdempotencyKey != "capability-change-0001" || len(command.Plan.Items) != 0 {
		t.Fatalf("calls=%d command=%+v", service.replaceCalls, command)
	}
	if !auth.proof || authorization.permission != productManagePermission || authorization.target.ProductID != "prod-a" {
		t.Fatalf("proof=%v authorization=%q %+v", auth.proof, authorization.permission, authorization.target)
	}
}

func TestHandlerTreatsCapabilityReplacementAsHighRisk(t *testing.T) {
	service := &serviceStub{}
	provisioner := &provisionerStub{}
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}}
	authorization := &authorizerStub{decision: adminrequest.Decision{Allowed: true, ReauthenticationRequired: true}}
	handler := New(service, provisioner, adminrequest.New(auth, authorization, nil))
	headers := validWriteHeaders("capability-change-0001")
	body := `{"expected_version":0,"change_plan":{"assembly_plan_id":"plan-1","catalog_revision":"revision-1","catalog_snapshot_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`
	recorder := perform(handler, http.MethodPut, "https://api.example.test/api/v1/admin/products/prod-a/capabilities", body, headers)
	assertProblem(t, recorder, http.StatusForbidden, "admin_auth.reauthentication_required")
	if service.replaceCalls != 0 || !auth.proof {
		t.Fatalf("replace calls=%d proof=%v", service.replaceCalls, auth.proof)
	}
}

func TestHandlerRejectsPendingProvisioningResult(t *testing.T) {
	service := &serviceStub{}
	pending := readyProduct("prod-a", "video-brain")
	pending.ProvisioningState = "pending"
	pending.OfficialTenantID = ""
	handler, _, _ := allowedHandler(service, &provisionerStub{result: ProvisionedProduct{Product: pending}})
	headers := validWriteHeaders("product-create-key-0001")
	recorder := perform(handler, http.MethodPost, "https://api.example.test/api/v1/admin/products", `{"code":"video-brain","name":"Video Brain","status":"active"}`, headers)
	assertProblem(t, recorder, http.StatusInternalServerError, "internal_error")
}

func TestHandlerRejectsQueriesNonCanonicalPathsAndMethods(t *testing.T) {
	service := &serviceStub{}
	handler, auth, _ := allowedHandler(service, &provisionerStub{})
	recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/products?product_id=forged", "", nil)
	assertProblem(t, recorder, http.StatusBadRequest, "product.invalid_query")
	if auth.calls != 0 {
		t.Fatal("invalid query reached authentication")
	}
	for _, target := range []string{
		"https://api.example.test/api/v1/admin/products/",
		"https://api.example.test/api/v1/admin/products/prod-a/",
		"https://api.example.test/api/v1/admin/products/%70rod-a",
		"https://api.example.test/api/v1/admin/products/prod-a/other",
	} {
		recorder = perform(handler, http.MethodGet, target, "", nil)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("target=%q status=%d", target, recorder.Code)
		}
	}
	recorder = perform(handler, http.MethodDelete, "https://api.example.test/api/v1/admin/products", "", nil)
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != "GET, POST" {
		t.Fatalf("method status=%d allow=%q", recorder.Code, recorder.Header().Get("Allow"))
	}
	recorder = perform(handler, http.MethodPatch, "https://api.example.test/api/v1/admin/products/prod-a/capabilities", "", nil)
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != "GET, PUT" {
		t.Fatalf("capability method status=%d allow=%q", recorder.Code, recorder.Header().Get("Allow"))
	}
}

func TestHandlerEnforcesStrictJSONMediaTypeAndSize(t *testing.T) {
	service := &serviceStub{}
	handler, _, _ := allowedHandler(service, &provisionerStub{})
	baseHeaders := http.Header{"Idempotency-Key": []string{"product-create-key-0001"}}
	tests := []struct {
		name, body, contentType string
		status                  int
		code                    string
	}{
		{name: "missing content type", body: `{}`, status: http.StatusUnsupportedMediaType, code: "product.unsupported_media_type"},
		{name: "unknown field", body: `{"code":"video-brain","name":"Video","status":"active","product_id":"forged"}`, contentType: "application/json", status: http.StatusBadRequest, code: "product.invalid_request"},
		{name: "two values", body: `{"code":"video-brain","name":"Video","status":"active"} {}`, contentType: "application/json", status: http.StatusBadRequest, code: "product.invalid_request"},
		{name: "too large", body: `{"code":"video-brain","name":"` + strings.Repeat("x", maxRequestBody) + `","status":"active"}`, contentType: "application/json", status: http.StatusRequestEntityTooLarge, code: "product.request_too_large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			headers := baseHeaders.Clone()
			if test.contentType != "" {
				headers.Set("Content-Type", test.contentType)
			}
			recorder := perform(handler, http.MethodPost, "https://api.example.test/api/v1/admin/products", test.body, headers)
			assertProblem(t, recorder, test.status, test.code)
		})
	}
}

func TestHandlerRequiresExactlyOneSafeIdempotencyKey(t *testing.T) {
	service := &serviceStub{}
	handler, _, _ := allowedHandler(service, &provisionerStub{})
	body := `{"code":"video-brain","name":"Video","status":"active"}`
	for name, values := range map[string][]string{
		"missing":    nil,
		"short":      {"short"},
		"multiple":   {"product-create-key-0001", "product-create-key-0002"},
		"whitespace": {" product-create-key-0001"},
	} {
		t.Run(name, func(t *testing.T) {
			headers := http.Header{"Content-Type": []string{"application/json"}}
			for _, value := range values {
				headers.Add("Idempotency-Key", value)
			}
			recorder := perform(handler, http.MethodPost, "https://api.example.test/api/v1/admin/products", body, headers)
			assertProblem(t, recorder, http.StatusBadRequest, "product.invalid_idempotency_key")
		})
	}
}

func TestHandlerMapsStableProductErrors(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{product.ErrInvalidCommand, http.StatusBadRequest, "product.invalid_request"},
		{product.ErrNotFound, http.StatusNotFound, "product.not_found"},
		{product.ErrConflict, http.StatusConflict, "product.conflict"},
		{product.ErrIdempotencyConflict, http.StatusConflict, "product.idempotency_conflict"},
		{product.ErrCapabilityVersionConflict, http.StatusConflict, "product.capability_version_conflict"},
		{product.ErrUntrustedChangePlan, http.StatusUnprocessableEntity, "product.capability_plan_rejected"},
		{errors.New("database unavailable"), http.StatusInternalServerError, "internal_error"},
	}
	for _, test := range tests {
		service := &serviceStub{getErr: test.err}
		handler, _, _ := allowedHandler(service, &provisionerStub{})
		recorder := perform(handler, http.MethodGet, "https://api.example.test/api/v1/admin/products/prod-a", "", nil)
		assertProblem(t, recorder, test.status, test.code)
	}
}

func allowedHandler(service *serviceStub, provisioner *provisionerStub) (*Handler, *authenticatorStub, *authorizerStub) {
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}}
	authorization := &authorizerStub{decision: adminrequest.Decision{Allowed: true}}
	return New(service, provisioner, adminrequest.New(auth, authorization, nil)), auth, authorization
}

func readyProduct(id, code string) product.Product {
	now := time.Date(2026, 7, 13, 18, 0, 0, 0, time.UTC)
	return product.Product{ProductID: id, ProductCode: code, Name: code, Status: "active", ProvisioningState: "ready", OfficialTenantID: "official-" + id, ContextVersion: 2, CreatedAt: now, UpdatedAt: now, AuditID: "audit-" + id}
}

func validWriteHeaders(idempotencyKey string) http.Header {
	return http.Header{"Content-Type": []string{"application/json"}, "Idempotency-Key": []string{idempotencyKey}, requestid.Header: []string{"request-product-write-0001"}}
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
