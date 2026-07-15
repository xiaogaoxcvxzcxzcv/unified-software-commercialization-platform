package product

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	ErrNotFound                  = errors.New("product resource not found")
	ErrConflict                  = errors.New("product resource conflict")
	ErrInvalidCommand            = errors.New("invalid product command")
	ErrIdempotencyConflict       = errors.New("product idempotency conflict")
	ErrProvisioningState         = errors.New("invalid product provisioning state")
	ErrProductUnavailable        = errors.New("product unavailable")
	ErrClientUnavailable         = errors.New("product client unavailable")
	ErrCredentialUnavailable     = errors.New("product client credential unavailable")
	ErrCredentialVersionConflict = errors.New("product client credential version conflict")
	ErrNonceReplayed             = errors.New("client proof nonce replayed")
	ErrSessionUnavailable        = errors.New("client session unavailable")
	ErrCapabilityVersionConflict = errors.New("product capability version conflict")
	ErrUntrustedChangePlan       = errors.New("untrusted product capability change plan")
)

var stableCodePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
var sha256Pattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type Product struct {
	ProductID         string    `json:"product_id"`
	ProductCode       string    `json:"product_code"`
	Name              string    `json:"name"`
	Status            string    `json:"status"`
	ProvisioningState string    `json:"provisioning_state"`
	OfficialTenantID  string    `json:"official_tenant_id,omitempty"`
	ContextVersion    int64     `json:"context_version"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	AuditID           string    `json:"audit_id,omitempty"`
}

type ProductContext struct {
	ProductID      string `json:"product_id"`
	ProductCode    string `json:"product_code"`
	Environment    string `json:"environment"`
	ContextVersion int64  `json:"context_version"`
}

type ProductPage struct {
	Items []Product
}

type Client struct {
	ClientID       string    `json:"client_id"`
	ProductID      string    `json:"product_id"`
	Environment    string    `json:"environment"`
	Status         string    `json:"status"`
	ContextVersion int64     `json:"context_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ClientCredential struct {
	CredentialID string     `json:"credential_id"`
	ClientID     string     `json:"client_id"`
	ProductID    string     `json:"product_id"`
	ProofType    string     `json:"proof_type"`
	ProofDigest  string     `json:"-"`
	PublicKey    string     `json:"public_key,omitempty"`
	Generation   int        `json:"generation"`
	Status       string     `json:"status"`
	NotBefore    time.Time  `json:"not_before"`
	ExpiresAt    time.Time  `json:"expires_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	AuditID      string     `json:"audit_id,omitempty"`
}

type ClientAuthentication struct {
	Client     Client
	Credential ClientCredential
	Context    ProductContext
}

type ClientProof struct {
	Type      string
	Value     string
	Payload   []byte
	Signature []byte
}

type StoredClientSession struct {
	SessionID                 string     `json:"session_id"`
	TokenDigest               string     `json:"-"`
	ProductID                 string     `json:"product_id"`
	Environment               string     `json:"environment"`
	ApplicationID             string     `json:"application_id"`
	TenantID                  string     `json:"tenant_id"`
	ClientID                  string     `json:"client_id"`
	CredentialID              string     `json:"credential_id"`
	ClientVersion             string     `json:"client_version"`
	ProductContextVersion     int64      `json:"product_context_version"`
	ApplicationContextVersion int64      `json:"application_context_version"`
	TenantContextVersion      int64      `json:"tenant_context_version"`
	CreatedAt                 time.Time  `json:"created_at"`
	ExpiresAt                 time.Time  `json:"expires_at"`
	RevokedAt                 *time.Time `json:"revoked_at,omitempty"`
}

type IssuedClientSession struct {
	StoredClientSession
	Token string `json:"client_session_token"`
}

type ResolvedSessionScope struct {
	ProductID                 string
	Environment               string
	ApplicationID             string
	TenantID                  string
	ApplicationContextVersion int64
	TenantContextVersion      int64
}

type CapabilityItem struct {
	CapabilityID         string          `json:"capability_id"`
	Enabled              bool            `json:"enabled"`
	Policy               json.RawMessage `json:"policy"`
	SourcePackageID      string          `json:"source_package_id"`
	SourcePackageVersion string          `json:"source_package_version"`
}

type CapabilitySet struct {
	CapabilitySetID       string           `json:"capability_set_id"`
	ProductID             string           `json:"product_id"`
	Version               int64            `json:"version"`
	SourcePlanID          string           `json:"source_plan_id"`
	CatalogRevision       string           `json:"catalog_revision"`
	CatalogSnapshotSHA256 string           `json:"catalog_snapshot_sha256"`
	ContentSHA256         string           `json:"content_sha256"`
	CreatedBy             string           `json:"created_by"`
	CreatedAt             time.Time        `json:"created_at"`
	Items                 []CapabilityItem `json:"capabilities"`
	AuditID               string           `json:"audit_id,omitempty"`
}

type TrustedCapabilityChangePlan struct {
	ProductID             string
	SourcePlanID          string
	CatalogRevision       string
	CatalogSnapshotSHA256 string
	Items                 []CapabilityItem
}

type CapabilityChangePlanVerifier interface {
	ResolveProductCapabilityChange(context.Context, TrustedCapabilityChangePlan) (TrustedCapabilityChangePlan, error)
}

type ClientProofVerifier interface {
	VerifyClientProof(context.Context, ClientAuthentication, ClientProof) error
}

type IDGenerator func(prefix string) (string, error)
type TokenIssuer func() (token string, digest string, err error)

type Idempotency struct {
	Operation     string
	ActorID       string
	ScopeID       string
	KeyDigest     string
	RequestDigest string
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

// EventPayload is owned by Product and can be mapped to Audit by the
// composition root without creating a module dependency on Audit.
type EventPayload struct {
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

type BeginProvisioningRecord struct {
	Product      Product
	Environments []string
	Idempotency  Idempotency
	Event        OutboxEvent
}

type CompleteProvisioningRecord struct {
	ProductID        string
	OfficialTenantID string
	Now              time.Time
	Idempotency      Idempotency
	Event            OutboxEvent
}

type RegisterClientRecord struct {
	Client      Client
	Credential  ClientCredential
	Idempotency Idempotency
	Event       OutboxEvent
}

type RotateCredentialRecord struct {
	ProductID          string
	ClientID           string
	ExpectedGeneration int
	Credential         ClientCredential
	Now                time.Time
	Idempotency        Idempotency
	Event              OutboxEvent
}

type RevokeCredentialRecord struct {
	ProductID    string
	ClientID     string
	CredentialID string
	Now          time.Time
	Idempotency  Idempotency
	Event        OutboxEvent
}

type CreateSessionRecord struct {
	Authentication ClientAuthentication
	Session        StoredClientSession
	NonceDigest    string
	RequestDigest  string
	Event          OutboxEvent
}

type ReplaceCapabilitySetRecord struct {
	Set             CapabilitySet
	ExpectedVersion int64
	Idempotency     Idempotency
	Event           OutboxEvent
}

type Repository interface {
	BeginProvisioning(context.Context, BeginProvisioningRecord) (Product, error)
	CompleteProvisioning(context.Context, CompleteProvisioningRecord) (Product, error)
	ListProducts(context.Context, int) ([]Product, error)
	GetProduct(context.Context, string) (Product, error)
	RegisterClient(context.Context, RegisterClientRecord) (ClientAuthentication, error)
	RotateClientCredential(context.Context, RotateCredentialRecord) (ClientCredential, error)
	RevokeClientCredential(context.Context, RevokeCredentialRecord) (ClientCredential, error)
	ResolveClientAuthentication(context.Context, string, string, time.Time) (ClientAuthentication, error)
	CreateClientSession(context.Context, CreateSessionRecord) (StoredClientSession, error)
	FindClientSessionByTokenDigest(context.Context, string, time.Time) (StoredClientSession, error)
	RevokeClientSession(context.Context, string, time.Time) error
	ReplaceCapabilitySet(context.Context, ReplaceCapabilitySetRecord) (CapabilitySet, error)
	CurrentCapabilitySet(context.Context, string) (CapabilitySet, error)
	AppendOutboxEvent(context.Context, OutboxEvent) error
	ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error)
	MarkOutboxPublished(context.Context, string, time.Time) error
	MarkOutboxFailed(context.Context, string, string, time.Time, bool) error
}

type Service struct {
	repository    Repository
	planVerifier  CapabilityChangePlanVerifier
	proofVerifier ClientProofVerifier
	idGenerator   IDGenerator
	tokenIssuer   TokenIssuer
	now           func() time.Time
}

func NewService(repository Repository, planVerifier CapabilityChangePlanVerifier, proofVerifier ClientProofVerifier, idGenerator IDGenerator, tokenIssuer TokenIssuer, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, planVerifier: planVerifier, proofVerifier: proofVerifier, idGenerator: idGenerator, tokenIssuer: tokenIssuer, now: now}
}

type BeginProvisioningCommand struct {
	ProductCode    string
	Name           string
	Status         string
	Environments   []string
	ActorID        string
	IdempotencyKey string
	TraceID        string
}

func (s *Service) BeginProvisioning(ctx context.Context, command BeginProvisioningCommand) (Product, error) {
	if s.repository == nil || s.idGenerator == nil {
		return Product{}, ErrInvalidCommand
	}
	command.ProductCode = strings.TrimSpace(command.ProductCode)
	command.Name = strings.TrimSpace(command.Name)
	if !stableCodePattern.MatchString(command.ProductCode) || command.Name == "" || len(command.Name) > 120 || (command.Status != "active" && command.Status != "suspended") {
		return Product{}, ErrInvalidCommand
	}
	environments, err := normalizeEnvironments(command.Environments)
	if err != nil || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return Product{}, ErrInvalidCommand
	}
	productID, err := s.idGenerator("prod_")
	if err != nil {
		return Product{}, err
	}
	eventID, err := s.idGenerator("evt_")
	if err != nil {
		return Product{}, err
	}
	now := s.now().UTC()
	request := struct {
		ProductCode  string   `json:"product_code"`
		Name         string   `json:"name"`
		Status       string   `json:"status"`
		Environments []string `json:"environments"`
	}{command.ProductCode, command.Name, command.Status, environments}
	idempotency, err := makeIdempotency("product.begin_provisioning", command.ActorID, "platform", command.IdempotencyKey, request)
	if err != nil {
		return Product{}, err
	}
	product := Product{ProductID: productID, ProductCode: command.ProductCode, Name: command.Name, Status: command.Status, ProvisioningState: "pending", ContextVersion: 1, CreatedAt: now, UpdatedAt: now}
	event := makeEvent(eventID, productID, "product.provisioning_started.v1", now, EventPayload{
		ActorID: command.ActorID, Permission: "product.manage", ScopeType: "platform",
		Action: "product.provisioning_started", TargetType: "product", TargetID: productID,
		Result: "success", TraceID: command.TraceID, RiskLevel: "normal",
		RedactedSummary: map[string]any{"product_code": command.ProductCode},
	})
	return s.repository.BeginProvisioning(ctx, BeginProvisioningRecord{Product: product, Environments: environments, Idempotency: idempotency, Event: event})
}

type CompleteProvisioningCommand struct {
	ProductID        string
	OfficialTenantID string
	ActorID          string
	IdempotencyKey   string
	TraceID          string
}

func (s *Service) CompleteProvisioning(ctx context.Context, command CompleteProvisioningCommand) (Product, error) {
	if s.repository == nil || s.idGenerator == nil || command.ProductID == "" || command.OfficialTenantID == "" || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return Product{}, ErrInvalidCommand
	}
	eventID, err := s.idGenerator("evt_")
	if err != nil {
		return Product{}, err
	}
	request := struct{ ProductID, OfficialTenantID string }{command.ProductID, command.OfficialTenantID}
	idempotency, err := makeIdempotency("product.complete_provisioning", command.ActorID, command.ProductID, command.IdempotencyKey, request)
	if err != nil {
		return Product{}, err
	}
	now := s.now().UTC()
	event := makeEvent(eventID, command.ProductID, "product.created.v1", now, EventPayload{
		ActorID: command.ActorID, Permission: "product.manage", ScopeType: "product", ScopeID: command.ProductID, ProductID: command.ProductID,
		Action: "product.created", TargetType: "product", TargetID: command.ProductID, Result: "success", TraceID: command.TraceID, RiskLevel: "normal",
		RedactedSummary: map[string]any{"official_tenant_id": command.OfficialTenantID},
	})
	return s.repository.CompleteProvisioning(ctx, CompleteProvisioningRecord{ProductID: command.ProductID, OfficialTenantID: command.OfficialTenantID, Now: now, Idempotency: idempotency, Event: event})
}

func (s *Service) ListProducts(ctx context.Context, limit int) ([]Product, error) {
	if s.repository == nil || limit < 1 || limit > 200 {
		return nil, ErrInvalidCommand
	}
	return s.repository.ListProducts(ctx, limit)
}

func (s *Service) GetProduct(ctx context.Context, productID string) (Product, error) {
	if s.repository == nil || productID == "" {
		return Product{}, ErrInvalidCommand
	}
	return s.repository.GetProduct(ctx, productID)
}

type RegisterClientCommand struct {
	ProductID      string
	Environment    string
	ProofType      string
	ProofDigest    string
	PublicKey      string
	NotBefore      time.Time
	ExpiresAt      time.Time
	ActorID        string
	IdempotencyKey string
	TraceID        string
}

func (s *Service) RegisterClient(ctx context.Context, command RegisterClientCommand) (ClientAuthentication, error) {
	if s.repository == nil || s.idGenerator == nil || command.ProductID == "" || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" || !validEnvironment(command.Environment) {
		return ClientAuthentication{}, ErrInvalidCommand
	}
	if err := validateProofMaterial(command.ProofType, command.ProofDigest, command.PublicKey); err != nil || command.NotBefore.IsZero() || !command.ExpiresAt.After(command.NotBefore) {
		return ClientAuthentication{}, ErrInvalidCommand
	}
	clientID, err := s.idGenerator("pcli_")
	if err != nil {
		return ClientAuthentication{}, err
	}
	credentialID, err := s.idGenerator("pcred_")
	if err != nil {
		return ClientAuthentication{}, err
	}
	eventID, err := s.idGenerator("evt_")
	if err != nil {
		return ClientAuthentication{}, err
	}
	request := struct {
		ProductID, Environment, ProofType, ProofDigest, PublicKey string
		NotBefore, ExpiresAt                                      time.Time
	}{command.ProductID, command.Environment, command.ProofType, command.ProofDigest, command.PublicKey, command.NotBefore.UTC(), command.ExpiresAt.UTC()}
	idempotency, err := makeIdempotency("product.register_client", command.ActorID, command.ProductID, command.IdempotencyKey, request)
	if err != nil {
		return ClientAuthentication{}, err
	}
	now := s.now().UTC()
	client := Client{ClientID: clientID, ProductID: command.ProductID, Environment: command.Environment, Status: "active", ContextVersion: 1, CreatedAt: now, UpdatedAt: now}
	credential := ClientCredential{CredentialID: credentialID, ClientID: clientID, ProductID: command.ProductID, ProofType: command.ProofType, ProofDigest: command.ProofDigest, PublicKey: command.PublicKey, Generation: 1, Status: "active", NotBefore: command.NotBefore.UTC(), ExpiresAt: command.ExpiresAt.UTC(), CreatedAt: now}
	event := makeEvent(eventID, clientID, "product.client_registered.v1", now, EventPayload{
		ActorID: command.ActorID, Permission: "product.application.security.manage", ScopeType: "product", ScopeID: command.ProductID, ProductID: command.ProductID,
		Action: "product.client_registered", TargetType: "product_client", TargetID: clientID, Result: "success", TraceID: command.TraceID, RiskLevel: "high",
		RedactedSummary: map[string]any{"credential_id": credentialID, "proof_type": command.ProofType},
	})
	credential.AuditID = event.Payload.AuditID
	return s.repository.RegisterClient(ctx, RegisterClientRecord{Client: client, Credential: credential, Idempotency: idempotency, Event: event})
}

type RotateClientCredentialCommand struct {
	ProductID          string
	ClientID           string
	ExpectedGeneration int
	ProofType          string
	ProofDigest        string
	PublicKey          string
	NotBefore          time.Time
	ExpiresAt          time.Time
	ActorID            string
	IdempotencyKey     string
	TraceID            string
}

func (s *Service) RotateClientCredential(ctx context.Context, command RotateClientCredentialCommand) (ClientCredential, error) {
	if s.repository == nil || s.idGenerator == nil || command.ProductID == "" || command.ClientID == "" || command.ExpectedGeneration < 1 || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return ClientCredential{}, ErrInvalidCommand
	}
	if err := validateProofMaterial(command.ProofType, command.ProofDigest, command.PublicKey); err != nil || command.NotBefore.IsZero() || !command.ExpiresAt.After(command.NotBefore) {
		return ClientCredential{}, ErrInvalidCommand
	}
	credentialID, err := s.idGenerator("pcred_")
	if err != nil {
		return ClientCredential{}, err
	}
	eventID, err := s.idGenerator("evt_")
	if err != nil {
		return ClientCredential{}, err
	}
	request := struct {
		ProductID, ClientID               string
		ExpectedGeneration                int
		ProofType, ProofDigest, PublicKey string
		NotBefore, ExpiresAt              time.Time
	}{command.ProductID, command.ClientID, command.ExpectedGeneration, command.ProofType, command.ProofDigest, command.PublicKey, command.NotBefore.UTC(), command.ExpiresAt.UTC()}
	idempotency, err := makeIdempotency("product.rotate_client_credential", command.ActorID, command.ProductID, command.IdempotencyKey, request)
	if err != nil {
		return ClientCredential{}, err
	}
	now := s.now().UTC()
	credential := ClientCredential{CredentialID: credentialID, ClientID: command.ClientID, ProductID: command.ProductID, ProofType: command.ProofType, ProofDigest: command.ProofDigest, PublicKey: command.PublicKey, Generation: command.ExpectedGeneration + 1, Status: "active", NotBefore: command.NotBefore.UTC(), ExpiresAt: command.ExpiresAt.UTC(), CreatedAt: now}
	event := makeEvent(eventID, command.ClientID, "product.client_credential_rotated.v1", now, EventPayload{
		ActorID: command.ActorID, Permission: "product.application.security.manage", ScopeType: "product", ScopeID: command.ProductID, ProductID: command.ProductID,
		Action: "product.client_credential_rotated", TargetType: "product_client", TargetID: command.ClientID, Result: "success", TraceID: command.TraceID, RiskLevel: "high",
		RedactedSummary: map[string]any{"credential_id": credentialID, "generation": credential.Generation},
	})
	credential.AuditID = event.Payload.AuditID
	return s.repository.RotateClientCredential(ctx, RotateCredentialRecord{ProductID: command.ProductID, ClientID: command.ClientID, ExpectedGeneration: command.ExpectedGeneration, Credential: credential, Now: now, Idempotency: idempotency, Event: event})
}

type RevokeClientCredentialCommand struct {
	ProductID, ClientID, CredentialID, ActorID, IdempotencyKey string
	TraceID                                                    string
}

func (s *Service) RevokeClientCredential(ctx context.Context, command RevokeClientCredentialCommand) (ClientCredential, error) {
	if s.repository == nil || s.idGenerator == nil || command.ProductID == "" || command.ClientID == "" || command.CredentialID == "" || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" {
		return ClientCredential{}, ErrInvalidCommand
	}
	eventID, err := s.idGenerator("evt_")
	if err != nil {
		return ClientCredential{}, err
	}
	request := struct{ ProductID, ClientID, CredentialID string }{command.ProductID, command.ClientID, command.CredentialID}
	idempotency, err := makeIdempotency("product.revoke_client_credential", command.ActorID, command.ProductID, command.IdempotencyKey, request)
	if err != nil {
		return ClientCredential{}, err
	}
	now := s.now().UTC()
	event := makeEvent(eventID, command.ClientID, "product.client_credential_revoked.v1", now, EventPayload{
		ActorID: command.ActorID, Permission: "product.application.security.manage", ScopeType: "product", ScopeID: command.ProductID, ProductID: command.ProductID,
		Action: "product.client_credential_revoked", TargetType: "product_client", TargetID: command.ClientID, Result: "success", TraceID: command.TraceID, RiskLevel: "high",
		RedactedSummary: map[string]any{"credential_id": command.CredentialID},
	})
	return s.repository.RevokeClientCredential(ctx, RevokeCredentialRecord{ProductID: command.ProductID, ClientID: command.ClientID, CredentialID: command.CredentialID, Now: now, Idempotency: idempotency, Event: event})
}

type CreateClientSessionCommand struct {
	ClientID      string
	CredentialID  string
	Proof         ClientProof
	RequestNonce  string
	ClientVersion string
	Scope         ResolvedSessionScope
	TTL           time.Duration
	TraceID       string
}

func (s *Service) CreateClientSession(ctx context.Context, command CreateClientSessionCommand) (IssuedClientSession, error) {
	if s.repository == nil || s.proofVerifier == nil || s.idGenerator == nil || s.tokenIssuer == nil || command.ClientID == "" || command.CredentialID == "" || len(command.RequestNonce) < 16 || command.ClientVersion == "" || command.TTL <= 0 || command.TraceID == "" {
		return IssuedClientSession{}, ErrInvalidCommand
	}
	now := s.now().UTC()
	authentication, err := s.repository.ResolveClientAuthentication(ctx, command.ClientID, command.CredentialID, now)
	if err != nil {
		if auditErr := s.recordClientSessionDenied(ctx, command, "invalid_client", now); auditErr != nil {
			return IssuedClientSession{}, auditErr
		}
		return IssuedClientSession{}, err
	}
	if authentication.Client.ProductID != command.Scope.ProductID || authentication.Client.Environment != command.Scope.Environment || command.Scope.ApplicationID == "" || command.Scope.TenantID == "" || command.Scope.ApplicationContextVersion < 1 || command.Scope.TenantContextVersion < 1 {
		if auditErr := s.recordClientSessionDenied(ctx, command, "scope_mismatch", now); auditErr != nil {
			return IssuedClientSession{}, auditErr
		}
		return IssuedClientSession{}, ErrClientUnavailable
	}
	if err := s.proofVerifier.VerifyClientProof(ctx, authentication, command.Proof); err != nil {
		if auditErr := s.recordClientSessionDenied(ctx, command, "invalid_proof", now); auditErr != nil {
			return IssuedClientSession{}, auditErr
		}
		return IssuedClientSession{}, ErrCredentialUnavailable
	}
	sessionID, err := s.idGenerator("csess_")
	if err != nil {
		return IssuedClientSession{}, err
	}
	eventID, err := s.idGenerator("evt_")
	if err != nil {
		return IssuedClientSession{}, err
	}
	token, tokenDigest, err := s.tokenIssuer()
	if err != nil {
		return IssuedClientSession{}, err
	}
	if token == "" || !sha256Pattern.MatchString(tokenDigest) || token == tokenDigest {
		return IssuedClientSession{}, ErrInvalidCommand
	}
	nonceDigest := digestString(command.RequestNonce)
	requestDigest, err := digestJSON(struct {
		ClientID, CredentialID, ClientVersion string
		Scope                                 ResolvedSessionScope
	}{command.ClientID, command.CredentialID, command.ClientVersion, command.Scope})
	if err != nil {
		return IssuedClientSession{}, err
	}
	stored := StoredClientSession{SessionID: sessionID, TokenDigest: tokenDigest, ProductID: authentication.Client.ProductID, Environment: authentication.Client.Environment, ApplicationID: command.Scope.ApplicationID, TenantID: command.Scope.TenantID, ClientID: command.ClientID, CredentialID: command.CredentialID, ClientVersion: command.ClientVersion, ProductContextVersion: authentication.Context.ContextVersion, ApplicationContextVersion: command.Scope.ApplicationContextVersion, TenantContextVersion: command.Scope.TenantContextVersion, CreatedAt: now, ExpiresAt: now.Add(command.TTL)}
	event := makeEvent(eventID, command.ClientID, "client.session_created.v1", now, EventPayload{
		ActorID: "product_client:" + command.ClientID, ScopeType: "tenant", ScopeID: command.Scope.TenantID, ProductID: authentication.Client.ProductID, TenantID: command.Scope.TenantID,
		Action: "client.session_created", TargetType: "client_session", TargetID: sessionID, Result: "success", TraceID: command.TraceID, RiskLevel: "normal",
		RedactedSummary: map[string]any{"application_id": command.Scope.ApplicationID, "credential_id": command.CredentialID},
	})
	stored, err = s.repository.CreateClientSession(ctx, CreateSessionRecord{Authentication: authentication, Session: stored, NonceDigest: nonceDigest, RequestDigest: requestDigest, Event: event})
	if err != nil {
		if errors.Is(err, ErrNonceReplayed) {
			if auditErr := s.recordClientSessionDenied(ctx, command, "nonce_replayed", now); auditErr != nil {
				return IssuedClientSession{}, auditErr
			}
		}
		return IssuedClientSession{}, err
	}
	return IssuedClientSession{StoredClientSession: stored, Token: token}, nil
}

func (s *Service) recordClientSessionDenied(ctx context.Context, command CreateClientSessionCommand, reason string, now time.Time) error {
	if s.repository == nil || s.idGenerator == nil {
		return ErrInvalidCommand
	}
	eventID, err := s.idGenerator("evt_")
	if err != nil {
		return err
	}
	event := makeEvent(eventID, command.ClientID, "client.session_denied.v1", now, EventPayload{
		ActorID: "anonymous_client", Action: "client.session_denied", TargetType: "product_client", TargetID: command.ClientID,
		Result: "denied", ReasonCode: reason, TraceID: command.TraceID, RiskLevel: "high",
		RedactedSummary: map[string]any{"credential_id": command.CredentialID},
	})
	return s.repository.AppendOutboxEvent(ctx, event)
}

func (s *Service) ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]ClaimedOutboxEvent, error) {
	if s.repository == nil || limit < 1 || limit > 200 {
		return nil, ErrInvalidCommand
	}
	return s.repository.ClaimOutbox(ctx, now.UTC(), limit)
}

func (s *Service) MarkOutboxPublished(ctx context.Context, eventID string, now time.Time) error {
	if s.repository == nil || eventID == "" {
		return ErrInvalidCommand
	}
	return s.repository.MarkOutboxPublished(ctx, eventID, now.UTC())
}

func (s *Service) MarkOutboxFailed(ctx context.Context, eventID, summary string, next time.Time, dead bool) error {
	if s.repository == nil || eventID == "" || len(summary) > 500 || next.IsZero() {
		return ErrInvalidCommand
	}
	return s.repository.MarkOutboxFailed(ctx, eventID, summary, next.UTC(), dead)
}

func (s *Service) ResolveProductContext(ctx context.Context, clientID, credentialID string) (ProductContext, error) {
	if s.repository == nil || clientID == "" || credentialID == "" {
		return ProductContext{}, ErrInvalidCommand
	}
	authentication, err := s.repository.ResolveClientAuthentication(ctx, clientID, credentialID, s.now().UTC())
	return authentication.Context, err
}

func (s *Service) FindClientSession(ctx context.Context, tokenDigest string) (StoredClientSession, error) {
	if s.repository == nil || tokenDigest == "" {
		return StoredClientSession{}, ErrInvalidCommand
	}
	return s.repository.FindClientSessionByTokenDigest(ctx, tokenDigest, s.now().UTC())
}

func (s *Service) RevokeClientSession(ctx context.Context, sessionID string) error {
	if s.repository == nil || sessionID == "" {
		return ErrInvalidCommand
	}
	return s.repository.RevokeClientSession(ctx, sessionID, s.now().UTC())
}

type ReplaceCapabilitySetCommand struct {
	Plan            TrustedCapabilityChangePlan
	ExpectedVersion int64
	ActorID         string
	IdempotencyKey  string
	TraceID         string
}

func (s *Service) ReplaceCapabilitySet(ctx context.Context, command ReplaceCapabilitySetCommand) (CapabilitySet, error) {
	if s.repository == nil || s.planVerifier == nil || s.idGenerator == nil || command.Plan.ProductID == "" || command.Plan.SourcePlanID == "" || command.Plan.CatalogRevision == "" || !sha256Pattern.MatchString(command.Plan.CatalogSnapshotSHA256) || command.ActorID == "" || command.IdempotencyKey == "" || command.TraceID == "" || command.ExpectedVersion < 0 {
		return CapabilitySet{}, ErrUntrustedChangePlan
	}
	resolvedPlan, err := s.planVerifier.ResolveProductCapabilityChange(ctx, command.Plan)
	if err != nil {
		return CapabilitySet{}, fmt.Errorf("%w: %v", ErrUntrustedChangePlan, err)
	}
	if resolvedPlan.ProductID != command.Plan.ProductID || resolvedPlan.SourcePlanID != command.Plan.SourcePlanID || resolvedPlan.CatalogRevision != command.Plan.CatalogRevision || resolvedPlan.CatalogSnapshotSHA256 != command.Plan.CatalogSnapshotSHA256 {
		return CapabilitySet{}, ErrUntrustedChangePlan
	}
	items, err := normalizeCapabilityItems(resolvedPlan.Items)
	if err != nil {
		return CapabilitySet{}, err
	}
	command.Plan = resolvedPlan
	command.Plan.Items = items
	setID, err := s.idGenerator("pcset_")
	if err != nil {
		return CapabilitySet{}, err
	}
	eventID, err := s.idGenerator("evt_")
	if err != nil {
		return CapabilitySet{}, err
	}
	contentDigest, err := digestJSON(items)
	if err != nil {
		return CapabilitySet{}, err
	}
	request := struct {
		Plan            TrustedCapabilityChangePlan
		ExpectedVersion int64
	}{command.Plan, command.ExpectedVersion}
	idempotency, err := makeIdempotency("product.replace_capability_set", command.ActorID, command.Plan.ProductID, command.IdempotencyKey, request)
	if err != nil {
		return CapabilitySet{}, err
	}
	now := s.now().UTC()
	set := CapabilitySet{CapabilitySetID: setID, ProductID: command.Plan.ProductID, Version: command.ExpectedVersion + 1, SourcePlanID: command.Plan.SourcePlanID, CatalogRevision: command.Plan.CatalogRevision, CatalogSnapshotSHA256: command.Plan.CatalogSnapshotSHA256, ContentSHA256: contentDigest, CreatedBy: command.ActorID, CreatedAt: now, Items: items}
	event := makeEvent(eventID, command.Plan.ProductID, "product.capabilities_changed.v1", now, EventPayload{
		ActorID: command.ActorID, Permission: "product.manage", ScopeType: "product", ScopeID: command.Plan.ProductID, ProductID: command.Plan.ProductID,
		Action: "product.capabilities_changed", TargetType: "product_capability_set", TargetID: setID, Result: "success", TraceID: command.TraceID, RiskLevel: "high",
		RedactedSummary: map[string]any{"version": set.Version, "source_plan_id": command.Plan.SourcePlanID},
	})
	set.AuditID = event.Payload.AuditID
	return s.repository.ReplaceCapabilitySet(ctx, ReplaceCapabilitySetRecord{Set: set, ExpectedVersion: command.ExpectedVersion, Idempotency: idempotency, Event: event})
}

func (s *Service) CurrentCapabilitySet(ctx context.Context, productID string) (CapabilitySet, error) {
	if s.repository == nil || productID == "" {
		return CapabilitySet{}, ErrInvalidCommand
	}
	return s.repository.CurrentCapabilitySet(ctx, productID)
}

func makeIdempotency(operation, actorID, scopeID, key string, request any) (Idempotency, error) {
	requestDigest, err := digestJSON(request)
	if err != nil {
		return Idempotency{}, err
	}
	return Idempotency{Operation: operation, ActorID: actorID, ScopeID: scopeID, KeyDigest: digestString(key), RequestDigest: requestDigest}, nil
}

func makeEvent(eventID, aggregateID, eventType string, occurredAt time.Time, payload EventPayload) OutboxEvent {
	payload.AuditID = "audit_" + strings.TrimPrefix(eventID, "evt_")
	payload.OccurredAt = occurredAt.UTC()
	return OutboxEvent{EventID: eventID, AggregateID: aggregateID, EventType: eventType, Payload: payload, OccurredAt: occurredAt}
}

func digestJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func digestString(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func DigestsEqual(first, second string) bool {
	return len(first) == len(second) && subtle.ConstantTimeCompare([]byte(first), []byte(second)) == 1
}

func normalizeEnvironments(values []string) ([]string, error) {
	if len(values) == 0 {
		values = []string{"local", "test", "production"}
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !validEnvironment(value) {
			return nil, ErrInvalidCommand
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, ErrInvalidCommand
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func validEnvironment(value string) bool {
	return value == "local" || value == "test" || value == "production"
}

func validateProofMaterial(proofType, proofDigest, publicKey string) error {
	switch proofType {
	case "hmac_sha256_v1":
		if !sha256Pattern.MatchString(proofDigest) || publicKey != "" {
			return ErrInvalidCommand
		}
	case "ed25519_signature_v1":
		if proofDigest != "" || strings.TrimSpace(publicKey) == "" || len(publicKey) > 512 {
			return ErrInvalidCommand
		}
	default:
		return ErrInvalidCommand
	}
	return nil
}

func normalizeCapabilityItems(items []CapabilityItem) ([]CapabilityItem, error) {
	result := append([]CapabilityItem(nil), items...)
	seen := make(map[string]struct{}, len(result))
	for index := range result {
		item := &result[index]
		if !stableCodePattern.MatchString(item.CapabilityID) || !stableCodePattern.MatchString(item.SourcePackageID) || item.SourcePackageVersion == "" {
			return nil, ErrInvalidCommand
		}
		if _, duplicate := seen[item.CapabilityID]; duplicate {
			return nil, ErrInvalidCommand
		}
		seen[item.CapabilityID] = struct{}{}
		if len(item.Policy) == 0 {
			item.Policy = json.RawMessage(`{}`)
		}
		var policy any
		if err := json.Unmarshal(item.Policy, &policy); err != nil {
			return nil, ErrInvalidCommand
		}
		if _, ok := policy.(map[string]any); !ok {
			return nil, ErrInvalidCommand
		}
		normalizedPolicy, err := json.Marshal(policy)
		if err != nil {
			return nil, ErrInvalidCommand
		}
		item.Policy = normalizedPolicy
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CapabilityID < result[j].CapabilityID })
	return result, nil
}
