package postgres_test

import (
	"context"
	"encoding/json"
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

func TestRepositoryRejectConflictWritesLedgerAndKeepsRevision(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	seedPolicyEx(t, database, policySeed{ProductID: "product-a", TenantID: "tenant-a", PolicyID: "policy-basic", PolicyCode: "basic", Version: 1, FeatureCode: "pro.member", FeatureValue: true, StackingRule: "union_latest_expiry", MutualExclusionGroup: "membership", Priority: 0})
	seedPolicyEx(t, database, policySeed{ProductID: "product-a", TenantID: "tenant-a", PolicyID: "policy-exclusive", PolicyCode: "exclusive", Version: 1, FeatureCode: "pro.member", FeatureValue: true, StackingRule: "reject_conflict", MutualExclusionGroup: "membership", Priority: 10})
	service := entitlement.NewService(entitlementpostgres.New(database.Pool), &sequenceIDs{}, digestKey, fixedNow)
	admin := entitlement.AdminScope{AdminID: "admin-a", ProductID: "product-a", TenantID: "tenant-a"}
	user := entitlement.UserContext{UserID: "user-a"}
	if _, err := service.GrantEntitlement(ctx, entitlement.GrantEntitlementCommand{
		Admin:          admin,
		User:           user,
		Policy:         entitlement.PolicyRef{PolicyID: "policy-basic", Version: 1},
		Validity:       entitlement.ValidityInput{Rule: entitlement.ValidityFixedDuration, Duration: time.Hour},
		Source:         entitlement.SourceRef{Type: entitlement.SourceAdmin, SourceID: "manual-basic", SourceEffectID: "effect-basic"},
		IdempotencyKey: "idempotency-grant-basic",
		TraceID:        "trace-grant-basic",
	}); err != nil {
		t.Fatalf("initial grant error=%v", err)
	}
	if _, err := service.GrantEntitlement(ctx, entitlement.GrantEntitlementCommand{
		Admin:          admin,
		User:           user,
		Policy:         entitlement.PolicyRef{PolicyID: "policy-exclusive", Version: 1},
		Validity:       entitlement.ValidityInput{Rule: entitlement.ValidityFixedDuration, Duration: time.Hour},
		Source:         entitlement.SourceRef{Type: entitlement.SourceAdmin, SourceID: "manual-exclusive", SourceEffectID: "effect-exclusive"},
		IdempotencyKey: "idempotency-exclusive",
		TraceID:        "trace-exclusive",
	}); !errors.Is(err, entitlement.ErrPolicyConflict) {
		t.Fatalf("exclusive grant error=%v, want ErrPolicyConflict", err)
	}
	current, err := service.GetCurrentEntitlements(ctx, entitlement.ProductContext{ProductID: "product-a"}, entitlement.TenantContext{ProductID: "product-a", TenantID: "tenant-a"}, user)
	if err != nil || current.Revision != 1 || current.PlanCode != "basic" {
		t.Fatalf("current after conflict=%+v error=%v", current, err)
	}
	var conflictLedger, grants int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM entitlement.ledger WHERE operation_type='policy_conflict' AND trace_id='trace-exclusive'`).Scan(&conflictLedger); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM entitlement.grants WHERE product_id='product-a' AND user_id='user-a'`).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if conflictLedger != 1 || grants != 1 {
		t.Fatalf("conflict ledger/grants=%d/%d, want 1/1", conflictLedger, grants)
	}
}

func TestRepositoryReplaceSameGroupChoosesPriorityThenCreateOrder(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	seedPolicyEx(t, database, policySeed{ProductID: "product-a", TenantID: "tenant-a", PolicyID: "policy-silver", PolicyCode: "silver", Version: 1, FeatureCode: "plan.level", FeatureValue: float64(1), StackingRule: "replace_same_group", MutualExclusionGroup: "membership", Priority: 10})
	seedPolicyEx(t, database, policySeed{ProductID: "product-a", TenantID: "tenant-a", PolicyID: "policy-gold", PolicyCode: "gold", Version: 1, FeatureCode: "plan.level", FeatureValue: float64(2), StackingRule: "replace_same_group", MutualExclusionGroup: "membership", Priority: 20})
	seedPolicyEx(t, database, policySeed{ProductID: "product-a", TenantID: "tenant-a", PolicyID: "policy-gold-later", PolicyCode: "gold-later", Version: 1, FeatureCode: "plan.level", FeatureValue: float64(3), StackingRule: "replace_same_group", MutualExclusionGroup: "membership", Priority: 20})
	service := entitlement.NewService(entitlementpostgres.New(database.Pool), &sequenceIDs{}, digestKey, fixedNow)
	admin := entitlement.AdminScope{AdminID: "admin-a", ProductID: "product-a", TenantID: "tenant-a"}
	user := entitlement.UserContext{UserID: "user-a"}
	grant := func(policyID, sourceID, effectID, key string) {
		t.Helper()
		_, err := service.GrantEntitlement(ctx, entitlement.GrantEntitlementCommand{
			Admin:          admin,
			User:           user,
			Policy:         entitlement.PolicyRef{PolicyID: policyID, Version: 1},
			Validity:       entitlement.ValidityInput{Rule: entitlement.ValidityFixedDuration, Duration: time.Hour},
			Source:         entitlement.SourceRef{Type: entitlement.SourceAdmin, SourceID: sourceID, SourceEffectID: effectID},
			IdempotencyKey: key,
			TraceID:        "trace-" + key,
		})
		if err != nil {
			t.Fatalf("grant %s error=%v", policyID, err)
		}
	}
	grant("policy-silver", "manual-silver", "effect-silver", "idempotency-silver")
	current, err := service.GetCurrentEntitlements(ctx, entitlement.ProductContext{ProductID: "product-a"}, entitlement.TenantContext{ProductID: "product-a", TenantID: "tenant-a"}, user)
	if err != nil || current.EffectiveFeatures["plan.level"] != float64(1) {
		t.Fatalf("silver current=%+v error=%v", current, err)
	}
	grant("policy-gold", "manual-gold", "effect-gold", "idempotency-gold")
	current, err = service.GetCurrentEntitlements(ctx, entitlement.ProductContext{ProductID: "product-a"}, entitlement.TenantContext{ProductID: "product-a", TenantID: "tenant-a"}, user)
	if err != nil || current.EffectiveFeatures["plan.level"] != float64(2) {
		t.Fatalf("gold current=%+v error=%v", current, err)
	}
	grant("policy-gold-later", "manual-gold-later", "effect-gold-later", "idempotency-gold-later")
	current, err = service.GetCurrentEntitlements(ctx, entitlement.ProductContext{ProductID: "product-a"}, entitlement.TenantContext{ProductID: "product-a", TenantID: "tenant-a"}, user)
	if err != nil || current.EffectiveFeatures["plan.level"] != float64(3) || current.Revision != 3 {
		t.Fatalf("tie current=%+v error=%v", current, err)
	}
}

func TestRepositoryRevokeBySourceTupleOnlyRemovesThatSource(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	seedPolicy(t, database, "product-a", "tenant-a", "policy-pro", "pro", 1, "pro.member")
	service := entitlement.NewService(entitlementpostgres.New(database.Pool), &sequenceIDs{}, digestKey, fixedNow)
	admin := entitlement.AdminScope{AdminID: "admin-a", ProductID: "product-a", TenantID: "tenant-a"}
	user := entitlement.UserContext{UserID: "user-a"}
	if _, err := service.GrantEntitlement(ctx, entitlement.GrantEntitlementCommand{
		Admin:          admin,
		User:           user,
		Policy:         entitlement.PolicyRef{PolicyID: "policy-pro", Version: 1},
		Validity:       entitlement.ValidityInput{Rule: entitlement.ValidityFixedDuration, Duration: time.Hour},
		Source:         entitlement.SourceRef{Type: entitlement.SourceTrial, SourceID: "trial-a", SourceEffectID: "trial-effect"},
		IdempotencyKey: "idempotency-trial",
		TraceID:        "trace-trial",
	}); err != nil {
		t.Fatalf("trial grant error=%v", err)
	}
	if _, err := service.GrantEntitlement(ctx, entitlement.GrantEntitlementCommand{
		Admin:          admin,
		User:           user,
		Policy:         entitlement.PolicyRef{PolicyID: "policy-pro", Version: 1},
		Validity:       entitlement.ValidityInput{Rule: entitlement.ValidityFixedDuration, Duration: 2 * time.Hour},
		Source:         entitlement.SourceRef{Type: entitlement.SourceGift, SourceID: "gift-a", SourceEffectID: "gift-effect"},
		IdempotencyKey: "idempotency-gift",
		TraceID:        "trace-gift",
	}); err != nil {
		t.Fatalf("gift grant error=%v", err)
	}
	revoke, err := service.RevokeEntitlement(ctx, entitlement.MutateEntitlementCommand{
		Admin:            admin,
		User:             user,
		Source:           entitlement.SourceRef{Type: entitlement.SourceTrial, SourceID: "trial-a", SourceEffectID: "trial-effect"},
		IdempotencyKey:   "idempotency-revoke-trial",
		ExpectedRevision: 2,
		TraceID:          "trace-revoke-trial",
	})
	if err != nil || revoke.Revision != 3 || !revoke.Decision.Allowed {
		t.Fatalf("source revoke=%+v error=%v", revoke, err)
	}
	decision, err := service.CheckEntitlement(ctx, entitlement.CheckEntitlementCommand{
		Product:           entitlement.ProductContext{ProductID: "product-a"},
		Tenant:            entitlement.TenantContext{ProductID: "product-a", TenantID: "tenant-a"},
		User:              user,
		RequestedFeatures: []string{"pro.member"},
	})
	if err != nil || !decision.Allowed || decision.Revision != 3 {
		t.Fatalf("decision after source revoke=%+v error=%v", decision, err)
	}
	var activeSources int
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM entitlement.grants g
		WHERE g.product_id='product-a' AND g.tenant_id='tenant-a' AND g.user_id='user-a'
		  AND g.effect IN ('grant','extend','replace')
		  AND NOT EXISTS (
		    SELECT 1 FROM entitlement.grants r
		    WHERE r.product_id=g.product_id AND r.tenant_id=g.tenant_id AND r.user_id=g.user_id
		      AND r.effect='revoke'
		      AND (r.source_id=g.grant_id OR (r.source_type=g.source_type AND r.source_id=g.source_id AND r.source_effect_id=g.source_effect_id))
		  )`).Scan(&activeSources); err != nil {
		t.Fatal(err)
	}
	if activeSources != 1 {
		t.Fatalf("active sources after source tuple revoke=%d, want 1", activeSources)
	}
}

func TestRepositoryG2B05ST039LifecycleStackingConcurrencyExpiryAndIsolation(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	seedPolicy(t, database, "product-a", "tenant-a", "policy-pro", "pro", 1, "pro.member")
	seedPolicy(t, database, "product-b", "tenant-a", "policy-pro-b", "pro", 1, "pro.member")
	seedPolicy(t, database, "product-a", "tenant-b", "policy-pro-tenant-b", "pro", 1, "pro.member")

	now := fixedNow()
	service := entitlement.NewService(entitlementpostgres.New(database.Pool), &sequenceIDs{}, digestKey, func() time.Time { return now })
	admin := entitlement.AdminScope{AdminID: "admin-a", ProductID: "product-a", TenantID: "tenant-a"}
	user := entitlement.UserContext{UserID: "user-a"}
	duplicate := entitlement.GrantEntitlementCommand{
		Admin:          admin,
		User:           user,
		Policy:         entitlement.PolicyRef{PolicyID: "policy-pro", Version: 1},
		Validity:       entitlement.ValidityInput{Rule: entitlement.ValidityFixedDuration, Duration: time.Hour},
		Source:         entitlement.SourceRef{Type: entitlement.SourceOrder, SourceID: "order-a", SourceEffectID: "line-pro"},
		IdempotencyKey: "idempotency-g2b05-concurrent",
		TraceID:        "trace-g2b05-concurrent",
	}

	const concurrentAttempts = 3
	var wg sync.WaitGroup
	results := make([]entitlement.GrantResult, concurrentAttempts)
	errs := make([]error, concurrentAttempts)
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], errs[index] = service.GrantEntitlement(ctx, duplicate)
		}(i)
	}
	wg.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent duplicate grant %d error=%v", index, err)
		}
		if results[index].Revision != 1 || results[index].GrantID == "" {
			t.Fatalf("concurrent duplicate grant %d result=%+v", index, results[index])
		}
		if results[index].GrantID != results[0].GrantID || results[index].AuditID != results[0].AuditID {
			t.Fatalf("concurrent duplicate grant %d produced a different fact: got=%+v first=%+v", index, results[index], results[0])
		}
	}

	gift, err := service.GrantEntitlement(ctx, entitlement.GrantEntitlementCommand{
		Admin:          admin,
		User:           user,
		Policy:         entitlement.PolicyRef{PolicyID: "policy-pro", Version: 1},
		Validity:       entitlement.ValidityInput{Rule: entitlement.ValidityFixedDuration, Duration: 2 * time.Hour},
		Source:         entitlement.SourceRef{Type: entitlement.SourceGift, SourceID: "gift-a", SourceEffectID: "line-pro"},
		IdempotencyKey: "idempotency-g2b05-gift",
		TraceID:        "trace-g2b05-gift",
	})
	if err != nil || gift.Revision != 2 {
		t.Fatalf("second source gift=%+v error=%v", gift, err)
	}
	decision, err := service.CheckEntitlement(ctx, entitlement.CheckEntitlementCommand{
		Product:           entitlement.ProductContext{ProductID: "product-a"},
		Tenant:            entitlement.TenantContext{ProductID: "product-a", TenantID: "tenant-a"},
		User:              user,
		RequestedFeatures: []string{"pro.member"},
	})
	if err != nil || !decision.Allowed || decision.Revision != 2 || decision.PlanCode != "pro" {
		t.Fatalf("stacked decision=%+v error=%v", decision, err)
	}

	revoke, err := service.RevokeEntitlement(ctx, entitlement.MutateEntitlementCommand{
		Admin:            admin,
		User:             user,
		Source:           entitlement.SourceRef{Type: entitlement.SourceOrder, SourceID: "order-a", SourceEffectID: "line-pro"},
		IdempotencyKey:   "idempotency-g2b05-revoke-order",
		ExpectedRevision: 2,
		TraceID:          "trace-g2b05-revoke-order",
	})
	if err != nil || revoke.Revision != 3 || !revoke.Decision.Allowed {
		t.Fatalf("source revoke=%+v error=%v", revoke, err)
	}
	decision, err = service.CheckEntitlement(ctx, entitlement.CheckEntitlementCommand{
		Product:           entitlement.ProductContext{ProductID: "product-a"},
		Tenant:            entitlement.TenantContext{ProductID: "product-a", TenantID: "tenant-a"},
		User:              user,
		RequestedFeatures: []string{"pro.member"},
	})
	if err != nil || !decision.Allowed || decision.Revision != 3 {
		t.Fatalf("decision after one source revoke=%+v error=%v", decision, err)
	}

	now = fixedNow().Add(3 * time.Hour)
	expired, err := service.CheckEntitlement(ctx, entitlement.CheckEntitlementCommand{
		Product:           entitlement.ProductContext{ProductID: "product-a"},
		Tenant:            entitlement.TenantContext{ProductID: "product-a", TenantID: "tenant-a"},
		User:              user,
		RequestedFeatures: []string{"pro.member"},
	})
	if err != nil || expired.Allowed || expired.ReasonCode != entitlement.ReasonEntitlementExpired || expired.Revision != 3 {
		t.Fatalf("expired decision=%+v error=%v", expired, err)
	}
	otherProduct, err := service.CheckEntitlement(ctx, entitlement.CheckEntitlementCommand{
		Product:           entitlement.ProductContext{ProductID: "product-b"},
		Tenant:            entitlement.TenantContext{ProductID: "product-b", TenantID: "tenant-a"},
		User:              user,
		RequestedFeatures: []string{"pro.member"},
	})
	if err != nil || otherProduct.Allowed || otherProduct.ReasonCode != entitlement.ReasonEntitlementRequired || otherProduct.Revision != 0 {
		t.Fatalf("other product decision=%+v error=%v", otherProduct, err)
	}
	otherTenant, err := service.CheckEntitlement(ctx, entitlement.CheckEntitlementCommand{
		Product:           entitlement.ProductContext{ProductID: "product-a"},
		Tenant:            entitlement.TenantContext{ProductID: "product-a", TenantID: "tenant-b"},
		User:              user,
		RequestedFeatures: []string{"pro.member"},
	})
	if err != nil || otherTenant.Allowed || otherTenant.ReasonCode != entitlement.ReasonEntitlementRequired || otherTenant.Revision != 0 {
		t.Fatalf("other tenant decision=%+v error=%v", otherTenant, err)
	}

	var grantEffects, revokeEffects, ledgerEntries int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM entitlement.grants WHERE product_id='product-a' AND tenant_id='tenant-a' AND user_id='user-a' AND effect IN ('grant','extend','replace')`).Scan(&grantEffects); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM entitlement.grants WHERE product_id='product-a' AND tenant_id='tenant-a' AND user_id='user-a' AND effect='revoke'`).Scan(&revokeEffects); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM entitlement.ledger WHERE product_id='product-a' AND tenant_id='tenant-a' AND user_id='user-a'`).Scan(&ledgerEntries); err != nil {
		t.Fatal(err)
	}
	if grantEffects != 2 || revokeEffects != 1 || ledgerEntries != 3 {
		t.Fatalf("effect/ledger counts grant=%d revoke=%d ledger=%d, want 2/1/3", grantEffects, revokeEffects, ledgerEntries)
	}
}

type policySeed struct {
	ProductID            string
	TenantID             string
	PolicyID             string
	PolicyCode           string
	Version              int64
	FeatureCode          string
	FeatureValue         any
	StackingRule         string
	MutualExclusionGroup string
	Priority             int
}

func seedPolicy(t *testing.T, database testpostgres.Database, productID, tenantID, policyID, policyCode string, version int64, featureCode string) {
	t.Helper()
	seedPolicyEx(t, database, policySeed{ProductID: productID, TenantID: tenantID, PolicyID: policyID, PolicyCode: policyCode, Version: version, FeatureCode: featureCode, FeatureValue: true, StackingRule: "union_latest_expiry"})
}

func seedPolicyEx(t *testing.T, database testpostgres.Database, seed policySeed) {
	t.Helper()
	now := fixedNow()
	if _, err := database.Pool.Exec(context.Background(), `
		INSERT INTO entitlement.features(feature_id, product_id, feature_code, kind, display_name, status, created_at)
		VALUES ($1, $2, $3, 'boolean', $3, 'active', $4)
		ON CONFLICT DO NOTHING
	`, "feature-"+seed.PolicyID, seed.ProductID, seed.FeatureCode, now); err != nil {
		t.Fatalf("seed feature: %v", err)
	}
	stackingRule := seed.StackingRule
	if stackingRule == "" {
		stackingRule = "union_latest_expiry"
	}
	var group any
	if seed.MutualExclusionGroup != "" {
		group = seed.MutualExclusionGroup
	}
	if _, err := database.Pool.Exec(context.Background(), `
		INSERT INTO entitlement.policies(
			policy_id, product_id, tenant_id, policy_code, version, status, features,
			validity_rule, validity_seconds, stacking_rule, mutual_exclusion_group, priority, revoke_scope,
			offline_grace_max_seconds, published_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'active',
		          jsonb_build_array(jsonb_build_object('feature_code', $6::text, 'value', $7::jsonb)),
		          'fixed_duration', 3600, $8, $9, $10, 'source_only',
		          300, $11, $11, $11)
	`, seed.PolicyID, seed.ProductID, seed.TenantID, seed.PolicyCode, seed.Version, seed.FeatureCode, jsonValue(t, seed.FeatureValue), stackingRule, group, seed.Priority, now); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
}

func jsonValue(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func fixedNow() time.Time { return time.Date(2026, 7, 23, 9, 0, 0, 123456000, time.UTC) }
