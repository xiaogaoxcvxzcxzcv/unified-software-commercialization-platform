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
	case isProductUserAccessRoute(path):
		h.productUserAccess.ServeHTTP(w, r)
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

func isProductUserAccessRoute(path string) bool {
	if !strings.HasPrefix(path, "/api/v1/admin/products/") || strings.HasSuffix(path, "/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/products/"), "/")
	return (len(parts) == 4 && parts[1] == "users" && parts[3] == "access") ||
		(len(parts) == 6 && parts[1] == "tenants" && parts[3] == "users" && parts[5] == "access")
}
