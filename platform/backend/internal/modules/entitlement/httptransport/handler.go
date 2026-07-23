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
	"strconv"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/entitlement"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

const (
	readPermission   = "entitlement.read"
	managePermission = "entitlement.manage"
	revokePermission = "entitlement.revoke"
	maxRequestBody   = 32 << 10
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,159}$`)

var (
	ErrInvalidBearer      = errors.New("invalid entitlement bearer")
	ErrEntitlementNoScope = errors.New("entitlement user scope unavailable")
)

type Service interface {
	CheckEntitlement(context.Context, entitlement.CheckEntitlementCommand) (entitlement.CheckDecision, error)
	GrantEntitlement(context.Context, entitlement.GrantEntitlementCommand) (entitlement.GrantResult, error)
	ExtendEntitlement(context.Context, entitlement.MutateEntitlementCommand) (entitlement.GrantResult, error)
	ReplaceEntitlement(context.Context, entitlement.MutateEntitlementCommand) (entitlement.GrantResult, error)
	RevokeEntitlement(context.Context, entitlement.MutateEntitlementCommand) (entitlement.GrantResult, error)
	GetCurrentEntitlements(context.Context, entitlement.ProductContext, entitlement.TenantContext, entitlement.UserContext) (entitlement.EntitlementSummary, error)
	ListHistory(context.Context, entitlement.HistoryQuery) ([]entitlement.LedgerEntry, error)
}

type UserSessionContext struct {
	UserID    string
	ProductID string
	TenantID  string
}

type UserSessionResolver interface {
	ResolveUserSession(context.Context, string) (UserSessionContext, error)
}

type Handler struct {
	service  Service
	guard    *adminrequest.Guard
	resolver UserSessionResolver
}

func New(service Service, guard *adminrequest.Guard, resolver UserSessionResolver) *Handler {
	return &Handler{service: service, guard: guard, resolver: resolver}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if h == nil || h.service == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "entitlement.unavailable", "entitlement service unavailable")
		return
	}
	if r.URL.RawPath != "" {
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	switch {
	case r.URL.Path == "/api/v1/entitlements/check":
		h.check(w, r)
	case r.URL.Path == "/api/v1/entitlements/current":
		h.current(w, r)
	case r.URL.Path == "/api/v1/entitlements/history":
		h.userHistory(w, r)
	case r.URL.Path == "/api/v1/admin/entitlements":
		h.adminEntitlements(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/admin/entitlements/"):
		h.adminMutation(w, r)
	default:
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
	}
}

type checkRequest struct {
	RequestedFeatures []string   `json:"requested_features"`
	DeviceID          string     `json:"device_id"`
	ClientTime        *time.Time `json:"client_time"`
}

func (h *Handler) check(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodPost) {
		return
	}
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var body checkRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	value, err := h.service.CheckEntitlement(r.Context(), entitlement.CheckEntitlementCommand{
		Product:           entitlement.ProductContext{ProductID: user.ProductID},
		Tenant:            entitlement.TenantContext{ProductID: user.ProductID, TenantID: user.TenantID},
		User:              entitlement.UserContext{UserID: user.UserID},
		RequestedFeatures: body.RequestedFeatures,
		DeviceID:          body.DeviceID,
		ClientObservedAt:  body.ClientTime,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, decisionResponse(value))
}

func (h *Handler) current(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) || !requireEmptyBody(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		httpx.Error(w, r, http.StatusBadRequest, "entitlement.invalid_query", "query parameters are not supported")
		return
	}
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	value, err := h.service.GetCurrentEntitlements(r.Context(), entitlement.ProductContext{ProductID: user.ProductID}, entitlement.TenantContext{ProductID: user.ProductID, TenantID: user.TenantID}, entitlement.UserContext{UserID: user.UserID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, summaryResponse(value))
}

func (h *Handler) userHistory(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) || !requireEmptyBody(w, r) {
		return
	}
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if err := validateKeys(r.URL.Query(), "page_size", "cursor"); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "entitlement.invalid_query", "query parameters are invalid")
		return
	}
	items, err := h.service.ListHistory(r.Context(), entitlement.HistoryQuery{
		ProductID: user.ProductID, TenantID: user.TenantID, UserID: user.UserID,
		Limit: parsePageSize(r.URL.Query().Get("page_size")), Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, historyResponse(items))
}

func (h *Handler) adminEntitlements(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.adminGrant(w, r)
	case http.MethodGet:
		h.adminHistory(w, r)
	default:
		httpx.MethodNotAllowed(w, r, "GET, POST")
	}
}

type grantRequest struct {
	UserID        string          `json:"user_id"`
	ProductID     string          `json:"product_id"`
	TenantID      string          `json:"tenant_id"`
	PolicyID      string          `json:"policy_id"`
	PolicyVersion int64           `json:"policy_version"`
	Validity      validityRequest `json:"validity"`
	Source        sourceRequest   `json:"source"`
	ReasonCode    string          `json:"reason_code"`
}

type mutateRequest struct {
	UserID           string          `json:"user_id"`
	ProductID        string          `json:"product_id"`
	TenantID         string          `json:"tenant_id"`
	ExpectedRevision int64           `json:"expected_revision"`
	PolicyID         string          `json:"policy_id"`
	PolicyVersion    int64           `json:"policy_version"`
	Validity         validityRequest `json:"validity"`
	Source           sourceRequest   `json:"source"`
	ReasonCode       string          `json:"reason_code"`
}

type revokeRequest struct {
	UserID           string        `json:"user_id"`
	ProductID        string        `json:"product_id"`
	TenantID         string        `json:"tenant_id"`
	ExpectedRevision int64         `json:"expected_revision"`
	Source           sourceRequest `json:"source"`
	ReasonCode       string        `json:"reason_code"`
}

type validityRequest struct {
	Rule            string     `json:"rule"`
	DurationSeconds int64      `json:"duration_seconds"`
	FixedUntil      *time.Time `json:"fixed_until"`
}

type sourceRequest struct {
	SourceType     string `json:"source_type"`
	SourceID       string `json:"source_id"`
	SourceEffectID string `json:"source_effect_id"`
}

func (h *Handler) adminGrant(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		httpx.Error(w, r, http.StatusBadRequest, "entitlement.invalid_query", "query parameters are not supported")
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body grantRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	admin, ok := h.authorizeAdmin(w, r, managePermission, body.ProductID, body.TenantID, true)
	if !ok {
		return
	}
	result, err := h.service.GrantEntitlement(r.Context(), entitlement.GrantEntitlementCommand{
		Admin:          entitlement.AdminScope{AdminID: admin.AdminUserID, ProductID: body.ProductID, TenantID: body.TenantID},
		User:           entitlement.UserContext{UserID: body.UserID},
		Policy:         entitlement.PolicyRef{PolicyID: body.PolicyID, Version: body.PolicyVersion},
		Validity:       validityFromRequest(body.Validity),
		Source:         sourceFromRequest(body.Source),
		IdempotencyKey: key,
		ReasonCode:     entitlement.ReasonCode(body.ReasonCode),
		TraceID:        requestid.FromContext(r.Context()),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, grantResponse(result))
}

func (h *Handler) adminHistory(w http.ResponseWriter, r *http.Request) {
	if !requireEmptyBody(w, r) {
		return
	}
	if err := validateKeys(r.URL.Query(), "product_id", "tenant_id", "user_id", "page_size", "cursor"); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "entitlement.invalid_query", "query parameters are invalid")
		return
	}
	productID, tenantID, userID := r.URL.Query().Get("product_id"), r.URL.Query().Get("tenant_id"), r.URL.Query().Get("user_id")
	if _, ok := h.authorizeAdmin(w, r, readPermission, productID, tenantID, false); !ok {
		return
	}
	items, err := h.service.ListHistory(r.Context(), entitlement.HistoryQuery{
		ProductID: productID, TenantID: tenantID, UserID: userID,
		Limit: parsePageSize(r.URL.Query().Get("page_size")), Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, historyResponse(items))
}

func (h *Handler) adminMutation(w http.ResponseWriter, r *http.Request) {
	grantID, action, ok := parseAdminMutationRoute(r.URL.Path)
	if !ok {
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	if !method(w, r, http.MethodPost) {
		return
	}
	if r.URL.RawQuery != "" {
		httpx.Error(w, r, http.StatusBadRequest, "entitlement.invalid_query", "query parameters are not supported")
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	if action == "revoke" {
		h.adminRevoke(w, r, grantID, key)
		return
	}
	var body mutateRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	admin, ok := h.authorizeAdmin(w, r, managePermission, body.ProductID, body.TenantID, true)
	if !ok {
		return
	}
	command := entitlement.MutateEntitlementCommand{
		Admin:            entitlement.AdminScope{AdminID: admin.AdminUserID, ProductID: body.ProductID, TenantID: body.TenantID},
		User:             entitlement.UserContext{UserID: body.UserID},
		Policy:           entitlement.PolicyRef{PolicyID: body.PolicyID, Version: body.PolicyVersion},
		Validity:         validityFromRequest(body.Validity),
		Source:           sourceFromRequest(body.Source),
		IdempotencyKey:   key,
		ExpectedRevision: body.ExpectedRevision,
		TargetGrantID:    grantID,
		ReasonCode:       entitlement.ReasonCode(body.ReasonCode),
		TraceID:          requestid.FromContext(r.Context()),
	}
	result, err := h.service.ExtendEntitlement(r.Context(), command)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, grantResponse(result))
}

func (h *Handler) adminRevoke(w http.ResponseWriter, r *http.Request, grantID, key string) {
	var body revokeRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	admin, ok := h.authorizeAdmin(w, r, revokePermission, body.ProductID, body.TenantID, true)
	if !ok {
		return
	}
	result, err := h.service.RevokeEntitlement(r.Context(), entitlement.MutateEntitlementCommand{
		Admin:            entitlement.AdminScope{AdminID: admin.AdminUserID, ProductID: body.ProductID, TenantID: body.TenantID},
		User:             entitlement.UserContext{UserID: body.UserID},
		Source:           sourceFromRequest(body.Source),
		IdempotencyKey:   key,
		ExpectedRevision: body.ExpectedRevision,
		TargetGrantID:    grantID,
		ReasonCode:       entitlement.ReasonCode(body.ReasonCode),
		TraceID:          requestid.FromContext(r.Context()),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, grantResponse(result))
}

func (h *Handler) requireUser(w http.ResponseWriter, r *http.Request) (UserSessionContext, bool) {
	if h.resolver == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "entitlement.unavailable", "entitlement user resolver unavailable")
		return UserSessionContext{}, false
	}
	token, ok := strictBearer(r)
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "entitlement.unauthorized", "authentication required")
		return UserSessionContext{}, false
	}
	value, err := h.resolver.ResolveUserSession(r.Context(), token)
	if err != nil {
		if errors.Is(err, ErrInvalidBearer) {
			httpx.Error(w, r, http.StatusUnauthorized, "entitlement.unauthorized", "authentication required")
		} else {
			httpx.Error(w, r, http.StatusServiceUnavailable, "entitlement.unavailable", "entitlement user resolver unavailable")
		}
		return UserSessionContext{}, false
	}
	if !validIdentifier(value.ProductID) || !validIdentifier(value.TenantID) || !validIdentifier(value.UserID) {
		httpx.Error(w, r, http.StatusUnauthorized, "entitlement.unauthorized", "authentication required")
		return UserSessionContext{}, false
	}
	return value, true
}

func (h *Handler) authorizeAdmin(w http.ResponseWriter, r *http.Request, permission, productID, tenantID string, highRisk bool) (adminrequest.Principal, bool) {
	if h.guard == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "entitlement.unavailable", "entitlement admin guard unavailable")
		return adminrequest.Principal{}, false
	}
	if !validIdentifier(productID) || !validIdentifier(tenantID) {
		httpx.Error(w, r, http.StatusBadRequest, "entitlement.invalid_request", "entitlement scope is invalid")
		return adminrequest.Principal{}, false
	}
	return h.guard.Authorize(w, r, permission, adminrequest.TargetScope{Type: "tenant", ID: tenantID, ProductID: productID, TenantID: tenantID}, highRisk)
}

func validityFromRequest(value validityRequest) entitlement.ValidityInput {
	switch value.Rule {
	case string(entitlement.ValidityFixedDuration):
		return entitlement.ValidityInput{Rule: entitlement.ValidityFixedDuration, Duration: time.Duration(value.DurationSeconds) * time.Second}
	case string(entitlement.ValidityFixedEnd):
		if value.FixedUntil == nil {
			return entitlement.ValidityInput{Rule: entitlement.ValidityFixedEnd}
		}
		return entitlement.ValidityInput{Rule: entitlement.ValidityFixedEnd, FixedUntil: value.FixedUntil.UTC()}
	case string(entitlement.ValidityLifetime):
		return entitlement.ValidityInput{Rule: entitlement.ValidityLifetime}
	default:
		return entitlement.ValidityInput{Rule: entitlement.ValidityRule(value.Rule)}
	}
}

func sourceFromRequest(value sourceRequest) entitlement.SourceRef {
	return entitlement.SourceRef{
		Type: entitlement.SourceType(value.SourceType), SourceID: value.SourceID, SourceEffectID: value.SourceEffectID,
	}
}

func parseAdminMutationRoute(path string) (string, string, bool) {
	if strings.HasSuffix(path, "/") {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/entitlements/"), "/")
	if len(parts) != 2 || !validIdentifier(parts[0]) || (parts[1] != "extend" && parts[1] != "revoke") {
		return "", "", false
	}
	return parts[0], parts[1], true
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
	if len(token) < 16 || len(token) > 4096 || strings.TrimSpace(token) != token || strings.ContainsAny(token, " ,\t\r\n") {
		return "", false
	}
	return token, true
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || values[0] != strings.TrimSpace(values[0]) || len(values[0]) < 16 || len(values[0]) > 128 || strings.Contains(values[0], ",") {
		httpx.Error(w, r, http.StatusBadRequest, "entitlement.invalid_idempotency_key", "exactly one valid Idempotency-Key is required")
		return "", false
	}
	return values[0], true
}

func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method != want {
		httpx.MethodNotAllowed(w, r, want)
		return false
	}
	return true
}

func requireEmptyBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1)
	var one [1]byte
	n, _ := r.Body.Read(one[:])
	if n != 0 {
		httpx.Error(w, r, http.StatusBadRequest, "entitlement.invalid_request", "request body is not supported")
		return false
	}
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		httpx.Error(w, r, http.StatusBadRequest, "entitlement.invalid_request", "Content-Type must be application/json")
		return false
	}
	if r.ContentLength > maxRequestBody {
		httpx.Error(w, r, http.StatusRequestEntityTooLarge, "entitlement.request_too_large", "request body exceeds 32 KiB")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	raw, err := io.ReadAll(r.Body)
	if err != nil || len(raw) == 0 || !validJSONShape(raw) {
		httpx.Error(w, r, http.StatusBadRequest, "entitlement.invalid_request", "invalid request body")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "entitlement.invalid_request", "invalid request body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "entitlement.invalid_request", "request body must contain one JSON value")
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

func validateKeys(values map[string][]string, allowed ...string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key, value := range values {
		if _, ok := allowedSet[key]; !ok || len(value) > 1 {
			return entitlement.ErrInvalidArgument
		}
	}
	return nil
}

func parsePageSize(value string) int {
	if value == "" {
		return 50
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > 200 {
		return 0
	}
	return parsed
}

func validIdentifier(value string) bool { return identifierPattern.MatchString(value) }

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, entitlement.ErrInvalidArgument):
		httpx.Error(w, r, http.StatusBadRequest, "entitlement.invalid_request", "entitlement request is invalid")
	case errors.Is(err, entitlement.ErrScopeMismatch):
		httpx.Error(w, r, http.StatusBadRequest, "ENTITLEMENT_SCOPE_MISMATCH", "entitlement scope does not match")
	case errors.Is(err, entitlement.ErrOperationConflict):
		httpx.Error(w, r, http.StatusConflict, "ENTITLEMENT_OPERATION_CONFLICT", "entitlement operation conflict")
	case errors.Is(err, entitlement.ErrPolicyConflict):
		httpx.Error(w, r, http.StatusConflict, "ENTITLEMENT_POLICY_CONFLICT", "entitlement policy conflict")
	case errors.Is(err, entitlement.ErrSourceDuplicate):
		httpx.Error(w, r, http.StatusConflict, "ENTITLEMENT_SOURCE_DUPLICATE", "entitlement source effect already exists")
	case errors.Is(err, entitlement.ErrRequired):
		httpx.Error(w, r, http.StatusForbidden, "ENTITLEMENT_REQUIRED", "entitlement is required")
	case errors.Is(err, entitlement.ErrExpired):
		httpx.Error(w, r, http.StatusForbidden, "ENTITLEMENT_EXPIRED", "entitlement is expired")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "entitlement.internal_error", "entitlement service unavailable")
	}
}

func decisionResponse(value entitlement.CheckDecision) map[string]any {
	response := map[string]any{
		"allowed": value.Allowed, "decision_stage": value.DecisionStage, "revision": value.Revision,
		"features": value.Features, "server_time": value.ServerTime,
	}
	if value.ReasonCode != "" {
		response["reason_code"] = value.ReasonCode
	} else {
		response["reason_code"] = nil
	}
	if value.PlanCode != "" {
		response["plan_code"] = value.PlanCode
	} else {
		response["plan_code"] = nil
	}
	response["valid_until"] = value.ValidUntil
	response["offline_grace_until"] = value.OfflineGraceUntil
	return response
}

func grantResponse(value entitlement.GrantResult) map[string]any {
	return map[string]any{
		"entitlement_id": value.EntitlementID, "grant_id": value.GrantID, "revision": value.Revision,
		"valid_until": value.ValidUntil, "audit_id": value.AuditID, "decision": decisionResponse(value.Decision),
	}
}

func summaryResponse(value entitlement.EntitlementSummary) map[string]any {
	features := value.EffectiveFeatures
	if features == nil {
		features = map[string]any{}
	}
	response := map[string]any{
		"product_id": value.ProductID, "tenant_id": value.TenantID, "user_id": value.UserID,
		"revision": value.Revision, "features": features, "updated_at": value.UpdatedAt,
		"valid_until": value.ValidUntil, "offline_grace_until": value.OfflineGraceUntil,
	}
	if value.PlanCode != "" {
		response["plan_code"] = value.PlanCode
	} else {
		response["plan_code"] = nil
	}
	return response
}

func historyResponse(items []entitlement.LedgerEntry) map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		sourceType, sourceID := any(nil), any(nil)
		if item.SourceType != "" {
			sourceType = item.SourceType
		}
		if item.SourceID != "" {
			sourceID = item.SourceID
		}
		out = append(out, map[string]any{
			"ledger_id": item.LedgerID, "operation_type": item.OperationType, "operation_id": item.OperationID,
			"source_type": sourceType, "source_id": sourceID, "grant_id": item.GrantID,
			"before_revision": item.BeforeRevision, "after_revision": item.AfterRevision,
			"audit_id": item.AuditID, "trace_id": item.TraceID, "created_at": item.CreatedAt,
		})
	}
	return map[string]any{"items": out, "next_cursor": nil}
}
