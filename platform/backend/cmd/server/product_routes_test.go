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

func TestProductAdminRouterForwardsOutputTargetCatalogToAssembly(t *testing.T) {
	assembly := &recordingHandler{}
	router := productAdminRouter{assembly: assembly}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/assembly-output-targets?environment=development", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if !assembly.called || response.Code != http.StatusNoContent {
		t.Fatalf("assembly called=%v status=%d", assembly.called, response.Code)
	}
}
