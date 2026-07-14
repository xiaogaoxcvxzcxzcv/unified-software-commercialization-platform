package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/tenant"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

const (
	tenantManagePermission = "tenant.manage"
	productsPrefix         = "/api/v1/admin/products/"
	maxRequestBody         = 1 << 20
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Service interface {
	CreateAgentTenant(context.Context, tenant.CreateAgentTenantCommand) (tenant.Tenant, error)
	ListTenants(context.Context, tenant.ListTenantsCommand) ([]tenant.Tenant, error)
}

type Handler struct {
	service Service
	guard   *adminrequest.Guard
}

func New(service Service, guard *adminrequest.Guard) *Handler {
	return &Handler{service: service, guard: guard}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	productID, ok := parseRoute(r)
	if !ok {
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	if h.service == nil || h.guard == nil {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.list(w, r, productID)
	case http.MethodPost:
		h.create(w, r, productID)
	default:
		httpx.MethodNotAllowed(w, r, "GET, POST")
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, productID string) {
	if r.URL.RawQuery != "" {
		httpx.Error(w, r, http.StatusBadRequest, "tenant.invalid_query", "query parameters are not supported")
		return
	}
	if _, ok := h.authorize(w, r, productID); !ok {
		return
	}
	items, err := h.service.ListTenants(r.Context(), tenant.ListTenantsCommand{ProductID: productID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	response := tenantListResponse{Items: make([]tenantSummary, len(items))}
	for i := range items {
		response.Items[i] = summarize(items[i])
	}
	httpx.JSON(w, http.StatusOK, response)
}

type createTenantRequest struct {
	Name             string `json:"name"`
	TenantCode       string `json:"tenant_code"`
	Status           string `json:"status"`
	ExternalAgentRef string `json:"external_agent_ref"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, productID string) {
	if r.URL.RawQuery != "" {
		httpx.Error(w, r, http.StatusBadRequest, "tenant.invalid_query", "query parameters are not supported")
		return
	}
	principal, ok := h.authorize(w, r, productID)
	if !ok {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body createTenantRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	created, err := h.service.CreateAgentTenant(r.Context(), tenant.CreateAgentTenantCommand{
		ProductID: productID, TenantCode: body.TenantCode, Name: body.Name, Status: body.Status,
		ExternalAgentRef: body.ExternalAgentRef, ActorID: principal.AdminUserID,
		IdempotencyKey: idempotencyKey, TraceID: requestid.FromContext(r.Context()),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if created.AuditID == "" || created.ProductID != productID || created.TenantType != tenant.TenantTypeAgent {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	httpx.JSON(w, http.StatusCreated, createTenantResponse{
		TenantID: created.TenantID, ProductID: created.ProductID, TenantType: created.TenantType,
		Status: created.Status, AuditID: created.AuditID,
	})
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, productID string) (adminrequest.Principal, bool) {
	return h.guard.Authorize(w, r, tenantManagePermission, adminrequest.TargetScope{
		Type: "product", ID: productID, ProductID: productID,
	}, false)
}

type createTenantResponse struct {
	TenantID   string `json:"tenant_id"`
	ProductID  string `json:"product_id"`
	TenantType string `json:"tenant_type"`
	Status     string `json:"status"`
	AuditID    string `json:"audit_id"`
}

type tenantSummary struct {
	TenantID         string    `json:"tenant_id"`
	ProductID        string    `json:"product_id"`
	TenantCode       string    `json:"tenant_code"`
	Name             string    `json:"name"`
	TenantType       string    `json:"tenant_type"`
	Status           string    `json:"status"`
	ExternalAgentRef string    `json:"external_agent_ref,omitempty"`
	ContextVersion   int64     `json:"context_version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type tenantListResponse struct {
	Items []tenantSummary `json:"items"`
}

func summarize(value tenant.Tenant) tenantSummary {
	return tenantSummary{
		TenantID: value.TenantID, ProductID: value.ProductID, TenantCode: value.TenantCode,
		Name: value.Name, TenantType: value.TenantType, Status: value.Status,
		ExternalAgentRef: value.ExternalAgentRef, ContextVersion: value.ContextVersion,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		httpx.Error(w, r, http.StatusBadRequest, "tenant.invalid_idempotency_key", "exactly one Idempotency-Key is required")
		return "", false
	}
	key := strings.TrimSpace(values[0])
	if len(key) < 16 || len(key) > 128 {
		httpx.Error(w, r, http.StatusBadRequest, "tenant.invalid_idempotency_key", "Idempotency-Key must be between 16 and 128 characters")
		return "", false
	}
	return key, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.ContentLength > maxRequestBody {
		httpx.Error(w, r, http.StatusRequestEntityTooLarge, "tenant.request_too_large", "request body exceeds 1 MiB")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeDecodeError(w, r, err)
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeDecodeError(w, r, err)
				return false
			}
		}
		httpx.Error(w, r, http.StatusBadRequest, "tenant.invalid_request", "request body must contain one JSON value")
		return false
	}
	return true
}

func writeDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		httpx.Error(w, r, http.StatusRequestEntityTooLarge, "tenant.request_too_large", "request body exceeds 1 MiB")
		return
	}
	httpx.Error(w, r, http.StatusBadRequest, "tenant.invalid_request", "invalid request body")
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tenant.ErrInvalidCommand):
		httpx.Error(w, r, http.StatusBadRequest, "tenant.invalid_request", "tenant request is invalid")
	case errors.Is(err, tenant.ErrTenantNotFound):
		httpx.Error(w, r, http.StatusNotFound, "tenant.not_found", "tenant was not found")
	case errors.Is(err, tenant.ErrTenantCodeConflict):
		httpx.Error(w, r, http.StatusConflict, "tenant.code_conflict", "tenant code already exists in this product")
	case errors.Is(err, tenant.ErrIdempotencyConflict):
		httpx.Error(w, r, http.StatusConflict, "tenant.idempotency_conflict", "Idempotency-Key was reused with a different request")
	case errors.Is(err, tenant.ErrTenantSuspended):
		httpx.Error(w, r, http.StatusConflict, "tenant.suspended", "tenant is suspended")
	case errors.Is(err, tenant.ErrInvalidTenantProof):
		httpx.Error(w, r, http.StatusForbidden, "tenant.proof_rejected", "tenant proof was rejected")
	case errors.Is(err, tenant.ErrResolutionUnsupported):
		httpx.Error(w, r, http.StatusBadRequest, "tenant.resolution_unsupported", "tenant resolution method is unsupported")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func parseRoute(r *http.Request) (string, bool) {
	if r.URL.RawPath != "" || strings.Contains(r.URL.EscapedPath(), "%") || r.URL.Path != path.Clean(r.URL.Path) || !strings.HasPrefix(r.URL.Path, productsPrefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, productsPrefix), "/")
	if len(parts) != 2 || !validIdentifier(parts[0]) || parts[1] != "tenants" {
		return "", false
	}
	return parts[0], true
}

func validIdentifier(value string) bool { return identifierPattern.MatchString(value) }
