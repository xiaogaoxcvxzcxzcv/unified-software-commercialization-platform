package tenantadmin

import (
	"context"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/modules/tenant"
)

type tenantStub struct{ items []tenant.Tenant }

func (s tenantStub) ListTenants(context.Context, tenant.ListTenantsCommand) ([]tenant.Tenant, error) {
	return s.items, nil
}

type accessStub struct {
	command accesscontrol.BindAdminScopeCommand
}

func (s *accessStub) BindAdminScope(_ context.Context, command accesscontrol.BindAdminScopeCommand) (accesscontrol.AdminScopeBinding, error) {
	s.command = command
	return accesscontrol.AdminScopeBinding{BindingID: "binding-1", Scope: command.Scope, AuditID: "audit-1"}, nil
}
func TestBindVerifiesTenantProductBeforeAccessControl(t *testing.T) {
	access := &accessStub{}
	service := New(tenantStub{items: []tenant.Tenant{{ProductID: "prod-1", TenantID: "ten-1"}}}, access)
	result, err := service.Bind(context.Background(), BindCommand{ProductID: "prod-1", TenantID: "ten-1", AdminUserID: "user-1", RoleCode: "tenant_operator", ActorID: "admin-1", IdempotencyKey: "0123456789abcdef", TraceID: "trace-1"})
	if err != nil || result.AuditID == "" || access.command.Scope.ProductID != "prod-1" || access.command.Scope.TenantID != "ten-1" {
		t.Fatalf("result=%#v error=%v command=%#v", result, err, access.command)
	}
}
