package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
	"platform.local/capability-platform/backend/internal/workflows/tenantadmin"
	"regexp"
	"strings"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Service interface {
	Bind(context.Context, tenantadmin.BindCommand) (accesscontrol.AdminScopeBinding, error)
}
type Handler struct {
	service Service
	guard   *adminrequest.Guard
}

func New(service Service, guard *adminrequest.Guard) *Handler {
	return &Handler{service: service, guard: guard}
}

type requestBody struct {
	UserID   string `json:"user_id"`
	RoleCode string `json:"role_code"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, t, ok := parse(r.URL.Path)
	if !ok {
		httpx.Error(w, r, 404, "route_not_found", "route not found")
		return
	}
	if r.Method != http.MethodPost {
		httpx.MethodNotAllowed(w, r, http.MethodPost)
		return
	}
	if h.service == nil || h.guard == nil || r.URL.RawQuery != "" {
		httpx.Error(w, r, 400, "tenant_admin.invalid_request", "invalid tenant administrator request")
		return
	}
	principal, ok := h.guard.Authorize(w, r, "access.manage", adminrequest.TargetScope{Type: "tenant", ID: t, ProductID: p, TenantID: t}, true)
	if !ok {
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || len(strings.TrimSpace(keys[0])) < 16 || len(strings.TrimSpace(keys[0])) > 128 {
		httpx.Error(w, r, 400, "tenant_admin.idempotency_key_required", "one valid Idempotency-Key is required")
		return
	}
	var body requestBody
	if !decode(w, r, &body) {
		return
	}
	result, err := h.service.Bind(r.Context(), tenantadmin.BindCommand{ProductID: p, TenantID: t, AdminUserID: body.UserID, RoleCode: body.RoleCode, ActorID: principal.AdminUserID, IdempotencyKey: strings.TrimSpace(keys[0]), TraceID: requestid.FromContext(r.Context())})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if result.Scope.ProductID != p || result.Scope.TenantID != t || result.AuditID == "" {
		httpx.Error(w, r, 500, "internal_error", "internal server error")
		return
	}
	httpx.JSON(w, 201, map[string]any{"status": "bound", "resource_id": result.BindingID, "audit_id": result.AuditID, "authorization_version": result.AuthorizationVersion})
}
func parse(raw string) (string, string, bool) {
	if raw != strings.TrimSuffix(raw, "/") || !strings.HasPrefix(raw, "/api/v1/admin/products/") {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(raw, "/api/v1/admin/products/"), "/")
	if len(parts) != 4 || parts[1] != "tenants" || parts[3] != "admins" || !identifier.MatchString(parts[0]) || !identifier.MatchString(parts[2]) {
		return "", "", false
	}
	return parts[0], parts[2], true
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		httpx.Error(w, r, 400, "tenant_admin.invalid_request", "invalid tenant administrator request")
		return false
	}
	if d.Decode(&struct{}{}) != io.EOF {
		httpx.Error(w, r, 400, "tenant_admin.invalid_request", "request body must contain one JSON value")
		return false
	}
	return true
}
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tenantadmin.ErrTenantScopeMismatch):
		httpx.Error(w, r, 404, "tenant_admin.tenant_not_found", "tenant not found")
	case errors.Is(err, accesscontrol.ErrInvalidScopeBinding):
		httpx.Error(w, r, 400, "tenant_admin.invalid_request", "invalid tenant administrator request")
	case errors.Is(err, accesscontrol.ErrScopeBindingConflict), errors.Is(err, accesscontrol.ErrIdempotencyConflict):
		httpx.Error(w, r, 409, "tenant_admin.conflict", "tenant administrator binding conflicts with current state")
	case errors.Is(err, accesscontrol.ErrRoleNotFound):
		httpx.Error(w, r, 404, "tenant_admin.role_not_found", "administrator role not found")
	default:
		httpx.Error(w, r, 500, "tenant_admin.unavailable", "tenant administrator service is temporarily unavailable")
	}
}
