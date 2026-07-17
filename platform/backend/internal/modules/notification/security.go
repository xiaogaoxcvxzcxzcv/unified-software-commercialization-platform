package notification

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidSecurityDelivery = errors.New("invalid security delivery")
	ErrIdempotencyConflict     = errors.New("NOTIFICATION_IDEMPOTENCY_CONFLICT")
	ErrProviderUnavailable     = errors.New("NOTIFICATION_SECURITY_PROVIDER_UNAVAILABLE")
	ErrDeliveryRejected        = errors.New("NOTIFICATION_SECURITY_DELIVERY_REJECTED")
	ErrPayloadUnavailable      = errors.New("notification security payload unavailable")
	ErrNotFound                = errors.New("notification security delivery not found")
	ErrLeaseLost               = errors.New("notification security delivery lease lost")
)

type SecurityDeliveryCommand struct {
	DeliveryID      string
	Purpose         string
	ProductID       string
	ApplicationID   string
	TenantID        *string
	ProviderRef     string
	DestinationType string
	Destination     string
	Proof           string
	ExpiresAt       time.Time
	TraceID         string
}

type ProtectedSecurityPayload struct {
	KeyRef     string
	Nonce      []byte
	Ciphertext []byte
	Digest     []byte
}

type SecurityPayload struct {
	Destination string
	Proof       string
}

type SecurityPayloadContext struct {
	DeliveryID      string
	Purpose         string
	ProductID       string
	ApplicationID   string
	TenantID        *string
	ProviderRef     string
	DestinationType string
	ExpiresAt       time.Time
	TraceID         string
}

type SecurityPayloadProtector interface {
	Seal(context.Context, SecurityPayloadContext, SecurityPayload) (ProtectedSecurityPayload, error)
	Open(context.Context, SecurityPayloadContext, ProtectedSecurityPayload) (SecurityPayload, error)
}

type SecretResolver interface {
	ResolveSecret(context.Context, string) ([]byte, error)
}

type SecurityDigestPort interface {
	Digest(context.Context, string, ...string) ([]byte, error)
}

type SecurityProviderCapability struct {
	DeliveryIDIdempotent bool
}

type SecurityProviderGateway interface {
	// RequireSecurityProvider must only return a capability when the provider
	// guarantees that repeated calls with one DeliveryID produce one effect.
	RequireSecurityProvider(context.Context, string) (SecurityProviderCapability, error)
	DeliverSecurity(context.Context, SecurityProviderRequest) (SecurityProviderResult, error)
}

type SecurityProviderRequest struct {
	DeliveryID      string
	Purpose         string
	ProductID       string
	ApplicationID   string
	TenantID        *string
	ProviderRef     string
	DestinationType string
	Destination     string
	Proof           string
	ExpiresAt       time.Time
	TraceID         string
	Secret          []byte
}

type SecurityProviderResult struct {
	MessageRef string
}

type SecurityDelivery struct {
	DeliveryID      string
	RequestDigest   []byte
	Purpose         string
	ProductID       string
	ApplicationID   string
	TenantID        *string
	ProviderRef     string
	DestinationType string
	Payload         ProtectedSecurityPayload
	Status          string
	AttemptCount    int
	MaxAttempts     int
	NextAttemptAt   time.Time
	LeaseOwner      string
	LeaseStartedAt  time.Time
	LeaseExpiresAt  *time.Time
	CreatedAt       time.Time
	DeliveredAt     *time.Time
	DeadAt          *time.Time
	ExpiresAt       time.Time
	TraceID         string
}

type SecurityOutboxEvent struct {
	EventID    string
	DeliveryID string
	EventType  string
	Payload    []byte
	OccurredAt time.Time
}

type CreateSecurityDeliveryRecord struct {
	Delivery SecurityDelivery
	Event    SecurityOutboxEvent
}

type SecurityDeliveryAttempt struct {
	AttemptID             string
	DeliveryID            string
	AttemptNumber         int
	Outcome               string
	ProviderMessageDigest []byte
	ErrorCode             *string
	ErrorDigest           []byte
	StartedAt             time.Time
	FinishedAt            time.Time
}

type CompleteSecurityDeliveryRecord struct {
	DeliveryID string
	LeaseOwner string
	Attempt    SecurityDeliveryAttempt
	NextStatus string
	RetryDelay time.Duration
}

type SecurityRepository interface {
	CreateSecurityDelivery(context.Context, CreateSecurityDeliveryRecord) (bool, error)
	ClaimSecurityDelivery(context.Context, string, time.Duration) (SecurityDelivery, error)
	CompleteSecurityDelivery(context.Context, CompleteSecurityDeliveryRecord) error
}
