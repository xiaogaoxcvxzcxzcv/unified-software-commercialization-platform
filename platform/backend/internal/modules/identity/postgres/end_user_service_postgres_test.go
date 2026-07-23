package postgres_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"platform.local/capability-platform/backend/internal/modules/identity"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

type acceptingRegistrationProof struct{}

func (acceptingRegistrationProof) VerifyRegistration(context.Context, identity.EndUserSessionScope, identity.NormalizedIdentifier, string, string, []byte, []byte) error {
	return nil
}

type errorRegistrationProof struct{ err error }

func (p errorRegistrationProof) VerifyRegistration(context.Context, identity.EndUserSessionScope, identity.NormalizedIdentifier, string, string, []byte, []byte) error {
	return p.err
}

type capturingRecoveryDelivery struct {
	proof    string
	commands []identity.RecoveryDeliveryCommand
}

func (d *capturingRecoveryDelivery) EnqueueSecurity(_ context.Context, command identity.SecurityDeliveryCommand) error {
	d.proof = command.Proof
	d.commands = append(d.commands, command)
	return nil
}

func TestEndUserServiceAuthenticationLifecycleAndRefreshRecovery(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	delivery := &capturingRecoveryDelivery{}
	now := time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)
	clock := now
	service := newEndUserService(t, repository, acceptingRegistrationProof{}, delivery, func() time.Time { return clock })
	scope := identity.EndUserSessionScope{ProductID: "product.service", ApplicationID: "application.service", Environment: "test"}
	registerCommand := identity.EndUserRegisterCommand{Scope: scope, Identifier: "service@example.com", Credential: "correct service password", VerificationProof: strings.Repeat("p", 16), DisplayName: "Service User", TraceID: "trace.register", IdempotencyKey: "register-key-00000001"}
	registered, err := service.Register(context.Background(), registerCommand)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if registered.AccessToken == "" || registered.RefreshToken == "" || registered.Session.ProductID != scope.ProductID {
		t.Fatalf("registered = %+v", registered)
	}
	var containsForbidden, hasRequiredSessionKeys bool
	if err := database.Pool.QueryRow(context.Background(), `SELECT ((response_document->'session') ?| ARRAY['risk_summary_digest','RiskSummaryDigest','token_family_id','TokenFamilyID','revoke_reason','RevokeReason','access_token','refresh_token','password','proof']) OR ((response_document->'profile') ?| ARRAY['risk_summary_digest','RiskSummaryDigest','token_family_id','TokenFamilyID','revoke_reason','RevokeReason','access_token','refresh_token','password','proof']), ((response_document->'session') ?& ARRAY['session_id','user_id','product_id','application_id','authentication_method','version','auth_time','created_at','last_seen_at','access_expires_at','refresh_expires_at','absolute_expires_at','account_status']) FROM identity.end_user_idempotency_records WHERE operation='register'`).Scan(&containsForbidden, &hasRequiredSessionKeys); err != nil || containsForbidden || !hasRequiredSessionKeys {
		t.Fatalf("registration response_document whitelist failed: forbidden=%t required=%t err=%v", containsForbidden, hasRequiredSessionKeys, err)
	}
	recoveredRegistration, err := service.Register(context.Background(), registerCommand)
	if err != nil || recoveredRegistration.Session.SessionID != registered.Session.SessionID || recoveredRegistration.AccessToken != registered.AccessToken || recoveredRegistration.RefreshToken != registered.RefreshToken || recoveredRegistration.Profile.UserID != registered.Profile.UserID {
		t.Fatalf("idempotent Register() = %+v, %v", recoveredRegistration, err)
	}
	current, err := service.ResolveCurrentSession(context.Background(), registered.AccessToken)
	if err != nil || current.AccessToken != "" || current.RefreshToken != "" || current.Profile.DisplayName != "Service User" {
		t.Fatalf("CurrentSession() = %+v, %v", current, err)
	}
	clock = now.Add(11 * time.Minute)
	if err := service.ChangePasswordResolved(context.Background(), registered.AccessToken, "correct service password", "unused replacement password", false, "password-change-key-01", "trace.password.stale"); !errors.Is(err, identity.ErrEndUserReauthenticationRequired) {
		t.Fatalf("stale ChangePassword() error = %v", err)
	}

	clock = now.Add(time.Minute)
	absoluteExpiry := now.Add(5 * time.Minute)
	if _, err := database.Pool.Exec(context.Background(), `UPDATE identity.end_user_sessions SET access_expires_at=$2,refresh_expires_at=$2,absolute_expires_at=$2 WHERE session_id=$1`, registered.Session.SessionID, absoluteExpiry); err != nil {
		t.Fatal(err)
	}
	first, err := service.RefreshResolved(context.Background(), registered.RefreshToken, "request-id-0000000001", "trace.refresh.first")
	if err != nil {
		t.Fatalf("first Refresh() error = %v", err)
	}
	if !first.Session.AccessExpiresAt.Equal(absoluteExpiry) || !first.Session.RefreshExpiresAt.Equal(absoluteExpiry) {
		t.Fatalf("refresh expiries were not clamped to absolute expiry: access=%v refresh=%v absolute=%v", first.Session.AccessExpiresAt, first.Session.RefreshExpiresAt, absoluteExpiry)
	}
	if _, err := database.Pool.Exec(context.Background(), `UPDATE identity.end_user_session_tokens SET expires_at=$2 WHERE token_family_id=$1 AND token_type='refresh' AND generation=1`, registered.Session.TokenFamilyID, now.Add(70*time.Second)); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(90 * time.Second)
	recovered, err := service.RefreshResolved(context.Background(), registered.RefreshToken, "request-id-0000000001", "trace.refresh.retry")
	if err != nil {
		t.Fatalf("recovered Refresh() error = %v", err)
	}
	if recovered.AccessToken != first.AccessToken || recovered.RefreshToken != first.RefreshToken || recovered.Session.SessionID != first.Session.SessionID {
		t.Fatalf("refresh recovery changed result: first=%+v recovered=%+v", first, recovered)
	}
	updatedName := "Updated Service User"
	namePatch := identity.EndUserProfilePatch{DisplayName: identity.EndUserProfilePatchValue{Set: true, Value: &updatedName}}
	updatedProfile, err := service.PatchProfileResolved(context.Background(), first.AccessToken, namePatch, recovered.Profile.Version, "profile-update-key-01", "trace.profile.update")
	if err != nil {
		t.Fatalf("UpdateProfile() error = %v", err)
	}
	recoveredProfile, err := service.PatchProfileResolved(context.Background(), first.AccessToken, namePatch, recovered.Profile.Version, "profile-update-key-01", "trace.profile.retry")
	if err != nil || recoveredProfile.Version != updatedProfile.Version || recoveredProfile.DisplayName != updatedProfile.DisplayName {
		t.Fatalf("idempotent UpdateProfile() = %+v, %v", recoveredProfile, err)
	}
	locale := "en-US"
	localePatch := identity.EndUserProfilePatch{Locale: identity.EndUserProfilePatchValue{Set: true, Value: &locale}}
	secondProfile, err := service.PatchProfileResolved(context.Background(), first.AccessToken, localePatch, updatedProfile.Version, "profile-update-key-02", "trace.profile.second")
	if err != nil {
		t.Fatalf("second UpdateProfile() error = %v", err)
	}
	registrationSnapshot, err := service.Register(context.Background(), registerCommand)
	if err != nil || registrationSnapshot.Profile.Version != registered.Profile.Version || registrationSnapshot.Profile.DisplayName != registered.Profile.DisplayName {
		t.Fatalf("registration replay lost first profile snapshot: got=%+v want=%+v err=%v", registrationSnapshot.Profile, registered.Profile, err)
	}
	firstSnapshot, err := service.PatchProfileResolved(context.Background(), first.AccessToken, namePatch, recovered.Profile.Version, "profile-update-key-01", "trace.profile.first-replay")
	if err != nil || firstSnapshot.Version != updatedProfile.Version || firstSnapshot.DisplayName != updatedProfile.DisplayName || !firstSnapshot.UpdatedAt.Equal(updatedProfile.UpdatedAt) || firstSnapshot.Locale != nil || secondProfile.Locale == nil {
		t.Fatalf("first profile replay lost response snapshot: first=%+v second=%+v err=%v", firstSnapshot, secondProfile, err)
	}
	conflictingName := "Conflicting Service User"
	conflictingPatch := identity.EndUserProfilePatch{DisplayName: identity.EndUserProfilePatchValue{Set: true, Value: &conflictingName}}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := service.PatchProfileResolved(context.Background(), first.AccessToken, conflictingPatch, recovered.Profile.Version, "profile-conflict-key-01", "trace.profile.conflict"); !errors.Is(err, identity.ErrEndUserVersionConflict) {
			t.Fatalf("profile conflict attempt %d error = %v", attempt, err)
		}
	}
	changedConflictName := "Changed Conflict Request"
	changedConflict := identity.EndUserProfilePatch{DisplayName: identity.EndUserProfilePatchValue{Set: true, Value: &changedConflictName}}
	if _, err := service.PatchProfileResolved(context.Background(), first.AccessToken, changedConflict, recovered.Profile.Version, "profile-conflict-key-01", "trace.profile.conflict.changed"); !errors.Is(err, identity.ErrEndUserVersionConflict) {
		t.Fatalf("changed profile conflict request error = %v", err)
	}
	var profileFailureReason string
	if err := database.Pool.QueryRow(context.Background(), `SELECT response_document->>'reason' FROM identity.end_user_idempotency_records WHERE operation='profile_update' AND state='failed'`).Scan(&profileFailureReason); err != nil || profileFailureReason != "version_conflict" {
		t.Fatalf("profile terminal failure reason=%q err=%v", profileFailureReason, err)
	}
	invalidPasswordKey := "password-invalid-key-01"
	for attempt := 0; attempt < 2; attempt++ {
		if err := service.ChangePasswordResolved(context.Background(), first.AccessToken, "wrong service password", "ignored replacement password", false, invalidPasswordKey, "trace.password.invalid"); !errors.Is(err, identity.ErrEndUserInvalidCredentials) {
			t.Fatalf("invalid password attempt %d error = %v", attempt, err)
		}
	}
	if err := service.ChangePasswordResolved(context.Background(), first.AccessToken, "different wrong password", "ignored replacement password", false, invalidPasswordKey, "trace.password.invalid.changed"); !errors.Is(err, identity.ErrEndUserVersionConflict) {
		t.Fatalf("changed invalid password request error = %v", err)
	}
	passwordKey := "password-change-key-02"
	if err := service.ChangePasswordResolved(context.Background(), first.AccessToken, "correct service password", "changed service password", false, passwordKey, "trace.password.change"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if _, err := service.ResolveCurrentSession(context.Background(), first.AccessToken); !errors.Is(err, identity.ErrEndUserSessionRevoked) {
		t.Fatalf("old access after password change error = %v", err)
	}
	if err := service.ChangePasswordResolved(context.Background(), first.AccessToken, "correct service password", "changed service password", false, passwordKey, "trace.password.lost-response-retry"); err != nil {
		t.Fatalf("lost password response was not recoverable with original access proof: %v", err)
	}
	if err := service.ChangePasswordResolved(context.Background(), "unrecognized-access-token", "correct service password", "changed service password", false, passwordKey, "trace.password.invalid-access"); !errors.Is(err, identity.ErrEndUserSessionExpired) {
		t.Fatalf("unrecognized access token probed password idempotency result: %v", err)
	}
	clock = now.Add(105 * time.Second)
	afterPassword, err := service.RefreshResolved(context.Background(), first.RefreshToken, "request-id-password-01", "trace.password.refresh")
	if err != nil {
		t.Fatalf("refresh after password change error = %v", err)
	}
	if afterPassword.Session.Version <= first.Session.Version {
		t.Fatalf("session version did not advance: before=%d after=%d", first.Session.Version, afterPassword.Session.Version)
	}
	clock = now.Add(2 * time.Minute)
	if _, err := service.RefreshResolved(context.Background(), registered.RefreshToken, "request-id-0000000002", "trace.refresh.replay"); !errors.Is(err, identity.ErrEndUserRefreshReplayed) {
		t.Fatalf("different request refresh error = %v", err)
	}
	if _, err := service.CurrentSession(context.Background(), first.AccessToken, scope); !errors.Is(err, identity.ErrEndUserSessionRevoked) {
		t.Fatalf("access after replay error = %v", err)
	}
}

func TestEndUserServiceRejectsExistingBearerAfterGlobalDisable(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 18, 1, 30, 0, 0, time.UTC)
	service := newEndUserService(t, repository, acceptingRegistrationProof{}, &capturingRecoveryDelivery{}, func() time.Time { return now })
	scope := identity.EndUserSessionScope{ProductID: "product.disabled", ApplicationID: "application.disabled", Environment: "test"}
	registered, err := service.Register(context.Background(), identity.EndUserRegisterCommand{Scope: scope, Identifier: "disabled@example.com", Credential: "correct disabled password", VerificationProof: strings.Repeat("d", 16), TraceID: "trace.disabled", IdempotencyKey: "register-key-disabled-01"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(context.Background(), `UPDATE identity.users SET account_status='disabled',security_changed_at=$2 WHERE user_id=$1`, registered.Session.UserID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveCurrentSession(context.Background(), registered.AccessToken); !errors.Is(err, identity.ErrEndUserAccountDisabled) {
		t.Fatalf("CurrentSession after disable error = %v", err)
	}
	if _, err := service.RefreshResolved(context.Background(), registered.RefreshToken, "request-id-disabled-01", "trace.disabled.refresh"); !errors.Is(err, identity.ErrEndUserAccountDisabled) {
		t.Fatalf("Refresh after disable error = %v", err)
	}
	var consumed *time.Time
	if err := database.Pool.QueryRow(context.Background(), `SELECT consumed_at FROM identity.end_user_session_tokens WHERE token_family_id=$1 AND token_type='refresh'`, registered.Session.TokenFamilyID).Scan(&consumed); err != nil || consumed != nil {
		t.Fatalf("disabled refresh mutated token: consumed=%v err=%v", consumed, err)
	}
}

func TestEndUserAccessCannotOutliveAbsoluteSessionExpiry(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 18, 1, 40, 0, 0, time.UTC)
	clock := now
	service := newEndUserService(t, repository, acceptingRegistrationProof{}, &capturingRecoveryDelivery{}, func() time.Time { return clock })
	scope := identity.EndUserSessionScope{ProductID: "product.absolute", ApplicationID: "application.absolute", Environment: "test"}
	registered, err := service.Register(context.Background(), identity.EndUserRegisterCommand{Scope: scope, Identifier: "absolute@example.com", Credential: "correct absolute password", VerificationProof: strings.Repeat("a", 16), TraceID: "trace.absolute", IdempotencyKey: "register-key-absolute-01"})
	if err != nil {
		t.Fatal(err)
	}
	absoluteExpiry := now.Add(time.Minute)
	if _, err := database.Pool.Exec(context.Background(), `UPDATE identity.end_user_sessions SET access_expires_at=$2,refresh_expires_at=$2,absolute_expires_at=$2 WHERE session_id=$1`, registered.Session.SessionID, absoluteExpiry); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(2 * time.Minute)
	if _, err := service.ResolveCurrentSession(context.Background(), registered.AccessToken); !errors.Is(err, identity.ErrEndUserSessionExpired) {
		t.Fatalf("access beyond absolute expiry error = %v", err)
	}
}

func TestEndUserRegistrationInvalidProofIsStableButProviderFailureIsRetryable(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 18, 1, 45, 0, 0, time.UTC)
	scope := identity.EndUserSessionScope{ProductID: "product.proof", ApplicationID: "application.proof", Environment: "test"}
	command := identity.EndUserRegisterCommand{Scope: scope, Identifier: "proof@example.com", Credential: "correct proof password", VerificationProof: "invalid-proof-value", TraceID: "trace.proof", IdempotencyKey: "register-invalid-proof-01"}
	service := newEndUserService(t, repository, errorRegistrationProof{err: identity.ErrEndUserInvalidCredentials}, &capturingRecoveryDelivery{}, func() time.Time { return now })
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := service.Register(context.Background(), command); !errors.Is(err, identity.ErrEndUserInvalidCredentials) {
			t.Fatalf("invalid registration proof attempt %d error = %v", attempt, err)
		}
	}
	changed := command
	changed.VerificationProof = "different-invalid-proof"
	if _, err := service.Register(context.Background(), changed); !errors.Is(err, identity.ErrEndUserVersionConflict) {
		t.Fatalf("changed invalid registration request error = %v", err)
	}
	var state, reason string
	if err := database.Pool.QueryRow(context.Background(), `SELECT state,response_document->>'reason' FROM identity.end_user_idempotency_records WHERE operation='register'`).Scan(&state, &reason); err != nil || state != "failed" || reason != "invalid_credentials" {
		t.Fatalf("registration proof terminal state=%q reason=%q err=%v", state, reason, err)
	}
	providerFailure := errors.New("verification provider unavailable")
	transientCommand := command
	transientCommand.Identifier = "provider@example.com"
	transientCommand.IdempotencyKey = "register-provider-error-01"
	transientService := newEndUserService(t, repository, errorRegistrationProof{err: providerFailure}, &capturingRecoveryDelivery{}, func() time.Time { return now })
	if _, err := transientService.Register(context.Background(), transientCommand); !errors.Is(err, providerFailure) {
		t.Fatalf("provider failure error = %v", err)
	}
	var transientRecords int
	if err := database.Pool.QueryRow(context.Background(), `SELECT count(*) FROM identity.end_user_idempotency_records WHERE operation='register' AND state='failed'`).Scan(&transientRecords); err != nil || transientRecords != 1 {
		t.Fatalf("provider failure persisted terminal record: count=%d err=%v", transientRecords, err)
	}
}

func TestEndUserRegistrationConcurrentSameKeyWaitsAndRecovers(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 18, 1, 47, 0, 0, time.UTC)
	service := newEndUserService(t, repository, acceptingRegistrationProof{}, &capturingRecoveryDelivery{}, func() time.Time { return now })
	command := identity.EndUserRegisterCommand{Scope: identity.EndUserSessionScope{ProductID: "product.concurrent", ApplicationID: "application.concurrent", Environment: "test"}, Identifier: "concurrent@example.com", Credential: "correct concurrent password", VerificationProof: strings.Repeat("q", 16), DisplayName: "Concurrent User", TraceID: "trace.concurrent", IdempotencyKey: "register-concurrent-key-01"}
	start := make(chan struct{})
	results := make(chan struct {
		issued identity.EndUserIssuedSession
		err    error
	}, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			issued, err := service.Register(context.Background(), command)
			results <- struct {
				issued identity.EndUserIssuedSession
				err    error
			}{issued: issued, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	var first *identity.EndUserIssuedSession
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent Register() error = %v", result.err)
		}
		if first == nil {
			value := result.issued
			first = &value
			continue
		}
		if result.issued.Session.SessionID != first.Session.SessionID || result.issued.AccessToken != first.AccessToken || result.issued.RefreshToken != first.RefreshToken || result.issued.Profile != first.Profile {
			t.Fatalf("concurrent registration results differ: first=%+v second=%+v", *first, result.issued)
		}
	}
	changed := command
	changed.DisplayName = "Changed Concurrent User"
	if _, err := service.Register(context.Background(), changed); !errors.Is(err, identity.ErrEndUserVersionConflict) {
		t.Fatalf("same key different registration request error = %v", err)
	}
}

func TestEndUserPasswordVersionConflictIsTerminal(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 18, 1, 50, 0, 0, time.UTC)
	service := newEndUserService(t, repository, acceptingRegistrationProof{}, &capturingRecoveryDelivery{}, func() time.Time { return now })
	scope := identity.EndUserSessionScope{ProductID: "product.password-conflict", ApplicationID: "application.password-conflict", Environment: "test"}
	registered, err := service.Register(context.Background(), identity.EndUserRegisterCommand{Scope: scope, Identifier: "password-conflict@example.com", Credential: "original conflict password", VerificationProof: strings.Repeat("c", 16), TraceID: "trace.password-conflict.register", IdempotencyKey: "register-password-conflict"})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := repository.FindEndUserPasswordCredentialByUser(context.Background(), registered.Session.UserID)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("replacement conflict password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	record := identity.EndUserIdempotency{Operation: "password_change", ScopeID: "password-conflict-scope", ActorDigest: protectedDigest("password-conflict-actor"), KeyDigest: protectedDigest("password-conflict-key"), RequestDigest: protectedDigest("password-conflict-request"), ResourceID: registered.Session.SessionID, Now: now}
	event := endUserEvent("event.password-conflict", "identity.password_changed.v1", registered.Session.UserID, now)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := repository.ReplaceEndUserPasswordIdempotent(context.Background(), registered.Session.UserID, registered.Session.SessionID, hash, "bcrypt", credential.Version-1, now, false, event, record); !errors.Is(err, identity.ErrEndUserVersionConflict) {
			t.Fatalf("password version conflict attempt %d error = %v", attempt, err)
		}
	}
	changed := record
	changed.RequestDigest = protectedDigest("changed-password-conflict-request")
	if _, err := repository.ReplaceEndUserPasswordIdempotent(context.Background(), registered.Session.UserID, registered.Session.SessionID, hash, "bcrypt", credential.Version-1, now, false, event, changed); !errors.Is(err, identity.ErrEndUserVersionConflict) {
		t.Fatalf("changed password conflict request error = %v", err)
	}
	var state, reason string
	if err := database.Pool.QueryRow(context.Background(), `SELECT state,response_document->>'reason' FROM identity.end_user_idempotency_records WHERE operation='password_change'`).Scan(&state, &reason); err != nil || state != "failed" || reason != "version_conflict" {
		t.Fatalf("password conflict terminal state=%q reason=%q err=%v", state, reason, err)
	}
}

func TestEndUserPasswordChangeConcurrentSameKeyWaitsAndRecovers(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 18, 1, 55, 0, 0, time.UTC)
	service := newEndUserService(t, repository, acceptingRegistrationProof{}, &capturingRecoveryDelivery{}, func() time.Time { return now })
	scope := identity.EndUserSessionScope{ProductID: "product.password-concurrent", ApplicationID: "application.password-concurrent", Environment: "test"}
	registered, err := service.Register(context.Background(), identity.EndUserRegisterCommand{Scope: scope, Identifier: "password-concurrent@example.com", Credential: "original concurrent password", VerificationProof: strings.Repeat("w", 16), TraceID: "trace.password-concurrent.register", IdempotencyKey: "register-password-concurrent"})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- service.ChangePasswordResolved(context.Background(), registered.AccessToken, "original concurrent password", "replacement concurrent password", false, "password-concurrent-key-01", "trace.password-concurrent")
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent ChangePasswordResolved() error = %v", err)
		}
	}
	var completed int
	if err := database.Pool.QueryRow(context.Background(), `SELECT count(*) FROM identity.end_user_idempotency_records WHERE operation='password_change' AND state='completed'`).Scan(&completed); err != nil || completed != 1 {
		t.Fatalf("password concurrent terminal count=%d err=%v", completed, err)
	}
}

func TestEndUserServiceRecoveryAndProviderFailClosed(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 18, 2, 0, 0, 0, time.UTC)
	scope := identity.EndUserSessionScope{ProductID: "product.recovery", ApplicationID: "application.recovery", Environment: "test"}
	withoutProviders := newEndUserService(t, repository, nil, nil, func() time.Time { return now })
	if _, err := withoutProviders.Register(context.Background(), identity.EndUserRegisterCommand{Scope: scope}); !errors.Is(err, identity.ErrEndUserProviderUnavailable) {
		t.Fatalf("Register without provider error = %v", err)
	}
	if _, err := withoutProviders.StartRecovery(context.Background(), identity.StartEndUserRecoveryCommand{Scope: scope, Identifier: "missing@example.com", TraceID: "trace.missing", IdempotencyKey: "missing-recovery-key-1"}); !errors.Is(err, identity.ErrEndUserProviderUnavailable) {
		t.Fatalf("StartRecovery without provider error = %v", err)
	}

	delivery := &capturingRecoveryDelivery{}
	service := newEndUserService(t, repository, acceptingRegistrationProof{}, delivery, func() time.Time { return now })
	missingStart := identity.StartEndUserRecoveryCommand{Scope: scope, Identifier: "no-account@example.com", TraceID: "trace.recovery.missing", IdempotencyKey: "recovery-start-missing-1"}
	missingContinuation, err := service.StartRecovery(context.Background(), missingStart)
	if err != nil {
		t.Fatal(err)
	}
	missingComplete := identity.CompleteEndUserRecoveryCommand{Scope: scope, Continuation: missingContinuation, Proof: delivery.proof, NewCredential: "unused replacement password", TraceID: "trace.recovery.missing.complete", IdempotencyKey: "recovery-complete-missing"}
	if err := service.CompleteRecovery(context.Background(), missingComplete); err != nil {
		t.Fatalf("unmatched CompleteRecovery() error = %v", err)
	}
	if err := service.CompleteRecovery(context.Background(), missingComplete); err != nil {
		t.Fatalf("idempotent unmatched CompleteRecovery() error = %v", err)
	}
	registered, err := service.Register(context.Background(), identity.EndUserRegisterCommand{Scope: scope, Identifier: "recover-service@example.com", Credential: "original service password", VerificationProof: strings.Repeat("v", 16), TraceID: "trace.recovery.register", IdempotencyKey: "register-key-00000002"})
	if err != nil {
		t.Fatal(err)
	}
	startCommand := identity.StartEndUserRecoveryCommand{Scope: scope, Identifier: "recover-service@example.com", TraceID: "trace.recovery.start", IdempotencyKey: "recovery-start-key-001"}
	continuation, err := service.StartRecovery(context.Background(), startCommand)
	if err != nil || continuation == "" || delivery.proof == "" {
		t.Fatalf("StartRecovery() continuation=%q proof=%q err=%v", continuation, delivery.proof, err)
	}
	if repeated, err := service.StartRecovery(context.Background(), startCommand); err != nil || repeated != continuation {
		t.Fatalf("idempotent StartRecovery() = %q, %v", repeated, err)
	}
	if len(delivery.commands) != 3 || delivery.commands[0].DeliveryID == "" || delivery.commands[0].Proof == "" || delivery.commands[1].DeliveryID != delivery.commands[2].DeliveryID || !delivery.commands[1].ExpiresAt.Equal(delivery.commands[2].ExpiresAt) {
		t.Fatalf("recovery durable enqueue was not same-shape/idempotent: %+v", delivery.commands)
	}
	invalidCommand := identity.CompleteEndUserRecoveryCommand{Scope: scope, Continuation: continuation, Proof: "incorrect-proof-value", NewCredential: "replacement service password", TraceID: "trace.recovery.invalid", IdempotencyKey: "recovery-invalid-key-01"}
	if err := service.CompleteRecovery(context.Background(), invalidCommand); !errors.Is(err, identity.ErrRecoveryProofInvalid) {
		t.Fatalf("first invalid recovery error = %v", err)
	}
	if err := service.CompleteRecovery(context.Background(), invalidCommand); !errors.Is(err, identity.ErrRecoveryProofInvalid) {
		t.Fatalf("replayed invalid recovery error = %v", err)
	}
	changedInvalidCommand := invalidCommand
	changedInvalidCommand.Proof = "different-invalid-proof"
	if err := service.CompleteRecovery(context.Background(), changedInvalidCommand); !errors.Is(err, identity.ErrEndUserVersionConflict) {
		t.Fatalf("changed invalid recovery request error = %v", err)
	}
	var failedState, failedReason string
	if err := database.Pool.QueryRow(context.Background(), `SELECT state,response_document->>'reason' FROM identity.end_user_idempotency_records WHERE operation='recovery_complete' AND state='failed'`).Scan(&failedState, &failedReason); err != nil || failedState != "failed" || failedReason != "invalid_recovery_proof" {
		t.Fatalf("invalid recovery terminal state=%q reason=%q err=%v", failedState, failedReason, err)
	}
	completeCommand := identity.CompleteEndUserRecoveryCommand{Scope: scope, Continuation: continuation, Proof: delivery.proof, NewCredential: "replacement service password", TraceID: "trace.recovery.complete", IdempotencyKey: "recovery-complete-key-1"}
	if err := service.CompleteRecovery(context.Background(), completeCommand); err != nil {
		t.Fatalf("CompleteRecovery() error = %v", err)
	}
	if err := service.CompleteRecovery(context.Background(), completeCommand); err != nil {
		t.Fatalf("idempotent CompleteRecovery() error = %v", err)
	}
	if _, err := service.CurrentSession(context.Background(), registered.AccessToken, scope); !errors.Is(err, identity.ErrEndUserSessionRevoked) {
		t.Fatalf("old session after recovery error = %v", err)
	}
	var identifierDigest []byte
	if err := database.Pool.QueryRow(context.Background(), `SELECT normalized_digest FROM identity.user_identifiers WHERE user_id=$1`, registered.Session.UserID).Scan(&identifierDigest); err != nil {
		t.Fatal(err)
	}
	scopeID := "p16:product.recovery|a20:application.recovery|t1:-|e4:test"
	if _, err := database.Pool.Exec(context.Background(), `INSERT INTO identity.end_user_login_failures(scope_id,identifier_digest,source_digest,failure_count,window_started_at,last_failed_at) VALUES($1,$2,$3,1,$4,$4)`, scopeID, identifierDigest, protectedDigest("source-before-login"), now); err != nil {
		t.Fatal(err)
	}
	loggedIn, err := service.Login(context.Background(), identity.EndUserLoginCommand{Scope: scope, Identifier: "recover-service@example.com", Credential: "replacement service password", Source: "loopback", TraceID: "trace.recovery.login"})
	if err != nil || loggedIn.AccessToken == "" {
		t.Fatalf("Login with recovered password = %+v, %v", loggedIn, err)
	}
	var failures int
	if err := database.Pool.QueryRow(context.Background(), `SELECT count(*) FROM identity.end_user_login_failures WHERE scope_id=$1 AND identifier_digest=$2`, scopeID, identifierDigest).Scan(&failures); err != nil || failures != 0 {
		t.Fatalf("successful login did not atomically clear failures: count=%d err=%v", failures, err)
	}
}

func newEndUserService(t *testing.T, repository identity.EndUserRepository, proof identity.RegistrationProofVerifier, delivery identity.RecoveryDeliveryPort, now func() time.Time) *identity.EndUserService {
	t.Helper()
	hasher, err := securevalue.NewHasher(strings.Repeat("end-user-test-pepper-", 2))
	if err != nil {
		t.Fatal(err)
	}
	service, err := identity.NewEndUserService(repository, identity.StrictIdentifierNormalizer{}, identity.Bcrypt{Cost: bcrypt.DefaultCost}, hasher, proof, delivery, identity.EndUserPolicy{AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour, RefreshAbsoluteTTL: 24 * time.Hour, RefreshRecoveryWindow: 2 * time.Minute, RecoveryTTL: 10 * time.Minute, RecoveryMaxAttempts: 3, LoginWindow: 5 * time.Minute, LoginMaximumAttempts: 3, LoginBlockDuration: 5 * time.Minute, RecentAuthTTL: 10 * time.Minute}, now)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
