package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
	accesspostgres "platform.local/capability-platform/backend/internal/modules/productuseraccess/postgres"
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

type constantIDs struct{}

func (constantIDs) ID(prefix string) (string, error) { return prefix + "duplicate", nil }

func TestRepositoryAdmissionDefaultsAndTransactionalStatusEvents(t *testing.T) {
	database := testpostgres.Open(t)
	service := productuseraccess.NewService(accesspostgres.New(database.Pool), &sequenceIDs{}, digestKey, fixedNow)
	ctx := context.Background()
	product := productuseraccess.ProductContext{ProductID: "product-a"}
	user := productuseraccess.UserContext{UserID: "user-a"}
	admission, err := service.EvaluateScopedAdmission(ctx, product, nil, user)
	if err != nil || !admission.Allowed || admission.ProductStatus != productuseraccess.StatusActive || admission.ProductVersion != 0 {
		t.Fatalf("default admission=%+v error=%v", admission, err)
	}
	command := productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusSuspended, ExpectedVersion: 0, ReasonCode: "security.review", OperatorNote: "private note", IdempotencyKey: "idempotency-key-0001"}
	first, err := service.SetProductAccessStatus(ctx, command)
	if err != nil || first.AccessVersion != 1 {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	replay, err := service.SetProductAccessStatus(ctx, command)
	if err != nil || replay != first {
		t.Fatalf("replay=%+v error=%v", replay, err)
	}
	changed := command
	changed.ReasonCode = "manual.review"
	if _, err := service.SetProductAccessStatus(ctx, changed); !errors.Is(err, productuseraccess.ErrConflict) {
		t.Fatalf("changed key error=%v", err)
	}
	admission, err = service.EvaluateScopedAdmission(ctx, product, nil, user)
	if err != nil || admission.Allowed || admission.Code != "PRODUCT_USER_ACCESS_SUSPENDED" || admission.ProductVersion != 1 {
		t.Fatalf("suspended admission=%+v error=%v", admission, err)
	}
	var note string
	var events, idempotency int
	var unsafePayloads int
	if err := database.Pool.QueryRow(ctx, `SELECT operator_note FROM product_user_access.product_access WHERE product_id=$1 AND user_id=$2`, product.ProductID, user.UserID).Scan(&note); err != nil || note != "private note" {
		t.Fatalf("note=%q error=%v", note, err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.outbox_events WHERE aggregate_id<>''`).Scan(&events); err != nil || events != 2 {
		t.Fatalf("events=%d error=%v", events, err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.idempotency_records WHERE octet_length(key_digest)=32 AND octet_length(request_digest)=32 AND state='completed'`).Scan(&idempotency); err != nil || idempotency != 1 {
		t.Fatalf("idempotency=%d error=%v", idempotency, err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.outbox_events WHERE payload ? 'operator_note' OR payload ? 'key_digest' OR payload ? 'request_digest' OR payload::text ILIKE '%private note%'`).Scan(&unsafePayloads); err != nil || unsafePayloads != 0 {
		t.Fatalf("unsafe payloads=%d error=%v", unsafePayloads, err)
	}
	activate := productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusActive, ExpectedVersion: 1, ReasonCode: "manual.restore", IdempotencyKey: "idempotency-key-0002"}
	second, err := service.SetProductAccessStatus(ctx, activate)
	if err != nil || second.AccessVersion != 2 {
		t.Fatalf("activate=%+v error=%v", second, err)
	}
	if _, err := service.SetProductAccessStatus(ctx, productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusSuspended, ExpectedVersion: 1, ReasonCode: "security.review", IdempotencyKey: "idempotency-key-0003"}); !errors.Is(err, productuseraccess.ErrConflict) {
		t.Fatalf("stale version error=%v", err)
	}
	changedConflict := productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusSuspended, ExpectedVersion: 2, ReasonCode: "security.review", IdempotencyKey: "idempotency-key-0003"}
	if _, err := service.SetProductAccessStatus(ctx, changedConflict); !errors.Is(err, productuseraccess.ErrConflict) {
		t.Fatalf("terminal conflict key accepted changed request: %v", err)
	}
	noChange := productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusActive, ExpectedVersion: 2, ReasonCode: "manual.restore", IdempotencyKey: "idempotency-key-0004"}
	unchanged, err := service.SetProductAccessStatus(ctx, noChange)
	if err != nil || unchanged.AccessVersion != 2 {
		t.Fatalf("same-status result=%+v error=%v", unchanged, err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.outbox_events`).Scan(&events); err != nil || events != 3 {
		t.Fatalf("same-status emitted event: events=%d error=%v", events, err)
	}
}

func TestRepositoryTenantScopePrecedenceAndLists(t *testing.T) {
	database := testpostgres.Open(t)
	service := productuseraccess.NewService(accesspostgres.New(database.Pool), &sequenceIDs{}, digestKey, fixedNow)
	ctx := context.Background()
	product := productuseraccess.ProductContext{ProductID: "product-a"}
	tenant := productuseraccess.TenantContext{ProductID: "product-a", TenantID: "tenant-a"}
	userA := productuseraccess.UserContext{UserID: "user-a"}
	userB := productuseraccess.UserContext{UserID: "user-b"}
	for index, user := range []productuseraccess.UserContext{userA, userB} {
		_, err := service.SetProductAccessStatus(ctx, productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusActive, ExpectedVersion: 0, ReasonCode: "scope.seed", IdempotencyKey: fmt.Sprintf("idempotency-product-%04d", index)})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := service.SetTenantAccessStatus(ctx, productuseraccess.SetTenantAccessStatusCommand{Product: product, Tenant: tenant, User: userA, Status: productuseraccess.StatusSuspended, ExpectedVersion: 0, ReasonCode: "tenant.review", IdempotencyKey: "idempotency-tenant-0001"})
	if err != nil {
		t.Fatal(err)
	}
	admission, err := service.EvaluateScopedAdmission(ctx, product, &tenant, userA)
	if err != nil || admission.Allowed || admission.Code != "TENANT_USER_ACCESS_SUSPENDED" || admission.TenantVersion != 1 {
		t.Fatalf("tenant admission=%+v error=%v", admission, err)
	}
	otherTenant := productuseraccess.TenantContext{ProductID: "product-a", TenantID: "tenant-b"}
	admission, err = service.EvaluateScopedAdmission(ctx, product, &otherTenant, userA)
	if err != nil || !admission.Allowed {
		t.Fatalf("other tenant admission=%+v error=%v", admission, err)
	}
	productUsers, err := service.ListScopedUserIDs(ctx, productuseraccess.ListScopedUserIDsQuery{Product: product})
	if err != nil || len(productUsers) != 2 || productUsers[0] != "user-a" || productUsers[1] != "user-b" {
		t.Fatalf("product users=%v error=%v", productUsers, err)
	}
	tenantUsers, err := service.ListScopedUserIDs(ctx, productuseraccess.ListScopedUserIDsQuery{Product: product, Tenant: &tenant})
	if err != nil || len(tenantUsers) != 1 || tenantUsers[0] != "user-a" {
		t.Fatalf("tenant users=%v error=%v", tenantUsers, err)
	}
}

func TestRepositoryConcurrentExpectedVersionAndOutboxFailureRollback(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()
	product := productuseraccess.ProductContext{ProductID: "product-a"}
	user := productuseraccess.UserContext{UserID: "user-a"}
	service := productuseraccess.NewService(accesspostgres.New(database.Pool), &sequenceIDs{}, digestKey, fixedNow)
	start := make(chan struct{})
	results := make(chan error, 2)
	for index := range 2 {
		go func(index int) {
			<-start
			_, err := service.SetProductAccessStatus(ctx, productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusSuspended, ExpectedVersion: 0, ReasonCode: "security.review", IdempotencyKey: fmt.Sprintf("idempotency-concurrent-%04d", index)})
			results <- err
		}(index)
	}
	close(start)
	first, second := <-results, <-results
	successes, conflicts := 0, 0
	for _, err := range []error{first, second} {
		if err == nil {
			successes++
		} else if errors.Is(err, productuseraccess.ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected error=%v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes/conflicts=%d/%d", successes, conflicts)
	}

	rollbackService := productuseraccess.NewService(accesspostgres.New(database.Pool), constantIDs{}, digestKey, fixedNow)
	_, err := rollbackService.SetProductAccessStatus(ctx, productuseraccess.SetProductAccessStatusCommand{Product: productuseraccess.ProductContext{ProductID: "product-b"}, User: user, Status: productuseraccess.StatusSuspended, ExpectedVersion: 0, ReasonCode: "security.review", IdempotencyKey: "idempotency-rollback-0001"})
	if err == nil {
		t.Fatal("duplicate outbox event ids unexpectedly succeeded")
	}
	var facts, events, idem int
	_ = database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.product_access WHERE product_id='product-b'`).Scan(&facts)
	_ = database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.outbox_events WHERE aggregate_id LIKE 'access_%'`).Scan(&events)
	_ = database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.idempotency_records WHERE product_id='product-b'`).Scan(&idem)
	if facts != 0 || idem != 0 {
		t.Fatalf("rollback left facts/idempotency=%d/%d (events total=%d)", facts, idem, events)
	}
}

func fixedNow() time.Time { return time.Date(2026, 7, 17, 8, 0, 0, 123456000, time.UTC) }
