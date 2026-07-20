package postgres

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
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
	_, err := r.FindHostedSession(ctx, expected)
	return err
}

func (r *Repository) FindHostedSession(ctx context.Context, expected identity.HostedSessionExpectation) (identity.EndUserSession, error) {
	session, err := findEndUserSessionByID(ctx, r.pool, expected.SessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.EndUserSession{}, identity.ErrEndUserSessionExpired
	}
	if err != nil {
		return identity.EndUserSession{}, err
	}
	var databaseNow time.Time
	if err := r.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return identity.EndUserSession{}, err
	}
	if session.UserID != expected.UserID || !sameHostedScope(identity.EndUserSessionScope{ProductID: session.ProductID, ApplicationID: session.ApplicationID, TenantID: session.TenantID, Environment: session.Environment}, expected.Scope) {
		return identity.EndUserSession{}, identity.ErrEndUserScopeMismatch
	}
	if session.AccountStatus != "active" {
		return identity.EndUserSession{}, identity.ErrEndUserAccountDisabled
	}
	if session.RevokedAt != nil {
		return identity.EndUserSession{}, identity.ErrEndUserSessionRevoked
	}
	if !session.AccessExpiresAt.After(databaseNow) || !session.AbsoluteExpiresAt.After(databaseNow) {
		return identity.EndUserSession{}, identity.ErrEndUserSessionExpired
	}
	return session, nil
}

func (r *Repository) RecoverHostedRegistration(ctx context.Context, record identity.EndUserIdempotency) (identity.HostedRegistrationResponse, bool, error) {
	if record.Operation != "hosted_register" || record.ScopeID == "" || len(record.ActorDigest) != 32 || len(record.KeyDigest) != 32 || len(record.RequestDigest) != 32 {
		return identity.HostedRegistrationResponse{}, false, errors.New("invalid hosted registration idempotency lookup")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.HostedRegistrationResponse{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockEndUserIdempotency(ctx, tx, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest); err != nil {
		return identity.HostedRegistrationResponse{}, false, err
	}
	v, found, err := recoverHostedRegistration(ctx, tx, record)
	if err != nil || !found {
		return v, found, err
	}
	return v, true, tx.Commit(ctx)
}

func recoverHostedRegistration(ctx context.Context, tx pgx.Tx, record identity.EndUserIdempotency) (identity.HostedRegistrationResponse, bool, error) {
	var stored []byte
	var state string
	var response []byte
	err := tx.QueryRow(ctx, `SELECT request_digest,state,response_document FROM identity.end_user_idempotency_records WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest).Scan(&stored, &state, &response)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.HostedRegistrationResponse{}, false, nil
	}
	if err != nil {
		return identity.HostedRegistrationResponse{}, false, err
	}
	if !hmac.Equal(stored, record.RequestDigest) {
		return identity.HostedRegistrationResponse{}, false, identity.ErrEndUserVersionConflict
	}
	if state != "completed" {
		return identity.HostedRegistrationResponse{}, false, identity.ErrEndUserVersionConflict
	}
	var value identity.HostedRegistrationResponse
	if err = json.Unmarshal(response, &value); err != nil {
		return value, false, fmt.Errorf("decode hosted registration response: %w", err)
	}
	return value, true, nil
}

func (r *Repository) CreateHostedRegistration(ctx context.Context, record identity.HostedRegistrationRecord) (identity.HostedRegistrationResponse, bool, error) {
	if err := record.Registration.Validate(); err != nil {
		return identity.HostedRegistrationResponse{}, false, err
	}
	idem := record.Idempotency
	if idem.Operation != "hosted_register" || idem.ScopeID == "" || len(idem.ActorDigest) != 32 || len(idem.KeyDigest) != 32 || len(idem.RequestDigest) != 32 || idem.ResourceID != record.Proof.ProofID || idem.Now.IsZero() || record.Proof.TTL <= 0 || record.Proof.UserID != record.Registration.User.UserID {
		return identity.HostedRegistrationResponse{}, false, errors.New("invalid hosted registration record")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.HostedRegistrationResponse{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockEndUserIdempotency(ctx, tx, idem.Operation, idem.ScopeID, idem.ActorDigest, idem.KeyDigest); err != nil {
		return identity.HostedRegistrationResponse{}, false, err
	}
	if recovered, found, recoverErr := recoverHostedRegistration(ctx, tx, idem); recoverErr != nil || found {
		if recoverErr == nil {
			recoverErr = tx.Commit(ctx)
		}
		return recovered, found, recoverErr
	}
	if _, err = tx.Exec(ctx, `INSERT INTO identity.end_user_idempotency_records(operation,scope_id,actor_digest,key_digest,request_digest,resource_id,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'pending',$7,$7)`, idem.Operation, idem.ScopeID, idem.ActorDigest, idem.KeyDigest, idem.RequestDigest, idem.ResourceID, idem.Now); err != nil {
		return identity.HostedRegistrationResponse{}, false, err
	}
	if err = insertEndUserRegistration(ctx, tx, record.Registration); err != nil {
		return identity.HostedRegistrationResponse{}, false, err
	}
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&record.Proof.CreatedAt); err != nil {
		return identity.HostedRegistrationResponse{}, false, err
	}
	record.Proof.ExpiresAt = record.Proof.CreatedAt.Add(record.Proof.TTL)
	record.Proof.OutboxEvent.Now = record.Proof.CreatedAt
	record.Proof.OutboxEvent.Payload.OccurredAt = record.Proof.CreatedAt
	if _, err = tx.Exec(ctx, `INSERT INTO identity.hosted_auth_proofs(proof_id,user_id,product_id,application_id,tenant_id,environment,authentication_method,risk_summary_digest,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, record.Proof.ProofID, record.Proof.UserID, record.Proof.Scope.ProductID, record.Proof.Scope.ApplicationID, record.Proof.Scope.TenantID, record.Proof.Scope.Environment, record.Proof.AuthenticationMethod, record.Proof.RiskSummaryDigest, record.Proof.CreatedAt, record.Proof.ExpiresAt); err != nil {
		return identity.HostedRegistrationResponse{}, false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM identity.end_user_login_failures WHERE scope_id=$1 AND identifier_digest=$2`, idem.ScopeID, record.IdentifierDigest); err != nil {
		return identity.HostedRegistrationResponse{}, false, err
	}
	if err = insertOutboxStrict(ctx, tx, record.Registration.OutboxEvent); err != nil {
		return identity.HostedRegistrationResponse{}, false, err
	}
	if err = insertOutboxStrict(ctx, tx, record.Proof.OutboxEvent); err != nil {
		return identity.HostedRegistrationResponse{}, false, err
	}
	value := identity.HostedRegistrationResponse{Proof: record.Proof, Profile: record.Registration.Profile}
	response, err := json.Marshal(value)
	if err != nil {
		return value, false, err
	}
	if err = completeEndUserIdempotencyWithResponse(ctx, tx, idem, record.Proof.ProofID, response); err != nil {
		return value, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return value, false, err
	}
	return value, false, nil
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
var _ identity.HostedSelfServiceRepository = (*Repository)(nil)
