package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

type serviceStub struct {
	productCommand productuseraccess.SetProductAccessStatusCommand
	tenantCommand  productuseraccess.SetTenantAccessStatusCommand
	productResult  productuseraccess.StatusChangeResult
	tenantResult   productuseraccess.StatusChangeResult
	err            error
	productCalls   int
	tenantCalls    int
}

func (s *serviceStub) SetProductAccessStatus(_ context.Context, command productuseraccess.SetProductAccessStatusCommand) (productuseraccess.StatusChangeResult, error) {
	s.productCalls++
	s.productCommand = command
	return s.productResult, s.err
}

func (s *serviceStub) SetTenantAccessStatus(_ context.Context, command productuseraccess.SetTenantAccessStatusCommand) (productuseraccess.StatusChangeResult, error) {
	s.tenantCalls++
	s.tenantCommand = command
	return s.tenantResult, s.err
}

type authenticatorStub struct {
	principal adminrequest.Principal
	err       error
	writes    []bool
}

func (s *authenticatorStub) Authenticate(_ context.Context, _ *http.Request, write bool) (adminrequest.Principal, error) {
	s.writes = append(s.writes, write)
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
	s.permission, s.target = permission, target
	return s.decision, s.err
}

func TestHandlerSetsProductAccessThroughExactHighRiskScope(t *testing.T) {
	service := &serviceStub{productResult: productuseraccess.StatusChangeResult{
		ScopeType: productuseraccess.ScopeProduct, ProductID: "product-a", UserID: "user-a",
		Status: productuseraccess.StatusSuspended, AccessVersion: 1, AuditID: "audit-product-a",
	}}
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-a", SessionID: "session-a"}}
	authorizer := &authorizerStub{decision: adminrequest.Decision{Allowed: true}}
	recorder := serve(New(service, adminrequest.New(auth, authorizer, nil)), http.MethodPut,
		"/api/v1/admin/products/product-a/users/user-a/access",
		`{"expected_version":0,"status":"suspended","reason_code":"security.review","operator_note":"private note"}`,
		map[string]string{"Idempotency-Key": "idempotency-product-0001", requestid.Header: "request-product-0001"})
	if recorder.Code != http.StatusOK || service.productCalls != 1 || service.tenantCalls != 0 {
		t.Fatalf("status=%d product/tenant calls=%d/%d body=%s", recorder.Code, service.productCalls, service.tenantCalls, recorder.Body.String())
	}
	command := service.productCommand
	if command.Product.ProductID != "product-a" || command.User.UserID != "user-a" || command.ExpectedVersion != 0 || command.Status != productuseraccess.StatusSuspended || command.ReasonCode != "security.review" || command.OperatorNote != "private note" || command.IdempotencyKey != "idempotency-product-0001" || command.ActorID != "admin-a" || command.TraceID != "request-product-0001" {
		t.Fatalf("command=%+v", command)
	}
	if authorizer.permission != managePermission || authorizer.target != (adminrequest.TargetScope{Type: "product", ID: "product-a", ProductID: "product-a"}) || len(auth.writes) != 1 || !auth.writes[0] {
		t.Fatalf("permission=%q target=%+v writes=%v", authorizer.permission, authorizer.target, auth.writes)
	}
	assertMutation(t, recorder, "user-a", "product", "product-a", "suspended", 1, "audit-product-a")
	if strings.Contains(recorder.Body.String(), "private note") || strings.Contains(recorder.Body.String(), "operator_note") {
		t.Fatalf("response leaked operator note: %s", recorder.Body.String())
	}
}

func TestHandlerSetsTenantAccessThroughExactHighRiskScope(t *testing.T) {
	service := &serviceStub{tenantResult: productuseraccess.StatusChangeResult{
		ScopeType: productuseraccess.ScopeTenant, ProductID: "product-a", TenantID: "tenant-a", UserID: "user-a",
		Status: productuseraccess.StatusActive, AccessVersion: 3, AuditID: "audit-tenant-a",
	}}
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-a"}}
	authorizer := &authorizerStub{decision: adminrequest.Decision{Allowed: true}}
	recorder := serve(New(service, adminrequest.New(auth, authorizer, nil)), http.MethodPut,
		"/api/v1/admin/products/product-a/tenants/tenant-a/users/user-a/access",
		`{"expected_version":2,"status":"active","reason_code":"manual.restore"}`,
		map[string]string{"Idempotency-Key": "idempotency-tenant-0001", requestid.Header: "request-tenant-0001"})
	if recorder.Code != http.StatusOK || service.tenantCalls != 1 || service.productCalls != 0 {
		t.Fatalf("status=%d tenant/product calls=%d/%d body=%s", recorder.Code, service.tenantCalls, service.productCalls, recorder.Body.String())
	}
	command := service.tenantCommand
	if command.Product.ProductID != "product-a" || command.Tenant.ProductID != "product-a" || command.Tenant.TenantID != "tenant-a" || command.User.UserID != "user-a" || command.ActorID != "admin-a" || command.TraceID != "request-tenant-0001" {
		t.Fatalf("command=%+v", command)
	}
	want := adminrequest.TargetScope{Type: "tenant", ID: "tenant-a", ProductID: "product-a", TenantID: "tenant-a"}
	if authorizer.permission != managePermission || authorizer.target != want {
		t.Fatalf("permission=%q target=%+v", authorizer.permission, authorizer.target)
	}
	assertMutation(t, recorder, "user-a", "tenant", "tenant-a", "active", 3, "audit-tenant-a")
}

func TestHandlerRequiresRecentAuthenticationBeforeService(t *testing.T) {
	service := &serviceStub{}
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-a"}}
	authorizer := &authorizerStub{decision: adminrequest.Decision{Allowed: true, ReauthenticationRequired: true}}
	recorder := serve(New(service, adminrequest.New(auth, authorizer, nil)), http.MethodPut,
		"/api/v1/admin/products/product-a/users/user-a/access",
		`{"expected_version":0,"status":"suspended","reason_code":"security.review"}`,
		map[string]string{"Idempotency-Key": "idempotency-product-0001"})
	if recorder.Code != http.StatusForbidden || service.productCalls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.productCalls, recorder.Body.String())
	}
	assertErrorCode(t, recorder, "admin_auth.reauthentication_required", "")
}

func TestHandlerStrictRoutesRequestsAndIdempotency(t *testing.T) {
	tests := []struct {
		name, method, target, body string
		headers                    map[string]string
		status                     int
		code, allow                string
	}{
		{name: "unknown field including client proof", method: http.MethodPut, target: "/api/v1/admin/products/product-a/users/user-a/access", body: `{"expected_version":0,"status":"active","reason_code":"manual.restore","recent_auth_proof":"untrusted"}`, headers: map[string]string{"Idempotency-Key": "idempotency-product-0001"}, status: 400, code: "product_user_access.invalid_request"},
		{name: "duplicate top-level field", method: http.MethodPut, target: "/api/v1/admin/products/product-a/users/user-a/access", body: `{"expected_version":0,"status":"active","status":"suspended","reason_code":"manual.restore"}`, headers: map[string]string{"Idempotency-Key": "idempotency-product-0001"}, status: 400, code: "product_user_access.invalid_request"},
		{name: "duplicate nested field", method: http.MethodPut, target: "/api/v1/admin/products/product-a/users/user-a/access", body: `{"expected_version":0,"status":"active","reason_code":"manual.restore","unknown":{"value":1,"value":2}}`, headers: map[string]string{"Idempotency-Key": "idempotency-product-0001"}, status: 400, code: "product_user_access.invalid_request"},
		{name: "trailing json value", method: http.MethodPut, target: "/api/v1/admin/products/product-a/users/user-a/access", body: `{"expected_version":0,"status":"active","reason_code":"manual.restore"} {}`, headers: map[string]string{"Idempotency-Key": "idempotency-product-0001"}, status: 400, code: "product_user_access.invalid_request"},
		{name: "wrong content type", method: http.MethodPut, target: "/api/v1/admin/products/product-a/users/user-a/access", body: `{"expected_version":0,"status":"active","reason_code":"manual.restore"}`, headers: map[string]string{"Idempotency-Key": "idempotency-product-0001", "Content-Type": "text/plain"}, status: 400, code: "product_user_access.invalid_request"},
		{name: "missing content type", method: http.MethodPut, target: "/api/v1/admin/products/product-a/users/user-a/access", body: `{"expected_version":0,"status":"active","reason_code":"manual.restore"}`, headers: map[string]string{"Idempotency-Key": "idempotency-product-0001", "Content-Type": ""}, status: 400, code: "product_user_access.invalid_request"},
		{name: "oversized body", method: http.MethodPut, target: "/api/v1/admin/products/product-a/users/user-a/access", body: `{"expected_version":0,"status":"active","reason_code":"manual.restore","operator_note":"` + strings.Repeat("a", maxRequestBody) + `"}`, headers: map[string]string{"Idempotency-Key": "idempotency-product-0001"}, status: 413, code: "product_user_access.request_too_large"},
		{name: "missing expected version", method: http.MethodPut, target: "/api/v1/admin/products/product-a/users/user-a/access", body: `{"status":"active","reason_code":"manual.restore"}`, headers: map[string]string{"Idempotency-Key": "idempotency-product-0001"}, status: 400, code: "product_user_access.invalid_request"},
		{name: "missing idempotency", method: http.MethodPut, target: "/api/v1/admin/products/product-a/users/user-a/access", body: `{}`, status: 400, code: "product_user_access.invalid_idempotency_key"},
		{name: "whitespace idempotency", method: http.MethodPut, target: "/api/v1/admin/products/product-a/users/user-a/access", body: `{}`, headers: map[string]string{"Idempotency-Key": " idempotency-product-0001"}, status: 400, code: "product_user_access.invalid_idempotency_key"},
		{name: "query", method: http.MethodPut, target: "/api/v1/admin/products/product-a/users/user-a/access?scope=other", body: `{}`, headers: map[string]string{"Idempotency-Key": "idempotency-product-0001"}, status: 400, code: "product_user_access.invalid_query"},
		{name: "encoded path", method: http.MethodPut, target: "/api/v1/admin/products/%70roduct-a/users/user-a/access", body: `{}`, status: 404, code: "route_not_found"},
		{name: "trailing slash", method: http.MethodPut, target: "/api/v1/admin/products/product-a/users/user-a/access/", body: `{}`, status: 404, code: "route_not_found"},
		{name: "method", method: http.MethodPost, target: "/api/v1/admin/products/product-a/users/user-a/access", body: `{}`, status: 405, code: "method_not_allowed", allow: "PUT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &serviceStub{}
			handler := New(service, adminrequest.New(&authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-a"}}, &authorizerStub{decision: adminrequest.Decision{Allowed: true}}, nil))
			recorder := serve(handler, test.method, test.target, test.body, test.headers)
			if recorder.Code != test.status || service.productCalls != 0 || service.tenantCalls != 0 {
				t.Fatalf("status=%d want=%d calls=%d/%d body=%s", recorder.Code, test.status, service.productCalls, service.tenantCalls, recorder.Body.String())
			}
			assertErrorCode(t, recorder, test.code, test.allow)
		})
	}
}

func TestHandlerMapsStableDomainErrorsAndRejectsMismatchedResult(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{productuseraccess.ErrInvalidArgument, 400, "product_user_access.invalid_request"},
		{productuseraccess.ErrScopeMismatch, 400, "PRODUCT_USER_ACCESS_SCOPE_MISMATCH"},
		{productuseraccess.ErrConflict, 409, "PRODUCT_USER_ACCESS_CONFLICT"},
		{errors.New("database unavailable"), 500, "internal_error"},
	} {
		service := &serviceStub{err: test.err}
		recorder := serve(allowedHandler(service), http.MethodPut, "/api/v1/admin/products/product-a/users/user-a/access",
			`{"expected_version":0,"status":"active","reason_code":"manual.restore"}`,
			map[string]string{"Idempotency-Key": "idempotency-product-0001"})
		if recorder.Code != test.status {
			t.Fatalf("error=%v status=%d body=%s", test.err, recorder.Code, recorder.Body.String())
		}
		assertErrorCode(t, recorder, test.code, "")
	}

	service := &serviceStub{productResult: productuseraccess.StatusChangeResult{ScopeType: productuseraccess.ScopeProduct, ProductID: "product-b", UserID: "user-a", Status: productuseraccess.StatusActive, AccessVersion: 1, AuditID: "audit-a"}}
	recorder := serve(allowedHandler(service), http.MethodPut, "/api/v1/admin/products/product-a/users/user-a/access",
		`{"expected_version":0,"status":"active","reason_code":"manual.restore"}`,
		map[string]string{"Idempotency-Key": "idempotency-product-0001"})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("mismatched result status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertErrorCode(t, recorder, "internal_error", "")
}

func TestHandlerAcceptsApplicationJSONParameters(t *testing.T) {
	service := &serviceStub{productResult: productuseraccess.StatusChangeResult{ScopeType: productuseraccess.ScopeProduct, ProductID: "product-a", UserID: "user-a", Status: productuseraccess.StatusActive, AccessVersion: 1, AuditID: "audit-a"}}
	recorder := serve(allowedHandler(service), http.MethodPut, "/api/v1/admin/products/product-a/users/user-a/access",
		`{"expected_version":0,"status":"active","reason_code":"manual.restore"}`,
		map[string]string{"Idempotency-Key": "idempotency-product-0001", "Content-Type": "application/json; charset=utf-8"})
	if recorder.Code != http.StatusOK || service.productCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.productCalls, recorder.Body.String())
	}
}

func allowedHandler(service Service) http.Handler {
	return New(service, adminrequest.New(
		&authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-a"}},
		&authorizerStub{decision: adminrequest.Decision{Allowed: true}}, nil,
	))
}

func serve(handler http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	requestid.Middleware(handler).ServeHTTP(recorder, request)
	return recorder
}

func assertMutation(t *testing.T, recorder *httptest.ResponseRecorder, userID, scopeType, scopeID, status string, version int64, auditID string) {
	t.Helper()
	var body struct {
		UserID    string `json:"user_id"`
		ScopeType string `json:"scope_type"`
		ScopeID   string `json:"scope_id"`
		Status    string `json:"status"`
		Version   int64  `json:"version"`
		AuditID   string `json:"audit_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.UserID != userID || body.ScopeType != scopeType || body.ScopeID != scopeID || body.Status != status || body.Version != version || body.AuditID != auditID {
		t.Fatalf("mutation response=%s decoded=%+v error=%v", recorder.Body.String(), body, err)
	}
}

func assertErrorCode(t *testing.T, recorder *httptest.ResponseRecorder, code, allow string) {
	t.Helper()
	var body struct {
		Code      string `json:"code"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != code || body.RequestID == "" {
		t.Fatalf("error response=%s decoded=%+v error=%v", recorder.Body.String(), body, err)
	}
	if allow != "" && recorder.Header().Get("Allow") != allow {
		t.Fatalf("Allow=%q want=%q", recorder.Header().Get("Allow"), allow)
	}
}
