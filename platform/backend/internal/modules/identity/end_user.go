package identity

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidEndUserIdentifier        = errors.New("invalid end-user identifier")
	ErrEndUserIdentifierConflict       = errors.New("end-user identifier already exists")
	ErrEndUserVersionConflict          = errors.New("end-user version conflict")
	ErrEndUserSessionExpired           = errors.New("end-user session expired")
	ErrEndUserSessionRevoked           = errors.New("end-user session revoked")
	ErrEndUserRefreshReplayed          = errors.New("end-user refresh token replayed")
	ErrRecoveryChallengeExpired        = errors.New("recovery challenge expired")
	ErrRecoveryProofInvalid            = errors.New("recovery proof invalid")
	ErrRecoveryProofReplayed           = errors.New("recovery proof replayed")
	ErrExternalIdentityConflict        = errors.New("external identity already linked")
	ErrEndUserScopeMismatch            = errors.New("end-user session scope mismatch")
	ErrEndUserAccountDisabled          = errors.New("end-user account disabled")
	ErrEndUserReauthenticationRequired = errors.New("end-user recent authentication required")
)

type IdentifierType string

const (
	IdentifierEmail IdentifierType = "email"
	IdentifierPhone IdentifierType = "phone"
)

type NormalizedIdentifier struct {
	Type                 IdentifierType
	Value                string
	NormalizationVersion int
}

type IdentifierNormalizer interface {
	Normalize(IdentifierType, string) (NormalizedIdentifier, error)
}

// StrictIdentifierNormalizer deliberately refuses regional phone inference.
// Phone callers must supply canonical E.164 and email comparison is case-folded.
type StrictIdentifierNormalizer struct{}

var e164Pattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

func (StrictIdentifierNormalizer) Normalize(kind IdentifierType, raw string) (NormalizedIdentifier, error) {
	value := strings.TrimSpace(raw)
	switch kind {
	case IdentifierEmail:
		value = strings.ToLower(value)
		if len(value) > 320 || strings.Count(value, "@") != 1 {
			return NormalizedIdentifier{}, ErrInvalidEndUserIdentifier
		}
		parts := strings.SplitN(value, "@", 2)
		if parts[0] == "" || parts[1] == "" || strings.ContainsAny(value, "\r\n\t ") {
			return NormalizedIdentifier{}, ErrInvalidEndUserIdentifier
		}
	case IdentifierPhone:
		if !e164Pattern.MatchString(value) {
			return NormalizedIdentifier{}, ErrInvalidEndUserIdentifier
		}
	default:
		return NormalizedIdentifier{}, ErrInvalidEndUserIdentifier
	}
	return NormalizedIdentifier{Type: kind, Value: value, NormalizationVersion: 1}, nil
}

type EndUser struct {
	UserID        string
	AccountStatus string
	Version       int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type EndUserIdentifier struct {
	IdentifierID         string
	UserID               string
	Type                 IdentifierType
	NormalizationVersion int
	NormalizedDigest     []byte
	MaskedValue          string
	VerificationStatus   string
	VerifiedAt           *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type EndUserCredential struct {
	CredentialID string
	UserID       string
	PasswordHash []byte
	Algorithm    string
	Status       string
	Version      int64
	ChangedAt    time.Time
}

type EndUserProfile struct {
	UserID      string
	Version     int64
	DisplayName string
	AvatarRef   *string
	Locale      *string
	Timezone    *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type EndUserRegistration struct {
	User        EndUser
	Identifier  EndUserIdentifier
	Credential  EndUserCredential
	Profile     EndUserProfile
	OutboxEvent OutboxEvent
}

type EndUserPasswordCredential struct {
	UserID        string
	AccountStatus string
	UserVersion   int64
	CredentialID  string
	PasswordHash  []byte
	Algorithm     string
	Version       int64
}

type EndUserSession struct {
	SessionID            string
	UserID               string
	ProductID            string
	ApplicationID        string
	TenantID             *string
	TokenFamilyID        string
	AuthenticationMethod string
	ExternalIdentityID   *string
	Version              int64
	AuthTime             time.Time
	CreatedAt            time.Time
	LastSeenAt           time.Time
	AccessExpiresAt      time.Time
	RefreshExpiresAt     time.Time
	AbsoluteExpiresAt    time.Time
	RiskSummaryDigest    []byte
	RevokedAt            *time.Time
	RevokeReason         *string
	AccountStatus        string
}

type EndUserSessionScope struct {
	ProductID     string
	ApplicationID string
	TenantID      *string
}

func (s EndUserSessionScope) Matches(session EndUserSession) bool {
	return s.ProductID != "" && s.ApplicationID != "" && s.ProductID == session.ProductID && s.ApplicationID == session.ApplicationID && nullableStringEqual(s.TenantID, session.TenantID)
}

func nullableStringEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

type EndUserSessionToken struct {
	TokenID    string
	TokenType  string
	Generation int
	Digest     []byte
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

type NewEndUserSession struct {
	Session      EndUserSession
	AccessToken  EndUserSessionToken
	RefreshToken EndUserSessionToken
	OutboxEvent  OutboxEvent
}

type EndUserRefreshRotation struct {
	AccessToken       EndUserSessionToken
	RefreshToken      EndUserSessionToken
	AccessExpiresAt   time.Time
	RefreshExpiresAt  time.Time
	Now               time.Time
	OutboxEvent       OutboxEvent
	RequestDigest     []byte
	RecoveryExpiresAt time.Time
}

type RecoveryChallenge struct {
	ChallengeID          string
	ContinuationDigest   []byte
	IdentifierType       IdentifierType
	IdentifierDigest     []byte
	MatchedUserID        *string
	DeliveryTargetMasked string
	ProofDigest          []byte
	DeliveryStatus       string
	MaxAttempts          int
	CreatedAt            time.Time
	ExpiresAt            time.Time
	OutboxEvent          OutboxEvent
}

type RecoveryConsumption struct {
	ChallengeID   string
	MatchedUserID *string
	ConsumedAt    time.Time
}

type EndUserRecoveryTarget struct {
	UserID      *string
	MaskedValue string
}

type EndUserLoginThrottle struct {
	FailureCount int
	BlockedUntil *time.Time
}

type EndUserLoginFailure struct {
	ScopeID          string
	IdentifierDigest []byte
	SourceDigest     []byte
	Now              time.Time
	Window           time.Duration
	MaximumAttempts  int
	BlockDuration    time.Duration
	OutboxEvent      OutboxEvent
}

type EndUserSessionSummary struct {
	SessionID            string
	ProductID            string
	ApplicationID        string
	TenantID             *string
	Current              bool
	AuthenticationMethod string
	CreatedAt            time.Time
	LastSeenAt           time.Time
	ExpiresAt            time.Time
	RevokedAt            *time.Time
}

type ExternalIdentity struct {
	ExternalIdentityID    string
	UserID                string
	Provider              string
	ProviderApplicationID string
	SubjectDigest         []byte
	SubjectMasked         string
	UnionSubjectDigest    []byte
	Status                string
	Version               int64
	LinkedAt              time.Time
	UpdatedAt             time.Time
	AccountStatus         string
	OutboxEvent           OutboxEvent
}

type ScopedSessionRevocation struct {
	ProductID     string
	UserID        string
	TenantID      *string
	Cutoff        time.Time
	AccessVersion int64
	EventIDDigest []byte
	RequestDigest []byte
	ActorDigest   []byte
	OutboxEvent   OutboxEvent
}

type EndUserIdempotency struct {
	Operation     string
	ScopeID       string
	ActorDigest   []byte
	KeyDigest     []byte
	RequestDigest []byte
	ResourceID    string
	Now           time.Time
}

type EndUserRegistrationResponse struct {
	Session RegistrationSessionSnapshot `json:"session"`
	Profile EndUserProfile              `json:"profile"`
}

type RegistrationSessionSnapshot struct {
	SessionID            string    `json:"session_id"`
	UserID               string    `json:"user_id"`
	ProductID            string    `json:"product_id"`
	ApplicationID        string    `json:"application_id"`
	TenantID             *string   `json:"tenant_id"`
	AuthenticationMethod string    `json:"authentication_method"`
	Version              int64     `json:"version"`
	AuthTime             time.Time `json:"auth_time"`
	CreatedAt            time.Time `json:"created_at"`
	LastSeenAt           time.Time `json:"last_seen_at"`
	AccessExpiresAt      time.Time `json:"access_expires_at"`
	RefreshExpiresAt     time.Time `json:"refresh_expires_at"`
	AbsoluteExpiresAt    time.Time `json:"absolute_expires_at"`
	AccountStatus        string    `json:"account_status"`
}

func NewRegistrationSessionSnapshot(session EndUserSession) RegistrationSessionSnapshot {
	return RegistrationSessionSnapshot{
		SessionID: session.SessionID, UserID: session.UserID, ProductID: session.ProductID,
		ApplicationID: session.ApplicationID, TenantID: session.TenantID,
		AuthenticationMethod: session.AuthenticationMethod, Version: session.Version,
		AuthTime: session.AuthTime, CreatedAt: session.CreatedAt, LastSeenAt: session.LastSeenAt,
		AccessExpiresAt: session.AccessExpiresAt, RefreshExpiresAt: session.RefreshExpiresAt,
		AbsoluteExpiresAt: session.AbsoluteExpiresAt, AccountStatus: session.AccountStatus,
	}
}

func (s RegistrationSessionSnapshot) EndUserSession() EndUserSession {
	return EndUserSession{
		SessionID: s.SessionID, UserID: s.UserID, ProductID: s.ProductID,
		ApplicationID: s.ApplicationID, TenantID: s.TenantID,
		AuthenticationMethod: s.AuthenticationMethod, Version: s.Version,
		AuthTime: s.AuthTime, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt,
		AccessExpiresAt: s.AccessExpiresAt, RefreshExpiresAt: s.RefreshExpiresAt,
		AbsoluteExpiresAt: s.AbsoluteExpiresAt, AccountStatus: s.AccountStatus,
	}
}

type EndUserProfilePatchValue struct {
	Set   bool
	Value *string
}

type EndUserProfilePatch struct {
	DisplayName EndUserProfilePatchValue
	AvatarRef   EndUserProfilePatchValue
	Locale      EndUserProfilePatchValue
	Timezone    EndUserProfilePatchValue
}

func (r EndUserRegistration) Validate() error {
	if r.User.UserID == "" || r.Identifier.IdentifierID == "" || r.Credential.CredentialID == "" || r.Profile.UserID != r.User.UserID || r.Identifier.UserID != r.User.UserID || r.Credential.UserID != r.User.UserID {
		return fmt.Errorf("invalid end-user registration ownership")
	}
	if len(r.Identifier.NormalizedDigest) == 0 {
		return fmt.Errorf("end-user registration requires digests and password hash")
	}
	if err := ValidateAdaptivePasswordHash(r.Credential.Algorithm, r.Credential.PasswordHash); err != nil {
		return err
	}
	return nil
}

// ValidateAdaptivePasswordHash verifies the encoded hash rather than trusting
// an algorithm label. Argon2id remains rejected until a PHC parser/verifier is
// part of the Identity password capability.
func ValidateAdaptivePasswordHash(algorithm string, encoded []byte) error {
	if algorithm != "bcrypt" {
		return fmt.Errorf("unsupported or unverifiable password algorithm")
	}
	cost, err := bcrypt.Cost(encoded)
	if err != nil {
		return fmt.Errorf("invalid bcrypt password hash: %w", err)
	}
	if cost < bcrypt.DefaultCost {
		return fmt.Errorf("bcrypt password hash cost %d is below required %d", cost, bcrypt.DefaultCost)
	}
	return nil
}
