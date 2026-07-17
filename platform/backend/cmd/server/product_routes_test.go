package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type recordingHandler struct{ called bool }

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.called = true
	w.WriteHeader(http.StatusNoContent)
}

func TestProductAdminRouterForwardsAssemblyTopLevelCatalogRoutes(t *testing.T) {
	for _, target := range []string{
		"/api/v1/admin/assembly-runs?page_size=30",
		"/api/v1/admin/assembly-output-targets?environment=development",
		"/api/v1/admin/assembly-catalog-options?target=web&delivery_mode=generated_source&environment=test",
		"/api/v1/admin/experimental/assembly-catalog-options?target=web&delivery_mode=generated_source&environment=test",
		"/api/v1/admin/assemblies/assembly-1/upgrade-plans",
		"/api/v1/admin/assemblies/assembly-1/eject-plans",
		"/api/v1/admin/assembly-lifecycle-plans/lifecycle-plan-1",
		"/api/v1/admin/assembly-lifecycle-operations/lifecycle-operation-1",
	} {
		assembly := &recordingHandler{}
		router := productAdminRouter{assembly: assembly}
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if !assembly.called || response.Code != http.StatusNoContent {
			t.Fatalf("target=%s assembly called=%v status=%d", target, assembly.called, response.Code)
		}
	}
}

func TestProductAdminRouterPrioritizesProductUserAccessRoutes(t *testing.T) {
	for _, target := range []string{
		"/api/v1/admin/products/product-a/users/user-a/access",
		"/api/v1/admin/products/product-a/tenants/tenant-a/users/user-a/access",
	} {
		access, product, tenant := &recordingHandler{}, &recordingHandler{}, &recordingHandler{}
		router := productAdminRouter{productUserAccess: access, product: product, tenant: tenant}
		request := httptest.NewRequest(http.MethodPut, target, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if !access.called || product.called || tenant.called || response.Code != http.StatusNoContent {
			t.Fatalf("target=%s access=%v product=%v tenant=%v status=%d", target, access.called, product.called, tenant.called, response.Code)
		}
	}
}
