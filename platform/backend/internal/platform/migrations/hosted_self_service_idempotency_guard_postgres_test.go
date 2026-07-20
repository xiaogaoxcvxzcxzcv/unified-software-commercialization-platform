package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/platform/migrations"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestHostedSelfServiceIdempotencyRollbackProtection(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	digest := func(marker byte) []byte {
		value := make([]byte, 32)
		value[0] = marker
		return value
	}
	if _, err := database.Pool.Exec(ctx, `INSERT INTO hosted_interaction.interactions(
		interaction_id,route_id,product_id,application_id,environment,channel,initiator_kind,
		initiator_client_session_id,return_target_code,return_target_uri,return_target_policy_version,
		state_protector_key_ref,state_ciphertext,state_digest,nonce_digest,pkce_challenge_digest,pkce_method,
		status,trace_id,created_at,expires_at
	) VALUES('hint_self_service_rollback_abcdefghijkl','hosted.auth','product_guard','application_guard','test','web','client',
		'client_session_guard','login.complete','https://client.example.test/callback',1,
		'hosted.state.guard',$1,$2,$3,$4,'S256','opened','trace_guard',$5,$6)`,
		append(make([]byte, 12), make([]byte, 32)...), digest(1), digest(2), digest(3), now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `INSERT INTO hosted_interaction.idempotency_records(
		operation,actor_digest,key_digest,request_digest,interaction_id,response_document,created_at
	) VALUES('auth_flow_reset',$1,$2,$3,'hint_self_service_rollback_abcdefghijkl','{}'::jsonb,$4)`,
		digest(4), digest(5), digest(6), now); err != nil {
		t.Fatal(err)
	}

	err := migrations.ApplyDownAll(ctx, database.Pool, database.MigrationPath)
	if err == nil || !strings.Contains(err.Error(), "rollback migration 000025_hosted_self_service_flow") || !strings.Contains(err.Error(), "refusing to narrow hosted idempotency operations") {
		t.Fatalf("ApplyDownAll() error=%v, want migration 000025 rollback refusal", err)
	}
	var retained bool
	if err := database.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM platform_meta.schema_migrations WHERE version=25
	)`).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if !retained {
		t.Fatal("failed rollback removed migration 000025 history")
	}
}
