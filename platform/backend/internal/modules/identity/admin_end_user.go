package identity

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

const AdminUserMaxPageSize = 100

var (
	ErrAdminEndUserInvalidArgument = errors.New("invalid administrator end-user request")
	ErrAdminEndUserNotFound        = errors.New("administrator end-user not found")
	ErrAdminEndUserVersionConflict = errors.New("administrator end-user version conflict")
	ErrAdminEndUserIdempotency     = errors.New("administrator end-user idempotency conflict")
	adminReasonPattern             = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
)

type AdminUserScopeType string

const (
	AdminUserScopePlatform AdminUserScopeType = "platform"
	AdminUserScopeProduct  AdminUserScopeType = "product"
	AdminUserScopeTenant   AdminUserScopeType = "tenant"
)

// AdminUserScope is resolved and authorized by the composition layer.
type AdminUserScope struct {
	Type      AdminUserScopeType
	ProductID string
	TenantID  string
}

type AdminMaskedIdentifier struct {
	Type        IdentifierType `json:"type"`
	MaskedValue string         `json:"masked_value"`
	Verified    bool           `json:"verified"`
}

type AdminUserProfile struct {
	UserID      string  `json:"user_id"`
	Version     int64   `json:"version"`
	DisplayName string  `json:"display_name"`
	AvatarRef   *string `json:"avatar_ref"`
	Locale      *string `json:"locale"`
	Timezone    *string `json:"timezone"`
}

type AdminUserRecord struct {
	Position           string                  `json:"-"`
	UserID             string                  `json:"user_id"`
	UserVersion        int64                   `json:"user_version"`
	AccountStatus      string                  `json:"account_status"`
	DisplayName        string                  `json:"display_name"`
	Identifiers        []AdminMaskedIdentifier `json:"identifiers"`
	Profile            AdminUserProfile        `json:"profile"`
	CreatedAt          time.Time               `json:"created_at"`
	MemberSince        *time.Time              `json:"member_since"`
	LastSeenAt         *time.Time              `json:"last_seen_at"`
	ActiveSessionCount int                     `json:"active_session_count"`
	TotalSessionCount  int                     `json:"total_session_count"`
}

type AdminUserQuery struct {
	Scope         AdminUserScope
	Query         string
	AccountStatus string
	AfterUserID   string
	Limit         int
}

type AdminUserPage struct {
	Items           []AdminUserRecord
	NextAfterUserID string
}

type AdminUserRepositoryQuery struct {
	Scope            AdminUserScope
	Text             string
	IdentifierType   IdentifierType
	IdentifierDigest []byte
	AccountStatus    string
	AfterUserID      string
	Limit            int
}

type AdminUserSessionPosition struct {
	CreatedAt time.Time
	SessionID string
}

type AdminUserSessionRecord struct {
	Position             AdminUserSessionPosition
	SessionID            string
	ProductID            string
	ApplicationID        string
	TenantID             *string
	Environment          *string
	AuthenticationMethod string
	DeviceLabel          *string
	CreatedAt            time.Time
	LastSeenAt           time.Time
	ExpiresAt            time.Time
	RevokedAt            *time.Time
}

type AdminUserSessionQuery struct {
	Scope  AdminUserScope
	UserID string
	After  *AdminUserSessionPosition
	Limit  int
}

type AdminUserSessionPage struct {
	Items []AdminUserSessionRecord
	Next  *AdminUserSessionPosition
}

type AdminGlobalSecurityStatusCommand struct {
	UserID          string
	Status          string
	ExpectedVersion int64
	ReasonCode      string
	OperatorNote    string
	IdempotencyKey  string
	ActorID         string
	TraceID         string
}

type AdminGlobalSecurityStatusResult struct {
	UserID  string `json:"user_id"`
	Status  string `json:"status"`
	Version int64  `json:"version"`
	AuditID string `json:"audit_id"`
}

type AdminGlobalSecurityStatusRecord struct {
	AdminGlobalSecurityStatusCommand
	ActorDigest   []byte
	KeyDigest     []byte
	RequestDigest []byte
	OutboxEvent   OutboxEvent
	Now           time.Time
}

type AdminSessionRevocationCommand struct {
	Scope          AdminUserScope
	UserID         string
	SessionIDs     []string
	AllActive      bool
	ReasonCode     string
	IdempotencyKey string
	ActorID        string
	TraceID        string
}

type AdminSessionRevocationResult struct {
	UserID       string             `json:"user_id"`
	ScopeType    AdminUserScopeType `json:"scope_type"`
	ScopeID      *string            `json:"scope_id"`
	RevokedCount int                `json:"revoked_count"`
	AuditID      string             `json:"audit_id"`
}

type AdminSessionRevocationRecord struct {
	AdminSessionRevocationCommand
	ActorDigest   []byte
	KeyDigest     []byte
	RequestDigest []byte
	OutboxEvent   OutboxEvent
	Now           time.Time
}

type AdminEndUserRepository interface {
	ListAdminUsers(context.Context, AdminUserRepositoryQuery) ([]AdminUserRecord, error)
	GetAdminUser(context.Context, AdminUserScope, string) (AdminUserRecord, error)
	ResolveAdminUserMemberships(context.Context, AdminUserScope, []string) (map[string]bool, error)
	ListAdminUserSessions(context.Context, AdminUserSessionQuery) ([]AdminUserSessionRecord, error)
	SetGlobalUserSecurityStatus(context.Context, AdminGlobalSecurityStatusRecord) (AdminGlobalSecurityStatusResult, error)
	RevokeAdminUserSessions(context.Context, AdminSessionRevocationRecord) (AdminSessionRevocationResult, error)
}

type AdminEndUserIDGenerator interface {
	ID(string) (string, error)
}

type AdminEndUserService struct {
	repository AdminEndUserRepository
	normalizer IdentifierNormalizer
	hasher     securevalue.Hasher
	ids        AdminEndUserIDGenerator
	now        func() time.Time
}

func NewAdminEndUserService(repository AdminEndUserRepository, normalizer IdentifierNormalizer, hasher securevalue.Hasher, ids AdminEndUserIDGenerator, now func() time.Time) (*AdminEndUserService, error) {
	if repository == nil || normalizer == nil || !hasher.Configured() || ids == nil {
		return nil, ErrAdminEndUserInvalidArgument
	}
	if now == nil {
		now = time.Now
	}
	return &AdminEndUserService{repository: repository, normalizer: normalizer, hasher: hasher, ids: ids, now: now}, nil
}

func (s *AdminEndUserService) ListUsers(ctx context.Context, query AdminUserQuery) (AdminUserPage, error) {
	if s == nil || !validAdminUserScope(query.Scope) || !validAdminQuery(query.Query) || !validAdminAccountStatusFilter(query.AccountStatus) || (query.AfterUserID != "" && !validAdminValue(query.AfterUserID, 160)) || query.Limit < 1 || query.Limit > AdminUserMaxPageSize {
		return AdminUserPage{}, ErrAdminEndUserInvalidArgument
	}
	repositoryQuery := AdminUserRepositoryQuery{Scope: query.Scope, Text: query.Query, AccountStatus: query.AccountStatus, AfterUserID: query.AfterUserID, Limit: query.Limit + 1}
	s.addIdentifierSearch(&repositoryQuery)
	items, err := s.repository.ListAdminUsers(ctx, repositoryQuery)
	if err != nil {
		return AdminUserPage{}, err
	}
	page := AdminUserPage{Items: items}
	if len(page.Items) > query.Limit {
		page.Items = page.Items[:query.Limit]
		page.NextAfterUserID = page.Items[len(page.Items)-1].UserID
	}
	for index := range page.Items {
		page.Items[index].Position = page.Items[index].UserID
		if page.Items[index].Identifiers == nil {
			page.Items[index].Identifiers = []AdminMaskedIdentifier{}
		}
	}
	return page, nil
}

func (s *AdminEndUserService) GetUser(ctx context.Context, scope AdminUserScope, userID string) (AdminUserRecord, error) {
	if s == nil || !validAdminUserScope(scope) || !validAdminValue(userID, 160) {
		return AdminUserRecord{}, ErrAdminEndUserInvalidArgument
	}
	value, err := s.repository.GetAdminUser(ctx, scope, userID)
	if err != nil {
		return AdminUserRecord{}, err
	}
	value.Position = value.UserID
	if value.Identifiers == nil {
		value.Identifiers = []AdminMaskedIdentifier{}
	}
	return value, nil
}

func (s *AdminEndUserService) IsMember(ctx context.Context, scope AdminUserScope, userID string) (bool, error) {
	values, err := s.ResolveMemberships(ctx, scope, []string{userID})
	if err != nil {
		return false, err
	}
	return values[userID], nil
}

func (s *AdminEndUserService) ResolveMemberships(ctx context.Context, scope AdminUserScope, userIDs []string) (map[string]bool, error) {
	if s == nil || !validAdminUserScope(scope) || len(userIDs) < 1 || len(userIDs) > 200 {
		return nil, ErrAdminEndUserInvalidArgument
	}
	seen := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if !validAdminValue(userID, 160) {
			return nil, ErrAdminEndUserInvalidArgument
		}
		if _, duplicate := seen[userID]; duplicate {
			return nil, ErrAdminEndUserInvalidArgument
		}
		seen[userID] = struct{}{}
	}
	return s.repository.ResolveAdminUserMemberships(ctx, scope, append([]string(nil), userIDs...))
}

func (s *AdminEndUserService) ListSessions(ctx context.Context, query AdminUserSessionQuery) (AdminUserSessionPage, error) {
	if s == nil || !validAdminUserScope(query.Scope) || !validAdminValue(query.UserID, 160) || query.Limit < 1 || query.Limit > AdminUserMaxPageSize || (query.After != nil && (query.After.CreatedAt.IsZero() || !validAdminValue(query.After.SessionID, 160))) {
		return AdminUserSessionPage{}, ErrAdminEndUserInvalidArgument
	}
	query.Limit++
	items, err := s.repository.ListAdminUserSessions(ctx, query)
	if err != nil {
		return AdminUserSessionPage{}, err
	}
	page := AdminUserSessionPage{Items: items}
	if len(page.Items) >= query.Limit {
		page.Items = page.Items[:query.Limit-1]
		position := page.Items[len(page.Items)-1].Position
		page.Next = &position
	}
	return page, nil
}

func (s *AdminEndUserService) SetGlobalSecurityStatus(ctx context.Context, command AdminGlobalSecurityStatusCommand) (AdminGlobalSecurityStatusResult, error) {
	if s == nil || !validAdminValue(command.UserID, 160) || !validGlobalStatus(command.Status) || command.ExpectedVersion < 1 || !validAdminReason(command.ReasonCode) || !validAdminNote(command.OperatorNote) || !validAdminIdempotency(command.IdempotencyKey) || !validAdminValue(command.ActorID, 160) || !validAdminValue(command.TraceID, 128) {
		return AdminGlobalSecurityStatusResult{}, ErrAdminEndUserInvalidArgument
	}
	auditID, eventID, err := s.writeIDs()
	if err != nil {
		return AdminGlobalSecurityStatusResult{}, err
	}
	now := s.now().UTC()
	request, err := json.Marshal(struct {
		UserID          string `json:"user_id"`
		Status          string `json:"status"`
		ExpectedVersion int64  `json:"expected_version"`
		ReasonCode      string `json:"reason_code"`
		OperatorNote    string `json:"operator_note"`
	}{command.UserID, command.Status, command.ExpectedVersion, command.ReasonCode, command.OperatorNote})
	if err != nil {
		return AdminGlobalSecurityStatusResult{}, err
	}
	record := AdminGlobalSecurityStatusRecord{AdminGlobalSecurityStatusCommand: command, ActorDigest: s.hasher.Digest("admin-end-user-actor\x00" + command.ActorID), KeyDigest: s.hasher.Digest("idempotency-key\x00" + command.IdempotencyKey), RequestDigest: s.hasher.Digest("admin-global-security-status\x00" + string(request)), Now: now}
	record.OutboxEvent = OutboxEvent{EventID: eventID, Topic: "identity.global_user_security_status_changed.v1", Now: now, Payload: SecurityEvent{AuditID: auditID, OccurredAt: now, ActorID: command.ActorID, Permission: "identity.security.manage", ScopeType: "platform", Action: "identity.global_user_security_status_changed", TargetType: "end_user", TargetID: command.UserID, Result: "success", ReasonCode: command.ReasonCode, TraceID: command.TraceID, RiskLevel: "high"}}
	return s.repository.SetGlobalUserSecurityStatus(ctx, record)
}

func (s *AdminEndUserService) RevokeSessions(ctx context.Context, command AdminSessionRevocationCommand) (AdminSessionRevocationResult, error) {
	if s == nil || !validAdminUserScope(command.Scope) || !validAdminValue(command.UserID, 160) || !validAdminSessionSelection(command.SessionIDs, command.AllActive) || !validAdminReason(command.ReasonCode) || !validAdminIdempotency(command.IdempotencyKey) || !validAdminValue(command.ActorID, 160) || !validAdminValue(command.TraceID, 128) {
		return AdminSessionRevocationResult{}, ErrAdminEndUserInvalidArgument
	}
	auditID, eventID, err := s.writeIDs()
	if err != nil {
		return AdminSessionRevocationResult{}, err
	}
	now := s.now().UTC()
	request, err := json.Marshal(struct {
		Scope      AdminUserScope `json:"scope"`
		UserID     string         `json:"user_id"`
		SessionIDs []string       `json:"session_ids"`
		AllActive  bool           `json:"all_active"`
		ReasonCode string         `json:"reason_code"`
	}{command.Scope, command.UserID, command.SessionIDs, command.AllActive, command.ReasonCode})
	if err != nil {
		return AdminSessionRevocationResult{}, err
	}
	record := AdminSessionRevocationRecord{AdminSessionRevocationCommand: command, ActorDigest: s.hasher.Digest("admin-end-user-actor\x00" + command.ActorID), KeyDigest: s.hasher.Digest("idempotency-key\x00" + command.IdempotencyKey), RequestDigest: s.hasher.Digest("admin-session-revocation\x00" + string(request)), Now: now}
	permission := "product.user-access.manage"
	if command.Scope.Type == AdminUserScopePlatform {
		permission = "identity.security.manage"
	}
	scopeID := adminScopeID(command.Scope)
	record.OutboxEvent = OutboxEvent{EventID: eventID, Topic: "identity.admin_user_sessions_revoked.v1", Now: now, Payload: SecurityEvent{AuditID: auditID, OccurredAt: now, ActorID: command.ActorID, Permission: permission, ScopeType: string(command.Scope.Type), ScopeID: scopeID, ProductID: command.Scope.ProductID, TenantID: command.Scope.TenantID, Action: "identity.admin_user_sessions_revoked", TargetType: "end_user", TargetID: command.UserID, Result: "success", ReasonCode: command.ReasonCode, TraceID: command.TraceID, RiskLevel: "high"}}
	return s.repository.RevokeAdminUserSessions(ctx, record)
}

func (s *AdminEndUserService) addIdentifierSearch(query *AdminUserRepositoryQuery) {
	if query.Text == "" {
		return
	}
	kind := IdentifierType("")
	if strings.Contains(query.Text, "@") {
		kind = IdentifierEmail
	} else if strings.HasPrefix(query.Text, "+") {
		kind = IdentifierPhone
	}
	if kind == "" {
		return
	}
	normalized, err := s.normalizer.Normalize(kind, query.Text)
	if err != nil {
		return
	}
	query.IdentifierType = kind
	query.IdentifierDigest = s.hasher.Digest("identifier\x00" + normalized.Value)
}

func (s *AdminEndUserService) writeIDs() (string, string, error) {
	auditID, err := s.ids.ID("aud_")
	if err != nil {
		return "", "", err
	}
	eventID, err := s.ids.ID("evt_")
	return auditID, eventID, err
}

func validAdminUserScope(scope AdminUserScope) bool {
	switch scope.Type {
	case AdminUserScopePlatform:
		return scope.ProductID == "" && scope.TenantID == ""
	case AdminUserScopeProduct:
		return validAdminValue(scope.ProductID, 160) && scope.TenantID == ""
	case AdminUserScopeTenant:
		return validAdminValue(scope.ProductID, 160) && validAdminValue(scope.TenantID, 160)
	default:
		return false
	}
}

func validAdminQuery(value string) bool {
	if value == "" {
		return true
	}
	return validAdminValue(value, 320)
}

func validAdminAccountStatusFilter(value string) bool {
	return value == "" || validGlobalStatus(value)
}

func validGlobalStatus(value string) bool {
	return value == "active" || value == "locked" || value == "disabled"
}

func validAdminValue(value string, maximum int) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validAdminReason(value string) bool {
	return len(value) <= 64 && adminReasonPattern.MatchString(value)
}

func validAdminNote(value string) bool {
	if value == "" {
		return true
	}
	return validAdminValue(value, 500)
}

func validAdminIdempotency(value string) bool {
	return len(value) >= 16 && len(value) <= 128 && validAdminValue(value, 128)
}

func validAdminSessionSelection(sessionIDs []string, allActive bool) bool {
	if allActive {
		return len(sessionIDs) == 0
	}
	if len(sessionIDs) < 1 || len(sessionIDs) > 100 {
		return false
	}
	seen := make(map[string]struct{}, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		if !validAdminValue(sessionID, 160) {
			return false
		}
		if _, duplicate := seen[sessionID]; duplicate {
			return false
		}
		seen[sessionID] = struct{}{}
	}
	return true
}

func adminScopeID(scope AdminUserScope) string {
	if scope.Type == AdminUserScopeProduct {
		return scope.ProductID
	}
	if scope.Type == AdminUserScopeTenant {
		return scope.TenantID
	}
	return ""
}
