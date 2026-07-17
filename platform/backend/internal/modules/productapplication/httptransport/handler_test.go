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

	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

type serviceStub struct {
	createCommand   productapplication.CreateCommand
	createResult    productapplication.Application
	createErr       error
	createCalls     int
	listProduct     productapplication.ProductContext
	listResult      []productapplication.Application
	listErr         error
	listCalls       int
	redirectCommand productapplication.ReplaceRedirectsCommand
	redirectResult  productapplication.RedirectPolicyVersion
	redirectErr     error
	redirectCalls   int
	suspendCommand  productapplication.SuspendCommand
	suspendResult   productapplication.SuspendResult
	suspendErr      error
	suspendCalls    int
}

func (s *serviceStub) CreateApplication(_ context.Context, command productapplication.CreateCommand) (productapplication.Application, error) {
	s.createCalls++
	s.createCommand = command
	return s.createResult, s.createErr
}

func (s *serviceStub) ListApplications(_ context.Context, product productapplication.ProductContext) ([]productapplication.Application, error) {
	s.listCalls++
	s.listProduct = product
	return s.listResult, s.listErr
}

func (s *serviceStub) ReplaceRedirects(_ context.Context, command productapplication.ReplaceRedirectsCommand) (productapplication.RedirectPolicyVersion, error) {
	s.redirectCalls++
	s.redirectCommand = command
	return s.redirectResult, s.redirectErr
}

func (s *serviceStub) SuspendApplication(_ context.Context, command productapplication.SuspendCommand) (productapplication.SuspendResult, error) {
	s.suspendCalls++
	s.suspendCommand = command
	return s.suspendResult, s.suspendErr
}

type authenticatorStub struct {
	principal adminrequest.Principal
	err       error
	proofs    []bool
}

func (s *authenticatorStub) Authenticate(_ context.Context, _ *http.Request, proof bool) (adminrequest.Principal, error) {
	s.proofs = append(s.proofs, proof)
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

func TestHandlerListsProductScopedApplications(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	service := &serviceStub{listResult: []productapplication.Application{{ApplicationID: "app-1", ProductID: "prod-1", ApplicationCode: "web", Name: "Web", Platform: productapplication.PlatformWeb, DistributionChannel: "official", ReleaseTrack: productapplication.ReleaseTrackStable, Status: productapplication.StatusActive, ContextVersion: 2, CreatedAt: now, UpdatedAt: now}}}
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}}
	authorizer := &authorizerStub{decision: adminrequest.Decision{Allowed: true}}
	recorder := serve(handler(service, auth, authorizer), http.MethodGet, "/api/v1/admin/products/prod-1/applications", "", nil)
	if recorder.Code != http.StatusOK || service.listCalls != 1 || service.listProduct.ProductID != "prod-1" || service.listProduct.Environment != productapplication.EnvironmentProduction {
		t.Fatalf("status=%d calls=%d product=%+v body=%s", recorder.Code, service.listCalls, service.listProduct, recorder.Body.String())
	}
	if authorizer.permission != productReadPermission || authorizer.target.ProductID != "prod-1" || len(auth.proofs) != 1 || auth.proofs[0] {
		t.Fatalf("authorization permission=%q target=%+v proofs=%+v", authorizer.permission, authorizer.target, auth.proofs)
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || len(body.Items) != 1 || body.Items[0]["application_code"] != "web" {
		t.Fatalf("response body = %s, error = %v", recorder.Body.String(), err)
	}
	if _, exists := body.Items[0]["audit_id"]; exists {
		t.Fatalf("list response leaked create-only audit field: %s", recorder.Body.String())
	}
}

func TestHandlerCreatesApplicationWithGuardIdempotencyAndRequestID(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	service := &serviceStub{createResult: productapplication.Application{ApplicationID: "app-1", ProductID: "prod-1", ApplicationCode: "web", Name: "Web", Platform: productapplication.PlatformWeb, DistributionChannel: "official", ReleaseTrack: productapplication.ReleaseTrackStable, Status: productapplication.StatusActive, CreatedAt: now, AuditID: "audit-1"}}
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}}
	authorizer := &authorizerStub{decision: adminrequest.Decision{Allowed: true}}
	headers := map[string]string{"Idempotency-Key": "idem-create-000001", requestid.Header: "request-create-001"}
	recorder := serve(handler(service, auth, authorizer), http.MethodPost, "/api/v1/admin/products/prod-1/applications", `{"application_code":"web","name":"Web","platform":"web","distribution_channel":"official","release_track":"stable","status":"active"}`, headers)
	if recorder.Code != http.StatusCreated || service.createCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.createCalls, recorder.Body.String())
	}
	command := service.createCommand
	if command.Product.ProductID != "prod-1" || command.ActorID != "admin-1" || command.TraceID != "request-create-001" || command.IdempotencyKey != headers["Idempotency-Key"] {
		t.Fatalf("command = %+v", command)
	}
	if authorizer.permission != applicationManagePermission || authorizer.target.ProductID != "prod-1" || len(auth.proofs) != 1 || !auth.proofs[0] {
		t.Fatalf("authorization permission=%q target=%+v proofs=%+v", authorizer.permission, authorizer.target, auth.proofs)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body["audit_id"] != "audit-1" || body["application_id"] != "app-1" {
		t.Fatalf("response=%s error=%v", recorder.Body.String(), err)
	}
	for _, forbidden := range []string{"context_version", "current_redirect_policy_version", "updated_at"} {
		if _, exists := body[forbidden]; exists {
			t.Fatalf("response contains non-contract field %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestHandlerRedirectsRequireRecentAuthentication(t *testing.T) {
	service := &serviceStub{}
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}}
	authorizer := &authorizerStub{decision: adminrequest.Decision{Allowed: true, ReauthenticationRequired: true}}
	recorder := serve(handler(service, auth, authorizer), http.MethodPut, "/api/v1/admin/products/prod-1/applications/app-1/redirects", `{"web_redirect_uris":[],"allowed_origins":[],"deep_links":[]}`, nil)
	if recorder.Code != http.StatusForbidden || service.redirectCalls != 0 || authorizer.permission != applicationSecurityManagePermission {
		t.Fatalf("status=%d calls=%d permission=%q body=%s", recorder.Code, service.redirectCalls, authorizer.permission, recorder.Body.String())
	}
	assertErrorCode(t, recorder, "admin_auth.reauthentication_required", "")
}

func TestHandlerRedirectPolicyRequiresNonNullBaseArrays(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "web redirects omitted", body: `{"allowed_origins":[],"deep_links":[]}`},
		{name: "origins omitted", body: `{"web_redirect_uris":[],"deep_links":[]}`},
		{name: "deep links omitted", body: `{"web_redirect_uris":[],"allowed_origins":[]}`},
		{name: "web redirects null", body: `{"web_redirect_uris":null,"allowed_origins":[],"deep_links":[]}`},
		{name: "origins null", body: `{"web_redirect_uris":[],"allowed_origins":null,"deep_links":[]}`},
		{name: "deep links null", body: `{"web_redirect_uris":[],"allowed_origins":[],"deep_links":null}`},
		{name: "optional auth targets null", body: `{"web_redirect_uris":[],"allowed_origins":[],"deep_links":[],"auth_return_targets":null}`},
		{name: "deep link unknown field", body: `{"web_redirect_uris":[],"allowed_origins":[],"deep_links":[{"scheme":"myapp","path_pattern":"/callback","extra":true}]}`},
		{name: "array over max", body: `{"web_redirect_uris":[` + strings.Repeat(`"https://example.com/callback",`, 100) + `"https://example.com/callback"],"allowed_origins":[],"deep_links":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &serviceStub{}
			recorder := serve(handler(service, &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}}, &authorizerStub{decision: adminrequest.Decision{Allowed: true}}), http.MethodPut, "/api/v1/admin/products/prod-1/applications/app-1/redirects", test.body, nil)
			if recorder.Code != http.StatusBadRequest || service.redirectCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.redirectCalls, recorder.Body.String())
			}
			assertErrorCode(t, recorder, "product_application.invalid_request", "")
		})
	}
}

func TestHandlerReplacesRedirectsAndSuspends(t *testing.T) {
	service := &serviceStub{redirectResult: productapplication.RedirectPolicyVersion{ProductID: "prod-1", ApplicationID: "app-1", Version: 3, AuditID: "audit-redir"}, suspendResult: productapplication.SuspendResult{ApplicationID: "app-1", Status: productapplication.StatusSuspended, SessionPolicy: productapplication.SessionPolicyRevokeExisting, AffectedClientBindings: 2, AuditID: "audit-suspend"}}
	auth := &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}}
	authorizer := &authorizerStub{decision: adminrequest.Decision{Allowed: true}}
	h := handler(service, auth, authorizer)
	redirect := serve(h, http.MethodPut, "/api/v1/admin/products/prod-1/applications/app-1/redirects", `{"web_redirect_uris":["https://example.com/callback"],"allowed_origins":["https://example.com"],"deep_links":[{"scheme":"myapp","path_pattern":"/callback"}],"auth_return_targets":[{"code":"login.complete","uri":"https://example.com/callback"}]}`, nil)
	if redirect.Code != http.StatusOK || service.redirectCalls != 1 || service.redirectCommand.ApplicationID != "app-1" || service.redirectCommand.TraceID == "" {
		t.Fatalf("redirect status=%d command=%+v body=%s", redirect.Code, service.redirectCommand, redirect.Body.String())
	}
	if len(service.redirectCommand.Policy.AuthReturnTargets) != 1 || service.redirectCommand.Policy.AuthReturnTargets[0].Code != "login.complete" {
		t.Fatalf("auth return targets = %+v", service.redirectCommand.Policy.AuthReturnTargets)
	}
	var redirectBody map[string]any
	_ = json.Unmarshal(redirect.Body.Bytes(), &redirectBody)
	if redirectBody["version"] != float64(3) || redirectBody["audit_id"] != "audit-redir" {
		t.Fatalf("redirect response = %s", redirect.Body.String())
	}
	suspend := serve(h, http.MethodPost, "/api/v1/admin/products/prod-1/applications/app-1/suspend", `{"reason":"security","session_policy":"revoke_existing"}`, map[string]string{"Idempotency-Key": "idem-suspend-00001"})
	if suspend.Code != http.StatusOK || service.suspendCalls != 1 || service.suspendCommand.IdempotencyKey != "idem-suspend-00001" {
		t.Fatalf("suspend status=%d command=%+v body=%s", suspend.Code, service.suspendCommand, suspend.Body.String())
	}
	var suspendBody map[string]any
	_ = json.Unmarshal(suspend.Body.Bytes(), &suspendBody)
	if suspendBody["affected_count"] != float64(2) || suspendBody["audit_id"] != "audit-suspend" {
		t.Fatalf("suspend response = %s", suspend.Body.String())
	}
}

func TestHandlerStrictRequestParsingAndStableErrors(t *testing.T) {
	tests := []struct {
		name, method, target, body string
		headers                    map[string]string
		status                     int
		code, allow                string
	}{
		{name: "unknown field", method: http.MethodPost, target: "/api/v1/admin/products/prod-1/applications", body: `{"application_code":"web","name":"Web","platform":"web","distribution_channel":"official","release_track":"stable","status":"active","extra":true}`, headers: map[string]string{"Idempotency-Key": "idem-create-000001"}, status: 400, code: "product_application.invalid_request"},
		{name: "multiple json", method: http.MethodPost, target: "/api/v1/admin/products/prod-1/applications", body: `{}` + `{}`, headers: map[string]string{"Idempotency-Key": "idem-create-000001"}, status: 400, code: "product_application.invalid_request"},
		{name: "missing idempotency", method: http.MethodPost, target: "/api/v1/admin/products/prod-1/applications", body: `{}`, status: 400, code: "product_application.invalid_idempotency_key"},
		{name: "query unsupported", method: http.MethodGet, target: "/api/v1/admin/products/prod-1/applications?limit=10", status: 400, code: "product_application.invalid_query"},
		{name: "encoded path", method: http.MethodGet, target: "/api/v1/admin/products/%70rod-1/applications", status: 404, code: "route_not_found"},
		{name: "trailing slash", method: http.MethodGet, target: "/api/v1/admin/products/prod-1/applications/", status: 404, code: "route_not_found"},
		{name: "method", method: http.MethodPatch, target: "/api/v1/admin/products/prod-1/applications", status: 405, code: "method_not_allowed", allow: "GET, POST"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &serviceStub{}
			h := handler(service, &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1"}}, &authorizerStub{decision: adminrequest.Decision{Allowed: true}})
			recorder := serve(h, test.method, test.target, test.body, test.headers)
			if recorder.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.status, recorder.Body.String())
			}
			assertErrorCode(t, recorder, test.code, test.allow)
		})
	}
}

func TestHandlerMapsDomainErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{productapplication.ErrInvalidArgument, 400, "product_application.invalid_request"},
		{productapplication.ErrNotFound, 404, "product_application.not_found"},
		{productapplication.ErrConflict, 409, "product_application.conflict"},
		{productapplication.ErrIdempotencyConflict, 409, "product_application.idempotency_conflict"},
		{productapplication.ErrOperationInProgress, 409, "product_application.operation_in_progress"},
		{errors.New("database unavailable"), 500, "internal_error"},
	}
	for _, test := range cases {
		service := &serviceStub{createErr: test.err}
		h := handler(service, &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1"}}, &authorizerStub{decision: adminrequest.Decision{Allowed: true}})
		recorder := serve(h, http.MethodPost, "/api/v1/admin/products/prod-1/applications", `{"application_code":"web","name":"Web","platform":"web","distribution_channel":"official","release_track":"stable","status":"active"}`, map[string]string{"Idempotency-Key": "idem-create-000001"})
		if recorder.Code != test.status {
			t.Fatalf("error=%v status=%d body=%s", test.err, recorder.Code, recorder.Body.String())
		}
		assertErrorCode(t, recorder, test.code, "")
	}
}

func TestHandlerRejectsBodyLargerThanOneMiB(t *testing.T) {
	service := &serviceStub{}
	h := handler(service, &authenticatorStub{principal: adminrequest.Principal{AdminUserID: "admin-1"}}, &authorizerStub{decision: adminrequest.Decision{Allowed: true}})
	body := `{"application_code":"web","name":"` + strings.Repeat("a", maxRequestBody) + `","platform":"web","distribution_channel":"official","release_track":"stable","status":"active"}`
	recorder := serve(h, http.MethodPost, "/api/v1/admin/products/prod-1/applications", body, map[string]string{"Idempotency-Key": "idem-create-000001"})
	if recorder.Code != http.StatusBadRequest || service.createCalls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.createCalls, recorder.Body.String())
	}
}

func handler(service Service, auth *authenticatorStub, authorizer *authorizerStub) http.Handler {
	guard := adminrequest.New(auth, authorizer, nil)
	return New(service, guard, Config{Environment: productapplication.EnvironmentProduction})
}

func serve(handler http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	requestid.Middleware(handler).ServeHTTP(recorder, request)
	return recorder
}

func assertErrorCode(t *testing.T, recorder *httptest.ResponseRecorder, code, allow string) {
	t.Helper()
	var body struct {
		Code      string `json:"code"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != code || body.RequestID == "" {
		t.Fatalf("error response=%s decode=%v", recorder.Body.String(), err)
	}
	if allow != "" && recorder.Header().Get("Allow") != allow {
		t.Fatalf("Allow=%q want=%q", recorder.Header().Get("Allow"), allow)
	}
}
