package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/workflows/clientcontext"
)

type serviceStub struct{ command clientcontext.CreateCommand }

func (s *serviceStub) CreateSession(_ context.Context, command clientcontext.CreateCommand) (clientcontext.Session, error) {
	s.command = command
	return clientcontext.Session{Token: "client-session-token-abcdefghijklmnopqrstuvwxyz", ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
}

func TestHandlerCreatesNoStoreClientSession(t *testing.T) {
	service := &serviceStub{}
	handler := New(service)
	body := `{"client_id":"client-1","credential_id":"credential-1","client_proof":{"schema_version":1,"type":"hmac_sha256_v1","value":"pcsec_abcdefghijklmnopqrstuvwxyz0123456789","timestamp":"2026-07-13T00:00:00Z"},"client_version":"1.0.0","request_nonce":"0123456789abcdef"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/client/session", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || recorder.Header().Get("Cache-Control") != "no-store" || service.command.CredentialID != "credential-1" {
		t.Fatalf("status=%d cache=%q command=%#v body=%s", recorder.Code, recorder.Header().Get("Cache-Control"), service.command, recorder.Body.String())
	}
}
