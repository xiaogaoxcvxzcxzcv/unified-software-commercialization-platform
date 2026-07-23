package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/workflows/accountuseradmin"
)

type capabilityStub struct{ enabled bool }

func (s capabilityStub) IsPackageEnabled(context.Context, string, string) (bool, error) {
	return s.enabled, nil
}

type membershipStub struct {
	calls int
	err   error
}

func (s *membershipStub) VerifyMember(context.Context, accountuseradmin.Scope, string) error {
	s.calls++
	return s.err
}

type accessStub struct {
	productCalls int
	tenantCalls  int
}

func (s *accessStub) SetProductAccessStatus(context.Context, productuseraccess.SetProductAccessStatusCommand) (productuseraccess.StatusChangeResult, error) {
	s.productCalls++
	return productuseraccess.StatusChangeResult{ScopeType: productuseraccess.ScopeProduct, ProductID: "product-a", UserID: "user-a", Status: productuseraccess.StatusSuspended, AccessVersion: 1, AuditID: "audit-product"}, nil
}
func (s *accessStub) SetTenantAccessStatus(context.Context, productuseraccess.SetTenantAccessStatusCommand) (productuseraccess.StatusChangeResult, error) {
	s.tenantCalls++
	return productuseraccess.StatusChangeResult{ScopeType: productuseraccess.ScopeTenant, ProductID: "product-a", TenantID: "tenant-a", UserID: "user-a", Status: productuseraccess.StatusSuspended, AccessVersion: 1, AuditID: "audit-tenant"}, nil
}

type identityStub struct {
	securityCalls int
	revokeCalls   int
	securityErr   error
	revokeErr     error
}

func (s *identityStub) SetGlobalUserSecurityStatus(context.Context, accountuseradmin.GlobalSecurityCommand) (accountuseradmin.SecurityMutationResult, error) {
	s.securityCalls++
	if s.securityErr != nil {
		return accountuseradmin.SecurityMutationResult{}, s.securityErr
	}
	return accountuseradmin.SecurityMutationResult{UserID: "user-a", ScopeType: "platform", Status: "disabled", Version: 2, AuditID: "audit-global"}, nil
}
func (s *identityStub) RevokeAdminUserSessions(context.Context, accountuseradmin.SessionRevocationCommand) (accountuseradmin.SessionRevocationResult, error) {
	s.revokeCalls++
	if s.revokeErr != nil {
		return accountuseradmin.SessionRevocationResult{}, s.revokeErr
	}
	return accountuseradmin.SessionRevocationResult{UserID: "user-a", ScopeType: "product", RevokedCount: 1, AuditID: "audit-revoke"}, nil
}

type authStub struct{}

func (authStub) Authenticate(context.Context, *http.Request, bool) (adminrequest.Principal, error) {
	return adminrequest.Principal{AdminUserID: "admin-a", SessionID: "session-a", AuthTime: time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)}, nil
}

type authorizerStub struct{}

func (authorizerStub) Authorize(context.Context, adminrequest.Principal, string, adminrequest.TargetScope) (adminrequest.Decision, error) {
	return adminrequest.Decision{Allowed: true}, nil
}

type denialStub struct{}

func (denialStub) RecordAuthorizationDenial(context.Context, adminrequest.Denial) error { return nil }

func newHandler() (*Handler, *accessStub, *membershipStub, *identityStub) {
	access := &accessStub{}
	membership := &membershipStub{}
	identity := &identityStub{}
	service := accountuseradmin.New(identity, access, membership, capabilityStub{enabled: true})
	guard := adminrequest.New(authStub{}, authorizerStub{}, denialStub{})
	return New(service, guard), access, membership, identity
}

func TestHandlerRequiresMembershipBeforeScopedMutation(t *testing.T) {
	h, access, membership, _ := newHandler()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/products/product-a/users/user-a/access", strings.NewReader(`{"expected_version":0,"status":"suspended","reason_code":"security.review"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idempotency-key-0001")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || access.productCalls != 1 || membership.calls != 1 {
		t.Fatalf("status=%d access=%d membership=%d body=%s", recorder.Code, access.productCalls, membership.calls, recorder.Body.String())
	}
}

func TestHandlerGlobalSecurityRejectsUnknownAndMissingIdempotency(t *testing.T) {
	h, _, _, identity := newHandler()
	for index, body := range []string{`{"expected_version":1,"status":"disabled","reason_code":"security.review"}`, `{"expected_version":1,"status":"disabled","reason_code":"security.review","recent_auth_proof":"untrusted"}`} {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/user-a/security-status", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if index == 1 {
			req.Header.Set("Idempotency-Key", "idempotency-key-0001")
		}
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusBadRequest || identity.securityCalls != 0 {
			t.Fatalf("status=%d calls=%d body=%s", recorder.Code, identity.securityCalls, recorder.Body.String())
		}
	}
}

func TestHandlerRejectsEncodedAndTrailingMutationRoutes(t *testing.T) {
	h, _, _, _ := newHandler()
	for _, target := range []string{"/api/v1/admin/products/%70roduct-a/users/user-a/access", "/api/v1/admin/products/product-a/users/user-a/access/"} {
		req := httptest.NewRequest(http.MethodPut, target, strings.NewReader(`{}`))
		req.URL.RawPath = "/encoded"
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("target=%s status=%d", target, recorder.Code)
		}
	}
}

func TestHandlerMapsIdentityMutationErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "not found", err: accountuseradmin.ErrIdentityUserNotFound, want: http.StatusNotFound},
		{name: "conflict", err: accountuseradmin.ErrIdentityConflict, want: http.StatusConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h, _, _, identity := newHandler()
			identity.securityErr = test.err
			req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/user-a/security-status", strings.NewReader(`{"expected_version":1,"status":"disabled","reason_code":"security.review"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "idempotency-key-0001")
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, req)
			if recorder.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}
