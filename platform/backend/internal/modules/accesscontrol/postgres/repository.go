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
	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
)

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) ResolveSnapshot(ctx context.Context, adminUserID string, now time.Time) (accesscontrol.Snapshot, error) {
	var version int64
	err := r.pool.QueryRow(ctx, `SELECT authorization_version FROM access_control.admin_authorization_versions WHERE admin_user_id=$1`, adminUserID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return accesscontrol.Snapshot{}, accesscontrol.ErrNoActiveScope
	}
	if err != nil {
		return accesscontrol.Snapshot{}, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT r.role_code, rp.permission_code, b.scope_type, COALESCE(b.scope_id,''), COALESCE(b.product_id,''), COALESCE(b.tenant_id,'')
		FROM access_control.admin_scope_bindings b
		JOIN access_control.admin_roles r ON r.role_id=b.role_id AND r.status='active'
		JOIN access_control.admin_role_permissions rp ON rp.role_id=r.role_id
		WHERE b.admin_user_id=$1 AND b.status='active' AND b.effective_from <= $2 AND (b.expires_at IS NULL OR b.expires_at > $2)
		ORDER BY r.role_code, rp.permission_code, b.scope_type, b.scope_id`, adminUserID, now)
	if err != nil {
		return accesscontrol.Snapshot{}, err
	}
	defer rows.Close()
	roles := map[string]struct{}{}
	permissions := map[string]struct{}{}
	scopes := map[accesscontrol.Scope]struct{}{}
	for rows.Next() {
		var role, permission string
		var scope accesscontrol.Scope
		if err := rows.Scan(&role, &permission, &scope.Type, &scope.ID, &scope.ProductID, &scope.TenantID); err != nil {
			return accesscontrol.Snapshot{}, err
		}
		roles[role], permissions[permission], scopes[scope] = struct{}{}, struct{}{}, struct{}{}
	}
	if err := rows.Err(); err != nil {
		return accesscontrol.Snapshot{}, err
	}
	result := accesscontrol.Snapshot{AuthorizationVersion: version}
	for role := range roles {
		result.Roles = append(result.Roles, role)
	}
	for permission := range permissions {
		result.Permissions = append(result.Permissions, permission)
	}
	for scope := range scopes {
		result.Scopes = append(result.Scopes, scope)
	}
	if len(result.Scopes) == 0 {
		return accesscontrol.Snapshot{}, accesscontrol.ErrNoActiveScope
	}
	return result, nil
}

func (r *Repository) BootstrapPlatformAdmin(ctx context.Context, command accesscontrol.BootstrapCommand, catalog accesscontrol.PermissionCatalog) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO access_control.admin_roles(role_id,role_code,display_name,status,created_at,updated_at) VALUES($1,'super_admin','Super Administrator','active',$2,$2) ON CONFLICT(role_code) DO UPDATE SET status='active',updated_at=EXCLUDED.updated_at`, command.RoleID, command.Now)
	if err != nil {
		return err
	}
	var roleID string
	if err := tx.QueryRow(ctx, `SELECT role_id FROM access_control.admin_roles WHERE role_code='super_admin'`).Scan(&roleID); err != nil {
		return err
	}
	for _, permission := range catalog.Definitions() {
		if _, err := tx.Exec(ctx, `INSERT INTO access_control.admin_permissions(permission_code,description,risk_level) VALUES($1,$2,$3) ON CONFLICT(permission_code) DO UPDATE SET description=EXCLUDED.description,risk_level=EXCLUDED.risk_level`, permission.Code, permission.Description, permission.Risk); err != nil {
			return err
		}
		if !permission.GrantsPlatformSuperAdminOnBootstrap() {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO access_control.admin_role_permissions(role_id,permission_code) VALUES($1,$2) ON CONFLICT DO NOTHING`, roleID, permission.Code); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO access_control.admin_scope_bindings(binding_id,admin_user_id,role_id,scope_type,status,effective_from,created_at,updated_at) VALUES($1,$2,$3,'platform','active',$4,$4,$4) ON CONFLICT DO NOTHING`, command.BindingID, command.AdminUserID, roleID, command.Now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE access_control.admin_scope_bindings SET status='active',updated_at=$3 WHERE admin_user_id=$1 AND role_id=$2 AND scope_type='platform'`, command.AdminUserID, roleID, command.Now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO access_control.admin_authorization_versions(admin_user_id,authorization_version,updated_at) VALUES($1,1,$2) ON CONFLICT(admin_user_id) DO UPDATE SET authorization_version=access_control.admin_authorization_versions.authorization_version+1,updated_at=EXCLUDED.updated_at`, command.AdminUserID, command.Now)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) BindAdminScope(ctx context.Context, record accesscontrol.ScopeBindingRecord) (accesscontrol.AdminScopeBinding, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return accesscontrol.AdminScopeBinding{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if replay, reserved, err := reserveIdempotency(ctx, tx, record.Idempotency); err != nil || !reserved {
		return replay, err
	}
	var roleID string
	err = tx.QueryRow(ctx, `SELECT role_id FROM access_control.admin_roles WHERE role_code=$1 AND status='active' FOR SHARE`, record.Binding.RoleCode).Scan(&roleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return accesscontrol.AdminScopeBinding{}, accesscontrol.ErrRoleNotFound
	}
	if err != nil {
		return accesscontrol.AdminScopeBinding{}, err
	}
	binding := record.Binding
	_, err = tx.Exec(ctx, `INSERT INTO access_control.admin_scope_bindings(binding_id,admin_user_id,role_id,scope_type,scope_id,product_id,tenant_id,status,effective_from,expires_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,'active',$8,$9,$10,$10)`, binding.BindingID, binding.AdminUserID, roleID, binding.Scope.Type, nullableText(binding.Scope.ID), nullableText(binding.Scope.ProductID), nullableText(binding.Scope.TenantID), binding.EffectiveFrom, binding.ExpiresAt, binding.CreatedAt)
	if err != nil {
		return accesscontrol.AdminScopeBinding{}, mapScopeBindingWriteError(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO access_control.admin_authorization_versions(admin_user_id,authorization_version,updated_at) VALUES($1,1,$2) ON CONFLICT(admin_user_id) DO UPDATE SET authorization_version=access_control.admin_authorization_versions.authorization_version+1,updated_at=EXCLUDED.updated_at RETURNING authorization_version`, binding.AdminUserID, binding.CreatedAt).Scan(&binding.AuthorizationVersion); err != nil {
		return accesscontrol.AdminScopeBinding{}, err
	}
	payload := map[string]any{
		"audit_id": binding.AuditID, "actor_id": record.ActorID, "permission": "access.manage",
		"scope_type": binding.Scope.Type, "scope_id": binding.Scope.ID, "product_id": binding.Scope.ProductID, "tenant_id": binding.Scope.TenantID,
		"action": "access_control.admin_scope_bound.v1", "target_type": "admin_scope_binding", "target_id": binding.BindingID,
		"result": "success", "trace_id": record.TraceID, "risk_level": "high",
		"redacted_summary": map[string]any{"admin_user_id": binding.AdminUserID, "role_code": binding.RoleCode, "effective_from": binding.EffectiveFrom, "expires_at": binding.ExpiresAt},
	}
	if err := insertOutbox(ctx, tx, record.EventID, binding.BindingID, "access_control.admin_scope_bound.v1", payload, binding.CreatedAt); err != nil {
		return accesscontrol.AdminScopeBinding{}, err
	}
	if err := completeIdempotency(ctx, tx, record.Idempotency, binding); err != nil {
		return accesscontrol.AdminScopeBinding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return accesscontrol.AdminScopeBinding{}, err
	}
	return binding, nil
}

func (r *Repository) ClaimOutbox(ctx context.Context, now time.Time, limit int) ([]accesscontrol.ClaimedOutboxEvent, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT event_id,aggregate_id,event_type,payload,attempt_count FROM access_control.outbox_events WHERE published_at IS NULL AND dead=FALSE AND next_attempt_at <= $1 ORDER BY occurred_at,event_id LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]accesscontrol.ClaimedOutboxEvent, 0)
	for rows.Next() {
		var event accesscontrol.ClaimedOutboxEvent
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
		if _, err := tx.Exec(ctx, `UPDATE access_control.outbox_events SET attempt_count=attempt_count+1,next_attempt_at=$2 WHERE event_id=$1`, result[i].EventID, now.Add(30*time.Second)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) MarkOutboxPublished(ctx context.Context, eventID string, now time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE access_control.outbox_events SET published_at=COALESCE(published_at,$2),last_error=NULL WHERE event_id=$1`, eventID, now)
	return err
}

func (r *Repository) MarkOutboxFailed(ctx context.Context, eventID, summary string, next time.Time, dead bool) error {
	_, err := r.pool.Exec(ctx, `UPDATE access_control.outbox_events SET next_attempt_at=$2,last_error=$3,dead=$4 WHERE event_id=$1 AND published_at IS NULL`, eventID, next, summary, dead)
	return err
}

func reserveIdempotency(ctx context.Context, tx pgx.Tx, key accesscontrol.ScopeBindingIdempotency) (accesscontrol.AdminScopeBinding, bool, error) {
	if replay, found, err := loadIdempotency(ctx, tx, key); err != nil || found {
		return replay, false, err
	}
	result, err := tx.Exec(ctx, `INSERT INTO access_control.scope_binding_idempotency_records(operation,actor_id,key_digest,request_digest,state,created_at,updated_at) VALUES($1,$2,$3,$4,'pending',$5,$5) ON CONFLICT DO NOTHING`, key.Operation, key.ActorID, key.KeyDigest, key.RequestDigest, key.Now)
	if err != nil {
		return accesscontrol.AdminScopeBinding{}, false, err
	}
	if result.RowsAffected() == 1 {
		return accesscontrol.AdminScopeBinding{}, true, nil
	}
	replay, found, err := loadIdempotency(ctx, tx, key)
	if err != nil {
		return accesscontrol.AdminScopeBinding{}, false, err
	}
	if !found {
		return accesscontrol.AdminScopeBinding{}, false, accesscontrol.ErrOperationInProgress
	}
	return replay, false, nil
}

func loadIdempotency(ctx context.Context, tx pgx.Tx, key accesscontrol.ScopeBindingIdempotency) (accesscontrol.AdminScopeBinding, bool, error) {
	var requestDigest, state string
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT request_digest,state,response_json FROM access_control.scope_binding_idempotency_records WHERE operation=$1 AND actor_id=$2 AND key_digest=$3 FOR UPDATE`, key.Operation, key.ActorID, key.KeyDigest).Scan(&requestDigest, &state, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return accesscontrol.AdminScopeBinding{}, false, nil
	}
	if err != nil {
		return accesscontrol.AdminScopeBinding{}, false, err
	}
	if requestDigest != key.RequestDigest {
		return accesscontrol.AdminScopeBinding{}, true, accesscontrol.ErrIdempotencyConflict
	}
	if state != "completed" || len(raw) == 0 {
		return accesscontrol.AdminScopeBinding{}, true, accesscontrol.ErrOperationInProgress
	}
	var result accesscontrol.AdminScopeBinding
	if err := json.Unmarshal(raw, &result); err != nil {
		return accesscontrol.AdminScopeBinding{}, true, fmt.Errorf("decode access control idempotency response: %w", err)
	}
	return result, true, nil
}

func completeIdempotency(ctx context.Context, tx pgx.Tx, key accesscontrol.ScopeBindingIdempotency, binding accesscontrol.AdminScopeBinding) error {
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE access_control.scope_binding_idempotency_records SET state='completed',binding_id=$4,response_json=$5,updated_at=$6 WHERE operation=$1 AND actor_id=$2 AND key_digest=$3`, key.Operation, key.ActorID, key.KeyDigest, binding.BindingID, raw, key.Now)
	return err
}

func insertOutbox(ctx context.Context, tx pgx.Tx, eventID, aggregateID, eventType string, payload map[string]any, now time.Time) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO access_control.outbox_events(event_id,aggregate_id,event_type,payload,occurred_at,next_attempt_at) VALUES($1,$2,$3,$4,$5,$5)`, eventID, aggregateID, eventType, raw, now)
	return err
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func mapScopeBindingWriteError(err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return accesscontrol.ErrScopeBindingConflict
	}
	return err
}
