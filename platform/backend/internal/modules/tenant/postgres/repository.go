package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/tenant"
)

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) EnsureOfficialTenant(ctx context.Context, candidate tenant.Tenant, record tenant.IdempotencyRecord, event tenant.OutboxEvent) (tenant.Tenant, error) {
	tx, err := r.begin(ctx)
	if err != nil {
		return tenant.Tenant{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The product-level lock makes the partial unique official-tenant index a
	// deterministic idempotency boundary even when callers use different keys.
	if err := advisoryLock(ctx, tx, "tenant.official|"+candidate.ProductID); err != nil {
		return tenant.Tenant{}, err
	}
	if err := advisoryLock(ctx, tx, receiptLockKey(record)); err != nil {
		return tenant.Tenant{}, err
	}
	if replay, found, err := loadReceipt(ctx, tx, record); err != nil {
		return tenant.Tenant{}, err
	} else if found {
		return replay, tx.Commit(ctx)
	}

	existing, err := findOfficial(ctx, tx, candidate.ProductID)
	if err == nil {
		if err := saveReceipt(ctx, tx, record, existing); err != nil {
			return tenant.Tenant{}, err
		}
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, tenant.ErrTenantNotFound) {
		return tenant.Tenant{}, err
	}
	if err := insertTenant(ctx, tx, candidate); err != nil {
		return tenant.Tenant{}, err
	}
	if err := insertOutbox(ctx, tx, event); err != nil {
		return tenant.Tenant{}, err
	}
	if err := saveReceipt(ctx, tx, record, candidate); err != nil {
		return tenant.Tenant{}, err
	}
	return candidate, tx.Commit(ctx)
}

func (r *Repository) CreateAgentTenant(ctx context.Context, candidate tenant.Tenant, record tenant.IdempotencyRecord, event tenant.OutboxEvent) (tenant.Tenant, error) {
	tx, err := r.begin(ctx)
	if err != nil {
		return tenant.Tenant{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := advisoryLock(ctx, tx, receiptLockKey(record)); err != nil {
		return tenant.Tenant{}, err
	}
	if replay, found, err := loadReceipt(ctx, tx, record); err != nil {
		return tenant.Tenant{}, err
	} else if found {
		return replay, tx.Commit(ctx)
	}
	if err := insertTenant(ctx, tx, candidate); err != nil {
		return tenant.Tenant{}, err
	}
	if err := insertOutbox(ctx, tx, event); err != nil {
		return tenant.Tenant{}, err
	}
	if err := saveReceipt(ctx, tx, record, candidate); err != nil {
		return tenant.Tenant{}, err
	}
	return candidate, tx.Commit(ctx)
}

func (r *Repository) ListTenants(ctx context.Context, productID string) ([]tenant.Tenant, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("tenant PostgreSQL repository is not configured")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT tenant_id,product_id,tenant_code,name,tenant_type,status,
		       COALESCE(external_agent_ref,''),context_version,created_at,updated_at
		  FROM tenant.product_tenants
		 WHERE product_id=$1
		 ORDER BY CASE tenant_type WHEN 'official' THEN 0 ELSE 1 END, tenant_code, tenant_id`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]tenant.Tenant, 0)
	for rows.Next() {
		value, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) FindOfficialTenant(ctx context.Context, productID string) (tenant.Tenant, error) {
	if r == nil || r.pool == nil {
		return tenant.Tenant{}, errors.New("tenant PostgreSQL repository is not configured")
	}
	return findOfficial(ctx, r.pool, productID)
}

func (r *Repository) FindTenantByDistribution(ctx context.Context, productID, applicationID, channelCode, proofSubjectDigest string) (tenant.Tenant, error) {
	if r == nil || r.pool == nil {
		return tenant.Tenant{}, errors.New("tenant PostgreSQL repository is not configured")
	}
	value, err := scanTenant(r.pool.QueryRow(ctx, `
		SELECT t.tenant_id,t.product_id,t.tenant_code,t.name,t.tenant_type,t.status,
		       COALESCE(t.external_agent_ref,''),t.context_version,t.created_at,t.updated_at
		  FROM tenant.distribution_bindings AS b
		  JOIN tenant.product_tenants AS t
		    ON t.product_id=b.product_id AND t.tenant_id=b.tenant_id
		 WHERE b.product_id=$1
		   AND b.channel_code=$2
		   AND b.proof_subject_digest=$3
		   AND b.status='active'
		   AND (b.application_id IS NULL OR b.application_id=$4)`, productID, channelCode, proofSubjectDigest, applicationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return tenant.Tenant{}, tenant.ErrTenantNotFound
	}
	return value, err
}

func (r *Repository) ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]tenant.ClaimedOutboxEvent, error) {
	tx, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT event_id,aggregate_id,event_type,payload,occurred_at,attempt_count
		  FROM tenant.outbox_events
		 WHERE published_at IS NULL AND dead=FALSE AND next_attempt_at <= $1
		 ORDER BY occurred_at,event_id
		 LIMIT $2
		 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]tenant.ClaimedOutboxEvent, 0)
	for rows.Next() {
		var item tenant.ClaimedOutboxEvent
		var payload []byte
		if err := rows.Scan(&item.EventID, &item.AggregateID, &item.EventType, &payload, &item.OccurredAt, &item.AttemptCount); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &item.Payload); err != nil {
			return nil, fmt.Errorf("decode tenant outbox event %s: %w", item.EventID, err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range result {
		if _, err := tx.Exec(ctx, `
			UPDATE tenant.outbox_events
			   SET attempt_count=attempt_count+1,next_attempt_at=$2
			 WHERE event_id=$1 AND published_at IS NULL AND dead=FALSE`, result[i].EventID, now.Add(30*time.Second)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) MarkOutboxPublished(ctx context.Context, eventID string, now time.Time) error {
	if r == nil || r.pool == nil {
		return errors.New("tenant PostgreSQL repository is not configured")
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE tenant.outbox_events
		   SET published_at=COALESCE(published_at,$2),last_error=NULL
		 WHERE event_id=$1`, eventID, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return tenant.ErrTenantNotFound
	}
	return nil
}

func (r *Repository) MarkOutboxFailed(ctx context.Context, eventID, errorSummary string, retryAt time.Time, dead bool) error {
	if r == nil || r.pool == nil {
		return errors.New("tenant PostgreSQL repository is not configured")
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE tenant.outbox_events
		   SET next_attempt_at=$2,dead=$3,last_error=$4
		 WHERE event_id=$1 AND published_at IS NULL`, eventID, retryAt, dead, errorSummary)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return tenant.ErrTenantNotFound
	}
	return nil
}

func (r *Repository) begin(ctx context.Context) (pgx.Tx, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("tenant PostgreSQL repository is not configured")
	}
	return r.pool.Begin(ctx)
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func findOfficial(ctx context.Context, query queryer, productID string) (tenant.Tenant, error) {
	value, err := scanTenant(query.QueryRow(ctx, `
		SELECT tenant_id,product_id,tenant_code,name,tenant_type,status,
		       COALESCE(external_agent_ref,''),context_version,created_at,updated_at
		  FROM tenant.product_tenants
		 WHERE product_id=$1 AND tenant_type='official'`, productID))
	if errors.Is(err, pgx.ErrNoRows) {
		return tenant.Tenant{}, tenant.ErrTenantNotFound
	}
	return value, err
}

type rowScanner interface{ Scan(...any) error }

func scanTenant(row rowScanner) (tenant.Tenant, error) {
	var value tenant.Tenant
	err := row.Scan(
		&value.TenantID, &value.ProductID, &value.TenantCode, &value.Name, &value.TenantType,
		&value.Status, &value.ExternalAgentRef, &value.ContextVersion, &value.CreatedAt, &value.UpdatedAt,
	)
	return value, err
}

func insertTenant(ctx context.Context, tx pgx.Tx, value tenant.Tenant) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO tenant.product_tenants(
			tenant_id,product_id,tenant_code,name,tenant_type,status,external_agent_ref,
			context_version,created_at,updated_at
		) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10)`,
		value.TenantID, value.ProductID, value.TenantCode, value.Name, value.TenantType, value.Status,
		value.ExternalAgentRef, value.ContextVersion, value.CreatedAt, value.UpdatedAt)
	if err == nil {
		return nil
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return tenant.ErrTenantCodeConflict
	}
	return err
}

func advisoryLock(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key)
	return err
}

func receiptLockKey(record tenant.IdempotencyRecord) string {
	return record.Operation + "|" + record.ActorID + "|" + record.ScopeID + "|" + record.KeyDigest
}

func loadReceipt(ctx context.Context, tx pgx.Tx, expected tenant.IdempotencyRecord) (tenant.Tenant, bool, error) {
	var requestDigest, state string
	var raw []byte
	err := tx.QueryRow(ctx, `
		SELECT request_digest,state,response_json
		  FROM tenant.idempotency_records
		 WHERE operation=$1 AND actor_id=$2 AND scope_id=$3 AND key_digest=$4
		 FOR UPDATE`, expected.Operation, expected.ActorID, expected.ScopeID, expected.KeyDigest).Scan(&requestDigest, &state, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenant.Tenant{}, false, nil
	}
	if err != nil {
		return tenant.Tenant{}, false, err
	}
	if requestDigest != expected.RequestDigest {
		return tenant.Tenant{}, false, tenant.ErrIdempotencyConflict
	}
	if state != "completed" || len(raw) == 0 {
		return tenant.Tenant{}, false, fmt.Errorf("tenant idempotency record has non-replayable state %q", state)
	}
	var value tenant.Tenant
	if err := json.Unmarshal(raw, &value); err != nil {
		return tenant.Tenant{}, false, fmt.Errorf("decode tenant idempotency response: %w", err)
	}
	if value.ProductID != expected.ScopeID || value.TenantID == "" {
		return tenant.Tenant{}, false, errors.New("tenant idempotency response scope mismatch")
	}
	return value, true, nil
}

func saveReceipt(ctx context.Context, tx pgx.Tx, record tenant.IdempotencyRecord, value tenant.Tenant) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO tenant.idempotency_records(
			operation,actor_id,scope_id,key_digest,request_digest,resource_id,state,response_json,created_at,updated_at
		) VALUES($1,$2,$3,$4,$5,$6,'completed',$7,$8,$8)`,
		record.Operation, record.ActorID, record.ScopeID, record.KeyDigest, record.RequestDigest,
		value.TenantID, raw, record.CreatedAt)
	return err
}

func insertOutbox(ctx context.Context, tx pgx.Tx, event tenant.OutboxEvent) error {
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO tenant.outbox_events(
			event_id,aggregate_id,event_type,payload,occurred_at,next_attempt_at
		) VALUES($1,$2,$3,$4,$5,$5)`,
		event.EventID, event.AggregateID, event.EventType, payload, event.OccurredAt)
	return err
}
