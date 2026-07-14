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

	"platform.local/capability-platform/backend/internal/modules/tenant"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

type serviceStub struct {
	createCommand tenant.CreateAgentTenantCommand
	created       tenant.Tenant
	createErr     error
	createCalls   int
	listCommand   tenant.ListTenantsCommand
	listed        []tenant.Tenant
	listErr       error
	listCalls     int
}

func (s *serviceStub) CreateAgentTenant(_ context.Context, command tenant.CreateAgentTenantCommand) (tenant.Tenant, error) {
	s.createCalls++
	s.createCommand = command
	return s.created, s.createErr
}

func (s *serviceStub) ListTenants(_ context.Context, command tenant.ListTenantsCommand) ([]tenant.Tenant, error) {
	s.listCalls++
	s.listCommand = command
	return s.listed, s.listErr
}

type authenticatorStub struct {
	principal tenantPrincipal
	err       error
	proof     bool
	calls     int
}

type tenantPrincipal = adminrequest.Principal

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

func TestHandlerListsOnlyAuthorizedProductScope(t *testing.T) {
	now := time.Date(2026, 7, 13, 17, 0, 0, 0, time.UTC)
	service := &serviceStub{listed: []tenant.Tenant{
		{TenantID: "tenant-official", ProductID: "product-a", TenantCode: "official", Name: "Official", TenantType: tenant.TenantTypeOfficial, Status: tenant.TenantStatusActive, ContextVersion: 1, CreatedAt: now, UpdatedAt: now},
		{TenantID: "tenant-agent", ProductID: "product-a", TenantCode: "partner", Name: "Partner", TenantType: tenant.TenantTypeAgent, Status: tenant.TenantStatusActive, ExternalAgentRef: "external-1", ContextVersion: 2, CreatedAt: now, UpdatedAt: now},
	}}
	authenticator := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-a", SessionID: "session-a"}}
	authorizer := &authorizerStub{decision: adminrequest.Decision{Allowed: true}}
	recorder := perform(New(service, adminrequest.New(authenticator, authorizer, nil)), http.MethodGet, "https://api.example.com/api/v1/admin/products/product-a/tenants", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if service.listCalls != 1 || service.listCommand.ProductID != "product-a" {
		t.Fatalf("list calls = %d command = %+v", service.listCalls, service.listCommand)
	}
	if authenticator.proof {
		t.Fatal("GET unexpectedly required unsafe-request proof")
	}
	if authorizer.permission != tenantManagePermission || authorizer.target.Type != "product" || authorizer.target.ProductID != "product-a" || authorizer.target.TenantID != "" {
		t.Fatalf("authorization = permission %q target %+v", authorizer.permission, authorizer.target)
	}
	var response tenantListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 || response.Items[0].TenantID != "tenant-official" || response.Items[1].ExternalAgentRef != "external-1" {
		t.Fatalf("response = %+v", response)
	}
}

func TestHandlerCreatesAgentFromTrustedPrincipalAndPath(t *testing.T) {
	service := &serviceStub{created: tenant.Tenant{
		TenantID: "tenant-agent", ProductID: "product-a", TenantCode: "partner", Name: "Partner",
		TenantType: tenant.TenantTypeAgent, Status: tenant.TenantStatusActive, AuditID: "audit-1",
	}}
	authenticator := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "server-admin", SessionID: "server-session"}}
	authorizer := &authorizerStub{decision: adminrequest.Decision{Allowed: true}}
	headers := http.Header{"Idempotency-Key": []string{"tenant-create-key-0001"}, requestid.Header: []string{"request-tenant-0001"}}
	recorder := perform(New(service, adminrequest.New(authenticator, authorizer, nil)), http.MethodPost,
		"https://api.example.com/api/v1/admin/products/product-a/tenants", `{"name":"Partner","tenant_code":"partner","status":"active","external_agent_ref":"external-1"}`, headers)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	command := service.createCommand
	if service.createCalls != 1 || command.ProductID != "product-a" || command.ActorID != "server-admin" || command.TraceID != "request-tenant-0001" || command.IdempotencyKey != "tenant-create-key-0001" || command.ExternalAgentRef != "external-1" {
		t.Fatalf("create calls = %d command = %+v", service.createCalls, command)
	}
	if !authenticator.proof {
		t.Fatal("POST did not require unsafe-request proof")
	}
	var response createTenantResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.TenantID != "tenant-agent" || response.ProductID != "product-a" || response.AuditID != "audit-1" {
		t.Fatalf("response = %+v", response)
	}
}

func TestHandlerRejectsUnsupportedQueryAndUntrustedJSONFields(t *testing.T) {
	service := &serviceStub{}
	authenticator := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin", SessionID: "session"}}
	authorizer := &authorizerStub{decision: adminrequest.Decision{Allowed: true}}
	handler := New(service, adminrequest.New(authenticator, authorizer, nil))

	recorder := perform(handler, http.MethodGet, "https://api.example.com/api/v1/admin/products/product-a/tenants?tenant_id=forged", "", nil)
	assertProblem(t, recorder, http.StatusBadRequest, "tenant.invalid_query")
	if authenticator.calls != 0 || service.listCalls != 0 {
		t.Fatalf("invalid query reached dependencies: auth=%d service=%d", authenticator.calls, service.listCalls)
	}

	headers := http.Header{"Idempotency-Key": []string{"tenant-create-key-0001"}}
	recorder = perform(handler, http.MethodPost, "https://api.example.com/api/v1/admin/products/product-a/tenants",
		`{"name":"Partner","tenant_code":"partner","status":"active","product_id":"forged","actor_id":"forged"}`, headers)
	assertProblem(t, recorder, http.StatusBadRequest, "tenant.invalid_request")
	if service.createCalls != 0 {
		t.Fatalf("untrusted fields reached service: %+v", service.createCommand)
	}
}

func TestHandlerEnforcesOneJSONValueAndOneMiBLimit(t *testing.T) {
	handler, service := allowedHandler()
	headers := http.Header{"Idempotency-Key": []string{"tenant-create-key-0001"}}
	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{name: "two values", body: `{"name":"A","tenant_code":"aaa","status":"active"} {}`, status: http.StatusBadRequest, code: "tenant.invalid_request"},
		{name: "empty", body: ``, status: http.StatusBadRequest, code: "tenant.invalid_request"},
		{name: "too large", body: `{"name":"` + strings.Repeat("x", maxRequestBody) + `","tenant_code":"aaa","status":"active"}`, status: http.StatusRequestEntityTooLarge, code: "tenant.request_too_large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := perform(handler, http.MethodPost, "https://api.example.com/api/v1/admin/products/product-a/tenants", test.body, headers)
			assertProblem(t, recorder, test.status, test.code)
		})
	}
	if service.createCalls != 0 {
		t.Fatalf("invalid JSON reached service %d times", service.createCalls)
	}
}

func TestHandlerRequiresExactlyOneValidIdempotencyKey(t *testing.T) {
	handler, service := allowedHandler()
	body := `{"name":"Partner","tenant_code":"partner","status":"active"}`
	for name, headers := range map[string]http.Header{
		"missing":  nil,
		"short":    {"Idempotency-Key": []string{"short"}},
		"multiple": {"Idempotency-Key": []string{"tenant-create-key-0001", "tenant-create-key-0002"}},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := perform(handler, http.MethodPost, "https://api.example.com/api/v1/admin/products/product-a/tenants", body, headers)
			assertProblem(t, recorder, http.StatusBadRequest, "tenant.invalid_idempotency_key")
		})
	}
	if service.createCalls != 0 {
		t.Fatalf("invalid idempotency keys reached service %d times", service.createCalls)
	}
}

func TestHandlerUsesGuardForAuthenticationAndProductAuthorization(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		service := &serviceStub{}
		guard := adminrequest.New(&authenticatorStub{err: adminrequest.ErrUnauthenticated}, &authorizerStub{}, nil)
		recorder := perform(New(service, guard), http.MethodGet, "https://api.example.com/api/v1/admin/products/product-a/tenants", "", nil)
		assertProblem(t, recorder, http.StatusUnauthorized, "admin_auth.session_expired")
		if service.listCalls != 0 {
			t.Fatal("unauthenticated request reached service")
		}
	})
	t.Run("forbidden", func(t *testing.T) {
		service := &serviceStub{}
		guard := adminrequest.New(
			&authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin", SessionID: "session"}},
			&authorizerStub{decision: adminrequest.Decision{Allowed: false, ReasonCode: "scope_mismatch"}}, nil,
		)
		recorder := perform(New(service, guard), http.MethodGet, "https://api.example.com/api/v1/admin/products/product-b/tenants", "", nil)
		assertProblem(t, recorder, http.StatusForbidden, "admin_auth.permission_denied")
		if service.listCalls != 0 {
			t.Fatal("forbidden request reached service")
		}
	})
}

func TestHandlerMapsStableTenantErrors(t *testing.T) {
	headers := http.Header{"Idempotency-Key": []string{"tenant-create-key-0001"}}
	body := `{"name":"Partner","tenant_code":"partner","status":"active"}`
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{tenant.ErrInvalidCommand, http.StatusBadRequest, "tenant.invalid_request"},
		{tenant.ErrTenantNotFound, http.StatusNotFound, "tenant.not_found"},
		{tenant.ErrTenantCodeConflict, http.StatusConflict, "tenant.code_conflict"},
		{tenant.ErrIdempotencyConflict, http.StatusConflict, "tenant.idempotency_conflict"},
		{tenant.ErrTenantSuspended, http.StatusConflict, "tenant.suspended"},
		{errors.New("database unavailable"), http.StatusInternalServerError, "internal_error"},
	}
	for _, test := range tests {
		service := &serviceStub{createErr: test.err}
		authenticator := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin", SessionID: "session"}}
		authorizer := &authorizerStub{decision: adminrequest.Decision{Allowed: true}}
		recorder := perform(New(service, adminrequest.New(authenticator, authorizer, nil)), http.MethodPost,
			"https://api.example.com/api/v1/admin/products/product-a/tenants", body, headers)
		assertProblem(t, recorder, test.status, test.code)
	}
}

func TestHandlerRejectsMalformedCreateResult(t *testing.T) {
	headers := http.Header{"Idempotency-Key": []string{"tenant-create-key-0001"}}
	body := `{"name":"Partner","tenant_code":"partner","status":"active"}`
	results := []tenant.Tenant{
		{TenantID: "tenant-a", ProductID: "product-b", TenantType: tenant.TenantTypeAgent, Status: tenant.TenantStatusActive, AuditID: "audit-1"},
		{TenantID: "tenant-a", ProductID: "product-a", TenantType: tenant.TenantTypeOfficial, Status: tenant.TenantStatusActive, AuditID: "audit-1"},
		{TenantID: "tenant-a", ProductID: "product-a", TenantType: tenant.TenantTypeAgent, Status: tenant.TenantStatusActive},
	}
	for _, result := range results {
		service := &serviceStub{created: result}
		authenticator := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin", SessionID: "session"}}
		authorizer := &authorizerStub{decision: adminrequest.Decision{Allowed: true}}
		recorder := perform(New(service, adminrequest.New(authenticator, authorizer, nil)), http.MethodPost,
			"https://api.example.com/api/v1/admin/products/product-a/tenants", body, headers)
		assertProblem(t, recorder, http.StatusInternalServerError, "internal_error")
	}
}

func TestHandlerRejectsNonCanonicalPathsAndMethods(t *testing.T) {
	handler, _ := allowedHandler()
	for _, target := range []string{
		"https://api.example.com/api/v1/admin/products/product-a/tenants/",
		"https://api.example.com/api/v1/admin/products/product-a/other",
		"https://api.example.com/api/v1/admin/products/product-a/tenants/admins",
		"https://api.example.com/api/v1/admin/products/%70roduct-a/tenants",
	} {
		recorder := perform(handler, http.MethodGet, target, "", nil)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("target %q status = %d", target, recorder.Code)
		}
	}
	recorder := perform(handler, http.MethodDelete, "https://api.example.com/api/v1/admin/products/product-a/tenants", "", nil)
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != "GET, POST" {
		t.Fatalf("method response = %d allow=%q", recorder.Code, recorder.Header().Get("Allow"))
	}
}

func allowedHandler() (*Handler, *serviceStub) {
	service := &serviceStub{}
	authenticator := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin", SessionID: "session"}}
	authorizer := &authorizerStub{decision: adminrequest.Decision{Allowed: true}}
	return New(service, adminrequest.New(authenticator, authorizer, nil)), service
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
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != code || body["request_id"] == "" {
		t.Fatalf("problem = %v", body)
	}
}
