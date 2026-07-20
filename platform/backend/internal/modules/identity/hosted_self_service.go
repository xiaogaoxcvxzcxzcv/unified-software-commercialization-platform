package identity

import (
	"context"
	"errors"
	"strings"
	"time"
)

// HostedSelfServiceCapabilities exposes configuration state only. It never
// exposes provider credentials or user-session bearer tokens.
type HostedSelfServiceCapabilities struct {
	PasswordEnabled     bool
	RegistrationEnabled bool
	RecoveryEnabled     bool
}

type HostedRegistrationRecord struct {
	Registration     EndUserRegistration
	Proof            HostedAuthProof
	Idempotency      EndUserIdempotency
	IdentifierDigest []byte
}
type HostedRegistrationResponse struct {
	Proof   HostedAuthProof `json:"proof"`
	Profile EndUserProfile  `json:"profile"`
}

type HostedSelfServiceRepository interface {
	FindHostedSession(context.Context, HostedSessionExpectation) (EndUserSession, error)
	RecoverHostedRegistration(context.Context, EndUserIdempotency) (HostedRegistrationResponse, bool, error)
	CreateHostedRegistration(context.Context, HostedRegistrationRecord) (HostedRegistrationResponse, bool, error)
	ListExternalIdentities(context.Context, string) ([]ExternalIdentity, error)
	RevokeHostedSessionIdempotent(context.Context, HostedSessionExpectation, string, time.Time, OutboxEvent, EndUserIdempotency) error
}

func (s *EndUserService) HostedSelfServiceCapabilities() HostedSelfServiceCapabilities {
	return HostedSelfServiceCapabilities{
		PasswordEnabled:     s != nil && s.passwords != nil,
		RegistrationEnabled: s != nil && s.proofs != nil,
		RecoveryEnabled:     s != nil && s.recovery != nil,
	}
}

func (s *EndUserService) RegisterHosted(ctx context.Context, command EndUserRegisterCommand) (HostedAuthProofResult, error) {
	if s == nil || s.hosted == nil || !s.hasher.Configured() || !ValidEndUserEnvironment(command.Scope.Environment) {
		return HostedAuthProofResult{}, ErrHostedAuthUnavailable
	}
	if s.proofs == nil {
		return HostedAuthProofResult{}, ErrEndUserProviderUnavailable
	}
	if len(command.IdempotencyKey) < 16 {
		return HostedAuthProofResult{}, errors.New("idempotency key is required")
	}
	normalized, err := s.normalize(command.Identifier)
	if err != nil {
		return HostedAuthProofResult{}, err
	}
	profileName := strings.TrimSpace(command.DisplayName)
	if profileName == "" {
		profileName = "User"
	}
	identifierDigest := s.hasher.Digest("identifier\x00" + normalized.Value)
	idempotency := EndUserIdempotency{Operation: "hosted_register", ScopeID: trustedScopeID(command.Scope), ActorDigest: identifierDigest, KeyDigest: s.hasher.Digest("idempotency-key\x00" + command.IdempotencyKey), RequestDigest: s.requestDigest("hosted-register-request", normalized.Value, command.Credential, command.VerificationContinuationID, command.VerificationProof, profileName)}
	repository, ok := s.repository.(HostedSelfServiceRepository)
	if !ok {
		return HostedAuthProofResult{}, ErrHostedAuthUnavailable
	}
	if recovered, found, recoverErr := repository.RecoverHostedRegistration(ctx, idempotency); recoverErr != nil {
		return HostedAuthProofResult{}, recoverErr
	} else if found {
		return HostedAuthProofResult{ProofID: recovered.Proof.ProofID, User: HostedSafeUserSummary{DisplayName: recovered.Profile.DisplayName, AvatarRef: recovered.Profile.AvatarRef}, AuthTime: recovered.Proof.CreatedAt, ExpiresAt: recovered.Proof.ExpiresAt}, nil
	}
	idempotency.Now = s.now().UTC()
	if err = s.proofs.VerifyRegistration(ctx, command.Scope, normalized, command.VerificationContinuationID, command.VerificationProof, idempotency.KeyDigest, idempotency.RequestDigest); err != nil {
		return HostedAuthProofResult{}, err
	}
	hash, err := s.passwords.Hash([]byte(command.Credential))
	if err != nil {
		return HostedAuthProofResult{}, err
	}
	if err = ValidateAdaptivePasswordHash("bcrypt", hash); err != nil {
		return HostedAuthProofResult{}, err
	}
	now := s.now().UTC()
	userID, err := s.secrets.ID("usr_")
	if err != nil {
		return HostedAuthProofResult{}, err
	}
	identifierID, err := s.secrets.ID("uid_")
	if err != nil {
		return HostedAuthProofResult{}, err
	}
	credentialID, err := s.secrets.ID("cred_")
	if err != nil {
		return HostedAuthProofResult{}, err
	}
	verifiedAt := now
	registration := EndUserRegistration{User: EndUser{UserID: userID, AccountStatus: "active", CreatedAt: now, UpdatedAt: now}, Identifier: EndUserIdentifier{IdentifierID: identifierID, UserID: userID, Type: normalized.Type, NormalizationVersion: normalized.NormalizationVersion, NormalizedDigest: identifierDigest, MaskedValue: maskEndUserIdentifier(normalized), VerificationStatus: "verified", VerifiedAt: &verifiedAt, CreatedAt: now, UpdatedAt: now}, Credential: EndUserCredential{CredentialID: credentialID, UserID: userID, PasswordHash: hash, Algorithm: "bcrypt", Status: "active", ChangedAt: now}, Profile: EndUserProfile{UserID: userID, Version: 1, DisplayName: profileName, CreatedAt: now, UpdatedAt: now}}
	registration.OutboxEvent, err = s.event("identity.registered.v1", "identity.registered", userID, command.TraceID, "success", "", now)
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
	event, err := s.event("identity.hosted_registration_succeeded.v1", "identity.hosted_registration_succeeded", userID, command.TraceID, "success", "", now)
	if err != nil {
		return HostedAuthProofResult{}, err
	}
	proof := HostedAuthProof{ProofID: proofID, UserID: userID, Scope: command.Scope, AuthenticationMethod: "password", RiskSummaryDigest: s.hasher.Digest("hosted-registration\x00" + command.IdempotencyKey), TTL: ttl, OutboxEvent: event}
	idempotency.ResourceID = proofID
	idempotency.Now = now
	persisted, _, err := repository.CreateHostedRegistration(ctx, HostedRegistrationRecord{Registration: registration, Proof: proof, Idempotency: idempotency, IdentifierDigest: identifierDigest})
	if err != nil {
		return HostedAuthProofResult{}, err
	}
	return HostedAuthProofResult{ProofID: persisted.Proof.ProofID, User: HostedSafeUserSummary{DisplayName: persisted.Profile.DisplayName, AvatarRef: persisted.Profile.AvatarRef}, AuthTime: persisted.Proof.CreatedAt, ExpiresAt: persisted.Proof.ExpiresAt}, nil
}

func (s *EndUserService) GetHostedProfile(ctx context.Context, expected HostedSessionExpectation) (EndUserProfile, error) {
	if err := s.ValidateHostedSession(ctx, expected); err != nil {
		return EndUserProfile{}, err
	}
	return s.repository.GetEndUserProfile(ctx, expected.UserID)
}

func (s *EndUserService) PatchHostedProfile(ctx context.Context, expected HostedSessionExpectation, patch EndUserProfilePatch, version int64, idempotencyKey, traceID string) (EndUserProfile, error) {
	if len(idempotencyKey) < 16 {
		return EndUserProfile{}, errors.New("idempotency key is required")
	}
	if err := s.ValidateHostedSession(ctx, expected); err != nil {
		return EndUserProfile{}, err
	}
	now := s.now().UTC()
	event, err := s.event("identity.profile_updated.v1", "identity.profile_updated", expected.UserID, traceID, "success", "", now)
	if err != nil {
		return EndUserProfile{}, err
	}
	record := EndUserIdempotency{Operation: "profile_update", ScopeID: trustedScopeID(expected.Scope), ActorDigest: s.hasher.Digest("user\x00" + expected.UserID), KeyDigest: s.hasher.Digest("idempotency-key\x00" + idempotencyKey), RequestDigest: s.profilePatchRequestDigest(version, patch), ResourceID: expected.UserID, Now: now}
	updated, _, err := s.repository.PatchEndUserProfileIdempotent(ctx, expected.UserID, patch, version, event, record)
	return updated, err
}

func (s *EndUserService) ListHostedSessions(ctx context.Context, expected HostedSessionExpectation) ([]EndUserSessionSummary, error) {
	if err := s.ValidateHostedSession(ctx, expected); err != nil {
		return nil, err
	}
	return s.repository.ListEndUserSessions(ctx, expected.UserID, expected.SessionID, expected.Scope)
}

func (s *EndUserService) ChangeHostedPassword(ctx context.Context, expected HostedSessionExpectation, current, next string, revokeOthers bool, idempotencyKey, traceID string) error {
	if len(idempotencyKey) < 16 {
		return errors.New("idempotency key is required")
	}
	repository, ok := s.repository.(HostedSelfServiceRepository)
	if !ok {
		return ErrHostedAuthUnavailable
	}
	session, err := repository.FindHostedSession(ctx, expected)
	if err != nil {
		return err
	}
	return s.changePasswordForSession(ctx, session, current, next, revokeOthers, expected.Scope, idempotencyKey, traceID)
}

func (s *EndUserService) ListHostedExternalIdentities(ctx context.Context, expected HostedSessionExpectation) ([]ExternalIdentity, error) {
	if err := s.ValidateHostedSession(ctx, expected); err != nil {
		return nil, err
	}
	repository, ok := s.repository.(HostedSelfServiceRepository)
	if !ok {
		return nil, ErrHostedAuthUnavailable
	}
	return repository.ListExternalIdentities(ctx, expected.UserID)
}

func (s *EndUserService) RevokeHostedSession(ctx context.Context, expected HostedSessionExpectation, targetSessionID, idempotencyKey, traceID string) error {
	if s == nil {
		return ErrHostedAuthUnavailable
	}
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 128 || strings.TrimSpace(targetSessionID) == "" {
		return errors.New("idempotency key and target session are required")
	}
	if expected.UserID == "" || expected.SessionID == "" || expected.Scope.ProductID == "" || expected.Scope.ApplicationID == "" || !ValidEndUserEnvironment(expected.Scope.Environment) {
		return ErrEndUserScopeMismatch
	}
	now := s.now().UTC()
	event, err := s.event("identity.session_revoked.v1", "identity.session_revoked", expected.UserID, traceID, "success", "user_requested", now)
	if err != nil {
		return err
	}
	repository, ok := s.repository.(HostedSelfServiceRepository)
	if !ok {
		return ErrHostedAuthUnavailable
	}
	record := EndUserIdempotency{Operation: "hosted_session_revoke", ScopeID: trustedScopeID(expected.Scope), ActorDigest: s.hasher.Digest("hosted-session-actor\x00" + expected.UserID + "\x00" + expected.SessionID), KeyDigest: s.hasher.Digest("idempotency-key\x00" + idempotencyKey), RequestDigest: s.requestDigest("hosted-session-revoke-request", targetSessionID), ResourceID: targetSessionID, Now: now}
	return repository.RevokeHostedSessionIdempotent(ctx, expected, targetSessionID, now, event, record)
}
