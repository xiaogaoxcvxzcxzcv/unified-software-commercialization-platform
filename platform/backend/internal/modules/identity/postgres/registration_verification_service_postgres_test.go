package postgres_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"platform.local/capability-platform/backend/internal/modules/identity"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

type controlledSecurityDelivery struct {
	err      error
	commands []identity.SecurityDeliveryCommand
}

type failRegistrationCreateOnce struct {
	identity.EndUserRepository
	err    error
	failed bool
}

func (r *failRegistrationCreateOnce) CreateEndUserWithSessionIdempotent(ctx context.Context, registration identity.EndUserRegistration, session identity.NewEndUserSession, record identity.EndUserIdempotency) (identity.EndUserRegistrationResponse, bool, error) {
	if !r.failed {
		r.failed = true
		return identity.EndUserRegistrationResponse{}, false, r.err
	}
	return r.EndUserRepository.CreateEndUserWithSessionIdempotent(ctx, registration, session, record)
}

func (d *controlledSecurityDelivery) EnqueueSecurity(_ context.Context, command identity.SecurityDeliveryCommand) error {
	d.commands = append(d.commands, command)
	return d.err
}

func TestRegistrationVerificationDeliveryActivationAndSingleUse(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 18, 6, 0, 0, 0, time.UTC)
	clock := now
	hasher, err := securevalue.NewHasher(strings.Repeat("registration-verification-pepper-", 2))
	if err != nil {
		t.Fatal(err)
	}
	deliveryFailure := errors.New("security delivery unavailable")
	delivery := &controlledSecurityDelivery{err: deliveryFailure}
	service, err := identity.NewRegistrationVerificationService(repository, identity.StrictIdentifierNormalizer{}, hasher, delivery, identity.RegistrationVerificationPolicy{TTL: 5 * time.Minute, MaxAttempts: 3}, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	scope := identity.EndUserSessionScope{ProductID: "product.verify", ApplicationID: "application.verify", Environment: "test"}
	command := identity.StartRegistrationVerificationCommand{Scope: scope, Identifier: "verify@example.com", IdempotencyKey: "registration-verify-key-01", TraceID: "trace.verify"}
	if continuation, err := service.Start(context.Background(), command); !errors.Is(err, deliveryFailure) || continuation != "" || len(delivery.commands) != 1 {
		t.Fatalf("failed Start() continuation=%q err=%v commands=%+v", continuation, err, delivery.commands)
	}
	var status string
	if err := database.Pool.QueryRow(context.Background(), `SELECT delivery_status FROM identity.registration_verification_challenges`).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("failed delivery challenge status=%q err=%v", status, err)
	}
	normalized, err := (identity.StrictIdentifierNormalizer{}).Normalize(identity.IdentifierEmail, command.Identifier)
	if err != nil {
		t.Fatal(err)
	}
	pendingContinuation := "rv_" + base64.RawURLEncoding.EncodeToString(hasher.Digest("registration-verification\x00continuation\x00"+normalized.Value+"\x00"+command.IdempotencyKey))
	consumerKey, consumerRequest := hasher.Digest("consumer-key"), hasher.Digest("consumer-request")
	if err := service.VerifyRegistration(context.Background(), scope, normalized, pendingContinuation, delivery.commands[0].Proof, consumerKey, consumerRequest); !errors.Is(err, identity.ErrRegistrationVerificationInvalid) {
		t.Fatalf("pending proof verification error = %v", err)
	}
	delivery.err = nil
	continuation, err := service.Start(context.Background(), command)
	if err != nil || continuation == "" || len(delivery.commands) != 2 || delivery.commands[0].DeliveryID != delivery.commands[1].DeliveryID || delivery.commands[0].Proof != delivery.commands[1].Proof || delivery.commands[1].Purpose != "registration_verify" || !delivery.commands[1].Scope.Matches(identity.EndUserSession{ProductID: scope.ProductID, ApplicationID: scope.ApplicationID, Environment: scope.Environment}) || delivery.commands[1].TraceID != command.TraceID {
		t.Fatalf("retry Start() continuation=%q err=%v commands=%+v", continuation, err, delivery.commands)
	}
	var challengeEnvironment string
	if err := database.Pool.QueryRow(context.Background(), `SELECT environment FROM identity.registration_verification_challenges WHERE delivery_status='active'`).Scan(&challengeEnvironment); err != nil || challengeEnvironment != scope.Environment {
		t.Fatalf("registration challenge environment=%q err=%v", challengeEnvironment, err)
	}
	if err := service.VerifyRegistration(context.Background(), identity.EndUserSessionScope{ProductID: "other", ApplicationID: scope.ApplicationID, Environment: scope.Environment}, normalized, continuation, delivery.commands[1].Proof, consumerKey, consumerRequest); !errors.Is(err, identity.ErrRegistrationVerificationInvalid) {
		t.Fatalf("cross-scope proof error = %v", err)
	}
	wrongEnvironment := scope
	wrongEnvironment.Environment = "production"
	if err := service.VerifyRegistration(context.Background(), wrongEnvironment, normalized, continuation, delivery.commands[1].Proof, consumerKey, consumerRequest); !errors.Is(err, identity.ErrRegistrationVerificationInvalid) {
		t.Fatalf("cross-environment proof error = %v", err)
	}
	if err := service.VerifyRegistration(context.Background(), scope, normalized, continuation, delivery.commands[1].Proof, consumerKey, consumerRequest); err != nil {
		t.Fatalf("active proof error = %v", err)
	}
	if err := service.VerifyRegistration(context.Background(), scope, normalized, continuation, delivery.commands[1].Proof, consumerKey, consumerRequest); err != nil {
		t.Fatalf("same consumer proof recovery error = %v", err)
	}
	if err := service.VerifyRegistration(context.Background(), scope, normalized, continuation, delivery.commands[1].Proof, hasher.Digest("other-consumer"), consumerRequest); !errors.Is(err, identity.ErrRegistrationVerificationInvalid) {
		t.Fatalf("replayed proof error = %v", err)
	}
	legacyCommand := identity.StartRegistrationVerificationCommand{Scope: scope, Identifier: "legacy-null@example.com", IdempotencyKey: "registration-legacy-null-01", TraceID: "trace.verify.legacy-null"}
	legacyContinuation, err := service.Start(context.Background(), legacyCommand)
	if err != nil {
		t.Fatal(err)
	}
	legacyProof := delivery.commands[len(delivery.commands)-1].Proof
	legacyNormalized, _ := (identity.StrictIdentifierNormalizer{}).Normalize(identity.IdentifierEmail, legacyCommand.Identifier)
	if _, err := database.Pool.Exec(context.Background(), `UPDATE identity.registration_verification_challenges SET environment=NULL WHERE identifier_digest=$1`, hasher.Digest("identifier\x00"+legacyNormalized.Value)); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyRegistration(context.Background(), scope, legacyNormalized, legacyContinuation, legacyProof, hasher.Digest("legacy-consumer"), hasher.Digest("legacy-request")); !errors.Is(err, identity.ErrRegistrationVerificationInvalid) {
		t.Fatalf("legacy null environment proof error = %v", err)
	}
	registerVerification := identity.StartRegistrationVerificationCommand{Scope: scope, Identifier: "new-user@example.com", IdempotencyKey: "registration-verify-key-03", TraceID: "trace.verify.register"}
	registerContinuation, err := service.Start(context.Background(), registerVerification)
	if err != nil {
		t.Fatal(err)
	}
	registerProof := delivery.commands[len(delivery.commands)-1].Proof
	injectedFailure := errors.New("registration database unavailable after proof consumption")
	failingRepository := &failRegistrationCreateOnce{EndUserRepository: repository, err: injectedFailure}
	endUsers, err := identity.NewEndUserService(failingRepository, identity.StrictIdentifierNormalizer{}, identity.Bcrypt{Cost: bcrypt.DefaultCost}, hasher, service, delivery, identity.EndUserPolicy{AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour, RefreshAbsoluteTTL: 24 * time.Hour, RefreshRecoveryWindow: time.Minute, RecoveryTTL: 5 * time.Minute, RecoveryMaxAttempts: 3, LoginWindow: time.Minute, LoginMaximumAttempts: 3, LoginBlockDuration: time.Minute, RecentAuthTTL: 10 * time.Minute}, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	registerCommand := identity.EndUserRegisterCommand{Scope: scope, Identifier: registerVerification.Identifier, Credential: "verified registration password", VerificationContinuationID: registerContinuation, VerificationProof: registerProof, DisplayName: "Verified User", IdempotencyKey: "register-verified-user-01", TraceID: "trace.register.verified"}
	if _, err := endUsers.Register(context.Background(), registerCommand); !errors.Is(err, injectedFailure) {
		t.Fatalf("injected registration failure = %v", err)
	}
	registered, err := endUsers.Register(context.Background(), registerCommand)
	if err != nil || registered.Session.UserID == "" {
		t.Fatalf("verified Register() result=%+v err=%v", registered, err)
	}

	expiredCommand := identity.StartRegistrationVerificationCommand{Scope: scope, Identifier: "expired@example.com", IdempotencyKey: "registration-verify-key-02", TraceID: "trace.verify.expired"}
	expiredContinuation, err := service.Start(context.Background(), expiredCommand)
	if err != nil {
		t.Fatal(err)
	}
	expiredNormalized, _ := (identity.StrictIdentifierNormalizer{}).Normalize(identity.IdentifierEmail, expiredCommand.Identifier)
	expiredProof := delivery.commands[len(delivery.commands)-1].Proof
	clock = now.Add(6 * time.Minute)
	if err := service.VerifyRegistration(context.Background(), scope, expiredNormalized, expiredContinuation, expiredProof, consumerKey, consumerRequest); !errors.Is(err, identity.ErrRegistrationVerificationInvalid) {
		t.Fatalf("expired proof error = %v", err)
	}
}

func TestRecoveryPendingCannotCompleteAndRetryActivates(t *testing.T) {
	database := testpostgres.Open(t)
	repository := identitypostgres.New(database.Pool)
	now := time.Date(2026, 7, 18, 6, 30, 0, 0, time.UTC)
	hasher, err := securevalue.NewHasher(strings.Repeat("recovery-pending-pepper-", 2))
	if err != nil {
		t.Fatal(err)
	}
	deliveryFailure := errors.New("security delivery unavailable")
	delivery := &controlledSecurityDelivery{err: deliveryFailure}
	service, err := identity.NewEndUserService(repository, identity.StrictIdentifierNormalizer{}, identity.Bcrypt{Cost: bcrypt.DefaultCost}, hasher, acceptingRegistrationProof{}, delivery, identity.EndUserPolicy{AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour, RefreshAbsoluteTTL: 24 * time.Hour, RefreshRecoveryWindow: time.Minute, RecoveryTTL: 5 * time.Minute, RecoveryMaxAttempts: 3, LoginWindow: time.Minute, LoginMaximumAttempts: 3, LoginBlockDuration: time.Minute, RecentAuthTTL: 10 * time.Minute}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	scope := identity.EndUserSessionScope{ProductID: "product.recovery-pending", ApplicationID: "application.recovery-pending", Environment: "test"}
	command := identity.StartEndUserRecoveryCommand{Scope: scope, Identifier: "missing@example.com", IdempotencyKey: "recovery-pending-key-01", TraceID: "trace.recovery.pending"}
	if continuation, err := service.StartRecovery(context.Background(), command); !errors.Is(err, deliveryFailure) || continuation != "" || len(delivery.commands) != 1 {
		t.Fatalf("failed recovery Start() continuation=%q err=%v commands=%+v", continuation, err, delivery.commands)
	}
	var challengeID string
	var status string
	if err := database.Pool.QueryRow(context.Background(), `SELECT challenge_id,delivery_status FROM identity.recovery_challenges`).Scan(&challengeID, &status); err != nil || status != "pending" {
		t.Fatalf("pending recovery challenge=%q status=%q err=%v", challengeID, status, err)
	}
	continuation := "ur_" + base64.RawURLEncoding.EncodeToString(hasher.Digest("end-user-refresh\x00recovery-continuation\x00missing@example.com\x00"+command.IdempotencyKey))
	if err := service.CompleteRecovery(context.Background(), identity.CompleteEndUserRecoveryCommand{Scope: scope, Continuation: continuation, Proof: delivery.commands[0].Proof, NewCredential: "replacement password", IdempotencyKey: "recovery-complete-pending", TraceID: "trace.complete.pending"}); !errors.Is(err, identity.ErrRecoveryProofInvalid) {
		t.Fatalf("pending recovery completion error = %v", err)
	}
	delivery.err = nil
	activeContinuation, err := service.StartRecovery(context.Background(), command)
	if err != nil || activeContinuation == "" || len(delivery.commands) != 2 || delivery.commands[0].DeliveryID != delivery.commands[1].DeliveryID || delivery.commands[1].Purpose != "password_recovery" || delivery.commands[1].TraceID != command.TraceID {
		t.Fatalf("recovery retry continuation=%q err=%v commands=%+v", activeContinuation, err, delivery.commands)
	}
	if err := database.Pool.QueryRow(context.Background(), `SELECT delivery_status FROM identity.recovery_challenges WHERE challenge_id=$1`, challengeID).Scan(&status); err != nil || status != "active" {
		t.Fatalf("active recovery status=%q err=%v", status, err)
	}
}
