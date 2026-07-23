package entitlement

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestServiceGrantEntitlementBuildsStableWriteRecord(t *testing.T) {
	repo := &fakeRepository{grantResult: GrantResult{EntitlementID: "entitlement-a", GrantID: "grant-result-a", Revision: 1, AuditID: "audit-result"}}
	ids := &sequenceIDs{}
	now := time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC)
	service := NewService(repo, ids, []byte("01234567890123456789012345678901"), func() time.Time { return now })

	result, err := service.GrantEntitlement(context.Background(), GrantEntitlementCommand{
		Admin:          AdminScope{AdminID: "admin-a", ProductID: "product-a", TenantID: "tenant-a"},
		User:           UserContext{UserID: "user-a"},
		Policy:         PolicyRef{PolicyID: "policy-a", Version: 2},
		Validity:       ValidityInput{Rule: ValidityFixedDuration, Duration: time.Hour},
		Source:         SourceRef{Type: SourceAdmin, SourceID: "manual-a", SourceEffectID: "effect-a"},
		IdempotencyKey: "idem-key-0000001",
		TraceID:        "trace-a",
	})
	if err != nil {
		t.Fatalf("GrantEntitlement() error = %v", err)
	}
	if result.EntitlementID != "entitlement-a" {
		t.Fatalf("result entitlement id = %q", result.EntitlementID)
	}
	record := repo.lastWrite
	if record.Operation != EffectGrant || record.ProductID != "product-a" || record.TenantID != "tenant-a" || record.UserID != "user-a" {
		t.Fatalf("unexpected scope record: %+v", record)
	}
	if record.PolicyID != "policy-a" || record.PolicyVersion != 2 || record.Source.Type != SourceAdmin {
		t.Fatalf("unexpected policy/source record: %+v", record)
	}
	if record.ReasonCode != ReasonManualGrant || record.Actor.Type != ActorAdmin || record.Actor.ID != "admin-a" {
		t.Fatalf("unexpected actor/reason record: %+v", record)
	}
	if len(record.KeyHash) != 32 || len(record.RequestHash) != 32 || record.AuditID == "" {
		t.Fatalf("missing digests/audit: key=%d request=%d audit=%q", len(record.KeyHash), len(record.RequestHash), record.AuditID)
	}
	if record.GrantID != "entitlement_grant_1" || record.LedgerID != "entitlement_ledger_2" || record.OutboxEventID != "entitlement_event_3" {
		t.Fatalf("unexpected generated ids: %+v", record)
	}
	if !record.Now.Equal(now) {
		t.Fatalf("record time = %v, want %v", record.Now, now)
	}
}

func TestServiceRejectsInvalidGrantBeforeRepository(t *testing.T) {
	repo := &fakeRepository{}
	service := NewService(repo, &sequenceIDs{}, []byte("01234567890123456789012345678901"), nil)
	_, err := service.GrantEntitlement(context.Background(), GrantEntitlementCommand{
		Admin:          AdminScope{AdminID: "admin-a", ProductID: "product-a", TenantID: "tenant-a"},
		User:           UserContext{UserID: "user-a"},
		Policy:         PolicyRef{PolicyID: "policy-a", Version: 1},
		Validity:       ValidityInput{Rule: ValidityFixedDuration},
		Source:         SourceRef{Type: SourceAdmin, SourceID: "manual-a", SourceEffectID: "effect-a"},
		IdempotencyKey: "idem-key-0000001",
		TraceID:        "trace-a",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("GrantEntitlement() error = %v, want ErrInvalidArgument", err)
	}
	if repo.grantCalls != 0 {
		t.Fatalf("repository was called for invalid grant")
	}
}

func TestServiceRequiresExpectedRevisionForMutations(t *testing.T) {
	repo := &fakeRepository{}
	service := NewService(repo, &sequenceIDs{}, []byte("01234567890123456789012345678901"), nil)
	_, err := service.ExtendEntitlement(context.Background(), MutateEntitlementCommand{
		Admin:          AdminScope{AdminID: "admin-a", ProductID: "product-a", TenantID: "tenant-a"},
		User:           UserContext{UserID: "user-a"},
		Policy:         PolicyRef{PolicyID: "policy-a", Version: 1},
		Validity:       ValidityInput{Rule: ValidityFixedDuration, Duration: time.Hour},
		Source:         SourceRef{Type: SourceAdmin, SourceID: "manual-a", SourceEffectID: "effect-a"},
		IdempotencyKey: "idem-key-0000001",
		TraceID:        "trace-a",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ExtendEntitlement() error = %v, want ErrInvalidArgument", err)
	}
}

func TestServiceCheckEntitlementValidatesTrustedScopeAndFeatures(t *testing.T) {
	now := time.Date(2026, 7, 23, 2, 0, 0, 0, time.UTC)
	repo := &fakeRepository{decision: CheckDecision{Allowed: true, Revision: 7, Features: map[string]any{"pro.member": true}}}
	service := NewService(repo, &sequenceIDs{}, []byte("01234567890123456789012345678901"), func() time.Time { return now })

	decision, err := service.CheckEntitlement(context.Background(), CheckEntitlementCommand{
		Product:           ProductContext{ProductID: "product-a"},
		Tenant:            TenantContext{ProductID: "product-a", TenantID: "tenant-a"},
		User:              UserContext{UserID: "user-a"},
		RequestedFeatures: []string{"pro.member"},
	})
	if err != nil {
		t.Fatalf("CheckEntitlement() error = %v", err)
	}
	if !decision.Allowed || decision.DecisionStage != "entitlement" || !decision.ServerTime.Equal(now) {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	if repo.lastCheck.ProductID != "product-a" || repo.lastCheck.TenantID != "tenant-a" || repo.lastCheck.UserID != "user-a" || repo.lastCheck.RequestedFeatures[0] != "pro.member" {
		t.Fatalf("unexpected check query: %+v", repo.lastCheck)
	}

	_, err = service.CheckEntitlement(context.Background(), CheckEntitlementCommand{
		Product:           ProductContext{ProductID: "product-a"},
		Tenant:            TenantContext{ProductID: "product-b", TenantID: "tenant-a"},
		User:              UserContext{UserID: "user-a"},
		RequestedFeatures: []string{"pro.member"},
	})
	if !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("scope mismatch check error = %v, want ErrScopeMismatch", err)
	}
}

type sequenceIDs struct{ next int }

func (s *sequenceIDs) ID(prefix string) (string, error) {
	s.next++
	return prefix + strconv.Itoa(s.next), nil
}

type fakeRepository struct {
	grantCalls  int
	lastWrite   WriteRecord
	lastCheck   CheckQuery
	decision    CheckDecision
	grantResult GrantResult
}

func (f *fakeRepository) CheckEntitlement(_ context.Context, query CheckQuery) (CheckDecision, error) {
	f.lastCheck = query
	return f.decision, nil
}

func (f *fakeRepository) GrantEntitlement(_ context.Context, record WriteRecord) (GrantResult, error) {
	f.grantCalls++
	f.lastWrite = record
	return f.grantResult, nil
}

func (f *fakeRepository) ExtendEntitlement(context.Context, WriteRecord) (GrantResult, error) {
	return GrantResult{}, nil
}

func (f *fakeRepository) ReplaceEntitlement(context.Context, WriteRecord) (GrantResult, error) {
	return GrantResult{}, nil
}

func (f *fakeRepository) RevokeEntitlement(context.Context, WriteRecord) (GrantResult, error) {
	return GrantResult{}, nil
}

func (f *fakeRepository) GetCurrentEntitlements(context.Context, CurrentQuery) (EntitlementSummary, error) {
	return EntitlementSummary{}, nil
}

func (f *fakeRepository) ListHistory(context.Context, HistoryQuery) ([]LedgerEntry, error) {
	return nil, nil
}

func (f *fakeRepository) ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error) {
	return nil, nil
}

func (f *fakeRepository) MarkOutboxPublished(context.Context, string, time.Time) error { return nil }

func (f *fakeRepository) MarkOutboxFailed(context.Context, string, string, time.Time, bool) error {
	return nil
}
