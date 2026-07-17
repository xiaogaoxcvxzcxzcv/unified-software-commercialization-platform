package postgres

import (
	"context"
	"crypto/hmac"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"platform.local/capability-platform/backend/internal/modules/identity"
)

func (r *Repository) CreateRegistrationVerificationIdempotent(ctx context.Context, challenge identity.RegistrationVerificationChallenge, record identity.EndUserIdempotency) (identity.RegistrationVerificationChallenge, bool, error) {
	if record.Operation != "registration_verification_start" || record.ScopeID == "" || len(record.ActorDigest) != 32 || len(record.KeyDigest) != 32 || len(record.RequestDigest) != 32 || record.ResourceID == "" || record.Now.IsZero() {
		return identity.RegistrationVerificationChallenge{}, false, errors.New("invalid registration verification idempotency record")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.RegistrationVerificationChallenge{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	recovered, err := beginEndUserIdempotency(ctx, tx, record)
	if err != nil {
		return identity.RegistrationVerificationChallenge{}, false, err
	}
	if recovered {
		var resourceID string
		if err := tx.QueryRow(ctx, `SELECT resource_id FROM identity.end_user_idempotency_records WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4`, record.Operation, record.ScopeID, record.ActorDigest, record.KeyDigest).Scan(&resourceID); err != nil {
			return identity.RegistrationVerificationChallenge{}, false, err
		}
		persisted, err := findRegistrationVerification(ctx, tx, resourceID)
		return persisted, true, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO identity.registration_verification_challenges(challenge_id,continuation_digest,product_id,application_id,tenant_id,identifier_type,identifier_digest,proof_digest,delivery_id,delivery_status,max_attempts,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending',$10,$11,$12)`, challenge.ChallengeID, challenge.ContinuationDigest, challenge.Scope.ProductID, challenge.Scope.ApplicationID, challenge.Scope.TenantID, challenge.IdentifierType, challenge.IdentifierDigest, challenge.ProofDigest, challenge.DeliveryID, challenge.MaxAttempts, challenge.CreatedAt, challenge.ExpiresAt)
	if err != nil {
		return identity.RegistrationVerificationChallenge{}, false, err
	}
	if err := completeEndUserIdempotency(ctx, tx, record, challenge.ChallengeID); err != nil {
		return identity.RegistrationVerificationChallenge{}, false, err
	}
	return challenge, false, tx.Commit(ctx)
}

func (r *Repository) ActivateRegistrationVerification(ctx context.Context, challengeID string) error {
	result, err := r.pool.Exec(ctx, `UPDATE identity.registration_verification_challenges SET delivery_status='active' WHERE challenge_id=$1 AND delivery_status IN ('pending','active')`, challengeID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrRegistrationVerificationInvalid
	}
	return nil
}

func (r *Repository) ConsumeRegistrationVerification(ctx context.Context, scope identity.EndUserSessionScope, identifierType identity.IdentifierType, identifierDigest, continuationDigest, proofDigest, consumerKeyDigest, consumerRequestDigest []byte, now time.Time) error {
	if len(consumerKeyDigest) != 32 || len(consumerRequestDigest) != 32 {
		return identity.ErrRegistrationVerificationInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var challengeID string
	var storedProof []byte
	var attempts, maximum int
	var expires time.Time
	var consumed *time.Time
	var storedConsumerKey, storedConsumerRequest []byte
	err = tx.QueryRow(ctx, `SELECT challenge_id,proof_digest,attempt_count,max_attempts,expires_at,consumed_at,consumer_key_digest,consumer_request_digest FROM identity.registration_verification_challenges WHERE product_id=$1 AND application_id=$2 AND tenant_id IS NOT DISTINCT FROM $3 AND identifier_type=$4 AND identifier_digest=$5 AND continuation_digest=$6 AND delivery_status='active' FOR UPDATE`, scope.ProductID, scope.ApplicationID, scope.TenantID, identifierType, identifierDigest, continuationDigest).Scan(&challengeID, &storedProof, &attempts, &maximum, &expires, &consumed, &storedConsumerKey, &storedConsumerRequest)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrRegistrationVerificationInvalid
	}
	if err != nil {
		return err
	}
	if consumed != nil {
		if hmac.Equal(storedConsumerKey, consumerKeyDigest) && hmac.Equal(storedConsumerRequest, consumerRequestDigest) {
			return tx.Commit(ctx)
		}
		return identity.ErrRegistrationVerificationReplayed
	}
	if !expires.After(now) {
		return identity.ErrRegistrationVerificationExpired
	}
	if attempts >= maximum || !hmac.Equal(storedProof, proofDigest) {
		if attempts < maximum {
			if _, err := tx.Exec(ctx, `UPDATE identity.registration_verification_challenges SET attempt_count=attempt_count+1 WHERE challenge_id=$1`, challengeID); err != nil {
				return err
			}
			if err := tx.Commit(ctx); err != nil {
				return err
			}
		}
		return identity.ErrRegistrationVerificationInvalid
	}
	result, err := tx.Exec(ctx, `UPDATE identity.registration_verification_challenges SET attempt_count=attempt_count+1,consumed_at=$2,consumer_key_digest=$3,consumer_request_digest=$4 WHERE challenge_id=$1 AND consumed_at IS NULL`, challengeID, now, consumerKeyDigest, consumerRequestDigest)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrRegistrationVerificationReplayed
	}
	return tx.Commit(ctx)
}

func findRegistrationVerification(ctx context.Context, tx pgx.Tx, challengeID string) (identity.RegistrationVerificationChallenge, error) {
	var challenge identity.RegistrationVerificationChallenge
	err := tx.QueryRow(ctx, `SELECT challenge_id,continuation_digest,product_id,application_id,tenant_id,identifier_type,identifier_digest,proof_digest,delivery_id,delivery_status,attempt_count,max_attempts,created_at,expires_at,consumed_at FROM identity.registration_verification_challenges WHERE challenge_id=$1`, challengeID).Scan(&challenge.ChallengeID, &challenge.ContinuationDigest, &challenge.Scope.ProductID, &challenge.Scope.ApplicationID, &challenge.Scope.TenantID, &challenge.IdentifierType, &challenge.IdentifierDigest, &challenge.ProofDigest, &challenge.DeliveryID, &challenge.DeliveryStatus, &challenge.AttemptCount, &challenge.MaxAttempts, &challenge.CreatedAt, &challenge.ExpiresAt, &challenge.ConsumedAt)
	return challenge, err
}
