package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/notification"
)

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) CreateSecurityDelivery(ctx context.Context, record notification.CreateSecurityDeliveryRecord) (bool, error) {
	if r == nil || r.pool == nil {
		return false, notification.ErrInvalidSecurityDelivery
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	d := record.Delivery
	tag, err := tx.Exec(ctx, `INSERT INTO notification.security_deliveries(
		delivery_id,request_digest,purpose,product_id,application_id,tenant_id,provider_ref,destination_type,
		protector_key_ref,payload_nonce,payload_ciphertext,payload_digest,status,attempt_count,max_attempts,
		next_attempt_at,created_at,expires_at,trace_id
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'pending',0,$13,$14,$15,$16,$17)
	ON CONFLICT (delivery_id) DO NOTHING`,
		d.DeliveryID, d.RequestDigest, d.Purpose, d.ProductID, d.ApplicationID, d.TenantID, d.ProviderRef, d.DestinationType,
		d.Payload.KeyRef, d.Payload.Nonce, d.Payload.Ciphertext, d.Payload.Digest, d.MaxAttempts,
		d.NextAttemptAt, d.CreatedAt, d.ExpiresAt, d.TraceID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		var stored []byte
		if err := tx.QueryRow(ctx, `SELECT request_digest FROM notification.security_deliveries WHERE delivery_id=$1`, d.DeliveryID).Scan(&stored); err != nil {
			return false, err
		}
		if len(stored) != len(d.RequestDigest) || !hmac.Equal(stored, d.RequestDigest) {
			return false, notification.ErrIdempotencyConflict
		}
		return false, tx.Commit(ctx)
	}
	e := record.Event
	if _, err = tx.Exec(ctx, `INSERT INTO notification.outbox_events(event_id,delivery_id,event_type,payload,occurred_at,next_attempt_at) VALUES($1,$2,$3,$4,$5,$5)`, e.EventID, e.DeliveryID, e.EventType, e.Payload, e.OccurredAt); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (r *Repository) ClaimSecurityDelivery(ctx context.Context, workerID string, lease time.Duration) (notification.SecurityDelivery, error) {
	if r == nil || r.pool == nil || workerID == "" || lease <= 0 {
		return notification.SecurityDelivery{}, notification.ErrInvalidSecurityDelivery
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return notification.SecurityDelivery{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return notification.SecurityDelivery{}, err
	}
	if err = finalizeExpiredSecurityDelivery(ctx, tx, databaseNow); err != nil {
		return notification.SecurityDelivery{}, err
	}
	if err = recoverExpiredSecurityLease(ctx, tx, databaseNow); err != nil {
		return notification.SecurityDelivery{}, err
	}
	var d notification.SecurityDelivery
	err = tx.QueryRow(ctx, `SELECT delivery_id,request_digest,purpose,product_id,application_id,tenant_id,provider_ref,destination_type,
		protector_key_ref,payload_nonce,payload_ciphertext,payload_digest,status,attempt_count,max_attempts,next_attempt_at,
		created_at,expires_at,trace_id
	FROM notification.security_deliveries
	WHERE status='pending' AND attempt_count<max_attempts AND next_attempt_at<=$1 AND expires_at>$1
	ORDER BY next_attempt_at,created_at,delivery_id LIMIT 1 FOR UPDATE SKIP LOCKED`, databaseNow).Scan(
		&d.DeliveryID, &d.RequestDigest, &d.Purpose, &d.ProductID, &d.ApplicationID, &d.TenantID, &d.ProviderRef, &d.DestinationType,
		&d.Payload.KeyRef, &d.Payload.Nonce, &d.Payload.Ciphertext, &d.Payload.Digest, &d.Status, &d.AttemptCount, &d.MaxAttempts,
		&d.NextAttemptAt, &d.CreatedAt, &d.ExpiresAt, &d.TraceID)
	if errors.Is(err, pgx.ErrNoRows) {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return notification.SecurityDelivery{}, commitErr
		}
		return notification.SecurityDelivery{}, notification.ErrNotFound
	}
	if err != nil {
		return notification.SecurityDelivery{}, err
	}
	d.AttemptCount++
	leaseExpires := databaseNow.Add(lease)
	tag, err := tx.Exec(ctx, `UPDATE notification.security_deliveries SET status='processing',attempt_count=attempt_count+1,lease_owner=$2,lease_started_at=$3,lease_expires_at=$4 WHERE delivery_id=$1`, d.DeliveryID, workerID, databaseNow, leaseExpires)
	if err != nil {
		return notification.SecurityDelivery{}, err
	}
	if tag.RowsAffected() != 1 {
		return notification.SecurityDelivery{}, notification.ErrLeaseLost
	}
	d.Status, d.LeaseOwner, d.LeaseStartedAt, d.LeaseExpiresAt = "processing", workerID, databaseNow, &leaseExpires
	if err := tx.Commit(ctx); err != nil {
		return notification.SecurityDelivery{}, err
	}
	return d, nil
}

func (r *Repository) CompleteSecurityDelivery(ctx context.Context, record notification.CompleteSecurityDeliveryRecord) error {
	if r == nil || r.pool == nil {
		return notification.ErrInvalidSecurityDelivery
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, owner string
	var attemptCount int
	var leaseStartedAt time.Time
	var leaseExpiresAt *time.Time
	var expiresAt time.Time
	var databaseNow time.Time
	err = tx.QueryRow(ctx, `SELECT status,COALESCE(lease_owner,''),attempt_count,lease_started_at,lease_expires_at,expires_at,clock_timestamp() FROM notification.security_deliveries WHERE delivery_id=$1 FOR UPDATE`, record.DeliveryID).Scan(&status, &owner, &attemptCount, &leaseStartedAt, &leaseExpiresAt, &expiresAt, &databaseNow)
	if errors.Is(err, pgx.ErrNoRows) {
		return notification.ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "processing" || owner != record.LeaseOwner || leaseExpiresAt == nil || !leaseExpiresAt.After(databaseNow) || !expiresAt.After(databaseNow) || record.Attempt.AttemptNumber != attemptCount || record.Attempt.DeliveryID != record.DeliveryID {
		return notification.ErrLeaseLost
	}
	a := record.Attempt
	a.StartedAt = leaseStartedAt
	a.FinishedAt = databaseNow
	if _, err = tx.Exec(ctx, `INSERT INTO notification.security_delivery_attempts(
		attempt_id,delivery_id,attempt_number,outcome,provider_message_digest,error_code,error_digest,started_at,finished_at
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, a.AttemptID, a.DeliveryID, a.AttemptNumber, a.Outcome, nullableDigest(a.ProviderMessageDigest), a.ErrorCode, nullableDigest(a.ErrorDigest), a.StartedAt, a.FinishedAt); err != nil {
		return err
	}
	var deliveredAt, deadAt *time.Time
	if record.NextStatus == "delivered" {
		deliveredAt = &databaseNow
	} else if record.NextStatus == "dead" {
		deadAt = &databaseNow
	} else if record.NextStatus != "pending" {
		return notification.ErrInvalidSecurityDelivery
	}
	nextAttemptAt := databaseNow.Add(record.RetryDelay)
	tag, err := tx.Exec(ctx, `UPDATE notification.security_deliveries SET status=$2,next_attempt_at=$3,lease_owner=NULL,lease_started_at=NULL,lease_expires_at=NULL,delivered_at=$4,dead_at=$5 WHERE delivery_id=$1 AND status='processing' AND lease_owner=$6 AND lease_expires_at>$7`, record.DeliveryID, record.NextStatus, nextAttemptAt, deliveredAt, deadAt, record.LeaseOwner, databaseNow)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return notification.ErrLeaseLost
	}
	return tx.Commit(ctx)
}

func finalizeExpiredSecurityDelivery(ctx context.Context, tx pgx.Tx, databaseNow time.Time) error {
	var deliveryID string
	var attemptCount int
	var leaseStartedAt time.Time
	err := tx.QueryRow(ctx, `SELECT delivery_id,attempt_count,lease_started_at FROM notification.security_deliveries
		WHERE status='processing' AND expires_at<=$1
		ORDER BY expires_at,delivery_id LIMIT 1 FOR UPDATE SKIP LOCKED`, databaseNow).Scan(&deliveryID, &attemptCount, &leaseStartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `UPDATE notification.security_deliveries
			SET status='dead',dead_at=$1,lease_owner=NULL,lease_started_at=NULL,lease_expires_at=NULL
			WHERE status='pending' AND expires_at<=$1`, databaseNow)
		return err
	}
	if err != nil {
		return err
	}
	code := "NOTIFICATION_SECURITY_DELIVERY_EXPIRED"
	digest := sha256.Sum256([]byte("notification.security.error.v1\x00" + code))
	startedAt := leaseStartedAt
	if startedAt.After(databaseNow) {
		startedAt = databaseNow
	}
	if _, err = tx.Exec(ctx, `INSERT INTO notification.security_delivery_attempts(
		attempt_id,delivery_id,attempt_number,outcome,provider_message_digest,error_code,error_digest,started_at,finished_at
	) VALUES($1,$2,$3,'terminal_failure',NULL,$4,$5,$6,$7)`,
		postgresAttemptID(deliveryID, attemptCount), deliveryID, attemptCount, code, digest[:], startedAt, databaseNow); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE notification.security_deliveries SET status='dead',dead_at=$2,lease_owner=NULL,lease_started_at=NULL,lease_expires_at=NULL WHERE delivery_id=$1 AND status='processing'`, deliveryID, databaseNow)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return notification.ErrLeaseLost
	}
	_, err = tx.Exec(ctx, `UPDATE notification.security_deliveries
		SET status='dead',dead_at=$1,lease_owner=NULL,lease_started_at=NULL,lease_expires_at=NULL
		WHERE status='pending' AND expires_at<=$1`, databaseNow)
	return err
}

func recoverExpiredSecurityLease(ctx context.Context, tx pgx.Tx, databaseNow time.Time) error {
	var deliveryID string
	var attemptCount, maxAttempts int
	var leaseStartedAt, leaseExpiresAt time.Time
	err := tx.QueryRow(ctx, `SELECT delivery_id,attempt_count,max_attempts,lease_started_at,lease_expires_at FROM notification.security_deliveries
		WHERE status='processing' AND expires_at>$1 AND lease_expires_at<=$1
		ORDER BY lease_expires_at,delivery_id LIMIT 1 FOR UPDATE SKIP LOCKED`, databaseNow).Scan(&deliveryID, &attemptCount, &maxAttempts, &leaseStartedAt, &leaseExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	terminal := attemptCount >= maxAttempts
	outcome, status, code := "retryable_failure", "pending", "NOTIFICATION_SECURITY_WORKER_LEASE_EXPIRED"
	if terminal {
		outcome, status = "terminal_failure", "dead"
	}
	digest := sha256.Sum256([]byte("notification.security.error.v1\x00" + code))
	if _, err = tx.Exec(ctx, `INSERT INTO notification.security_delivery_attempts(
		attempt_id,delivery_id,attempt_number,outcome,provider_message_digest,error_code,error_digest,started_at,finished_at
	) VALUES($1,$2,$3,$4,NULL,$5,$6,$7,$8)`, postgresAttemptID(deliveryID, attemptCount), deliveryID, attemptCount, outcome, code, digest[:], leaseStartedAt, databaseNow); err != nil {
		return err
	}
	var deadAt *time.Time
	if terminal {
		deadAt = &databaseNow
	}
	tag, err := tx.Exec(ctx, `UPDATE notification.security_deliveries
		SET status=$2,next_attempt_at=$3,dead_at=$4,lease_owner=NULL,lease_started_at=NULL,lease_expires_at=NULL
		WHERE delivery_id=$1 AND status='processing'`, deliveryID, status, databaseNow, deadAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return notification.ErrLeaseLost
	}
	return nil
}

func postgresAttemptID(deliveryID string, attempt int) string {
	digest := sha256.Sum256([]byte(deliveryID + "\x00" + strconv.Itoa(attempt)))
	return "nat_" + hex.EncodeToString(digest[:16])
}

func nullableDigest(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

var _ notification.SecurityRepository = (*Repository)(nil)
