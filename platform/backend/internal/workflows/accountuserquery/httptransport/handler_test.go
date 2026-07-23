package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/workflows/accountuserquery"
)

type identityStub struct{}

func (identityStub) ListUsers(context.Context, accountuserquery.IdentityListQuery) ([]accountuserquery.IdentityUser, error) {
	return []accountuserquery.IdentityUser{{UserID: "user-a", UserVersion: 1, AccountStatus: "active", CreatedAt: timeNow(), Position: "position-a"}}, nil
}
func (identityStub) GetUser(context.Context, accountuserquery.Scope, string) (accountuserquery.IdentityUserDetail, error) {
	return accountuserquery.IdentityUserDetail{User: accountuserquery.IdentityUser{UserID: "user-a", UserVersion: 1, AccountStatus: "active", CreatedAt: timeNow(), Position: "position-a"}}, nil
}
func (identityStub) ListSessions(context.Context, accountuserquery.IdentitySessionQuery) ([]accountuserquery.IdentitySession, error) {
	return nil, nil
}

type accessStub struct{}

func (accessStub) GetScopedAccessBatch(context.Context, productuseraccess.GetScopedAccessBatchQuery) ([]productuseraccess.ScopedAccess, error) {
	return nil, nil
}

type capabilityStub struct{}

func (capabilityStub) IsPackageEnabled(context.Context, string, string) (bool, error) {
	return true, nil
}

type authStub struct{}

func (authStub) Authenticate(context.Context, *http.Request, bool) (adminrequest.Principal, error) {
	return adminrequest.Principal{AdminUserID: "admin-a", SessionID: "session-a", AuthTime: timeNow()}, nil
}

type authorizerStub struct{}

func (authorizerStub) Authorize(context.Context, adminrequest.Principal, string, adminrequest.TargetScope) (adminrequest.Decision, error) {
	return adminrequest.Decision{Allowed: true}, nil
}

type denialStub struct{}

func (denialStub) RecordAuthorizationDenial(context.Context, adminrequest.Denial) error { return nil }

func TestHandlerRejectsUnknownAndDuplicateFilters(t *testing.T) {
	h := New(accountuserquery.New(identityStub{}, accessStub{}, capabilityStub{}, []byte("0123456789abcdef0123456789abcdef")), adminrequest.New(authStub{}, authorizerStub{}, denialStub{}))
	for _, target := range []string{"/api/v1/admin/users?query=user&unknown=x", "/api/v1/admin/users?page_size=20&page_size=21"} {
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("target=%s status=%d body=%s", target, recorder.Code, recorder.Body.String())
		}
	}
}

func TestHandlerRejectsEncodedAndTrailingRoutes(t *testing.T) {
	h := New(accountuserquery.New(identityStub{}, accessStub{}, capabilityStub{}, []byte("0123456789abcdef0123456789abcdef")), adminrequest.New(authStub{}, authorizerStub{}, denialStub{}))
	for _, target := range []string{"/api/v1/admin/products/%70roduct-a/users", "/api/v1/admin/users/"} {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.URL.RawPath = "/encoded"
		h.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("target=%s status=%d", target, recorder.Code)
		}
	}
}

func timeNow() time.Time { return time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC) }
