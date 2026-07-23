package productuseraccess

import (
	"context"
	"time"
)

type Repository interface {
	EvaluateScopedAdmission(ctx context.Context, productID, tenantID, userID string) (Admission, error)
	SetProductAccessStatus(ctx context.Context, record ChangeRecord) (StatusChangeResult, error)
	SetTenantAccessStatus(ctx context.Context, record ChangeRecord) (StatusChangeResult, error)
	ListScopedUserIDs(ctx context.Context, productID, tenantID string) ([]string, error)
	GetScopedAccessBatch(ctx context.Context, productID, tenantID string, userIDs []string) ([]AccessFact, error)
	ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]ClaimedOutboxEvent, error)
	MarkOutboxPublished(ctx context.Context, eventID string, now time.Time) error
	MarkOutboxFailed(ctx context.Context, eventID, summary string, next time.Time, dead bool) error
}
