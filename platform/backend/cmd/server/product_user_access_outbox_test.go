package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/audit"
	"platform.local/capability-platform/backend/internal/modules/identity"
	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

type puaOutboxStub struct {
	items     []productuseraccess.ClaimedOutboxEvent
	published []string
	failed    []string
}

func (s *puaOutboxStub) ClaimOutbox(context.Context, int) ([]productuseraccess.ClaimedOutboxEvent, error) {
	items := s.items
	s.items = nil
	return items, nil
}
func (s *puaOutboxStub) MarkOutboxPublished(_ context.Context, id string) error {
	s.published = append(s.published, id)
	return nil
}
func (s *puaOutboxStub) MarkOutboxFailed(_ context.Context, id, _ string, _ time.Time, _ bool) error {
	s.failed = append(s.failed, id)
	return nil
}

type puaAuditRepositoryStub struct{ items []audit.Event }

func (s *puaAuditRepositoryStub) Append(_ context.Context, event audit.Event) error {
	s.items = append(s.items, event)
	return nil
}

type scopedRevokerStub struct {
	command identity.ScopedSessionRevocation
	err     error
}

func (s *scopedRevokerStub) RevokeScopedSessions(_ context.Context, command identity.ScopedSessionRevocation) error {
	s.command = command
	return s.err
}

func TestProductUserAccessDispatcherRoutesAuditAndRevocation(t *testing.T) {
	now := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	payload := productuseraccess.EventPayload{
		AuditID: "audit-pua-1", OccurredAt: now, ActorID: "admin-a", Permission: "product.user-access.manage",
		ScopeType: "product", ScopeID: "product-a", ProductID: "product-a", UserID: "user-a",
		Action: "product_user_access.status_changed", TargetType: "end_user", TargetID: "user-a",
		Result: "success", ReasonCode: "security.review", TraceID: "trace-a", RiskLevel: "high",
		Status: productuseraccess.StatusSuspended, AccessVersion: 2, StatusChangedAt: now,
	}
	source := &puaOutboxStub{items: []productuseraccess.ClaimedOutboxEvent{
		{EventID: "status-event", EventType: "product-user-access.status-changed.v1", Payload: payload, AttemptCount: 1},
		{EventID: "revoke-event", EventType: "product-user-access.session-revocation-requested.v1", Payload: payload, AttemptCount: 1},
	}}
	auditRepository := &puaAuditRepositoryStub{}
	revoker := &scopedRevokerStub{}
	hasher, _ := securevalue.NewHasher(strings.Repeat("p", 32))
	dispatcher := productUserAccessDispatcher{source: source, audit: audit.NewService(auditRepository), revoker: revoker, hasher: hasher, logger: slog.Default(), now: func() time.Time { return now }}
	dispatcher.dispatch(context.Background())

	if len(source.published) != 2 || len(source.failed) != 0 || len(auditRepository.items) != 1 || auditRepository.items[0].AuditID != payload.AuditID {
		t.Fatalf("published=%v failed=%v audit=%+v", source.published, source.failed, auditRepository.items)
	}
	if revoker.command.ProductID != "product-a" || revoker.command.UserID != "user-a" || revoker.command.AccessVersion != 2 || len(revoker.command.EventIDDigest) != 32 || revoker.command.OutboxEvent.Payload.AuditID == payload.AuditID {
		t.Fatalf("revocation command = %+v", revoker.command)
	}
}

func TestProductUserAccessDispatcherRetriesWithoutPublishing(t *testing.T) {
	now := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	source := &puaOutboxStub{items: []productuseraccess.ClaimedOutboxEvent{{
		EventID: "revoke-event", EventType: "product-user-access.session-revocation-requested.v1", AttemptCount: 2,
		Payload: productuseraccess.EventPayload{ProductID: "product-a", UserID: "user-a", ScopeType: "product", AccessVersion: 1, StatusChangedAt: now},
	}}}
	hasher, _ := securevalue.NewHasher(strings.Repeat("p", 32))
	dispatcher := productUserAccessDispatcher{source: source, audit: audit.NewService(&puaAuditRepositoryStub{}), revoker: &scopedRevokerStub{err: errors.New("database unavailable")}, hasher: hasher, now: func() time.Time { return now }}
	dispatcher.dispatch(context.Background())
	if len(source.published) != 0 || len(source.failed) != 1 || source.failed[0] != "revoke-event" {
		t.Fatalf("published=%v failed=%v", source.published, source.failed)
	}
}
