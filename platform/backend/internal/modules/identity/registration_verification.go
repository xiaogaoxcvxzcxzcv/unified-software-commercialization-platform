package identity

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

var (
	ErrRegistrationVerificationInvalid  = errors.New("registration verification invalid")
	ErrRegistrationVerificationExpired  = errors.New("registration verification expired")
	ErrRegistrationVerificationReplayed = errors.New("registration verification replayed")
)

type SecurityDeliveryCommand struct {
	DeliveryID  string
	Purpose     string
	Scope       EndUserSessionScope
	Destination NormalizedIdentifier
	Proof       string
	ExpiresAt   time.Time
	TraceID     string
}

type SecurityDeliveryPort interface {
	EnqueueSecurity(context.Context, SecurityDeliveryCommand) error
}

type RegistrationVerificationChallenge struct {
	ChallengeID        string
	ContinuationDigest []byte
	Scope              EndUserSessionScope
	IdentifierType     IdentifierType
	IdentifierDigest   []byte
	ProofDigest        []byte
	DeliveryID         string
	DeliveryStatus     string
	AttemptCount       int
	MaxAttempts        int
	CreatedAt          time.Time
	ExpiresAt          time.Time
	ConsumedAt         *time.Time
}

type RegistrationVerificationRepository interface {
	CreateRegistrationVerificationIdempotent(context.Context, RegistrationVerificationChallenge, EndUserIdempotency) (RegistrationVerificationChallenge, bool, error)
	ActivateRegistrationVerification(context.Context, string) error
	ConsumeRegistrationVerification(context.Context, EndUserSessionScope, IdentifierType, []byte, []byte, []byte, []byte, []byte, time.Time) error
}

type RegistrationVerificationPolicy struct {
	TTL         time.Duration
	MaxAttempts int
}

type StartRegistrationVerificationCommand struct {
	Scope          EndUserSessionScope
	Identifier     string
	IdempotencyKey string
	TraceID        string
}

type RegistrationVerificationService struct {
	repository RegistrationVerificationRepository
	normalizer IdentifierNormalizer
	hasher     securevalue.Hasher
	secrets    securevalue.Generator
	delivery   SecurityDeliveryPort
	policy     RegistrationVerificationPolicy
	now        func() time.Time
}

func NewRegistrationVerificationService(repository RegistrationVerificationRepository, normalizer IdentifierNormalizer, hasher securevalue.Hasher, delivery SecurityDeliveryPort, policy RegistrationVerificationPolicy, now func() time.Time) (*RegistrationVerificationService, error) {
	if repository == nil || normalizer == nil || delivery == nil || !hasher.Configured() {
		return nil, ErrEndUserProviderUnavailable
	}
	if policy.TTL <= 0 || policy.MaxAttempts < 1 {
		return nil, errors.New("invalid registration verification policy")
	}
	if now == nil {
		now = time.Now
	}
	return &RegistrationVerificationService{repository: repository, normalizer: normalizer, hasher: hasher, secrets: securevalue.DefaultGenerator(), delivery: delivery, policy: policy, now: now}, nil
}

func (s *RegistrationVerificationService) Start(ctx context.Context, command StartRegistrationVerificationCommand) (string, error) {
	if command.Scope.ProductID == "" || command.Scope.ApplicationID == "" || len(command.IdempotencyKey) < 16 {
		return "", ErrRegistrationVerificationInvalid
	}
	normalized, err := normalizeRegistrationIdentifier(s.normalizer, command.Identifier)
	if err != nil {
		return "", ErrRegistrationVerificationInvalid
	}
	identifierDigest := s.hasher.Digest("identifier\x00" + normalized.Value)
	continuation := s.deterministicSecret("continuation", normalized.Value, command.IdempotencyKey)
	proof := s.deterministicSecret("proof", normalized.Value, command.IdempotencyKey)
	challengeID, err := s.secrets.ID("rvc_")
	if err != nil {
		return "", err
	}
	deliveryID, err := s.secrets.ID("sdl_")
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	challenge := RegistrationVerificationChallenge{ChallengeID: challengeID, ContinuationDigest: s.hasher.Digest("registration-continuation\x00" + continuation), Scope: command.Scope, IdentifierType: normalized.Type, IdentifierDigest: identifierDigest, ProofDigest: s.hasher.Digest("registration-proof\x00" + proof), DeliveryID: deliveryID, DeliveryStatus: "pending", MaxAttempts: s.policy.MaxAttempts, CreatedAt: now, ExpiresAt: now.Add(s.policy.TTL)}
	record := EndUserIdempotency{Operation: "registration_verification_start", ScopeID: trustedScopeID(command.Scope), ActorDigest: identifierDigest, KeyDigest: s.hasher.Digest("idempotency-key\x00" + command.IdempotencyKey), RequestDigest: s.hasher.Digest("registration-verification-start\x00" + normalized.Value), ResourceID: challengeID, Now: now}
	persisted, _, err := s.repository.CreateRegistrationVerificationIdempotent(ctx, challenge, record)
	if err != nil {
		return "", err
	}
	if err := s.delivery.EnqueueSecurity(ctx, SecurityDeliveryCommand{DeliveryID: persisted.DeliveryID, Purpose: "registration_verify", Scope: persisted.Scope, Destination: normalized, Proof: proof, ExpiresAt: persisted.ExpiresAt, TraceID: command.TraceID}); err != nil {
		return "", err
	}
	if err := s.repository.ActivateRegistrationVerification(ctx, persisted.ChallengeID); err != nil {
		return "", err
	}
	return continuation, nil
}

func (s *RegistrationVerificationService) VerifyRegistration(ctx context.Context, scope EndUserSessionScope, identifier NormalizedIdentifier, continuation, proof string, consumerKeyDigest, consumerRequestDigest []byte) error {
	normalized, err := s.normalizer.Normalize(identifier.Type, identifier.Value)
	if err != nil || normalized.Value != identifier.Value || normalized.NormalizationVersion != identifier.NormalizationVersion || strings.TrimSpace(continuation) == "" || strings.TrimSpace(proof) == "" || len(consumerKeyDigest) != 32 || len(consumerRequestDigest) != 32 {
		return ErrRegistrationVerificationInvalid
	}
	err = s.repository.ConsumeRegistrationVerification(ctx, scope, normalized.Type, s.hasher.Digest("identifier\x00"+normalized.Value), s.hasher.Digest("registration-continuation\x00"+continuation), s.hasher.Digest("registration-proof\x00"+proof), consumerKeyDigest, consumerRequestDigest, s.now().UTC())
	if err != nil {
		return ErrRegistrationVerificationInvalid
	}
	return nil
}

func (s *RegistrationVerificationService) deterministicSecret(kind, identifier, key string) string {
	return "rv_" + base64.RawURLEncoding.EncodeToString(s.hasher.Digest("registration-verification\x00"+kind+"\x00"+identifier+"\x00"+key))
}

func normalizeRegistrationIdentifier(normalizer IdentifierNormalizer, raw string) (NormalizedIdentifier, error) {
	kind := IdentifierEmail
	if strings.HasPrefix(strings.TrimSpace(raw), "+") {
		kind = IdentifierPhone
	}
	return normalizer.Normalize(kind, raw)
}
