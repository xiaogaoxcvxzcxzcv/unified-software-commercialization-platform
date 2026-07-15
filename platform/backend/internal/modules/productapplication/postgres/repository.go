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
	"platform.local/capability-platform/backend/internal/modules/productapplication"
)

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) CreateApplication(ctx context.Context, record productapplication.CreateRecord) (productapplication.Application, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return productapplication.Application{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if replay, reserved, err := reserveIdempotency[productapplication.Application](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	a := record.Application
	_, err = tx.Exec(ctx, `INSERT INTO product_application.product_applications(application_id,product_id,application_code,name,platform,distribution_channel,release_track,status,context_version,current_redirect_policy_version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, a.ApplicationID, a.ProductID, a.ApplicationCode, a.Name, a.Platform, a.DistributionChannel, a.ReleaseTrack, a.Status, a.ContextVersion, a.CurrentRedirectPolicyVersion, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return productapplication.Application{}, mapWriteError(err)
	}
	payload := eventPayload(a.AuditID, record.ActorID, record.TraceID, "product.application.manage", "product_application.created.v1", a.ProductID, a.ApplicationID, map[string]any{"application_code": a.ApplicationCode, "platform": a.Platform, "distribution_channel": a.DistributionChannel})
	if err := insertOutbox(ctx, tx, record.EventID, a.ApplicationID, "product_application.created.v1", payload, a.CreatedAt); err != nil {
		return productapplication.Application{}, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, a.ApplicationID, a); err != nil {
		return productapplication.Application{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return productapplication.Application{}, err
	}
	return a, nil
}

func (r *Repository) ListApplications(ctx context.Context, productID string) ([]productapplication.Application, error) {
	rows, err := r.pool.Query(ctx, applicationSelect+` WHERE a.product_id=$1 ORDER BY a.created_at,a.application_id`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]productapplication.Application, 0)
	for rows.Next() {
		application, err := scanApplication(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, application)
	}
	return result, rows.Err()
}

func (r *Repository) GetApplication(ctx context.Context, productID, applicationID string) (productapplication.Application, error) {
	application, err := scanApplication(r.pool.QueryRow(ctx, applicationSelect+` WHERE a.product_id=$1 AND a.application_id=$2`, productID, applicationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return productapplication.Application{}, productapplication.ErrNotFound
	}
	return application, err
}

func (r *Repository) BindClient(ctx context.Context, record productapplication.BindRecord) (productapplication.ClientBinding, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return productapplication.ClientBinding{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if replay, reserved, err := reserveIdempotency[productapplication.ClientBinding](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	var status productapplication.Status
	err = tx.QueryRow(ctx, `SELECT status FROM product_application.product_applications WHERE product_id=$1 AND application_id=$2 FOR UPDATE`, record.Binding.ProductID, record.Binding.ApplicationID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return productapplication.ClientBinding{}, productapplication.ErrNotFound
	}
	if err != nil {
		return productapplication.ClientBinding{}, err
	}
	if status != productapplication.StatusActive {
		return productapplication.ClientBinding{}, productapplication.ErrApplicationSuspended
	}
	b := record.Binding
	_, err = tx.Exec(ctx, `INSERT INTO product_application.application_client_bindings(binding_id,product_id,application_id,client_id,environment,status,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, b.BindingID, b.ProductID, b.ApplicationID, b.ClientID, b.Environment, b.Status, b.CreatedAt, b.UpdatedAt)
	if err != nil {
		return productapplication.ClientBinding{}, mapWriteError(err)
	}
	payload := eventPayload(b.AuditID, record.ActorID, record.TraceID, "product.application.security.manage", "product_application.client_bound.v1", b.ProductID, b.ApplicationID, map[string]any{"binding_id": b.BindingID, "client_id": b.ClientID, "environment": b.Environment})
	if err := insertOutbox(ctx, tx, record.EventID, b.ApplicationID, "product_application.client_bound.v1", payload, b.CreatedAt); err != nil {
		return productapplication.ClientBinding{}, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, b.BindingID, b); err != nil {
		return productapplication.ClientBinding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return productapplication.ClientBinding{}, err
	}
	return b, nil
}

func (r *Repository) ReplaceRedirects(ctx context.Context, record productapplication.RedirectRecord) (productapplication.RedirectPolicyVersion, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return productapplication.RedirectPolicyVersion{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentVersion int64
	err = tx.QueryRow(ctx, `SELECT current_redirect_policy_version FROM product_application.product_applications WHERE product_id=$1 AND application_id=$2 FOR UPDATE`, record.Version.ProductID, record.Version.ApplicationID).Scan(&currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return productapplication.RedirectPolicyVersion{}, productapplication.ErrNotFound
	}
	if err != nil {
		return productapplication.RedirectPolicyVersion{}, err
	}
	if currentVersion > 0 {
		existing, err := r.redirectVersion(ctx, tx, record.Version.ProductID, record.Version.ApplicationID, currentVersion)
		if err != nil {
			return productapplication.RedirectPolicyVersion{}, err
		}
		if existing.ContentSHA256 == record.Version.ContentSHA256 {
			if err := tx.Commit(ctx); err != nil {
				return productapplication.RedirectPolicyVersion{}, err
			}
			return existing, nil
		}
	}
	record.Version.Version = currentVersion + 1
	_, err = tx.Exec(ctx, `INSERT INTO product_application.redirect_policy_versions(policy_id,product_id,application_id,version,content_sha256,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, record.Version.PolicyID, record.Version.ProductID, record.Version.ApplicationID, record.Version.Version, record.Version.ContentSHA256, record.Version.CreatedBy, record.Version.CreatedAt)
	if err != nil {
		return productapplication.RedirectPolicyVersion{}, mapWriteError(err)
	}
	for _, value := range record.Policy.WebRedirectURIs {
		if _, err := tx.Exec(ctx, `INSERT INTO product_application.redirect_policy_entries(policy_id,entry_type,value) VALUES($1,'web_redirect',$2)`, record.Version.PolicyID, value); err != nil {
			return productapplication.RedirectPolicyVersion{}, err
		}
	}
	for _, value := range record.Policy.AllowedOrigins {
		if _, err := tx.Exec(ctx, `INSERT INTO product_application.redirect_policy_entries(policy_id,entry_type,value) VALUES($1,'origin',$2)`, record.Version.PolicyID, value); err != nil {
			return productapplication.RedirectPolicyVersion{}, err
		}
	}
	for _, value := range record.Policy.DeepLinks {
		raw, err := json.Marshal(value)
		if err != nil {
			return productapplication.RedirectPolicyVersion{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO product_application.redirect_policy_entries(policy_id,entry_type,value) VALUES($1,'deep_link',$2)`, record.Version.PolicyID, string(raw)); err != nil {
			return productapplication.RedirectPolicyVersion{}, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE product_application.product_applications SET current_redirect_policy_version=$3,context_version=context_version+1,updated_at=$4 WHERE product_id=$1 AND application_id=$2`, record.Version.ProductID, record.Version.ApplicationID, record.Version.Version, record.Version.CreatedAt)
	if err != nil {
		return productapplication.RedirectPolicyVersion{}, err
	}
	payload := eventPayload(record.Version.AuditID, record.ActorID, record.TraceID, "product.application.security.manage", "product_application.redirects_changed.v1", record.Version.ProductID, record.Version.ApplicationID, map[string]any{"policy_id": record.Version.PolicyID, "version": record.Version.Version, "content_sha256": record.Version.ContentSHA256})
	if err := insertOutbox(ctx, tx, record.EventID, record.Version.ApplicationID, "product_application.redirects_changed.v1", payload, record.Version.CreatedAt); err != nil {
		return productapplication.RedirectPolicyVersion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return productapplication.RedirectPolicyVersion{}, err
	}
	return record.Version, nil
}

func (r *Repository) SuspendApplication(ctx context.Context, record productapplication.SuspendRecord) (productapplication.SuspendResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return productapplication.SuspendResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if replay, reserved, err := reserveIdempotency[productapplication.SuspendResult](ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	var status productapplication.Status
	err = tx.QueryRow(ctx, `SELECT status FROM product_application.product_applications WHERE product_id=$1 AND application_id=$2 FOR UPDATE`, record.ProductID, record.ApplicationID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return productapplication.SuspendResult{}, productapplication.ErrNotFound
	}
	if err != nil {
		return productapplication.SuspendResult{}, err
	}
	var affected int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM product_application.application_client_bindings WHERE product_id=$1 AND application_id=$2 AND status='active'`, record.ProductID, record.ApplicationID).Scan(&affected); err != nil {
		return productapplication.SuspendResult{}, err
	}
	result := productapplication.SuspendResult{ApplicationID: record.ApplicationID, Status: productapplication.StatusSuspended, SessionPolicy: record.Policy, AffectedClientBindings: affected, AuditID: record.AuditID}
	if status == productapplication.StatusActive {
		if _, err := tx.Exec(ctx, `UPDATE product_application.product_applications SET status='suspended',context_version=context_version+1,updated_at=$3 WHERE product_id=$1 AND application_id=$2`, record.ProductID, record.ApplicationID, record.Now); err != nil {
			return productapplication.SuspendResult{}, err
		}
		payload := eventPayload(record.AuditID, record.ActorID, record.TraceID, "product.application.manage", "product_application.suspended.v1", record.ProductID, record.ApplicationID, map[string]any{"reason": record.Reason, "session_policy": record.Policy, "affected_client_bindings": affected})
		if err := insertOutbox(ctx, tx, record.EventID, record.ApplicationID, "product_application.suspended.v1", payload, record.Now); err != nil {
			return productapplication.SuspendResult{}, err
		}
	} else {
		var priorAuditID string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(payload->>'audit_id','') FROM product_application.outbox_events WHERE aggregate_id=$1 AND event_type='product_application.suspended.v1' ORDER BY occurred_at DESC LIMIT 1`, record.ApplicationID).Scan(&priorAuditID); err == nil {
			result.AuditID = priorAuditID
		} else if errors.Is(err, pgx.ErrNoRows) {
			result.AuditID = ""
		} else {
			return productapplication.SuspendResult{}, err
		}
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, record.ApplicationID, result); err != nil {
		return productapplication.SuspendResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return productapplication.SuspendResult{}, err
	}
	return result, nil
}

func (r *Repository) ResolveApplication(ctx context.Context, query productapplication.ResolveQuery) (productapplication.Application, productapplication.ClientBinding, error) {
	row := r.pool.QueryRow(ctx, `SELECT a.application_id,a.product_id,a.application_code,a.name,a.platform,a.distribution_channel,a.release_track,a.status,a.context_version,a.current_redirect_policy_version,a.created_at,a.updated_at,b.binding_id,b.client_id,b.environment,b.status,b.created_at,b.updated_at FROM product_application.application_client_bindings b JOIN product_application.product_applications a ON a.product_id=b.product_id AND a.application_id=b.application_id WHERE b.product_id=$1 AND b.client_id=$2 AND b.environment=$3`, query.ProductID, query.ClientID, query.Environment)
	var a productapplication.Application
	var b productapplication.ClientBinding
	err := row.Scan(&a.ApplicationID, &a.ProductID, &a.ApplicationCode, &a.Name, &a.Platform, &a.DistributionChannel, &a.ReleaseTrack, &a.Status, &a.ContextVersion, &a.CurrentRedirectPolicyVersion, &a.CreatedAt, &a.UpdatedAt, &b.BindingID, &b.ClientID, &b.Environment, &b.Status, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return productapplication.Application{}, productapplication.ClientBinding{}, productapplication.ErrContextRejected
	}
	if err != nil {
		return productapplication.Application{}, productapplication.ClientBinding{}, err
	}
	b.ProductID, b.ApplicationID = a.ProductID, a.ApplicationID
	return a, b, nil
}

func (r *Repository) ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]productapplication.ClaimedOutboxEvent, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT event_id,aggregate_id,event_type,payload,attempt_count FROM product_application.outbox_events WHERE published_at IS NULL AND dead=FALSE AND next_attempt_at <= $1 ORDER BY occurred_at,event_id LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]productapplication.ClaimedOutboxEvent, 0)
	for rows.Next() {
		var event productapplication.ClaimedOutboxEvent
		if err := rows.Scan(&event.EventID, &event.AggregateID, &event.EventType, &event.Payload, &event.AttemptCount); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range result {
		result[i].AttemptCount++
		if _, err := tx.Exec(ctx, `UPDATE product_application.outbox_events SET attempt_count=attempt_count+1,next_attempt_at=$2 WHERE event_id=$1`, result[i].EventID, now.Add(30*time.Second)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) MarkOutboxPublished(ctx context.Context, eventID string, now time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE product_application.outbox_events SET published_at=COALESCE(published_at,$2),last_error=NULL WHERE event_id=$1`, eventID, now)
	return err
}

func (r *Repository) MarkOutboxFailed(ctx context.Context, eventID, summary string, next time.Time, dead bool) error {
	_, err := r.pool.Exec(ctx, `UPDATE product_application.outbox_events SET next_attempt_at=$2,last_error=$3,dead=$4 WHERE event_id=$1 AND published_at IS NULL`, eventID, next, summary, dead)
	return err
}

func (r *Repository) redirectVersion(ctx context.Context, tx pgx.Tx, productID, applicationID string, version int64) (productapplication.RedirectPolicyVersion, error) {
	var result productapplication.RedirectPolicyVersion
	err := tx.QueryRow(ctx, `SELECT p.policy_id,p.product_id,p.application_id,p.version,p.content_sha256,p.created_by,p.created_at,COALESCE((SELECT e.payload->>'audit_id' FROM product_application.outbox_events e WHERE e.aggregate_id=p.application_id AND e.event_type='product_application.redirects_changed.v1' AND e.payload#>>'{redacted_summary,version}'=p.version::text ORDER BY e.occurred_at DESC LIMIT 1),'') FROM product_application.redirect_policy_versions p WHERE p.product_id=$1 AND p.application_id=$2 AND p.version=$3`, productID, applicationID, version).Scan(&result.PolicyID, &result.ProductID, &result.ApplicationID, &result.Version, &result.ContentSHA256, &result.CreatedBy, &result.CreatedAt, &result.AuditID)
	return result, err
}

const applicationSelect = `SELECT a.application_id,a.product_id,a.application_code,a.name,a.platform,a.distribution_channel,a.release_track,a.status,a.context_version,a.current_redirect_policy_version,a.created_at,a.updated_at,COALESCE((SELECT e.payload->>'audit_id' FROM product_application.outbox_events e WHERE e.aggregate_id=a.application_id AND e.event_type='product_application.created.v1' ORDER BY e.occurred_at LIMIT 1),'') FROM product_application.product_applications a`

type rowScanner interface{ Scan(...any) error }

func scanApplication(row rowScanner) (productapplication.Application, error) {
	var a productapplication.Application
	err := row.Scan(&a.ApplicationID, &a.ProductID, &a.ApplicationCode, &a.Name, &a.Platform, &a.DistributionChannel, &a.ReleaseTrack, &a.Status, &a.ContextVersion, &a.CurrentRedirectPolicyVersion, &a.CreatedAt, &a.UpdatedAt, &a.AuditID)
	return a, err
}

func loadIdempotency[T any](ctx context.Context, tx pgx.Tx, key productapplication.Idempotency) (T, bool, error) {
	var zero T
	var requestDigest, state string
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT request_digest,state,response_json FROM product_application.idempotency_records WHERE operation=$1 AND actor_id=$2 AND scope_id=$3 AND key_digest=$4 FOR UPDATE`, key.Operation, key.ActorID, key.ScopeID, key.KeyDigest).Scan(&requestDigest, &state, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, false, nil
	}
	if err != nil {
		return zero, false, err
	}
	if requestDigest != key.RequestDigest {
		return zero, true, productapplication.ErrIdempotencyConflict
	}
	if state != "completed" || len(raw) == 0 {
		return zero, true, productapplication.ErrOperationInProgress
	}
	if err := json.Unmarshal(raw, &zero); err != nil {
		return zero, true, fmt.Errorf("decode product application idempotency response: %w", err)
	}
	return zero, true, nil
}

func reserveIdempotency[T any](ctx context.Context, tx pgx.Tx, key productapplication.Idempotency) (T, bool, error) {
	var zero T
	if replay, found, err := loadIdempotency[T](ctx, tx, key); err != nil || found {
		return replay, false, err
	}
	result, err := tx.Exec(ctx, `INSERT INTO product_application.idempotency_records(operation,actor_id,scope_id,key_digest,request_digest,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,'pending',$6,$6) ON CONFLICT DO NOTHING`, key.Operation, key.ActorID, key.ScopeID, key.KeyDigest, key.RequestDigest, key.Now)
	if err != nil {
		return zero, false, err
	}
	if result.RowsAffected() == 1 {
		return zero, true, nil
	}
	replay, found, err := loadIdempotency[T](ctx, tx, key)
	if err != nil {
		return zero, false, err
	}
	if !found {
		return zero, false, productapplication.ErrOperationInProgress
	}
	return replay, false, nil
}

func completeIdempotency(ctx context.Context, tx pgx.Tx, key productapplication.Idempotency, resourceID string, response any) error {
	raw, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE product_application.idempotency_records SET resource_id=$5,state='completed',response_json=$6,updated_at=$7 WHERE operation=$1 AND actor_id=$2 AND scope_id=$3 AND key_digest=$4`, key.Operation, key.ActorID, key.ScopeID, key.KeyDigest, resourceID, raw, key.Now)
	return err
}

func eventPayload(auditID, actorID, traceID, permission, action, productID, applicationID string, summary map[string]any) map[string]any {
	return map[string]any{"audit_id": auditID, "actor_id": actorID, "permission": permission, "scope_type": "product", "scope_id": productID, "product_id": productID, "action": action, "target_type": "product_application", "target_id": applicationID, "result": "success", "trace_id": traceID, "risk_level": "normal", "redacted_summary": summary}
}

func insertOutbox(ctx context.Context, tx pgx.Tx, eventID, aggregateID, eventType string, payload map[string]any, now time.Time) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_application.outbox_events(event_id,aggregate_id,event_type,payload,occurred_at,next_attempt_at) VALUES($1,$2,$3,$4,$5,$5)`, eventID, aggregateID, eventType, raw, now)
	return err
}

func mapWriteError(err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return productapplication.ErrConflict
	}
	return err
}
