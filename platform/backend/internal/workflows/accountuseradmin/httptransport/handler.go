package httptransport

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
	"platform.local/capability-platform/backend/internal/workflows/accountuseradmin"
	"platform.local/capability-platform/backend/internal/workflows/accountuserquery"
)

const (
	securityPermission = "identity.security.manage"
	accessPermission   = "product.user-access.manage"
	maxBody            = 32 << 10
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Handler struct {
	service *accountuseradmin.Service
	guard   *adminrequest.Guard
}

func New(service *accountuseradmin.Service, guard *adminrequest.Guard) *Handler {
	return &Handler{service: service, guard: guard}
}

type route struct {
	kind   string
	scope  accountuserquery.Scope
	userID string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.service == nil || h.guard == nil {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	target, ok := parseRoute(r.URL.Path, r.URL.RawPath)
	if !ok {
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	if r.URL.RawQuery != "" {
		httpx.Error(w, r, http.StatusBadRequest, "account_admin.invalid_query", "query parameters are not supported")
		return
	}
	scopeTarget := adminrequest.TargetScope{Type: string(target.scope.Type), ID: target.scope.ProductID, ProductID: target.scope.ProductID, TenantID: target.scope.TenantID}
	if target.scope.Type == accountuserquery.ScopePlatform {
		scopeTarget = adminrequest.TargetScope{Type: "platform"}
	} else if target.scope.Type == accountuserquery.ScopeTenant {
		scopeTarget.ID = target.scope.TenantID
	}
	permission := accessPermission
	if target.kind == "global-security" || target.kind == "global-revoke" {
		permission = securityPermission
	}
	principal, ok := h.guard.Authorize(w, r, permission, scopeTarget, true)
	if !ok {
		return
	}
	traceID := requestid.FromContext(r.Context())
	if traceID == "" {
		traceID = "account-admin-request"
	}
	switch target.kind {
	case "global-security":
		if r.Method != http.MethodPut {
			httpx.MethodNotAllowed(w, r, http.MethodPut)
			return
		}
		var body globalSecurityRequest
		if !decodeJSON(w, r, &body) {
			return
		}
		result, err := h.service.SetGlobalSecurityStatus(r.Context(), accountuseradmin.GlobalSecurityCommand{UserID: target.userID, Status: body.Status, ExpectedVersion: body.ExpectedVersion, ReasonCode: body.ReasonCode, OperatorNote: body.OperatorNote, ActorID: principal.AdminUserID, TraceID: traceID, IdempotencyKey: idempotencyKey(r)})
		if err != nil {
			writeError(w, r, err)
			return
		}
		httpx.JSON(w, http.StatusOK, result)
	case "product-access", "tenant-access":
		if r.Method != http.MethodPut {
			httpx.MethodNotAllowed(w, r, http.MethodPut)
			return
		}
		var body accessRequest
		if !decodeJSON(w, r, &body) {
			return
		}
		var result productuseraccess.StatusChangeResult
		var err error
		if target.kind == "product-access" {
			result, err = h.service.SetProductAccessStatus(r.Context(), target.scope, target.userID, productuseraccess.Status(body.Status), body.ExpectedVersion, body.ReasonCode, body.OperatorNote, principal.AdminUserID, traceID, idempotencyKey(r))
		} else {
			result, err = h.service.SetTenantAccessStatus(r.Context(), target.scope, target.userID, productuseraccess.Status(body.Status), body.ExpectedVersion, body.ReasonCode, body.OperatorNote, principal.AdminUserID, traceID, idempotencyKey(r))
		}
		if err != nil {
			writeError(w, r, err)
			return
		}
		scopeID := target.scope.ProductID
		if target.scope.Type == accountuserquery.ScopeTenant {
			scopeID = target.scope.TenantID
		}
		httpx.JSON(w, http.StatusOK, accessMutationResponse{UserID: result.UserID, ScopeType: string(result.ScopeType), ScopeID: scopeID, Status: string(result.Status), Version: result.AccessVersion, AuditID: result.AuditID})
	case "global-revoke", "product-revoke", "tenant-revoke":
		if r.Method != http.MethodPost {
			httpx.MethodNotAllowed(w, r, http.MethodPost)
			return
		}
		var body revokeRequest
		if !decodeJSON(w, r, &body) {
			return
		}
		result, err := h.service.RevokeSessions(r.Context(), accountuseradmin.SessionRevocationCommand{Scope: target.scope, UserID: target.userID, SessionIDs: body.SessionIDs, AllActive: body.AllActive, ReasonCode: body.ReasonCode, ActorID: principal.AdminUserID, TraceID: traceID, IdempotencyKey: idempotencyKey(r)})
		if err != nil {
			writeError(w, r, err)
			return
		}
		httpx.JSON(w, http.StatusOK, result)
	default:
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
	}
}

type globalSecurityRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	Status          string `json:"status"`
	ReasonCode      string `json:"reason_code"`
	OperatorNote    string `json:"operator_note"`
}
type accessRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	Status          string `json:"status"`
	ReasonCode      string `json:"reason_code"`
	OperatorNote    string `json:"operator_note"`
}
type revokeRequest struct {
	SessionIDs []string `json:"session_ids"`
	AllActive  bool     `json:"all_active"`
	ReasonCode string   `json:"reason_code"`
}

type accessMutationResponse struct {
	UserID    string `json:"user_id"`
	ScopeType string `json:"scope_type"`
	ScopeID   string `json:"scope_id"`
	Status    string `json:"status"`
	Version   int64  `json:"version"`
	AuditID   string `json:"audit_id"`
}

func parseRoute(path, rawPath string) (route, bool) {
	if rawPath != "" || strings.HasSuffix(path, "/") {
		return route{}, false
	}
	if strings.HasPrefix(path, "/api/v1/admin/users/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/users/"), "/")
		if len(parts) == 2 && parts[1] == "security-status" && validID(parts[0]) {
			return route{kind: "global-security", scope: accountuserquery.Scope{Type: accountuserquery.ScopePlatform}, userID: parts[0]}, true
		}
		if len(parts) == 3 && parts[1] == "sessions" && parts[2] == "revoke" && validID(parts[0]) {
			return route{kind: "global-revoke", scope: accountuserquery.Scope{Type: accountuserquery.ScopePlatform}, userID: parts[0]}, true
		}
	}
	const prefix = "/api/v1/admin/products/"
	if !strings.HasPrefix(path, prefix) {
		return route{}, false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) == 4 && validID(parts[0]) && parts[1] == "users" && validID(parts[2]) && parts[3] == "access" {
		return route{kind: "product-access", scope: accountuserquery.Scope{Type: accountuserquery.ScopeProduct, ProductID: parts[0]}, userID: parts[2]}, true
	}
	if len(parts) == 5 && validID(parts[0]) && parts[1] == "users" && validID(parts[2]) && parts[3] == "sessions" && parts[4] == "revoke" {
		return route{kind: "product-revoke", scope: accountuserquery.Scope{Type: accountuserquery.ScopeProduct, ProductID: parts[0]}, userID: parts[2]}, true
	}
	if len(parts) == 6 && validID(parts[0]) && parts[1] == "tenants" && validID(parts[2]) && parts[3] == "users" && validID(parts[4]) && parts[5] == "access" {
		return route{kind: "tenant-access", scope: accountuserquery.Scope{Type: accountuserquery.ScopeTenant, ProductID: parts[0], TenantID: parts[2]}, userID: parts[4]}, true
	}
	if len(parts) == 7 && validID(parts[0]) && parts[1] == "tenants" && validID(parts[2]) && parts[3] == "users" && validID(parts[4]) && parts[5] == "sessions" && parts[6] == "revoke" {
		return route{kind: "tenant-revoke", scope: accountuserquery.Scope{Type: accountuserquery.ScopeTenant, ProductID: parts[0], TenantID: parts[2]}, userID: parts[4]}, true
	}
	return route{}, false
}

func validID(value string) bool { return identifierPattern.MatchString(value) }
func idempotencyKey(r *http.Request) string {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 1 {
		return values[0]
	}
	return ""
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		httpx.Error(w, r, http.StatusBadRequest, "account_admin.invalid_request", "Content-Type must be application/json")
		return false
	}
	if r.ContentLength > maxBody {
		httpx.Error(w, r, http.StatusRequestEntityTooLarge, "account_admin.invalid_request", "request body exceeds 32 KiB")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	raw, err := io.ReadAll(r.Body)
	if err != nil || len(raw) == 0 {
		httpx.Error(w, r, http.StatusBadRequest, "account_admin.invalid_request", "invalid request body")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "account_admin.invalid_request", "invalid request body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "account_admin.invalid_request", "request body must contain one JSON value")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, accountuseradmin.ErrInvalidRequest):
		httpx.Error(w, r, http.StatusBadRequest, "account_admin.invalid_request", "account mutation is invalid")
	case errors.Is(err, accountuseradmin.ErrCapabilityNotEnabled):
		httpx.Error(w, r, http.StatusForbidden, "account_admin.capability_not_enabled", "account capability is not enabled")
	case errors.Is(err, accountuseradmin.ErrScopedUserNotFound):
		httpx.Error(w, r, http.StatusNotFound, "account_admin.scoped_user_not_found", "user is not present in this scope")
	case errors.Is(err, accountuseradmin.ErrIdentityUserNotFound):
		httpx.Error(w, r, http.StatusNotFound, "account_admin.user_not_found", "user is not available")
	case errors.Is(err, accountuseradmin.ErrIdentityConflict):
		httpx.Error(w, r, http.StatusConflict, "ACCOUNT_USER_CONFLICT", "user state version or idempotency conflict")
	case errors.Is(err, productuseraccess.ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "PRODUCT_USER_ACCESS_CONFLICT", "product user access version or idempotency conflict")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
