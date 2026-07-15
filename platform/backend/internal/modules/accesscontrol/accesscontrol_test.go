package accesscontrol

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

type repositoryStub struct {
	snapshot         Snapshot
	resolveCalls     int
	bootstrapCatalog PermissionCatalog
	boundRecord      ScopeBindingRecord
}

func (r *repositoryStub) ResolveSnapshot(context.Context, string, time.Time) (Snapshot, error) {
	r.resolveCalls++
	return r.snapshot, nil
}
func (r *repositoryStub) BootstrapPlatformAdmin(_ context.Context, _ BootstrapCommand, catalog PermissionCatalog) error {
	r.bootstrapCatalog = catalog
	return nil
}
func (r *repositoryStub) BindAdminScope(_ context.Context, record ScopeBindingRecord) (AdminScopeBinding, error) {
	r.boundRecord = record
	record.Binding.AuthorizationVersion = 1
	return record.Binding, nil
}
func (*repositoryStub) ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error) {
	return nil, nil
}
func (*repositoryStub) MarkOutboxPublished(context.Context, string, time.Time) error { return nil }
func (*repositoryStub) MarkOutboxFailed(context.Context, string, string, time.Time, bool) error {
	return nil
}

type fixedIDs struct{ value string }

func (f fixedIDs) ID(prefix string) (string, error) { return prefix + f.value, nil }

func TestAuthorizeAdminHonorsPermissionAndScope(t *testing.T) {
	repository := &repositoryStub{snapshot: Snapshot{AuthorizationVersion: 1, Permissions: []string{"product.read"}, Scopes: []Scope{{Type: "product", ID: "prod-a", ProductID: "prod-a"}}}}
	service := NewService(repository, nil)
	allowed, err := service.AuthorizeAdmin(context.Background(), "user", "session", "product.read", TargetScope{Type: "product", ID: "prod-a", ProductID: "prod-a"})
	if err != nil || !allowed.Allowed {
		t.Fatalf("expected allowed decision: %+v, %v", allowed, err)
	}
	denied, err := service.AuthorizeAdmin(context.Background(), "user", "session", "product.read", TargetScope{Type: "product", ID: "prod-b", ProductID: "prod-b"})
	if err != nil || denied.Allowed || denied.ReasonCode != "scope_mismatch" {
		t.Fatalf("expected scope denial: %+v, %v", denied, err)
	}
}

func TestAuthorizeAdminRejectsUnknownPermissionBeforeRepositoryLookup(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository, nil)

	decision, err := service.AuthorizeAdmin(context.Background(), "user", "session", "manifest.injected", TargetScope{Type: "platform"})
	if err != nil || decision.Allowed || decision.ReasonCode != "unknown_permission" {
		t.Fatalf("expected unknown permission denial: %+v, %v", decision, err)
	}
	if repository.resolveCalls != 0 {
		t.Fatalf("unknown permission must not reach repository, got %d calls", repository.resolveCalls)
	}
}

func TestResolveSnapshotRejectsPermissionOutsideCatalog(t *testing.T) {
	repository := &repositoryStub{snapshot: Snapshot{
		AuthorizationVersion: 1,
		Permissions:          []string{"product.read", "manifest.injected"},
		Scopes:               []Scope{{Type: "platform"}},
	}}
	service := NewService(repository, nil)

	_, err := service.ResolveAdminAccessSnapshot(context.Background(), "user", "session")
	if !errors.Is(err, ErrUnknownPermission) {
		t.Fatalf("expected unknown permission error, got %v", err)
	}
}

func TestBootstrapPassesCurrentCatalogToRepository(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository, nil)

	if err := service.BootstrapPlatformAdmin(context.Background(), BootstrapCommand{AdminUserID: "user"}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if repository.bootstrapCatalog.Version() != PermissionCatalogVersion {
		t.Fatalf("catalog version = %q, want %q", repository.bootstrapCatalog.Version(), PermissionCatalogVersion)
	}
	if !reflect.DeepEqual(repository.bootstrapCatalog.Definitions(), CurrentPermissionCatalog().Definitions()) {
		t.Fatal("bootstrap did not receive the current permission catalog")
	}
}

func TestBindAdminScopeNormalizesTenantScopeAndCreatesRedactedRecord(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	repository := &repositoryStub{}
	service := NewServiceWithGenerator(repository, func() time.Time { return now }, fixedIDs{value: "1"})
	expires := now.Add(24 * time.Hour)
	result, err := service.BindAdminScope(context.Background(), BindAdminScopeCommand{
		AdminUserID: "admin-target", RoleCode: "tenant_operator",
		Scope: Scope{Type: "tenant", ProductID: "prod-1", TenantID: "tenant-1"}, ExpiresAt: &expires,
		ActorID: "admin-actor", TraceID: "trace-1", IdempotencyKey: "idem-scope-000001",
	})
	if err != nil {
		t.Fatalf("BindAdminScope() error = %v", err)
	}
	if result.BindingID != "scopebind_1" || result.AuditID != "audit_1" || result.Scope.ID != "tenant-1" || result.AuthorizationVersion != 1 {
		t.Fatalf("result = %+v", result)
	}
	record := repository.boundRecord
	if record.EventID != "evt_1" || record.Binding.Scope.ProductID != "prod-1" || record.Binding.Scope.TenantID != "tenant-1" || record.Idempotency.KeyDigest == "" || record.Idempotency.RequestDigest == "" {
		t.Fatalf("record = %+v", record)
	}
	raw, err := json.Marshal(record)
	if err != nil || string(raw) == "" {
		t.Fatal(err)
	}
}

func TestBindAdminScopeRejectsMalformedOrExpiredScope(t *testing.T) {
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	service := NewServiceWithGenerator(&repositoryStub{}, func() time.Time { return now }, fixedIDs{value: "1"})
	cases := []BindAdminScopeCommand{
		{AdminUserID: "target", RoleCode: "role", Scope: Scope{Type: "tenant", TenantID: "tenant-1"}, ActorID: "actor", TraceID: "trace", IdempotencyKey: "idem-scope-000001"},
		{AdminUserID: "target", RoleCode: "Role", Scope: Scope{Type: "product", ProductID: "prod-1"}, ActorID: "actor", TraceID: "trace", IdempotencyKey: "idem-scope-000001"},
		{AdminUserID: "target", RoleCode: "role", Scope: Scope{Type: "platform"}, ActorID: "actor", TraceID: "trace", IdempotencyKey: "idem-scope-000001"},
		{AdminUserID: "target", RoleCode: "role", Scope: Scope{Type: "platform", ProductID: "prod-1"}, ActorID: "actor", TraceID: "trace", IdempotencyKey: "idem-scope-000001"},
		{AdminUserID: "target", RoleCode: "role", Scope: Scope{Type: "product", ProductID: "prod-1"}, EffectiveFrom: now, ExpiresAt: timePointer(now), ActorID: "actor", TraceID: "trace", IdempotencyKey: "idem-scope-000001"},
	}
	for _, command := range cases {
		if _, err := service.BindAdminScope(context.Background(), command); !errors.Is(err, ErrInvalidScopeBinding) {
			t.Fatalf("command %+v error = %v", command, err)
		}
	}
}

func timePointer(value time.Time) *time.Time { return &value }
