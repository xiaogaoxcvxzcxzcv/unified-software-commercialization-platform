package hostedinteraction

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

func TestAEADStateProtectorBindsTrustedContext(t *testing.T) {
	protector, err := NewAEADStateProtector("hosted-state-key", staticSecret{value: bytesOf(7, 32)})
	if err != nil {
		t.Fatal(err)
	}
	securityContext := StateContext{InteractionID: "hint_123456789012345678901234", Route: RouteAuth, Scope: testScope(), TraceID: "trace-state"}
	protected, err := protector.Protect(context.Background(), securityContext, "private-state-value")
	if err != nil || protected.KeyRef != "hosted-state-key" || len(protected.Ciphertext) == 0 || len(protected.Digest) != sha256.Size {
		t.Fatalf("protect = (%+v,%v)", protected, err)
	}
	revealed, err := protector.Reveal(context.Background(), securityContext, protected)
	if err != nil || revealed != "private-state-value" {
		t.Fatalf("reveal = (%q,%v)", revealed, err)
	}
	tampered := securityContext
	tampered.Scope.ApplicationID = "app_other"
	if _, err = protector.Reveal(context.Background(), tampered, protected); !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("tampered context error = %v", err)
	}
	rotated, err := NewAEADStateProtector("new-state-key", keyedSecrets{"hosted-state-key": bytesOf(7, 32), "new-state-key": bytesOf(8, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if value, revealErr := rotated.Reveal(context.Background(), securityContext, protected); revealErr != nil || value != "private-state-value" {
		t.Fatalf("old key after rotation = (%q,%v)", value, revealErr)
	}
}

func TestCompletionCodeRecoveryAndBrowserCSRFDerivation(t *testing.T) {
	hasher, err := securevalue.NewHasher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	repository := &stubRepository{value: accountValue()}
	var validatedScope Scope
	var validatedActor Actor
	service, err := NewService(repository, stubReturnTarget{}, stubIdentity{}, stubSessions{scope: &validatedScope, actor: &validatedActor}, stubState{}, hasher, "https://hosted.test")
	if err != nil {
		t.Fatal(err)
	}

	browser, projection, err := service.OpenBrowserSession(context.Background(), repository.value.InteractionID)
	if err != nil || projection.Status != StatusOpened || browser.CSRFToken == "" {
		t.Fatalf("open = (%+v,%+v,%v)", browser, projection, err)
	}
	if _, err = service.ValidateBrowserWrite(context.Background(), repository.value.InteractionID, browser.Token, browser.CSRFToken); err != nil {
		t.Fatalf("valid csrf: %v", err)
	}
	_, browserSessionID, _, err := service.ResolveBrowserAccess(context.Background(), repository.value.InteractionID, browser.Token)
	if err != nil || browserSessionID != "browser-session-db" {
		t.Fatalf("browser access session id = (%q,%v)", browserSessionID, err)
	}
	if _, err = service.ValidateBrowserWrite(context.Background(), repository.value.InteractionID, browser.Token, browser.CSRFToken+"x"); !errors.Is(err, ErrCSRF) {
		t.Fatalf("invalid csrf error = %v", err)
	}

	command := CompleteAccountCommand{InteractionID: repository.value.InteractionID, BrowserToken: browser.Token, IdempotencyKey: "same-key", TraceID: "trace", Result: "closed"}
	first, err := service.CompleteAccount(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !validatedScope.Matches(repository.value.Scope) || validatedActor.UserID != repository.value.Actor.UserID || validatedActor.UserSessionID != repository.value.Actor.UserSessionID || validatedActor.ClientSessionID != "" {
		t.Fatalf("session validation did not use persisted context: scope=%+v actor=%+v", validatedScope, validatedActor)
	}
	reopened, reopenedProjection, err := service.OpenBrowserSession(context.Background(), repository.value.InteractionID)
	if err != nil || reopenedProjection.Status != StatusCompleted {
		t.Fatalf("reopen completed = (%+v,%+v,%v)", reopened, reopenedProjection, err)
	}
	command.BrowserToken = reopened.Token
	second, err := service.CompleteAccount(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.Code == "" || first.Code != second.Code || first.ReturnURL != second.ReturnURL {
		t.Fatalf("completion not recoverable: first=%+v second=%+v", first, second)
	}
	if first.GrantExpiresAt.IsZero() || !first.GrantExpiresAt.Equal(second.GrantExpiresAt) {
		t.Fatalf("grant expiry not recovered: first=%v second=%v", first.GrantExpiresAt, second.GrantExpiresAt)
	}
	if repository.completeCalls != 2 || repository.grant.GrantID == "" {
		t.Fatalf("complete calls=%d grant=%+v", repository.completeCalls, repository.grant)
	}
}

func TestExchangePreservesIssuedUserSessionProjection(t *testing.T) {
	hasher, _ := securevalue.NewHasher("0123456789abcdef0123456789abcdef")
	productID, tenantID := "prod_test", "tenant_test"
	accessVersion := int64(7)
	accessStatus := "active"
	issued := IssuedUserSession{SessionID: "session_1", AccessToken: "access-token-value", RefreshToken: "refresh-token-value", AccessExpiresAt: time.Now().Add(time.Minute), RefreshExpiresAt: time.Now().Add(time.Hour), User: SafeUserSummary{UserID: "user_1", AccountStatus: "active", DisplayName: "Safe Name", ProductID: &productID, TenantID: &tenantID, AccessVersion: &accessVersion, ProductAccessStatus: &accessStatus}}
	value := accountValue()
	value.Route, value.Actor, value.Status = RouteAuth, Actor{Kind: "client", ClientSessionID: "client"}, StatusCompleted
	repository := &stubRepository{value: value, claim: ClaimedGrant{GrantID: "hgrant_123456789012345678901234", GrantType: "authorization_code", IdentityProofID: "hproof_123456789012345678901234", Scope: value.Scope}}
	service, err := NewService(repository, stubReturnTarget{}, stubIdentity{issued: issued}, stubSessions{}, stubState{}, hasher, "https://hosted.test")
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Exchange(context.Background(), ExchangeCommand{InteractionID: value.InteractionID, Code: "code-value", CodeVerifier: "verifier-value", TraceID: "trace", Scope: value.Scope})
	if err != nil {
		t.Fatal(err)
	}
	if result.UserSession == nil || result.UserSession.AccessExpiresAt != issued.AccessExpiresAt || result.UserSession.RefreshExpiresAt != issued.RefreshExpiresAt || result.UserSession.User.UserID != "user_1" || result.UserSession.User.DisplayName != "Safe Name" {
		t.Fatalf("issued session projection lost fields: %+v", result.UserSession)
	}
}

func TestAuthenticateTrustsDatabaseProofIntervalInsteadOfApplicationClock(t *testing.T) {
	hasher, _ := securevalue.NewHasher("0123456789abcdef0123456789abcdef")
	value := accountValue()
	value.Route = RouteAuth
	value.Actor = Actor{Kind: "client", ClientSessionID: "client"}
	repository := &stubRepository{value: value}
	databaseAuthTime := time.Now().Add(-2 * time.Hour)
	var receivedRisk map[string]any
	identity := stubIdentity{proof: HostedAuthProof{ProofID: "hproof_123456789012345678901234", AuthTime: databaseAuthTime, ExpiresAt: databaseAuthTime.Add(5 * time.Minute)}, receivedRisk: &receivedRisk}
	service, err := NewService(repository, stubReturnTarget{}, identity, stubSessions{}, stubState{}, hasher, "https://hosted.test")
	if err != nil {
		t.Fatal(err)
	}
	browser, _, err := service.OpenBrowserSession(context.Background(), value.InteractionID)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := service.Authenticate(context.Background(), AuthenticateCommand{InteractionID: value.InteractionID, BrowserToken: browser.Token, Identifier: "person@example.test", Credential: "correct-password", Source: "browser", TraceID: "trace-auth", Risk: map[string]any{"score": 2.5, "trusted": true, "optional": nil, "source": "test"}})
	if err != nil || completion.Code == "" {
		t.Fatalf("Authenticate() = (%+v, %v)", completion, err)
	}
	if receivedRisk["score"] != 2.5 || receivedRisk["trusted"] != true || receivedRisk["optional"] != nil || receivedRisk["source"] != "test" {
		t.Fatalf("risk summary types lost: %#v", receivedRisk)
	}
}

func TestReturnTargetSchemePolicy(t *testing.T) {
	values := []struct {
		target  ReturnTarget
		channel Channel
		valid   bool
	}{
		{ReturnTarget{URI: "https://client.test/callback", Kind: "web_redirect"}, ChannelWeb, true},
		{ReturnTarget{URI: "http://client.test/callback", Kind: "web_redirect"}, ChannelWeb, false},
		{ReturnTarget{URI: "javascript:alert(1)", Kind: "deep_link"}, ChannelApp, false},
		{ReturnTarget{URI: "file:///secret", Kind: "deep_link"}, ChannelDesktop, false},
		{ReturnTarget{URI: "myapp://callback/path", Kind: "deep_link"}, ChannelApp, true},
		{ReturnTarget{URI: "myapp://callback/path#secret", Kind: "deep_link"}, ChannelApp, false},
		{ReturnTarget{URI: "https://client.test/callback?token=secret", Kind: "web_redirect"}, ChannelWeb, false},
	}
	for _, value := range values {
		if got := validReturnURI(value.target, value.channel); got != value.valid {
			t.Errorf("validReturnURI(%q,%q)=%v want %v", value.target.URI, value.channel, got, value.valid)
		}
	}
	if built := buildReturnURL("https://client.test/callback?token=secret", "code-value", "state-value", "hint_123456789012345678901234"); built != "https://client.test/callback?code=code-value&interaction_id=hint_123456789012345678901234&state=state-value" {
		t.Fatalf("return URL retained untrusted query: %s", built)
	}
}

func TestCreateSecurityShapeIsEnforcedBelowHTTP(t *testing.T) {
	command := CreateCommand{Scope: testScope(), Actor: Actor{Kind: "client", ClientSessionID: "client"}, Route: RouteAuth, ReturnTargetCode: "auth.callback", State: "1234567890123456789012", Nonce: "1234567890123456789012", CodeChallenge: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", CodeChallengeMethod: "S256", IdempotencyKey: "key", TraceID: "trace"}
	if !validCreate(command) {
		t.Fatal("valid auth create was rejected")
	}
	command.State = "short"
	if validCreate(command) {
		t.Fatal("short state accepted")
	}
	command.State = "1234567890123456789012"
	command.CodeChallenge = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA!"
	if validCreate(command) {
		t.Fatal("non-base64url PKCE challenge accepted")
	}
}

func TestPortTransientErrorsPassThrough(t *testing.T) {
	hasher, _ := securevalue.NewHasher("0123456789abcdef0123456789abcdef")
	transient := errors.New("database temporarily unavailable")
	repository := &stubRepository{value: accountValue()}
	service, err := NewService(repository, stubReturnTarget{err: transient}, stubIdentity{}, stubSessions{}, stubState{}, hasher, "https://hosted.test")
	if err != nil {
		t.Fatal(err)
	}
	authCreate := CreateCommand{Scope: testScope(), Actor: Actor{Kind: "client", ClientSessionID: "client"}, Route: RouteAuth, ReturnTargetCode: "auth.callback", State: "1234567890123456789012", Nonce: "1234567890123456789012", CodeChallenge: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", CodeChallengeMethod: "S256", IdempotencyKey: "key", TraceID: "trace"}
	if _, err = service.Create(context.Background(), authCreate); !errors.Is(err, transient) {
		t.Fatalf("return target transient=%v", err)
	}

	sessionService, err := NewService(repository, stubReturnTarget{}, stubIdentity{}, stubSessions{err: transient}, stubState{}, hasher, "https://hosted.test")
	if err != nil {
		t.Fatal(err)
	}
	accountCreate := CreateCommand{Scope: testScope(), Actor: repository.value.Actor, Route: RouteAccount, ReturnTargetCode: "account.callback", State: "1234567890123456789012", IdempotencyKey: "key", TraceID: "trace"}
	if _, err = sessionService.Create(context.Background(), accountCreate); !errors.Is(err, transient) {
		t.Fatalf("create session transient=%v", err)
	}
	if _, err = sessionService.GetForScope(context.Background(), repository.value.InteractionID, repository.value.Scope, repository.value.Actor); !errors.Is(err, transient) {
		t.Fatalf("get session transient=%v", err)
	}
	browser, _, err := sessionService.OpenBrowserSession(context.Background(), repository.value.InteractionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sessionService.CompleteAccount(context.Background(), CompleteAccountCommand{InteractionID: repository.value.InteractionID, BrowserToken: browser.Token, IdempotencyKey: "key", Result: "closed"}); !errors.Is(err, transient) {
		t.Fatalf("complete session transient=%v", err)
	}
}

type staticSecret struct{ value []byte }

func (s staticSecret) ResolveSecret(context.Context, string) ([]byte, error) {
	return append([]byte(nil), s.value...), nil
}

type keyedSecrets map[string][]byte

func (s keyedSecrets) ResolveSecret(_ context.Context, ref string) ([]byte, error) {
	value, ok := s[ref]
	if !ok {
		return nil, errors.New("missing key")
	}
	return append([]byte(nil), value...), nil
}

type stubState struct{}

func (stubState) Protect(_ context.Context, _ StateContext, value string) (ProtectedState, error) {
	sum := sha256.Sum256([]byte(value))
	return ProtectedState{KeyRef: "test-key", Ciphertext: []byte("sealed:" + value), Digest: sum[:]}, nil
}
func (stubState) Reveal(_ context.Context, _ StateContext, value ProtectedState) (string, error) {
	return "state-value", nil
}

type stubReturnTarget struct {
	target ReturnTarget
	err    error
}

func (s stubReturnTarget) ResolveHostedReturnTarget(context.Context, Scope, string) (ReturnTarget, error) {
	return s.target, s.err
}

type stubIdentity struct {
	issued       IssuedUserSession
	proof        HostedAuthProof
	receivedRisk *map[string]any
}

func (s stubIdentity) AuthenticateHosted(_ context.Context, _ Scope, _, _, _ string, risk map[string]any, _ string) (HostedAuthProof, error) {
	if s.receivedRisk != nil {
		*s.receivedRisk = risk
	}
	return s.proof, nil
}
func (s stubIdentity) RedeemHostedAuthGrant(context.Context, string, string, Scope, string) (IssuedUserSession, error) {
	return s.issued, nil
}

type stubSessions struct {
	scope *Scope
	actor *Actor
	err   error
}

func (s stubSessions) ValidateHostedAccountSession(_ context.Context, scope Scope, actor Actor) error {
	if s.scope != nil {
		*s.scope = scope
	}
	if s.actor != nil {
		*s.actor = actor
	}
	return s.err
}

type stubRepository struct {
	value         Interaction
	tokenDigest   []byte
	grant         CompletionGrant
	completeCalls int
	claim         ClaimedGrant
}

func (r *stubRepository) Create(context.Context, CreateRecord) (Interaction, bool, error) {
	return r.value, false, nil
}
func (r *stubRepository) Get(context.Context, string) (Interaction, error) { return r.value, nil }
func (r *stubRepository) GetForScope(context.Context, string, Scope, Actor) (Interaction, error) {
	return r.value, nil
}
func (r *stubRepository) OpenBrowserSession(_ context.Context, record OpenBrowserRecord) (Interaction, time.Time, error) {
	r.tokenDigest = record.TokenDigest
	if r.value.Status != StatusCompleted {
		r.value.Status = StatusOpened
	}
	return r.value, time.Now().Add(time.Minute), nil
}
func (r *stubRepository) ValidateBrowserSession(_ context.Context, _ string, digest []byte) (BrowserAccess, error) {
	if !EqualSecret(r.tokenDigest, digest) {
		return BrowserAccess{}, ErrSessionRevoked
	}
	return BrowserAccess{Interaction: r.value, BrowserSessionID: "browser-session-db"}, nil
}
func (r *stubRepository) BeginAuthentication(_ context.Context, _ string, _, lease []byte, ttl time.Duration) (Interaction, time.Time, error) {
	expires := time.Now().Add(ttl)
	r.value.Status = StatusAuthenticating
	r.value.AuthenticationLeaseDigest = append([]byte(nil), lease...)
	r.value.AuthenticationLeaseExpiresAt = &expires
	return r.value, expires, nil
}
func (r *stubRepository) ResetAuthentication(context.Context, string, []byte, []byte) error {
	return nil
}
func (r *stubRepository) GetCompletionGrant(context.Context, string, []byte) (CompletionGrant, error) {
	return r.grant, nil
}
func (r *stubRepository) Complete(_ context.Context, record CompleteRecord) (Interaction, CompletionGrant, bool, error) {
	r.completeCalls++
	recovered := r.grant.GrantID != ""
	if !recovered {
		r.grant = CompletionGrant{GrantID: record.GrantID, InteractionID: record.InteractionID, GrantType: record.GrantType, IdentityProofID: record.IdentityProofID, ExpiresAt: time.Now().Add(time.Minute)}
		r.value.Status = StatusCompleted
		now := time.Now()
		r.value.CompletedAt = &now
		r.value.ResultKind = record.GrantType
	}
	return r.value, r.grant, recovered, nil
}
func (r *stubRepository) Cancel(context.Context, string, []byte, OutboxEvent) (Interaction, error) {
	return r.value, nil
}
func (r *stubRepository) ClaimGrant(context.Context, string, Scope, []byte, []byte, time.Duration, string, []byte) (ClaimedGrant, error) {
	return r.claim, nil
}
func (r *stubRepository) ConsumeGrant(context.Context, string, []byte, OutboxEvent) (Interaction, error) {
	r.value.Status = StatusExchanged
	return r.value, nil
}
func (r *stubRepository) ExpireDue(context.Context, int) (int, error) { return 0, nil }

func testScope() Scope {
	return Scope{ProductID: "prod_test", ApplicationID: "app_test", Environment: "test", Channel: ChannelWeb}
}
func accountValue() Interaction {
	now := time.Now()
	return Interaction{InteractionID: "hint_123456789012345678901234", Route: RouteAccount, Scope: testScope(), Actor: Actor{Kind: "user", UserID: "user", UserSessionID: "session"}, ReturnTargetURI: "https://client.test/account", ReturnTargetCode: "account.callback", ReturnTargetPolicyVersion: 1, StateProtectorKeyRef: "test-key", StateCiphertext: []byte("sealed"), Status: StatusCreated, Version: 1, TraceID: "trace", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
}
func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for i := range result {
		result[i] = value
	}
	return result
}
