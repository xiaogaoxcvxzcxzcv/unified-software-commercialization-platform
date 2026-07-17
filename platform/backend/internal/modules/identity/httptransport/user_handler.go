package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

var (
	ErrInvalidBearer                  = errors.New("invalid user bearer")
	ErrInvalidUserRequest             = errors.New("invalid user request")
	ErrInvalidUserCredentials         = errors.New("invalid user credentials")
	ErrUserProviderUnavailable        = errors.New("user identity provider unavailable")
	ErrProductUserAccessSuspended     = errors.New("product user access suspended")
	ErrTenantUserAccessSuspended      = errors.New("tenant user access suspended")
	ErrUserAccountDisabled            = errors.New("user account disabled")
	ErrUserSessionExpired             = errors.New("user session expired")
	ErrUserSessionRevoked             = errors.New("user session revoked")
	ErrUserRefreshReplayed            = errors.New("user refresh replayed")
	ErrUserConflict                   = errors.New("user conflict")
	ErrUserVersionConflict            = errors.New("user version conflict")
	ErrRecentAuthenticationNeeded     = errors.New("recent authentication required")
	ErrUserRateLimited                = errors.New("user authentication rate limited")
	ErrAdditionalVerificationRequired = errors.New("additional verification required")
	ErrRecoveryExpired                = errors.New("recovery challenge expired")
	ErrRecoveryInvalid                = errors.New("recovery proof invalid")
	ErrRecoveryReplayed               = errors.New("recovery proof replayed")
)

type UserRateLimitError struct{ RetryAfter time.Duration }

func (e *UserRateLimitError) Error() string { return ErrUserRateLimited.Error() }
func (e *UserRateLimitError) Unwrap() error { return ErrUserRateLimited }

const userRequestBodyLimit int64 = 32 << 10

type ClientSessionContext struct {
	ProductID     string
	ApplicationID string
	TenantID      string
}

type UserSessionContext struct {
	UserID        string
	SessionID     string
	ProductID     string
	ApplicationID string
	TenantID      string
	AccountStatus string
	AuthTime      time.Time
	AccessToken   string `json:"-"`
}

type UserSessionResolver interface {
	ResolveClientSession(context.Context, string) (ClientSessionContext, error)
	ResolveUserSession(context.Context, string) (UserSessionContext, error)
	// ResolveLogoutSession may return ErrUserSessionRevoked only after the
	// bearer proof was validated as a previously revoked access session.
	ResolveLogoutSession(context.Context, string) (UserSessionContext, error)
}

type UserService interface {
	RegisterUser(context.Context, ClientSessionContext, RegisterUserCommand) (IssuedUserSession, error)
	LoginUser(context.Context, ClientSessionContext, LoginUserCommand) (IssuedUserSession, error)
	CurrentUserSession(context.Context, UserSessionContext) (CurrentUserSession, error)
	GetCurrentUserAccess(context.Context, UserSessionContext) (ProductUserAccessDecision, error)
	StartPasswordRecovery(context.Context, ClientSessionContext, StartPasswordRecoveryCommand) (RecoveryChallengeResponse, error)
	CompletePasswordRecovery(context.Context, ClientSessionContext, CompletePasswordRecoveryCommand) error
	GetCurrentUserProfile(context.Context, UserSessionContext) (UserProfileResponse, error)
	UpdateCurrentUserProfile(context.Context, UserSessionContext, UpdateUserProfileCommand) (UserProfileResponse, error)
	ChangeCurrentUserPassword(context.Context, UserSessionContext, ChangePasswordCommand) error
	ListCurrentUserSessions(context.Context, UserSessionContext) ([]UserSessionSummary, error)
	RevokeCurrentUserSession(context.Context, UserSessionContext, string) error
	RefreshUserSession(context.Context, RefreshUserSessionCommand) (TokenPair, error)
	LogoutUser(context.Context, UserSessionContext) error
}

type RegisterUserCommand struct {
	Identifier        string
	Credential        string
	VerificationProof string
	DisplayName       string
	IdempotencyKey    string
	RequestID         string
}

type LoginUserCommand struct {
	Identifier        string
	Credential        string
	DeviceRiskSummary map[string]any
	Source            string
	RequestID         string
}

type StartPasswordRecoveryCommand struct {
	Identifier     string
	IdempotencyKey string
	RequestID      string
}

type CompletePasswordRecoveryCommand struct {
	ContinuationID string
	RecoveryProof  string
	NewCredential  string
	IdempotencyKey string
	RequestID      string
}

type OptionalString struct {
	Set   bool
	Value *string
}

func (o *OptionalString) UnmarshalJSON(raw []byte) error {
	o.Set = true
	if string(raw) == "null" {
		o.Value = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

type UpdateUserProfileCommand struct {
	ExpectedVersion int64
	DisplayName     OptionalString
	AvatarURL       OptionalString
	Locale          OptionalString
	Timezone        OptionalString
	IdempotencyKey  string
	RequestID       string
}

type ChangePasswordCommand struct {
	CurrentCredential   string
	NewCredential       string
	RevokeOtherSessions bool
	IdempotencyKey      string
	RequestID           string
}

type RefreshUserSessionCommand struct {
	RefreshToken    string
	ClientRequestID string
	RequestID       string
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

type TokenPair struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

type IssuedUserSession struct {
	TokenPair
	User UserSummary `json:"user"`
}

type CurrentUserSession struct {
	SessionID        string      `json:"session_id"`
	User             UserSummary `json:"user"`
	AccessExpiresAt  time.Time   `json:"access_expires_at"`
	RefreshExpiresAt time.Time   `json:"refresh_expires_at"`
}

type RecoveryChallengeResponse struct {
	Accepted       bool   `json:"accepted"`
	ContinuationID string `json:"continuation_id"`
}

type UserProfileResponse struct {
	UserID      string  `json:"user_id"`
	Version     int64   `json:"version"`
	DisplayName *string `json:"display_name"`
	AvatarURL   *string `json:"avatar_url"`
	Locale      *string `json:"locale"`
	Timezone    *string `json:"timezone"`
}

type UserSessionSummary struct {
	SessionID   string    `json:"session_id"`
	Current     bool      `json:"current"`
	DeviceLabel *string   `json:"device_label"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type ProductUserAccessDecision struct {
	Allowed       bool    `json:"allowed"`
	DecisionStage string  `json:"decision_stage"`
	ReasonCode    *string `json:"reason_code"`
}

type UserHandler struct {
	service  UserService
	resolver UserSessionResolver
}

func NewUserHandler(service UserService, resolver UserSessionResolver) *UserHandler {
	return &UserHandler{service: service, resolver: resolver}
}

func (h *UserHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if h == nil || h.service == nil || h.resolver == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "identity.unavailable", "identity service unavailable")
		return
	}
	if r.URL.RawPath != "" {
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	if r.URL.RawQuery != "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "query parameters are not allowed")
		return
	}
	switch {
	case r.URL.Path == "/api/v1/auth/register":
		h.register(w, r)
	case r.URL.Path == "/api/v1/auth/login":
		h.loginUser(w, r)
	case r.URL.Path == "/api/v1/auth/session":
		h.currentSession(w, r)
	case r.URL.Path == "/api/v1/account/access":
		h.currentAccess(w, r)
	case r.URL.Path == "/api/v1/auth/recovery/start":
		h.startRecovery(w, r)
	case r.URL.Path == "/api/v1/auth/recovery/complete":
		h.completeRecovery(w, r)
	case r.URL.Path == "/api/v1/account/profile":
		h.profile(w, r)
	case r.URL.Path == "/api/v1/account/password":
		h.password(w, r)
	case r.URL.Path == "/api/v1/account/sessions":
		h.sessions(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/account/sessions/"):
		h.revokeSession(w, r)
	case r.URL.Path == "/api/v1/auth/refresh":
		h.refreshUser(w, r)
	case r.URL.Path == "/api/v1/auth/logout":
		h.logoutUser(w, r)
	default:
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
	}
}

type registerUserRequest struct {
	Identifier        string `json:"identifier"`
	Credential        string `json:"credential"`
	VerificationProof string `json:"verification_proof"`
	DisplayName       string `json:"display_name"`
}

func (h *UserHandler) register(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	client, ok := h.requireClient(w, r)
	if !ok {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body registerUserRequest
	if !decodeUserJSON(w, r, &body) {
		return
	}
	if !bounded(body.Identifier, 1, 320) || !bounded(body.Credential, 12, 1024) || !bounded(body.VerificationProof, 16, 1024) || !bounded(body.DisplayName, 0, 128) {
		invalidRequest(w, r)
		return
	}
	value, err := h.service.RegisterUser(r.Context(), client, RegisterUserCommand{Identifier: body.Identifier, Credential: body.Credential, VerificationProof: body.VerificationProof, DisplayName: body.DisplayName, IdempotencyKey: key, RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	writeUserJSON(w, http.StatusCreated, value)
}

type loginUserRequest struct {
	Identifier        string         `json:"identifier"`
	Credential        string         `json:"credential"`
	DeviceRiskSummary map[string]any `json:"device_risk_summary"`
}

func (h *UserHandler) loginUser(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	client, ok := h.requireClient(w, r)
	if !ok {
		return
	}
	var body loginUserRequest
	if !decodeUserJSON(w, r, &body) {
		return
	}
	if !bounded(body.Identifier, 1, 320) || !bounded(body.Credential, 1, 1024) {
		invalidRequest(w, r)
		return
	}
	value, err := h.service.LoginUser(r.Context(), client, LoginUserCommand{Identifier: body.Identifier, Credential: body.Credential, DeviceRiskSummary: body.DeviceRiskSummary, Source: remoteSource(r.RemoteAddr), RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	writeUserJSON(w, http.StatusOK, value)
}

func remoteSource(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}

func (h *UserHandler) currentSession(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) || !requireEmptyBody(w, r) {
		return
	}
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	value, err := h.service.CurrentUserSession(r.Context(), user)
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	writeUserJSON(w, http.StatusOK, value)
}

func (h *UserHandler) currentAccess(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) || !requireEmptyBody(w, r) {
		return
	}
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	value, err := h.service.GetCurrentUserAccess(r.Context(), user)
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	if !validAccessDecision(value) {
		h.writeUserError(w, r, errors.New("invalid account access decision"))
		return
	}
	writeUserJSON(w, http.StatusOK, value)
}

func validAccessDecision(value ProductUserAccessDecision) bool {
	if value.DecisionStage == "allowed" {
		return value.Allowed && value.ReasonCode == nil
	}
	if value.Allowed || value.ReasonCode == nil {
		return false
	}
	want := map[string]map[string]bool{
		"identity": {"IDENTITY_ACCOUNT_DISABLED": true},
		"product":  {"PRODUCT_USER_ACCESS_SUSPENDED": true},
		"tenant":   {"TENANT_USER_ACCESS_SUSPENDED": true},
		"entitlement": {
			"ENTITLEMENT_REQUIRED": true,
			"ENTITLEMENT_EXPIRED":  true,
		},
	}
	return want[value.DecisionStage][*value.ReasonCode]
}

type startRecoveryRequest struct {
	Identifier string `json:"identifier"`
}

func (h *UserHandler) startRecovery(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	client, ok := h.requireClient(w, r)
	if !ok {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body startRecoveryRequest
	if !decodeUserJSON(w, r, &body) {
		return
	}
	if !bounded(body.Identifier, 1, 320) {
		invalidRequest(w, r)
		return
	}
	value, err := h.service.StartPasswordRecovery(r.Context(), client, StartPasswordRecoveryCommand{Identifier: body.Identifier, IdempotencyKey: key, RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	value.Accepted = true
	writeUserJSON(w, http.StatusAccepted, value)
}

type completeRecoveryRequest struct {
	ContinuationID string `json:"continuation_id"`
	RecoveryProof  string `json:"recovery_proof"`
	NewCredential  string `json:"new_credential"`
}

func (h *UserHandler) completeRecovery(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	client, ok := h.requireClient(w, r)
	if !ok {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body completeRecoveryRequest
	if !decodeUserJSON(w, r, &body) {
		return
	}
	if !identifier(body.ContinuationID) || !bounded(body.RecoveryProof, 16, 1024) || !bounded(body.NewCredential, 12, 1024) {
		invalidRequest(w, r)
		return
	}
	err := h.service.CompletePasswordRecovery(r.Context(), client, CompletePasswordRecoveryCommand{ContinuationID: body.ContinuationID, RecoveryProof: body.RecoveryProof, NewCredential: body.NewCredential, IdempotencyKey: key, RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type updateProfileRequest struct {
	ExpectedVersion int64          `json:"expected_version"`
	DisplayName     OptionalString `json:"display_name"`
	AvatarURL       OptionalString `json:"avatar_url"`
	Locale          OptionalString `json:"locale"`
	Timezone        OptionalString `json:"timezone"`
}

func (h *UserHandler) profile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		user, ok := h.requireUser(w, r)
		if !ok {
			return
		}
		if !requireEmptyBody(w, r) {
			return
		}
		value, err := h.service.GetCurrentUserProfile(r.Context(), user)
		if err != nil {
			h.writeUserError(w, r, err)
			return
		}
		writeUserJSON(w, http.StatusOK, value)
	case http.MethodPatch:
		user, ok := h.requireUser(w, r)
		if !ok {
			return
		}
		key, ok := requireIdempotencyKey(w, r)
		if !ok {
			return
		}
		var body updateProfileRequest
		if !decodeUserJSON(w, r, &body) {
			return
		}
		if body.ExpectedVersion < 1 || (!body.DisplayName.Set && !body.AvatarURL.Set && !body.Locale.Set && !body.Timezone.Set) || !validOptional(body.DisplayName, 128) || !validOptional(body.Locale, 32) || !validOptional(body.Timezone, 64) || !validAvatar(body.AvatarURL) {
			invalidRequest(w, r)
			return
		}
		value, err := h.service.UpdateCurrentUserProfile(r.Context(), user, UpdateUserProfileCommand{ExpectedVersion: body.ExpectedVersion, DisplayName: body.DisplayName, AvatarURL: body.AvatarURL, Locale: body.Locale, Timezone: body.Timezone, IdempotencyKey: key, RequestID: requestid.FromContext(r.Context())})
		if err != nil {
			h.writeUserError(w, r, err)
			return
		}
		writeUserJSON(w, http.StatusOK, value)
	default:
		httpx.MethodNotAllowed(w, r, "GET, PATCH")
	}
}

type changePasswordRequest struct {
	CurrentCredential   string `json:"current_credential"`
	NewCredential       string `json:"new_credential"`
	RevokeOtherSessions *bool  `json:"revoke_other_sessions"`
}

func (h *UserHandler) password(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPut) {
		return
	}
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body changePasswordRequest
	if !decodeUserJSON(w, r, &body) {
		return
	}
	if !bounded(body.CurrentCredential, 1, 1024) || !bounded(body.NewCredential, 12, 1024) || body.RevokeOtherSessions == nil {
		invalidRequest(w, r)
		return
	}
	err := h.service.ChangeCurrentUserPassword(r.Context(), user, ChangePasswordCommand{CurrentCredential: body.CurrentCredential, NewCredential: body.NewCredential, RevokeOtherSessions: *body.RevokeOtherSessions, IdempotencyKey: key, RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *UserHandler) sessions(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) || !requireEmptyBody(w, r) {
		return
	}
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListCurrentUserSessions(r.Context(), user)
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	if items == nil {
		items = []UserSessionSummary{}
	}
	writeUserJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *UserHandler) revokeSession(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodDelete) || !requireEmptyBody(w, r) {
		return
	}
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/v1/account/sessions/")
	if !identifier(sessionID) || strings.Contains(sessionID, "/") {
		invalidRequest(w, r)
		return
	}
	if err := h.service.RevokeCurrentUserSession(r.Context(), user, sessionID); err != nil {
		h.writeUserError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type refreshUserRequest struct {
	RefreshToken    string `json:"refresh_token"`
	ClientRequestID string `json:"client_request_id"`
}

func (h *UserHandler) refreshUser(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	var body refreshUserRequest
	if !decodeUserJSON(w, r, &body) {
		return
	}
	if !bounded(body.RefreshToken, 16, 4096) || !opaqueKey(body.ClientRequestID, 16, 128) {
		invalidRequest(w, r)
		return
	}
	value, err := h.service.RefreshUserSession(r.Context(), RefreshUserSessionCommand{RefreshToken: body.RefreshToken, ClientRequestID: body.ClientRequestID, RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	writeUserJSON(w, http.StatusOK, value)
}

func (h *UserHandler) logoutUser(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) || !requireEmptyBody(w, r) {
		return
	}
	token, ok := strictBearer(r)
	if !ok {
		unauthorized(w, r)
		return
	}
	user, err := h.resolver.ResolveLogoutSession(r.Context(), token)
	if errors.Is(err, ErrUserSessionRevoked) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		if errors.Is(err, ErrInvalidBearer) {
			unauthorized(w, r)
		} else {
			h.writeUserError(w, r, err)
		}
		return
	}
	if user.UserID == "" || user.SessionID == "" || user.ProductID == "" || user.ApplicationID == "" {
		unauthorized(w, r)
		return
	}
	user.AccessToken = token
	if err := h.service.LogoutUser(r.Context(), user); err != nil && !errors.Is(err, ErrUserSessionRevoked) {
		h.writeUserError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *UserHandler) requireClient(w http.ResponseWriter, r *http.Request) (ClientSessionContext, bool) {
	token, ok := strictBearer(r)
	if !ok {
		unauthorized(w, r)
		return ClientSessionContext{}, false
	}
	value, err := h.resolver.ResolveClientSession(r.Context(), token)
	if err != nil {
		if errors.Is(err, ErrInvalidBearer) {
			unauthorized(w, r)
		} else {
			h.writeUserError(w, r, err)
		}
		return ClientSessionContext{}, false
	}
	if value.ProductID == "" || value.ApplicationID == "" {
		unauthorized(w, r)
		return ClientSessionContext{}, false
	}
	return value, true
}
func (h *UserHandler) requireUser(w http.ResponseWriter, r *http.Request) (UserSessionContext, bool) {
	token, ok := strictBearer(r)
	if !ok {
		unauthorized(w, r)
		return UserSessionContext{}, false
	}
	value, err := h.resolver.ResolveUserSession(r.Context(), token)
	if err != nil {
		if errors.Is(err, ErrInvalidBearer) {
			unauthorized(w, r)
		} else {
			h.writeUserError(w, r, err)
		}
		return UserSessionContext{}, false
	}
	if value.UserID == "" || value.SessionID == "" || value.ProductID == "" || value.ApplicationID == "" {
		unauthorized(w, r)
		return UserSessionContext{}, false
	}
	value.AccessToken = token
	return value, true
}

func (h *UserHandler) writeUserError(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	status, code, detail, retryable := http.StatusInternalServerError, "identity.internal_error", "identity service unavailable", true
	options := httpx.ErrorOptions{}
	switch {
	case errors.Is(err, ErrInvalidUserRequest):
		status, code, detail, retryable = http.StatusBadRequest, "invalid_request", "invalid request", false
	case errors.Is(err, ErrUserProviderUnavailable):
		status, code, detail, retryable = http.StatusServiceUnavailable, "identity.provider_unavailable", "identity provider unavailable", true
	case errors.Is(err, ErrProductUserAccessSuspended):
		status, code, detail, retryable = http.StatusForbidden, "PRODUCT_USER_ACCESS_SUSPENDED", "product access is suspended", false
	case errors.Is(err, ErrTenantUserAccessSuspended):
		status, code, detail, retryable = http.StatusForbidden, "TENANT_USER_ACCESS_SUSPENDED", "tenant access is suspended", false
	case errors.Is(err, ErrUserRateLimited):
		status, code, detail, retryable = http.StatusTooManyRequests, "identity.rate_limited", "authentication temporarily unavailable", true
		var rateLimit *UserRateLimitError
		if errors.As(err, &rateLimit) && rateLimit.RetryAfter > 0 {
			seconds := int((rateLimit.RetryAfter + time.Second - 1) / time.Second)
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			options.RetryAfterSeconds = &seconds
		}
	case errors.Is(err, ErrAdditionalVerificationRequired):
		status, code, detail, retryable = http.StatusForbidden, "identity.additional_verification_required", "additional verification required", false
	case errors.Is(err, ErrRecoveryExpired):
		status, code, detail, retryable = http.StatusGone, "identity.recovery_expired", "recovery challenge expired", false
	case errors.Is(err, ErrRecoveryInvalid):
		status, code, detail, retryable = http.StatusBadRequest, "identity.recovery_invalid", "recovery proof is invalid", false
	case errors.Is(err, ErrRecoveryReplayed):
		status, code, detail, retryable = http.StatusConflict, "identity.recovery_replayed", "recovery proof was already used", false
	case errors.Is(err, ErrInvalidUserCredentials):
		status, code, detail, retryable = http.StatusUnauthorized, "IDENTITY_INVALID_CREDENTIALS", "authentication failed", false
	case errors.Is(err, ErrUserAccountDisabled):
		status, code, detail, retryable = http.StatusForbidden, "IDENTITY_ACCOUNT_DISABLED", "account is unavailable", false
	case errors.Is(err, ErrUserSessionExpired):
		status, code, detail, retryable = http.StatusUnauthorized, "IDENTITY_SESSION_EXPIRED", "session expired", false
	case errors.Is(err, ErrUserSessionRevoked):
		status, code, detail, retryable = http.StatusUnauthorized, "IDENTITY_SESSION_REVOKED", "session revoked", false
	case errors.Is(err, ErrUserRefreshReplayed):
		status, code, detail, retryable = http.StatusUnauthorized, "IDENTITY_REFRESH_REPLAYED", "session revoked", false
	case errors.Is(err, ErrUserConflict):
		status, code, detail, retryable = http.StatusConflict, "IDENTITY_CONFLICT", "request conflicts with current state", false
	case errors.Is(err, ErrUserVersionConflict):
		status, code, detail, retryable = http.StatusConflict, "IDENTITY_VERSION_CONFLICT", "resource version changed", false
	case errors.Is(err, ErrRecentAuthenticationNeeded):
		status, code, detail, retryable = http.StatusForbidden, "IDENTITY_RECENT_AUTH_REQUIRED", "recent authentication required", false
	}
	options.Retryable = retryable
	httpx.ErrorWithOptions(w, r, status, code, detail, options)
}

func decodeUserJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		invalidRequest(w, r)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, userRequestBodyLimit)
	raw, err := io.ReadAll(r.Body)
	if err != nil || len(raw) == 0 || !validJSONShape(raw) {
		invalidRequest(w, r)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		invalidRequest(w, r)
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		invalidRequest(w, r)
		return false
	}
	return true
}

func validJSONShape(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if !consumeUniqueJSONValue(decoder) {
		return false
	}
	_, err := decoder.Token()
	return errors.Is(err, io.EOF)
}

func consumeUniqueJSONValue(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return true
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return false
			}
			if _, duplicate := seen[key]; duplicate {
				return false
			}
			seen[key] = struct{}{}
			if !consumeUniqueJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !consumeUniqueJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}
func requireEmptyBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1)
	var one [1]byte
	n, _ := r.Body.Read(one[:])
	if n != 0 {
		invalidRequest(w, r)
		return false
	}
	return true
}
func strictBearer(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	value := values[0]
	if !strings.HasPrefix(strings.ToLower(value), "bearer ") || len(value) < 8 || value[7] == ' ' {
		return "", false
	}
	token := value[7:]
	if !bounded(token, 16, 4096) || strings.TrimSpace(token) != token || strings.ContainsAny(token, " ,\t\r\n") {
		return "", false
	}
	return token, true
}
func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || !opaqueKey(values[0], 16, 128) {
		invalidRequest(w, r)
		return "", false
	}
	return values[0], true
}
func opaqueKey(value string, min, max int) bool {
	if !bounded(value, min, max) {
		return false
	}
	for _, b := range []byte(value) {
		if b < 0x21 || b > 0x7e || b == ',' {
			return false
		}
	}
	return true
}
func bounded(value string, min, max int) bool {
	return utf8.ValidString(value) && len(value) >= min && len(value) <= max
}
func identifier(value string) bool {
	return opaqueKey(value, 1, 160) && !strings.ContainsAny(value, "/?#")
}
func validOptional(value OptionalString, max int) bool {
	return !value.Set || value.Value == nil || bounded(*value.Value, 0, max)
}
func validAvatar(value OptionalString) bool {
	if !value.Set || value.Value == nil {
		return true
	}
	if !bounded(*value.Value, 1, 512) {
		return false
	}
	parsed, err := url.ParseRequestURI(*value.Value)
	return err == nil && parsed.IsAbs() && (parsed.Scheme == "https" || parsed.Scheme == "http")
}
func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method != want {
		httpx.MethodNotAllowed(w, r, want)
		return false
	}
	return true
}
func invalidRequest(w http.ResponseWriter, r *http.Request) {
	httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid request")
}
func unauthorized(w http.ResponseWriter, r *http.Request) {
	httpx.ErrorWithOptions(w, r, http.StatusUnauthorized, "identity.unauthorized", "authentication required", httpx.ErrorOptions{Retryable: false})
}
func writeUserJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
