package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
)

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) EvaluateScopedAdmission(ctx context.Context, productID, tenantID, userID string) (productuseraccess.Admission, error) {
	result := productuseraccess.Admission{Allowed: true, ProductStatus: productuseraccess.StatusActive}
	err := r.pool.QueryRow(ctx, `SELECT status,access_version FROM product_user_access.product_access WHERE product_id=$1 AND user_id=$2`, productID, userID).Scan(&result.ProductStatus, &result.ProductVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return productuseraccess.Admission{}, err
	}
	if result.ProductStatus == productuseraccess.StatusSuspended {
		result.Allowed, result.Code = false, "PRODUCT_USER_ACCESS_SUSPENDED"
		return result, nil
	}
	if tenantID == "" {
		return result, nil
	}
	result.TenantStatus = productuseraccess.StatusActive
	err = r.pool.QueryRow(ctx, `SELECT status,access_version FROM product_user_access.tenant_access WHERE product_id=$1 AND tenant_id=$2 AND user_id=$3`, productID, tenantID, userID).Scan(&result.TenantStatus, &result.TenantVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return productuseraccess.Admission{}, err
	}
	if result.TenantStatus == productuseraccess.StatusSuspended {
		result.Allowed, result.Code = false, "TENANT_USER_ACCESS_SUSPENDED"
	}
	return result, nil
}

func (r *Repository) SetProductAccessStatus(ctx context.Context, record productuseraccess.ChangeRecord) (productuseraccess.StatusChangeResult, error) {
	record.ScopeType, record.TenantID = productuseraccess.ScopeProduct, ""
	return r.setStatus(ctx, record)
}

func (r *Repository) SetTenantAccessStatus(ctx context.Context, record productuseraccess.ChangeRecord) (productuseraccess.StatusChangeResult, error) {
	record.ScopeType = productuseraccess.ScopeTenant
	return r.setStatus(ctx, record)
}

func (r *Repository) setStatus(ctx context.Context, record productuseraccess.ChangeRecord) (productuseraccess.StatusChangeResult, error) {
	if r == nil || r.pool == nil {
		return productuseraccess.StatusChangeResult{}, productuseraccess.ErrInvalidArgument
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return productuseraccess.StatusChangeResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if replay, reserved, err := reserveIdempotency(ctx, tx, record); err != nil || !reserved {
		return replay, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, scopeLockKey(record)); err != nil {
		return productuseraccess.StatusChangeResult{}, err
	}
	currentVersion, currentStatus, currentChangedAt, exists, err := lockCurrent(ctx, tx, record)
	if err != nil {
		return productuseraccess.StatusChangeResult{}, err
	}
	if currentVersion != record.ExpectedVersion {
		if err := finishIdempotency(ctx, tx, record, "failed", nil, record.Now); err != nil {
			return productuseraccess.StatusChangeResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return productuseraccess.StatusChangeResult{}, err
		}
		return productuseraccess.StatusChangeResult{}, productuseraccess.ErrConflict
	}
	var databaseNow time.Time
	if err := tx.QueryRow(ctx, `SELECT CURRENT_TIMESTAMP`).Scan(&databaseNow); err != nil {
		return productuseraccess.StatusChangeResult{}, err
	}
	record.Now = databaseNow.UTC()
	if exists && currentStatus == record.Status {
		result := productuseraccess.StatusChangeResult{ScopeType: record.ScopeType, ProductID: record.ProductID, TenantID: record.TenantID, UserID: record.UserID, Status: currentStatus, AccessVersion: currentVersion, AuditID: record.AuditID}
		if err := insertAuditEvent(ctx, tx, record, currentVersion, currentChangedAt); err != nil {
			return productuseraccess.StatusChangeResult{}, err
		}
		if err := finishIdempotency(ctx, tx, record, "completed", &currentVersion, record.Now); err != nil {
			return productuseraccess.StatusChangeResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return productuseraccess.StatusChangeResult{}, err
		}
		return result, nil
	}
	if exists && !record.Now.After(currentChangedAt) {
		record.Now = currentChangedAt.Add(time.Microsecond)
	}
	nextVersion := currentVersion + 1
	if exists {
		err = updateFact(ctx, tx, record, nextVersion)
	} else {
		err = insertFact(ctx, tx, record, nextVersion)
	}
	if err != nil {
		return productuseraccess.StatusChangeResult{}, mapWriteError(err)
	}
	result := productuseraccess.StatusChangeResult{ScopeType: record.ScopeType, ProductID: record.ProductID, TenantID: record.TenantID, UserID: record.UserID, Status: record.Status, AccessVersion: nextVersion, AuditID: record.AuditID}
	if err := insertEvents(ctx, tx, record, nextVersion); err != nil {
		return productuseraccess.StatusChangeResult{}, err
	}
	if err := finishIdempotency(ctx, tx, record, "completed", &nextVersion, record.Now); err != nil {
		return productuseraccess.StatusChangeResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return productuseraccess.StatusChangeResult{}, err
	}
	return result, nil
}

func reserveIdempotency(ctx context.Context, tx pgx.Tx, record productuseraccess.ChangeRecord) (productuseraccess.StatusChangeResult, bool, error) {
	result, err := tx.Exec(ctx, `INSERT INTO product_user_access.idempotency_records(operation,scope_type,product_id,scope_id,user_id,key_digest,request_digest,audit_id,state,created_at,updated_at) VALUES('set_access_status',$1,$2,$3,$4,$5,$6,$7,'pending',$8,$8) ON CONFLICT DO NOTHING`, record.ScopeType, record.ProductID, scopeID(record), record.UserID, record.KeyDigest, record.RequestDigest, record.AuditID, record.Now)
	if err != nil {
		return productuseraccess.StatusChangeResult{}, false, err
	}
	if result.RowsAffected() == 1 {
		return productuseraccess.StatusChangeResult{}, true, nil
	}
	var storedDigest []byte
	var state string
	var version *int64
	var auditID string
	err = tx.QueryRow(ctx, `SELECT request_digest,state,result_version,COALESCE(audit_id,'') FROM product_user_access.idempotency_records WHERE operation='set_access_status' AND scope_type=$1 AND product_id=$2 AND scope_id=$3 AND user_id=$4 AND key_digest=$5 FOR UPDATE`, record.ScopeType, record.ProductID, scopeID(record), record.UserID, record.KeyDigest).Scan(&storedDigest, &state, &version, &auditID)
	if err != nil {
		return productuseraccess.StatusChangeResult{}, false, err
	}
	if !bytes.Equal(storedDigest, record.RequestDigest) || state != "completed" || version == nil || auditID == "" {
		return productuseraccess.StatusChangeResult{}, false, productuseraccess.ErrConflict
	}
	return productuseraccess.StatusChangeResult{ScopeType: record.ScopeType, ProductID: record.ProductID, TenantID: record.TenantID, UserID: record.UserID, Status: record.Status, AccessVersion: *version, AuditID: auditID}, false, nil
}

func lockCurrent(ctx context.Context, tx pgx.Tx, record productuseraccess.ChangeRecord) (int64, productuseraccess.Status, time.Time, bool, error) {
	var version int64
	var status productuseraccess.Status
	var changedAt time.Time
	var err error
	if record.ScopeType == productuseraccess.ScopeProduct {
		err = tx.QueryRow(ctx, `SELECT access_version,status,status_changed_at FROM product_user_access.product_access WHERE product_id=$1 AND user_id=$2 FOR UPDATE`, record.ProductID, record.UserID).Scan(&version, &status, &changedAt)
	} else {
		err = tx.QueryRow(ctx, `SELECT access_version,status,status_changed_at FROM product_user_access.tenant_access WHERE product_id=$1 AND tenant_id=$2 AND user_id=$3 FOR UPDATE`, record.ProductID, record.TenantID, record.UserID).Scan(&version, &status, &changedAt)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", time.Time{}, false, nil
	}
	return version, status, changedAt, err == nil, err
}

func finishIdempotency(ctx context.Context, tx pgx.Tx, record productuseraccess.ChangeRecord, state string, version *int64, now time.Time) error {
	result, err := tx.Exec(ctx, `UPDATE product_user_access.idempotency_records SET state=$7,result_version=$8,updated_at=$9 WHERE operation='set_access_status' AND scope_type=$1 AND product_id=$2 AND scope_id=$3 AND user_id=$4 AND key_digest=$5 AND request_digest=$6`, record.ScopeType, record.ProductID, scopeID(record), record.UserID, record.KeyDigest, record.RequestDigest, state, version, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return productuseraccess.ErrConflict
	}
	return nil
}

func insertFact(ctx context.Context, tx pgx.Tx, record productuseraccess.ChangeRecord, version int64) error {
	if record.ScopeType == productuseraccess.ScopeProduct {
		_, err := tx.Exec(ctx, `INSERT INTO product_user_access.product_access(product_id,user_id,status,access_version,reason_code,operator_note,status_changed_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7,$7)`, record.ProductID, record.UserID, record.Status, version, record.ReasonCode, nullableNote(record.OperatorNote), record.Now)
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO product_user_access.tenant_access(product_id,tenant_id,user_id,status,access_version,reason_code,operator_note,status_changed_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,$8)`, record.ProductID, record.TenantID, record.UserID, record.Status, version, record.ReasonCode, nullableNote(record.OperatorNote), record.Now)
	return err
}

func updateFact(ctx context.Context, tx pgx.Tx, record productuseraccess.ChangeRecord, version int64) error {
	if record.ScopeType == productuseraccess.ScopeProduct {
		_, err := tx.Exec(ctx, `UPDATE product_user_access.product_access SET status=$3,access_version=$4,reason_code=$5,operator_note=$6,status_changed_at=$7,updated_at=$7 WHERE product_id=$1 AND user_id=$2`, record.ProductID, record.UserID, record.Status, version, record.ReasonCode, nullableNote(record.OperatorNote), record.Now)
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE product_user_access.tenant_access SET status=$4,access_version=$5,reason_code=$6,operator_note=$7,status_changed_at=$8,updated_at=$8 WHERE product_id=$1 AND tenant_id=$2 AND user_id=$3`, record.ProductID, record.TenantID, record.UserID, record.Status, version, record.ReasonCode, nullableNote(record.OperatorNote), record.Now)
	return err
}

func insertEvents(ctx context.Context, tx pgx.Tx, record productuseraccess.ChangeRecord, version int64) error {
	payload := eventPayload(record, version, record.Now)
	if err := insertOutbox(ctx, tx, record.StatusEventID, aggregateID(record), "product-user-access.status-changed.v1", payload, record.Now); err != nil {
		return err
	}
	if record.Status == productuseraccess.StatusSuspended {
		if err := insertOutbox(ctx, tx, record.RevocationEventID, aggregateID(record), "product-user-access.session-revocation-requested.v1", payload, record.Now); err != nil {
			return err
		}
	}
	return nil
}

func insertAuditEvent(ctx context.Context, tx pgx.Tx, record productuseraccess.ChangeRecord, version int64, statusChangedAt time.Time) error {
	return insertOutbox(ctx, tx, record.StatusEventID, aggregateID(record), "product-user-access.command-audited.v1", eventPayload(record, version, statusChangedAt), record.Now)
}

func eventPayload(record productuseraccess.ChangeRecord, version int64, statusChangedAt time.Time) productuseraccess.EventPayload {
	targetID := aggregateID(record)
	return productuseraccess.EventPayload{
		AuditID: record.AuditID, OccurredAt: record.Now, ActorID: record.ActorID,
		Permission: "product.user-access.manage", ScopeType: string(record.ScopeType), ScopeID: scopeID(record),
		ProductID: record.ProductID, TenantID: record.TenantID, Action: "product_user_access.set_status",
		TargetType: "product_user_access", TargetID: targetID, Result: "success", ReasonCode: record.ReasonCode,
		TraceID: record.TraceID, RiskLevel: "high", UserID: record.UserID, Status: record.Status,
		AccessVersion: version, StatusChangedAt: statusChangedAt,
		RedactedSummary: map[string]any{"user_id": record.UserID, "status": record.Status, "access_version": version, "status_changed_at": statusChangedAt},
	}
}

func insertOutbox(ctx context.Context, tx pgx.Tx, eventID, aggregateID, eventType string, payload productuseraccess.EventPayload, now time.Time) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_user_access.outbox_events(event_id,aggregate_id,event_type,payload,occurred_at,next_attempt_at) VALUES($1,$2,$3,$4,$5,$5)`, eventID, aggregateID, eventType, raw, now)
	return err
}

func (r *Repository) ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]productuseraccess.ClaimedOutboxEvent, error) {
	if r == nil || r.pool == nil || limit < 1 {
		return nil, productuseraccess.ErrInvalidArgument
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT event_id,aggregate_id,event_type,payload,occurred_at,attempt_count FROM product_user_access.outbox_events WHERE published_at IS NULL AND dead=FALSE AND next_attempt_at <= $1 ORDER BY occurred_at,event_id LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	items := make([]productuseraccess.ClaimedOutboxEvent, 0)
	for rows.Next() {
		var item productuseraccess.ClaimedOutboxEvent
		var payload []byte
		if err := rows.Scan(&item.EventID, &item.AggregateID, &item.EventType, &payload, &item.OccurredAt, &item.AttemptCount); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(payload, &item.Payload); err != nil {
			item.PayloadError = "invalid product user access outbox payload"
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for index := range items {
		items[index].AttemptCount++
		if _, err := tx.Exec(ctx, `UPDATE product_user_access.outbox_events SET attempt_count=attempt_count+1,next_attempt_at=$2 WHERE event_id=$1 AND published_at IS NULL AND dead=FALSE`, items[index].EventID, now.Add(30*time.Second)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *Repository) MarkOutboxPublished(ctx context.Context, eventID string, now time.Time) error {
	if r == nil || r.pool == nil {
		return productuseraccess.ErrInvalidArgument
	}
	result, err := r.pool.Exec(ctx, `UPDATE product_user_access.outbox_events SET published_at=COALESCE(published_at,$2),last_error=NULL WHERE event_id=$1`, eventID, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return productuseraccess.ErrOutboxNotFound
	}
	return nil
}

func (r *Repository) MarkOutboxFailed(ctx context.Context, eventID, summary string, next time.Time, dead bool) error {
	if r == nil || r.pool == nil {
		return productuseraccess.ErrInvalidArgument
	}
	result, err := r.pool.Exec(ctx, `UPDATE product_user_access.outbox_events SET next_attempt_at=$2,last_error=$3,dead=$4 WHERE event_id=$1 AND published_at IS NULL AND dead=FALSE`, eventID, next, summary, dead)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return productuseraccess.ErrOutboxNotFound
	}
	return nil
}

func (r *Repository) ListScopedUserIDs(ctx context.Context, productID, tenantID string) ([]string, error) {
	query, args := `SELECT user_id FROM product_user_access.product_access WHERE product_id=$1 ORDER BY user_id`, []any{productID}
	if tenantID != "" {
		query, args = `SELECT user_id FROM product_user_access.tenant_access WHERE product_id=$1 AND tenant_id=$2 ORDER BY user_id`, []any{productID, tenantID}
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		result = append(result, userID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(result)
	return result, nil
}

func (r *Repository) GetScopedAccessBatch(ctx context.Context, productID, tenantID string, userIDs []string) ([]productuseraccess.AccessFact, error) {
	if r == nil || r.pool == nil || len(userIDs) == 0 {
		return nil, productuseraccess.ErrInvalidArgument
	}
	query := `SELECT user_id,status,access_version,reason_code,status_changed_at FROM product_user_access.product_access WHERE product_id=$1 AND user_id=ANY($2::text[]) ORDER BY user_id`
	args := []any{productID, userIDs}
	scopeType := productuseraccess.ScopeProduct
	if tenantID != "" {
		query = `SELECT user_id,status,access_version,reason_code,status_changed_at FROM product_user_access.tenant_access WHERE product_id=$1 AND tenant_id=$2 AND user_id=ANY($3::text[]) ORDER BY user_id`
		args, scopeType = []any{productID, tenantID, userIDs}, productuseraccess.ScopeTenant
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]productuseraccess.AccessFact, 0, len(userIDs))
	for rows.Next() {
		item := productuseraccess.AccessFact{ScopeType: scopeType, ProductID: productID, TenantID: tenantID}
		if err := rows.Scan(&item.UserID, &item.Status, &item.AccessVersion, &item.ReasonCode, &item.StatusChangedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func scopeID(record productuseraccess.ChangeRecord) string {
	if record.ScopeType == productuseraccess.ScopeTenant {
		return record.TenantID
	}
	return record.ProductID
}
func scopeLockKey(record productuseraccess.ChangeRecord) string {
	return fmt.Sprintf("%s:%d:%s:%d:%s:%d:%s", record.ScopeType, len(record.ProductID), record.ProductID, len(record.TenantID), record.TenantID, len(record.UserID), record.UserID)
}
func nullableNote(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func aggregateID(record productuseraccess.ChangeRecord) string {
	sum := sha256.Sum256([]byte(string(record.ScopeType) + "\x00" + record.ProductID + "\x00" + record.TenantID + "\x00" + record.UserID))
	return "access_" + hex.EncodeToString(sum[:16])
}
func mapWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return productuseraccess.ErrConflict
	}
	return fmt.Errorf("write product user access: %w", err)
}
