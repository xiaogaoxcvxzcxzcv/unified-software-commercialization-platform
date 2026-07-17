package identity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

type externalRepositoryStub struct {
	ExternalAuthRepository
	flow   ExternalAuthFlow
	writes int
}

func (r *externalRepositoryStub) CreateExternalAuthFlow(_ context.Context, flow ExternalAuthFlow) error {
	r.flow = flow
	r.writes++
	return nil
}

type externalRegistryStub struct {
	application ExternalProviderApplication
	err         error
}

func (r externalRegistryStub) ResolveExternalProvider(context.Context, ExternalProviderQuery) (ExternalProviderApplication, error) {
	return r.application, r.err
}

type externalProviderStub struct {
	request       ExternalAuthorizationRequest
	starts        int
	authorization ExternalAuthorization
}

func (p *externalProviderStub) StartAuthorization(_ context.Context, _ ExternalProviderApplication, request ExternalAuthorizationRequest) (ExternalAuthorization, error) {
	p.request = request
	p.starts++
	if p.authorization.AuthorizationURL != "" || p.authorization.QRPayload != "" {
		return p.authorization, nil
	}
	return ExternalAuthorization{AuthorizationURL: "https://provider.example/authorize"}, nil
}

func TestExternalAuthStartRejectsProviderModeMismatch(t *testing.T) {
	now := time.Date(2026, 7, 18, 4, 20, 0, 0, time.UTC)
	scope := EndUserSessionScope{ProductID: "product.mode", ApplicationID: "application.mode"}
	hasher, err := securevalue.NewHasher(strings.Repeat("external-mode-pepper-", 2))
	if err != nil {
		t.Fatal(err)
	}
	users := &antiEnumerationRepository{}
	sessions, err := NewEndUserService(users, StrictIdentifierNormalizer{}, &countingPasswordVerifier{}, hasher, nil, nil, EndUserPolicy{AccessTTL: time.Minute, RefreshTTL: time.Hour, RefreshAbsoluteTTL: 2 * time.Hour, RefreshRecoveryWindow: time.Minute, RecoveryTTL: time.Minute, RecoveryMaxAttempts: 3, LoginWindow: time.Minute, LoginMaximumAttempts: 3, LoginBlockDuration: time.Minute, RecentAuthTTL: time.Minute}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		mode          string
		authorization ExternalAuthorization
	}{
		{mode: "qr", authorization: ExternalAuthorization{AuthorizationURL: "https://provider.example/authorize"}},
		{mode: "redirect", authorization: ExternalAuthorization{QRPayload: "qr-payload"}},
		{mode: "native", authorization: ExternalAuthorization{AuthorizationURL: "https://provider.example/authorize", QRPayload: "qr-payload"}},
		{mode: "redirect", authorization: ExternalAuthorization{AuthorizationURL: "javascript:alert(1)"}},
		{mode: "redirect", authorization: ExternalAuthorization{AuthorizationURL: "https://user:secret@provider.example/authorize"}},
		{mode: "redirect", authorization: ExternalAuthorization{AuthorizationURL: "https://provider.example/authorize#token"}},
	} {
		repository := &externalRepositoryStub{}
		provider := &externalProviderStub{authorization: test.authorization}
		service, err := NewExternalAuthService(repository, users, sessions, externalRegistryStub{application: ExternalProviderApplication{Scope: scope, Environment: "test", Provider: "oidc", ProviderApplicationRef: "oidc.app", Enabled: true}}, provider, externalReturnTargetStub{target: AuthReturnTarget{Code: "account", URI: "https://app.example/callback", PolicyVersion: 1}}, hasher, ExternalAuthPolicy{FlowTTL: time.Minute, ProofTTL: time.Minute, RecentAuthTTL: time.Minute}, func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Start(context.Background(), ExternalAuthStartCommand{Scope: scope, Environment: "test", Provider: "oidc", Mode: test.mode, ReturnTargetCode: "account", BrowserSession: "browser"}); !errors.Is(err, ErrExternalAuthFlowInvalid) || repository.writes != 0 {
			t.Fatalf("mode=%s authorization=%+v error=%v writes=%d", test.mode, test.authorization, err, repository.writes)
		}
	}
}
func (*externalProviderStub) ExchangeAuthorizationCode(context.Context, ExternalProviderApplication, string, string, string) (VerifiedExternalClaims, error) {
	return VerifiedExternalClaims{}, errors.New("unused")
}

type externalReturnTargetStub struct{ target AuthReturnTarget }

func (r externalReturnTargetStub) ResolveAuthReturnTarget(context.Context, EndUserSessionScope, string, string) (AuthReturnTarget, error) {
	return r.target, nil
}

func TestExternalAuthStartUsesDeterministicS256AndPersistsOnlyDigests(t *testing.T) {
	now := time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)
	scope := EndUserSessionScope{ProductID: "product.external", ApplicationID: "application.external"}
	hasher, err := securevalue.NewHasher(strings.Repeat("external-auth-pepper-", 2))
	if err != nil {
		t.Fatal(err)
	}
	users := &antiEnumerationRepository{}
	sessions, err := NewEndUserService(users, StrictIdentifierNormalizer{}, &countingPasswordVerifier{}, hasher, nil, nil, EndUserPolicy{AccessTTL: time.Minute, RefreshTTL: time.Hour, RefreshAbsoluteTTL: 2 * time.Hour, RefreshRecoveryWindow: time.Minute, RecoveryTTL: time.Minute, RecoveryMaxAttempts: 3, LoginWindow: time.Minute, LoginMaximumAttempts: 3, LoginBlockDuration: time.Minute, RecentAuthTTL: time.Minute}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	repository := &externalRepositoryStub{}
	provider := &externalProviderStub{}
	registry := externalRegistryStub{application: ExternalProviderApplication{Scope: scope, Environment: "test", Provider: "oidc", ProviderApplicationRef: "oidc.app", Enabled: true}}
	service, err := NewExternalAuthService(repository, users, sessions, registry, provider, externalReturnTargetStub{target: AuthReturnTarget{Code: "account", URI: "https://app.example/callback", PolicyVersion: 1}}, hasher, ExternalAuthPolicy{FlowTTL: 5 * time.Minute, ProofTTL: 5 * time.Minute, RecentAuthTTL: time.Minute}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(context.Background(), ExternalAuthStartCommand{Scope: scope, Environment: "test", Provider: "oidc", Mode: "redirect", ReturnTargetCode: "account"}); !errors.Is(err, ErrExternalAuthFlowInvalid) {
		t.Fatalf("empty browser session error = %v", err)
	}
	result, err := service.Start(context.Background(), ExternalAuthStartCommand{Scope: scope, Environment: "test", Provider: "oidc", Mode: "redirect", ReturnTargetCode: "account", BrowserSession: "browser-secret"})
	if err != nil || result.FlowID == "" || repository.writes != 1 || provider.starts != 1 {
		t.Fatalf("Start() result=%+v err=%v writes=%d starts=%d", result, err, repository.writes, provider.starts)
	}
	verifier := service.externalSecret("pkce-verifier", result.FlowID)
	wantRaw := sha256.Sum256([]byte(verifier))
	wantChallenge := base64.RawURLEncoding.EncodeToString(wantRaw[:])
	if provider.request.PKCEMethod != "S256" || provider.request.PKCEChallenge != wantChallenge || provider.request.State == "" || provider.request.Nonce == "" {
		t.Fatalf("authorization request = %+v", provider.request)
	}
	if string(repository.flow.StateDigest) == provider.request.State || string(repository.flow.NonceDigest) == provider.request.Nonce || string(repository.flow.PKCEChallengeDigest) == provider.request.PKCEChallenge || len(repository.flow.StateDigest) != 32 || len(repository.flow.NonceDigest) != 32 || len(repository.flow.PKCEChallengeDigest) != 32 {
		t.Fatalf("flow persisted plaintext or invalid digest: %+v", repository.flow)
	}
	if _, err := service.Link(context.Background(), LinkExternalIdentityCommand{Session: EndUserSession{UserID: "user", ProductID: scope.ProductID, ApplicationID: scope.ApplicationID, AuthTime: now.Add(time.Second)}, Provider: "oidc", ExternalProofID: "proof", IdempotencyKey: "future-auth-link-key"}); !errors.Is(err, ErrExternalRecentAuthRequired) {
		t.Fatalf("future auth time error = %v", err)
	}
	if _, err := service.Complete(context.Background(), ExternalAuthCallbackCommand{FlowID: "flow", Provider: "oidc", BrowserSession: "browser", State: "state", ProviderError: "invalid provider error"}); !errors.Is(err, ErrExternalAuthFlowInvalid) {
		t.Fatalf("unsafe provider error code = %v", err)
	}
}

type registrationRepositoryStub struct {
	RegistrationVerificationRepository
}

type securityDeliveryStub struct{}

func (securityDeliveryStub) EnqueueSecurity(context.Context, SecurityDeliveryCommand) error {
	return nil
}

func TestSecurityServicesRejectUnconfiguredHasher(t *testing.T) {
	now := time.Now().UTC()
	users := &antiEnumerationRepository{}
	configured, _ := securevalue.NewHasher(strings.Repeat("configured-pepper-", 2))
	sessions, err := NewEndUserService(users, StrictIdentifierNormalizer{}, &countingPasswordVerifier{}, configured, nil, nil, EndUserPolicy{AccessTTL: time.Minute, RefreshTTL: time.Hour, RefreshAbsoluteTTL: 2 * time.Hour, RefreshRecoveryWindow: time.Minute, RecoveryTTL: time.Minute, RecoveryMaxAttempts: 3, LoginWindow: time.Minute, LoginMaximumAttempts: 3, LoginBlockDuration: time.Minute, RecentAuthTTL: time.Minute}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewExternalAuthService(&externalRepositoryStub{}, users, sessions, externalRegistryStub{}, &externalProviderStub{}, externalReturnTargetStub{}, securevalue.Hasher{}, ExternalAuthPolicy{FlowTTL: time.Minute, ProofTTL: time.Minute, RecentAuthTTL: time.Minute}, time.Now); err == nil {
		t.Fatal("external auth accepted unconfigured hasher")
	}
	if _, err := NewRegistrationVerificationService(&registrationRepositoryStub{}, StrictIdentifierNormalizer{}, securevalue.Hasher{}, securityDeliveryStub{}, RegistrationVerificationPolicy{TTL: time.Minute, MaxAttempts: 3}, time.Now); err == nil {
		t.Fatal("registration verification accepted unconfigured hasher")
	}
}

func TestExternalAuthStartFailsClosedForDisabledOrMismatchedProvider(t *testing.T) {
	now := time.Date(2026, 7, 18, 4, 10, 0, 0, time.UTC)
	scope := EndUserSessionScope{ProductID: "product.external", ApplicationID: "application.external"}
	hasher, err := securevalue.NewHasher(strings.Repeat("external-disabled-pepper-", 2))
	if err != nil {
		t.Fatal(err)
	}
	users := &antiEnumerationRepository{}
	sessions, err := NewEndUserService(users, StrictIdentifierNormalizer{}, &countingPasswordVerifier{}, hasher, nil, nil, EndUserPolicy{AccessTTL: time.Minute, RefreshTTL: time.Hour, RefreshAbsoluteTTL: 2 * time.Hour, RefreshRecoveryWindow: time.Minute, RecoveryTTL: time.Minute, RecoveryMaxAttempts: 3, LoginWindow: time.Minute, LoginMaximumAttempts: 3, LoginBlockDuration: time.Minute, RecentAuthTTL: time.Minute}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for _, application := range []ExternalProviderApplication{
		{Scope: scope, Environment: "test", Provider: "oidc", ProviderApplicationRef: "oidc.app", Enabled: false},
		{Scope: EndUserSessionScope{ProductID: "other", ApplicationID: scope.ApplicationID}, Environment: "test", Provider: "oidc", ProviderApplicationRef: "oidc.app", Enabled: true},
	} {
		repository := &externalRepositoryStub{}
		provider := &externalProviderStub{}
		service, err := NewExternalAuthService(repository, users, sessions, externalRegistryStub{application: application}, provider, externalReturnTargetStub{target: AuthReturnTarget{Code: "account", URI: "https://app.example/callback", PolicyVersion: 1}}, hasher, ExternalAuthPolicy{FlowTTL: time.Minute, ProofTTL: time.Minute, RecentAuthTTL: time.Minute}, func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.Start(context.Background(), ExternalAuthStartCommand{Scope: scope, Environment: "test", Provider: "oidc", Mode: "redirect", ReturnTargetCode: "account", BrowserSession: "browser"})
		if !errors.Is(err, ErrExternalProviderDisabled) || repository.writes != 0 || provider.starts != 0 {
			t.Fatalf("disabled provider error=%v writes=%d starts=%d", err, repository.writes, provider.starts)
		}
	}
}
