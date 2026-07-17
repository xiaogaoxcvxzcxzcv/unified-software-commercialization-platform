package hostedinteraction

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

var (
	stableCode        = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	pkceChallengeCode = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

type Service struct {
	repository     Repository
	returnTargets  ReturnTargetPort
	identity       HostedIdentityPort
	sessions       SessionValidationPort
	protector      StateProtector
	ids            IDGenerator
	digester       Digester
	baseURL        string
	interactionTTL time.Duration
	browserTTL     time.Duration
	grantTTL       time.Duration
	leaseTTL       time.Duration
}

type ServicePolicy struct {
	InteractionTTL time.Duration
	BrowserTTL     time.Duration
	GrantTTL       time.Duration
	LeaseTTL       time.Duration
}

func DefaultServicePolicy() ServicePolicy {
	return ServicePolicy{InteractionTTL: 10 * time.Minute, BrowserTTL: 10 * time.Minute, GrantTTL: 2 * time.Minute, LeaseTTL: 30 * time.Second}
}

func NewService(repository Repository, returnTargets ReturnTargetPort, identity HostedIdentityPort, sessions SessionValidationPort, protector StateProtector, digester Digester, baseURL string) (*Service, error) {
	return NewServiceWithPolicy(repository, returnTargets, identity, sessions, protector, digester, baseURL, DefaultServicePolicy())
}

func NewServiceWithPolicy(repository Repository, returnTargets ReturnTargetPort, identity HostedIdentityPort, sessions SessionValidationPort, protector StateProtector, digester Digester, baseURL string, policy ServicePolicy) (*Service, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if repository == nil || returnTargets == nil || identity == nil || protector == nil || digester == nil || err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || !validServicePolicy(policy) {
		return nil, ErrTemporarilyUnavailable
	}
	return &Service{repository: repository, returnTargets: returnTargets, identity: identity, sessions: sessions,
		protector: protector, ids: securevalue.DefaultGenerator(), digester: digester, baseURL: baseURL,
		interactionTTL: policy.InteractionTTL, browserTTL: policy.BrowserTTL, grantTTL: policy.GrantTTL, leaseTTL: policy.LeaseTTL}, nil
}

type CreateCommand struct {
	Scope               Scope
	Actor               Actor
	Route               Route
	ReturnTargetCode    string
	State               string
	Nonce               string
	CodeChallenge       string
	CodeChallengeMethod string
	Locale              string
	ThemeVariant        string
	IdempotencyKey      string
	TraceID             string
}

type Launch struct {
	InteractionID  string
	InteractionURL string
	Route          Route
	Status         Status
	ExpiresAt      time.Time
}

func (s *Service) Create(ctx context.Context, command CreateCommand) (Launch, error) {
	if s == nil || !validCreate(command) {
		return Launch{}, ErrInvalidArgument
	}
	if command.Route == RouteAccount {
		if s.sessions == nil || s.sessions.ValidateHostedAccountSession(ctx, command.Scope, command.Actor) != nil {
			return Launch{}, ErrAuthenticationNeeded
		}
	}
	target, err := s.returnTargets.ResolveHostedReturnTarget(ctx, command.Scope, command.ReturnTargetCode)
	if err != nil || target.ProductID != command.Scope.ProductID || target.ApplicationID != command.Scope.ApplicationID || target.Code != command.ReturnTargetCode || target.PolicyVersion < 1 || !validReturnURI(target, command.Scope.Channel) {
		return Launch{}, ErrInvalidReturnTarget
	}
	interactionID, err := s.ids.ID("hint_")
	if err != nil {
		return Launch{}, err
	}
	securityContext := StateContext{InteractionID: interactionID, Route: command.Route, Scope: command.Scope, TraceID: command.TraceID}
	protectedState, err := s.protector.Protect(ctx, securityContext, command.State)
	if err != nil || protectedState.KeyRef == "" || len(protectedState.Ciphertext) == 0 || len(protectedState.Digest) != sha256.Size {
		return Launch{}, ErrTemporarilyUnavailable
	}
	now := time.Now().UTC()
	value := Interaction{InteractionID: interactionID, Route: command.Route, Scope: command.Scope, Actor: command.Actor,
		ReturnTargetCode: target.Code, ReturnTargetURI: target.URI, ReturnTargetPolicyVersion: target.PolicyVersion,
		StateProtectorKeyRef: protectedState.KeyRef, StateCiphertext: protectedState.Ciphertext, StateDigest: protectedState.Digest, Locale: command.Locale, ThemeVariant: command.ThemeVariant,
		Status: StatusCreated, Version: 1, TraceID: command.TraceID, CreatedAt: now, ExpiresAt: now.Add(s.interactionTTL)}
	if command.Route == RouteAuth {
		value.NonceDigest = s.digest("nonce", command.Nonce)
		value.PKCEChallengeDigest = s.digest("pkce-challenge", command.CodeChallenge)
		value.PKCEMethod = "S256"
	}
	requestDigest := digestJSON(struct {
		Scope                                          Scope
		Actor                                          Actor
		Route                                          Route
		Target, State, Nonce, Challenge, Locale, Theme string
	}{command.Scope, command.Actor, command.Route, command.ReturnTargetCode, command.State, command.Nonce, command.CodeChallenge, command.Locale, command.ThemeVariant})
	actorDigest := s.digest("actor", actorKey(command.Actor, command.Scope, command.Route))
	response, _ := json.Marshal(Launch{InteractionID: interactionID, Route: command.Route, Status: StatusCreated, ExpiresAt: value.ExpiresAt})
	event, err := s.event(value, "hosted.interaction_created.v1", StatusCreated)
	if err != nil {
		return Launch{}, err
	}
	stored, _, err := s.repository.Create(ctx, CreateRecord{Interaction: value, Operation: "create", ActorDigest: actorDigest, KeyDigest: s.digest("idempotency-key", command.IdempotencyKey), RequestDigest: requestDigest, Response: response, Event: event})
	if err != nil {
		return Launch{}, err
	}
	return s.launch(stored), nil
}

func (s *Service) GetForScope(ctx context.Context, interactionID string, scope Scope, actor Actor) (Projection, error) {
	if !validScope(scope) || !validActorForRoute(actor, routeForActor(actor)) {
		return Projection{}, ErrInvalidArgument
	}
	if actor.Kind == "user" && (s.sessions == nil || s.sessions.ValidateHostedAccountSession(ctx, scope, actor) != nil) {
		return Projection{}, ErrAuthenticationNeeded
	}
	value, err := s.repository.GetForScope(ctx, interactionID, scope, actor)
	if err != nil {
		return Projection{}, err
	}
	return Project(value), nil
}

func (s *Service) OpenBrowserSession(ctx context.Context, interactionID string) (BrowserSession, Projection, error) {
	sessionID, err := s.ids.ID("hbs_")
	if err != nil {
		return BrowserSession{}, Projection{}, err
	}
	token, err := s.ids.Token("")
	if err != nil {
		return BrowserSession{}, Projection{}, err
	}
	csrf := base64.RawURLEncoding.EncodeToString(s.digest("browser-csrf", token))
	current, err := s.repository.Get(ctx, interactionID)
	if err != nil {
		return BrowserSession{}, Projection{}, err
	}
	event, err := s.event(current, "hosted.interaction_opened.v1", StatusOpened)
	if err != nil {
		return BrowserSession{}, Projection{}, err
	}
	value, expiresAt, err := s.repository.OpenBrowserSession(ctx, OpenBrowserRecord{InteractionID: interactionID, SessionID: sessionID, TokenDigest: s.digest("browser-token", token), TTL: s.browserTTL, Event: event})
	if err != nil {
		return BrowserSession{}, Projection{}, err
	}
	return BrowserSession{BrowserSessionID: sessionID, InteractionID: interactionID, Token: token, CSRFToken: csrf, ExpiresAt: expiresAt}, Project(value), nil
}

func (s *Service) GetForBrowser(ctx context.Context, interactionID, browserToken string) (Projection, error) {
	value, err := s.repository.ValidateBrowserSession(ctx, interactionID, s.digest("browser-token", browserToken))
	if err != nil {
		return Projection{}, err
	}
	return Project(value), nil
}

func (s *Service) ValidateBrowserWrite(ctx context.Context, interactionID, browserToken, csrfToken string) (Projection, error) {
	wanted := base64.RawURLEncoding.EncodeToString(s.digest("browser-csrf", browserToken))
	if !hmac.Equal([]byte(wanted), []byte(csrfToken)) {
		return Projection{}, ErrCSRF
	}
	return s.GetForBrowser(ctx, interactionID, browserToken)
}

type AuthenticateCommand struct {
	InteractionID, BrowserToken, Identifier, Credential, Source, TraceID string
	Risk                                                                 map[string]string
}

func (s *Service) Authenticate(ctx context.Context, command AuthenticateCommand) (Completion, error) {
	if strings.TrimSpace(command.Identifier) == "" || command.Credential == "" {
		return Completion{}, ErrAuthenticationNeeded
	}
	browserDigest := s.digest("browser-token", command.BrowserToken)
	current, err := s.repository.ValidateBrowserSession(ctx, command.InteractionID, browserDigest)
	if err != nil {
		return Completion{}, err
	}
	if current.Status == StatusCompleted {
		grant, grantErr := s.repository.GetCompletionGrant(ctx, current.InteractionID, browserDigest)
		if grantErr != nil || grant.GrantType != "authorization_code" {
			return Completion{}, ErrInvalidGrant
		}
		return s.completion(ctx, current, grant)
	}
	value, err := s.repository.BeginAuthentication(ctx, command.InteractionID, browserDigest)
	if err != nil {
		return Completion{}, err
	}
	proof, err := s.identity.AuthenticateHosted(ctx, value.Scope, command.Identifier, command.Credential, command.Source, command.Risk, command.TraceID)
	if err != nil {
		_ = s.repository.ResetAuthentication(ctx, command.InteractionID, browserDigest)
		return Completion{}, err
	}
	if proof.ProofID == "" || proof.AuthTime.IsZero() || !proof.ExpiresAt.After(proof.AuthTime) {
		_ = s.repository.ResetAuthentication(ctx, command.InteractionID, browserDigest)
		return Completion{}, ErrAuthenticationNeeded
	}
	return s.complete(ctx, value, browserDigest, "", "", proof.ProofID, "authorization_code", nil)
}

type CompleteAccountCommand struct {
	InteractionID, BrowserToken, IdempotencyKey, TraceID string
	Actor                                                Actor
	Scope                                                Scope
	Result                                               string
}

func (s *Service) CompleteAccount(ctx context.Context, command CompleteAccountCommand) (Completion, error) {
	if s.sessions == nil || !validScope(command.Scope) || s.sessions.ValidateHostedAccountSession(ctx, command.Scope, command.Actor) != nil {
		return Completion{}, ErrAuthenticationNeeded
	}
	if command.Result != "closed" && command.Result != "self_service_completed" {
		return Completion{}, ErrInvalidArgument
	}
	value, err := s.repository.ValidateBrowserSession(ctx, command.InteractionID, s.digest("browser-token", command.BrowserToken))
	if err != nil {
		return Completion{}, err
	}
	if value.Route != RouteAccount || !value.Scope.Matches(command.Scope) || value.Actor.UserID != command.Actor.UserID || value.Actor.UserSessionID != command.Actor.UserSessionID {
		return Completion{}, ErrAuthenticationNeeded
	}
	return s.complete(ctx, value, s.digest("browser-token", command.BrowserToken), command.IdempotencyKey, actorKey(command.Actor, command.Scope, value.Route), "", "account_completed", map[string]any{"result": command.Result})
}

func (s *Service) complete(ctx context.Context, value Interaction, browserDigest []byte, idempotencyKey, actorKeyValue, proofID, grantType string, result map[string]any) (Completion, error) {
	grantID, err := s.ids.ID("hgrant_")
	if err != nil {
		return Completion{}, err
	}
	code := s.completionCode(grantID)
	if result == nil {
		result = map[string]any{}
	}
	resultDocument, _ := json.Marshal(result)
	operation := ""
	var actorDigest, keyDigest, requestDigest []byte
	if grantType == "account_completed" {
		if strings.TrimSpace(idempotencyKey) == "" {
			return Completion{}, ErrInvalidArgument
		}
		operation, actorDigest, keyDigest = "account_complete", s.digest("actor", actorKeyValue), s.digest("idempotency-key", idempotencyKey)
		requestDigest = digestJSON(struct {
			InteractionID string
			Result        map[string]any
		}{value.InteractionID, result})
	}
	event, err := s.event(value, "hosted.interaction_completed.v1", StatusCompleted)
	if err != nil {
		return Completion{}, err
	}
	completed, grant, _, err := s.repository.Complete(ctx, CompleteRecord{InteractionID: value.InteractionID, BrowserTokenDigest: browserDigest, ExpectedStatus: []Status{StatusOpened, StatusAuthenticating}, GrantID: grantID, GrantType: grantType, CodeDigest: s.digest("completion-code", code), IdentityProofID: proofID, ResultDocument: resultDocument, GrantTTL: s.grantTTL, Operation: operation, ActorDigest: actorDigest, KeyDigest: keyDigest, RequestDigest: requestDigest, Event: event})
	if err != nil {
		return Completion{}, err
	}
	return s.completion(ctx, completed, grant)
}

func (s *Service) completion(ctx context.Context, completed Interaction, grant CompletionGrant) (Completion, error) {
	code := s.completionCode(grant.GrantID)
	state, err := s.protector.Reveal(ctx, StateContext{InteractionID: completed.InteractionID, Route: completed.Route, Scope: completed.Scope, TraceID: completed.TraceID}, ProtectedState{KeyRef: completed.StateProtectorKeyRef, Ciphertext: completed.StateCiphertext, Digest: completed.StateDigest})
	if err != nil {
		return Completion{}, ErrTemporarilyUnavailable
	}
	return Completion{Interaction: Project(completed), Code: code, ReturnURL: buildReturnURL(completed.ReturnTargetURI, code, state, completed.InteractionID), GrantExpiresAt: grant.ExpiresAt}, nil
}

func (s *Service) Cancel(ctx context.Context, interactionID, browserToken string) (Projection, error) {
	value, err := s.repository.Get(ctx, interactionID)
	if err != nil {
		return Projection{}, err
	}
	event, err := s.event(value, "hosted.interaction_cancelled.v1", StatusCancelled)
	if err != nil {
		return Projection{}, err
	}
	value, err = s.repository.Cancel(ctx, interactionID, s.digest("browser-token", browserToken), event)
	if err != nil {
		return Projection{}, err
	}
	return Project(value), nil
}

type ExchangeCommand struct {
	InteractionID, Code, CodeVerifier, TraceID string
	Scope                                      Scope
}

func (s *Service) Exchange(ctx context.Context, command ExchangeCommand) (ExchangeResult, error) {
	value, err := s.repository.Get(ctx, command.InteractionID)
	if err != nil {
		return ExchangeResult{}, err
	}
	var verifierDigest []byte
	if value.Route == RouteAuth {
		challenge := sha256.Sum256([]byte(command.CodeVerifier))
		verifierDigest = s.digest("pkce-challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	}
	leaseToken, err := s.ids.Token("")
	if err != nil {
		return ExchangeResult{}, err
	}
	claim, err := s.repository.ClaimGrant(ctx, command.InteractionID, command.Scope, s.digest("completion-code", command.Code), verifierDigest, s.leaseTTL, leaseToken, s.digest("grant-lease", leaseToken))
	if err != nil {
		return ExchangeResult{}, err
	}
	var issued *IssuedUserSession
	if claim.GrantType == "authorization_code" {
		result, redeemErr := s.identity.RedeemHostedAuthGrant(ctx, claim.GrantID, claim.IdentityProofID, claim.Scope, command.TraceID)
		if redeemErr != nil {
			return ExchangeResult{}, redeemErr
		}
		issued = &result
	}
	event, err := s.event(value, "hosted.interaction_exchanged.v1", StatusExchanged)
	if err != nil {
		return ExchangeResult{}, err
	}
	exchanged, err := s.repository.ConsumeGrant(ctx, claim.GrantID, s.digest("grant-lease", leaseToken), event)
	if err != nil {
		return ExchangeResult{}, err
	}
	return ExchangeResult{Interaction: Project(exchanged), ResultKind: claim.GrantType, UserSession: issued, Document: claim.ResultDocument}, nil
}

func (s *Service) ExpireDue(ctx context.Context, limit int) (int, error) {
	return s.repository.ExpireDue(ctx, limit)
}

func (s *Service) launch(value Interaction) Launch {
	path := "/ui/v1/account"
	if value.Route == RouteAuth {
		path = "/ui/v1/auth"
	}
	return Launch{InteractionID: value.InteractionID, InteractionURL: s.baseURL + path + "?interaction_id=" + url.QueryEscape(value.InteractionID), Route: value.Route, Status: value.Status, ExpiresAt: value.ExpiresAt}
}

func (s *Service) event(value Interaction, eventType string, status Status) (OutboxEvent, error) {
	eventID, err := s.ids.ID("evt_")
	if err != nil {
		return OutboxEvent{}, err
	}
	payload, err := json.Marshal(struct {
		InteractionID string  `json:"interaction_id"`
		ProductID     string  `json:"product_id"`
		ApplicationID string  `json:"application_id"`
		TenantID      *string `json:"tenant_id,omitempty"`
		Route         Route   `json:"route"`
		Status        Status  `json:"status"`
		TraceID       string  `json:"trace_id"`
	}{value.InteractionID, value.Scope.ProductID, value.Scope.ApplicationID, value.Scope.TenantID, value.Route, status, value.TraceID})
	return OutboxEvent{EventID: eventID, InteractionID: value.InteractionID, EventType: eventType, Payload: payload, OccurredAt: time.Now().UTC()}, err
}

func validCreate(c CreateCommand) bool {
	if !validScope(c.Scope) || c.Route != routeForActor(c.Actor) || !validActorForRoute(c.Actor, c.Route) || !stableCode.MatchString(c.ReturnTargetCode) || len(c.State) < 22 || len(c.State) > 512 || strings.TrimSpace(c.IdempotencyKey) == "" || strings.TrimSpace(c.TraceID) == "" {
		return false
	}
	if c.Route == RouteAuth {
		return len(c.Nonce) >= 22 && pkceChallengeCode.MatchString(c.CodeChallenge) && c.CodeChallengeMethod == "S256"
	}
	return c.Nonce == "" && c.CodeChallenge == "" && c.CodeChallengeMethod == ""
}

func validScope(s Scope) bool {
	return s.ProductID != "" && s.ApplicationID != "" && (s.Environment == "local" || s.Environment == "test" || s.Environment == "production") && (s.Channel == ChannelWeb || s.Channel == ChannelH5 || s.Channel == ChannelDesktop || s.Channel == ChannelApp)
}
func routeForActor(a Actor) Route {
	if a.Kind == "user" {
		return RouteAccount
	}
	return RouteAuth
}
func validActorForRoute(a Actor, route Route) bool {
	if route == RouteAuth {
		return a.Kind == "client" && a.ClientSessionID != "" && a.UserID == "" && a.UserSessionID == ""
	}
	return a.Kind == "user" && a.ClientSessionID != "" && a.UserID != "" && a.UserSessionID != ""
}
func actorKey(a Actor, scope Scope, route Route) string {
	return strings.Join([]string{a.Kind, a.ClientSessionID, a.UserID, a.UserSessionID, scope.ProductID, scope.ApplicationID, optional(scope.TenantID), scope.Environment, string(scope.Channel), string(route)}, "\x00")
}
func optional(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func validReturnURI(target ReturnTarget, channel Channel) bool {
	parsed, err := url.Parse(target.URI)
	if err != nil || !parsed.IsAbs() || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if target.Kind == "web_redirect" || channel == ChannelWeb || channel == ChannelH5 {
		return scheme == "https" && parsed.Host != ""
	}
	if target.Kind != "deep_link" || (channel != ChannelDesktop && channel != ChannelApp) {
		return false
	}
	if scheme == "" || scheme == "http" || scheme == "https" || scheme == "javascript" || scheme == "data" || scheme == "file" {
		return false
	}
	return regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`).MatchString(scheme)
}
func buildReturnURL(raw, code, state, interactionID string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	q := make(url.Values)
	q.Set("code", code)
	q.Set("state", state)
	q.Set("interaction_id", interactionID)
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func validServicePolicy(policy ServicePolicy) bool {
	return policy.InteractionTTL >= time.Minute && policy.InteractionTTL <= time.Hour &&
		policy.BrowserTTL >= time.Minute && policy.BrowserTTL <= policy.InteractionTTL &&
		policy.GrantTTL >= 30*time.Second && policy.GrantTTL <= policy.InteractionTTL &&
		policy.LeaseTTL >= time.Second && policy.LeaseTTL < policy.GrantTTL
}
func digestJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return sum[:]
}

func (s *Service) digest(purpose, value string) []byte {
	return s.digester.Digest("hosted-interaction.v1\x00" + purpose + "\x00" + value)
}

func (s *Service) completionCode(grantID string) string {
	return base64.RawURLEncoding.EncodeToString(s.digest("completion-code-material", grantID))
}
