package httptransport

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/workflows/accountuserquery"
)

const (
	readPermission = "identity.user.read"
	usersPrefix    = "/api/v1/admin/users"
	productsPrefix = "/api/v1/admin/products/"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Handler struct {
	service *accountuserquery.Service
	guard   *adminrequest.Guard
}

func New(service *accountuserquery.Service, guard *adminrequest.Guard) *Handler {
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
	if r.Method != http.MethodGet {
		httpx.MethodNotAllowed(w, r, http.MethodGet)
		return
	}
	scopeTarget := adminrequest.TargetScope{Type: string(target.scope.Type), ID: target.scope.ProductID, ProductID: target.scope.ProductID, TenantID: target.scope.TenantID}
	if target.scope.Type == accountuserquery.ScopeTenant {
		scopeTarget.ID = target.scope.TenantID
	}
	if target.scope.Type == accountuserquery.ScopePlatform {
		scopeTarget = adminrequest.TargetScope{Type: "platform"}
	}
	if _, ok := h.guard.Authorize(w, r, readPermission, scopeTarget, false); !ok {
		return
	}
	switch target.kind {
	case "list":
		if err := validateKeys(r.URL.Query(), "query", "account_status", "access_status", "cursor", "page_size"); err != nil {
			writeError(w, r, err)
			return
		}
		page, err := h.service.ListUsers(r.Context(), accountuserquery.ListQuery{Scope: target.scope, Query: strings.TrimSpace(r.URL.Query().Get("query")), AccountStatus: r.URL.Query().Get("account_status"), AccessStatus: r.URL.Query().Get("access_status"), Cursor: r.URL.Query().Get("cursor"), PageSize: parsePageSize(r.URL.Query().Get("page_size"))})
		if err != nil {
			writeError(w, r, err)
			return
		}
		httpx.JSON(w, http.StatusOK, page)
	case "detail":
		if r.URL.RawQuery != "" {
			writeError(w, r, accountuserquery.ErrInvalidFilter)
			return
		}
		detail, err := h.service.GetUser(r.Context(), target.scope, target.userID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		httpx.JSON(w, http.StatusOK, detail)
	case "sessions":
		if err := validateKeys(r.URL.Query(), "cursor", "page_size"); err != nil {
			writeError(w, r, err)
			return
		}
		page, err := h.service.ListSessions(r.Context(), target.scope, target.userID, r.URL.Query().Get("cursor"), parsePageSize(r.URL.Query().Get("page_size")))
		if err != nil {
			writeError(w, r, err)
			return
		}
		httpx.JSON(w, http.StatusOK, page)
	default:
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
	}
}

func parseRoute(path, rawPath string) (route, bool) {
	if rawPath != "" || strings.HasSuffix(path, "/") {
		return route{}, false
	}
	if path == usersPrefix {
		return route{kind: "list", scope: accountuserquery.Scope{Type: accountuserquery.ScopePlatform}}, true
	}
	if strings.HasPrefix(path, usersPrefix+"/") {
		parts := strings.Split(strings.TrimPrefix(path, usersPrefix+"/"), "/")
		if len(parts) == 1 && validID(parts[0]) {
			return route{kind: "detail", scope: accountuserquery.Scope{Type: accountuserquery.ScopePlatform}, userID: parts[0]}, true
		}
		if len(parts) == 2 && parts[1] == "sessions" && validID(parts[0]) {
			return route{kind: "sessions", scope: accountuserquery.Scope{Type: accountuserquery.ScopePlatform}, userID: parts[0]}, true
		}
		return route{}, false
	}
	if !strings.HasPrefix(path, productsPrefix) {
		return route{}, false
	}
	parts := strings.Split(strings.TrimPrefix(path, productsPrefix), "/")
	if len(parts) >= 2 && validID(parts[0]) && parts[1] == "users" {
		productID := parts[0]
		if len(parts) == 2 {
			return route{kind: "list", scope: accountuserquery.Scope{Type: accountuserquery.ScopeProduct, ProductID: productID}}, true
		}
		if len(parts) == 3 && validID(parts[2]) {
			return route{kind: "detail", scope: accountuserquery.Scope{Type: accountuserquery.ScopeProduct, ProductID: productID}, userID: parts[2]}, true
		}
		if len(parts) == 4 && validID(parts[2]) && parts[3] == "sessions" {
			return route{kind: "sessions", scope: accountuserquery.Scope{Type: accountuserquery.ScopeProduct, ProductID: productID}, userID: parts[2]}, true
		}
	}
	if len(parts) >= 4 && validID(parts[0]) && parts[1] == "tenants" && validID(parts[2]) && parts[3] == "users" {
		scope := accountuserquery.Scope{Type: accountuserquery.ScopeTenant, ProductID: parts[0], TenantID: parts[2]}
		if len(parts) == 4 {
			return route{kind: "list", scope: scope}, true
		}
		if len(parts) == 5 && validID(parts[4]) {
			return route{kind: "detail", scope: scope, userID: parts[4]}, true
		}
		if len(parts) == 6 && validID(parts[4]) && parts[5] == "sessions" {
			return route{kind: "sessions", scope: scope, userID: parts[4]}, true
		}
	}
	return route{}, false
}

func validateKeys(values map[string][]string, allowed ...string) error {
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := set[key]; !ok || len(entries) != 1 {
			return accountuserquery.ErrInvalidFilter
		}
	}
	return nil
}

func parsePageSize(raw string) int {
	if raw == "" {
		return 20
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return value
}

func validID(value string) bool { return identifierPattern.MatchString(value) }

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, accountuserquery.ErrInvalidFilter):
		httpx.Error(w, r, http.StatusBadRequest, "account_admin.invalid_filter", "account query filter is invalid")
	case errors.Is(err, accountuserquery.ErrInvalidCursor):
		httpx.Error(w, r, http.StatusBadRequest, "account_admin.invalid_cursor", "account query cursor is invalid")
	case errors.Is(err, accountuserquery.ErrScopedUserNotFound):
		httpx.Error(w, r, http.StatusNotFound, "account_admin.scoped_user_not_found", "user is not present in this scope")
	case errors.Is(err, accountuserquery.ErrCapabilityNotEnabled):
		httpx.Error(w, r, http.StatusForbidden, "account_admin.capability_not_enabled", "account capability is not enabled")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		httpx.Error(w, r, http.StatusServiceUnavailable, "account_admin.retryable_dependency", "account query is temporarily unavailable")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
