package httptransport

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

const (
	HostedSessionCookieName = "__Host-platform_hosted_session"
	requestBodyLimit        = 32 << 10
)

var (
	ErrAuthenticationRequired = errors.New("hosted authentication required")
	ErrInvalidInteraction     = errors.New("invalid hosted interaction")
	ErrInteractionExpired     = errors.New("hosted interaction expired")
	ErrInvalidReturnTarget    = errors.New("invalid hosted return target")
	ErrStateMismatch          = errors.New("hosted state mismatch")
	ErrPKCERequired           = errors.New("hosted pkce required")
	ErrInvalidGrant           = errors.New("invalid hosted grant")
	ErrChannelNotSupported    = errors.New("hosted channel not supported")
	ErrSessionRevoked         = errors.New("hosted session revoked")
	ErrCSRFFailed             = errors.New("hosted csrf failed")
	ErrConflict               = errors.New("hosted request conflict")
	ErrTemporarilyUnavailable = errors.New("hosted temporarily unavailable")

	interactionIDPattern = regexp.MustCompile(`^hint_[A-Za-z0-9_-]{24,160}$`)
	stableCodePattern    = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	pkceChallengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	pkceVerifierPattern  = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
)

type Scope struct {
	ProductID     string
	ApplicationID string
	TenantID      *string
	Environment   string
	Channel       string
}

type BearerPrincipal struct {
	Kind      string // client or user
	Scope     Scope
	SessionID string
	UserID    string
	AuthTime  time.Time
}

type HostedPrincipal struct {
	InteractionID    string
	BrowserSessionID string
	CSRFToken        string
}

type Authenticator interface {
	ResolveBearer(context.Context, string) (BearerPrincipal, error)
	ResolveHostedSession(context.Context, string, string) (HostedPrincipal, error)
}

type Service interface {
	Create(context.Context, BearerPrincipal, CreateCommand) (InteractionLaunch, error)
	Get(context.Context, AccessPrincipal, string) (Interaction, error)
	OpenBrowserSession(context.Context, string) (BrowserSession, error)
	AuthenticatePassword(context.Context, HostedPrincipal, string, PasswordCommand) (Completion, error)
	CompleteAccount(context.Context, HostedPrincipal, string, CompleteAccountCommand) (Completion, error)
	Cancel(context.Context, HostedPrincipal, string, string) (Interaction, error)
	Exchange(context.Context, BearerPrincipal, string, ExchangeCommand) (ExchangeResult, error)
}

type AccessPrincipal struct {
	Bearer *BearerPrincipal
	Hosted *HostedPrincipal
}

type CreateCommand struct {
	RouteID, Channel, ReturnTargetCode, State, Nonce, CodeChallenge, CodeChallengeMethod string
	Locale, ThemeVariant                                                                 *string
	IdempotencyKey, RequestID                                                            string
}

type PasswordCommand struct {
	Identifier, Credential string
	RiskSummary            map[string]any
	RequestID              string
}

type CompleteAccountCommand struct {
	Result, IdempotencyKey, RequestID string
}

type ExchangeCommand struct {
	Code, CodeVerifier, RequestID string
}

type InteractionLaunch struct {
	InteractionID  string    `json:"interaction_id"`
	InteractionURL string    `json:"interaction_url"`
	RouteID        string    `json:"route_id"`
	Status         string    `json:"status"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type Interaction struct {
	InteractionID  string     `json:"interaction_id"`
	RouteID        string     `json:"route_id"`
	Channel        string     `json:"channel"`
	Status         string     `json:"status"`
	AllowedActions []string   `json:"allowed_actions"`
	ResultKind     *string    `json:"result_kind,omitempty"`
	FailureCode    *string    `json:"failure_code,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	OpenedAt       *time.Time `json:"opened_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

type BrowserSession struct {
	Interaction             Interaction `json:"interaction"`
	CSRFToken               string      `json:"csrf_token"`
	BrowserSessionExpiresAt time.Time   `json:"browser_session_expires_at"`
	CookieToken             string      `json:"-"`
}

type Completion struct {
	InteractionID string    `json:"interaction_id"`
	Status        string    `json:"status"`
	ReturnURL     string    `json:"return_url"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type UserSummary struct {
	UserID              string  `json:"user_id"`
	AccountStatus       string  `json:"account_status"`
	DisplayName         *string `json:"display_name,omitempty"`
	ProductID           *string `json:"product_id,omitempty"`
	TenantID            *string `json:"tenant_id,omitempty"`
	AccessVersion       *int64  `json:"access_version,omitempty"`
	ProductAccessStatus *string `json:"product_access_status,omitempty"`
	TenantAccessStatus  *string `json:"tenant_access_status,omitempty"`
}

type IssuedUserSession struct {
	AccessToken      string      `json:"access_token"`
	RefreshToken     string      `json:"refresh_token"`
	AccessExpiresAt  time.Time   `json:"access_expires_at"`
	RefreshExpiresAt time.Time   `json:"refresh_expires_at"`
	User             UserSummary `json:"user"`
}

type AccountResult struct {
	Result string `json:"result"`
}
type ExchangeResult struct {
	InteractionID string             `json:"interaction_id"`
	ResultType    string             `json:"result_type"`
	UserSession   *IssuedUserSession `json:"user_session,omitempty"`
	AccountResult *AccountResult     `json:"account_result,omitempty"`
}

type Handler struct {
	service      Service
	auth         Authenticator
	hostedOrigin string
}

func New(service Service, auth Authenticator, hostedOrigin string) (*Handler, error) {
	parsed, err := url.Parse(hostedOrigin)
	if service == nil || auth == nil || err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || parsed.String() != hostedOrigin {
		return nil, errors.New("invalid hosted HTTP transport configuration")
	}
	return &Handler{service: service, auth: auth, hostedOrigin: hostedOrigin}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	if r.URL.RawQuery != "" || r.URL.RawPath != "" {
		h.writeError(w, r, ErrInvalidInteraction)
		return
	}
	path := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(path) == 4 && strings.Join(path, "/") == "api/v1/hosted/interactions" {
		h.create(w, r)
		return
	}
	if len(path) < 5 || path[0] != "api" || path[1] != "v1" || path[2] != "hosted" || path[3] != "interactions" || !interactionIDPattern.MatchString(path[4]) {
		h.writeError(w, r, ErrInvalidInteraction)
		return
	}
	id := path[4]
	switch {
	case len(path) == 5:
		h.get(w, r, id)
	case len(path) == 6 && path[5] == "browser-session":
		h.open(w, r, id)
	case len(path) == 7 && path[5] == "auth" && path[6] == "password":
		h.password(w, r, id)
	case len(path) == 7 && path[5] == "account" && path[6] == "complete":
		h.accountComplete(w, r, id)
	case len(path) == 6 && path[5] == "cancel":
		h.cancel(w, r, id)
	case len(path) == 6 && path[5] == "exchange":
		h.exchange(w, r, id)
	default:
		h.writeError(w, r, ErrInvalidInteraction)
	}
}

type createRequest struct {
	RouteID             string  `json:"route_id"`
	Channel             string  `json:"channel"`
	ReturnTargetCode    string  `json:"return_target_code"`
	State               string  `json:"state"`
	Nonce               *string `json:"nonce"`
	CodeChallenge       *string `json:"code_challenge"`
	CodeChallengeMethod *string `json:"code_challenge_method"`
	Locale              *string `json:"locale"`
	ThemeVariant        *string `json:"theme_variant"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	key, ok := singleOpaqueHeader(r, "Idempotency-Key", 16, 128)
	if !ok {
		h.writeInvalid(w, r)
		return
	}
	var body createRequest
	if !decodeStrictJSON(w, r, &body) || !validCreate(body) {
		h.writeInvalid(w, r)
		return
	}
	principal, ok := h.requireBearer(w, r)
	if !ok {
		return
	}
	if (body.RouteID == "hosted.auth" && principal.Kind != "client") || (body.RouteID == "hosted.account" && principal.Kind != "user") {
		h.writeError(w, r, ErrAuthenticationRequired)
		return
	}
	if body.Channel != principal.Scope.Channel {
		h.writeError(w, r, ErrChannelNotSupported)
		return
	}
	value, err := h.service.Create(r.Context(), principal, CreateCommand{RouteID: body.RouteID, Channel: body.Channel, ReturnTargetCode: body.ReturnTargetCode, State: body.State, Nonce: stringValue(body.Nonce), CodeChallenge: stringValue(body.CodeChallenge), CodeChallengeMethod: stringValue(body.CodeChallengeMethod), Locale: body.Locale, ThemeVariant: body.ThemeVariant, IdempotencyKey: key, RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if !h.validLaunch(value, body.RouteID) {
		h.writeError(w, r, ErrTemporarilyUnavailable)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func validCreate(v createRequest) bool {
	if (v.RouteID != "hosted.auth" && v.RouteID != "hosted.account") || !oneOf(v.Channel, "web", "h5", "desktop", "app") || !stableCodePattern.MatchString(v.ReturnTargetCode) || !bounded(v.State, 22, 512) || !validOptional(v.Locale, 2, 32) || !validOptional(v.ThemeVariant, 1, 64) {
		return false
	}
	if v.RouteID == "hosted.auth" {
		return v.Nonce != nil && bounded(*v.Nonce, 22, 512) && v.CodeChallenge != nil && pkceChallengePattern.MatchString(*v.CodeChallenge) && v.CodeChallengeMethod != nil && *v.CodeChallengeMethod == "S256"
	}
	return v.Nonce == nil && v.CodeChallenge == nil && v.CodeChallengeMethod == nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodGet) || !emptyBody(w, r) {
		return
	}
	access, ok := h.requireAccess(w, r, id)
	if !ok {
		return
	}
	if access.Hosted != nil && access.Hosted.InteractionID != id {
		h.writeError(w, r, ErrInvalidInteraction)
		return
	}
	value, err := h.service.Get(r.Context(), access, id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if !validInteraction(value, id) {
		h.writeError(w, r, ErrTemporarilyUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *Handler) open(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodPost) || !emptyBody(w, r) {
		return
	}
	if len(r.Header.Values("Authorization")) != 0 {
		h.writeError(w, r, ErrAuthenticationRequired)
		return
	}
	value, err := h.service.OpenBrowserSession(r.Context(), id)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if !opaque(value.CookieToken, 32, 4096) || !bounded(value.CSRFToken, 32, 256) || !value.BrowserSessionExpiresAt.After(time.Now()) || !validInteraction(value.Interaction, id) {
		h.writeError(w, r, ErrTemporarilyUnavailable)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: HostedSessionCookieName, Value: value.CookieToken, Path: "/", Expires: value.BrowserSessionExpiresAt, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, value)
}

type passwordRequest struct {
	Identifier  string          `json:"identifier"`
	Credential  string          `json:"credential"`
	RiskSummary json.RawMessage `json:"risk_summary"`
}

func (h *Handler) password(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	p, ok := h.requireHostedWrite(w, r, id)
	if !ok {
		return
	}
	var body passwordRequest
	if !decodeStrictJSON(w, r, &body) || !bounded(body.Identifier, 3, 320) || !bounded(body.Credential, 8, 1024) {
		h.writeInvalid(w, r)
		return
	}
	risk, ok := decodeRisk(body.RiskSummary)
	if !ok {
		h.writeInvalid(w, r)
		return
	}
	value, err := h.service.AuthenticatePassword(r.Context(), p, id, PasswordCommand{Identifier: body.Identifier, Credential: body.Credential, RiskSummary: risk, RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if !validCompletion(value, id) {
		h.writeError(w, r, ErrTemporarilyUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

type completeAccountRequest struct {
	Result string `json:"result"`
}

func (h *Handler) accountComplete(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	p, ok := h.requireHostedWrite(w, r, id)
	if !ok {
		return
	}
	key, ok := singleOpaqueHeader(r, "Idempotency-Key", 16, 128)
	if !ok {
		h.writeInvalid(w, r)
		return
	}
	var body completeAccountRequest
	if !decodeStrictJSON(w, r, &body) || !oneOf(body.Result, "closed", "self_service_completed") {
		h.writeInvalid(w, r)
		return
	}
	value, err := h.service.CompleteAccount(r.Context(), p, id, CompleteAccountCommand{Result: body.Result, IdempotencyKey: key, RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if !validCompletion(value, id) {
		h.writeError(w, r, ErrTemporarilyUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodPost) || !emptyBody(w, r) {
		return
	}
	p, ok := h.requireHostedWrite(w, r, id)
	if !ok {
		return
	}
	value, err := h.service.Cancel(r.Context(), p, id, requestid.FromContext(r.Context()))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if !validInteraction(value, id) {
		h.writeError(w, r, ErrTemporarilyUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

type exchangeRequest struct {
	Code         string  `json:"code"`
	CodeVerifier *string `json:"code_verifier"`
}

func (h *Handler) exchange(w http.ResponseWriter, r *http.Request, id string) {
	if !onlyMethod(w, r, http.MethodPost) {
		return
	}
	p, ok := h.requireBearer(w, r)
	if !ok {
		return
	}
	if p.Kind != "client" {
		h.writeError(w, r, ErrAuthenticationRequired)
		return
	}
	var body exchangeRequest
	if !decodeStrictJSON(w, r, &body) || !bounded(body.Code, 32, 512) || (body.CodeVerifier != nil && !pkceVerifierPattern.MatchString(*body.CodeVerifier)) {
		h.writeInvalid(w, r)
		return
	}
	value, err := h.service.Exchange(r.Context(), p, id, ExchangeCommand{Code: body.Code, CodeVerifier: stringValue(body.CodeVerifier), RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if !validExchange(value, id, p.Scope) {
		h.writeError(w, r, ErrTemporarilyUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (h *Handler) requireBearer(w http.ResponseWriter, r *http.Request) (BearerPrincipal, bool) {
	if hostedCookieCount(r) != 0 {
		h.writeError(w, r, ErrAuthenticationRequired)
		return BearerPrincipal{}, false
	}
	token, ok := bearer(r)
	if !ok {
		h.writeError(w, r, ErrAuthenticationRequired)
		return BearerPrincipal{}, false
	}
	p, err := h.auth.ResolveBearer(r.Context(), token)
	if err != nil {
		if errors.Is(err, ErrAuthenticationRequired) || errors.Is(err, ErrSessionRevoked) {
			h.writeError(w, r, ErrAuthenticationRequired)
		} else {
			h.writeError(w, r, ErrTemporarilyUnavailable)
		}
		return BearerPrincipal{}, false
	}
	if !validBearerPrincipal(p) {
		h.writeError(w, r, ErrAuthenticationRequired)
		return BearerPrincipal{}, false
	}
	return p, true
}

func (h *Handler) requireAccess(w http.ResponseWriter, r *http.Request, interactionID string) (AccessPrincipal, bool) {
	hasAuth := len(r.Header.Values("Authorization")) != 0
	cookie, hasCookie := hostedCookie(r)
	if hasAuth == hasCookie {
		h.writeError(w, r, ErrAuthenticationRequired)
		return AccessPrincipal{}, false
	}
	if hasAuth {
		p, ok := h.requireBearer(w, r)
		return AccessPrincipal{Bearer: &p}, ok
	}
	p, err := h.auth.ResolveHostedSession(r.Context(), interactionID, cookie)
	if err != nil {
		if errors.Is(err, ErrSessionRevoked) || errors.Is(err, ErrAuthenticationRequired) {
			h.writeError(w, r, ErrSessionRevoked)
		} else {
			h.writeError(w, r, ErrTemporarilyUnavailable)
		}
		return AccessPrincipal{}, false
	}
	if !validHostedPrincipal(p) {
		h.writeError(w, r, ErrSessionRevoked)
		return AccessPrincipal{}, false
	}
	return AccessPrincipal{Hosted: &p}, true
}

func (h *Handler) requireHostedWrite(w http.ResponseWriter, r *http.Request, id string) (HostedPrincipal, bool) {
	if len(r.Header.Values("Authorization")) != 0 {
		h.writeError(w, r, ErrAuthenticationRequired)
		return HostedPrincipal{}, false
	}
	cookie, ok := hostedCookie(r)
	if !ok {
		h.writeError(w, r, ErrAuthenticationRequired)
		return HostedPrincipal{}, false
	}
	p, err := h.auth.ResolveHostedSession(r.Context(), id, cookie)
	if err != nil {
		if errors.Is(err, ErrSessionRevoked) || errors.Is(err, ErrAuthenticationRequired) {
			h.writeError(w, r, ErrSessionRevoked)
		} else {
			h.writeError(w, r, ErrTemporarilyUnavailable)
		}
		return HostedPrincipal{}, false
	}
	if !validHostedPrincipal(p) {
		h.writeError(w, r, ErrSessionRevoked)
		return HostedPrincipal{}, false
	}
	if p.InteractionID != id {
		h.writeError(w, r, ErrInvalidInteraction)
		return HostedPrincipal{}, false
	}
	origins := r.Header.Values("Origin")
	csrf := r.Header.Values("X-CSRF-Token")
	if len(origins) != 1 || origins[0] != h.hostedOrigin || len(csrf) != 1 || !constantEqual(csrf[0], p.CSRFToken) {
		h.writeError(w, r, ErrCSRFFailed)
		return HostedPrincipal{}, false
	}
	return p, true
}

func validBearerPrincipal(p BearerPrincipal) bool {
	return oneOf(p.Kind, "client", "user") && p.SessionID != "" && p.Scope.ProductID != "" && p.Scope.ApplicationID != "" && oneOf(p.Scope.Environment, "local", "test", "production") && oneOf(p.Scope.Channel, "web", "h5", "desktop", "app") && (p.Kind != "user" || p.UserID != "")
}
func validHostedPrincipal(p HostedPrincipal) bool {
	return interactionIDPattern.MatchString(p.InteractionID) && p.BrowserSessionID != "" && bounded(p.CSRFToken, 32, 256)
}

func hostedCookie(r *http.Request) (string, bool) {
	var value string
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == HostedSessionCookieName {
			count++
			value = c.Value
		}
	}
	return value, count == 1 && bounded(value, 32, 4096)
}

func hostedCookieCount(r *http.Request) int {
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == HostedSessionCookieName {
			count++
		}
	}
	return count
}

func bearer(r *http.Request) (string, bool) {
	v := r.Header.Values("Authorization")
	if len(v) != 1 || len(v[0]) < 8 || !strings.EqualFold(v[0][:7], "Bearer ") || v[0][7] == ' ' {
		return "", false
	}
	t := v[0][7:]
	return t, opaque(t, 16, 4096)
}

func singleOpaqueHeader(r *http.Request, name string, min, max int) (string, bool) {
	v := r.Header.Values(name)
	if len(v) != 1 || !opaque(v[0], min, max) {
		return "", false
	}
	return v[0], true
}
func opaque(v string, min, max int) bool {
	if !bounded(v, min, max) || strings.TrimSpace(v) != v {
		return false
	}
	for _, b := range []byte(v) {
		if b < 0x21 || b > 0x7e || b == ',' {
			return false
		}
	}
	return true
}
func bounded(v string, min, max int) bool {
	return utf8.ValidString(v) && len(v) >= min && len(v) <= max
}
func validOptional(v *string, min, max int) bool { return v == nil || bounded(*v, min, max) }
func oneOf(v string, values ...string) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}
func constantEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func decodeRisk(raw json.RawMessage) (map[string]any, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	var v map[string]any
	if string(raw) == "null" || json.Unmarshal(raw, &v) != nil || v == nil {
		return nil, false
	}
	if len(v) > 16 {
		return nil, false
	}
	for _, x := range v {
		switch x.(type) {
		case nil, string, float64, bool:
		default:
			return nil, false
		}
	}
	return v, true
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func (h *Handler) validLaunch(value InteractionLaunch, route string) bool {
	if !interactionIDPattern.MatchString(value.InteractionID) || value.RouteID != route || value.Status != "created" || !value.ExpiresAt.After(time.Now()) {
		return false
	}
	parsed, err := url.Parse(value.InteractionURL)
	if err != nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.User != nil || parsed.Fragment != "" || parsed.Scheme+"://"+parsed.Host != h.hostedOrigin {
		return false
	}
	wantPath := "/ui/v1/auth"
	if route == "hosted.account" {
		wantPath = "/ui/v1/account"
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	return err == nil && parsed.Path == wantPath && len(query) == 1 && len(query["interaction_id"]) == 1 && query.Get("interaction_id") == value.InteractionID
}

func validInteraction(value Interaction, expectedID string) bool {
	if value.InteractionID != expectedID || !interactionIDPattern.MatchString(value.InteractionID) || !oneOf(value.RouteID, "hosted.auth", "hosted.account") || !oneOf(value.Channel, "web", "h5", "desktop", "app") || !oneOf(value.Status, "created", "opened", "authenticating", "completed", "exchanged", "cancelled", "failed", "expired") || value.CreatedAt.IsZero() || !value.ExpiresAt.After(value.CreatedAt) {
		return false
	}
	wantActions := map[string][]string{
		"created": {"open", "cancel"}, "opened": {"authenticate", "complete", "cancel"}, "authenticating": {"authenticate", "complete", "cancel"},
		"completed": {"exchange"}, "exchanged": {}, "cancelled": {}, "failed": {"restart"}, "expired": {"restart"},
	}
	if !sameUniqueStrings(value.AllowedActions, wantActions[value.Status]) {
		return false
	}
	switch value.Status {
	case "created", "opened", "authenticating":
		return value.ResultKind == nil && value.FailureCode == nil && value.CompletedAt == nil
	case "completed", "exchanged":
		return value.ResultKind != nil && oneOf(*value.ResultKind, "authorization_code", "account_completed") && ((value.RouteID == "hosted.auth") == (*value.ResultKind == "authorization_code")) && value.FailureCode == nil && value.CompletedAt != nil
	case "cancelled":
		return value.ResultKind != nil && *value.ResultKind == "cancelled" && value.FailureCode == nil && value.CompletedAt == nil
	case "failed", "expired":
		return value.ResultKind != nil && *value.ResultKind == "failed" && value.FailureCode != nil && stableProblemCode(*value.FailureCode) && value.CompletedAt == nil
	}
	return false
}

func validCompletion(value Completion, expectedID string) bool {
	if value.InteractionID != expectedID || value.Status != "completed" || !value.ExpiresAt.After(time.Now()) {
		return false
	}
	parsed, err := url.Parse(value.ReturnURL)
	if err != nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.User != nil || parsed.Fragment != "" || strings.ContainsAny(value.ReturnURL, "\r\n") {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if oneOf(scheme, "http", "javascript", "data", "file") || (scheme == "https" && parsed.Host == "") {
		return false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(query) != 3 || len(query["code"]) != 1 || len(query["state"]) != 1 || len(query["interaction_id"]) != 1 {
		return false
	}
	return bounded(query.Get("code"), 32, 512) && bounded(query.Get("state"), 22, 512) && query.Get("interaction_id") == expectedID
}

func validExchange(value ExchangeResult, expectedID string, scope Scope) bool {
	if value.InteractionID != expectedID {
		return false
	}
	switch value.ResultType {
	case "user_session":
		if value.UserSession == nil || value.AccountResult != nil {
			return false
		}
		s := value.UserSession
		if !opaque(s.AccessToken, 32, 4096) || !opaque(s.RefreshToken, 32, 4096) || !s.AccessExpiresAt.After(time.Now()) || s.RefreshExpiresAt.Before(s.AccessExpiresAt) || s.User.UserID == "" || !oneOf(s.User.AccountStatus, "active", "locked", "disabled") {
			return false
		}
		if s.User.ProductID != nil && *s.User.ProductID != scope.ProductID {
			return false
		}
		if s.User.TenantID != nil && !equalOptionalString(s.User.TenantID, scope.TenantID) {
			return false
		}
		return true
	case "account_completed":
		return value.UserSession == nil && value.AccountResult != nil && oneOf(value.AccountResult.Result, "closed", "self_service_completed")
	default:
		return false
	}
}

func sameUniqueStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	want := make(map[string]bool, len(right))
	for _, value := range right {
		want[value] = true
	}
	seen := make(map[string]bool, len(left))
	for _, value := range left {
		if !want[value] || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func stableProblemCode(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, requestBodyLimit)
	raw, err := io.ReadAll(r.Body)
	if err != nil || len(raw) == 0 || !uniqueJSON(raw) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(&struct{}{}) != io.EOF {
		return false
	}
	return true
}

func uniqueJSON(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	if !consumeJSON(d) {
		return false
	}
	_, err := d.Token()
	return errors.Is(err, io.EOF)
}
func consumeJSON(d *json.Decoder) bool {
	t, err := d.Token()
	if err != nil {
		return false
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			key, ok := k.(string)
			if err != nil || !ok || seen[key] {
				return false
			}
			seen[key] = true
			if !consumeJSON(d) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for d.More() {
			if !consumeJSON(d) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == json.Delim(']')
	}
	return false
}

func emptyBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1)
	var b [1]byte
	n, _ := r.Body.Read(b[:])
	if n != 0 {
		httpx.ErrorWithOptions(w, r, http.StatusBadRequest, "invalid_request", "invalid request", httpx.ErrorOptions{Retryable: false})
		return false
	}
	return true
}
func onlyMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		httpx.MethodNotAllowed(w, r, method)
		return false
	}
	return true
}

func securityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (h *Handler) writeInvalid(w http.ResponseWriter, r *http.Request) {
	httpx.ErrorWithOptions(w, r, http.StatusBadRequest, "invalid_request", "invalid request", httpx.ErrorOptions{Retryable: false})
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, detail, retryable := http.StatusServiceUnavailable, "hosted.temporarily_unavailable", "hosted interaction temporarily unavailable", true
	switch {
	case errors.Is(err, ErrAuthenticationRequired):
		status, code, detail, retryable = http.StatusUnauthorized, "hosted.authentication_required", "authentication required", false
	case errors.Is(err, ErrInvalidInteraction):
		status, code, detail, retryable = http.StatusNotFound, "hosted.invalid_interaction", "hosted interaction not found", false
	case errors.Is(err, ErrInteractionExpired):
		status, code, detail, retryable = http.StatusGone, "hosted.interaction_expired", "hosted interaction expired", false
	case errors.Is(err, ErrInvalidReturnTarget):
		status, code, detail, retryable = http.StatusBadRequest, "hosted.invalid_return_target", "invalid return target", false
	case errors.Is(err, ErrStateMismatch):
		status, code, detail, retryable = http.StatusBadRequest, "hosted.state_mismatch", "state mismatch", false
	case errors.Is(err, ErrPKCERequired):
		status, code, detail, retryable = http.StatusBadRequest, "hosted.pkce_required", "PKCE verification required", false
	case errors.Is(err, ErrInvalidGrant):
		status, code, detail, retryable = http.StatusConflict, "hosted.invalid_grant", "hosted grant is invalid", false
	case errors.Is(err, ErrChannelNotSupported):
		status, code, detail, retryable = http.StatusBadRequest, "hosted.channel_not_supported", "hosted channel is not supported", false
	case errors.Is(err, ErrSessionRevoked):
		status, code, detail, retryable = http.StatusUnauthorized, "hosted.session_revoked", "hosted browser session is unavailable", false
	case errors.Is(err, ErrCSRFFailed):
		status, code, detail, retryable = http.StatusForbidden, "hosted.csrf_failed", "hosted request verification failed", false
	case errors.Is(err, ErrConflict):
		status, code, detail, retryable = http.StatusConflict, "hosted.invalid_interaction", "hosted interaction conflicts with current state", false
	}
	httpx.ErrorWithOptions(w, r, status, code, detail, httpx.ErrorOptions{Retryable: retryable})
}
