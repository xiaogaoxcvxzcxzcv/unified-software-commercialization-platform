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

type sequenceIDs struct{ n int }

func (s *sequenceIDs) ID(prefix string) (string, error) {
	s.n++
	return prefix + string(rune('0'+s.n)), nil
}

func TestServiceBuildsHMACDigestsAndTrustedScopeRecord(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository, &sequenceIDs{}, []byte("0123456789abcdef0123456789abcdef"), fixedNow)
	command := SetTenantAccessStatusCommand{Product: ProductContext{ProductID: "product-a"}, Tenant: TenantContext{ProductID: "product-a", TenantID: "tenant-a"}, User: UserContext{UserID: "user-a"}, Status: StatusSuspended, ExpectedVersion: 3, ReasonCode: "security.review", OperatorNote: "private operator note", IdempotencyKey: "idempotency-key-0001"}
	result, err := service.SetTenantAccessStatus(context.Background(), command)
	if err != nil || result.AccessVersion != 4 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if repository.record.ScopeType != ScopeTenant || repository.record.ProductID != "product-a" || repository.record.TenantID != "tenant-a" || len(repository.record.KeyDigest) != 32 || len(repository.record.RequestDigest) != 32 || repository.record.StatusEventID == repository.record.RevocationEventID {
		t.Fatalf("record=%+v", repository.record)
	}
	firstKey, firstRequest := append([]byte(nil), repository.record.KeyDigest...), append([]byte(nil), repository.record.RequestDigest...)
	_, err = service.SetTenantAccessStatus(context.Background(), command)
	if err != nil || string(firstKey) != string(repository.record.KeyDigest) || string(firstRequest) != string(repository.record.RequestDigest) {
		t.Fatalf("digests are not stable: %v", err)
	}
	changed := command
	changed.OperatorNote = "different"
	_, _ = service.SetTenantAccessStatus(context.Background(), changed)
	if string(firstRequest) == string(repository.record.RequestDigest) {
		t.Fatal("request digest ignored operator_note")
	}
}

func TestServiceRejectsScopeAndOperatorNote(t *testing.T) {
	service := NewService(&repositoryStub{}, &sequenceIDs{}, []byte("0123456789abcdef0123456789abcdef"), fixedNow)
	base := SetTenantAccessStatusCommand{Product: ProductContext{ProductID: "product-a"}, Tenant: TenantContext{ProductID: "product-b", TenantID: "tenant-a"}, User: UserContext{UserID: "user-a"}, Status: StatusActive, ReasonCode: "manual.review", IdempotencyKey: "idempotency-key-0001"}
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

func resultFor(record ChangeRecord, version int64) StatusChangeResult {
	return StatusChangeResult{ScopeType: record.ScopeType, ProductID: record.ProductID, TenantID: record.TenantID, UserID: record.UserID, Status: record.Status, AccessVersion: version}
}
func fixedNow() time.Time { return time.Date(2026, 7, 17, 8, 0, 0, 123456000, time.UTC) }
