package identity

import (
	"context"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
)

var (
	ErrInvalidCredentials = errors.New("invalid administrator credentials")
	ErrRateLimited        = errors.New("administrator authentication rate limited")
	ErrSessionExpired     = errors.New("administrator session expired")
	ErrSessionRevoked     = errors.New("administrator session revoked")
	ErrRefreshReplayed    = errors.New("administrator refresh token replayed")
	ErrCSRFFailed         = errors.New("administrator csrf validation failed")
	ErrBearerNotAllowed   = errors.New("administrator bearer transport not allowed")
	ErrNotFound           = errors.New("record not found")
)

type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string { return ErrRateLimited.Error() }
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

func newRateLimitError(retryAfter time.Duration) error {
	if retryAfter < 0 {
		retryAfter = 0
	}
	return &RateLimitError{RetryAfter: retryAfter}
}

type Transport string

const (
	TransportCookie Transport = "cookie"
	TransportBearer Transport = "bearer"
)

type Credential struct {
	UserID        string
	DisplayName   string
	AccountStatus string
	PasswordHash  []byte
}

type StoredSession struct {
	SessionID              string
	UserID                 string
	DisplayName            string
	AccountStatus          string
	TokenFamilyID          string
	Transport              Transport
	AuthenticationMethod   string
	SessionVersion         int64
	AuthTime               time.Time
	AccessExpiresAt        time.Time
	RefreshExpiresAt       time.Time
	AbsoluteExpiresAt      time.Time
	CSRFDigest             []byte
	RevokedAt              *time.Time
	ControlledClientID     string
	ControlledCredentialID string
}

type TokenRecord struct {
	TokenID    string
	TokenType  string
	Generation int
	Digest     []byte
	ExpiresAt  time.Time
}

type NewSession struct {
	StoredSession
	AccessToken  TokenRecord
	RefreshToken TokenRecord
	RiskSummary  map[string]any
	OutboxEvent  OutboxEvent
	CreatedAt    time.Time
}

type Rotation struct {
	AccessToken    TokenRecord
	RefreshToken   TokenRecord
	CSRFDigest     []byte
	AccessExpires  time.Time
	RefreshExpires time.Time
	Now            time.Time
	OutboxEvent    OutboxEvent
}

type ThrottleState struct {
	FailureCount int
	BlockedUntil *time.Time
}

type LoginFailure struct {
	IdentifierDigest []byte
	SourceDigest     []byte
	Now              time.Time
	Window           time.Duration
	MaximumAttempts  int
	BlockDuration    time.Duration
	OutboxEvent      OutboxEvent
}

type OutboxEvent struct {
	EventID string
	Topic   string
	Payload SecurityEvent
	Now     time.Time
}

type ClaimedOutboxEvent struct {
	EventID      string
	Payload      SecurityEvent
	AttemptCount int
}

type BootstrapUser struct {
	UserID           string
	CredentialID     string
	IdentifierDigest []byte
	IdentifierMasked string
	DisplayName      string
	PasswordHash     []byte
	Now              time.Time
}

type ControlledClientProof struct {
	ClientID     string
	CredentialID string
	ProofType    string
	Secret       string
}

type ControlledClientBinding struct {
	ClientID     string
	CredentialID string
}

type ControlledClientCredential struct {
	ControlledClientBinding
	DisplayName string
	ClientType  string
}

type ControlledClientRegistration struct {
	ClientID     string
	DisplayName  string
	ClientType   string
	CredentialID string
	ProofType    string
	SecretDigest []byte
	CreatedAt    time.Time
	NotBefore    time.Time
	ExpiresAt    *time.Time
	OutboxEvent  OutboxEvent
}

type ControlledClientCredentialRegistration struct {
	ClientID     string
	CredentialID string
	ProofType    string
	SecretDigest []byte
	CreatedAt    time.Time
	NotBefore    time.Time
	ExpiresAt    *time.Time
	OutboxEvent  OutboxEvent
}

type Repository interface {
	FindCredential(context.Context, []byte) (Credential, error)
	LoginThrottle(context.Context, []byte, []byte, time.Time) (ThrottleState, error)
	RecordLoginFailure(context.Context, LoginFailure) (ThrottleState, error)
	ClearLoginFailures(context.Context, []byte) error
	CreateAdminSession(context.Context, NewSession) error
	FindByAccessDigest(context.Context, []byte, time.Time) (StoredSession, error)
	TouchSession(context.Context, string, time.Time) error
	RotateCSRF(context.Context, string, []byte, time.Time) error
	RotateRefresh(context.Context, []byte, Transport, *ControlledClientBinding, Rotation) (StoredSession, error)
	RevokeByToken(context.Context, []byte, time.Time, OutboxEvent) error
	BootstrapIdentity(context.Context, BootstrapUser) (string, error)
	ResolveControlledClientCredential(context.Context, string, string, string, []byte, time.Time) (ControlledClientCredential, error)
	RegisterControlledClient(context.Context, ControlledClientRegistration) error
	AddControlledClientCredential(context.Context, ControlledClientCredentialRegistration) error
	DisableControlledClient(context.Context, string, time.Time, OutboxEvent) error
	RevokeControlledClientCredential(context.Context, string, string, time.Time, OutboxEvent) error
	ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error)
	MarkOutboxPublished(context.Context, string, time.Time) error
	MarkOutboxFailed(context.Context, string, string, time.Time, bool) error
}

type AccessSnapshotResolver interface {
	ResolveAdminAccessSnapshot(context.Context, string, string) (accesscontrol.Snapshot, error)
}

type PasswordVerifier interface {
	Compare([]byte, []byte) error
	Hash([]byte) ([]byte, error)
}

type Bcrypt struct{ Cost int }

func (b Bcrypt) Compare(hash, password []byte) error {
	return bcrypt.CompareHashAndPassword(hash, password)
}

func (b Bcrypt) Hash(password []byte) ([]byte, error) {
	return bcrypt.GenerateFromPassword(password, b.Cost)
}

type Policy struct {
	AccessTTL            time.Duration
	RefreshTTL           time.Duration
	LoginWindow          time.Duration
	LoginMaximumAttempts int
	LoginBlockDuration   time.Duration
	AllowBearer          bool
}

type LoginCommand struct {
	Identifier       string
	Credential       string
	Requested        Transport
	ControlledClient *ControlledClientProof
	Source           string
	RiskSummary      map[string]any
	TraceID          string
}

type RefreshCommand struct {
	RefreshToken     string
	Transport        Transport
	ControlledClient *ControlledClientProof
	TraceID          string
}

type IssuedTokens struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

type AdminIdentitySummary struct {
	AdminUserID          string    `json:"admin_user_id"`
	DisplayName          string    `json:"display_name"`
	AccountStatus        string    `json:"account_status"`
	AuthTime             time.Time `json:"auth_time"`
	AuthenticationMethod string    `json:"authentication_method"`
}

type AdminSession struct {
	SessionID          string                 `json:"session_id"`
	Transport          Transport              `json:"transport"`
	ControlledClientID *string                `json:"controlled_client_id,omitempty"`
	Admin              AdminIdentitySummary   `json:"admin"`
	Authorization      accesscontrol.Snapshot `json:"authorization"`
	AccessExpiresAt    time.Time              `json:"access_expires_at"`
	RefreshExpiresAt   time.Time              `json:"refresh_expires_at"`
	CSRFToken          *string                `json:"csrf_token"`
	TokenPair          *IssuedTokens          `json:"token_pair,omitempty"`
	CookieTokens       *IssuedTokens          `json:"-"`
}
