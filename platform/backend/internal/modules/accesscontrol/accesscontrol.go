package accesscontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

var (
	ErrNoActiveScope        = errors.New("no active administrator scope")
	ErrInvalidScopeBinding  = errors.New("invalid administrator scope binding")
	ErrRoleNotFound         = errors.New("active administrator role not found")
	ErrScopeBindingConflict = errors.New("administrator scope binding conflict")
	ErrIdempotencyConflict  = errors.New("administrator scope binding idempotency conflict")
	ErrOperationInProgress  = errors.New("administrator scope binding operation in progress")
	stableRoleCodePattern   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	scopeIdentifierPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

type Scope struct {
	Type      string `json:"scope_type"`
	ID        string `json:"scope_id,omitempty"`
	ProductID string `json:"product_id,omitempty"`
	TenantID  string `json:"tenant_id,omitempty"`
}

type Snapshot struct {
	AuthorizationVersion     int64    `json:"authorization_version"`
	Roles                    []string `json:"-"`
	Permissions              []string `json:"permissions"`
	Scopes                   []Scope  `json:"scopes"`
	ReauthenticationRequired bool     `json:"reauthentication_required"`
}

type TargetScope struct {
	Type      string
	ID        string
	ProductID string
	TenantID  string
}

type Decision struct {
	Allowed                  bool
	MatchedScope             *Scope
	ReasonCode               string
	ReauthenticationRequired bool
}

type Repository interface {
	ResolveSnapshot(context.Context, string, time.Time) (Snapshot, error)
	BootstrapPlatformAdmin(context.Context, BootstrapCommand, PermissionCatalog) error
	BindAdminScope(context.Context, ScopeBindingRecord) (AdminScopeBinding, error)
	ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error)
	MarkOutboxPublished(context.Context, string, time.Time) error
	MarkOutboxFailed(context.Context, string, string, time.Time, bool) error
}

type BootstrapCommand struct {
	BindingID   string
	RoleID      string
	AdminUserID string
	Now         time.Time
}

type Service struct {
	repository Repository
	catalog    PermissionCatalog
	now        func() time.Time
	ids        IDGenerator
}

func NewService(repository Repository, now func() time.Time) *Service {
	return NewServiceWithGenerator(repository, now, securevalue.DefaultGenerator())
}

type IDGenerator interface {
	ID(string) (string, error)
}

func NewServiceWithGenerator(repository Repository, now func() time.Time, ids IDGenerator) *Service {
	if now == nil {
		now = time.Now
	}
	if ids == nil {
		ids = securevalue.DefaultGenerator()
	}
	return &Service{repository: repository, catalog: CurrentPermissionCatalog(), now: now, ids: ids}
}

func (s *Service) ResolveAdminAccessSnapshot(ctx context.Context, adminUserID, _ string) (Snapshot, error) {
	snapshot, err := s.repository.ResolveSnapshot(ctx, adminUserID, s.now().UTC())
	if err != nil {
		return Snapshot{}, err
	}
	if len(snapshot.Scopes) == 0 || len(snapshot.Permissions) == 0 {
		return Snapshot{}, ErrNoActiveScope
	}
	if err := s.catalog.ValidateRequiredPermissions(snapshot.Permissions); err != nil {
		return Snapshot{}, err
	}
	sort.Strings(snapshot.Roles)
	sort.Strings(snapshot.Permissions)
	return snapshot, nil
}

func (s *Service) AuthorizeAdmin(ctx context.Context, adminUserID, sessionID, permission string, target TargetScope) (Decision, error) {
	if !s.catalog.Contains(permission) {
		return Decision{ReasonCode: "unknown_permission"}, nil
	}
	snapshot, err := s.ResolveAdminAccessSnapshot(ctx, adminUserID, sessionID)
	if err != nil {
		return Decision{}, err
	}
	if !contains(snapshot.Permissions, permission) {
		return Decision{ReasonCode: "permission_missing"}, nil
	}
	for i := range snapshot.Scopes {
		if scopeMatches(snapshot.Scopes[i], target) {
			matched := snapshot.Scopes[i]
			return Decision{Allowed: true, MatchedScope: &matched, ReauthenticationRequired: snapshot.ReauthenticationRequired}, nil
		}
	}
	return Decision{ReasonCode: "scope_mismatch"}, nil
}

func (s *Service) BootstrapPlatformAdmin(ctx context.Context, command BootstrapCommand) error {
	return s.repository.BootstrapPlatformAdmin(ctx, command, s.catalog)
}

type BindAdminScopeCommand struct {
	AdminUserID    string
	RoleCode       string
	Scope          Scope
	EffectiveFrom  time.Time
	ExpiresAt      *time.Time
	ActorID        string
	TraceID        string
	IdempotencyKey string
}

type AdminScopeBinding struct {
	BindingID            string     `json:"binding_id"`
	AdminUserID          string     `json:"admin_user_id"`
	RoleCode             string     `json:"role_code"`
	Scope                Scope      `json:"scope"`
	EffectiveFrom        time.Time  `json:"effective_from"`
	ExpiresAt            *time.Time `json:"expires_at,omitempty"`
	AuthorizationVersion int64      `json:"authorization_version"`
	AuditID              string     `json:"audit_id"`
	CreatedAt            time.Time  `json:"created_at"`
}

type ScopeBindingRecord struct {
	Binding     AdminScopeBinding
	ActorID     string
	TraceID     string
	EventID     string
	Idempotency ScopeBindingIdempotency
}

type ScopeBindingIdempotency struct {
	Operation     string
	ActorID       string
	KeyDigest     string
	RequestDigest string
	Now           time.Time
}

type ClaimedOutboxEvent struct {
	EventID      string
	AggregateID  string
	EventType    string
	Payload      json.RawMessage
	AttemptCount int
}

func (s *Service) BindAdminScope(ctx context.Context, command BindAdminScopeCommand) (AdminScopeBinding, error) {
	if s == nil || s.repository == nil || s.ids == nil {
		return AdminScopeBinding{}, fmt.Errorf("access control service is not configured")
	}
	command.AdminUserID = strings.TrimSpace(command.AdminUserID)
	command.RoleCode = strings.TrimSpace(command.RoleCode)
	command.ActorID = strings.TrimSpace(command.ActorID)
	command.TraceID = strings.TrimSpace(command.TraceID)
	scope, err := normalizeBindingScope(command.Scope)
	if err != nil || !scopeIdentifierPattern.MatchString(command.AdminUserID) || !stableRoleCodePattern.MatchString(command.RoleCode) || !scopeIdentifierPattern.MatchString(command.ActorID) || command.TraceID == "" || len(command.TraceID) > 128 || len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 128 {
		return AdminScopeBinding{}, ErrInvalidScopeBinding
	}
	now := s.now().UTC()
	effectiveFrom := command.EffectiveFrom.UTC()
	if command.EffectiveFrom.IsZero() {
		effectiveFrom = now
	}
	var expiresAt *time.Time
	if command.ExpiresAt != nil {
		value := command.ExpiresAt.UTC()
		if !value.After(effectiveFrom) {
			return AdminScopeBinding{}, ErrInvalidScopeBinding
		}
		expiresAt = &value
	}
	bindingID, err := s.ids.ID("scopebind_")
	if err != nil {
		return AdminScopeBinding{}, err
	}
	auditID, err := s.ids.ID("audit_")
	if err != nil {
		return AdminScopeBinding{}, err
	}
	eventID, err := s.ids.ID("evt_")
	if err != nil {
		return AdminScopeBinding{}, err
	}
	requestDigest, err := scopeBindingRequestDigest(command.AdminUserID, command.RoleCode, scope, effectiveFrom, expiresAt)
	if err != nil {
		return AdminScopeBinding{}, err
	}
	binding := AdminScopeBinding{BindingID: bindingID, AdminUserID: command.AdminUserID, RoleCode: command.RoleCode, Scope: scope, EffectiveFrom: effectiveFrom, ExpiresAt: expiresAt, AuditID: auditID, CreatedAt: now}
	return s.repository.BindAdminScope(ctx, ScopeBindingRecord{Binding: binding, ActorID: command.ActorID, TraceID: command.TraceID, EventID: eventID, Idempotency: ScopeBindingIdempotency{Operation: "bind_admin_scope", ActorID: command.ActorID, KeyDigest: sha256Hex(command.IdempotencyKey), RequestDigest: requestDigest, Now: now}})
}

func (s *Service) ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]ClaimedOutboxEvent, error) {
	if s == nil || s.repository == nil || now.IsZero() || limit < 1 || limit > 200 {
		return nil, ErrInvalidScopeBinding
	}
	return s.repository.ClaimOutbox(ctx, now.UTC(), limit)
}

func (s *Service) MarkOutboxPublished(ctx context.Context, eventID string, now time.Time) error {
	if s == nil || s.repository == nil || strings.TrimSpace(eventID) == "" || now.IsZero() {
		return ErrInvalidScopeBinding
	}
	return s.repository.MarkOutboxPublished(ctx, eventID, now.UTC())
}

func (s *Service) MarkOutboxFailed(ctx context.Context, eventID, summary string, next time.Time, dead bool) error {
	if s == nil || s.repository == nil || strings.TrimSpace(eventID) == "" || strings.TrimSpace(summary) == "" || next.IsZero() {
		return ErrInvalidScopeBinding
	}
	if len(summary) > 500 {
		summary = summary[:500]
	}
	return s.repository.MarkOutboxFailed(ctx, eventID, summary, next.UTC(), dead)
}

func normalizeBindingScope(scope Scope) (Scope, error) {
	switch scope.Type {
	case "platform":
		// Platform scope is deliberately reserved for the controlled bootstrap
		// flow until a caller authorization context is part of this command.
		return Scope{}, ErrInvalidScopeBinding
	case "product":
		productID := scope.ProductID
		if productID == "" {
			productID = scope.ID
		}
		if !scopeIdentifierPattern.MatchString(productID) || scope.TenantID != "" || (scope.ID != "" && scope.ID != productID) {
			return Scope{}, ErrInvalidScopeBinding
		}
		return Scope{Type: "product", ID: productID, ProductID: productID}, nil
	case "tenant":
		tenantID := scope.TenantID
		if tenantID == "" {
			tenantID = scope.ID
		}
		if !scopeIdentifierPattern.MatchString(scope.ProductID) || !scopeIdentifierPattern.MatchString(tenantID) || (scope.ID != "" && scope.ID != tenantID) {
			return Scope{}, ErrInvalidScopeBinding
		}
		return Scope{Type: "tenant", ID: tenantID, ProductID: scope.ProductID, TenantID: tenantID}, nil
	default:
		return Scope{}, ErrInvalidScopeBinding
	}
}

func scopeBindingRequestDigest(adminUserID, roleCode string, scope Scope, effectiveFrom time.Time, expiresAt *time.Time) (string, error) {
	raw, err := json.Marshal(struct {
		AdminUserID   string     `json:"admin_user_id"`
		RoleCode      string     `json:"role_code"`
		Scope         Scope      `json:"scope"`
		EffectiveFrom time.Time  `json:"effective_from"`
		ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	}{adminUserID, roleCode, scope, effectiveFrom, expiresAt})
	if err != nil {
		return "", err
	}
	return sha256Hex(string(raw)), nil
}

func sha256Hex(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func scopeMatches(scope Scope, target TargetScope) bool {
	if scope.Type == "platform" {
		return true
	}
	if scope.Type == "product" {
		return target.ProductID == scope.ProductID || (target.Type == "product" && target.ID == scope.ID)
	}
	return scope.Type == "tenant" && target.TenantID == scope.TenantID && target.ProductID == scope.ProductID
}
