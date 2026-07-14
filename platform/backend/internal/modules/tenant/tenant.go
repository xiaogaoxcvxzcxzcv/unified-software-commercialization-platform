package tenant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

const (
	TenantTypeOfficial = "official"
	TenantTypeAgent    = "agent"

	TenantStatusActive    = "active"
	TenantStatusSuspended = "suspended"

	ResolutionOfficialChannel = "official_channel"
	ResolutionDistribution    = "distribution"
)

var (
	ErrInvalidCommand        = errors.New("invalid tenant command")
	ErrTenantNotFound        = errors.New("tenant not found")
	ErrTenantSuspended       = errors.New("tenant is suspended")
	ErrTenantCodeConflict    = errors.New("tenant code already exists in product")
	ErrIdempotencyConflict   = errors.New("idempotency key reused with different tenant request")
	ErrInvalidTenantProof    = errors.New("invalid tenant distribution proof")
	ErrResolutionUnsupported = errors.New("tenant resolution method is unsupported")

	stableCodePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
)

// Tenant is a product-owned operating scope. TenantID is never globally
// authoritative without the matching ProductID.
type Tenant struct {
	TenantID         string    `json:"tenant_id"`
	ProductID        string    `json:"product_id"`
	TenantCode       string    `json:"tenant_code"`
	Name             string    `json:"name"`
	TenantType       string    `json:"tenant_type"`
	Status           string    `json:"status"`
	ExternalAgentRef string    `json:"external_agent_ref,omitempty"`
	ContextVersion   int64     `json:"context_version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	AuditID          string    `json:"audit_id,omitempty"`
}

type TenantContext struct {
	ProductID      string `json:"product_id"`
	TenantID       string `json:"tenant_id"`
	TenantType     string `json:"tenant_type"`
	TenantStatus   string `json:"tenant_status"`
	ResolvedBy     string `json:"resolved_by"`
	ContextVersion int64  `json:"context_version"`
}

type EnsureOfficialTenantCommand struct {
	ProductID      string
	Name           string
	ActorID        string
	IdempotencyKey string
	TraceID        string
}

type CreateAgentTenantCommand struct {
	ProductID        string
	TenantCode       string
	Name             string
	Status           string
	ExternalAgentRef string
	ActorID          string
	IdempotencyKey   string
	TraceID          string
}

type ListTenantsCommand struct {
	ProductID string
}

// ResolveTenantContextCommand deliberately has no TenantID field. Agent
// tenancy is selected from a verified distribution proof inside ProductID.
type ResolveTenantContextCommand struct {
	ProductID     string
	ApplicationID string
	Method        string
	ChannelCode   string
	ProofSubject  string
}

type IdempotencyRecord struct {
	Operation     string
	ActorID       string
	ScopeID       string
	KeyDigest     string
	RequestDigest string
	CreatedAt     time.Time
}

type OutboxEvent struct {
	EventID     string
	AggregateID string
	EventType   string
	Payload     map[string]any
	OccurredAt  time.Time
}

type ClaimedOutboxEvent struct {
	OutboxEvent
	AttemptCount int
}

type Repository interface {
	EnsureOfficialTenant(context.Context, Tenant, IdempotencyRecord, OutboxEvent) (Tenant, error)
	CreateAgentTenant(context.Context, Tenant, IdempotencyRecord, OutboxEvent) (Tenant, error)
	ListTenants(context.Context, string) ([]Tenant, error)
	FindOfficialTenant(context.Context, string) (Tenant, error)
	FindTenantByDistribution(context.Context, string, string, string, string) (Tenant, error)
	ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error)
	MarkOutboxPublished(context.Context, string, time.Time) error
	MarkOutboxFailed(context.Context, string, string, time.Time, bool) error
}

type ProofDigester interface {
	DigestHex(string) string
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(service *Service) {
		if now != nil {
			service.now = now
		}
	}
}

func WithIDGenerator(generate func(string) (string, error)) Option {
	return func(service *Service) {
		if generate != nil {
			service.generateID = generate
		}
	}
}

func WithProofDigester(digester ProofDigester) Option {
	return func(service *Service) { service.proofDigester = digester }
}

type Service struct {
	repository    Repository
	now           func() time.Time
	generateID    func(string) (string, error)
	proofDigester ProofDigester
}

func NewService(repository Repository, options ...Option) *Service {
	service := &Service{repository: repository, now: time.Now, generateID: securevalue.ID}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) EnsureOfficialTenant(ctx context.Context, command EnsureOfficialTenantCommand) (Tenant, error) {
	command.ProductID = strings.TrimSpace(command.ProductID)
	command.Name = strings.TrimSpace(command.Name)
	command.ActorID = strings.TrimSpace(command.ActorID)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	if s.repository == nil || !validIdentifier(command.ProductID) || !validIdentifier(command.ActorID) || !validName(command.Name) || !validIdempotencyKey(command.IdempotencyKey) {
		return Tenant{}, ErrInvalidCommand
	}
	now := s.now().UTC()
	tenantID, auditID, eventID, err := s.newIDs()
	if err != nil {
		return Tenant{}, err
	}
	value := Tenant{
		TenantID: tenantID, ProductID: command.ProductID, TenantCode: TenantTypeOfficial,
		Name: command.Name, TenantType: TenantTypeOfficial, Status: TenantStatusActive,
		ContextVersion: 1, CreatedAt: now, UpdatedAt: now, AuditID: auditID,
	}
	record, err := newIdempotencyRecord("tenant.ensure_official", command.ActorID, command.ProductID, command.IdempotencyKey, now, struct {
		ProductID string `json:"product_id"`
		Name      string `json:"name"`
	}{command.ProductID, command.Name})
	if err != nil {
		return Tenant{}, err
	}
	event := tenantEvent(eventID, "tenant.created.v1", value, command.ActorID, command.TraceID, now)
	return s.repository.EnsureOfficialTenant(ctx, value, record, event)
}

func (s *Service) CreateAgentTenant(ctx context.Context, command CreateAgentTenantCommand) (Tenant, error) {
	command.ProductID = strings.TrimSpace(command.ProductID)
	command.TenantCode = strings.TrimSpace(command.TenantCode)
	command.Name = strings.TrimSpace(command.Name)
	command.Status = strings.TrimSpace(command.Status)
	command.ExternalAgentRef = strings.TrimSpace(command.ExternalAgentRef)
	command.ActorID = strings.TrimSpace(command.ActorID)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	if s.repository == nil || !validIdentifier(command.ProductID) || !validIdentifier(command.ActorID) || !stableCodePattern.MatchString(command.TenantCode) || !validIdentifier(command.TenantCode) || command.TenantCode == TenantTypeOfficial || !validName(command.Name) || !validStatus(command.Status) || utf8.RuneCountInString(command.ExternalAgentRef) > 128 || !validIdempotencyKey(command.IdempotencyKey) {
		return Tenant{}, ErrInvalidCommand
	}
	now := s.now().UTC()
	tenantID, auditID, eventID, err := s.newIDs()
	if err != nil {
		return Tenant{}, err
	}
	value := Tenant{
		TenantID: tenantID, ProductID: command.ProductID, TenantCode: command.TenantCode,
		Name: command.Name, TenantType: TenantTypeAgent, Status: command.Status,
		ExternalAgentRef: command.ExternalAgentRef, ContextVersion: 1, CreatedAt: now, UpdatedAt: now,
		AuditID: auditID,
	}
	record, err := newIdempotencyRecord("tenant.create_agent", command.ActorID, command.ProductID, command.IdempotencyKey, now, struct {
		ProductID        string `json:"product_id"`
		TenantCode       string `json:"tenant_code"`
		Name             string `json:"name"`
		Status           string `json:"status"`
		ExternalAgentRef string `json:"external_agent_ref,omitempty"`
	}{command.ProductID, command.TenantCode, command.Name, command.Status, command.ExternalAgentRef})
	if err != nil {
		return Tenant{}, err
	}
	event := tenantEvent(eventID, "tenant.created.v1", value, command.ActorID, command.TraceID, now)
	return s.repository.CreateAgentTenant(ctx, value, record, event)
}

func (s *Service) ListTenants(ctx context.Context, command ListTenantsCommand) ([]Tenant, error) {
	productID := strings.TrimSpace(command.ProductID)
	if s.repository == nil || !validIdentifier(productID) {
		return nil, ErrInvalidCommand
	}
	return s.repository.ListTenants(ctx, productID)
}

func (s *Service) ResolveTenantContext(ctx context.Context, command ResolveTenantContextCommand) (TenantContext, error) {
	productID := strings.TrimSpace(command.ProductID)
	method := strings.TrimSpace(command.Method)
	applicationID := strings.TrimSpace(command.ApplicationID)
	if s.repository == nil || !validIdentifier(productID) || (applicationID != "" && !validIdentifier(applicationID)) {
		return TenantContext{}, ErrInvalidCommand
	}
	var value Tenant
	var err error
	switch method {
	case ResolutionOfficialChannel:
		value, err = s.repository.FindOfficialTenant(ctx, productID)
	case ResolutionDistribution:
		channelCode := strings.TrimSpace(command.ChannelCode)
		proofSubject := command.ProofSubject
		if !stableCodePattern.MatchString(channelCode) || !validIdentifier(channelCode) || strings.TrimSpace(proofSubject) == "" || len(proofSubject) > 4096 || s.proofDigester == nil {
			return TenantContext{}, ErrInvalidTenantProof
		}
		digest := "hmac-sha256:" + s.proofDigester.DigestHex(proofSubject)
		value, err = s.repository.FindTenantByDistribution(ctx, productID, applicationID, channelCode, digest)
		if errors.Is(err, ErrTenantNotFound) {
			return TenantContext{}, ErrInvalidTenantProof
		}
	default:
		return TenantContext{}, ErrResolutionUnsupported
	}
	if err != nil {
		return TenantContext{}, err
	}
	if value.ProductID != productID {
		return TenantContext{}, ErrTenantNotFound
	}
	if value.Status != TenantStatusActive {
		return TenantContext{}, ErrTenantSuspended
	}
	return TenantContext{
		ProductID: value.ProductID, TenantID: value.TenantID, TenantType: value.TenantType,
		TenantStatus: value.Status, ResolvedBy: method, ContextVersion: value.ContextVersion,
	}, nil
}

func (s *Service) ClaimOutbox(ctx context.Context, limit int) ([]ClaimedOutboxEvent, error) {
	if s.repository == nil || limit < 1 || limit > 200 {
		return nil, ErrInvalidCommand
	}
	return s.repository.ClaimOutbox(ctx, s.now().UTC(), limit)
}

func (s *Service) MarkOutboxPublished(ctx context.Context, eventID string) error {
	eventID = strings.TrimSpace(eventID)
	if s.repository == nil || eventID == "" {
		return ErrInvalidCommand
	}
	return s.repository.MarkOutboxPublished(ctx, eventID, s.now().UTC())
}

func (s *Service) MarkOutboxFailed(ctx context.Context, eventID, errorSummary string, retryAt time.Time, dead bool) error {
	eventID = strings.TrimSpace(eventID)
	errorSummary = strings.TrimSpace(errorSummary)
	if s.repository == nil || eventID == "" || errorSummary == "" || retryAt.IsZero() {
		return ErrInvalidCommand
	}
	if summaryRunes := []rune(errorSummary); len(summaryRunes) > 512 {
		errorSummary = string(summaryRunes[:512])
	}
	return s.repository.MarkOutboxFailed(ctx, eventID, errorSummary, retryAt.UTC(), dead)
}

func (s *Service) newIDs() (string, string, string, error) {
	if s.generateID == nil {
		return "", "", "", errors.New("tenant identifier generator is not configured")
	}
	tenantID, err := s.generateID("ten_")
	if err != nil {
		return "", "", "", fmt.Errorf("generate tenant identifier: %w", err)
	}
	auditID, err := s.generateID("audit_")
	if err != nil {
		return "", "", "", fmt.Errorf("generate tenant audit identifier: %w", err)
	}
	eventID, err := s.generateID("evt_")
	if err != nil {
		return "", "", "", fmt.Errorf("generate tenant event identifier: %w", err)
	}
	return tenantID, auditID, eventID, nil
}

func tenantEvent(eventID, eventType string, value Tenant, actorID, traceID string, now time.Time) OutboxEvent {
	return OutboxEvent{
		EventID: eventID, AggregateID: value.TenantID, EventType: eventType, OccurredAt: now,
		Payload: map[string]any{
			"audit_id": value.AuditID, "occurred_at": now, "actor_id": actorID,
			"permission": "tenant.manage", "scope_type": "product", "scope_id": value.ProductID,
			"product_id": value.ProductID, "action": eventType, "target_type": "tenant",
			"target_id": value.TenantID, "result": "success", "trace_id": strings.TrimSpace(traceID),
			"risk_level": "normal", "redacted_summary": map[string]any{
				"tenant_code": value.TenantCode, "tenant_type": value.TenantType, "status": value.Status,
			},
		},
	}
}

func newIdempotencyRecord(operation, actorID, scopeID, key string, now time.Time, request any) (IdempotencyRecord, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return IdempotencyRecord{}, fmt.Errorf("encode tenant idempotency request: %w", err)
	}
	return IdempotencyRecord{
		Operation: operation, ActorID: actorID, ScopeID: scopeID,
		KeyDigest: digestString(key), RequestDigest: digestBytes(raw), CreatedAt: now,
	}, nil
}

func digestString(value string) string { return digestBytes([]byte(value)) }

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validName(value string) bool {
	length := utf8.RuneCountInString(value)
	return length >= 1 && length <= 120
}

func validStatus(value string) bool {
	return value == TenantStatusActive || value == TenantStatusSuspended
}

func validIdempotencyKey(value string) bool {
	length := len(value)
	return length >= 16 && length <= 128
}

func validIdentifier(value string) bool {
	length := utf8.RuneCountInString(value)
	return length >= 1 && length <= 128
}
