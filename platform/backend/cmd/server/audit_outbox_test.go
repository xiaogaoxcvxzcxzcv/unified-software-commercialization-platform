package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/audit"
	"platform.local/capability-platform/backend/internal/modules/entitlement"
)

type outboxSourceStub struct {
	events    []auditableOutboxEvent
	published []string
	failed    []string
}

func (s *outboxSourceStub) Claim(context.Context, time.Time, int) ([]auditableOutboxEvent, error) {
	result := s.events
	s.events = nil
	return result, nil
}
func (s *outboxSourceStub) Published(_ context.Context, event auditableOutboxEvent, _ time.Time) error {
	s.published = append(s.published, event.EventID)
	return nil
}
func (s *outboxSourceStub) Failed(_ context.Context, event auditableOutboxEvent, _ string, _ time.Time, _ bool) error {
	s.failed = append(s.failed, event.EventID)
	return nil
}

type auditOutboxRepositoryStub struct{ events []audit.Event }

func (s *auditOutboxRepositoryStub) Append(_ context.Context, event audit.Event) error {
	s.events = append(s.events, event)
	return nil
}

func TestAuditOutboxDispatcherPublishesValidEventAndQuarantinesInvalidPayload(t *testing.T) {
	source := &outboxSourceStub{events: []auditableOutboxEvent{
		{EventID: "evt-valid", Payload: []byte(`{"audit_id":"audit-1","actor_id":"admin-1","action":"product.created","target_type":"product","target_id":"prod-1","result":"success","trace_id":"trace-1","risk_level":"normal"}`)},
		{EventID: "evt-invalid", Payload: []byte(`{`)},
	}}
	repository := &auditOutboxRepositoryStub{}
	dispatcher := auditOutboxDispatcher{name: "test", source: source, audit: audit.NewService(repository), logger: slog.Default()}
	dispatcher.dispatch(context.Background())
	if len(repository.events) != 1 || len(source.published) != 1 || source.published[0] != "evt-valid" || len(source.failed) != 1 || source.failed[0] != "evt-invalid" {
		t.Fatalf("events=%d published=%v failed=%v", len(repository.events), source.published, source.failed)
	}
}

func TestAuditEventFromEntitlementOutboxPreservesScopeActorAndPermission(t *testing.T) {
	occurredAt := time.Date(2026, 7, 23, 11, 0, 0, 0, time.UTC)
	event := auditEventFromEntitlementOutbox(entitlement.ClaimedOutboxEvent{
		EventID: "entitlement-event-a", EventType: "entitlement.revoked.v1", OccurredAt: occurredAt,
		Payload: map[string]any{
			"audit_id": "audit-a", "product_id": "product-a", "tenant_id": "tenant-a", "user_id": "user-a",
			"grant_id": "grant-a", "revision": float64(2), "operation": "revoke", "actor_id": "admin-a",
			"reason_code": "manual_revoke", "trace_id": "trace-a",
		},
	})
	if event.AuditID != "audit-a" || event.ActorID != "admin-a" || event.Permission != "entitlement.revoke" || event.ScopeType != "tenant" || event.ProductID != "product-a" || event.TenantID != "tenant-a" || event.TargetID != "grant-a" || event.TraceID != "trace-a" {
		t.Fatalf("event=%+v", event)
	}
	if event.RedactedSummary["user_id"] != "user-a" || event.RedactedSummary["grant_id"] != "grant-a" {
		t.Fatalf("summary=%+v", event.RedactedSummary)
	}
}
