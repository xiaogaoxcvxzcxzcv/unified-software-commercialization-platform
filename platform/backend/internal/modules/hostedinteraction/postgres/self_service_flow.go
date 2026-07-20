package postgres

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"platform.local/capability-platform/backend/internal/modules/hostedinteraction"
)

func (r *Repository) PutSelfServiceFlow(ctx context.Context, record hostedinteraction.PutSelfServiceFlowRecord) (hostedinteraction.SelfServiceFlow, error) {
	if record.InteractionID == "" || (record.Kind != "registration_verification" && record.Kind != "recovery_verification") || record.IdentifierHint == "" || record.TTL <= 0 || record.Protected.KeyRef == "" || len(record.Protected.Ciphertext) < 32 || len(record.Protected.Digest) != 32 {
		return hostedinteraction.SelfServiceFlow{}, hostedinteraction.ErrInvalidArgument
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return hostedinteraction.SelfServiceFlow{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, route string
	var interactionExpiry, now time.Time
	if err = tx.QueryRow(ctx, `SELECT status,route_id,expires_at,clock_timestamp() FROM hosted_interaction.interactions WHERE interaction_id=$1 FOR UPDATE`, record.InteractionID).Scan(&status, &route, &interactionExpiry, &now); errors.Is(err, pgx.ErrNoRows) {
		return hostedinteraction.SelfServiceFlow{}, hostedinteraction.ErrInvalidArgument
	}
	if err != nil {
		return hostedinteraction.SelfServiceFlow{}, err
	}
	if route != "hosted.auth" || status != "opened" || !interactionExpiry.After(now) {
		return hostedinteraction.SelfServiceFlow{}, hostedinteraction.ErrInvalidGrant
	}
	expires := now.Add(record.TTL)
	if expires.After(interactionExpiry) {
		expires = interactionExpiry
	}
	_, err = tx.Exec(ctx, `INSERT INTO hosted_interaction.self_service_flows(interaction_id,flow_kind,protected_key_ref,protected_ciphertext,protected_digest,identifier_hint,version,created_at,updated_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,1,$7,$7,$8) ON CONFLICT(interaction_id) DO UPDATE SET flow_kind=EXCLUDED.flow_kind,protected_key_ref=EXCLUDED.protected_key_ref,protected_ciphertext=EXCLUDED.protected_ciphertext,protected_digest=EXCLUDED.protected_digest,identifier_hint=EXCLUDED.identifier_hint,version=hosted_interaction.self_service_flows.version+1,updated_at=EXCLUDED.updated_at,expires_at=EXCLUDED.expires_at`, record.InteractionID, record.Kind, record.Protected.KeyRef, record.Protected.Ciphertext, record.Protected.Digest, record.IdentifierHint, now, expires)
	if err != nil {
		return hostedinteraction.SelfServiceFlow{}, err
	}
	value, _, err := getSelfServiceFlow(ctx, tx, record.InteractionID, now)
	if err != nil {
		return value, err
	}
	if err = tx.Commit(ctx); err != nil {
		return value, err
	}
	return value, nil
}

func (r *Repository) GetSelfServiceFlow(ctx context.Context, id string) (hostedinteraction.SelfServiceFlow, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return hostedinteraction.SelfServiceFlow{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return hostedinteraction.SelfServiceFlow{}, false, err
	}
	value, found, err := getSelfServiceFlow(ctx, tx, id, now)
	if err != nil {
		return value, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return value, false, err
	}
	return value, found, nil
}
func getSelfServiceFlow(ctx context.Context, tx pgx.Tx, id string, now time.Time) (hostedinteraction.SelfServiceFlow, bool, error) {
	var v hostedinteraction.SelfServiceFlow
	v.InteractionID = id
	err := tx.QueryRow(ctx, `SELECT flow_kind,protected_key_ref,protected_ciphertext,protected_digest,identifier_hint,version,created_at,updated_at,expires_at FROM hosted_interaction.self_service_flows WHERE interaction_id=$1 FOR UPDATE`, id).Scan(&v.Kind, &v.Protected.KeyRef, &v.Protected.Ciphertext, &v.Protected.Digest, &v.IdentifierHint, &v.Version, &v.CreatedAt, &v.UpdatedAt, &v.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}
	if !v.ExpiresAt.After(now) {
		if _, err = tx.Exec(ctx, `DELETE FROM hosted_interaction.self_service_flows WHERE interaction_id=$1`, id); err != nil {
			return v, false, err
		}
		return hostedinteraction.SelfServiceFlow{}, false, nil
	}
	return v, true, nil
}
func (r *Repository) DeleteSelfServiceFlow(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM hosted_interaction.self_service_flows WHERE interaction_id=$1`, id)
	return err
}

func (r *Repository) ResetSelfServiceFlowIdempotent(ctx context.Context, record hostedinteraction.ResetSelfServiceFlowRecord) error {
	if record.InteractionID == "" || len(record.ActorDigest) != 32 || len(record.KeyDigest) != 32 || len(record.RequestDigest) != 32 {
		return hostedinteraction.ErrInvalidArgument
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	const storedOperation = "auth_flow_reset"
	lockID := "hosted-flow-reset|" + hex.EncodeToString(record.ActorDigest) + "|" + hex.EncodeToString(record.KeyDigest)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockID); err != nil {
		return err
	}
	var interactionID string
	var requestDigest []byte
	err = tx.QueryRow(ctx, `SELECT interaction_id,request_digest FROM hosted_interaction.idempotency_records WHERE operation=$1 AND actor_digest=$2 AND key_digest=$3`, storedOperation, record.ActorDigest, record.KeyDigest).Scan(&interactionID, &requestDigest)
	if err == nil {
		if interactionID != record.InteractionID || !hmac.Equal(requestDigest, record.RequestDigest) {
			return hostedinteraction.ErrIdempotencyConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var route, status string
	var expiresAt, now time.Time
	if err = tx.QueryRow(ctx, `SELECT route_id,status,expires_at,clock_timestamp() FROM hosted_interaction.interactions WHERE interaction_id=$1 FOR UPDATE`, record.InteractionID).Scan(&route, &status, &expiresAt, &now); errors.Is(err, pgx.ErrNoRows) {
		return hostedinteraction.ErrInvalidArgument
	}
	if err != nil {
		return err
	}
	if route != "hosted.auth" || status != "opened" || !expiresAt.After(now) {
		return hostedinteraction.ErrInvalidGrant
	}
	if _, err = tx.Exec(ctx, `DELETE FROM hosted_interaction.self_service_flows WHERE interaction_id=$1`, record.InteractionID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO hosted_interaction.idempotency_records(operation,actor_digest,key_digest,request_digest,interaction_id,response_document,created_at) VALUES($1,$2,$3,$4,$5,'{}'::jsonb,$6)`, storedOperation, record.ActorDigest, record.KeyDigest, record.RequestDigest, record.InteractionID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var _ hostedinteraction.SelfServiceFlowRepository = (*Repository)(nil)
