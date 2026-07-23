package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

type adminEndUserRepositoryStub struct {
	query            AdminUserRepositoryQuery
	users            []AdminUserRecord
	user             AdminUserRecord
	memberships      map[string]bool
	sessions         []AdminUserSessionRecord
	statusRecord     AdminGlobalSecurityStatusRecord
	revocationRecord AdminSessionRevocationRecord
}

func (s *adminEndUserRepositoryStub) ListAdminUsers(_ context.Context, query AdminUserRepositoryQuery) ([]AdminUserRecord, error) {
	s.query = query
	return append([]AdminUserRecord(nil), s.users...), nil
}
func (s *adminEndUserRepositoryStub) GetAdminUser(_ context.Context, _ AdminUserScope, _ string) (AdminUserRecord, error) {
	return s.user, nil
}
func (s *adminEndUserRepositoryStub) ResolveAdminUserMemberships(_ context.Context, _ AdminUserScope, _ []string) (map[string]bool, error) {
	return s.memberships, nil
}
func (s *adminEndUserRepositoryStub) ListAdminUserSessions(_ context.Context, _ AdminUserSessionQuery) ([]AdminUserSessionRecord, error) {
	return append([]AdminUserSessionRecord(nil), s.sessions...), nil
}
func (s *adminEndUserRepositoryStub) SetGlobalUserSecurityStatus(_ context.Context, record AdminGlobalSecurityStatusRecord) (AdminGlobalSecurityStatusResult, error) {
	s.statusRecord = record
	return AdminGlobalSecurityStatusResult{UserID: record.UserID, Status: record.Status, Version: 2, AuditID: record.OutboxEvent.Payload.AuditID}, nil
}
func (s *adminEndUserRepositoryStub) RevokeAdminUserSessions(_ context.Context, record AdminSessionRevocationRecord) (AdminSessionRevocationResult, error) {
	s.revocationRecord = record
	return AdminSessionRevocationResult{UserID: record.UserID, ScopeType: record.Scope.Type, RevokedCount: 1, AuditID: record.OutboxEvent.Payload.AuditID}, nil
}

type adminEndUserIDs struct{ next int }

func (g *adminEndUserIDs) ID(prefix string) (string, error) {
	g.next++
	return fmt.Sprintf("%s%032d", prefix, g.next), nil
}

func newAdminEndUserServiceForTest(t *testing.T, repository AdminEndUserRepository) *AdminEndUserService {
	t.Helper()
	hasher, err := securevalue.NewHasher(strings.Repeat("admin-end-user-pepper-", 2))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewAdminEndUserService(repository, StrictIdentifierNormalizer{}, hasher, &adminEndUserIDs{}, func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestAdminEndUserListBuildsIdentifierSearchAndStablePosition(t *testing.T) {
	repository := &adminEndUserRepositoryStub{users: []AdminUserRecord{{UserID: "user-a"}, {UserID: "user-b"}, {UserID: "user-c"}}}
	service := newAdminEndUserServiceForTest(t, repository)
	page, err := service.ListUsers(context.Background(), AdminUserQuery{Scope: AdminUserScope{Type: AdminUserScopeProduct, ProductID: "product-a"}, Query: "USER@EXAMPLE.COM", AccountStatus: "active", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Position != "user-a" || page.NextAfterUserID != "user-b" {
		t.Fatalf("page = %#v", page)
	}
	if repository.query.Limit != 3 || repository.query.IdentifierType != IdentifierEmail || len(repository.query.IdentifierDigest) != 32 || repository.query.Text != "USER@EXAMPLE.COM" {
		t.Fatalf("repository query = %#v", repository.query)
	}
}

func TestAdminEndUserWritesUseTrustedScopeAndRedactedOutbox(t *testing.T) {
	repository := &adminEndUserRepositoryStub{}
	service := newAdminEndUserServiceForTest(t, repository)
	result, err := service.SetGlobalSecurityStatus(context.Background(), AdminGlobalSecurityStatusCommand{UserID: "user-a", Status: "disabled", ExpectedVersion: 1, ReasonCode: "security.review", OperatorNote: "private operator context", IdempotencyKey: "global-security-key-0001", ActorID: "admin-a", TraceID: "trace-global-status"})
	if err != nil {
		t.Fatal(err)
	}
	if result.AuditID == "" || repository.statusRecord.OutboxEvent.Payload.Permission != "identity.security.manage" || repository.statusRecord.OutboxEvent.Payload.ScopeType != "platform" {
		t.Fatalf("status record = %#v", repository.statusRecord)
	}
	encoded := fmt.Sprintf("%#v", repository.statusRecord.OutboxEvent.Payload)
	if strings.Contains(encoded, "private operator context") {
		t.Fatal("operator note leaked to outbox payload")
	}

	_, err = service.RevokeSessions(context.Background(), AdminSessionRevocationCommand{Scope: AdminUserScope{Type: AdminUserScopeTenant, ProductID: "product-a", TenantID: "tenant-a"}, UserID: "user-a", AllActive: true, ReasonCode: "security.review", IdempotencyKey: "scoped-revoke-key-0001", ActorID: "admin-a", TraceID: "trace-revoke"})
	if err != nil {
		t.Fatal(err)
	}
	if repository.revocationRecord.OutboxEvent.Payload.Permission != "product.user-access.manage" || repository.revocationRecord.OutboxEvent.Payload.ScopeType != "tenant" || repository.revocationRecord.OutboxEvent.Payload.ProductID != "product-a" || repository.revocationRecord.OutboxEvent.Payload.TenantID != "tenant-a" {
		t.Fatalf("revocation record = %#v", repository.revocationRecord)
	}
}

func TestAdminEndUserRejectsInvalidScopeAndSessionSelection(t *testing.T) {
	service := newAdminEndUserServiceForTest(t, &adminEndUserRepositoryStub{})
	_, err := service.ListUsers(context.Background(), AdminUserQuery{Scope: AdminUserScope{Type: AdminUserScopeProduct}, Limit: 20})
	if !errors.Is(err, ErrAdminEndUserInvalidArgument) {
		t.Fatalf("invalid scope error = %v", err)
	}
	_, err = service.RevokeSessions(context.Background(), AdminSessionRevocationCommand{Scope: AdminUserScope{Type: AdminUserScopeProduct, ProductID: "product-a"}, UserID: "user-a", SessionIDs: []string{"session-a"}, AllActive: true, ReasonCode: "security.review", IdempotencyKey: "scoped-revoke-key-0001", ActorID: "admin-a", TraceID: "trace-revoke"})
	if !errors.Is(err, ErrAdminEndUserInvalidArgument) {
		t.Fatalf("invalid selection error = %v", err)
	}
}
