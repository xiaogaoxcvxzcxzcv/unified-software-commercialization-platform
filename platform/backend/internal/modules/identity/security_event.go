package identity

import (
	"context"
	"time"
)

// SecurityEvent is Identity's audit boundary. Adapters translate it to an
// audit-module DTO without coupling Identity's repositories or services to it.
type SecurityEvent struct {
	AuditID         string         `json:"audit_id"`
	OccurredAt      time.Time      `json:"occurred_at"`
	ActorID         string         `json:"actor_id"`
	Permission      string         `json:"permission,omitempty"`
	ScopeType       string         `json:"scope_type,omitempty"`
	ScopeID         string         `json:"scope_id,omitempty"`
	ProductID       string         `json:"product_id,omitempty"`
	TenantID        string         `json:"tenant_id,omitempty"`
	Action          string         `json:"action"`
	TargetType      string         `json:"target_type"`
	TargetID        string         `json:"target_id"`
	Result          string         `json:"result"`
	ReasonCode      string         `json:"reason_code,omitempty"`
	TraceID         string         `json:"trace_id"`
	RiskLevel       string         `json:"risk_level"`
	RedactedSummary map[string]any `json:"redacted_summary,omitempty"`
}

type AuditPort interface {
	AppendSecurityEvent(context.Context, SecurityEvent) (string, error)
}
