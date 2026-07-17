package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

var (
	ErrEndUserInvalidCredentials  = errors.New("invalid end-user credentials")
	ErrEndUserProviderUnavailable = errors.New("end-user security provider unavailable")
	ErrEndUserRateLimited         = errors.New("end-user authentication rate limited")
)

type EndUserRateLimitError struct {
	BlockedUntil time.Time
	RetryAfter   time.Duration
}

func (e *EndUserRateLimitError) Error() string { return ErrEndUserRateLimited.Error() }
func (e *EndUserRateLimitError) Is(target error) bool {
	return target == ErrEndUserRateLimited
}

type RegistrationProofVerifier interface {
	VerifyRegistration(context.Context, EndUserSessionScope, NormalizedIdentifier, string) error
}

type EndUserAdmissionRequest struct {
	Scope  EndUserSessionScope
	UserID string
	At     time.Time
}

type EndUserAdmissionPort interface {
	AdmitEndUser(context.Context, EndUserAdmissionRequest) error
}

type EndUserServiceOption func(*EndUserService) error

func WithEndUserAdmissionPort(port EndUserAdmissionPort) EndUserServiceOption {
	return func(service *EndUserService) error {
		if port == nil {
			return errors.New("end-user admission port is required")
		}
		service.admission = port
		return nil
	}
}

type RecoveryDeliveryCommand struct {
	DeliveryID  string
	Destination NormalizedIdentifier
	Proof       string
	ExpiresAt   time.Time
}

type RecoveryDeliveryPort interface {
	EnqueueRecovery(context.Context, RecoveryDeliveryCommand) error
}

type EndUserPolicy struct {
	AccessTTL             time.Duration
	RefreshTTL            time.Duration
	RefreshAbsoluteTTL    time.Duration
	RefreshRecoveryWindow time.Duration
	RecoveryTTL           time.Duration
	RecoveryMaxAttempts   int
	LoginWindow           time.Duration
	LoginMaximumAttempts  int
	LoginBlockDuration    time.Duration
	RecentAuthTTL         time.Duration
}

type EndUserService struct {
	repository EndUserRepository
	normalizer IdentifierNormalizer
	passwords  PasswordVerifier
	hasher     securevalue.Hasher
	secrets    securevalue.Generator
	proofs     RegistrationProofVerifier
	recovery   RecoveryDeliveryPort
	admission  EndUserAdmissionPort
	policy     EndUserPolicy
	now        func() time.Time
	fakeHash   []byte
}

func NewEndUserService(repository EndUserRepository, normalizer IdentifierNormalizer, passwords PasswordVerifier, hasher securevalue.Hasher, proofs RegistrationProofVerifier, recovery RecoveryDeliveryPort, policy EndUserPolicy, now func() time.Time, options ...EndUserServiceOption) (*EndUserService, error) {
	if repository == nil || normalizer == nil || passwords == nil {
		return nil, errors.New("end-user repository, normalizer, and password verifier are required")
	}
	if policy.AccessTTL <= 0 || policy.RefreshTTL <= 0 || policy.RefreshAbsoluteTTL < policy.RefreshTTL || policy.RefreshRecoveryWindow <= 0 || policy.RecoveryTTL <= 0 || policy.RecoveryMaxAttempts < 1 || policy.LoginWindow <= 0 || policy.LoginMaximumAttempts < 1 || policy.LoginBlockDuration <= 0 || policy.RecentAuthTTL <= 0 {
		return nil, errors.New("invalid end-user policy")
	}
	if now == nil {
		now = time.Now
	}
	fakeHash, err := passwords.Hash([]byte("fixed-nonexistent-end-user-password"))
	if err != nil {
		return nil, fmt.Errorf("create end-user anti-enumeration hash: %w", err)
	}
	if err := ValidateAdaptivePasswordHash("bcrypt", fakeHash); err != nil {
		return nil, err
	}
	service := &EndUserService{repository: repository, normalizer: normalizer, passwords: passwords, hasher: hasher, secrets: securevalue.DefaultGenerator(), proofs: proofs, recovery: recovery, policy: policy, now: now, fakeHash: fakeHash}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("nil end-user service option")
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

type EndUserRegisterCommand struct {
	Scope             EndUserSessionScope
	Identifier        string
	Credential        string
	VerificationProof string
	DisplayName       string
	TraceID           string
	IdempotencyKey    string
}

type EndUserLoginCommand struct {
	Scope       EndUserSessionScope
	Identifier  string
	Credential  string
	Source      string
	RiskSummary map[string]any
	TraceID     string
}

type EndUserIssuedSession struct {
	Session      EndUserSession
	Profile      EndUserProfile
	AccessToken  string
	RefreshToken string
}

func (s *EndUserService) Register(ctx context.Context, command EndUserRegisterCommand) (EndUserIssuedSession, error) {
	if s.proofs == nil {
		return EndUserIssuedSession{}, ErrEndUserProviderUnavailable
	}
	if len(command.IdempotencyKey) < 16 {
		return EndUserIssuedSession{}, errors.New("idempotency key is required")
	}
	normalized, err := s.normalize(command.Identifier)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	profileName := strings.TrimSpace(command.DisplayName)
	if profileName == "" {
		profileName = "User"
	}
	identifierDigest := s.hasher.Digest("identifier\x00" + normalized.Value)
	idempotency := EndUserIdempotency{Operation: "register", ScopeID: trustedScopeID(command.Scope), ActorDigest: identifierDigest, KeyDigest: s.hasher.Digest("idempotency-key\x00" + command.IdempotencyKey), RequestDigest: s.hasher.Digest("register-request\x00" + normalized.Value + "\x00" + command.Credential + "\x00" + command.VerificationProof + "\x00" + profileName)}
	if persisted, found, err := s.repository.RecoverEndUserRegistration(ctx, idempotency); err != nil {
		return EndUserIssuedSession{}, err
	} else if found {
		return EndUserIssuedSession{Session: persisted.Session.EndUserSession(), Profile: persisted.Profile, AccessToken: s.derivedToken("register-access", normalized.Value, command.IdempotencyKey), RefreshToken: s.derivedToken("register-refresh", normalized.Value, command.IdempotencyKey)}, nil
	}
	idempotency.Now = s.now().UTC()
	if err := s.proofs.VerifyRegistration(ctx, command.Scope, normalized, command.VerificationProof); err != nil {
		if !errors.Is(err, ErrEndUserInvalidCredentials) {
			return EndUserIssuedSession{}, err
		}
		if persistErr := s.repository.FailEndUserIdempotency(ctx, idempotency, "invalid_credentials"); persistErr != nil {
			return EndUserIssuedSession{}, persistErr
		}
		return EndUserIssuedSession{}, ErrEndUserInvalidCredentials
	}
	hash, err := s.passwords.Hash([]byte(command.Credential))
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	if err := ValidateAdaptivePasswordHash("bcrypt", hash); err != nil {
		return EndUserIssuedSession{}, err
	}
	now := s.now().UTC()
	userID, err := s.secrets.ID("usr_")
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	identifierID, err := s.secrets.ID("uid_")
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	credentialID, err := s.secrets.ID("cred_")
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	verifiedAt := now
	registration := EndUserRegistration{
		User:       EndUser{UserID: userID, AccountStatus: "active", CreatedAt: now, UpdatedAt: now},
		Identifier: EndUserIdentifier{IdentifierID: identifierID, UserID: userID, Type: normalized.Type, NormalizationVersion: normalized.NormalizationVersion, NormalizedDigest: identifierDigest, MaskedValue: maskEndUserIdentifier(normalized), VerificationStatus: "verified", VerifiedAt: &verifiedAt, CreatedAt: now, UpdatedAt: now},
		Credential: EndUserCredential{CredentialID: credentialID, UserID: userID, PasswordHash: hash, Algorithm: "bcrypt", Status: "active", ChangedAt: now},
		Profile:    EndUserProfile{UserID: userID, Version: 1, DisplayName: profileName, CreatedAt: now, UpdatedAt: now},
	}
	registration.OutboxEvent, err = s.event("identity.registered.v1", "identity.registered", userID, command.TraceID, "success", "", now)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	issued, stored, err := s.newSession(userID, command.Scope, "password", nil, now, command.TraceID)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	issued.AccessToken = s.derivedToken("register-access", normalized.Value, command.IdempotencyKey)
	issued.RefreshToken = s.derivedToken("register-refresh", normalized.Value, command.IdempotencyKey)
	stored.AccessToken.Digest = s.hasher.Digest("access\x00" + issued.AccessToken)
	stored.RefreshToken.Digest = s.hasher.Digest("refresh\x00" + issued.RefreshToken)
	idempotency.ResourceID, idempotency.Now = stored.Session.SessionID, now
	persisted, recovered, err := s.repository.CreateEndUserWithSessionIdempotent(ctx, registration, stored, idempotency)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	if recovered {
		issued.Session = persisted.Session.EndUserSession()
	} else {
		issued.Session = stored.Session
	}
	issued.Profile = persisted.Profile
	return issued, nil
}

func (s *EndUserService) Login(ctx context.Context, command EndUserLoginCommand) (EndUserIssuedSession, error) {
	normalized, err := s.normalize(command.Identifier)
	if err != nil {
		return EndUserIssuedSession{}, ErrEndUserInvalidCredentials
	}
	identifierDigest := s.hasher.Digest("identifier\x00" + normalized.Value)
	sourceDigest := s.hasher.Digest("login-source\x00" + command.Source)
	scopeID := trustedScopeID(command.Scope)
	now := s.now().UTC()
	throttle, err := s.repository.EndUserLoginThrottle(ctx, scopeID, identifierDigest, sourceDigest, now)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	if throttle.BlockedUntil != nil && throttle.BlockedUntil.After(now) {
		return EndUserIssuedSession{}, &EndUserRateLimitError{BlockedUntil: *throttle.BlockedUntil, RetryAfter: throttle.BlockedUntil.Sub(now)}
	}
	credential, findErr := s.repository.FindEndUserPasswordCredential(ctx, normalized.Type, identifierDigest)
	comparisonHash := s.fakeHash
	if findErr == nil {
		comparisonHash = credential.PasswordHash
	} else if !errors.Is(findErr, ErrNotFound) {
		return EndUserIssuedSession{}, findErr
	}
	compareErr := s.passwords.Compare(comparisonHash, []byte(command.Credential))
	if findErr != nil || compareErr != nil || credential.AccountStatus != "active" {
		if err := s.recordLoginFailure(ctx, scopeID, identifierDigest, sourceDigest, command.TraceID, now); err != nil {
			return EndUserIssuedSession{}, err
		}
		return EndUserIssuedSession{}, ErrEndUserInvalidCredentials
	}
	if s.admission != nil {
		if err := s.admission.AdmitEndUser(ctx, EndUserAdmissionRequest{Scope: command.Scope, UserID: credential.UserID, At: now}); err != nil {
			return EndUserIssuedSession{}, err
		}
	}
	risk, err := json.Marshal(command.RiskSummary)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	issued, stored, err := s.newSession(credential.UserID, command.Scope, "password", s.hasher.Digest("risk\x00"+string(risk)), now, command.TraceID)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	if err := s.repository.CreateEndUserSessionAndClearFailures(ctx, stored, scopeID, identifierDigest); err != nil {
		return EndUserIssuedSession{}, err
	}
	profile, err := s.repository.GetEndUserProfile(ctx, credential.UserID)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	issued.Profile = profile
	return issued, nil
}

func (s *EndUserService) CurrentSession(ctx context.Context, accessToken string, scope EndUserSessionScope) (EndUserIssuedSession, error) {
	session, err := s.repository.FindEndUserByAccessDigest(ctx, s.hasher.Digest("access\x00"+accessToken), scope, s.now().UTC())
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	profile, err := s.repository.GetEndUserProfile(ctx, session.UserID)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	return EndUserIssuedSession{Session: session, Profile: profile}, nil
}

func (s *EndUserService) ResolveCurrentSession(ctx context.Context, accessToken string) (EndUserIssuedSession, error) {
	session, err := s.repository.ResolveEndUserByAccessDigest(ctx, s.hasher.Digest("access\x00"+accessToken), s.now().UTC())
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	profile, err := s.repository.GetEndUserProfile(ctx, session.UserID)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	return EndUserIssuedSession{Session: session, Profile: profile}, nil
}

func (s *EndUserService) Refresh(ctx context.Context, refreshToken, clientRequestID, traceID string, scope EndUserSessionScope) (EndUserIssuedSession, error) {
	if len(clientRequestID) < 16 {
		return EndUserIssuedSession{}, errors.New("client request id is required")
	}
	now := s.now().UTC()
	access := s.derivedToken("access", refreshToken, clientRequestID)
	refresh := s.derivedToken("refresh", refreshToken, clientRequestID)
	accessID, err := s.secrets.ID("uat_")
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	refreshID, err := s.secrets.ID("urt_")
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	event, err := s.event("identity.session_refreshed.v1", "identity.session_refreshed", "system", traceID, "success", "", now)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	rotation := EndUserRefreshRotation{
		AccessToken:     EndUserSessionToken{TokenID: accessID, TokenType: "access", Generation: 2, Digest: s.hasher.Digest("access\x00" + access), CreatedAt: now, ExpiresAt: now.Add(s.policy.AccessTTL)},
		RefreshToken:    EndUserSessionToken{TokenID: refreshID, TokenType: "refresh", Generation: 2, Digest: s.hasher.Digest("refresh\x00" + refresh), CreatedAt: now, ExpiresAt: now.Add(s.policy.RefreshTTL)},
		AccessExpiresAt: now.Add(s.policy.AccessTTL), RefreshExpiresAt: now.Add(s.policy.RefreshTTL), Now: now, OutboxEvent: event,
		RequestDigest: s.hasher.Digest("refresh-request\x00" + clientRequestID), RecoveryExpiresAt: now.Add(s.policy.RefreshRecoveryWindow),
	}
	session, err := s.repository.RotateEndUserRefresh(ctx, s.hasher.Digest("refresh\x00"+refreshToken), scope, rotation)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	profile, err := s.repository.GetEndUserProfile(ctx, session.UserID)
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	return EndUserIssuedSession{Session: session, Profile: profile, AccessToken: access, RefreshToken: refresh}, nil
}

func (s *EndUserService) RefreshResolved(ctx context.Context, refreshToken, clientRequestID, traceID string) (EndUserIssuedSession, error) {
	scope, err := s.repository.ResolveEndUserRefreshScope(ctx, s.hasher.Digest("refresh\x00"+refreshToken), s.now().UTC())
	if err != nil {
		return EndUserIssuedSession{}, err
	}
	return s.Refresh(ctx, refreshToken, clientRequestID, traceID, scope)
}

func (s *EndUserService) Logout(ctx context.Context, accessToken, traceID string, scope EndUserSessionScope) error {
	now := s.now().UTC()
	session, err := s.repository.FindEndUserByAccessDigest(ctx, s.hasher.Digest("access\x00"+accessToken), scope, now)
	if errors.Is(err, ErrEndUserSessionRevoked) {
		return nil
	}
	if err != nil {
		return err
	}
	event, err := s.event("identity.session_revoked.v1", "identity.session_revoked", session.UserID, traceID, "success", "logout", now)
	if err != nil {
		return err
	}
	return s.repository.RevokeEndUserSession(ctx, session.UserID, session.SessionID, scope, "logout", now, event)
}

func (s *EndUserService) LogoutResolved(ctx context.Context, accessToken, traceID string) error {
	current, err := s.ResolveCurrentSession(ctx, accessToken)
	if errors.Is(err, ErrEndUserSessionRevoked) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.Logout(ctx, accessToken, traceID, scopeFromSession(current.Session))
}

func (s *EndUserService) GetProfile(ctx context.Context, accessToken string, scope EndUserSessionScope) (EndUserProfile, error) {
	current, err := s.CurrentSession(ctx, accessToken, scope)
	return current.Profile, err
}

func (s *EndUserService) GetProfileResolved(ctx context.Context, accessToken string) (EndUserProfile, error) {
	current, err := s.ResolveCurrentSession(ctx, accessToken)
	return current.Profile, err
}

func (s *EndUserService) UpdateProfile(ctx context.Context, accessToken string, scope EndUserSessionScope, profile EndUserProfile, expectedVersion int64, idempotencyKey, traceID string) (EndUserProfile, error) {
	return s.PatchProfile(ctx, accessToken, scope, EndUserProfilePatch{
		DisplayName: EndUserProfilePatchValue{Set: true, Value: &profile.DisplayName},
		AvatarRef:   EndUserProfilePatchValue{Set: true, Value: profile.AvatarRef},
		Locale:      EndUserProfilePatchValue{Set: true, Value: profile.Locale},
		Timezone:    EndUserProfilePatchValue{Set: true, Value: profile.Timezone},
	}, expectedVersion, idempotencyKey, traceID)
}

func (s *EndUserService) PatchProfile(ctx context.Context, accessToken string, scope EndUserSessionScope, patch EndUserProfilePatch, expectedVersion int64, idempotencyKey, traceID string) (EndUserProfile, error) {
	if len(idempotencyKey) < 16 {
		return EndUserProfile{}, errors.New("idempotency key is required")
	}
	current, err := s.CurrentSession(ctx, accessToken, scope)
	if err != nil {
		return EndUserProfile{}, err
	}
	now := s.now().UTC()
	event, err := s.event("identity.profile_updated.v1", "identity.profile_updated", current.Session.UserID, traceID, "success", "", now)
	if err != nil {
		return EndUserProfile{}, err
	}
	record := EndUserIdempotency{Operation: "profile_update", ScopeID: trustedScopeID(scope), ActorDigest: s.hasher.Digest("user\x00" + current.Session.UserID), KeyDigest: s.hasher.Digest("idempotency-key\x00" + idempotencyKey), RequestDigest: s.profilePatchRequestDigest(expectedVersion, patch), ResourceID: current.Session.UserID, Now: now}
	updated, _, err := s.repository.PatchEndUserProfileIdempotent(ctx, current.Session.UserID, patch, expectedVersion, event, record)
	return updated, err
}

func (s *EndUserService) UpdateProfileResolved(ctx context.Context, accessToken string, profile EndUserProfile, expectedVersion int64, idempotencyKey, traceID string) (EndUserProfile, error) {
	current, err := s.ResolveCurrentSession(ctx, accessToken)
	if err != nil {
		return EndUserProfile{}, err
	}
	return s.UpdateProfile(ctx, accessToken, scopeFromSession(current.Session), profile, expectedVersion, idempotencyKey, traceID)
}

func (s *EndUserService) PatchProfileResolved(ctx context.Context, accessToken string, patch EndUserProfilePatch, expectedVersion int64, idempotencyKey, traceID string) (EndUserProfile, error) {
	current, err := s.ResolveCurrentSession(ctx, accessToken)
	if err != nil {
		return EndUserProfile{}, err
	}
	return s.PatchProfile(ctx, accessToken, scopeFromSession(current.Session), patch, expectedVersion, idempotencyKey, traceID)
}

func (s *EndUserService) profilePatchRequestDigest(expectedVersion int64, patch EndUserProfilePatch) []byte {
	return s.hasher.Digest(fmt.Sprintf("profile-patch\x00%d\x00%t\x00%s\x00%t\x00%s\x00%t\x00%s\x00%t\x00%s", expectedVersion,
		patch.DisplayName.Set, nullableEndUserPatchValue(patch.DisplayName.Value),
		patch.AvatarRef.Set, nullableEndUserPatchValue(patch.AvatarRef.Value),
		patch.Locale.Set, nullableEndUserPatchValue(patch.Locale.Value),
		patch.Timezone.Set, nullableEndUserPatchValue(patch.Timezone.Value)))
}

func nullableEndUserPatchValue(value *string) string {
	if value == nil {
		return "0:"
	}
	return fmt.Sprintf("1:%d:%s", len(*value), *value)
}

func (s *EndUserService) ChangePassword(ctx context.Context, accessToken, currentPassword, newPassword string, revokeOthers bool, scope EndUserSessionScope, idempotencyKey, traceID string) error {
	if len(idempotencyKey) < 16 {
		return errors.New("idempotency key is required")
	}
	current, err := s.CurrentSession(ctx, accessToken, scope)
	if err != nil {
		return err
	}
	return s.changePasswordForSession(ctx, current.Session, currentPassword, newPassword, revokeOthers, scope, idempotencyKey, traceID)
}

func (s *EndUserService) changePasswordForSession(ctx context.Context, session EndUserSession, currentPassword, newPassword string, revokeOthers bool, scope EndUserSessionScope, idempotencyKey, traceID string) error {
	if s.now().UTC().Sub(session.AuthTime) > s.policy.RecentAuthTTL {
		return ErrEndUserReauthenticationRequired
	}
	record := EndUserIdempotency{Operation: "password_change", ScopeID: trustedScopeID(scope), ActorDigest: s.hasher.Digest("user\x00" + session.UserID), KeyDigest: s.hasher.Digest("idempotency-key\x00" + idempotencyKey), RequestDigest: s.passwordChangeRequestDigest(currentPassword, newPassword, revokeOthers), ResourceID: session.SessionID, Now: s.now().UTC()}
	if recovered, err := s.repository.RecoverEndUserIdempotency(ctx, record); err != nil {
		return err
	} else if recovered {
		return nil
	}
	credential, err := s.repository.FindEndUserPasswordCredentialByUser(ctx, session.UserID)
	if err != nil {
		return err
	}
	if s.passwords.Compare(credential.PasswordHash, []byte(currentPassword)) != nil {
		if persistErr := s.repository.FailEndUserIdempotency(ctx, record, "invalid_credentials"); persistErr != nil {
			return persistErr
		}
		return ErrEndUserInvalidCredentials
	}
	hash, err := s.passwords.Hash([]byte(newPassword))
	if err != nil {
		return err
	}
	now := s.now().UTC()
	event, err := s.event("identity.password_changed.v1", "identity.password_changed", session.UserID, traceID, "success", "", now)
	if err != nil {
		return err
	}
	record.Now = now
	_, err = s.repository.ReplaceEndUserPasswordIdempotent(ctx, session.UserID, session.SessionID, hash, "bcrypt", credential.Version, now, revokeOthers, event, record)
	return err
}

func (s *EndUserService) ChangePasswordResolved(ctx context.Context, accessToken, currentPassword, newPassword string, revokeOthers bool, idempotencyKey, traceID string) error {
	if len(idempotencyKey) < 16 {
		return errors.New("idempotency key is required")
	}
	recovered, err := s.repository.RecoverEndUserPasswordChange(ctx, s.hasher.Digest("access\x00"+accessToken), s.hasher.Digest("idempotency-key\x00"+idempotencyKey), s.passwordChangeRequestDigest(currentPassword, newPassword, revokeOthers))
	if err != nil || recovered {
		return err
	}
	current, err := s.ResolveCurrentSession(ctx, accessToken)
	if err != nil {
		if recovered, recoveryErr := s.repository.RecoverEndUserPasswordChange(ctx, s.hasher.Digest("access\x00"+accessToken), s.hasher.Digest("idempotency-key\x00"+idempotencyKey), s.passwordChangeRequestDigest(currentPassword, newPassword, revokeOthers)); recoveryErr != nil || recovered {
			return recoveryErr
		}
		return err
	}
	return s.changePasswordForSession(ctx, current.Session, currentPassword, newPassword, revokeOthers, scopeFromSession(current.Session), idempotencyKey, traceID)
}

func (s *EndUserService) passwordChangeRequestDigest(currentPassword, newPassword string, revokeOthers bool) []byte {
	return s.hasher.Digest(fmt.Sprintf("password-change\x00%s\x00%s\x00%t", currentPassword, newPassword, revokeOthers))
}

func (s *EndUserService) ListSessions(ctx context.Context, accessToken string, scope EndUserSessionScope) ([]EndUserSessionSummary, error) {
	current, err := s.CurrentSession(ctx, accessToken, scope)
	if err != nil {
		return nil, err
	}
	return s.repository.ListEndUserSessions(ctx, current.Session.UserID, current.Session.SessionID, scope)
}

func (s *EndUserService) ListSessionsResolved(ctx context.Context, accessToken string) ([]EndUserSessionSummary, error) {
	current, err := s.ResolveCurrentSession(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	return s.repository.ListEndUserSessions(ctx, current.Session.UserID, current.Session.SessionID, scopeFromSession(current.Session))
}

func (s *EndUserService) RevokeSession(ctx context.Context, accessToken, targetSessionID, traceID string, scope EndUserSessionScope) error {
	current, err := s.CurrentSession(ctx, accessToken, scope)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	event, err := s.event("identity.session_revoked.v1", "identity.session_revoked", current.Session.UserID, traceID, "success", "user_requested", now)
	if err != nil {
		return err
	}
	return s.repository.RevokeEndUserSession(ctx, current.Session.UserID, targetSessionID, scope, "user_requested", now, event)
}

func (s *EndUserService) RevokeSessionResolved(ctx context.Context, accessToken, targetSessionID, traceID string) error {
	current, err := s.ResolveCurrentSession(ctx, accessToken)
	if err != nil {
		return err
	}
	return s.RevokeSession(ctx, accessToken, targetSessionID, traceID, scopeFromSession(current.Session))
}

type StartEndUserRecoveryCommand struct {
	Scope          EndUserSessionScope
	Identifier     string
	IdempotencyKey string
	TraceID        string
}

func (s *EndUserService) StartRecovery(ctx context.Context, command StartEndUserRecoveryCommand) (string, error) {
	if s.recovery == nil {
		return "", ErrEndUserProviderUnavailable
	}
	if len(command.IdempotencyKey) < 16 {
		return "", errors.New("idempotency key is required")
	}
	normalized, err := s.normalize(command.Identifier)
	if err != nil {
		return "", ErrEndUserInvalidCredentials
	}
	digest := s.hasher.Digest("identifier\x00" + normalized.Value)
	target, findErr := s.repository.FindEndUserRecoveryTarget(ctx, normalized.Type, digest)
	if findErr != nil && !errors.Is(findErr, ErrNotFound) {
		return "", findErr
	}
	var userID *string
	masked := maskEndUserIdentifier(normalized)
	if findErr == nil {
		userID, masked = target.UserID, target.MaskedValue
	}
	continuation := s.derivedToken("recovery-continuation", normalized.Value, command.IdempotencyKey)
	proof := s.derivedToken("recovery-proof", normalized.Value, command.IdempotencyKey)
	challengeID, err := s.secrets.ID("rch_")
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	event, err := s.event("identity.recovery_started.v1", "identity.recovery_started", "anonymous", command.TraceID, "success", "", now)
	if err != nil {
		return "", err
	}
	challenge := RecoveryChallenge{ChallengeID: challengeID, ContinuationDigest: s.hasher.Digest("recovery-continuation\x00" + continuation), IdentifierType: normalized.Type, IdentifierDigest: digest, MatchedUserID: userID, DeliveryTargetMasked: masked, ProofDigest: s.hasher.Digest("recovery-proof\x00" + proof), MaxAttempts: s.policy.RecoveryMaxAttempts, CreatedAt: now, ExpiresAt: now.Add(s.policy.RecoveryTTL), OutboxEvent: event}
	idempotency := EndUserIdempotency{Operation: "recovery_start", ScopeID: trustedScopeID(command.Scope), ActorDigest: digest, KeyDigest: s.hasher.Digest("idempotency-key\x00" + command.IdempotencyKey), RequestDigest: s.hasher.Digest("recovery-start-request\x00" + normalized.Value), ResourceID: challengeID, Now: now}
	persistedChallenge, _, err := s.repository.CreateRecoveryChallengeIdempotent(ctx, challenge, idempotency)
	if err != nil {
		return "", err
	}
	if err := s.recovery.EnqueueRecovery(ctx, RecoveryDeliveryCommand{DeliveryID: persistedChallenge.ChallengeID, Destination: normalized, Proof: proof, ExpiresAt: persistedChallenge.ExpiresAt}); err != nil {
		return "", err
	}
	return continuation, nil
}

type CompleteEndUserRecoveryCommand struct {
	Scope          EndUserSessionScope
	Continuation   string
	Proof          string
	NewCredential  string
	IdempotencyKey string
	TraceID        string
}

func (s *EndUserService) CompleteRecovery(ctx context.Context, command CompleteEndUserRecoveryCommand) error {
	if len(command.IdempotencyKey) < 16 {
		return errors.New("idempotency key is required")
	}
	hash, err := s.passwords.Hash([]byte(command.NewCredential))
	if err != nil {
		return err
	}
	now := s.now().UTC()
	event, err := s.event("identity.credential_recovered.v1", "identity.credential_recovered", "anonymous", command.TraceID, "success", "", now)
	if err != nil {
		return err
	}
	continuationDigest := s.hasher.Digest("recovery-continuation\x00" + command.Continuation)
	idempotency := EndUserIdempotency{Operation: "recovery_complete", ScopeID: trustedScopeID(command.Scope), ActorDigest: continuationDigest, KeyDigest: s.hasher.Digest("idempotency-key\x00" + command.IdempotencyKey), RequestDigest: s.hasher.Digest("recovery-complete-request\x00" + command.Continuation + "\x00" + command.Proof + "\x00" + command.NewCredential), Now: now}
	_, _, err = s.repository.CompleteEndUserRecoveryIdempotent(ctx, idempotency.ScopeID, continuationDigest, s.hasher.Digest("recovery-proof\x00"+command.Proof), hash, "bcrypt", now, event, idempotency)
	if err != nil {
		return err
	}
	return nil
}

func (s *EndUserService) normalize(raw string) (NormalizedIdentifier, error) {
	kind := IdentifierEmail
	if strings.HasPrefix(strings.TrimSpace(raw), "+") {
		kind = IdentifierPhone
	}
	return s.normalizer.Normalize(kind, raw)
}

func (s *EndUserService) newSession(userID string, scope EndUserSessionScope, method string, riskDigest []byte, now time.Time, traceID string) (EndUserIssuedSession, NewEndUserSession, error) {
	sessionID, err := s.secrets.ID("uses_")
	if err != nil {
		return EndUserIssuedSession{}, NewEndUserSession{}, err
	}
	familyID, err := s.secrets.ID("ufam_")
	if err != nil {
		return EndUserIssuedSession{}, NewEndUserSession{}, err
	}
	accessID, err := s.secrets.ID("uat_")
	if err != nil {
		return EndUserIssuedSession{}, NewEndUserSession{}, err
	}
	refreshID, err := s.secrets.ID("urt_")
	if err != nil {
		return EndUserIssuedSession{}, NewEndUserSession{}, err
	}
	access, err := s.secrets.Token("ua_")
	if err != nil {
		return EndUserIssuedSession{}, NewEndUserSession{}, err
	}
	refresh, err := s.secrets.Token("ur_")
	if err != nil {
		return EndUserIssuedSession{}, NewEndUserSession{}, err
	}
	accessExpires, refreshExpires, absoluteExpires := now.Add(s.policy.AccessTTL), now.Add(s.policy.RefreshTTL), now.Add(s.policy.RefreshAbsoluteTTL)
	session := EndUserSession{SessionID: sessionID, UserID: userID, ProductID: scope.ProductID, ApplicationID: scope.ApplicationID, TenantID: scope.TenantID, TokenFamilyID: familyID, AuthenticationMethod: method, Version: 1, AuthTime: now, CreatedAt: now, LastSeenAt: now, AccessExpiresAt: accessExpires, RefreshExpiresAt: refreshExpires, AbsoluteExpiresAt: absoluteExpires, RiskSummaryDigest: riskDigest, AccountStatus: "active"}
	event, err := s.event("identity.session_created.v1", "identity.session_created", userID, traceID, "success", "", now)
	if err != nil {
		return EndUserIssuedSession{}, NewEndUserSession{}, err
	}
	stored := NewEndUserSession{Session: session, AccessToken: EndUserSessionToken{TokenID: accessID, TokenType: "access", Generation: 1, Digest: s.hasher.Digest("access\x00" + access), CreatedAt: now, ExpiresAt: accessExpires}, RefreshToken: EndUserSessionToken{TokenID: refreshID, TokenType: "refresh", Generation: 1, Digest: s.hasher.Digest("refresh\x00" + refresh), CreatedAt: now, ExpiresAt: refreshExpires}, OutboxEvent: event}
	return EndUserIssuedSession{Session: session, AccessToken: access, RefreshToken: refresh}, stored, nil
}

func (s *EndUserService) derivedToken(kind, oldRefresh, requestID string) string {
	return "u" + kind[:1] + "_" + base64.RawURLEncoding.EncodeToString(s.hasher.Digest("end-user-refresh\x00"+kind+"\x00"+oldRefresh+"\x00"+requestID))
}

func (s *EndUserService) event(topic, action, actor, trace, result, reason string, now time.Time) (OutboxEvent, error) {
	id, err := s.secrets.ID("evt_")
	if err != nil {
		return OutboxEvent{}, err
	}
	auditID, err := s.secrets.ID("aud_")
	if err != nil {
		return OutboxEvent{}, err
	}
	return OutboxEvent{EventID: id, Topic: topic, Now: now, Payload: SecurityEvent{AuditID: auditID, OccurredAt: now, ActorID: actor, Action: action, TargetType: "end_user", TargetID: s.hasher.DigestHex(actor), Result: result, ReasonCode: reason, TraceID: trace, RiskLevel: "normal"}}, nil
}

func (s *EndUserService) recordLoginFailure(ctx context.Context, scopeID string, identifierDigest, sourceDigest []byte, traceID string, now time.Time) error {
	event, err := s.event("identity.login_failed.v1", "identity.login_failed", "anonymous", traceID, "failure", "invalid_credentials", now)
	if err != nil {
		return err
	}
	_, err = s.repository.RecordEndUserLoginFailure(ctx, EndUserLoginFailure{ScopeID: scopeID, IdentifierDigest: identifierDigest, SourceDigest: sourceDigest, Now: now, Window: s.policy.LoginWindow, MaximumAttempts: s.policy.LoginMaximumAttempts, BlockDuration: s.policy.LoginBlockDuration, OutboxEvent: event})
	return err
}

func trustedScopeID(scope EndUserSessionScope) string {
	tenant := "-"
	if scope.TenantID != nil {
		tenant = *scope.TenantID
	}
	return fmt.Sprintf("p%d:%s|a%d:%s|t%d:%s", len(scope.ProductID), scope.ProductID, len(scope.ApplicationID), scope.ApplicationID, len(tenant), tenant)
}

func scopeFromSession(session EndUserSession) EndUserSessionScope {
	return EndUserSessionScope{ProductID: session.ProductID, ApplicationID: session.ApplicationID, TenantID: session.TenantID}
}

func maskEndUserIdentifier(value NormalizedIdentifier) string {
	if value.Type == IdentifierPhone {
		if len(value.Value) <= 6 {
			return "***"
		}
		return value.Value[:3] + "***" + value.Value[len(value.Value)-3:]
	}
	parts := strings.SplitN(value.Value, "@", 2)
	if len(parts) != 2 {
		return "***"
	}
	return parts[0][:1] + "***@" + parts[1]
}
