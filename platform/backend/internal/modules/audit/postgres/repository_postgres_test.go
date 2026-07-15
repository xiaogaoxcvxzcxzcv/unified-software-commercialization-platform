package postgres_test

import (
	"context"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/audit"
	auditpostgres "platform.local/capability-platform/backend/internal/modules/audit/postgres"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestRepositoryAuditEventsAreIdempotentAndAppendOnly(t *testing.T) {
	database := testpostgres.Open(t)
	repository := auditpostgres.New(database.Pool)
	ctx := context.Background()
	event := audit.Event{
		AuditID: "audit-1", OccurredAt: time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC), ActorID: "admin-1",
		Permission: "audit.read", ScopeType: "platform", Action: "integration.test", TargetType: "test", TargetID: "target-1",
		Result: "success", TraceID: "trace-1", RiskLevel: "normal", RedactedSummary: map[string]any{"safe": true},
	}
	if err := repository.Append(ctx, event); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := repository.Append(ctx, event); err != nil {
		t.Fatalf("repeat Append() error = %v", err)
	}
	var count int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM audit.events WHERE audit_id=$1`, event.AuditID).Scan(&count); err != nil {
		t.Fatalf("count audit event: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit event count = %d, want 1", count)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE audit.events SET result='failure' WHERE audit_id=$1`, event.AuditID); err == nil {
		t.Fatal("audit UPDATE unexpectedly succeeded")
	}
	if _, err := database.Pool.Exec(ctx, `DELETE FROM audit.events WHERE audit_id=$1`, event.AuditID); err == nil {
		t.Fatal("audit DELETE unexpectedly succeeded")
	}
}

func TestRepositoryQueryFiltersScopeAndUsesStableKeysetPagination(t *testing.T) {
	database := testpostgres.Open(t)
	repository := auditpostgres.New(database.Pool)
	ctx := context.Background()
	sameTime := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)
	events := []audit.Event{
		{AuditID: "aud-d", OccurredAt: sameTime, ActorID: "admin-1", ScopeType: "tenant", ScopeID: "tenant-1", ProductID: "product-1", TenantID: "tenant-1", Action: "query.test", TargetType: "test", TargetID: "d", Result: "success", TraceID: "trace-match", RiskLevel: "normal"},
		{AuditID: "aud-c", OccurredAt: sameTime, ActorID: "admin-1", ScopeType: "tenant", ScopeID: "tenant-1", ProductID: "product-1", TenantID: "tenant-1", Action: "query.test", TargetType: "test", TargetID: "c", Result: "success", TraceID: "trace-match", RiskLevel: "normal"},
		{AuditID: "aud-b", OccurredAt: sameTime.Add(-time.Second), ActorID: "admin-1", ScopeType: "tenant", ScopeID: "tenant-1", ProductID: "product-1", TenantID: "tenant-1", Action: "query.test", TargetType: "test", TargetID: "b", Result: "success", TraceID: "trace-match", RiskLevel: "normal"},
		{AuditID: "aud-other-tenant", OccurredAt: sameTime.Add(time.Minute), ActorID: "admin-2", ScopeType: "tenant", ScopeID: "tenant-2", ProductID: "product-1", TenantID: "tenant-2", Action: "query.test", TargetType: "test", TargetID: "other-tenant", Result: "success", TraceID: "trace-match", RiskLevel: "normal"},
		{AuditID: "aud-other-trace", OccurredAt: sameTime.Add(time.Minute), ActorID: "admin-1", ScopeType: "tenant", ScopeID: "tenant-1", ProductID: "product-1", TenantID: "tenant-1", Action: "query.test", TargetType: "test", TargetID: "other-trace", Result: "success", TraceID: "trace-other", RiskLevel: "normal"},
	}
	for _, event := range events {
		if err := repository.Append(ctx, event); err != nil {
			t.Fatalf("Append(%s) error = %v", event.AuditID, err)
		}
	}

	first, err := repository.Query(ctx, audit.RepositoryQuery{
		TraceID: "trace-match", TargetScope: audit.Scope{Type: "tenant", ProductID: "product-1", TenantID: "tenant-1"}, Limit: 2,
	})
	if err != nil {
		t.Fatalf("first Query() error = %v", err)
	}
	if len(first) != 2 || first[0].AuditID != "aud-d" || first[1].AuditID != "aud-c" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := repository.Query(ctx, audit.RepositoryQuery{
		TraceID: "trace-match", TargetScope: audit.Scope{Type: "tenant", ProductID: "product-1", TenantID: "tenant-1"},
		After: &audit.PagePosition{OccurredAt: first[1].OccurredAt, AuditID: first[1].AuditID}, Limit: 2,
	})
	if err != nil {
		t.Fatalf("second Query() error = %v", err)
	}
	if len(second) != 1 || second[0].AuditID != "aud-b" {
		t.Fatalf("second page = %+v", second)
	}
}

func TestRepositoryProductScopeCannotReadAnotherProduct(t *testing.T) {
	database := testpostgres.Open(t)
	repository := auditpostgres.New(database.Pool)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	for _, productID := range []string{"product-1", "product-2"} {
		event := audit.Event{AuditID: "aud-" + productID, OccurredAt: now, ActorID: "admin", ScopeType: "product", ScopeID: productID, ProductID: productID, Action: "scope.test", TargetType: "product", TargetID: productID, Result: "success", TraceID: "trace-product", RiskLevel: "normal"}
		if err := repository.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := repository.Query(ctx, audit.RepositoryQuery{TargetScope: audit.Scope{Type: "product", ProductID: "product-1"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ProductID != "product-1" {
		t.Fatalf("events = %+v", events)
	}
}
