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
	command := productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusSuspended, ExpectedVersion: 0, ReasonCode: "security.review", OperatorNote: "private note", IdempotencyKey: "idempotency-key-0001", ActorID: "admin-a", TraceID: "trace-0001"}
	first, err := service.SetProductAccessStatus(ctx, command)
	if err != nil || first.AccessVersion != 1 || first.AuditID == "" {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	replayCommand := command
	replayCommand.TraceID = "trace-retry-0001"
	replay, err := service.SetProductAccessStatus(ctx, replayCommand)
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
	var storedAuditID string
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
	if err := database.Pool.QueryRow(ctx, `SELECT audit_id FROM product_user_access.idempotency_records WHERE product_id=$1 AND user_id=$2`, product.ProductID, user.UserID).Scan(&storedAuditID); err != nil || storedAuditID != first.AuditID {
		t.Fatalf("stored audit_id=%q first=%q error=%v", storedAuditID, first.AuditID, err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.outbox_events WHERE payload ? 'operator_note' OR payload ? 'key_digest' OR payload ? 'request_digest' OR payload::text ILIKE '%private note%'`).Scan(&unsafePayloads); err != nil || unsafePayloads != 0 {
		t.Fatalf("unsafe payloads=%d error=%v", unsafePayloads, err)
	}
	var auditPayloads int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.outbox_events WHERE payload->>'audit_id'=$1 AND payload->>'actor_id'='admin-a' AND payload->>'trace_id'='trace-0001'`, first.AuditID).Scan(&auditPayloads); err != nil || auditPayloads != 2 {
		t.Fatalf("audit payloads=%d error=%v", auditPayloads, err)
	}
	activate := productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusActive, ExpectedVersion: 1, ReasonCode: "manual.restore", IdempotencyKey: "idempotency-key-0002", ActorID: "admin-a", TraceID: "trace-0002"}
	second, err := service.SetProductAccessStatus(ctx, activate)
	if err != nil || second.AccessVersion != 2 {
		t.Fatalf("activate=%+v error=%v", second, err)
	}
	if _, err := service.SetProductAccessStatus(ctx, productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusSuspended, ExpectedVersion: 1, ReasonCode: "security.review", IdempotencyKey: "idempotency-key-0003", ActorID: "admin-a", TraceID: "trace-0003"}); !errors.Is(err, productuseraccess.ErrConflict) {
		t.Fatalf("stale version error=%v", err)
	}
	changedConflict := productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusSuspended, ExpectedVersion: 2, ReasonCode: "security.review", IdempotencyKey: "idempotency-key-0003", ActorID: "admin-a", TraceID: "trace-0004"}
	if _, err := service.SetProductAccessStatus(ctx, changedConflict); !errors.Is(err, productuseraccess.ErrConflict) {
		t.Fatalf("terminal conflict key accepted changed request: %v", err)
	}
	noChange := productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusActive, ExpectedVersion: 2, ReasonCode: "manual.restore", IdempotencyKey: "idempotency-key-0004", ActorID: "admin-a", TraceID: "trace-0005"}
	unchanged, err := service.SetProductAccessStatus(ctx, noChange)
	if err != nil || unchanged.AccessVersion != 2 {
		t.Fatalf("same-status result=%+v error=%v", unchanged, err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.outbox_events`).Scan(&events); err != nil || events != 4 {
		t.Fatalf("same-status event count=%d error=%v", events, err)
	}
	var auditIntentCount, falseStatusChangeCount int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.outbox_events WHERE event_type='product-user-access.command-audited.v1' AND payload->>'audit_id'=$1 AND payload->>'trace_id'=$2`, unchanged.AuditID, noChange.TraceID).Scan(&auditIntentCount); err != nil || auditIntentCount != 1 {
		t.Fatalf("no-op audit intent count=%d error=%v", auditIntentCount, err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.outbox_events WHERE event_type='product-user-access.status-changed.v1' AND payload->>'trace_id'=$1`, noChange.TraceID).Scan(&falseStatusChangeCount); err != nil || falseStatusChangeCount != 0 {
		t.Fatalf("no-op false status changes=%d error=%v", falseStatusChangeCount, err)
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
		_, err := service.SetProductAccessStatus(ctx, productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusActive, ExpectedVersion: 0, ReasonCode: "scope.seed", IdempotencyKey: fmt.Sprintf("idempotency-product-%04d", index), ActorID: "admin-a", TraceID: fmt.Sprintf("trace-product-%04d", index)})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := service.SetTenantAccessStatus(ctx, productuseraccess.SetTenantAccessStatusCommand{Product: product, Tenant: tenant, User: userA, Status: productuseraccess.StatusSuspended, ExpectedVersion: 0, ReasonCode: "tenant.review", IdempotencyKey: "idempotency-tenant-0001", ActorID: "admin-a", TraceID: "trace-tenant-0001"})
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
	batch, err := service.GetScopedAccessBatch(ctx, productuseraccess.GetScopedAccessBatchQuery{Product: product, Tenant: &tenant, UserIDs: []string{"user-b", "user-a"}})
	if err != nil || len(batch) != 2 || batch[0].UserID != "user-b" || batch[0].Status != productuseraccess.StatusActive || batch[0].Explicit || batch[0].AccessVersion != 0 || batch[1].UserID != "user-a" || batch[1].Status != productuseraccess.StatusSuspended || !batch[1].Explicit || batch[1].AccessVersion != 1 {
		t.Fatalf("tenant batch=%+v error=%v", batch, err)
	}
	var tenantFacts int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM product_user_access.tenant_access WHERE product_id=$1 AND tenant_id=$2`, product.ProductID, tenant.TenantID).Scan(&tenantFacts); err != nil || tenantFacts != 1 {
		t.Fatalf("batch read created default facts count=%d error=%v", tenantFacts, err)
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
			_, err := service.SetProductAccessStatus(ctx, productuseraccess.SetProductAccessStatusCommand{Product: product, User: user, Status: productuseraccess.StatusSuspended, ExpectedVersion: 0, ReasonCode: "security.review", IdempotencyKey: fmt.Sprintf("idempotency-concurrent-%04d", index), ActorID: "admin-a", TraceID: fmt.Sprintf("trace-concurrent-%04d", index)})
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
	_, err := rollbackService.SetProductAccessStatus(ctx, productuseraccess.SetProductAccessStatusCommand{Product: productuseraccess.ProductContext{ProductID: "product-b"}, User: user, Status: productuseraccess.StatusSuspended, ExpectedVersion: 0, ReasonCode: "security.review", IdempotencyKey: "idempotency-rollback-0001", ActorID: "admin-a", TraceID: "trace-rollback-0001"})
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

func TestRepositoryOutboxDeliveryLeaseRetryDeadAndPublishedTerminals(t *testing.T) {
	database := testpostgres.Open(t)
	repository := accesspostgres.New(database.Pool)
	service := productuseraccess.NewService(repository, &sequenceIDs{}, digestKey, fixedNow)
	ctx := context.Background()
	_, err := service.SetProductAccessStatus(ctx, productuseraccess.SetProductAccessStatusCommand{
		Product: productuseraccess.ProductContext{ProductID: "product-delivery"}, User: productuseraccess.UserContext{UserID: "user-delivery"},
		Status: productuseraccess.StatusSuspended, ExpectedVersion: 0, ReasonCode: "security.review",
		IdempotencyKey: "idempotency-delivery-0001", ActorID: "admin-delivery", TraceID: "trace-delivery",
	})
	if err != nil {
		t.Fatal(err)
	}
	claimAt := time.Now().UTC().Add(time.Minute)
	start := make(chan struct{})
	claims := make(chan []productuseraccess.ClaimedOutboxEvent, 2)
	errorsChannel := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			items, claimErr := repository.ClaimOutbox(ctx, claimAt, 1)
			claims <- items
			errorsChannel <- claimErr
		}()
	}
	close(start)
	claimed := make([]productuseraccess.ClaimedOutboxEvent, 0, 2)
	for range 2 {
		items := <-claims
		if claimErr := <-errorsChannel; claimErr != nil {
			t.Fatal(claimErr)
		}
		claimed = append(claimed, items...)
	}
	if len(claimed) != 2 || claimed[0].EventID == claimed[1].EventID {
		t.Fatalf("concurrent claims=%+v", claimed)
	}
	types := map[string]bool{}
	for _, item := range claimed {
		types[item.EventType] = true
		if item.AttemptCount != 1 || item.PayloadError != "" || item.Payload.AuditID == "" || item.Payload.ActorID != "admin-delivery" || item.Payload.Permission != "product.user-access.manage" || item.Payload.Action != "product_user_access.set_status" || item.Payload.TargetID == "" || item.Payload.Result != "success" || item.Payload.TraceID != "trace-delivery" || item.Payload.UserID != "user-delivery" || item.Payload.Status != productuseraccess.StatusSuspended || item.Payload.AccessVersion != 1 {
			t.Fatalf("invalid claimed payload=%+v", item)
		}
	}
	if !types["product-user-access.status-changed.v1"] || !types["product-user-access.session-revocation-requested.v1"] {
		t.Fatalf("claimed event types=%v", types)
	}
	if immediate, err := repository.ClaimOutbox(ctx, claimAt, 10); err != nil || len(immediate) != 0 {
		t.Fatalf("lease did not hide claims: %+v error=%v", immediate, err)
	}
	first, second := claimed[0], claimed[1]
	if err := service.MarkOutboxFailed(ctx, first.EventID, " temporary delivery failure ", claimAt.Add(-time.Second), false); err != nil {
		t.Fatal(err)
	}
	retried, err := repository.ClaimOutbox(ctx, claimAt, 10)
	if err != nil || len(retried) != 1 || retried[0].EventID != first.EventID || retried[0].AttemptCount != 2 {
		t.Fatalf("retried=%+v error=%v", retried, err)
	}
	if err := service.MarkOutboxPublished(ctx, first.EventID); err != nil {
		t.Fatal(err)
	}
	if err := service.MarkOutboxFailed(ctx, second.EventID, "terminal delivery failure", claimAt.Add(time.Hour), true); err != nil {
		t.Fatal(err)
	}
	if terminal, err := repository.ClaimOutbox(ctx, claimAt.Add(2*time.Hour), 10); err != nil || len(terminal) != 0 {
		t.Fatalf("terminal events reclaimed: %+v error=%v", terminal, err)
	}
	var publishedAt *time.Time
	var publishedError *string
	if err := database.Pool.QueryRow(ctx, `SELECT published_at,last_error FROM product_user_access.outbox_events WHERE event_id=$1`, first.EventID).Scan(&publishedAt, &publishedError); err != nil || publishedAt == nil || publishedError != nil {
		t.Fatalf("published terminal at=%v error_summary=%v query_error=%v", publishedAt, publishedError, err)
	}
	var dead bool
	var deadError string
	if err := database.Pool.QueryRow(ctx, `SELECT dead,last_error FROM product_user_access.outbox_events WHERE event_id=$1`, second.EventID).Scan(&dead, &deadError); err != nil || !dead || deadError != "terminal delivery failure" {
		t.Fatalf("dead terminal=%v summary=%q query_error=%v", dead, deadError, err)
	}
}

func TestRepositoryOutboxPoisonPayloadIsLeasedAndDoesNotBlockFollowingEvent(t *testing.T) {
	database := testpostgres.Open(t)
	repository := accesspostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	poisonID, validID := "product_user_access_event_poison", "product_user_access_event_valid"
	if _, err := database.Pool.Exec(ctx, `INSERT INTO product_user_access.outbox_events(event_id,aggregate_id,event_type,payload,occurred_at,next_attempt_at) VALUES
		($1,'access_poison','product-user-access.status-changed.v1','{"audit_id":"audit_poison_0001","occurred_at":false}'::jsonb,$3,$3),
		($2,'access_valid','product-user-access.command-audited.v1','{"audit_id":"audit_valid_000001","occurred_at":"2026-07-17T08:00:00Z","actor_id":"admin-a","permission":"product.user-access.manage","scope_type":"product","scope_id":"product-a","product_id":"product-a","action":"product_user_access.set_status","target_type":"product_user_access","target_id":"access_valid","result":"success","reason_code":"manual.review","trace_id":"trace-valid","risk_level":"high","user_id":"user-a","status":"active","access_version":1,"status_changed_at":"2026-07-17T08:00:00Z"}'::jsonb,$4,$3)`, poisonID, validID, now, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	first, err := repository.ClaimOutbox(ctx, now.Add(time.Second), 1)
	if err != nil || len(first) != 1 || first[0].EventID != poisonID || first[0].AttemptCount != 1 || first[0].PayloadError == "" {
		t.Fatalf("poison claim=%+v error=%v", first, err)
	}
	second, err := repository.ClaimOutbox(ctx, now.Add(time.Second), 1)
	if err != nil || len(second) != 1 || second[0].EventID != validID || second[0].AttemptCount != 1 || second[0].PayloadError != "" || second[0].Payload.Permission != "product.user-access.manage" {
		t.Fatalf("following claim=%+v error=%v", second, err)
	}
	if err := repository.MarkOutboxFailed(ctx, poisonID, first[0].PayloadError, now.Add(time.Hour), true); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkOutboxPublished(ctx, validID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var dead bool
	var summary string
	if err := database.Pool.QueryRow(ctx, `SELECT attempt_count,dead,last_error FROM product_user_access.outbox_events WHERE event_id=$1`, poisonID).Scan(&attempts, &dead, &summary); err != nil || attempts != 1 || !dead || summary != "invalid product user access outbox payload" {
		t.Fatalf("poison terminal attempts=%d dead=%v summary=%q error=%v", attempts, dead, summary, err)
	}
	if claimed, err := repository.ClaimOutbox(ctx, now.Add(2*time.Hour), 10); err != nil || len(claimed) != 0 {
		t.Fatalf("poison reclaimed=%+v error=%v", claimed, err)
	}
}

func fixedNow() time.Time { return time.Date(2026, 7, 17, 8, 0, 0, 123456000, time.UTC) }
