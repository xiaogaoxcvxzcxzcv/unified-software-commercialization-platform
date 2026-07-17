package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/platform/migrations"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestPostgreSQLMigration16DownRefusesDurableIdentityState(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		seed string
	}{
		{
			name: "idempotency response",
			seed: `INSERT INTO identity.end_user_idempotency_records(
				operation,scope_id,actor_digest,key_digest,request_digest,resource_id,state,response_document,created_at,updated_at
			) VALUES ('profile_update','scope-a',decode(repeat('11',32),'hex'),decode(repeat('12',32),'hex'),
				decode(repeat('13',32),'hex'),'user-a','completed','{"version":2}'::jsonb,$1,$1)`,
		},
		{
			name: "refresh recovery metadata",
			seed: `WITH inserted_user AS (
				INSERT INTO identity.users(user_id,display_name,account_status,created_at,updated_at)
				VALUES ('user-a','User A','active',$1::timestamptz,$1::timestamptz)
				RETURNING user_id
			), inserted_session AS (
				INSERT INTO identity.end_user_sessions(
					session_id,user_id,product_id,application_id,token_family_id,authentication_method,
					auth_time,created_at,last_seen_at,access_expires_at,refresh_expires_at,absolute_expires_at
				) VALUES ('session-a','user-a','product-a','application-a','family-a','password',
					$1::timestamptz,$1::timestamptz,$1::timestamptz,$1::timestamptz + interval '5 minutes',
					$1::timestamptz + interval '1 day',$1::timestamptz + interval '7 days')
				RETURNING session_id
			)
				INSERT INTO identity.end_user_session_tokens(
					token_id,session_id,token_family_id,token_type,generation,token_digest,created_at,expires_at,
					consumed_at,rotation_request_digest,rotation_recovery_expires_at
				) VALUES ('refresh-a','session-a','family-a','refresh',1,decode(repeat('21',32),'hex'),$1::timestamptz,
					$1::timestamptz + interval '1 day',$1::timestamptz,decode(repeat('22',32),'hex'),
					$1::timestamptz + interval '30 seconds')`,
		},
		{
			name: "login throttle",
			seed: `INSERT INTO identity.end_user_login_failures(
				scope_id,identifier_digest,source_digest,failure_count,window_started_at,last_failed_at,blocked_until
			) VALUES ('scope-a',decode(repeat('31',32),'hex'),decode(repeat('32',32),'hex'),3,
				$1::timestamptz,$1::timestamptz,$1::timestamptz + interval '5 minutes')`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := testpostgres.Open(t)
			ctx := context.Background()
			if _, err := database.Pool.Exec(ctx, test.seed, now); err != nil {
				t.Fatalf("seed durable identity state: %v", err)
			}
			err := migrations.ApplyDownAll(ctx, database.Pool, database.MigrationPath)
			if err == nil || !strings.Contains(err.Error(), "durable identity API state exists") {
				t.Fatalf("ApplyDownAll error=%v", err)
			}
			var applied bool
			if err := database.Pool.QueryRow(ctx, `SELECT EXISTS(
				SELECT 1 FROM platform_meta.schema_migrations WHERE version=16
			)`).Scan(&applied); err != nil {
				t.Fatalf("read migration history: %v", err)
			}
			if !applied {
				t.Fatal("failed rollback removed migration 16 history")
			}
		})
	}
}
