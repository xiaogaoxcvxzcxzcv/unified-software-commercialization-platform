package entitlement

import (
	"context"
	"time"
)

type Repository interface {
	CheckEntitlement(ctx context.Context, query CheckQuery) (CheckDecision, error)
	GrantEntitlement(ctx context.Context, record WriteRecord) (GrantResult, error)
	ExtendEntitlement(ctx context.Context, record WriteRecord) (GrantResult, error)
	ReplaceEntitlement(ctx context.Context, record WriteRecord) (GrantResult, error)
	RevokeEntitlement(ctx context.Context, record WriteRecord) (GrantResult, error)
	GetCurrentEntitlements(ctx context.Context, query CurrentQuery) (EntitlementSummary, error)
	ListHistory(ctx context.Context, query HistoryQuery) ([]LedgerEntry, error)
	ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]ClaimedOutboxEvent, error)
	MarkOutboxPublished(ctx context.Context, eventID string, now time.Time) error
	MarkOutboxFailed(ctx context.Context, eventID, summary string, next time.Time, dead bool) error
}

type CheckQuery struct {
	ProductID         string
	TenantID          string
	UserID            string
	RequestedFeatures []string
	DeviceID          string
	ClientObservedAt  *time.Time
	ServerTime        time.Time
}

type CurrentQuery struct {
	ProductID string
	TenantID  string
	UserID    string
}

type HistoryQuery struct {
	ProductID string
	TenantID  string
	UserID    string
	Limit     int
	Cursor    string
}

type WriteRecord struct {
	Operation        Effect
	ProductID        string
	TenantID         string
	UserID           string
	PolicyID         string
	PolicyVersion    int64
	Validity         ValidityInput
	Source           SourceRef
	IdempotencyKey   string
	ExpectedRevision int64
	TargetGrantID    string
	Actor            ActorRef
	ReasonCode       ReasonCode
	RequestHash      []byte
	KeyHash          []byte
	GrantID          string
	LedgerID         string
	OutboxEventID    string
	AuditID          string
	TraceID          string
	Now              time.Time
}
