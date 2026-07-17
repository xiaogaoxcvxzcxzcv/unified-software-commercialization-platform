package postgres

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/notification"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

type pgSecretResolver struct {
	values map[string][]byte
}

func (r pgSecretResolver) ResolveSecret(_ context.Context, ref string) ([]byte, error) {
	value, ok := r.values[ref]
	if !ok {
		return nil, notification.ErrProviderUnavailable
	}
	return append([]byte(nil), value...), nil
}

type pgProvider struct{}

func (pgProvider) DeliverSecurity(_ context.Context, request notification.SecurityProviderRequest) (notification.SecurityProviderResult, error) {
	if request.Destination != "pg-person@example.com" || request.Proof != "pg-proof-private" || string(request.Secret) != "pg-provider-secret" {
		return notification.SecurityProviderResult{}, notification.ErrDeliveryRejected
	}
	return notification.SecurityProviderResult{MessageRef: "pg-message-ref-private"}, nil
}

func (pgProvider) RequireSecurityProvider(_ context.Context, ref string) (notification.SecurityProviderCapability, error) {
	if ref != "provider-pg" {
		return notification.SecurityProviderCapability{}, notification.ErrProviderUnavailable
	}
	return notification.SecurityProviderCapability{DeliveryIDIdempotent: true}, nil
}

type pgProviderRegistry struct {
	enabled    bool
	idempotent bool
}

func (r pgProviderRegistry) RequireSecurityProvider(_ context.Context, ref string) (notification.SecurityProviderCapability, error) {
	if !r.enabled || ref != "provider-pg" {
		return notification.SecurityProviderCapability{}, notification.ErrProviderUnavailable
	}
	return notification.SecurityProviderCapability{DeliveryIDIdempotent: r.idempotent}, nil
}

func (r pgProviderRegistry) DeliverSecurity(ctx context.Context, request notification.SecurityProviderRequest) (notification.SecurityProviderResult, error) {
	return pgProvider{}.DeliverSecurity(ctx, request)
}

func TestSecurityDeliveryPostgresLifecycleUsesDatabaseClockAndRedactsReceipts(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	var databaseNow time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	repository := New(database.Pool)
	protector, err := notification.NewAEADSecurityPayloadProtector("pg-protector-key", pgSecretResolver{values: map[string][]byte{"pg-protector-key": bytes.Repeat([]byte{0x51}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	digester, err := notification.NewHMACSecurityDigester("pg-request-digest-key", pgSecretResolver{values: map[string][]byte{"pg-request-digest-key": bytes.Repeat([]byte{0x61}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return databaseNow }
	service, err := notification.NewService(repository, protector, digester, pgProviderRegistry{enabled: true, idempotent: true}, clock)
	if err != nil {
		t.Fatal(err)
	}
	tenant := "tenant-pg"
	command := notification.SecurityDeliveryCommand{DeliveryID: "delivery-pg-0001", Purpose: "password_recovery", ProductID: "product-pg", ApplicationID: "application-pg", TenantID: &tenant, ProviderRef: "provider-pg", DestinationType: "email", Destination: "pg-person@example.com", Proof: "pg-proof-private", ExpiresAt: databaseNow.Add(time.Hour), TraceID: "trace-pg-0001"}

	for name, registry := range map[string]pgProviderRegistry{
		"disabled":       {},
		"not idempotent": {enabled: true},
	} {
		t.Run(name, func(t *testing.T) {
			disabled, newErr := notification.NewService(repository, protector, digester, registry, clock)
			if newErr != nil {
				t.Fatal(newErr)
			}
			if enqueueErr := disabled.EnqueueSecurityDelivery(ctx, command); !errors.Is(enqueueErr, notification.ErrProviderUnavailable) {
				t.Fatalf("provider error=%v", enqueueErr)
			}
		})
	}
	var disabledDeliveries, disabledOutbox int
	if err := database.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM notification.security_deliveries),(SELECT count(*) FROM notification.outbox_events)`).Scan(&disabledDeliveries, &disabledOutbox); err != nil || disabledDeliveries != 0 || disabledOutbox != 0 {
		t.Fatalf("disabled provider deliveries=%d outbox=%d err=%v", disabledDeliveries, disabledOutbox, err)
	}

	if err := service.EnqueueSecurityDelivery(ctx, command); err != nil {
		t.Fatal(err)
	}
	if err := service.EnqueueSecurityDelivery(ctx, command); err != nil {
		t.Fatalf("idempotent enqueue: %v", err)
	}
	var deliveries, outbox int
	var storedText string
	if err := database.Pool.QueryRow(ctx, `SELECT count(*),string_agg(encode(payload_ciphertext,'hex')||payload::text,' ') FROM notification.security_deliveries d JOIN notification.outbox_events o USING(delivery_id)`).Scan(&deliveries, &storedText); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM notification.outbox_events`).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if deliveries != 1 || outbox != 1 || strings.Contains(storedText, command.Destination) || strings.Contains(storedText, command.Proof) || strings.Contains(storedText, "pg-provider-secret") {
		t.Fatalf("deliveries=%d outbox=%d stored=%s", deliveries, outbox, storedText)
	}
	changed := command
	changed.Proof = "changed-private-proof"
	if err := service.EnqueueSecurityDelivery(ctx, changed); !errors.Is(err, notification.ErrIdempotencyConflict) {
		t.Fatalf("conflict error=%v", err)
	}

	claimed, err := repository.ClaimSecurityDelivery(ctx, "worker-one", 30*time.Millisecond)
	if err != nil || claimed.AttemptCount != 1 || claimed.LeaseOwner != "worker-one" || claimed.LeaseStartedAt.IsZero() {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	if _, err := repository.ClaimSecurityDelivery(ctx, "worker-two", time.Minute); !errors.Is(err, notification.ErrNotFound) {
		t.Fatalf("active lease claim error=%v", err)
	}
	time.Sleep(50 * time.Millisecond)
	reclaimed, err := repository.ClaimSecurityDelivery(ctx, "worker-two", 30*time.Millisecond)
	if err != nil || reclaimed.AttemptCount != 2 || reclaimed.LeaseOwner != "worker-two" {
		t.Fatalf("reclaimed=%+v err=%v", reclaimed, err)
	}
	time.Sleep(50 * time.Millisecond)
	worker, err := notification.NewWorker(repository, protector, digester, pgProvider{}, pgSecretResolver{values: map[string][]byte{"provider-pg": []byte("pg-provider-secret")}}, "worker-three", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if !worker.RunOne(ctx) {
		t.Fatal("worker did not complete recovered delivery")
	}
	var status string
	var attemptCount, attemptRows, recoveredAttemptRows int
	if err := database.Pool.QueryRow(ctx, `SELECT status,attempt_count FROM notification.security_deliveries WHERE delivery_id=$1`, command.DeliveryID).Scan(&status, &attemptCount); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM notification.security_delivery_attempts WHERE delivery_id=$1`, command.DeliveryID).Scan(&attemptRows); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM notification.security_delivery_attempts WHERE delivery_id=$1 AND outcome='retryable_failure' AND error_code='NOTIFICATION_SECURITY_WORKER_LEASE_EXPIRED'`, command.DeliveryID).Scan(&recoveredAttemptRows); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" || attemptCount != 3 || attemptRows != 3 || recoveredAttemptRows != 2 {
		t.Fatalf("status=%s attempts=%d rows=%d recovered=%d", status, attemptCount, attemptRows, recoveredAttemptRows)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE notification.security_delivery_attempts SET outcome='terminal_failure' WHERE delivery_id=$1`, command.DeliveryID); err == nil || !strings.Contains(err.Error(), "security delivery attempts are immutable") {
		t.Fatalf("attempt update error=%v", err)
	}
	var digest []byte
	if err := database.Pool.QueryRow(ctx, `SELECT provider_message_digest FROM notification.security_delivery_attempts WHERE delivery_id=$1 AND provider_message_digest IS NOT NULL`, command.DeliveryID).Scan(&digest); err != nil || len(digest) != 32 || strings.Contains(string(digest), "pg-message-ref-private") {
		t.Fatalf("receipt digest length=%d err=%v", len(digest), err)
	}
}

func TestSecurityDeliveryPostgresRejectsExpiredCompletionAndFinalizesCrashedLastAttempt(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	var databaseNow time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	repository := New(database.Pool)
	protector, _ := notification.NewAEADSecurityPayloadProtector("pg-protector-key", pgSecretResolver{values: map[string][]byte{"pg-protector-key": bytes.Repeat([]byte{0x71}, 32)}})
	digester, _ := notification.NewHMACSecurityDigester("pg-request-digest-key", pgSecretResolver{values: map[string][]byte{"pg-request-digest-key": bytes.Repeat([]byte{0x72}, 32)}})
	service, _ := notification.NewService(repository, protector, digester, pgProviderRegistry{enabled: true, idempotent: true}, func() time.Time { return databaseNow })

	enqueue := func(id string) {
		t.Helper()
		command := notification.SecurityDeliveryCommand{DeliveryID: id, Purpose: "account_security", ProductID: "product-pg", ApplicationID: "application-pg", ProviderRef: "provider-pg", DestinationType: "email", Destination: "pg-person@example.com", Proof: "pg-proof-private", ExpiresAt: databaseNow.Add(time.Hour), TraceID: "trace-" + id}
		if err := service.EnqueueSecurityDelivery(ctx, command); err != nil {
			t.Fatal(err)
		}
	}

	enqueue("delivery-pg-expired-completion")
	claimed, err := repository.ClaimSecurityDelivery(ctx, "expired-owner", 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	attempt := notification.SecurityDeliveryAttempt{AttemptID: "nat_expired_owner", DeliveryID: claimed.DeliveryID, AttemptNumber: claimed.AttemptCount, Outcome: "delivered", StartedAt: claimed.LeaseStartedAt}
	complete := notification.CompleteSecurityDeliveryRecord{DeliveryID: claimed.DeliveryID, LeaseOwner: "expired-owner", Attempt: attempt, NextStatus: "delivered"}
	if err := repository.CompleteSecurityDelivery(ctx, complete); !errors.Is(err, notification.ErrLeaseLost) {
		t.Fatalf("expired completion error=%v", err)
	}
	var attemptRows int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM notification.security_delivery_attempts WHERE delivery_id=$1`, claimed.DeliveryID).Scan(&attemptRows); err != nil || attemptRows != 0 {
		t.Fatalf("expired completion attempts=%d err=%v", attemptRows, err)
	}
	recovered, err := repository.ClaimSecurityDelivery(ctx, "recovered-owner", time.Minute)
	if err != nil || recovered.DeliveryID != claimed.DeliveryID || recovered.AttemptCount != 2 {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	code := "NOTIFICATION_SECURITY_DELIVERY_REJECTED"
	recoveredAttempt := notification.SecurityDeliveryAttempt{AttemptID: "nat_recovered_owner", DeliveryID: recovered.DeliveryID, AttemptNumber: recovered.AttemptCount, Outcome: "terminal_failure", ErrorCode: &code, ErrorDigest: bytes.Repeat([]byte{0x73}, 32), StartedAt: recovered.LeaseStartedAt.Add(-time.Hour)}
	if err := repository.CompleteSecurityDelivery(ctx, notification.CompleteSecurityDeliveryRecord{DeliveryID: recovered.DeliveryID, LeaseOwner: "recovered-owner", Attempt: recoveredAttempt, NextStatus: "dead"}); err != nil {
		t.Fatal(err)
	}
	var storedStartedAt time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT started_at FROM notification.security_delivery_attempts WHERE attempt_id=$1`, recoveredAttempt.AttemptID).Scan(&storedStartedAt); err != nil || !storedStartedAt.Equal(recovered.LeaseStartedAt) {
		t.Fatalf("attempt started_at=%v lease_started_at=%v err=%v", storedStartedAt, recovered.LeaseStartedAt, err)
	}

	enqueue("delivery-pg-crashed-last-attempt")
	for attempt := 1; attempt <= 5; attempt++ {
		crashed, claimErr := repository.ClaimSecurityDelivery(ctx, "crashed-worker", 30*time.Millisecond)
		if claimErr != nil || crashed.DeliveryID != "delivery-pg-crashed-last-attempt" || crashed.AttemptCount != attempt {
			t.Fatalf("crashed claim attempt=%d delivery=%+v err=%v", attempt, crashed, claimErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	_, _ = repository.ClaimSecurityDelivery(ctx, "recovery-worker", time.Minute)
	var status, outcome, errorCode string
	if err := database.Pool.QueryRow(ctx, `SELECT d.status,a.outcome,a.error_code FROM notification.security_deliveries d JOIN notification.security_delivery_attempts a USING(delivery_id) WHERE d.delivery_id='delivery-pg-crashed-last-attempt' AND a.attempt_number=d.attempt_count`).Scan(&status, &outcome, &errorCode); err != nil {
		t.Fatal(err)
	}
	if status != "dead" || outcome != "terminal_failure" || errorCode != "NOTIFICATION_SECURITY_WORKER_LEASE_EXPIRED" {
		t.Fatalf("status=%s outcome=%s error=%s", status, outcome, errorCode)
	}
	if _, err := database.Pool.Exec(ctx, `DELETE FROM notification.security_delivery_attempts WHERE delivery_id='delivery-pg-crashed-last-attempt'`); err == nil || !strings.Contains(err.Error(), "security delivery attempts are immutable") {
		t.Fatalf("terminal attempt delete error=%v", err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM notification.security_delivery_attempts WHERE delivery_id='delivery-pg-crashed-last-attempt'`).Scan(&attemptRows); err != nil || attemptRows != 5 {
		t.Fatalf("crashed attempt facts=%d err=%v", attemptRows, err)
	}

	expiringCommand := notification.SecurityDeliveryCommand{DeliveryID: "delivery-pg-database-expired", Purpose: "account_security", ProductID: "product-pg", ApplicationID: "application-pg", ProviderRef: "provider-pg", DestinationType: "email", Destination: "pg-person@example.com", Proof: "pg-proof-private", ExpiresAt: databaseNow.Add(500 * time.Millisecond), TraceID: "trace-delivery-pg-database-expired"}
	if err := service.EnqueueSecurityDelivery(ctx, expiringCommand); err != nil {
		t.Fatal(err)
	}
	expiredClaim, err := repository.ClaimSecurityDelivery(ctx, "expiry-worker", time.Minute)
	if err != nil || expiredClaim.DeliveryID != "delivery-pg-database-expired" {
		t.Fatalf("expired claim=%+v err=%v", expiredClaim, err)
	}
	time.Sleep(550 * time.Millisecond)
	if _, err := repository.ClaimSecurityDelivery(ctx, "must-not-claim-expired", time.Minute); !errors.Is(err, notification.ErrNotFound) {
		t.Fatalf("database-expired claim error=%v", err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT d.status,a.outcome,a.error_code FROM notification.security_deliveries d JOIN notification.security_delivery_attempts a USING(delivery_id) WHERE d.delivery_id=$1`, expiredClaim.DeliveryID).Scan(&status, &outcome, &errorCode); err != nil {
		t.Fatal(err)
	}
	if status != "dead" || outcome != "terminal_failure" || errorCode != "NOTIFICATION_SECURITY_DELIVERY_EXPIRED" {
		t.Fatalf("expired status=%s outcome=%s code=%s", status, outcome, errorCode)
	}
}
