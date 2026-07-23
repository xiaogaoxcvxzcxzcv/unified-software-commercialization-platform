package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	assemblycore "platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/audit"
	"platform.local/capability-platform/backend/internal/modules/entitlement"
	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/modules/tenant"
)

type auditableOutboxEvent struct {
	EventID      string
	Payload      []byte
	AttemptCount int
	LeaseToken   string
}

type auditableOutboxSource interface {
	Claim(context.Context, time.Time, int) ([]auditableOutboxEvent, error)
	Published(context.Context, auditableOutboxEvent, time.Time) error
	Failed(context.Context, auditableOutboxEvent, string, time.Time, bool) error
}

type productOutboxSource struct{ service *product.Service }

type assemblyOutboxSource struct{ service *assemblycore.Service }

func (s assemblyOutboxSource) Claim(ctx context.Context, now time.Time, limit int) ([]auditableOutboxEvent, error) {
	items, err := s.service.ClaimOutbox(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	result := make([]auditableOutboxEvent, len(items))
	for i := range items {
		payload, err := json.Marshal(items[i].Payload)
		if err != nil {
			return nil, err
		}
		result[i] = auditableOutboxEvent{EventID: items[i].EventID, Payload: payload, AttemptCount: items[i].AttemptCount}
	}
	return result, nil
}
func (s assemblyOutboxSource) Published(ctx context.Context, event auditableOutboxEvent, now time.Time) error {
	return s.service.MarkOutboxPublished(ctx, event.EventID, now)
}
func (s assemblyOutboxSource) Failed(ctx context.Context, event auditableOutboxEvent, summary string, next time.Time, dead bool) error {
	return s.service.MarkOutboxFailed(ctx, event.EventID, summary, next, dead)
}

func (s productOutboxSource) Claim(ctx context.Context, now time.Time, limit int) ([]auditableOutboxEvent, error) {
	items, err := s.service.ClaimOutbox(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	result := make([]auditableOutboxEvent, len(items))
	for i := range items {
		payload, err := json.Marshal(items[i].Payload)
		if err != nil {
			return nil, err
		}
		result[i] = auditableOutboxEvent{EventID: items[i].EventID, Payload: payload, AttemptCount: items[i].AttemptCount}
	}
	return result, nil
}
func (s productOutboxSource) Published(ctx context.Context, event auditableOutboxEvent, now time.Time) error {
	return s.service.MarkOutboxPublished(ctx, event.EventID, now)
}
func (s productOutboxSource) Failed(ctx context.Context, event auditableOutboxEvent, summary string, next time.Time, dead bool) error {
	return s.service.MarkOutboxFailed(ctx, event.EventID, summary, next, dead)
}

type accessControlOutboxSource struct{ service *accesscontrol.Service }

func (s accessControlOutboxSource) Claim(ctx context.Context, now time.Time, limit int) ([]auditableOutboxEvent, error) {
	items, err := s.service.ClaimOutbox(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	result := make([]auditableOutboxEvent, len(items))
	for i := range items {
		result[i] = auditableOutboxEvent{EventID: items[i].EventID, Payload: items[i].Payload, AttemptCount: items[i].AttemptCount}
	}
	return result, nil
}
func (s accessControlOutboxSource) Published(ctx context.Context, event auditableOutboxEvent, now time.Time) error {
	return s.service.MarkOutboxPublished(ctx, event.EventID, now)
}
func (s accessControlOutboxSource) Failed(ctx context.Context, event auditableOutboxEvent, summary string, next time.Time, dead bool) error {
	return s.service.MarkOutboxFailed(ctx, event.EventID, summary, next, dead)
}

type auditOutboxDispatcher struct {
	name   string
	source auditableOutboxSource
	audit  *audit.Service
	logger *slog.Logger
}

func (d auditOutboxDispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		d.dispatch(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d auditOutboxDispatcher) dispatch(ctx context.Context) {
	if d.source == nil || d.audit == nil {
		return
	}
	now := time.Now().UTC()
	events, err := d.source.Claim(ctx, now, 50)
	if err != nil {
		d.logger.Error("audit outbox claim failed", "source", d.name, "error", err)
		return
	}
	for _, item := range events {
		var event audit.Event
		if err := json.Unmarshal(item.Payload, &event); err != nil {
			d.fail(ctx, item, errors.New("invalid audit event payload"))
			continue
		}
		if _, err := d.audit.AppendAuditEvent(ctx, event); err != nil {
			d.fail(ctx, item, err)
			continue
		}
		if err := d.source.Published(ctx, item, time.Now().UTC()); err != nil {
			d.logger.Error("audit outbox publish confirmation failed", "source", d.name, "event_id", item.EventID, "error", err)
		}
	}
}

func (d auditOutboxDispatcher) fail(ctx context.Context, item auditableOutboxEvent, cause error) {
	dead := item.AttemptCount >= 10
	retryAt := time.Now().UTC().Add(time.Duration(item.AttemptCount+1) * 30 * time.Second)
	if err := d.source.Failed(ctx, item, cause.Error(), retryAt, dead); err != nil {
		d.logger.Error("audit outbox failure update failed", "source", d.name, "event_id", item.EventID, "error", err)
	}
}

type productApplicationOutboxSource struct{ service *productapplication.Service }

func (s productApplicationOutboxSource) Claim(ctx context.Context, now time.Time, limit int) ([]auditableOutboxEvent, error) {
	items, err := s.service.ClaimOutbox(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	result := make([]auditableOutboxEvent, len(items))
	for i := range items {
		result[i] = auditableOutboxEvent{EventID: items[i].EventID, Payload: items[i].Payload, AttemptCount: items[i].AttemptCount, LeaseToken: items[i].LeaseToken}
	}
	return result, nil
}
func (s productApplicationOutboxSource) Published(ctx context.Context, event auditableOutboxEvent, now time.Time) error {
	return s.service.MarkOutboxPublished(ctx, event.EventID, event.LeaseToken, now)
}
func (s productApplicationOutboxSource) Failed(ctx context.Context, event auditableOutboxEvent, summary string, next time.Time, dead bool) error {
	return s.service.MarkOutboxFailed(ctx, event.EventID, event.LeaseToken, summary, next, dead)
}

type tenantOutboxSource struct{ service *tenant.Service }

func (s tenantOutboxSource) Claim(ctx context.Context, _ time.Time, limit int) ([]auditableOutboxEvent, error) {
	items, err := s.service.ClaimOutbox(ctx, limit)
	if err != nil {
		return nil, err
	}
	result := make([]auditableOutboxEvent, len(items))
	for i := range items {
		payload, err := json.Marshal(items[i].Payload)
		if err != nil {
			return nil, err
		}
		result[i] = auditableOutboxEvent{EventID: items[i].EventID, Payload: payload, AttemptCount: items[i].AttemptCount}
	}
	return result, nil
}
func (s tenantOutboxSource) Published(ctx context.Context, event auditableOutboxEvent, _ time.Time) error {
	return s.service.MarkOutboxPublished(ctx, event.EventID)
}
func (s tenantOutboxSource) Failed(ctx context.Context, event auditableOutboxEvent, summary string, next time.Time, dead bool) error {
	return s.service.MarkOutboxFailed(ctx, event.EventID, summary, next, dead)
}

type entitlementOutboxSource struct{ service *entitlement.Service }

func (s entitlementOutboxSource) Claim(ctx context.Context, _ time.Time, limit int) ([]auditableOutboxEvent, error) {
	items, err := s.service.ClaimOutbox(ctx, limit)
	if err != nil {
		return nil, err
	}
	result := make([]auditableOutboxEvent, len(items))
	for i := range items {
		event := auditEventFromEntitlementOutbox(items[i])
		payload, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		result[i] = auditableOutboxEvent{EventID: items[i].EventID, Payload: payload, AttemptCount: items[i].AttemptCount}
	}
	return result, nil
}

func (s entitlementOutboxSource) Published(ctx context.Context, event auditableOutboxEvent, _ time.Time) error {
	return s.service.MarkOutboxPublished(ctx, event.EventID)
}

func (s entitlementOutboxSource) Failed(ctx context.Context, event auditableOutboxEvent, summary string, next time.Time, dead bool) error {
	return s.service.MarkOutboxFailed(ctx, event.EventID, summary, next, dead)
}

func auditEventFromEntitlementOutbox(item entitlement.ClaimedOutboxEvent) audit.Event {
	payload := item.Payload
	operation := stringValue(payload["operation"])
	if operation == "" {
		operation = item.EventType
	}
	productID := stringValue(payload["product_id"])
	tenantID := stringValue(payload["tenant_id"])
	grantID := stringValue(payload["grant_id"])
	auditID := stringValue(payload["audit_id"])
	if auditID == "" {
		auditID = item.EventID
	}
	traceID := stringValue(payload["trace_id"])
	if traceID == "" {
		traceID = item.EventID
	}
	return audit.Event{
		AuditID:    auditID,
		OccurredAt: item.OccurredAt,
		ActorID:    stringValue(payload["actor_id"]),
		Permission: entitlementPermission(operation),
		ScopeType:  "tenant",
		ScopeID:    tenantID,
		ProductID:  productID,
		TenantID:   tenantID,
		Action:     "entitlement." + operation,
		TargetType: "entitlement_grant",
		TargetID:   grantID,
		Result:     "succeeded",
		ReasonCode: stringValue(payload["reason_code"]),
		TraceID:    traceID,
		RiskLevel:  "high",
		RedactedSummary: map[string]any{
			"user_id":    stringValue(payload["user_id"]),
			"grant_id":   grantID,
			"revision":   payload["revision"],
			"event_type": item.EventType,
		},
	}
}

func entitlementPermission(operation string) string {
	if operation == string(entitlement.EffectRevoke) {
		return "entitlement.revoke"
	}
	return "entitlement.manage"
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}
