package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/identity"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestHostedAuthProofAndGrantRedemptionPostgres(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	repository := identitypostgres.New(database.Pool)
	var clock time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&clock); err != nil {
		t.Fatal(err)
	}
	service := newEndUserService(t, repository, acceptingRegistrationProof{}, &capturingRecoveryDelivery{}, func() time.Time { return clock })
	scope := identity.EndUserSessionScope{ProductID: "product.hosted", ApplicationID: "application.hosted", Environment: "test"}
	registered, err := service.Register(ctx, identity.EndUserRegisterCommand{Scope: scope, Identifier: "hosted@example.com", Credential: "correct hosted password", VerificationProof: strings.Repeat("h", 16), DisplayName: "Hosted User", TraceID: "trace.hosted.register", IdempotencyKey: "register-hosted-user-0001"})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := service.AuthenticateHosted(ctx, identity.AuthenticateHostedCommand{Scope: scope, Identifier: "hosted@example.com", Credential: "correct hosted password", Source: "loopback", RiskSummary: map[string]any{"device": "known"}, TraceID: "trace.hosted.authenticate"})
	if err != nil {
		t.Fatal(err)
	}
	var proofRows, userSessions int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM identity.hosted_auth_proofs WHERE proof_id=$1`, proof.ProofID).Scan(&proofRows); err != nil || proofRows != 1 {
		t.Fatalf("proof rows=%d err=%v", proofRows, err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM identity.end_user_sessions WHERE user_id=$1`, registered.Session.UserID).Scan(&userSessions); err != nil || userSessions != 1 {
		t.Fatalf("hosted authentication created session count=%d err=%v", userSessions, err)
	}

	grantID := "hgrant_" + strings.Repeat("a", 24)
	command := identity.RedeemHostedAuthGrantCommand{GrantID: grantID, ProofID: proof.ProofID, Scope: scope, TraceID: "trace.hosted.redeem"}
	start := make(chan struct{})
	results := make(chan identity.EndUserIssuedSession, 2)
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, redeemErr := service.RedeemHostedAuthGrant(ctx, command)
			results <- result
			errorsFound <- redeemErr
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsFound)
	for redeemErr := range errorsFound {
		if redeemErr != nil {
			t.Fatalf("concurrent redemption error=%v", redeemErr)
		}
	}
	var first identity.EndUserIssuedSession
	for result := range results {
		if first.Session.SessionID == "" {
			first = result
			continue
		}
		if result.Session.SessionID != first.Session.SessionID || result.AccessToken != first.AccessToken || result.RefreshToken != first.RefreshToken {
			t.Fatalf("redemption did not recover stable result first=%+v second=%+v", first, result)
		}
	}
	if first.Session.UserID != registered.Session.UserID || first.AccessToken == "" || first.RefreshToken == "" {
		t.Fatalf("issued hosted session=%+v", first)
	}
	firstAccessExpiry, firstRefreshExpiry, firstAbsoluteExpiry := first.Session.AccessExpiresAt, first.Session.RefreshExpiresAt, first.Session.AbsoluteExpiresAt
	var redemptionRows, issuedSessionRows int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM identity.hosted_grant_redemptions WHERE grant_id=$1`, grantID).Scan(&redemptionRows); err != nil || redemptionRows != 1 {
		t.Fatalf("redemptions=%d err=%v", redemptionRows, err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM identity.end_user_sessions WHERE session_id=$1`, first.Session.SessionID).Scan(&issuedSessionRows); err != nil || issuedSessionRows != 1 {
		t.Fatalf("issued sessions=%d err=%v", issuedSessionRows, err)
	}
	clock = clock.Add(time.Minute)
	recovered, err := service.RedeemHostedAuthGrant(ctx, command)
	if err != nil || recovered.Session.SessionID != first.Session.SessionID || recovered.AccessToken != first.AccessToken || recovered.RefreshToken != first.RefreshToken || !recovered.Session.AccessExpiresAt.Equal(firstAccessExpiry) || !recovered.Session.RefreshExpiresAt.Equal(firstRefreshExpiry) || !recovered.Session.AbsoluteExpiresAt.Equal(firstAbsoluteExpiry) {
		t.Fatalf("later recovery=%+v err=%v", recovered, err)
	}
	otherGrant := command
	otherGrant.GrantID = "hgrant_" + strings.Repeat("b", 24)
	if _, err := service.RedeemHostedAuthGrant(ctx, otherGrant); !errors.Is(err, identity.ErrHostedAuthProofReplayed) {
		t.Fatalf("different grant error=%v", err)
	}
	var accessTokenExpiry, refreshTokenExpiry time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT max(expires_at) FILTER (WHERE token_type='access'),max(expires_at) FILTER (WHERE token_type='refresh') FROM identity.end_user_session_tokens WHERE session_id=$1`, first.Session.SessionID).Scan(&accessTokenExpiry, &refreshTokenExpiry); err != nil || !accessTokenExpiry.Equal(firstAccessExpiry) || !refreshTokenExpiry.Equal(firstRefreshExpiry) {
		t.Fatalf("token expiries access=%v refresh=%v err=%v", accessTokenExpiry, refreshTokenExpiry, err)
	}
	var outboxText, proofText, redemptionText, sessionText string
	queries := []struct {
		query string
		dest  *string
	}{
		{`SELECT COALESCE(string_agg(payload::text,' '),'') FROM identity.outbox_events`, &outboxText},
		{`SELECT COALESCE(string_agg(row_to_json(p)::text,' '),'') FROM identity.hosted_auth_proofs p`, &proofText},
		{`SELECT COALESCE(string_agg(row_to_json(r)::text,' '),'') FROM identity.hosted_grant_redemptions r`, &redemptionText},
		{`SELECT COALESCE(string_agg(row_to_json(v)::text,' '),'') FROM (SELECT s.session_id,s.user_id,s.product_id,s.application_id,s.tenant_id,s.token_family_id,s.authentication_method,s.auth_time,s.created_at,s.access_expires_at,s.refresh_expires_at,s.absolute_expires_at,t.token_id,t.token_type,encode(t.token_digest,'hex') token_digest FROM identity.end_user_sessions s JOIN identity.end_user_session_tokens t USING(session_id)) v`, &sessionText},
	}
	for _, query := range queries {
		if err := database.Pool.QueryRow(ctx, query.query).Scan(query.dest); err != nil {
			t.Fatal(err)
		}
	}
	allStored := outboxText + proofText + redemptionText + sessionText
	for _, secret := range []string{"hosted@example.com", "correct hosted password", first.AccessToken, first.RefreshToken} {
		if strings.Contains(allStored, secret) {
			t.Fatalf("hosted identity storage leaked secret %q", secret)
		}
	}
	if strings.Contains(outboxText, proof.ProofID) {
		t.Fatal("identity outbox leaked opaque hosted proof id")
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE identity.end_user_sessions SET access_expires_at=clock_timestamp()-interval '1 second' WHERE session_id=$1`, first.Session.SessionID); err != nil {
		t.Fatal(err)
	}
	expiredReplay, err := service.RedeemHostedAuthGrant(ctx, command)
	if !errors.Is(err, identity.ErrEndUserSessionExpired) || expiredReplay.AccessToken != "" || expiredReplay.RefreshToken != "" {
		t.Fatalf("expired completed redemption replay=%+v err=%v", expiredReplay, err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE identity.end_user_sessions SET access_expires_at=$2,absolute_expires_at=$3 WHERE session_id=$1`, first.Session.SessionID, firstAccessExpiry, firstAbsoluteExpiry); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE identity.users SET account_status='disabled' WHERE user_id=$1`, registered.Session.UserID); err != nil {
		t.Fatal(err)
	}
	disabledReplay, err := service.RedeemHostedAuthGrant(ctx, command)
	if !errors.Is(err, identity.ErrEndUserAccountDisabled) || disabledReplay.AccessToken != "" || disabledReplay.RefreshToken != "" {
		t.Fatalf("disabled completed redemption replay=%+v err=%v", disabledReplay, err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE identity.users SET account_status='active' WHERE user_id=$1`, registered.Session.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE identity.end_user_sessions SET revoked_at=clock_timestamp(),revoke_reason='test_replay_revoked' WHERE session_id=$1`, first.Session.SessionID); err != nil {
		t.Fatal(err)
	}
	revokedReplay, err := service.RedeemHostedAuthGrant(ctx, command)
	if !errors.Is(err, identity.ErrEndUserSessionRevoked) || revokedReplay.AccessToken != "" || revokedReplay.RefreshToken != "" {
		t.Fatalf("revoked completed redemption replay=%+v err=%v", revokedReplay, err)
	}

	contestedProof, err := service.AuthenticateHosted(ctx, identity.AuthenticateHostedCommand{Scope: scope, Identifier: "hosted@example.com", Credential: "correct hosted password", Source: "loopback", TraceID: "trace.hosted.contested"})
	if err != nil {
		t.Fatal(err)
	}
	contestedStart := make(chan struct{})
	contestedErrors := make(chan error, 2)
	for _, grant := range []string{"hgrant_" + strings.Repeat("c", 24), "hgrant_" + strings.Repeat("d", 24)} {
		wait.Add(1)
		go func(grantID string) {
			defer wait.Done()
			<-contestedStart
			_, redeemErr := service.RedeemHostedAuthGrant(ctx, identity.RedeemHostedAuthGrantCommand{GrantID: grantID, ProofID: contestedProof.ProofID, Scope: scope, TraceID: "trace.hosted.contested.redeem"})
			contestedErrors <- redeemErr
		}(grant)
	}
	close(contestedStart)
	wait.Wait()
	close(contestedErrors)
	var contestedSuccess, contestedReplay int
	for redeemErr := range contestedErrors {
		switch {
		case redeemErr == nil:
			contestedSuccess++
		case errors.Is(redeemErr, identity.ErrHostedAuthProofReplayed):
			contestedReplay++
		default:
			t.Fatalf("different grant concurrent error=%v", redeemErr)
		}
	}
	if contestedSuccess != 1 || contestedReplay != 1 {
		t.Fatalf("different grant concurrent success=%d replay=%d", contestedSuccess, contestedReplay)
	}
}

func TestHostedAuthProofScopeExpiryAndTransactionRollbackPostgres(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	repository := identitypostgres.New(database.Pool)
	var databaseNow time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	service := newEndUserService(t, repository, acceptingRegistrationProof{}, &capturingRecoveryDelivery{}, func() time.Time { return databaseNow })
	scope := identity.EndUserSessionScope{ProductID: "product.hosted.guard", ApplicationID: "application.hosted.guard", Environment: "test"}
	registered, err := service.Register(ctx, identity.EndUserRegisterCommand{Scope: scope, Identifier: "hosted-guard@example.com", Credential: "correct hosted guard password", VerificationProof: strings.Repeat("g", 16), TraceID: "trace.hosted.guard.register", IdempotencyKey: "register-hosted-guard-01"})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := service.AuthenticateHosted(ctx, identity.AuthenticateHostedCommand{Scope: scope, Identifier: "hosted-guard@example.com", Credential: "correct hosted guard password", Source: "loopback", TraceID: "trace.hosted.guard.authenticate"})
	if err != nil {
		t.Fatal(err)
	}
	wrongScope := scope
	wrongScope.ApplicationID = "application.hosted.forged"
	if _, err := service.RedeemHostedAuthGrant(ctx, identity.RedeemHostedAuthGrantCommand{GrantID: "hgrant_" + strings.Repeat("c", 24), ProofID: proof.ProofID, Scope: wrongScope, TraceID: "trace.hosted.wrong-scope"}); !errors.Is(err, identity.ErrEndUserScopeMismatch) {
		t.Fatalf("scope mismatch error=%v", err)
	}
	wrongEnvironment := scope
	wrongEnvironment.Environment = "production"
	if _, err := service.RedeemHostedAuthGrant(ctx, identity.RedeemHostedAuthGrantCommand{GrantID: "hgrant_" + strings.Repeat("v", 24), ProofID: proof.ProofID, Scope: wrongEnvironment, TraceID: "trace.hosted.wrong-environment"}); !errors.Is(err, identity.ErrEndUserScopeMismatch) {
		t.Fatalf("environment mismatch error=%v", err)
	}
	var consumedAt *time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT consumed_at FROM identity.hosted_auth_proofs WHERE proof_id=$1`, proof.ProofID).Scan(&consumedAt); err != nil || consumedAt != nil {
		t.Fatalf("scope mismatch consumed proof=%v err=%v", consumedAt, err)
	}

	expiredProof := identity.HostedAuthProof{ProofID: "hproof_" + strings.Repeat("e", 24), UserID: registered.Session.UserID, Scope: scope, AuthenticationMethod: "password", RiskSummaryDigest: bytes.Repeat([]byte{0x41}, 32), TTL: 100 * time.Millisecond, OutboxEvent: identity.OutboxEvent{EventID: "evt_hosted_expiring_proof", Topic: "identity.hosted_auth_succeeded.v1", Now: databaseNow, Payload: identity.SecurityEvent{AuditID: "aud_hosted_expiring_proof", OccurredAt: databaseNow, ActorID: registered.Session.UserID, Action: "identity.hosted_auth_succeeded", TargetType: "end_user", TargetID: "expired-proof", Result: "success", TraceID: "trace.hosted.expiring", RiskLevel: "normal"}}}
	if _, err := repository.CreateHostedAuthProofAndClearFailures(ctx, expiredProof, "scope", bytes.Repeat([]byte{0x42}, 32)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := service.RedeemHostedAuthGrant(ctx, identity.RedeemHostedAuthGrantCommand{GrantID: "hgrant_" + strings.Repeat("e", 24), ProofID: expiredProof.ProofID, Scope: scope, TraceID: "trace.hosted.expired"}); !errors.Is(err, identity.ErrHostedAuthProofExpired) {
		t.Fatalf("expired proof error=%v", err)
	}

	rollbackProof := identity.HostedAuthProof{ProofID: "hproof_" + strings.Repeat("r", 24), UserID: registered.Session.UserID, Scope: scope, AuthenticationMethod: "password", RiskSummaryDigest: bytes.Repeat([]byte{0x51}, 32), TTL: time.Hour, OutboxEvent: identity.OutboxEvent{EventID: "evt_hosted_rollback_proof", Topic: "identity.hosted_auth_succeeded.v1", Now: databaseNow, Payload: identity.SecurityEvent{AuditID: "aud_hosted_rollback_proof", OccurredAt: databaseNow, ActorID: registered.Session.UserID, Action: "identity.hosted_auth_succeeded", TargetType: "end_user", TargetID: "rollback-proof", Result: "success", TraceID: "trace.hosted.rollback-proof", RiskLevel: "normal"}}}
	if _, err := repository.CreateHostedAuthProofAndClearFailures(ctx, rollbackProof, "scope", bytes.Repeat([]byte{0x52}, 32)); err != nil {
		t.Fatal(err)
	}
	duplicateEventID := "evt_hosted_redemption_duplicate"
	if _, err := database.Pool.Exec(ctx, `INSERT INTO identity.outbox_events(event_id,topic,payload,next_attempt_at,created_at) VALUES($1,'identity.seed.v1','{}'::jsonb,$2,$2)`, duplicateEventID, databaseNow); err != nil {
		t.Fatal(err)
	}
	rollbackSession := identity.EndUserSession{SessionID: "uses_" + strings.Repeat("s", 24), ProductID: scope.ProductID, ApplicationID: scope.ApplicationID, Environment: scope.Environment, TokenFamilyID: "ufam_" + strings.Repeat("f", 24), AuthenticationMethod: "password", Version: 1, CreatedAt: databaseNow, LastSeenAt: databaseNow, AccessExpiresAt: databaseNow.Add(time.Minute), RefreshExpiresAt: databaseNow.Add(time.Hour), AbsoluteExpiresAt: databaseNow.Add(2 * time.Hour), AccountStatus: "active"}
	rollbackRecord := identity.HostedAuthGrantRedemption{GrantID: "hgrant_" + strings.Repeat("r", 24), ProofID: rollbackProof.ProofID, Scope: scope, RequestDigest: bytes.Repeat([]byte{0x61}, 32), Session: identity.NewEndUserSession{Session: rollbackSession, AccessToken: identity.EndUserSessionToken{TokenID: "uat_" + strings.Repeat("a", 24), TokenType: "access", Generation: 1, Digest: bytes.Repeat([]byte{0x62}, 32), CreatedAt: databaseNow, ExpiresAt: databaseNow.Add(time.Minute)}, RefreshToken: identity.EndUserSessionToken{TokenID: "urt_" + strings.Repeat("b", 24), TokenType: "refresh", Generation: 1, Digest: bytes.Repeat([]byte{0x63}, 32), CreatedAt: databaseNow, ExpiresAt: databaseNow.Add(time.Hour)}}, OutboxEvent: identity.OutboxEvent{EventID: duplicateEventID, Topic: "identity.hosted_auth_grant_redeemed.v1", Now: databaseNow, Payload: identity.SecurityEvent{AuditID: "aud_hosted_rollback", OccurredAt: databaseNow, ActorID: "system", Action: "identity.hosted_auth_grant_redeemed", TargetType: "end_user_session", TargetID: "rollback", Result: "success", TraceID: "trace.hosted.rollback", RiskLevel: "normal"}}, AccessTTL: time.Minute, RefreshTTL: time.Hour, AbsoluteTTL: 2 * time.Hour}
	forgedScopeRecord := rollbackRecord
	forgedScopeRecord.Scope.ApplicationID = "application.hosted.forged"
	if _, _, err := repository.RedeemHostedAuthGrant(ctx, forgedScopeRecord); !errors.Is(err, identity.ErrEndUserScopeMismatch) {
		t.Fatalf("forged redemption scope error=%v", err)
	}
	forgedUserRecord := rollbackRecord
	forgedUserRecord.Session.Session.UserID = registered.Session.UserID
	if _, _, err := repository.RedeemHostedAuthGrant(ctx, forgedUserRecord); !errors.Is(err, identity.ErrEndUserScopeMismatch) {
		t.Fatalf("prepopulated redemption user error=%v", err)
	}
	if _, _, err := repository.RedeemHostedAuthGrant(ctx, rollbackRecord); err == nil {
		t.Fatal("redemption unexpectedly committed despite outbox conflict")
	}
	var redemptions, sessions int
	if err := database.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM identity.hosted_grant_redemptions WHERE grant_id=$1),(SELECT count(*) FROM identity.end_user_sessions WHERE session_id=$2)`, rollbackRecord.GrantID, rollbackSession.SessionID).Scan(&redemptions, &sessions); err != nil || redemptions != 0 || sessions != 0 {
		t.Fatalf("rollback redemptions=%d sessions=%d err=%v", redemptions, sessions, err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT consumed_at FROM identity.hosted_auth_proofs WHERE proof_id=$1`, rollbackProof.ProofID).Scan(&consumedAt); err != nil || consumedAt != nil {
		t.Fatalf("rollback consumed proof=%v err=%v", consumedAt, err)
	}
}

func TestHostedAuthProofAndSessionUseDatabaseClockDespiteApplicationSkewPostgres(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	repository := identitypostgres.New(database.Pool)
	var databaseNow time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	scope := identity.EndUserSessionScope{ProductID: "product.hosted.clock", ApplicationID: "application.hosted.clock", Environment: "test"}
	baseService := newEndUserService(t, repository, acceptingRegistrationProof{}, &capturingRecoveryDelivery{}, func() time.Time { return databaseNow })
	if _, err := baseService.Register(ctx, identity.EndUserRegisterCommand{Scope: scope, Identifier: "hosted-clock@example.com", Credential: "correct hosted clock password", VerificationProof: strings.Repeat("k", 16), TraceID: "trace.hosted.clock.register", IdempotencyKey: "register-hosted-clock-01"}); err != nil {
		t.Fatal(err)
	}
	for index, offset := range []time.Duration{-time.Hour, time.Hour} {
		t.Run(offset.String(), func(t *testing.T) {
			applicationNow := databaseNow.Add(offset)
			service := newEndUserService(t, repository, acceptingRegistrationProof{}, &capturingRecoveryDelivery{}, func() time.Time { return applicationNow })
			var before, after time.Time
			if err := database.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			proof, err := service.AuthenticateHosted(ctx, identity.AuthenticateHostedCommand{Scope: scope, Identifier: "hosted-clock@example.com", Credential: "correct hosted clock password", Source: "loopback", TraceID: "trace.hosted.clock.authenticate"})
			if err != nil {
				t.Fatal(err)
			}
			if err := database.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if proof.AuthTime.Before(before) || proof.AuthTime.After(after) || !proof.ExpiresAt.Equal(proof.AuthTime.Add(5*time.Minute)) {
				t.Fatalf("canonical proof auth=%v expires=%v db-window=[%v,%v] app=%v", proof.AuthTime, proof.ExpiresAt, before, after, applicationNow)
			}
			grantID := "hgrant_" + strings.Repeat(string(rune('m'+index)), 24)
			issued, err := service.RedeemHostedAuthGrant(ctx, identity.RedeemHostedAuthGrantCommand{GrantID: grantID, ProofID: proof.ProofID, Scope: scope, TraceID: "trace.hosted.clock.redeem"})
			if err != nil {
				t.Fatal(err)
			}
			var persistedCreated, afterRedeem time.Time
			if err := database.Pool.QueryRow(ctx, `SELECT created_at,clock_timestamp() FROM identity.end_user_sessions WHERE session_id=$1`, issued.Session.SessionID).Scan(&persistedCreated, &afterRedeem); err != nil {
				t.Fatal(err)
			}
			if issued.Session.CreatedAt.Before(after) || issued.Session.CreatedAt.After(afterRedeem) || !issued.Session.CreatedAt.Equal(persistedCreated) || !issued.Session.AccessExpiresAt.Equal(issued.Session.CreatedAt.Add(15*time.Minute)) || !issued.Session.RefreshExpiresAt.Equal(issued.Session.CreatedAt.Add(time.Hour)) || !issued.Session.AbsoluteExpiresAt.Equal(issued.Session.CreatedAt.Add(24*time.Hour)) {
				t.Fatalf("canonical session=%+v persisted-created=%v app=%v", issued.Session, persistedCreated, applicationNow)
			}
		})
	}
}

func TestValidateHostedSessionPostgres(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	repository := identitypostgres.New(database.Pool)
	var databaseNow time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	service := newEndUserService(t, repository, acceptingRegistrationProof{}, &capturingRecoveryDelivery{}, func() time.Time { return databaseNow })
	tenant := "tenant.hosted.validation"
	scope := identity.EndUserSessionScope{ProductID: "product.hosted.validation", ApplicationID: "application.hosted.validation", TenantID: &tenant, Environment: "test"}
	registered, err := service.Register(ctx, identity.EndUserRegisterCommand{Scope: scope, Identifier: "hosted-validation@example.com", Credential: "correct hosted validation password", VerificationProof: strings.Repeat("s", 16), TraceID: "trace.hosted.validation.register", IdempotencyKey: "register-hosted-validation-01"})
	if err != nil {
		t.Fatal(err)
	}
	expected := identity.HostedSessionExpectation{Scope: scope, UserID: registered.Session.UserID, SessionID: registered.Session.SessionID}
	if err := service.ValidateHostedSession(ctx, expected); err != nil {
		t.Fatalf("valid hosted session error=%v", err)
	}
	wrongUser := expected
	wrongUser.UserID = "usr_forged_hosted_user"
	if err := service.ValidateHostedSession(ctx, wrongUser); !errors.Is(err, identity.ErrEndUserScopeMismatch) {
		t.Fatalf("wrong user error=%v", err)
	}
	wrongScope := expected
	wrongScope.Scope.ApplicationID = "application.hosted.forged"
	if err := service.ValidateHostedSession(ctx, wrongScope); !errors.Is(err, identity.ErrEndUserScopeMismatch) {
		t.Fatalf("wrong scope error=%v", err)
	}
	wrongTenant := expected
	forgedTenant := "tenant.hosted.forged"
	wrongTenant.Scope.TenantID = &forgedTenant
	if err := service.ValidateHostedSession(ctx, wrongTenant); !errors.Is(err, identity.ErrEndUserScopeMismatch) {
		t.Fatalf("wrong tenant error=%v", err)
	}
	wrongEnvironment := expected
	wrongEnvironment.Scope.Environment = "production"
	if err := service.ValidateHostedSession(ctx, wrongEnvironment); !errors.Is(err, identity.ErrEndUserScopeMismatch) {
		t.Fatalf("wrong environment error=%v", err)
	}
	unknown := expected
	unknown.SessionID = "uses_missing_hosted_session"
	if err := service.ValidateHostedSession(ctx, unknown); !errors.Is(err, identity.ErrEndUserSessionExpired) {
		t.Fatalf("unknown session error=%v", err)
	}

	second, err := service.Login(ctx, identity.EndUserLoginCommand{Scope: scope, Identifier: "hosted-validation@example.com", Credential: "correct hosted validation password", Source: "loopback", TraceID: "trace.hosted.validation.login"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE identity.end_user_sessions SET access_expires_at=clock_timestamp()-interval '3 seconds',refresh_expires_at=clock_timestamp()-interval '2 seconds',absolute_expires_at=clock_timestamp()-interval '1 second' WHERE session_id=$1`, second.Session.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateHostedSession(ctx, identity.HostedSessionExpectation{Scope: scope, UserID: second.Session.UserID, SessionID: second.Session.SessionID}); !errors.Is(err, identity.ErrEndUserSessionExpired) {
		t.Fatalf("expired hosted session error=%v", err)
	}
	legacy, err := service.Login(ctx, identity.EndUserLoginCommand{Scope: scope, Identifier: "hosted-validation@example.com", Credential: "correct hosted validation password", Source: "loopback", TraceID: "trace.hosted.validation.legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE identity.end_user_sessions SET environment=NULL WHERE session_id=$1`, legacy.Session.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateHostedSession(ctx, identity.HostedSessionExpectation{Scope: scope, UserID: legacy.Session.UserID, SessionID: legacy.Session.SessionID}); !errors.Is(err, identity.ErrEndUserScopeMismatch) {
		t.Fatalf("legacy null environment error=%v", err)
	}
	if err := service.Logout(ctx, registered.AccessToken, "trace.hosted.validation.logout", scope); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateHostedSession(ctx, expected); !errors.Is(err, identity.ErrEndUserSessionRevoked) {
		t.Fatalf("revoked hosted session error=%v", err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE identity.users SET account_status='disabled',updated_at=clock_timestamp() WHERE user_id=$1`, registered.Session.UserID); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateHostedSession(ctx, expected); !errors.Is(err, identity.ErrEndUserAccountDisabled) {
		t.Fatalf("disabled hosted user error=%v", err)
	}
}
