package httptransport

import (
	"bytes"
	"context"
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
)

const (
	managePermission = "product.user-access.manage"
	productsPrefix   = "/api/v1/admin/products/"
	maxRequestBody   = 32 << 10
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Service interface {
	SetProductAccessStatus(context.Context, productuseraccess.SetProductAccessStatusCommand) (productuseraccess.StatusChangeResult, error)
	SetTenantAccessStatus(context.Context, productuseraccess.SetTenantAccessStatusCommand) (productuseraccess.StatusChangeResult, error)
}

type Handler struct {
	service Service
	guard   *adminrequest.Guard
}

func New(service Service, guard *adminrequest.Guard) *Handler {
	return &Handler{service: service, guard: guard}
}

type route struct {
	productID string
	tenantID  string
	userID    string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target, ok := parseRoute(r)
	if !ok {
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	if h.service == nil || h.guard == nil {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if r.Method != http.MethodPut {
		httpx.MethodNotAllowed(w, r, http.MethodPut)
		return
	}
	if r.URL.RawQuery != "" {
		httpx.Error(w, r, http.StatusBadRequest, "product_user_access.invalid_query", "query parameters are not supported")
		return
	}
	h.setStatus(w, r, target)
}

type setStatusRequest struct {
	ExpectedVersion *int64 `json:"expected_version"`
	Status          string `json:"status"`
	ReasonCode      string `json:"reason_code"`
	OperatorNote    string `json:"operator_note"`
}

type mutationResponse struct {
	UserID    string                      `json:"user_id"`
	ScopeType productuseraccess.ScopeType `json:"scope_type"`
	ScopeID   string                      `json:"scope_id"`
	Status    productuseraccess.Status    `json:"status"`
	Version   int64                       `json:"version"`
	AuditID   string                      `json:"audit_id"`
}

func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request, target route) {
	scope := adminrequest.TargetScope{Type: "product", ID: target.productID, ProductID: target.productID}
	if target.tenantID != "" {
		scope = adminrequest.TargetScope{Type: "tenant", ID: target.tenantID, ProductID: target.productID, TenantID: target.tenantID}
	}
	principal, ok := h.guard.Authorize(w, r, managePermission, scope, true)
	if !ok {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body setStatusRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ExpectedVersion == nil {
		httpx.Error(w, r, http.StatusBadRequest, "product_user_access.invalid_request", "expected_version is required")
		return
	}

	traceID := requestid.FromContext(r.Context())
	var result productuseraccess.StatusChangeResult
	var err error
	if target.tenantID == "" {
		result, err = h.service.SetProductAccessStatus(r.Context(), productuseraccess.SetProductAccessStatusCommand{
			Product: productuseraccess.ProductContext{ProductID: target.productID}, User: productuseraccess.UserContext{UserID: target.userID},
			Status: productuseraccess.Status(body.Status), ExpectedVersion: *body.ExpectedVersion,
			ReasonCode: body.ReasonCode, OperatorNote: body.OperatorNote, IdempotencyKey: idempotencyKey,
			ActorID: principal.AdminUserID, TraceID: traceID,
		})
	} else {
		result, err = h.service.SetTenantAccessStatus(r.Context(), productuseraccess.SetTenantAccessStatusCommand{
			Product: productuseraccess.ProductContext{ProductID: target.productID},
			Tenant:  productuseraccess.TenantContext{ProductID: target.productID, TenantID: target.tenantID},
			User:    productuseraccess.UserContext{UserID: target.userID}, Status: productuseraccess.Status(body.Status),
			ExpectedVersion: *body.ExpectedVersion, ReasonCode: body.ReasonCode, OperatorNote: body.OperatorNote,
			IdempotencyKey: idempotencyKey, ActorID: principal.AdminUserID, TraceID: traceID,
		})
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !validResult(result, target, productuseraccess.Status(body.Status)) {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	scopeID := target.productID
	if target.tenantID != "" {
		scopeID = target.tenantID
	}
	httpx.JSON(w, http.StatusOK, mutationResponse{
		UserID: result.UserID, ScopeType: result.ScopeType, ScopeID: scopeID,
		Status: result.Status, Version: result.AccessVersion, AuditID: result.AuditID,
	})
}

func validResult(result productuseraccess.StatusChangeResult, target route, status productuseraccess.Status) bool {
	wantScope := productuseraccess.ScopeProduct
	if target.tenantID != "" {
		wantScope = productuseraccess.ScopeTenant
	}
	return result.ScopeType == wantScope && result.ProductID == target.productID && result.TenantID == target.tenantID &&
		result.UserID == target.userID && result.Status == status && result.AccessVersion > 0 && result.AuditID != ""
}

func parseRoute(r *http.Request) (route, bool) {
	if r.URL.RawPath != "" || !strings.HasPrefix(r.URL.Path, productsPrefix) || strings.HasSuffix(r.URL.Path, "/") {
		return route{}, false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, productsPrefix), "/")
	if len(parts) == 4 && parts[1] == "users" && parts[3] == "access" && validIdentifier(parts[0]) && validIdentifier(parts[2]) {
		return route{productID: parts[0], userID: parts[2]}, true
	}
	if len(parts) == 6 && parts[1] == "tenants" && parts[3] == "users" && parts[5] == "access" && validIdentifier(parts[0]) && validIdentifier(parts[2]) && validIdentifier(parts[4]) {
		return route{productID: parts[0], tenantID: parts[2], userID: parts[4]}, true
	}
	return route{}, false
}

func validIdentifier(value string) bool { return identifierPattern.MatchString(value) }

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || values[0] != strings.TrimSpace(values[0]) || len(values[0]) < 16 || len(values[0]) > 128 {
		httpx.Error(w, r, http.StatusBadRequest, "product_user_access.invalid_idempotency_key", "exactly one valid Idempotency-Key is required")
		return "", false
	}
	return values[0], true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		httpx.Error(w, r, http.StatusBadRequest, "product_user_access.invalid_request", "Content-Type must be application/json")
		return false
	}
	if r.ContentLength > maxRequestBody {
		httpx.Error(w, r, http.StatusRequestEntityTooLarge, "product_user_access.request_too_large", "request body exceeds 32 KiB")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeDecodeError(w, r, err)
		return false
	}
	if len(raw) == 0 || !validJSONShape(raw) {
		httpx.Error(w, r, http.StatusBadRequest, "product_user_access.invalid_request", "invalid request body")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
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
		httpx.Error(w, r, http.StatusBadRequest, "product_user_access.invalid_request", "request body must contain one JSON value")
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
	delimiter, composite := token.(json.Delim)
	if !composite {
		return true
	}
	switch delimiter {
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

func writeDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		httpx.Error(w, r, http.StatusRequestEntityTooLarge, "product_user_access.request_too_large", "request body exceeds 32 KiB")
		return
	}
	httpx.Error(w, r, http.StatusBadRequest, "product_user_access.invalid_request", "invalid request body")
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, productuseraccess.ErrInvalidArgument):
		httpx.Error(w, r, http.StatusBadRequest, "product_user_access.invalid_request", "product user access request is invalid")
	case errors.Is(err, productuseraccess.ErrScopeMismatch):
		httpx.Error(w, r, http.StatusBadRequest, "PRODUCT_USER_ACCESS_SCOPE_MISMATCH", "product user access scope does not match")
	case errors.Is(err, productuseraccess.ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "PRODUCT_USER_ACCESS_CONFLICT", "product user access version or idempotency conflict")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
