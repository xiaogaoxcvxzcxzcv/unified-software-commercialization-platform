package main

import (
	"context"
	"errors"

	"platform.local/capability-platform/backend/internal/modules/identity"
	identityhttp "platform.local/capability-platform/backend/internal/modules/identity/httptransport"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

type productApplicationReturnTargetAdapter struct {
	applications *productapplication.Service
}

func (a productApplicationReturnTargetAdapter) ResolveAuthReturnTarget(ctx context.Context, scope identity.EndUserSessionScope, environment, code string) (identity.AuthReturnTarget, error) {
	value, err := a.applications.ResolveAuthReturnTarget(ctx, productapplication.ProductContext{ProductID: scope.ProductID, Environment: productapplication.Environment(environment)}, scope.ApplicationID, code)
	if err != nil {
		return identity.AuthReturnTarget{}, err
	}
	return identity.AuthReturnTarget{Code: value.Code, URI: value.URI, PolicyVersion: value.PolicyVersion}, nil
}

type disabledExternalProviderRegistry struct{}

func (disabledExternalProviderRegistry) ResolveExternalProvider(context.Context, identity.ExternalProviderQuery) (identity.ExternalProviderApplication, error) {
	return identity.ExternalProviderApplication{}, identity.ErrExternalProviderDisabled
}

type disabledExternalIdentityProvider struct{}

func (disabledExternalIdentityProvider) StartAuthorization(context.Context, identity.ExternalProviderApplication, identity.ExternalAuthorizationRequest) (identity.ExternalAuthorization, error) {
	return identity.ExternalAuthorization{}, identity.ErrExternalProviderDisabled
}

func (disabledExternalIdentityProvider) ExchangeAuthorizationCode(context.Context, identity.ExternalProviderApplication, string, string, string) (identity.VerifiedExternalClaims, error) {
	return identity.VerifiedExternalClaims{}, identity.ErrExternalProviderDisabled
}

type externalUserHTTPAdapter struct {
	base         endUserHTTPAdapter
	external     *identity.ExternalAuthService
	verification *identity.RegistrationVerificationService
}

func (a externalUserHTTPAdapter) StartRegistrationVerification(ctx context.Context, client identityhttp.ClientSessionContext, command identityhttp.StartRegistrationVerificationCommand) (identityhttp.VerificationChallengeResponse, error) {
	if a.verification == nil {
		return identityhttp.VerificationChallengeResponse{}, identityhttp.ErrUserProviderUnavailable
	}
	continuation, err := a.verification.Start(ctx, identity.StartRegistrationVerificationCommand{Scope: scopeFromClient(client), Identifier: command.Identifier, IdempotencyKey: command.IdempotencyKey, TraceID: command.RequestID})
	if err != nil {
		return identityhttp.VerificationChallengeResponse{}, mapExternalUserHTTPError(err)
	}
	return identityhttp.VerificationChallengeResponse{Accepted: true, ContinuationID: continuation}, nil
}

func (a externalUserHTTPAdapter) StartExternalLogin(ctx context.Context, client identityhttp.ClientSessionContext, provider string, command identityhttp.StartExternalLoginCommand) (identityhttp.ExternalLoginFlowResponse, error) {
	if a.external == nil {
		return identityhttp.ExternalLoginFlowResponse{}, identityhttp.ErrUserProviderUnavailable
	}
	value, err := a.external.Start(ctx, identity.ExternalAuthStartCommand{Scope: scopeFromClient(client), Environment: client.Environment, Provider: provider, Mode: command.Mode, ReturnTargetCode: command.ReturnTargetCode, BrowserSession: client.SessionID, TraceID: command.RequestID})
	if err != nil {
		return identityhttp.ExternalLoginFlowResponse{}, mapExternalUserHTTPError(err)
	}
	return identityhttp.ExternalLoginFlowResponse{FlowID: value.FlowID, Mode: value.Mode, AuthorizationURL: value.AuthorizationURL, QRPayload: value.QRPayload, ExpiresAt: value.ExpiresAt}, nil
}

func (a externalUserHTTPAdapter) CompleteExternalLogin(ctx context.Context, client identityhttp.ClientSessionContext, provider string, command identityhttp.CompleteExternalLoginCommand) (identityhttp.ExternalExchangeResponse, error) {
	if a.external == nil {
		return identityhttp.ExternalExchangeResponse{}, identityhttp.ErrUserProviderUnavailable
	}
	scope := scopeFromClient(client)
	value, err := a.external.Complete(ctx, identity.ExternalAuthCallbackCommand{FlowID: command.FlowID, Provider: provider, ExpectedScope: &scope, BrowserSession: client.SessionID, State: command.State, Code: command.Code, ProviderError: command.ProviderError, TraceID: command.RequestID})
	if err != nil {
		return identityhttp.ExternalExchangeResponse{}, mapExternalUserHTTPError(err)
	}
	result := identityhttp.ExternalExchangeResponse{Status: value.Status, ProofID: value.ExternalProofID}
	if value.Session != nil {
		mapped := mapIssuedUserSession(*value.Session)
		result.Session = &mapped
	}
	return result, nil
}

func (a externalUserHTTPAdapter) LinkExternalIdentity(ctx context.Context, user identityhttp.UserSessionContext, provider string, command identityhttp.LinkExternalIdentityCommand) (identityhttp.ExternalIdentityResponse, error) {
	if a.external == nil {
		return identityhttp.ExternalIdentityResponse{}, identityhttp.ErrUserProviderUnavailable
	}
	current, err := a.base.resolveAuthorized(ctx, user)
	if err != nil {
		return identityhttp.ExternalIdentityResponse{}, err
	}
	value, err := a.external.Link(ctx, identity.LinkExternalIdentityCommand{Session: current.Session, Provider: provider, ExternalProofID: command.ExternalProofID, IdempotencyKey: command.IdempotencyKey, TraceID: command.RequestID})
	if err != nil {
		return identityhttp.ExternalIdentityResponse{}, mapExternalUserHTTPError(err)
	}
	return mapExternalIdentity(value), nil
}

func (a externalUserHTTPAdapter) ListExternalIdentities(ctx context.Context, user identityhttp.UserSessionContext) ([]identityhttp.ExternalIdentityResponse, error) {
	if a.external == nil {
		return nil, identityhttp.ErrUserProviderUnavailable
	}
	current, err := a.base.resolveAuthorized(ctx, user)
	if err != nil {
		return nil, err
	}
	values, err := a.external.List(ctx, current.Session)
	if err != nil {
		return nil, mapExternalUserHTTPError(err)
	}
	result := make([]identityhttp.ExternalIdentityResponse, len(values))
	for i, value := range values {
		result[i] = mapExternalIdentity(value)
	}
	return result, nil
}

func (a externalUserHTTPAdapter) UnlinkExternalIdentity(ctx context.Context, user identityhttp.UserSessionContext, externalIdentityID string) error {
	if a.external == nil {
		return identityhttp.ErrUserProviderUnavailable
	}
	current, err := a.base.resolveAuthorized(ctx, user)
	if err != nil {
		return err
	}
	return mapExternalUserHTTPError(a.external.Unlink(ctx, identity.UnlinkExternalIdentityCommand{Session: current.Session, ExternalIdentityID: externalIdentityID, TraceID: requestid.FromContext(ctx)}))
}

func mapExternalIdentity(value identity.ExternalIdentity) identityhttp.ExternalIdentityResponse {
	return identityhttp.ExternalIdentityResponse{ExternalIdentityID: value.ExternalIdentityID, Provider: value.Provider, MaskedSubject: value.SubjectMasked, Status: value.Status, LinkedAt: value.LinkedAt, AuditID: value.OutboxEvent.Payload.AuditID}
}

func mapExternalUserHTTPError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, identity.ErrExternalProviderDisabled), errors.Is(err, identity.ErrEndUserProviderUnavailable):
		return identityhttp.ErrUserProviderUnavailable
	case errors.Is(err, identity.ErrExternalAuthFlowExpired):
		return identityhttp.ErrExternalFlowExpired
	case errors.Is(err, identity.ErrExternalAuthFlowReplayed):
		return identityhttp.ErrExternalFlowReplayed
	case errors.Is(err, identity.ErrExternalAuthFlowInvalid), errors.Is(err, identity.ErrExternalProofInvalid):
		return identityhttp.ErrExternalFlowInvalid
	case errors.Is(err, identity.ErrExternalIdentityConflict):
		return identityhttp.ErrExternalIdentityConflict
	case errors.Is(err, identity.ErrExternalIdentityNotOwned):
		return identityhttp.ErrExternalIdentityNotOwned
	case errors.Is(err, identity.ErrExternalIdentityLastLogin):
		return identityhttp.ErrExternalIdentityLastLogin
	case errors.Is(err, identity.ErrExternalRecentAuthRequired):
		return identityhttp.ErrRecentAuthenticationNeeded
	case errors.Is(err, identity.ErrRegistrationVerificationInvalid), errors.Is(err, identity.ErrRegistrationVerificationExpired), errors.Is(err, identity.ErrRegistrationVerificationReplayed):
		return identityhttp.ErrRegistrationVerificationFail
	default:
		return err
	}
}
