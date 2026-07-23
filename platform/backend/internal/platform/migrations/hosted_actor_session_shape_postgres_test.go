package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/platform/migrations"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestHostedActorSessionShapeAndRollbackProtection(t *testing.T) {
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
		initiator_user_id,initiator_user_session_id,return_target_code,return_target_uri,return_target_policy_version,
		state_protector_key_ref,state_ciphertext,state_digest,status,trace_id,created_at,expires_at
	) VALUES('hint_actor_user_guard_abcdefghijklmnop','hosted.account','product_guard','application_guard','test','web','user',
		'user_guard','user_session_guard','account.complete','https://client.example.test/account',1,'hosted.state.guard',$1,$2,'created','trace_guard',$3,$4)`,
		digest(1), digest(2), now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE hosted_interaction.interactions SET initiator_user_session_id='other_session',version=version+1 WHERE interaction_id='hint_actor_user_guard_abcdefghijklmnop'`); err == nil || !strings.Contains(err.Error(), "scope and security facts are immutable") {
		t.Fatalf("user session mutation error=%v", err)
	}
	if _, err := database.Pool.Exec(ctx, `INSERT INTO hosted_interaction.interactions(
		interaction_id,route_id,product_id,application_id,environment,channel,initiator_kind,
		return_target_code,return_target_uri,return_target_policy_version,state_protector_key_ref,state_ciphertext,
		state_digest,nonce_digest,pkce_challenge_digest,pkce_method,status,trace_id,created_at,expires_at
	) VALUES('hint_actor_client_guard_abcdefghijkl','hosted.auth','product_guard','application_guard','test','web','client',
		'login.complete','https://client.example.test/login',1,'hosted.state.guard',$1,$2,$3,$4,'S256','created','trace_guard',$5,$6)`,
		digest(3), digest(4), digest(5), digest(6), now, now.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "hosted_interactions_actor_session_shape") {
		t.Fatalf("client without session error=%v", err)
	}

	err := migrations.ApplyDownAll(ctx, database.Pool, database.MigrationPath)
	if err == nil || !strings.Contains(err.Error(), "migration 000022 rollback refused") {
		t.Fatalf("ApplyDownAll() error=%v, want user actor rollback refusal", err)
	}
}
