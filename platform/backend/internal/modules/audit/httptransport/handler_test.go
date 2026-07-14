package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/audit"
)

type resolverStub struct {
	admin audit.AdminContext
	err   error
	calls int
}

func (s *resolverStub) ResolveAdminContext(context.Context, *http.Request) (audit.AdminContext, error) {
	s.calls++
	return s.admin, s.err
}

type serviceStub struct {
	command audit.SearchCommand
	page    audit.Page
	err     error
	calls   int
}

func (s *serviceStub) SearchAuditEvents(_ context.Context, command audit.SearchCommand) (audit.Page, error) {
	s.calls++
	s.command = command
	return s.page, s.err
}

func TestHandlerResolvesAdminAndParsesQuery(t *testing.T) {
	resolver := &resolverStub{admin: audit.AdminContext{AdminUserID: "admin-1", SessionID: "session-1", TargetScope: audit.Scope{Type: "product", ProductID: "product-1"}}}
	service := &serviceStub{page: audit.Page{Items: []audit.RedactedEvent{{AuditID: "aud-1"}}, NextCursor: "next"}}
	handler := New(service, resolver)
	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/admin/audit/events?trace_id=trace-1&limit=25&cursor=opaque", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if resolver.calls != 1 || service.calls != 1 {
		t.Fatalf("calls: resolver=%d service=%d", resolver.calls, service.calls)
	}
	if service.command.AdminUserID != "admin-1" || service.command.TargetScope.ProductID != "product-1" || service.command.TraceID != "trace-1" || service.command.Limit != 25 || service.command.Cursor != "opaque" {
		t.Fatalf("command = %+v", service.command)
	}
	var page audit.Page
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.NextCursor != "next" {
		t.Fatalf("page = %+v", page)
	}
}

func TestHandlerRejectsInvalidLimitBeforeIdentityResolution(t *testing.T) {
	resolver := &resolverStub{}
	service := &serviceStub{}
	handler := New(service, resolver)
	for _, limit := range []string{"abc", "0", "-1", "201"} {
		req := httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/admin/audit/events?limit="+limit, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("limit %q status = %d", limit, recorder.Code)
		}
	}
	if resolver.calls != 0 || service.calls != 0 {
		t.Fatalf("invalid query reached dependencies: resolver=%d service=%d", resolver.calls, service.calls)
	}
}

func TestHandlerDoesNotTrustQueryForAdminScope(t *testing.T) {
	resolver := &resolverStub{admin: audit.AdminContext{AdminUserID: "admin", SessionID: "session", TargetScope: audit.Scope{Type: "tenant", ProductID: "server-product", TenantID: "server-tenant"}}}
	service := &serviceStub{}
	handler := New(service, resolver)
	req := httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/admin/audit/events?product_id=forged&tenant_id=forged", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if service.command.TargetScope.ProductID != "server-product" || service.command.TargetScope.TenantID != "server-tenant" {
		t.Fatalf("trusted scope = %+v", service.command.TargetScope)
	}
}

func TestHandlerMapsAuthenticationAndAuthorizationErrors(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		handler := New(&serviceStub{}, &resolverStub{err: ErrAdminContextUnavailable})
		req := httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/admin/audit/events", nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		assertProblem(t, recorder, http.StatusUnauthorized, "audit.unauthenticated")
	})
	t.Run("forbidden", func(t *testing.T) {
		handler := New(&serviceStub{err: audit.ErrForbidden}, &resolverStub{admin: audit.AdminContext{AdminUserID: "admin", SessionID: "session", TargetScope: audit.Scope{Type: "platform"}}})
		req := httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/admin/audit/events", nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		assertProblem(t, recorder, http.StatusForbidden, "audit.forbidden")
	})
	t.Run("transient", func(t *testing.T) {
		handler := New(&serviceStub{err: errors.New("database unavailable")}, &resolverStub{admin: audit.AdminContext{AdminUserID: "admin", SessionID: "session", TargetScope: audit.Scope{Type: "platform"}}})
		req := httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/admin/audit/events", nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		assertProblem(t, recorder, http.StatusInternalServerError, "internal_error")
	})
}

func TestHandlerRejectsMethodAndUnknownRoute(t *testing.T) {
	handler := New(&serviceStub{}, &resolverStub{})
	request := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/audit/events", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method response = %d allow=%q", recorder.Code, recorder.Header().Get("Allow"))
	}
	request = httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/admin/audit/other", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("route status = %d", recorder.Code)
	}
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
	if body["code"] != code {
		t.Fatalf("problem = %v", body)
	}
}
