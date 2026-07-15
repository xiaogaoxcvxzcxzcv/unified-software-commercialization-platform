package postgres

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/identity"
)

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) FindCredential(ctx context.Context, digest []byte) (identity.Credential, error) {
	var result identity.Credential
	err := r.pool.QueryRow(ctx, `SELECT u.user_id,u.display_name,u.account_status,c.password_hash FROM identity.user_credentials c JOIN identity.users u ON u.user_id=c.user_id WHERE c.credential_type='password' AND c.identifier_digest=$1`, digest).Scan(&result.UserID, &result.DisplayName, &result.AccountStatus, &result.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Credential{}, identity.ErrNotFound
	}
	return result, err
}

func (r *Repository) LoginThrottle(ctx context.Context, identifierDigest, sourceDigest []byte, now time.Time) (identity.ThrottleState, error) {
	var state identity.ThrottleState
	err := r.pool.QueryRow(ctx, `SELECT failure_count,blocked_until FROM identity.admin_login_failures WHERE identifier_digest=$1 AND source_digest=$2`, identifierDigest, sourceDigest).Scan(&state.FailureCount, &state.BlockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ThrottleState{}, nil
	}
	return state, err
}

func (r *Repository) RecordLoginFailure(ctx context.Context, failure identity.LoginFailure) (identity.ThrottleState, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.ThrottleState{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	windowStart := failure.Now.Add(-failure.Window)
	var state identity.ThrottleState
	err = tx.QueryRow(ctx, `
		INSERT INTO identity.admin_login_failures(identifier_digest,source_digest,failure_count,window_started_at,last_failed_at,blocked_until)
		VALUES($1,$2,1,$3,$3,NULL)
		ON CONFLICT(identifier_digest,source_digest) DO UPDATE SET
			failure_count=CASE WHEN identity.admin_login_failures.window_started_at < $4 THEN 1 ELSE identity.admin_login_failures.failure_count+1 END,
			window_started_at=CASE WHEN identity.admin_login_failures.window_started_at < $4 THEN $3 ELSE identity.admin_login_failures.window_started_at END,
			last_failed_at=$3,
			blocked_until=CASE WHEN (CASE WHEN identity.admin_login_failures.window_started_at < $4 THEN 1 ELSE identity.admin_login_failures.failure_count+1 END) >= $5 THEN $3 + $6::interval ELSE identity.admin_login_failures.blocked_until END
		RETURNING failure_count,blocked_until`, failure.IdentifierDigest, failure.SourceDigest, failure.Now, windowStart, failure.MaximumAttempts, failure.BlockDuration.String()).Scan(&state.FailureCount, &state.BlockedUntil)
	if err != nil {
		return identity.ThrottleState{}, err
	}
	if state.BlockedUntil != nil {
		failure.OutboxEvent.Payload.Action = "admin.auth.account_locked"
		failure.OutboxEvent.Payload.RiskLevel = "high"
	}
	if err := insertOutbox(ctx, tx, failure.OutboxEvent); err != nil {
		return identity.ThrottleState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.ThrottleState{}, err
	}
	return state, nil
}

func (r *Repository) ClearLoginFailures(ctx context.Context, identifierDigest []byte) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM identity.admin_login_failures WHERE identifier_digest=$1`, identifierDigest)
	return err
}

func (r *Repository) CreateAdminSession(ctx context.Context, session identity.NewSession) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	risk, err := json.Marshal(session.RiskSummary)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity.admin_sessions(session_id,user_id,token_family_id,transport,authentication_method,session_version,auth_time,created_at,last_seen_at,access_expires_at,refresh_expires_at,absolute_expires_at,csrf_digest,risk_summary,controlled_client_id,controlled_client_credential_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,$9,$10,$11,$12,$13,$14,$15)`, session.SessionID, session.UserID, session.TokenFamilyID, session.Transport, session.AuthenticationMethod, session.SessionVersion, session.AuthTime, session.CreatedAt, session.AccessExpiresAt, session.RefreshExpiresAt, session.AbsoluteExpiresAt, session.CSRFDigest, risk, nullableText(session.ControlledClientID), nullableText(session.ControlledCredentialID))
	if err != nil {
		return err
	}
	if err := insertToken(ctx, tx, session.SessionID, session.TokenFamilyID, session.AccessToken, session.CreatedAt); err != nil {
		return err
	}
	if err := insertToken(ctx, tx, session.SessionID, session.TokenFamilyID, session.RefreshToken, session.CreatedAt); err != nil {
		return err
	}
	if err := insertOutbox(ctx, tx, session.OutboxEvent); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) FindByAccessDigest(ctx context.Context, digest []byte, now time.Time) (identity.StoredSession, error) {
	result, _, _, token, controlledClientActive, err := scanTokenSession(r.pool.QueryRow(ctx, tokenSessionQuery+` WHERE t.token_digest=$1 AND t.token_type='access'`, digest))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.StoredSession{}, identity.ErrSessionExpired
	}
	if err != nil {
		return identity.StoredSession{}, err
	}
	if result.RevokedAt != nil || token.revokedAt != nil {
		return result, identity.ErrSessionRevoked
	}
	if result.Transport == identity.TransportBearer && !controlledClientActive {
		return result, identity.ErrSessionRevoked
	}
	if !result.AccessExpiresAt.After(now) || !token.expiresAt.After(now) {
		return result, identity.ErrSessionExpired
	}
	return result, nil
}

func (r *Repository) TouchSession(ctx context.Context, sessionID string, now time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE identity.admin_sessions SET last_seen_at=$2 WHERE session_id=$1 AND revoked_at IS NULL`, sessionID, now)
	return err
}

func (r *Repository) InspectRefresh(ctx context.Context, digest []byte, transport identity.Transport, binding *identity.ControlledClientBinding, now time.Time) (identity.RefreshInspection, error) {
	stored, tokenType, _, tokenState, controlledClientActive, err := scanTokenSession(r.pool.QueryRow(ctx, tokenSessionQuery+` WHERE t.token_digest=$1`, digest))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.RefreshInspection{}, identity.ErrSessionExpired
	}
	if err != nil {
		return identity.RefreshInspection{}, err
	}
	if tokenType != "refresh" || stored.Transport != transport {
		return identity.RefreshInspection{}, identity.ErrSessionRevoked
	}
	if transport == identity.TransportBearer {
		if !controlledClientActive || binding == nil || stored.ControlledClientID != binding.ClientID || stored.ControlledCredentialID != binding.CredentialID {
			return identity.RefreshInspection{}, identity.ErrSessionRevoked
		}
	} else if binding != nil {
		return identity.RefreshInspection{}, identity.ErrSessionRevoked
	}
	if tokenState.consumedAt != nil {
		return identity.RefreshInspection{Session: stored, Replayed: true}, nil
	}
	if stored.RevokedAt != nil || tokenState.revokedAt != nil {
		return identity.RefreshInspection{}, identity.ErrSessionRevoked
	}
	if !tokenState.expiresAt.After(now) || !stored.RefreshExpiresAt.After(now) || !stored.AbsoluteExpiresAt.After(now) {
		return identity.RefreshInspection{}, identity.ErrSessionExpired
	}
	return identity.RefreshInspection{Session: stored}, nil
}

func (r *Repository) RotateRefresh(ctx context.Context, digest []byte, transport identity.Transport, binding *identity.ControlledClientBinding, rotation identity.Rotation) (identity.StoredSession, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.StoredSession{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stored, tokenType, generation, tokenState, controlledClientActive, err := scanTokenSession(tx.QueryRow(ctx, tokenSessionQuery+` WHERE t.token_digest=$1 FOR UPDATE OF t,s`, digest))
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.StoredSession{}, identity.ErrSessionExpired
	}
	if err != nil {
		return identity.StoredSession{}, err
	}
	if tokenType != "refresh" || stored.Transport != transport {
		return identity.StoredSession{}, identity.ErrSessionRevoked
	}
	if transport == identity.TransportBearer {
		if !controlledClientActive || binding == nil || stored.ControlledClientID != binding.ClientID || stored.ControlledCredentialID != binding.CredentialID {
			return identity.StoredSession{}, identity.ErrSessionRevoked
		}
	} else if binding != nil {
		return identity.StoredSession{}, identity.ErrSessionRevoked
	}
	if tokenState.consumedAt != nil {
		if stored.RevokedAt == nil {
			if err := revokeFamily(ctx, tx, stored.TokenFamilyID, rotation.Now, "refresh_replayed"); err != nil {
				return identity.StoredSession{}, err
			}
			rotation.OutboxEvent.Payload.ActorID = stored.UserID
			rotation.OutboxEvent.Payload.Action = "admin.auth.refresh_replayed"
			rotation.OutboxEvent.Payload.Result = "failure"
			rotation.OutboxEvent.Payload.ReasonCode = "refresh_replayed"
			rotation.OutboxEvent.Payload.RiskLevel = "high"
			if err := insertOutbox(ctx, tx, rotation.OutboxEvent); err != nil {
				return identity.StoredSession{}, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return identity.StoredSession{}, err
		}
		return identity.StoredSession{}, identity.ErrRefreshReplayed
	}
	if stored.RevokedAt != nil || tokenState.revokedAt != nil {
		return identity.StoredSession{}, identity.ErrSessionRevoked
	}
	if !tokenState.expiresAt.After(rotation.Now) || !stored.RefreshExpiresAt.After(rotation.Now) || !stored.AbsoluteExpiresAt.After(rotation.Now) {
		return identity.StoredSession{}, identity.ErrSessionExpired
	}
	refreshExpiry := stored.AbsoluteExpiresAt
	if rotation.RefreshExpires.Before(refreshExpiry) {
		refreshExpiry = rotation.RefreshExpires
	}
	rotation.RefreshToken.Generation = generation + 1
	rotation.RefreshToken.ExpiresAt = refreshExpiry
	rotation.AccessToken.Generation = generation + 1
	_, err = tx.Exec(ctx, `UPDATE identity.admin_session_tokens SET consumed_at=$2,replaced_by_token_id=$3 WHERE token_digest=$1 AND consumed_at IS NULL`, digest, rotation.Now, rotation.RefreshToken.TokenID)
	if err != nil {
		return identity.StoredSession{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE identity.admin_session_tokens SET revoked_at=$2 WHERE token_family_id=$1 AND token_type='access' AND revoked_at IS NULL`, stored.TokenFamilyID, rotation.Now)
	if err != nil {
		return identity.StoredSession{}, err
	}
	if err := insertToken(ctx, tx, stored.SessionID, stored.TokenFamilyID, rotation.AccessToken, rotation.Now); err != nil {
		return identity.StoredSession{}, err
	}
	if err := insertToken(ctx, tx, stored.SessionID, stored.TokenFamilyID, rotation.RefreshToken, rotation.Now); err != nil {
		return identity.StoredSession{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE identity.admin_sessions SET session_version=session_version+1,last_seen_at=$2,access_expires_at=$3,refresh_expires_at=$4,csrf_digest=$5 WHERE session_id=$1`, stored.SessionID, rotation.Now, rotation.AccessExpires, refreshExpiry, rotation.CSRFDigest)
	if err != nil {
		return identity.StoredSession{}, err
	}
	rotation.OutboxEvent.Payload.ActorID = stored.UserID
	if err := insertOutbox(ctx, tx, rotation.OutboxEvent); err != nil {
		return identity.StoredSession{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.StoredSession{}, err
	}
	stored.SessionVersion++
	stored.AccessExpiresAt = rotation.AccessExpires
	stored.RefreshExpiresAt = refreshExpiry
	stored.CSRFDigest = rotation.CSRFDigest
	return stored, nil
}

func (r *Repository) RevokeByToken(ctx context.Context, digest []byte, expected identity.TokenExpectation, now time.Time, reason identity.SessionRevokeReason, event identity.OutboxEvent) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stored, tokenType, _, _, _, err := scanTokenSession(tx.QueryRow(ctx, tokenSessionQuery+` WHERE t.token_digest=$1 FOR UPDATE OF t,s`, digest))
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if stored.Transport != expected.Transport || tokenType != expected.TokenType {
		return identity.ErrSessionRevoked
	}
	if stored.RevokedAt == nil {
		if err := revokeFamily(ctx, tx, stored.TokenFamilyID, now, string(reason)); err != nil {
			return err
		}
		event.Payload.ActorID = stored.UserID
		if err := insertOutboxStrict(ctx, tx, event); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Repository) RevokeCookieSession(ctx context.Context, proof identity.CookieLogoutProof, now time.Time, event identity.OutboxEvent) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	type requestedProof struct {
		kind   string
		digest []byte
	}
	requested := make([]requestedProof, 0, 2)
	if len(proof.AccessDigest) != 0 {
		requested = append(requested, requestedProof{kind: "access", digest: proof.AccessDigest})
	}
	if len(proof.RefreshDigest) != 0 {
		requested = append(requested, requestedProof{kind: "refresh", digest: proof.RefreshDigest})
	}
	if len(requested) == 2 && bytes.Equal(requested[0].digest, requested[1].digest) {
		return identity.ErrSessionRevoked
	}
	// All cookie logout transactions acquire proof locks in digest order. This
	// keeps mixed or concurrently submitted cookie pairs from reversing locks.
	if len(requested) == 2 && bytes.Compare(requested[0].digest, requested[1].digest) > 0 {
		requested[0], requested[1] = requested[1], requested[0]
	}

	locked := make(map[string]lockedLogoutProof, len(requested))
	for _, item := range requested {
		stored, tokenType, _, token, _, scanErr := scanTokenSession(tx.QueryRow(ctx, tokenSessionQuery+` WHERE t.token_digest=$1 FOR UPDATE OF t,s`, item.digest))
		if errors.Is(scanErr, pgx.ErrNoRows) {
			locked[item.kind] = lockedLogoutProof{}
			continue
		}
		if scanErr != nil {
			return scanErr
		}
		if stored.Transport != identity.TransportCookie || tokenType != item.kind {
			return identity.ErrSessionRevoked
		}
		locked[item.kind] = lockedLogoutProof{stored: stored, token: token, found: true}
	}

	access, accessRequested := locked["access"]
	refresh, refreshRequested := locked["refresh"]
	if accessRequested && refreshRequested {
		if !access.found || !refresh.found {
			return identity.ErrSessionRevoked
		}
		if access.stored.SessionID != refresh.stored.SessionID || access.stored.TokenFamilyID != refresh.stored.TokenFamilyID {
			return identity.ErrSessionRevoked
		}
	}
	if refreshRequested && refresh.found && refresh.token.consumedAt != nil && refresh.stored.RevokedAt == nil {
		event.Payload.Action = "admin.auth.refresh_replayed"
		event.Payload.Result = "failure"
		event.Payload.ReasonCode = "refresh_replayed"
		event.Payload.RiskLevel = "high"
		if err := revokeLockedCookieFamily(ctx, tx, refresh.stored, now, identity.SessionRevokeReason("refresh_replayed"), event); err != nil {
			return err
		}
		return identity.ErrRefreshReplayed
	}
	if accessRequested && access.found && access.activeAccess(now) {
		if len(proof.CSRFDigest) == 0 || !hmac.Equal(access.stored.CSRFDigest, proof.CSRFDigest) {
			return identity.ErrCSRFFailed
		}
		return revokeLockedCookieFamily(ctx, tx, access.stored, now, identity.RevokeReasonLogout, event)
	}

	if refreshRequested && refresh.found {
		if refresh.activeRefresh(now) {
			return revokeLockedCookieFamily(ctx, tx, refresh.stored, now, identity.RevokeReasonLogout, event)
		}
	}

	// Unknown, expired, or already revoked proofs are an idempotent terminal
	// logout. The handler may clear only the browser's local cookies.
	return tx.Commit(ctx)
}

func (r *Repository) BootstrapIdentity(ctx context.Context, user identity.BootstrapUser) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var existing string
	err = tx.QueryRow(ctx, `SELECT user_id FROM identity.user_credentials WHERE credential_type='password' AND identifier_digest=$1`, user.IdentifierDigest).Scan(&existing)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity.users(user_id,display_name,account_status,created_at,updated_at) VALUES($1,$2,'active',$3,$3)`, user.UserID, user.DisplayName, user.Now)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity.user_credentials(credential_id,user_id,credential_type,identifier_digest,identifier_masked,password_hash,created_at,updated_at) VALUES($1,$2,'password',$3,$4,$5,$6,$6)`, user.CredentialID, user.UserID, user.IdentifierDigest, user.IdentifierMasked, user.PasswordHash, user.Now)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return user.UserID, nil
}

func (r *Repository) ResolveControlledClientCredential(ctx context.Context, clientID, credentialID, proofType string, digest []byte, now time.Time) (identity.ControlledClientCredential, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.ControlledClientCredential{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result := identity.ControlledClientCredential{ControlledClientBinding: identity.ControlledClientBinding{ClientID: clientID, CredentialID: credentialID}}
	err = tx.QueryRow(ctx, `
		SELECT c.display_name,c.client_type
		FROM identity.admin_auth_clients c
		JOIN identity.admin_auth_client_credentials cr ON cr.client_id=c.client_id
		WHERE c.client_id=$1 AND cr.credential_id=$2 AND cr.proof_type=$3 AND cr.secret_digest=$4
		  AND c.status='active' AND c.disabled_at IS NULL
		  AND (c.expires_at IS NULL OR c.expires_at > $5)
		  AND cr.revoked_at IS NULL AND cr.not_before <= $5
		  AND (cr.expires_at IS NULL OR cr.expires_at > $5)
		FOR UPDATE OF c,cr`, clientID, credentialID, proofType, digest, now).Scan(&result.DisplayName, &result.ClientType)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ControlledClientCredential{}, identity.ErrNotFound
	}
	if err != nil {
		return identity.ControlledClientCredential{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.admin_auth_clients SET last_used_at=$2,updated_at=GREATEST(updated_at,$2) WHERE client_id=$1`, clientID, now); err != nil {
		return identity.ControlledClientCredential{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.admin_auth_client_credentials SET last_used_at=$3 WHERE client_id=$1 AND credential_id=$2`, clientID, credentialID, now); err != nil {
		return identity.ControlledClientCredential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.ControlledClientCredential{}, err
	}
	return result, nil
}

func (r *Repository) RegisterControlledClient(ctx context.Context, registration identity.ControlledClientRegistration) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO identity.admin_auth_clients(client_id,display_name,client_type,status,created_at,updated_at,expires_at) VALUES($1,$2,$3,'active',$4,$4,$5)`, registration.ClientID, registration.DisplayName, registration.ClientType, registration.CreatedAt, registration.ExpiresAt)
	if err != nil {
		return controlledClientWriteError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity.admin_auth_client_credentials(credential_id,client_id,proof_type,secret_digest,digest_version,created_at,not_before,expires_at) VALUES($1,$2,$3,$4,1,$5,$6,$7)`, registration.CredentialID, registration.ClientID, registration.ProofType, registration.SecretDigest, registration.CreatedAt, registration.NotBefore, registration.ExpiresAt)
	if err != nil {
		return controlledClientWriteError(err)
	}
	if err := insertOutboxStrict(ctx, tx, registration.OutboxEvent); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) AddControlledClientCredential(ctx context.Context, registration identity.ControlledClientCredentialRegistration) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	err = tx.QueryRow(ctx, `SELECT TRUE FROM identity.admin_auth_clients WHERE client_id=$1 AND status='active' AND disabled_at IS NULL AND (expires_at IS NULL OR expires_at > $2) FOR UPDATE`, registration.ClientID, registration.CreatedAt).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity.admin_auth_client_credentials(credential_id,client_id,proof_type,secret_digest,digest_version,created_at,not_before,expires_at) VALUES($1,$2,$3,$4,1,$5,$6,$7)`, registration.CredentialID, registration.ClientID, registration.ProofType, registration.SecretDigest, registration.CreatedAt, registration.NotBefore, registration.ExpiresAt)
	if err != nil {
		return controlledClientWriteError(err)
	}
	if err := insertOutboxStrict(ctx, tx, registration.OutboxEvent); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) DisableControlledClient(ctx context.Context, clientID string, now time.Time, event identity.OutboxEvent) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `UPDATE identity.admin_auth_clients SET status='disabled',disabled_at=COALESCE(disabled_at,$2),updated_at=$2 WHERE client_id=$1`, clientID, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.admin_sessions SET revoked_at=COALESCE(revoked_at,$2),revoke_reason=COALESCE(revoke_reason,'controlled_client_disabled'),last_seen_at=$2 WHERE controlled_client_id=$1`, clientID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.admin_session_tokens t SET revoked_at=COALESCE(t.revoked_at,$2) FROM identity.admin_sessions s WHERE t.session_id=s.session_id AND s.controlled_client_id=$1`, clientID, now); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) RevokeControlledClientCredential(ctx context.Context, clientID, credentialID string, now time.Time, event identity.OutboxEvent) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `UPDATE identity.admin_auth_client_credentials SET revoked_at=COALESCE(revoked_at,$3) WHERE client_id=$1 AND credential_id=$2`, clientID, credentialID, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.admin_sessions SET revoked_at=COALESCE(revoked_at,$3),revoke_reason=COALESCE(revoke_reason,'controlled_client_credential_revoked'),last_seen_at=$3 WHERE controlled_client_id=$1 AND controlled_client_credential_id=$2`, clientID, credentialID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.admin_session_tokens t SET revoked_at=COALESCE(t.revoked_at,$3) FROM identity.admin_sessions s WHERE t.session_id=s.session_id AND s.controlled_client_id=$1 AND s.controlled_client_credential_id=$2`, clientID, credentialID, now); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]identity.ClaimedOutboxEvent, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT event_id,payload,attempt_count FROM identity.outbox_events WHERE status IN ('pending','processing') AND next_attempt_at <= $1 ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []identity.ClaimedOutboxEvent
	for rows.Next() {
		var item identity.ClaimedOutboxEvent
		var raw []byte
		if err := rows.Scan(&item.EventID, &raw, &item.AttemptCount); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.Payload); err != nil {
			return nil, fmt.Errorf("decode outbox %s: %w", item.EventID, err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, item := range result {
		if _, err := tx.Exec(ctx, `UPDATE identity.outbox_events SET status='processing',attempt_count=attempt_count+1,next_attempt_at=$2 WHERE event_id=$1`, item.EventID, now.Add(30*time.Second)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) MarkOutboxPublished(ctx context.Context, eventID string, now time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE identity.outbox_events SET status='published',published_at=$2,last_error=NULL WHERE event_id=$1`, eventID, now)
	return err
}
func (r *Repository) MarkOutboxFailed(ctx context.Context, eventID, errorSummary string, next time.Time, dead bool) error {
	status := "pending"
	if dead {
		status = "dead"
	}
	_, err := r.pool.Exec(ctx, `UPDATE identity.outbox_events SET status=$2,next_attempt_at=$3,last_error=$4 WHERE event_id=$1`, eventID, status, next, errorSummary)
	return err
}

type tokenState struct {
	expiresAt             time.Time
	consumedAt, revokedAt *time.Time
}

type lockedLogoutProof struct {
	stored identity.StoredSession
	token  tokenState
	found  bool
}

func (p lockedLogoutProof) activeAccess(now time.Time) bool {
	return p.found && p.stored.RevokedAt == nil && p.token.revokedAt == nil &&
		p.stored.AccessExpiresAt.After(now) && p.stored.AbsoluteExpiresAt.After(now) && p.token.expiresAt.After(now)
}

func (p lockedLogoutProof) activeRefresh(now time.Time) bool {
	return p.found && p.stored.RevokedAt == nil && p.token.revokedAt == nil && p.token.consumedAt == nil &&
		p.stored.RefreshExpiresAt.After(now) && p.stored.AbsoluteExpiresAt.After(now) && p.token.expiresAt.After(now)
}

func revokeLockedCookieFamily(ctx context.Context, tx pgx.Tx, stored identity.StoredSession, now time.Time, reason identity.SessionRevokeReason, event identity.OutboxEvent) error {
	if stored.RevokedAt != nil {
		return tx.Commit(ctx)
	}
	if err := revokeFamily(ctx, tx, stored.TokenFamilyID, now, string(reason)); err != nil {
		return err
	}
	event.Payload.ActorID = stored.UserID
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const tokenSessionQuery = `SELECT s.session_id,s.user_id,u.display_name,u.account_status,s.token_family_id,s.transport,s.authentication_method,s.session_version,s.auth_time,s.access_expires_at,s.refresh_expires_at,s.absolute_expires_at,s.csrf_digest,s.revoked_at,t.token_type,t.generation,t.expires_at,t.consumed_at,t.revoked_at,s.controlled_client_id,s.controlled_client_credential_id,CASE WHEN s.transport='cookie' THEN TRUE ELSE COALESCE(c.status='active' AND c.disabled_at IS NULL AND (c.expires_at IS NULL OR c.expires_at > CURRENT_TIMESTAMP) AND cr.revoked_at IS NULL AND cr.not_before <= CURRENT_TIMESTAMP AND (cr.expires_at IS NULL OR cr.expires_at > CURRENT_TIMESTAMP),FALSE) END FROM identity.admin_session_tokens t JOIN identity.admin_sessions s ON s.session_id=t.session_id JOIN identity.users u ON u.user_id=s.user_id LEFT JOIN identity.admin_auth_clients c ON c.client_id=s.controlled_client_id LEFT JOIN identity.admin_auth_client_credentials cr ON cr.client_id=s.controlled_client_id AND cr.credential_id=s.controlled_client_credential_id`

type rowScanner interface{ Scan(...any) error }

func scanTokenSession(row rowScanner) (identity.StoredSession, string, int, tokenState, bool, error) {
	var s identity.StoredSession
	var tokenType string
	var generation int
	var state tokenState
	var clientID, credentialID *string
	var controlledClientActive bool
	err := row.Scan(&s.SessionID, &s.UserID, &s.DisplayName, &s.AccountStatus, &s.TokenFamilyID, &s.Transport, &s.AuthenticationMethod, &s.SessionVersion, &s.AuthTime, &s.AccessExpiresAt, &s.RefreshExpiresAt, &s.AbsoluteExpiresAt, &s.CSRFDigest, &s.RevokedAt, &tokenType, &generation, &state.expiresAt, &state.consumedAt, &state.revokedAt, &clientID, &credentialID, &controlledClientActive)
	if clientID != nil {
		s.ControlledClientID = *clientID
	}
	if credentialID != nil {
		s.ControlledCredentialID = *credentialID
	}
	return s, tokenType, generation, state, controlledClientActive, err
}

func insertToken(ctx context.Context, tx pgx.Tx, sessionID, familyID string, token identity.TokenRecord, created time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO identity.admin_session_tokens(token_id,session_id,token_family_id,token_type,generation,token_digest,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, token.TokenID, sessionID, familyID, token.TokenType, token.Generation, token.Digest, created, token.ExpiresAt)
	return err
}
func insertOutbox(ctx context.Context, tx pgx.Tx, event identity.OutboxEvent) error {
	raw, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity.outbox_events(event_id,topic,payload,next_attempt_at,created_at) VALUES($1,$2,$3,$4,$4) ON CONFLICT(event_id) DO NOTHING`, event.EventID, event.Topic, raw, event.Now)
	return err
}

func insertOutboxStrict(ctx context.Context, tx pgx.Tx, event identity.OutboxEvent) error {
	raw, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity.outbox_events(event_id,topic,payload,next_attempt_at,created_at) VALUES($1,$2,$3,$4,$4)`, event.EventID, event.Topic, raw, event.Now)
	return err
}
func revokeFamily(ctx context.Context, tx pgx.Tx, familyID string, now time.Time, reason string) error {
	if _, err := tx.Exec(ctx, `UPDATE identity.admin_sessions SET revoked_at=COALESCE(revoked_at,$2),revoke_reason=COALESCE(revoke_reason,$3),last_seen_at=$2 WHERE token_family_id=$1`, familyID, now, reason); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE identity.admin_session_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE token_family_id=$1`, familyID, now)
	return err
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func controlledClientWriteError(err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return identity.ErrControlledClientConflict
	}
	return err
}
