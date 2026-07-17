package postgres

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/hostedinteraction"
)

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

const interactionColumns = `interaction_id,route_id,product_id,application_id,tenant_id,environment,channel,initiator_kind,initiator_client_session_id,initiator_user_id,initiator_user_session_id,return_target_code,return_target_uri,return_target_policy_version,state_protector_key_ref,state_ciphertext,state_digest,nonce_digest,pkce_challenge_digest,pkce_method,locale,theme_variant,status,version,result_kind,failure_code,trace_id,created_at,expires_at,opened_at,completed_at,terminal_at,authentication_lease_digest,authentication_started_at,authentication_lease_expires_at`

func (r *Repository) Create(ctx context.Context, record hostedinteraction.CreateRecord) (hostedinteraction.Interaction, bool, error) {
	if r == nil || r.pool == nil {
		return hostedinteraction.Interaction{}, false, hostedinteraction.ErrTemporarilyUnavailable
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return hostedinteraction.Interaction{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "hosted-idem|"+hex.EncodeToString(record.ActorDigest)+"|"+hex.EncodeToString(record.KeyDigest)); err != nil {
		return hostedinteraction.Interaction{}, false, err
	}
	var existingID string
	var existingDigest []byte
	err = tx.QueryRow(ctx, `SELECT interaction_id,request_digest FROM hosted_interaction.idempotency_records WHERE operation=$1 AND actor_digest=$2 AND key_digest=$3`, record.Operation, record.ActorDigest, record.KeyDigest).Scan(&existingID, &existingDigest)
	if err == nil {
		if !hmac.Equal(existingDigest, record.RequestDigest) {
			return hostedinteraction.Interaction{}, false, hostedinteraction.ErrIdempotencyConflict
		}
		value, getErr := getInteraction(ctx, tx, existingID, false)
		if getErr != nil {
			return hostedinteraction.Interaction{}, false, getErr
		}
		if err = tx.Commit(ctx); err != nil {
			return hostedinteraction.Interaction{}, false, err
		}
		return value, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return hostedinteraction.Interaction{}, false, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return hostedinteraction.Interaction{}, false, err
	}
	ttl := record.Interaction.ExpiresAt.Sub(record.Interaction.CreatedAt)
	record.Interaction.CreatedAt = now
	record.Interaction.ExpiresAt = now.Add(ttl)
	// Service clocks are advisory only; preserve its requested TTL while anchoring facts to PostgreSQL.
	if !record.Interaction.ExpiresAt.After(now) {
		record.Interaction.ExpiresAt = now.Add(10 * time.Minute)
	}
	if err = insertInteraction(ctx, tx, record.Interaction); err != nil {
		if unique(err) {
			return hostedinteraction.Interaction{}, false, hostedinteraction.ErrIdempotencyConflict
		}
		return hostedinteraction.Interaction{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO hosted_interaction.idempotency_records(operation,actor_digest,key_digest,request_digest,interaction_id,response_document,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, record.Operation, record.ActorDigest, record.KeyDigest, record.RequestDigest, record.Interaction.InteractionID, record.Response, now)
	if err != nil {
		if unique(err) {
			return hostedinteraction.Interaction{}, false, hostedinteraction.ErrIdempotencyConflict
		}
		return hostedinteraction.Interaction{}, false, err
	}
	record.Event.OccurredAt = now
	if err = insertOutbox(ctx, tx, record.Event); err != nil {
		return hostedinteraction.Interaction{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return hostedinteraction.Interaction{}, false, err
	}
	return record.Interaction, false, nil
}

func (r *Repository) Get(ctx context.Context, interactionID string) (hostedinteraction.Interaction, error) {
	if r == nil || r.pool == nil {
		return hostedinteraction.Interaction{}, hostedinteraction.ErrTemporarilyUnavailable
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return hostedinteraction.Interaction{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	value, err := getInteraction(ctx, tx, interactionID, true)
	if err != nil {
		return hostedinteraction.Interaction{}, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return hostedinteraction.Interaction{}, err
	}
	due, err := interactionDue(ctx, tx, value, now)
	if err != nil {
		return hostedinteraction.Interaction{}, err
	}
	if !terminal(value.Status) && due {
		value, err = expireLocked(ctx, tx, value, now)
		if err != nil {
			return hostedinteraction.Interaction{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return hostedinteraction.Interaction{}, err
	}
	return value, nil
}

func (r *Repository) GetForScope(ctx context.Context, interactionID string, scope hostedinteraction.Scope, actor hostedinteraction.Actor) (hostedinteraction.Interaction, error) {
	value, err := r.Get(ctx, interactionID)
	if err != nil {
		return hostedinteraction.Interaction{}, err
	}
	if !value.Scope.Matches(scope) || value.Actor.Kind != actor.Kind ||
		(value.Route == hostedinteraction.RouteAccount && (value.Actor.UserID != actor.UserID || value.Actor.UserSessionID != actor.UserSessionID)) {
		return hostedinteraction.Interaction{}, hostedinteraction.ErrInvalidArgument
	}
	return value, nil
}

func (r *Repository) OpenBrowserSession(ctx context.Context, record hostedinteraction.OpenBrowserRecord) (hostedinteraction.Interaction, time.Time, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	value, err := getInteraction(ctx, tx, record.InteractionID, true)
	if err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	due, err := interactionDue(ctx, tx, value, now)
	if err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	if due {
		if _, err = expireLocked(ctx, tx, value, now); err != nil {
			return hostedinteraction.Interaction{}, time.Time{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return hostedinteraction.Interaction{}, time.Time{}, err
		}
		return hostedinteraction.Interaction{}, time.Time{}, hostedinteraction.ErrInteractionExpired
	}
	if value.Status == hostedinteraction.StatusAuthenticating {
		if value.AuthenticationLeaseExpiresAt != nil && value.AuthenticationLeaseExpiresAt.After(now) {
			return hostedinteraction.Interaction{}, time.Time{}, hostedinteraction.ErrTemporarilyUnavailable
		}
		result, resetErr := tx.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='opened',version=version+1,authentication_lease_digest=NULL,authentication_started_at=NULL,authentication_lease_expires_at=NULL WHERE interaction_id=$1 AND status='authenticating' AND authentication_lease_expires_at<=$2`, record.InteractionID, now)
		if resetErr != nil || result.RowsAffected() != 1 {
			if resetErr == nil {
				resetErr = hostedinteraction.ErrTemporarilyUnavailable
			}
			return hostedinteraction.Interaction{}, time.Time{}, resetErr
		}
		value.Status = hostedinteraction.StatusOpened
	}
	if terminal(value.Status) {
		return hostedinteraction.Interaction{}, time.Time{}, hostedinteraction.ErrInvalidArgument
	}
	if _, err = tx.Exec(ctx, `UPDATE hosted_interaction.browser_sessions SET status='revoked',revoked_at=$2,revoke_reason='rotated' WHERE interaction_id=$1 AND status='active'`, record.InteractionID, now); err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	expiresAt := now.Add(record.TTL)
	if expiresAt.After(value.ExpiresAt) {
		expiresAt = value.ExpiresAt
	}
	_, err = tx.Exec(ctx, `INSERT INTO hosted_interaction.browser_sessions(browser_session_id,interaction_id,token_digest,status,created_at,last_seen_at,expires_at) VALUES($1,$2,$3,'active',$4,$4,$5)`, record.SessionID, record.InteractionID, record.TokenDigest, now, expiresAt)
	if err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	if value.Status != hostedinteraction.StatusCompleted {
		result, updateErr := tx.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='opened',version=version+1,opened_at=COALESCE(opened_at,$2) WHERE interaction_id=$1 AND status IN ('created','opened')`, record.InteractionID, now)
		if updateErr != nil || result.RowsAffected() != 1 {
			if updateErr == nil {
				updateErr = hostedinteraction.ErrInvalidArgument
			}
			return hostedinteraction.Interaction{}, time.Time{}, updateErr
		}
	}
	value, err = getInteraction(ctx, tx, record.InteractionID, false)
	if err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	record.Event, err = eventWithStatus(record.Event, value.Status)
	if err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	record.Event.InteractionID, record.Event.OccurredAt = record.InteractionID, now
	if err = insertOutbox(ctx, tx, record.Event); err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	return value, expiresAt, nil
}

func (r *Repository) ValidateBrowserSession(ctx context.Context, interactionID string, tokenDigest []byte) (hostedinteraction.BrowserAccess, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return hostedinteraction.BrowserAccess{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	value, browserSessionID, now, err := lockBrowserAndInteraction(ctx, tx, interactionID, tokenDigest)
	if err != nil {
		return hostedinteraction.BrowserAccess{}, err
	}
	if !value.ExpiresAt.After(now) {
		return hostedinteraction.BrowserAccess{}, hostedinteraction.ErrInteractionExpired
	}
	if _, err = tx.Exec(ctx, `UPDATE hosted_interaction.browser_sessions SET last_seen_at=$2 WHERE interaction_id=$1 AND status='active'`, interactionID, now); err != nil {
		return hostedinteraction.BrowserAccess{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return hostedinteraction.BrowserAccess{}, err
	}
	return hostedinteraction.BrowserAccess{Interaction: value, BrowserSessionID: browserSessionID}, nil
}

func (r *Repository) BeginAuthentication(ctx context.Context, interactionID string, tokenDigest, leaseDigest []byte, leaseTTL time.Duration) (hostedinteraction.Interaction, time.Time, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	value, _, now, err := lockBrowserAndInteraction(ctx, tx, interactionID, tokenDigest)
	if err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	if value.Route != hostedinteraction.RouteAuth || !value.ExpiresAt.After(now) || len(leaseDigest) != 32 || leaseTTL <= 0 {
		return hostedinteraction.Interaction{}, time.Time{}, hostedinteraction.ErrAuthenticationNeeded
	}
	if value.Status == hostedinteraction.StatusAuthenticating {
		if value.AuthenticationLeaseExpiresAt != nil && value.AuthenticationLeaseExpiresAt.After(now) {
			return hostedinteraction.Interaction{}, time.Time{}, hostedinteraction.ErrTemporarilyUnavailable
		}
		result, resetErr := tx.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='opened',version=version+1,authentication_lease_digest=NULL,authentication_started_at=NULL,authentication_lease_expires_at=NULL WHERE interaction_id=$1 AND status='authenticating' AND authentication_lease_expires_at<= $2`, interactionID, now)
		if resetErr != nil || result.RowsAffected() != 1 {
			if resetErr == nil {
				resetErr = hostedinteraction.ErrTemporarilyUnavailable
			}
			return hostedinteraction.Interaction{}, time.Time{}, resetErr
		}
		value.Status = hostedinteraction.StatusOpened
	}
	if value.Status != hostedinteraction.StatusOpened {
		return hostedinteraction.Interaction{}, time.Time{}, hostedinteraction.ErrAuthenticationNeeded
	}
	leaseExpiresAt := minTime(now.Add(leaseTTL), value.ExpiresAt)
	if !leaseExpiresAt.After(now) {
		return hostedinteraction.Interaction{}, time.Time{}, hostedinteraction.ErrInteractionExpired
	}
	if _, err = tx.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='authenticating',version=version+1,authentication_lease_digest=$2,authentication_started_at=$3,authentication_lease_expires_at=$4 WHERE interaction_id=$1 AND status='opened'`, interactionID, leaseDigest, now, leaseExpiresAt); err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	value, err = getInteraction(ctx, tx, interactionID, false)
	if err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return hostedinteraction.Interaction{}, time.Time{}, err
	}
	return value, leaseExpiresAt, nil
}

func (r *Repository) ResetAuthentication(ctx context.Context, interactionID string, tokenDigest, leaseDigest []byte) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	value, _, now, err := lockBrowserAndInteraction(ctx, tx, interactionID, tokenDigest)
	if err != nil {
		return err
	}
	if value.Status != hostedinteraction.StatusAuthenticating || !value.ExpiresAt.After(now) || value.AuthenticationLeaseExpiresAt == nil || !value.AuthenticationLeaseExpiresAt.After(now) || !hmac.Equal(value.AuthenticationLeaseDigest, leaseDigest) {
		return hostedinteraction.ErrLeaseLost
	}
	result, err := tx.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='opened',version=version+1,authentication_lease_digest=NULL,authentication_started_at=NULL,authentication_lease_expires_at=NULL WHERE interaction_id=$1 AND status='authenticating' AND authentication_lease_digest=$2 AND authentication_lease_expires_at>$3`, interactionID, leaseDigest, now)
	if err != nil || result.RowsAffected() != 1 {
		if err == nil {
			return hostedinteraction.ErrLeaseLost
		}
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) GetCompletionGrant(ctx context.Context, interactionID string, browserTokenDigest []byte) (hostedinteraction.CompletionGrant, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return hostedinteraction.CompletionGrant{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	value, _, _, err := lockBrowserAndInteraction(ctx, tx, interactionID, browserTokenDigest)
	if err != nil {
		return hostedinteraction.CompletionGrant{}, err
	}
	if value.Status != hostedinteraction.StatusCompleted {
		return hostedinteraction.CompletionGrant{}, hostedinteraction.ErrInvalidGrant
	}
	grant, err := getCompletionGrant(ctx, tx, interactionID)
	if err != nil {
		return hostedinteraction.CompletionGrant{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return hostedinteraction.CompletionGrant{}, err
	}
	return grant, nil
}

func (r *Repository) Complete(ctx context.Context, record hostedinteraction.CompleteRecord) (hostedinteraction.Interaction, hostedinteraction.CompletionGrant, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if record.Operation != "" {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "hosted-idem|"+hex.EncodeToString(record.ActorDigest)+"|"+hex.EncodeToString(record.KeyDigest)); err != nil {
			return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, err
		}
	}
	value, _, now, err := lockBrowserAndInteraction(ctx, tx, record.InteractionID, record.BrowserTokenDigest)
	if err != nil {
		return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, err
	}
	if !value.ExpiresAt.After(now) {
		return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, hostedinteraction.ErrInteractionExpired
	}
	if record.Operation != "" {
		var existingID string
		var existingDigest []byte
		err = tx.QueryRow(ctx, `SELECT interaction_id,request_digest FROM hosted_interaction.idempotency_records WHERE operation=$1 AND actor_digest=$2 AND key_digest=$3`, record.Operation, record.ActorDigest, record.KeyDigest).Scan(&existingID, &existingDigest)
		if err == nil {
			if existingID != record.InteractionID || !hmac.Equal(existingDigest, record.RequestDigest) {
				return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, hostedinteraction.ErrIdempotencyConflict
			}
			grant, grantErr := getCompletionGrant(ctx, tx, existingID)
			if grantErr != nil {
				return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, grantErr
			}
			if err = tx.Commit(ctx); err != nil {
				return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, err
			}
			return value, grant, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, err
		}
	}
	if !contains(record.ExpectedStatus, value.Status) {
		return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, hostedinteraction.ErrInvalidArgument
	}
	if record.GrantType == "authorization_code" {
		if value.Status != hostedinteraction.StatusAuthenticating || value.AuthenticationLeaseExpiresAt == nil || !value.AuthenticationLeaseExpiresAt.After(now) || !hmac.Equal(value.AuthenticationLeaseDigest, record.AuthenticationLeaseDigest) {
			return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, hostedinteraction.ErrLeaseLost
		}
	} else if len(record.AuthenticationLeaseDigest) != 0 {
		return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, hostedinteraction.ErrInvalidArgument
	}
	_, err = tx.Exec(ctx, `INSERT INTO hosted_interaction.completion_grants(grant_id,interaction_id,grant_type,code_digest,identity_proof_id,result_document,status,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,'available',$7,$8)`, record.GrantID, record.InteractionID, record.GrantType, record.CodeDigest, nullableString(record.IdentityProofID), json.RawMessage(record.ResultDocument), now, minTime(now.Add(record.GrantTTL), value.ExpiresAt))
	if err != nil {
		if unique(err) {
			return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, hostedinteraction.ErrInvalidGrant
		}
		return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, err
	}
	resultKind := record.GrantType
	var result pgconn.CommandTag
	if record.GrantType == "authorization_code" {
		result, err = tx.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='completed',version=version+1,result_kind=$2,completed_at=$3,authentication_lease_digest=NULL,authentication_started_at=NULL,authentication_lease_expires_at=NULL WHERE interaction_id=$1 AND status='authenticating' AND authentication_lease_digest=$4 AND authentication_lease_expires_at>$3`, record.InteractionID, resultKind, now, record.AuthenticationLeaseDigest)
	} else {
		result, err = tx.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='completed',version=version+1,result_kind=$2,completed_at=$3 WHERE interaction_id=$1 AND status=ANY($4)`, record.InteractionID, resultKind, now, statusStrings(record.ExpectedStatus))
	}
	if err != nil || result.RowsAffected() != 1 {
		if err == nil {
			err = hostedinteraction.ErrLeaseLost
		}
		return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, err
	}
	if record.Operation != "" {
		response := record.IdempotencyResponse
		if len(response) == 0 {
			response = []byte(`{}`)
		}
		_, err = tx.Exec(ctx, `INSERT INTO hosted_interaction.idempotency_records(operation,actor_digest,key_digest,request_digest,interaction_id,response_document,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, record.Operation, record.ActorDigest, record.KeyDigest, record.RequestDigest, record.InteractionID, response, now)
		if err != nil {
			return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, err
		}
	}
	record.Event.OccurredAt = now
	if err = insertOutbox(ctx, tx, record.Event); err != nil {
		return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, err
	}
	value, err = getInteraction(ctx, tx, record.InteractionID, false)
	if err != nil {
		return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, err
	}
	grant, err := getCompletionGrant(ctx, tx, record.InteractionID)
	if err != nil {
		return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return hostedinteraction.Interaction{}, hostedinteraction.CompletionGrant{}, false, err
	}
	return value, grant, false, nil
}

func (r *Repository) Cancel(ctx context.Context, interactionID string, tokenDigest []byte, event hostedinteraction.OutboxEvent) (hostedinteraction.Interaction, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return hostedinteraction.Interaction{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	value, _, now, err := lockBrowserAndInteraction(ctx, tx, interactionID, tokenDigest)
	if err != nil {
		return hostedinteraction.Interaction{}, err
	}
	if value.Status == hostedinteraction.StatusCancelled {
		if err = tx.Commit(ctx); err != nil {
			return hostedinteraction.Interaction{}, err
		}
		return value, nil
	}
	if value.Status != hostedinteraction.StatusCreated && value.Status != hostedinteraction.StatusOpened && value.Status != hostedinteraction.StatusAuthenticating {
		return hostedinteraction.Interaction{}, hostedinteraction.ErrInvalidArgument
	}
	result, err := tx.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='cancelled',version=version+1,result_kind='cancelled',terminal_at=$2,authentication_lease_digest=NULL,authentication_started_at=NULL,authentication_lease_expires_at=NULL WHERE interaction_id=$1 AND status IN ('created','opened','authenticating')`, interactionID, now)
	if err != nil || result.RowsAffected() != 1 {
		if err == nil {
			err = hostedinteraction.ErrInvalidArgument
		}
		return hostedinteraction.Interaction{}, err
	}
	event.OccurredAt = now
	if err = insertOutbox(ctx, tx, event); err != nil {
		return hostedinteraction.Interaction{}, err
	}
	value, err = getInteraction(ctx, tx, interactionID, false)
	if err != nil {
		return hostedinteraction.Interaction{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return hostedinteraction.Interaction{}, err
	}
	return value, nil
}

func (r *Repository) ClaimGrant(ctx context.Context, interactionID string, scope hostedinteraction.Scope, codeDigest, verifierDigest []byte, leaseTTL time.Duration, leaseToken string, leaseDigest []byte) (hostedinteraction.ClaimedGrant, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return hostedinteraction.ClaimedGrant{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	value, err := getInteraction(ctx, tx, interactionID, true)
	if err != nil {
		return hostedinteraction.ClaimedGrant{}, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return hostedinteraction.ClaimedGrant{}, err
	}
	if value.Status != hostedinteraction.StatusCompleted || !value.Scope.Matches(scope) || !value.ExpiresAt.After(now) {
		return hostedinteraction.ClaimedGrant{}, hostedinteraction.ErrInvalidGrant
	}
	if value.Route == hostedinteraction.RouteAuth && !hmac.Equal(value.PKCEChallengeDigest, verifierDigest) {
		return hostedinteraction.ClaimedGrant{}, hostedinteraction.ErrPKCERequired
	}
	var claim hostedinteraction.ClaimedGrant
	var resultRaw []byte
	var storedCode, processingDigest []byte
	var identityProofID *string
	var processingExpires *time.Time
	var status string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT grant_id,grant_type,identity_proof_id,result_document,status,code_digest,processing_token_digest,processing_expires_at,expires_at FROM hosted_interaction.completion_grants WHERE interaction_id=$1 FOR UPDATE`, interactionID).Scan(&claim.GrantID, &claim.GrantType, &identityProofID, &resultRaw, &status, &storedCode, &processingDigest, &processingExpires, &expiresAt)
	if err != nil {
		return hostedinteraction.ClaimedGrant{}, hostedinteraction.ErrInvalidGrant
	}
	if !expiresAt.After(now) || !hmac.Equal(storedCode, codeDigest) {
		return hostedinteraction.ClaimedGrant{}, hostedinteraction.ErrInvalidGrant
	}
	if status == "processing" && processingExpires != nil && processingExpires.After(now) {
		return hostedinteraction.ClaimedGrant{}, hostedinteraction.ErrTemporarilyUnavailable
	}
	if status != "available" && status != "processing" {
		return hostedinteraction.ClaimedGrant{}, hostedinteraction.ErrInvalidGrant
	}
	leaseExpires := minTime(now.Add(leaseTTL), expiresAt)
	result, err := tx.Exec(ctx, `UPDATE hosted_interaction.completion_grants SET status='processing',processing_token_digest=$2,processing_expires_at=$3 WHERE grant_id=$1 AND (status='available' OR (status='processing' AND processing_expires_at<=$4))`, claim.GrantID, leaseDigest, leaseExpires, now)
	if err != nil || result.RowsAffected() != 1 {
		if err == nil {
			err = hostedinteraction.ErrTemporarilyUnavailable
		}
		return hostedinteraction.ClaimedGrant{}, err
	}
	if len(resultRaw) > 0 {
		_ = json.Unmarshal(resultRaw, &claim.ResultDocument)
	}
	if identityProofID != nil {
		claim.IdentityProofID = *identityProofID
	}
	claim.InteractionID, claim.LeaseToken, claim.LeaseExpiresAt, claim.ExpiresAt, claim.Scope, claim.TraceID = interactionID, leaseToken, leaseExpires, expiresAt, value.Scope, value.TraceID
	if err = tx.Commit(ctx); err != nil {
		return hostedinteraction.ClaimedGrant{}, err
	}
	return claim, nil
}

func (r *Repository) ConsumeGrant(ctx context.Context, grantID string, leaseDigest []byte, event hostedinteraction.OutboxEvent) (hostedinteraction.Interaction, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return hostedinteraction.Interaction{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var interactionID, status string
	var storedLease []byte
	var leaseExpires *time.Time
	err = tx.QueryRow(ctx, `SELECT interaction_id FROM hosted_interaction.completion_grants WHERE grant_id=$1`, grantID).Scan(&interactionID)
	if err != nil {
		return hostedinteraction.Interaction{}, hostedinteraction.ErrInvalidGrant
	}
	value, err := getInteraction(ctx, tx, interactionID, true)
	if err != nil {
		return hostedinteraction.Interaction{}, err
	}
	err = tx.QueryRow(ctx, `SELECT status,processing_token_digest,processing_expires_at FROM hosted_interaction.completion_grants WHERE grant_id=$1 FOR UPDATE`, grantID).Scan(&status, &storedLease, &leaseExpires)
	if err != nil {
		return hostedinteraction.Interaction{}, hostedinteraction.ErrInvalidGrant
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return hostedinteraction.Interaction{}, err
	}
	if value.Status != hostedinteraction.StatusCompleted || status != "processing" || leaseExpires == nil || !leaseExpires.After(now) || !hmac.Equal(storedLease, leaseDigest) {
		return hostedinteraction.Interaction{}, hostedinteraction.ErrLeaseLost
	}
	if _, err = tx.Exec(ctx, `UPDATE hosted_interaction.completion_grants SET status='consumed',processing_token_digest=NULL,processing_expires_at=NULL,consumed_at=$2 WHERE grant_id=$1 AND status='processing'`, grantID, now); err != nil {
		return hostedinteraction.Interaction{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE hosted_interaction.interactions SET status='exchanged',version=version+1,terminal_at=$2 WHERE interaction_id=$1 AND status='completed'`, interactionID, now)
	if err != nil || result.RowsAffected() != 1 {
		if err == nil {
			err = hostedinteraction.ErrLeaseLost
		}
		return hostedinteraction.Interaction{}, err
	}
	event.InteractionID, event.OccurredAt = interactionID, now
	if err = insertOutbox(ctx, tx, event); err != nil {
		return hostedinteraction.Interaction{}, err
	}
	value, err = getInteraction(ctx, tx, interactionID, false)
	if err != nil {
		return hostedinteraction.Interaction{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return hostedinteraction.Interaction{}, err
	}
	return value, nil
}

func (r *Repository) ExpireDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		return 0, hostedinteraction.ErrInvalidArgument
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT i.interaction_id FROM hosted_interaction.interactions i LEFT JOIN hosted_interaction.completion_grants g ON g.interaction_id=i.interaction_id WHERE i.status IN ('created','opened','authenticating','completed') AND (i.expires_at<=clock_timestamp() OR (i.status='completed' AND g.expires_at<=clock_timestamp())) ORDER BY LEAST(i.expires_at,COALESCE(g.expires_at,i.expires_at)),i.interaction_id FOR UPDATE OF i SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		var now time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `UPDATE hosted_interaction.completion_grants SET status='expired',processing_token_digest=NULL,processing_expires_at=NULL WHERE interaction_id=$1 AND status IN ('available','processing')`, id); err != nil {
			return 0, err
		}
		var version int64
		if err = tx.QueryRow(ctx, `UPDATE hosted_interaction.interactions SET status='expired',version=version+1,result_kind='failed',failure_code='hosted.interaction_expired',terminal_at=$2,authentication_lease_digest=NULL,authentication_started_at=NULL,authentication_lease_expires_at=NULL WHERE interaction_id=$1 AND status IN ('created','opened','authenticating','completed') RETURNING version`, id, now).Scan(&version); err != nil {
			return 0, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO hosted_interaction.outbox_events(event_id,interaction_id,event_type,payload,occurred_at,next_attempt_at) SELECT 'evt_'||md5($1||$2),interaction_id,'hosted.interaction_expired.v1',jsonb_build_object('interaction_id',interaction_id,'product_id',product_id,'application_id',application_id,'tenant_id',tenant_id,'route',route_id,'status','expired','trace_id',trace_id),$3,$3 FROM hosted_interaction.interactions WHERE interaction_id=$1`, id, strconv.FormatInt(version, 10), now)
		if err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func expireLocked(ctx context.Context, tx pgx.Tx, value hostedinteraction.Interaction, now time.Time) (hostedinteraction.Interaction, error) {
	if _, err := tx.Exec(ctx, `UPDATE hosted_interaction.completion_grants SET status='expired',processing_token_digest=NULL,processing_expires_at=NULL WHERE interaction_id=$1 AND status IN ('available','processing')`, value.InteractionID); err != nil {
		return hostedinteraction.Interaction{}, err
	}
	var version int64
	if err := tx.QueryRow(ctx, `UPDATE hosted_interaction.interactions SET status='expired',version=version+1,result_kind='failed',failure_code='hosted.interaction_expired',terminal_at=$2,authentication_lease_digest=NULL,authentication_started_at=NULL,authentication_lease_expires_at=NULL WHERE interaction_id=$1 AND status IN ('created','opened','authenticating','completed') RETURNING version`, value.InteractionID, now).Scan(&version); err != nil {
		return hostedinteraction.Interaction{}, err
	}
	_, err := tx.Exec(ctx, `INSERT INTO hosted_interaction.outbox_events(event_id,interaction_id,event_type,payload,occurred_at,next_attempt_at) SELECT 'evt_'||md5($1||$2),interaction_id,'hosted.interaction_expired.v1',jsonb_build_object('interaction_id',interaction_id,'product_id',product_id,'application_id',application_id,'tenant_id',tenant_id,'route',route_id,'status','expired','trace_id',trace_id),$3,$3 FROM hosted_interaction.interactions WHERE interaction_id=$1`, value.InteractionID, strconv.FormatInt(version, 10), now)
	if err != nil {
		return hostedinteraction.Interaction{}, err
	}
	return getInteraction(ctx, tx, value.InteractionID, false)
}

type queryable interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getInteraction(ctx context.Context, q queryable, interactionID string, lock bool) (hostedinteraction.Interaction, error) {
	query := `SELECT ` + interactionColumns + ` FROM hosted_interaction.interactions WHERE interaction_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var value hostedinteraction.Interaction
	var route, channel, status string
	var tenant, clientSession, userID, userSession, pkceMethod, locale, theme, result, failure *string
	var nonce, pkce []byte
	err := q.QueryRow(ctx, query, interactionID).Scan(&value.InteractionID, &route, &value.Scope.ProductID, &value.Scope.ApplicationID, &tenant, &value.Scope.Environment, &channel, &value.Actor.Kind, &clientSession, &userID, &userSession, &value.ReturnTargetCode, &value.ReturnTargetURI, &value.ReturnTargetPolicyVersion, &value.StateProtectorKeyRef, &value.StateCiphertext, &value.StateDigest, &nonce, &pkce, &pkceMethod, &locale, &theme, &status, &value.Version, &result, &failure, &value.TraceID, &value.CreatedAt, &value.ExpiresAt, &value.OpenedAt, &value.CompletedAt, &value.TerminalAt, &value.AuthenticationLeaseDigest, &value.AuthenticationStartedAt, &value.AuthenticationLeaseExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return hostedinteraction.Interaction{}, hostedinteraction.ErrInvalidArgument
	}
	if err != nil {
		return hostedinteraction.Interaction{}, err
	}
	value.Route, value.Scope.Channel, value.Status, value.Scope.TenantID = hostedinteraction.Route(route), hostedinteraction.Channel(channel), hostedinteraction.Status(status), tenant
	if clientSession != nil {
		value.Actor.ClientSessionID = *clientSession
	}
	if userID != nil {
		value.Actor.UserID = *userID
	}
	if userSession != nil {
		value.Actor.UserSessionID = *userSession
	}
	value.NonceDigest = nonce
	value.PKCEChallengeDigest = pkce
	if pkceMethod != nil {
		value.PKCEMethod = *pkceMethod
	}
	if locale != nil {
		value.Locale = *locale
	}
	if theme != nil {
		value.ThemeVariant = *theme
	}
	if result != nil {
		value.ResultKind = *result
	}
	if failure != nil {
		value.FailureCode = *failure
	}
	return value, nil
}

func insertInteraction(ctx context.Context, tx pgx.Tx, v hostedinteraction.Interaction) error {
	_, err := tx.Exec(ctx, `INSERT INTO hosted_interaction.interactions(`+interactionColumns+`) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35)`, v.InteractionID, v.Route, v.Scope.ProductID, v.Scope.ApplicationID, v.Scope.TenantID, v.Scope.Environment, v.Scope.Channel, v.Actor.Kind, nullableString(v.Actor.ClientSessionID), nullableString(v.Actor.UserID), nullableString(v.Actor.UserSessionID), v.ReturnTargetCode, v.ReturnTargetURI, v.ReturnTargetPolicyVersion, v.StateProtectorKeyRef, v.StateCiphertext, v.StateDigest, nullableBytes(v.NonceDigest), nullableBytes(v.PKCEChallengeDigest), nullableString(v.PKCEMethod), nullableString(v.Locale), nullableString(v.ThemeVariant), v.Status, v.Version, nullableString(v.ResultKind), nullableString(v.FailureCode), v.TraceID, v.CreatedAt, v.ExpiresAt, v.OpenedAt, v.CompletedAt, v.TerminalAt, nullableBytes(v.AuthenticationLeaseDigest), v.AuthenticationStartedAt, v.AuthenticationLeaseExpiresAt)
	return err
}

func insertOutbox(ctx context.Context, tx pgx.Tx, event hostedinteraction.OutboxEvent) error {
	if event.EventID == "" || event.InteractionID == "" || len(event.Payload) == 0 {
		return hostedinteraction.ErrInvalidArgument
	}
	_, err := tx.Exec(ctx, `INSERT INTO hosted_interaction.outbox_events(event_id,interaction_id,event_type,payload,occurred_at,next_attempt_at) VALUES($1,$2,$3,$4,$5,$5)`, event.EventID, event.InteractionID, event.EventType, json.RawMessage(event.Payload), event.OccurredAt)
	return err
}

func getCompletionGrant(ctx context.Context, q queryable, interactionID string) (hostedinteraction.CompletionGrant, error) {
	var grant hostedinteraction.CompletionGrant
	var proof *string
	var raw []byte
	err := q.QueryRow(ctx, `SELECT grant_id,interaction_id,grant_type,identity_proof_id,result_document,expires_at FROM hosted_interaction.completion_grants WHERE interaction_id=$1`, interactionID).Scan(&grant.GrantID, &grant.InteractionID, &grant.GrantType, &proof, &raw, &grant.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return hostedinteraction.CompletionGrant{}, hostedinteraction.ErrInvalidGrant
	}
	if err != nil {
		return hostedinteraction.CompletionGrant{}, err
	}
	if proof != nil {
		grant.IdentityProofID = *proof
	}
	_ = json.Unmarshal(raw, &grant.ResultDocument)
	return grant, nil
}

func interactionDue(ctx context.Context, q queryable, value hostedinteraction.Interaction, now time.Time) (bool, error) {
	if !value.ExpiresAt.After(now) {
		return true, nil
	}
	if value.Status != hostedinteraction.StatusCompleted {
		return false, nil
	}
	var grantExpiresAt time.Time
	err := q.QueryRow(ctx, `SELECT expires_at FROM hosted_interaction.completion_grants WHERE interaction_id=$1`, value.InteractionID).Scan(&grantExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, hostedinteraction.ErrInvalidGrant
	}
	if err != nil {
		return false, err
	}
	return !grantExpiresAt.After(now), nil
}

func eventWithStatus(event hostedinteraction.OutboxEvent, status hostedinteraction.Status) (hostedinteraction.OutboxEvent, error) {
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload == nil {
		return hostedinteraction.OutboxEvent{}, hostedinteraction.ErrInvalidArgument
	}
	payload["status"] = string(status)
	raw, err := json.Marshal(payload)
	if err != nil {
		return hostedinteraction.OutboxEvent{}, err
	}
	event.Payload = raw
	return event, nil
}

func lockBrowserAndInteraction(ctx context.Context, tx pgx.Tx, interactionID string, tokenDigest []byte) (hostedinteraction.Interaction, string, time.Time, error) {
	value, err := getInteraction(ctx, tx, interactionID, true)
	if err != nil {
		return hostedinteraction.Interaction{}, "", time.Time{}, err
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return hostedinteraction.Interaction{}, "", time.Time{}, err
	}
	var browserSessionID string
	var stored []byte
	err = tx.QueryRow(ctx, `SELECT browser_session_id,token_digest FROM hosted_interaction.browser_sessions WHERE interaction_id=$1 AND status='active' AND expires_at>$2 FOR UPDATE`, interactionID, now).Scan(&browserSessionID, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return hostedinteraction.Interaction{}, "", time.Time{}, hostedinteraction.ErrSessionRevoked
	}
	if err != nil {
		return hostedinteraction.Interaction{}, "", time.Time{}, err
	}
	if !hmac.Equal(stored, tokenDigest) {
		return hostedinteraction.Interaction{}, "", time.Time{}, hostedinteraction.ErrSessionRevoked
	}
	due, err := interactionDue(ctx, tx, value, now)
	if err != nil {
		return hostedinteraction.Interaction{}, "", time.Time{}, err
	}
	if due {
		return hostedinteraction.Interaction{}, "", time.Time{}, hostedinteraction.ErrInteractionExpired
	}
	return value, browserSessionID, now, nil
}

func contains(values []hostedinteraction.Status, wanted hostedinteraction.Status) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func statusStrings(values []hostedinteraction.Status) []string {
	result := make([]string, len(values))
	for i := range values {
		result[i] = string(values[i])
	}
	return result
}
func terminal(status hostedinteraction.Status) bool {
	return status == hostedinteraction.StatusExchanged || status == hostedinteraction.StatusCancelled || status == hostedinteraction.StatusFailed || status == hostedinteraction.StatusExpired
}
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
func unique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
