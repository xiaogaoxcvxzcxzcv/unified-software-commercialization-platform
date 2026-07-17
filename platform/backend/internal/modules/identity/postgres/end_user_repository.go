package postgres

import (
	"context"
	"crypto/hmac"
	"encoding/json"
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
	if err := insertEndUserRegistration(ctx, tx, registration); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, registration.OutboxEvent); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) CreateEndUserWithSession(ctx context.Context, registration identity.EndUserRegistration, session identity.NewEndUserSession) error {
	if err := registration.Validate(); err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertEndUserRegistration(ctx, tx, registration); err != nil {
		return err
	}
	if err := insertNewEndUserSession(ctx, tx, session); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, registration.OutboxEvent); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, session.OutboxEvent); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) CreateEndUserWithSessionIdempotent(ctx context.Context, registration identity.EndUserRegistration, session identity.NewEndUserSession, record identity.EndUserIdempotency) (identity.EndUserRegistrationResponse, bool, error) {
	if err := registration.Validate(); err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	if record.Operation != "register" || record.ScopeID == "" || len(record.ActorDigest) != 32 || len(record.KeyDigest) != 32 || len(record.RequestDigest) != 32 || record.ResourceID == "" || record.Now.IsZero() {
		return identity.EndUserRegistrationResponse{}, false, errors.New("invalid registration idempotency record")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockEndUserIdempotency(ctx, tx, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest); err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	var storedRequest []byte
	var resourceID *string
	var state string
	var responseDocument []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,resource_id,state,response_document FROM identity.end_user_idempotency_records WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest).Scan(&storedRequest, &resourceID, &state, &responseDocument)
	if err == nil {
		if !hmac.Equal(storedRequest, record.RequestDigest) {
			return identity.EndUserRegistrationResponse{}, false, identity.ErrEndUserVersionConflict
		}
		if state == "failed" {
			return identity.EndUserRegistrationResponse{}, false, decodeEndUserIdempotencyFailure(responseDocument)
		}
		if state != "completed" {
			return identity.EndUserRegistrationResponse{}, false, identity.ErrEndUserVersionConflict
		}
		var persisted identity.EndUserRegistrationResponse
		if err := json.Unmarshal(responseDocument, &persisted); err != nil {
			return identity.EndUserRegistrationResponse{}, false, fmt.Errorf("decode registration idempotency response: %w", err)
		}
		return persisted, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity.end_user_idempotency_records(operation,scope_id,actor_digest,key_digest,request_digest,resource_id,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'pending',$7,$7)`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest, record.RequestDigest, record.ResourceID, record.Now); err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	if err := insertEndUserRegistration(ctx, tx, registration); err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	if err := insertNewEndUserSession(ctx, tx, session); err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	if err := insertOutboxStrict(ctx, tx, registration.OutboxEvent); err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	if err := insertOutboxStrict(ctx, tx, session.OutboxEvent); err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	persisted := identity.EndUserRegistrationResponse{Session: identity.NewRegistrationSessionSnapshot(session.Session), Profile: registration.Profile}
	responseDocument, err = json.Marshal(persisted)
	if err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	if err := completeEndUserIdempotencyWithResponse(ctx, tx, record, record.ResourceID, responseDocument); err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	return persisted, false, nil
}

func (r *Repository) RecoverEndUserRegistration(ctx context.Context, record identity.EndUserIdempotency) (identity.EndUserRegistrationResponse, bool, error) {
	if record.Operation != "register" || record.ScopeID == "" || len(record.ActorDigest) != 32 || len(record.KeyDigest) != 32 || len(record.RequestDigest) != 32 {
		return identity.EndUserRegistrationResponse{}, false, errors.New("invalid registration idempotency lookup")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockEndUserIdempotency(ctx, tx, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest); err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	var storedRequest []byte
	var resourceID *string
	var state string
	var responseDocument []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,resource_id,state,response_document FROM identity.end_user_idempotency_records WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest).Scan(&storedRequest, &resourceID, &state, &responseDocument)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserRegistrationResponse{}, false, nil
	}
	if err != nil {
		return identity.EndUserRegistrationResponse{}, false, err
	}
	if !hmac.Equal(storedRequest, record.RequestDigest) {
		return identity.EndUserRegistrationResponse{}, false, identity.ErrEndUserVersionConflict
	}
	if state == "failed" {
		return identity.EndUserRegistrationResponse{}, false, decodeEndUserIdempotencyFailure(responseDocument)
	}
	if state != "completed" {
		return identity.EndUserRegistrationResponse{}, false, identity.ErrEndUserVersionConflict
	}
	var persisted identity.EndUserRegistrationResponse
	if err := json.Unmarshal(responseDocument, &persisted); err != nil {
		return identity.EndUserRegistrationResponse{}, false, fmt.Errorf("decode registration idempotency response: %w", err)
	}
	return persisted, true, nil
}

func (r *Repository) RecoverEndUserIdempotency(ctx context.Context, record identity.EndUserIdempotency) (bool, error) {
	if record.Operation == "" || record.ScopeID == "" || len(record.ActorDigest) != 32 || len(record.KeyDigest) != 32 || len(record.RequestDigest) != 32 {
		return false, errors.New("invalid end-user idempotency lookup")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockEndUserIdempotency(ctx, tx, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest); err != nil {
		return false, err
	}
	var storedRequest []byte
	var state string
	var responseDocument []byte
	err = tx.QueryRow(ctx, `SELECT request_digest,state,response_document FROM identity.end_user_idempotency_records WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest).Scan(&storedRequest, &state, &responseDocument)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !hmac.Equal(storedRequest, record.RequestDigest) {
		return false, identity.ErrEndUserVersionConflict
	}
	switch state {
	case "completed":
		return true, nil
	case "failed":
		return false, decodeEndUserIdempotencyFailure(responseDocument)
	default:
		return false, identity.ErrEndUserVersionConflict
	}
}

func (r *Repository) RecoverEndUserPasswordChange(ctx context.Context, accessDigest, keyDigest, requestDigest []byte) (bool, error) {
	if len(accessDigest) != 32 || len(keyDigest) != 32 || len(requestDigest) != 32 {
		return false, errors.New("invalid password-change idempotency lookup")
	}
	var scopeID string
	var actorDigest, storedRequest, responseDocument []byte
	var state string
	err := r.pool.QueryRow(ctx, `SELECT i.scope_id,i.actor_digest FROM identity.end_user_session_tokens t JOIN identity.end_user_sessions s ON s.session_id=t.session_id JOIN identity.end_user_idempotency_records i ON i.operation='password_change' AND i.resource_id=s.session_id AND i.key_digest=$2 WHERE t.token_type='access' AND t.token_digest=$1`, accessDigest, keyDigest).Scan(&scopeID, &actorDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockEndUserIdempotency(ctx, tx, "password_change", scopeID, actorDigest, keyDigest); err != nil {
		return false, err
	}
	err = tx.QueryRow(ctx, `SELECT i.request_digest,i.state,i.response_document FROM identity.end_user_session_tokens t JOIN identity.end_user_sessions s ON s.session_id=t.session_id JOIN identity.end_user_idempotency_records i ON i.operation='password_change' AND i.resource_id=s.session_id AND i.scope_id=$3 AND i.actor_digest=$4 AND i.key_digest=$2 WHERE t.token_type='access' AND t.token_digest=$1`, accessDigest, keyDigest, scopeID, actorDigest).Scan(&storedRequest, &state, &responseDocument)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !hmac.Equal(storedRequest, requestDigest) {
		return false, identity.ErrEndUserVersionConflict
	}
	switch state {
	case "completed":
		return true, nil
	case "failed":
		return false, decodeEndUserIdempotencyFailure(responseDocument)
	default:
		return false, identity.ErrEndUserVersionConflict
	}
}

func (r *Repository) FailEndUserIdempotency(ctx context.Context, record identity.EndUserIdempotency, reason string) error {
	if record.Operation == "" || record.ScopeID == "" || len(record.ActorDigest) != 32 || len(record.KeyDigest) != 32 || len(record.RequestDigest) != 32 || record.Now.IsZero() || reason == "" {
		return errors.New("invalid failed end-user idempotency record")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	recovered, err := beginEndUserIdempotency(ctx, tx, record)
	if err != nil {
		return err
	}
	if recovered {
		return identity.ErrEndUserVersionConflict
	}
	if err := failEndUserIdempotency(ctx, tx, record, reason); err != nil {
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

func (r *Repository) FindEndUserPasswordCredentialByUser(ctx context.Context, userID string) (identity.EndUserPasswordCredential, error) {
	var result identity.EndUserPasswordCredential
	err := r.pool.QueryRow(ctx, `SELECT u.user_id,u.account_status,u.user_version,c.credential_id,c.password_hash,c.password_algorithm,c.credential_version FROM identity.users u JOIN identity.user_credentials c ON c.user_id=u.user_id AND c.credential_type='password' AND c.credential_status='active' WHERE u.user_id=$1`, userID).Scan(&result.UserID, &result.AccountStatus, &result.UserVersion, &result.CredentialID, &result.PasswordHash, &result.Algorithm, &result.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserPasswordCredential{}, identity.ErrNotFound
	}
	return result, err
}

func (r *Repository) FindEndUserRecoveryTarget(ctx context.Context, kind identity.IdentifierType, digest []byte) (identity.EndUserRecoveryTarget, error) {
	var result identity.EndUserRecoveryTarget
	err := r.pool.QueryRow(ctx, `SELECT user_id,masked_value FROM identity.user_identifiers WHERE identifier_type=$1 AND normalized_digest=$2`, kind, digest).Scan(&result.UserID, &result.MaskedValue)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserRecoveryTarget{}, identity.ErrNotFound
	}
	return result, err
}

func (r *Repository) EndUserLoginThrottle(ctx context.Context, scopeID string, identifierDigest, sourceDigest []byte, now time.Time) (identity.EndUserLoginThrottle, error) {
	var result identity.EndUserLoginThrottle
	err := r.pool.QueryRow(ctx, `SELECT failure_count,blocked_until FROM identity.end_user_login_failures WHERE scope_id=$1 AND identifier_digest=$2 AND source_digest=$3`, scopeID, identifierDigest, sourceDigest).Scan(&result.FailureCount, &result.BlockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserLoginThrottle{}, nil
	}
	return result, err
}

func (r *Repository) RecordEndUserLoginFailure(ctx context.Context, failure identity.EndUserLoginFailure) (identity.EndUserLoginThrottle, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.EndUserLoginThrottle{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	windowStart := failure.Now.Add(-failure.Window)
	var result identity.EndUserLoginThrottle
	err = tx.QueryRow(ctx, `INSERT INTO identity.end_user_login_failures(scope_id,identifier_digest,source_digest,failure_count,window_started_at,last_failed_at,blocked_until) VALUES($1,$2,$3,1,$4,$4,NULL) ON CONFLICT(scope_id,identifier_digest,source_digest) DO UPDATE SET failure_count=CASE WHEN identity.end_user_login_failures.window_started_at < $5 THEN 1 ELSE identity.end_user_login_failures.failure_count+1 END,window_started_at=CASE WHEN identity.end_user_login_failures.window_started_at < $5 THEN $4 ELSE identity.end_user_login_failures.window_started_at END,last_failed_at=$4,blocked_until=CASE WHEN (CASE WHEN identity.end_user_login_failures.window_started_at < $5 THEN 1 ELSE identity.end_user_login_failures.failure_count+1 END) >= $6 THEN $4+$7::interval ELSE identity.end_user_login_failures.blocked_until END RETURNING failure_count,blocked_until`, failure.ScopeID, failure.IdentifierDigest, failure.SourceDigest, failure.Now, windowStart, failure.MaximumAttempts, failure.BlockDuration.String()).Scan(&result.FailureCount, &result.BlockedUntil)
	if err != nil {
		return identity.EndUserLoginThrottle{}, err
	}
	if err := insertOutboxStrict(ctx, tx, failure.OutboxEvent); err != nil {
		return identity.EndUserLoginThrottle{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.EndUserLoginThrottle{}, err
	}
	return result, nil
}

func (r *Repository) ClearEndUserLoginFailures(ctx context.Context, scopeID string, identifierDigest []byte) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM identity.end_user_login_failures WHERE scope_id=$1 AND identifier_digest=$2`, scopeID, identifierDigest)
	return err
}

func (r *Repository) GetEndUserProfile(ctx context.Context, userID string) (identity.EndUserProfile, error) {
	var result identity.EndUserProfile
	err := r.pool.QueryRow(ctx, `SELECT user_id,profile_version,display_name,avatar_ref,locale,timezone,created_at,updated_at FROM identity.user_profiles WHERE user_id=$1`, userID).Scan(&result.UserID, &result.Version, &result.DisplayName, &result.AvatarRef, &result.Locale, &result.Timezone, &result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserProfile{}, identity.ErrNotFound
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

func (r *Repository) UpdateEndUserProfileIdempotent(ctx context.Context, profile identity.EndUserProfile, expectedVersion int64, event identity.OutboxEvent, record identity.EndUserIdempotency) (identity.EndUserProfile, bool, error) {
	return r.PatchEndUserProfileIdempotent(ctx, profile.UserID, identity.EndUserProfilePatch{
		DisplayName: identity.EndUserProfilePatchValue{Set: true, Value: &profile.DisplayName},
		AvatarRef:   identity.EndUserProfilePatchValue{Set: true, Value: profile.AvatarRef},
		Locale:      identity.EndUserProfilePatchValue{Set: true, Value: profile.Locale},
		Timezone:    identity.EndUserProfilePatchValue{Set: true, Value: profile.Timezone},
	}, expectedVersion, event, record)
}

func (r *Repository) PatchEndUserProfileIdempotent(ctx context.Context, userID string, patch identity.EndUserProfilePatch, expectedVersion int64, event identity.OutboxEvent, record identity.EndUserIdempotency) (identity.EndUserProfile, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.EndUserProfile{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	recovered, err := beginEndUserIdempotency(ctx, tx, record)
	if err != nil {
		return identity.EndUserProfile{}, false, err
	}
	if recovered {
		var stored identity.EndUserProfile
		var responseDocument []byte
		if err := tx.QueryRow(ctx, `SELECT response_document FROM identity.end_user_idempotency_records WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest).Scan(&responseDocument); err != nil {
			return identity.EndUserProfile{}, false, err
		}
		if err := json.Unmarshal(responseDocument, &stored); err != nil {
			return identity.EndUserProfile{}, false, fmt.Errorf("decode profile idempotency response: %w", err)
		}
		return stored, true, tx.Commit(ctx)
	}
	var current identity.EndUserProfile
	err = tx.QueryRow(ctx, `SELECT user_id,profile_version,display_name,avatar_ref,locale,timezone,created_at,updated_at FROM identity.user_profiles WHERE user_id=$1 AND profile_version=$2 FOR UPDATE`, userID, expectedVersion).Scan(&current.UserID, &current.Version, &current.DisplayName, &current.AvatarRef, &current.Locale, &current.Timezone, &current.CreatedAt, &current.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := failEndUserIdempotency(ctx, tx, record, "version_conflict"); err != nil {
			return identity.EndUserProfile{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return identity.EndUserProfile{}, false, err
		}
		return identity.EndUserProfile{}, false, identity.ErrEndUserVersionConflict
	}
	if err != nil {
		return identity.EndUserProfile{}, false, err
	}
	applyEndUserProfilePatch(&current, patch)
	var updated identity.EndUserProfile
	err = tx.QueryRow(ctx, `UPDATE identity.user_profiles SET profile_version=profile_version+1,display_name=$3,avatar_ref=$4,locale=$5,timezone=$6,updated_at=$7 WHERE user_id=$1 AND profile_version=$2 RETURNING user_id,profile_version,display_name,avatar_ref,locale,timezone,created_at,updated_at`, userID, expectedVersion, current.DisplayName, current.AvatarRef, current.Locale, current.Timezone, record.Now).Scan(&updated.UserID, &updated.Version, &updated.DisplayName, &updated.AvatarRef, &updated.Locale, &updated.Timezone, &updated.CreatedAt, &updated.UpdatedAt)
	if err != nil {
		return identity.EndUserProfile{}, false, err
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return identity.EndUserProfile{}, false, err
	}
	responseDocument, err := json.Marshal(updated)
	if err != nil {
		return identity.EndUserProfile{}, false, err
	}
	if err := completeEndUserIdempotencyWithResponse(ctx, tx, record, userID, responseDocument); err != nil {
		return identity.EndUserProfile{}, false, err
	}
	return updated, false, tx.Commit(ctx)
}

func applyEndUserProfilePatch(profile *identity.EndUserProfile, patch identity.EndUserProfilePatch) {
	if patch.DisplayName.Set && patch.DisplayName.Value != nil {
		profile.DisplayName = *patch.DisplayName.Value
	}
	if patch.AvatarRef.Set {
		profile.AvatarRef = patch.AvatarRef.Value
	}
	if patch.Locale.Set {
		profile.Locale = patch.Locale.Value
	}
	if patch.Timezone.Set {
		profile.Timezone = patch.Timezone.Value
	}
}

func (r *Repository) ReplaceEndUserPassword(ctx context.Context, userID, currentSessionID string, passwordHash []byte, algorithm string, expectedVersion int64, now time.Time, revokeSessions bool, event identity.OutboxEvent) error {
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
		if _, err := tx.Exec(ctx, `WITH target AS (UPDATE identity.end_user_sessions SET revoked_at=COALESCE(revoked_at,$3),revoke_reason=COALESCE(revoke_reason,'credential_changed'),last_seen_at=$3 WHERE user_id=$1 AND session_id<>$2 RETURNING session_id) UPDATE identity.end_user_session_tokens t SET revoked_at=COALESCE(t.revoked_at,$3) FROM target WHERE t.session_id=target.session_id`, userID, currentSessionID, now); err != nil {
			return err
		}
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ReplaceEndUserPasswordIdempotent(ctx context.Context, userID, currentSessionID string, passwordHash []byte, algorithm string, expectedVersion int64, now time.Time, revokeSessions bool, event identity.OutboxEvent, record identity.EndUserIdempotency) (bool, error) {
	if err := identity.ValidateAdaptivePasswordHash(algorithm, passwordHash); err != nil {
		return false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	recovered, err := beginEndUserIdempotency(ctx, tx, record)
	if err != nil || recovered {
		return recovered, err
	}
	result, err := tx.Exec(ctx, `UPDATE identity.user_credentials SET password_hash=$3,password_algorithm=$4,credential_version=credential_version+1,password_changed_at=$5,updated_at=$5 WHERE user_id=$1 AND credential_type='password' AND credential_status='active' AND credential_version=$2`, userID, expectedVersion, passwordHash, algorithm, now)
	if err != nil {
		return false, err
	}
	if result.RowsAffected() != 1 {
		if err := failEndUserIdempotency(ctx, tx, record, "version_conflict"); err != nil {
			return false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, identity.ErrEndUserVersionConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.end_user_sessions SET session_version=session_version+1,last_seen_at=$3 WHERE session_id=$1 AND user_id=$2`, currentSessionID, userID, now); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.end_user_session_tokens t SET revoked_at=COALESCE(t.revoked_at,$3) FROM identity.end_user_sessions s WHERE t.session_id=s.session_id AND s.session_id=$1 AND s.user_id=$2 AND t.token_type='access'`, currentSessionID, userID, now); err != nil {
		return false, err
	}
	if revokeSessions {
		if _, err := tx.Exec(ctx, `WITH target AS (UPDATE identity.end_user_sessions SET revoked_at=COALESCE(revoked_at,$3),revoke_reason=COALESCE(revoke_reason,'credential_changed'),last_seen_at=$3 WHERE user_id=$1 AND session_id<>$2 RETURNING session_id) UPDATE identity.end_user_session_tokens t SET revoked_at=COALESCE(t.revoked_at,$3) FROM target WHERE t.session_id=target.session_id`, userID, currentSessionID, now); err != nil {
			return false, err
		}
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return false, err
	}
	if err := completeEndUserIdempotency(ctx, tx, record, currentSessionID); err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}

func (r *Repository) CreateEndUserSession(ctx context.Context, value identity.NewEndUserSession) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertNewEndUserSession(ctx, tx, value); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, value.OutboxEvent); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) CreateEndUserSessionAndClearFailures(ctx context.Context, value identity.NewEndUserSession, scopeID string, identifierDigest []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertNewEndUserSession(ctx, tx, value); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM identity.end_user_login_failures WHERE scope_id=$1 AND identifier_digest=$2`, scopeID, identifierDigest); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, value.OutboxEvent); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) FindEndUserByAccessDigest(ctx context.Context, digest []byte, scope identity.EndUserSessionScope, now time.Time) (identity.EndUserSession, error) {
	session, err := r.ResolveEndUserByAccessDigest(ctx, digest, now)
	if err != nil {
		return session, err
	}
	if !scope.Matches(session) {
		return identity.EndUserSession{}, identity.ErrEndUserScopeMismatch
	}
	return session, nil
}

func (r *Repository) ResolveEndUserByAccessDigest(ctx context.Context, digest []byte, now time.Time) (identity.EndUserSession, error) {
	session, token, err := scanEndUserToken(r.pool.QueryRow(ctx, endUserTokenQuery+` WHERE t.token_digest=$1`, digest))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserSession{}, identity.ErrEndUserSessionExpired
	}
	if err != nil {
		return identity.EndUserSession{}, err
	}
	if session.AccountStatus != "active" {
		return session, identity.ErrEndUserAccountDisabled
	}
	if token.tokenType != "access" || session.RevokedAt != nil || token.revokedAt != nil {
		return session, identity.ErrEndUserSessionRevoked
	}
	if !session.AccessExpiresAt.After(now) || !token.expiresAt.After(now) || !session.AbsoluteExpiresAt.After(now) {
		return session, identity.ErrEndUserSessionExpired
	}
	return session, nil
}

func (r *Repository) ResolveEndUserRefreshScope(ctx context.Context, digest []byte, now time.Time) (identity.EndUserSessionScope, error) {
	session, token, err := scanEndUserToken(r.pool.QueryRow(ctx, endUserTokenQuery+` WHERE t.token_digest=$1`, digest))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserSessionScope{}, identity.ErrEndUserSessionExpired
	}
	if err != nil {
		return identity.EndUserSessionScope{}, err
	}
	if token.tokenType != "refresh" || session.RevokedAt != nil || token.revokedAt != nil {
		return identity.EndUserSessionScope{}, identity.ErrEndUserSessionRevoked
	}
	if session.AccountStatus != "active" {
		return identity.EndUserSessionScope{}, identity.ErrEndUserAccountDisabled
	}
	if token.consumedAt != nil && token.rotationRecoveryExpiresAt != nil && token.rotationRecoveryExpiresAt.After(now) && session.RefreshExpiresAt.After(now) && session.AbsoluteExpiresAt.After(now) {
		return identity.EndUserSessionScope{ProductID: session.ProductID, ApplicationID: session.ApplicationID, TenantID: session.TenantID, Environment: session.Environment}, nil
	}
	if !token.expiresAt.After(now) || !session.RefreshExpiresAt.After(now) || !session.AbsoluteExpiresAt.After(now) {
		return identity.EndUserSessionScope{}, identity.ErrEndUserSessionExpired
	}
	return identity.EndUserSessionScope{ProductID: session.ProductID, ApplicationID: session.ApplicationID, TenantID: session.TenantID, Environment: session.Environment}, nil
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
	if session.AccountStatus != "active" {
		return identity.EndUserSession{}, identity.ErrEndUserAccountDisabled
	}
	if token.tokenType != "refresh" {
		return identity.EndUserSession{}, identity.ErrEndUserSessionRevoked
	}
	if session.RevokedAt != nil || token.revokedAt != nil {
		return identity.EndUserSession{}, identity.ErrEndUserSessionRevoked
	}
	if token.consumedAt != nil {
		sameRecoveryRequest := len(rotation.RequestDigest) == 32 && token.rotationRequestDigest != nil && hmac.Equal(rotation.RequestDigest, token.rotationRequestDigest) && token.rotationRecoveryExpiresAt != nil && token.rotationRecoveryExpiresAt.After(rotation.Now)
		if sameRecoveryRequest && (!session.RefreshExpiresAt.After(rotation.Now) || !session.AbsoluteExpiresAt.After(rotation.Now)) {
			return identity.EndUserSession{}, identity.ErrEndUserSessionExpired
		}
		if sameRecoveryRequest {
			var refreshDigest, accessDigest []byte
			err := tx.QueryRow(ctx, `SELECT r.token_digest,a.token_digest FROM identity.end_user_session_tokens r JOIN identity.end_user_session_tokens a ON a.session_id=r.session_id AND a.token_family_id=r.token_family_id AND a.generation=r.generation AND a.token_type='access' WHERE r.token_id=$1 AND r.token_type='refresh'`, token.replacedByTokenID).Scan(&refreshDigest, &accessDigest)
			if err != nil {
				return identity.EndUserSession{}, err
			}
			if !hmac.Equal(refreshDigest, rotation.RefreshToken.Digest) || !hmac.Equal(accessDigest, rotation.AccessToken.Digest) {
				return identity.EndUserSession{}, identity.ErrEndUserSessionRevoked
			}
			return session, tx.Commit(ctx)
		}
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
	if !token.expiresAt.After(rotation.Now) || !session.RefreshExpiresAt.After(rotation.Now) || !session.AbsoluteExpiresAt.After(rotation.Now) {
		return identity.EndUserSession{}, identity.ErrEndUserSessionExpired
	}
	nextGeneration := token.generation + 1
	rotation.AccessToken.Generation = nextGeneration
	rotation.RefreshToken.Generation = nextGeneration
	rotation.AccessExpiresAt = minimumEndUserTime(rotation.AccessExpiresAt, session.AbsoluteExpiresAt)
	rotation.RefreshExpiresAt = minimumEndUserTime(rotation.RefreshExpiresAt, session.AbsoluteExpiresAt)
	rotation.AccessToken.ExpiresAt = minimumEndUserTime(rotation.AccessToken.ExpiresAt, session.AbsoluteExpiresAt)
	rotation.RefreshToken.ExpiresAt = minimumEndUserTime(rotation.RefreshToken.ExpiresAt, session.AbsoluteExpiresAt)
	if !rotation.AccessExpiresAt.After(rotation.Now) || !rotation.RefreshExpiresAt.After(rotation.Now) || !rotation.AccessToken.ExpiresAt.After(rotation.Now) || !rotation.RefreshToken.ExpiresAt.After(rotation.Now) {
		return identity.EndUserSession{}, identity.ErrEndUserSessionExpired
	}
	if len(rotation.RequestDigest) != 32 || !rotation.RecoveryExpiresAt.After(rotation.Now) {
		return identity.EndUserSession{}, errors.New("refresh recovery request digest and window are required")
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.end_user_session_tokens SET consumed_at=$2,replaced_by_token_id=$3,rotation_request_digest=$4,rotation_recovery_expires_at=$5 WHERE token_id=$1`, token.tokenID, rotation.Now, rotation.RefreshToken.TokenID, rotation.RequestDigest, rotation.RecoveryExpiresAt); err != nil {
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
	err = tx.QueryRow(ctx, `SELECT token_family_id FROM identity.end_user_sessions WHERE session_id=$1 AND user_id=$2 AND product_id=$3 AND application_id=$4 AND tenant_id IS NOT DISTINCT FROM $5 AND COALESCE(environment,'')=$6 FOR UPDATE`, sessionID, userID, scope.ProductID, scope.ApplicationID, scope.TenantID, scope.Environment).Scan(&family)
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

func (r *Repository) ListEndUserSessions(ctx context.Context, userID, currentSessionID string, scope identity.EndUserSessionScope) ([]identity.EndUserSessionSummary, error) {
	rows, err := r.pool.Query(ctx, `SELECT session_id,product_id,application_id,tenant_id,COALESCE(environment,''),authentication_method,created_at,last_seen_at,refresh_expires_at,revoked_at FROM identity.end_user_sessions WHERE user_id=$1 AND product_id=$2 AND application_id=$3 AND tenant_id IS NOT DISTINCT FROM $4 AND COALESCE(environment,'')=$5 ORDER BY created_at DESC,session_id`, userID, scope.ProductID, scope.ApplicationID, scope.TenantID, scope.Environment)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []identity.EndUserSessionSummary
	for rows.Next() {
		var item identity.EndUserSessionSummary
		if err := rows.Scan(&item.SessionID, &item.ProductID, &item.ApplicationID, &item.TenantID, &item.Environment, &item.AuthenticationMethod, &item.CreatedAt, &item.LastSeenAt, &item.ExpiresAt, &item.RevokedAt); err != nil {
			return nil, err
		}
		item.Current = item.SessionID == currentSessionID
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) CreateRecoveryChallenge(ctx context.Context, challenge identity.RecoveryChallenge) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO identity.recovery_challenges(challenge_id,continuation_digest,identifier_type,identifier_digest,matched_user_id,delivery_target_masked,proof_digest,delivery_status,max_attempts,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,'pending',$8,$9,$10)`, challenge.ChallengeID, challenge.ContinuationDigest, challenge.IdentifierType, challenge.IdentifierDigest, challenge.MatchedUserID, challenge.DeliveryTargetMasked, challenge.ProofDigest, challenge.MaxAttempts, challenge.CreatedAt, challenge.ExpiresAt); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, challenge.OutboxEvent); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) CreateRecoveryChallengeIdempotent(ctx context.Context, challenge identity.RecoveryChallenge, record identity.EndUserIdempotency) (identity.RecoveryChallenge, bool, error) {
	if record.Operation != "recovery_start" || record.ScopeID == "" || len(record.ActorDigest) != 32 || len(record.KeyDigest) != 32 || len(record.RequestDigest) != 32 || record.ResourceID == "" || record.Now.IsZero() {
		return identity.RecoveryChallenge{}, false, errors.New("invalid recovery-start idempotency record")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.RecoveryChallenge{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	recovered, err := beginEndUserIdempotency(ctx, tx, record)
	if err != nil {
		return identity.RecoveryChallenge{}, false, err
	}
	if recovered {
		var resourceID string
		if err := tx.QueryRow(ctx, `SELECT resource_id FROM identity.end_user_idempotency_records WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest).Scan(&resourceID); err != nil {
			return identity.RecoveryChallenge{}, false, err
		}
		var persisted identity.RecoveryChallenge
		err := tx.QueryRow(ctx, `SELECT challenge_id,continuation_digest,identifier_type,identifier_digest,matched_user_id,delivery_target_masked,proof_digest,delivery_status,max_attempts,created_at,expires_at FROM identity.recovery_challenges WHERE challenge_id=$1`, resourceID).Scan(&persisted.ChallengeID, &persisted.ContinuationDigest, &persisted.IdentifierType, &persisted.IdentifierDigest, &persisted.MatchedUserID, &persisted.DeliveryTargetMasked, &persisted.ProofDigest, &persisted.DeliveryStatus, &persisted.MaxAttempts, &persisted.CreatedAt, &persisted.ExpiresAt)
		return persisted, true, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity.recovery_challenges(challenge_id,continuation_digest,identifier_type,identifier_digest,matched_user_id,delivery_target_masked,proof_digest,delivery_status,max_attempts,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,'pending',$8,$9,$10)`, challenge.ChallengeID, challenge.ContinuationDigest, challenge.IdentifierType, challenge.IdentifierDigest, challenge.MatchedUserID, challenge.DeliveryTargetMasked, challenge.ProofDigest, challenge.MaxAttempts, challenge.CreatedAt, challenge.ExpiresAt); err != nil {
		return identity.RecoveryChallenge{}, false, err
	}
	if err := insertOutboxStrict(ctx, tx, challenge.OutboxEvent); err != nil {
		return identity.RecoveryChallenge{}, false, err
	}
	if err := completeEndUserIdempotency(ctx, tx, record, record.ResourceID); err != nil {
		return identity.RecoveryChallenge{}, false, err
	}
	return challenge, false, tx.Commit(ctx)
}

func (r *Repository) ActivateRecoveryChallenge(ctx context.Context, challengeID string) error {
	result, err := r.pool.Exec(ctx, `UPDATE identity.recovery_challenges SET delivery_status='active' WHERE challenge_id=$1 AND delivery_status IN ('pending','active')`, challengeID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrRecoveryProofInvalid
	}
	return nil
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
	err = tx.QueryRow(ctx, `SELECT challenge_id,matched_user_id,proof_digest,attempt_count,max_attempts,expires_at,consumed_at FROM identity.recovery_challenges WHERE continuation_digest=$1 AND delivery_status='active' FOR UPDATE`, continuationDigest).Scan(&result.ChallengeID, &result.MatchedUserID, &storedProof, &attempts, &maximum, &expires, &consumed)
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

func (r *Repository) CompleteEndUserRecovery(ctx context.Context, continuationDigest, proofDigest, passwordHash []byte, algorithm string, now time.Time, event identity.OutboxEvent) (bool, error) {
	if err := identity.ValidateAdaptivePasswordHash(algorithm, passwordHash); err != nil {
		return false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var challengeID string
	var userID *string
	var storedProof []byte
	var attempts, maximum int
	var expires time.Time
	var consumed *time.Time
	err = tx.QueryRow(ctx, `SELECT challenge_id,matched_user_id,proof_digest,attempt_count,max_attempts,expires_at,consumed_at FROM identity.recovery_challenges WHERE continuation_digest=$1 AND delivery_status='active' FOR UPDATE`, continuationDigest).Scan(&challengeID, &userID, &storedProof, &attempts, &maximum, &expires, &consumed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, identity.ErrRecoveryProofInvalid
	}
	if err != nil {
		return false, err
	}
	if consumed != nil {
		return false, identity.ErrRecoveryProofReplayed
	}
	if !expires.After(now) {
		return false, identity.ErrRecoveryChallengeExpired
	}
	if attempts >= maximum || !hmac.Equal(storedProof, proofDigest) {
		if attempts < maximum {
			if _, err := tx.Exec(ctx, `UPDATE identity.recovery_challenges SET attempt_count=attempt_count+1 WHERE challenge_id=$1`, challengeID); err != nil {
				return false, err
			}
			if err := insertOutboxStrict(ctx, tx, event); err != nil {
				return false, err
			}
			if err := tx.Commit(ctx); err != nil {
				return false, err
			}
		}
		return false, identity.ErrRecoveryProofInvalid
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.recovery_challenges SET attempt_count=attempt_count+1,consumed_at=$2 WHERE challenge_id=$1`, challengeID, now); err != nil {
		return false, err
	}
	if userID != nil {
		result, err := tx.Exec(ctx, `UPDATE identity.user_credentials SET password_hash=$2,password_algorithm=$3,credential_version=credential_version+1,password_changed_at=$4,updated_at=$4 WHERE user_id=$1 AND credential_type='password' AND credential_status='active'`, *userID, passwordHash, algorithm, now)
		if err != nil {
			return false, err
		}
		if result.RowsAffected() != 1 {
			return false, identity.ErrNotFound
		}
		if err := revokeEndUserSessions(ctx, tx, `user_id=$1`, []any{*userID}, "credential_recovered", now); err != nil {
			return false, err
		}
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return userID != nil, nil
}

func (r *Repository) CompleteEndUserRecoveryIdempotent(ctx context.Context, recoveryScopeID string, continuationDigest, proofDigest, passwordHash []byte, algorithm string, now time.Time, event identity.OutboxEvent, record identity.EndUserIdempotency) (bool, bool, error) {
	if err := identity.ValidateAdaptivePasswordHash(algorithm, passwordHash); err != nil {
		return false, false, err
	}
	if record.Operation != "recovery_complete" || record.ScopeID != recoveryScopeID || len(record.ActorDigest) != 32 || len(record.KeyDigest) != 32 || len(record.RequestDigest) != 32 || record.Now.IsZero() {
		return false, false, errors.New("invalid recovery-complete idempotency record")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	recovered, err := beginEndUserIdempotency(ctx, tx, record)
	if err != nil {
		return false, false, err
	}
	if recovered {
		var resource string
		if err := tx.QueryRow(ctx, `SELECT resource_id FROM identity.end_user_idempotency_records WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest).Scan(&resource); err != nil {
			return false, false, err
		}
		return resource == "matched", true, tx.Commit(ctx)
	}
	var challengeID string
	var userID *string
	var storedProof []byte
	var attempts, maximum int
	var expires time.Time
	var consumed *time.Time
	err = tx.QueryRow(ctx, `SELECT c.challenge_id,c.matched_user_id,c.proof_digest,c.attempt_count,c.max_attempts,c.expires_at,c.consumed_at FROM identity.recovery_challenges c WHERE c.continuation_digest=$1 AND c.delivery_status='active' AND EXISTS (SELECT 1 FROM identity.end_user_idempotency_records i WHERE i.operation='recovery_start' AND i.scope_id=$2 AND i.resource_id=c.challenge_id AND i.state='completed') FOR UPDATE OF c`, continuationDigest, recoveryScopeID).Scan(&challengeID, &userID, &storedProof, &attempts, &maximum, &expires, &consumed)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := failEndUserIdempotency(ctx, tx, record, "invalid_recovery_proof"); err != nil {
			return false, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, false, err
		}
		return false, false, identity.ErrRecoveryProofInvalid
	}
	if err != nil {
		return false, false, err
	}
	if consumed != nil {
		if err := failEndUserIdempotency(ctx, tx, record, "recovery_replayed"); err != nil {
			return false, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, false, err
		}
		return false, false, identity.ErrRecoveryProofReplayed
	}
	if !expires.After(now) {
		if err := failEndUserIdempotency(ctx, tx, record, "recovery_expired"); err != nil {
			return false, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, false, err
		}
		return false, false, identity.ErrRecoveryChallengeExpired
	}
	if attempts >= maximum || !hmac.Equal(storedProof, proofDigest) {
		if attempts < maximum {
			if _, err := tx.Exec(ctx, `UPDATE identity.recovery_challenges SET attempt_count=attempt_count+1 WHERE challenge_id=$1`, challengeID); err != nil {
				return false, false, err
			}
			event.Payload.Result = "failure"
			event.Payload.ReasonCode = "invalid_recovery_proof"
			if err := insertOutboxStrict(ctx, tx, event); err != nil {
				return false, false, err
			}
		}
		if err := failEndUserIdempotency(ctx, tx, record, "invalid_recovery_proof"); err != nil {
			return false, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, false, err
		}
		return false, false, identity.ErrRecoveryProofInvalid
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.recovery_challenges SET attempt_count=attempt_count+1,consumed_at=$2 WHERE challenge_id=$1`, challengeID, now); err != nil {
		return false, false, err
	}
	resource := "unmatched"
	if userID != nil {
		result, err := tx.Exec(ctx, `UPDATE identity.user_credentials SET password_hash=$2,password_algorithm=$3,credential_version=credential_version+1,password_changed_at=$4,updated_at=$4 WHERE user_id=$1 AND credential_type='password' AND credential_status='active'`, *userID, passwordHash, algorithm, now)
		if err != nil || result.RowsAffected() != 1 {
			return false, false, identity.ErrNotFound
		}
		if err := revokeEndUserSessions(ctx, tx, `user_id=$1`, []any{*userID}, "credential_recovered", now); err != nil {
			return false, false, err
		}
		resource = "matched"
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return false, false, err
	}
	if err := completeEndUserIdempotency(ctx, tx, record, resource); err != nil {
		return false, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, err
	}
	return userID != nil, false, nil
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
	err := r.pool.QueryRow(ctx, `SELECT e.external_identity_id,e.user_id,e.provider,e.provider_application_id,e.subject_digest,e.subject_masked,e.union_subject_digest,e.status,e.identity_version,e.linked_at,e.updated_at,u.account_status FROM identity.external_identities e JOIN identity.users u ON u.user_id=e.user_id WHERE e.provider=$1 AND e.provider_application_id=$2 AND e.subject_digest=$3 AND e.status='active'`, provider, applicationID, subjectDigest).Scan(&result.ExternalIdentityID, &result.UserID, &result.Provider, &result.ProviderApplicationID, &result.SubjectDigest, &result.SubjectMasked, &result.UnionSubjectDigest, &result.Status, &result.Version, &result.LinkedAt, &result.UpdatedAt, &result.AccountStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ExternalIdentity{}, identity.ErrNotFound
	}
	return result, err
}

type endUserTokenState struct {
	tokenID                   string
	tokenType                 string
	generation                int
	expiresAt                 time.Time
	consumedAt                *time.Time
	revokedAt                 *time.Time
	replacedByTokenID         *string
	rotationRequestDigest     []byte
	rotationRecoveryExpiresAt *time.Time
}

const endUserTokenQuery = `SELECT s.session_id,s.user_id,s.product_id,s.application_id,s.tenant_id,COALESCE(s.environment,''),s.token_family_id,s.authentication_method,s.external_identity_id,s.session_version,s.auth_time,s.created_at,s.last_seen_at,s.access_expires_at,s.refresh_expires_at,s.absolute_expires_at,s.risk_summary_digest,s.revoked_at,s.revoke_reason,u.account_status,t.token_id,t.token_type,t.generation,t.expires_at,t.consumed_at,t.revoked_at,t.replaced_by_token_id,t.rotation_request_digest,t.rotation_recovery_expires_at FROM identity.end_user_session_tokens t JOIN identity.end_user_sessions s ON s.session_id=t.session_id JOIN identity.users u ON u.user_id=s.user_id`

func scanEndUserToken(row rowScanner) (identity.EndUserSession, endUserTokenState, error) {
	var session identity.EndUserSession
	var token endUserTokenState
	err := row.Scan(&session.SessionID, &session.UserID, &session.ProductID, &session.ApplicationID, &session.TenantID, &session.Environment, &session.TokenFamilyID, &session.AuthenticationMethod, &session.ExternalIdentityID, &session.Version, &session.AuthTime, &session.CreatedAt, &session.LastSeenAt, &session.AccessExpiresAt, &session.RefreshExpiresAt, &session.AbsoluteExpiresAt, &session.RiskSummaryDigest, &session.RevokedAt, &session.RevokeReason, &session.AccountStatus, &token.tokenID, &token.tokenType, &token.generation, &token.expiresAt, &token.consumedAt, &token.revokedAt, &token.replacedByTokenID, &token.rotationRequestDigest, &token.rotationRecoveryExpiresAt)
	return session, token, err
}

func findEndUserSessionByID(ctx context.Context, queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, sessionID string) (identity.EndUserSession, error) {
	var session identity.EndUserSession
	err := queryer.QueryRow(ctx, `SELECT s.session_id,s.user_id,s.product_id,s.application_id,s.tenant_id,COALESCE(s.environment,''),s.token_family_id,s.authentication_method,s.external_identity_id,s.session_version,s.auth_time,s.created_at,s.last_seen_at,s.access_expires_at,s.refresh_expires_at,s.absolute_expires_at,s.risk_summary_digest,s.revoked_at,s.revoke_reason,u.account_status FROM identity.end_user_sessions s JOIN identity.users u ON u.user_id=s.user_id WHERE s.session_id=$1`, sessionID).Scan(&session.SessionID, &session.UserID, &session.ProductID, &session.ApplicationID, &session.TenantID, &session.Environment, &session.TokenFamilyID, &session.AuthenticationMethod, &session.ExternalIdentityID, &session.Version, &session.AuthTime, &session.CreatedAt, &session.LastSeenAt, &session.AccessExpiresAt, &session.RefreshExpiresAt, &session.AbsoluteExpiresAt, &session.RiskSummaryDigest, &session.RevokedAt, &session.RevokeReason, &session.AccountStatus)
	return session, err
}

func insertEndUserToken(ctx context.Context, tx pgx.Tx, sessionID, familyID string, token identity.EndUserSessionToken) error {
	_, err := tx.Exec(ctx, `INSERT INTO identity.end_user_session_tokens(token_id,session_id,token_family_id,token_type,generation,token_digest,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, token.TokenID, sessionID, familyID, token.TokenType, token.Generation, token.Digest, token.CreatedAt, token.ExpiresAt)
	return err
}

func beginEndUserIdempotency(ctx context.Context, tx pgx.Tx, record identity.EndUserIdempotency) (bool, error) {
	if err := lockEndUserIdempotency(ctx, tx, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest); err != nil {
		return false, err
	}
	var storedRequest []byte
	var state string
	var responseDocument []byte
	err := tx.QueryRow(ctx, `SELECT request_digest,state,response_document FROM identity.end_user_idempotency_records WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest).Scan(&storedRequest, &state, &responseDocument)
	if err == nil {
		if !hmac.Equal(storedRequest, record.RequestDigest) {
			return false, identity.ErrEndUserVersionConflict
		}
		switch state {
		case "completed":
			return true, nil
		case "failed":
			return false, decodeEndUserIdempotencyFailure(responseDocument)
		default:
			return false, identity.ErrEndUserVersionConflict
		}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity.end_user_idempotency_records(operation,scope_id,actor_digest,key_digest,request_digest,resource_id,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'pending',$7,$7)`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest, record.RequestDigest, endUserNullableText(record.ResourceID), record.Now)
	return false, err
}

func lockEndUserIdempotency(ctx context.Context, tx pgx.Tx, operation, scopeID string, actorDigest, keyDigest []byte) error {
	lockID := operation + "|" + scopeID + "|" + fmt.Sprintf("%x", actorDigest) + "|" + fmt.Sprintf("%x", keyDigest)
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockID)
	return err
}

func completeEndUserIdempotency(ctx context.Context, tx pgx.Tx, record identity.EndUserIdempotency, resourceID string) error {
	return completeEndUserIdempotencyWithResponse(ctx, tx, record, resourceID, nil)
}

func completeEndUserIdempotencyWithResponse(ctx context.Context, tx pgx.Tx, record identity.EndUserIdempotency, resourceID string, responseDocument []byte) error {
	result, err := tx.Exec(ctx, `UPDATE identity.end_user_idempotency_records SET resource_id=$5,state='completed',response_document=$6,updated_at=$7 WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4 AND state='pending'`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest, resourceID, jsonDocumentValue(responseDocument), record.Now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrEndUserVersionConflict
	}
	return nil
}

type endUserIdempotencyFailureDocument struct {
	Reason string `json:"reason"`
}

func failEndUserIdempotency(ctx context.Context, tx pgx.Tx, record identity.EndUserIdempotency, reason string) error {
	document, err := json.Marshal(endUserIdempotencyFailureDocument{Reason: reason})
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE identity.end_user_idempotency_records SET state='failed',response_document=$5,updated_at=$6 WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4 AND state='pending'`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest, string(document), record.Now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrEndUserVersionConflict
	}
	return nil
}

func decodeEndUserIdempotencyFailure(document []byte) error {
	var failure endUserIdempotencyFailureDocument
	if err := json.Unmarshal(document, &failure); err != nil {
		return identity.ErrEndUserVersionConflict
	}
	switch failure.Reason {
	case "invalid_credentials":
		return identity.ErrEndUserInvalidCredentials
	case "invalid_recovery_proof":
		return identity.ErrRecoveryProofInvalid
	case "recovery_expired":
		return identity.ErrRecoveryChallengeExpired
	case "recovery_replayed":
		return identity.ErrRecoveryProofReplayed
	default:
		return identity.ErrEndUserVersionConflict
	}
}

func jsonDocumentValue(document []byte) any {
	if len(document) == 0 {
		return nil
	}
	return string(document)
}

func minimumEndUserTime(left, right time.Time) time.Time {
	if right.Before(left) {
		return right
	}
	return left
}

func insertEndUserRegistration(ctx context.Context, tx pgx.Tx, registration identity.EndUserRegistration) error {
	user, identifier, credential, profile := registration.User, registration.Identifier, registration.Credential, registration.Profile
	if _, err := tx.Exec(ctx, `INSERT INTO identity.users(user_id,display_name,account_status,user_version,created_at,updated_at) VALUES($1,$2,$3,1,$4,$4)`, user.UserID, profile.DisplayName, user.AccountStatus, user.CreatedAt); err != nil {
		return mapEndUserConflict(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity.user_identifiers(identifier_id,user_id,identifier_type,normalization_version,normalized_digest,masked_value,verification_status,verified_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`, identifier.IdentifierID, user.UserID, identifier.Type, identifier.NormalizationVersion, identifier.NormalizedDigest, identifier.MaskedValue, identifier.VerificationStatus, identifier.VerifiedAt, identifier.CreatedAt); err != nil {
		return mapEndUserConflict(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity.user_credentials(credential_id,user_id,credential_type,identifier_digest,identifier_masked,password_hash,credential_status,password_algorithm,credential_version,password_changed_at,created_at,updated_at) VALUES($1,$2,'password',$3,$4,$5,'active',$6,1,$7,$7,$7)`, credential.CredentialID, user.UserID, identifier.NormalizedDigest, identifier.MaskedValue, credential.PasswordHash, credential.Algorithm, credential.ChangedAt); err != nil {
		return mapEndUserConflict(err)
	}
	_, err := tx.Exec(ctx, `INSERT INTO identity.user_profiles(user_id,profile_version,display_name,avatar_ref,locale,timezone,created_at,updated_at) VALUES($1,1,$2,$3,$4,$5,$6,$6)`, user.UserID, profile.DisplayName, profile.AvatarRef, profile.Locale, profile.Timezone, profile.CreatedAt)
	return err
}

func insertNewEndUserSession(ctx context.Context, tx pgx.Tx, value identity.NewEndUserSession) error {
	session := value.Session
	if session.UserID == "" || !identity.ValidEndUserEnvironment(session.Environment) {
		return identity.ErrEndUserScopeMismatch
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity.end_user_sessions(session_id,user_id,product_id,application_id,tenant_id,environment,token_family_id,authentication_method,external_identity_id,session_version,auth_time,created_at,last_seen_at,access_expires_at,refresh_expires_at,absolute_expires_at,risk_summary_digest) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,1,$10,$11,$11,$12,$13,$14,$15)`, session.SessionID, session.UserID, session.ProductID, session.ApplicationID, session.TenantID, session.Environment, session.TokenFamilyID, session.AuthenticationMethod, session.ExternalIdentityID, session.AuthTime, session.CreatedAt, session.AccessExpiresAt, session.RefreshExpiresAt, session.AbsoluteExpiresAt, endUserNullableBytes(session.RiskSummaryDigest)); err != nil {
		return err
	}
	if err := insertEndUserToken(ctx, tx, session.SessionID, session.TokenFamilyID, value.AccessToken); err != nil {
		return err
	}
	return insertEndUserToken(ctx, tx, session.SessionID, session.TokenFamilyID, value.RefreshToken)
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

func endUserNullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
