package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/audit"
	auditpostgres "platform.local/capability-platform/backend/internal/modules/audit/postgres"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
	productuseraccesspostgres "platform.local/capability-platform/backend/internal/modules/productuseraccess/postgres"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

type puaSequenceIDGenerator struct{ next int }

func (g *puaSequenceIDGenerator) ID(prefix string) (string, error) {
	g.next++
	return fmt.Sprintf("%s%04d", prefix, g.next), nil
}

func TestProductUserAccessDispatcherAuditsAndRevokesOnlyTargetProduct(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := now
	if _, err := database.Pool.Exec(ctx, `INSERT INTO identity.users(user_id,display_name,account_status,created_at,updated_at) VALUES('user-a','User A','active',$1,$1)`, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	for _, session := range []struct {
		id, product, application, family, digest string
	}{
		{"session-a", "product-a", "app-a", "family-a", strings.Repeat("a1", 32)},
		{"session-b", "product-b", "app-b", "family-b", strings.Repeat("b1", 32)},
	} {
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO identity.end_user_sessions(
				session_id,user_id,product_id,application_id,tenant_id,token_family_id,
				authentication_method,auth_time,created_at,last_seen_at,
				access_expires_at,refresh_expires_at,absolute_expires_at
			) VALUES ($1,'user-a',$2,$3,NULL,$4,'password',$5::timestamptz,$5::timestamptz,$5::timestamptz,
			          $5::timestamptz+interval '15 minutes',$5::timestamptz+interval '1 day',$5::timestamptz+interval '30 days')
		`, session.id, session.product, session.application, session.family, now.Add(-time.Minute)); err != nil {
			t.Fatalf("seed session %s: %v", session.id, err)
		}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO identity.end_user_session_tokens(
				token_id,session_id,token_family_id,token_type,generation,token_digest,created_at,expires_at
			) VALUES ($1,$2,$3,'access',1,decode($4,'hex'),$5::timestamptz,$5::timestamptz+interval '15 minutes')
		`, "token-"+session.id, session.id, session.family, session.digest, now); err != nil {
			t.Fatalf("seed token %s: %v", session.id, err)
		}
	}

	hasher, err := securevalue.NewHasher(strings.Repeat("user-pepper-", 4))
	if err != nil {
		t.Fatal(err)
	}
	accessService := productuseraccess.NewService(productuseraccesspostgres.New(database.Pool), &puaSequenceIDGenerator{}, []byte(strings.Repeat("user-pepper-", 4)), func() time.Time { return clock })
	result, err := accessService.SetProductAccessStatus(ctx, productuseraccess.SetProductAccessStatusCommand{
		Product: productuseraccess.ProductContext{ProductID: "product-a"}, User: productuseraccess.UserContext{UserID: "user-a"},
		Status: productuseraccess.StatusSuspended, ExpectedVersion: 0, ReasonCode: "security.review",
		IdempotencyKey: "pua-e2e-idempotency-0001", ActorID: "admin-a", TraceID: "trace-pua-e2e",
	})
	if err != nil {
		t.Fatalf("suspend product access: %v", err)
	}
	clock = now.Add(time.Hour)
	dispatcher := productUserAccessDispatcher{
		source: accessService, audit: audit.NewService(auditpostgres.New(database.Pool)),
		revoker: identitypostgres.New(database.Pool), hasher: hasher, now: func() time.Time { return clock },
	}
	dispatcher.dispatch(ctx)

	var auditCount int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM audit.events WHERE audit_id=$1 AND permission='product.user-access.manage' AND product_id='product-a'`, result.AuditID).Scan(&auditCount); err != nil || auditCount != 1 {
		var deliveryState string
		_ = database.Pool.QueryRow(ctx, `SELECT concat(event_type,':',published_at IS NOT NULL,':',attempt_count,':',COALESCE(last_error,'')) FROM product_user_access.outbox_events WHERE event_type='product-user-access.status-changed.v1'`).Scan(&deliveryState)
		t.Fatalf("audit count=%d error=%v delivery=%s", auditCount, err, deliveryState)
	}
	var revokedA, revokedB *time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT revoked_at FROM identity.end_user_sessions WHERE session_id='session-a'`).Scan(&revokedA); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT revoked_at FROM identity.end_user_sessions WHERE session_id='session-b'`).Scan(&revokedB); err != nil {
		t.Fatal(err)
	}
	if revokedA == nil || revokedB != nil {
		t.Fatalf("target product revoked_at=%v, other product revoked_at=%v", revokedA, revokedB)
	}
	var tokenRevokedA, tokenRevokedB *time.Time
	if err := database.Pool.QueryRow(ctx, `SELECT revoked_at FROM identity.end_user_session_tokens WHERE token_id='token-session-a'`).Scan(&tokenRevokedA); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT revoked_at FROM identity.end_user_session_tokens WHERE token_id='token-session-b'`).Scan(&tokenRevokedB); err != nil {
		t.Fatal(err)
	}
	if tokenRevokedA == nil || tokenRevokedB != nil {
		t.Fatalf("target token revoked_at=%v, other product token revoked_at=%v", tokenRevokedA, tokenRevokedB)
	}
	var incompleteDeliveries int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.outbox_events WHERE published_at IS NULL OR dead OR last_error IS NOT NULL`).Scan(&incompleteDeliveries); err != nil || incompleteDeliveries != 0 {
		t.Fatalf("incomplete outbox deliveries=%d error=%v", incompleteDeliveries, err)
	}
}
