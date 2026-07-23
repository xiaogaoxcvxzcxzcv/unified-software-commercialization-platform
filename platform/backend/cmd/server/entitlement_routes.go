package main

import (
	"context"
	"errors"

	entitlementhttp "platform.local/capability-platform/backend/internal/modules/entitlement/httptransport"
	identityhttp "platform.local/capability-platform/backend/internal/modules/identity/httptransport"
)

type entitlementUserSessionResolver struct {
	base endUserHTTPAdapter
}

func (r entitlementUserSessionResolver) ResolveUserSession(ctx context.Context, token string) (entitlementhttp.UserSessionContext, error) {
	value, err := r.base.ResolveUserSession(ctx, token)
	if err != nil {
		if errors.Is(err, identityhttp.ErrInvalidBearer) || errors.Is(err, identityhttp.ErrUserSessionExpired) || errors.Is(err, identityhttp.ErrUserSessionRevoked) {
			return entitlementhttp.UserSessionContext{}, entitlementhttp.ErrInvalidBearer
		}
		return entitlementhttp.UserSessionContext{}, err
	}
	if value.UserID == "" || value.ProductID == "" || value.TenantID == "" {
		return entitlementhttp.UserSessionContext{}, entitlementhttp.ErrEntitlementNoScope
	}
	return entitlementhttp.UserSessionContext{UserID: value.UserID, ProductID: value.ProductID, TenantID: value.TenantID}, nil
}
