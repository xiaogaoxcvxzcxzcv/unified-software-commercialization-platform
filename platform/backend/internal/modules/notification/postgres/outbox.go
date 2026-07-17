package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"platform.local/capability-platform/backend/internal/modules/notification"
)

func (r *Repository) ClaimSecurityOutbox(ctx context.Context, workerID string, lease time.Duration) (notification.ClaimedSecurityOutboxEvent, error) {
	if r == nil || r.pool == nil || !validOutboxWorkerID(workerID) || lease <= 0 {
		return notification.ClaimedSecurityOutboxEvent{}, notification.ErrInvalidSecurityDelivery
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return notification.ClaimedSecurityOutboxEvent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return notification.ClaimedSecurityOutboxEvent{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE notification.outbox_events
		SET dead=TRUE,last_error_code='NOTIFICATION_OUTBOX_WORKER_LEASE_EXPIRED',lease_owner=NULL,lease_expires_at=NULL
		WHERE published_at IS NULL AND dead=FALSE AND attempt_count>=$2 AND lease_expires_at<=$1`, databaseNow, notification.SecurityOutboxMaxAttempts); err != nil {
		return notification.ClaimedSecurityOutboxEvent{}, err
	}
	var event notification.ClaimedSecurityOutboxEvent
	err = tx.QueryRow(ctx, `SELECT event_id,delivery_id,event_type,payload,occurred_at,attempt_count
		FROM notification.outbox_events
		WHERE published_at IS NULL AND dead=FALSE AND next_attempt_at<=$1
			AND attempt_count<$2
			AND (lease_expires_at IS NULL OR lease_expires_at<=$1)
		ORDER BY next_attempt_at,occurred_at,event_id
		LIMIT 1 FOR UPDATE SKIP LOCKED`, databaseNow, notification.SecurityOutboxMaxAttempts).Scan(
		&event.EventID, &event.DeliveryID, &event.EventType, &event.Payload, &event.OccurredAt, &event.AttemptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return notification.ClaimedSecurityOutboxEvent{}, commitErr
		}
		return notification.ClaimedSecurityOutboxEvent{}, notification.ErrNotFound
	}
	if err != nil {
		return notification.ClaimedSecurityOutboxEvent{}, err
	}
	event.AttemptCount++
	event.LeaseToken, err = newOutboxLeaseToken(workerID)
	if err != nil {
		return notification.ClaimedSecurityOutboxEvent{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE notification.outbox_events
		SET attempt_count=attempt_count+1,lease_owner=$2,lease_expires_at=$3
		WHERE event_id=$1 AND published_at IS NULL AND dead=FALSE
			AND (lease_expires_at IS NULL OR lease_expires_at<=$4)`, event.EventID, event.LeaseToken, databaseNow.Add(lease), databaseNow)
	if err != nil {
		return notification.ClaimedSecurityOutboxEvent{}, err
	}
	if tag.RowsAffected() != 1 {
		return notification.ClaimedSecurityOutboxEvent{}, notification.ErrOutboxLeaseLost
	}
	if err = tx.Commit(ctx); err != nil {
		return notification.ClaimedSecurityOutboxEvent{}, err
	}
	return event, nil
}

func (r *Repository) MarkSecurityOutboxPublished(ctx context.Context, eventID, leaseToken string) error {
	return r.finishSecurityOutbox(ctx, eventID, leaseToken, "published", 0, "")
}

func (r *Repository) MarkSecurityOutboxRetry(ctx context.Context, eventID, leaseToken string, delay time.Duration, errorCode string) error {
	if delay < 0 || !validOutboxErrorCode(errorCode) {
		return notification.ErrInvalidSecurityDelivery
	}
	return r.finishSecurityOutbox(ctx, eventID, leaseToken, "retry", delay, errorCode)
}

func (r *Repository) MarkSecurityOutboxDead(ctx context.Context, eventID, leaseToken, errorCode string) error {
	if !validOutboxErrorCode(errorCode) {
		return notification.ErrInvalidSecurityDelivery
	}
	return r.finishSecurityOutbox(ctx, eventID, leaseToken, "dead", 0, errorCode)
}

func (r *Repository) finishSecurityOutbox(ctx context.Context, eventID, leaseToken, outcome string, delay time.Duration, errorCode string) error {
	if r == nil || r.pool == nil || eventID == "" || leaseToken == "" {
		return notification.ErrInvalidSecurityDelivery
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return err
	}
	var tag pgconn.CommandTag
	switch outcome {
	case "published":
		tag, err = tx.Exec(ctx, `UPDATE notification.outbox_events
			SET published_at=$3,last_error_code=NULL,lease_owner=NULL,lease_expires_at=NULL
			WHERE event_id=$1 AND lease_owner=$2 AND lease_expires_at>$3 AND published_at IS NULL AND dead=FALSE`, eventID, leaseToken, databaseNow)
	case "retry":
		tag, err = tx.Exec(ctx, `UPDATE notification.outbox_events
			SET next_attempt_at=$3,last_error_code=$4,lease_owner=NULL,lease_expires_at=NULL
			WHERE event_id=$1 AND lease_owner=$2 AND lease_expires_at>$5 AND published_at IS NULL AND dead=FALSE`, eventID, leaseToken, databaseNow.Add(delay), errorCode, databaseNow)
	case "dead":
		tag, err = tx.Exec(ctx, `UPDATE notification.outbox_events
			SET dead=TRUE,last_error_code=$3,lease_owner=NULL,lease_expires_at=NULL
			WHERE event_id=$1 AND lease_owner=$2 AND lease_expires_at>$4 AND published_at IS NULL AND dead=FALSE`, eventID, leaseToken, errorCode, databaseNow)
	default:
		return notification.ErrInvalidSecurityDelivery
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return notification.ErrOutboxLeaseLost
	}
	return tx.Commit(ctx)
}

func newOutboxLeaseToken(workerID string) (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return "lease:" + workerID + ":" + hex.EncodeToString(nonce[:]), nil
}

func validOutboxErrorCode(value string) bool {
	if len(value) == 0 || len(value) > 160 {
		return false
	}
	for _, char := range value {
		if (char < 'A' || char > 'Z') && char != '_' && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func validOutboxWorkerID(value string) bool {
	if len(value) == 0 || len(value) > 160 {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e || char == ',' {
			return false
		}
	}
	return true
}

var _ notification.SecurityOutboxRepository = (*Repository)(nil)
