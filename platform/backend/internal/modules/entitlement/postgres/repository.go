package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/entitlement"
)

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) CheckEntitlement(ctx context.Context, query entitlement.CheckQuery) (entitlement.CheckDecision, error) {
	if r == nil || r.pool == nil {
		return entitlement.CheckDecision{}, entitlement.ErrInvalidArgument
	}
	summary, err := r.GetCurrentEntitlements(ctx, entitlement.CurrentQuery{ProductID: query.ProductID, TenantID: query.TenantID, UserID: query.UserID})
	serverTime := query.ServerTime.UTC()
	if serverTime.IsZero() {
		serverTime = time.Now().UTC()
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return entitlement.CheckDecision{Allowed: false, DecisionStage: "entitlement", ReasonCode: entitlement.ReasonEntitlementRequired, ServerTime: serverTime, Features: map[string]any{}}, nil
	}
	if err != nil {
		return entitlement.CheckDecision{}, err
	}
	missing, expired := false, false
	for _, feature := range query.RequestedFeatures {
		value, ok := summary.EffectiveFeatures[feature]
		if !ok {
			missing = true
			continue
		}
		if allowed, ok := value.(bool); ok && !allowed {
			missing = true
		}
	}
	if summary.ValidUntil != nil && !summary.ValidUntil.After(serverTime) {
		expired = true
	}
	decision := entitlement.CheckDecision{
		Allowed:           !missing && !expired,
		DecisionStage:     "entitlement",
		Revision:          summary.Revision,
		Features:          summary.EffectiveFeatures,
		PlanCode:          summary.PlanCode,
		ValidUntil:        summary.ValidUntil,
		OfflineGraceUntil: summary.OfflineGraceUntil,
		ServerTime:        serverTime,
	}
	if expired {
		decision.ReasonCode = entitlement.ReasonEntitlementExpired
	} else if missing {
		decision.ReasonCode = entitlement.ReasonEntitlementRequired
	}
	return decision, nil
}

func (r *Repository) GrantEntitlement(ctx context.Context, record entitlement.WriteRecord) (entitlement.GrantResult, error) {
	return r.write(ctx, record)
}

func (r *Repository) ExtendEntitlement(ctx context.Context, record entitlement.WriteRecord) (entitlement.GrantResult, error) {
	return r.write(ctx, record)
}

func (r *Repository) ReplaceEntitlement(ctx context.Context, record entitlement.WriteRecord) (entitlement.GrantResult, error) {
	return r.write(ctx, record)
}

func (r *Repository) RevokeEntitlement(ctx context.Context, record entitlement.WriteRecord) (entitlement.GrantResult, error) {
	return r.write(ctx, record)
}

func (r *Repository) write(ctx context.Context, record entitlement.WriteRecord) (entitlement.GrantResult, error) {
	if r == nil || r.pool == nil {
		return entitlement.GrantResult{}, entitlement.ErrInvalidArgument
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return entitlement.GrantResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if replay, reserved, err := reserveIdempotency(ctx, tx, record); err != nil || !reserved {
		return replay, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, scopeLockKey(record.ProductID, record.TenantID, record.UserID)); err != nil {
		return entitlement.GrantResult{}, err
	}
	before, existed, err := lockRevision(ctx, tx, record)
	if err != nil {
		return entitlement.GrantResult{}, err
	}
	if record.Operation != entitlement.EffectGrant && (!existed || before.Revision != record.ExpectedRevision) {
		if err := failIdempotency(ctx, tx, record); err != nil {
			return entitlement.GrantResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return entitlement.GrantResult{}, err
		}
		return entitlement.GrantResult{}, entitlement.ErrOperationConflict
	}
	if record.Operation == entitlement.EffectRevoke {
		if err := hydrateRevokeTarget(ctx, tx, &record); err != nil {
			return entitlement.GrantResult{}, err
		}
	}
	if err := insertGrant(ctx, tx, record); err != nil {
		if isUniqueViolation(err) {
			replay, replayErr := replayDuplicateSource(ctx, tx, record)
			if replayErr == nil {
				if commitErr := tx.Commit(ctx); commitErr != nil {
					return entitlement.GrantResult{}, commitErr
				}
				return replay, nil
			}
			return entitlement.GrantResult{}, replayErr
		}
		return entitlement.GrantResult{}, err
	}
	after, err := recomputeRevision(ctx, tx, record, before, existed)
	if err != nil {
		return entitlement.GrantResult{}, err
	}
	if err := insertLedger(ctx, tx, record, before, after); err != nil {
		return entitlement.GrantResult{}, err
	}
	if err := insertOutbox(ctx, tx, record, after); err != nil {
		return entitlement.GrantResult{}, err
	}
	result := resultFrom(record, after)
	if err := finishIdempotency(ctx, tx, record, result); err != nil {
		return entitlement.GrantResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return entitlement.GrantResult{}, err
	}
	return result, nil
}

func (r *Repository) GetCurrentEntitlements(ctx context.Context, query entitlement.CurrentQuery) (entitlement.EntitlementSummary, error) {
	if r == nil || r.pool == nil {
		return entitlement.EntitlementSummary{}, entitlement.ErrInvalidArgument
	}
	var summary entitlement.EntitlementSummary
	var features []byte
	var validUntil, graceUntil *time.Time
	var planCode *string
	err := r.pool.QueryRow(ctx, `SELECT product_id,tenant_id,user_id,version,decision_hash,effective_features,plan_code,valid_until,offline_grace_until,updated_at FROM entitlement.revisions WHERE product_id=$1 AND tenant_id=$2 AND user_id=$3`, query.ProductID, query.TenantID, query.UserID).
		Scan(&summary.ProductID, &summary.TenantID, &summary.UserID, &summary.Revision, &summary.DecisionHash, &features, &planCode, &validUntil, &graceUntil, &summary.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return entitlement.EntitlementSummary{
			ProductID: query.ProductID, TenantID: query.TenantID, UserID: query.UserID,
			EffectiveFeatures: map[string]any{},
		}, nil
	}
	if err != nil {
		return entitlement.EntitlementSummary{}, err
	}
	if err := json.Unmarshal(features, &summary.EffectiveFeatures); err != nil {
		return entitlement.EntitlementSummary{}, err
	}
	if planCode != nil {
		summary.PlanCode = *planCode
	}
	summary.ValidUntil = validUntil
	summary.OfflineGraceUntil = graceUntil
	return summary, nil
}

func (r *Repository) ListHistory(ctx context.Context, query entitlement.HistoryQuery) ([]entitlement.LedgerEntry, error) {
	if r == nil || r.pool == nil {
		return nil, entitlement.ErrInvalidArgument
	}
	rows, err := r.pool.Query(ctx, `SELECT ledger_id,product_id,tenant_id,user_id,operation_type,operation_id,COALESCE(source_type,''),COALESCE(source_id,''),COALESCE(grant_id,''),COALESCE(before_revision,0),COALESCE(after_revision,0),before_decision_hash,after_decision_hash,COALESCE(audit_id,''),trace_id,created_at FROM entitlement.ledger WHERE product_id=$1 AND tenant_id=$2 AND user_id=$3 ORDER BY created_at,ledger_id LIMIT $4`, query.ProductID, query.TenantID, query.UserID, query.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]entitlement.LedgerEntry, 0)
	for rows.Next() {
		var item entitlement.LedgerEntry
		if err := rows.Scan(&item.LedgerID, &item.ProductID, &item.TenantID, &item.UserID, &item.OperationType, &item.OperationID, &item.SourceType, &item.SourceID, &item.GrantID, &item.BeforeRevision, &item.AfterRevision, &item.BeforeDecisionHash, &item.AfterDecisionHash, &item.AuditID, &item.TraceID, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]entitlement.ClaimedOutboxEvent, error) {
	if r == nil || r.pool == nil || limit < 1 {
		return nil, entitlement.ErrInvalidArgument
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT event_id,aggregate_id,event_type,payload,occurred_at,attempt_count FROM entitlement.outbox_events WHERE published_at IS NULL AND dead=FALSE AND next_attempt_at <= $1 ORDER BY occurred_at,event_id LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	items := make([]entitlement.ClaimedOutboxEvent, 0)
	for rows.Next() {
		var item entitlement.ClaimedOutboxEvent
		var payload []byte
		if err := rows.Scan(&item.EventID, &item.AggregateID, &item.EventType, &payload, &item.OccurredAt, &item.AttemptCount); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(payload, &item.Payload); err != nil {
			item.PayloadError = "invalid entitlement outbox payload"
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
		if _, err := tx.Exec(ctx, `UPDATE entitlement.outbox_events SET attempt_count=attempt_count+1,next_attempt_at=$2 WHERE event_id=$1 AND published_at IS NULL AND dead=FALSE`, items[index].EventID, now.Add(30*time.Second)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *Repository) MarkOutboxPublished(ctx context.Context, eventID string, now time.Time) error {
	result, err := r.pool.Exec(ctx, `UPDATE entitlement.outbox_events SET published_at=COALESCE(published_at,$2),last_error=NULL WHERE event_id=$1`, eventID, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return entitlement.ErrOutboxEventNotFound
	}
	return nil
}

func (r *Repository) MarkOutboxFailed(ctx context.Context, eventID, summary string, next time.Time, dead bool) error {
	result, err := r.pool.Exec(ctx, `UPDATE entitlement.outbox_events SET next_attempt_at=$2,last_error=$3,dead=$4 WHERE event_id=$1 AND published_at IS NULL AND dead=FALSE`, eventID, next, summary, dead)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return entitlement.ErrOutboxEventNotFound
	}
	return nil
}

type revisionSnapshot struct {
	Revision          int64
	DecisionHash      []byte
	EffectiveFeatures map[string]any
	PlanCode          string
	ValidUntil        *time.Time
	OfflineGraceUntil *time.Time
	UpdatedAt         time.Time
}

func reserveIdempotency(ctx context.Context, tx pgx.Tx, record entitlement.WriteRecord) (entitlement.GrantResult, bool, error) {
	key := hex.EncodeToString(record.KeyHash)
	result, err := tx.Exec(ctx, `INSERT INTO entitlement.idempotency_records(product_id,tenant_id,user_id,idempotency_key,operation,request_hash,response_document,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'{}'::jsonb,'pending',$7,$7) ON CONFLICT DO NOTHING`, record.ProductID, record.TenantID, record.UserID, key, record.Operation, record.RequestHash, record.Now)
	if err != nil {
		return entitlement.GrantResult{}, false, err
	}
	if result.RowsAffected() == 1 {
		return entitlement.GrantResult{}, true, nil
	}
	var storedDigest []byte
	var state string
	var response []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,state,response_document FROM entitlement.idempotency_records WHERE product_id=$1 AND tenant_id=$2 AND user_id=$3 AND idempotency_key=$4 FOR UPDATE`, record.ProductID, record.TenantID, record.UserID, key).Scan(&storedDigest, &state, &response)
	if err != nil {
		return entitlement.GrantResult{}, false, err
	}
	if !bytes.Equal(storedDigest, record.RequestHash) || state != "completed" {
		return entitlement.GrantResult{}, false, entitlement.ErrOperationConflict
	}
	var replay entitlement.GrantResult
	if err := json.Unmarshal(response, &replay); err != nil {
		return entitlement.GrantResult{}, false, err
	}
	return replay, false, nil
}

func failIdempotency(ctx context.Context, tx pgx.Tx, record entitlement.WriteRecord) error {
	_, err := tx.Exec(ctx, `UPDATE entitlement.idempotency_records SET state='failed',updated_at=$5 WHERE product_id=$1 AND tenant_id=$2 AND user_id=$3 AND idempotency_key=$4`, record.ProductID, record.TenantID, record.UserID, hex.EncodeToString(record.KeyHash), record.Now)
	return err
}

func finishIdempotency(ctx context.Context, tx pgx.Tx, record entitlement.WriteRecord, result entitlement.GrantResult) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE entitlement.idempotency_records SET state='completed',result_grant_id=$5,result_revision=$6,response_document=$7,updated_at=$8 WHERE product_id=$1 AND tenant_id=$2 AND user_id=$3 AND idempotency_key=$4 AND request_hash=$9`, record.ProductID, record.TenantID, record.UserID, hex.EncodeToString(record.KeyHash), result.GrantID, result.Revision, raw, record.Now, record.RequestHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return entitlement.ErrOperationConflict
	}
	return nil
}

func lockRevision(ctx context.Context, tx pgx.Tx, record entitlement.WriteRecord) (revisionSnapshot, bool, error) {
	var snapshot revisionSnapshot
	var features []byte
	var plan *string
	err := tx.QueryRow(ctx, `SELECT version,decision_hash,effective_features,plan_code,valid_until,offline_grace_until,updated_at FROM entitlement.revisions WHERE product_id=$1 AND tenant_id=$2 AND user_id=$3 FOR UPDATE`, record.ProductID, record.TenantID, record.UserID).Scan(&snapshot.Revision, &snapshot.DecisionHash, &features, &plan, &snapshot.ValidUntil, &snapshot.OfflineGraceUntil, &snapshot.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return revisionSnapshot{EffectiveFeatures: map[string]any{}}, false, nil
	}
	if err != nil {
		return revisionSnapshot{}, false, err
	}
	if err := json.Unmarshal(features, &snapshot.EffectiveFeatures); err != nil {
		return revisionSnapshot{}, false, err
	}
	if plan != nil {
		snapshot.PlanCode = *plan
	}
	return snapshot, true, nil
}

func hydrateRevokeTarget(ctx context.Context, tx pgx.Tx, record *entitlement.WriteRecord) error {
	if record.PolicyID != "" && record.PolicyVersion > 0 {
		return nil
	}
	var sourceType entitlement.SourceType
	var sourceID, sourceEffectID string
	query := `SELECT policy_id,policy_version,source_type,source_id,source_effect_id FROM entitlement.grants WHERE product_id=$1 AND tenant_id=$2 AND user_id=$3`
	args := []any{record.ProductID, record.TenantID, record.UserID}
	if record.TargetGrantID != "" {
		query += ` AND grant_id=$4`
		args = append(args, record.TargetGrantID)
	} else {
		query += ` AND source_type=$4 AND source_id=$5 AND source_effect_id=$6`
		args = append(args, record.Source.Type, record.Source.SourceID, record.Source.SourceEffectID)
	}
	query += ` ORDER BY created_at DESC LIMIT 1`
	if err := tx.QueryRow(ctx, query, args...).Scan(&record.PolicyID, &record.PolicyVersion, &sourceType, &sourceID, &sourceEffectID); err != nil {
		return err
	}
	if record.Source.Type == "" {
		record.Source.Type = sourceType
		record.Source.SourceID = record.TargetGrantID
		record.Source.SourceEffectID = record.GrantID
	}
	_ = sourceID
	_ = sourceEffectID
	return nil
}

func insertGrant(ctx context.Context, tx pgx.Tx, record entitlement.WriteRecord) error {
	validUntil, err := calculateValidUntil(record)
	if err != nil {
		return err
	}
	idempotencyKey := hex.EncodeToString(record.KeyHash)
	_, err = tx.Exec(ctx, `INSERT INTO entitlement.grants(grant_id,product_id,tenant_id,user_id,policy_id,policy_version,effect,source_type,source_id,source_effect_id,idempotency_key,valid_from,valid_until,actor_type,actor_id,reason_code,request_hash,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$12)`,
		record.GrantID, record.ProductID, record.TenantID, record.UserID, record.PolicyID, record.PolicyVersion, record.Operation, record.Source.Type, record.Source.SourceID, record.Source.SourceEffectID, idempotencyKey, record.Now, validUntil, record.Actor.Type, record.Actor.ID, record.ReasonCode, record.RequestHash)
	return err
}

func recomputeRevision(ctx context.Context, tx pgx.Tx, record entitlement.WriteRecord, before revisionSnapshot, existed bool) (revisionSnapshot, error) {
	rows, err := tx.Query(ctx, `
		SELECT g.grant_id,g.policy_id,p.policy_code,p.features,g.valid_until,p.offline_grace_max_seconds,g.created_at
		FROM entitlement.grants g
		JOIN entitlement.policies p ON p.policy_id=g.policy_id
		WHERE g.product_id=$1 AND g.tenant_id=$2 AND g.user_id=$3
		  AND g.effect IN ('grant','extend','replace')
		  AND (g.valid_until IS NULL OR g.valid_until > $4)
		  AND NOT EXISTS (
		    SELECT 1 FROM entitlement.grants r
		    WHERE r.product_id=g.product_id AND r.tenant_id=g.tenant_id AND r.user_id=g.user_id
		      AND r.effect='revoke'
		      AND (r.source_id=g.grant_id OR (r.source_type=g.source_type AND r.source_id=g.source_id AND r.source_effect_id=g.source_effect_id))
		  )
		ORDER BY g.created_at,g.grant_id`, record.ProductID, record.TenantID, record.UserID, record.Now)
	if err != nil {
		return revisionSnapshot{}, err
	}
	defer rows.Close()
	features := map[string]any{}
	var planCode string
	var validUntil *time.Time
	var graceUntil *time.Time
	for rows.Next() {
		var grantID, policyID, policyCode string
		var rawFeatures []byte
		var grantValidUntil *time.Time
		var graceSeconds int64
		var createdAt time.Time
		if err := rows.Scan(&grantID, &policyID, &policyCode, &rawFeatures, &grantValidUntil, &graceSeconds, &createdAt); err != nil {
			return revisionSnapshot{}, err
		}
		_ = grantID
		_ = policyID
		_ = createdAt
		if planCode == "" {
			planCode = policyCode
		}
		mergeFeatures(features, rawFeatures)
		if grantValidUntil == nil {
			validUntil = nil
		} else if validUntil == nil || grantValidUntil.After(*validUntil) {
			copied := grantValidUntil.UTC()
			validUntil = &copied
		}
		if grantValidUntil != nil && graceSeconds > 0 {
			grace := grantValidUntil.Add(time.Duration(graceSeconds) * time.Second).UTC()
			if graceUntil == nil || grace.After(*graceUntil) {
				graceUntil = &grace
			}
		}
	}
	if err := rows.Err(); err != nil {
		return revisionSnapshot{}, err
	}
	raw, err := json.Marshal(features)
	if err != nil {
		return revisionSnapshot{}, err
	}
	hash := sha256.Sum256(raw)
	nextVersion := before.Revision + 1
	if !existed {
		nextVersion = 1
		_, err = tx.Exec(ctx, `INSERT INTO entitlement.revisions(revision_id,product_id,tenant_id,user_id,version,decision_hash,effective_features,plan_code,valid_until,offline_grace_until,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, revisionID(record), record.ProductID, record.TenantID, record.UserID, nextVersion, hash[:], raw, nullableString(planCode), validUntil, graceUntil, record.Now)
	} else {
		_, err = tx.Exec(ctx, `UPDATE entitlement.revisions SET version=$4,decision_hash=$5,effective_features=$6,plan_code=$7,valid_until=$8,offline_grace_until=$9,updated_at=$10 WHERE product_id=$1 AND tenant_id=$2 AND user_id=$3`, record.ProductID, record.TenantID, record.UserID, nextVersion, hash[:], raw, nullableString(planCode), validUntil, graceUntil, record.Now)
	}
	if err != nil {
		return revisionSnapshot{}, err
	}
	return revisionSnapshot{Revision: nextVersion, DecisionHash: hash[:], EffectiveFeatures: features, PlanCode: planCode, ValidUntil: validUntil, OfflineGraceUntil: graceUntil, UpdatedAt: record.Now}, nil
}

func insertLedger(ctx context.Context, tx pgx.Tx, record entitlement.WriteRecord, before, after revisionSnapshot) error {
	var beforeRevision any
	if before.Revision > 0 {
		beforeRevision = before.Revision
	}
	_, err := tx.Exec(ctx, `INSERT INTO entitlement.ledger(ledger_id,product_id,tenant_id,user_id,operation_type,operation_id,source_type,source_id,grant_id,before_revision,after_revision,before_decision_hash,after_decision_hash,audit_id,trace_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		record.LedgerID, record.ProductID, record.TenantID, record.UserID, record.Operation, record.GrantID, record.Source.Type, record.Source.SourceID, record.GrantID, beforeRevision, after.Revision, nullableBytes(before.DecisionHash), after.DecisionHash, record.AuditID, record.TraceID, record.Now)
	return err
}

func insertOutbox(ctx context.Context, tx pgx.Tx, record entitlement.WriteRecord, after revisionSnapshot) error {
	payload := map[string]any{
		"audit_id": record.AuditID, "product_id": record.ProductID, "tenant_id": record.TenantID,
		"user_id": record.UserID, "grant_id": record.GrantID, "revision": after.Revision,
		"operation": record.Operation, "actor_type": record.Actor.Type, "actor_id": record.Actor.ID,
		"reason_code": record.ReasonCode, "trace_id": record.TraceID, "occurred_at": record.Now,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO entitlement.outbox_events(event_id,aggregate_id,event_type,payload,occurred_at,next_attempt_at) VALUES($1,$2,$3,$4,$5,$5)`, record.OutboxEventID, aggregateID(record.ProductID, record.TenantID, record.UserID), eventType(record.Operation), raw, record.Now)
	return err
}

func replayDuplicateSource(ctx context.Context, tx pgx.Tx, record entitlement.WriteRecord) (entitlement.GrantResult, error) {
	var grantID string
	var revision int64
	var validUntil *time.Time
	err := tx.QueryRow(ctx, `SELECT g.grant_id,r.version,g.valid_until FROM entitlement.grants g JOIN entitlement.revisions r ON r.product_id=g.product_id AND r.tenant_id=g.tenant_id AND r.user_id=g.user_id WHERE g.product_id=$1 AND g.tenant_id=$2 AND g.user_id=$3 AND g.source_type=$4 AND g.source_id=$5 AND g.source_effect_id=$6`, record.ProductID, record.TenantID, record.UserID, record.Source.Type, record.Source.SourceID, record.Source.SourceEffectID).Scan(&grantID, &revision, &validUntil)
	if err != nil {
		return entitlement.GrantResult{}, entitlement.ErrSourceDuplicate
	}
	return entitlement.GrantResult{EntitlementID: aggregateID(record.ProductID, record.TenantID, record.UserID), GrantID: grantID, Revision: revision, ValidUntil: validUntil, AuditID: record.AuditID}, nil
}

func resultFrom(record entitlement.WriteRecord, after revisionSnapshot) entitlement.GrantResult {
	return entitlement.GrantResult{
		EntitlementID: aggregateID(record.ProductID, record.TenantID, record.UserID),
		GrantID:       record.GrantID,
		Revision:      after.Revision,
		ValidUntil:    after.ValidUntil,
		AuditID:       record.AuditID,
		Decision: entitlement.CheckDecision{
			Allowed:           len(after.EffectiveFeatures) > 0,
			DecisionStage:     "entitlement",
			Revision:          after.Revision,
			Features:          after.EffectiveFeatures,
			PlanCode:          after.PlanCode,
			ValidUntil:        after.ValidUntil,
			OfflineGraceUntil: after.OfflineGraceUntil,
			ServerTime:        record.Now,
		},
	}
}

func mergeFeatures(target map[string]any, raw []byte) {
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return
	}
	for _, item := range items {
		code, _ := item["feature_code"].(string)
		if code == "" {
			continue
		}
		if value, ok := item["value"]; ok {
			target[code] = value
		} else {
			target[code] = true
		}
	}
}

func calculateValidUntil(record entitlement.WriteRecord) (*time.Time, error) {
	switch record.Validity.Rule {
	case entitlement.ValidityFixedDuration:
		value := record.Now.Add(record.Validity.Duration).UTC()
		return &value, nil
	case entitlement.ValidityFixedEnd:
		value := record.Validity.FixedUntil.UTC()
		return &value, nil
	case entitlement.ValidityLifetime:
		return nil, nil
	default:
		if record.Operation == entitlement.EffectRevoke {
			return nil, nil
		}
		return nil, entitlement.ErrInvalidArgument
	}
}

func eventType(operation entitlement.Effect) string {
	switch operation {
	case entitlement.EffectExtend:
		return "entitlement.extended.v1"
	case entitlement.EffectReplace:
		return "entitlement.replaced.v1"
	case entitlement.EffectRevoke:
		return "entitlement.revoked.v1"
	case entitlement.EffectExpire:
		return "entitlement.expired.v1"
	default:
		return "entitlement.granted.v1"
	}
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}

func scopeLockKey(productID, tenantID, userID string) string {
	return fmt.Sprintf("%s:%d:%s:%d:%s:%d:%s", "entitlement", len(productID), productID, len(tenantID), tenantID, len(userID), userID)
}

func aggregateID(productID, tenantID, userID string) string {
	sum := sha256.Sum256([]byte(productID + "\x00" + tenantID + "\x00" + userID))
	return "entitlement_" + hex.EncodeToString(sum[:16])
}

func revisionID(record entitlement.WriteRecord) string {
	sum := sha256.Sum256([]byte(record.ProductID + "\x00" + record.TenantID + "\x00" + record.UserID))
	return "entitlement_revision_" + hex.EncodeToString(sum[:16])
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
