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
	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/modules/tenant"
)

type auditableOutboxEvent struct {
	EventID      string
	Payload      []byte
	AttemptCount int
}

type auditableOutboxSource interface {
	Claim(context.Context, time.Time, int) ([]auditableOutboxEvent, error)
	Published(context.Context, string, time.Time) error
	Failed(context.Context, string, string, time.Time, bool) error
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
func (s assemblyOutboxSource) Published(ctx context.Context, id string, now time.Time) error {
	return s.service.MarkOutboxPublished(ctx, id, now)
}
func (s assemblyOutboxSource) Failed(ctx context.Context, id, summary string, next time.Time, dead bool) error {
	return s.service.MarkOutboxFailed(ctx, id, summary, next, dead)
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
func (s productOutboxSource) Published(ctx context.Context, id string, now time.Time) error {
	return s.service.MarkOutboxPublished(ctx, id, now)
}
func (s productOutboxSource) Failed(ctx context.Context, id, summary string, next time.Time, dead bool) error {
	return s.service.MarkOutboxFailed(ctx, id, summary, next, dead)
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
func (s accessControlOutboxSource) Published(ctx context.Context, id string, now time.Time) error {
	return s.service.MarkOutboxPublished(ctx, id, now)
}
func (s accessControlOutboxSource) Failed(ctx context.Context, id, summary string, next time.Time, dead bool) error {
	return s.service.MarkOutboxFailed(ctx, id, summary, next, dead)
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
		if err := d.source.Published(ctx, item.EventID, time.Now().UTC()); err != nil {
			d.logger.Error("audit outbox publish confirmation failed", "source", d.name, "event_id", item.EventID, "error", err)
		}
	}
}

func (d auditOutboxDispatcher) fail(ctx context.Context, item auditableOutboxEvent, cause error) {
	dead := item.AttemptCount >= 10
	retryAt := time.Now().UTC().Add(time.Duration(item.AttemptCount+1) * 30 * time.Second)
	if err := d.source.Failed(ctx, item.EventID, cause.Error(), retryAt, dead); err != nil {
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
		result[i] = auditableOutboxEvent{EventID: items[i].EventID, Payload: items[i].Payload, AttemptCount: items[i].AttemptCount}
	}
	return result, nil
}
func (s productApplicationOutboxSource) Published(ctx context.Context, eventID string, now time.Time) error {
	return s.service.MarkOutboxPublished(ctx, eventID, now)
}
func (s productApplicationOutboxSource) Failed(ctx context.Context, eventID, summary string, next time.Time, dead bool) error {
	return s.service.MarkOutboxFailed(ctx, eventID, summary, next, dead)
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
func (s tenantOutboxSource) Published(ctx context.Context, eventID string, _ time.Time) error {
	return s.service.MarkOutboxPublished(ctx, eventID)
}
func (s tenantOutboxSource) Failed(ctx context.Context, eventID, summary string, next time.Time, dead bool) error {
	return s.service.MarkOutboxFailed(ctx, eventID, summary, next, dead)
}
