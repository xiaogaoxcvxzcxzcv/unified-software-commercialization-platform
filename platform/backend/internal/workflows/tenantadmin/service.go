package tenantadmin

import (
	"context"
	"errors"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/modules/tenant"
)

var ErrTenantScopeMismatch = errors.New("tenant does not belong to product")

type TenantService interface {
	ListTenants(context.Context, tenant.ListTenantsCommand) ([]tenant.Tenant, error)
}
type AccessService interface {
	BindAdminScope(context.Context, accesscontrol.BindAdminScopeCommand) (accesscontrol.AdminScopeBinding, error)
}
type Service struct {
	tenants TenantService
	access  AccessService
}

func New(tenants TenantService, access AccessService) *Service {
	return &Service{tenants: tenants, access: access}
}

type BindCommand struct{ ProductID, TenantID, AdminUserID, RoleCode, ActorID, IdempotencyKey, TraceID string }

func (s *Service) Bind(ctx context.Context, command BindCommand) (accesscontrol.AdminScopeBinding, error) {
	if s == nil || s.tenants == nil || s.access == nil {
		return accesscontrol.AdminScopeBinding{}, ErrTenantScopeMismatch
	}
	items, err := s.tenants.ListTenants(ctx, tenant.ListTenantsCommand{ProductID: command.ProductID})
	if err != nil {
		return accesscontrol.AdminScopeBinding{}, err
	}
	found := false
	for _, item := range items {
		if item.ProductID == command.ProductID && item.TenantID == command.TenantID {
			found = true
			break
		}
	}
	if !found {
		return accesscontrol.AdminScopeBinding{}, ErrTenantScopeMismatch
	}
	return s.access.BindAdminScope(ctx, accesscontrol.BindAdminScopeCommand{AdminUserID: command.AdminUserID, RoleCode: command.RoleCode, Scope: accesscontrol.Scope{Type: "tenant", ID: command.TenantID, ProductID: command.ProductID, TenantID: command.TenantID}, ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID})
}
