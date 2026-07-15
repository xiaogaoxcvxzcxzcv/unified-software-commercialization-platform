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

	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

const (
	productReadPermission               = "product.read"
	applicationManagePermission         = "product.application.manage"
	applicationSecurityManagePermission = "product.application.security.manage"
	productsPrefix                      = "/api/v1/admin/products/"
	maxRequestBody                      = 1 << 20
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Service interface {
	CreateApplication(context.Context, productapplication.CreateCommand) (productapplication.Application, error)
	ListApplications(context.Context, productapplication.ProductContext) ([]productapplication.Application, error)
	ReplaceRedirects(context.Context, productapplication.ReplaceRedirectsCommand) (productapplication.RedirectPolicyVersion, error)
	SuspendApplication(context.Context, productapplication.SuspendCommand) (productapplication.SuspendResult, error)
}

type Config struct {
	Environment productapplication.Environment
}

type Handler struct {
	service Service
	guard   *adminrequest.Guard
	config  Config
}

func New(service Service, guard *adminrequest.Guard, config Config) *Handler {
	return &Handler{service: service, guard: guard, config: config}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.service == nil || h.guard == nil || !validEnvironment(h.config.Environment) {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	route, ok := parseRoute(r)
	if !ok {
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	switch route.kind {
	case routeApplications:
		switch r.Method {
		case http.MethodGet:
			h.list(w, r, route)
		case http.MethodPost:
			h.create(w, r, route)
		default:
			httpx.MethodNotAllowed(w, r, "GET, POST")
		}
	case routeRedirects:
		if r.Method != http.MethodPut {
			httpx.MethodNotAllowed(w, r, http.MethodPut)
			return
		}
		h.replaceRedirects(w, r, route)
	case routeSuspend:
		if r.Method != http.MethodPost {
			httpx.MethodNotAllowed(w, r, http.MethodPost)
			return
		}
		h.suspend(w, r, route)
	default:
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, route parsedRoute) {
	if _, ok := h.authorize(w, r, route.productID, productReadPermission, false); !ok {
		return
	}
	if r.URL.RawQuery != "" {
		httpx.Error(w, r, http.StatusBadRequest, "product_application.invalid_query", "query parameters are not supported")
		return
	}
	items, err := h.service.ListApplications(r.Context(), h.productContext(route.productID))
	if err != nil {
		writeError(w, r, err)
		return
	}
	result := applicationListResponse{Items: make([]applicationSummary, len(items))}
	for i := range items {
		if items[i].ProductID != route.productID || !validIdentifier(items[i].ApplicationID) {
			httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		result.Items[i] = summarize(items[i])
	}
	httpx.JSON(w, http.StatusOK, result)
}

type createApplicationRequest struct {
	ApplicationCode     string                          `json:"application_code"`
	Name                string                          `json:"name"`
	Platform            productapplication.Platform     `json:"platform"`
	DistributionChannel string                          `json:"distribution_channel"`
	ReleaseTrack        productapplication.ReleaseTrack `json:"release_track"`
	Status              productapplication.Status       `json:"status"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, route parsedRoute) {
	principal, ok := h.authorize(w, r, route.productID, applicationManagePermission, false)
	if !ok {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body createApplicationRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	created, err := h.service.CreateApplication(r.Context(), productapplication.CreateCommand{
		Product: h.productContext(route.productID), ApplicationCode: body.ApplicationCode, Name: body.Name,
		Platform: body.Platform, DistributionChannel: body.DistributionChannel, ReleaseTrack: body.ReleaseTrack,
		Status: body.Status, ActorID: principal.AdminUserID, TraceID: requestid.FromContext(r.Context()), IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if created.ProductID != route.productID || !validIdentifier(created.ApplicationID) || created.AuditID == "" {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	httpx.JSON(w, http.StatusCreated, applicationResponseFrom(created))
}

type redirectPolicyRequest struct {
	WebRedirectURIs []string                          `json:"web_redirect_uris"`
	AllowedOrigins  []string                          `json:"allowed_origins"`
	DeepLinks       []productapplication.DeepLinkRule `json:"deep_links"`
}

func (h *Handler) replaceRedirects(w http.ResponseWriter, r *http.Request, route parsedRoute) {
	principal, ok := h.authorize(w, r, route.productID, applicationSecurityManagePermission, true)
	if !ok {
		return
	}
	var body redirectPolicyRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	version, err := h.service.ReplaceRedirects(r.Context(), productapplication.ReplaceRedirectsCommand{
		Product: h.productContext(route.productID), ApplicationID: route.applicationID,
		Policy:  productapplication.RedirectPolicy{WebRedirectURIs: body.WebRedirectURIs, AllowedOrigins: body.AllowedOrigins, DeepLinks: body.DeepLinks},
		ActorID: principal.AdminUserID, TraceID: requestid.FromContext(r.Context()),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if version.ProductID != route.productID || version.ApplicationID != route.applicationID || version.Version < 1 || version.AuditID == "" {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	httpx.JSON(w, http.StatusOK, versionedAdminResult{Status: "updated", ResourceID: route.applicationID, AuditID: version.AuditID, Version: version.Version})
}

type suspendRequest struct {
	Reason        string                           `json:"reason"`
	SessionPolicy productapplication.SessionPolicy `json:"session_policy"`
}

func (h *Handler) suspend(w http.ResponseWriter, r *http.Request, route parsedRoute) {
	principal, ok := h.authorize(w, r, route.productID, applicationManagePermission, false)
	if !ok {
		return
	}
	idempotencyKey, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var body suspendRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := h.service.SuspendApplication(r.Context(), productapplication.SuspendCommand{
		Product: h.productContext(route.productID), ApplicationID: route.applicationID,
		Reason: body.Reason, SessionPolicy: body.SessionPolicy, ActorID: principal.AdminUserID,
		TraceID: requestid.FromContext(r.Context()), IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	if result.ApplicationID != route.applicationID || result.AuditID == "" {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	httpx.JSON(w, http.StatusOK, adminMutationResult{
		Status: string(result.Status), ResourceID: result.ApplicationID, AuditID: result.AuditID,
		AffectedCount: result.AffectedClientBindings,
		ImpactSummary: map[string]any{"session_policy": result.SessionPolicy, "affected_client_bindings": result.AffectedClientBindings},
	})
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, productID, permission string, highRisk bool) (adminrequest.Principal, bool) {
	return h.guard.Authorize(w, r, permission, adminrequest.TargetScope{Type: "product", ID: productID, ProductID: productID}, highRisk)
}

func (h *Handler) productContext(productID string) productapplication.ProductContext {
	return productapplication.ProductContext{ProductID: productID, Environment: h.config.Environment}
}

type applicationResponse struct {
	ApplicationID       string                          `json:"application_id"`
	ProductID           string                          `json:"product_id"`
	ApplicationCode     string                          `json:"application_code"`
	Name                string                          `json:"name"`
	Platform            productapplication.Platform     `json:"platform"`
	DistributionChannel string                          `json:"distribution_channel"`
	ReleaseTrack        productapplication.ReleaseTrack `json:"release_track"`
	Status              productapplication.Status       `json:"status"`
	CreatedAt           time.Time                       `json:"created_at"`
	AuditID             string                          `json:"audit_id"`
}

type applicationSummary struct {
	ApplicationID       string                          `json:"application_id"`
	ProductID           string                          `json:"product_id"`
	ApplicationCode     string                          `json:"application_code"`
	Name                string                          `json:"name"`
	Platform            productapplication.Platform     `json:"platform"`
	DistributionChannel string                          `json:"distribution_channel"`
	ReleaseTrack        productapplication.ReleaseTrack `json:"release_track"`
	Status              productapplication.Status       `json:"status"`
	ContextVersion      int64                           `json:"context_version"`
	CreatedAt           time.Time                       `json:"created_at"`
	UpdatedAt           time.Time                       `json:"updated_at"`
}

type applicationListResponse struct {
	Items []applicationSummary `json:"items"`
}

type adminMutationResult struct {
	Status        string         `json:"status"`
	ResourceID    string         `json:"resource_id,omitempty"`
	AuditID       string         `json:"audit_id"`
	AffectedCount int64          `json:"affected_count,omitempty"`
	ImpactSummary map[string]any `json:"impact_summary,omitempty"`
}

type versionedAdminResult struct {
	Status     string `json:"status"`
	ResourceID string `json:"resource_id,omitempty"`
	AuditID    string `json:"audit_id"`
	Version    int64  `json:"version"`
}

func applicationResponseFrom(value productapplication.Application) applicationResponse {
	return applicationResponse{ApplicationID: value.ApplicationID, ProductID: value.ProductID, ApplicationCode: value.ApplicationCode, Name: value.Name, Platform: value.Platform, DistributionChannel: value.DistributionChannel, ReleaseTrack: value.ReleaseTrack, Status: value.Status, CreatedAt: value.CreatedAt, AuditID: value.AuditID}
}

func summarize(value productapplication.Application) applicationSummary {
	return applicationSummary{ApplicationID: value.ApplicationID, ProductID: value.ProductID, ApplicationCode: value.ApplicationCode, Name: value.Name, Platform: value.Platform, DistributionChannel: value.DistributionChannel, ReleaseTrack: value.ReleaseTrack, Status: value.Status, ContextVersion: value.ContextVersion, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) < 16 || len(key) > 128 {
		httpx.Error(w, r, http.StatusBadRequest, "product_application.invalid_idempotency_key", "Idempotency-Key must be between 16 and 128 characters")
		return "", false
	}
	return key, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "product_application.invalid_request", "invalid request body")
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		httpx.Error(w, r, http.StatusBadRequest, "product_application.invalid_request", "request body must contain one JSON value")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, productapplication.ErrInvalidArgument):
		httpx.Error(w, r, http.StatusBadRequest, "product_application.invalid_request", "product application request is invalid")
	case errors.Is(err, productapplication.ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "product_application.not_found", "product application was not found")
	case errors.Is(err, productapplication.ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "product_application.conflict", "product application conflicts with an existing resource")
	case errors.Is(err, productapplication.ErrIdempotencyConflict):
		httpx.Error(w, r, http.StatusConflict, "product_application.idempotency_conflict", "Idempotency-Key was reused with a different request")
	case errors.Is(err, productapplication.ErrOperationInProgress):
		httpx.ErrorWithOptions(w, r, http.StatusConflict, "product_application.operation_in_progress", "the idempotent operation is still in progress", httpx.ErrorOptions{Retryable: true})
	case errors.Is(err, productapplication.ErrApplicationSuspended):
		httpx.Error(w, r, http.StatusConflict, "product_application.suspended", "product application is suspended")
	case errors.Is(err, productapplication.ErrContextRejected):
		httpx.Error(w, r, http.StatusForbidden, "product_application.context_rejected", "product application context was rejected")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

type routeKind uint8

const (
	routeUnknown routeKind = iota
	routeApplications
	routeRedirects
	routeSuspend
)

type parsedRoute struct {
	kind          routeKind
	productID     string
	applicationID string
}

func parseRoute(r *http.Request) (parsedRoute, bool) {
	if r.URL.RawPath != "" || strings.Contains(r.URL.EscapedPath(), "%") || r.URL.Path != path.Clean(r.URL.Path) || !strings.HasPrefix(r.URL.Path, productsPrefix) {
		return parsedRoute{}, false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, productsPrefix), "/")
	if len(parts) == 2 && validIdentifier(parts[0]) && parts[1] == "applications" {
		return parsedRoute{kind: routeApplications, productID: parts[0]}, true
	}
	if len(parts) == 4 && validIdentifier(parts[0]) && parts[1] == "applications" && validIdentifier(parts[2]) {
		switch parts[3] {
		case "redirects":
			return parsedRoute{kind: routeRedirects, productID: parts[0], applicationID: parts[2]}, true
		case "suspend":
			return parsedRoute{kind: routeSuspend, productID: parts[0], applicationID: parts[2]}, true
		}
	}
	return parsedRoute{}, false
}

func validIdentifier(value string) bool { return identifierPattern.MatchString(value) }

func validEnvironment(value productapplication.Environment) bool {
	return value == productapplication.EnvironmentLocal || value == productapplication.EnvironmentTest || value == productapplication.EnvironmentProduction
}
