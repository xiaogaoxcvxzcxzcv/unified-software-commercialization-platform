package accountuserquery

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
)

const accountPackageID = "package.account"

var (
	ErrInvalidFilter        = errors.New("account_admin.invalid_filter")
	ErrInvalidCursor        = errors.New("account_admin.invalid_cursor")
	ErrScopedUserNotFound   = errors.New("account_admin.scoped_user_not_found")
	ErrCapabilityNotEnabled = errors.New("account_admin.capability_not_enabled")
	ErrIdentityUserNotFound = errors.New("identity admin user not found")
)

type ScopeType string

const (
	ScopePlatform ScopeType = "platform"
	ScopeProduct  ScopeType = "product"
	ScopeTenant   ScopeType = "tenant"
)

type Scope struct {
	Type      ScopeType
	ProductID string
	TenantID  string
}

type MaskedIdentifier struct {
	Type        string `json:"type"`
	MaskedValue string `json:"masked_value"`
	Verified    bool   `json:"verified"`
}

type Profile struct {
	UserID      string  `json:"user_id"`
	Version     int64   `json:"version"`
	DisplayName *string `json:"display_name"`
	AvatarURL   *string `json:"avatar_url"`
	Locale      *string `json:"locale"`
	Timezone    *string `json:"timezone"`
}

type IdentityUser struct {
	UserID             string
	UserVersion        int64
	AccountStatus      string
	DisplayName        *string
	Identifiers        []MaskedIdentifier
	CreatedAt          time.Time
	MemberSince        *time.Time
	LastSeenAt         *time.Time
	ActiveSessionCount int
	TotalSessionCount  int
	Position           string
}

type IdentityUserDetail struct {
	User    IdentityUser
	Profile Profile
}

type IdentitySession struct {
	SessionID            string     `json:"session_id"`
	ProductID            string     `json:"product_id"`
	ApplicationID        string     `json:"application_id"`
	TenantID             *string    `json:"tenant_id"`
	Environment          *string    `json:"environment"`
	AuthenticationMethod string     `json:"authentication_method"`
	DeviceLabel          *string    `json:"device_label"`
	CreatedAt            time.Time  `json:"created_at"`
	LastSeenAt           time.Time  `json:"last_seen_at"`
	ExpiresAt            time.Time  `json:"expires_at"`
	RevokedAt            *time.Time `json:"revoked_at"`
	Position             string     `json:"-"`
}

type IdentityListQuery struct {
	Scope         Scope
	Query         string
	AccountStatus string
	After         string
	Limit         int
}

type AdminUserQuery = IdentityListQuery
type AdminUserRecord = IdentityUser
type AdminUserDetail = IdentityUserDetail
type AdminUserSessionQuery = IdentitySessionQuery
type AdminUserSession = IdentitySession

type IdentitySessionQuery struct {
	Scope  Scope
	UserID string
	After  string
	Limit  int
}

type IdentityPort interface {
	ListUsers(context.Context, AdminUserQuery) ([]AdminUserRecord, error)
	GetUser(context.Context, Scope, string) (AdminUserDetail, error)
	ListSessions(context.Context, AdminUserSessionQuery) ([]AdminUserSession, error)
}

type AccessReader interface {
	GetScopedAccessBatch(context.Context, productuseraccess.GetScopedAccessBatchQuery) ([]productuseraccess.ScopedAccess, error)
}

type CapabilityChecker interface {
	IsPackageEnabled(context.Context, string, string) (bool, error)
}

type ListQuery struct {
	Scope         Scope
	Query         string
	AccountStatus string
	AccessStatus  string
	Cursor        string
	PageSize      int
}

type UserSummary struct {
	UserID             string                  `json:"user_id"`
	UserVersion        int64                   `json:"user_version"`
	AccountStatus      string                  `json:"account_status"`
	DisplayName        *string                 `json:"display_name"`
	Identifiers        []MaskedIdentifier      `json:"identifiers"`
	CreatedAt          time.Time               `json:"created_at"`
	MemberSince        *time.Time              `json:"member_since"`
	LastSeenAt         *time.Time              `json:"last_seen_at"`
	ActiveSessionCount int                     `json:"active_session_count"`
	TotalSessionCount  int                     `json:"total_session_count"`
	Access             *ScopedAccessProjection `json:"access"`
}

// ScopedAccessProjection is the Account public projection. Product User
// Access ownership fields remain inside that module and are not serialized.
type ScopedAccessProjection struct {
	ScopeType       productuseraccess.ScopeType `json:"scope_type"`
	ScopeID         string                      `json:"scope_id"`
	Status          productuseraccess.Status    `json:"status"`
	Explicit        bool                        `json:"explicit"`
	AccessVersion   int64                       `json:"version"`
	StatusChangedAt *time.Time                  `json:"status_changed_at,omitempty"`
}

type UserDetail struct {
	User    UserSummary `json:"user"`
	Profile Profile     `json:"profile"`
}

type UserPage struct {
	Items      []UserSummary `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

type SessionPage struct {
	Items      []IdentitySession `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

type Service struct {
	identity     IdentityPort
	access       AccessReader
	capabilities CapabilityChecker
	cursorKey    []byte
}

func New(identity IdentityPort, access AccessReader, capabilities CapabilityChecker, cursorKey []byte) *Service {
	return &Service{identity: identity, access: access, capabilities: capabilities, cursorKey: append([]byte(nil), cursorKey...)}
}

func (s *Service) ListUsers(ctx context.Context, query ListQuery) (UserPage, error) {
	query.Query = strings.TrimSpace(query.Query)
	if err := validateListQuery(query); err != nil || s == nil || s.identity == nil || len(s.cursorKey) < 32 {
		return UserPage{}, ErrInvalidFilter
	}
	if err := s.requireCapability(ctx, query.Scope); err != nil {
		return UserPage{}, err
	}
	after, err := s.decodeCursor(query, "users")
	if err != nil {
		return UserPage{}, err
	}
	target := query.PageSize + 1
	matches := make([]positionedUser, 0, target)
	for len(matches) < target {
		batch, listErr := s.identity.ListUsers(ctx, IdentityListQuery{Scope: query.Scope, Query: query.Query, AccountStatus: query.AccountStatus, After: after, Limit: 100})
		if listErr != nil {
			return UserPage{}, listErr
		}
		if len(batch) == 0 {
			break
		}
		if len(batch) > 100 {
			return UserPage{}, ErrInvalidCursor
		}
		accessByUser, accessErr := s.accessFor(ctx, query.Scope, batch)
		if accessErr != nil {
			return UserPage{}, accessErr
		}
		for _, candidate := range batch {
			if !validIdentityUser(candidate) {
				return UserPage{}, ErrInvalidCursor
			}
			after = candidate.Position
			item := mapUser(candidate, accessByUser[candidate.UserID])
			if query.AccessStatus == "" || (item.Access != nil && string(item.Access.Status) == query.AccessStatus) {
				matches = append(matches, positionedUser{item: item, position: candidate.Position})
				if len(matches) == target {
					break
				}
			}
		}
		if len(batch) < 100 {
			break
		}
	}
	page := UserPage{Items: make([]UserSummary, 0, min(query.PageSize, len(matches)))}
	for index := 0; index < len(matches) && index < query.PageSize; index++ {
		page.Items = append(page.Items, matches[index].item)
	}
	if len(matches) > query.PageSize {
		cursor, encodeErr := s.encodeCursor(query, "users", matches[query.PageSize-1].position)
		if encodeErr != nil {
			return UserPage{}, encodeErr
		}
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *Service) GetUser(ctx context.Context, scope Scope, userID string) (UserDetail, error) {
	if s == nil || s.identity == nil || !validScope(scope) || !validID(userID) {
		return UserDetail{}, ErrInvalidFilter
	}
	if err := s.requireCapability(ctx, scope); err != nil {
		return UserDetail{}, err
	}
	detail, err := s.identity.GetUser(ctx, scope, userID)
	if err != nil {
		return UserDetail{}, s.mapIdentityError(scope, err)
	}
	if detail.User.UserID != userID || !validIdentityUserWithoutPosition(detail.User) {
		return UserDetail{}, ErrScopedUserNotFound
	}
	accessByUser, err := s.accessFor(ctx, scope, []IdentityUser{detail.User})
	if err != nil {
		return UserDetail{}, err
	}
	return UserDetail{User: mapUser(detail.User, accessByUser[userID]), Profile: detail.Profile}, nil
}

func (s *Service) ListSessions(ctx context.Context, scope Scope, userID, cursor string, pageSize int) (SessionPage, error) {
	if s == nil || s.identity == nil || len(s.cursorKey) < 32 || !validScope(scope) || !validID(userID) || pageSize < 1 || pageSize > 100 {
		return SessionPage{}, ErrInvalidFilter
	}
	if err := s.requireCapability(ctx, scope); err != nil {
		return SessionPage{}, err
	}
	if _, err := s.identity.GetUser(ctx, scope, userID); err != nil {
		return SessionPage{}, s.mapIdentityError(scope, err)
	}
	query := ListQuery{Scope: scope, Query: userID, Cursor: cursor, PageSize: pageSize}
	after, err := s.decodeCursor(query, "sessions")
	if err != nil {
		return SessionPage{}, err
	}
	items, err := s.identity.ListSessions(ctx, IdentitySessionQuery{Scope: scope, UserID: userID, After: after, Limit: pageSize + 1})
	if err != nil {
		return SessionPage{}, err
	}
	if len(items) > pageSize+1 {
		return SessionPage{}, ErrInvalidCursor
	}
	page := SessionPage{Items: items}
	if len(page.Items) > pageSize {
		position := page.Items[pageSize-1].Position
		page.Items = page.Items[:pageSize]
		if position == "" {
			return SessionPage{}, ErrInvalidCursor
		}
		next, encodeErr := s.encodeCursor(query, "sessions", position)
		if encodeErr != nil {
			return SessionPage{}, encodeErr
		}
		page.NextCursor = &next
	}
	for index := range page.Items {
		if page.Items[index].Position == "" || !validID(page.Items[index].SessionID) {
			return SessionPage{}, ErrInvalidCursor
		}
		page.Items[index].Position = ""
	}
	return page, nil
}

type positionedUser struct {
	item     UserSummary
	position string
}

func (s *Service) accessFor(ctx context.Context, scope Scope, users []IdentityUser) (map[string]*productuseraccess.ScopedAccess, error) {
	result := make(map[string]*productuseraccess.ScopedAccess, len(users))
	if scope.Type == ScopePlatform {
		return result, nil
	}
	if s.access == nil {
		return nil, ErrInvalidFilter
	}
	ids := make([]string, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.UserID)
	}
	query := productuseraccess.GetScopedAccessBatchQuery{Product: productuseraccess.ProductContext{ProductID: scope.ProductID}, UserIDs: ids}
	if scope.Type == ScopeTenant {
		query.Tenant = &productuseraccess.TenantContext{ProductID: scope.ProductID, TenantID: scope.TenantID}
	}
	items, err := s.access.GetScopedAccessBatch(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(items) != len(ids) {
		return nil, productuseraccess.ErrInvalidArgument
	}
	for index := range items {
		item := items[index]
		if item.UserID != ids[index] {
			return nil, productuseraccess.ErrScopeMismatch
		}
		result[item.UserID] = &item
	}
	return result, nil
}

func (s *Service) requireCapability(ctx context.Context, scope Scope) error {
	if !validScope(scope) {
		return ErrInvalidFilter
	}
	if scope.Type == ScopePlatform {
		return nil
	}
	if s.capabilities == nil {
		return ErrCapabilityNotEnabled
	}
	enabled, err := s.capabilities.IsPackageEnabled(ctx, scope.ProductID, accountPackageID)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrCapabilityNotEnabled
	}
	return nil
}

func (s *Service) mapIdentityError(scope Scope, err error) error {
	if errors.Is(err, ErrIdentityUserNotFound) && scope.Type != ScopePlatform {
		return ErrScopedUserNotFound
	}
	return err
}

func mapUser(user IdentityUser, access *productuseraccess.ScopedAccess) UserSummary {
	identifiers := append([]MaskedIdentifier(nil), user.Identifiers...)
	var projection *ScopedAccessProjection
	if access != nil {
		projection = &ScopedAccessProjection{ScopeType: access.ScopeType, ScopeID: access.ScopeID, Status: access.Status, Explicit: access.Explicit, AccessVersion: access.AccessVersion, StatusChangedAt: access.StatusChangedAt}
	}
	return UserSummary{UserID: user.UserID, UserVersion: user.UserVersion, AccountStatus: user.AccountStatus, DisplayName: user.DisplayName, Identifiers: identifiers, CreatedAt: user.CreatedAt, MemberSince: user.MemberSince, LastSeenAt: user.LastSeenAt, ActiveSessionCount: user.ActiveSessionCount, TotalSessionCount: user.TotalSessionCount, Access: projection}
}

type cursorPayload struct {
	Version       int       `json:"v"`
	Kind          string    `json:"kind"`
	ScopeType     ScopeType `json:"scope_type"`
	ProductID     string    `json:"product_id,omitempty"`
	TenantID      string    `json:"tenant_id,omitempty"`
	Query         string    `json:"query,omitempty"`
	AccountStatus string    `json:"account_status,omitempty"`
	AccessStatus  string    `json:"access_status,omitempty"`
	Position      string    `json:"position"`
}

func (s *Service) encodeCursor(query ListQuery, kind, position string) (string, error) {
	if position == "" {
		return "", ErrInvalidCursor
	}
	raw, err := json.Marshal(cursorPayload{Version: 1, Kind: kind, ScopeType: query.Scope.Type, ProductID: query.Scope.ProductID, TenantID: query.Scope.TenantID, Query: query.Query, AccountStatus: query.AccountStatus, AccessStatus: query.AccessStatus, Position: position})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.cursorKey)
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Service) decodeCursor(query ListQuery, kind string) (string, error) {
	if query.Cursor == "" {
		return "", nil
	}
	if len(query.Cursor) > 512 {
		return "", ErrInvalidCursor
	}
	parts := strings.Split(query.Cursor, ".")
	if len(parts) != 2 {
		return "", ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, s.cursorKey)
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", ErrInvalidCursor
	}
	var payload cursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Version != 1 || payload.Kind != kind || payload.Position == "" || payload.ScopeType != query.Scope.Type || payload.ProductID != query.Scope.ProductID || payload.TenantID != query.Scope.TenantID || payload.Query != query.Query || payload.AccountStatus != query.AccountStatus || payload.AccessStatus != query.AccessStatus {
		return "", ErrInvalidCursor
	}
	return payload.Position, nil
}

func validateListQuery(query ListQuery) error {
	if !validScope(query.Scope) || query.PageSize < 1 || query.PageSize > 100 || len(query.Query) > 320 || !validText(query.Query) {
		return ErrInvalidFilter
	}
	if query.AccountStatus != "" && query.AccountStatus != "active" && query.AccountStatus != "locked" && query.AccountStatus != "disabled" {
		return ErrInvalidFilter
	}
	if query.AccessStatus != "" && query.AccessStatus != "active" && query.AccessStatus != "suspended" {
		return ErrInvalidFilter
	}
	if query.Scope.Type == ScopePlatform && query.AccessStatus != "" {
		return ErrInvalidFilter
	}
	return nil
}

func validScope(scope Scope) bool {
	switch scope.Type {
	case ScopePlatform:
		return scope.ProductID == "" && scope.TenantID == ""
	case ScopeProduct:
		return validID(scope.ProductID) && scope.TenantID == ""
	case ScopeTenant:
		return validID(scope.ProductID) && validID(scope.TenantID)
	default:
		return false
	}
}

func validIdentityUser(user IdentityUser) bool {
	return user.Position != "" && validIdentityUserWithoutPosition(user)
}

func validIdentityUserWithoutPosition(user IdentityUser) bool {
	return validID(user.UserID) && user.UserVersion > 0 && (user.AccountStatus == "active" || user.AccountStatus == "locked" || user.AccountStatus == "disabled") && !user.CreatedAt.IsZero() && user.ActiveSessionCount >= 0 && user.TotalSessionCount >= user.ActiveSessionCount
}

func validID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 128 && validText(value)
}

func validText(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
