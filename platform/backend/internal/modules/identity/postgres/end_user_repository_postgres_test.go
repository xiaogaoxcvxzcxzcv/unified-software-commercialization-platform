package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"platform.local/capability-platform/backend/internal/modules/identity"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestEndUserRepositoryRegistrationIsAtomicAndStoresOnlyProtectedValues(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 17, 4, 0, 0, 0, time.UTC)
	registration := endUserRegistration(t, "user.end-one", "credential.end-one", "identifier.end-one", "person@example.com", now)
	if err := repository.CreateEndUser(context.Background(), registration); err != nil {
		t.Fatalf("CreateEndUser() error = %v", err)
	}
	credential, err := repository.FindEndUserPasswordCredential(context.Background(), identity.IdentifierEmail, registration.Identifier.NormalizedDigest)
	if err != nil {
		t.Fatalf("FindEndUserPasswordCredential() error = %v", err)
	}
	if credential.UserID != registration.User.UserID || bcrypt.CompareHashAndPassword(credential.PasswordHash, []byte("correct horse battery staple")) != nil {
		t.Fatalf("credential = %+v", credential)
	}
	if err := repository.ReplaceEndUserPassword(context.Background(), registration.User.UserID, "", []byte("not-a-bcrypt-hash"), "bcrypt", credential.Version, now.Add(time.Minute), false, endUserEvent("event.password.invalid", "identity.password_changed.v1", registration.User.UserID, now.Add(time.Minute))); err == nil {
		t.Fatal("ReplaceEndUserPassword accepted an unparseable bcrypt hash")
	}
	unchanged, err := repository.FindEndUserPasswordCredential(context.Background(), identity.IdentifierEmail, registration.Identifier.NormalizedDigest)
	if err != nil || unchanged.Version != credential.Version || string(unchanged.PasswordHash) != string(credential.PasswordHash) {
		t.Fatalf("invalid hash changed credential: %+v, %v", unchanged, err)
	}
	var identifierDigest, passwordHash []byte
	var masked string
	if err := database.Pool.QueryRow(context.Background(), `SELECT identifier_digest,password_hash,identifier_masked FROM identity.user_credentials WHERE credential_id=$1`, registration.Credential.CredentialID).Scan(&identifierDigest, &passwordHash, &masked); err != nil {
		t.Fatal(err)
	}
	if string(identifierDigest) == "person@example.com" || string(passwordHash) == "correct horse battery staple" || masked == "person@example.com" {
		t.Fatal("plaintext identifier or credential was persisted")
	}

	duplicate := endUserRegistration(t, "user.end-two", "credential.end-two", "identifier.end-two", "person@example.com", now)
	duplicate.Identifier.NormalizedDigest = registration.Identifier.NormalizedDigest
	if err := repository.CreateEndUser(context.Background(), duplicate); !errors.Is(err, identity.ErrEndUserIdentifierConflict) {
		t.Fatalf("duplicate CreateEndUser() error = %v", err)
	}
	var count int
	if err := database.Pool.QueryRow(context.Background(), `SELECT count(*) FROM identity.users WHERE user_id=$1`, duplicate.User.UserID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("duplicate transaction left user: count=%d err=%v", count, err)
	}
}

func TestEndUserRepositoryRefreshReplayRevokesFamily(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 17, 5, 0, 0, 0, time.UTC)
	registration := endUserRegistration(t, "user.refresh", "credential.refresh", "identifier.refresh", "refresh@example.com", now)
	if err := repository.CreateEndUser(context.Background(), registration); err != nil {
		t.Fatal(err)
	}
	session := newEndUserSession("session.refresh", "family.refresh", registration.User.UserID, now)
	scope := endUserScope(session.Session)
	invalidSession := session
	invalidSession.Session.SessionID = "session.refresh.invalid-environment"
	invalidSession.Session.Environment = ""
	if err := repository.CreateEndUserSession(context.Background(), invalidSession); !errors.Is(err, identity.ErrEndUserScopeMismatch) {
		t.Fatalf("CreateEndUserSession() empty environment error = %v", err)
	}
	if err := repository.CreateEndUserSession(context.Background(), session); err != nil {
		t.Fatalf("CreateEndUserSession() error = %v", err)
	}
	if _, err := repository.FindEndUserByAccessDigest(context.Background(), session.AccessToken.Digest, scope, now.Add(time.Minute)); err != nil {
		t.Fatalf("FindEndUserByAccessDigest() error = %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			rotationAt := now.Add(2 * time.Minute)
			_, err := repository.RotateEndUserRefresh(context.Background(), session.RefreshToken.Digest, scope, identity.EndUserRefreshRotation{
				AccessToken:     identity.EndUserSessionToken{TokenID: fmt.Sprintf("access.next.%d", index), TokenType: "access", Generation: 2, Digest: protectedDigest(fmt.Sprintf("access-next-%d", index)), CreatedAt: rotationAt, ExpiresAt: rotationAt.Add(15 * time.Minute)},
				RefreshToken:    identity.EndUserSessionToken{TokenID: fmt.Sprintf("refresh.next.%d", index), TokenType: "refresh", Generation: 2, Digest: protectedDigest(fmt.Sprintf("refresh-next-%d", index)), CreatedAt: rotationAt, ExpiresAt: rotationAt.Add(time.Hour)},
				AccessExpiresAt: rotationAt.Add(15 * time.Minute), RefreshExpiresAt: rotationAt.Add(time.Hour), Now: rotationAt,
				OutboxEvent:   endUserEvent(fmt.Sprintf("event.refresh.%d", index), "identity.session_refreshed.v1", registration.User.UserID, rotationAt),
				RequestDigest: protectedDigest(fmt.Sprintf("request.refresh.%d", index)), RecoveryExpiresAt: rotationAt.Add(time.Minute),
			})
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var succeeded, replayed int
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, identity.ErrEndUserRefreshReplayed):
			replayed++
		default:
			t.Fatalf("RotateEndUserRefresh() error = %v", err)
		}
	}
	if succeeded != 1 || replayed != 1 {
		t.Fatalf("rotation results success=%d replay=%d", succeeded, replayed)
	}
	if _, err := repository.FindEndUserByAccessDigest(context.Background(), session.AccessToken.Digest, scope, now.Add(3*time.Minute)); !errors.Is(err, identity.ErrEndUserSessionRevoked) {
		t.Fatalf("access after replay error = %v", err)
	}
	var reason string
	if err := database.Pool.QueryRow(context.Background(), `SELECT revoke_reason FROM identity.end_user_sessions WHERE session_id=$1`, session.Session.SessionID).Scan(&reason); err != nil || reason != "refresh_replayed" {
		t.Fatalf("revoke reason=%q err=%v", reason, err)
	}
}

func TestEndUserRepositoryRejectsMismatchedTrustedScopeBeforeRefreshMutation(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 17, 5, 30, 0, 0, time.UTC)
	registration := endUserRegistration(t, "user.scope", "credential.scope", "identifier.scope", "scope@example.com", now)
	if err := repository.CreateEndUser(context.Background(), registration); err != nil {
		t.Fatal(err)
	}
	tenant := "tenant.one"
	session := newEndUserSession("session.scope", "family.scope", registration.User.UserID, now)
	session.Session.TenantID = &tenant
	if err := repository.CreateEndUserSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	wrongTenant := "tenant.two"
	wrongScopes := []identity.EndUserSessionScope{
		{ProductID: "product.other", ApplicationID: session.Session.ApplicationID, TenantID: &tenant},
		{ProductID: session.Session.ProductID, ApplicationID: "application.other", TenantID: &tenant},
		{ProductID: session.Session.ProductID, ApplicationID: session.Session.ApplicationID, TenantID: &wrongTenant},
		{ProductID: session.Session.ProductID, ApplicationID: session.Session.ApplicationID},
	}
	for index, scope := range wrongScopes {
		if _, err := repository.FindEndUserByAccessDigest(context.Background(), session.AccessToken.Digest, scope, now.Add(time.Minute)); !errors.Is(err, identity.ErrEndUserScopeMismatch) {
			t.Fatalf("wrong access scope %d error = %v", index, err)
		}
		rotationAt := now.Add(time.Duration(index+1) * time.Minute)
		_, err := repository.RotateEndUserRefresh(context.Background(), session.RefreshToken.Digest, scope, identity.EndUserRefreshRotation{
			AccessToken:     identity.EndUserSessionToken{TokenID: fmt.Sprintf("scope.access.%d", index), TokenType: "access", Generation: 2, Digest: protectedDigest(fmt.Sprintf("scope-access-%d", index)), CreatedAt: rotationAt, ExpiresAt: rotationAt.Add(15 * time.Minute)},
			RefreshToken:    identity.EndUserSessionToken{TokenID: fmt.Sprintf("scope.refresh.%d", index), TokenType: "refresh", Generation: 2, Digest: protectedDigest(fmt.Sprintf("scope-refresh-%d", index)), CreatedAt: rotationAt, ExpiresAt: rotationAt.Add(time.Hour)},
			AccessExpiresAt: rotationAt.Add(15 * time.Minute), RefreshExpiresAt: rotationAt.Add(time.Hour), Now: rotationAt,
			OutboxEvent:   endUserEvent(fmt.Sprintf("event.scope.%d", index), "identity.session_refreshed.v1", registration.User.UserID, rotationAt),
			RequestDigest: protectedDigest(fmt.Sprintf("request.scope.%d", index)), RecoveryExpiresAt: rotationAt.Add(time.Minute),
		})
		if !errors.Is(err, identity.ErrEndUserScopeMismatch) {
			t.Fatalf("wrong scope %d error = %v", index, err)
		}
	}
	if err := repository.RevokeEndUserSession(context.Background(), registration.User.UserID, session.Session.SessionID, wrongScopes[0], "user_requested", now.Add(5*time.Minute), endUserEvent("event.scope.revoke", "identity.session_revoked.v1", registration.User.UserID, now.Add(5*time.Minute))); !errors.Is(err, identity.ErrEndUserScopeMismatch) {
		t.Fatalf("wrong revoke scope error = %v", err)
	}
	var consumed, revoked *time.Time
	if err := database.Pool.QueryRow(context.Background(), `SELECT consumed_at,revoked_at FROM identity.end_user_session_tokens WHERE token_id=$1`, session.RefreshToken.TokenID).Scan(&consumed, &revoked); err != nil || consumed != nil || revoked != nil {
		t.Fatalf("mismatched scope mutated refresh: consumed=%v revoked=%v err=%v", consumed, revoked, err)
	}
	if _, err := repository.FindEndUserByAccessDigest(context.Background(), session.AccessToken.Digest, endUserScope(session.Session), now.Add(6*time.Minute)); err != nil {
		t.Fatalf("correct scope access after rejected refresh = %v", err)
	}
}

func TestEndUserRepositoryScopedRevocationIsVersionedAndIsolated(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 17, 5, 45, 0, 0, time.UTC)
	registration := endUserRegistration(t, "user.scoped-revoke", "credential.scoped-revoke", "identifier.scoped-revoke", "revoke@example.com", now)
	if err := repository.CreateEndUser(context.Background(), registration); err != nil {
		t.Fatal(err)
	}
	tenantOne, tenantTwo := "tenant.one", "tenant.two"
	oldTenantOne := newEndUserSession("session.old.t1", "family.old.t1", registration.User.UserID, now.Add(-time.Hour))
	oldTenantOne.Session.TenantID = &tenantOne
	newTenantOne := newEndUserSession("session.new.t1", "family.new.t1", registration.User.UserID, now.Add(time.Minute))
	newTenantOne.Session.TenantID = &tenantOne
	oldTenantTwo := newEndUserSession("session.old.t2", "family.old.t2", registration.User.UserID, now.Add(-time.Hour))
	oldTenantTwo.Session.TenantID = &tenantTwo
	otherProduct := newEndUserSession("session.other-product", "family.other-product", registration.User.UserID, now.Add(-time.Hour))
	otherProduct.Session.ProductID = "product.other"
	for _, session := range []identity.NewEndUserSession{oldTenantOne, newTenantOne, oldTenantTwo, otherProduct} {
		if err := repository.CreateEndUserSession(context.Background(), session); err != nil {
			t.Fatal(err)
		}
	}
	command := scopedRevocation("event.revoke.t1.v1", registration.User.UserID, oldTenantOne.Session.ProductID, &tenantOne, now, 1)
	if err := repository.RevokeScopedSessions(context.Background(), command); err != nil {
		t.Fatalf("RevokeScopedSessions() error = %v", err)
	}
	if err := repository.RevokeScopedSessions(context.Background(), command); err != nil {
		t.Fatalf("idempotent RevokeScopedSessions() error = %v", err)
	}
	assertEndUserSessionRevoked(t, database, oldTenantOne.Session.SessionID, true)
	assertEndUserSessionRevoked(t, database, newTenantOne.Session.SessionID, false)
	assertEndUserSessionRevoked(t, database, oldTenantTwo.Session.SessionID, false)
	assertEndUserSessionRevoked(t, database, otherProduct.Session.SessionID, false)

	productCommand := scopedRevocation("event.revoke.product.v2", registration.User.UserID, oldTenantOne.Session.ProductID, nil, now, 2)
	if err := repository.RevokeScopedSessions(context.Background(), productCommand); err != nil {
		t.Fatalf("product RevokeScopedSessions() error = %v", err)
	}
	assertEndUserSessionRevoked(t, database, oldTenantTwo.Session.SessionID, true)
	assertEndUserSessionRevoked(t, database, newTenantOne.Session.SessionID, false)
	assertEndUserSessionRevoked(t, database, otherProduct.Session.SessionID, false)

	stale := scopedRevocation("event.revoke.product.stale", registration.User.UserID, oldTenantOne.Session.ProductID, nil, now.Add(2*time.Hour), 1)
	if err := repository.RevokeScopedSessions(context.Background(), stale); err != nil {
		t.Fatalf("stale RevokeScopedSessions() error = %v", err)
	}
	assertEndUserSessionRevoked(t, database, newTenantOne.Session.SessionID, false)
}

func TestEndUserRepositoryListsOnlyActiveSessionsWithinTrustedScope(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	var databaseNow time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	registration := endUserRegistration(t, "user.session-list", "credential.session-list", "identifier.session-list", "session-list@example.com", databaseNow.Add(-4*time.Hour))
	if err := repository.CreateEndUser(ctx, registration); err != nil {
		t.Fatal(err)
	}

	activeOlder := newEndUserSession("session.list.active-older", "family.list.active-older", registration.User.UserID, databaseNow.Add(-30*time.Minute))
	revoked := newEndUserSession("session.list.revoked", "family.list.revoked", registration.User.UserID, databaseNow.Add(-20*time.Minute))
	expired := newEndUserSession("session.list.expired", "family.list.expired", registration.User.UserID, databaseNow.Add(-2*time.Hour))
	current := newEndUserSession("session.list.current", "family.list.current", registration.User.UserID, databaseNow.Add(-10*time.Minute))
	otherScope := newEndUserSession("session.list.other-scope", "family.list.other-scope", registration.User.UserID, databaseNow.Add(-5*time.Minute))
	otherScope.Session.ApplicationID = "application.other"
	for _, session := range []identity.NewEndUserSession{activeOlder, revoked, expired, current, otherScope} {
		if err := repository.CreateEndUserSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE identity.end_user_sessions SET revoked_at=clock_timestamp(),revoke_reason='test_revoked' WHERE session_id=$1`, revoked.Session.SessionID); err != nil {
		t.Fatal(err)
	}

	got, err := repository.ListEndUserSessions(ctx, registration.User.UserID, current.Session.SessionID, endUserScope(current.Session))
	if err != nil {
		t.Fatalf("ListEndUserSessions() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("active sessions = %+v", got)
	}
	if got[0].SessionID != current.Session.SessionID || !got[0].Current || got[1].SessionID != activeOlder.Session.SessionID || got[1].Current {
		t.Fatalf("active session order/current projection = %+v", got)
	}
	for _, item := range got {
		if item.ProductID != current.Session.ProductID || item.ApplicationID != current.Session.ApplicationID || item.Environment != current.Session.Environment || item.TenantID != nil || item.RevokedAt != nil || !item.ExpiresAt.After(databaseNow) {
			t.Fatalf("active session scope/projection changed = %+v", item)
		}
	}
	var historicalRows int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM identity.end_user_sessions WHERE user_id=$1 AND product_id=$2 AND application_id=$3`, registration.User.UserID, current.Session.ProductID, current.Session.ApplicationID).Scan(&historicalRows); err != nil || historicalRows != 4 {
		t.Fatalf("session history rows=%d err=%v", historicalRows, err)
	}
}

func TestEndUserRepositoryRecoveryAndExternalIdentityAreSingleOwner(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 17, 6, 0, 0, 0, time.UTC)
	registration := endUserRegistration(t, "user.recovery", "credential.recovery", "identifier.recovery", "recover@example.com", now)
	if err := repository.CreateEndUser(context.Background(), registration); err != nil {
		t.Fatal(err)
	}
	userID := registration.User.UserID
	challenge := identity.RecoveryChallenge{ChallengeID: "challenge.real", ContinuationDigest: protectedDigest("continuation-real"), IdentifierType: identity.IdentifierEmail, IdentifierDigest: registration.Identifier.NormalizedDigest, MatchedUserID: &userID, DeliveryTargetMasked: "r***@example.com", ProofDigest: protectedDigest("proof-real"), MaxAttempts: 3, CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute), OutboxEvent: endUserEvent("event.challenge.real", "identity.recovery_started.v1", "anonymous", now)}
	if err := repository.CreateRecoveryChallenge(context.Background(), challenge); err != nil {
		t.Fatal(err)
	}
	if err := repository.ActivateRecoveryChallenge(context.Background(), challenge.ChallengeID); err != nil {
		t.Fatal(err)
	}
	result, err := repository.ConsumeRecoveryChallenge(context.Background(), challenge.ContinuationDigest, challenge.ProofDigest, now.Add(time.Minute), endUserEvent("event.challenge.real.consume", "identity.recovery_completed.v1", userID, now.Add(time.Minute)))
	if err != nil || result.MatchedUserID == nil || *result.MatchedUserID != userID {
		t.Fatalf("ConsumeRecoveryChallenge() = %+v, %v", result, err)
	}
	if _, err := repository.ConsumeRecoveryChallenge(context.Background(), challenge.ContinuationDigest, challenge.ProofDigest, now.Add(2*time.Minute), endUserEvent("event.challenge.real.replay", "identity.recovery_replayed.v1", "anonymous", now.Add(2*time.Minute))); !errors.Is(err, identity.ErrRecoveryProofReplayed) {
		t.Fatalf("replayed recovery error = %v", err)
	}

	fake := identity.RecoveryChallenge{ChallengeID: "challenge.fake", ContinuationDigest: protectedDigest("continuation-fake"), IdentifierType: identity.IdentifierEmail, IdentifierDigest: protectedDigest("missing@example.com"), DeliveryTargetMasked: "m***@example.com", ProofDigest: protectedDigest("proof-fake"), MaxAttempts: 3, CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute), OutboxEvent: endUserEvent("event.challenge.fake", "identity.recovery_started.v1", "anonymous", now)}
	if err := repository.CreateRecoveryChallenge(context.Background(), fake); err != nil {
		t.Fatal(err)
	}
	if err := repository.ActivateRecoveryChallenge(context.Background(), fake.ChallengeID); err != nil {
		t.Fatal(err)
	}
	fakeResult, err := repository.ConsumeRecoveryChallenge(context.Background(), fake.ContinuationDigest, fake.ProofDigest, now.Add(time.Minute), endUserEvent("event.challenge.fake.consume", "identity.recovery_completed.v1", "anonymous", now.Add(time.Minute)))
	if err != nil || fakeResult.MatchedUserID != nil {
		t.Fatalf("fake recovery = %+v, %v", fakeResult, err)
	}

	external := identity.ExternalIdentity{ExternalIdentityID: "external.one", UserID: userID, Provider: "wechat", ProviderApplicationID: "provider-app.one", SubjectDigest: protectedDigest("openid-one"), SubjectMasked: "op***one", LinkedAt: now, UpdatedAt: now, OutboxEvent: endUserEvent("event.external.one", "identity.external_identity_linked.v1", userID, now)}
	if err := repository.LinkExternalIdentity(context.Background(), external); err != nil {
		t.Fatal(err)
	}
	duplicate := external
	duplicate.ExternalIdentityID = "external.two"
	duplicate.OutboxEvent = endUserEvent("event.external.two", "identity.external_identity_linked.v1", userID, now)
	if err := repository.LinkExternalIdentity(context.Background(), duplicate); !errors.Is(err, identity.ErrExternalIdentityConflict) {
		t.Fatalf("duplicate external identity error = %v", err)
	}
}

func TestEndUserOutboxFailureRollsBackProfileAndPayloadIsRedacted(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 17, 7, 0, 0, 0, time.UTC)
	registration := endUserRegistration(t, "user.outbox", "credential.outbox", "identifier.outbox", "secret-person@example.com", now)
	if err := repository.CreateEndUser(context.Background(), registration); err != nil {
		t.Fatal(err)
	}
	profile := registration.Profile
	profile.DisplayName = "Changed Name"
	profile.UpdatedAt = now.Add(time.Minute)
	if _, err := repository.UpdateEndUserProfile(context.Background(), profile, 1, registration.OutboxEvent); err == nil {
		t.Fatal("duplicate outbox event should fail profile transaction")
	}
	var displayName string
	var version int64
	if err := database.Pool.QueryRow(context.Background(), `SELECT display_name,profile_version FROM identity.user_profiles WHERE user_id=$1`, registration.User.UserID).Scan(&displayName, &version); err != nil || displayName != registration.Profile.DisplayName || version != 1 {
		t.Fatalf("outbox failure did not roll back profile: name=%q version=%d err=%v", displayName, version, err)
	}
	var payload string
	if err := database.Pool.QueryRow(context.Background(), `SELECT payload::text FROM identity.outbox_events WHERE event_id=$1`, registration.OutboxEvent.EventID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{"secret-person@example.com", "correct horse battery staple", "openid", "proof-real"} {
		if strings.Contains(payload, sensitive) {
			t.Fatalf("outbox payload contains sensitive value %q", sensitive)
		}
	}
}

func endUserRegistration(t *testing.T, userID, credentialID, identifierID, email string, now time.Time) identity.EndUserRegistration {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse battery staple"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	return identity.EndUserRegistration{
		User:        identity.EndUser{UserID: userID, AccountStatus: "active", CreatedAt: now, UpdatedAt: now},
		Identifier:  identity.EndUserIdentifier{IdentifierID: identifierID, UserID: userID, Type: identity.IdentifierEmail, NormalizationVersion: 1, NormalizedDigest: protectedDigest(email), MaskedValue: "p***@example.com", VerificationStatus: "verified", VerifiedAt: &now, CreatedAt: now, UpdatedAt: now},
		Credential:  identity.EndUserCredential{CredentialID: credentialID, UserID: userID, PasswordHash: hash, Algorithm: "bcrypt", Status: "active", ChangedAt: now},
		Profile:     identity.EndUserProfile{UserID: userID, DisplayName: "End User", CreatedAt: now, UpdatedAt: now},
		OutboxEvent: endUserEvent("event.register."+userID, "identity.registered.v1", userID, now),
	}
}

func newEndUserSession(sessionID, familyID, userID string, now time.Time) identity.NewEndUserSession {
	return identity.NewEndUserSession{
		Session:      identity.EndUserSession{SessionID: sessionID, UserID: userID, ProductID: "product.one", ApplicationID: "application.one", Environment: "test", TokenFamilyID: familyID, AuthenticationMethod: "password", AuthTime: now, CreatedAt: now, LastSeenAt: now, AccessExpiresAt: now.Add(15 * time.Minute), RefreshExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(24 * time.Hour), RiskSummaryDigest: protectedDigest("risk")},
		AccessToken:  identity.EndUserSessionToken{TokenID: "access." + sessionID, TokenType: "access", Generation: 1, Digest: protectedDigest("access-" + sessionID), CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)},
		RefreshToken: identity.EndUserSessionToken{TokenID: "refresh." + sessionID, TokenType: "refresh", Generation: 1, Digest: protectedDigest("refresh-" + sessionID), CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		OutboxEvent:  endUserEvent("event.session."+sessionID, "identity.session_created.v1", userID, now),
	}
}

func endUserScope(session identity.EndUserSession) identity.EndUserSessionScope {
	return identity.EndUserSessionScope{ProductID: session.ProductID, ApplicationID: session.ApplicationID, TenantID: session.TenantID, Environment: session.Environment}
}

func endUserEvent(eventID, topic, actor string, now time.Time) identity.OutboxEvent {
	return identity.OutboxEvent{EventID: eventID, Topic: topic, Now: now, Payload: identity.SecurityEvent{AuditID: "audit." + eventID, Action: topic, ActorID: actor, TargetType: "end_user", TargetID: fmt.Sprintf("subject.%x", protectedDigest(actor)[:4]), Result: "success", TraceID: "trace." + eventID, RiskLevel: "normal", OccurredAt: now}}
}

func scopedRevocation(eventID, userID, productID string, tenantID *string, cutoff time.Time, version int64) identity.ScopedSessionRevocation {
	return identity.ScopedSessionRevocation{ProductID: productID, UserID: userID, TenantID: tenantID, Cutoff: cutoff, AccessVersion: version, EventIDDigest: protectedDigest(eventID), RequestDigest: protectedDigest(fmt.Sprintf("%s|%s|%v|%s|%d", productID, userID, tenantID, cutoff.UTC().Format(time.RFC3339Nano), version)), ActorDigest: protectedDigest("product-user-access"), OutboxEvent: endUserEvent(eventID, "identity.scoped_sessions_revoked.v1", "product-user-access", cutoff.Add(time.Second))}
}

func assertEndUserSessionRevoked(t *testing.T, database testpostgres.Database, sessionID string, want bool) {
	t.Helper()
	var revoked bool
	if err := database.Pool.QueryRow(context.Background(), `SELECT revoked_at IS NOT NULL FROM identity.end_user_sessions WHERE session_id=$1`, sessionID).Scan(&revoked); err != nil || revoked != want {
		t.Fatalf("session %s revoked=%v want=%v err=%v", sessionID, revoked, want, err)
	}
}

func protectedDigest(value string) []byte {
	digest := sha256.Sum256([]byte("test-domain\x00" + value))
	return digest[:]
}
