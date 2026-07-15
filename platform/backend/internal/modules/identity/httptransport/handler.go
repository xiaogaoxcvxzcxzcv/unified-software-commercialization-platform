package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/identity"
	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

const (
	accessCookieName  = "__Host-platform_admin_access"
	refreshCookieName = "__Secure-platform_admin_refresh"
	csrfHeader        = "X-CSRF-Token"
)

type Service interface {
	LoginAdmin(context.Context, identity.LoginCommand) (identity.AdminSession, error)
	CurrentAdminSession(context.Context, string) (identity.AdminSession, error)
	RefreshAdminSessionWithClient(context.Context, identity.RefreshCommand) (identity.AdminSession, error)
	LogoutAdmin(context.Context, identity.LogoutCommand) error
}

type Config struct {
	AllowedOrigins []string
}

type Handler struct {
	service Service
	origins map[string]struct{}
}

func New(service Service, cfg Config) *Handler {
	origins := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, origin := range cfg.AllowedOrigins {
		origins[origin] = struct{}{}
	}
	return &Handler{service: service, origins: origins}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		h.preflight(w, r)
		return
	}
	switch r.URL.Path {
	case "/api/v1/admin/auth/login":
		h.login(w, r)
	case "/api/v1/admin/auth/session":
		h.session(w, r)
	case "/api/v1/admin/auth/refresh":
		h.refresh(w, r)
	case "/api/v1/admin/auth/logout":
		h.logout(w, r)
	default:
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
	}
}

type loginRequest struct {
	Identifier       string                   `json:"identifier"`
	Credential       string                   `json:"credential"`
	Transport        identity.Transport       `json:"transport"`
	ControlledClient *controlledClientRequest `json:"controlled_client"`
	RiskSummary      map[string]any           `json:"risk_summary"`
}

type controlledClientRequest struct {
	ClientID     string `json:"client_id"`
	CredentialID string `json:"credential_id"`
	ProofType    string `json:"proof_type"`
	Proof        string `json:"proof"`
}

func (c *controlledClientRequest) domain() *identity.ControlledClientProof {
	if c == nil {
		return nil
	}
	return &identity.ControlledClientProof{ClientID: c.ClientID, CredentialID: c.CredentialID, ProofType: c.ProofType, Secret: c.Proof}
}

func (c *controlledClientRequest) valid() bool {
	return c != nil && strings.TrimSpace(c.ClientID) != "" && strings.TrimSpace(c.CredentialID) != "" && c.ProofType == "shared_secret_v1" && len(c.Proof) >= 43 && len(c.Proof) <= 512
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.MethodNotAllowed(w, r, http.MethodPost)
		return
	}
	var body loginRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Identifier == "" || body.Credential == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "identifier and credential are required")
		return
	}
	if body.Transport == "" {
		body.Transport = identity.TransportCookie
	}
	if (body.Transport == identity.TransportBearer && !body.ControlledClient.valid()) ||
		(body.Transport == identity.TransportCookie && body.ControlledClient != nil) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "controlled client proof does not match the requested transport")
		return
	}
	if body.Transport == identity.TransportCookie && !h.requireOrigin(w, r) {
		return
	}
	session, err := h.service.LoginAdmin(r.Context(), identity.LoginCommand{Identifier: body.Identifier, Credential: body.Credential, Requested: body.Transport, ControlledClient: body.ControlledClient.domain(), Source: requestSource(r), RiskSummary: body.RiskSummary, TraceID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.applyCORS(w, r)
	setSessionCookies(w, session)
	httpx.JSON(w, http.StatusOK, session)
}

func (h *Handler) session(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.MethodNotAllowed(w, r, http.MethodGet)
		return
	}
	token, _, ok := accessProof(r)
	if !ok {
		h.writeError(w, r, identity.ErrSessionExpired)
		return
	}
	session, err := h.service.CurrentAdminSession(r.Context(), token)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.applyCORS(w, r)
	httpx.JSON(w, http.StatusOK, session)
}

type refreshRequest struct {
	Transport        identity.Transport       `json:"transport"`
	RefreshToken     string                   `json:"refresh_token"`
	ControlledClient *controlledClientRequest `json:"controlled_client"`
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.MethodNotAllowed(w, r, http.MethodPost)
		return
	}
	var token string
	var controlledClient *identity.ControlledClientProof
	transport := identity.TransportCookie
	if cookie, err := r.Cookie(refreshCookieName); err == nil && cookie.Value != "" {
		if !h.requireOrigin(w, r) {
			return
		}
		if r.Body != nil && r.ContentLength != 0 {
			var body refreshRequest
			if !decodeJSON(w, r, &body) {
				return
			}
			if body.RefreshToken != "" || body.Transport == identity.TransportBearer || body.ControlledClient != nil {
				httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "cookie refresh rejects body token material")
				return
			}
		}
		token = cookie.Value
	} else {
		body := refreshRequest{Transport: identity.TransportCookie}
		if r.Body != nil && r.ContentLength != 0 {
			if !decodeJSON(w, r, &body) {
				return
			}
			if body.Transport == "" {
				body.Transport = identity.TransportCookie
			}
		}
		if body.Transport == identity.TransportCookie {
			if !h.requireOrigin(w, r) {
				return
			}
			if body.RefreshToken != "" || body.ControlledClient != nil {
				httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "cookie refresh rejects body token material")
				return
			}
			clearSessionCookies(w)
			h.writeError(w, r, identity.ErrSessionExpired)
			return
		}
		if body.Transport != identity.TransportBearer || body.RefreshToken == "" || !body.ControlledClient.valid() {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "bearer refresh token is required")
			return
		}
		transport = identity.TransportBearer
		token = body.RefreshToken
		controlledClient = body.ControlledClient.domain()
	}
	session, err := h.service.RefreshAdminSessionWithClient(r.Context(), identity.RefreshCommand{RefreshToken: token, Transport: transport, ControlledClient: controlledClient, TraceID: requestid.FromContext(r.Context())})
	if err != nil {
		if isTerminalSessionError(err) {
			clearSessionCookies(w)
		}
		h.writeError(w, r, err)
		return
	}
	h.applyCORS(w, r)
	setSessionCookies(w, session)
	httpx.JSON(w, http.StatusOK, session)
}

func isTerminalSessionError(err error) bool {
	return errors.Is(err, identity.ErrSessionExpired) ||
		errors.Is(err, identity.ErrSessionRevoked) ||
		errors.Is(err, identity.ErrRefreshReplayed)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.MethodNotAllowed(w, r, http.MethodPost)
		return
	}
	command := identity.LogoutCommand{TraceID: requestid.FromContext(r.Context())}
	if cookie, err := r.Cookie(accessCookieName); err == nil && cookie.Value != "" {
		command.AccessToken = cookie.Value
	}
	if cookie, err := r.Cookie(refreshCookieName); err == nil && cookie.Value != "" {
		command.RefreshToken = cookie.Value
	}
	if command.AccessToken != "" || command.RefreshToken != "" {
		if !h.requireOrigin(w, r) {
			return
		}
		command.Transport = identity.TransportCookie
		command.CSRFToken = r.Header.Get(csrfHeader)
	} else if value, ok := bearerProof(r); ok {
		command.Transport = identity.TransportBearer
		command.AccessToken = value
	}
	if command.AccessToken != "" || command.RefreshToken != "" {
		if err := h.service.LogoutAdmin(r.Context(), command); err != nil {
			if isTerminalSessionError(err) {
				clearSessionCookies(w)
			}
			h.writeError(w, r, err)
			return
		}
	}
	clearSessionCookies(w)
	h.applyCORS(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) requireOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if _, ok := h.origins[origin]; !ok {
		httpx.Error(w, r, http.StatusForbidden, "admin_auth.origin_denied", "origin denied")
		return false
	}
	h.applyCORS(w, r)
	return true
}
func (h *Handler) applyCORS(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" {
		if _, ok := h.origins[origin]; ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}
	}
}
func (h *Handler) preflight(w http.ResponseWriter, r *http.Request) {
	if !h.requireOrigin(w, r) {
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRF-Token, X-Request-ID")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	detail := "internal server error"
	options := httpx.ErrorOptions{Retryable: true}
	switch {
	case errors.Is(err, identity.ErrInvalidCredentials), errors.Is(err, identity.ErrBearerNotAllowed):
		status, code, detail = http.StatusUnauthorized, "admin_auth.invalid_credentials", "administrator authentication failed"
		options.Retryable = false
	case errors.Is(err, identity.ErrRateLimited):
		status, code, detail = http.StatusTooManyRequests, "admin_auth.rate_limited", "administrator authentication temporarily unavailable"
		var rateLimit *identity.RateLimitError
		if errors.As(err, &rateLimit) {
			seconds := int((rateLimit.RetryAfter + time.Second - 1) / time.Second)
			if seconds < 0 {
				seconds = 0
			}
			options.RetryAfterSeconds = &seconds
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
	case errors.Is(err, identity.ErrSessionExpired):
		status, code, detail = http.StatusUnauthorized, "admin_auth.session_expired", "administrator session expired"
		options.Retryable = false
	case errors.Is(err, identity.ErrSessionRevoked):
		status, code, detail = http.StatusUnauthorized, "admin_auth.session_revoked", "administrator session revoked"
		options.Retryable = false
	case errors.Is(err, identity.ErrRefreshReplayed):
		status, code, detail = http.StatusUnauthorized, "admin_auth.refresh_replayed", "administrator session revoked"
		options.Retryable = false
	case errors.Is(err, identity.ErrCSRFFailed):
		status, code, detail = http.StatusForbidden, "admin_auth.csrf_failed", "request verification failed"
		options.Retryable = false
	}
	httpx.ErrorWithOptions(w, r, status, code, detail, options)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid request body")
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "request body must contain one JSON value")
		return false
	}
	return true
}
func requestSource(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
func accessProof(r *http.Request) (string, bool, bool) {
	if cookie, err := r.Cookie(accessCookieName); err == nil && cookie.Value != "" {
		return cookie.Value, true, true
	}
	if token, ok := bearerProof(r); ok {
		return token, false, true
	}
	return "", false, false
}

// AdminAccessToken extracts only the opaque administrator access proof. The
// caller must still resolve the server-side session and authorize its target.
func AdminAccessToken(r *http.Request) (string, bool) {
	token, _, ok := accessProof(r)
	return token, ok
}

// AdminAccessProof identifies the administrator transport without exposing
// cookie names to business handlers. The opaque proof must still be resolved
// through Identity before it is trusted.
func AdminAccessProof(r *http.Request) (string, identity.Transport, bool) {
	token, cookie, ok := accessProof(r)
	if !ok {
		return "", "", false
	}
	if cookie {
		return token, identity.TransportCookie, true
	}
	return token, identity.TransportBearer, true
}

func bearerProof(r *http.Request) (string, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	returnValue := ""
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && parts[1] != "" {
		returnValue = parts[1]
		return returnValue, true
	}
	return "", false
}
func setSessionCookies(w http.ResponseWriter, session identity.AdminSession) {
	if session.Transport != identity.TransportCookie || session.CookieTokens == nil {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: accessCookieName, Value: session.CookieTokens.AccessToken, Path: "/", Expires: session.CookieTokens.AccessExpiresAt, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: refreshCookieName, Value: session.CookieTokens.RefreshToken, Path: "/api/v1/admin/auth", Expires: session.CookieTokens.RefreshExpiresAt, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}
func clearSessionCookies(w http.ResponseWriter) {
	expired := time.Unix(1, 0)
	http.SetCookie(w, &http.Cookie{Name: accessCookieName, Path: "/", Expires: expired, MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: refreshCookieName, Path: "/api/v1/admin/auth", Expires: expired, MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}
