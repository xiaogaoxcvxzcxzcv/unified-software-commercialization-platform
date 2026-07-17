package productuseraccess

import "context"

type Repository interface {
	EvaluateScopedAdmission(ctx context.Context, productID, tenantID, userID string) (Admission, error)
	SetProductAccessStatus(ctx context.Context, record ChangeRecord) (StatusChangeResult, error)
	SetTenantAccessStatus(ctx context.Context, record ChangeRecord) (StatusChangeResult, error)
	ListScopedUserIDs(ctx context.Context, productID, tenantID string) ([]string, error)
}
