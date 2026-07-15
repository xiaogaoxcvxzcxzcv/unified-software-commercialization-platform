package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/modules/audit"
	audithttp "platform.local/capability-platform/backend/internal/modules/audit/httptransport"
	"platform.local/capability-platform/backend/internal/modules/identity"
)

type auditRepositoryStub struct {
	event audit.Event
}

func (r *auditRepositoryStub) Append(_ context.Context, event audit.Event) error {
	r.event = event
	return nil
}

func TestIdentityAuditAdapterMapsEverySecurityField(t *testing.T) {
	repository := &auditRepositoryStub{}
	adapter := identityAuditAdapter{service: audit.NewService(repository)}
	occurredAt := time.Date(2026, 7, 13, 12, 30, 0, 0, time.UTC)
	securityEvent := identity.SecurityEvent{
		AuditID:         "aud-1",
		OccurredAt:      occurredAt,
		ActorID:         "usr-1",
		Permission:      "identity.manage",
		ScopeType:       "tenant",
		ScopeID:         "tenant-1",
		ProductID:       "product-1",
		TenantID:        "tenant-1",
		Action:          "admin.auth.session_revoked",
		TargetType:      "session",
		TargetID:        "session-digest",
		Result:          "success",
		ReasonCode:      "logout",
		TraceID:         "trace-1",
		RiskLevel:       "high",
		RedactedSummary: map[string]any{"transport": "cookie"},
	}

	id, err := adapter.AppendSecurityEvent(context.Background(), securityEvent)
	if err != nil {
		t.Fatal(err)
	}
	if id != securityEvent.AuditID {
		t.Fatalf("audit ID = %q, want %q", id, securityEvent.AuditID)
	}
	want := audit.Event{
		AuditID:         securityEvent.AuditID,
		OccurredAt:      securityEvent.OccurredAt,
		ActorID:         securityEvent.ActorID,
		Permission:      securityEvent.Permission,
		ScopeType:       securityEvent.ScopeType,
		ScopeID:         securityEvent.ScopeID,
		ProductID:       securityEvent.ProductID,
		TenantID:        securityEvent.TenantID,
		Action:          securityEvent.Action,
		TargetType:      securityEvent.TargetType,
		TargetID:        securityEvent.TargetID,
		Result:          securityEvent.Result,
		ReasonCode:      securityEvent.ReasonCode,
		TraceID:         securityEvent.TraceID,
		RiskLevel:       securityEvent.RiskLevel,
		RedactedSummary: securityEvent.RedactedSummary,
	}
	if !reflect.DeepEqual(repository.event, want) {
		t.Fatalf("mapped event = %#v, want %#v", repository.event, want)
	}
}

type adminSessionResolverStub struct {
	session identity.AdminSession
	err     error
}

func (s adminSessionResolverStub) CurrentAdminSession(context.Context, string) (identity.AdminSession, error) {
	return s.session, s.err
}

func TestAuditAdminContextAdapterUsesVerifiedSessionAndFailsClosedOnAmbiguousScope(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://api.example.test/api/v1/admin/audit/events", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-platform_admin_access", Value: "opaque-access"})
	adapter := auditAdminContextAdapter{identity: adminSessionResolverStub{session: identity.AdminSession{
		SessionID: "session-1",
		Admin:     identity.AdminIdentitySummary{AdminUserID: "admin-1"},
		Authorization: accesscontrol.Snapshot{Scopes: []accesscontrol.Scope{
			{Type: "platform"},
		}},
	}}}
	contextValue, err := adapter.ResolveAdminContext(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if contextValue.AdminUserID != "admin-1" || contextValue.SessionID != "session-1" || contextValue.TargetScope.Type != "platform" {
		t.Fatalf("context = %#v", contextValue)
	}

	adapter.identity = adminSessionResolverStub{session: identity.AdminSession{
		SessionID: "session-2", Admin: identity.AdminIdentitySummary{AdminUserID: "admin-2"},
		Authorization: accesscontrol.Snapshot{Scopes: []accesscontrol.Scope{
			{Type: "product", ID: "product-a", ProductID: "product-a"},
			{Type: "product", ID: "product-b", ProductID: "product-b"},
		}},
	}}
	if _, err := adapter.ResolveAdminContext(context.Background(), request); !errors.Is(err, audithttp.ErrAdminContextUnavailable) {
		t.Fatalf("ambiguous scope error = %v", err)
	}
}

func TestAuditAdminContextAdapterRejectsMissingAccessProof(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://api.example.test/api/v1/admin/audit/events", nil)
	adapter := auditAdminContextAdapter{identity: adminSessionResolverStub{}}
	if _, err := adapter.ResolveAdminContext(context.Background(), request); !errors.Is(err, audithttp.ErrAdminContextUnavailable) {
		t.Fatalf("missing proof error = %v", err)
	}
}
