package httptransport

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/audit"
	"platform.local/capability-platform/backend/internal/platform/httpx"
)

const auditEventsPath = "/api/v1/admin/audit/events"

var ErrAdminContextUnavailable = errors.New("administrator context unavailable")

type AdminContextResolver interface {
	ResolveAdminContext(context.Context, *http.Request) (audit.AdminContext, error)
}

type Service interface {
	SearchAuditEvents(context.Context, audit.SearchCommand) (audit.Page, error)
}

type DetailService interface {
	GetAuditEvent(context.Context, audit.GetEventCommand) (audit.RedactedEvent, error)
}

type AdminIdentityResolver interface {
	ResolveAdminIdentity(context.Context, *http.Request) (audit.AdminContext, error)
}

type Handler struct {
	service  Service
	resolver AdminContextResolver
}

func New(service Service, resolver AdminContextResolver) *Handler {
	return &Handler{service: service, resolver: resolver}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != auditEventsPath {
		if strings.HasPrefix(r.URL.Path, auditEventsPath+"/") {
			h.serveDetail(w, r)
			return
		}
		httpx.Error(w, r, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	if r.Method != http.MethodGet {
		httpx.MethodNotAllowed(w, r, http.MethodGet)
		return
	}
	if h.service == nil || h.resolver == nil {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	query := r.URL.Query()
	traceID := strings.TrimSpace(query.Get("trace_id"))
	if len(traceID) > 256 {
		httpx.Error(w, r, http.StatusBadRequest, "audit.invalid_query", "trace_id is too long")
		return
	}
	limit, ok := parseLimit(query.Get("limit"))
	if !ok {
		httpx.Error(w, r, http.StatusBadRequest, "audit.invalid_limit", "limit must be a positive integer within the supported range")
		return
	}
	cursor := strings.TrimSpace(query.Get("cursor"))
	if len(cursor) > 2048 {
		httpx.Error(w, r, http.StatusBadRequest, "audit.invalid_cursor", "cursor is invalid")
		return
	}
	admin, err := h.resolver.ResolveAdminContext(r.Context(), r)
	if err != nil {
		httpx.Error(w, r, http.StatusUnauthorized, "audit.unauthenticated", "administrator authentication required")
		return
	}
	page, err := h.service.SearchAuditEvents(r.Context(), audit.SearchCommand{
		AdminUserID: admin.AdminUserID,
		SessionID:   admin.SessionID,
		TargetScope: admin.TargetScope,
		TraceID:     traceID,
		Limit:       limit,
		Cursor:      cursor,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, page)
}

func (h *Handler) serveDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.MethodNotAllowed(w, r, http.MethodGet)
		return
	}
	auditID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, auditEventsPath+"/"))
	if auditID == "" || strings.Contains(auditID, "/") || len(auditID) > 160 {
		httpx.Error(w, r, http.StatusNotFound, "audit.event_not_found", "audit event not found")
		return
	}
	detail, detailOK := h.service.(DetailService)
	identity, identityOK := h.resolver.(AdminIdentityResolver)
	if !detailOK || !identityOK {
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	admin, err := identity.ResolveAdminIdentity(r.Context(), r)
	if err != nil {
		httpx.Error(w, r, http.StatusUnauthorized, "audit.unauthenticated", "administrator authentication required")
		return
	}
	event, err := detail.GetAuditEvent(r.Context(), audit.GetEventCommand{AdminUserID: admin.AdminUserID, SessionID: admin.SessionID, AuditID: auditID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, event)
}

func parseLimit(raw string) (int, bool) {
	if raw == "" {
		return 0, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > audit.MaxPageLimit {
		return 0, false
	}
	return limit, true
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, audit.ErrForbidden):
		httpx.Error(w, r, http.StatusForbidden, "audit.forbidden", "audit events are not available in this scope")
	case errors.Is(err, audit.ErrInvalidCursor):
		httpx.Error(w, r, http.StatusBadRequest, "audit.invalid_cursor", "cursor is invalid")
	case errors.Is(err, audit.ErrInvalidLimit):
		httpx.Error(w, r, http.StatusBadRequest, "audit.invalid_limit", "limit is outside the supported range")
	case errors.Is(err, audit.ErrInvalidScope):
		httpx.Error(w, r, http.StatusBadRequest, "audit.invalid_scope", "audit scope is invalid")
	case errors.Is(err, audit.ErrEventNotFound):
		httpx.Error(w, r, http.StatusNotFound, "audit.event_not_found", "audit event not found")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
