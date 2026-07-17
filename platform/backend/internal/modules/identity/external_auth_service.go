package identity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

var externalFailureCodePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)

type ExternalAuthPolicy struct {
	FlowTTL       time.Duration
	ProofTTL      time.Duration
	RecentAuthTTL time.Duration
}

type ExternalAuthService struct {
	repository ExternalAuthRepository
	users      EndUserRepository
	sessions   *EndUserService
	registry   ExternalProviderRegistry
	provider   ExternalIdentityProvider
	returns    AuthReturnTargetResolver
	hasher     securevalue.Hasher
	secrets    securevalue.Generator
	policy     ExternalAuthPolicy
	now        func() time.Time
}

func NewExternalAuthService(repository ExternalAuthRepository, users EndUserRepository, sessions *EndUserService, registry ExternalProviderRegistry, provider ExternalIdentityProvider, returns AuthReturnTargetResolver, hasher securevalue.Hasher, policy ExternalAuthPolicy, now func() time.Time) (*ExternalAuthService, error) {
	if repository == nil || users == nil || sessions == nil || registry == nil || provider == nil || returns == nil || !hasher.Configured() {
		return nil, errors.New("external auth repository, sessions, registry, provider, and return target resolver are required")
	}
	if policy.FlowTTL <= 0 || policy.ProofTTL <= 0 || policy.RecentAuthTTL <= 0 {
		return nil, errors.New("invalid external auth policy")
	}
	if now == nil {
		now = time.Now
	}
	return &ExternalAuthService{repository: repository, users: users, sessions: sessions, registry: registry, provider: provider, returns: returns, hasher: hasher, secrets: securevalue.DefaultGenerator(), policy: policy, now: now}, nil
}

func (s *ExternalAuthService) Start(ctx context.Context, command ExternalAuthStartCommand) (ExternalAuthStartResult, error) {
	if command.Scope.ProductID == "" || command.Scope.ApplicationID == "" || command.Provider == "" || command.Environment == "" || command.ReturnTargetCode == "" || command.BrowserSession == "" || (command.Mode != "redirect" && command.Mode != "qr" && command.Mode != "native") {
		return ExternalAuthStartResult{}, ErrExternalAuthFlowInvalid
	}
	providerApplication, err := s.resolveProvider(ctx, command.Scope, command.Environment, command.Provider)
	if err != nil {
		return ExternalAuthStartResult{}, err
	}
	target, err := s.returns.ResolveAuthReturnTarget(ctx, command.Scope, command.Environment, command.ReturnTargetCode)
	if err != nil || target.Code != command.ReturnTargetCode || strings.TrimSpace(target.URI) == "" || target.PolicyVersion < 1 {
		return ExternalAuthStartResult{}, ErrExternalAuthFlowInvalid
	}
	flowID, err := s.secrets.ID("eaf_")
	if err != nil {
		return ExternalAuthStartResult{}, err
	}
	state := s.externalSecret("state", flowID)
	nonce := s.externalSecret("nonce", flowID)
	verifier := s.externalSecret("pkce-verifier", flowID)
	challengeRaw := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeRaw[:])
	now := s.now().UTC()
	flow := ExternalAuthFlow{
		FlowID: flowID, Scope: command.Scope, Environment: command.Environment,
		Provider: command.Provider, ProviderApplicationRef: providerApplication.ProviderApplicationRef,
		Mode: command.Mode, ReturnTargetCode: target.Code, ReturnTargetURI: target.URI,
		ReturnTargetPolicyVersion: target.PolicyVersion,
		StateDigest:               s.hasher.Digest("external-state\x00" + state), NonceDigest: s.hasher.Digest("external-nonce\x00" + nonce),
		PKCEChallengeDigest: s.hasher.Digest("external-pkce-challenge\x00" + challenge), Status: "pending", CreatedAt: now, ExpiresAt: now.Add(s.policy.FlowTTL),
	}
	flow.BrowserSessionDigest = s.hasher.Digest("external-browser-session\x00" + command.BrowserSession)
	authorization, err := s.provider.StartAuthorization(ctx, providerApplication, ExternalAuthorizationRequest{FlowID: flowID, Mode: command.Mode, ReturnURI: target.URI, State: state, Nonce: nonce, PKCEMethod: "S256", PKCEChallenge: challenge})
	if err != nil {
		return ExternalAuthStartResult{}, err
	}
	if !validExternalAuthorization(command.Mode, authorization) {
		return ExternalAuthStartResult{}, ErrExternalAuthFlowInvalid
	}
	if err := s.repository.CreateExternalAuthFlow(ctx, flow); err != nil {
		return ExternalAuthStartResult{}, err
	}
	return ExternalAuthStartResult{FlowID: flowID, Mode: command.Mode, AuthorizationURL: authorization.AuthorizationURL, QRPayload: authorization.QRPayload, ExpiresAt: flow.ExpiresAt}, nil
}

func (s *ExternalAuthService) Complete(ctx context.Context, command ExternalAuthCallbackCommand) (ExternalAuthResult, error) {
	now := s.now().UTC()
	if command.FlowID == "" || command.Provider == "" || command.BrowserSession == "" || command.State == "" || (command.ProviderError != "" && !externalFailureCodePattern.MatchString(command.ProviderError)) {
		return ExternalAuthResult{}, ErrExternalAuthFlowInvalid
	}
	claimToken, err := s.secrets.Token("ec_")
	if err != nil {
		return ExternalAuthResult{}, err
	}
	stateDigest := s.hasher.Digest("external-state\x00" + command.State)
	claimDigest := s.hasher.Digest("external-processing-claim\x00" + claimToken)
	flow, err := s.repository.ClaimExternalAuthFlow(ctx, ExternalAuthFlowClaim{FlowID: command.FlowID, Provider: command.Provider, ExpectedScope: command.ExpectedScope, BrowserSessionDigest: s.hasher.Digest("external-browser-session\x00" + command.BrowserSession), StateDigest: stateDigest, ProcessingTokenDigest: claimDigest, ProcessingExpiresAt: now.Add(30 * time.Second), Now: now})
	if err != nil {
		return ExternalAuthResult{}, err
	}
	if command.ProviderError != "" {
		event, eventErr := s.sessions.event("identity.external_auth_failed.v1", "identity.external_auth_failed", "anonymous", command.TraceID, "failure", command.ProviderError, now)
		if eventErr != nil {
			return ExternalAuthResult{}, eventErr
		}
		if err := s.repository.ConsumeExternalAuthFlowFailure(ctx, flow.FlowID, claimDigest, command.ProviderError, now, event); err != nil {
			return ExternalAuthResult{}, err
		}
		return ExternalAuthResult{}, ErrExternalAuthFlowInvalid
	}
	if command.Code == "" {
		return ExternalAuthResult{}, ErrExternalAuthFlowInvalid
	}
	providerApplication, err := s.resolveProvider(ctx, flow.Scope, flow.Environment, flow.Provider)
	if err != nil || providerApplication.ProviderApplicationRef != flow.ProviderApplicationRef {
		return ExternalAuthResult{}, ErrExternalProviderDisabled
	}
	nonce := s.externalSecret("nonce", flow.FlowID)
	verifier := s.externalSecret("pkce-verifier", flow.FlowID)
	claims, err := s.provider.ExchangeAuthorizationCode(ctx, providerApplication, command.Code, nonce, verifier)
	if err != nil {
		if event, eventErr := s.sessions.event("identity.external_auth_failed.v1", "identity.external_auth_failed", "anonymous", command.TraceID, "failure", "EXTERNAL_PROVIDER_EXCHANGE_FAILED", now); eventErr == nil {
			_ = s.repository.ConsumeExternalAuthFlowFailure(context.WithoutCancel(ctx), flow.FlowID, claimDigest, "EXTERNAL_PROVIDER_EXCHANGE_FAILED", s.now().UTC(), event)
		}
		return ExternalAuthResult{}, err
	}
	completedAt := s.now().UTC()
	if claims.Provider != flow.Provider || claims.ProviderApplicationRef != flow.ProviderApplicationRef || claims.Subject == "" {
		return ExternalAuthResult{}, ErrExternalAuthFlowInvalid
	}
	subjectDigest := s.externalSubjectDigest(flow.Provider, flow.ProviderApplicationRef, claims.Subject)
	codeDigest := s.hasher.Digest("external-code\x00" + flow.Provider + "\x00" + flow.ProviderApplicationRef + "\x00" + command.Code)
	linked, findErr := s.users.FindExternalIdentity(ctx, flow.Provider, flow.ProviderApplicationRef, subjectDigest)
	if findErr == nil {
		if linked.AccountStatus != "active" {
			return ExternalAuthResult{}, ErrEndUserAccountDisabled
		}
		if s.sessions.admission != nil {
			if err := s.sessions.admission.AdmitEndUser(ctx, EndUserAdmissionRequest{Scope: flow.Scope, UserID: linked.UserID, At: completedAt}); err != nil {
				return ExternalAuthResult{}, err
			}
		}
		issued, stored, err := s.sessions.newSession(linked.UserID, flow.Scope, externalAuthenticationMethod(flow.Provider), nil, completedAt, command.TraceID)
		if err != nil {
			return ExternalAuthResult{}, err
		}
		externalIdentityID := linked.ExternalIdentityID
		issued.Session.ExternalIdentityID = &externalIdentityID
		stored.Session.ExternalIdentityID = &externalIdentityID
		if err := s.repository.ConsumeExternalAuthFlowWithSession(ctx, flow.FlowID, claimDigest, codeDigest, stored, completedAt); err != nil {
			return ExternalAuthResult{}, err
		}
		profile, err := s.users.GetEndUserProfile(ctx, linked.UserID)
		if err != nil {
			return ExternalAuthResult{}, err
		}
		issued.Profile = profile
		return ExternalAuthResult{Status: "authenticated", Session: &issued}, nil
	}
	if !errors.Is(findErr, ErrNotFound) {
		return ExternalAuthResult{}, findErr
	}
	proofID, err := s.secrets.ID("xpf_")
	if err != nil {
		return ExternalAuthResult{}, err
	}
	proofSecret, err := s.secrets.Token("xp_")
	if err != nil {
		return ExternalAuthResult{}, err
	}
	proof := ExternalIdentityProof{ProofID: proofID, FlowID: flow.FlowID, Scope: flow.Scope, Provider: flow.Provider, ProviderApplicationRef: flow.ProviderApplicationRef, SubjectDigest: subjectDigest, SubjectMasked: safeExternalSubjectMask(flow.Provider, claims.MaskedSubject), ProofDigest: s.hasher.Digest("external-proof\x00" + proofSecret), CreatedAt: completedAt, ExpiresAt: completedAt.Add(s.policy.ProofTTL)}
	if claims.UnionSubject != "" {
		proof.UnionSubjectDigest = s.externalSubjectDigest(flow.Provider, flow.ProviderApplicationRef, claims.UnionSubject)
	}
	event, err := s.sessions.event("identity.external_proof_issued.v1", "identity.external_proof_issued", "anonymous", command.TraceID, "success", "", completedAt)
	if err != nil {
		return ExternalAuthResult{}, err
	}
	if err := s.repository.ConsumeExternalAuthFlowWithProof(ctx, flow.FlowID, claimDigest, codeDigest, proof, completedAt, event); err != nil {
		return ExternalAuthResult{}, err
	}
	return ExternalAuthResult{Status: "link_required", ExternalProofID: proofSecret}, nil
}

func (s *ExternalAuthService) Link(ctx context.Context, command LinkExternalIdentityCommand) (ExternalIdentity, error) {
	now := s.now().UTC()
	if command.Session.AuthTime.After(now) || now.Sub(command.Session.AuthTime) > s.policy.RecentAuthTTL {
		return ExternalIdentity{}, ErrExternalRecentAuthRequired
	}
	if command.Provider == "" || command.ExternalProofID == "" || len(command.IdempotencyKey) < 16 {
		return ExternalIdentity{}, ErrExternalProofInvalid
	}
	externalID, err := s.secrets.ID("xid_")
	if err != nil {
		return ExternalIdentity{}, err
	}
	event, err := s.sessions.event("identity.external_identity_linked.v1", "identity.external_identity_linked", command.Session.UserID, command.TraceID, "success", "", now)
	if err != nil {
		return ExternalIdentity{}, err
	}
	value := ExternalIdentity{ExternalIdentityID: externalID, UserID: command.Session.UserID, Provider: command.Provider, Status: "active", Version: 1, LinkedAt: now, UpdatedAt: now, OutboxEvent: event}
	record := EndUserIdempotency{Operation: "external_identity_link", ScopeID: trustedScopeID(scopeFromSession(command.Session)), ActorDigest: s.hasher.Digest("user\x00" + command.Session.UserID), KeyDigest: s.hasher.Digest("idempotency-key\x00" + command.IdempotencyKey), RequestDigest: identityRequestDigest(s.hasher, "external-identity-link", command.Provider, command.ExternalProofID), ResourceID: externalID, Now: now}
	result, _, err := s.repository.ConsumeExternalIdentityProofAndLink(ctx, s.hasher.Digest("external-proof\x00"+command.ExternalProofID), scopeFromSession(command.Session), command.Provider, value, now, record)
	return result, err
}

func (s *ExternalAuthService) List(ctx context.Context, session EndUserSession) ([]ExternalIdentity, error) {
	return s.repository.ListExternalIdentities(ctx, session.UserID)
}

func (s *ExternalAuthService) Unlink(ctx context.Context, command UnlinkExternalIdentityCommand) error {
	now := s.now().UTC()
	if command.Session.AuthTime.After(now) || now.Sub(command.Session.AuthTime) > s.policy.RecentAuthTTL {
		return ErrExternalRecentAuthRequired
	}
	event, err := s.sessions.event("identity.external_identity_unlinked.v1", "identity.external_identity_unlinked", command.Session.UserID, command.TraceID, "success", "", now)
	if err != nil {
		return err
	}
	return s.repository.UnlinkExternalIdentity(ctx, command.Session.UserID, command.ExternalIdentityID, now, event)
}

func (s *ExternalAuthService) resolveProvider(ctx context.Context, scope EndUserSessionScope, environment, provider string) (ExternalProviderApplication, error) {
	resolved, err := s.registry.ResolveExternalProvider(ctx, ExternalProviderQuery{Scope: scope, Environment: environment, Provider: provider})
	if err != nil || !resolved.Enabled || resolved.Provider != provider || resolved.Environment != environment || !resolved.Scope.Matches(EndUserSession{ProductID: scope.ProductID, ApplicationID: scope.ApplicationID, TenantID: scope.TenantID}) || resolved.ProviderApplicationRef == "" {
		return ExternalProviderApplication{}, ErrExternalProviderDisabled
	}
	return resolved, nil
}

func (s *ExternalAuthService) externalSecret(kind, flowID string) string {
	return "ex_" + base64.RawURLEncoding.EncodeToString(s.hasher.Digest("external-auth\x00"+kind+"\x00"+flowID))
}

func (s *ExternalAuthService) externalSubjectDigest(provider, applicationRef, subject string) []byte {
	return s.hasher.Digest(fmt.Sprintf("external-subject\x00%d:%s\x00%d:%s\x00%s", len(provider), provider, len(applicationRef), applicationRef, subject))
}

func externalAuthenticationMethod(provider string) string {
	if provider == "wechat" {
		return "wechat"
	}
	return "oidc"
}

func validExternalAuthorization(mode string, authorization ExternalAuthorization) bool {
	if mode == "qr" {
		return strings.TrimSpace(authorization.QRPayload) != "" && authorization.AuthorizationURL == ""
	}
	if (mode != "redirect" && mode != "native") || strings.TrimSpace(authorization.AuthorizationURL) == "" || authorization.QRPayload != "" {
		return false
	}
	parsed, err := url.Parse(authorization.AuthorizationURL)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

func safeExternalSubjectMask(provider, value string) string {
	return provider + " identity"
}
