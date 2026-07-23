package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/entitlement"
	entitlementpostgres "platform.local/capability-platform/backend/internal/modules/entitlement/postgres"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

var digestKey = []byte("0123456789abcdef0123456789abcdef")

type sequenceIDs struct {
	mu sync.Mutex
	n  int
}

func (s *sequenceIDs) ID(prefix string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return fmt.Sprintf("%s%032d", prefix, s.n), nil
}

func TestRepositoryGrantCheckCurrentHistoryAndOutbox(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	seedPolicy(t, database, "product-a", "tenant-a", "policy-pro", "pro", 1, "pro.member")

	repository := entitlementpostgres.New(database.Pool)
	service := entitlement.NewService(repository, &sequenceIDs{}, digestKey, fixedNow)
	admin := entitlement.AdminScope{AdminID: "admin-a", ProductID: "product-a", TenantID: "tenant-a"}
	user := entitlement.UserContext{UserID: "user-a"}
	grantCommand := entitlement.GrantEntitlementCommand{
		Admin:          admin,
		User:           user,
		Policy:         entitlement.PolicyRef{PolicyID: "policy-pro", Version: 1},
		Validity:       entitlement.ValidityInput{Rule: entitlement.ValidityFixedDuration, Duration: time.Hour},
		Source:         entitlement.SourceRef{Type: entitlement.SourceAdmin, SourceID: "manual-source-a", SourceEffectID: "effect-a"},
		IdempotencyKey: "idempotency-grant-0001",
		TraceID:        "trace-grant-0001",
	}
	first, err := service.GrantEntitlement(ctx, grantCommand)
	if err != nil {
		t.Fatalf("GrantEntitlement() error = %v", err)
	}
	if first.Revision != 1 || first.GrantID == "" || first.AuditID == "" || first.ValidUntil == nil {
		t.Fatalf("first grant result=%+v", first)
	}
	replayCommand := grantCommand
	replayCommand.TraceID = "trace-grant-replay"
	replay, err := service.GrantEntitlement(ctx, replayCommand)
	if err != nil || replay.GrantID != first.GrantID || replay.Revision != first.Revision || replay.AuditID != first.AuditID {
		t.Fatalf("replay=%+v error=%v first=%+v", replay, err, first)
	}
	changed := grantCommand
	changed.Source.SourceEffectID = "effect-changed"
	if _, err := service.GrantEntitlement(ctx, changed); !errors.Is(err, entitlement.ErrOperationConflict) {
		t.Fatalf("changed idempotency request error=%v, want ErrOperationConflict", err)
	}

	decision, err := service.CheckEntitlement(ctx, entitlement.CheckEntitlementCommand{
		Product:           entitlement.ProductContext{ProductID: "product-a"},
		Tenant:            entitlement.TenantContext{ProductID: "product-a", TenantID: "tenant-a"},
		User:              user,
		RequestedFeatures: []string{"pro.member"},
	})
	if err != nil || !decision.Allowed || decision.Revision != 1 || decision.Features["pro.member"] != true {
		t.Fatalf("decision=%+v error=%v", decision, err)
	}
	current, err := service.GetCurrentEntitlements(ctx, entitlement.ProductContext{ProductID: "product-a"}, entitlement.TenantContext{ProductID: "product-a", TenantID: "tenant-a"}, user)
	if err != nil || current.Revision != 1 || current.PlanCode != "pro" || current.EffectiveFeatures["pro.member"] != true {
		t.Fatalf("current=%+v error=%v", current, err)
	}
	history, err := service.ListHistory(ctx, entitlement.HistoryQuery{ProductID: "product-a", TenantID: "tenant-a", UserID: "user-a", Limit: 10})
	if err != nil || len(history) != 1 || history[0].OperationType != entitlement.EffectGrant || history[0].AfterRevision != 1 || history[0].AuditID != first.AuditID {
		t.Fatalf("history=%+v error=%v", history, err)
	}

	claimed, err := repository.ClaimOutbox(ctx, fixedNow().Add(time.Minute), 10)
	if err != nil || len(claimed) != 1 || claimed[0].EventType != "entitlement.granted.v1" || claimed[0].AttemptCount != 1 || claimed[0].PayloadError != "" {
		t.Fatalf("claimed=%+v error=%v", claimed, err)
	}
	if claimed[0].Payload["audit_id"] != first.AuditID || claimed[0].Payload["trace_id"] != "trace-grant-0001" {
		t.Fatalf("claimed payload=%+v", claimed[0].Payload)
	}
	if err := service.MarkOutboxPublished(ctx, claimed[0].EventID); err != nil {
		t.Fatalf("MarkOutboxPublished() error=%v", err)
	}
}

func TestRepositoryExpectedRevisionRevokeAndIsolation(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	seedPolicy(t, database, "product-a", "tenant-a", "policy-pro", "pro", 1, "pro.member")
	seedPolicy(t, database, "product-b", "tenant-a", "policy-pro-b", "pro", 1, "pro.member")
	service := entitlement.NewService(entitlementpostgres.New(database.Pool), &sequenceIDs{}, digestKey, fixedNow)
	user := entitlement.UserContext{UserID: "user-a"}
	first, err := service.GrantEntitlement(ctx, entitlement.GrantEntitlementCommand{
		Admin:          entitlement.AdminScope{AdminID: "admin-a", ProductID: "product-a", TenantID: "tenant-a"},
		User:           user,
		Policy:         entitlement.PolicyRef{PolicyID: "policy-pro", Version: 1},
		Validity:       entitlement.ValidityInput{Rule: entitlement.ValidityFixedDuration, Duration: time.Hour},
		Source:         entitlement.SourceRef{Type: entitlement.SourceAdmin, SourceID: "manual-source-a", SourceEffectID: "effect-a"},
		IdempotencyKey: "idempotency-grant-0001",
		TraceID:        "trace-grant-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExtendEntitlement(ctx, entitlement.MutateEntitlementCommand{
		Admin:            entitlement.AdminScope{AdminID: "admin-a", ProductID: "product-a", TenantID: "tenant-a"},
		User:             user,
		Policy:           entitlement.PolicyRef{PolicyID: "policy-pro", Version: 1},
		Validity:         entitlement.ValidityInput{Rule: entitlement.ValidityFixedDuration, Duration: time.Hour},
		Source:           entitlement.SourceRef{Type: entitlement.SourceAdmin, SourceID: "manual-source-a", SourceEffectID: "effect-extend"},
		IdempotencyKey:   "idempotency-extend-stale",
		ExpectedRevision: 99,
		TraceID:          "trace-extend-stale",
	}); !errors.Is(err, entitlement.ErrOperationConflict) {
		t.Fatalf("stale extend error=%v, want ErrOperationConflict", err)
	}
	revoke, err := service.RevokeEntitlement(ctx, entitlement.MutateEntitlementCommand{
		Admin:            entitlement.AdminScope{AdminID: "admin-a", ProductID: "product-a", TenantID: "tenant-a"},
		User:             user,
		TargetGrantID:    first.GrantID,
		IdempotencyKey:   "idempotency-revoke-0001",
		ExpectedRevision: 1,
		TraceID:          "trace-revoke-0001",
	})
	if err != nil || revoke.Revision != 2 {
		t.Fatalf("revoke=%+v error=%v", revoke, err)
	}
	decision, err := service.CheckEntitlement(ctx, entitlement.CheckEntitlementCommand{
		Product:           entitlement.ProductContext{ProductID: "product-a"},
		Tenant:            entitlement.TenantContext{ProductID: "product-a", TenantID: "tenant-a"},
		User:              user,
		RequestedFeatures: []string{"pro.member"},
	})
	if err != nil || decision.Allowed || decision.ReasonCode != entitlement.ReasonEntitlementRequired || decision.Revision != 2 {
		t.Fatalf("post revoke decision=%+v error=%v", decision, err)
	}
	otherProductDecision, err := service.CheckEntitlement(ctx, entitlement.CheckEntitlementCommand{
		Product:           entitlement.ProductContext{ProductID: "product-b"},
		Tenant:            entitlement.TenantContext{ProductID: "product-b", TenantID: "tenant-a"},
		User:              user,
		RequestedFeatures: []string{"pro.member"},
	})
	if err != nil || otherProductDecision.Allowed || otherProductDecision.Revision != 0 {
		t.Fatalf("other product decision=%+v error=%v", otherProductDecision, err)
	}
	var productAGrants, productBGrants int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM entitlement.grants WHERE product_id='product-a'`).Scan(&productAGrants); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM entitlement.grants WHERE product_id='product-b'`).Scan(&productBGrants); err != nil {
		t.Fatal(err)
	}
	if productAGrants != 2 || productBGrants != 0 {
		t.Fatalf("grant isolation productA/productB=%d/%d", productAGrants, productBGrants)
	}
}

func seedPolicy(t *testing.T, database testpostgres.Database, productID, tenantID, policyID, policyCode string, version int64, featureCode string) {
	t.Helper()
	now := fixedNow()
	if _, err := database.Pool.Exec(context.Background(), `
		INSERT INTO entitlement.features(feature_id, product_id, feature_code, kind, display_name, status, created_at)
		VALUES ($1, $2, $3, 'boolean', $3, 'active', $4)
		ON CONFLICT DO NOTHING
	`, "feature-"+policyID, productID, featureCode, now); err != nil {
		t.Fatalf("seed feature: %v", err)
	}
	if _, err := database.Pool.Exec(context.Background(), `
		INSERT INTO entitlement.policies(
			policy_id, product_id, tenant_id, policy_code, version, status, features,
			validity_rule, validity_seconds, stacking_rule, priority, revoke_scope,
			offline_grace_max_seconds, published_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'active',
		          jsonb_build_array(jsonb_build_object('feature_code', $6::text, 'value', true)),
		          'fixed_duration', 3600, 'union_latest_expiry', 0, 'source_only',
		          300, $7, $7, $7)
	`, policyID, productID, tenantID, policyCode, version, featureCode, now); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
}

func fixedNow() time.Time { return time.Date(2026, 7, 23, 9, 0, 0, 123456000, time.UTC) }
