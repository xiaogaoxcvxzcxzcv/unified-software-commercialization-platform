package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	accesspostgres "platform.local/capability-platform/backend/internal/modules/accesscontrol/postgres"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

type sequenceIDs struct {
	mu sync.Mutex
	n  int
}

func (s *sequenceIDs) ID(prefix string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return fmt.Sprintf("%s%032d", prefix, s.n), nil
}

func TestRepositoryBootstrapSnapshotVersionAndExpiry(t *testing.T) {
	database := testpostgres.Open(t)
	repository := accesspostgres.New(database.Pool)
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	service := accesscontrol.NewService(repository, func() time.Time { return now })
	command := accesscontrol.BootstrapCommand{BindingID: "binding-1", RoleID: "role-1", AdminUserID: "admin-1", Now: now}

	if err := service.BootstrapPlatformAdmin(context.Background(), command); err != nil {
		t.Fatalf("BootstrapPlatformAdmin() error = %v", err)
	}
	if err := service.BootstrapPlatformAdmin(context.Background(), command); err != nil {
		t.Fatalf("repeat BootstrapPlatformAdmin() error = %v", err)
	}
	snapshot, err := service.ResolveAdminAccessSnapshot(context.Background(), "admin-1", "session-1")
	if err != nil {
		t.Fatalf("ResolveAdminAccessSnapshot() error = %v", err)
	}
	if snapshot.AuthorizationVersion != 2 {
		t.Fatalf("authorization version = %d, want 2", snapshot.AuthorizationVersion)
	}
	wantPermissionCount := 0
	for _, definition := range accesscontrol.CurrentPermissionCatalog().Definitions() {
		if definition.GrantsPlatformSuperAdminOnBootstrap() {
			wantPermissionCount++
		}
	}
	if len(snapshot.Permissions) != wantPermissionCount || len(snapshot.Scopes) != 1 || snapshot.Scopes[0].Type != "platform" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	for _, permission := range snapshot.Permissions {
		if permission == "assembly.experimental.use" {
			t.Fatal("bootstrap unexpectedly granted experimental catalog access")
		}
	}

	if _, err := database.Pool.Exec(context.Background(), `UPDATE access_control.admin_scope_bindings SET expires_at=$1 WHERE binding_id=$2`, now.Add(-time.Minute), command.BindingID); err != nil {
		t.Fatalf("expire scope binding: %v", err)
	}
	if _, err := service.ResolveAdminAccessSnapshot(context.Background(), "admin-1", "session-1"); !errors.Is(err, accesscontrol.ErrNoActiveScope) {
		t.Fatalf("expired scope error = %v, want ErrNoActiveScope", err)
	}
}

func TestBindAdminScopeIsIdempotentScopedAndVersioned(t *testing.T) {
	database := testpostgres.Open(t)
	repository := accesspostgres.New(database.Pool)
	now := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)
	bootstrap := accesscontrol.NewService(repository, func() time.Time { return now })
	if err := bootstrap.BootstrapPlatformAdmin(context.Background(), accesscontrol.BootstrapCommand{BindingID: "bootstrap-binding", RoleID: "bootstrap-role", AdminUserID: "bootstrap-admin", Now: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(context.Background(), `INSERT INTO access_control.admin_roles(role_id,role_code,display_name,status,created_at,updated_at) VALUES('role-tenant-operator','tenant_operator','Tenant Operator','active',$1,$1)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(context.Background(), `INSERT INTO access_control.admin_role_permissions(role_id,permission_code) VALUES('role-tenant-operator','tenant.manage')`); err != nil {
		t.Fatal(err)
	}
	service := accesscontrol.NewServiceWithGenerator(repository, func() time.Time { return now }, &sequenceIDs{})
	tenantCommand := accesscontrol.BindAdminScopeCommand{
		AdminUserID: "target-admin", RoleCode: "tenant_operator",
		Scope:   accesscontrol.Scope{Type: "tenant", ProductID: "prod-a", TenantID: "tenant-a1"},
		ActorID: "bootstrap-admin", TraceID: "trace-tenant", IdempotencyKey: "idem-scope-tenant-001",
	}
	first, err := service.BindAdminScope(context.Background(), tenantCommand)
	if err != nil {
		t.Fatalf("tenant BindAdminScope() error = %v", err)
	}
	replayed, err := service.BindAdminScope(context.Background(), tenantCommand)
	if err != nil || replayed.BindingID != first.BindingID || replayed.AuditID != first.AuditID || replayed.AuthorizationVersion != 1 {
		t.Fatalf("tenant replay = %+v, error = %v", replayed, err)
	}
	changed := tenantCommand
	changed.Scope.TenantID = "tenant-a2"
	if _, err := service.BindAdminScope(context.Background(), changed); !errors.Is(err, accesscontrol.ErrIdempotencyConflict) {
		t.Fatalf("changed idempotency payload error = %v", err)
	}
	productCommand := accesscontrol.BindAdminScopeCommand{
		AdminUserID: "target-admin", RoleCode: "tenant_operator", Scope: accesscontrol.Scope{Type: "product", ProductID: "prod-b"},
		ActorID: "bootstrap-admin", TraceID: "trace-product", IdempotencyKey: "idem-scope-product-001",
	}
	productBinding, err := service.BindAdminScope(context.Background(), productCommand)
	if err != nil || productBinding.AuthorizationVersion != 2 {
		t.Fatalf("product binding = %+v, error = %v", productBinding, err)
	}
	snapshot, err := service.ResolveAdminAccessSnapshot(context.Background(), "target-admin", "session-1")
	if err != nil || snapshot.AuthorizationVersion != 2 || len(snapshot.Scopes) != 2 || len(snapshot.Permissions) != 1 || snapshot.Permissions[0] != "tenant.manage" {
		t.Fatalf("snapshot = %+v, error = %v", snapshot, err)
	}
	var bindingCount, outboxCount int
	if err := database.Pool.QueryRow(context.Background(), `SELECT count(*) FROM access_control.admin_scope_bindings WHERE admin_user_id='target-admin'`).Scan(&bindingCount); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(context.Background(), `SELECT count(*) FROM access_control.outbox_events WHERE payload->>'actor_id'='bootstrap-admin' AND payload->>'audit_id'<>'' AND payload::text NOT ILIKE '%idempotency%' AND payload::text NOT ILIKE '%digest%'`).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if bindingCount != 2 || outboxCount != 2 {
		t.Fatalf("binding count=%d outbox count=%d", bindingCount, outboxCount)
	}
	duplicate := tenantCommand
	duplicate.IdempotencyKey = "idem-scope-tenant-002"
	if _, err := service.BindAdminScope(context.Background(), duplicate); !errors.Is(err, accesscontrol.ErrScopeBindingConflict) {
		t.Fatalf("duplicate binding error = %v", err)
	}
}

func TestBindAdminScopeRejectsDisabledRoleAndOutboxRetries(t *testing.T) {
	database := testpostgres.Open(t)
	repository := accesspostgres.New(database.Pool)
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	bootstrap := accesscontrol.NewService(repository, func() time.Time { return now })
	if err := bootstrap.BootstrapPlatformAdmin(context.Background(), accesscontrol.BootstrapCommand{BindingID: "bootstrap-binding", RoleID: "bootstrap-role", AdminUserID: "bootstrap-admin", Now: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(context.Background(), `INSERT INTO access_control.admin_roles(role_id,role_code,display_name,status,created_at,updated_at) VALUES('role-disabled','disabled_operator','Disabled','disabled',$1,$1)`, now); err != nil {
		t.Fatal(err)
	}
	service := accesscontrol.NewServiceWithGenerator(repository, func() time.Time { return now }, &sequenceIDs{})
	disabled := accesscontrol.BindAdminScopeCommand{AdminUserID: "target", RoleCode: "disabled_operator", Scope: accesscontrol.Scope{Type: "product", ProductID: "prod-a"}, ActorID: "bootstrap-admin", TraceID: "trace-disabled", IdempotencyKey: "idem-disabled-000001"}
	if _, err := service.BindAdminScope(context.Background(), disabled); !errors.Is(err, accesscontrol.ErrRoleNotFound) {
		t.Fatalf("disabled role error = %v", err)
	}
	valid := disabled
	valid.RoleCode = "super_admin"
	valid.IdempotencyKey = "idem-valid-00000001"
	if _, err := service.BindAdminScope(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	events, err := service.ClaimOutbox(context.Background(), now.Add(time.Second), 10)
	if err != nil || len(events) != 1 || events[0].AttemptCount != 1 {
		t.Fatalf("ClaimOutbox() = %+v, error = %v", events, err)
	}
	if err := service.MarkOutboxFailed(context.Background(), events[0].EventID, "temporary audit failure", now.Add(time.Minute), false); err != nil {
		t.Fatal(err)
	}
	events, err = service.ClaimOutbox(context.Background(), now.Add(2*time.Minute), 10)
	if err != nil || len(events) != 1 || events[0].AttemptCount != 2 {
		t.Fatalf("reclaim = %+v, error = %v", events, err)
	}
	if err := service.MarkOutboxPublished(context.Background(), events[0].EventID, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	events, err = service.ClaimOutbox(context.Background(), now.Add(4*time.Minute), 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("published event reclaimed = %+v, error = %v", events, err)
	}
}
