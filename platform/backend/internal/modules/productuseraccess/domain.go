package productuseraccess

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument  = errors.New("invalid product user access argument")
	ErrScopeMismatch    = errors.New("PRODUCT_USER_ACCESS_SCOPE_MISMATCH")
	ErrConflict         = errors.New("PRODUCT_USER_ACCESS_CONFLICT")
	ErrProductSuspended = errors.New("PRODUCT_USER_ACCESS_SUSPENDED")
	ErrTenantSuspended  = errors.New("TENANT_USER_ACCESS_SUSPENDED")
)

type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
)

type ScopeType string

const (
	ScopeProduct ScopeType = "product"
	ScopeTenant  ScopeType = "tenant"
)

// Context values are trusted inputs resolved by the composition layer.
type ProductContext struct{ ProductID string }
type TenantContext struct {
	ProductID string
	TenantID  string
}
type UserContext struct{ UserID string }

type Admission struct {
	Allowed        bool   `json:"allowed"`
	Code           string `json:"code,omitempty"`
	ProductStatus  Status `json:"product_status"`
	ProductVersion int64  `json:"product_version"`
	TenantStatus   Status `json:"tenant_status,omitempty"`
	TenantVersion  int64  `json:"tenant_version,omitempty"`
}

type AccessFact struct {
	ScopeType       ScopeType `json:"scope_type"`
	ProductID       string    `json:"product_id"`
	TenantID        string    `json:"tenant_id,omitempty"`
	UserID          string    `json:"user_id"`
	Status          Status    `json:"status"`
	AccessVersion   int64     `json:"access_version"`
	ReasonCode      string    `json:"reason_code"`
	OperatorNote    string    `json:"-"`
	StatusChangedAt time.Time `json:"status_changed_at"`
}

type StatusChangeResult struct {
	ScopeType     ScopeType `json:"scope_type"`
	ProductID     string    `json:"product_id"`
	TenantID      string    `json:"tenant_id,omitempty"`
	UserID        string    `json:"user_id"`
	Status        Status    `json:"status"`
	AccessVersion int64     `json:"access_version"`
}

type SetProductAccessStatusCommand struct {
	Product         ProductContext
	User            UserContext
	Status          Status
	ExpectedVersion int64
	ReasonCode      string
	OperatorNote    string
	IdempotencyKey  string
}

type SetTenantAccessStatusCommand struct {
	Product         ProductContext
	Tenant          TenantContext
	User            UserContext
	Status          Status
	ExpectedVersion int64
	ReasonCode      string
	OperatorNote    string
	IdempotencyKey  string
}

type ListScopedUserIDsQuery struct {
	Product ProductContext
	Tenant  *TenantContext
}

type ChangeRecord struct {
	ScopeType         ScopeType
	ProductID         string
	TenantID          string
	UserID            string
	Status            Status
	ExpectedVersion   int64
	ReasonCode        string
	OperatorNote      string
	KeyDigest         []byte
	RequestDigest     []byte
	StatusEventID     string
	RevocationEventID string
	Now               time.Time
}
