package entitlement

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument      = errors.New("ENTITLEMENT_INVALID_ARGUMENT")
	ErrScopeMismatch        = errors.New("ENTITLEMENT_SCOPE_MISMATCH")
	ErrOperationConflict    = errors.New("ENTITLEMENT_OPERATION_CONFLICT")
	ErrPolicyConflict       = errors.New("ENTITLEMENT_POLICY_CONFLICT")
	ErrSourceDuplicate      = errors.New("ENTITLEMENT_SOURCE_DUPLICATE")
	ErrRequired             = errors.New("ENTITLEMENT_REQUIRED")
	ErrExpired              = errors.New("ENTITLEMENT_EXPIRED")
	ErrCapabilityDisabled   = errors.New("ENTITLEMENT_CAPABILITY_DISABLED")
	ErrDeviceLimited        = errors.New("ENTITLEMENT_DEVICE_LIMITED")
	ErrOutboxEventNotFound  = errors.New("entitlement outbox event not found")
	ErrUnsupportedOperation = errors.New("ENTITLEMENT_UNSUPPORTED_OPERATION")
)

type ProductContext struct{ ProductID string }

type TenantContext struct {
	ProductID string
	TenantID  string
}

type UserContext struct{ UserID string }

type AdminScope struct {
	AdminID   string
	ProductID string
	TenantID  string
}

type FeatureKind string

const (
	FeatureBoolean      FeatureKind = "boolean"
	FeatureLimit        FeatureKind = "limit"
	FeatureQuota        FeatureKind = "quota"
	FeatureDevicePolicy FeatureKind = "device_policy"
)

type PolicyStatus string

const (
	PolicyDraft   PolicyStatus = "draft"
	PolicyActive  PolicyStatus = "active"
	PolicyRetired PolicyStatus = "retired"
)

type ValidityRule string

const (
	ValidityFixedDuration ValidityRule = "fixed_duration"
	ValidityFixedEnd      ValidityRule = "fixed_end"
	ValidityLifetime      ValidityRule = "lifetime"
)

type StackingRule string

const (
	StackUnionLatestExpiry StackingRule = "union_latest_expiry"
	StackReplaceSameGroup  StackingRule = "replace_same_group"
	StackRejectConflict    StackingRule = "reject_conflict"
)

type RevokeScope string

const (
	RevokeSourceOnly          RevokeScope = "source_only"
	RevokeConclusionGroup     RevokeScope = "conclusion_group"
	RevokeAllUserEntitlements RevokeScope = "all_user_entitlements"
)

type Effect string

const (
	EffectGrant   Effect = "grant"
	EffectExtend  Effect = "extend"
	EffectReplace Effect = "replace"
	EffectRevoke  Effect = "revoke"
	EffectExpire  Effect = "expire"
)

type SourceType string

const (
	SourceAdmin   SourceType = "admin"
	SourceTrial   SourceType = "trial"
	SourceGift    SourceType = "gift"
	SourceOrder   SourceType = "order"
	SourceLicense SourceType = "license"
)

type ActorType string

const (
	ActorAdmin  ActorType = "admin"
	ActorSystem ActorType = "system"
	ActorUser   ActorType = "user"
)

type ReasonCode string

const (
	ReasonManualGrant  ReasonCode = "manual_grant"
	ReasonManualExtend ReasonCode = "manual_extend"
	ReasonManualRevoke ReasonCode = "manual_revoke"
	ReasonSourceRevoke ReasonCode = "source_revoke"
	ReasonExpired      ReasonCode = "expired"
)

type StableReasonCode string

const (
	ReasonEntitlementRequired           StableReasonCode = "ENTITLEMENT_REQUIRED"
	ReasonEntitlementExpired            StableReasonCode = "ENTITLEMENT_EXPIRED"
	ReasonEntitlementDeviceLimited      StableReasonCode = "ENTITLEMENT_DEVICE_LIMITED"
	ReasonEntitlementCapabilityDisabled StableReasonCode = "ENTITLEMENT_CAPABILITY_DISABLED"
)

type PolicyRef struct {
	PolicyID string
	Version  int64
}

type ValidityInput struct {
	Rule       ValidityRule
	Duration   time.Duration
	FixedUntil time.Time
}

type SourceRef struct {
	Type           SourceType
	SourceID       string
	SourceEffectID string
}

type ActorRef struct {
	Type ActorType
	ID   string
}

type CheckDecision struct {
	Allowed           bool             `json:"allowed"`
	DecisionStage     string           `json:"decision_stage"`
	ReasonCode        StableReasonCode `json:"reason_code,omitempty"`
	Revision          int64            `json:"revision"`
	Features          map[string]any   `json:"features"`
	PlanCode          string           `json:"plan_code,omitempty"`
	ValidUntil        *time.Time       `json:"valid_until,omitempty"`
	OfflineGraceUntil *time.Time       `json:"offline_grace_until,omitempty"`
	ServerTime        time.Time        `json:"server_time"`
}

type GrantResult struct {
	EntitlementID string
	GrantID       string
	Revision      int64
	ValidUntil    *time.Time
	AuditID       string
	Decision      CheckDecision
}

type EntitlementSummary struct {
	ProductID         string
	TenantID          string
	UserID            string
	Revision          int64
	DecisionHash      []byte
	EffectiveFeatures map[string]any
	PlanCode          string
	ValidUntil        *time.Time
	OfflineGraceUntil *time.Time
	UpdatedAt         time.Time
}

type LedgerEntry struct {
	LedgerID           string
	ProductID          string
	TenantID           string
	UserID             string
	OperationType      Effect
	OperationID        string
	SourceType         SourceType
	SourceID           string
	GrantID            string
	BeforeRevision     int64
	AfterRevision      int64
	BeforeDecisionHash []byte
	AfterDecisionHash  []byte
	AuditID            string
	TraceID            string
	CreatedAt          time.Time
}

type ClaimedOutboxEvent struct {
	EventID      string
	AggregateID  string
	EventType    string
	Payload      map[string]any
	PayloadError string
	OccurredAt   time.Time
	AttemptCount int
}
