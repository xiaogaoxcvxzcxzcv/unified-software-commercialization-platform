// Package g2a06acceptance prepares test-only Hosted browser acceptance interactions.
package g2a06acceptance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"platform.local/capability-platform/backend/internal/modules/hostedinteraction"
	hostedpostgres "platform.local/capability-platform/backend/internal/modules/hostedinteraction/postgres"
	"platform.local/capability-platform/backend/internal/modules/identity"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	"platform.local/capability-platform/backend/internal/modules/product"
	productpostgres "platform.local/capability-platform/backend/internal/modules/product/postgres"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	applicationpostgres "platform.local/capability-platform/backend/internal/modules/productapplication/postgres"
	"platform.local/capability-platform/backend/internal/modules/tenant"
	tenantpostgres "platform.local/capability-platform/backend/internal/modules/tenant/postgres"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	"platform.local/capability-platform/backend/internal/testsupport/g2a05acceptance"
	"strings"
	"time"
)

const (
	ReturnTargetCode = "g2a06.browser-acceptance"
	ReturnTargetURI  = "https://127.0.0.1:5174/login"
	HostedBaseURL    = "https://127.0.0.1:5175"
)

var errRejected = errors.New("g2a06 acceptance fixture rejected")

type Options struct {
	RepositoryRoot    string
	Password          []byte
	UserAuth          config.UserAuth
	HostedInteraction config.HostedInteraction
	AdminTokenPepper  string
	AcceptanceFixture bool
}
type Result struct {
	AuthInteractionID                                                string `json:"auth_interaction_id"`
	AuthURL                                                          string `json:"auth_url"`
	AccountInteractionID                                             string `json:"account_interaction_id"`
	AccountURL                                                       string `json:"account_url"`
	NegativeAuthInteractionID                                        string `json:"-"`
	ClientSessionID, ClientToken, CodeVerifier, NegativeCodeVerifier string `json:"-"`
	AccountClientSessionID, AccountClientToken                       string `json:"-"`
	AuthState, NegativeAuthState, AccountState                       string `json:"-"`
	ProductID, ApplicationID, TenantID, UserID, UserSessionID        string `json:"-"`
	AccountApplicationID, AccountUserSessionID                       string `json:"-"`
}
type CleanupCommand struct {
	AuthInteractionID, NegativeAuthInteractionID, AccountInteractionID         string
	ClientSessionID, ProductID, ApplicationID, TenantID, UserID, UserSessionID string
	AccountClientSessionID, AccountApplicationID, AccountUserSessionID         string
}

type InteractionStatuses struct {
	Auth, NegativeAuth, Account hostedinteraction.Status
}

func Prepare(ctx context.Context, pool *pgxpool.Pool, o Options) (Result, error) {
	if !o.AcceptanceFixture || validatePool(pool) != nil || len(o.Password) < 16 || len(o.AdminTokenPepper) < 32 || len(o.UserAuth.TokenPepper) < 32 || len(o.HostedInteraction.DigestKey) < 32 || len(o.HostedInteraction.StateKey) < 32 || o.HostedInteraction.StateKeyRef == "" {
		return Result{}, errRejected
	}
	seed, err := g2a05acceptance.Seed(ctx, pool, g2a05acceptance.Options{RepositoryRoot: o.RepositoryRoot, Password: o.Password, UserPepper: []byte(o.UserAuth.TokenPepper)})
	if err != nil {
		return Result{}, err
	}
	service, apps, users, err := runtime(pool, o)
	if err != nil {
		return Result{}, err
	}
	adminHasher, err := securevalue.NewHasher(o.AdminTokenPepper)
	if err != nil {
		return Result{}, err
	}
	proofVerifier := product.NewVersionedProofVerifier(adminHasher)
	productService := product.NewService(productpostgres.New(pool), nil, proofVerifier, securevalue.ID, func() (string, string, error) {
		token, issueErr := securevalue.Token("pcs_")
		if issueErr != nil {
			return "", "", issueErr
		}
		return token, "sha256:" + adminHasher.DigestHex("product-client-session:"+token), nil
	}, nil)
	clientSecret, err := randomOpaque(32)
	if err != nil {
		return Result{}, err
	}
	clientKey, err := randomOpaque(24)
	if err != nil {
		return Result{}, err
	}
	now := time.Now().UTC()
	client, err := productService.RegisterClient(ctx, product.RegisterClientCommand{ProductID: seed.ProductID, Environment: "test", ProofType: "hmac_sha256_v1", ProofDigest: proofVerifier.SharedSecretDigest(clientSecret), NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), ActorID: "acceptance.g2a06.fixture", IdempotencyKey: clientKey, TraceID: "trace.g2a06.client"})
	if err != nil {
		return Result{}, fmt.Errorf("register acceptance client: %w", err)
	}
	application, err := apps.GetApplication(ctx, productapplication.ProductContext{ProductID: seed.ProductID, Environment: productapplication.EnvironmentTest}, seed.ApplicationID)
	if err != nil {
		return Result{}, err
	}
	tenantContext, err := tenant.NewService(tenantpostgres.New(pool)).ResolveTenantContext(ctx, tenant.ResolveTenantContextCommand{ProductID: seed.ProductID, ApplicationID: seed.ApplicationID, Method: tenant.ResolutionOfficialChannel})
	if err != nil {
		return Result{}, err
	}
	tenantID := seed.TenantID
	login, err := users.Login(ctx, identity.EndUserLoginCommand{Scope: identity.EndUserSessionScope{ProductID: seed.ProductID, ApplicationID: seed.ApplicationID, TenantID: &tenantID, Environment: "test"}, Identifier: "g2a05-account-acceptance@example.test", Credential: string(o.Password), Source: "loopback", TraceID: "trace.g2a06.user-session"})
	if err != nil {
		return Result{}, fmt.Errorf("refresh acceptance user session: %w", err)
	}
	seed.UserID, seed.UserSessionID = login.Session.UserID, login.Session.SessionID
	nonce, err := randomOpaque(24)
	if err != nil {
		return Result{}, err
	}
	clientSession, err := productService.CreateClientSession(ctx, product.CreateClientSessionCommand{ClientID: client.Client.ClientID, CredentialID: client.Credential.CredentialID, Proof: product.ClientProof{Type: "hmac_sha256_v1", Value: clientSecret}, RequestNonce: nonce, ClientVersion: "g2a06-acceptance", Scope: product.ResolvedSessionScope{ProductID: seed.ProductID, Environment: "test", ApplicationID: seed.ApplicationID, TenantID: seed.TenantID, ApplicationContextVersion: application.ContextVersion, TenantContextVersion: tenantContext.ContextVersion}, TTL: time.Hour, TraceID: "trace.g2a06.client-session"})
	if err != nil {
		return Result{}, fmt.Errorf("create acceptance client session: %w", err)
	}
	desktopApplication, err := apps.CreateApplication(ctx, productapplication.CreateCommand{Product: productapplication.ProductContext{ProductID: seed.ProductID, Environment: productapplication.EnvironmentTest}, ApplicationCode: "g2a06-desktop", Name: "[ACCEPTANCE FIXTURE] G2A-06 Desktop", Platform: productapplication.PlatformWindows, DistributionChannel: "official", ReleaseTrack: productapplication.ReleaseTrackStable, Status: productapplication.StatusActive, ActorID: "acceptance.g2a06.fixture", TraceID: "trace.g2a06.desktop-application", IdempotencyKey: "g2a06-desktop-application-v1"})
	if err != nil {
		return Result{}, fmt.Errorf("create desktop acceptance application: %w", err)
	}
	desktopTenantContext, err := tenant.NewService(tenantpostgres.New(pool)).ResolveTenantContext(ctx, tenant.ResolveTenantContextCommand{ProductID: seed.ProductID, ApplicationID: desktopApplication.ApplicationID, Method: tenant.ResolutionOfficialChannel})
	if err != nil {
		return Result{}, fmt.Errorf("resolve desktop acceptance tenant: %w", err)
	}
	desktopLogin, err := users.Login(ctx, identity.EndUserLoginCommand{Scope: identity.EndUserSessionScope{ProductID: seed.ProductID, ApplicationID: desktopApplication.ApplicationID, TenantID: &tenantID, Environment: "test"}, Identifier: "g2a05-account-acceptance@example.test", Credential: string(o.Password), Source: "loopback", TraceID: "trace.g2a06.desktop-user-session"})
	if err != nil {
		return Result{}, fmt.Errorf("create desktop acceptance user session: %w", err)
	}
	desktopNonce, err := randomOpaque(24)
	if err != nil {
		return Result{}, err
	}
	desktopClientSession, err := productService.CreateClientSession(ctx, product.CreateClientSessionCommand{ClientID: client.Client.ClientID, CredentialID: client.Credential.CredentialID, Proof: product.ClientProof{Type: "hmac_sha256_v1", Value: clientSecret}, RequestNonce: desktopNonce, ClientVersion: "g2a06-acceptance-desktop", Scope: product.ResolvedSessionScope{ProductID: seed.ProductID, Environment: "test", ApplicationID: desktopApplication.ApplicationID, TenantID: seed.TenantID, ApplicationContextVersion: desktopApplication.ContextVersion, TenantContextVersion: desktopTenantContext.ContextVersion}, TTL: time.Hour, TraceID: "trace.g2a06.desktop-client-session"})
	if err != nil {
		return Result{}, fmt.Errorf("create desktop acceptance client session: %w", err)
	}
	scope := hostedinteraction.Scope{ProductID: seed.ProductID, ApplicationID: seed.ApplicationID, TenantID: &seed.TenantID, Environment: "test", Channel: hostedinteraction.ChannelWeb}
	_, err = apps.ReplaceRedirects(ctx, productapplication.ReplaceRedirectsCommand{Product: productapplication.ProductContext{ProductID: seed.ProductID, Environment: productapplication.EnvironmentTest}, ApplicationID: seed.ApplicationID, Policy: productapplication.RedirectPolicy{WebRedirectURIs: []string{ReturnTargetURI}, AllowedOrigins: []string{"https://127.0.0.1:5174"}, AuthReturnTargets: []productapplication.AuthReturnTarget{{Code: ReturnTargetCode, URI: ReturnTargetURI}}}, ActorID: "acceptance.g2a06.fixture", TraceID: "trace.g2a06.redirect"})
	if err != nil {
		return Result{}, err
	}
	_, err = apps.ReplaceRedirects(ctx, productapplication.ReplaceRedirectsCommand{Product: productapplication.ProductContext{ProductID: seed.ProductID, Environment: productapplication.EnvironmentTest}, ApplicationID: desktopApplication.ApplicationID, Policy: productapplication.RedirectPolicy{WebRedirectURIs: []string{ReturnTargetURI}, AllowedOrigins: []string{"https://127.0.0.1:5174"}, AuthReturnTargets: []productapplication.AuthReturnTarget{{Code: ReturnTargetCode, URI: ReturnTargetURI}}}, ActorID: "acceptance.g2a06.fixture", TraceID: "trace.g2a06.desktop-redirect"})
	if err != nil {
		return Result{}, err
	}
	state, err := randomOpaque(32)
	if err != nil {
		return Result{}, err
	}
	nonce, err = randomOpaque(32)
	if err != nil {
		return Result{}, err
	}
	verifier, err := randomOpaque(32)
	if err != nil {
		return Result{}, err
	}
	challengeRaw := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeRaw[:])
	authKey, err := randomOpaque(24)
	if err != nil {
		return Result{}, err
	}
	auth, err := service.Create(ctx, hostedinteraction.CreateCommand{Scope: scope, Actor: hostedinteraction.Actor{Kind: "client", ClientSessionID: clientSession.SessionID}, Route: hostedinteraction.RouteAuth, ReturnTargetCode: ReturnTargetCode, State: state, Nonce: nonce, CodeChallenge: challenge, CodeChallengeMethod: "S256", IdempotencyKey: authKey, TraceID: "trace.g2a06.auth"})
	if err != nil {
		return Result{}, fmt.Errorf("create acceptance auth interaction: %w", err)
	}
	negativeVerifier, err := randomOpaque(32)
	if err != nil {
		return Result{}, err
	}
	negativeState, err := randomOpaque(32)
	if err != nil {
		return Result{}, err
	}
	negativeNonce, err := randomOpaque(32)
	if err != nil {
		return Result{}, err
	}
	negativeKey, err := randomOpaque(24)
	if err != nil {
		return Result{}, err
	}
	negativeChallengeRaw := sha256.Sum256([]byte(negativeVerifier))
	negative, err := service.Create(ctx, hostedinteraction.CreateCommand{Scope: scope, Actor: hostedinteraction.Actor{Kind: "client", ClientSessionID: clientSession.SessionID}, Route: hostedinteraction.RouteAuth, ReturnTargetCode: ReturnTargetCode, State: negativeState, Nonce: negativeNonce, CodeChallenge: base64.RawURLEncoding.EncodeToString(negativeChallengeRaw[:]), CodeChallengeMethod: "S256", IdempotencyKey: negativeKey, TraceID: "trace.g2a06.auth-negative"})
	if err != nil {
		return Result{}, fmt.Errorf("create hidden acceptance auth interaction: %w", err)
	}
	accountState, err := randomOpaque(32)
	if err != nil {
		return Result{}, fmt.Errorf("create acceptance account interaction: %w", err)
	}
	accountKey, err := randomOpaque(24)
	if err != nil {
		return Result{}, err
	}
	accountScope := hostedinteraction.Scope{ProductID: seed.ProductID, ApplicationID: desktopApplication.ApplicationID, TenantID: &seed.TenantID, Environment: "test", Channel: hostedinteraction.ChannelDesktop}
	account, err := service.Create(ctx, hostedinteraction.CreateCommand{Scope: accountScope, Actor: hostedinteraction.Actor{Kind: "user", UserID: desktopLogin.Session.UserID, UserSessionID: desktopLogin.Session.SessionID}, Route: hostedinteraction.RouteAccount, ReturnTargetCode: ReturnTargetCode, State: accountState, IdempotencyKey: accountKey, TraceID: "trace.g2a06.account"})
	if err != nil {
		return Result{}, err
	}
	return Result{AuthInteractionID: auth.InteractionID, AuthURL: auth.InteractionURL, AccountInteractionID: account.InteractionID, AccountURL: account.InteractionURL, NegativeAuthInteractionID: negative.InteractionID, ClientSessionID: clientSession.SessionID, ClientToken: clientSession.Token, AccountClientSessionID: desktopClientSession.SessionID, AccountClientToken: desktopClientSession.Token, CodeVerifier: verifier, NegativeCodeVerifier: negativeVerifier, AuthState: state, NegativeAuthState: negativeState, AccountState: accountState, ProductID: seed.ProductID, ApplicationID: seed.ApplicationID, AccountApplicationID: desktopApplication.ApplicationID, TenantID: seed.TenantID, UserID: desktopLogin.Session.UserID, UserSessionID: seed.UserSessionID, AccountUserSessionID: desktopLogin.Session.SessionID}, nil
}

func Cleanup(ctx context.Context, pool *pgxpool.Pool, o Options, c CleanupCommand) error {
	if !o.AcceptanceFixture || validatePool(pool) != nil {
		return errRejected
	}
	service, _, _, err := runtime(pool, o)
	if err != nil {
		return err
	}
	repository := hostedpostgres.New(pool)
	items, err := ownedInteractions(ctx, repository, c)
	if err != nil {
		return err
	}
	for _, item := range items {
		id, value := item.id, item.value
		if cleanupTerminal(value.Status) {
			continue
		}
		browser, _, openErr := service.OpenBrowserSession(ctx, id)
		if openErr != nil {
			if errors.Is(openErr, hostedinteraction.ErrInteractionExpired) {
				continue
			}
			return openErr
		}
		if _, cancelErr := service.Cancel(ctx, id, browser.Token, "g2a06-cleanup-idempotency-"+id); cancelErr != nil {
			latest, getErr := repository.Get(ctx, id)
			if getErr == nil && cleanupTerminal(latest.Status) {
				continue
			}
			return cancelErr
		}
	}
	return nil
}

func Statuses(ctx context.Context, pool *pgxpool.Pool, o Options, c CleanupCommand) (InteractionStatuses, error) {
	if !o.AcceptanceFixture || validatePool(pool) != nil {
		return InteractionStatuses{}, errRejected
	}
	items, err := ownedInteractions(ctx, hostedpostgres.New(pool), c)
	if err != nil {
		return InteractionStatuses{}, err
	}
	return InteractionStatuses{Auth: items[0].value.Status, NegativeAuth: items[1].value.Status, Account: items[2].value.Status}, nil
}

type ownedInteraction struct {
	id    string
	value hostedinteraction.Interaction
}

func ownedInteractions(ctx context.Context, repository *hostedpostgres.Repository, c CleanupCommand) ([]ownedInteraction, error) {
	items := []struct{ id, route, channel, kind, trace, applicationID, clientSessionID, userSessionID string }{{c.AuthInteractionID, string(hostedinteraction.RouteAuth), string(hostedinteraction.ChannelWeb), "client", "trace.g2a06.auth", c.ApplicationID, c.ClientSessionID, ""}, {c.NegativeAuthInteractionID, string(hostedinteraction.RouteAuth), string(hostedinteraction.ChannelWeb), "client", "trace.g2a06.auth-negative", c.ApplicationID, c.ClientSessionID, ""}, {c.AccountInteractionID, string(hostedinteraction.RouteAccount), string(hostedinteraction.ChannelDesktop), "user", "trace.g2a06.account", c.AccountApplicationID, "", c.AccountUserSessionID}}
	result := make([]ownedInteraction, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.id) == "" {
			return nil, errRejected
		}
		value, err := repository.Get(ctx, item.id)
		if err != nil || string(value.Route) != item.route || string(value.Scope.Channel) != item.channel || value.Scope.ProductID != c.ProductID || value.Scope.ApplicationID != item.applicationID || value.Scope.TenantID == nil || *value.Scope.TenantID != c.TenantID || value.Scope.Environment != "test" || value.Actor.Kind != item.kind || value.TraceID != item.trace {
			return nil, errRejected
		}
		if item.kind == "client" && value.Actor.ClientSessionID != item.clientSessionID || item.kind == "user" && (value.Actor.UserID != c.UserID || value.Actor.UserSessionID != item.userSessionID) {
			return nil, errRejected
		}
		result = append(result, ownedInteraction{id: item.id, value: value})
	}
	return result, nil
}

func cleanupTerminal(status hostedinteraction.Status) bool {
	switch status {
	case hostedinteraction.StatusCompleted, hostedinteraction.StatusExchanged, hostedinteraction.StatusCancelled, hostedinteraction.StatusFailed, hostedinteraction.StatusExpired:
		return true
	default:
		return false
	}
}

func runtime(pool *pgxpool.Pool, o Options) (*hostedinteraction.Service, *productapplication.Service, *identity.EndUserService, error) {
	digest, err := securevalue.NewHasher(o.HostedInteraction.DigestKey)
	if err != nil {
		return nil, nil, nil, err
	}
	userHasher, err := securevalue.NewHasher(o.UserAuth.TokenPepper)
	if err != nil {
		return nil, nil, nil, err
	}
	users, err := identity.NewEndUserService(identitypostgres.New(pool), identity.StrictIdentifierNormalizer{}, identity.Bcrypt{Cost: o.UserAuth.BcryptCost}, userHasher, nil, nil, identity.EndUserPolicy{AccessTTL: o.UserAuth.AccessTTL, RefreshTTL: o.UserAuth.RefreshTTL, RefreshAbsoluteTTL: o.UserAuth.AbsoluteTTL, RefreshRecoveryWindow: o.UserAuth.RefreshRecoveryWindow, RecoveryTTL: o.UserAuth.RecoveryTTL, RecoveryMaxAttempts: o.UserAuth.RecoveryMaximumAttempts, LoginWindow: o.UserAuth.LoginWindow, LoginMaximumAttempts: o.UserAuth.LoginMaximumAttempts, LoginBlockDuration: o.UserAuth.LoginBlockDuration, RecentAuthTTL: o.UserAuth.RecentAuthTTL, HostedAuthProofTTL: o.HostedInteraction.AuthProofTTL}, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	apps := productapplication.NewService(applicationpostgres.New(pool), nil, nil)
	stateKey := sha256.Sum256([]byte("hosted-interaction.state-key.v1\x00" + o.HostedInteraction.StateKey))
	protector, err := hostedinteraction.NewAEADStateProtector(o.HostedInteraction.StateKeyRef, secretResolver{reference: o.HostedInteraction.StateKeyRef, key: stateKey})
	if err != nil {
		return nil, nil, nil, err
	}
	adapter := identityAdapter{users: users}
	service, err := hostedinteraction.NewServiceWithPolicy(hostedpostgres.New(pool), returnTargetAdapter{apps}, adapter, adapter, protector, digest, o.HostedInteraction.BaseURL, hostedinteraction.ServicePolicy{InteractionTTL: o.HostedInteraction.InteractionTTL, BrowserTTL: o.HostedInteraction.BrowserTTL, AuthLeaseTTL: o.HostedInteraction.AuthLeaseTTL, GrantTTL: o.HostedInteraction.GrantTTL, LeaseTTL: o.HostedInteraction.GrantLeaseTTL})
	return service, apps, users, err
}

type secretResolver struct {
	reference string
	key       [sha256.Size]byte
}

func (s secretResolver) ResolveSecret(_ context.Context, reference string) ([]byte, error) {
	if reference == "" || reference != s.reference {
		return nil, errors.New("unexpected acceptance state secret reference")
	}
	return append([]byte(nil), s.key[:]...), nil
}

type returnTargetAdapter struct{ apps *productapplication.Service }

func (a returnTargetAdapter) ResolveHostedReturnTarget(ctx context.Context, s hostedinteraction.Scope, code string) (hostedinteraction.ReturnTarget, error) {
	v, err := a.apps.ResolveAuthReturnTarget(ctx, productapplication.ProductContext{ProductID: s.ProductID, Environment: productapplication.Environment(s.Environment)}, s.ApplicationID, code)
	if err != nil {
		return hostedinteraction.ReturnTarget{}, err
	}
	return hostedinteraction.ReturnTarget{ProductID: v.ProductID, ApplicationID: v.ApplicationID, Code: v.Code, URI: v.URI, PolicyVersion: v.PolicyVersion, Kind: string(v.Kind)}, nil
}

type identityAdapter struct{ users *identity.EndUserService }

func (a identityAdapter) AuthenticateHosted(ctx context.Context, s hostedinteraction.Scope, i, c, src string, r map[string]any, t string) (hostedinteraction.HostedAuthProof, error) {
	v, e := a.users.AuthenticateHosted(ctx, identity.AuthenticateHostedCommand{Scope: identityScope(s), Identifier: i, Credential: c, Source: src, RiskSummary: r, TraceID: t})
	return hostedinteraction.HostedAuthProof{ProofID: v.ProofID, AuthTime: v.AuthTime, ExpiresAt: v.ExpiresAt}, e
}
func (a identityAdapter) RedeemHostedAuthGrant(ctx context.Context, g, p string, s hostedinteraction.Scope, t string) (hostedinteraction.IssuedUserSession, error) {
	v, e := a.users.RedeemHostedAuthGrant(ctx, identity.RedeemHostedAuthGrantCommand{GrantID: g, ProofID: p, Scope: identityScope(s), TraceID: t})
	return hostedinteraction.IssuedUserSession{SessionID: v.Session.SessionID, AccessToken: v.AccessToken, RefreshToken: v.RefreshToken, AccessExpiresAt: v.Session.AccessExpiresAt, RefreshExpiresAt: v.Session.RefreshExpiresAt}, e
}
func (a identityAdapter) ValidateHostedAccountSession(ctx context.Context, s hostedinteraction.Scope, actor hostedinteraction.Actor) error {
	return a.users.ValidateHostedSession(ctx, identity.HostedSessionExpectation{Scope: identityScope(s), UserID: actor.UserID, SessionID: actor.UserSessionID})
}
func (a identityAdapter) Capabilities(context.Context, hostedinteraction.Scope) hostedinteraction.HostedCapabilities {
	return hostedinteraction.HostedCapabilities{Password: true, Profile: true, Sessions: true, AccountCompletion: true}
}
func identityScope(s hostedinteraction.Scope) identity.EndUserSessionScope {
	return identity.EndUserSessionScope{ProductID: s.ProductID, ApplicationID: s.ApplicationID, TenantID: s.TenantID, Environment: s.Environment}
}
func randomOpaque(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	defer clear(b)
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func validatePool(pool *pgxpool.Pool) error {
	if pool == nil || pool.Config() == nil || pool.Config().ConnConfig == nil {
		return errRejected
	}
	c := pool.Config().ConnConfig
	host := strings.ToLower(c.Host)
	if host != "127.0.0.1" && host != "localhost" && host != "::1" || c.Database != "platform_test_control" {
		return errRejected
	}
	return nil
}
func ValidateDatabaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || strings.TrimPrefix(u.Path, "/") != "platform_test_control" {
		return errRejected
	}
	h := strings.ToLower(u.Hostname())
	if h != "127.0.0.1" && h != "localhost" && h != "::1" {
		return errRejected
	}
	return nil
}
