package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/modules/identity"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
)

type adminIdentityStub struct {
	plain int
	csrf  int
	now   time.Time
}

func (s *adminIdentityStub) CurrentAdminSession(context.Context, string) (identity.AdminSession, error) {
	s.plain++
	return identity.AdminSession{SessionID: "session-1", Admin: identity.AdminIdentitySummary{AdminUserID: "admin-1", AuthTime: s.now}}, nil
}

func (s *adminIdentityStub) CurrentAdminSessionWithCSRF(context.Context, string, string) (identity.AdminSession, error) {
	s.csrf++
	return identity.AdminSession{SessionID: "session-1", Admin: identity.AdminIdentitySummary{AdminUserID: "admin-1", AuthTime: s.now}}, nil
}

type accessSnapshotRepositoryStub struct{ snapshot accesscontrol.Snapshot }

func (s accessSnapshotRepositoryStub) ResolveSnapshot(context.Context, string, time.Time) (accesscontrol.Snapshot, error) {
	return s.snapshot, nil
}
func (accessSnapshotRepositoryStub) BootstrapPlatformAdmin(context.Context, accesscontrol.BootstrapCommand, accesscontrol.PermissionCatalog) error {
	return nil
}
func (accessSnapshotRepositoryStub) BindAdminScope(context.Context, accesscontrol.ScopeBindingRecord) (accesscontrol.AdminScopeBinding, error) {
	return accesscontrol.AdminScopeBinding{}, nil
}
func (accessSnapshotRepositoryStub) ClaimOutbox(context.Context, time.Time, int) ([]accesscontrol.ClaimedOutboxEvent, error) {
	return nil, nil
}
func (accessSnapshotRepositoryStub) MarkOutboxPublished(context.Context, string, time.Time) error {
	return nil
}
func (accessSnapshotRepositoryStub) MarkOutboxFailed(context.Context, string, string, time.Time, bool) error {
	return nil
}

func TestAdminRequestAuthenticatorUsesCSRFPathForCookieWrites(t *testing.T) {
	now := time.Now().UTC()
	identityService := &adminIdentityStub{now: now}
	authenticator := newAdminRequestAuthenticator(identityService, []string{"https://admin.example.com"})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/products", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-platform_admin_access", Value: "opaque"})
	request.Header.Set("Origin", "https://admin.example.com")
	request.Header.Set("X-CSRF-Token", "csrf")
	principal, err := authenticator.Authenticate(context.Background(), request, true)
	if err != nil || principal.AdminUserID != "admin-1" || !principal.AuthTime.Equal(now) || identityService.csrf != 1 || identityService.plain != 0 {
		t.Fatalf("Authenticate() = %#v, %v; plain=%d csrf=%d", principal, err, identityService.plain, identityService.csrf)
	}
}

func TestAdminRequestAuthorizerRequiresRecentAuthenticationForHighRiskUse(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	repository := accessSnapshotRepositoryStub{snapshot: accesscontrol.Snapshot{
		AuthorizationVersion: 1,
		Permissions:          []string{"product.application.security.manage"},
		Scopes:               []accesscontrol.Scope{{Type: "platform"}},
	}}
	authorizer := adminRequestAuthorizer{
		access:       accesscontrol.NewService(repository, func() time.Time { return now }),
		now:          func() time.Time { return now },
		reauthWindow: 5 * time.Minute,
	}

	fresh, err := authorizer.Authorize(context.Background(), adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1", AuthTime: now.Add(-4 * time.Minute)}, "product.application.security.manage", adminrequest.TargetScope{Type: "platform"})
	if err != nil || !fresh.Allowed || fresh.ReauthenticationRequired {
		t.Fatalf("fresh decision = %#v, %v", fresh, err)
	}
	stale, err := authorizer.Authorize(context.Background(), adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1", AuthTime: now.Add(-6 * time.Minute)}, "product.application.security.manage", adminrequest.TargetScope{Type: "platform"})
	if err != nil || !stale.Allowed || !stale.ReauthenticationRequired {
		t.Fatalf("stale decision = %#v, %v", stale, err)
	}
	missing, err := authorizer.Authorize(context.Background(), adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}, "product.application.security.manage", adminrequest.TargetScope{Type: "platform"})
	if err != nil || !missing.ReauthenticationRequired {
		t.Fatalf("missing auth_time decision = %#v, %v", missing, err)
	}
}

func TestAdminPermissionRiskComesFromTrustedCatalog(t *testing.T) {
	if got := adminPermissionRisk("assembly.execute"); got != "high" {
		t.Fatalf("assembly.execute risk = %q, want high", got)
	}
	if got := adminPermissionRisk("assembly.lifecycle.execute"); got != "high" {
		t.Fatalf("assembly.lifecycle.execute risk = %q, want high", got)
	}
	if got := adminPermissionRisk("assembly.lifecycle.plan"); got != "normal" {
		t.Fatalf("assembly.lifecycle.plan risk = %q, want normal", got)
	}
	if got := adminPermissionRisk("unknown.permission"); got != "normal" {
		t.Fatalf("unknown permission risk = %q, want normal", got)
	}
}

func TestAdminRequestAuthenticatorRejectsUntrustedCookieOrigin(t *testing.T) {
	authenticator := newAdminRequestAuthenticator(&adminIdentityStub{}, []string{"https://admin.example.com"})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/products", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-platform_admin_access", Value: "opaque"})
	request.Header.Set("Origin", "https://other.example.com")
	if _, err := authenticator.Authenticate(context.Background(), request, true); err != adminrequest.ErrRequestProof {
		t.Fatalf("Authenticate() error = %v, want %v", err, adminrequest.ErrRequestProof)
	}
}
