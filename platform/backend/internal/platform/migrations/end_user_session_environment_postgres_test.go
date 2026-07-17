package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/platform/migrations"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestEndUserSessionEnvironmentIsBoundAndRollbackProtected(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := database.Pool.Exec(ctx, `INSERT INTO identity.users(user_id,display_name,account_status,created_at,updated_at) VALUES('environment_guard_user','Environment Guard','active',$1,$1)`, now); err != nil {
		t.Fatal(err)
	}
	insert := func(sessionID, environment string) error {
		_, err := database.Pool.Exec(ctx, `INSERT INTO identity.end_user_sessions(
			session_id,user_id,product_id,application_id,tenant_id,environment,token_family_id,authentication_method,
			auth_time,created_at,last_seen_at,access_expires_at,refresh_expires_at,absolute_expires_at
		) VALUES($1,'environment_guard_user','product_guard','application_guard','tenant_guard',$2,$3,'password',$4,$4,$4,$5,$6,$7)`,
			sessionID, environment, "family_"+sessionID, now, now.Add(time.Minute), now.Add(2*time.Minute), now.Add(3*time.Minute))
		return err
	}
	if err := insert("environment_guard_invalid", "staging"); err == nil || !strings.Contains(err.Error(), "end_user_sessions_environment_valid") {
		t.Fatalf("invalid environment error=%v", err)
	}
	if err := insert("environment_guard_valid", "test"); err != nil {
		t.Fatal(err)
	}
	riskDigest := make([]byte, 32)
	if _, err := database.Pool.Exec(ctx, `INSERT INTO identity.hosted_auth_proofs(
		proof_id,user_id,product_id,application_id,tenant_id,environment,authentication_method,risk_summary_digest,created_at,expires_at
	) VALUES('hproof_environment_guard_abcdefghijklmnop','environment_guard_user','product_guard','application_guard','tenant_guard','staging','password',$1,$2,$3)`, riskDigest, now, now.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "hosted_auth_proofs_environment_valid") {
		t.Fatalf("invalid proof environment error=%v", err)
	}
	var environment string
	if err := database.Pool.QueryRow(ctx, `SELECT environment FROM identity.end_user_sessions WHERE session_id='environment_guard_valid'`).Scan(&environment); err != nil || environment != "test" {
		t.Fatalf("persisted environment=%q error=%v", environment, err)
	}

	err := migrations.ApplyDownAll(ctx, database.Pool, database.MigrationPath)
	if err == nil || !strings.Contains(err.Error(), "migration 000019 rollback refused") {
		t.Fatalf("ApplyDownAll() error=%v, want environment rollback refusal", err)
	}
}
