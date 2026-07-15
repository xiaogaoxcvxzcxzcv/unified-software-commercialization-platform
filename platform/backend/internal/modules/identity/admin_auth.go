package identity

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

type Service struct {
	repository Repository
	access     AccessSnapshotResolver
	passwords  PasswordVerifier
	hasher     securevalue.Hasher
	secrets    securevalue.Generator
	policy     Policy
	now        func() time.Time
	fakeHash   []byte
}

func NewService(repository Repository, access AccessSnapshotResolver, passwords PasswordVerifier, hasher securevalue.Hasher, policy Policy, now func() time.Time) (*Service, error) {
	if now == nil {
		now = time.Now
	}
	fakeHash, err := passwords.Hash([]byte("fixed-nonexistent-administrator-password"))
	if err != nil {
		return nil, fmt.Errorf("create anti-enumeration hash: %w", err)
	}
	return &Service{repository: repository, access: access, passwords: passwords, hasher: hasher, secrets: securevalue.DefaultGenerator(), policy: policy, now: now, fakeHash: fakeHash}, nil
}

func (s *Service) LoginAdmin(ctx context.Context, command LoginCommand) (AdminSession, error) {
	now := s.now().UTC()
	transport := command.Requested
	if transport == "" {
		transport = TransportCookie
	}
	if transport != TransportCookie && transport != TransportBearer {
		return AdminSession{}, ErrInvalidCredentials
	}
	identifier := normalizeIdentifier(command.Identifier)
	identifierDigest := s.hasher.Digest(identifier)
	sourceDigest := s.hasher.Digest(command.Source)
	throttle, err := s.repository.LoginThrottle(ctx, identifierDigest, sourceDigest, now)
	if err != nil {
		return AdminSession{}, err
	}
	if throttle.BlockedUntil != nil && throttle.BlockedUntil.After(now) {
		return AdminSession{}, newRateLimitError(throttle.BlockedUntil.Sub(now))
	}
	credential, findErr := s.repository.FindCredential(ctx, identifierDigest)
	if findErr != nil && !errors.Is(findErr, ErrNotFound) {
		return AdminSession{}, findErr
	}
	hash := s.fakeHash
	if findErr == nil {
		hash = credential.PasswordHash
	}
	passwordOK := s.passwords.Compare(hash, []byte(command.Credential)) == nil
	var controlledClient *ControlledClientCredential
	var controlledClientErr error
	if transport == TransportBearer {
		if !s.policy.AllowBearer {
			controlledClientErr = ErrBearerNotAllowed
		} else {
			resolved, err := s.resolveControlledClient(ctx, command.ControlledClient, now)
			if err != nil {
				controlledClientErr = err
			} else {
				controlledClient = &resolved
			}
		}
	}
	if controlledClientErr != nil && !errors.Is(controlledClientErr, ErrBearerNotAllowed) {
		return AdminSession{}, controlledClientErr
	}
	if findErr != nil || !passwordOK || credential.AccountStatus != "active" || controlledClientErr != nil {
		return AdminSession{}, s.failedLogin(ctx, identifierDigest, sourceDigest, command.TraceID, "invalid_credentials", now)
	}
	sessionID, familyID, accessID, refreshID, accessToken, refreshToken, csrfToken, err := generateSessionSecrets(s.secrets, transport)
	if err != nil {
		return AdminSession{}, err
	}
	if transport == TransportCookie {
		csrfToken = s.csrfTokenFor(sessionID, 1)
	}
	snapshot, err := s.access.ResolveAdminAccessSnapshot(ctx, credential.UserID, sessionID)
	if err != nil {
		if !errors.Is(err, accesscontrol.ErrNoActiveScope) {
			return AdminSession{}, err
		}
		return AdminSession{}, s.failedLogin(ctx, identifierDigest, sourceDigest, command.TraceID, "no_active_admin_scope", now)
	}
	accessExpires, refreshExpires := now.Add(s.policy.AccessTTL), now.Add(s.policy.RefreshTTL)
	stored := StoredSession{SessionID: sessionID, UserID: credential.UserID, DisplayName: credential.DisplayName, AccountStatus: credential.AccountStatus, TokenFamilyID: familyID, Transport: transport, AuthenticationMethod: "password", SessionVersion: 1, AuthTime: now, AccessExpiresAt: accessExpires, RefreshExpiresAt: refreshExpires, AbsoluteExpiresAt: refreshExpires}
	if controlledClient != nil {
		stored.ControlledClientID = controlledClient.ClientID
		stored.ControlledCredentialID = controlledClient.CredentialID
	}
	if csrfToken != "" {
		stored.CSRFDigest = s.hasher.Digest(csrfToken)
	}
	eventSummary := map[string]any{"authorization_version": snapshot.AuthorizationVersion, "transport": transport}
	if controlledClient != nil {
		eventSummary["controlled_client_ref"] = s.hasher.DigestHex(controlledClient.ClientID)
	}
	event, err := s.securityEvent("admin.auth.login_succeeded", credential.UserID, s.hasher.DigestHex(sessionID), "success", "", command.TraceID, "normal", now, eventSummary)
	if err != nil {
		return AdminSession{}, err
	}
	created := NewSession{StoredSession: stored, AccessToken: TokenRecord{TokenID: accessID, TokenType: "access", Generation: 1, Digest: s.hasher.Digest(accessToken), ExpiresAt: accessExpires}, RefreshToken: TokenRecord{TokenID: refreshID, TokenType: "refresh", Generation: 1, Digest: s.hasher.Digest(refreshToken), ExpiresAt: refreshExpires}, RiskSummary: redactRisk(command.RiskSummary), OutboxEvent: event, CreatedAt: now}
	if err := s.repository.CreateAdminSession(ctx, created); err != nil {
		return AdminSession{}, err
	}
	_ = s.repository.ClearLoginFailures(ctx, identifierDigest)
	return buildAdminSession(stored, snapshot, csrfToken, accessToken, refreshToken), nil
}

func (s *Service) CurrentAdminSession(ctx context.Context, accessToken string) (AdminSession, error) {
	now := s.now().UTC()
	stored, err := s.repository.FindByAccessDigest(ctx, s.hasher.Digest(accessToken), now)
	if err != nil {
		return AdminSession{}, err
	}
	if stored.Transport == TransportBearer && !s.policy.AllowBearer {
		return AdminSession{}, ErrSessionRevoked
	}
	snapshot, err := s.access.ResolveAdminAccessSnapshot(ctx, stored.UserID, stored.SessionID)
	if err != nil {
		if errors.Is(err, accesscontrol.ErrNoActiveScope) {
			return AdminSession{}, ErrSessionRevoked
		}
		return AdminSession{}, err
	}
	_ = s.repository.TouchSession(ctx, stored.SessionID, now)
	csrf := ""
	if stored.Transport == TransportCookie {
		csrf = s.csrfTokenFor(stored.SessionID, stored.SessionVersion)
	}
	return buildAdminSession(stored, snapshot, csrf, "", ""), nil
}

func (s *Service) csrfTokenFor(sessionID string, version int64) string {
	value := fmt.Sprintf("admin-csrf:%s:%d", sessionID, version)
	return "csrf_" + base64.RawURLEncoding.EncodeToString(s.hasher.Digest(value))
}

func (s *Service) CurrentAdminSessionWithCSRF(ctx context.Context, accessToken, csrfToken string) (AdminSession, error) {
	now := s.now().UTC()
	stored, err := s.repository.FindByAccessDigest(ctx, s.hasher.Digest(accessToken), now)
	if err != nil {
		return AdminSession{}, err
	}
	if stored.Transport == TransportBearer && !s.policy.AllowBearer {
		return AdminSession{}, ErrSessionRevoked
	}
	if stored.Transport == TransportCookie && !hmac.Equal(stored.CSRFDigest, s.hasher.Digest(csrfToken)) {
		return AdminSession{}, ErrCSRFFailed
	}
	snapshot, err := s.access.ResolveAdminAccessSnapshot(ctx, stored.UserID, stored.SessionID)
	if err != nil {
		if errors.Is(err, accesscontrol.ErrNoActiveScope) {
			return AdminSession{}, ErrSessionRevoked
		}
		return AdminSession{}, err
	}
	_ = s.repository.TouchSession(ctx, stored.SessionID, now)
	return buildAdminSession(stored, snapshot, csrfToken, "", ""), nil
}

func (s *Service) RefreshAdminSession(ctx context.Context, refreshToken string, transport Transport, traceID string) (AdminSession, error) {
	return s.RefreshAdminSessionWithClient(ctx, RefreshCommand{RefreshToken: refreshToken, Transport: transport, TraceID: traceID})
}

func (s *Service) RefreshAdminSessionWithClient(ctx context.Context, command RefreshCommand) (AdminSession, error) {
	now := s.now().UTC()
	transport := command.Transport
	if transport != TransportCookie && transport != TransportBearer {
		return AdminSession{}, ErrSessionRevoked
	}
	var binding *ControlledClientBinding
	if transport == TransportBearer {
		if !s.policy.AllowBearer {
			return AdminSession{}, ErrSessionRevoked
		}
		controlledClient, err := s.resolveControlledClient(ctx, command.ControlledClient, now)
		if err != nil {
			if errors.Is(err, ErrBearerNotAllowed) {
				return AdminSession{}, ErrSessionRevoked
			}
			return AdminSession{}, err
		}
		binding = &ControlledClientBinding{ClientID: controlledClient.ClientID, CredentialID: controlledClient.CredentialID}
	}
	refreshDigest := s.hasher.Digest(command.RefreshToken)
	inspection, err := s.repository.InspectRefresh(ctx, refreshDigest, transport, binding, now)
	if err != nil {
		return AdminSession{}, err
	}
	eventSummary := map[string]any{"transport": transport}
	if binding != nil {
		eventSummary["controlled_client_ref"] = s.hasher.DigestHex(binding.ClientID)
	}
	refreshEvent, err := s.securityEvent("admin.auth.session_refreshed", "system", s.hasher.DigestHex(command.RefreshToken), "success", "", command.TraceID, "normal", now, eventSummary)
	if err != nil {
		return AdminSession{}, err
	}
	if inspection.Replayed {
		_, replayErr := s.repository.RotateRefresh(ctx, refreshDigest, transport, binding, Rotation{Now: now, OutboxEvent: refreshEvent})
		if replayErr != nil && !errors.Is(replayErr, ErrRefreshReplayed) {
			return AdminSession{}, replayErr
		}
		return AdminSession{}, ErrRefreshReplayed
	}
	snapshot, err := s.access.ResolveAdminAccessSnapshot(ctx, inspection.Session.UserID, inspection.Session.SessionID)
	if err != nil {
		if !errors.Is(err, accesscontrol.ErrNoActiveScope) {
			return AdminSession{}, err
		}
		revokedEvent, eventErr := s.securityEvent("admin.auth.session_revoked", inspection.Session.UserID, s.hasher.DigestHex(inspection.Session.SessionID), "success", "no_active_admin_scope", command.TraceID, "high", now, nil)
		if eventErr != nil {
			return AdminSession{}, eventErr
		}
		expectation := TokenExpectation{Transport: transport, TokenType: "refresh"}
		if revokeErr := s.repository.RevokeByToken(ctx, refreshDigest, expectation, now, RevokeReasonNoAdminScope, revokedEvent); revokeErr != nil {
			return AdminSession{}, revokeErr
		}
		return AdminSession{}, ErrSessionRevoked
	}
	accessID, err := s.secrets.ID("atok_")
	if err != nil {
		return AdminSession{}, err
	}
	refreshID, err := s.secrets.ID("rtok_")
	if err != nil {
		return AdminSession{}, err
	}
	accessToken, err := s.secrets.Token("adm_at_")
	if err != nil {
		return AdminSession{}, err
	}
	newRefresh, err := s.secrets.Token("adm_rt_")
	if err != nil {
		return AdminSession{}, err
	}
	csrfToken := ""
	var csrfDigest []byte
	if transport == TransportCookie {
		csrfToken = s.csrfTokenFor(inspection.Session.SessionID, inspection.Session.SessionVersion+1)
		csrfDigest = s.hasher.Digest(csrfToken)
	}
	accessExpires, refreshExpires := now.Add(s.policy.AccessTTL), now.Add(s.policy.RefreshTTL)
	rotation := Rotation{AccessToken: TokenRecord{TokenID: accessID, TokenType: "access", Digest: s.hasher.Digest(accessToken), ExpiresAt: accessExpires}, RefreshToken: TokenRecord{TokenID: refreshID, TokenType: "refresh", Digest: s.hasher.Digest(newRefresh), ExpiresAt: refreshExpires}, CSRFDigest: csrfDigest, AccessExpires: accessExpires, RefreshExpires: refreshExpires, Now: now, OutboxEvent: refreshEvent}
	stored, err := s.repository.RotateRefresh(ctx, refreshDigest, transport, binding, rotation)
	if err != nil {
		return AdminSession{}, err
	}
	if stored.SessionID != inspection.Session.SessionID || stored.UserID != inspection.Session.UserID {
		return AdminSession{}, fmt.Errorf("refresh session identity changed during rotation")
	}
	return buildAdminSession(stored, snapshot, csrfToken, accessToken, newRefresh), nil
}

func (s *Service) LogoutAdmin(ctx context.Context, command LogoutCommand) error {
	now := s.now().UTC()
	transport := command.Transport
	if transport == "" {
		transport = TransportCookie
	}
	if transport == TransportBearer {
		if command.AccessToken == "" {
			return ErrSessionExpired
		}
		digest := s.hasher.Digest(command.AccessToken)
		event, err := s.securityEvent("admin.auth.session_revoked", "system", s.hasher.DigestHex(command.AccessToken), "success", "logout", command.TraceID, "normal", now, nil)
		if err != nil {
			return err
		}
		expectation := TokenExpectation{Transport: TransportBearer, TokenType: "access"}
		return s.repository.RevokeByToken(ctx, digest, expectation, now, RevokeReasonLogout, event)
	}
	if transport != TransportCookie {
		return ErrSessionRevoked
	}
	if command.AccessToken == "" && command.RefreshToken == "" {
		return nil
	}

	targetMaterial := command.AccessToken
	if targetMaterial == "" {
		targetMaterial = command.RefreshToken
	}
	event, err := s.securityEvent("admin.auth.session_revoked", "system", s.hasher.DigestHex(targetMaterial), "success", "logout", command.TraceID, "normal", now, nil)
	if err != nil {
		return err
	}
	proof := CookieLogoutProof{}
	if command.AccessToken != "" {
		proof.AccessDigest = s.hasher.Digest(command.AccessToken)
	}
	if command.RefreshToken != "" {
		proof.RefreshDigest = s.hasher.Digest(command.RefreshToken)
	}
	if command.CSRFToken != "" {
		proof.CSRFDigest = s.hasher.Digest(command.CSRFToken)
	}
	return s.repository.RevokeCookieSession(ctx, proof, now, event)
}

func (s *Service) BootstrapAdminIdentity(ctx context.Context, identifier, displayName string, password []byte) (string, error) {
	if len(password) < 12 {
		return "", fmt.Errorf("bootstrap password must contain at least 12 characters")
	}
	hash, err := s.passwords.Hash(password)
	if err != nil {
		return "", err
	}
	userID, err := s.secrets.ID("usr_")
	if err != nil {
		return "", err
	}
	credentialID, err := s.secrets.ID("cred_")
	if err != nil {
		return "", err
	}
	return s.repository.BootstrapIdentity(ctx, BootstrapUser{UserID: userID, CredentialID: credentialID, IdentifierDigest: s.hasher.Digest(normalizeIdentifier(identifier)), IdentifierMasked: maskIdentifier(identifier), DisplayName: strings.TrimSpace(displayName), PasswordHash: hash, Now: s.now().UTC()})
}

func (s *Service) failedLogin(ctx context.Context, identifierDigest, sourceDigest []byte, traceID, reason string, now time.Time) error {
	event, err := s.securityEvent("admin.auth.login_failed", "anonymous_admin", hex.EncodeToString(identifierDigest), "failure", reason, traceID, "normal", now, nil)
	if err != nil {
		return err
	}
	state, err := s.repository.RecordLoginFailure(ctx, LoginFailure{IdentifierDigest: identifierDigest, SourceDigest: sourceDigest, Now: now, Window: s.policy.LoginWindow, MaximumAttempts: s.policy.LoginMaximumAttempts, BlockDuration: s.policy.LoginBlockDuration, OutboxEvent: event})
	if err != nil {
		return err
	}
	if state.BlockedUntil != nil && state.BlockedUntil.After(now) {
		return newRateLimitError(state.BlockedUntil.Sub(now))
	}
	return ErrInvalidCredentials
}

func (s *Service) securityEvent(action, actorID, targetID, result, reason, traceID, risk string, now time.Time, summary map[string]any) (OutboxEvent, error) {
	id, err := s.secrets.ID("evt_")
	if err != nil {
		return OutboxEvent{}, err
	}
	auditID, err := s.secrets.ID("aud_")
	if err != nil {
		return OutboxEvent{}, err
	}
	return OutboxEvent{EventID: id, Topic: "audit.append", Now: now, Payload: SecurityEvent{AuditID: auditID, OccurredAt: now, ActorID: actorID, ScopeType: "platform", Action: action, TargetType: "admin_session", TargetID: targetID, Result: result, ReasonCode: reason, TraceID: traceID, RiskLevel: risk, RedactedSummary: summary}}, nil
}

func generateSessionSecrets(generator securevalue.Generator, transport Transport) (string, string, string, string, string, string, string, error) {
	sessionID, err := generator.ID("ases_")
	if err != nil {
		return "", "", "", "", "", "", "", err
	}
	familyID, err := generator.ID("afam_")
	if err != nil {
		return "", "", "", "", "", "", "", err
	}
	accessID, err := generator.ID("atok_")
	if err != nil {
		return "", "", "", "", "", "", "", err
	}
	refreshID, err := generator.ID("rtok_")
	if err != nil {
		return "", "", "", "", "", "", "", err
	}
	accessToken, err := generator.Token("adm_at_")
	if err != nil {
		return "", "", "", "", "", "", "", err
	}
	refreshToken, err := generator.Token("adm_rt_")
	if err != nil {
		return "", "", "", "", "", "", "", err
	}
	csrf := ""
	if transport == TransportCookie {
		csrf, err = generator.Token("csrf_")
		if err != nil {
			return "", "", "", "", "", "", "", err
		}
	}
	return sessionID, familyID, accessID, refreshID, accessToken, refreshToken, csrf, nil
}

func buildAdminSession(stored StoredSession, snapshot accesscontrol.Snapshot, csrf, access, refresh string) AdminSession {
	result := AdminSession{SessionID: stored.SessionID, SessionVersion: stored.SessionVersion, Transport: stored.Transport, Admin: AdminIdentitySummary{AdminUserID: stored.UserID, DisplayName: stored.DisplayName, AccountStatus: stored.AccountStatus, AuthTime: stored.AuthTime, AuthenticationMethod: stored.AuthenticationMethod}, Authorization: snapshot, AccessExpiresAt: stored.AccessExpiresAt, RefreshExpiresAt: stored.RefreshExpiresAt}
	if stored.ControlledClientID != "" {
		clientID := stored.ControlledClientID
		result.ControlledClientID = &clientID
	}
	if stored.Transport == TransportCookie {
		result.CSRFToken = &csrf
		if access != "" || refresh != "" {
			result.CookieTokens = &IssuedTokens{AccessToken: access, RefreshToken: refresh, AccessExpiresAt: stored.AccessExpiresAt, RefreshExpiresAt: stored.RefreshExpiresAt}
		}
	} else if access != "" || refresh != "" {
		result.TokenPair = &IssuedTokens{AccessToken: access, RefreshToken: refresh, AccessExpiresAt: stored.AccessExpiresAt, RefreshExpiresAt: stored.RefreshExpiresAt}
	}
	return result
}

func normalizeIdentifier(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func maskIdentifier(value string) string {
	value = normalizeIdentifier(value)
	parts := strings.Split(value, "@")
	if len(parts) == 2 && len(parts[0]) > 0 {
		return string(parts[0][0]) + "***@" + parts[1]
	}
	if len(value) <= 2 {
		return "**"
	}
	return value[:1] + "***" + value[len(value)-1:]
}

func redactRisk(value map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"device_class", "risk_level", "client_version"} {
		if v, ok := value[key]; ok {
			result[key] = v
		}
	}
	return result
}
