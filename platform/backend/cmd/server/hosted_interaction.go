package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/entitlement"
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

func newHostedInteractionRuntime(cfg config.HostedInteraction, pool *pgxpool.Pool, products *product.Service, applications *productapplication.Service, users *identity.EndUserService, entitlements *entitlement.Service, clientHasher securevalue.Hasher, registrationVerification ...*identity.RegistrationVerificationService) (hostedInteractionRuntime, error) {
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
	var verification *identity.RegistrationVerificationService
	if len(registrationVerification) > 0 {
		verification = registrationVerification[0]
	}
	identityAdapter := hostedIdentityAdapter{users: users, verification: verification}
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
	if err := service.ConfigureSelfService(identityAdapter, hostedPresentationAdapter{products: products}); err != nil {
		return hostedInteractionRuntime{}, err
	}
	service.ConfigureCapabilityPort(hostedCapabilityAdapter{identity: identityAdapter, products: products})
	service.ConfigureEntitlementProjection(hostedEntitlementAdapter{service: entitlements})
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

type hostedIdentityAdapter struct {
	users        *identity.EndUserService
	verification *identity.RegistrationVerificationService
}

type hostedPresentationAdapter struct{ products *product.Service }
type hostedEntitlementAdapter struct{ service *entitlement.Service }
type hostedCapabilityAdapter struct {
	identity hostedIdentityAdapter
	products *product.Service
}

func (a hostedPresentationAdapter) ResolveHostedPresentation(ctx context.Context, scope hostedinteraction.Scope) (hostedinteraction.HostedPresentation, error) {
	value, err := a.products.GetProduct(ctx, scope.ProductID)
	if err != nil {
		return hostedinteraction.HostedPresentation{}, err
	}
	return hostedinteraction.HostedPresentation{ProductName: value.Name}, nil
}

func (a hostedIdentityAdapter) Capabilities(_ context.Context, _ hostedinteraction.Scope) hostedinteraction.HostedCapabilities {
	c := a.users.HostedSelfServiceCapabilities()
	return hostedinteraction.HostedCapabilities{Password: c.PasswordEnabled, Registration: c.RegistrationEnabled && a.verification != nil, Recovery: c.RecoveryEnabled, Profile: true, Sessions: true, AccountCompletion: true}
}

func (a hostedCapabilityAdapter) Capabilities(ctx context.Context, scope hostedinteraction.Scope) hostedinteraction.HostedCapabilities {
	capabilities := a.identity.Capabilities(ctx, scope)
	capabilities.Entitlement = packageEnabled(ctx, a.products, scope.ProductID, "package.entitlement")
	return capabilities
}

func (a hostedEntitlementAdapter) CurrentEntitlementSummary(ctx context.Context, scope hostedinteraction.Scope, actor hostedinteraction.Actor) (*hostedinteraction.HostedEntitlementSummary, error) {
	if a.service == nil || scope.TenantID == nil || actor.Kind != "user" || strings.TrimSpace(actor.UserID) == "" {
		return nil, hostedinteraction.ErrCapabilityUnavailable
	}
	value, err := a.service.GetCurrentEntitlements(ctx, entitlement.ProductContext{ProductID: scope.ProductID}, entitlement.TenantContext{ProductID: scope.ProductID, TenantID: *scope.TenantID}, entitlement.UserContext{UserID: actor.UserID})
	if err != nil {
		if errors.Is(err, entitlement.ErrInvalidArgument) || errors.Is(err, entitlement.ErrScopeMismatch) {
			return nil, hostedinteraction.ErrCapabilityUnavailable
		}
		return nil, err
	}
	return &hostedinteraction.HostedEntitlementSummary{Revision: value.Revision, PlanCode: value.PlanCode, Features: value.EffectiveFeatures, ValidUntil: value.ValidUntil, OfflineGraceUntil: value.OfflineGraceUntil, UpdatedAt: value.UpdatedAt}, nil
}

func packageEnabled(ctx context.Context, products *product.Service, productID, packageID string) bool {
	enabled, err := productPackageEnabled(ctx, products, productID, packageID)
	return err == nil && enabled
}

func productPackageEnabled(ctx context.Context, products *product.Service, productID, packageID string) (bool, error) {
	if products == nil {
		return false, nil
	}
	set, err := products.CurrentCapabilitySet(ctx, productID)
	if err != nil {
		return false, err
	}
	for _, item := range set.Items {
		if item.SourcePackageID == packageID && item.Enabled {
			return true, nil
		}
	}
	return false, nil
}
func (a hostedIdentityAdapter) StartRegistrationVerification(ctx context.Context, scope hostedinteraction.Scope, identifier, key, traceID string) (string, error) {
	if a.verification == nil {
		return "", hostedinteraction.ErrTemporarilyUnavailable
	}
	v, e := a.verification.Start(ctx, identity.StartRegistrationVerificationCommand{Scope: identityScope(scope), Identifier: identifier, IdempotencyKey: key, TraceID: traceID})
	return v, mapHostedIdentitySelfServiceError(e)
}
func (a hostedIdentityAdapter) RegisterHosted(ctx context.Context, scope hostedinteraction.Scope, identifier, credential, continuation, proof, displayName, key, traceID string) (hostedinteraction.HostedAuthProof, error) {
	v, err := a.users.RegisterHosted(ctx, identity.EndUserRegisterCommand{Scope: identityScope(scope), Identifier: identifier, Credential: credential, VerificationContinuationID: continuation, VerificationProof: proof, DisplayName: displayName, IdempotencyKey: key, TraceID: traceID})
	if err != nil {
		return hostedinteraction.HostedAuthProof{}, mapHostedIdentitySelfServiceError(err)
	}
	return hostedinteraction.HostedAuthProof{ProofID: v.ProofID, AuthTime: v.AuthTime, ExpiresAt: v.ExpiresAt}, nil
}
func (a hostedIdentityAdapter) StartRecovery(ctx context.Context, scope hostedinteraction.Scope, identifier, key, traceID string) (string, error) {
	v, e := a.users.StartRecovery(ctx, identity.StartEndUserRecoveryCommand{Scope: identityScope(scope), Identifier: identifier, IdempotencyKey: key, TraceID: traceID})
	return v, mapHostedIdentitySelfServiceError(e)
}
func (a hostedIdentityAdapter) CompleteRecovery(ctx context.Context, scope hostedinteraction.Scope, continuation, proof, credential, key, traceID string) error {
	return mapHostedIdentitySelfServiceError(a.users.CompleteRecovery(ctx, identity.CompleteEndUserRecoveryCommand{Scope: identityScope(scope), Continuation: continuation, Proof: proof, NewCredential: credential, IdempotencyKey: key, TraceID: traceID}))
}
func hostedExpectation(scope hostedinteraction.Scope, actor hostedinteraction.Actor) identity.HostedSessionExpectation {
	return identity.HostedSessionExpectation{Scope: identityScope(scope), UserID: actor.UserID, SessionID: actor.UserSessionID}
}
func mapHostedProfile(v identity.EndUserProfile) hostedinteraction.HostedUserProfile {
	d := v.DisplayName
	return hostedinteraction.HostedUserProfile{UserID: v.UserID, Version: v.Version, DisplayName: &d, AvatarURL: v.AvatarRef, Locale: v.Locale, Timezone: v.Timezone}
}
func (a hostedIdentityAdapter) GetProfile(ctx context.Context, scope hostedinteraction.Scope, actor hostedinteraction.Actor) (hostedinteraction.HostedUserProfile, error) {
	v, e := a.users.GetHostedProfile(ctx, hostedExpectation(scope, actor))
	return mapHostedProfile(v), mapHostedIdentitySelfServiceError(e)
}
func (a hostedIdentityAdapter) PatchProfile(ctx context.Context, scope hostedinteraction.Scope, actor hostedinteraction.Actor, p hostedinteraction.HostedProfilePatch, version int64, key, traceID string) (hostedinteraction.HostedUserProfile, error) {
	v, e := a.users.PatchHostedProfile(ctx, hostedExpectation(scope, actor), identity.EndUserProfilePatch{DisplayName: identity.EndUserProfilePatchValue{Set: p.DisplayName.Set, Value: p.DisplayName.Value}, AvatarRef: identity.EndUserProfilePatchValue{Set: p.AvatarURL.Set, Value: p.AvatarURL.Value}, Locale: identity.EndUserProfilePatchValue{Set: p.Locale.Set, Value: p.Locale.Value}, Timezone: identity.EndUserProfilePatchValue{Set: p.Timezone.Set, Value: p.Timezone.Value}}, version, key, traceID)
	if errors.Is(e, identity.ErrEndUserVersionConflict) {
		return hostedinteraction.HostedUserProfile{}, hostedinteraction.ErrVersionConflict
	}
	return mapHostedProfile(v), mapHostedIdentitySelfServiceError(e)
}
func (a hostedIdentityAdapter) ListSessions(ctx context.Context, scope hostedinteraction.Scope, actor hostedinteraction.Actor) ([]hostedinteraction.HostedSessionSummary, error) {
	v, e := a.users.ListHostedSessions(ctx, hostedExpectation(scope, actor))
	if e != nil {
		return nil, mapHostedIdentitySelfServiceError(e)
	}
	out := make([]hostedinteraction.HostedSessionSummary, 0, len(v))
	for _, x := range v {
		out = append(out, hostedinteraction.HostedSessionSummary{SessionID: x.SessionID, Current: x.Current, CreatedAt: x.CreatedAt, LastSeenAt: x.LastSeenAt, ExpiresAt: x.ExpiresAt})
	}
	return out, nil
}
func (a hostedIdentityAdapter) ListExternalIdentities(ctx context.Context, scope hostedinteraction.Scope, actor hostedinteraction.Actor) ([]hostedinteraction.HostedExternalIdentity, error) {
	v, e := a.users.ListHostedExternalIdentities(ctx, hostedExpectation(scope, actor))
	if e != nil {
		return nil, mapHostedIdentitySelfServiceError(e)
	}
	out := make([]hostedinteraction.HostedExternalIdentity, 0, len(v))
	for _, x := range v {
		masked := x.SubjectMasked
		out = append(out, hostedinteraction.HostedExternalIdentity{ExternalIdentityID: x.ExternalIdentityID, Provider: x.Provider, MaskedSubject: &masked, Status: x.Status, LinkedAt: x.LinkedAt})
	}
	return out, nil
}
func (a hostedIdentityAdapter) ChangePassword(ctx context.Context, scope hostedinteraction.Scope, actor hostedinteraction.Actor, current, next string, revoke bool, key, traceID string) error {
	return mapHostedIdentitySelfServiceError(a.users.ChangeHostedPassword(ctx, hostedExpectation(scope, actor), current, next, revoke, key, traceID))
}
func (a hostedIdentityAdapter) RevokeSession(ctx context.Context, scope hostedinteraction.Scope, actor hostedinteraction.Actor, target, key, traceID string) error {
	return mapHostedIdentitySelfServiceError(a.users.RevokeHostedSession(ctx, hostedExpectation(scope, actor), target, key, traceID))
}

func mapHostedIdentitySelfServiceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, identity.ErrEndUserVersionConflict):
		return hostedinteraction.ErrIdempotencyConflict
	case errors.Is(err, identity.ErrEndUserInvalidCredentials), errors.Is(err, identity.ErrEndUserReauthenticationRequired), errors.Is(err, identity.ErrEndUserRateLimited), errors.Is(err, identity.ErrRegistrationVerificationInvalid), errors.Is(err, identity.ErrRegistrationVerificationExpired), errors.Is(err, identity.ErrRegistrationVerificationReplayed):
		return hostedinteraction.ErrAuthenticationNeeded
	case errors.Is(err, identity.ErrEndUserScopeMismatch), errors.Is(err, identity.ErrEndUserSessionExpired), errors.Is(err, identity.ErrEndUserSessionRevoked), errors.Is(err, identity.ErrEndUserAccountDisabled), errors.Is(err, identity.ErrNotFound):
		return hostedinteraction.ErrSessionRevoked
	case errors.Is(err, identity.ErrEndUserProviderUnavailable), errors.Is(err, identity.ErrHostedAuthUnavailable):
		return hostedinteraction.ErrTemporarilyUnavailable
	default:
		return err
	}
}

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
	case errors.Is(err, hostedinteraction.ErrInteractionTerminal):
		return hostedhttp.ErrInteractionTerminal
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

func mapHTTPInteraction(v hostedinteraction.Projection) hostedhttp.Interaction {
	return mapHostedProjection(v)
}
func mapHTTPPresentation(v hostedinteraction.HostedPresentation) hostedhttp.HostedPresentation {
	return hostedhttp.HostedPresentation{ProductName: v.ProductName, ThemeVariant: v.ThemeVariant}
}
func mapHTTPProfile(v hostedinteraction.HostedUserProfile) hostedhttp.UserProfile {
	return hostedhttp.UserProfile{UserID: v.UserID, Version: v.Version, DisplayName: v.DisplayName, AvatarURL: v.AvatarURL, Locale: v.Locale, Timezone: v.Timezone}
}
func (a hostedHTTPServiceAdapter) AuthBootstrap(ctx context.Context, p hostedhttp.HostedPrincipal, id string) (hostedhttp.HostedAuthBootstrap, error) {
	v, e := a.service.AuthBootstrap(ctx, id, p.BrowserToken)
	if e != nil {
		return hostedhttp.HostedAuthBootstrap{}, mapHostedCoreError(e)
	}
	providers := make([]hostedhttp.HostedExternalProvider, 0, len(v.ExternalProviders))
	for _, x := range v.ExternalProviders {
		providers = append(providers, hostedhttp.HostedExternalProvider{Provider: x.Provider, Mode: x.Mode, DisplayName: x.DisplayName})
	}
	return hostedhttp.HostedAuthBootstrap{Interaction: mapHTTPInteraction(v.Interaction), Presentation: mapHTTPPresentation(v.Presentation), Flow: hostedhttp.HostedAuthFlow{Kind: v.Flow.Kind, IdentifierHint: v.Flow.IdentifierHint}, PasswordEnabled: v.PasswordEnabled, RegistrationEnabled: v.RegistrationEnabled, RecoveryEnabled: v.RecoveryEnabled, ExternalProviders: providers}, nil
}
func (a hostedHTTPServiceAdapter) StartRegistrationVerification(ctx context.Context, p hostedhttp.HostedPrincipal, id string, c hostedhttp.VerificationStartCommand) (hostedhttp.HostedAuthFlow, error) {
	v, e := a.service.StartRegistrationVerification(ctx, id, p.BrowserToken, c.Identifier, c.IdempotencyKey, c.RequestID)
	return hostedhttp.HostedAuthFlow{Kind: v.Kind, IdentifierHint: v.IdentifierHint}, mapHostedCoreError(e)
}
func (a hostedHTTPServiceAdapter) RegisterHosted(ctx context.Context, p hostedhttp.HostedPrincipal, id string, c hostedhttp.RegisterCommand) (hostedhttp.Completion, error) {
	v, e := a.service.RegisterHosted(ctx, hostedinteraction.RegisterHostedCommand{InteractionID: id, BrowserToken: p.BrowserToken, Credential: c.Credential, Proof: c.Proof, DisplayName: c.DisplayName, IdempotencyKey: c.IdempotencyKey, TraceID: c.RequestID})
	if e != nil {
		return hostedhttp.Completion{}, mapHostedCoreError(e)
	}
	return mapHostedCompletion(v), nil
}
func (a hostedHTTPServiceAdapter) StartRecovery(ctx context.Context, p hostedhttp.HostedPrincipal, id string, c hostedhttp.RecoveryStartCommand) (hostedhttp.HostedAuthFlow, error) {
	v, e := a.service.StartRecovery(ctx, id, p.BrowserToken, c.Identifier, c.IdempotencyKey, c.RequestID)
	return hostedhttp.HostedAuthFlow{Kind: v.Kind, IdentifierHint: v.IdentifierHint}, mapHostedCoreError(e)
}
func (a hostedHTTPServiceAdapter) CompleteRecovery(ctx context.Context, p hostedhttp.HostedPrincipal, id string, c hostedhttp.RecoveryCompleteCommand) error {
	return mapHostedCoreError(a.service.CompleteRecovery(ctx, id, p.BrowserToken, c.Proof, c.NewCredential, c.IdempotencyKey, c.RequestID))
}
func (a hostedHTTPServiceAdapter) ResetAuthFlow(ctx context.Context, p hostedhttp.HostedPrincipal, id, key string) error {
	return mapHostedCoreError(a.service.ResetAuthFlow(ctx, id, p.BrowserToken, key))
}
func (a hostedHTTPServiceAdapter) AccountBootstrap(ctx context.Context, p hostedhttp.HostedPrincipal, id string) (hostedhttp.HostedAccountBootstrap, error) {
	v, e := a.service.AccountBootstrap(ctx, id, p.BrowserToken)
	if e != nil {
		return hostedhttp.HostedAccountBootstrap{}, mapHostedCoreError(e)
	}
	sessions := make([]hostedhttp.UserSessionSummary, 0, len(v.Sessions))
	for _, x := range v.Sessions {
		sessions = append(sessions, hostedhttp.UserSessionSummary{SessionID: x.SessionID, Current: x.Current, DeviceLabel: x.DeviceLabel, CreatedAt: x.CreatedAt, LastSeenAt: x.LastSeenAt, ExpiresAt: x.ExpiresAt})
	}
	external := make([]hostedhttp.ExternalIdentity, 0, len(v.ExternalIdentities))
	for _, x := range v.ExternalIdentities {
		external = append(external, hostedhttp.ExternalIdentity{ExternalIdentityID: x.ExternalIdentityID, Provider: x.Provider, MaskedSubject: x.MaskedSubject, Status: x.Status, LinkedAt: x.LinkedAt, AuditID: x.AuditID})
	}
	return hostedhttp.HostedAccountBootstrap{Interaction: mapHTTPInteraction(v.Interaction), Presentation: mapHTTPPresentation(v.Presentation), Profile: mapHTTPProfile(v.Profile), Sessions: sessions, ExternalIdentities: external, AllowedActions: v.AllowedActions, EntitlementSummary: mapHTTPEntitlementSummary(v.EntitlementSummary)}, nil
}

func mapHTTPEntitlementSummary(v *hostedinteraction.HostedEntitlementSummary) *hostedhttp.EntitlementSummary {
	if v == nil {
		return nil
	}
	var plan *string
	if strings.TrimSpace(v.PlanCode) != "" {
		value := v.PlanCode
		plan = &value
	}
	return &hostedhttp.EntitlementSummary{Revision: v.Revision, PlanCode: plan, Features: v.Features, ValidUntil: v.ValidUntil, OfflineGraceUntil: v.OfflineGraceUntil, UpdatedAt: v.UpdatedAt}
}
func (a hostedHTTPServiceAdapter) PatchAccountProfile(ctx context.Context, p hostedhttp.HostedPrincipal, id string, c hostedhttp.ProfilePatchCommand) (hostedhttp.UserProfile, error) {
	v, e := a.service.PatchAccountProfile(ctx, id, p.BrowserToken, hostedinteraction.HostedProfilePatch{DisplayName: hostedinteraction.HostedProfilePatchValue{Set: c.DisplayName.Set, Value: c.DisplayName.Value}, AvatarURL: hostedinteraction.HostedProfilePatchValue{Set: c.AvatarURL.Set, Value: c.AvatarURL.Value}, Locale: hostedinteraction.HostedProfilePatchValue{Set: c.Locale.Set, Value: c.Locale.Value}, Timezone: hostedinteraction.HostedProfilePatchValue{Set: c.Timezone.Set, Value: c.Timezone.Value}}, c.ExpectedVersion, c.IdempotencyKey, c.RequestID)
	return mapHTTPProfile(v), mapHostedCoreError(e)
}
func (a hostedHTTPServiceAdapter) ChangeAccountPassword(ctx context.Context, p hostedhttp.HostedPrincipal, id string, c hostedhttp.PasswordChangeCommand) error {
	return mapHostedCoreError(a.service.ChangeAccountPassword(ctx, id, p.BrowserToken, c.CurrentCredential, c.NewCredential, c.RevokeOtherSessions, c.IdempotencyKey, c.RequestID))
}
func (a hostedHTTPServiceAdapter) RevokeAccountSession(ctx context.Context, p hostedhttp.HostedPrincipal, id, target, key, traceID string) error {
	return mapHostedCoreError(a.service.RevokeAccountSession(ctx, id, p.BrowserToken, target, key, traceID))
}

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
	result := hostedhttp.BrowserSession{Interaction: mapHostedProjection(projection), CSRFToken: session.CSRFToken, BrowserSessionExpiresAt: session.ExpiresAt, CookieToken: session.Token}
	if projection.Status == hostedinteraction.StatusCompleted {
		completion, e := a.service.RecoverBrowserCompletion(ctx, interactionID, session.Token)
		if e != nil {
			return hostedhttp.BrowserSession{}, mapHostedCoreError(e)
		}
		mapped := mapHostedCompletion(completion)
		result.Completion = &mapped
	}
	return result, nil
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

func (a hostedHTTPServiceAdapter) Cancel(ctx context.Context, principal hostedhttp.HostedPrincipal, interactionID, key, requestID string) (hostedhttp.Interaction, error) {
	value, err := a.service.Cancel(ctx, interactionID, principal.BrowserToken, key)
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
	case errors.Is(err, hostedinteraction.ErrInteractionTerminal):
		return hostedhttp.ErrInteractionTerminal
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
	case errors.Is(err, hostedinteraction.ErrVersionConflict):
		return hostedhttp.ErrVersionConflict
	case errors.Is(err, hostedinteraction.ErrCapabilityUnavailable):
		return hostedhttp.ErrCapabilityUnavailable
	case errors.Is(err, hostedinteraction.ErrLeaseLost), errors.Is(err, hostedinteraction.ErrTemporarilyUnavailable):
		return hostedhttp.ErrTemporarilyUnavailable
	default:
		return fmt.Errorf("hosted interaction dependency: %w", err)
	}
}

var (
	_ hostedinteraction.ReturnTargetPort       = hostedReturnTargetAdapter{}
	_ hostedinteraction.HostedIdentityPort     = hostedIdentityAdapter{}
	_ hostedinteraction.SessionValidationPort  = hostedIdentityAdapter{}
	_ hostedinteraction.HostedSelfServicePort  = hostedIdentityAdapter{}
	_ hostedinteraction.HostedPresentationPort = hostedPresentationAdapter{}
	_ hostedinteraction.HostedEntitlementPort  = hostedEntitlementAdapter{}
	_ hostedinteraction.HostedCapabilityPort   = hostedCapabilityAdapter{}
	_ hostedhttp.Authenticator                 = hostedHTTPAuthenticator{}
	_ hostedhttp.Service                       = hostedHTTPServiceAdapter{}
	_ hostedhttp.SelfService                   = hostedHTTPServiceAdapter{}
)
