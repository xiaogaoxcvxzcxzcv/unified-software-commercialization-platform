package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestHostedInteractionMigrationProtectsFactsAndTerminalStates(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	digest := func(marker byte) []byte {
		value := make([]byte, 32)
		value[0] = marker
		return value
	}

	insertInteraction := func(id string, marker byte) {
		t.Helper()
		_, err := database.Pool.Exec(ctx, `INSERT INTO hosted_interaction.interactions(
			interaction_id,route_id,product_id,application_id,environment,channel,initiator_kind,
			initiator_client_session_id,return_target_code,return_target_uri,return_target_policy_version,
			state_ciphertext,state_digest,nonce_digest,pkce_challenge_digest,pkce_method,status,trace_id,created_at,expires_at
		) VALUES($1,'hosted.auth','product_guard','application_guard','test','desktop','client',
			'client_session_guard','login.complete','https://client.example.test/callback',1,$2,$3,$4,$5,'S256','created','trace_guard',$6,$7)`,
			id, []byte{marker, marker + 1}, digest(marker), digest(marker+20), digest(marker+40), now, now.Add(10*time.Minute))
		if err != nil {
			t.Fatalf("insert interaction %s: %v", id, err)
		}
	}

	insertInteraction("hint_guard_fact_abcdefghijklmnop", 1)
	if _, err := database.Pool.Exec(ctx, `UPDATE hosted_interaction.interactions SET locale='en-US',version=version+1 WHERE interaction_id='hint_guard_fact_abcdefghijklmnop'`); err == nil || !strings.Contains(err.Error(), "security facts are immutable") {
		t.Fatalf("interaction fact mutation error=%v", err)
	}
	if _, err := database.Pool.Exec(ctx, `DELETE FROM hosted_interaction.interactions WHERE interaction_id='hint_guard_fact_abcdefghijklmnop'`); err == nil || !strings.Contains(err.Error(), "immutable facts") {
		t.Fatalf("interaction delete error=%v", err)
	}

	insertInteraction("hint_guard_terminal_abcdefghijkl", 2)
	if _, err := database.Pool.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='opened',version=version+1,opened_at=$2 WHERE interaction_id=$1`, "hint_guard_terminal_abcdefghijkl", now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='cancelled',version=version+1,result_kind='cancelled',terminal_at=$2 WHERE interaction_id=$1`, "hint_guard_terminal_abcdefghijkl", now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='opened',version=version+1,result_kind=NULL,terminal_at=NULL WHERE interaction_id='hint_guard_terminal_abcdefghijkl'`); err == nil || !strings.Contains(err.Error(), "terminal state is immutable") {
		t.Fatalf("interaction terminal reversal error=%v", err)
	}

	insertInteraction("hint_guard_browser_abcdefghijklmn", 3)
	if _, err := database.Pool.Exec(ctx, `INSERT INTO hosted_interaction.browser_sessions(browser_session_id,interaction_id,token_digest,status,created_at,last_seen_at,expires_at) VALUES('hbs_guard_abcdefghijklmnopqrstuvwx','hint_guard_browser_abcdefghijklmn',$1,'active',$2,$2,$3)`, digest(90), now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE hosted_interaction.browser_sessions SET status='revoked',revoked_at=$1,revoke_reason='rotated' WHERE browser_session_id='hbs_guard_abcdefghijklmnopqrstuvwx'`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `DELETE FROM hosted_interaction.browser_sessions WHERE browser_session_id='hbs_guard_abcdefghijklmnopqrstuvwx'`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("browser session delete error=%v", err)
	}

	if _, err := database.Pool.Exec(ctx, `INSERT INTO hosted_interaction.idempotency_records(operation,actor_digest,key_digest,request_digest,interaction_id,response_document,created_at) VALUES('create',$1,$2,$3,'hint_guard_fact_abcdefghijklmnop','{}'::jsonb,$4)`, digest(100), digest(101), digest(102), now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `DELETE FROM hosted_interaction.idempotency_records WHERE operation='create'`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("idempotency delete error=%v", err)
	}

	if _, err := database.Pool.Exec(ctx, `INSERT INTO identity.users(user_id,display_name,account_status,created_at,updated_at) VALUES('hosted_guard_user','Hosted Guard','active',$1,$1)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `INSERT INTO identity.hosted_auth_proofs(proof_id,user_id,product_id,application_id,authentication_method,risk_summary_digest,created_at,expires_at) VALUES('hproof_guard_abcdefghijklmnopqrst','hosted_guard_user','product_guard','application_guard','password',$1,$2,$3)`, digest(110), now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE identity.hosted_auth_proofs SET consumed_by_grant_id='hgrant_guard_abcdefghijklmnopqrstu',consumed_at=$1 WHERE proof_id='hproof_guard_abcdefghijklmnopqrst'`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE identity.hosted_auth_proofs SET consumed_by_grant_id=NULL,consumed_at=NULL WHERE proof_id='hproof_guard_abcdefghijklmnopqrst'`); err == nil || !strings.Contains(err.Error(), "exactly once") {
		t.Fatalf("hosted proof reversal error=%v", err)
	}
}
