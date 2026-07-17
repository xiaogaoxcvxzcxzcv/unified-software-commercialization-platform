package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/platform/migrations"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestExternalIdentityNotificationRollbackRefusesDurableState(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	digestA := make([]byte, 32)
	digestB := make([]byte, 32)
	digestA[0] = 1
	digestB[0] = 2

	_, err := database.Pool.Exec(ctx, `
		INSERT INTO identity.external_auth_flows(
			flow_id, product_id, application_id, environment, provider,
			provider_application_ref, mode, return_target_code, return_target_uri,
			return_target_policy_version, state_digest, nonce_digest, status, created_at, expires_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,1,$10,$11,'pending',$12,$13)`,
		"flow_guard", "product_guard", "application_guard", "test", "oidc",
		"provider_application_guard", "redirect", "login", "https://example.test/auth/return",
		digestA, digestB, now, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("insert durable external auth flow: %v", err)
	}

	err = migrations.ApplyDownAll(ctx, database.Pool, database.MigrationPath)
	if err == nil || !strings.Contains(err.Error(), "rollback refused") {
		t.Fatalf("ApplyDownAll() error = %v, want rollback refusal", err)
	}

	var retained bool
	if err := database.Pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM platform_meta.schema_migrations WHERE version=17)`).Scan(&retained); err != nil {
		t.Fatalf("read migration 17 history: %v", err)
	}
	if !retained {
		t.Fatal("failed rollback removed migration 17 history")
	}
}

func TestExternalIdentityNotificationDurableFactsAndTerminalStatesAreImmutable(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	digest := make([]byte, 32)
	nonce := make([]byte, 12)
	ciphertext := make([]byte, 32)

	if _, err := database.Pool.Exec(ctx, `INSERT INTO notification.security_deliveries(
		delivery_id,request_digest,purpose,product_id,application_id,provider_ref,destination_type,
		protector_key_ref,payload_nonce,payload_ciphertext,payload_digest,status,max_attempts,
		next_attempt_at,created_at,expires_at,trace_id
	) VALUES('delivery_guard',$1,'account_security','product_guard','application_guard','provider_guard','email','protector_guard',$2,$3,$1,'pending',3,$4,$4,$5,'trace_guard')`, digest, nonce, ciphertext, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE notification.security_deliveries SET product_id='other_product' WHERE delivery_id='delivery_guard'`); err == nil || !strings.Contains(err.Error(), "facts are immutable") {
		t.Fatalf("security delivery fact mutation error=%v", err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE notification.security_deliveries SET attempt_count=1 WHERE delivery_id='delivery_guard'`); err == nil || !strings.Contains(err.Error(), "invalid pending security delivery transition") {
		t.Fatalf("security delivery claim bypass error=%v", err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE notification.security_deliveries SET status='dead',dead_at=clock_timestamp() WHERE delivery_id='delivery_guard'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE notification.security_deliveries SET status='pending',dead_at=NULL WHERE delivery_id='delivery_guard'`); err == nil || !strings.Contains(err.Error(), "terminal state is immutable") {
		t.Fatalf("security delivery terminal reversal error=%v", err)
	}
	if _, err := database.Pool.Exec(ctx, `DELETE FROM notification.security_deliveries WHERE delivery_id='delivery_guard'`); err == nil || !strings.Contains(err.Error(), "rows are immutable") {
		t.Fatalf("security delivery delete error=%v", err)
	}

	if _, err := database.Pool.Exec(ctx, `INSERT INTO notification.outbox_events(event_id,delivery_id,event_type,payload,occurred_at,next_attempt_at) VALUES('notification_outbox_guard','delivery_guard','notification.security-delivery-requested.v1','{"delivery_id":"delivery_guard"}'::jsonb,$1,$1)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE notification.outbox_events SET payload='{"delivery_id":"other"}'::jsonb WHERE event_id='notification_outbox_guard'`); err == nil || !strings.Contains(err.Error(), "outbox facts are immutable") {
		t.Fatalf("notification outbox fact mutation error=%v", err)
	}
	if _, err := database.Pool.Exec(ctx, `DELETE FROM notification.outbox_events WHERE event_id='notification_outbox_guard'`); err == nil || !strings.Contains(err.Error(), "rows are immutable") {
		t.Fatalf("notification outbox delete error=%v", err)
	}

	if _, err := database.Pool.Exec(ctx, `INSERT INTO product_application.outbox_events(event_id,aggregate_id,event_type,payload,occurred_at,next_attempt_at) VALUES('product_application_outbox_guard','application_guard','product_application.redirects_changed.v1','{"application_id":"application_guard"}'::jsonb,$1,$1)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE product_application.outbox_events SET payload='{"application_id":"other"}'::jsonb WHERE event_id='product_application_outbox_guard'`); err == nil || !strings.Contains(err.Error(), "outbox facts are immutable") {
		t.Fatalf("product application outbox fact mutation error=%v", err)
	}
	if _, err := database.Pool.Exec(ctx, `DELETE FROM product_application.outbox_events WHERE event_id='product_application_outbox_guard'`); err == nil || !strings.Contains(err.Error(), "rows are immutable") {
		t.Fatalf("product application outbox delete error=%v", err)
	}
}
