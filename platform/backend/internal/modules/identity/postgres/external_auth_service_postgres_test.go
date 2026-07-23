package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/identity"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

type externalRegistry struct {
	disabled bool
}

func TestConcurrentExternalIdentityUnlinkPreservesOneLoginMethod(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	sessions := newEndUserService(t, repository, acceptingRegistrationProof{}, &capturingRecoveryDelivery{}, func() time.Time { return now })
	registered, err := sessions.Register(context.Background(), identity.EndUserRegisterCommand{Scope: identity.EndUserSessionScope{ProductID: "product.concurrent-unlink", ApplicationID: "application.concurrent-unlink", Environment: "test"}, Identifier: "concurrent-unlink@example.com", Credential: "correct concurrent password", VerificationProof: strings.Repeat("u", 16), TraceID: "trace.concurrent-unlink.register", IdempotencyKey: "register-concurrent-unlink-01"})
	if err != nil {
		t.Fatal(err)
	}
	userID := registered.Session.UserID
	if _, err := database.Pool.Exec(context.Background(), `UPDATE identity.user_credentials SET credential_status='revoked' WHERE user_id=$1 AND credential_type='password'`, userID); err != nil {
		t.Fatal(err)
	}
	for index, externalID := range []string{"ext_concurrent_unlink_a", "ext_concurrent_unlink_b"} {
		if _, err := database.Pool.Exec(context.Background(), `INSERT INTO identity.external_identities(external_identity_id,user_id,provider,provider_application_id,subject_digest,subject_masked,status,identity_version,linked_at,updated_at) VALUES($1,$2,'oidc',$3,$4,'oidc identity','active',1,$5,$5)`, externalID, userID, "oidc.concurrent."+externalID, bytes.Repeat([]byte{byte(index + 1)}, 32), now); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	errorsByIdentity := make(chan error, 2)
	var wait sync.WaitGroup
	for index, externalID := range []string{"ext_concurrent_unlink_a", "ext_concurrent_unlink_b"} {
		wait.Add(1)
		go func(index int, externalID string) {
			defer wait.Done()
			<-start
			event := identity.OutboxEvent{EventID: fmt.Sprintf("evt_concurrent_unlink_%d", index), Topic: "identity.external_identity_unlinked.v1", Now: now, Payload: identity.SecurityEvent{AuditID: fmt.Sprintf("audit_concurrent_unlink_%d", index), OccurredAt: now, ActorID: userID, Action: "identity.external_identity_unlinked", TargetType: "external_identity", TargetID: externalID, Result: "success", TraceID: fmt.Sprintf("trace.concurrent-unlink.%d", index), RiskLevel: "medium"}}
			errorsByIdentity <- repository.UnlinkExternalIdentity(context.Background(), userID, externalID, now, event)
		}(index, externalID)
	}
	close(start)
	wait.Wait()
	close(errorsByIdentity)
	var succeeded, rejected int
	for unlinkErr := range errorsByIdentity {
		switch {
		case unlinkErr == nil:
			succeeded++
		case errors.Is(unlinkErr, identity.ErrExternalIdentityLastLogin):
			rejected++
		default:
			t.Fatalf("unexpected concurrent unlink error: %v", unlinkErr)
		}
	}
	var active int
	if err := database.Pool.QueryRow(context.Background(), `SELECT count(*) FROM identity.external_identities WHERE user_id=$1 AND status='active'`, userID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || rejected != 1 || active != 1 {
		t.Fatalf("concurrent unlink succeeded=%d rejected=%d active=%d", succeeded, rejected, active)
	}
}

func (r externalRegistry) ResolveExternalProvider(_ context.Context, query identity.ExternalProviderQuery) (identity.ExternalProviderApplication, error) {
	return identity.ExternalProviderApplication{Scope: query.Scope, Environment: query.Environment, Provider: query.Provider, ProviderApplicationRef: query.Provider + ".app", Enabled: !r.disabled}, nil
}

type externalProvider struct {
	requests   map[string]identity.ExternalAuthorizationRequest
	claims     identity.VerifiedExternalClaims
	exchanges  int
	onExchange func()
}

func (p *externalProvider) StartAuthorization(_ context.Context, _ identity.ExternalProviderApplication, request identity.ExternalAuthorizationRequest) (identity.ExternalAuthorization, error) {
	if p.requests == nil {
		p.requests = map[string]identity.ExternalAuthorizationRequest{}
	}
	p.requests[request.FlowID] = request
	return identity.ExternalAuthorization{AuthorizationURL: "https://provider.example/authorize"}, nil
}

func (p *externalProvider) ExchangeAuthorizationCode(_ context.Context, _ identity.ExternalProviderApplication, _ string, nonce, verifier string) (identity.VerifiedExternalClaims, error) {
	p.exchanges++
	if p.onExchange != nil {
		p.onExchange()
	}
	if nonce == "" || verifier == "" {
		return identity.VerifiedExternalClaims{}, errors.New("missing nonce or verifier")
	}
	return p.claims, nil
}

type externalReturnTarget struct{}

func (externalReturnTarget) ResolveAuthReturnTarget(_ context.Context, _ identity.EndUserSessionScope, _ string, code string) (identity.AuthReturnTarget, error) {
	return identity.AuthReturnTarget{Code: code, URI: "https://app.example/auth/callback", PolicyVersion: 1}, nil
}

func TestExternalAuthLifecycleIsScopedSingleUseAndFailClosed(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 18, 5, 0, 0, 0, time.UTC)
	clock := now
	sessions := newEndUserService(t, repository, acceptingRegistrationProof{}, &capturingRecoveryDelivery{}, func() time.Time { return clock })
	scope := identity.EndUserSessionScope{ProductID: "product.external", ApplicationID: "application.external", Environment: "test"}
	registered, err := sessions.Register(context.Background(), identity.EndUserRegisterCommand{Scope: scope, Identifier: "external@example.com", Credential: "correct external password", VerificationProof: strings.Repeat("e", 16), TraceID: "trace.external.register", IdempotencyKey: "register-external-key-01"})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := securevalue.NewHasher(strings.Repeat("external-postgres-pepper-", 2))
	if err != nil {
		t.Fatal(err)
	}
	provider := &externalProvider{claims: identity.VerifiedExternalClaims{Provider: "oidc", ProviderApplicationRef: "oidc.app", Subject: "provider-subject-one", MaskedSubject: "su***ne"}}
	service, err := identity.NewExternalAuthService(repository, repository, sessions, externalRegistry{}, provider, externalReturnTarget{}, hasher, identity.ExternalAuthPolicy{FlowTTL: 5 * time.Minute, ProofTTL: 5 * time.Minute, RecentAuthTTL: 10 * time.Minute}, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	start := func(providerName string) identity.ExternalAuthStartResult {
		result, err := service.Start(context.Background(), identity.ExternalAuthStartCommand{Scope: scope, Environment: "test", Provider: providerName, Mode: "redirect", ReturnTargetCode: "account", BrowserSession: "browser-session", TraceID: "trace.external.start"})
		if err != nil {
			t.Fatalf("Start(%s) error = %v", providerName, err)
		}
		return result
	}

	first := start("oidc")
	request := provider.requests[first.FlowID]
	if request.PKCEMethod != "S256" || request.State == "" || request.Nonce == "" || request.PKCEChallenge == "" {
		t.Fatalf("authorization request = %+v", request)
	}
	result, err := service.Complete(context.Background(), identity.ExternalAuthCallbackCommand{FlowID: first.FlowID, Provider: "oidc", BrowserSession: "browser-session", State: request.State, Code: "provider-code-one", TraceID: "trace.external.complete"})
	if err != nil || result.Status != "link_required" || result.ExternalProofID == "" || result.Session != nil {
		t.Fatalf("unbound Complete() result=%+v err=%v", result, err)
	}
	var stateDigest, nonceDigest, challengeDigest, codeDigest []byte
	var failureCode *string
	var policyVersion int64
	if err := database.Pool.QueryRow(context.Background(), `SELECT return_target_policy_version,state_digest,nonce_digest,pkce_challenge_digest,authorization_code_digest,failure_code FROM identity.external_auth_flows WHERE flow_id=$1`, first.FlowID).Scan(&policyVersion, &stateDigest, &nonceDigest, &challengeDigest, &codeDigest, &failureCode); err != nil || policyVersion != 1 || len(stateDigest) != 32 || len(nonceDigest) != 32 || len(challengeDigest) != 32 || len(codeDigest) != 32 || failureCode != nil || string(stateDigest) == request.State || string(nonceDigest) == request.Nonce || string(codeDigest) == "provider-code-one" {
		t.Fatalf("persisted flow secret boundary invalid: state=%d nonce=%d challenge=%d code=%d failure=%v err=%v", len(stateDigest), len(nonceDigest), len(challengeDigest), len(codeDigest), failureCode, err)
	}
	if _, err := service.Complete(context.Background(), identity.ExternalAuthCallbackCommand{FlowID: first.FlowID, Provider: "oidc", BrowserSession: "browser-session", State: request.State, Code: "provider-code-one"}); !errors.Is(err, identity.ErrExternalAuthFlowReplayed) {
		t.Fatalf("flow replay error = %v", err)
	}
	if _, err := database.Pool.Exec(context.Background(), `UPDATE identity.external_auth_flows SET consumed_at=consumed_at+interval '1 second' WHERE flow_id=$1`, first.FlowID); err == nil {
		t.Fatal("consumed flow fact was mutable")
	}

	second := start("oidc")
	secondRequest := provider.requests[second.FlowID]
	if _, err := service.Complete(context.Background(), identity.ExternalAuthCallbackCommand{FlowID: second.FlowID, Provider: "oidc", BrowserSession: "browser-session", State: secondRequest.State, Code: "provider-code-one"}); !errors.Is(err, identity.ErrExternalAuthFlowReplayed) {
		t.Fatalf("cross-flow code replay error = %v", err)
	}
	var proofs int
	if err := database.Pool.QueryRow(context.Background(), `SELECT count(*) FROM identity.external_identity_proofs`).Scan(&proofs); err != nil || proofs != 1 {
		t.Fatalf("proof count after code replay=%d err=%v", proofs, err)
	}
	var proofEnvironment string
	if err := database.Pool.QueryRow(context.Background(), `SELECT environment FROM identity.external_identity_proofs WHERE flow_id=$1`, first.FlowID).Scan(&proofEnvironment); err != nil || proofEnvironment != scope.Environment {
		t.Fatalf("external proof environment=%q err=%v", proofEnvironment, err)
	}
	forgedSession := registered.Session
	forgedSession.Environment = "production"
	if _, err := service.Link(context.Background(), identity.LinkExternalIdentityCommand{Session: forgedSession, Provider: "oidc", ExternalProofID: result.ExternalProofID, IdempotencyKey: "external-link-cross-env-01", TraceID: "trace.external.cross-env"}); !errors.Is(err, identity.ErrExternalProofInvalid) {
		t.Fatalf("cross-environment link error=%v", err)
	}

	linked, err := service.Link(context.Background(), identity.LinkExternalIdentityCommand{Session: registered.Session, Provider: "oidc", ExternalProofID: result.ExternalProofID, IdempotencyKey: "external-link-key-0001", TraceID: "trace.external.link"})
	if err != nil || linked.UserID != registered.Session.UserID || linked.ProviderApplicationID != "oidc.app" || linked.SubjectMasked != "oidc identity" || len(linked.SubjectDigest) != 0 || len(linked.UnionSubjectDigest) != 0 {
		t.Fatalf("Link() result=%+v err=%v", linked, err)
	}
	if replay, err := service.Link(context.Background(), identity.LinkExternalIdentityCommand{Session: registered.Session, Provider: "oidc", ExternalProofID: result.ExternalProofID, IdempotencyKey: "external-link-key-0001"}); err != nil || replay.ExternalIdentityID != linked.ExternalIdentityID {
		t.Fatalf("proof idempotent replay = %+v, %v", replay, err)
	}
	if _, err := service.Link(context.Background(), identity.LinkExternalIdentityCommand{Session: registered.Session, Provider: "oidc", ExternalProofID: "different-proof", IdempotencyKey: "external-link-key-0001"}); !errors.Is(err, identity.ErrEndUserVersionConflict) {
		t.Fatalf("changed link request error = %v", err)
	}
	if _, err := database.Pool.Exec(context.Background(), `UPDATE identity.external_identity_proofs SET consumed_at=consumed_at+interval '1 second' WHERE flow_id=$1`, first.FlowID); err == nil {
		t.Fatal("consumed external proof fact was mutable")
	}

	third := start("oidc")
	thirdRequest := provider.requests[third.FlowID]
	authenticated, err := service.Complete(context.Background(), identity.ExternalAuthCallbackCommand{FlowID: third.FlowID, Provider: "oidc", BrowserSession: "browser-session", State: thirdRequest.State, Code: "provider-code-two", TraceID: "trace.external.login"})
	if err != nil || authenticated.Status != "authenticated" || authenticated.Session == nil || authenticated.Session.Session.AuthenticationMethod != "oidc" || authenticated.Session.Session.UserID != registered.Session.UserID {
		t.Fatalf("bound Complete() result=%+v err=%v", authenticated, err)
	}
	var sessionExternalIdentityID, sessionEnvironment string
	if err := database.Pool.QueryRow(context.Background(), `SELECT external_identity_id,environment FROM identity.end_user_sessions WHERE session_id=$1`, authenticated.Session.Session.SessionID).Scan(&sessionExternalIdentityID, &sessionEnvironment); err != nil || sessionExternalIdentityID != linked.ExternalIdentityID || sessionEnvironment != "test" {
		t.Fatalf("external session provenance=%q environment=%q err=%v", sessionExternalIdentityID, sessionEnvironment, err)
	}

	wechat := start("wechat")
	wechatRequest := provider.requests[wechat.FlowID]
	provider.claims = identity.VerifiedExternalClaims{Provider: "wechat", ProviderApplicationRef: "wechat.app", Subject: "wechat-subject", MaskedSubject: "bad\nmask"}
	exchangesBefore := provider.exchanges
	if _, err := service.Complete(context.Background(), identity.ExternalAuthCallbackCommand{FlowID: wechat.FlowID, Provider: "oidc", BrowserSession: "browser-session", State: wechatRequest.State, Code: "wechat-code"}); !errors.Is(err, identity.ErrExternalAuthFlowInvalid) || provider.exchanges != exchangesBefore {
		t.Fatalf("path provider mismatch error=%v exchanges=%d/%d", err, provider.exchanges, exchangesBefore)
	}
	if _, err := service.Complete(context.Background(), identity.ExternalAuthCallbackCommand{FlowID: wechat.FlowID, Provider: "wechat", BrowserSession: "browser-session", State: "wrong-state", Code: "wechat-code"}); !errors.Is(err, identity.ErrExternalAuthFlowInvalid) || provider.exchanges != exchangesBefore {
		t.Fatalf("wechat missing state boundary error=%v exchanges=%d/%d", err, provider.exchanges, exchangesBefore)
	}
	wrongClientScope := identity.EndUserSessionScope{ProductID: "other-product", ApplicationID: scope.ApplicationID, Environment: scope.Environment}
	if _, err := service.Complete(context.Background(), identity.ExternalAuthCallbackCommand{FlowID: wechat.FlowID, Provider: "wechat", ExpectedScope: &wrongClientScope, BrowserSession: "browser-session", State: wechatRequest.State, Code: "wechat-code"}); !errors.Is(err, identity.ErrExternalAuthFlowInvalid) || provider.exchanges != exchangesBefore {
		t.Fatalf("wechat cross-client scope error=%v exchanges=%d/%d", err, provider.exchanges, exchangesBefore)
	}
	if _, err := service.Complete(context.Background(), identity.ExternalAuthCallbackCommand{FlowID: wechat.FlowID, Provider: "wechat", ExpectedScope: &scope, BrowserSession: "other-browser", State: wechatRequest.State, Code: "wechat-code"}); !errors.Is(err, identity.ErrExternalAuthFlowInvalid) || provider.exchanges != exchangesBefore {
		t.Fatalf("wechat cross-browser session error=%v exchanges=%d/%d", err, provider.exchanges, exchangesBefore)
	}
	wechatResult, err := service.Complete(context.Background(), identity.ExternalAuthCallbackCommand{FlowID: wechat.FlowID, Provider: "wechat", ExpectedScope: &scope, BrowserSession: "browser-session", State: wechatRequest.State, Code: "wechat-code"})
	if err != nil || wechatResult.Status != "link_required" {
		t.Fatalf("wechat state-bound Complete() result=%+v err=%v", wechatResult, err)
	}
	var wechatMask string
	if err := database.Pool.QueryRow(context.Background(), `SELECT subject_masked FROM identity.external_identity_proofs WHERE flow_id=$1`, wechat.FlowID).Scan(&wechatMask); err != nil || wechatMask != "wechat identity" {
		t.Fatalf("invalid provider mask persisted as %q err=%v", wechatMask, err)
	}

	provider.claims = identity.VerifiedExternalClaims{Provider: "oidc", ProviderApplicationRef: "wrong.app", Subject: "provider-subject-one"}
	mismatch := start("oidc")
	if _, err := service.Complete(context.Background(), identity.ExternalAuthCallbackCommand{FlowID: mismatch.FlowID, Provider: "oidc", BrowserSession: "browser-session", State: provider.requests[mismatch.FlowID].State, Code: "provider-code-three"}); !errors.Is(err, identity.ErrExternalAuthFlowInvalid) {
		t.Fatalf("provider application mismatch error = %v", err)
	}

	provider.claims = identity.VerifiedExternalClaims{Provider: "oidc", ProviderApplicationRef: "oidc.app", Subject: "slow-provider-subject"}
	slow := start("oidc")
	slowRequest := provider.requests[slow.FlowID]
	provider.onExchange = func() { clock = clock.Add(31 * time.Second) }
	exchangesBefore = provider.exchanges
	if _, err := service.Complete(context.Background(), identity.ExternalAuthCallbackCommand{FlowID: slow.FlowID, Provider: "oidc", BrowserSession: "browser-session", State: slowRequest.State, Code: "slow-provider-code"}); !errors.Is(err, identity.ErrExternalAuthFlowExpired) {
		t.Fatalf("slow provider completion error = %v", err)
	}
	provider.onExchange = nil
	if _, err := service.Complete(context.Background(), identity.ExternalAuthCallbackCommand{FlowID: slow.FlowID, Provider: "oidc", BrowserSession: "browser-session", State: slowRequest.State, Code: "slow-provider-code"}); !errors.Is(err, identity.ErrExternalAuthFlowExpired) || provider.exchanges != exchangesBefore+1 {
		t.Fatalf("slow provider retry error=%v exchanges=%d/%d", err, provider.exchanges, exchangesBefore+1)
	}
	clock = now

	if _, err := database.Pool.Exec(context.Background(), `UPDATE identity.user_credentials SET credential_status='revoked' WHERE user_id=$1 AND credential_type='password'`, registered.Session.UserID); err != nil {
		t.Fatal(err)
	}
	if err := service.Unlink(context.Background(), identity.UnlinkExternalIdentityCommand{Session: registered.Session, ExternalIdentityID: linked.ExternalIdentityID, TraceID: "trace.external.unlink.last"}); !errors.Is(err, identity.ErrExternalIdentityLastLogin) {
		t.Fatalf("last login method unlink error = %v", err)
	}
	if _, err := database.Pool.Exec(context.Background(), `UPDATE identity.user_credentials SET credential_status='active' WHERE user_id=$1 AND credential_type='password'`, registered.Session.UserID); err != nil {
		t.Fatal(err)
	}
	if err := service.Unlink(context.Background(), identity.UnlinkExternalIdentityCommand{Session: registered.Session, ExternalIdentityID: linked.ExternalIdentityID, TraceID: "trace.external.unlink"}); err != nil {
		t.Fatalf("Unlink() error = %v", err)
	}
	if _, err := sessions.ResolveCurrentSession(context.Background(), authenticated.Session.AccessToken); !errors.Is(err, identity.ErrEndUserSessionRevoked) {
		t.Fatalf("external session after unlink error = %v", err)
	}
	clock = now.Add(11 * time.Minute)
	if err := service.Unlink(context.Background(), identity.UnlinkExternalIdentityCommand{Session: registered.Session, ExternalIdentityID: linked.ExternalIdentityID}); !errors.Is(err, identity.ErrExternalRecentAuthRequired) {
		t.Fatalf("stale auth unlink error = %v", err)
	}
	expired := start("oidc")
	expiredRequest := provider.requests[expired.FlowID]
	clock = clock.Add(6 * time.Minute)
	if _, err := service.Complete(context.Background(), identity.ExternalAuthCallbackCommand{FlowID: expired.FlowID, Provider: "oidc", BrowserSession: "browser-session", State: expiredRequest.State, Code: "expired-provider-code"}); !errors.Is(err, identity.ErrExternalAuthFlowExpired) {
		t.Fatalf("expired external flow error = %v", err)
	}
	var expiredStatus, expiredFailure string
	var expiredConsumed *time.Time
	var expiredCode []byte
	if err := database.Pool.QueryRow(context.Background(), `SELECT status,failure_code,consumed_at,authorization_code_digest FROM identity.external_auth_flows WHERE flow_id=$1`, expired.FlowID).Scan(&expiredStatus, &expiredFailure, &expiredConsumed, &expiredCode); err != nil || expiredStatus != "expired" || expiredFailure != "EXTERNAL_FLOW_EXPIRED" || expiredConsumed == nil || expiredCode != nil {
		t.Fatalf("expired flow terminal status=%q failure=%q consumed=%v code=%x err=%v", expiredStatus, expiredFailure, expiredConsumed, expiredCode, err)
	}

	disabledService, err := identity.NewExternalAuthService(repository, repository, sessions, externalRegistry{disabled: true}, provider, externalReturnTarget{}, hasher, identity.ExternalAuthPolicy{FlowTTL: time.Minute, ProofTTL: time.Minute, RecentAuthTTL: time.Minute}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disabledService.Start(context.Background(), identity.ExternalAuthStartCommand{Scope: scope, Environment: "test", Provider: "oidc", Mode: "redirect", ReturnTargetCode: "account", BrowserSession: "browser-session"}); !errors.Is(err, identity.ErrExternalProviderDisabled) {
		t.Fatalf("disabled provider start error = %v", err)
	}
}

func TestExternalAuthClaimLeaseCannotReexchangeAfterAbandonment(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	hasher, err := securevalue.NewHasher(strings.Repeat("external-claim-pepper-", 2))
	if err != nil {
		t.Fatal(err)
	}
	scope := identity.EndUserSessionScope{ProductID: "product.claim", ApplicationID: "application.claim", Environment: "test"}
	flow := identity.ExternalAuthFlow{FlowID: "flow-claim-abandoned", Scope: scope, Environment: "test", Provider: "oidc", ProviderApplicationRef: "oidc.claim", Mode: "redirect", ReturnTargetCode: "account", ReturnTargetURI: "https://app.example/callback", ReturnTargetPolicyVersion: 1, StateDigest: hasher.Digest("state"), NonceDigest: hasher.Digest("nonce"), PKCEChallengeDigest: hasher.Digest("pkce"), BrowserSessionDigest: hasher.Digest("browser"), CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := repository.CreateExternalAuthFlow(context.Background(), flow); err != nil {
		t.Fatal(err)
	}
	first := identity.ExternalAuthFlowClaim{FlowID: flow.FlowID, Provider: flow.Provider, ExpectedScope: &scope, BrowserSessionDigest: flow.BrowserSessionDigest, StateDigest: flow.StateDigest, ProcessingTokenDigest: hasher.Digest("claim-one"), ProcessingExpiresAt: now.Add(time.Second), Now: now}
	if claimed, err := repository.ClaimExternalAuthFlow(context.Background(), first); err != nil || claimed.Status != "processing" {
		t.Fatalf("first claim=%+v err=%v", claimed, err)
	}
	second := first
	second.ProcessingTokenDigest = hasher.Digest("claim-two")
	if _, err := repository.ClaimExternalAuthFlow(context.Background(), second); !errors.Is(err, identity.ErrExternalAuthFlowReplayed) {
		t.Fatalf("active claim replay error=%v", err)
	}
	second.Now, second.ProcessingExpiresAt = now.Add(2*time.Second), now.Add(3*time.Second)
	if _, err := repository.ClaimExternalAuthFlow(context.Background(), second); !errors.Is(err, identity.ErrExternalAuthFlowExpired) {
		t.Fatalf("abandoned claim error=%v", err)
	}
	var status, failure string
	var consumed *time.Time
	if err := database.Pool.QueryRow(context.Background(), `SELECT status,failure_code,consumed_at FROM identity.external_auth_flows WHERE flow_id=$1`, flow.FlowID).Scan(&status, &failure, &consumed); err != nil || status != "expired" || failure != "EXTERNAL_PROCESSING_EXPIRED" || consumed == nil {
		t.Fatalf("abandoned terminal status=%q failure=%q consumed=%v err=%v", status, failure, consumed, err)
	}
}
