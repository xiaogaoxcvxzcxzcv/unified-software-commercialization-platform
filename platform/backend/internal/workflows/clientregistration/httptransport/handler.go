package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
	"platform.local/capability-platform/backend/internal/workflows/clientregistration"
)

const permission = "product.application.security.manage"

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Service interface {
	Register(context.Context, clientregistration.RegisterCommand) (clientregistration.CredentialResult, error)
	Rotate(context.Context, clientregistration.RotateCommand) (clientregistration.CredentialResult, error)
	Revoke(context.Context, string, string, string, string, string, string) (product.ClientCredential, error)
}

type Handler struct {
	service Service
	guard   *adminrequest.Guard
	now     func() time.Time
}

func New(service Service, guard *adminrequest.Guard, now func() time.Time) *Handler {
	if now == nil {
		now = time.Now
	}
	return &Handler{service: service, guard: guard, now: now}
}

type route struct{ productID, applicationID, clientID, credentialID, action string }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := parseRoute(r.URL.Path)
	if !ok {
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	if r.Method != http.MethodPost {
		httpx.MethodNotAllowed(w, r, http.MethodPost)
		return
	}
	if h.service == nil || h.guard == nil || r.URL.RawQuery != "" {
		httpx.Error(w, r, http.StatusBadRequest, "product_client.invalid_request", "invalid client credential request")
		return
	}
	principal, ok := h.guard.Authorize(w, r, permission, adminrequest.TargetScope{Type: "product", ID: route.productID, ProductID: route.productID}, true)
	if !ok {
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	switch route.action {
	case "register":
		h.register(w, r, route, principal, key)
	case "rotate":
		h.rotate(w, r, route, principal, key)
	case "revoke":
		h.revoke(w, r, route, principal, key)
	}
}

type registerRequest struct {
	Environment string    `json:"environment"`
	ProofType   string    `json:"proof_type"`
	PublicKey   string    `json:"public_key"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request, route route, principal adminrequest.Principal, key string) {
	var body registerRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := h.service.Register(r.Context(), clientregistration.RegisterCommand{ProductID: route.productID, ApplicationID: route.applicationID, Environment: body.Environment, ProofType: body.ProofType, PublicKey: body.PublicKey, NotBefore: h.now().UTC(), ExpiresAt: body.ExpiresAt, ActorID: principal.AdminUserID, IdempotencyKey: key, TraceID: requestid.FromContext(r.Context())})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCredential(w, r, http.StatusCreated, result)
}

type rotateRequest struct {
	ExpectedGeneration int       `json:"expected_generation"`
	ProofType          string    `json:"proof_type"`
	PublicKey          string    `json:"public_key"`
	ExpiresAt          time.Time `json:"expires_at"`
	RevokePrevious     bool      `json:"revoke_previous"`
}

func (h *Handler) rotate(w http.ResponseWriter, r *http.Request, route route, principal adminrequest.Principal, key string) {
	var body rotateRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if !body.RevokePrevious {
		httpx.Error(w, r, http.StatusBadRequest, "product_client.invalid_request", "previous credentials must be revoked during rotation")
		return
	}
	result, err := h.service.Rotate(r.Context(), clientregistration.RotateCommand{ProductID: route.productID, ApplicationID: route.applicationID, ClientID: route.clientID, ExpectedGeneration: body.ExpectedGeneration, ProofType: body.ProofType, PublicKey: body.PublicKey, NotBefore: h.now().UTC(), ExpiresAt: body.ExpiresAt, ActorID: principal.AdminUserID, IdempotencyKey: key, TraceID: requestid.FromContext(r.Context())})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCredential(w, r, http.StatusCreated, result)
}

type revokeRequest struct {
	Reason         string `json:"reason"`
	RevokeSessions bool   `json:"revoke_sessions"`
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request, route route, principal adminrequest.Principal, key string) {
	var body revokeRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" || len(body.Reason) > 500 || !body.RevokeSessions {
		httpx.Error(w, r, http.StatusBadRequest, "product_client.invalid_request", "credential revocation must include a reason and revoke sessions")
		return
	}
	credential, err := h.service.Revoke(r.Context(), route.productID, route.clientID, route.credentialID, principal.AdminUserID, key, requestid.FromContext(r.Context()))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if credential.ProductID != route.productID || credential.ClientID != route.clientID || credential.CredentialID != route.credentialID || credential.AuditID == "" {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "revoked", "resource_id": credential.CredentialID, "audit_id": credential.AuditID})
}

func writeCredential(w http.ResponseWriter, r *http.Request, status int, result clientregistration.CredentialResult) {
	if result.ClientID == "" || result.CredentialID == "" || result.AuditID == "" {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	payload := map[string]any{"client_id": result.ClientID, "credential_id": result.CredentialID, "product_id": result.ProductID, "application_id": result.ApplicationID, "environment": result.Environment, "proof_type": result.ProofType, "generation": result.Generation, "expires_at": result.ExpiresAt, "audit_id": result.AuditID}
	if result.Secret != "" {
		payload["secret"] = result.Secret
	}
	httpx.JSON(w, status, payload)
}

func parseRoute(raw string) (route, bool) {
	if raw != strings.TrimSuffix(raw, "/") || !strings.HasPrefix(raw, "/api/v1/admin/products/") {
		return route{}, false
	}
	parts := strings.Split(strings.TrimPrefix(raw, "/api/v1/admin/products/"), "/")
	if len(parts) == 4 && parts[1] == "applications" && parts[3] == "clients" && valid(parts[0]) && valid(parts[2]) {
		return route{productID: parts[0], applicationID: parts[2], action: "register"}, true
	}
	if len(parts) == 7 && parts[1] == "applications" && parts[3] == "clients" && parts[5] == "credentials" && parts[6] == "rotate" && valid(parts[0]) && valid(parts[2]) && valid(parts[4]) {
		return route{productID: parts[0], applicationID: parts[2], clientID: parts[4], action: "rotate"}, true
	}
	if len(parts) == 7 && parts[1] == "applications" && parts[3] == "clients" && parts[5] == "credentials" && valid(parts[0]) && valid(parts[2]) && valid(parts[4]) && valid(parts[6]) {
		return route{productID: parts[0], applicationID: parts[2], clientID: parts[4], credentialID: parts[6], action: "revoke"}, true
	}
	return route{}, false
}
func valid(value string) bool { return identifierPattern.MatchString(value) }
func idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || len(strings.TrimSpace(values[0])) < 16 || len(strings.TrimSpace(values[0])) > 128 {
		httpx.Error(w, r, http.StatusBadRequest, "product_client.idempotency_key_required", "one valid Idempotency-Key is required")
		return "", false
	}
	return strings.TrimSpace(values[0]), true
}
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		status := http.StatusBadRequest
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			status = http.StatusRequestEntityTooLarge
		}
		httpx.Error(w, r, status, "product_client.invalid_request", "invalid client credential request")
		return false
	}
	if d.Decode(&struct{}{}) != io.EOF {
		httpx.Error(w, r, http.StatusBadRequest, "product_client.invalid_request", "request body must contain one JSON value")
		return false
	}
	return true
}
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, clientregistration.ErrInvalidRegistration), errors.Is(err, product.ErrInvalidCommand):
		httpx.Error(w, r, http.StatusBadRequest, "product_client.invalid_request", "invalid client credential request")
	case errors.Is(err, product.ErrNotFound), errors.Is(err, productapplication.ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "product_client.not_found", "client credential target not found")
	case errors.Is(err, product.ErrConflict), errors.Is(err, product.ErrIdempotencyConflict), errors.Is(err, product.ErrCredentialVersionConflict), errors.Is(err, productapplication.ErrConflict), errors.Is(err, productapplication.ErrIdempotencyConflict):
		httpx.Error(w, r, http.StatusConflict, "product_client.conflict", "client credential request conflicts with current state")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "product_client.unavailable", "client credential service is temporarily unavailable")
	}
}
