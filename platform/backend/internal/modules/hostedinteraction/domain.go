package hostedinteraction

import (
	"crypto/hmac"
	"errors"
	"time"
)

var (
	ErrInvalidArgument        = errors.New("hosted.invalid_interaction")
	ErrInteractionExpired     = errors.New("hosted.interaction_expired")
	ErrInteractionTerminal    = errors.New("hosted.interaction_terminal")
	ErrInvalidReturnTarget    = errors.New("hosted.invalid_return_target")
	ErrStateMismatch          = errors.New("hosted.state_mismatch")
	ErrPKCERequired           = errors.New("hosted.pkce_required")
	ErrInvalidGrant           = errors.New("hosted.invalid_grant")
	ErrAuthenticationNeeded   = errors.New("hosted.authentication_required")
	ErrChannelNotSupported    = errors.New("hosted.channel_not_supported")
	ErrSessionRevoked         = errors.New("hosted.session_revoked")
	ErrCSRF                   = errors.New("hosted.csrf_failed")
	ErrTemporarilyUnavailable = errors.New("hosted.temporarily_unavailable")
	ErrIdempotencyConflict    = errors.New("hosted.idempotency_conflict")
	ErrVersionConflict        = errors.New("hosted.version_conflict")
	ErrCapabilityUnavailable  = errors.New("hosted.capability_not_available")
	ErrLeaseLost              = errors.New("hosted.grant_lease_lost")
)

type Route string

const (
	RouteAuth    Route = "hosted.auth"
	RouteAccount Route = "hosted.account"
)

type Channel string

const (
	ChannelWeb     Channel = "web"
	ChannelH5      Channel = "h5"
	ChannelDesktop Channel = "desktop"
	ChannelApp     Channel = "app"
)

type Status string

const (
	StatusCreated        Status = "created"
	StatusOpened         Status = "opened"
	StatusAuthenticating Status = "authenticating"
	StatusCompleted      Status = "completed"
	StatusExchanged      Status = "exchanged"
	StatusCancelled      Status = "cancelled"
	StatusFailed         Status = "failed"
	StatusExpired        Status = "expired"
)

type Scope struct {
	ProductID     string
	ApplicationID string
	TenantID      *string
	Environment   string
	Channel       Channel
}

func (s Scope) Matches(other Scope) bool {
	return s.ProductID == other.ProductID && s.ApplicationID == other.ApplicationID &&
		equalOptional(s.TenantID, other.TenantID) && s.Environment == other.Environment && s.Channel == other.Channel
}

type Actor struct {
	Kind            string
	ClientSessionID string
	UserID          string
	UserSessionID   string
}

type ReturnTarget struct {
	ProductID     string
	ApplicationID string
	Code          string
	URI           string
	PolicyVersion int64
	Kind          string
}

type Interaction struct {
	InteractionID                string
	Route                        Route
	Scope                        Scope
	Actor                        Actor
	ReturnTargetCode             string
	ReturnTargetURI              string
	ReturnTargetPolicyVersion    int64
	StateProtectorKeyRef         string
	StateCiphertext              []byte
	StateDigest                  []byte
	NonceDigest                  []byte
	PKCEChallengeDigest          []byte
	PKCEMethod                   string
	Locale                       string
	ThemeVariant                 string
	Status                       Status
	Version                      int64
	ResultKind                   string
	FailureCode                  string
	TraceID                      string
	CreatedAt                    time.Time
	ExpiresAt                    time.Time
	OpenedAt                     *time.Time
	CompletedAt                  *time.Time
	TerminalAt                   *time.Time
	AuthenticationLeaseDigest    []byte
	AuthenticationStartedAt      *time.Time
	AuthenticationLeaseExpiresAt *time.Time
}

type Projection struct {
	InteractionID  string
	Route          Route
	Channel        Channel
	Status         Status
	AllowedActions []string
	ResultKind     string
	FailureCode    string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	OpenedAt       *time.Time
	CompletedAt    *time.Time
}

func Project(value Interaction) Projection {
	return Projection{
		InteractionID: value.InteractionID, Route: value.Route, Channel: value.Scope.Channel,
		Status: value.Status, AllowedActions: allowedActions(value.Status), ResultKind: value.ResultKind,
		FailureCode: value.FailureCode, CreatedAt: value.CreatedAt, ExpiresAt: value.ExpiresAt,
		OpenedAt: value.OpenedAt, CompletedAt: value.CompletedAt,
	}
}

type BrowserSession struct {
	BrowserSessionID string
	InteractionID    string
	Token            string
	CSRFToken        string
	ExpiresAt        time.Time
}

type BrowserAccess struct {
	Interaction      Interaction
	BrowserSessionID string
}

type Completion struct {
	Interaction    Projection
	ReturnURL      string
	Code           string
	GrantExpiresAt time.Time
}

type ExchangeResult struct {
	Interaction Projection
	ResultKind  string
	UserSession *IssuedUserSession
	Document    map[string]any
}

type HostedAuthProof struct {
	ProofID         string
	SafeUserSummary map[string]string
	AuthTime        time.Time
	ExpiresAt       time.Time
}

type IssuedUserSession struct {
	SessionID        string
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	User             SafeUserSummary
}

type SafeUserSummary struct {
	UserID              string
	AccountStatus       string
	DisplayName         string
	ProductID           *string
	TenantID            *string
	AccessVersion       *int64
	ProductAccessStatus *string
	TenantAccessStatus  *string
}

type ClaimedGrant struct {
	GrantID         string
	InteractionID   string
	GrantType       string
	IdentityProofID string
	ResultDocument  map[string]any
	LeaseToken      string
	LeaseExpiresAt  time.Time
	ExpiresAt       time.Time
	Scope           Scope
	TraceID         string
}

type CompletionGrant struct {
	GrantID         string
	InteractionID   string
	GrantType       string
	IdentityProofID string
	ResultDocument  map[string]any
	ExpiresAt       time.Time
}

type OutboxEvent struct {
	EventID       string
	InteractionID string
	EventType     string
	Payload       []byte
	OccurredAt    time.Time
}

func allowedActions(status Status) []string {
	switch status {
	case StatusCreated:
		return []string{"open", "cancel"}
	case StatusOpened, StatusAuthenticating:
		return []string{"authenticate", "complete", "cancel"}
	case StatusCompleted:
		return []string{"exchange"}
	case StatusFailed, StatusExpired:
		return []string{"restart"}
	default:
		return []string{}
	}
}

func equalOptional(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func EqualSecret(left, right []byte) bool {
	return len(left) > 0 && len(right) > 0 && hmac.Equal(left, right)
}
