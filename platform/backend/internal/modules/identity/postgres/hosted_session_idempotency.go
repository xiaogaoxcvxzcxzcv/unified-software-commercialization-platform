package postgres

import (
	"context"
	"crypto/hmac"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"platform.local/capability-platform/backend/internal/modules/identity"
)

func (r *Repository) RevokeHostedSessionIdempotent(ctx context.Context, expected identity.HostedSessionExpectation, target string, now time.Time, event identity.OutboxEvent, record identity.EndUserIdempotency) error {
	if record.Operation != "hosted_session_revoke" || record.ScopeID == "" || len(record.ActorDigest) != 32 || len(record.KeyDigest) != 32 || len(record.RequestDigest) != 32 || record.ResourceID != target || record.Now.IsZero() {
		return errors.New("invalid hosted session revoke record")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockEndUserIdempotency(ctx, tx, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest); err != nil {
		return err
	}
	var stored []byte
	var resource, state string
	err = tx.QueryRow(ctx, `SELECT request_digest,resource_id,state FROM identity.end_user_idempotency_records WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest).Scan(&stored, &resource, &state)
	if err == nil {
		if !hmac.Equal(stored, record.RequestDigest) || resource != target || state != "completed" {
			return identity.ErrEndUserVersionConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err = lockHostedSessionPair(ctx, tx, expected.SessionID, target); err != nil {
		return err
	}
	if err = validateHostedRevocationActor(ctx, tx, expected); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO identity.end_user_idempotency_records(operation,scope_id,actor_digest,key_digest,request_digest,resource_id,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'pending',$7,$7)`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest, record.RequestDigest, target, record.Now); err != nil {
		return err
	}
	var family string
	var revoked *time.Time
	err = tx.QueryRow(ctx, `SELECT token_family_id,revoked_at FROM identity.end_user_sessions WHERE session_id=$1 AND user_id=$2 AND product_id=$3 AND application_id=$4 AND tenant_id IS NOT DISTINCT FROM $5 AND COALESCE(environment,'')=$6 FOR UPDATE`, target, expected.UserID, expected.Scope.ProductID, expected.Scope.ApplicationID, expected.Scope.TenantID, expected.Scope.Environment).Scan(&family, &revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = completeEndUserIdempotencyWithResponse(ctx, tx, record, target, nil); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if revoked == nil {
		if err = revokeEndUserSessions(ctx, tx, `token_family_id=$1`, []any{family}, "user_requested", now); err != nil {
			return err
		}
		if err = insertOutboxStrict(ctx, tx, event); err != nil {
			return err
		}
	}
	if err = completeEndUserIdempotencyWithResponse(ctx, tx, record, target, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockHostedSessionPair(ctx context.Context, tx pgx.Tx, left, right string) error {
	first, second := left, right
	if second < first {
		first, second = second, first
	}
	sessionIDs := []string{first}
	if second != first {
		sessionIDs = append(sessionIDs, second)
	}
	for _, sessionID := range sessionIDs {
		if sessionID == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "hosted-session-revoke|"+sessionID); err != nil {
			return err
		}
	}
	return nil
}

func validateHostedRevocationActor(ctx context.Context, tx pgx.Tx, expected identity.HostedSessionExpectation) error {
	var userID, productID, applicationID, environment, accountStatus string
	var tenantID *string
	var revokedAt *time.Time
	var accessExpiresAt, absoluteExpiresAt, databaseNow time.Time
	err := tx.QueryRow(ctx, `SELECT s.user_id,s.product_id,s.application_id,s.tenant_id,COALESCE(s.environment,''),s.revoked_at,s.access_expires_at,s.absolute_expires_at,u.account_status,clock_timestamp() FROM identity.end_user_sessions s JOIN identity.users u ON u.user_id=s.user_id WHERE s.session_id=$1 FOR UPDATE OF s`, expected.SessionID).Scan(&userID, &productID, &applicationID, &tenantID, &environment, &revokedAt, &accessExpiresAt, &absoluteExpiresAt, &accountStatus, &databaseNow)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrEndUserSessionExpired
	}
	if err != nil {
		return err
	}
	actualScope := identity.EndUserSessionScope{ProductID: productID, ApplicationID: applicationID, TenantID: tenantID, Environment: environment}
	if userID != expected.UserID || !sameHostedScope(actualScope, expected.Scope) {
		return identity.ErrEndUserScopeMismatch
	}
	if accountStatus != "active" {
		return identity.ErrEndUserAccountDisabled
	}
	if revokedAt != nil {
		return identity.ErrEndUserSessionRevoked
	}
	if !accessExpiresAt.After(databaseNow) || !absoluteExpiresAt.After(databaseNow) {
		return identity.ErrEndUserSessionExpired
	}
	return nil
}
