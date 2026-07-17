package productuseraccess

import (
	"context"
	"errors"
	"testing"
	"time"
)

type repositoryStub struct {
	record    ChangeRecord
	admission Admission
	users     []string
	claimed   []ClaimedOutboxEvent
	failed    string
}

func (r *repositoryStub) EvaluateScopedAdmission(context.Context, string, string, string) (Admission, error) {
	return r.admission, nil
}
func (r *repositoryStub) SetProductAccessStatus(_ context.Context, record ChangeRecord) (StatusChangeResult, error) {
	r.record = record
	return resultFor(record, record.ExpectedVersion+1), nil
}
func (r *repositoryStub) SetTenantAccessStatus(_ context.Context, record ChangeRecord) (StatusChangeResult, error) {
	r.record = record
	return resultFor(record, record.ExpectedVersion+1), nil
}
func (r *repositoryStub) ListScopedUserIDs(context.Context, string, string) ([]string, error) {
	return r.users, nil
}
func (r *repositoryStub) ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error) {
	return r.claimed, nil
}
func (r *repositoryStub) MarkOutboxPublished(context.Context, string, time.Time) error { return nil }
func (r *repositoryStub) MarkOutboxFailed(_ context.Context, _ string, summary string, _ time.Time, _ bool) error {
	r.failed = summary
	return nil
}

type sequenceIDs struct{ n int }

func (s *sequenceIDs) ID(prefix string) (string, error) {
	s.n++
	return prefix + string(rune('0'+s.n)), nil
}

func TestServiceBuildsHMACDigestsAndTrustedScopeRecord(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository, &sequenceIDs{}, []byte("0123456789abcdef0123456789abcdef"), fixedNow)
	command := SetTenantAccessStatusCommand{Product: ProductContext{ProductID: "product-a"}, Tenant: TenantContext{ProductID: "product-a", TenantID: "tenant-a"}, User: UserContext{UserID: "user-a"}, Status: StatusSuspended, ExpectedVersion: 3, ReasonCode: "security.review", OperatorNote: "private operator note", IdempotencyKey: "idempotency-key-0001", ActorID: "admin-a", TraceID: "trace-0001"}
	result, err := service.SetTenantAccessStatus(context.Background(), command)
	if err != nil || result.AccessVersion != 4 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if repository.record.ScopeType != ScopeTenant || repository.record.ProductID != "product-a" || repository.record.TenantID != "tenant-a" || len(repository.record.KeyDigest) != 32 || len(repository.record.RequestDigest) != 32 || repository.record.StatusEventID == repository.record.RevocationEventID || repository.record.ActorID != "admin-a" || repository.record.TraceID != "trace-0001" || repository.record.AuditID == "" || result.AuditID != repository.record.AuditID {
		t.Fatalf("record=%+v", repository.record)
	}
	firstKey, firstRequest, firstAudit := append([]byte(nil), repository.record.KeyDigest...), append([]byte(nil), repository.record.RequestDigest...), repository.record.AuditID
	_, err = service.SetTenantAccessStatus(context.Background(), command)
	if err != nil || string(firstKey) != string(repository.record.KeyDigest) || string(firstRequest) != string(repository.record.RequestDigest) || firstAudit != repository.record.AuditID {
		t.Fatalf("digests are not stable: %v", err)
	}
	changed := command
	changed.OperatorNote = "different"
	_, _ = service.SetTenantAccessStatus(context.Background(), changed)
	if string(firstRequest) == string(repository.record.RequestDigest) {
		t.Fatal("request digest ignored operator_note")
	}
	otherScope := command
	otherScope.Tenant.TenantID = "tenant-b"
	_, _ = service.SetTenantAccessStatus(context.Background(), otherScope)
	if repository.record.AuditID == firstAudit {
		t.Fatal("stable audit ID collided across tenant scopes")
	}
}

func TestServiceRejectsScopeAndOperatorNote(t *testing.T) {
	service := NewService(&repositoryStub{}, &sequenceIDs{}, []byte("0123456789abcdef0123456789abcdef"), fixedNow)
	base := SetTenantAccessStatusCommand{Product: ProductContext{ProductID: "product-a"}, Tenant: TenantContext{ProductID: "product-b", TenantID: "tenant-a"}, User: UserContext{UserID: "user-a"}, Status: StatusActive, ReasonCode: "manual.review", IdempotencyKey: "idempotency-key-0001", ActorID: "admin-a", TraceID: "trace-0001"}
	if _, err := service.SetTenantAccessStatus(context.Background(), base); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("scope error=%v", err)
	}
	base.Tenant.ProductID = "product-a"
	base.OperatorNote = "unsafe\nvalue"
	if _, err := service.SetTenantAccessStatus(context.Background(), base); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("note error=%v", err)
	}
	base.OperatorNote = ""
	for range 501 {
		base.OperatorNote += "界"
	}
	if _, err := service.SetTenantAccessStatus(context.Background(), base); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("long note error=%v", err)
	}
	base.OperatorNote = ""
	base.ActorID = ""
	if _, err := service.SetTenantAccessStatus(context.Background(), base); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("missing actor error=%v", err)
	}
	base.ActorID, base.TraceID = "admin-a", ""
	if _, err := service.SetTenantAccessStatus(context.Background(), base); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("missing trace error=%v", err)
	}
}

func TestServiceValidatesAdmissionAndListContexts(t *testing.T) {
	repository := &repositoryStub{admission: Admission{Allowed: true, ProductStatus: StatusActive}, users: []string{"user-a"}}
	service := NewService(repository, &sequenceIDs{}, []byte("0123456789abcdef0123456789abcdef"), fixedNow)
	wrong := &TenantContext{ProductID: "product-b", TenantID: "tenant-a"}
	if _, err := service.EvaluateScopedAdmission(context.Background(), ProductContext{ProductID: "product-a"}, wrong, UserContext{UserID: "user-a"}); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("admission scope error=%v", err)
	}
	if _, err := service.ListScopedUserIDs(context.Background(), ListScopedUserIDsQuery{Product: ProductContext{ProductID: "product-a"}, Tenant: wrong}); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("list scope error=%v", err)
	}
}

func TestServiceValidatesOutboxDeliveryAndBoundsFailureSummary(t *testing.T) {
	repository := &repositoryStub{claimed: []ClaimedOutboxEvent{{EventID: "event-a", AttemptCount: 1}}}
	service := NewService(repository, &sequenceIDs{}, []byte("0123456789abcdef0123456789abcdef"), fixedNow)
	claimed, err := service.ClaimOutbox(context.Background(), 1)
	if err != nil || len(claimed) != 1 || claimed[0].EventID != "event-a" {
		t.Fatalf("claimed=%+v error=%v", claimed, err)
	}
	if _, err := service.ClaimOutbox(context.Background(), 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid claim limit error=%v", err)
	}
	if err := service.MarkOutboxPublished(context.Background(), ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty publish event error=%v", err)
	}
	longSummary := ""
	for range 501 {
		longSummary += "x"
	}
	if err := service.MarkOutboxFailed(context.Background(), "event-a", longSummary, fixedNow().Add(time.Minute), false); err != nil {
		t.Fatal(err)
	}
	if len([]rune(repository.failed)) != 500 {
		t.Fatalf("failure summary length=%d", len([]rune(repository.failed)))
	}
}

func resultFor(record ChangeRecord, version int64) StatusChangeResult {
	return StatusChangeResult{ScopeType: record.ScopeType, ProductID: record.ProductID, TenantID: record.TenantID, UserID: record.UserID, Status: record.Status, AccessVersion: version, AuditID: record.AuditID}
}
func fixedNow() time.Time { return time.Date(2026, 7, 17, 8, 0, 0, 123456000, time.UTC) }
