package postgres

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"platform.local/capability-platform/backend/internal/modules/identity"
)

func (r *Repository) CreateExternalAuthFlow(ctx context.Context, flow identity.ExternalAuthFlow) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO identity.external_auth_flows(flow_id,product_id,application_id,tenant_id,environment,provider,provider_application_ref,mode,return_target_code,return_target_uri,return_target_policy_version,state_digest,nonce_digest,pkce_challenge_digest,browser_session_digest,status,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'pending',$16,$17)`, flow.FlowID, flow.Scope.ProductID, flow.Scope.ApplicationID, flow.Scope.TenantID, flow.Environment, flow.Provider, flow.ProviderApplicationRef, flow.Mode, flow.ReturnTargetCode, flow.ReturnTargetURI, flow.ReturnTargetPolicyVersion, flow.StateDigest, flow.NonceDigest, nullableExternalBytes(flow.PKCEChallengeDigest), nullableExternalBytes(flow.BrowserSessionDigest), flow.CreatedAt, flow.ExpiresAt)
	return err
}

func (r *Repository) FindExternalAuthFlow(ctx context.Context, flowID string) (identity.ExternalAuthFlow, error) {
	var flow identity.ExternalAuthFlow
	err := scanExternalAuthFlow(r.pool.QueryRow(ctx, externalAuthFlowSelect+` WHERE flow_id=$1`, flowID), &flow)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ExternalAuthFlow{}, identity.ErrNotFound
	}
	return flow, err
}

func (r *Repository) ClaimExternalAuthFlow(ctx context.Context, claim identity.ExternalAuthFlowClaim) (identity.ExternalAuthFlow, error) {
	if len(claim.StateDigest) != 32 || len(claim.BrowserSessionDigest) != 32 || len(claim.ProcessingTokenDigest) != 32 || claim.FlowID == "" || claim.Provider == "" || claim.Now.IsZero() || !claim.ProcessingExpiresAt.After(claim.Now) {
		return identity.ExternalAuthFlow{}, identity.ErrExternalAuthFlowInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.ExternalAuthFlow{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	flow, err := lockExternalFlow(ctx, tx, claim.FlowID)
	if err != nil {
		return identity.ExternalAuthFlow{}, err
	}
	if flow.Provider != claim.Provider || !hmac.Equal(flow.StateDigest, claim.StateDigest) || !hmac.Equal(flow.BrowserSessionDigest, claim.BrowserSessionDigest) || (claim.ExpectedScope != nil && !claim.ExpectedScope.Matches(identity.EndUserSession{ProductID: flow.Scope.ProductID, ApplicationID: flow.Scope.ApplicationID, TenantID: flow.Scope.TenantID, Environment: flow.Environment})) {
		return identity.ExternalAuthFlow{}, identity.ErrExternalAuthFlowInvalid
	}
	if flow.Status == "expired" {
		return identity.ExternalAuthFlow{}, identity.ErrExternalAuthFlowExpired
	}
	if flow.Status == "processing" {
		if flow.ProcessingExpiresAt != nil && !flow.ProcessingExpiresAt.After(claim.Now) {
			if err := terminalizeExternalFlow(ctx, tx, flow.FlowID, "processing", claim.Now, "EXTERNAL_PROCESSING_EXPIRED"); err != nil {
				return identity.ExternalAuthFlow{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return identity.ExternalAuthFlow{}, err
			}
			return identity.ExternalAuthFlow{}, identity.ErrExternalAuthFlowExpired
		}
		return identity.ExternalAuthFlow{}, identity.ErrExternalAuthFlowReplayed
	}
	if flow.Status != "pending" {
		return identity.ExternalAuthFlow{}, identity.ErrExternalAuthFlowReplayed
	}
	if !flow.ExpiresAt.After(claim.Now) {
		if err := terminalizeExternalFlow(ctx, tx, flow.FlowID, "pending", claim.Now, "EXTERNAL_FLOW_EXPIRED"); err != nil {
			return identity.ExternalAuthFlow{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return identity.ExternalAuthFlow{}, err
		}
		return identity.ExternalAuthFlow{}, identity.ErrExternalAuthFlowExpired
	}
	processingExpiresAt := claim.ProcessingExpiresAt
	if flow.ExpiresAt.Before(processingExpiresAt) {
		processingExpiresAt = flow.ExpiresAt
	}
	result, err := tx.Exec(ctx, `UPDATE identity.external_auth_flows SET status='processing',processing_token_digest=$2,processing_expires_at=$3 WHERE flow_id=$1 AND status='pending'`, flow.FlowID, claim.ProcessingTokenDigest, processingExpiresAt)
	if err != nil {
		return identity.ExternalAuthFlow{}, err
	}
	if result.RowsAffected() != 1 {
		return identity.ExternalAuthFlow{}, identity.ErrExternalAuthFlowReplayed
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.ExternalAuthFlow{}, err
	}
	flow.Status, flow.ProcessingTokenDigest, flow.ProcessingExpiresAt = "processing", append([]byte(nil), claim.ProcessingTokenDigest...), &processingExpiresAt
	return flow, nil
}

func (r *Repository) ConsumeExternalAuthFlowWithProof(ctx context.Context, flowID string, stateDigest, codeDigest []byte, proof identity.ExternalIdentityProof, now time.Time, event identity.OutboxEvent) error {
	if len(codeDigest) != 32 {
		return identity.ErrExternalAuthFlowInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	flow, err := lockAndValidateClaimedExternalFlow(ctx, tx, flowID, stateDigest, now)
	if err != nil {
		if errors.Is(err, identity.ErrExternalAuthFlowExpired) {
			return terminalizeExpiredClaim(ctx, tx, flowID, now)
		}
		return err
	}
	if !flow.Scope.Matches(identity.EndUserSession{ProductID: proof.Scope.ProductID, ApplicationID: proof.Scope.ApplicationID, TenantID: proof.Scope.TenantID, Environment: proof.Scope.Environment}) || flow.Provider != proof.Provider || flow.ProviderApplicationRef != proof.ProviderApplicationRef {
		return identity.ErrExternalAuthFlowInvalid
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity.external_identity_proofs(proof_id,flow_id,product_id,application_id,tenant_id,environment,provider,provider_application_ref,subject_digest,subject_masked,union_subject_digest,proof_digest,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, proof.ProofID, proof.FlowID, proof.Scope.ProductID, proof.Scope.ApplicationID, proof.Scope.TenantID, flow.Environment, proof.Provider, proof.ProviderApplicationRef, proof.SubjectDigest, proof.SubjectMasked, nullableExternalBytes(proof.UnionSubjectDigest), proof.ProofDigest, proof.CreatedAt, proof.ExpiresAt)
	if err != nil {
		return err
	}
	if err := consumeExternalFlow(ctx, tx, flowID, now, "consumed", "", codeDigest); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ConsumeExternalAuthFlowWithSession(ctx context.Context, flowID string, stateDigest, codeDigest []byte, session identity.NewEndUserSession, now time.Time) error {
	if len(codeDigest) != 32 || session.Session.ExternalIdentityID == nil || *session.Session.ExternalIdentityID == "" {
		return identity.ErrExternalAuthFlowInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	flow, err := lockAndValidateClaimedExternalFlow(ctx, tx, flowID, stateDigest, now)
	if err != nil {
		if errors.Is(err, identity.ErrExternalAuthFlowExpired) {
			return terminalizeExpiredClaim(ctx, tx, flowID, now)
		}
		return err
	}
	if !flow.Scope.Matches(session.Session) {
		return identity.ErrEndUserScopeMismatch
	}
	var accountStatus string
	if err := tx.QueryRow(ctx, `SELECT account_status FROM identity.users WHERE user_id=$1 FOR UPDATE`, session.Session.UserID).Scan(&accountStatus); err != nil {
		return err
	}
	if accountStatus != "active" {
		return identity.ErrEndUserAccountDisabled
	}
	var externalUserID, externalProvider, externalProviderApplication, externalStatus string
	if err := tx.QueryRow(ctx, `SELECT user_id,provider,provider_application_id,status FROM identity.external_identities WHERE external_identity_id=$1 FOR UPDATE`, *session.Session.ExternalIdentityID).Scan(&externalUserID, &externalProvider, &externalProviderApplication, &externalStatus); err != nil {
		return err
	}
	if externalUserID != session.Session.UserID || externalProvider != flow.Provider || externalProviderApplication != flow.ProviderApplicationRef || externalStatus != "active" {
		return identity.ErrExternalAuthFlowInvalid
	}
	if err := insertNewEndUserSession(ctx, tx, session); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, session.OutboxEvent); err != nil {
		return err
	}
	if err := consumeExternalFlow(ctx, tx, flowID, now, "consumed", "", codeDigest); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ConsumeExternalAuthFlowFailure(ctx context.Context, flowID string, stateDigest []byte, failureCode string, now time.Time, event identity.OutboxEvent) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = lockAndValidateClaimedExternalFlow(ctx, tx, flowID, stateDigest, now)
	if err != nil {
		if errors.Is(err, identity.ErrExternalAuthFlowExpired) {
			return terminalizeExpiredClaim(ctx, tx, flowID, now)
		}
		return err
	}
	if err := consumeExternalFlow(ctx, tx, flowID, now, "failed", failureCode, nil); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) ConsumeExternalIdentityProofAndLink(ctx context.Context, proofDigest []byte, scope identity.EndUserSessionScope, provider string, value identity.ExternalIdentity, now time.Time, record identity.EndUserIdempotency) (identity.ExternalIdentity, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.ExternalIdentity{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	recovered, err := beginEndUserIdempotency(ctx, tx, record)
	if err != nil {
		return identity.ExternalIdentity{}, false, err
	}
	if recovered {
		var responseDocument []byte
		if err := tx.QueryRow(ctx, `SELECT response_document FROM identity.end_user_idempotency_records WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest).Scan(&responseDocument); err != nil {
			return identity.ExternalIdentity{}, false, err
		}
		var response externalIdentityLinkResponse
		if len(responseDocument) == 0 || json.Unmarshal(responseDocument, &response) != nil {
			return identity.ExternalIdentity{}, false, identity.ErrEndUserVersionConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return identity.ExternalIdentity{}, false, err
		}
		return response.identity(), true, nil
	}
	var proof identity.ExternalIdentityProof
	err = tx.QueryRow(ctx, `SELECT proof_id,flow_id,product_id,application_id,tenant_id,environment,provider,provider_application_ref,subject_digest,subject_masked,union_subject_digest,proof_digest,created_at,expires_at,consumed_at FROM identity.external_identity_proofs WHERE proof_digest=$1 FOR UPDATE`, proofDigest).Scan(&proof.ProofID, &proof.FlowID, &proof.Scope.ProductID, &proof.Scope.ApplicationID, &proof.Scope.TenantID, &proof.Scope.Environment, &proof.Provider, &proof.ProviderApplicationRef, &proof.SubjectDigest, &proof.SubjectMasked, &proof.UnionSubjectDigest, &proof.ProofDigest, &proof.CreatedAt, &proof.ExpiresAt, &proof.ConsumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ExternalIdentity{}, false, identity.ErrExternalProofInvalid
	}
	if err != nil {
		return identity.ExternalIdentity{}, false, err
	}
	if proof.ConsumedAt != nil {
		return identity.ExternalIdentity{}, false, identity.ErrExternalProofReplayed
	}
	if !proof.ExpiresAt.After(now) {
		return identity.ExternalIdentity{}, false, identity.ErrExternalProofExpired
	}
	if proof.Provider != provider || !scope.Matches(identity.EndUserSession{ProductID: proof.Scope.ProductID, ApplicationID: proof.Scope.ApplicationID, TenantID: proof.Scope.TenantID, Environment: proof.Scope.Environment}) {
		return identity.ExternalIdentity{}, false, identity.ErrExternalProofInvalid
	}
	var accountStatus string
	if err := tx.QueryRow(ctx, `SELECT account_status FROM identity.users WHERE user_id=$1 FOR UPDATE`, value.UserID).Scan(&accountStatus); err != nil {
		return identity.ExternalIdentity{}, false, err
	}
	if accountStatus != "active" {
		return identity.ExternalIdentity{}, false, identity.ErrEndUserAccountDisabled
	}
	value.ProviderApplicationID = proof.ProviderApplicationRef
	value.SubjectDigest = proof.SubjectDigest
	value.UnionSubjectDigest = proof.UnionSubjectDigest
	value.SubjectMasked = proof.SubjectMasked
	_, err = tx.Exec(ctx, `INSERT INTO identity.external_identities(external_identity_id,user_id,provider,provider_application_id,subject_digest,subject_masked,union_subject_digest,status,identity_version,linked_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,'active',1,$8,$8)`, value.ExternalIdentityID, value.UserID, provider, value.ProviderApplicationID, value.SubjectDigest, value.SubjectMasked, nullableExternalBytes(value.UnionSubjectDigest), now)
	if isUniqueViolation(err) {
		return identity.ExternalIdentity{}, false, identity.ErrExternalIdentityConflict
	}
	if err != nil {
		return identity.ExternalIdentity{}, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.external_identity_proofs SET consumed_at=$2 WHERE proof_id=$1 AND consumed_at IS NULL`, proof.ProofID, now); err != nil {
		return identity.ExternalIdentity{}, false, err
	}
	if err := insertOutboxStrict(ctx, tx, value.OutboxEvent); err != nil {
		return identity.ExternalIdentity{}, false, err
	}
	value.Status, value.Version, value.LinkedAt, value.UpdatedAt = "active", 1, now, now
	responseDocument, err := json.Marshal(externalIdentityLinkResponseFrom(value))
	if err != nil {
		return identity.ExternalIdentity{}, false, err
	}
	if err := completeEndUserIdempotencyWithResponse(ctx, tx, record, value.ExternalIdentityID, responseDocument); err != nil {
		return identity.ExternalIdentity{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return identity.ExternalIdentity{}, false, err
	}
	return externalIdentityLinkResponseFrom(value).identity(), false, nil
}

type externalIdentityLinkResponse struct {
	ExternalIdentityID    string    `json:"external_identity_id"`
	UserID                string    `json:"user_id"`
	Provider              string    `json:"provider"`
	ProviderApplicationID string    `json:"provider_application_id"`
	SubjectMasked         string    `json:"subject_masked"`
	Status                string    `json:"status"`
	Version               int64     `json:"version"`
	LinkedAt              time.Time `json:"linked_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

func externalIdentityLinkResponseFrom(value identity.ExternalIdentity) externalIdentityLinkResponse {
	return externalIdentityLinkResponse{ExternalIdentityID: value.ExternalIdentityID, UserID: value.UserID, Provider: value.Provider, ProviderApplicationID: value.ProviderApplicationID, SubjectMasked: value.SubjectMasked, Status: value.Status, Version: value.Version, LinkedAt: value.LinkedAt, UpdatedAt: value.UpdatedAt}
}

func (r externalIdentityLinkResponse) identity() identity.ExternalIdentity {
	return identity.ExternalIdentity{ExternalIdentityID: r.ExternalIdentityID, UserID: r.UserID, Provider: r.Provider, ProviderApplicationID: r.ProviderApplicationID, SubjectMasked: r.SubjectMasked, Status: r.Status, Version: r.Version, LinkedAt: r.LinkedAt, UpdatedAt: r.UpdatedAt}
}

func (r *Repository) ListExternalIdentities(ctx context.Context, userID string) ([]identity.ExternalIdentity, error) {
	rows, err := r.pool.Query(ctx, `SELECT external_identity_id,user_id,provider,provider_application_id,subject_digest,subject_masked,union_subject_digest,status,identity_version,linked_at,updated_at FROM identity.external_identities WHERE user_id=$1 ORDER BY linked_at,external_identity_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []identity.ExternalIdentity
	for rows.Next() {
		var value identity.ExternalIdentity
		if err := rows.Scan(&value.ExternalIdentityID, &value.UserID, &value.Provider, &value.ProviderApplicationID, &value.SubjectDigest, &value.SubjectMasked, &value.UnionSubjectDigest, &value.Status, &value.Version, &value.LinkedAt, &value.UpdatedAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) UnlinkExternalIdentity(ctx context.Context, userID, externalIdentityID string, now time.Time, event identity.OutboxEvent) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var lockedUserID string
	err = tx.QueryRow(ctx, `SELECT user_id FROM identity.users WHERE user_id=$1 FOR UPDATE`, userID).Scan(&lockedUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrExternalIdentityNotOwned
	}
	if err != nil {
		return err
	}
	var ownedIdentityID string
	err = tx.QueryRow(ctx, `SELECT external_identity_id FROM identity.external_identities WHERE external_identity_id=$1 AND user_id=$2 AND status='active' FOR UPDATE`, externalIdentityID, userID).Scan(&ownedIdentityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrExternalIdentityNotOwned
	}
	if err != nil {
		return err
	}
	var loginMethods int
	err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM identity.user_credentials WHERE user_id=$1 AND credential_type='password' AND credential_status='active') + (SELECT count(*) FROM identity.external_identities WHERE user_id=$1 AND status='active')`, userID).Scan(&loginMethods)
	if err != nil {
		return err
	}
	if loginMethods <= 1 {
		return identity.ErrExternalIdentityLastLogin
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.external_identities SET status='revoked',identity_version=identity_version+1,revoked_at=$3,updated_at=$3 WHERE external_identity_id=$1 AND user_id=$2 AND status='active'`, externalIdentityID, userID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.end_user_sessions SET revoked_at=COALESCE(revoked_at,$3),revoke_reason=COALESCE(revoke_reason,'external_identity_unlinked'),last_seen_at=$3 WHERE user_id=$1 AND external_identity_id=$2`, userID, externalIdentityID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.end_user_session_tokens t SET revoked_at=COALESCE(t.revoked_at,$3) FROM identity.end_user_sessions s WHERE t.session_id=s.session_id AND s.user_id=$1 AND s.external_identity_id=$2`, userID, externalIdentityID, now); err != nil {
		return err
	}
	if err := insertOutboxStrict(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const externalAuthFlowSelect = `SELECT flow_id,product_id,application_id,tenant_id,environment,provider,provider_application_ref,mode,return_target_code,return_target_uri,return_target_policy_version,state_digest,nonce_digest,pkce_challenge_digest,browser_session_digest,authorization_code_digest,processing_token_digest,processing_expires_at,status,created_at,expires_at,consumed_at,failure_code FROM identity.external_auth_flows`

func scanExternalAuthFlow(row pgx.Row, flow *identity.ExternalAuthFlow) error {
	if err := row.Scan(&flow.FlowID, &flow.Scope.ProductID, &flow.Scope.ApplicationID, &flow.Scope.TenantID, &flow.Environment, &flow.Provider, &flow.ProviderApplicationRef, &flow.Mode, &flow.ReturnTargetCode, &flow.ReturnTargetURI, &flow.ReturnTargetPolicyVersion, &flow.StateDigest, &flow.NonceDigest, &flow.PKCEChallengeDigest, &flow.BrowserSessionDigest, &flow.AuthorizationCodeDigest, &flow.ProcessingTokenDigest, &flow.ProcessingExpiresAt, &flow.Status, &flow.CreatedAt, &flow.ExpiresAt, &flow.ConsumedAt, &flow.FailureCode); err != nil {
		return err
	}
	flow.Scope.Environment = flow.Environment
	return nil
}

func lockExternalFlow(ctx context.Context, tx pgx.Tx, flowID string) (identity.ExternalAuthFlow, error) {
	var flow identity.ExternalAuthFlow
	err := scanExternalAuthFlow(tx.QueryRow(ctx, externalAuthFlowSelect+` WHERE flow_id=$1 FOR UPDATE`, flowID), &flow)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ExternalAuthFlow{}, identity.ErrExternalAuthFlowInvalid
	}
	return flow, err
}

func lockAndValidateClaimedExternalFlow(ctx context.Context, tx pgx.Tx, flowID string, claimDigest []byte, now time.Time) (identity.ExternalAuthFlow, error) {
	if len(claimDigest) != 32 {
		return identity.ExternalAuthFlow{}, identity.ErrExternalAuthFlowInvalid
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "external-claim|"+hex.EncodeToString(claimDigest)); err != nil {
		return identity.ExternalAuthFlow{}, err
	}
	flow, err := lockExternalFlow(ctx, tx, flowID)
	if err != nil {
		return identity.ExternalAuthFlow{}, err
	}
	if flow.Status != "processing" || !hmac.Equal(flow.ProcessingTokenDigest, claimDigest) {
		return identity.ExternalAuthFlow{}, identity.ErrExternalAuthFlowReplayed
	}
	if !flow.ExpiresAt.After(now) || flow.ProcessingExpiresAt == nil || !flow.ProcessingExpiresAt.After(now) {
		return identity.ExternalAuthFlow{}, identity.ErrExternalAuthFlowExpired
	}
	return flow, nil
}

func consumeExternalFlow(ctx context.Context, tx pgx.Tx, flowID string, now time.Time, status, failureCode string, codeDigest []byte) error {
	var failure any
	if failureCode != "" {
		failure = failureCode
	}
	result, err := tx.Exec(ctx, `UPDATE identity.external_auth_flows SET status=$2,consumed_at=$3,failure_code=$4,authorization_code_digest=$5,processing_token_digest=NULL,processing_expires_at=NULL WHERE flow_id=$1 AND status='processing'`, flowID, status, now, failure, nullableExternalBytes(codeDigest))
	if isUniqueViolation(err) {
		return identity.ErrExternalAuthFlowReplayed
	}
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrExternalAuthFlowReplayed
	}
	return nil
}

func terminalizeExternalFlow(ctx context.Context, tx pgx.Tx, flowID, expectedStatus string, now time.Time, failureCode string) error {
	result, err := tx.Exec(ctx, `UPDATE identity.external_auth_flows SET status='expired',consumed_at=$3,failure_code=$4,authorization_code_digest=NULL,processing_token_digest=NULL,processing_expires_at=NULL WHERE flow_id=$1 AND status=$2`, flowID, expectedStatus, now, failureCode)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrExternalAuthFlowReplayed
	}
	return nil
}

func terminalizeExpiredClaim(ctx context.Context, tx pgx.Tx, flowID string, now time.Time) error {
	if err := terminalizeExternalFlow(ctx, tx, flowID, "processing", now, "EXTERNAL_PROCESSING_EXPIRED"); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return identity.ErrExternalAuthFlowExpired
}

func nullableExternalBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
