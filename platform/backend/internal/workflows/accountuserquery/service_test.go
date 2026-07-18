package accountuserquery

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
)

var cursorKey = []byte("0123456789abcdef0123456789abcdef")

type identityStub struct {
	users       []IdentityUser
	detail      IdentityUserDetail
	sessions    []IdentitySession
	listCalls   int
	afterValues []string
	err         error
}

func (s *identityStub) ListUsers(_ context.Context, query IdentityListQuery) ([]IdentityUser, error) {
	s.listCalls++
	s.afterValues = append(s.afterValues, query.After)
	if s.err != nil {
		return nil, s.err
	}
	start := 0
	if query.After != "" {
		for index, user := range s.users {
			if user.Position == query.After {
				start = index + 1
				break
			}
		}
	}
	end := min(len(s.users), start+query.Limit)
	return append([]IdentityUser(nil), s.users[start:end]...), nil
}
func (s *identityStub) GetUser(context.Context, Scope, string) (IdentityUserDetail, error) {
	if s.err != nil {
		return IdentityUserDetail{}, s.err
	}
	return s.detail, nil
}
func (s *identityStub) ListSessions(context.Context, IdentitySessionQuery) ([]IdentitySession, error) {
	return append([]IdentitySession(nil), s.sessions...), s.err
}

type accessStub struct {
	statuses map[string]productuseraccess.Status
}

func (s accessStub) GetScopedAccessBatch(_ context.Context, query productuseraccess.GetScopedAccessBatchQuery) ([]productuseraccess.ScopedAccess, error) {
	result := make([]productuseraccess.ScopedAccess, 0, len(query.UserIDs))
	for _, userID := range query.UserIDs {
		status := productuseraccess.StatusActive
		explicit, version := false, int64(0)
		if configured, ok := s.statuses[userID]; ok {
			status, explicit, version = configured, true, 1
		}
		result = append(result, productuseraccess.ScopedAccess{ScopeType: productuseraccess.ScopeProduct, ScopeID: query.Product.ProductID, ProductID: query.Product.ProductID, UserID: userID, Status: status, Explicit: explicit, AccessVersion: version})
	}
	return result, nil
}

type capabilityStub struct {
	enabled bool
	calls   int
}

func (s *capabilityStub) IsPackageEnabled(context.Context, string, string) (bool, error) {
	s.calls++
	return s.enabled, nil
}

func TestListUsersFiltersAccessAndBindsCursorToScopeAndFilters(t *testing.T) {
	users := make([]IdentityUser, 0, 5)
	for index := range 5 {
		users = append(users, testUser(fmt.Sprintf("user-%d", index), fmt.Sprintf("position-%d", index)))
	}
	identity := &identityStub{users: users}
	capability := &capabilityStub{enabled: true}
	service := New(identity, accessStub{statuses: map[string]productuseraccess.Status{"user-1": productuseraccess.StatusSuspended, "user-3": productuseraccess.StatusSuspended, "user-4": productuseraccess.StatusSuspended}}, capability, cursorKey)
	query := ListQuery{Scope: Scope{Type: ScopeProduct, ProductID: "product-a"}, AccessStatus: "suspended", PageSize: 2}
	first, err := service.ListUsers(context.Background(), query)
	if err != nil || len(first.Items) != 2 || first.Items[0].UserID != "user-1" || first.Items[1].UserID != "user-3" || first.NextCursor == nil {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	query.Cursor = *first.NextCursor
	second, err := service.ListUsers(context.Background(), query)
	if err != nil || len(second.Items) != 1 || second.Items[0].UserID != "user-4" || second.NextCursor != nil {
		t.Fatalf("second=%+v error=%v", second, err)
	}
	tampered := query
	tampered.Scope.ProductID = "product-b"
	if _, err := service.ListUsers(context.Background(), tampered); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cross-scope cursor error=%v", err)
	}
	tampered = query
	tampered.AccessStatus = "active"
	if _, err := service.ListUsers(context.Background(), tampered); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cross-filter cursor error=%v", err)
	}
}

func TestScopedCapabilityAndMembershipFailClosed(t *testing.T) {
	identity := &identityStub{detail: IdentityUserDetail{User: testUser("user-a", "")}}
	disabled := &capabilityStub{}
	service := New(identity, accessStub{}, disabled, cursorKey)
	scope := Scope{Type: ScopeProduct, ProductID: "product-a"}
	if _, err := service.GetUser(context.Background(), scope, "user-a"); !errors.Is(err, ErrCapabilityNotEnabled) {
		t.Fatalf("disabled capability error=%v", err)
	}
	if identity.listCalls != 0 {
		t.Fatal("identity queried before capability check")
	}
	enabled := &capabilityStub{enabled: true}
	identity.err = ErrIdentityUserNotFound
	service = New(identity, accessStub{}, enabled, cursorKey)
	if _, err := service.GetUser(context.Background(), scope, "user-a"); !errors.Is(err, ErrScopedUserNotFound) {
		t.Fatalf("scoped member error=%v", err)
	}
}

func TestGlobalListDoesNotRequireCapabilityOrAccess(t *testing.T) {
	identity := &identityStub{users: []IdentityUser{testUser("user-a", "position-a")}}
	capability := &capabilityStub{}
	service := New(identity, nil, capability, cursorKey)
	page, err := service.ListUsers(context.Background(), ListQuery{Scope: Scope{Type: ScopePlatform}, PageSize: 20})
	if err != nil || len(page.Items) != 1 || page.Items[0].Access != nil || capability.calls != 0 {
		t.Fatalf("page=%+v capability_calls=%d error=%v", page, capability.calls, err)
	}
}

func testUser(id, position string) IdentityUser {
	return IdentityUser{UserID: id, UserVersion: 1, AccountStatus: "active", Identifiers: []MaskedIdentifier{}, CreatedAt: time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC), ActiveSessionCount: 1, TotalSessionCount: 1, Position: position}
}
