package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestPostgreSQLEntitlementMigrationInvariants(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("schema relations and columns exist", func(t *testing.T) {
		for _, relation := range []string{
			"entitlement.features",
			"entitlement.policies",
			"entitlement.grants",
			"entitlement.revisions",
			"entitlement.ledger",
			"entitlement.idempotency_records",
			"entitlement.outbox_events",
		} {
			var found *string
			if err := database.Pool.QueryRow(ctx, `SELECT to_regclass($1)::text`, relation).Scan(&found); err != nil {
				t.Fatalf("query relation %s: %v", relation, err)
			}
			if found == nil || *found != relation {
				t.Errorf("relation %s is missing", relation)
			}
		}

		for relation, columns := range map[string][]string{
			"entitlement.features":            {"feature_id", "product_id", "feature_code", "kind", "status"},
			"entitlement.policies":            {"policy_id", "product_id", "tenant_id", "policy_code", "version", "features", "validity_rule", "stacking_rule", "revoke_scope"},
			"entitlement.grants":              {"grant_id", "product_id", "tenant_id", "user_id", "policy_id", "effect", "source_type", "source_id", "source_effect_id", "idempotency_key", "request_hash"},
			"entitlement.revisions":           {"revision_id", "product_id", "tenant_id", "user_id", "version", "decision_hash", "effective_features", "offline_grace_until"},
			"entitlement.ledger":              {"ledger_id", "operation_type", "operation_id", "before_revision", "after_revision", "audit_id", "trace_id"},
			"entitlement.idempotency_records": {"idempotency_key", "operation", "request_hash", "result_grant_id", "result_revision", "response_document", "state"},
			"entitlement.outbox_events":       {"event_id", "aggregate_id", "event_type", "payload", "published_at", "attempt_count", "dead"},
		} {
			for _, column := range columns {
				var exists bool
				if err := database.Pool.QueryRow(ctx, `
					SELECT EXISTS (
						SELECT 1 FROM information_schema.columns
						WHERE table_schema || '.' || table_name = $1 AND column_name = $2
					)
				`, relation, column).Scan(&exists); err != nil {
					t.Fatalf("query column %s.%s: %v", relation, column, err)
				}
				if !exists {
					t.Errorf("column %s.%s is missing", relation, column)
				}
			}
		}
	})

	t.Run("feature policy grant revision and idempotency constraints are scope bound", func(t *testing.T) {
		insertFeature(t, database, now, "feature-a", "product-a", "pro.member")
		insertFeature(t, database, now, "feature-b", "product-b", "pro.member")
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO entitlement.features(feature_id, product_id, feature_code, kind, display_name, status, created_at)
			VALUES ('feature-duplicate', 'product-a', 'pro.member', 'boolean', 'duplicate', 'active', $1)
		`, now); err == nil {
			t.Fatal("duplicate product feature code was accepted")
		}

		insertPolicy(t, database, now, "policy-a", "product-a", "tenant-a", "pro", 1)
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO entitlement.policies(
				policy_id, product_id, tenant_id, policy_code, version, status, features,
				validity_rule, validity_seconds, stacking_rule, priority, revoke_scope,
				offline_grace_max_seconds, published_at, created_at, updated_at
			) VALUES ('policy-duplicate', 'product-a', 'tenant-a', 'pro', 1, 'active', '[]'::jsonb,
			          'fixed_duration', 3600, 'union_latest_expiry', 0, 'source_only', 0, $1, $1, $1)
		`, now); err == nil {
			t.Fatal("duplicate product tenant policy version was accepted")
		}

		insertGrant(t, database, now, "grant-a", "product-a", "tenant-a", "user-a", "policy-a", "source-a", "effect-a", "idem-a")
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO entitlement.grants(
				grant_id, product_id, tenant_id, user_id, policy_id, policy_version, effect,
				source_type, source_id, source_effect_id, idempotency_key, valid_from, valid_until,
				actor_type, actor_id, reason_code, request_hash, created_at
			) VALUES ('grant-duplicate-source', 'product-a', 'tenant-a', 'user-a', 'policy-a', 1, 'grant',
			          'admin', 'source-a', 'effect-a', 'idem-b', $1, $1 + interval '1 hour',
			          'admin', 'admin-a', 'manual', decode(repeat('a2', 32), 'hex'), $1)
		`, now); err == nil {
			t.Fatal("duplicate source effect was accepted")
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO entitlement.grants(
				grant_id, product_id, tenant_id, user_id, policy_id, policy_version, effect,
				source_type, source_id, source_effect_id, idempotency_key, valid_from, valid_until,
				actor_type, actor_id, reason_code, request_hash, created_at
			) VALUES ('grant-duplicate-idempotency', 'product-a', 'tenant-a', 'user-a', 'policy-a', 1, 'grant',
			          'admin', 'source-b', 'effect-b', 'idem-a', $1, $1 + interval '1 hour',
			          'admin', 'admin-a', 'manual', decode(repeat('a3', 32), 'hex'), $1)
		`, now); err == nil {
			t.Fatal("duplicate idempotency key was accepted")
		}

		insertRevision(t, database, now, "revision-a", "product-a", "tenant-a", "user-a", 1)
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO entitlement.revisions(
				revision_id, product_id, tenant_id, user_id, version, decision_hash,
				effective_features, updated_at
			) VALUES ('revision-duplicate', 'product-a', 'tenant-a', 'user-a', 1,
			          decode(repeat('b2', 32), 'hex'), '{}'::jsonb, $1)
		`, now); err == nil {
			t.Fatal("duplicate product tenant user revision was accepted")
		}

		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO entitlement.idempotency_records(
				product_id, tenant_id, user_id, idempotency_key, operation, request_hash,
				response_document, state, created_at, updated_at
			) VALUES ('product-a', 'tenant-a', 'user-a', 'idem-a', 'grant',
			          decode(repeat('c1', 32), 'hex'), '{}'::jsonb, 'pending', $1, $1)
		`, now); err != nil {
			t.Fatalf("insert idempotency record: %v", err)
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO entitlement.idempotency_records(
				product_id, tenant_id, user_id, idempotency_key, operation, request_hash,
				response_document, state, created_at, updated_at
			) VALUES ('product-a', 'tenant-a', 'user-a', 'idem-a', 'grant',
			          decode(repeat('c2', 32), 'hex'), '{}'::jsonb, 'pending', $1, $1)
		`, now); err == nil {
			t.Fatal("duplicate idempotency scope key was accepted")
		}
	})

	t.Run("append only and monotonic guards reject unsafe mutation", func(t *testing.T) {
		if _, err := database.Pool.Exec(ctx, `
			UPDATE entitlement.grants SET source_id='changed' WHERE grant_id='grant-a'
		`); err == nil {
			t.Fatal("grant update was accepted")
		}
		if _, err := database.Pool.Exec(ctx, `
			DELETE FROM entitlement.grants WHERE grant_id='grant-a'
		`); err == nil {
			t.Fatal("grant delete was accepted")
		}

		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO entitlement.ledger(
				ledger_id, product_id, tenant_id, user_id, operation_type, operation_id,
				source_type, source_id, grant_id, before_revision, after_revision,
				before_decision_hash, after_decision_hash, audit_id, trace_id, created_at
			) VALUES ('ledger-a', 'product-a', 'tenant-a', 'user-a', 'grant', 'operation-a',
			          'admin', 'source-a', 'grant-a', NULL, 1,
			          NULL, decode(repeat('d1', 32), 'hex'), 'audit-a', 'trace-a', $1)
		`, now); err != nil {
			t.Fatalf("insert ledger: %v", err)
		}
		if _, err := database.Pool.Exec(ctx, `UPDATE entitlement.ledger SET trace_id='changed' WHERE ledger_id='ledger-a'`); err == nil {
			t.Fatal("ledger update was accepted")
		}
		if _, err := database.Pool.Exec(ctx, `DELETE FROM entitlement.ledger WHERE ledger_id='ledger-a'`); err == nil {
			t.Fatal("ledger delete was accepted")
		}

		if _, err := database.Pool.Exec(ctx, `
			UPDATE entitlement.revisions
			SET version=1, decision_hash=decode(repeat('b3', 32), 'hex'), updated_at=$1
			WHERE revision_id='revision-a'
		`, now.Add(time.Second)); err == nil {
			t.Fatal("non-increasing revision update was accepted")
		}
		if _, err := database.Pool.Exec(ctx, `
			UPDATE entitlement.revisions
			SET product_id='product-b', version=2, decision_hash=decode(repeat('b4', 32), 'hex'), updated_at=$1
			WHERE revision_id='revision-a'
		`, now.Add(time.Second)); err == nil {
			t.Fatal("revision scope identity update was accepted")
		}
	})

	t.Run("validity digests json outbox and module ownership are guarded", func(t *testing.T) {
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO entitlement.policies(
				policy_id, product_id, tenant_id, policy_code, version, status, features,
				validity_rule, validity_seconds, fixed_valid_until, stacking_rule, priority,
				revoke_scope, offline_grace_max_seconds, created_at, updated_at
			) VALUES ('policy-invalid-validity', 'product-a', 'tenant-a', 'bad', 1, 'draft',
			          '{}'::jsonb, 'fixed_duration', NULL, NULL, 'union_latest_expiry', 0,
			          'source_only', 0, $1, $1)
		`, now); err == nil {
			t.Fatal("invalid policy validity/json shape was accepted")
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO entitlement.grants(
				grant_id, product_id, tenant_id, user_id, policy_id, policy_version, effect,
				source_type, source_id, source_effect_id, idempotency_key, valid_from, valid_until,
				actor_type, actor_id, reason_code, request_hash, created_at
			) VALUES ('grant-short-hash', 'product-a', 'tenant-a', 'user-hash', 'policy-a', 1, 'grant',
			          'admin', 'source-hash', 'effect-hash', 'idem-hash', $1, NULL,
			          'admin', 'admin-a', 'manual', decode('01', 'hex'), $1)
		`, now); err == nil {
			t.Fatal("short grant request hash was accepted")
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO entitlement.outbox_events(
				event_id, aggregate_id, event_type, payload, occurred_at, next_attempt_at
			) VALUES ('outbox-a', 'product-a:tenant-a:user-a', 'entitlement.granted.v1',
			          '{}'::jsonb, $1, $1)
		`, now); err != nil {
			t.Fatalf("insert valid outbox event: %v", err)
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO entitlement.outbox_events(
				event_id, aggregate_id, event_type, payload, occurred_at, next_attempt_at
			) VALUES ('outbox-invalid', 'product-a:tenant-a:user-a', 'entitlement.unknown.v1',
			          '{}'::jsonb, $1, $1)
		`, now); err == nil {
			t.Fatal("unknown entitlement outbox event type was accepted")
		}

		var count int
		if err := database.Pool.QueryRow(ctx, `
			SELECT count(*)
			FROM pg_constraint c
			JOIN pg_class source_table ON source_table.oid = c.conrelid
			JOIN pg_namespace source_schema ON source_schema.oid = source_table.relnamespace
			JOIN pg_class target_table ON target_table.oid = c.confrelid
			JOIN pg_namespace target_schema ON target_schema.oid = target_table.relnamespace
			WHERE c.contype = 'f'
			  AND source_schema.nspname = 'entitlement'
			  AND target_schema.nspname <> 'entitlement'
		`).Scan(&count); err != nil {
			t.Fatalf("query entitlement cross-module foreign keys: %v", err)
		}
		if count != 0 {
			t.Fatalf("entitlement cross-module foreign key count = %d, want 0", count)
		}
	})
}

func insertFeature(t *testing.T, database testpostgres.Database, now time.Time, featureID, productID, featureCode string) {
	t.Helper()
	if _, err := database.Pool.Exec(context.Background(), `
		INSERT INTO entitlement.features(feature_id, product_id, feature_code, kind, display_name, status, created_at)
		VALUES ($1, $2, $3, 'boolean', $3, 'active', $4)
	`, featureID, productID, featureCode, now); err != nil {
		t.Fatalf("insert feature %s: %v", featureID, err)
	}
}

func insertPolicy(t *testing.T, database testpostgres.Database, now time.Time, policyID, productID, tenantID, policyCode string, version int) {
	t.Helper()
	if _, err := database.Pool.Exec(context.Background(), `
		INSERT INTO entitlement.policies(
			policy_id, product_id, tenant_id, policy_code, version, status, features,
			validity_rule, validity_seconds, stacking_rule, priority, revoke_scope,
			offline_grace_max_seconds, published_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'active', '[{"feature_code":"pro.member","kind":"boolean"}]'::jsonb,
		          'fixed_duration', 3600, 'union_latest_expiry', 0, 'source_only', 300, $6, $6, $6)
	`, policyID, productID, tenantID, policyCode, version, now); err != nil {
		t.Fatalf("insert policy %s: %v", policyID, err)
	}
}

func insertGrant(t *testing.T, database testpostgres.Database, now time.Time, grantID, productID, tenantID, userID, policyID, sourceID, sourceEffectID, idempotencyKey string) {
	t.Helper()
	if _, err := database.Pool.Exec(context.Background(), `
		INSERT INTO entitlement.grants(
			grant_id, product_id, tenant_id, user_id, policy_id, policy_version, effect,
			source_type, source_id, source_effect_id, idempotency_key, valid_from, valid_until,
			actor_type, actor_id, reason_code, request_hash, created_at
		) VALUES ($1, $2, $3, $4, $5, 1, 'grant', 'admin', $6, $7, $8, $9::timestamptz,
		          $9::timestamptz + interval '1 hour', 'admin', 'admin-a', 'manual',
		          decode(repeat('a1', 32), 'hex'), $9::timestamptz)
	`, grantID, productID, tenantID, userID, policyID, sourceID, sourceEffectID, idempotencyKey, now); err != nil {
		t.Fatalf("insert grant %s: %v", grantID, err)
	}
}

func insertRevision(t *testing.T, database testpostgres.Database, now time.Time, revisionID, productID, tenantID, userID string, version int) {
	t.Helper()
	if _, err := database.Pool.Exec(context.Background(), `
		INSERT INTO entitlement.revisions(
			revision_id, product_id, tenant_id, user_id, version, decision_hash,
			effective_features, plan_code, valid_until, offline_grace_until, updated_at
		) VALUES ($1, $2, $3, $4, $5, decode(repeat('b1', 32), 'hex'),
		          '{"pro.member":{"allowed":true}}'::jsonb, 'pro',
		          $6::timestamptz + interval '1 hour',
		          $6::timestamptz + interval '90 minutes', $6::timestamptz)
	`, revisionID, productID, tenantID, userID, version, now); err != nil {
		t.Fatalf("insert revision %s: %v", revisionID, err)
	}
}

func TestEntitlementMigrationDoesNotExposeRawSecretColumns(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()

	rows, err := database.Pool.Query(ctx, `
		SELECT table_name, column_name
		FROM information_schema.columns
		WHERE table_schema = 'entitlement'
	`)
	if err != nil {
		t.Fatalf("query entitlement columns: %v", err)
	}
	defer rows.Close()

	forbidden := map[string]struct{}{
		"token": {}, "access_token": {}, "refresh_token": {}, "secret": {}, "password": {},
		"raw_token": {}, "raw_secret": {}, "raw_password": {},
	}
	for rows.Next() {
		var tableName, columnName string
		if err := rows.Scan(&tableName, &columnName); err != nil {
			t.Fatalf("scan entitlement column: %v", err)
		}
		_, explicitlyForbidden := forbidden[columnName]
		if explicitlyForbidden || strings.Contains(columnName, "raw_") || strings.HasSuffix(columnName, "_raw") {
			t.Errorf("entitlement.%s exposes forbidden raw column %s", tableName, columnName)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate entitlement columns: %v", err)
	}
}
