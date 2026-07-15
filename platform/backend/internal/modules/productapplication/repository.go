package productapplication

import (
	"context"
	"encoding/json"
	"time"
)

type CreateRecord struct {
	Application Application
	ActorID     string
	TraceID     string
	Idempotency Idempotency
	EventID     string
}

type BindRecord struct {
	Binding     ClientBinding
	ActorID     string
	TraceID     string
	Idempotency Idempotency
	EventID     string
}

type RedirectRecord struct {
	Policy  RedirectPolicy
	Version RedirectPolicyVersion
	ActorID string
	TraceID string
	EventID string
}

type SuspendRecord struct {
	ProductID     string
	ApplicationID string
	Reason        string
	Policy        SessionPolicy
	ActorID       string
	TraceID       string
	Now           time.Time
	AuditID       string
	EventID       string
	Idempotency   Idempotency
}

type Idempotency struct {
	Operation     string
	ActorID       string
	ScopeID       string
	KeyDigest     string
	RequestDigest string
	Now           time.Time
}

type ResolveQuery struct {
	ProductID   string
	ClientID    string
	Environment Environment
}

type ClaimedOutboxEvent struct {
	EventID      string
	AggregateID  string
	EventType    string
	Payload      json.RawMessage
	AttemptCount int
}

type Repository interface {
	CreateApplication(context.Context, CreateRecord) (Application, error)
	ListApplications(context.Context, string) ([]Application, error)
	GetApplication(context.Context, string, string) (Application, error)
	BindClient(context.Context, BindRecord) (ClientBinding, error)
	ReplaceRedirects(context.Context, RedirectRecord) (RedirectPolicyVersion, error)
	SuspendApplication(context.Context, SuspendRecord) (SuspendResult, error)
	ResolveApplication(context.Context, ResolveQuery) (Application, ClientBinding, error)
	ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error)
	MarkOutboxPublished(context.Context, string, time.Time) error
	MarkOutboxFailed(context.Context, string, string, time.Time, bool) error
}
