package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"platform.local/capability-platform/backend/internal/modules/audit"
	"platform.local/capability-platform/backend/internal/modules/identity"
	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

type productUserAccessOutbox interface {
	ClaimOutbox(context.Context, int) ([]productuseraccess.ClaimedOutboxEvent, error)
	MarkOutboxPublished(context.Context, string) error
	MarkOutboxFailed(context.Context, string, string, time.Time, bool) error
}

type scopedSessionRevoker interface {
	RevokeScopedSessions(context.Context, identity.ScopedSessionRevocation) error
}

type productUserAccessDispatcher struct {
	source  productUserAccessOutbox
	audit   *audit.Service
	revoker scopedSessionRevoker
	hasher  securevalue.Hasher
	logger  *slog.Logger
	now     func() time.Time
}

func (d productUserAccessDispatcher) Run(ctx context.Context) {
	if d.now == nil {
		d.now = time.Now
	}
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

func (d productUserAccessDispatcher) dispatch(ctx context.Context) {
	if d.source == nil || d.audit == nil || d.revoker == nil {
		return
	}
	if d.now == nil {
		d.now = time.Now
	}
	items, err := d.source.ClaimOutbox(ctx, 50)
	if err != nil {
		d.logError("product user access outbox claim failed", "error", err)
		return
	}
	for _, item := range items {
		err := d.deliver(ctx, item)
		if err == nil {
			if err = d.source.MarkOutboxPublished(ctx, item.EventID); err != nil {
				d.logError("product user access publish confirmation failed", "event_id", item.EventID, "error", err)
			}
			continue
		}
		dead := item.AttemptCount >= 10
		next := d.now().UTC().Add(time.Duration(item.AttemptCount+1) * 30 * time.Second)
		if markErr := d.source.MarkOutboxFailed(ctx, item.EventID, err.Error(), next, dead); markErr != nil {
			d.logError("product user access failure update failed", "event_id", item.EventID, "error", markErr)
		}
	}
}

func (d productUserAccessDispatcher) deliver(ctx context.Context, item productuseraccess.ClaimedOutboxEvent) error {
	if item.PayloadError != "" {
		return errors.New("invalid product user access outbox payload")
	}
	switch item.EventType {
	case "product-user-access.status-changed.v1", "product-user-access.command-audited.v1":
		p := item.Payload
		_, err := d.audit.AppendAuditEvent(ctx, audit.Event{
			AuditID: p.AuditID, OccurredAt: p.OccurredAt, ActorID: p.ActorID,
			Permission: p.Permission, ScopeType: p.ScopeType, ScopeID: p.ScopeID,
			ProductID: p.ProductID, TenantID: p.TenantID, Action: p.Action,
			TargetType: p.TargetType, TargetID: p.TargetID, Result: p.Result,
			ReasonCode: p.ReasonCode, TraceID: p.TraceID, RiskLevel: p.RiskLevel,
			RedactedSummary: p.RedactedSummary,
		})
		return err
	case "product-user-access.session-revocation-requested.v1":
		return d.revokeScopedSessions(ctx, item)
	default:
		return fmt.Errorf("unsupported product user access event type")
	}
}

func (d productUserAccessDispatcher) revokeScopedSessions(ctx context.Context, item productuseraccess.ClaimedOutboxEvent) error {
	p := item.Payload
	if p.ProductID == "" || p.UserID == "" || p.AccessVersion < 1 || p.StatusChangedAt.IsZero() {
		return errors.New("invalid product user access revocation payload")
	}
	var tenantID *string
	if p.ScopeType == "tenant" {
		if p.TenantID == "" {
			return errors.New("invalid tenant revocation payload")
		}
		value := p.TenantID
		tenantID = &value
	} else if p.ScopeType != "product" {
		return errors.New("invalid revocation scope type")
	}
	now := d.now().UTC()
	eventDigest := d.hasher.Digest("product-user-access-event:" + item.EventID)
	auditDigest := d.hasher.Digest("product-user-access-revocation-audit:" + item.EventID)
	outboxEventID := "evt_pua_revoke_" + hex.EncodeToString(eventDigest[:16])
	auditID := "aud_pua_revoke_" + hex.EncodeToString(auditDigest[:16])
	targetDigest := d.hasher.DigestHex("end-user:" + p.UserID)
	requestDigest := d.hasher.Digest(fmt.Sprintf("product-user-access-revocation:%s:%s:%s:%d:%s", p.ProductID, p.TenantID, p.UserID, p.AccessVersion, p.StatusChangedAt.UTC().Format(time.RFC3339Nano)))
	return d.revoker.RevokeScopedSessions(ctx, identity.ScopedSessionRevocation{
		ProductID: p.ProductID, UserID: p.UserID, TenantID: tenantID,
		Cutoff: p.StatusChangedAt.UTC(), AccessVersion: p.AccessVersion,
		EventIDDigest: eventDigest, RequestDigest: requestDigest,
		ActorDigest: d.hasher.Digest("product-user-access-actor:" + p.ActorID),
		OutboxEvent: identity.OutboxEvent{EventID: outboxEventID, Topic: "identity.scoped_sessions_revoked.v1", Now: now, Payload: identity.SecurityEvent{
			AuditID: auditID, OccurredAt: now, ActorID: p.ActorID,
			Action: "identity.scoped_sessions_revoked", TargetType: "end_user", TargetID: targetDigest,
			Result: "success", ReasonCode: p.ReasonCode, TraceID: p.TraceID, RiskLevel: "high",
			ProductID: p.ProductID, TenantID: p.TenantID,
		}},
	})
}

func (d productUserAccessDispatcher) logError(message string, args ...any) {
	if d.logger != nil {
		d.logger.Error(message, args...)
	}
}
