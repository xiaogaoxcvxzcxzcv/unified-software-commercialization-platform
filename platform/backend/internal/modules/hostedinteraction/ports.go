package hostedinteraction

import (
	"context"
	"time"
)

type IDGenerator interface {
	ID(string) (string, error)
	Token(string) (string, error)
}

type Digester interface {
	Digest(string) []byte
}

type StateContext struct {
	InteractionID string
	Route         Route
	Scope         Scope
	TraceID       string
}

type StateProtector interface {
	Protect(context.Context, StateContext, string) (ProtectedState, error)
	Reveal(context.Context, StateContext, ProtectedState) (string, error)
}

type ProtectedState struct {
	KeyRef     string
	Ciphertext []byte
	Digest     []byte
}

type ReturnTargetPort interface {
	ResolveHostedReturnTarget(context.Context, Scope, string) (ReturnTarget, error)
}

type HostedIdentityPort interface {
	AuthenticateHosted(context.Context, Scope, string, string, string, map[string]any, string) (HostedAuthProof, error)
	RedeemHostedAuthGrant(context.Context, string, string, Scope, string) (IssuedUserSession, error)
}

type SessionValidationPort interface {
	ValidateHostedAccountSession(context.Context, Scope, Actor) error
}

type CreateRecord struct {
	Interaction   Interaction
	Operation     string
	ActorDigest   []byte
	KeyDigest     []byte
	RequestDigest []byte
	Response      []byte
	Event         OutboxEvent
}

type OpenBrowserRecord struct {
	InteractionID string
	SessionID     string
	TokenDigest   []byte
	TTL           time.Duration
	Event         OutboxEvent
}

type CompleteRecord struct {
	InteractionID             string
	BrowserTokenDigest        []byte
	AuthenticationLeaseDigest []byte
	ExpectedStatus            []Status
	GrantID                   string
	GrantType                 string
	CodeDigest                []byte
	IdentityProofID           string
	ResultDocument            []byte
	GrantTTL                  time.Duration
	Operation                 string
	ActorDigest               []byte
	KeyDigest                 []byte
	RequestDigest             []byte
	IdempotencyResponse       []byte
	Event                     OutboxEvent
}

type CancelRecord struct {
	InteractionID      string
	BrowserTokenDigest []byte
	ActorDigest        []byte
	KeyDigest          []byte
	RequestDigest      []byte
	Event              OutboxEvent
}

type Repository interface {
	Create(context.Context, CreateRecord) (Interaction, bool, error)
	Get(context.Context, string) (Interaction, error)
	GetForScope(context.Context, string, Scope, Actor) (Interaction, error)
	OpenBrowserSession(context.Context, OpenBrowserRecord) (Interaction, time.Time, error)
	ValidateBrowserSession(context.Context, string, []byte) (BrowserAccess, error)
	BeginAuthentication(context.Context, string, []byte, []byte, time.Duration) (Interaction, time.Time, error)
	ResetAuthentication(context.Context, string, []byte, []byte) error
	GetCompletionGrant(context.Context, string, []byte) (CompletionGrant, error)
	Complete(context.Context, CompleteRecord) (Interaction, CompletionGrant, bool, error)
	Cancel(context.Context, CancelRecord) (Interaction, error)
	ClaimGrant(context.Context, string, Scope, []byte, []byte, time.Duration, string, []byte) (ClaimedGrant, error)
	ConsumeGrant(context.Context, string, []byte, OutboxEvent) (Interaction, error)
	ExpireDue(context.Context, int) (int, error)
}
