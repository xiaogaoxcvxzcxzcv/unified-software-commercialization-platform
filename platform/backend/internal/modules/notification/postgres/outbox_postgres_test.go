package postgres

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/notification"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

type failingOutboxSink struct{}

func (failingOutboxSink) Publish(context.Context, notification.ClaimedSecurityOutboxEvent) error {
	return errors.New("sink failed with private@example.com pg-proof-private")
}

func TestSecurityOutboxPostgresConcurrentClaimCrashRecoveryRetryPublishAndDead(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	var databaseNow time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	repository := New(database.Pool)
	protector, _ := notification.NewAEADSecurityPayloadProtector("outbox-pg-protector", pgSecretResolver{values: map[string][]byte{"outbox-pg-protector": bytes.Repeat([]byte{0x31}, 32)}})
	digester, _ := notification.NewHMACSecurityDigester("outbox-pg-digest", pgSecretResolver{values: map[string][]byte{"outbox-pg-digest": bytes.Repeat([]byte{0x32}, 32)}})
	service, _ := notification.NewService(repository, protector, digester, pgProviderRegistry{enabled: true, idempotent: true}, func() time.Time { return databaseNow })

	enqueue := func(id string) {
		t.Helper()
		command := notification.SecurityDeliveryCommand{DeliveryID: id, Purpose: "account_security", ProductID: "product-pg", ApplicationID: "application-pg", ProviderRef: "provider-pg", DestinationType: "email", Destination: "private@example.com", Proof: "pg-proof-private", ExpiresAt: databaseNow.Add(time.Hour), TraceID: "trace-" + id}
		if err := service.EnqueueSecurityDelivery(ctx, command); err != nil {
			t.Fatal(err)
		}
	}

	enqueue("delivery-outbox-exclusive")
	start := make(chan struct{})
	results := make(chan error, 2)
	claims := make(chan notification.ClaimedSecurityOutboxEvent, 2)
	var wait sync.WaitGroup
	for _, worker := range []string{"outbox-worker-a", "outbox-worker-b"} {
		wait.Add(1)
		go func(workerID string) {
			defer wait.Done()
			<-start
			event, err := repository.ClaimSecurityOutbox(ctx, workerID, 500*time.Millisecond)
			claims <- event
			results <- err
		}(worker)
	}
	close(start)
	wait.Wait()
	close(results)
	close(claims)
	var claimed notification.ClaimedSecurityOutboxEvent
	var successes, notFound int
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, notification.ErrNotFound) {
			notFound++
		} else {
			t.Fatalf("claim error=%v", err)
		}
	}
	for event := range claims {
		if event.EventID != "" {
			claimed = event
		}
	}
	if successes != 1 || notFound != 1 || claimed.AttemptCount != 1 {
		t.Fatalf("successes=%d notFound=%d claim=%+v", successes, notFound, claimed)
	}

	time.Sleep(550 * time.Millisecond)
	recovered, err := repository.ClaimSecurityOutbox(ctx, "outbox-recovery", time.Minute)
	if err != nil || recovered.EventID != claimed.EventID || recovered.AttemptCount != 2 || recovered.LeaseToken == claimed.LeaseToken {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	if err := repository.MarkSecurityOutboxPublished(ctx, claimed.EventID, claimed.LeaseToken); !errors.Is(err, notification.ErrOutboxLeaseLost) {
		t.Fatalf("stale lease publish error=%v", err)
	}
	if err := repository.MarkSecurityOutboxRetry(ctx, recovered.EventID, recovered.LeaseToken, 0, "NOTIFICATION_OUTBOX_SINK_UNAVAILABLE"); err != nil {
		t.Fatal(err)
	}
	var retryOwner *string
	var retryError string
	if err := database.Pool.QueryRow(ctx, `SELECT lease_owner,last_error_code FROM notification.outbox_events WHERE event_id=$1`, recovered.EventID).Scan(&retryOwner, &retryError); err != nil || retryOwner != nil || retryError != "NOTIFICATION_OUTBOX_SINK_UNAVAILABLE" {
		t.Fatalf("retry owner=%v code=%s err=%v", retryOwner, retryError, err)
	}
	retried, err := repository.ClaimSecurityOutbox(ctx, "outbox-retry", time.Minute)
	if err != nil || retried.AttemptCount != 3 {
		t.Fatalf("retried=%+v err=%v", retried, err)
	}
	if err := repository.MarkSecurityOutboxPublished(ctx, retried.EventID, retried.LeaseToken); err != nil {
		t.Fatal(err)
	}
	var publishedAt, publishedLeaseExpires *time.Time
	var publishedOwner, publishedError *string
	if err := database.Pool.QueryRow(ctx, `SELECT published_at,lease_owner,lease_expires_at,last_error_code FROM notification.outbox_events WHERE event_id=$1`, retried.EventID).Scan(&publishedAt, &publishedOwner, &publishedLeaseExpires, &publishedError); err != nil || publishedAt == nil || publishedOwner != nil || publishedLeaseExpires != nil || publishedError != nil {
		t.Fatalf("published_at=%v owner=%v lease=%v code=%v err=%v", publishedAt, publishedOwner, publishedLeaseExpires, publishedError, err)
	}

	enqueue("delivery-outbox-dead")
	for attempt := 1; attempt <= 4; attempt++ {
		seed, claimErr := repository.ClaimSecurityOutbox(ctx, "outbox-dead-seed", time.Minute)
		if claimErr != nil || seed.AttemptCount != attempt {
			t.Fatalf("dead seed attempt=%d claim=%+v err=%v", attempt, seed, claimErr)
		}
		if err := repository.MarkSecurityOutboxRetry(ctx, seed.EventID, seed.LeaseToken, 0, "NOTIFICATION_OUTBOX_SINK_UNAVAILABLE"); err != nil {
			t.Fatal(err)
		}
	}
	dispatcher, _ := notification.NewOutboxDispatcher(repository, failingOutboxSink{}, "outbox-dead-worker")
	if !dispatcher.DispatchOne(ctx) {
		t.Fatal("dispatcher did not finalize exhausted event")
	}
	var dead bool
	var errorCode, payload string
	var deadOwner *string
	var deadLease *time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT dead,last_error_code,payload::text,lease_owner,lease_expires_at FROM notification.outbox_events WHERE delivery_id='delivery-outbox-dead'`).Scan(&dead, &errorCode, &payload, &deadOwner, &deadLease); err != nil {
		t.Fatal(err)
	}
	if !dead || errorCode != "NOTIFICATION_OUTBOX_SINK_UNAVAILABLE" || deadOwner != nil || deadLease != nil {
		t.Fatalf("dead=%v code=%s owner=%v lease=%v", dead, errorCode, deadOwner, deadLease)
	}
	if strings.Contains(payload, "private@example.com") || strings.Contains(payload, "pg-proof-private") {
		t.Fatalf("outbox leaked plaintext: %s", payload)
	}
	if _, err := repository.ClaimSecurityOutbox(ctx, "outbox-after-dead", time.Minute); !errors.Is(err, notification.ErrNotFound) {
		t.Fatalf("dead/published events remained claimable: %v", err)
	}

	enqueue("delivery-outbox-crashed-last")
	for attempt := 1; attempt <= 4; attempt++ {
		seed, claimErr := repository.ClaimSecurityOutbox(ctx, "outbox-crash-seed", time.Minute)
		if claimErr != nil || seed.AttemptCount != attempt {
			t.Fatalf("crash seed attempt=%d claim=%+v err=%v", attempt, seed, claimErr)
		}
		if err := repository.MarkSecurityOutboxRetry(ctx, seed.EventID, seed.LeaseToken, 0, "NOTIFICATION_OUTBOX_SINK_UNAVAILABLE"); err != nil {
			t.Fatal(err)
		}
	}
	crashed, err := repository.ClaimSecurityOutbox(ctx, "outbox-crashed", 30*time.Millisecond)
	if err != nil || crashed.AttemptCount != 5 {
		t.Fatalf("last crash claim=%+v err=%v", crashed, err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := repository.ClaimSecurityOutbox(ctx, "outbox-no-sixth-attempt", time.Minute); !errors.Is(err, notification.ErrNotFound) {
		t.Fatalf("sixth outbox claim error=%v", err)
	}
	var crashAttempts int
	if err := database.Pool.QueryRow(ctx, `SELECT dead,attempt_count,last_error_code,lease_owner,lease_expires_at FROM notification.outbox_events WHERE delivery_id='delivery-outbox-crashed-last'`).Scan(&dead, &crashAttempts, &errorCode, &deadOwner, &deadLease); err != nil {
		t.Fatal(err)
	}
	if !dead || crashAttempts != 5 || errorCode != "NOTIFICATION_OUTBOX_WORKER_LEASE_EXPIRED" || deadOwner != nil || deadLease != nil {
		t.Fatalf("crash dead=%v attempts=%d code=%s owner=%v lease=%v", dead, crashAttempts, errorCode, deadOwner, deadLease)
	}
}
