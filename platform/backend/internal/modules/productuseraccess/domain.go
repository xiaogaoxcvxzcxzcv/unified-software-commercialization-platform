package productuseraccess

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument  = errors.New("invalid product user access argument")
	ErrScopeMismatch    = errors.New("PRODUCT_USER_ACCESS_SCOPE_MISMATCH")
	ErrConflict         = errors.New("PRODUCT_USER_ACCESS_CONFLICT")
	ErrProductSuspended = errors.New("PRODUCT_USER_ACCESS_SUSPENDED")
	ErrTenantSuspended  = errors.New("TENANT_USER_ACCESS_SUSPENDED")
	ErrOutboxNotFound   = errors.New("product user access outbox event not found")
)

type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
)

type ScopeType string

const (
	ScopeProduct ScopeType = "product"
	ScopeTenant  ScopeType = "tenant"
)

// Context values are trusted inputs resolved by the composition layer.
type ProductContext struct{ ProductID string }
type TenantContext struct {
	ProductID string
	TenantID  string
}
type UserContext struct{ UserID string }

type Admission struct {
	Allowed        bool   `json:"allowed"`
	Code           string `json:"code,omitempty"`
	ProductStatus  Status `json:"product_status"`
	ProductVersion int64  `json:"product_version"`
	TenantStatus   Status `json:"tenant_status,omitempty"`
	TenantVersion  int64  `json:"tenant_version,omitempty"`
}

type AccessFact struct {
	ScopeType       ScopeType `json:"scope_type"`
	ProductID       string    `json:"product_id"`
	TenantID        string    `json:"tenant_id,omitempty"`
	UserID          string    `json:"user_id"`
	Status          Status    `json:"status"`
	AccessVersion   int64     `json:"access_version"`
	ReasonCode      string    `json:"reason_code"`
	OperatorNote    string    `json:"-"`
	StatusChangedAt time.Time `json:"status_changed_at"`
}

type StatusChangeResult struct {
	ScopeType     ScopeType `json:"scope_type"`
	ProductID     string    `json:"product_id"`
	TenantID      string    `json:"tenant_id,omitempty"`
	UserID        string    `json:"user_id"`
	Status        Status    `json:"status"`
	AccessVersion int64     `json:"access_version"`
	AuditID       string    `json:"audit_id"`
}

type SetProductAccessStatusCommand struct {
	Product         ProductContext
	User            UserContext
	Status          Status
	ExpectedVersion int64
	ReasonCode      string
	OperatorNote    string
	IdempotencyKey  string
	ActorID         string
	TraceID         string
}

type SetTenantAccessStatusCommand struct {
	Product         ProductContext
	Tenant          TenantContext
	User            UserContext
	Status          Status
	ExpectedVersion int64
	ReasonCode      string
	OperatorNote    string
	IdempotencyKey  string
	ActorID         string
	TraceID         string
}

type ListScopedUserIDsQuery struct {
	Product ProductContext
	Tenant  *TenantContext
}

type ChangeRecord struct {
	ScopeType         ScopeType
	ProductID         string
	TenantID          string
	UserID            string
	Status            Status
	ExpectedVersion   int64
	ReasonCode        string
	OperatorNote      string
	KeyDigest         []byte
	RequestDigest     []byte
	StatusEventID     string
	RevocationEventID string
	AuditID           string
	ActorID           string
	TraceID           string
	Now               time.Time
}

// EventPayload is owned by Product User Access and can be mapped to Audit or
// Identity by the composition root without introducing a module dependency.
type EventPayload struct {
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
	UserID          string         `json:"user_id"`
	Status          Status         `json:"status"`
	AccessVersion   int64          `json:"access_version"`
	StatusChangedAt time.Time      `json:"status_changed_at"`
}

type ClaimedOutboxEvent struct {
	EventID      string
	AggregateID  string
	EventType    string
	Payload      EventPayload
	PayloadError string
	OccurredAt   time.Time
	AttemptCount int
}
