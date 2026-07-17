package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/platform/migrations"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestHostedAuthenticationLeaseShapeAndRollbackProtection(t *testing.T) {
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
		state_protector_key_ref,state_ciphertext,state_digest,nonce_digest,pkce_challenge_digest,pkce_method,status,trace_id,created_at,expires_at,opened_at
	) VALUES('hint_auth_lease_guard_abcdefghijklmnop','hosted.auth','product_guard','application_guard','test','web','client',
		'client_session_guard','login.complete','https://client.example.test/callback',1,'hosted.state.guard',$1,$2,$3,$4,'S256','opened','trace_guard',$5,$6,$5)`,
		digest(1), digest(2), digest(3), digest(4), now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='authenticating',version=version+1 WHERE interaction_id='hint_auth_lease_guard_abcdefghijklmnop'`); err == nil || !strings.Contains(err.Error(), "hosted_interactions_authentication_lease_shape") {
		t.Fatalf("authentication without lease error=%v", err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='authenticating',version=version+1,authentication_lease_digest=$2,authentication_started_at=$3,authentication_lease_expires_at=$4 WHERE interaction_id=$1`, "hint_auth_lease_guard_abcdefghijklmnop", digest(5), now, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='completed',version=version+1 WHERE interaction_id='hint_auth_lease_guard_abcdefghijklmnop'`); err == nil || !strings.Contains(err.Error(), "hosted_interactions_authentication_lease_shape") {
		t.Fatalf("terminal transition retaining lease error=%v", err)
	}

	err := migrations.ApplyDownAll(ctx, database.Pool, database.MigrationPath)
	if err == nil || !strings.Contains(err.Error(), "migration 000020 rollback refused") {
		t.Fatalf("ApplyDownAll() error=%v, want active lease rollback refusal", err)
	}
}
