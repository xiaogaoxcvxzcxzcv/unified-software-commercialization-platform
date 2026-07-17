package httptransport

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

type externalUserServiceStub struct {
	call     string
	client   ClientSessionContext
	user     UserSessionContext
	provider string
	value    any
	err      error
}

func (s *externalUserServiceStub) StartRegistrationVerification(_ context.Context, client ClientSessionContext, command StartRegistrationVerificationCommand) (VerificationChallengeResponse, error) {
	s.call, s.client, s.value = "verification", client, command
	return VerificationChallengeResponse{ContinuationID: "verification-continuation-123456"}, s.err
}
func (s *externalUserServiceStub) StartExternalLogin(_ context.Context, client ClientSessionContext, provider string, command StartExternalLoginCommand) (ExternalLoginFlowResponse, error) {
	s.call, s.client, s.provider, s.value = "external-start", client, provider, command
	return ExternalLoginFlowResponse{FlowID: "flow-external-123456", Mode: command.Mode, AuthorizationURL: "https://provider.example/authorize", ExpiresAt: time.Now().Add(time.Minute)}, s.err
}
func (s *externalUserServiceStub) CompleteExternalLogin(_ context.Context, client ClientSessionContext, provider string, command CompleteExternalLoginCommand) (ExternalExchangeResponse, error) {
	s.call, s.client, s.provider, s.value = "external-complete", client, provider, command
	return ExternalExchangeResponse{Status: "link_required", ProofID: "external-proof-123456789"}, s.err
}
func (s *externalUserServiceStub) LinkExternalIdentity(_ context.Context, user UserSessionContext, provider string, command LinkExternalIdentityCommand) (ExternalIdentityResponse, error) {
	s.call, s.user, s.provider, s.value = "external-link", user, provider, command
	return ExternalIdentityResponse{ExternalIdentityID: "external-identity-123456", Provider: provider, Status: "active", LinkedAt: time.Now()}, s.err
}
func (s *externalUserServiceStub) ListExternalIdentities(_ context.Context, user UserSessionContext) ([]ExternalIdentityResponse, error) {
	s.call, s.user = "external-list", user
	return []ExternalIdentityResponse{}, s.err
}
func (s *externalUserServiceStub) UnlinkExternalIdentity(_ context.Context, user UserSessionContext, externalID string) error {
	s.call, s.user, s.value = "external-unlink", user, externalID
	return s.err
}

func TestExternalUserRoutesBindTrustedSessions(t *testing.T) {
	base := &userServiceStub{}
	resolver := &userResolverStub{}
	external := &externalUserServiceStub{}
	handler := NewUserHandler(base, resolver, WithExternalUserService(external))

	tests := []struct {
		name, method, path, body, auth, idem, wantCall string
		wantStatus                                     int
	}{
		{"verification", http.MethodPost, "/api/v1/auth/verification/start", `{"identifier":"person@example.com"}`, "client", "verification-key-123456", "verification", http.StatusAccepted},
		{"external start", http.MethodPost, "/api/v1/auth/external/oidc/start", `{"mode":"redirect","return_target_code":"account"}`, "client", "", "external-start", http.StatusCreated},
		{"external callback", http.MethodPost, "/api/v1/auth/external/oidc/callback", `{"flow_id":"flow-external-123456","state":"state-state-state-state-state-1234","code":"provider-code"}`, "client", "", "external-complete", http.StatusOK},
		{"wechat exchange", http.MethodPost, "/api/v1/auth/external/wechat/exchange", `{"flow_id":"flow-external-123456","state":"state-state-state-state-state-1234","code":"provider-code"}`, "client", "", "external-complete", http.StatusOK},
		{"link", http.MethodPost, "/api/v1/account/external-identities/oidc/link", `{"external_proof_id":"external-proof-123456789"}`, "user", "external-link-key-123456", "external-link", http.StatusOK},
		{"list", http.MethodGet, "/api/v1/account/external-identities", "", "user", "", "external-list", http.StatusOK},
		{"unlink", http.MethodDelete, "/api/v1/account/external-identities/external-identity-123456", "", "user", "", "external-unlink", http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			external.call = ""
			response := serveUser(handler, test.method, test.path, test.body, test.auth, test.idem)
			if response.Code != test.wantStatus || external.call != test.wantCall {
				t.Fatalf("status/call = %d/%q, want %d/%q; body=%s", response.Code, external.call, test.wantStatus, test.wantCall, response.Body.String())
			}
			if stringsHasPrefix(test.wantCall, "external-") && test.wantCall != "external-link" && external.client.SessionID != "client-session-a" {
				t.Fatalf("external client session binding missing: %+v", external.client)
			}
		})
	}
}

func TestExternalRoutesFailClosedAndRedactErrors(t *testing.T) {
	base := &userServiceStub{}
	resolver := &userResolverStub{}
	withoutExternal := NewUserHandler(base, resolver)
	response := serveUser(withoutExternal, http.MethodPost, "/api/v1/auth/external/oidc/start", `{"mode":"redirect","return_target_code":"account"}`, "client", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled provider status = %d, body=%s", response.Code, response.Body.String())
	}

	external := &externalUserServiceStub{err: ErrExternalFlowInvalid}
	handler := NewUserHandler(base, resolver, WithExternalUserService(external))
	response = serveUser(handler, http.MethodPost, "/api/v1/auth/external/oidc/callback", `{"flow_id":"flow-external-123456","state":"state-state-state-state-state-1234","code":"private-provider-code"}`, "client", "")
	if response.Code != http.StatusBadRequest || contains(response.Body.String(), "private-provider-code") {
		t.Fatalf("external error leaked or wrong status: %d %s", response.Code, response.Body.String())
	}

	external.err = errors.New("private provider response")
	response = serveUser(handler, http.MethodPost, "/api/v1/auth/external/oidc/start", `{"mode":"redirect","return_target_code":"account"}`, "client", "")
	if response.Code != http.StatusInternalServerError || contains(response.Body.String(), "private provider response") {
		t.Fatalf("internal provider error leaked: %d %s", response.Code, response.Body.String())
	}
}

func TestWechatExchangeRejectsProviderErrorPayload(t *testing.T) {
	external := &externalUserServiceStub{}
	handler := NewUserHandler(&userServiceStub{}, &userResolverStub{}, WithExternalUserService(external))
	response := serveUser(handler, http.MethodPost, "/api/v1/auth/external/wechat/exchange", `{"flow_id":"flow-external-123456","state":"state-state-state-state-state-1234","provider_error":"ACCESS_DENIED"}`, "client", "")
	if response.Code != http.StatusBadRequest || external.call != "" {
		t.Fatalf("wechat provider_error status/call = %d/%q; body=%s", response.Code, external.call, response.Body.String())
	}
}

func stringsHasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
func contains(value, target string) bool {
	for i := 0; i+len(target) <= len(value); i++ {
		if value[i:i+len(target)] == target {
			return true
		}
	}
	return false
}

var _ ExternalUserService = (*externalUserServiceStub)(nil)
