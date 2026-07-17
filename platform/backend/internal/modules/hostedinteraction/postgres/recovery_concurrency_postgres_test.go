package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/hostedinteraction"
	hostedpostgres "platform.local/capability-platform/backend/internal/modules/hostedinteraction/postgres"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestAuthenticationLeaseCrashTakeoverAndReopen(t *testing.T) {
	database := testpostgres.Open(t)
	repository := hostedpostgres.New(database.Pool)
	ctx := context.Background()

	value := authInteraction(testID("hint_", 7001), 5*time.Minute)
	createUnique(t, ctx, repository, value, 1)
	browserDigest := digestOf("browser-lease")
	if _, _, err := repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: value.InteractionID, SessionID: testID("hbs_", 7001), TokenDigest: browserDigest, TTL: time.Minute, Event: event("evt_auth_lease_open", value.InteractionID, "hosted.interaction_opened.v1")}); err != nil {
		t.Fatal(err)
	}
	leaseOne, leaseTwo := digestOf("auth-lease-one"), digestOf("auth-lease-two")
	if _, _, err := repository.BeginAuthentication(ctx, value.InteractionID, browserDigest, leaseOne, 40*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.BeginAuthentication(ctx, value.InteractionID, browserDigest, leaseTwo, time.Minute); !errors.Is(err, hostedinteraction.ErrTemporarilyUnavailable) {
		t.Fatalf("active auth lease error=%v", err)
	}
	time.Sleep(65 * time.Millisecond)
	claimed, _, err := repository.BeginAuthentication(ctx, value.InteractionID, browserDigest, leaseTwo, time.Minute)
	if err != nil || claimed.Status != hostedinteraction.StatusAuthenticating {
		t.Fatalf("stale auth takeover=(%+v,%v)", claimed, err)
	}
	if err := repository.ResetAuthentication(ctx, value.InteractionID, browserDigest, leaseOne); !errors.Is(err, hostedinteraction.ErrLeaseLost) {
		t.Fatalf("old reset error=%v", err)
	}
	oldRecord := authCompleteRecord(value.InteractionID, browserDigest, leaseOne, 7001)
	if _, _, _, err := repository.Complete(ctx, oldRecord); !errors.Is(err, hostedinteraction.ErrLeaseLost) {
		t.Fatalf("old complete error=%v", err)
	}
	currentRecord := authCompleteRecord(value.InteractionID, browserDigest, leaseTwo, 7002)
	completed, _, _, err := repository.Complete(ctx, currentRecord)
	if err != nil || completed.Status != hostedinteraction.StatusCompleted {
		t.Fatalf("current complete=(%+v,%v)", completed, err)
	}

	reopenValue := authInteraction(testID("hint_", 7003), 5*time.Minute)
	reopenValue.StateDigest = digestOf("reopen-auth-state")
	createUnique(t, ctx, repository, reopenValue, 2)
	reopenOld := digestOf("reopen-old-browser")
	if _, _, err = repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: reopenValue.InteractionID, SessionID: testID("hbs_", 7003), TokenDigest: reopenOld, TTL: time.Minute, Event: event("evt_reopen_auth_open", reopenValue.InteractionID, "hosted.interaction_opened.v1")}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repository.BeginAuthentication(ctx, reopenValue.InteractionID, reopenOld, digestOf("reopen-auth-lease"), 40*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: reopenValue.InteractionID, SessionID: testID("hbs_", 7004), TokenDigest: digestOf("too-early-browser"), TTL: time.Minute, Event: event("evt_reopen_too_early", reopenValue.InteractionID, "hosted.interaction_opened.v1")}); !errors.Is(err, hostedinteraction.ErrTemporarilyUnavailable) {
		t.Fatalf("active authenticating reopen error=%v", err)
	}
	time.Sleep(65 * time.Millisecond)
	reopened, _, err := repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: reopenValue.InteractionID, SessionID: testID("hbs_", 7005), TokenDigest: digestOf("reopened-browser"), TTL: time.Minute, Event: event("evt_reopen_after_crash", reopenValue.InteractionID, "hosted.interaction_opened.v1")})
	if err != nil || reopened.Status != hostedinteraction.StatusOpened || len(reopened.AuthenticationLeaseDigest) != 0 {
		t.Fatalf("expired auth reopen=(%+v,%v)", reopened, err)
	}
}

func TestAuthenticationLeaseClearedOnCancelAndExpiry(t *testing.T) {
	database := testpostgres.Open(t)
	repository := hostedpostgres.New(database.Pool)
	ctx := context.Background()

	cancelledValue := authInteraction(testID("hint_", 7101), 5*time.Minute)
	cancelledValue.StateDigest = digestOf("cancel-auth-state")
	createUnique(t, ctx, repository, cancelledValue, 7101)
	cancelBrowser := digestOf("cancel-auth-browser")
	if _, _, err := repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: cancelledValue.InteractionID, SessionID: testID("hbs_", 7101), TokenDigest: cancelBrowser, TTL: time.Minute, Event: event("evt_cancel_auth_open", cancelledValue.InteractionID, "hosted.interaction_opened.v1")}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.BeginAuthentication(ctx, cancelledValue.InteractionID, cancelBrowser, digestOf("cancel-auth-lease"), time.Minute); err != nil {
		t.Fatal(err)
	}
	cancelled, err := repository.Cancel(ctx, cancelledValue.InteractionID, cancelBrowser, event("evt_cancel_auth", cancelledValue.InteractionID, "hosted.interaction_cancelled.v1"))
	if err != nil || cancelled.Status != hostedinteraction.StatusCancelled || len(cancelled.AuthenticationLeaseDigest) != 0 || cancelled.AuthenticationLeaseExpiresAt != nil {
		t.Fatalf("cancel authenticating=(%+v,%v)", cancelled, err)
	}

	expiredValue := authInteraction(testID("hint_", 7102), 80*time.Millisecond)
	expiredValue.StateDigest = digestOf("expire-auth-state")
	createUnique(t, ctx, repository, expiredValue, 7102)
	expireBrowser := digestOf("expire-auth-browser")
	if _, _, err = repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: expiredValue.InteractionID, SessionID: testID("hbs_", 7102), TokenDigest: expireBrowser, TTL: time.Minute, Event: event("evt_expire_auth_open", expiredValue.InteractionID, "hosted.interaction_opened.v1")}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repository.BeginAuthentication(ctx, expiredValue.InteractionID, expireBrowser, digestOf("expire-auth-lease"), time.Minute); err != nil {
		t.Fatal(err)
	}
	time.Sleep(110 * time.Millisecond)
	if count, expireErr := repository.ExpireDue(ctx, 100); expireErr != nil || count < 1 {
		t.Fatalf("expire due=(%d,%v)", count, expireErr)
	}
	expired, err := repository.Get(ctx, expiredValue.InteractionID)
	if err != nil || expired.Status != hostedinteraction.StatusExpired || len(expired.AuthenticationLeaseDigest) != 0 || expired.AuthenticationLeaseExpiresAt != nil {
		t.Fatalf("expire authenticating=(%+v,%v)", expired, err)
	}
}

func TestCompletionGrantExpiryEndsInteraction(t *testing.T) {
	database := testpostgres.Open(t)
	repository := hostedpostgres.New(database.Pool)
	ctx := context.Background()

	workerValue := accountInteraction(testID("hint_", 7201), 5*time.Minute)
	workerValue.StateDigest = digestOf("grant-expiry-worker-state")
	createUnique(t, ctx, repository, workerValue, 7201)
	workerBrowser := digestOf("grant-expiry-worker-browser")
	if _, _, err := repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: workerValue.InteractionID, SessionID: testID("hbs_", 7201), TokenDigest: workerBrowser, TTL: time.Minute, Event: event("evt_grant_expiry_worker_open", workerValue.InteractionID, "hosted.interaction_opened.v1")}); err != nil {
		t.Fatal(err)
	}
	workerComplete := accountCompleteRecord(workerValue.InteractionID, workerBrowser, 7201)
	workerComplete.GrantTTL = 50 * time.Millisecond
	if _, _, _, err := repository.Complete(ctx, workerComplete); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if count, err := repository.ExpireDue(ctx, 100); err != nil || count < 1 {
		t.Fatalf("expire completed grant=(%d,%v)", count, err)
	}
	workerExpired, err := repository.Get(ctx, workerValue.InteractionID)
	if err != nil || workerExpired.Status != hostedinteraction.StatusExpired {
		t.Fatalf("worker expired interaction=(%+v,%v)", workerExpired, err)
	}

	browserValue := accountInteraction(testID("hint_", 7202), 5*time.Minute)
	browserValue.StateDigest = digestOf("grant-expiry-browser-state")
	createUnique(t, ctx, repository, browserValue, 7202)
	browserDigest := digestOf("grant-expiry-browser")
	if _, _, err = repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: browserValue.InteractionID, SessionID: testID("hbs_", 7202), TokenDigest: browserDigest, TTL: time.Minute, Event: event("evt_grant_expiry_browser_open", browserValue.InteractionID, "hosted.interaction_opened.v1")}); err != nil {
		t.Fatal(err)
	}
	browserComplete := accountCompleteRecord(browserValue.InteractionID, browserDigest, 7202)
	browserComplete.GrantTTL = 50 * time.Millisecond
	if _, _, _, err = repository.Complete(ctx, browserComplete); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if _, _, err = repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: browserValue.InteractionID, SessionID: testID("hbs_", 7203), TokenDigest: digestOf("grant-expiry-new-browser"), TTL: time.Minute, Event: event("evt_grant_expiry_reopen", browserValue.InteractionID, "hosted.interaction_opened.v1")}); !errors.Is(err, hostedinteraction.ErrInteractionExpired) {
		t.Fatalf("expired grant reopen error=%v", err)
	}
	browserExpired, err := repository.Get(ctx, browserValue.InteractionID)
	if err != nil || browserExpired.Status != hostedinteraction.StatusExpired {
		t.Fatalf("browser expired interaction=(%+v,%v)", browserExpired, err)
	}
	if _, _, err = repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: browserValue.InteractionID, SessionID: testID("hbs_", 7204), TokenDigest: digestOf("grant-expiry-second-browser"), TTL: time.Minute, Event: event("evt_grant_expiry_second_reopen", browserValue.InteractionID, "hosted.interaction_opened.v1")}); !errors.Is(err, hostedinteraction.ErrInteractionExpired) {
		t.Fatalf("already expired second reopen error=%v", err)
	}
}

func TestConcurrentRotateCompleteNoDeadlockTwentyRounds(t *testing.T) {
	database := testpostgres.Open(t)
	repository := hostedpostgres.New(database.Pool)
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		value := accountInteraction(testID("hint_", 8000+i), 5*time.Minute)
		value.StateDigest = digestOf(fmt.Sprintf("rotate-state-%d", i))
		createUnique(t, ctx, repository, value, 20+i)
		oldDigest := digestOf(fmt.Sprintf("rotate-old-%d", i))
		if _, _, err := repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: value.InteractionID, SessionID: testID("hbs_", 8000+i), TokenDigest: oldDigest, TTL: time.Minute, Event: event(fmt.Sprintf("evt_rotate_open_%d", i), value.InteractionID, "hosted.interaction_opened.v1")}); err != nil {
			cancel()
			t.Fatal(err)
		}
		start := make(chan struct{})
		openResult, completeResult := make(chan error, 1), make(chan error, 1)
		go func() {
			<-start
			_, _, err := repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: value.InteractionID, SessionID: testID("hbs_", 9000+i), TokenDigest: digestOf(fmt.Sprintf("rotate-new-%d", i)), TTL: time.Minute, Event: event(fmt.Sprintf("evt_rotate_again_%d", i), value.InteractionID, "hosted.interaction_opened.v1")})
			openResult <- err
		}()
		go func() {
			<-start
			_, _, _, err := repository.Complete(ctx, accountCompleteRecord(value.InteractionID, oldDigest, 8000+i))
			completeResult <- err
		}()
		close(start)
		openErr, completeErr := <-openResult, <-completeResult
		cancel()
		if openErr != nil {
			t.Fatalf("round %d rotate error=%v", i, openErr)
		}
		if completeErr != nil && !errors.Is(completeErr, hostedinteraction.ErrSessionRevoked) {
			t.Fatalf("round %d complete error=%v", i, completeErr)
		}
		var interactionStatus, eventStatus string
		if err := database.Pool.QueryRow(context.Background(), `SELECT i.status,e.payload->>'status' FROM hosted_interaction.interactions i JOIN hosted_interaction.outbox_events e ON e.interaction_id=i.interaction_id WHERE i.interaction_id=$1 AND e.event_id=$2`, value.InteractionID, fmt.Sprintf("evt_rotate_again_%d", i)).Scan(&interactionStatus, &eventStatus); err != nil {
			t.Fatalf("round %d read reopen event: %v", i, err)
		}
		if eventStatus != interactionStatus {
			t.Fatalf("round %d reopen event status=%q interaction status=%q", i, eventStatus, interactionStatus)
		}
	}
}

func TestConcurrentClaimConsumeAndExpireNoDeadlockTwentyRounds(t *testing.T) {
	database := testpostgres.Open(t)
	repository := hostedpostgres.New(database.Pool)
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		value := accountInteraction(testID("hint_", 10000+i), 5*time.Minute)
		value.StateDigest = digestOf(fmt.Sprintf("claim-state-%d", i))
		createUnique(t, ctx, repository, value, 50+i)
		browserDigest := digestOf(fmt.Sprintf("claim-browser-%d", i))
		if _, _, err := repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: value.InteractionID, SessionID: testID("hbs_", 10000+i), TokenDigest: browserDigest, TTL: time.Minute, Event: event(fmt.Sprintf("evt_claim_open_%d", i), value.InteractionID, "hosted.interaction_opened.v1")}); err != nil {
			cancel()
			t.Fatal(err)
		}
		complete := accountCompleteRecord(value.InteractionID, browserDigest, 10000+i)
		_, grant, _, err := repository.Complete(ctx, complete)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		leaseDigest := digestOf(fmt.Sprintf("claim-lease-%d", i))
		if _, err = repository.ClaimGrant(ctx, value.InteractionID, value.Scope, complete.CodeDigest, nil, time.Minute, "lease", leaseDigest); err != nil {
			cancel()
			t.Fatal(err)
		}
		start := make(chan struct{})
		claimResult, consumeResult := make(chan error, 1), make(chan error, 1)
		go func() {
			<-start
			_, err := repository.ClaimGrant(ctx, value.InteractionID, value.Scope, complete.CodeDigest, nil, time.Minute, "lease-2", digestOf(fmt.Sprintf("claim-second-%d", i)))
			claimResult <- err
		}()
		go func() {
			<-start
			_, err := repository.ConsumeGrant(ctx, grant.GrantID, leaseDigest, event(fmt.Sprintf("evt_claim_consume_%d", i), value.InteractionID, "hosted.interaction_exchanged.v1"))
			consumeResult <- err
		}()
		close(start)
		claimErr, consumeErr := <-claimResult, <-consumeResult
		cancel()
		if consumeErr != nil {
			t.Fatalf("round %d consume error=%v", i, consumeErr)
		}
		if claimErr != nil && !errors.Is(claimErr, hostedinteraction.ErrTemporarilyUnavailable) && !errors.Is(claimErr, hostedinteraction.ErrInvalidGrant) {
			t.Fatalf("round %d claim error=%v", i, claimErr)
		}
	}

	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		value := accountInteraction(testID("hint_", 12000+i), 90*time.Millisecond)
		value.StateDigest = digestOf(fmt.Sprintf("expire-state-%d", i))
		createUnique(t, ctx, repository, value, 80+i)
		browserDigest := digestOf(fmt.Sprintf("expire-browser-%d", i))
		if _, _, err := repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: value.InteractionID, SessionID: testID("hbs_", 12000+i), TokenDigest: browserDigest, TTL: time.Minute, Event: event(fmt.Sprintf("evt_expire_open_%d", i), value.InteractionID, "hosted.interaction_opened.v1")}); err != nil {
			cancel()
			t.Fatal(err)
		}
		complete := accountCompleteRecord(value.InteractionID, browserDigest, 12000+i)
		complete.GrantTTL = 80 * time.Millisecond
		_, grant, _, err := repository.Complete(ctx, complete)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		leaseDigest := digestOf(fmt.Sprintf("expire-lease-%d", i))
		if _, err = repository.ClaimGrant(ctx, value.InteractionID, value.Scope, complete.CodeDigest, nil, 70*time.Millisecond, "lease", leaseDigest); err != nil {
			cancel()
			t.Fatal(err)
		}
		time.Sleep(110 * time.Millisecond)
		start := make(chan struct{})
		consumeResult, expireResult := make(chan error, 1), make(chan error, 1)
		go func() {
			<-start
			_, err := repository.ConsumeGrant(ctx, grant.GrantID, leaseDigest, event(fmt.Sprintf("evt_expire_consume_%d", i), value.InteractionID, "hosted.interaction_exchanged.v1"))
			consumeResult <- err
		}()
		go func() { <-start; _, err := repository.ExpireDue(ctx, 100); expireResult <- err }()
		close(start)
		consumeErr, expireErr := <-consumeResult, <-expireResult
		cancel()
		if expireErr != nil {
			t.Fatalf("round %d expire error=%v", i, expireErr)
		}
		if consumeErr != nil && !errors.Is(consumeErr, hostedinteraction.ErrLeaseLost) {
			t.Fatalf("round %d expired consume error=%v", i, consumeErr)
		}
	}
}

func createUnique(t *testing.T, ctx context.Context, repository *hostedpostgres.Repository, value hostedinteraction.Interaction, marker int) {
	t.Helper()
	if value.Route == hostedinteraction.RouteAuth {
		value.NonceDigest = digestOf(fmt.Sprintf("nonce-%d", marker))
		value.PKCEChallengeDigest = digestOf(fmt.Sprintf("pkce-%d", marker))
	}
	record := createRecord(value, fmt.Sprintf("evt_create_unique_%d", marker))
	record.ActorDigest = digestOf(fmt.Sprintf("actor-%d", marker))
	record.KeyDigest = digestOf(fmt.Sprintf("key-%d", marker))
	record.RequestDigest = digestOf(fmt.Sprintf("request-%d", marker))
	if _, _, err := repository.Create(ctx, record); err != nil {
		t.Fatalf("create unique %d: %v", marker, err)
	}
}

func authCompleteRecord(interactionID string, browserDigest, authLeaseDigest []byte, marker int) hostedinteraction.CompleteRecord {
	suffix := fmt.Sprintf("%024d", marker)
	return hostedinteraction.CompleteRecord{InteractionID: interactionID, BrowserTokenDigest: browserDigest, AuthenticationLeaseDigest: authLeaseDigest, ExpectedStatus: []hostedinteraction.Status{hostedinteraction.StatusAuthenticating}, GrantID: "hgrant_" + suffix, GrantType: "authorization_code", CodeDigest: digestOf("auth-code-" + suffix), IdentityProofID: "hproof_" + suffix, ResultDocument: []byte(`{}`), GrantTTL: time.Minute, Event: event("evt_auth_complete_"+suffix, interactionID, "hosted.interaction_completed.v1")}
}

func accountCompleteRecord(interactionID string, browserDigest []byte, marker int) hostedinteraction.CompleteRecord {
	suffix := fmt.Sprintf("%024d", marker)
	return hostedinteraction.CompleteRecord{InteractionID: interactionID, BrowserTokenDigest: browserDigest, ExpectedStatus: []hostedinteraction.Status{hostedinteraction.StatusOpened}, GrantID: "hgrant_" + suffix, GrantType: "account_completed", CodeDigest: digestOf("account-code-" + suffix), ResultDocument: []byte(`{"result":"closed"}`), GrantTTL: time.Minute, Event: event("evt_account_complete_"+suffix, interactionID, "hosted.interaction_completed.v1")}
}

func testID(prefix string, marker int) string { return prefix + fmt.Sprintf("%024d", marker) }
func digestOf(value string) []byte            { sum := sha256.Sum256([]byte(value)); return sum[:] }
