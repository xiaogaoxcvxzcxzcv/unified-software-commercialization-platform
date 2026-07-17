package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/hostedinteraction"
	hostedhttp "platform.local/capability-platform/backend/internal/modules/hostedinteraction/httptransport"
	hostedpostgres "platform.local/capability-platform/backend/internal/modules/hostedinteraction/postgres"
	"platform.local/capability-platform/backend/internal/modules/identity"
	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

type hostedInteractionRuntime struct {
	service *hostedinteraction.Service
	handler *hostedhttp.Handler
}

func newHostedInteractionRuntime(cfg config.HostedInteraction, pool *pgxpool.Pool, products *product.Service, applications *productapplication.Service, users *identity.EndUserService, clientHasher securevalue.Hasher) (hostedInteractionRuntime, error) {
	if pool == nil || products == nil || applications == nil || users == nil || !clientHasher.Configured() {
		return hostedInteractionRuntime{}, errors.New("hosted interaction dependencies are required")
	}
	digester, err := securevalue.NewHasher(cfg.DigestKey)
	if err != nil {
		return hostedInteractionRuntime{}, err
	}
	stateKey := sha256.Sum256([]byte("hosted-interaction.state-key.v1\x00" + cfg.StateKey))
	protector, err := hostedinteraction.NewAEADStateProtector(cfg.StateKeyRef, hostedStateSecretResolver{reference: cfg.StateKeyRef, key: stateKey})
	if err != nil {
		return hostedInteractionRuntime{}, err
	}
	identityAdapter := hostedIdentityAdapter{users: users}
	service, err := hostedinteraction.NewServiceWithPolicy(
		hostedpostgres.New(pool),
		hostedReturnTargetAdapter{applications: applications},
		identityAdapter,
		identityAdapter,
		protector,
		digester,
		cfg.BaseURL,
		hostedinteraction.ServicePolicy{
			InteractionTTL: cfg.InteractionTTL,
			BrowserTTL:     cfg.BrowserTTL,
			AuthLeaseTTL:   cfg.AuthLeaseTTL,
			GrantTTL:       cfg.GrantTTL,
			LeaseTTL:       cfg.GrantLeaseTTL,
		},
	)
	if err != nil {
		return hostedInteractionRuntime{}, err
	}
	httpAdapter := hostedHTTPServiceAdapter{service: service}
	authenticator := hostedHTTPAuthenticator{products: products, applications: applications, users: users, clientHasher: clientHasher, hosted: service}
	handler, err := hostedhttp.New(httpAdapter, authenticator, cfg.AllowedOrigin)
	if err != nil {
		return hostedInteractionRuntime{}, err
	}
	return hostedInteractionRuntime{service: service, handler: handler}, nil
}

type hostedStateSecretResolver struct {
	reference string
	key       [sha256.Size]byte
}

func (r hostedStateSecretResolver) ResolveSecret(_ context.Context, reference string) ([]byte, error) {
	if reference == "" || reference != r.reference {
		return nil, hostedinteraction.ErrTemporarilyUnavailable
	}
	return append([]byte(nil), r.key[:]...), nil
}

type hostedReturnTargetAdapter struct{ applications *productapplication.Service }

func (a hostedReturnTargetAdapter) ResolveHostedReturnTarget(ctx context.Context, scope hostedinteraction.Scope, code string) (hostedinteraction.ReturnTarget, error) {
	value, err := a.applications.ResolveAuthReturnTarget(ctx, productapplication.ProductContext{ProductID: scope.ProductID, Environment: productapplication.Environment(scope.Environment)}, scope.ApplicationID, code)
	if err != nil {
		switch {
		case errors.Is(err, productapplication.ErrInvalidArgument), errors.Is(err, productapplication.ErrNotFound), errors.Is(err, productapplication.ErrContextRejected), errors.Is(err, productapplication.ErrApplicationSuspended):
			return hostedinteraction.ReturnTarget{}, hostedinteraction.ErrInvalidReturnTarget
		default:
			return hostedinteraction.ReturnTarget{}, err
		}
	}
	return hostedinteraction.ReturnTarget{ProductID: value.ProductID, ApplicationID: value.ApplicationID, Code: value.Code, URI: value.URI, PolicyVersion: value.PolicyVersion, Kind: string(value.Kind)}, nil
}

type hostedIdentityAdapter struct{ users *identity.EndUserService }

func (a hostedIdentityAdapter) AuthenticateHosted(ctx context.Context, scope hostedinteraction.Scope, identifier, credential, source string, risk map[string]any, traceID string) (hostedinteraction.HostedAuthProof, error) {
	value, err := a.users.AuthenticateHosted(ctx, identity.AuthenticateHostedCommand{Scope: identityScope(scope), Identifier: identifier, Credential: credential, Source: source, RiskSummary: risk, TraceID: traceID})
	if err != nil {
		return hostedinteraction.HostedAuthProof{}, mapHostedIdentityAuthenticationError(err)
	}
	summary := map[string]string{"display_name": value.User.DisplayName}
	if value.User.AvatarRef != nil {
		summary["avatar_ref"] = *value.User.AvatarRef
	}
	return hostedinteraction.HostedAuthProof{ProofID: value.ProofID, SafeUserSummary: summary, AuthTime: value.AuthTime, ExpiresAt: value.ExpiresAt}, nil
}

func (a hostedIdentityAdapter) RedeemHostedAuthGrant(ctx context.Context, grantID, proofID string, scope hostedinteraction.Scope, traceID string) (hostedinteraction.IssuedUserSession, error) {
	value, err := a.users.RedeemHostedAuthGrant(ctx, identity.RedeemHostedAuthGrantCommand{GrantID: grantID, ProofID: proofID, Scope: identityScope(scope), TraceID: traceID})
	if err != nil {
		return hostedinteraction.IssuedUserSession{}, mapHostedIdentityRedemptionError(err)
	}
	productID := value.Session.ProductID
	return hostedinteraction.IssuedUserSession{
		SessionID: value.Session.SessionID, AccessToken: value.AccessToken, RefreshToken: value.RefreshToken,
		AccessExpiresAt: value.Session.AccessExpiresAt, RefreshExpiresAt: value.Session.RefreshExpiresAt,
		User: hostedinteraction.SafeUserSummary{
			UserID: value.Session.UserID, AccountStatus: value.Session.AccountStatus, DisplayName: value.Profile.DisplayName,
			ProductID: &productID, TenantID: value.Session.TenantID,
		},
	}, nil
}

func (a hostedIdentityAdapter) ValidateHostedAccountSession(ctx context.Context, scope hostedinteraction.Scope, actor hostedinteraction.Actor) error {
	err := a.users.ValidateHostedSession(ctx, identity.HostedSessionExpectation{Scope: identityScope(scope), UserID: actor.UserID, SessionID: actor.UserSessionID})
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, identity.ErrEndUserScopeMismatch), errors.Is(err, identity.ErrEndUserSessionExpired), errors.Is(err, identity.ErrEndUserSessionRevoked), errors.Is(err, identity.ErrEndUserAccountDisabled), errors.Is(err, identity.ErrNotFound):
		return hostedinteraction.ErrSessionRevoked
	default:
		return err
	}
}

func identityScope(scope hostedinteraction.Scope) identity.EndUserSessionScope {
	return identity.EndUserSessionScope{ProductID: scope.ProductID, ApplicationID: scope.ApplicationID, TenantID: scope.TenantID, Environment: scope.Environment}
}

func mapHostedIdentityAuthenticationError(err error) error {
	switch {
	case errors.Is(err, identity.ErrEndUserInvalidCredentials), errors.Is(err, identity.ErrEndUserAccountDisabled), errors.Is(err, identity.ErrEndUserScopeMismatch):
		return hostedinteraction.ErrAuthenticationNeeded
	case errors.Is(err, identity.ErrHostedAuthProofInvalid):
		return hostedinteraction.ErrAuthenticationNeeded
	default:
		return err
	}
}

func mapHostedIdentityRedemptionError(err error) error {
	switch {
	case errors.Is(err, identity.ErrHostedAuthProofInvalid), errors.Is(err, identity.ErrHostedAuthProofExpired), errors.Is(err, identity.ErrHostedAuthProofReplayed), errors.Is(err, identity.ErrHostedAuthGrantConflict), errors.Is(err, identity.ErrEndUserScopeMismatch):
		return hostedinteraction.ErrInvalidGrant
	case errors.Is(err, identity.ErrEndUserSessionExpired), errors.Is(err, identity.ErrEndUserSessionRevoked), errors.Is(err, identity.ErrEndUserAccountDisabled):
		return hostedinteraction.ErrSessionRevoked
	default:
		return err
	}
}

type hostedHTTPAuthenticator struct {
	products     *product.Service
	applications *productapplication.Service
	users        *identity.EndUserService
	clientHasher securevalue.Hasher
	hosted       *hostedinteraction.Service
}

func (a hostedHTTPAuthenticator) ResolveBearer(ctx context.Context, token string) (hostedhttp.BearerPrincipal, error) {
	client, err := a.products.FindClientSession(ctx, "sha256:"+a.clientHasher.DigestHex("product-client-session:"+token))
	if err == nil {
		scope, scopeErr := a.scope(ctx, client.ProductID, client.ApplicationID, optionalHostedTenant(client.TenantID), client.Environment)
		if scopeErr != nil {
			return hostedhttp.BearerPrincipal{}, scopeErr
		}
		return hostedhttp.BearerPrincipal{Kind: "client", Scope: scope, SessionID: client.SessionID}, nil
	}
	if !errors.Is(err, product.ErrSessionUnavailable) {
		return hostedhttp.BearerPrincipal{}, err
	}
	current, err := a.users.ResolveCurrentSession(ctx, token)
	if err != nil {
		if isUnavailableUserSession(err) {
			return hostedhttp.BearerPrincipal{}, hostedhttp.ErrAuthenticationRequired
		}
		return hostedhttp.BearerPrincipal{}, err
	}
	scope, err := a.scope(ctx, current.Session.ProductID, current.Session.ApplicationID, current.Session.TenantID, current.Session.Environment)
	if err != nil {
		return hostedhttp.BearerPrincipal{}, err
	}
	return hostedhttp.BearerPrincipal{Kind: "user", Scope: scope, SessionID: current.Session.SessionID, UserID: current.Session.UserID, AuthTime: current.Session.AuthTime}, nil
}

func (a hostedHTTPAuthenticator) ResolveHostedSession(ctx context.Context, interactionID, token string) (hostedhttp.HostedPrincipal, error) {
	_, browserSessionID, csrf, err := a.hosted.ResolveBrowserAccess(ctx, interactionID, token)
	if err != nil {
		return hostedhttp.HostedPrincipal{}, mapHostedSessionAuthenticationError(err)
	}
	return hostedhttp.HostedPrincipal{InteractionID: interactionID, BrowserSessionID: browserSessionID, BrowserToken: token, CSRFToken: csrf}, nil
}

func mapHostedSessionAuthenticationError(err error) error {
	switch {
	case errors.Is(err, hostedinteraction.ErrInteractionExpired):
		return hostedhttp.ErrInteractionExpired
	case errors.Is(err, hostedinteraction.ErrSessionRevoked), errors.Is(err, hostedinteraction.ErrInvalidArgument):
		return hostedhttp.ErrSessionRevoked
	default:
		return err
	}
}

func (a hostedHTTPAuthenticator) scope(ctx context.Context, productID, applicationID string, tenantID *string, environment string) (hostedhttp.Scope, error) {
	if !identity.ValidEndUserEnvironment(environment) {
		return hostedhttp.Scope{}, hostedhttp.ErrAuthenticationRequired
	}
	application, err := a.applications.GetApplication(ctx, productapplication.ProductContext{ProductID: productID, Environment: productapplication.Environment(environment)}, applicationID)
	if err != nil {
		if errors.Is(err, productapplication.ErrInvalidArgument) || errors.Is(err, productapplication.ErrNotFound) || errors.Is(err, productapplication.ErrContextRejected) || errors.Is(err, productapplication.ErrApplicationSuspended) {
			return hostedhttp.Scope{}, hostedhttp.ErrAuthenticationRequired
		}
		return hostedhttp.Scope{}, err
	}
	if application.Status != productapplication.StatusActive {
		return hostedhttp.Scope{}, hostedhttp.ErrAuthenticationRequired
	}
	channel, err := hostedChannel(application.Platform)
	if err != nil {
		return hostedhttp.Scope{}, err
	}
	return hostedhttp.Scope{ProductID: productID, ApplicationID: applicationID, TenantID: tenantID, Environment: environment, Channel: channel}, nil
}

func hostedChannel(platform productapplication.Platform) (string, error) {
	switch platform {
	case productapplication.PlatformWeb:
		return "web", nil
	case productapplication.PlatformH5:
		return "h5", nil
	case productapplication.PlatformWindows, productapplication.PlatformMacOS, productapplication.PlatformLinux:
		return "desktop", nil
	case productapplication.PlatformAndroid, productapplication.PlatformIOS, productapplication.PlatformWechatMiniProgram:
		return "app", nil
	default:
		return "", hostedhttp.ErrChannelNotSupported
	}
}

func optionalHostedTenant(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func isUnavailableUserSession(err error) bool {
	return errors.Is(err, identity.ErrEndUserSessionExpired) || errors.Is(err, identity.ErrEndUserSessionRevoked) || errors.Is(err, identity.ErrEndUserAccountDisabled) || errors.Is(err, identity.ErrEndUserScopeMismatch) || errors.Is(err, identity.ErrNotFound)
}

type hostedHTTPServiceAdapter struct{ service *hostedinteraction.Service }

func (a hostedHTTPServiceAdapter) Create(ctx context.Context, principal hostedhttp.BearerPrincipal, command hostedhttp.CreateCommand) (hostedhttp.InteractionLaunch, error) {
	scope := coreHostedScope(principal.Scope)
	actor := hostedinteraction.Actor{Kind: principal.Kind}
	if principal.Kind == "client" {
		actor.ClientSessionID = principal.SessionID
	} else {
		actor.UserID, actor.UserSessionID = principal.UserID, principal.SessionID
	}
	value, err := a.service.Create(ctx, hostedinteraction.CreateCommand{
		Scope: scope, Actor: actor, Route: hostedinteraction.Route(command.RouteID), ReturnTargetCode: command.ReturnTargetCode,
		State: command.State, Nonce: command.Nonce, CodeChallenge: command.CodeChallenge, CodeChallengeMethod: command.CodeChallengeMethod,
		Locale: valueOrEmpty(command.Locale), ThemeVariant: valueOrEmpty(command.ThemeVariant), IdempotencyKey: command.IdempotencyKey, TraceID: command.RequestID,
	})
	if err != nil {
		return hostedhttp.InteractionLaunch{}, mapHostedCoreError(err)
	}
	return hostedhttp.InteractionLaunch{InteractionID: value.InteractionID, InteractionURL: value.InteractionURL, RouteID: string(value.Route), Status: string(value.Status), ExpiresAt: value.ExpiresAt}, nil
}

func (a hostedHTTPServiceAdapter) Get(ctx context.Context, principal hostedhttp.AccessPrincipal, interactionID string) (hostedhttp.Interaction, error) {
	var value hostedinteraction.Projection
	var err error
	if principal.Hosted != nil {
		value, err = a.service.GetForBrowser(ctx, interactionID, principal.Hosted.BrowserToken)
	} else if principal.Bearer != nil {
		actor := hostedinteraction.Actor{Kind: principal.Bearer.Kind}
		if actor.Kind == "client" {
			actor.ClientSessionID = principal.Bearer.SessionID
		} else {
			actor.UserID, actor.UserSessionID = principal.Bearer.UserID, principal.Bearer.SessionID
		}
		value, err = a.service.GetForScope(ctx, interactionID, coreHostedScope(principal.Bearer.Scope), actor)
	} else {
		err = hostedinteraction.ErrAuthenticationNeeded
	}
	if err != nil {
		return hostedhttp.Interaction{}, mapHostedCoreError(err)
	}
	return mapHostedProjection(value), nil
}

func (a hostedHTTPServiceAdapter) OpenBrowserSession(ctx context.Context, interactionID string) (hostedhttp.BrowserSession, error) {
	session, projection, err := a.service.OpenBrowserSession(ctx, interactionID)
	if err != nil {
		return hostedhttp.BrowserSession{}, mapHostedCoreError(err)
	}
	return hostedhttp.BrowserSession{Interaction: mapHostedProjection(projection), CSRFToken: session.CSRFToken, BrowserSessionExpiresAt: session.ExpiresAt, CookieToken: session.Token}, nil
}

func (a hostedHTTPServiceAdapter) AuthenticatePassword(ctx context.Context, principal hostedhttp.HostedPrincipal, interactionID string, command hostedhttp.PasswordCommand) (hostedhttp.Completion, error) {
	value, err := a.service.Authenticate(ctx, hostedinteraction.AuthenticateCommand{InteractionID: interactionID, BrowserToken: principal.BrowserToken, Identifier: command.Identifier, Credential: command.Credential, Source: principal.BrowserSessionID, Risk: command.RiskSummary, TraceID: command.RequestID})
	if err != nil {
		return hostedhttp.Completion{}, mapHostedCoreError(err)
	}
	return mapHostedCompletion(value), nil
}

func (a hostedHTTPServiceAdapter) CompleteAccount(ctx context.Context, principal hostedhttp.HostedPrincipal, interactionID string, command hostedhttp.CompleteAccountCommand) (hostedhttp.Completion, error) {
	value, err := a.service.CompleteAccount(ctx, hostedinteraction.CompleteAccountCommand{InteractionID: interactionID, BrowserToken: principal.BrowserToken, IdempotencyKey: command.IdempotencyKey, TraceID: command.RequestID, Result: command.Result})
	if err != nil {
		return hostedhttp.Completion{}, mapHostedCoreError(err)
	}
	return mapHostedCompletion(value), nil
}

func (a hostedHTTPServiceAdapter) Cancel(ctx context.Context, principal hostedhttp.HostedPrincipal, interactionID, requestID string) (hostedhttp.Interaction, error) {
	value, err := a.service.Cancel(ctx, interactionID, principal.BrowserToken)
	if err != nil {
		return hostedhttp.Interaction{}, mapHostedCoreError(err)
	}
	return mapHostedProjection(value), nil
}

func (a hostedHTTPServiceAdapter) Exchange(ctx context.Context, principal hostedhttp.BearerPrincipal, interactionID string, command hostedhttp.ExchangeCommand) (hostedhttp.ExchangeResult, error) {
	value, err := a.service.Exchange(ctx, hostedinteraction.ExchangeCommand{InteractionID: interactionID, Code: command.Code, CodeVerifier: command.CodeVerifier, TraceID: command.RequestID, Scope: coreHostedScope(principal.Scope)})
	if err != nil {
		return hostedhttp.ExchangeResult{}, mapHostedCoreError(err)
	}
	result := hostedhttp.ExchangeResult{InteractionID: value.Interaction.InteractionID}
	switch value.ResultKind {
	case "authorization_code":
		if value.UserSession == nil {
			return hostedhttp.ExchangeResult{}, hostedhttp.ErrTemporarilyUnavailable
		}
		result.ResultType = "user_session"
		result.UserSession = mapHostedIssuedSession(*value.UserSession)
	case "account_completed":
		accountResult, ok := value.Document["result"].(string)
		if !ok {
			return hostedhttp.ExchangeResult{}, hostedhttp.ErrTemporarilyUnavailable
		}
		result.ResultType = "account_completed"
		result.AccountResult = &hostedhttp.AccountResult{Result: accountResult}
	default:
		return hostedhttp.ExchangeResult{}, hostedhttp.ErrTemporarilyUnavailable
	}
	return result, nil
}

func coreHostedScope(scope hostedhttp.Scope) hostedinteraction.Scope {
	return hostedinteraction.Scope{ProductID: scope.ProductID, ApplicationID: scope.ApplicationID, TenantID: scope.TenantID, Environment: scope.Environment, Channel: hostedinteraction.Channel(scope.Channel)}
}

func mapHostedProjection(value hostedinteraction.Projection) hostedhttp.Interaction {
	return hostedhttp.Interaction{
		InteractionID: value.InteractionID, RouteID: string(value.Route), Channel: string(value.Channel), Status: string(value.Status),
		AllowedActions: append([]string(nil), value.AllowedActions...), ResultKind: optionalHostedString(value.ResultKind), FailureCode: optionalHostedString(value.FailureCode),
		CreatedAt: value.CreatedAt, ExpiresAt: value.ExpiresAt, OpenedAt: value.OpenedAt, CompletedAt: value.CompletedAt,
	}
}

func mapHostedCompletion(value hostedinteraction.Completion) hostedhttp.Completion {
	return hostedhttp.Completion{InteractionID: value.Interaction.InteractionID, Status: string(value.Interaction.Status), ReturnURL: value.ReturnURL, ExpiresAt: value.GrantExpiresAt}
}

func mapHostedIssuedSession(value hostedinteraction.IssuedUserSession) *hostedhttp.IssuedUserSession {
	user := hostedhttp.UserSummary{
		UserID: value.User.UserID, AccountStatus: value.User.AccountStatus, ProductID: value.User.ProductID, TenantID: value.User.TenantID,
		AccessVersion: value.User.AccessVersion, ProductAccessStatus: value.User.ProductAccessStatus, TenantAccessStatus: value.User.TenantAccessStatus,
	}
	if value.User.DisplayName != "" {
		displayName := value.User.DisplayName
		user.DisplayName = &displayName
	}
	return &hostedhttp.IssuedUserSession{AccessToken: value.AccessToken, RefreshToken: value.RefreshToken, AccessExpiresAt: value.AccessExpiresAt, RefreshExpiresAt: value.RefreshExpiresAt, User: user}
}

func optionalHostedString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func mapHostedCoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, hostedinteraction.ErrInvalidArgument):
		return hostedhttp.ErrInvalidInteraction
	case errors.Is(err, hostedinteraction.ErrInteractionExpired):
		return hostedhttp.ErrInteractionExpired
	case errors.Is(err, hostedinteraction.ErrInvalidReturnTarget):
		return hostedhttp.ErrInvalidReturnTarget
	case errors.Is(err, hostedinteraction.ErrStateMismatch):
		return hostedhttp.ErrStateMismatch
	case errors.Is(err, hostedinteraction.ErrPKCERequired):
		return hostedhttp.ErrPKCERequired
	case errors.Is(err, hostedinteraction.ErrInvalidGrant):
		return hostedhttp.ErrInvalidGrant
	case errors.Is(err, hostedinteraction.ErrAuthenticationNeeded):
		return hostedhttp.ErrAuthenticationRequired
	case errors.Is(err, hostedinteraction.ErrChannelNotSupported):
		return hostedhttp.ErrChannelNotSupported
	case errors.Is(err, hostedinteraction.ErrSessionRevoked):
		return hostedhttp.ErrSessionRevoked
	case errors.Is(err, hostedinteraction.ErrCSRF):
		return hostedhttp.ErrCSRFFailed
	case errors.Is(err, hostedinteraction.ErrIdempotencyConflict):
		return hostedhttp.ErrConflict
	case errors.Is(err, hostedinteraction.ErrLeaseLost), errors.Is(err, hostedinteraction.ErrTemporarilyUnavailable):
		return hostedhttp.ErrTemporarilyUnavailable
	default:
		return fmt.Errorf("hosted interaction dependency: %w", err)
	}
}

var (
	_ hostedinteraction.ReturnTargetPort      = hostedReturnTargetAdapter{}
	_ hostedinteraction.HostedIdentityPort    = hostedIdentityAdapter{}
	_ hostedinteraction.SessionValidationPort = hostedIdentityAdapter{}
	_ hostedhttp.Authenticator                = hostedHTTPAuthenticator{}
	_ hostedhttp.Service                      = hostedHTTPServiceAdapter{}
)
