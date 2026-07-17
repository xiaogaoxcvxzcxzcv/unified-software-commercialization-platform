package postgres

import (
	"context"
	"crypto/hmac"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"platform.local/capability-platform/backend/internal/modules/identity"
)

func (r *Repository) CreateHostedAuthProofAndClearFailures(ctx context.Context, proof identity.HostedAuthProof, scopeID string, identifierDigest []byte) (identity.HostedAuthProof, error) {
	if proof.TTL <= 0 {
		return identity.HostedAuthProof{}, identity.ErrHostedAuthProofInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.HostedAuthProof{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&proof.CreatedAt); err != nil {
		return identity.HostedAuthProof{}, err
	}
	proof.ExpiresAt = proof.CreatedAt.Add(proof.TTL)
	proof.OutboxEvent.Now = proof.CreatedAt
	proof.OutboxEvent.Payload.OccurredAt = proof.CreatedAt
	if _, err = tx.Exec(ctx, `INSERT INTO identity.hosted_auth_proofs(
		proof_id,user_id,product_id,application_id,tenant_id,environment,authentication_method,risk_summary_digest,created_at,expires_at
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, proof.ProofID, proof.UserID, proof.Scope.ProductID, proof.Scope.ApplicationID, proof.Scope.TenantID, proof.Scope.Environment, proof.AuthenticationMethod, proof.RiskSummaryDigest, proof.CreatedAt, proof.ExpiresAt); err != nil {
		return identity.HostedAuthProof{}, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM identity.end_user_login_failures WHERE scope_id=$1 AND identifier_digest=$2`, scopeID, identifierDigest); err != nil {
		return identity.HostedAuthProof{}, err
	}
	if err = insertOutboxStrict(ctx, tx, proof.OutboxEvent); err != nil {
		return identity.HostedAuthProof{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return identity.HostedAuthProof{}, err
	}
	return proof, nil
}

func (r *Repository) RedeemHostedAuthGrant(ctx context.Context, record identity.HostedAuthGrantRedemption) (identity.EndUserSession, bool, error) {
	if record.Session.Session.UserID != "" || !record.Scope.Matches(record.Session.Session) {
		return identity.EndUserSession{}, false, identity.ErrEndUserScopeMismatch
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.EndUserSession{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "hosted-auth-grant|"+record.GrantID); err != nil {
		return identity.EndUserSession{}, false, err
	}
	var existingProofID, existingSessionID string
	var existingRequestDigest []byte
	err = tx.QueryRow(ctx, `SELECT proof_id,request_digest,session_id FROM identity.hosted_grant_redemptions WHERE grant_id=$1`, record.GrantID).Scan(&existingProofID, &existingRequestDigest, &existingSessionID)
	if err == nil {
		if existingProofID != record.ProofID || !hmac.Equal(existingRequestDigest, record.RequestDigest) {
			return identity.EndUserSession{}, false, identity.ErrHostedAuthGrantConflict
		}
		session, findErr := findEndUserSessionByID(ctx, tx, existingSessionID)
		if findErr != nil {
			return identity.EndUserSession{}, false, findErr
		}
		if !record.Scope.Matches(session) {
			return identity.EndUserSession{}, false, identity.ErrEndUserScopeMismatch
		}
		var databaseNow time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
			return identity.EndUserSession{}, false, err
		}
		if session.AccountStatus != "active" {
			return identity.EndUserSession{}, false, identity.ErrEndUserAccountDisabled
		}
		if session.RevokedAt != nil {
			return identity.EndUserSession{}, false, identity.ErrEndUserSessionRevoked
		}
		if !session.AccessExpiresAt.After(databaseNow) || !session.AbsoluteExpiresAt.After(databaseNow) {
			return identity.EndUserSession{}, false, identity.ErrEndUserSessionExpired
		}
		if err = tx.Commit(ctx); err != nil {
			return identity.EndUserSession{}, false, err
		}
		return session, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserSession{}, false, err
	}

	var userID, productID, applicationID, environment, method, accountStatus string
	var tenantID, consumedByGrantID *string
	var riskDigest []byte
	var createdAt, expiresAt time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT p.user_id,p.product_id,p.application_id,p.tenant_id,COALESCE(p.environment,''),p.authentication_method,p.risk_summary_digest,p.created_at,p.expires_at,p.consumed_by_grant_id,p.consumed_at,u.account_status
		FROM identity.hosted_auth_proofs p JOIN identity.users u ON u.user_id=p.user_id
		WHERE p.proof_id=$1 FOR UPDATE OF p,u`, record.ProofID).Scan(&userID, &productID, &applicationID, &tenantID, &environment, &method, &riskDigest, &createdAt, &expiresAt, &consumedByGrantID, &consumedAt, &accountStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserSession{}, false, identity.ErrHostedAuthProofInvalid
	}
	if err != nil {
		return identity.EndUserSession{}, false, err
	}
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return identity.EndUserSession{}, false, err
	}
	proofScope := identity.EndUserSessionScope{ProductID: productID, ApplicationID: applicationID, TenantID: tenantID, Environment: environment}
	if !sameHostedScope(proofScope, record.Scope) {
		return identity.EndUserSession{}, false, identity.ErrEndUserScopeMismatch
	}
	if consumedAt != nil || consumedByGrantID != nil {
		if consumedByGrantID != nil && *consumedByGrantID == record.GrantID {
			return identity.EndUserSession{}, false, identity.ErrHostedAuthGrantConflict
		}
		return identity.EndUserSession{}, false, identity.ErrHostedAuthProofReplayed
	}
	if !expiresAt.After(databaseNow) {
		return identity.EndUserSession{}, false, identity.ErrHostedAuthProofExpired
	}
	if accountStatus != "active" {
		return identity.EndUserSession{}, false, identity.ErrEndUserAccountDisabled
	}
	if record.AccessTTL <= 0 || record.RefreshTTL <= 0 || record.AbsoluteTTL < record.RefreshTTL {
		return identity.EndUserSession{}, false, identity.ErrHostedAuthProofInvalid
	}

	value := record.Session
	value.Session.UserID = userID
	value.Session.AuthTime = createdAt
	value.Session.RiskSummaryDigest = riskDigest
	value.Session.AccountStatus = accountStatus
	value.Session.CreatedAt = databaseNow
	value.Session.LastSeenAt = databaseNow
	value.Session.AccessExpiresAt = databaseNow.Add(record.AccessTTL)
	value.Session.RefreshExpiresAt = databaseNow.Add(record.RefreshTTL)
	value.Session.AbsoluteExpiresAt = databaseNow.Add(record.AbsoluteTTL)
	value.AccessToken.CreatedAt = databaseNow
	value.AccessToken.ExpiresAt = value.Session.AccessExpiresAt
	value.RefreshToken.CreatedAt = databaseNow
	value.RefreshToken.ExpiresAt = value.Session.RefreshExpiresAt
	if method != "password" {
		value.Session.AuthenticationMethod = method
	}
	if err = insertNewEndUserSession(ctx, tx, value); err != nil {
		return identity.EndUserSession{}, false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE identity.hosted_auth_proofs SET consumed_by_grant_id=$2,consumed_at=$3 WHERE proof_id=$1 AND consumed_at IS NULL`, record.ProofID, record.GrantID, databaseNow); err != nil {
		return identity.EndUserSession{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO identity.hosted_grant_redemptions(grant_id,proof_id,request_digest,session_id,created_at) VALUES($1,$2,$3,$4,$5)`, record.GrantID, record.ProofID, record.RequestDigest, value.Session.SessionID, databaseNow); err != nil {
		return identity.EndUserSession{}, false, err
	}
	record.OutboxEvent.Now = databaseNow
	record.OutboxEvent.Payload.OccurredAt = databaseNow
	if err = insertOutboxStrict(ctx, tx, record.OutboxEvent); err != nil {
		return identity.EndUserSession{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return identity.EndUserSession{}, false, err
	}
	return value.Session, false, nil
}

func (r *Repository) ValidateHostedSession(ctx context.Context, expected identity.HostedSessionExpectation) error {
	var userID, productID, applicationID, environment, accountStatus string
	var tenantID *string
	var revokedAt *time.Time
	var accessExpiresAt, absoluteExpiresAt, databaseNow time.Time
	err := r.pool.QueryRow(ctx, `SELECT s.user_id,s.product_id,s.application_id,s.tenant_id,COALESCE(s.environment,''),s.revoked_at,s.access_expires_at,s.absolute_expires_at,u.account_status,clock_timestamp()
		FROM identity.end_user_sessions s JOIN identity.users u ON u.user_id=s.user_id
		WHERE s.session_id=$1`, expected.SessionID).Scan(&userID, &productID, &applicationID, &tenantID, &environment, &revokedAt, &accessExpiresAt, &absoluteExpiresAt, &accountStatus, &databaseNow)
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

func sameHostedScope(left, right identity.EndUserSessionScope) bool {
	return left.ProductID != "" && left.ApplicationID != "" && left.Environment != "" && left.ProductID == right.ProductID && left.ApplicationID == right.ApplicationID && left.Environment == right.Environment && nullableHostedStringEqual(left.TenantID, right.TenantID)
}

func nullableHostedStringEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

var _ identity.HostedAuthRepository = (*Repository)(nil)
