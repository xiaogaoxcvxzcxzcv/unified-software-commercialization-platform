package audit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

const (
	AuditReadPermission = "audit.read"
	DefaultPageLimit    = 50
	MaxPageLimit        = 200
)

var (
	ErrForbidden     = errors.New("audit query forbidden")
	ErrInvalidCursor = errors.New("invalid audit query cursor")
	ErrInvalidLimit  = errors.New("invalid audit query limit")
	ErrInvalidScope  = errors.New("invalid audit query scope")
)

type Event struct {
	AuditID         string         `json:"audit_id"`
	OccurredAt      time.Time      `json:"occurred_at"`
	ActorID         string         `json:"actor_id"`
	Permission      string         `json:"permission,omitempty"`
	ScopeType       string         `json:"scope_type,omitempty"`
	ScopeID         string         `json:"scope_id,omitempty"`
	ProductID       string         `json:"product_id,omitempty"`
	TenantID        string         `json:"tenant_id,omitempty"`
	Action          string         `json:"action"`
	TargetType      string         `json:"target_type"`
	TargetID        string         `json:"target_id"`
	Result          string         `json:"result"`
	ReasonCode      string         `json:"reason_code,omitempty"`
	TraceID         string         `json:"trace_id"`
	RiskLevel       string         `json:"risk_level"`
	RedactedSummary map[string]any `json:"redacted_summary,omitempty"`
}

type Repository interface {
	Append(context.Context, Event) error
}

type Scope struct {
	Type      string
	ID        string
	ProductID string
	TenantID  string
}

type AdminContext struct {
	AdminUserID string
	SessionID   string
	TargetScope Scope
}

type AuthorizationCommand struct {
	AdminUserID string
	SessionID   string
	Permission  string
	TargetScope Scope
}

type AuthorizationDecision struct {
	Allowed    bool
	ReasonCode string
}

type Authorizer interface {
	AuthorizeAdmin(context.Context, AuthorizationCommand) (AuthorizationDecision, error)
}

type PagePosition struct {
	OccurredAt time.Time
	AuditID    string
}

type RepositoryQuery struct {
	TraceID     string
	TargetScope Scope
	After       *PagePosition
	Limit       int
}

type QueryRepository interface {
	Query(context.Context, RepositoryQuery) ([]Event, error)
}

type SearchCommand struct {
	AdminUserID string
	SessionID   string
	TargetScope Scope
	TraceID     string
	Limit       int
	Cursor      string
}

type RedactedEvent struct {
	AuditID         string         `json:"audit_id"`
	OccurredAt      time.Time      `json:"occurred_at"`
	ActorID         string         `json:"actor_id"`
	Permission      string         `json:"permission,omitempty"`
	ScopeType       string         `json:"scope_type,omitempty"`
	ScopeID         string         `json:"scope_id,omitempty"`
	ProductID       string         `json:"product_id,omitempty"`
	TenantID        string         `json:"tenant_id,omitempty"`
	Action          string         `json:"action"`
	TargetType      string         `json:"target_type"`
	TargetID        string         `json:"target_id"`
	Result          string         `json:"result"`
	ReasonCode      string         `json:"reason_code,omitempty"`
	TraceID         string         `json:"trace_id"`
	RiskLevel       string         `json:"risk_level"`
	RedactedSummary map[string]any `json:"redacted_summary,omitempty"`
}

type Page struct {
	Items      []RedactedEvent `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

type QueryService struct {
	repository QueryRepository
	authorizer Authorizer
}

func NewQueryService(repository QueryRepository, authorizer Authorizer) *QueryService {
	return &QueryService{repository: repository, authorizer: authorizer}
}

func (s *QueryService) SearchAuditEvents(ctx context.Context, command SearchCommand) (Page, error) {
	if s.repository == nil || s.authorizer == nil {
		return Page{}, errors.New("audit query service is not configured")
	}
	if command.AdminUserID == "" || command.SessionID == "" {
		return Page{}, ErrForbidden
	}
	scope, err := normalizeScope(command.TargetScope)
	if err != nil {
		return Page{}, err
	}
	limit := command.Limit
	if limit == 0 {
		limit = DefaultPageLimit
	}
	if limit < 1 || limit > MaxPageLimit {
		return Page{}, ErrInvalidLimit
	}
	var after *PagePosition
	if command.Cursor != "" {
		position, err := decodeCursor(command.Cursor)
		if err != nil {
			return Page{}, err
		}
		after = &position
	}
	decision, err := s.authorizer.AuthorizeAdmin(ctx, AuthorizationCommand{
		AdminUserID: command.AdminUserID,
		SessionID:   command.SessionID,
		Permission:  AuditReadPermission,
		TargetScope: scope,
	})
	if err != nil {
		return Page{}, err
	}
	if !decision.Allowed {
		return Page{}, ErrForbidden
	}
	events, err := s.repository.Query(ctx, RepositoryQuery{
		TraceID:     strings.TrimSpace(command.TraceID),
		TargetScope: scope,
		After:       after,
		Limit:       limit + 1,
	})
	if err != nil {
		return Page{}, err
	}
	hasNext := len(events) > limit
	if hasNext {
		events = events[:limit]
	}
	page := Page{Items: make([]RedactedEvent, len(events))}
	for i := range events {
		page.Items[i] = redactEvent(events[i])
	}
	if hasNext && len(events) != 0 {
		page.NextCursor, err = encodeCursor(PagePosition{OccurredAt: events[len(events)-1].OccurredAt, AuditID: events[len(events)-1].AuditID})
		if err != nil {
			return Page{}, err
		}
	}
	return page, nil
}

func normalizeScope(scope Scope) (Scope, error) {
	switch scope.Type {
	case "platform":
		return Scope{Type: "platform"}, nil
	case "product":
		if scope.ProductID == "" {
			scope.ProductID = scope.ID
		}
		if scope.ProductID == "" {
			return Scope{}, ErrInvalidScope
		}
		return Scope{Type: "product", ID: scope.ProductID, ProductID: scope.ProductID}, nil
	case "tenant":
		if scope.TenantID == "" {
			scope.TenantID = scope.ID
		}
		if scope.ProductID == "" || scope.TenantID == "" {
			return Scope{}, ErrInvalidScope
		}
		return Scope{Type: "tenant", ID: scope.TenantID, ProductID: scope.ProductID, TenantID: scope.TenantID}, nil
	default:
		return Scope{}, ErrInvalidScope
	}
}

type cursorPayload struct {
	Version    int    `json:"v"`
	OccurredAt string `json:"occurred_at"`
	AuditID    string `json:"audit_id"`
}

func encodeCursor(position PagePosition) (string, error) {
	if position.OccurredAt.IsZero() || position.AuditID == "" {
		return "", ErrInvalidCursor
	}
	payload, err := json.Marshal(cursorPayload{Version: 1, OccurredAt: position.OccurredAt.UTC().Format(time.RFC3339Nano), AuditID: position.AuditID})
	if err != nil {
		return "", fmt.Errorf("encode audit cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeCursor(value string) (PagePosition, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return PagePosition{}, ErrInvalidCursor
	}
	var cursor cursorPayload
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Version != 1 || cursor.AuditID == "" {
		return PagePosition{}, ErrInvalidCursor
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, cursor.OccurredAt)
	if err != nil {
		return PagePosition{}, ErrInvalidCursor
	}
	return PagePosition{OccurredAt: occurredAt.UTC(), AuditID: cursor.AuditID}, nil
}

func redactEvent(event Event) RedactedEvent {
	return RedactedEvent{
		AuditID: event.AuditID, OccurredAt: event.OccurredAt, ActorID: event.ActorID,
		Permission: event.Permission, ScopeType: event.ScopeType, ScopeID: event.ScopeID,
		ProductID: event.ProductID, TenantID: event.TenantID, Action: event.Action,
		TargetType: event.TargetType, TargetID: event.TargetID, Result: event.Result,
		ReasonCode: event.ReasonCode, TraceID: event.TraceID, RiskLevel: event.RiskLevel,
		RedactedSummary: redactMap(event.RedactedSummary),
	}
}

func redactMap(source map[string]any) map[string]any {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		if sensitiveSummaryKey(key) {
			continue
		}
		result[key] = redactValue(value)
	}
	return result
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return redactMap(typed)
	case []any:
		result := make([]any, len(typed))
		for i := range typed {
			result[i] = redactValue(typed[i])
		}
		return result
	default:
		return typed
	}
}

func sensitiveSummaryKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(key))
	for _, fragment := range []string{"password", "passwd", "credential", "cookie", "secret", "api_key", "private_key", "token", "csrf", "proof"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	if normalized == "authorization" || normalized == "authorization_header" {
		return true
	}
	return false
}

type Service struct{ repository Repository }

func NewService(repository Repository) *Service { return &Service{repository: repository} }

func (s *Service) AppendAuditEvent(ctx context.Context, event Event) (string, error) {
	if event.AuditID == "" {
		id, err := securevalue.ID("aud_")
		if err != nil {
			return "", err
		}
		event.AuditID = id
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if event.RiskLevel == "" {
		event.RiskLevel = "normal"
	}
	if err := s.repository.Append(ctx, event); err != nil {
		return "", err
	}
	return event.AuditID, nil
}
