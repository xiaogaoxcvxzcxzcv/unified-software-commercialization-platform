package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/hostedinteraction"
	hostedpostgres "platform.local/capability-platform/backend/internal/modules/hostedinteraction/postgres"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestRepositoryRotationGrantLeaseAndExpiry(t *testing.T) {
	database := testpostgres.Open(t)
	repository := hostedpostgres.New(database.Pool)
	ctx := context.Background()

	interaction := authInteraction("hint_123456789012345678901234", 10*time.Minute)
	created, recovered, err := repository.Create(ctx, createRecord(interaction, "evt_created_auth"))
	if err != nil || recovered || created.Status != hostedinteraction.StatusCreated {
		t.Fatalf("create = (%+v,%v,%v)", created, recovered, err)
	}
	newClient := interaction.Actor
	newClient.ClientSessionID = "client_session_2"
	if recoveredByNewClient, recoveryErr := repository.GetForScope(ctx, interaction.InteractionID, interaction.Scope, newClient); recoveryErr != nil || recoveredByNewClient.InteractionID != interaction.InteractionID {
		t.Fatalf("same-scope new client recovery = (%+v,%v)", recoveredByNewClient, recoveryErr)
	}
	wrongScope := interaction.Scope
	wrongScope.ApplicationID = "application_other"
	if _, err = repository.GetForScope(ctx, interaction.InteractionID, wrongScope, newClient); !errors.Is(err, hostedinteraction.ErrInvalidArgument) {
		t.Fatalf("cross-scope new client read error = %v", err)
	}
	recoveredValue, recovered, err := repository.Create(ctx, createRecord(interaction, "evt_created_duplicate"))
	if err != nil || !recovered || recoveredValue.InteractionID != interaction.InteractionID {
		t.Fatalf("idempotent create = (%+v,%v,%v)", recoveredValue, recovered, err)
	}

	oldDigest := bytes32(21)
	opened, _, err := repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: interaction.InteractionID, SessionID: "hbs_123456789012345678901234", TokenDigest: oldDigest, TTL: time.Minute, Event: event("evt_open_1", interaction.InteractionID, "hosted.interaction_opened.v1")})
	if err != nil || opened.Status != hostedinteraction.StatusOpened {
		t.Fatalf("open first = (%+v,%v)", opened, err)
	}
	newDigest := bytes32(22)
	_, _, err = repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: interaction.InteractionID, SessionID: "hbs_223456789012345678901234", TokenDigest: newDigest, TTL: time.Minute, Event: event("evt_open_2", interaction.InteractionID, "hosted.interaction_opened.v1")})
	if err != nil {
		t.Fatalf("open rotated: %v", err)
	}
	if _, err = repository.ValidateBrowserSession(ctx, interaction.InteractionID, oldDigest); !errors.Is(err, hostedinteraction.ErrSessionRevoked) {
		t.Fatalf("old browser token error = %v", err)
	}
	access, err := repository.ValidateBrowserSession(ctx, interaction.InteractionID, newDigest)
	if err != nil || access.BrowserSessionID != "hbs_223456789012345678901234" || access.Interaction.InteractionID != interaction.InteractionID {
		t.Fatalf("new browser token: %v", err)
	}

	grantCodeDigest := bytes32(31)
	authLeaseDigest := bytes32(23)
	if _, _, err = repository.BeginAuthentication(ctx, interaction.InteractionID, newDigest, authLeaseDigest, time.Minute); err != nil {
		t.Fatalf("begin authentication: %v", err)
	}
	if _, _, _, err = repository.Complete(ctx, hostedinteraction.CompleteRecord{InteractionID: interaction.InteractionID, BrowserTokenDigest: oldDigest, AuthenticationLeaseDigest: authLeaseDigest, ExpectedStatus: []hostedinteraction.Status{hostedinteraction.StatusAuthenticating}, GrantID: "hgrant_023456789012345678901234", GrantType: "authorization_code", CodeDigest: grantCodeDigest, IdentityProofID: "hproof_023456789012345678901234", ResultDocument: []byte(`{}`), GrantTTL: time.Minute, Event: event("evt_old_cookie_complete", interaction.InteractionID, "hosted.interaction_completed.v1")}); !errors.Is(err, hostedinteraction.ErrSessionRevoked) {
		t.Fatalf("old cookie complete error = %v", err)
	}
	completed, grant, recovered, err := repository.Complete(ctx, hostedinteraction.CompleteRecord{InteractionID: interaction.InteractionID, BrowserTokenDigest: newDigest, AuthenticationLeaseDigest: authLeaseDigest, ExpectedStatus: []hostedinteraction.Status{hostedinteraction.StatusAuthenticating}, GrantID: "hgrant_123456789012345678901234", GrantType: "authorization_code", CodeDigest: grantCodeDigest, IdentityProofID: "hproof_123456789012345678901234", ResultDocument: []byte(`{}`), GrantTTL: time.Minute, Event: event("evt_complete_auth", interaction.InteractionID, "hosted.interaction_completed.v1")})
	if err != nil || recovered || completed.Status != hostedinteraction.StatusCompleted || grant.GrantID == "" || grant.ExpiresAt.IsZero() || !grant.ExpiresAt.Before(completed.ExpiresAt) {
		t.Fatalf("complete = (%+v,%+v,%v,%v)", completed, grant, recovered, err)
	}
	reopenDigest := bytes32(24)
	reopened, _, err := repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: interaction.InteractionID, SessionID: "hbs_423456789012345678901234", TokenDigest: reopenDigest, TTL: time.Minute, Event: event("evt_reopen_completed", interaction.InteractionID, "hosted.interaction_opened.v1")})
	if err != nil || reopened.Status != hostedinteraction.StatusCompleted {
		t.Fatalf("reopen completed = (%+v,%v)", reopened, err)
	}
	recoveredGrant, err := repository.GetCompletionGrant(ctx, interaction.InteractionID, reopenDigest)
	if err != nil || recoveredGrant.GrantID != grant.GrantID || !recoveredGrant.ExpiresAt.Equal(grant.ExpiresAt) {
		t.Fatalf("recover completed grant = (%+v,%v)", recoveredGrant, err)
	}
	leaseOne := bytes32(41)
	claimOne, err := repository.ClaimGrant(ctx, interaction.InteractionID, interaction.Scope, grantCodeDigest, interaction.PKCEChallengeDigest, time.Minute, "lease-one", leaseOne)
	if err != nil {
		t.Fatalf("claim one: %v", err)
	}
	if !claimOne.ExpiresAt.Equal(grant.ExpiresAt) {
		t.Fatalf("claim grant expiry=%v want %v", claimOne.ExpiresAt, grant.ExpiresAt)
	}
	if _, err = repository.ClaimGrant(ctx, interaction.InteractionID, interaction.Scope, grantCodeDigest, interaction.PKCEChallengeDigest, time.Minute, "lease-live", bytes32(42)); !errors.Is(err, hostedinteraction.ErrTemporarilyUnavailable) {
		t.Fatalf("live lease claim error = %v", err)
	}
	if _, err = database.Pool.Exec(ctx, `UPDATE hosted_interaction.completion_grants SET processing_expires_at=clock_timestamp()-interval '1 second' WHERE grant_id=$1`, claimOne.GrantID); err != nil {
		t.Fatalf("age lease: %v", err)
	}
	leaseTwo := bytes32(43)
	claimTwo, err := repository.ClaimGrant(ctx, interaction.InteractionID, interaction.Scope, grantCodeDigest, interaction.PKCEChallengeDigest, time.Minute, "lease-two", leaseTwo)
	if err != nil {
		t.Fatalf("claim stale lease: %v", err)
	}
	if _, err = repository.ConsumeGrant(ctx, claimOne.GrantID, leaseOne, event("evt_old_worker", interaction.InteractionID, "hosted.interaction_exchanged.v1")); !errors.Is(err, hostedinteraction.ErrLeaseLost) {
		t.Fatalf("old worker consume error = %v", err)
	}
	exchanged, err := repository.ConsumeGrant(ctx, claimTwo.GrantID, leaseTwo, event("evt_exchange_auth", interaction.InteractionID, "hosted.interaction_exchanged.v1"))
	if err != nil || exchanged.Status != hostedinteraction.StatusExchanged {
		t.Fatalf("new worker consume = (%+v,%v)", exchanged, err)
	}

	account := accountInteraction("hint_323456789012345678901234", 10*time.Minute)
	account.StateDigest = bytes32(5)
	accountRecord := createRecord(account, "evt_created_account")
	accountRecord.KeyDigest = bytes32(14)
	if _, _, err = repository.Create(ctx, accountRecord); err != nil {
		t.Fatalf("create account: %v", err)
	}
	accountBrowserDigest := bytes32(51)
	if _, _, err = repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: account.InteractionID, SessionID: "hbs_323456789012345678901234", TokenDigest: accountBrowserDigest, TTL: time.Minute, Event: event("evt_open_account", account.InteractionID, "hosted.interaction_opened.v1")}); err != nil {
		t.Fatalf("open account: %v", err)
	}
	accountComplete := hostedinteraction.CompleteRecord{InteractionID: account.InteractionID, BrowserTokenDigest: accountBrowserDigest, ExpectedStatus: []hostedinteraction.Status{hostedinteraction.StatusOpened}, GrantID: "hgrant_323456789012345678901234", GrantType: "account_completed", CodeDigest: bytes32(52), ResultDocument: []byte(`{"result":"closed"}`), GrantTTL: time.Minute, Operation: "account_complete", ActorDigest: bytes32(53), KeyDigest: bytes32(54), RequestDigest: bytes32(55), Event: event("evt_complete_account", account.InteractionID, "hosted.interaction_completed.v1")}
	_, firstGrant, wasRecovered, err := repository.Complete(ctx, accountComplete)
	if err != nil || wasRecovered {
		t.Fatalf("complete account = (%+v,%v,%v)", firstGrant, wasRecovered, err)
	}
	accountComplete.GrantID = "hgrant_423456789012345678901234"
	accountComplete.CodeDigest = bytes32(56)
	accountComplete.Event = event("evt_complete_account_retry", account.InteractionID, "hosted.interaction_completed.v1")
	_, recoveredGrant, wasRecovered, err = repository.Complete(ctx, accountComplete)
	if err != nil || !wasRecovered || recoveredGrant.GrantID != firstGrant.GrantID || !recoveredGrant.ExpiresAt.Equal(firstGrant.ExpiresAt) {
		t.Fatalf("recover account = (%+v,%v,%v)", recoveredGrant, wasRecovered, err)
	}
	var grantCount int
	if err = database.Pool.QueryRow(ctx, `SELECT count(*) FROM hosted_interaction.completion_grants WHERE interaction_id=$1`, account.InteractionID).Scan(&grantCount); err != nil || grantCount != 1 {
		t.Fatalf("account grant count = (%d,%v)", grantCount, err)
	}
	accountLease := bytes32(57)
	accountClaim, err := repository.ClaimGrant(ctx, account.InteractionID, account.Scope, bytes32(52), nil, time.Minute, "account-lease", accountLease)
	if err != nil || accountClaim.IdentityProofID != "" || accountClaim.ResultDocument["result"] != "closed" {
		t.Fatalf("claim account = (%+v,%v)", accountClaim, err)
	}
	accountExchanged, err := repository.ConsumeGrant(ctx, accountClaim.GrantID, accountLease, event("evt_exchange_account", account.InteractionID, "hosted.interaction_exchanged.v1"))
	if err != nil || accountExchanged.Status != hostedinteraction.StatusExchanged {
		t.Fatalf("exchange account = (%+v,%v)", accountExchanged, err)
	}

	expiring := accountInteraction("hint_223456789012345678901234", 15*time.Millisecond)
	expiringRecord := createRecord(expiring, "evt_created_expiring")
	expiringRecord.KeyDigest = bytes32(13)
	if _, _, err = repository.Create(ctx, expiringRecord); err != nil {
		t.Fatalf("create expiring: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	count, err := repository.ExpireDue(ctx, 10)
	if err != nil || count != 1 {
		t.Fatalf("expire due = (%d,%v)", count, err)
	}
	expired, err := repository.Get(ctx, expiring.InteractionID)
	if err != nil || expired.Status != hostedinteraction.StatusExpired || expired.FailureCode != "hosted.interaction_expired" {
		t.Fatalf("expired = (%+v,%v)", expired, err)
	}

	rows, err := database.Pool.Query(ctx, `SELECT payload::text FROM hosted_interaction.outbox_events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if err = rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(payload, "credential") || strings.Contains(payload, "token") || strings.Contains(payload, "verifier") || strings.Contains(payload, "state-value") {
			t.Fatalf("sensitive outbox payload: %s", payload)
		}
		var object map[string]any
		if json.Unmarshal([]byte(payload), &object) != nil {
			t.Fatalf("invalid outbox json: %s", payload)
		}
	}
}

func TestSelfServiceFlowPersistenceVersionResetAndExpiryPostgres(t *testing.T) {
	database := testpostgres.Open(t)
	repository := hostedpostgres.New(database.Pool)
	ctx := context.Background()
	interaction := authInteraction("hint_flow123456789012345678901234", 10*time.Minute)
	if _, _, err := repository.Create(ctx, createRecord(interaction, "evt_flow_create")); err != nil {
		t.Fatal(err)
	}
	digest := bytes32(71)
	if _, _, err := repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: interaction.InteractionID, SessionID: "hbs_flow123456789012345678901234", TokenDigest: digest, TTL: time.Minute, Event: event("evt_flow_open", interaction.InteractionID, "hosted.interaction_opened.v1")}); err != nil {
		t.Fatal(err)
	}
	record := hostedinteraction.PutSelfServiceFlowRecord{InteractionID: interaction.InteractionID, Kind: "registration_verification", IdentifierHint: "u***@example.com", Protected: hostedinteraction.ProtectedState{KeyRef: "flow.key", Ciphertext: append(bytes32(1), bytes32(2)...), Digest: bytes32(3)}, TTL: time.Minute}
	first, err := repository.PutSelfServiceFlow(ctx, record)
	if err != nil || first.Version != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	record.Kind = "recovery_verification"
	second, err := repository.PutSelfServiceFlow(ctx, record)
	if err != nil || second.Version != 2 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	loaded, found, err := repository.GetSelfServiceFlow(ctx, interaction.InteractionID)
	if err != nil || !found || loaded.Kind != record.Kind || string(loaded.Protected.Ciphertext) == "user@example.com" {
		t.Fatalf("loaded=%+v found=%v err=%v", loaded, found, err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			next := record
			if index%2 == 0 {
				next.Kind = "registration_verification"
			}
			_, putErr := repository.PutSelfServiceFlow(ctx, next)
			errs <- putErr
		}(i)
	}
	wg.Wait()
	close(errs)
	for putErr := range errs {
		if putErr != nil {
			t.Fatal(putErr)
		}
	}
	loaded, found, err = repository.GetSelfServiceFlow(ctx, interaction.InteractionID)
	if err != nil || !found || loaded.Version != 10 {
		t.Fatalf("concurrent flow=%+v found=%v err=%v", loaded, found, err)
	}
	reset := hostedinteraction.ResetSelfServiceFlowRecord{InteractionID: interaction.InteractionID, ActorDigest: bytes32(72), KeyDigest: bytes32(73), RequestDigest: bytes32(74)}
	if err = repository.ResetSelfServiceFlowIdempotent(ctx, reset); err != nil {
		t.Fatal(err)
	}
	if _, found, err = repository.GetSelfServiceFlow(ctx, interaction.InteractionID); err != nil || found {
		t.Fatalf("flow A after reset found=%v err=%v", found, err)
	}
	if _, err = repository.PutSelfServiceFlow(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err = repository.ResetSelfServiceFlowIdempotent(ctx, reset); err != nil {
		t.Fatalf("old reset replay error=%v", err)
	}
	if _, found, err = repository.GetSelfServiceFlow(ctx, interaction.InteractionID); err != nil || !found {
		t.Fatalf("old reset replay deleted flow B: found=%v err=%v", found, err)
	}
	changed := reset
	changed.RequestDigest = bytes32(75)
	if err = repository.ResetSelfServiceFlowIdempotent(ctx, changed); !errors.Is(err, hostedinteraction.ErrIdempotencyConflict) {
		t.Fatalf("same key different request context error=%v", err)
	}
	reset.KeyDigest = bytes32(76)
	var resetGroup sync.WaitGroup
	resetErrors := make(chan error, 8)
	for index := 0; index < 8; index++ {
		resetGroup.Add(1)
		go func() {
			defer resetGroup.Done()
			resetErrors <- repository.ResetSelfServiceFlowIdempotent(ctx, reset)
		}()
	}
	resetGroup.Wait()
	close(resetErrors)
	for resetErr := range resetErrors {
		if resetErr != nil {
			t.Fatalf("concurrent reset error=%v", resetErr)
		}
	}
	if _, found, err = repository.GetSelfServiceFlow(ctx, interaction.InteractionID); err != nil || found {
		t.Fatalf("flow B after new reset found=%v err=%v", found, err)
	}
	if err = repository.DeleteSelfServiceFlow(ctx, interaction.InteractionID); err != nil {
		t.Fatal(err)
	}
	if err = repository.DeleteSelfServiceFlow(ctx, interaction.InteractionID); err != nil {
		t.Fatal(err)
	}
	if _, found, err = repository.GetSelfServiceFlow(ctx, interaction.InteractionID); err != nil || found {
		t.Fatalf("after reset found=%v err=%v", found, err)
	}
	record.TTL = 40 * time.Millisecond
	if _, err = repository.PutSelfServiceFlow(ctx, record); err != nil {
		t.Fatal(err)
	}
	time.Sleep(70 * time.Millisecond)
	if _, found, err = repository.GetSelfServiceFlow(ctx, interaction.InteractionID); err != nil || found {
		t.Fatalf("expired flow found=%v err=%v", found, err)
	}
}

func TestCancelIdempotencyConcurrentReplayAndCrossRequestConflictPostgres(t *testing.T) {
	database := testpostgres.Open(t)
	repository := hostedpostgres.New(database.Pool)
	ctx := context.Background()
	interaction := authInteraction("hint_cancelidem12345678901234567890", 10*time.Minute)
	if _, _, err := repository.Create(ctx, createRecord(interaction, "evt_cancel_idem_create")); err != nil {
		t.Fatal(err)
	}
	browserDigest := bytes32(81)
	if _, _, err := repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: interaction.InteractionID, SessionID: "hbs_cancelidem12345678901234567890", TokenDigest: browserDigest, TTL: time.Minute, Event: event("evt_cancel_idem_open", interaction.InteractionID, "hosted.interaction_opened.v1")}); err != nil {
		t.Fatal(err)
	}
	record := hostedinteraction.CancelRecord{InteractionID: interaction.InteractionID, BrowserTokenDigest: browserDigest, ActorDigest: bytes32(82), KeyDigest: bytes32(83), RequestDigest: bytes32(84), Event: event("evt_cancel_idem_cancel", interaction.InteractionID, "hosted.interaction_cancelled.v1")}
	const workers = 8
	var group sync.WaitGroup
	results := make(chan error, workers)
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			value, cancelErr := repository.Cancel(ctx, record)
			if cancelErr == nil && value.Status != hostedinteraction.StatusCancelled {
				cancelErr = errors.New("cancel replay returned non-terminal projection")
			}
			results <- cancelErr
		}()
	}
	group.Wait()
	close(results)
	for cancelErr := range results {
		if cancelErr != nil {
			t.Fatal(cancelErr)
		}
	}
	changed := record
	changed.RequestDigest = bytes32(85)
	if _, err := repository.Cancel(ctx, changed); !errors.Is(err, hostedinteraction.ErrIdempotencyConflict) {
		t.Fatalf("same key changed request error=%v", err)
	}
	other := authInteraction("hint_cancelidemother123456789012345", 10*time.Minute)
	other.StateDigest, other.NonceDigest, other.PKCEChallengeDigest = bytes32(90), bytes32(91), bytes32(92)
	otherCreate := createRecord(other, "evt_cancel_idem_other_create")
	otherCreate.ActorDigest, otherCreate.KeyDigest, otherCreate.RequestDigest = bytes32(86), bytes32(87), bytes32(88)
	if _, _, err := repository.Create(ctx, otherCreate); err != nil {
		t.Fatal(err)
	}
	otherBrowser := bytes32(89)
	if _, _, err := repository.OpenBrowserSession(ctx, hostedinteraction.OpenBrowserRecord{InteractionID: other.InteractionID, SessionID: "hbs_cancelidemother123456789012345", TokenDigest: otherBrowser, TTL: time.Minute, Event: event("evt_cancel_idem_other_open", other.InteractionID, "hosted.interaction_opened.v1")}); err != nil {
		t.Fatal(err)
	}
	crossInteraction := record
	crossInteraction.InteractionID = other.InteractionID
	crossInteraction.BrowserTokenDigest = otherBrowser
	if _, err := repository.Cancel(ctx, crossInteraction); !errors.Is(err, hostedinteraction.ErrIdempotencyConflict) {
		t.Fatalf("same actor and key across interactions error=%v", err)
	}
	otherValue, err := repository.Get(ctx, other.InteractionID)
	if err != nil || otherValue.Status != hostedinteraction.StatusOpened {
		t.Fatalf("cross-interaction conflict mutated target: value=%+v err=%v", otherValue, err)
	}
	var records, events int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM hosted_interaction.idempotency_records WHERE operation='cancel'`).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM hosted_interaction.outbox_events WHERE event_type='hosted.interaction_cancelled.v1'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if records != 1 || events != 1 {
		t.Fatalf("cancel records=%d events=%d", records, events)
	}
}

func authInteraction(id string, ttl time.Duration) hostedinteraction.Interaction {
	now := time.Now().UTC()
	return hostedinteraction.Interaction{InteractionID: id, Route: hostedinteraction.RouteAuth, Scope: hostedinteraction.Scope{ProductID: "prod_test", ApplicationID: "app_test", Environment: "test", Channel: hostedinteraction.ChannelWeb}, Actor: hostedinteraction.Actor{Kind: "client", ClientSessionID: "client_session_1"}, ReturnTargetCode: "auth.callback", ReturnTargetURI: "https://client.test/callback", ReturnTargetPolicyVersion: 1, StateProtectorKeyRef: "test-key", StateCiphertext: bytesOf(9, 40), StateDigest: bytes32(1), NonceDigest: bytes32(2), PKCEChallengeDigest: bytes32(3), PKCEMethod: "S256", Status: hostedinteraction.StatusCreated, Version: 1, TraceID: "trace-test", CreatedAt: now, ExpiresAt: now.Add(ttl)}
}

func accountInteraction(id string, ttl time.Duration) hostedinteraction.Interaction {
	now := time.Now().UTC()
	return hostedinteraction.Interaction{InteractionID: id, Route: hostedinteraction.RouteAccount, Scope: hostedinteraction.Scope{ProductID: "prod_test", ApplicationID: "app_test", Environment: "test", Channel: hostedinteraction.ChannelWeb}, Actor: hostedinteraction.Actor{Kind: "user", UserID: "user_test", UserSessionID: "session_test"}, ReturnTargetCode: "account.callback", ReturnTargetURI: "https://client.test/account", ReturnTargetPolicyVersion: 1, StateProtectorKeyRef: "test-key", StateCiphertext: bytesOf(10, 40), StateDigest: bytes32(4), Status: hostedinteraction.StatusCreated, Version: 1, TraceID: "trace-account", CreatedAt: now, ExpiresAt: now.Add(ttl)}
}

func createRecord(value hostedinteraction.Interaction, eventID string) hostedinteraction.CreateRecord {
	return hostedinteraction.CreateRecord{Interaction: value, Operation: "create", ActorDigest: bytes32(10), KeyDigest: bytes32(11), RequestDigest: bytes32(12), Response: []byte(`{"ok":true}`), Event: event(eventID, value.InteractionID, "hosted.interaction_created.v1")}
}

func event(id, interactionID, eventType string) hostedinteraction.OutboxEvent {
	return hostedinteraction.OutboxEvent{EventID: id, InteractionID: interactionID, EventType: eventType, Payload: []byte(`{"interaction_id":"` + interactionID + `","status":"safe"}`)}
}

func bytes32(value byte) []byte {
	result := make([]byte, 32)
	for i := range result {
		result[i] = value
	}
	return result
}

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for i := range result {
		result[i] = value
	}
	return result
}
