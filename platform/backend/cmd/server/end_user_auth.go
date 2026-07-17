package main

import (
	"context"
	"errors"

	"platform.local/capability-platform/backend/internal/modules/identity"
	identityhttp "platform.local/capability-platform/backend/internal/modules/identity/httptransport"
	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
	"platform.local/capability-platform/backend/internal/platform/requestid"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	"platform.local/capability-platform/backend/internal/workflows/accountaccess"
)

type endUserHTTPAdapter struct {
	users        *identity.EndUserService
	products     *product.Service
	clientHasher securevalue.Hasher
	access       *accountaccess.Service
}

type endUserAdmissionAdapter struct {
	access *accountaccess.Service
}

func (a endUserAdmissionAdapter) AdmitEndUser(ctx context.Context, request identity.EndUserAdmissionRequest) error {
	decision, err := a.access.Decide(ctx,
		accountaccess.UserContext{UserID: request.UserID, AccountStatus: "active"},
		accountaccess.Scope{ProductID: request.Scope.ProductID, ApplicationID: request.Scope.ApplicationID, TenantID: request.Scope.TenantID},
		accountaccess.OperationPolicy{},
	)
	if err != nil {
		return err
	}
	if decision.Allowed {
		return nil
	}
	switch decision.ReasonCode {
	case "PRODUCT_USER_ACCESS_SUSPENDED":
		return productuseraccess.ErrProductSuspended
	case "TENANT_USER_ACCESS_SUSPENDED":
		return productuseraccess.ErrTenantSuspended
	default:
		return accountaccess.ErrInvalidContext
	}
}

func (a endUserHTTPAdapter) ResolveClientSession(ctx context.Context, token string) (identityhttp.ClientSessionContext, error) {
	if a.products == nil || token == "" {
		return identityhttp.ClientSessionContext{}, identityhttp.ErrInvalidBearer
	}
	session, err := a.products.FindClientSession(ctx, "sha256:"+a.clientHasher.DigestHex("product-client-session:"+token))
	if err != nil {
		if errors.Is(err, product.ErrSessionUnavailable) {
			return identityhttp.ClientSessionContext{}, identityhttp.ErrInvalidBearer
		}
		return identityhttp.ClientSessionContext{}, err
	}
	return identityhttp.ClientSessionContext{ProductID: session.ProductID, ApplicationID: session.ApplicationID, TenantID: session.TenantID}, nil
}

func (a endUserHTTPAdapter) ResolveUserSession(ctx context.Context, token string) (identityhttp.UserSessionContext, error) {
	current, err := a.users.ResolveCurrentSession(ctx, token)
	if err != nil {
		return identityhttp.UserSessionContext{}, mapEndUserHTTPError(err)
	}
	return mapUserSessionContext(current.Session), nil
}

func (a endUserHTTPAdapter) ResolveLogoutSession(ctx context.Context, token string) (identityhttp.UserSessionContext, error) {
	return a.ResolveUserSession(ctx, token)
}

func (a endUserHTTPAdapter) RegisterUser(ctx context.Context, client identityhttp.ClientSessionContext, command identityhttp.RegisterUserCommand) (identityhttp.IssuedUserSession, error) {
	issued, err := a.users.Register(ctx, identity.EndUserRegisterCommand{
		Scope: scopeFromClient(client), Identifier: command.Identifier, Credential: command.Credential,
		VerificationProof: command.VerificationProof, DisplayName: command.DisplayName,
		TraceID: command.RequestID, IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return identityhttp.IssuedUserSession{}, mapEndUserHTTPError(err)
	}
	return mapIssuedUserSession(issued), nil
}

func (a endUserHTTPAdapter) LoginUser(ctx context.Context, client identityhttp.ClientSessionContext, command identityhttp.LoginUserCommand) (identityhttp.IssuedUserSession, error) {
	issued, err := a.users.Login(ctx, identity.EndUserLoginCommand{
		Scope: scopeFromClient(client), Identifier: command.Identifier, Credential: command.Credential,
		Source: command.Source, RiskSummary: command.DeviceRiskSummary, TraceID: command.RequestID,
	})
	if err != nil {
		return identityhttp.IssuedUserSession{}, mapEndUserHTTPError(err)
	}
	if err := a.requireAdmission(ctx, issued.Session); err != nil {
		if cleanupErr := a.users.Logout(ctx, issued.AccessToken, command.RequestID, endUserScopeFromSession(issued.Session)); cleanupErr != nil {
			return identityhttp.IssuedUserSession{}, cleanupErr
		}
		return identityhttp.IssuedUserSession{}, err
	}
	return mapIssuedUserSession(issued), nil
}

func (a endUserHTTPAdapter) CurrentUserSession(ctx context.Context, user identityhttp.UserSessionContext) (identityhttp.CurrentUserSession, error) {
	current, err := a.resolveAuthorized(ctx, user)
	if err != nil {
		return identityhttp.CurrentUserSession{}, err
	}
	return identityhttp.CurrentUserSession{
		SessionID: current.Session.SessionID, User: mapUserSummary(current),
		AccessExpiresAt: current.Session.AccessExpiresAt, RefreshExpiresAt: current.Session.RefreshExpiresAt,
	}, nil
}

func (a endUserHTTPAdapter) StartPasswordRecovery(ctx context.Context, client identityhttp.ClientSessionContext, command identityhttp.StartPasswordRecoveryCommand) (identityhttp.RecoveryChallengeResponse, error) {
	continuation, err := a.users.StartRecovery(ctx, identity.StartEndUserRecoveryCommand{
		Scope: scopeFromClient(client), Identifier: command.Identifier,
		IdempotencyKey: command.IdempotencyKey, TraceID: command.RequestID,
	})
	if err != nil {
		return identityhttp.RecoveryChallengeResponse{}, mapEndUserHTTPError(err)
	}
	return identityhttp.RecoveryChallengeResponse{Accepted: true, ContinuationID: continuation}, nil
}

func (a endUserHTTPAdapter) CompletePasswordRecovery(ctx context.Context, client identityhttp.ClientSessionContext, command identityhttp.CompletePasswordRecoveryCommand) error {
	err := a.users.CompleteRecovery(ctx, identity.CompleteEndUserRecoveryCommand{
		Scope: scopeFromClient(client), Continuation: command.ContinuationID, Proof: command.RecoveryProof,
		NewCredential: command.NewCredential, IdempotencyKey: command.IdempotencyKey, TraceID: command.RequestID,
	})
	return mapEndUserHTTPError(err)
}

func (a endUserHTTPAdapter) GetCurrentUserProfile(ctx context.Context, user identityhttp.UserSessionContext) (identityhttp.UserProfileResponse, error) {
	if _, err := a.resolveAuthorized(ctx, user); err != nil {
		return identityhttp.UserProfileResponse{}, err
	}
	profile, err := a.users.GetProfileResolved(ctx, user.AccessToken)
	if err != nil {
		return identityhttp.UserProfileResponse{}, mapEndUserHTTPError(err)
	}
	return mapUserProfile(profile), nil
}

func (a endUserHTTPAdapter) UpdateCurrentUserProfile(ctx context.Context, user identityhttp.UserSessionContext, command identityhttp.UpdateUserProfileCommand) (identityhttp.UserProfileResponse, error) {
	if _, err := a.resolveAuthorized(ctx, user); err != nil {
		return identityhttp.UserProfileResponse{}, err
	}
	updated, err := a.users.PatchProfileResolved(ctx, user.AccessToken, identity.EndUserProfilePatch{
		DisplayName: mapEndUserProfilePatchValue(command.DisplayName),
		AvatarRef:   mapEndUserProfilePatchValue(command.AvatarURL),
		Locale:      mapEndUserProfilePatchValue(command.Locale),
		Timezone:    mapEndUserProfilePatchValue(command.Timezone),
	}, command.ExpectedVersion, command.IdempotencyKey, command.RequestID)
	if err != nil {
		return identityhttp.UserProfileResponse{}, mapEndUserHTTPError(err)
	}
	return mapUserProfile(updated), nil
}

func (a endUserHTTPAdapter) ChangeCurrentUserPassword(ctx context.Context, user identityhttp.UserSessionContext, command identityhttp.ChangePasswordCommand) error {
	if _, err := a.resolveAuthorized(ctx, user); err != nil {
		return err
	}
	err := a.users.ChangePasswordResolved(ctx, user.AccessToken, command.CurrentCredential, command.NewCredential, command.RevokeOtherSessions, command.IdempotencyKey, command.RequestID)
	return mapEndUserHTTPError(err)
}

func (a endUserHTTPAdapter) ListCurrentUserSessions(ctx context.Context, user identityhttp.UserSessionContext) ([]identityhttp.UserSessionSummary, error) {
	if _, err := a.resolveAuthorized(ctx, user); err != nil {
		return nil, err
	}
	items, err := a.users.ListSessionsResolved(ctx, user.AccessToken)
	if err != nil {
		return nil, mapEndUserHTTPError(err)
	}
	result := make([]identityhttp.UserSessionSummary, len(items))
	for i, item := range items {
		result[i] = identityhttp.UserSessionSummary{SessionID: item.SessionID, Current: item.Current, CreatedAt: item.CreatedAt, LastSeenAt: item.LastSeenAt, ExpiresAt: item.ExpiresAt}
	}
	return result, nil
}

func (a endUserHTTPAdapter) RevokeCurrentUserSession(ctx context.Context, user identityhttp.UserSessionContext, sessionID string) error {
	if _, err := a.resolveAuthorized(ctx, user); err != nil {
		return err
	}
	return mapEndUserHTTPError(a.users.RevokeSessionResolved(ctx, user.AccessToken, sessionID, requestid.FromContext(ctx)))
}

func (a endUserHTTPAdapter) RefreshUserSession(ctx context.Context, command identityhttp.RefreshUserSessionCommand) (identityhttp.TokenPair, error) {
	issued, err := a.users.RefreshResolved(ctx, command.RefreshToken, command.ClientRequestID, command.RequestID)
	if err != nil {
		return identityhttp.TokenPair{}, mapEndUserHTTPError(err)
	}
	if err := a.requireAdmission(ctx, issued.Session); err != nil {
		if cleanupErr := a.users.Logout(ctx, issued.AccessToken, command.RequestID, endUserScopeFromSession(issued.Session)); cleanupErr != nil {
			return identityhttp.TokenPair{}, cleanupErr
		}
		return identityhttp.TokenPair{}, err
	}
	return identityhttp.TokenPair{AccessToken: issued.AccessToken, RefreshToken: issued.RefreshToken, AccessExpiresAt: issued.Session.AccessExpiresAt, RefreshExpiresAt: issued.Session.RefreshExpiresAt}, nil
}

func (a endUserHTTPAdapter) LogoutUser(ctx context.Context, user identityhttp.UserSessionContext) error {
	return mapEndUserHTTPError(a.users.LogoutResolved(ctx, user.AccessToken, requestid.FromContext(ctx)))
}

func (a endUserHTTPAdapter) GetCurrentUserAccess(ctx context.Context, user identityhttp.UserSessionContext) (identityhttp.ProductUserAccessDecision, error) {
	current, err := a.users.ResolveCurrentSession(ctx, user.AccessToken)
	if err != nil {
		return identityhttp.ProductUserAccessDecision{}, mapEndUserHTTPError(err)
	}
	decision, err := a.access.Decide(ctx,
		accountaccess.UserContext{UserID: current.Session.UserID, AccountStatus: current.Session.AccountStatus},
		accountaccess.Scope{ProductID: current.Session.ProductID, ApplicationID: current.Session.ApplicationID, TenantID: current.Session.TenantID},
		accountaccess.OperationPolicy{},
	)
	if err != nil {
		return identityhttp.ProductUserAccessDecision{}, err
	}
	var reason *string
	if decision.ReasonCode != "" {
		value := decision.ReasonCode
		reason = &value
	}
	return identityhttp.ProductUserAccessDecision{Allowed: decision.Allowed, DecisionStage: decision.DecisionStage, ReasonCode: reason}, nil
}

func (a endUserHTTPAdapter) resolveAuthorized(ctx context.Context, claimed identityhttp.UserSessionContext) (identity.EndUserIssuedSession, error) {
	current, err := a.users.ResolveCurrentSession(ctx, claimed.AccessToken)
	if err != nil {
		return identity.EndUserIssuedSession{}, mapEndUserHTTPError(err)
	}
	if claimed.UserID != current.Session.UserID || claimed.SessionID != current.Session.SessionID || claimed.ProductID != current.Session.ProductID || claimed.ApplicationID != current.Session.ApplicationID || claimed.TenantID != optionalTenant(current.Session.TenantID) {
		return identity.EndUserIssuedSession{}, identityhttp.ErrInvalidBearer
	}
	if err := a.requireAdmission(ctx, current.Session); err != nil {
		return identity.EndUserIssuedSession{}, err
	}
	return current, nil
}

func (a endUserHTTPAdapter) requireAdmission(ctx context.Context, session identity.EndUserSession) error {
	decision, err := a.access.Decide(ctx,
		accountaccess.UserContext{UserID: session.UserID, AccountStatus: session.AccountStatus},
		accountaccess.Scope{ProductID: session.ProductID, ApplicationID: session.ApplicationID, TenantID: session.TenantID},
		accountaccess.OperationPolicy{},
	)
	if err != nil {
		return err
	}
	if decision.Allowed {
		return nil
	}
	switch decision.ReasonCode {
	case "IDENTITY_ACCOUNT_DISABLED":
		return identityhttp.ErrUserAccountDisabled
	case "PRODUCT_USER_ACCESS_SUSPENDED":
		return identityhttp.ErrProductUserAccessSuspended
	case "TENANT_USER_ACCESS_SUSPENDED":
		return identityhttp.ErrTenantUserAccessSuspended
	default:
		return errors.New("unrecognized account access denial")
	}
}

func mapEndUserHTTPError(err error) error {
	if err == nil {
		return nil
	}
	var rate *identity.EndUserRateLimitError
	switch {
	case errors.As(err, &rate):
		return &identityhttp.UserRateLimitError{RetryAfter: rate.RetryAfter}
	case errors.Is(err, identity.ErrInvalidEndUserIdentifier):
		return identityhttp.ErrInvalidUserRequest
	case errors.Is(err, identity.ErrEndUserInvalidCredentials):
		return identityhttp.ErrInvalidUserCredentials
	case errors.Is(err, identity.ErrRecoveryProofInvalid):
		return identityhttp.ErrRecoveryInvalid
	case errors.Is(err, identity.ErrEndUserProviderUnavailable):
		return identityhttp.ErrUserProviderUnavailable
	case errors.Is(err, identity.ErrEndUserAccountDisabled):
		return identityhttp.ErrUserAccountDisabled
	case errors.Is(err, identity.ErrEndUserSessionExpired):
		return identityhttp.ErrUserSessionExpired
	case errors.Is(err, identity.ErrEndUserSessionRevoked):
		return identityhttp.ErrUserSessionRevoked
	case errors.Is(err, identity.ErrEndUserRefreshReplayed):
		return identityhttp.ErrUserRefreshReplayed
	case errors.Is(err, identity.ErrEndUserIdentifierConflict):
		return identityhttp.ErrUserConflict
	case errors.Is(err, identity.ErrEndUserVersionConflict):
		return identityhttp.ErrUserVersionConflict
	case errors.Is(err, identity.ErrEndUserReauthenticationRequired):
		return identityhttp.ErrRecentAuthenticationNeeded
	case errors.Is(err, identity.ErrRecoveryChallengeExpired):
		return identityhttp.ErrRecoveryExpired
	case errors.Is(err, identity.ErrRecoveryProofReplayed):
		return identityhttp.ErrRecoveryReplayed
	case errors.Is(err, productuseraccess.ErrProductSuspended):
		return identityhttp.ErrProductUserAccessSuspended
	case errors.Is(err, productuseraccess.ErrTenantSuspended):
		return identityhttp.ErrTenantUserAccessSuspended
	default:
		return err
	}
}

func scopeFromClient(value identityhttp.ClientSessionContext) identity.EndUserSessionScope {
	var tenantID *string
	if value.TenantID != "" {
		tenant := value.TenantID
		tenantID = &tenant
	}
	return identity.EndUserSessionScope{ProductID: value.ProductID, ApplicationID: value.ApplicationID, TenantID: tenantID}
}

func mapUserSessionContext(session identity.EndUserSession) identityhttp.UserSessionContext {
	return identityhttp.UserSessionContext{UserID: session.UserID, SessionID: session.SessionID, ProductID: session.ProductID, ApplicationID: session.ApplicationID, TenantID: optionalTenant(session.TenantID), AccountStatus: session.AccountStatus, AuthTime: session.AuthTime}
}

func mapIssuedUserSession(value identity.EndUserIssuedSession) identityhttp.IssuedUserSession {
	return identityhttp.IssuedUserSession{TokenPair: identityhttp.TokenPair{
		AccessToken: value.AccessToken, RefreshToken: value.RefreshToken,
		AccessExpiresAt: value.Session.AccessExpiresAt, RefreshExpiresAt: value.Session.RefreshExpiresAt,
	}, User: mapUserSummary(value)}
}

func mapUserSummary(value identity.EndUserIssuedSession) identityhttp.UserSummary {
	displayName := value.Profile.DisplayName
	productID := value.Session.ProductID
	tenantID := value.Session.TenantID
	return identityhttp.UserSummary{UserID: value.Session.UserID, AccountStatus: value.Session.AccountStatus, DisplayName: &displayName, ProductID: &productID, TenantID: tenantID}
}

func mapUserProfile(value identity.EndUserProfile) identityhttp.UserProfileResponse {
	displayName := value.DisplayName
	return identityhttp.UserProfileResponse{UserID: value.UserID, Version: value.Version, DisplayName: &displayName, AvatarURL: value.AvatarRef, Locale: value.Locale, Timezone: value.Timezone}
}

func mapEndUserProfilePatchValue(value identityhttp.OptionalString) identity.EndUserProfilePatchValue {
	return identity.EndUserProfilePatchValue{Set: value.Set, Value: value.Value}
}

func endUserScopeFromSession(session identity.EndUserSession) identity.EndUserSessionScope {
	return identity.EndUserSessionScope{ProductID: session.ProductID, ApplicationID: session.ApplicationID, TenantID: session.TenantID}
}

func optionalTenant(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

var _ identityhttp.UserSessionResolver = endUserHTTPAdapter{}
var _ identityhttp.UserService = endUserHTTPAdapter{}
