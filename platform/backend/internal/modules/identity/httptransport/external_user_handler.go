package httptransport

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

var (
	ErrExternalFlowInvalid          = errors.New("external authentication flow invalid")
	ErrExternalFlowExpired          = errors.New("external authentication flow expired")
	ErrExternalFlowReplayed         = errors.New("external authentication flow replayed")
	ErrExternalIdentityConflict     = errors.New("external identity conflict")
	ErrExternalIdentityNotOwned     = errors.New("external identity not owned")
	ErrExternalIdentityLastLogin    = errors.New("external identity is the last login method")
	ErrRegistrationVerificationFail = errors.New("registration verification failed")
)

var externalStableCode = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

type ExternalUserService interface {
	StartRegistrationVerification(context.Context, ClientSessionContext, StartRegistrationVerificationCommand) (VerificationChallengeResponse, error)
	StartExternalLogin(context.Context, ClientSessionContext, string, StartExternalLoginCommand) (ExternalLoginFlowResponse, error)
	CompleteExternalLogin(context.Context, ClientSessionContext, string, CompleteExternalLoginCommand) (ExternalExchangeResponse, error)
	LinkExternalIdentity(context.Context, UserSessionContext, string, LinkExternalIdentityCommand) (ExternalIdentityResponse, error)
	ListExternalIdentities(context.Context, UserSessionContext) ([]ExternalIdentityResponse, error)
	UnlinkExternalIdentity(context.Context, UserSessionContext, string) error
}

type StartRegistrationVerificationCommand struct {
	Identifier     string
	IdempotencyKey string
	RequestID      string
}

type VerificationChallengeResponse struct {
	Accepted       bool   `json:"accepted"`
	ContinuationID string `json:"continuation_id"`
}

type StartExternalLoginCommand struct {
	Mode             string
	ReturnTargetCode string
	RequestID        string
}

type ExternalLoginFlowResponse struct {
	FlowID           string    `json:"flow_id"`
	Mode             string    `json:"mode"`
	AuthorizationURL string    `json:"authorization_url,omitempty"`
	QRPayload        string    `json:"qr_payload,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type CompleteExternalLoginCommand struct {
	FlowID        string
	State         string
	Code          string
	ProviderError string
	RequestID     string
}

type ExternalExchangeResponse struct {
	Status  string             `json:"status"`
	Session *IssuedUserSession `json:"session,omitempty"`
	ProofID string             `json:"proof_id,omitempty"`
}

type LinkExternalIdentityCommand struct {
	ExternalProofID string
	IdempotencyKey  string
	RequestID       string
}

type ExternalIdentityResponse struct {
	ExternalIdentityID string    `json:"external_identity_id"`
	Provider           string    `json:"provider"`
	MaskedSubject      string    `json:"masked_subject,omitempty"`
	Status             string    `json:"status"`
	LinkedAt           time.Time `json:"linked_at"`
	AuditID            string    `json:"audit_id,omitempty"`
}

func (h *UserHandler) startRegistrationVerification(w http.ResponseWriter, r *http.Request) {
	if h.external == nil {
		h.writeUserError(w, r, ErrUserProviderUnavailable)
		return
	}
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
	var body struct {
		Identifier string `json:"identifier"`
	}
	if !decodeUserJSON(w, r, &body) {
		return
	}
	if !bounded(body.Identifier, 1, 320) {
		invalidRequest(w, r)
		return
	}
	value, err := h.external.StartRegistrationVerification(r.Context(), client, StartRegistrationVerificationCommand{Identifier: body.Identifier, IdempotencyKey: key, RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	value.Accepted = true
	writeUserJSON(w, http.StatusAccepted, value)
}

func (h *UserHandler) externalAuthentication(w http.ResponseWriter, r *http.Request) {
	if h.external == nil {
		h.writeUserError(w, r, ErrUserProviderUnavailable)
		return
	}
	parts := splitExternalPath(r.URL.Path, "/api/v1/auth/external/")
	if len(parts) != 2 || !validExternalProvider(parts[0]) {
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	provider, action := parts[0], parts[1]
	switch action {
	case "start":
		h.startExternalLogin(w, r, provider)
	case "callback":
		h.completeExternalLogin(w, r, provider, false)
	case "exchange":
		if provider != "wechat" {
			httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
			return
		}
		h.completeExternalLogin(w, r, provider, true)
	default:
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
	}
}

func (h *UserHandler) startExternalLogin(w http.ResponseWriter, r *http.Request, provider string) {
	if !method(w, r, http.MethodPost) {
		return
	}
	client, ok := h.requireClient(w, r)
	if !ok {
		return
	}
	if client.SessionID == "" || client.Environment == "" {
		unauthorized(w, r)
		return
	}
	var body struct {
		Mode             string `json:"mode"`
		ReturnTargetCode string `json:"return_target_code"`
	}
	if !decodeUserJSON(w, r, &body) {
		return
	}
	if (body.Mode != "redirect" && body.Mode != "qr" && body.Mode != "native") || !externalStableCode.MatchString(body.ReturnTargetCode) {
		invalidRequest(w, r)
		return
	}
	value, err := h.external.StartExternalLogin(r.Context(), client, provider, StartExternalLoginCommand{Mode: body.Mode, ReturnTargetCode: body.ReturnTargetCode, RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	writeUserJSON(w, http.StatusCreated, value)
}

func (h *UserHandler) completeExternalLogin(w http.ResponseWriter, r *http.Request, provider string, legacyWechat bool) {
	if !method(w, r, http.MethodPost) {
		return
	}
	client, ok := h.requireClient(w, r)
	if !ok {
		return
	}
	if client.SessionID == "" || client.Environment == "" {
		unauthorized(w, r)
		return
	}
	var body struct {
		FlowID        string `json:"flow_id"`
		State         string `json:"state"`
		Code          string `json:"code"`
		ProviderError string `json:"provider_error"`
	}
	if !decodeUserJSON(w, r, &body) {
		return
	}
	validOutcome := bounded(body.Code, 1, 4096) && body.ProviderError == ""
	if !legacyWechat {
		validOutcome = validOutcome || (body.Code == "" && externalStableCode.MatchString(body.ProviderError))
	}
	if !identifier(body.FlowID) || !bounded(body.State, 32, 1024) || !validOutcome {
		invalidRequest(w, r)
		return
	}
	value, err := h.external.CompleteExternalLogin(r.Context(), client, provider, CompleteExternalLoginCommand{FlowID: body.FlowID, State: body.State, Code: body.Code, ProviderError: body.ProviderError, RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	writeUserJSON(w, http.StatusOK, value)
}

func (h *UserHandler) externalIdentities(w http.ResponseWriter, r *http.Request) {
	if h.external == nil {
		h.writeUserError(w, r, ErrUserProviderUnavailable)
		return
	}
	if !method(w, r, http.MethodGet) || !requireEmptyBody(w, r) {
		return
	}
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	items, err := h.external.ListExternalIdentities(r.Context(), user)
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	writeUserJSON(w, http.StatusOK, struct {
		Items []ExternalIdentityResponse `json:"items"`
	}{Items: items})
}

func (h *UserHandler) externalIdentityMutation(w http.ResponseWriter, r *http.Request) {
	if h.external == nil {
		h.writeUserError(w, r, ErrUserProviderUnavailable)
		return
	}
	parts := splitExternalPath(r.URL.Path, "/api/v1/account/external-identities/")
	if len(parts) == 2 && parts[1] == "link" && validExternalProvider(parts[0]) {
		h.linkExternalIdentity(w, r, parts[0])
		return
	}
	if len(parts) == 1 && identifier(parts[0]) {
		h.unlinkExternalIdentity(w, r, parts[0])
		return
	}
	httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
}

func (h *UserHandler) linkExternalIdentity(w http.ResponseWriter, r *http.Request, provider string) {
	if !method(w, r, http.MethodPost) {
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
	var body struct {
		ExternalProofID string `json:"external_proof_id"`
	}
	if !decodeUserJSON(w, r, &body) {
		return
	}
	if !bounded(body.ExternalProofID, 16, 1024) {
		invalidRequest(w, r)
		return
	}
	value, err := h.external.LinkExternalIdentity(r.Context(), user, provider, LinkExternalIdentityCommand{ExternalProofID: body.ExternalProofID, IdempotencyKey: key, RequestID: requestid.FromContext(r.Context())})
	if err != nil {
		h.writeUserError(w, r, err)
		return
	}
	writeUserJSON(w, http.StatusOK, value)
}

func (h *UserHandler) unlinkExternalIdentity(w http.ResponseWriter, r *http.Request, externalIdentityID string) {
	if !method(w, r, http.MethodDelete) || !requireEmptyBody(w, r) {
		return
	}
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if err := h.external.UnlinkExternalIdentity(r.Context(), user, externalIdentityID); err != nil {
		h.writeUserError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func splitExternalPath(path, prefix string) []string {
	if !strings.HasPrefix(path, prefix) {
		return nil
	}
	remainder := strings.TrimPrefix(path, prefix)
	if remainder == "" || strings.HasSuffix(remainder, "/") {
		return nil
	}
	return strings.Split(remainder, "/")
}

func validExternalProvider(value string) bool {
	return value == "oidc" || value == "wechat" || value == "other"
}
