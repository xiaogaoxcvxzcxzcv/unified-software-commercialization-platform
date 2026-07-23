package main

import (
	"context"
	"net/http"
	"strings"

	producthttp "platform.local/capability-platform/backend/internal/modules/product/httptransport"
	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/workflows/productprovisioning"
)

type productProvisionerAdapter struct{ workflow *productprovisioning.Service }

func (a productProvisionerAdapter) ProvisionProduct(ctx context.Context, command producthttp.ProvisionCommand) (producthttp.ProvisionedProduct, error) {
	created, err := a.workflow.CreateProduct(ctx, productprovisioning.CreateCommand{
		ProductCode: command.ProductCode, Name: command.Name, Status: command.Status,
		ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey, TraceID: command.TraceID,
	})
	return producthttp.ProvisionedProduct{Product: created}, err
}

type productAdminRouter struct {
	assembly           http.Handler
	product            http.Handler
	application        http.Handler
	tenant             http.Handler
	productUserAccess  http.Handler
	accountUserQuery   http.Handler
	accountUserAdmin   http.Handler
	entitlement        http.Handler
	clientRegistration http.Handler
	tenantAdmin        http.Handler
}

func (h productAdminRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/api/v1/admin/blueprints" || path == "/api/v1/admin/assembly-runs" || path == "/api/v1/admin/assembly-output-targets" || path == "/api/v1/admin/assembly-catalog-options" || path == "/api/v1/admin/experimental/assembly-catalog-options" || strings.HasPrefix(path, "/api/v1/admin/blueprints/") ||
		strings.HasPrefix(path, "/api/v1/admin/assembly-plans/") || strings.HasPrefix(path, "/api/v1/admin/assembly-runs/") ||
		strings.HasPrefix(path, "/api/v1/admin/assemblies/") || strings.HasPrefix(path, "/api/v1/admin/assembly-lifecycle-plans/") || strings.HasPrefix(path, "/api/v1/admin/assembly-lifecycle-operations/") ||
		strings.HasPrefix(path, "/api/v1/admin/assembly-manifests/") || strings.HasPrefix(path, "/api/v1/admin/generated-project-locks/"):
		h.assembly.ServeHTTP(w, r)
	case isAccountUserAdminRoute(path) && h.accountUserAdmin != nil:
		h.accountUserAdmin.ServeHTTP(w, r)
	case isAccountUserQueryRoute(path) && h.accountUserQuery != nil:
		h.accountUserQuery.ServeHTTP(w, r)
	case isProductUserAccessRoute(path):
		h.productUserAccess.ServeHTTP(w, r)
	case isEntitlementAdminRoute(path) && h.entitlement != nil:
		h.entitlement.ServeHTTP(w, r)
	case strings.Contains(path, "/applications/") && strings.Contains(path, "/clients"):
		h.clientRegistration.ServeHTTP(w, r)
	case strings.Contains(path, "/applications"):
		h.application.ServeHTTP(w, r)
	case strings.HasSuffix(path, "/admins") && strings.Contains(path, "/tenants/"):
		h.tenantAdmin.ServeHTTP(w, r)
	case strings.Contains(path, "/tenants"):
		h.tenant.ServeHTTP(w, r)
	case path == "/api/v1/admin/products" || strings.HasPrefix(path, "/api/v1/admin/products/"):
		h.product.ServeHTTP(w, r)
	default:
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
	}
}

func isEntitlementAdminRoute(path string) bool {
	return path == "/api/v1/admin/entitlements" || strings.HasPrefix(path, "/api/v1/admin/entitlements/")
}

func isAccountUserQueryRoute(path string) bool {
	if path == "/api/v1/admin/users" {
		return true
	}
	if strings.HasPrefix(path, "/api/v1/admin/users/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/users/"), "/")
		return len(parts) == 1 || (len(parts) == 2 && parts[1] == "sessions")
	}
	if !strings.HasPrefix(path, "/api/v1/admin/products/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/products/"), "/")
	if len(parts) >= 2 && parts[1] == "users" {
		return len(parts) == 2 || (len(parts) == 3) || (len(parts) == 4 && parts[3] == "sessions")
	}
	return len(parts) >= 4 && parts[1] == "tenants" && len(parts) >= 4 && parts[3] == "users" && (len(parts) == 4 || len(parts) == 5 || (len(parts) == 6 && parts[5] == "sessions"))
}

func isAccountUserAdminRoute(path string) bool {
	if strings.HasPrefix(path, "/api/v1/admin/users/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/users/"), "/")
		return (len(parts) == 2 && parts[1] == "security-status") || (len(parts) == 3 && parts[1] == "sessions" && parts[2] == "revoke")
	}
	if !strings.HasPrefix(path, "/api/v1/admin/products/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/products/"), "/")
	if len(parts) == 4 && parts[1] == "users" && parts[3] == "access" {
		return true
	}
	if len(parts) == 5 && parts[1] == "users" && parts[3] == "sessions" && parts[4] == "revoke" {
		return true
	}
	if len(parts) == 6 && parts[1] == "tenants" && parts[3] == "users" && parts[5] == "access" {
		return true
	}
	return len(parts) == 7 && parts[1] == "tenants" && parts[3] == "users" && parts[5] == "sessions" && parts[6] == "revoke"
}

func isProductUserAccessRoute(path string) bool {
	if !strings.HasPrefix(path, "/api/v1/admin/products/") || strings.HasSuffix(path, "/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/products/"), "/")
	return (len(parts) == 4 && parts[1] == "users" && parts[3] == "access") ||
		(len(parts) == 6 && parts[1] == "tenants" && parts[3] == "users" && parts[5] == "access")
}
