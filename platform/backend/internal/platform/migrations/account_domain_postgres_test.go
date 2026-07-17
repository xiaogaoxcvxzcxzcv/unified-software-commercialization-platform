package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestPostgreSQLAccountDomainMigrationInvariants(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO identity.users(user_id, display_name, account_status, created_at, updated_at)
		VALUES ('user-account-a', 'Account A', 'active', $1, $1),
		       ('user-account-b', 'Account B', 'active', $1, $1)
	`, now); err != nil {
		t.Fatalf("seed account users: %v", err)
	}

	t.Run("schemas tables and columns exist", func(t *testing.T) {
		for _, relation := range []string{
			"identity.user_identifiers",
			"identity.user_profiles",
			"identity.end_user_sessions",
			"identity.end_user_session_tokens",
			"identity.recovery_challenges",
			"identity.external_identities",
			"identity.end_user_idempotency_records",
			"identity.end_user_login_failures",
			"product_user_access.product_access",
			"product_user_access.tenant_access",
			"product_user_access.idempotency_records",
			"product_user_access.outbox_events",
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
			"identity.user_identifiers":          {"identifier_type", "normalized_digest", "masked_value", "verification_status"},
			"identity.end_user_sessions":         {"product_id", "application_id", "tenant_id", "token_family_id", "refresh_expires_at", "revoked_at"},
			"identity.end_user_session_tokens":   {"session_id", "token_family_id", "token_type", "generation", "token_digest", "consumed_at", "rotation_request_digest", "rotation_recovery_expires_at"},
			"identity.end_user_login_failures":   {"scope_id", "identifier_digest", "source_digest", "failure_count", "blocked_until"},
			"identity.recovery_challenges":       {"continuation_digest", "proof_digest", "expires_at", "consumed_at"},
			"identity.external_identities":       {"provider", "provider_application_id", "subject_digest", "union_subject_digest"},
			"product_user_access.product_access": {"product_id", "user_id", "status", "access_version", "operator_note"},
			"product_user_access.tenant_access":  {"product_id", "tenant_id", "user_id", "status", "access_version", "operator_note"},
		} {
			for _, column := range columns {
				var exists bool
				if err := database.Pool.QueryRow(ctx, `
					SELECT EXISTS (
						SELECT 1 FROM information_schema.columns
						WHERE table_schema || '.' || table_name = $1 AND column_name = $2
					)
				`, relation, column).Scan(&exists); err != nil {
					t.Fatalf("query %s.%s: %v", relation, column, err)
				}
				if !exists {
					t.Errorf("column %s.%s is missing", relation, column)
				}
			}
		}
	})

	t.Run("login throttle facts are scope bound", func(t *testing.T) {
		for _, scopeID := range []string{"product-a|app-a|tenant-a", "product-b|app-b|tenant-b"} {
			if _, err := database.Pool.Exec(ctx, `
				INSERT INTO identity.end_user_login_failures(
					scope_id, identifier_digest, source_digest, failure_count,
					window_started_at, last_failed_at, blocked_until
				) VALUES ($1, decode(repeat('d1', 32), 'hex'), decode(repeat('d2', 32), 'hex'), 1, $2, $2, NULL)
			`, scopeID, now); err != nil {
				t.Fatalf("insert login throttle fact for %s: %v", scopeID, err)
			}
		}
		var count int
		if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM identity.end_user_login_failures`).Scan(&count); err != nil {
			t.Fatalf("count login throttle facts: %v", err)
		}
		if count != 2 {
			t.Fatalf("login throttle fact count = %d, want 2 independently scoped facts", count)
		}
	})

	t.Run("identity stores no raw identifier token or provider subject", func(t *testing.T) {
		rows, err := database.Pool.Query(ctx, `
			SELECT table_name, column_name
			FROM information_schema.columns
			WHERE table_schema = 'identity'
			  AND table_name IN (
				'user_identifiers', 'end_user_sessions', 'end_user_session_tokens',
				'recovery_challenges', 'external_identities', 'end_user_idempotency_records'
			  )
		`)
		if err != nil {
			t.Fatalf("query identity columns: %v", err)
		}
		defer rows.Close()
		forbidden := map[string]struct{}{
			"identifier": {}, "normalized_identifier": {}, "identifier_value": {},
			"token": {}, "access_token": {}, "refresh_token": {}, "token_value": {},
			"continuation": {}, "recovery_proof": {}, "proof_value": {},
			"subject": {}, "provider_subject": {}, "union_subject": {},
		}
		for rows.Next() {
			var tableName, columnName string
			if err := rows.Scan(&tableName, &columnName); err != nil {
				t.Fatalf("scan identity column: %v", err)
			}
			_, explicitlyForbidden := forbidden[columnName]
			if explicitlyForbidden || strings.Contains(columnName, "raw_") || strings.HasSuffix(columnName, "_raw") {
				t.Errorf("identity.%s exposes forbidden raw column %s", tableName, columnName)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate identity columns: %v", err)
		}
	})

	t.Run("identifiers are globally unique and user sessions reuse identity across products", func(t *testing.T) {
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO identity.user_identifiers(
				identifier_id, user_id, identifier_type, normalization_version, normalized_digest,
				masked_value, verification_status, verified_at, created_at, updated_at
			) VALUES ('identifier-a', 'user-account-a', 'email', 1, decode(repeat('aa', 32), 'hex'),
			          'a***@example.test', 'verified', $1, $1, $1)
		`, now); err != nil {
			t.Fatalf("insert first identifier: %v", err)
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO identity.user_identifiers(
				identifier_id, user_id, identifier_type, normalization_version, normalized_digest,
				masked_value, verification_status, verified_at, created_at, updated_at
			) VALUES ('identifier-b', 'user-account-b', 'email', 1, decode(repeat('aa', 32), 'hex'),
			          'a***@example.test', 'verified', $1, $1, $1)
		`, now); err == nil {
			t.Fatal("duplicate normalized email digest was accepted for another global user")
		}

		insertSession := func(sessionID, productID, applicationID, tenantID, familyID string) error {
			_, err := database.Pool.Exec(ctx, `
				INSERT INTO identity.end_user_sessions(
					session_id, user_id, product_id, application_id, tenant_id, token_family_id,
					authentication_method, auth_time, created_at, last_seen_at,
					access_expires_at, refresh_expires_at, absolute_expires_at
				) VALUES ($1, 'user-account-a', $2, $3, NULLIF($4, ''), $5,
				          'password', $6::timestamptz, $6::timestamptz, $6::timestamptz,
				          $6::timestamptz + interval '5 minutes',
				          $6::timestamptz + interval '1 day', $6::timestamptz + interval '7 days')
			`, sessionID, productID, applicationID, tenantID, familyID, now)
			return err
		}
		if err := insertSession("session-product-a", "product-a", "app-a", "tenant-a", "family-a"); err != nil {
			t.Fatalf("insert product A session: %v", err)
		}
		if err := insertSession("session-product-b", "product-b", "app-b", "tenant-b", "family-b"); err != nil {
			t.Fatalf("same global user could not open product B session: %v", err)
		}
		var productCount int
		if err := database.Pool.QueryRow(ctx, `
			SELECT count(DISTINCT product_id) FROM identity.end_user_sessions WHERE user_id='user-account-a'
		`).Scan(&productCount); err != nil {
			t.Fatalf("count session products: %v", err)
		}
		if productCount != 2 {
			t.Fatalf("global user session product count = %d, want 2", productCount)
		}
	})

	t.Run("refresh token digests are unique across session scopes", func(t *testing.T) {
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO identity.end_user_session_tokens(
				token_id, session_id, token_family_id, token_type, generation, token_digest, created_at, expires_at
			) VALUES ('refresh-a', 'session-product-a', 'family-a', 'refresh', 1, decode(repeat('bb', 32), 'hex'),
			          $1::timestamptz, $1::timestamptz + interval '1 day')
		`, now); err != nil {
			t.Fatalf("insert first refresh token digest: %v", err)
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO identity.end_user_session_tokens(
				token_id, session_id, token_family_id, token_type, generation, token_digest, created_at, expires_at
			) VALUES ('refresh-b', 'session-product-b', 'family-b', 'refresh', 1, decode(repeat('bb', 32), 'hex'),
			          $1::timestamptz, $1::timestamptz + interval '1 day')
		`, now); err == nil {
			t.Fatal("duplicate refresh token digest was accepted across product sessions")
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO identity.end_user_session_tokens(
				token_id, session_id, token_family_id, token_type, generation, token_digest, created_at, expires_at
			) VALUES ('refresh-family-mismatch', 'session-product-a', 'family-b', 'refresh', 2,
			          decode(repeat('bc', 32), 'hex'), $1::timestamptz, $1::timestamptz + interval '1 day')
		`, now); err == nil {
			t.Fatal("token family from another product session was accepted")
		}
	})

	t.Run("refresh recovery metadata is paired and refresh-only", func(t *testing.T) {
		if _, err := database.Pool.Exec(ctx, `
			UPDATE identity.end_user_session_tokens
			SET consumed_at=$2::timestamptz, rotation_request_digest=decode(repeat('e1', 32), 'hex'),
			    rotation_recovery_expires_at=$2::timestamptz + interval '30 seconds'
			WHERE token_id=$1
		`, "refresh-a", now); err != nil {
			t.Fatalf("store valid refresh recovery metadata: %v", err)
		}
		if _, err := database.Pool.Exec(ctx, `
			UPDATE identity.end_user_session_tokens
			SET rotation_request_digest=decode(repeat('e2', 32), 'hex'),
			    rotation_recovery_expires_at=NULL
			WHERE token_id=$1
		`, "refresh-a"); err == nil {
			t.Fatal("unpaired refresh recovery request digest was accepted")
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO identity.end_user_session_tokens(
				token_id, session_id, token_family_id, token_type, generation, token_digest,
				created_at, expires_at, rotation_request_digest, rotation_recovery_expires_at
			) VALUES ('access-with-recovery', 'session-product-a', 'family-a', 'access', 1,
			          decode(repeat('e3', 32), 'hex'), $1, $1 + interval '5 minutes',
			          decode(repeat('e4', 32), 'hex'), $1 + interval '30 seconds')
		`, now); err == nil {
			t.Fatal("access token accepted refresh recovery metadata")
		}
	})

	t.Run("security digests require exactly 32 bytes", func(t *testing.T) {
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO identity.user_identifiers(
				identifier_id, user_id, identifier_type, normalization_version, normalized_digest,
				masked_value, verification_status, created_at, updated_at
			) VALUES ('identifier-short', 'user-account-a', 'phone', 1, decode('01', 'hex'),
			          '***0000', 'unverified', $1, $1)
		`, now); err == nil {
			t.Fatal("short normalized identifier digest was accepted")
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO identity.end_user_session_tokens(
				token_id, session_id, token_family_id, token_type, generation, token_digest, created_at, expires_at
			) VALUES ('refresh-short', 'session-product-a', 'family-a', 'refresh', 2,
			          decode('02', 'hex'), $1::timestamptz, $1::timestamptz + interval '1 day')
		`, now); err == nil {
			t.Fatal("short token digest was accepted")
		}
	})

	t.Run("recovery continuation and proof digests are unique", func(t *testing.T) {
		insertChallenge := func(id, continuationHex, proofHex string) error {
			_, err := database.Pool.Exec(ctx, `
				INSERT INTO identity.recovery_challenges(
					challenge_id, continuation_digest, identifier_type, identifier_digest,
					matched_user_id, delivery_target_masked, proof_digest, max_attempts,
					created_at, expires_at
				) VALUES ($1, decode($2, 'hex'), 'email', decode(repeat('cc', 32), 'hex'),
				          'user-account-a', 'a***@example.test', decode($3, 'hex'), 5,
				          $4::timestamptz, $4::timestamptz + interval '15 minutes')
			`, id, continuationHex, proofHex, now)
			return err
		}
		if err := insertChallenge("challenge-a", strings.Repeat("01", 32), strings.Repeat("11", 32)); err != nil {
			t.Fatalf("insert first recovery challenge: %v", err)
		}
		if err := insertChallenge("challenge-continuation-duplicate", strings.Repeat("01", 32), strings.Repeat("12", 32)); err == nil {
			t.Fatal("duplicate recovery continuation digest was accepted")
		}
		if err := insertChallenge("challenge-proof-duplicate", strings.Repeat("02", 32), strings.Repeat("11", 32)); err == nil {
			t.Fatal("duplicate recovery proof digest was accepted")
		}
	})

	t.Run("recovery expiry and consumption are structurally one way", func(t *testing.T) {
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO identity.recovery_challenges(
				challenge_id, continuation_digest, identifier_type, identifier_digest,
				delivery_target_masked, proof_digest, max_attempts, created_at, expires_at
			) VALUES ('challenge-invalid-expiry', decode(repeat('03', 32), 'hex'), 'email', decode(repeat('cd', 32), 'hex'),
			          'x***@example.test', decode(repeat('13', 32), 'hex'), 5, $1, $1)
		`, now); err == nil {
			t.Fatal("recovery challenge with non-increasing expiry was accepted")
		}
		if _, err := database.Pool.Exec(ctx, `
			UPDATE identity.recovery_challenges SET attempt_count=2 WHERE challenge_id='challenge-a'
		`); err != nil {
			t.Fatalf("advance recovery attempt count: %v", err)
		}
		if _, err := database.Pool.Exec(ctx, `
			UPDATE identity.recovery_challenges SET attempt_count=1 WHERE challenge_id='challenge-a'
		`); err == nil {
			t.Fatal("recovery attempt count could move backwards")
		}
		if _, err := database.Pool.Exec(ctx, `
			UPDATE identity.recovery_challenges SET consumed_at=$1 WHERE challenge_id='challenge-a' AND consumed_at IS NULL
		`, now.Add(time.Minute)); err != nil {
			t.Fatalf("consume recovery challenge once: %v", err)
		}
		if _, err := database.Pool.Exec(ctx, `
			UPDATE identity.recovery_challenges SET consumed_at=NULL WHERE challenge_id='challenge-a'
		`); err == nil {
			t.Fatal("consumed recovery challenge could be reset to reusable")
		}
	})

	t.Run("external identity provider tuple is globally unique", func(t *testing.T) {
		insertExternalIdentity := func(id, userID string) error {
			_, err := database.Pool.Exec(ctx, `
				INSERT INTO identity.external_identities(
					external_identity_id, user_id, provider, provider_application_id,
					subject_digest, subject_masked, status, linked_at, updated_at
				) VALUES ($1, $2, 'wechat', 'wechat-app-a', decode(repeat('dd', 32), 'hex'),
				          'wx***', 'active', $3, $3)
			`, id, userID, now)
			return err
		}
		if err := insertExternalIdentity("external-a", "user-account-a"); err != nil {
			t.Fatalf("insert first external identity: %v", err)
		}
		if err := insertExternalIdentity("external-b", "user-account-b"); err == nil {
			t.Fatal("same provider application subject was accepted for a second global user")
		}
	})

	t.Run("product and tenant access keys are unique and immutable", func(t *testing.T) {
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO product_user_access.product_access(
				product_id, user_id, status, reason_code, status_changed_at, created_at, updated_at
			) VALUES ('product-a', 'user-account-a', 'active', 'initial', $1, $1, $1)
		`, now); err != nil {
			t.Fatalf("insert product access: %v", err)
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO product_user_access.product_access(
				product_id, user_id, status, reason_code, status_changed_at, created_at, updated_at
			) VALUES ('product-a', 'user-account-a', 'suspended', 'duplicate', $1, $1, $1)
		`, now); err == nil {
			t.Fatal("duplicate product/user access identity was accepted")
		}
		if _, err := database.Pool.Exec(ctx, `
			UPDATE product_user_access.product_access SET product_id='product-b'
			WHERE product_id='product-a' AND user_id='user-account-a'
		`); err == nil {
			t.Fatal("product access scope identity was mutable")
		}

		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO product_user_access.tenant_access(
				product_id, tenant_id, user_id, status, reason_code, status_changed_at, created_at, updated_at
			) VALUES ('product-a', 'tenant-a', 'user-account-a', 'active', 'initial', $1, $1, $1)
		`, now); err != nil {
			t.Fatalf("insert tenant access: %v", err)
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO product_user_access.tenant_access(
				product_id, tenant_id, user_id, status, reason_code, status_changed_at, created_at, updated_at
			) VALUES ('product-a', 'tenant-a', 'user-account-a', 'suspended', 'duplicate', $1, $1, $1)
		`, now); err == nil {
			t.Fatal("duplicate product/tenant/user access identity was accepted")
		}
		if _, err := database.Pool.Exec(ctx, `
			UPDATE product_user_access.tenant_access SET tenant_id='tenant-b'
			WHERE product_id='product-a' AND tenant_id='tenant-a' AND user_id='user-account-a'
		`); err == nil {
			t.Fatal("tenant access scope identity was mutable")
		}
	})

	t.Run("operator notes reject control characters", func(t *testing.T) {
		for name, statement := range map[string]string{
			"product": `INSERT INTO product_user_access.product_access(
				product_id, user_id, status, reason_code, operator_note, status_changed_at, created_at, updated_at
			) VALUES ('product-note', 'user-account-a', 'suspended', 'manual', E'bad\nnote', $1, $1, $1)`,
			"tenant": `INSERT INTO product_user_access.tenant_access(
				product_id, tenant_id, user_id, status, reason_code, operator_note, status_changed_at, created_at, updated_at
			) VALUES ('product-note', 'tenant-note', 'user-account-a', 'suspended', 'manual', E'bad\tnote', $1, $1, $1)`,
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := database.Pool.Exec(ctx, statement, now); err == nil {
					t.Fatal("operator_note with control characters was accepted")
				}
			})
		}
	})

	t.Run("product idempotency scope cannot name another product", func(t *testing.T) {
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO product_user_access.idempotency_records(
				operation, scope_type, product_id, scope_id, user_id, key_digest,
				request_digest, state, created_at, updated_at
			) VALUES ('set-access', 'product', 'product-a', 'product-b', 'user-account-a',
			          decode(repeat('ee', 32), 'hex'), decode(repeat('ef', 32), 'hex'),
			          'pending', $1, $1)
		`, now); err == nil {
			t.Fatal("product idempotency record accepted a scope_id from another product")
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO product_user_access.idempotency_records(
				operation, scope_type, product_id, scope_id, user_id, key_digest,
				request_digest, state, created_at, updated_at
			) VALUES ('set-access', 'product', 'product-a', 'product-a', 'user-account-a',
			          decode('ee', 'hex'), decode(repeat('ef', 32), 'hex'),
			          'pending', $1, $1)
		`, now); err == nil {
			t.Fatal("product user access idempotency record accepted a short key digest")
		}
	})

	t.Run("product user access owns no cross module foreign keys", func(t *testing.T) {
		var count int
		if err := database.Pool.QueryRow(ctx, `
			SELECT count(*)
			FROM pg_constraint c
			JOIN pg_class source_table ON source_table.oid = c.conrelid
			JOIN pg_namespace source_schema ON source_schema.oid = source_table.relnamespace
			JOIN pg_class target_table ON target_table.oid = c.confrelid
			JOIN pg_namespace target_schema ON target_schema.oid = target_table.relnamespace
			WHERE c.contype = 'f'
			  AND source_schema.nspname = 'product_user_access'
			  AND target_schema.nspname <> 'product_user_access'
		`).Scan(&count); err != nil {
			t.Fatalf("query cross-module foreign keys: %v", err)
		}
		if count != 0 {
			t.Fatalf("product_user_access cross-module foreign key count = %d, want 0", count)
		}
	})
}
