package httptransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/workflows/clientregistration"
)

type serviceStub struct {
	register clientregistration.RegisterCommand
}

func (s *serviceStub) Register(_ context.Context, command clientregistration.RegisterCommand) (clientregistration.CredentialResult, error) {
	s.register = command
	return clientregistration.CredentialResult{ClientID: "client-1", CredentialID: "credential-1", ProductID: command.ProductID, ApplicationID: command.ApplicationID, Environment: command.Environment, ProofType: command.ProofType, Generation: 1, Secret: "pcsec_abcdefghijklmnopqrstuvwxyz0123456789", ExpiresAt: command.ExpiresAt, AuditID: "audit-1"}, nil
}
func (s *serviceStub) Rotate(context.Context, clientregistration.RotateCommand) (clientregistration.CredentialResult, error) {
	return clientregistration.CredentialResult{}, nil
}
func (s *serviceStub) Revoke(context.Context, string, string, string, string, string, string) (product.ClientCredential, error) {
	return product.ClientCredential{}, nil
}

type authStub struct{}

func (authStub) Authenticate(context.Context, *http.Request, bool) (adminrequest.Principal, error) {
	return adminrequest.Principal{AdminUserID: "admin-1", SessionID: "session-1"}, nil
}

type allowStub struct{}

func (allowStub) Authorize(context.Context, adminrequest.Principal, string, adminrequest.TargetScope) (adminrequest.Decision, error) {
	return adminrequest.Decision{Allowed: true}, nil
}

func TestHandlerRegistersClientThroughHighRiskGuard(t *testing.T) {
	service := &serviceStub{}
	handler := New(service, adminrequest.New(authStub{}, allowStub{}, nil), func() time.Time { return time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC) })
	body := `{"environment":"production","proof_type":"hmac_sha256_v1","expires_at":"2026-08-13T00:00:00Z"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/products/prod-1/applications/app-1/clients", strings.NewReader(body))
	request.Header.Set("Idempotency-Key", "0123456789abcdef")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || recorder.Header().Get("Cache-Control") != "no-store" || service.register.ApplicationID != "app-1" {
		t.Fatalf("status=%d cache=%q command=%#v body=%s", recorder.Code, recorder.Header().Get("Cache-Control"), service.register, recorder.Body.String())
	}
}
