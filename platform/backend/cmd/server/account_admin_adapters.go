package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/identity"
	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	"platform.local/capability-platform/backend/internal/workflows/accountuseradmin"
	"platform.local/capability-platform/backend/internal/workflows/accountuserquery"
)

type accountIdentityQueryAdapter struct{ service *identity.AdminEndUserService }

type accountIDGenerator struct{}

func (accountIDGenerator) ID(prefix string) (string, error) { return securevalue.ID(prefix) }

func (a accountIdentityQueryAdapter) ListUsers(ctx context.Context, query accountuserquery.AdminUserQuery) ([]accountuserquery.AdminUserRecord, error) {
	page, err := a.service.ListUsers(ctx, identity.AdminUserQuery{Scope: toIdentityScope(query.Scope), Query: query.Query, AccountStatus: query.AccountStatus, AfterUserID: query.After, Limit: query.Limit})
	if err != nil {
		return nil, mapAccountIdentityError(err)
	}
	result := make([]accountuserquery.AdminUserRecord, 0, len(page.Items))
	for _, item := range page.Items {
		result = append(result, toQueryUser(item))
	}
	return result, nil
}

func (a accountIdentityQueryAdapter) GetUser(ctx context.Context, scope accountuserquery.Scope, userID string) (accountuserquery.AdminUserDetail, error) {
	item, err := a.service.GetUser(ctx, toIdentityScope(scope), userID)
	if err != nil {
		return accountuserquery.AdminUserDetail{}, mapAccountIdentityError(err)
	}
	return accountuserquery.AdminUserDetail{User: toQueryUser(item), Profile: toQueryProfile(item.Profile)}, nil
}

func (a accountIdentityQueryAdapter) ListSessions(ctx context.Context, query accountuserquery.AdminUserSessionQuery) ([]accountuserquery.AdminUserSession, error) {
	var after *identity.AdminUserSessionPosition
	if query.After != "" {
		parts := strings.SplitN(query.After, "|", 2)
		if len(parts) != 2 {
			return nil, accountuserquery.ErrInvalidCursor
		}
		parsed, err := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			return nil, accountuserquery.ErrInvalidCursor
		}
		after = &identity.AdminUserSessionPosition{CreatedAt: parsed, SessionID: parts[1]}
	}
	page, err := a.service.ListSessions(ctx, identity.AdminUserSessionQuery{Scope: toIdentityScope(query.Scope), UserID: query.UserID, After: after, Limit: query.Limit})
	if err != nil {
		return nil, mapAccountIdentityError(err)
	}
	result := make([]accountuserquery.IdentitySession, 0, len(page.Items))
	for _, item := range page.Items {
		position := item.Position.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + item.Position.SessionID
		result = append(result, accountuserquery.IdentitySession{SessionID: item.SessionID, ProductID: item.ProductID, ApplicationID: item.ApplicationID, TenantID: item.TenantID, Environment: item.Environment, AuthenticationMethod: item.AuthenticationMethod, DeviceLabel: item.DeviceLabel, CreatedAt: item.CreatedAt, LastSeenAt: item.LastSeenAt, ExpiresAt: item.ExpiresAt, RevokedAt: item.RevokedAt, Position: position})
	}
	return result, nil
}

func toIdentityScope(scope accountuserquery.Scope) identity.AdminUserScope {
	return identity.AdminUserScope{Type: identity.AdminUserScopeType(scope.Type), ProductID: scope.ProductID, TenantID: scope.TenantID}
}

func toQueryUser(item identity.AdminUserRecord) accountuserquery.IdentityUser {
	var displayName *string
	if item.DisplayName != "" {
		value := item.DisplayName
		displayName = &value
	}
	identifiers := make([]accountuserquery.MaskedIdentifier, 0, len(item.Identifiers))
	for _, identifier := range item.Identifiers {
		identifiers = append(identifiers, accountuserquery.MaskedIdentifier{Type: string(identifier.Type), MaskedValue: identifier.MaskedValue, Verified: identifier.Verified})
	}
	return accountuserquery.IdentityUser{UserID: item.UserID, UserVersion: item.UserVersion, AccountStatus: item.AccountStatus, DisplayName: displayName, Identifiers: identifiers, CreatedAt: item.CreatedAt, MemberSince: item.MemberSince, LastSeenAt: item.LastSeenAt, ActiveSessionCount: item.ActiveSessionCount, TotalSessionCount: item.TotalSessionCount, Position: item.Position}
}

func toQueryProfile(item identity.AdminUserProfile) accountuserquery.Profile {
	var displayName *string
	if item.DisplayName != "" {
		value := item.DisplayName
		displayName = &value
	}
	return accountuserquery.Profile{UserID: item.UserID, Version: item.Version, DisplayName: displayName, AvatarURL: item.AvatarRef, Locale: item.Locale, Timezone: item.Timezone}
}

func mapAccountIdentityError(err error) error {
	if errors.Is(err, identity.ErrAdminEndUserNotFound) {
		return accountuserquery.ErrIdentityUserNotFound
	}
	return err
}

func mapAccountIdentityMutationError(err error) error {
	if errors.Is(err, identity.ErrAdminEndUserNotFound) {
		return accountuseradmin.ErrIdentityUserNotFound
	}
	if errors.Is(err, identity.ErrAdminEndUserVersionConflict) || errors.Is(err, identity.ErrAdminEndUserIdempotency) {
		return accountuseradmin.ErrIdentityConflict
	}
	return err
}

type accountMembershipAdapter struct{ service *identity.AdminEndUserService }

func (a accountMembershipAdapter) VerifyMember(ctx context.Context, scope accountuseradmin.Scope, userID string) error {
	ok, err := a.service.IsMember(ctx, toIdentityScope(accountuserquery.Scope(scope)), userID)
	if err != nil || !ok {
		return accountuseradmin.ErrScopedUserNotFound
	}
	return nil
}

type accountIdentityMutationAdapter struct{ service *identity.AdminEndUserService }

func (a accountIdentityMutationAdapter) SetGlobalUserSecurityStatus(ctx context.Context, command accountuseradmin.GlobalSecurityCommand) (accountuseradmin.SecurityMutationResult, error) {
	result, err := a.service.SetGlobalSecurityStatus(ctx, identity.AdminGlobalSecurityStatusCommand{UserID: command.UserID, Status: command.Status, ExpectedVersion: command.ExpectedVersion, ReasonCode: command.ReasonCode, OperatorNote: command.OperatorNote, ActorID: command.ActorID, TraceID: command.TraceID, IdempotencyKey: command.IdempotencyKey})
	if err != nil {
		return accountuseradmin.SecurityMutationResult{}, mapAccountIdentityMutationError(err)
	}
	return accountuseradmin.SecurityMutationResult{UserID: result.UserID, ScopeType: string(identity.AdminUserScopePlatform), Status: result.Status, Version: result.Version, AuditID: result.AuditID}, nil
}

func (a accountIdentityMutationAdapter) RevokeAdminUserSessions(ctx context.Context, command accountuseradmin.SessionRevocationCommand) (accountuseradmin.SessionRevocationResult, error) {
	result, err := a.service.RevokeSessions(ctx, identity.AdminSessionRevocationCommand{Scope: toIdentityScope(accountuserquery.Scope(command.Scope)), UserID: command.UserID, SessionIDs: command.SessionIDs, AllActive: command.AllActive, ReasonCode: command.ReasonCode, ActorID: command.ActorID, TraceID: command.TraceID, IdempotencyKey: command.IdempotencyKey})
	if err != nil {
		return accountuseradmin.SessionRevocationResult{}, mapAccountIdentityMutationError(err)
	}
	return accountuseradmin.SessionRevocationResult{UserID: result.UserID, ScopeType: string(result.ScopeType), ScopeID: result.ScopeID, RevokedCount: result.RevokedCount, AuditID: result.AuditID}, nil
}

type accountCapabilityAdapter struct{ service *product.Service }

func (a accountCapabilityAdapter) IsPackageEnabled(ctx context.Context, productID, packageID string) (bool, error) {
	return productPackageEnabled(ctx, a.service, productID, packageID)
}

func accountScopeID(scope accountuserquery.Scope) string {
	if scope.Type == accountuserquery.ScopeProduct {
		return scope.ProductID
	}
	if scope.Type == accountuserquery.ScopeTenant {
		return fmt.Sprintf("%s:%s", scope.ProductID, scope.TenantID)
	}
	return ""
}
