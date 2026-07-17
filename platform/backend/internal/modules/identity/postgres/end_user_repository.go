package postgres

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"platform.local/capability-platform/backend/internal/modules/identity"
)

func (r *Repository) CreateEndUser(ctx context.Context, registration identity.EndUserRegistration) error {
	if err := registration.Validate(); err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user, identifier, credential, profile := registration.User, registration.Identifier, registration.Credential, registration.Profile
	if _, err = tx.Exec(ctx, `INSERT INTO identity.users(user_id,display_name,account_status,user_version,created_at,updated_at) VALUES($1,$2,$3,1,$4,$4)`, user.UserID, profile.DisplayName, user.AccountStatus, user.CreatedAt); err != nil {
		return mapEndUserConflict(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO identity.user_identifiers(identifier_id,user_id,identifier_type,normalization_version,normalized_digest,masked_value,verification_status,verified_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`, identifier.IdentifierID, user.UserID, identifier.Type, identifier.NormalizationVersion, identifier.NormalizedDigest, identifier.MaskedValue, identifier.VerificationStatus, identifier.VerifiedAt, identifier.CreatedAt); err != nil {
		return mapEndUserConflict(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO identity.user_credentials(credential_id,user_id,credential_type,identifier_digest,identifier_masked,password_hash,credential_status,password_algorithm,credential_version,password_changed_at,created_at,updated_at) VALUES($1,$2,'password',$3,$4,$5,'active',$6,1,$7,$7,$7)`, credential.CredentialID, user.UserID, identifier.NormalizedDigest, identifier.MaskedValue, credential.PasswordHash, credential.Algorithm, credential.ChangedAt); err != nil {
		return mapEndUserConflict(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO identity.user_profiles(user_id,profile_version,display_name,avatar_ref,locale,timezone,created_at,updated_at) VALUES($1,1,$2,$3,$4,$5,$6,$6)`, user.UserID, profile.DisplayName, profile.AvatarRef, profile.Locale, profile.Timezone, profile.CreatedAt); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, registration.OutboxEvent); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) FindEndUserPasswordCredential(ctx context.Context, kind identity.IdentifierType, digest []byte) (identity.EndUserPasswordCredential, error) {
	var result identity.EndUserPasswordCredential
	err := r.pool.QueryRow(ctx, `SELECT u.user_id,u.account_status,u.user_version,c.credential_id,c.password_hash,c.password_algorithm,c.credential_version FROM identity.user_identifiers i JOIN identity.users u ON u.user_id=i.user_id JOIN identity.user_credentials c ON c.user_id=u.user_id AND c.credential_type='password' AND c.credential_status='active' WHERE i.identifier_type=$1 AND i.normalized_digest=$2`, kind, digest).Scan(&result.UserID, &result.AccountStatus, &result.UserVersion, &result.CredentialID, &result.PasswordHash, &result.Algorithm, &result.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserPasswordCredential{}, identity.ErrNotFound
	}
	return result, err
}

func (r *Repository) UpdateEndUserProfile(ctx context.Context, profile identity.EndUserProfile, expectedVersion int64, event identity.OutboxEvent) (identity.EndUserProfile, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.EndUserProfile{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var updated identity.EndUserProfile
	err = tx.QueryRow(ctx, `UPDATE identity.user_profiles SET profile_version=profile_version+1,display_name=$3,avatar_ref=$4,locale=$5,timezone=$6,updated_at=$7 WHERE user_id=$1 AND profile_version=$2 RETURNING user_id,profile_version,display_name,avatar_ref,locale,timezone,created_at,updated_at`, profile.UserID, expectedVersion, profile.DisplayName, profile.AvatarRef, profile.Locale, profile.Timezone, profile.UpdatedAt).Scan(&updated.UserID, &updated.Version, &updated.DisplayName, &updated.AvatarRef, &updated.Locale, &updated.Timezone, &updated.CreatedAt, &updated.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserProfile{}, identity.ErrEndUserVersionConflict
	}
	if err != nil {
		return identity.EndUserProfile{}, err
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return identity.EndUserProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.EndUserProfile{}, err
	}
	return updated, nil
}

func (r *Repository) ReplaceEndUserPassword(ctx context.Context, userID string, passwordHash []byte, algorithm string, expectedVersion int64, now time.Time, revokeSessions bool, event identity.OutboxEvent) error {
	if err := identity.ValidateAdaptivePasswordHash(algorithm, passwordHash); err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `UPDATE identity.user_credentials SET password_hash=$3,password_algorithm=$4,credential_version=credential_version+1,password_changed_at=$5,updated_at=$5 WHERE user_id=$1 AND credential_type='password' AND credential_status='active' AND credential_version=$2`, userID, expectedVersion, passwordHash, algorithm, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrEndUserVersionConflict
	}
	if revokeSessions {
		if err := revokeEndUserSessions(ctx, tx, `user_id=$1`, []any{userID}, "credential_changed", now); err != nil {
			return err
		}
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) CreateEndUserSession(ctx context.Context, value identity.NewEndUserSession) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	session := value.Session
	_, err = tx.Exec(ctx, `INSERT INTO identity.end_user_sessions(session_id,user_id,product_id,application_id,tenant_id,token_family_id,authentication_method,session_version,auth_time,created_at,last_seen_at,access_expires_at,refresh_expires_at,absolute_expires_at,risk_summary_digest) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$9,$9,$10,$11,$12,$13)`, session.SessionID, session.UserID, session.ProductID, session.ApplicationID, session.TenantID, session.TokenFamilyID, session.AuthenticationMethod, session.AuthTime, session.CreatedAt, session.AccessExpiresAt, session.RefreshExpiresAt, session.AbsoluteExpiresAt, endUserNullableBytes(session.RiskSummaryDigest))
	if err != nil {
		return err
	}
	if err := insertEndUserToken(ctx, tx, session.SessionID, session.TokenFamilyID, value.AccessToken); err != nil {
		return err
	}
	if err := insertEndUserToken(ctx, tx, session.SessionID, session.TokenFamilyID, value.RefreshToken); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, value.OutboxEvent); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) FindEndUserByAccessDigest(ctx context.Context, digest []byte, scope identity.EndUserSessionScope, now time.Time) (identity.EndUserSession, error) {
	session, token, err := scanEndUserToken(r.pool.QueryRow(ctx, endUserTokenQuery+` WHERE t.token_digest=$1`, digest))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserSession{}, identity.ErrEndUserSessionExpired
	}
	if err != nil {
		return identity.EndUserSession{}, err
	}
	if !scope.Matches(session) {
		return identity.EndUserSession{}, identity.ErrEndUserScopeMismatch
	}
	if token.tokenType != "access" || session.RevokedAt != nil || token.revokedAt != nil {
		return session, identity.ErrEndUserSessionRevoked
	}
	if !session.AccessExpiresAt.After(now) || !token.expiresAt.After(now) {
		return session, identity.ErrEndUserSessionExpired
	}
	return session, nil
}

func (r *Repository) RotateEndUserRefresh(ctx context.Context, digest []byte, scope identity.EndUserSessionScope, rotation identity.EndUserRefreshRotation) (identity.EndUserSession, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.EndUserSession{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	session, token, err := scanEndUserToken(tx.QueryRow(ctx, endUserTokenQuery+` WHERE t.token_digest=$1 FOR UPDATE OF t,s`, digest))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserSession{}, identity.ErrEndUserSessionExpired
	}
	if err != nil {
		return identity.EndUserSession{}, err
	}
	if !scope.Matches(session) {
		return identity.EndUserSession{}, identity.ErrEndUserScopeMismatch
	}
	if token.tokenType != "refresh" {
		return identity.EndUserSession{}, identity.ErrEndUserSessionRevoked
	}
	if token.consumedAt != nil {
		if err := revokeEndUserSessions(ctx, tx, `token_family_id=$1`, []any{session.TokenFamilyID}, "refresh_replayed", rotation.Now); err != nil {
			return identity.EndUserSession{}, err
		}
		rotation.OutboxEvent.Topic = "identity.refresh_replayed.v1"
		rotation.OutboxEvent.Payload.ActorID = session.UserID
		rotation.OutboxEvent.Payload.Action = "identity.refresh_replayed"
		rotation.OutboxEvent.Payload.Result = "failure"
		rotation.OutboxEvent.Payload.ReasonCode = "refresh_replayed"
		rotation.OutboxEvent.Payload.RiskLevel = "high"
		if err := insertOutboxStrict(ctx, tx, rotation.OutboxEvent); err != nil {
			return identity.EndUserSession{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return identity.EndUserSession{}, err
		}
		return identity.EndUserSession{}, identity.ErrEndUserRefreshReplayed
	}
	if session.RevokedAt != nil || token.revokedAt != nil {
		return identity.EndUserSession{}, identity.ErrEndUserSessionRevoked
	}
	if !token.expiresAt.After(rotation.Now) || !session.RefreshExpiresAt.After(rotation.Now) || !session.AbsoluteExpiresAt.After(rotation.Now) {
		return identity.EndUserSession{}, identity.ErrEndUserSessionExpired
	}
	nextGeneration := token.generation + 1
	if rotation.AccessToken.Generation != nextGeneration || rotation.RefreshToken.Generation != nextGeneration {
		return identity.EndUserSession{}, errors.New("end-user token generation is not monotonic")
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.end_user_session_tokens SET consumed_at=$2,replaced_by_token_id=$3 WHERE token_id=$1`, token.tokenID, rotation.Now, rotation.RefreshToken.TokenID); err != nil {
		return identity.EndUserSession{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.end_user_session_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE token_family_id=$1 AND token_type='access'`, session.TokenFamilyID, rotation.Now); err != nil {
		return identity.EndUserSession{}, err
	}
	if err := insertEndUserToken(ctx, tx, session.SessionID, session.TokenFamilyID, rotation.AccessToken); err != nil {
		return identity.EndUserSession{}, err
	}
	if err := insertEndUserToken(ctx, tx, session.SessionID, session.TokenFamilyID, rotation.RefreshToken); err != nil {
		return identity.EndUserSession{}, err
	}
	err = tx.QueryRow(ctx, `UPDATE identity.end_user_sessions SET session_version=session_version+1,last_seen_at=$2,access_expires_at=$3,refresh_expires_at=$4 WHERE session_id=$1 RETURNING session_version`, session.SessionID, rotation.Now, rotation.AccessExpiresAt, rotation.RefreshExpiresAt).Scan(&session.Version)
	if err != nil {
		return identity.EndUserSession{}, err
	}
	session.LastSeenAt, session.AccessExpiresAt, session.RefreshExpiresAt = rotation.Now, rotation.AccessExpiresAt, rotation.RefreshExpiresAt
	rotation.OutboxEvent.Payload.ActorID = session.UserID
	if err := insertOutboxStrict(ctx, tx, rotation.OutboxEvent); err != nil {
		return identity.EndUserSession{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.EndUserSession{}, err
	}
	return session, nil
}

func (r *Repository) RevokeEndUserSession(ctx context.Context, userID, sessionID string, scope identity.EndUserSessionScope, reason string, now time.Time, event identity.OutboxEvent) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var family string
	err = tx.QueryRow(ctx, `SELECT token_family_id FROM identity.end_user_sessions WHERE session_id=$1 AND user_id=$2 AND product_id=$3 AND application_id=$4 AND tenant_id IS NOT DISTINCT FROM $5 FOR UPDATE`, sessionID, userID, scope.ProductID, scope.ApplicationID, scope.TenantID).Scan(&family)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrEndUserScopeMismatch
	}
	if err != nil {
		return err
	}
	if err := revokeEndUserSessions(ctx, tx, `token_family_id=$1`, []any{family}, reason, now); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) RevokeScopedSessions(ctx context.Context, command identity.ScopedSessionRevocation) error {
	if command.ProductID == "" || command.UserID == "" || command.AccessVersion < 1 || len(command.EventIDDigest) != 32 || len(command.RequestDigest) != 32 || len(command.ActorDigest) != 32 || command.Cutoff.IsZero() || command.OutboxEvent.Now.IsZero() {
		return errors.New("invalid scoped session revocation")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	scopeID := fmt.Sprintf("p%d:%s|u%d:%s", len(command.ProductID), command.ProductID, len(command.UserID), command.UserID)
	if command.TenantID != nil {
		scopeID += fmt.Sprintf("|t%d:%s", len(*command.TenantID), *command.TenantID)
	} else {
		scopeID += "|t-"
	}
	version := strconv.FormatInt(command.AccessVersion, 10)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "revoke_scoped_sessions|"+scopeID); err != nil {
		return err
	}
	var storedRequest []byte
	var storedVersion, state string
	err = tx.QueryRow(ctx, `SELECT request_digest,resource_id,state FROM identity.end_user_idempotency_records WHERE operation='revoke_scoped_sessions' AND scope_id=$1 AND actor_digest=$2 AND key_digest=$3`, scopeID, command.ActorDigest, command.EventIDDigest).Scan(&storedRequest, &storedVersion, &state)
	if err == nil {
		if hmac.Equal(storedRequest, command.RequestDigest) && storedVersion == version && state == "completed" {
			return nil
		}
		return identity.ErrEndUserVersionConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var latestVersion int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(resource_id::bigint),0) FROM identity.end_user_idempotency_records WHERE operation='revoke_scoped_sessions' AND scope_id=$1 AND state='completed'`, scopeID).Scan(&latestVersion); err != nil {
		return err
	}
	if latestVersion > command.AccessVersion {
		return nil
	}
	if latestVersion == command.AccessVersion {
		return identity.ErrEndUserVersionConflict
	}
	result, err := tx.Exec(ctx, `INSERT INTO identity.end_user_idempotency_records(operation,scope_id,actor_digest,key_digest,request_digest,resource_id,state,created_at,updated_at) VALUES('revoke_scoped_sessions',$1,$2,$3,$4,$5,'pending',$6,$6)`, scopeID, command.ActorDigest, command.EventIDDigest, command.RequestDigest, version, command.OutboxEvent.Now)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT session_id FROM identity.end_user_sessions WHERE product_id=$1 AND user_id=$2 AND ($3::text IS NULL OR tenant_id=$3) AND auth_time <= $4 FOR UPDATE`, command.ProductID, command.UserID, command.TenantID, command.Cutoff)
	if err != nil {
		return err
	}
	var sessionIDs []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			rows.Close()
			return err
		}
		sessionIDs = append(sessionIDs, sessionID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(sessionIDs) != 0 {
		if _, err := tx.Exec(ctx, `UPDATE identity.end_user_sessions SET revoked_at=COALESCE(revoked_at,$2),revoke_reason=COALESCE(revoke_reason,'product_user_access_suspended'),last_seen_at=$2 WHERE session_id=ANY($1)`, sessionIDs, command.OutboxEvent.Now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE identity.end_user_session_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE session_id=ANY($1)`, sessionIDs, command.OutboxEvent.Now); err != nil {
			return err
		}
	}
	if err := insertOutboxStrict(ctx, tx, command.OutboxEvent); err != nil {
		return err
	}
	result, err = tx.Exec(ctx, `UPDATE identity.end_user_idempotency_records SET state='completed',updated_at=$6 WHERE operation='revoke_scoped_sessions' AND scope_id=$1 AND actor_digest=$2 AND key_digest=$3 AND request_digest=$4 AND resource_id=$5 AND state='pending'`, scopeID, command.ActorDigest, command.EventIDDigest, command.RequestDigest, version, command.OutboxEvent.Now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("complete scoped session revocation idempotency record")
	}
	return tx.Commit(ctx)
}

func (r *Repository) CreateRecoveryChallenge(ctx context.Context, challenge identity.RecoveryChallenge) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO identity.recovery_challenges(challenge_id,continuation_digest,identifier_type,identifier_digest,matched_user_id,delivery_target_masked,proof_digest,max_attempts,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, challenge.ChallengeID, challenge.ContinuationDigest, challenge.IdentifierType, challenge.IdentifierDigest, challenge.MatchedUserID, challenge.DeliveryTargetMasked, challenge.ProofDigest, challenge.MaxAttempts, challenge.CreatedAt, challenge.ExpiresAt); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, challenge.OutboxEvent); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ConsumeRecoveryChallenge(ctx context.Context, continuationDigest, proofDigest []byte, now time.Time, event identity.OutboxEvent) (identity.RecoveryConsumption, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.RecoveryConsumption{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var result identity.RecoveryConsumption
	var storedProof []byte
	var attempts, maximum int
	var expires time.Time
	var consumed *time.Time
	err = tx.QueryRow(ctx, `SELECT challenge_id,matched_user_id,proof_digest,attempt_count,max_attempts,expires_at,consumed_at FROM identity.recovery_challenges WHERE continuation_digest=$1 FOR UPDATE`, continuationDigest).Scan(&result.ChallengeID, &result.MatchedUserID, &storedProof, &attempts, &maximum, &expires, &consumed)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.RecoveryConsumption{}, identity.ErrRecoveryProofInvalid
	}
	if err != nil {
		return identity.RecoveryConsumption{}, err
	}
	if consumed != nil {
		return identity.RecoveryConsumption{}, identity.ErrRecoveryProofReplayed
	}
	if !expires.After(now) {
		return identity.RecoveryConsumption{}, identity.ErrRecoveryChallengeExpired
	}
	if attempts >= maximum || !hmac.Equal(storedProof, proofDigest) {
		if attempts < maximum {
			_, err = tx.Exec(ctx, `UPDATE identity.recovery_challenges SET attempt_count=attempt_count+1 WHERE challenge_id=$1`, result.ChallengeID)
			if err != nil {
				return identity.RecoveryConsumption{}, err
			}
			if err := insertOutboxStrict(ctx, tx, event); err != nil {
				return identity.RecoveryConsumption{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return identity.RecoveryConsumption{}, err
			}
		}
		return identity.RecoveryConsumption{}, identity.ErrRecoveryProofInvalid
	}
	err = tx.QueryRow(ctx, `UPDATE identity.recovery_challenges SET attempt_count=attempt_count+1,consumed_at=$2 WHERE challenge_id=$1 RETURNING consumed_at`, result.ChallengeID, now).Scan(&result.ConsumedAt)
	if err != nil {
		return identity.RecoveryConsumption{}, err
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return identity.RecoveryConsumption{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.RecoveryConsumption{}, err
	}
	return result, nil
}

func (r *Repository) LinkExternalIdentity(ctx context.Context, value identity.ExternalIdentity) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO identity.external_identities(external_identity_id,user_id,provider,provider_application_id,subject_digest,subject_masked,union_subject_digest,status,identity_version,linked_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,'active',1,$8,$8)`, value.ExternalIdentityID, value.UserID, value.Provider, value.ProviderApplicationID, value.SubjectDigest, value.SubjectMasked, endUserNullableBytes(value.UnionSubjectDigest), value.LinkedAt)
	if isUniqueViolation(err) {
		return identity.ErrExternalIdentityConflict
	}
	if err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, value.OutboxEvent); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) FindExternalIdentity(ctx context.Context, provider, applicationID string, subjectDigest []byte) (identity.ExternalIdentity, error) {
	var result identity.ExternalIdentity
	err := r.pool.QueryRow(ctx, `SELECT external_identity_id,user_id,provider,provider_application_id,subject_digest,subject_masked,union_subject_digest,status,identity_version,linked_at,updated_at FROM identity.external_identities WHERE provider=$1 AND provider_application_id=$2 AND subject_digest=$3 AND status='active'`, provider, applicationID, subjectDigest).Scan(&result.ExternalIdentityID, &result.UserID, &result.Provider, &result.ProviderApplicationID, &result.SubjectDigest, &result.SubjectMasked, &result.UnionSubjectDigest, &result.Status, &result.Version, &result.LinkedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ExternalIdentity{}, identity.ErrNotFound
	}
	return result, err
}

type endUserTokenState struct {
	tokenID    string
	tokenType  string
	generation int
	expiresAt  time.Time
	consumedAt *time.Time
	revokedAt  *time.Time
}

const endUserTokenQuery = `SELECT s.session_id,s.user_id,s.product_id,s.application_id,s.tenant_id,s.token_family_id,s.authentication_method,s.session_version,s.auth_time,s.created_at,s.last_seen_at,s.access_expires_at,s.refresh_expires_at,s.absolute_expires_at,s.risk_summary_digest,s.revoked_at,s.revoke_reason,t.token_id,t.token_type,t.generation,t.expires_at,t.consumed_at,t.revoked_at FROM identity.end_user_session_tokens t JOIN identity.end_user_sessions s ON s.session_id=t.session_id`

func scanEndUserToken(row rowScanner) (identity.EndUserSession, endUserTokenState, error) {
	var session identity.EndUserSession
	var token endUserTokenState
	err := row.Scan(&session.SessionID, &session.UserID, &session.ProductID, &session.ApplicationID, &session.TenantID, &session.TokenFamilyID, &session.AuthenticationMethod, &session.Version, &session.AuthTime, &session.CreatedAt, &session.LastSeenAt, &session.AccessExpiresAt, &session.RefreshExpiresAt, &session.AbsoluteExpiresAt, &session.RiskSummaryDigest, &session.RevokedAt, &session.RevokeReason, &token.tokenID, &token.tokenType, &token.generation, &token.expiresAt, &token.consumedAt, &token.revokedAt)
	return session, token, err
}

func insertEndUserToken(ctx context.Context, tx pgx.Tx, sessionID, familyID string, token identity.EndUserSessionToken) error {
	_, err := tx.Exec(ctx, `INSERT INTO identity.end_user_session_tokens(token_id,session_id,token_family_id,token_type,generation,token_digest,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, token.TokenID, sessionID, familyID, token.TokenType, token.Generation, token.Digest, token.CreatedAt, token.ExpiresAt)
	return err
}

func revokeEndUserSessions(ctx context.Context, tx pgx.Tx, predicate string, values []any, reason string, now time.Time) error {
	arguments := append(values, now, reason)
	if _, err := tx.Exec(ctx, `UPDATE identity.end_user_sessions SET revoked_at=COALESCE(revoked_at,$2),revoke_reason=COALESCE(revoke_reason,$3),last_seen_at=$2 WHERE `+predicate, arguments...); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE identity.end_user_session_tokens t SET revoked_at=COALESCE(t.revoked_at,$2) FROM identity.end_user_sessions s WHERE t.session_id=s.session_id AND `+"s."+predicate, append(values, now)...)
	return err
}

func mapEndUserConflict(err error) error {
	if isUniqueViolation(err) {
		return identity.ErrEndUserIdentifierConflict
	}
	return err
}

func isUniqueViolation(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23505"
}

func endUserNullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
