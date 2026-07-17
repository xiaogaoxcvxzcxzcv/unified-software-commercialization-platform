package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

var (
	ErrHostedAuthUnavailable   = errors.New("hosted authentication unavailable")
	ErrHostedAuthProofInvalid  = errors.New("hosted authentication proof invalid")
	ErrHostedAuthProofExpired  = errors.New("hosted authentication proof expired")
	ErrHostedAuthProofReplayed = errors.New("hosted authentication proof replayed")
	ErrHostedAuthGrantConflict = errors.New("hosted authentication grant conflict")
)

type HostedAuthProof struct {
	ProofID              string
	UserID               string
	Scope                EndUserSessionScope
	AuthenticationMethod string
	RiskSummaryDigest    []byte
	TTL                  time.Duration
	CreatedAt            time.Time
	ExpiresAt            time.Time
	OutboxEvent          OutboxEvent
}

type HostedSafeUserSummary struct {
	DisplayName string
	AvatarRef   *string
}

type HostedAuthProofResult struct {
	ProofID   string
	User      HostedSafeUserSummary
	AuthTime  time.Time
	ExpiresAt time.Time
}

type AuthenticateHostedCommand struct {
	Scope       EndUserSessionScope
	Identifier  string
	Credential  string
	Source      string
	RiskSummary map[string]any
	TraceID     string
}

type RedeemHostedAuthGrantCommand struct {
	GrantID string
	ProofID string
	Scope   EndUserSessionScope
	TraceID string
}

type HostedSessionExpectation struct {
	Scope     EndUserSessionScope
	UserID    string
	SessionID string
}

type HostedAuthGrantRedemption struct {
	GrantID       string
	ProofID       string
	Scope         EndUserSessionScope
	RequestDigest []byte
	Session       NewEndUserSession
	OutboxEvent   OutboxEvent
	AccessTTL     time.Duration
	RefreshTTL    time.Duration
	AbsoluteTTL   time.Duration
}

type HostedAuthRepository interface {
	CreateHostedAuthProofAndClearFailures(context.Context, HostedAuthProof, string, []byte) (HostedAuthProof, error)
	RedeemHostedAuthGrant(context.Context, HostedAuthGrantRedemption) (EndUserSession, bool, error)
	ValidateHostedSession(context.Context, HostedSessionExpectation) error
}

var (
	hostedProofIDPattern = regexp.MustCompile(`^hproof_[A-Za-z0-9_-]{24,160}$`)
	hostedGrantIDPattern = regexp.MustCompile(`^hgrant_[A-Za-z0-9_-]{24,160}$`)
)

func (s *EndUserService) AuthenticateHosted(ctx context.Context, command AuthenticateHostedCommand) (HostedAuthProofResult, error) {
	if s == nil || s.hosted == nil || !s.hasher.Configured() {
		return HostedAuthProofResult{}, ErrHostedAuthUnavailable
	}
	if command.Scope.ProductID == "" || command.Scope.ApplicationID == "" || !ValidEndUserEnvironment(command.Scope.Environment) || command.TraceID == "" {
		return HostedAuthProofResult{}, ErrHostedAuthProofInvalid
	}
	authenticated, err := s.authenticatePassword(ctx, command.Scope, command.Identifier, command.Credential, command.Source, command.TraceID)
	if err != nil {
		return HostedAuthProofResult{}, err
	}
	profile, err := s.repository.GetEndUserProfile(ctx, authenticated.Credential.UserID)
	if err != nil {
		return HostedAuthProofResult{}, err
	}
	risk, err := json.Marshal(command.RiskSummary)
	if err != nil {
		return HostedAuthProofResult{}, err
	}
	proofID, err := s.secrets.ID("hproof_")
	if err != nil {
		return HostedAuthProofResult{}, err
	}
	ttl := s.policy.HostedAuthProofTTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	event, err := s.event("identity.hosted_auth_succeeded.v1", "identity.hosted_auth_succeeded", authenticated.Credential.UserID, command.TraceID, "success", "", authenticated.Now)
	if err != nil {
		return HostedAuthProofResult{}, err
	}
	proof := HostedAuthProof{ProofID: proofID, UserID: authenticated.Credential.UserID, Scope: command.Scope, AuthenticationMethod: "password", RiskSummaryDigest: s.hasher.Digest("hosted-auth-risk\x00" + string(risk)), TTL: ttl, OutboxEvent: event}
	persisted, err := s.hosted.CreateHostedAuthProofAndClearFailures(ctx, proof, authenticated.ScopeID, authenticated.IdentifierDigest)
	if err != nil {
		return HostedAuthProofResult{}, err
	}
	return HostedAuthProofResult{ProofID: persisted.ProofID, User: HostedSafeUserSummary{DisplayName: profile.DisplayName, AvatarRef: profile.AvatarRef}, AuthTime: persisted.CreatedAt, ExpiresAt: persisted.ExpiresAt}, nil
}

func (s *EndUserService) RedeemHostedAuthGrant(ctx context.Context, command RedeemHostedAuthGrantCommand) (EndUserIssuedSession, error) {
	if s == nil || s.hosted == nil || !s.hasher.Configured() {
		return EndUserIssuedSession{}, ErrHostedAuthUnavailable
	}
	if !hostedGrantIDPattern.MatchString(command.GrantID) || !hostedProofIDPattern.MatchString(command.ProofID) || command.Scope.ProductID == "" || command.Scope.ApplicationID == "" || !ValidEndUserEnvironment(command.Scope.Environment) || command.TraceID == "" {
		return EndUserIssuedSession{}, ErrHostedAuthProofInvalid
	}
	now := s.now().UTC()
	issued, stored := s.newDeterministicHostedSession(command, now)
	event := s.deterministicHostedEvent(command, now)
	redemption := HostedAuthGrantRedemption{GrantID: command.GrantID, ProofID: command.ProofID, Scope: command.Scope, RequestDigest: s.requestDigest("hosted-grant-redemption", command.GrantID, command.ProofID, trustedScopeID(command.Scope)), Session: stored, OutboxEvent: event, AccessTTL: s.policy.AccessTTL, RefreshTTL: s.policy.RefreshTTL, AbsoluteTTL: s.policy.RefreshAbsoluteTTL}
	session, _, err := s.hosted.RedeemHostedAuthGrant(ctx, redemption)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	profile, err := s.repository.GetEndUserProfile(ctx, session.UserID)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	issued.Session, issued.Profile = session, profile
	return issued, nil
}

func (s *EndUserService) ValidateHostedSession(ctx context.Context, expected HostedSessionExpectation) error {
	if s == nil || s.hosted == nil {
		return ErrHostedAuthUnavailable
	}
	if expected.UserID == "" || expected.SessionID == "" || expected.Scope.ProductID == "" || expected.Scope.ApplicationID == "" || !ValidEndUserEnvironment(expected.Scope.Environment) {
		return ErrEndUserScopeMismatch
	}
	return s.hosted.ValidateHostedSession(ctx, expected)
}

func (s *EndUserService) newDeterministicHostedSession(command RedeemHostedAuthGrantCommand, now time.Time) (EndUserIssuedSession, NewEndUserSession) {
	scopeID := trustedScopeID(command.Scope)
	seed := command.GrantID + "\x00" + command.ProofID + "\x00" + scopeID
	identifier := func(kind, prefix string) string {
		digest := s.hasher.Digest("hosted-auth-" + kind + "\x00" + seed)
		return prefix + base64.RawURLEncoding.EncodeToString(digest[:16])
	}
	token := func(kind, prefix string) string {
		return prefix + base64.RawURLEncoding.EncodeToString(s.hasher.Digest("hosted-auth-token-"+kind+"\x00"+seed))
	}
	access, refresh := token("access", "ua_"), token("refresh", "ur_")
	accessExpires, refreshExpires := now.Add(s.policy.AccessTTL), now.Add(s.policy.RefreshTTL)
	session := EndUserSession{SessionID: identifier("session", "uses_"), ProductID: command.Scope.ProductID, ApplicationID: command.Scope.ApplicationID, TenantID: command.Scope.TenantID, Environment: command.Scope.Environment, TokenFamilyID: identifier("family", "ufam_"), AuthenticationMethod: "password", Version: 1, CreatedAt: now, LastSeenAt: now, AccessExpiresAt: accessExpires, RefreshExpiresAt: refreshExpires, AbsoluteExpiresAt: now.Add(s.policy.RefreshAbsoluteTTL), AccountStatus: "active"}
	stored := NewEndUserSession{Session: session, AccessToken: EndUserSessionToken{TokenID: identifier("access-id", "uat_"), TokenType: "access", Generation: 1, Digest: s.hasher.Digest("access\x00" + access), CreatedAt: now, ExpiresAt: accessExpires}, RefreshToken: EndUserSessionToken{TokenID: identifier("refresh-id", "urt_"), TokenType: "refresh", Generation: 1, Digest: s.hasher.Digest("refresh\x00" + refresh), CreatedAt: now, ExpiresAt: refreshExpires}}
	return EndUserIssuedSession{Session: session, AccessToken: access, RefreshToken: refresh}, stored
}

func (s *EndUserService) deterministicHostedEvent(command RedeemHostedAuthGrantCommand, now time.Time) OutboxEvent {
	digest := s.hasher.Digest("hosted-auth-event\x00" + command.GrantID)
	audit := s.hasher.Digest("hosted-auth-audit\x00" + command.GrantID)
	return OutboxEvent{EventID: "evt_" + base64.RawURLEncoding.EncodeToString(digest[:16]), Topic: "identity.hosted_auth_grant_redeemed.v1", Now: now, Payload: SecurityEvent{AuditID: "aud_" + base64.RawURLEncoding.EncodeToString(audit[:16]), OccurredAt: now, ActorID: "system", Action: "identity.hosted_auth_grant_redeemed", TargetType: "end_user_session", TargetID: s.hasher.DigestHex(command.GrantID), Result: "success", TraceID: command.TraceID, RiskLevel: "normal"}}
}
