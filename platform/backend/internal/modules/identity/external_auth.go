package identity

import (
	"context"
	"errors"
	"time"
)

var (
	ErrExternalProviderDisabled   = errors.New("external identity provider disabled")
	ErrExternalAuthFlowInvalid    = errors.New("external authentication flow invalid")
	ErrExternalAuthFlowExpired    = errors.New("external authentication flow expired")
	ErrExternalAuthFlowReplayed   = errors.New("external authentication flow replayed")
	ErrExternalProofInvalid       = errors.New("external identity proof invalid")
	ErrExternalProofExpired       = errors.New("external identity proof expired")
	ErrExternalProofReplayed      = errors.New("external identity proof replayed")
	ErrExternalIdentityLastLogin  = errors.New("cannot unlink last active login method")
	ErrExternalIdentityNotOwned   = errors.New("external identity is not owned by current user")
	ErrExternalRecentAuthRequired = errors.New("recent authentication required for external identity mutation")
)

type ExternalProviderQuery struct {
	Scope       EndUserSessionScope
	Environment string
	Provider    string
}

type ExternalProviderApplication struct {
	Scope                  EndUserSessionScope
	Environment            string
	Provider               string
	ProviderApplicationRef string
	Enabled                bool
}

type ExternalProviderRegistry interface {
	ResolveExternalProvider(context.Context, ExternalProviderQuery) (ExternalProviderApplication, error)
}

type AuthReturnTarget struct {
	Code          string
	URI           string
	PolicyVersion int64
}

type AuthReturnTargetResolver interface {
	ResolveAuthReturnTarget(context.Context, EndUserSessionScope, string, string) (AuthReturnTarget, error)
}

type ExternalAuthorizationRequest struct {
	FlowID        string
	Mode          string
	ReturnURI     string
	State         string
	Nonce         string
	PKCEMethod    string
	PKCEChallenge string
}

type ExternalAuthorization struct {
	AuthorizationURL string
	QRPayload        string
}

// VerifiedExternalClaims is trusted only when returned by ExternalIdentityProvider.
// OIDC signature/issuer/audience/expiry/nonce verification belongs in that adapter.
type VerifiedExternalClaims struct {
	Provider               string
	ProviderApplicationRef string
	Subject                string
	UnionSubject           string
	MaskedSubject          string
}

type ExternalIdentityProvider interface {
	StartAuthorization(context.Context, ExternalProviderApplication, ExternalAuthorizationRequest) (ExternalAuthorization, error)
	ExchangeAuthorizationCode(context.Context, ExternalProviderApplication, string, string, string) (VerifiedExternalClaims, error)
}

type ExternalAuthFlow struct {
	FlowID                    string
	Scope                     EndUserSessionScope
	Environment               string
	Provider                  string
	ProviderApplicationRef    string
	Mode                      string
	ReturnTargetCode          string
	ReturnTargetURI           string
	ReturnTargetPolicyVersion int64
	StateDigest               []byte
	NonceDigest               []byte
	PKCEChallengeDigest       []byte
	BrowserSessionDigest      []byte
	AuthorizationCodeDigest   []byte
	ProcessingTokenDigest     []byte
	ProcessingExpiresAt       *time.Time
	Status                    string
	CreatedAt                 time.Time
	ExpiresAt                 time.Time
	ConsumedAt                *time.Time
	FailureCode               *string
}

type ExternalIdentityProof struct {
	ProofID                string
	FlowID                 string
	Scope                  EndUserSessionScope
	Provider               string
	ProviderApplicationRef string
	SubjectDigest          []byte
	SubjectMasked          string
	UnionSubjectDigest     []byte
	ProofDigest            []byte
	CreatedAt              time.Time
	ExpiresAt              time.Time
	ConsumedAt             *time.Time
}

type ExternalAuthStartCommand struct {
	Scope            EndUserSessionScope
	Environment      string
	Provider         string
	Mode             string
	ReturnTargetCode string
	BrowserSession   string
	TraceID          string
}

type ExternalAuthStartResult struct {
	FlowID           string
	Mode             string
	AuthorizationURL string
	QRPayload        string
	ExpiresAt        time.Time
}

type ExternalAuthCallbackCommand struct {
	FlowID         string
	Provider       string
	ExpectedScope  *EndUserSessionScope
	BrowserSession string
	State          string
	Code           string
	ProviderError  string
	TraceID        string
}

type ExternalAuthResult struct {
	Status          string
	Session         *EndUserIssuedSession
	ExternalProofID string
}

type LinkExternalIdentityCommand struct {
	Session         EndUserSession
	Provider        string
	ExternalProofID string
	IdempotencyKey  string
	TraceID         string
}

type UnlinkExternalIdentityCommand struct {
	Session            EndUserSession
	ExternalIdentityID string
	TraceID            string
}

type ExternalAuthRepository interface {
	CreateExternalAuthFlow(context.Context, ExternalAuthFlow) error
	FindExternalAuthFlow(context.Context, string) (ExternalAuthFlow, error)
	ClaimExternalAuthFlow(context.Context, ExternalAuthFlowClaim) (ExternalAuthFlow, error)
	ConsumeExternalAuthFlowWithProof(context.Context, string, []byte, []byte, ExternalIdentityProof, time.Time, OutboxEvent) error
	ConsumeExternalAuthFlowWithSession(context.Context, string, []byte, []byte, NewEndUserSession, time.Time) error
	ConsumeExternalAuthFlowFailure(context.Context, string, []byte, string, time.Time, OutboxEvent) error
	ConsumeExternalIdentityProofAndLink(context.Context, []byte, EndUserSessionScope, string, ExternalIdentity, time.Time, EndUserIdempotency) (ExternalIdentity, bool, error)
	ListExternalIdentities(context.Context, string) ([]ExternalIdentity, error)
	UnlinkExternalIdentity(context.Context, string, string, time.Time, OutboxEvent) error
}

type ExternalAuthFlowClaim struct {
	FlowID                string
	Provider              string
	ExpectedScope         *EndUserSessionScope
	BrowserSessionDigest  []byte
	StateDigest           []byte
	ProcessingTokenDigest []byte
	ProcessingExpiresAt   time.Time
	Now                   time.Time
}
