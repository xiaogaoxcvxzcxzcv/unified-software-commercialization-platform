package accountuseradmin

import (
	"context"
	"errors"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
	"platform.local/capability-platform/backend/internal/workflows/accountuserquery"
)

var (
	ErrInvalidRequest       = errors.New("account_admin.invalid_request")
	ErrScopedUserNotFound   = accountuserquery.ErrScopedUserNotFound
	ErrCapabilityNotEnabled = accountuserquery.ErrCapabilityNotEnabled
	ErrIdentityUserNotFound = errors.New("account_admin.identity_user_not_found")
	ErrIdentityConflict     = errors.New("account_admin.identity_conflict")
)

type Scope = accountuserquery.Scope

const (
	ScopePlatform = accountuserquery.ScopePlatform
	ScopeProduct  = accountuserquery.ScopeProduct
	ScopeTenant   = accountuserquery.ScopeTenant
)

type CapabilityChecker interface {
	IsPackageEnabled(context.Context, string, string) (bool, error)
}

type MembershipPort interface {
	VerifyMember(context.Context, Scope, string) error
}

type AccessWriter interface {
	SetProductAccessStatus(context.Context, productuseraccess.SetProductAccessStatusCommand) (productuseraccess.StatusChangeResult, error)
	SetTenantAccessStatus(context.Context, productuseraccess.SetTenantAccessStatusCommand) (productuseraccess.StatusChangeResult, error)
}

type GlobalSecurityCommand struct {
	UserID          string
	Status          string
	ExpectedVersion int64
	ReasonCode      string
	OperatorNote    string
	ActorID         string
	TraceID         string
	IdempotencyKey  string
}

type SessionRevocationCommand struct {
	Scope          Scope
	UserID         string
	SessionIDs     []string
	AllActive      bool
	ReasonCode     string
	ActorID        string
	TraceID        string
	IdempotencyKey string
}

type SecurityMutationResult struct {
	UserID    string  `json:"user_id"`
	ScopeType string  `json:"scope_type"`
	ScopeID   *string `json:"scope_id"`
	Status    string  `json:"status"`
	Version   int64   `json:"version"`
	AuditID   string  `json:"audit_id"`
}

type SessionRevocationResult struct {
	UserID       string  `json:"user_id"`
	ScopeType    string  `json:"scope_type"`
	ScopeID      *string `json:"scope_id"`
	RevokedCount int     `json:"revoked_count"`
	AuditID      string  `json:"audit_id"`
}

type IdentityPort interface {
	SetGlobalUserSecurityStatus(context.Context, GlobalSecurityCommand) (SecurityMutationResult, error)
	RevokeAdminUserSessions(context.Context, SessionRevocationCommand) (SessionRevocationResult, error)
}

type Service struct {
	identity     IdentityPort
	access       AccessWriter
	membership   MembershipPort
	capabilities CapabilityChecker
}

func New(identity IdentityPort, access AccessWriter, membership MembershipPort, capabilities CapabilityChecker) *Service {
	return &Service{identity: identity, access: access, membership: membership, capabilities: capabilities}
}

func (s *Service) SetGlobalSecurityStatus(ctx context.Context, command GlobalSecurityCommand) (SecurityMutationResult, error) {
	if s == nil || s.identity == nil || !validID(command.UserID) || (command.Status != "active" && command.Status != "locked" && command.Status != "disabled") || command.ExpectedVersion < 1 || !validID(command.ReasonCode) || !validID(command.ActorID) || !validID(command.TraceID) || !validIdempotency(command.IdempotencyKey) || !validNote(command.OperatorNote) {
		return SecurityMutationResult{}, ErrInvalidRequest
	}
	return s.identity.SetGlobalUserSecurityStatus(ctx, command)
}

func (s *Service) SetProductAccessStatus(ctx context.Context, scope Scope, userID string, status productuseraccess.Status, expectedVersion int64, reasonCode, operatorNote, actorID, traceID, idempotencyKey string) (productuseraccess.StatusChangeResult, error) {
	if err := s.validateScopedTarget(ctx, scope, userID); err != nil {
		return productuseraccess.StatusChangeResult{}, err
	}
	if s.access == nil || status != productuseraccess.StatusActive && status != productuseraccess.StatusSuspended {
		return productuseraccess.StatusChangeResult{}, ErrInvalidRequest
	}
	return s.access.SetProductAccessStatus(ctx, productuseraccess.SetProductAccessStatusCommand{Product: productuseraccess.ProductContext{ProductID: scope.ProductID}, User: productuseraccess.UserContext{UserID: userID}, Status: status, ExpectedVersion: expectedVersion, ReasonCode: reasonCode, OperatorNote: operatorNote, ActorID: actorID, TraceID: traceID, IdempotencyKey: idempotencyKey})
}

func (s *Service) SetTenantAccessStatus(ctx context.Context, scope Scope, userID string, status productuseraccess.Status, expectedVersion int64, reasonCode, operatorNote, actorID, traceID, idempotencyKey string) (productuseraccess.StatusChangeResult, error) {
	if scope.Type != ScopeTenant {
		return productuseraccess.StatusChangeResult{}, ErrInvalidRequest
	}
	if err := s.validateScopedTarget(ctx, scope, userID); err != nil {
		return productuseraccess.StatusChangeResult{}, err
	}
	if s.access == nil || status != productuseraccess.StatusActive && status != productuseraccess.StatusSuspended {
		return productuseraccess.StatusChangeResult{}, ErrInvalidRequest
	}
	return s.access.SetTenantAccessStatus(ctx, productuseraccess.SetTenantAccessStatusCommand{Product: productuseraccess.ProductContext{ProductID: scope.ProductID}, Tenant: productuseraccess.TenantContext{ProductID: scope.ProductID, TenantID: scope.TenantID}, User: productuseraccess.UserContext{UserID: userID}, Status: status, ExpectedVersion: expectedVersion, ReasonCode: reasonCode, OperatorNote: operatorNote, ActorID: actorID, TraceID: traceID, IdempotencyKey: idempotencyKey})
}

func (s *Service) RevokeSessions(ctx context.Context, command SessionRevocationCommand) (SessionRevocationResult, error) {
	if s == nil || s.identity == nil || !validScope(command.Scope) || !validID(command.UserID) || !validID(command.ReasonCode) || !validID(command.ActorID) || !validID(command.TraceID) || !validIdempotency(command.IdempotencyKey) || (command.AllActive == (len(command.SessionIDs) != 0)) || len(command.SessionIDs) > 100 {
		return SessionRevocationResult{}, ErrInvalidRequest
	}
	if command.Scope.Type != ScopePlatform {
		if err := s.validateScopedTarget(ctx, command.Scope, command.UserID); err != nil {
			return SessionRevocationResult{}, err
		}
	}
	return s.identity.RevokeAdminUserSessions(ctx, command)
}

func (s *Service) validateScopedTarget(ctx context.Context, scope Scope, userID string) error {
	if s == nil || !validScope(scope) || scope.Type == ScopePlatform || !validID(userID) {
		return ErrInvalidRequest
	}
	if s.capabilities == nil {
		return ErrCapabilityNotEnabled
	}
	enabled, err := s.capabilities.IsPackageEnabled(ctx, scope.ProductID, "package.account")
	if err != nil {
		return err
	}
	if !enabled {
		return ErrCapabilityNotEnabled
	}
	if s.membership == nil {
		return ErrScopedUserNotFound
	}
	if err := s.membership.VerifyMember(ctx, scope, userID); err != nil {
		return ErrScopedUserNotFound
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

func validID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 160
}
func validIdempotency(value string) bool {
	return len(value) >= 16 && len(value) <= 128 && value == strings.TrimSpace(value)
}
func validNote(value string) bool { return len(value) <= 500 && !strings.ContainsAny(value, "\r\n\t") }
