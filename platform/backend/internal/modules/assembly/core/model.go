package core

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"platform.local/capability-platform/backend/internal/modules/product"
)

var (
	ErrInvalidCommand          = errors.New("invalid assembly command")
	ErrDocumentInvalid         = errors.New("assembly document is invalid")
	ErrNotFound                = errors.New("assembly resource not found")
	ErrConflict                = errors.New("assembly resource conflict")
	ErrVersionConflict         = errors.New("assembly version conflict")
	ErrIdempotencyConflict     = errors.New("assembly idempotency conflict")
	ErrOperationInProgress     = errors.New("assembly operation is in progress")
	ErrPlanUnavailable         = errors.New("assembly plan is unavailable")
	ErrPlanNotExecutable       = errors.New("assembly plan is not executable")
	ErrPlanNotConfirmed        = errors.New("assembly plan is not confirmed")
	ErrOutputTargetUnavailable = errors.New("assembly output target is unavailable")
	ErrInvalidRunTransition    = errors.New("invalid assembly run transition")
)

type ValidatedDocument struct {
	SchemaName    string
	SchemaVersion string
	CanonicalJSON json.RawMessage
	SHA256        string
}

type DocumentValidator interface {
	Validate(string, json.RawMessage) (ValidatedDocument, error)
}

type OutputTargetVerifier interface {
	VerifyOutputTarget(context.Context, string, string) error
}

type OutputTargetVerifierFunc func(context.Context, string, string) error

func (verify OutputTargetVerifierFunc) VerifyOutputTarget(ctx context.Context, environment, outputTargetRef string) error {
	if verify == nil {
		return ErrOutputTargetUnavailable
	}
	return verify(ctx, environment, outputTargetRef)
}

type PlannedDocument struct {
	Document     json.RawMessage
	Capabilities []product.CapabilityItem
}

type Planner interface {
	BuildPlan(context.Context, Blueprint, string) (PlannedDocument, error)
}

type Blueprint struct {
	BlueprintID     string          `json:"blueprint_id"`
	ProductID       string          `json:"product_id"`
	Revision        int64           `json:"revision"`
	DocumentVersion string          `json:"document_version"`
	SchemaVersion   string          `json:"schema_version"`
	Document        json.RawMessage `json:"document"`
	ContentSHA256   string          `json:"content_sha256"`
	CreatedBy       string          `json:"created_by"`
	CreatedAt       time.Time       `json:"created_at"`
	AuditID         string          `json:"audit_id,omitempty"`
}

type Plan struct {
	PlanID                string                   `json:"plan_id"`
	ProductID             string                   `json:"product_id"`
	BlueprintID           string                   `json:"blueprint_id"`
	BlueprintRevision     int64                    `json:"blueprint_revision"`
	Version               int64                    `json:"version"`
	Environment           string                   `json:"environment"`
	SchemaVersion         string                   `json:"schema_version"`
	Document              json.RawMessage          `json:"document"`
	BlueprintSHA256       string                   `json:"blueprint_sha256"`
	CatalogRevision       string                   `json:"catalog_revision"`
	CatalogSnapshotSHA256 string                   `json:"catalog_snapshot_sha256"`
	PlanSHA256            string                   `json:"plan_sha256"`
	Executable            bool                     `json:"executable"`
	ConfirmedAt           *time.Time               `json:"confirmed_at,omitempty"`
	ConfirmedBy           string                   `json:"confirmed_by,omitempty"`
	Capabilities          []product.CapabilityItem `json:"capabilities,omitempty"`
	CreatedBy             string                   `json:"created_by"`
	CreatedAt             time.Time                `json:"created_at"`
	UpdatedAt             time.Time                `json:"updated_at"`
	AuditID               string                   `json:"audit_id,omitempty"`
}

type RunStatus string

const (
	RunStatusPlanned      RunStatus = "planned"
	RunStatusProvisioning RunStatus = "provisioning"
	RunStatusGenerating   RunStatus = "generating"
	RunStatusValidating   RunStatus = "validating"
	RunStatusCompleted    RunStatus = "completed"
	RunStatusFailed       RunStatus = "failed"
	RunStatusRollingBack  RunStatus = "rolling_back"
	RunStatusRolledBack   RunStatus = "rolled_back"
)

type RunStep struct {
	StepID             string     `json:"step_id"`
	Kind               string     `json:"kind"`
	Status             string     `json:"status"`
	Attempt            int        `json:"attempt"`
	CompensationStatus string     `json:"compensation_status"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
	DiagnosticIDs      []string   `json:"diagnostic_ids,omitempty"`
}

type RunRecovery struct {
	Retryable        bool   `json:"retryable"`
	RollbackRequired bool   `json:"rollback_required"`
	ResumeFromStepID string `json:"resume_from_step_id,omitempty"`
}

type Run struct {
	RunID                string          `json:"run_id"`
	ProductID            string          `json:"product_id"`
	PlanID               string          `json:"plan_id"`
	PlanVersion          int64           `json:"plan_version"`
	Version              int64           `json:"version"`
	PlanSHA256           string          `json:"plan_sha256"`
	SchemaVersion        string          `json:"schema_version"`
	Document             json.RawMessage `json:"document"`
	DocumentSHA256       string          `json:"document_sha256"`
	IdempotencyKeyDigest string          `json:"idempotency_key_digest"`
	OutputTargetRef      string          `json:"output_target_ref"`
	Status               RunStatus       `json:"status"`
	CurrentStepID        string          `json:"current_step_id,omitempty"`
	Steps                []RunStep       `json:"steps"`
	DiagnosticIDs        []string        `json:"diagnostic_ids,omitempty"`
	Recovery             RunRecovery     `json:"recovery"`
	ManifestID           string          `json:"manifest_id,omitempty"`
	LockID               string          `json:"lock_id,omitempty"`
	CreatedBy            string          `json:"created_by"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
	CompletedAt          *time.Time      `json:"completed_at,omitempty"`
	AuditID              string          `json:"audit_id,omitempty"`
}

type Manifest struct {
	AssemblyID     string          `json:"assembly_id"`
	ProductID      string          `json:"product_id"`
	RunID          string          `json:"run_id"`
	SchemaVersion  string          `json:"schema_version"`
	Document       json.RawMessage `json:"document"`
	DocumentSHA256 string          `json:"document_sha256"`
	ManifestSHA256 string          `json:"manifest_sha256"`
	CreatedAt      time.Time       `json:"created_at"`
}

type GeneratedProjectLock struct {
	LockID         string          `json:"lock_id"`
	ProductID      string          `json:"product_id"`
	RunID          string          `json:"run_id"`
	AssemblyID     string          `json:"assembly_id"`
	SchemaVersion  string          `json:"schema_version"`
	Document       json.RawMessage `json:"document"`
	DocumentSHA256 string          `json:"document_sha256"`
	LockSHA256     string          `json:"lock_sha256"`
	CreatedAt      time.Time       `json:"created_at"`
}

type Idempotency struct {
	Operation     string
	ActorID       string
	ScopeID       string
	KeyDigest     string
	RequestDigest string
	Now           time.Time
}

type EventPayload struct {
	AuditID         string         `json:"audit_id"`
	OccurredAt      time.Time      `json:"occurred_at"`
	ActorID         string         `json:"actor_id"`
	Permission      string         `json:"permission"`
	ScopeType       string         `json:"scope_type"`
	ScopeID         string         `json:"scope_id"`
	ProductID       string         `json:"product_id"`
	Action          string         `json:"action"`
	TargetType      string         `json:"target_type"`
	TargetID        string         `json:"target_id"`
	Result          string         `json:"result"`
	ReasonCode      string         `json:"reason_code,omitempty"`
	TraceID         string         `json:"trace_id"`
	RiskLevel       string         `json:"risk_level"`
	RedactedSummary map[string]any `json:"redacted_summary,omitempty"`
}

type OutboxEvent struct {
	EventID     string
	AggregateID string
	EventType   string
	Payload     EventPayload
	OccurredAt  time.Time
}

type ClaimedOutboxEvent struct {
	EventID      string
	AggregateID  string
	EventType    string
	Payload      EventPayload
	OccurredAt   time.Time
	AttemptCount int
}

type CreateBlueprintRecord struct {
	Blueprint   Blueprint
	Idempotency Idempotency
	Event       OutboxEvent
}
type CreatePlanRecord struct {
	Plan        Plan
	Idempotency Idempotency
	Event       OutboxEvent
}
type ConfirmPlanRecord struct {
	ProductID, PlanID, ConfirmedBy string
	ExpectedVersion                int64
	ConfirmedAt                    time.Time
	Idempotency                    Idempotency
	Event                          OutboxEvent
}
type StartRunRecord struct {
	Run         Run
	Idempotency Idempotency
	Event       OutboxEvent
}
type BindProductRecord struct {
	ProductID, RunID string
	ExpectedVersion  int64
	BoundAt          time.Time
	Idempotency      Idempotency
	Event            OutboxEvent
}
type UpdateRunRecord struct {
	Run             Run
	ExpectedVersion int64
	Idempotency     Idempotency
	Event           OutboxEvent
}
type CompleteRunRecord struct {
	Run             Run
	ExpectedVersion int64
	Manifest        Manifest
	Lock            GeneratedProjectLock
	Idempotency     Idempotency
	Event           OutboxEvent
}

type Repository interface {
	CreateBlueprint(context.Context, CreateBlueprintRecord) (Blueprint, error)
	GetBlueprint(context.Context, string, string, int64) (Blueprint, error)
	CreatePlan(context.Context, CreatePlanRecord) (Plan, error)
	GetPlan(context.Context, string, string) (Plan, error)
	ConfirmPlan(context.Context, ConfirmPlanRecord) (Plan, error)
	StartRun(context.Context, StartRunRecord) (Run, error)
	BindProduct(context.Context, BindProductRecord) (Run, error)
	GetRun(context.Context, string, string) (Run, error)
	UpdateRun(context.Context, UpdateRunRecord) (Run, error)
	CompleteRun(context.Context, CompleteRunRecord) (Run, error)
	GetManifest(context.Context, string, string) (Manifest, error)
	GetLock(context.Context, string, string) (GeneratedProjectLock, error)
	ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error)
	MarkOutboxPublished(context.Context, string, time.Time) error
	MarkOutboxFailed(context.Context, string, string, time.Time, bool) error
}
