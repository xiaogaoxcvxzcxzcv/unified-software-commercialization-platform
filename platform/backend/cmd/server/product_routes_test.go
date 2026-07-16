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
