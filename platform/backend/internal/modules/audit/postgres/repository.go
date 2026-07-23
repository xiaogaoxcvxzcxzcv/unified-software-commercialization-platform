package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/audit"
)

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) Append(ctx context.Context, event audit.Event) error {
	summary, err := json.Marshal(event.RedactedSummary)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO audit.events(audit_id,occurred_at,actor_id,permission,scope_type,scope_id,product_id,tenant_id,action,target_type,target_id,result,reason_code,trace_id,risk_level,redacted_summary) VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),$9,$10,$11,$12,NULLIF($13,''),$14,$15,$16) ON CONFLICT(audit_id) DO NOTHING`, event.AuditID, event.OccurredAt, event.ActorID, event.Permission, event.ScopeType, event.ScopeID, event.ProductID, event.TenantID, event.Action, event.TargetType, event.TargetID, event.Result, event.ReasonCode, event.TraceID, event.RiskLevel, summary)
	return err
}

func (r *Repository) Query(ctx context.Context, query audit.RepositoryQuery) ([]audit.Event, error) {
	var statement strings.Builder
	statement.WriteString(`SELECT audit_id,occurred_at,actor_id,COALESCE(permission,''),COALESCE(scope_type,''),COALESCE(scope_id,''),COALESCE(product_id,''),COALESCE(tenant_id,''),action,target_type,target_id,result,COALESCE(reason_code,''),trace_id,risk_level,redacted_summary FROM audit.events WHERE TRUE`)
	arguments := make([]any, 0, 6)
	add := func(clause string, value any) {
		arguments = append(arguments, value)
		statement.WriteString(fmt.Sprintf(clause, len(arguments)))
	}
	if query.TraceID != "" {
		add(" AND trace_id=$%d", query.TraceID)
	}
	switch query.TargetScope.Type {
	case "product":
		add(" AND product_id=$%d", query.TargetScope.ProductID)
	case "tenant":
		add(" AND product_id=$%d", query.TargetScope.ProductID)
		add(" AND tenant_id=$%d", query.TargetScope.TenantID)
	}
	if query.After != nil {
		arguments = append(arguments, query.After.OccurredAt, query.After.AuditID)
		statement.WriteString(fmt.Sprintf(" AND (occurred_at,audit_id) < ($%d,$%d)", len(arguments)-1, len(arguments)))
	}
	arguments = append(arguments, query.Limit)
	statement.WriteString(fmt.Sprintf(" ORDER BY occurred_at DESC,audit_id DESC LIMIT $%d", len(arguments)))

	rows, err := r.pool.Query(ctx, statement.String(), arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]audit.Event, 0, query.Limit)
	for rows.Next() {
		var event audit.Event
		var summary []byte
		if err := rows.Scan(
			&event.AuditID, &event.OccurredAt, &event.ActorID, &event.Permission,
			&event.ScopeType, &event.ScopeID, &event.ProductID, &event.TenantID,
			&event.Action, &event.TargetType, &event.TargetID, &event.Result,
			&event.ReasonCode, &event.TraceID, &event.RiskLevel, &summary,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(summary, &event.RedactedSummary); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *Repository) GetByID(ctx context.Context, auditID string) (audit.Event, bool, error) {
	row := r.pool.QueryRow(ctx, `SELECT audit_id,occurred_at,actor_id,COALESCE(permission,''),COALESCE(scope_type,''),COALESCE(scope_id,''),COALESCE(product_id,''),COALESCE(tenant_id,''),action,target_type,target_id,result,COALESCE(reason_code,''),trace_id,risk_level,redacted_summary FROM audit.events WHERE audit_id=$1`, auditID)
	var event audit.Event
	var summary []byte
	if err := row.Scan(&event.AuditID, &event.OccurredAt, &event.ActorID, &event.Permission, &event.ScopeType, &event.ScopeID, &event.ProductID, &event.TenantID, &event.Action, &event.TargetType, &event.TargetID, &event.Result, &event.ReasonCode, &event.TraceID, &event.RiskLevel, &summary); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return audit.Event{}, false, nil
		}
		return audit.Event{}, false, err
	}
	if err := json.Unmarshal(summary, &event.RedactedSummary); err != nil {
		return audit.Event{}, false, err
	}
	return event, true, nil
}
