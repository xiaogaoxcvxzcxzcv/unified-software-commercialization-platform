package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/modules/identity"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestRepositoryRefreshReplayRevokesTokenFamily(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)
	bootstrapUser(t, repository, now)

	session := testSession(now)
	if err := repository.CreateAdminSession(ctx, session); err != nil {
		t.Fatalf("CreateAdminSession() error = %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		index := index
		go func() {
			<-start
			rotationTime := now.Add(time.Minute)
			_, err := repository.RotateRefresh(ctx, session.RefreshToken.Digest, identity.TransportCookie, nil, identity.Rotation{
				AccessToken:    identity.TokenRecord{TokenID: fmt.Sprintf("access-next-%d", index), TokenType: "access", Digest: []byte(fmt.Sprintf("access-next-digest-%d", index)), ExpiresAt: rotationTime.Add(15 * time.Minute)},
				RefreshToken:   identity.TokenRecord{TokenID: fmt.Sprintf("refresh-next-%d", index), TokenType: "refresh", Digest: []byte(fmt.Sprintf("refresh-next-digest-%d", index)), ExpiresAt: rotationTime.Add(time.Hour)},
				CSRFDigest:     []byte(fmt.Sprintf("csrf-next-%d", index)),
				AccessExpires:  rotationTime.Add(15 * time.Minute),
				RefreshExpires: rotationTime.Add(time.Hour),
				Now:            rotationTime,
				OutboxEvent:    securityEvent(fmt.Sprintf("refresh-event-%d", index), rotationTime),
			})
			results <- err
		}()
	}
	close(start)

	var successes, replays int
	for index := 0; index < 2; index++ {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, identity.ErrRefreshReplayed):
			replays++
		default:
			t.Fatalf("RotateRefresh() unexpected error = %v", err)
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("concurrent refresh results: successes=%d replays=%d", successes, replays)
	}
	if _, err := repository.FindByAccessDigest(ctx, session.AccessToken.Digest, now.Add(2*time.Minute)); !errors.Is(err, identity.ErrSessionRevoked) {
		t.Fatalf("old access after replay error = %v, want ErrSessionRevoked", err)
	}
	var reason string
	if err := database.Pool.QueryRow(ctx, `SELECT revoke_reason FROM identity.admin_sessions WHERE session_id=$1`, session.SessionID).Scan(&reason); err != nil {
		t.Fatalf("query revoked session: %v", err)
	}
	if reason != "refresh_replayed" {
		t.Fatalf("revoke reason = %q, want refresh_replayed", reason)
	}
}

type firstCallBlockingAccessResolver struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (r *firstCallBlockingAccessResolver) ResolveAdminAccessSnapshot(context.Context, string, string) (accesscontrol.Snapshot, error) {
	if r.calls.Add(1) == 1 {
		close(r.entered)
		<-r.release
	}
	return accesscontrol.Snapshot{
		AuthorizationVersion: 1,
		Permissions:          []string{"platform.read"},
		Scopes:               []accesscontrol.Scope{{Type: "platform"}},
	}, nil
}

func TestCurrentSessionCannotOverwriteConcurrentRefreshCSRF(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Now().UTC()
	bootstrapUser(t, repository, now)
	hasher, err := securevalue.NewHasher("csrf-race-integration-pepper-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	stored := testSession(now)
	const oldAccess = "old-access-token-for-csrf-race"
	const oldRefresh = "old-refresh-token-for-csrf-race"
	stored.AccessToken.Digest = hasher.Digest(oldAccess)
	stored.RefreshToken.Digest = hasher.Digest(oldRefresh)
	if err := repository.CreateAdminSession(ctx, stored); err != nil {
		t.Fatal(err)
	}
	resolver := &firstCallBlockingAccessResolver{entered: make(chan struct{}), release: make(chan struct{})}
	service, err := identity.NewService(repository, resolver, identity.Bcrypt{Cost: 4}, hasher, identity.Policy{
		AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour,
		LoginWindow: time.Minute, LoginMaximumAttempts: 5, LoginBlockDuration: time.Minute,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	type currentResult struct {
		session identity.AdminSession
		err     error
	}
	currentDone := make(chan currentResult, 1)
	go func() {
		session, currentErr := service.CurrentAdminSession(ctx, oldAccess)
		currentDone <- currentResult{session: session, err: currentErr}
	}()
	select {
	case <-resolver.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("current session did not reach the controlled race point")
	}

	refreshed, err := service.RefreshAdminSession(ctx, oldRefresh, identity.TransportCookie, "trace-csrf-race-refresh")
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.SessionVersion != 2 || refreshed.CookieTokens == nil || refreshed.CSRFToken == nil {
		t.Fatalf("unexpected refreshed session: %+v", refreshed)
	}
	close(resolver.release)
	var stale currentResult
	select {
	case stale = <-currentDone:
	case <-time.After(5 * time.Second):
		t.Fatal("stale current session did not complete")
	}
	if stale.err != nil || stale.session.SessionVersion != 1 {
		t.Fatalf("unexpected stale current result: session=%+v error=%v", stale.session, stale.err)
	}

	var persistedCSRF []byte
	if err := database.Pool.QueryRow(ctx, `SELECT csrf_digest FROM identity.admin_sessions WHERE session_id=$1`, stored.SessionID).Scan(&persistedCSRF); err != nil {
		t.Fatal(err)
	}
	if string(persistedCSRF) != string(hasher.Digest(*refreshed.CSRFToken)) {
		t.Fatal("stale GET session overwrote the CSRF digest committed by refresh")
	}
	if _, err := service.CurrentAdminSessionWithCSRF(ctx, refreshed.CookieTokens.AccessToken, *refreshed.CSRFToken); err != nil {
		t.Fatalf("refreshed access and CSRF were not usable after stale GET completed: %v", err)
	}
}

func TestRepositoryCookieLogoutRequiresCSRFAndRevokesFamily(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	bootstrapUser(t, repository, now)
	session := testSession(now)
	if err := repository.CreateAdminSession(ctx, session); err != nil {
		t.Fatal(err)
	}

	proof := identity.CookieLogoutProof{AccessDigest: session.AccessToken.Digest, RefreshDigest: session.RefreshToken.Digest, CSRFDigest: []byte("wrong-csrf")}
	if err := repository.RevokeCookieSession(ctx, proof, now.Add(time.Minute), securityEvent("logout-wrong-csrf", now)); !errors.Is(err, identity.ErrCSRFFailed) {
		t.Fatalf("wrong CSRF error = %v, want ErrCSRFFailed", err)
	}
	if _, err := repository.FindByAccessDigest(ctx, session.AccessToken.Digest, now.Add(time.Minute)); err != nil {
		t.Fatalf("wrong CSRF revoked session: %v", err)
	}

	proof.CSRFDigest = session.CSRFDigest
	if err := repository.RevokeCookieSession(ctx, proof, now.Add(2*time.Minute), securityEvent("logout-valid", now)); err != nil {
		t.Fatalf("valid cookie logout error = %v", err)
	}
	if _, err := repository.FindByAccessDigest(ctx, session.AccessToken.Digest, now.Add(2*time.Minute)); !errors.Is(err, identity.ErrSessionRevoked) {
		t.Fatalf("access after logout error = %v, want ErrSessionRevoked", err)
	}
	if _, err := repository.InspectRefresh(ctx, session.RefreshToken.Digest, identity.TransportCookie, nil, now.Add(2*time.Minute)); !errors.Is(err, identity.ErrSessionRevoked) {
		t.Fatalf("refresh after logout error = %v, want ErrSessionRevoked", err)
	}
}

func TestRepositoryCookieLogoutExpiredAccessRequiresSameFamilyRefresh(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	bootstrapUser(t, repository, now)
	first := testSession(now)
	if err := repository.CreateAdminSession(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := testSession(now.Add(time.Second))
	second.SessionID = "session-2"
	second.TokenFamilyID = "family-2"
	second.AccessToken.TokenID = "access-2"
	second.AccessToken.Digest = []byte("access-digest-2")
	second.RefreshToken.TokenID = "refresh-2"
	second.RefreshToken.Digest = []byte("refresh-digest-2")
	second.OutboxEvent = securityEvent("session-created-2", now)
	if err := repository.CreateAdminSession(ctx, second); err != nil {
		t.Fatal(err)
	}

	expiredAt := now.Add(16 * time.Minute)
	mismatched := identity.CookieLogoutProof{AccessDigest: first.AccessToken.Digest, RefreshDigest: second.RefreshToken.Digest}
	if err := repository.RevokeCookieSession(ctx, mismatched, expiredAt, securityEvent("logout-mismatched", now)); !errors.Is(err, identity.ErrSessionRevoked) {
		t.Fatalf("mismatched family error = %v, want ErrSessionRevoked", err)
	}
	assertRowCount(t, database, `SELECT count(*) FROM identity.admin_sessions WHERE session_id=$1 AND revoked_at IS NULL`, first.SessionID, 1)
	assertRowCount(t, database, `SELECT count(*) FROM identity.admin_sessions WHERE session_id=$1 AND revoked_at IS NULL`, second.SessionID, 1)

	sameFamily := identity.CookieLogoutProof{AccessDigest: first.AccessToken.Digest, RefreshDigest: first.RefreshToken.Digest}
	if err := repository.RevokeCookieSession(ctx, sameFamily, expiredAt, securityEvent("logout-expired-access", now)); err != nil {
		t.Fatalf("expired access fallback error = %v", err)
	}
	assertRowCount(t, database, `SELECT count(*) FROM identity.admin_sessions WHERE session_id=$1 AND revoked_at IS NOT NULL`, first.SessionID, 1)
}

func TestRepositoryCookieLogoutActiveAccessRejectsIncompleteOrMixedProofPair(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 9, 30, 0, 0, time.UTC)
	bootstrapUser(t, repository, now)
	first := testSession(now)
	if err := repository.CreateAdminSession(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := testSession(now.Add(time.Second))
	second.SessionID = "session-active-mixed-2"
	second.TokenFamilyID = "family-active-mixed-2"
	second.AccessToken.TokenID = "access-active-mixed-2"
	second.AccessToken.Digest = []byte("access-active-mixed-digest-2")
	second.RefreshToken.TokenID = "refresh-active-mixed-2"
	second.RefreshToken.Digest = []byte("refresh-active-mixed-digest-2")
	second.OutboxEvent = securityEvent("session-active-mixed-created-2", now)
	if err := repository.CreateAdminSession(ctx, second); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		refreshDigest []byte
	}{
		{name: "missing refresh", refreshDigest: []byte("unknown-refresh-digest")},
		{name: "different family", refreshDigest: second.RefreshToken.Digest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proof := identity.CookieLogoutProof{
				AccessDigest: first.AccessToken.Digest, RefreshDigest: test.refreshDigest, CSRFDigest: first.CSRFDigest,
			}
			if err := repository.RevokeCookieSession(ctx, proof, now.Add(time.Minute), securityEvent("logout-active-mixed-"+test.name, now)); !errors.Is(err, identity.ErrSessionRevoked) {
				t.Fatalf("mixed proof error = %v, want ErrSessionRevoked", err)
			}
			assertRowCount(t, database, `SELECT count(*) FROM identity.admin_sessions WHERE session_id=$1 AND revoked_at IS NULL`, first.SessionID, 1)
			assertRowCount(t, database, `SELECT count(*) FROM identity.admin_sessions WHERE session_id=$1 AND revoked_at IS NULL`, second.SessionID, 1)
		})
	}
}

func TestRepositoryCookieLogoutConsumedRefreshReplayWinsBeforeActiveAccess(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 9, 45, 0, 0, time.UTC)
	bootstrapUser(t, repository, now)
	session := testSession(now)
	if err := repository.CreateAdminSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	rotationAt := now.Add(time.Minute)
	rotation := identity.Rotation{
		AccessToken:  identity.TokenRecord{TokenID: "access-after-consume", TokenType: "access", Digest: []byte("access-after-consume-digest"), ExpiresAt: rotationAt.Add(15 * time.Minute)},
		RefreshToken: identity.TokenRecord{TokenID: "refresh-after-consume", TokenType: "refresh", Digest: []byte("refresh-after-consume-digest"), ExpiresAt: rotationAt.Add(time.Hour)},
		CSRFDigest:   []byte("csrf-after-consume"), AccessExpires: rotationAt.Add(15 * time.Minute), RefreshExpires: rotationAt.Add(time.Hour), Now: rotationAt,
		OutboxEvent: securityEvent("refresh-before-cookie-replay", rotationAt),
	}
	if _, err := repository.RotateRefresh(ctx, session.RefreshToken.Digest, identity.TransportCookie, nil, rotation); err != nil {
		t.Fatal(err)
	}
	proof := identity.CookieLogoutProof{
		AccessDigest: rotation.AccessToken.Digest, RefreshDigest: session.RefreshToken.Digest, CSRFDigest: rotation.CSRFDigest,
	}
	if err := repository.RevokeCookieSession(ctx, proof, rotationAt.Add(time.Minute), securityEvent("logout-consumed-refresh-replay", rotationAt)); !errors.Is(err, identity.ErrRefreshReplayed) {
		t.Fatalf("consumed refresh logout error = %v, want ErrRefreshReplayed", err)
	}
	if _, err := repository.FindByAccessDigest(ctx, rotation.AccessToken.Digest, rotationAt.Add(time.Minute)); !errors.Is(err, identity.ErrSessionRevoked) {
		t.Fatalf("current access after consumed refresh replay error = %v, want ErrSessionRevoked", err)
	}
}

func TestRepositoryRevokeByTokenEnforcesTransportAndTokenType(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 9, 55, 0, 0, time.UTC)
	bootstrapUser(t, repository, now)
	session := testSession(now)
	if err := repository.CreateAdminSession(ctx, session); err != nil {
		t.Fatal(err)
	}

	wrongExpectations := []struct {
		name        string
		digest      []byte
		expectation identity.TokenExpectation
	}{
		{name: "cookie access as bearer access", digest: session.AccessToken.Digest, expectation: identity.TokenExpectation{Transport: identity.TransportBearer, TokenType: "access"}},
		{name: "cookie refresh as bearer access", digest: session.RefreshToken.Digest, expectation: identity.TokenExpectation{Transport: identity.TransportBearer, TokenType: "access"}},
		{name: "cookie refresh as cookie access", digest: session.RefreshToken.Digest, expectation: identity.TokenExpectation{Transport: identity.TransportCookie, TokenType: "access"}},
	}
	for _, test := range wrongExpectations {
		t.Run(test.name, func(t *testing.T) {
			err := repository.RevokeByToken(ctx, test.digest, test.expectation, now.Add(time.Minute), identity.RevokeReasonLogout, securityEvent("revoke-wrong-"+test.name, now))
			if !errors.Is(err, identity.ErrSessionRevoked) {
				t.Fatalf("wrong expectation error = %v, want ErrSessionRevoked", err)
			}
			if _, err := repository.FindByAccessDigest(ctx, session.AccessToken.Digest, now.Add(time.Minute)); err != nil {
				t.Fatalf("wrong expectation revoked session: %v", err)
			}
		})
	}

	correct := identity.TokenExpectation{Transport: identity.TransportCookie, TokenType: "refresh"}
	if err := repository.RevokeByToken(ctx, session.RefreshToken.Digest, correct, now.Add(2*time.Minute), identity.RevokeReasonNoAdminScope, securityEvent("revoke-cookie-refresh", now)); err != nil {
		t.Fatalf("correct refresh expectation error = %v", err)
	}
	if _, err := repository.FindByAccessDigest(ctx, session.AccessToken.Digest, now.Add(2*time.Minute)); !errors.Is(err, identity.ErrSessionRevoked) {
		t.Fatalf("correct refresh revoke access error = %v, want ErrSessionRevoked", err)
	}
}

func TestRepositoryCookieLogoutOutboxFailureRollsBackRevocation(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)
	bootstrapUser(t, repository, now)
	session := testSession(now)
	if err := repository.CreateAdminSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	duplicate := securityEvent("duplicate-logout", now)
	if _, err := database.Pool.Exec(ctx, `INSERT INTO identity.outbox_events(event_id,topic,payload,next_attempt_at,created_at) VALUES($1,$2,'{}'::jsonb,$3,$3)`, duplicate.EventID, duplicate.Topic, now); err != nil {
		t.Fatal(err)
	}
	proof := identity.CookieLogoutProof{AccessDigest: session.AccessToken.Digest, CSRFDigest: session.CSRFDigest}
	if err := repository.RevokeCookieSession(ctx, proof, now.Add(time.Minute), duplicate); err == nil {
		t.Fatal("logout succeeded despite duplicate outbox event")
	}
	if _, err := repository.FindByAccessDigest(ctx, session.AccessToken.Digest, now.Add(time.Minute)); err != nil {
		t.Fatalf("outbox failure did not roll back revocation: %v", err)
	}
}

func TestRepositoryRefreshAndLogoutRaceEndsWithRevokedFamily(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 14, 11, 0, 0, 0, time.UTC)
	bootstrapUser(t, repository, now)
	session := testSession(now)
	if err := repository.CreateAdminSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	rotationAt := now.Add(time.Minute)
	rotation := identity.Rotation{
		AccessToken:  identity.TokenRecord{TokenID: "access-race-next", TokenType: "access", Digest: []byte("access-race-next-digest"), ExpiresAt: rotationAt.Add(15 * time.Minute)},
		RefreshToken: identity.TokenRecord{TokenID: "refresh-race-next", TokenType: "refresh", Digest: []byte("refresh-race-next-digest"), ExpiresAt: rotationAt.Add(time.Hour)},
		CSRFDigest:   []byte("csrf-race-next"), AccessExpires: rotationAt.Add(15 * time.Minute), RefreshExpires: rotationAt.Add(time.Hour), Now: rotationAt,
		OutboxEvent: securityEvent("refresh-race", rotationAt),
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := repository.RotateRefresh(ctx, session.RefreshToken.Digest, identity.TransportCookie, nil, rotation)
		results <- err
	}()
	go func() {
		<-start
		results <- repository.RevokeCookieSession(ctx, identity.CookieLogoutProof{RefreshDigest: session.RefreshToken.Digest}, rotationAt, securityEvent("logout-race", rotationAt))
	}()
	close(start)
	for index := 0; index < 2; index++ {
		select {
		case err := <-results:
			if err != nil && !errors.Is(err, identity.ErrSessionRevoked) && !errors.Is(err, identity.ErrRefreshReplayed) {
				t.Fatalf("race result error = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("refresh/logout race deadlocked")
		}
	}
	assertRowCount(t, database, `SELECT count(*) FROM identity.admin_sessions WHERE session_id=$1 AND revoked_at IS NOT NULL`, session.SessionID, 1)
	if _, err := repository.FindByAccessDigest(ctx, rotation.AccessToken.Digest, rotationAt.Add(time.Minute)); !errors.Is(err, identity.ErrSessionRevoked) && !errors.Is(err, identity.ErrSessionExpired) {
		t.Fatalf("derived access after race error = %v", err)
	}
}

func TestRepositoryConcurrentOutboxClaimsAreDisjoint(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)
	bootstrapUser(t, repository, now)
	for index := 0; index < 4; index++ {
		session := testSession(now.Add(time.Duration(index) * time.Second))
		session.SessionID = fmt.Sprintf("session-%d", index)
		session.TokenFamilyID = fmt.Sprintf("family-%d", index)
		session.AccessToken.TokenID = fmt.Sprintf("access-%d", index)
		session.AccessToken.Digest = []byte(fmt.Sprintf("access-digest-%d", index))
		session.RefreshToken.TokenID = fmt.Sprintf("refresh-%d", index)
		session.RefreshToken.Digest = []byte(fmt.Sprintf("refresh-digest-%d", index))
		session.OutboxEvent = securityEvent(fmt.Sprintf("outbox-%d", index), now)
		if err := repository.CreateAdminSession(ctx, session); err != nil {
			t.Fatalf("CreateAdminSession(%d) error = %v", index, err)
		}
	}

	start := make(chan struct{})
	claims := make(chan []identity.ClaimedOutboxEvent, 2)
	errorsChannel := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func() {
			<-start
			items, err := repository.ClaimOutbox(ctx, now, 2)
			claims <- items
			errorsChannel <- err
		}()
	}
	close(start)
	seen := make(map[string]struct{})
	for index := 0; index < 2; index++ {
		items := <-claims
		if err := <-errorsChannel; err != nil {
			t.Fatalf("ClaimOutbox() error = %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("ClaimOutbox() item count = %d, want 2", len(items))
		}
		for _, item := range items {
			if _, duplicate := seen[item.EventID]; duplicate {
				t.Fatalf("outbox event %q claimed twice", item.EventID)
			}
			seen[item.EventID] = struct{}{}
		}
	}
	if len(seen) != 4 {
		t.Fatalf("unique claimed events = %d, want 4", len(seen))
	}
}

func TestRepositoryConcurrentLoginFailuresAreNotLost(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	const attempts = 8

	start := make(chan struct{})
	states := make(chan identity.ThrottleState, attempts)
	errorsChannel := make(chan error, attempts)
	var group sync.WaitGroup
	for index := 0; index < attempts; index++ {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			state, err := repository.RecordLoginFailure(ctx, identity.LoginFailure{
				IdentifierDigest: []byte("same-identifier"),
				SourceDigest:     []byte("same-source"),
				Now:              now.Add(time.Duration(index) * time.Millisecond),
				Window:           10 * time.Minute,
				MaximumAttempts:  5,
				BlockDuration:    15 * time.Minute,
				OutboxEvent:      securityEvent(fmt.Sprintf("failure-%d", index), now),
			})
			states <- state
			errorsChannel <- err
		}()
	}
	close(start)
	group.Wait()
	close(states)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("RecordLoginFailure() error = %v", err)
		}
	}
	maximum := 0
	for state := range states {
		if state.FailureCount > maximum {
			maximum = state.FailureCount
		}
	}
	if maximum != attempts {
		t.Fatalf("maximum observed failure count = %d, want %d", maximum, attempts)
	}
	state, err := repository.LoginThrottle(ctx, []byte("same-identifier"), []byte("same-source"), now)
	if err != nil {
		t.Fatalf("LoginThrottle() error = %v", err)
	}
	if state.FailureCount != attempts || state.BlockedUntil == nil {
		t.Fatalf("stored throttle state = %#v", state)
	}
}

func TestControlledBearerClientBindingAndRevocation(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	bootstrapUser(t, repository, now)
	secretDigest := []byte("01234567890123456789012345678901")
	registration := identity.ControlledClientRegistration{
		ClientID: "acli_controlled1", DisplayName: "Controlled Automation", ClientType: "automation",
		CredentialID: "acred_credential1", ProofType: "shared_secret_v1", SecretDigest: secretDigest,
		CreatedAt: now, NotBefore: now, OutboxEvent: lifecycleEvent("client-register", "identity.admin_client_registered.v1", now),
	}
	if err := repository.RegisterControlledClient(ctx, registration); err != nil {
		t.Fatalf("RegisterControlledClient() error = %v", err)
	}
	resolved, err := repository.ResolveControlledClientCredential(ctx, registration.ClientID, registration.CredentialID, registration.ProofType, secretDigest, now)
	if err != nil {
		t.Fatalf("ResolveControlledClientCredential() error = %v", err)
	}
	if resolved.ClientID != registration.ClientID || resolved.CredentialID != registration.CredentialID {
		t.Fatalf("resolved binding = %+v", resolved)
	}
	if _, err := repository.ResolveControlledClientCredential(ctx, registration.ClientID, registration.CredentialID, registration.ProofType, []byte("wrong-secret-digest-wrong-secret"), now); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("wrong secret error = %v, want ErrNotFound", err)
	}

	session := testSession(now)
	session.Transport = identity.TransportBearer
	session.CSRFDigest = nil
	session.ControlledClientID = registration.ClientID
	session.ControlledCredentialID = registration.CredentialID
	if err := repository.CreateAdminSession(ctx, session); err != nil {
		t.Fatalf("CreateAdminSession() error = %v", err)
	}
	if _, err := repository.FindByAccessDigest(ctx, session.AccessToken.Digest, now.Add(time.Minute)); err != nil {
		t.Fatalf("FindByAccessDigest() before revoke error = %v", err)
	}

	rotationTime := now.Add(time.Minute)
	rotation := identity.Rotation{
		AccessToken:   identity.TokenRecord{TokenID: "access-bound-next", TokenType: "access", Digest: []byte("access-bound-next-digest"), ExpiresAt: rotationTime.Add(15 * time.Minute)},
		RefreshToken:  identity.TokenRecord{TokenID: "refresh-bound-next", TokenType: "refresh", Digest: []byte("refresh-bound-next-digest"), ExpiresAt: rotationTime.Add(time.Hour)},
		AccessExpires: rotationTime.Add(15 * time.Minute), RefreshExpires: rotationTime.Add(time.Hour), Now: rotationTime,
		OutboxEvent: securityEvent("controlled-refresh", rotationTime),
	}
	wrongBinding := &identity.ControlledClientBinding{ClientID: registration.ClientID, CredentialID: "acred_other000"}
	if _, err := repository.RotateRefresh(ctx, session.RefreshToken.Digest, identity.TransportBearer, wrongBinding, rotation); !errors.Is(err, identity.ErrSessionRevoked) {
		t.Fatalf("mismatched binding error = %v, want ErrSessionRevoked", err)
	}
	exactBinding := &identity.ControlledClientBinding{ClientID: registration.ClientID, CredentialID: registration.CredentialID}
	if _, err := repository.RotateRefresh(ctx, session.RefreshToken.Digest, identity.TransportBearer, exactBinding, rotation); err != nil {
		t.Fatalf("exact binding RotateRefresh() error = %v", err)
	}
	if err := repository.RevokeControlledClientCredential(ctx, registration.ClientID, registration.CredentialID, rotationTime.Add(time.Minute), lifecycleEvent("credential-revoke", "identity.admin_client_credential_revoked.v1", rotationTime.Add(time.Minute))); err != nil {
		t.Fatalf("RevokeControlledClientCredential() error = %v", err)
	}
	if _, err := repository.FindByAccessDigest(ctx, rotation.AccessToken.Digest, rotationTime.Add(2*time.Minute)); !errors.Is(err, identity.ErrSessionRevoked) {
		t.Fatalf("access after credential revoke error = %v, want ErrSessionRevoked", err)
	}
	if _, err := repository.RotateRefresh(ctx, rotation.RefreshToken.Digest, identity.TransportBearer, exactBinding, identity.Rotation{Now: rotationTime.Add(2 * time.Minute)}); !errors.Is(err, identity.ErrSessionRevoked) {
		t.Fatalf("refresh after credential revoke error = %v, want ErrSessionRevoked", err)
	}
}

func TestControlledBearerDatabaseConstraints(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	bootstrapUser(t, repository, now)
	for index, clientID := range []string{"acli_first0000", "acli_second000"} {
		if err := repository.RegisterControlledClient(ctx, identity.ControlledClientRegistration{
			ClientID: clientID, DisplayName: fmt.Sprintf("Client %d", index), ClientType: "automation",
			CredentialID: fmt.Sprintf("acred_credential%d", index), ProofType: "shared_secret_v1",
			SecretDigest: []byte(fmt.Sprintf("%032d", index+1)), CreatedAt: now, NotBefore: now,
			OutboxEvent: lifecycleEvent(fmt.Sprintf("constraint-register-%d", index), "identity.admin_client_registered.v1", now),
		}); err != nil {
			t.Fatalf("register client %d: %v", index, err)
		}
	}

	unbound := testSession(now)
	unbound.Transport = identity.TransportBearer
	unbound.CSRFDigest = nil
	if err := repository.CreateAdminSession(ctx, unbound); err == nil {
		t.Fatal("database accepted an unbound active bearer session")
	}

	crossBound := testSession(now)
	crossBound.SessionID = "session-cross-bound"
	crossBound.TokenFamilyID = "family-cross-bound"
	crossBound.AccessToken.TokenID = "access-cross-bound"
	crossBound.AccessToken.Digest = []byte("access-cross-bound-digest")
	crossBound.RefreshToken.TokenID = "refresh-cross-bound"
	crossBound.RefreshToken.Digest = []byte("refresh-cross-bound-digest")
	crossBound.Transport = identity.TransportBearer
	crossBound.CSRFDigest = nil
	crossBound.ControlledClientID = "acli_first0000"
	crossBound.ControlledCredentialID = "acred_credential1"
	if err := repository.CreateAdminSession(ctx, crossBound); err == nil {
		t.Fatal("database accepted a credential belonging to another controlled client")
	}

	cookieBound := testSession(now)
	cookieBound.SessionID = "session-cookie-bound"
	cookieBound.TokenFamilyID = "family-cookie-bound"
	cookieBound.AccessToken.TokenID = "access-cookie-bound"
	cookieBound.AccessToken.Digest = []byte("access-cookie-bound-digest")
	cookieBound.RefreshToken.TokenID = "refresh-cookie-bound"
	cookieBound.RefreshToken.Digest = []byte("refresh-cookie-bound-digest")
	cookieBound.ControlledClientID = "acli_first0000"
	cookieBound.ControlledCredentialID = "acred_credential0"
	if err := repository.CreateAdminSession(ctx, cookieBound); err == nil {
		t.Fatal("database accepted controlled-client fields on a cookie session")
	}
}

func TestDisablingControlledClientRevokesExistingBearerFamily(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	bootstrapUser(t, repository, now)
	registration := identity.ControlledClientRegistration{
		ClientID: "acli_disable00", DisplayName: "Disable Test", ClientType: "cli",
		CredentialID: "acred_disable00", ProofType: "shared_secret_v1",
		SecretDigest: []byte("abcdefghijklmnopqrstuvwxyz012345"), CreatedAt: now, NotBefore: now,
		OutboxEvent: lifecycleEvent("disable-register", "identity.admin_client_registered.v1", now),
	}
	if err := repository.RegisterControlledClient(ctx, registration); err != nil {
		t.Fatal(err)
	}
	session := testSession(now)
	session.Transport = identity.TransportBearer
	session.CSRFDigest = nil
	session.ControlledClientID = registration.ClientID
	session.ControlledCredentialID = registration.CredentialID
	if err := repository.CreateAdminSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := repository.DisableControlledClient(ctx, registration.ClientID, now.Add(time.Minute), lifecycleEvent("client-disable", "identity.admin_client_disabled.v1", now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FindByAccessDigest(ctx, session.AccessToken.Digest, now.Add(2*time.Minute)); !errors.Is(err, identity.ErrSessionRevoked) {
		t.Fatalf("access after client disable error = %v", err)
	}
	binding := &identity.ControlledClientBinding{ClientID: registration.ClientID, CredentialID: registration.CredentialID}
	if _, err := repository.RotateRefresh(ctx, session.RefreshToken.Digest, identity.TransportBearer, binding, identity.Rotation{Now: now.Add(2 * time.Minute)}); !errors.Is(err, identity.ErrSessionRevoked) {
		t.Fatalf("refresh after client disable error = %v", err)
	}
	if _, err := repository.ResolveControlledClientCredential(ctx, registration.ClientID, registration.CredentialID, registration.ProofType, registration.SecretDigest, now.Add(2*time.Minute)); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("disabled client resolution error = %v", err)
	}
}

func TestControlledClientLifecyclePersistsRedactedOutboxEvents(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	registration := identity.ControlledClientRegistration{
		ClientID: "acli_audited00", DisplayName: "Audited Client", ClientType: "automation",
		CredentialID: "acred_audited00", ProofType: "shared_secret_v1",
		SecretDigest: []byte("12345678901234567890123456789012"), CreatedAt: now, NotBefore: now,
		OutboxEvent: lifecycleEvent("audit-register", "identity.admin_client_registered.v1", now),
	}
	if err := repository.RegisterControlledClient(ctx, registration); err != nil {
		t.Fatal(err)
	}
	rotated := identity.ControlledClientCredentialRegistration{
		ClientID: registration.ClientID, CredentialID: "acred_audited01", ProofType: "shared_secret_v1",
		SecretDigest: []byte("22345678901234567890123456789012"), CreatedAt: now.Add(time.Second), NotBefore: now.Add(time.Second),
		OutboxEvent: lifecycleEvent("audit-rotate", "identity.admin_client_credential_rotated.v1", now.Add(time.Second)),
	}
	if err := repository.AddControlledClientCredential(ctx, rotated); err != nil {
		t.Fatal(err)
	}
	if err := repository.RevokeControlledClientCredential(ctx, rotated.ClientID, rotated.CredentialID, now.Add(2*time.Second), lifecycleEvent("audit-revoke", "identity.admin_client_credential_revoked.v1", now.Add(2*time.Second))); err != nil {
		t.Fatal(err)
	}
	if err := repository.DisableControlledClient(ctx, registration.ClientID, now.Add(3*time.Second), lifecycleEvent("audit-disable", "identity.admin_client_disabled.v1", now.Add(3*time.Second))); err != nil {
		t.Fatal(err)
	}

	for _, action := range []string{
		"identity.admin_client_registered.v1",
		"identity.admin_client_credential_rotated.v1",
		"identity.admin_client_credential_revoked.v1",
		"identity.admin_client_disabled.v1",
	} {
		var count int
		if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM identity.outbox_events WHERE payload->>'action'=$1 AND payload->>'actor_id'='offline_operator' AND COALESCE(payload->>'trace_id','')<>'' AND payload::text NOT ILIKE '%secret%' AND payload::text NOT ILIKE '%digest%'`, action).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("redacted outbox event %q count = %d", action, count)
		}
	}
}

func TestControlledClientLifecycleOutboxFailureRollsBackState(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	duplicate := lifecycleEvent("duplicate-lifecycle-event", "identity.test.v1", now)
	if _, err := database.Pool.Exec(ctx, `INSERT INTO identity.outbox_events(event_id,topic,payload,next_attempt_at,created_at) VALUES($1,$2,'{}'::jsonb,$3,$3)`, duplicate.EventID, duplicate.Topic, now); err != nil {
		t.Fatal(err)
	}

	failedRegistration := identity.ControlledClientRegistration{
		ClientID: "acli_rollback0", DisplayName: "Rollback", ClientType: "automation",
		CredentialID: "acred_rollback0", ProofType: "shared_secret_v1",
		SecretDigest: []byte("32345678901234567890123456789012"), CreatedAt: now, NotBefore: now, OutboxEvent: duplicate,
	}
	if err := repository.RegisterControlledClient(ctx, failedRegistration); err == nil {
		t.Fatal("registration succeeded despite outbox conflict")
	}
	assertRowCount(t, database, `SELECT count(*) FROM identity.admin_auth_clients WHERE client_id=$1`, failedRegistration.ClientID, 0)

	valid := failedRegistration
	valid.OutboxEvent = lifecycleEvent("rollback-valid-register", "identity.admin_client_registered.v1", now)
	if err := repository.RegisterControlledClient(ctx, valid); err != nil {
		t.Fatal(err)
	}
	failedRotation := identity.ControlledClientCredentialRegistration{
		ClientID: valid.ClientID, CredentialID: "acred_rollback1", ProofType: "shared_secret_v1",
		SecretDigest: []byte("42345678901234567890123456789012"), CreatedAt: now, NotBefore: now, OutboxEvent: duplicate,
	}
	if err := repository.AddControlledClientCredential(ctx, failedRotation); err == nil {
		t.Fatal("credential rotation succeeded despite outbox conflict")
	}
	assertRowCount(t, database, `SELECT count(*) FROM identity.admin_auth_client_credentials WHERE credential_id=$1`, failedRotation.CredentialID, 0)

	bootstrapUser(t, repository, now)
	session := testSession(now)
	session.Transport = identity.TransportBearer
	session.CSRFDigest = nil
	session.ControlledClientID = valid.ClientID
	session.ControlledCredentialID = valid.CredentialID
	if err := repository.CreateAdminSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := repository.RevokeControlledClientCredential(ctx, valid.ClientID, valid.CredentialID, now.Add(time.Minute), duplicate); err == nil {
		t.Fatal("credential revoke succeeded despite outbox conflict")
	}
	assertRowCount(t, database, `SELECT count(*) FROM identity.admin_auth_client_credentials WHERE credential_id=$1 AND revoked_at IS NULL`, valid.CredentialID, 1)
	if err := repository.DisableControlledClient(ctx, valid.ClientID, now.Add(time.Minute), duplicate); err == nil {
		t.Fatal("client disable succeeded despite outbox conflict")
	}
	assertRowCount(t, database, `SELECT count(*) FROM identity.admin_auth_clients WHERE client_id=$1 AND status='active'`, valid.ClientID, 1)
	assertRowCount(t, database, `SELECT count(*) FROM identity.admin_sessions WHERE session_id=$1 AND revoked_at IS NULL`, session.SessionID, 1)
}

func bootstrapUser(t *testing.T, repository *identitypostgres.Repository, now time.Time) {
	t.Helper()
	ctx := context.Background()
	user := identity.BootstrapUser{UserID: "user-1", CredentialID: "credential-1", IdentifierDigest: []byte("identifier-digest"), IdentifierMasked: "a***@example.com", DisplayName: "Admin", PasswordHash: []byte("password-hash"), Now: now}
	first, err := repository.BootstrapIdentity(ctx, user)
	if err != nil {
		t.Fatalf("BootstrapIdentity() error = %v", err)
	}
	user.UserID, user.CredentialID = "user-duplicate", "credential-duplicate"
	second, err := repository.BootstrapIdentity(ctx, user)
	if err != nil {
		t.Fatalf("repeat BootstrapIdentity() error = %v", err)
	}
	if first != "user-1" || second != first {
		t.Fatalf("bootstrap user IDs = %q and %q", first, second)
	}
}

func testSession(now time.Time) identity.NewSession {
	return identity.NewSession{
		StoredSession: identity.StoredSession{
			SessionID: "session-1", UserID: "user-1", TokenFamilyID: "family-1",
			Transport: identity.TransportCookie, AuthenticationMethod: "password", SessionVersion: 1,
			AuthTime: now, AccessExpiresAt: now.Add(15 * time.Minute), RefreshExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(time.Hour), CSRFDigest: []byte("csrf-digest"),
		},
		AccessToken:  identity.TokenRecord{TokenID: "access-1", TokenType: "access", Generation: 1, Digest: []byte("access-digest"), ExpiresAt: now.Add(15 * time.Minute)},
		RefreshToken: identity.TokenRecord{TokenID: "refresh-1", TokenType: "refresh", Generation: 1, Digest: []byte("refresh-digest"), ExpiresAt: now.Add(time.Hour)},
		RiskSummary:  map[string]any{"source": "integration-test"},
		OutboxEvent:  securityEvent("session-created", now),
		CreatedAt:    now,
	}
}

func securityEvent(eventID string, now time.Time) identity.OutboxEvent {
	return identity.OutboxEvent{
		EventID: eventID,
		Topic:   "audit.append",
		Now:     now,
		Payload: identity.SecurityEvent{AuditID: "audit-" + eventID, OccurredAt: now, ActorID: "user-1", ScopeType: "platform", Action: "integration.test", TargetType: "session", TargetID: "session-1", Result: "success", TraceID: "trace-" + eventID, RiskLevel: "normal"},
	}
}

func lifecycleEvent(eventID, action string, now time.Time) identity.OutboxEvent {
	return identity.OutboxEvent{
		EventID: eventID,
		Topic:   "audit.append",
		Now:     now,
		Payload: identity.SecurityEvent{
			AuditID: "audit-" + eventID, OccurredAt: now, ActorID: "offline_operator",
			ScopeType: "platform", Action: action, TargetType: "admin_auth_client",
			TargetID: "test-target", Result: "success", TraceID: "trace-" + eventID, RiskLevel: "high",
		},
	}
}

func assertRowCount(t *testing.T, database testpostgres.Database, query string, argument any, want int) {
	t.Helper()
	var count int
	if err := database.Pool.QueryRow(context.Background(), query, argument).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("row count = %d, want %d for %s", count, want, query)
	}
}
