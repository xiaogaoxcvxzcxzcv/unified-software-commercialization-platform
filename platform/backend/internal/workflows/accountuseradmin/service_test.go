package accountuseradmin

import (
	"context"
	"errors"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
)

type capabilityStub struct {
	enabled bool
	calls   int
}

func (s *capabilityStub) IsPackageEnabled(context.Context, string, string) (bool, error) {
	s.calls++
	return s.enabled, nil
}

type membershipStub struct {
	calls int
	err   error
}

func (s *membershipStub) VerifyMember(context.Context, Scope, string) error { s.calls++; return s.err }

type accessStub struct{ productCalls, tenantCalls int }

func (s *accessStub) SetProductAccessStatus(_ context.Context, c productuseraccess.SetProductAccessStatusCommand) (productuseraccess.StatusChangeResult, error) {
	s.productCalls++
	return productuseraccess.StatusChangeResult{ProductID: c.Product.ProductID, UserID: c.User.UserID, Status: c.Status, AccessVersion: c.ExpectedVersion + 1, AuditID: "audit-product"}, nil
}
func (s *accessStub) SetTenantAccessStatus(_ context.Context, c productuseraccess.SetTenantAccessStatusCommand) (productuseraccess.StatusChangeResult, error) {
	s.tenantCalls++
	return productuseraccess.StatusChangeResult{ProductID: c.Product.ProductID, TenantID: c.Tenant.TenantID, UserID: c.User.UserID, Status: c.Status, AccessVersion: c.ExpectedVersion + 1, AuditID: "audit-tenant"}, nil
}

type identityStub struct{ securityCalls, revokeCalls int }

func (s *identityStub) SetGlobalUserSecurityStatus(context.Context, GlobalSecurityCommand) (SecurityMutationResult, error) {
	s.securityCalls++
	return SecurityMutationResult{UserID: "user-a", Status: "disabled", Version: 2, AuditID: "audit-global"}, nil
}
func (s *identityStub) RevokeAdminUserSessions(context.Context, SessionRevocationCommand) (SessionRevocationResult, error) {
	s.revokeCalls++
	return SessionRevocationResult{UserID: "user-a", RevokedCount: 1, AuditID: "audit-revoke"}, nil
}

func TestScopedWritesCheckCapabilityAndMembershipBeforeAccess(t *testing.T) {
	capability := &capabilityStub{}
	membership := &membershipStub{}
	access := &accessStub{}
	service := New(&identityStub{}, access, membership, capability)
	_, err := service.SetProductAccessStatus(context.Background(), Scope{Type: ScopeProduct, ProductID: "product-a"}, "user-a", productuseraccess.StatusSuspended, 0, "security.review", "", "admin-a", "trace-a", "idempotency-key-0001")
	if !errors.Is(err, ErrCapabilityNotEnabled) || membership.calls != 0 || access.productCalls != 0 {
		t.Fatalf("disabled capability error=%v membership=%d access=%d", err, membership.calls, access.productCalls)
	}
	capability.enabled = true
	membership.err = errors.New("not a member")
	_, err = service.SetProductAccessStatus(context.Background(), Scope{Type: ScopeProduct, ProductID: "product-a"}, "user-a", productuseraccess.StatusSuspended, 0, "security.review", "", "admin-a", "trace-a", "idempotency-key-0001")
	if !errors.Is(err, ErrScopedUserNotFound) || access.productCalls != 0 {
		t.Fatalf("membership error=%v access=%d", err, access.productCalls)
	}
	membership.err = nil
	result, err := service.SetProductAccessStatus(context.Background(), Scope{Type: ScopeProduct, ProductID: "product-a"}, "user-a", productuseraccess.StatusSuspended, 0, "security.review", "", "admin-a", "trace-a", "idempotency-key-0001")
	if err != nil || result.AuditID != "audit-product" || access.productCalls != 1 {
		t.Fatalf("result=%+v error=%v calls=%d", result, err, access.productCalls)
	}
}

func TestGlobalSecurityAndScopedSessionRevokeUseIdentityPort(t *testing.T) {
	identity := &identityStub{}
	capability := &capabilityStub{enabled: true}
	membership := &membershipStub{}
	service := New(identity, &accessStub{}, membership, capability)
	result, err := service.SetGlobalSecurityStatus(context.Background(), GlobalSecurityCommand{UserID: "user-a", Status: "disabled", ExpectedVersion: 1, ReasonCode: "security.review", ActorID: "admin-a", TraceID: "trace-a", IdempotencyKey: "idempotency-key-0001"})
	if err != nil || result.AuditID != "audit-global" || identity.securityCalls != 1 {
		t.Fatalf("global result=%+v error=%v calls=%d", result, err, identity.securityCalls)
	}
	revoked, err := service.RevokeSessions(context.Background(), SessionRevocationCommand{Scope: Scope{Type: ScopeProduct, ProductID: "product-a"}, UserID: "user-a", AllActive: true, ReasonCode: "security.review", ActorID: "admin-a", TraceID: "trace-b", IdempotencyKey: "idempotency-key-0002"})
	if err != nil || revoked.AuditID != "audit-revoke" || identity.revokeCalls != 1 || membership.calls != 1 {
		t.Fatalf("revoke=%+v error=%v identity=%d membership=%d", revoked, err, identity.revokeCalls, membership.calls)
	}
	_, err = service.RevokeSessions(context.Background(), SessionRevocationCommand{Scope: Scope{Type: ScopeProduct, ProductID: "product-a"}, UserID: "user-a", AllActive: true, SessionIDs: []string{"session-a"}, ReasonCode: "security.review", ActorID: "admin-a", TraceID: "trace-c", IdempotencyKey: "idempotency-key-0003"})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("mutually exclusive revoke error=%v", err)
	}
}
