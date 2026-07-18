package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"platform.local/capability-platform/backend/internal/modules/identity"
)

func (r *Repository) ListAdminUsers(ctx context.Context, query identity.AdminUserRepositoryQuery) ([]identity.AdminUserRecord, error) {
	if r == nil || r.pool == nil {
		return nil, identity.ErrAdminEndUserInvalidArgument
	}
	return r.queryAdminUsers(ctx, query, "")
}

func (r *Repository) GetAdminUser(ctx context.Context, scope identity.AdminUserScope, userID string) (identity.AdminUserRecord, error) {
	if r == nil || r.pool == nil {
		return identity.AdminUserRecord{}, identity.ErrAdminEndUserInvalidArgument
	}
	items, err := r.queryAdminUsers(ctx, identity.AdminUserRepositoryQuery{Scope: scope, Limit: 1}, userID)
	if err != nil {
		return identity.AdminUserRecord{}, err
	}
	if len(items) != 1 {
		return identity.AdminUserRecord{}, identity.ErrAdminEndUserNotFound
	}
	return items[0], nil
}

func (r *Repository) queryAdminUsers(ctx context.Context, query identity.AdminUserRepositoryQuery, exactUserID string) ([]identity.AdminUserRecord, error) {
	var statement strings.Builder
	arguments := make([]any, 0, 12)
	add := func(clause string, value any) {
		arguments = append(arguments, value)
		statement.WriteString(fmt.Sprintf(clause, len(arguments)))
	}

	statement.WriteString(`WITH member AS (`)
	switch query.Scope.Type {
	case identity.AdminUserScopePlatform:
		statement.WriteString(`SELECT u.user_id,NULL::timestamptz AS member_since,MAX(s.last_seen_at) AS last_seen_at,COUNT(s.session_id) FILTER (WHERE s.revoked_at IS NULL AND s.refresh_expires_at>CURRENT_TIMESTAMP)::int AS active_count,COUNT(s.session_id)::int AS total_count FROM identity.users u LEFT JOIN identity.end_user_sessions s ON s.user_id=u.user_id GROUP BY u.user_id`)
	case identity.AdminUserScopeProduct:
		statement.WriteString(`SELECT s.user_id,MIN(s.created_at) AS member_since,MAX(s.last_seen_at) AS last_seen_at,COUNT(*) FILTER (WHERE s.revoked_at IS NULL AND s.refresh_expires_at>CURRENT_TIMESTAMP)::int AS active_count,COUNT(*)::int AS total_count FROM identity.end_user_sessions s WHERE s.product_id=`)
		add(`$%d`, query.Scope.ProductID)
		statement.WriteString(` GROUP BY s.user_id`)
	case identity.AdminUserScopeTenant:
		statement.WriteString(`SELECT s.user_id,MIN(s.created_at) AS member_since,MAX(s.last_seen_at) AS last_seen_at,COUNT(*) FILTER (WHERE s.revoked_at IS NULL AND s.refresh_expires_at>CURRENT_TIMESTAMP)::int AS active_count,COUNT(*)::int AS total_count FROM identity.end_user_sessions s WHERE s.product_id=`)
		add(`$%d`, query.Scope.ProductID)
		statement.WriteString(` AND s.tenant_id=`)
		add(`$%d`, query.Scope.TenantID)
		statement.WriteString(` GROUP BY s.user_id`)
	default:
		return nil, identity.ErrAdminEndUserInvalidArgument
	}
	statement.WriteString(`) SELECT u.user_id,u.user_version,u.account_status,COALESCE(p.display_name,u.display_name),u.created_at,m.member_since,m.last_seen_at,m.active_count,m.total_count,COALESCE(p.profile_version,1),COALESCE(p.display_name,u.display_name),p.avatar_ref,p.locale,p.timezone,COALESCE((SELECT jsonb_agg(jsonb_build_object('type',i.identifier_type,'masked_value',i.masked_value,'verified',i.verification_status='verified') ORDER BY i.identifier_type,i.identifier_id) FROM identity.user_identifiers i WHERE i.user_id=u.user_id),'[]'::jsonb) FROM member m JOIN identity.users u ON u.user_id=m.user_id LEFT JOIN identity.user_profiles p ON p.user_id=u.user_id WHERE TRUE`)
	if exactUserID != "" {
		add(` AND u.user_id=$%d`, exactUserID)
	}
	if query.AccountStatus != "" {
		add(` AND u.account_status=$%d`, query.AccountStatus)
	}
	if query.Text != "" {
		arguments = append(arguments, query.Text)
		textPosition := len(arguments)
		statement.WriteString(fmt.Sprintf(` AND (u.user_id=$%d OR lower(COALESCE(p.display_name,u.display_name)) LIKE lower($%d)||'%%'`, textPosition, textPosition))
		if query.IdentifierType != "" && len(query.IdentifierDigest) != 0 {
			arguments = append(arguments, query.IdentifierType, query.IdentifierDigest)
			statement.WriteString(fmt.Sprintf(` OR EXISTS (SELECT 1 FROM identity.user_identifiers si WHERE si.user_id=u.user_id AND si.identifier_type=$%d AND si.normalized_digest=$%d)`, len(arguments)-1, len(arguments)))
		}
		statement.WriteString(`)`)
	}
	if query.AfterUserID != "" {
		add(` AND u.user_id>$%d`, query.AfterUserID)
	}
	statement.WriteString(` ORDER BY u.user_id`)
	if query.Limit > 0 {
		add(` LIMIT $%d`, query.Limit)
	}

	rows, err := r.pool.Query(ctx, statement.String(), arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]identity.AdminUserRecord, 0)
	for rows.Next() {
		var item identity.AdminUserRecord
		var identifiers []byte
		if err := rows.Scan(&item.UserID, &item.UserVersion, &item.AccountStatus, &item.DisplayName, &item.CreatedAt, &item.MemberSince, &item.LastSeenAt, &item.ActiveSessionCount, &item.TotalSessionCount, &item.Profile.Version, &item.Profile.DisplayName, &item.Profile.AvatarRef, &item.Profile.Locale, &item.Profile.Timezone, &identifiers); err != nil {
			return nil, err
		}
		item.Position = item.UserID
		item.Profile.UserID = item.UserID
		if err := json.Unmarshal(identifiers, &item.Identifiers); err != nil {
			return nil, fmt.Errorf("decode administrator user identifiers: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) ResolveAdminUserMemberships(ctx context.Context, scope identity.AdminUserScope, userIDs []string) (map[string]bool, error) {
	if r == nil || r.pool == nil {
		return nil, identity.ErrAdminEndUserInvalidArgument
	}
	result := make(map[string]bool, len(userIDs))
	for _, userID := range userIDs {
		result[userID] = false
	}
	statement := `SELECT user_id FROM identity.users WHERE user_id=ANY($1)`
	arguments := []any{userIDs}
	switch scope.Type {
	case identity.AdminUserScopePlatform:
	case identity.AdminUserScopeProduct:
		statement = `SELECT DISTINCT user_id FROM identity.end_user_sessions WHERE product_id=$1 AND user_id=ANY($2)`
		arguments = []any{scope.ProductID, userIDs}
	case identity.AdminUserScopeTenant:
		statement = `SELECT DISTINCT user_id FROM identity.end_user_sessions WHERE product_id=$1 AND tenant_id=$2 AND user_id=ANY($3)`
		arguments = []any{scope.ProductID, scope.TenantID, userIDs}
	default:
		return nil, identity.ErrAdminEndUserInvalidArgument
	}
	rows, err := r.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		result[userID] = true
	}
	return result, rows.Err()
}

func (r *Repository) ListAdminUserSessions(ctx context.Context, query identity.AdminUserSessionQuery) ([]identity.AdminUserSessionRecord, error) {
	members, err := r.ResolveAdminUserMemberships(ctx, query.Scope, []string{query.UserID})
	if err != nil {
		return nil, err
	}
	if !members[query.UserID] {
		return nil, identity.ErrAdminEndUserNotFound
	}
	var statement strings.Builder
	arguments := []any{query.UserID}
	statement.WriteString(`SELECT session_id,product_id,application_id,tenant_id,environment,authentication_method,created_at,last_seen_at,absolute_expires_at,revoked_at FROM identity.end_user_sessions WHERE user_id=$1`)
	switch query.Scope.Type {
	case identity.AdminUserScopePlatform:
	case identity.AdminUserScopeProduct:
		arguments = append(arguments, query.Scope.ProductID)
		statement.WriteString(fmt.Sprintf(` AND product_id=$%d`, len(arguments)))
	case identity.AdminUserScopeTenant:
		arguments = append(arguments, query.Scope.ProductID, query.Scope.TenantID)
		statement.WriteString(fmt.Sprintf(` AND product_id=$%d AND tenant_id=$%d`, len(arguments)-1, len(arguments)))
	default:
		return nil, identity.ErrAdminEndUserInvalidArgument
	}
	if query.After != nil {
		arguments = append(arguments, query.After.CreatedAt, query.After.SessionID)
		statement.WriteString(fmt.Sprintf(` AND (created_at,session_id)<($%d,$%d)`, len(arguments)-1, len(arguments)))
	}
	arguments = append(arguments, query.Limit)
	statement.WriteString(fmt.Sprintf(` ORDER BY created_at DESC,session_id DESC LIMIT $%d`, len(arguments)))
	rows, err := r.pool.Query(ctx, statement.String(), arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]identity.AdminUserSessionRecord, 0)
	for rows.Next() {
		var item identity.AdminUserSessionRecord
		if err := rows.Scan(&item.SessionID, &item.ProductID, &item.ApplicationID, &item.TenantID, &item.Environment, &item.AuthenticationMethod, &item.CreatedAt, &item.LastSeenAt, &item.ExpiresAt, &item.RevokedAt); err != nil {
			return nil, err
		}
		item.Position = identity.AdminUserSessionPosition{CreatedAt: item.CreatedAt, SessionID: item.SessionID}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) SetGlobalUserSecurityStatus(ctx context.Context, record identity.AdminGlobalSecurityStatusRecord) (identity.AdminGlobalSecurityStatusResult, error) {
	if r == nil || r.pool == nil {
		return identity.AdminGlobalSecurityStatusResult{}, identity.ErrAdminEndUserInvalidArgument
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.AdminGlobalSecurityStatusResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	operation, scopeID := "admin_global_security_status", "platform"
	var replay identity.AdminGlobalSecurityStatusResult
	found, err := loadAdminIdempotency(ctx, tx, operation, scopeID, record.UserID, record.ActorDigest, record.KeyDigest, record.RequestDigest, &replay)
	if err != nil || found {
		return replay, err
	}
	var currentStatus string
	var currentVersion int64
	err = tx.QueryRow(ctx, `SELECT account_status,user_version FROM identity.users WHERE user_id=$1 FOR UPDATE`, record.UserID).Scan(&currentStatus, &currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.AdminGlobalSecurityStatusResult{}, finishAdminFailure(ctx, tx, operation, scopeID, record, "not_found", identity.ErrAdminEndUserNotFound)
	}
	if err != nil {
		return identity.AdminGlobalSecurityStatusResult{}, err
	}
	if currentVersion != record.ExpectedVersion {
		return identity.AdminGlobalSecurityStatusResult{}, finishAdminFailure(ctx, tx, operation, scopeID, record, "version_conflict", identity.ErrAdminEndUserVersionConflict)
	}
	var databaseNow time.Time
	if err := tx.QueryRow(ctx, `SELECT CURRENT_TIMESTAMP`).Scan(&databaseNow); err != nil {
		return identity.AdminGlobalSecurityStatusResult{}, err
	}
	nextVersion := currentVersion
	if currentStatus != record.Status {
		nextVersion++
		if _, err := tx.Exec(ctx, `UPDATE identity.users SET account_status=$2,user_version=$3,security_changed_at=$4,updated_at=$4 WHERE user_id=$1`, record.UserID, record.Status, nextVersion, databaseNow); err != nil {
			return identity.AdminGlobalSecurityStatusResult{}, err
		}
	}
	if record.Status != "active" {
		if err := revokeAllIdentitySessionsForUser(ctx, tx, record.UserID, databaseNow, record.ReasonCode); err != nil {
			return identity.AdminGlobalSecurityStatusResult{}, err
		}
	}
	result := identity.AdminGlobalSecurityStatusResult{UserID: record.UserID, Status: record.Status, Version: nextVersion, AuditID: record.OutboxEvent.Payload.AuditID}
	record.OutboxEvent.Now = databaseNow.UTC()
	record.OutboxEvent.Payload.OccurredAt = databaseNow.UTC()
	record.OutboxEvent.Payload.RedactedSummary = map[string]any{"user_id": record.UserID, "status": record.Status, "user_version": nextVersion}
	if err := insertOutboxStrict(ctx, tx, record.OutboxEvent); err != nil {
		return identity.AdminGlobalSecurityStatusResult{}, err
	}
	if err := completeAdminIdempotency(ctx, tx, operation, scopeID, record.UserID, record.ActorDigest, record.KeyDigest, record.RequestDigest, result, databaseNow); err != nil {
		return identity.AdminGlobalSecurityStatusResult{}, err
	}
	return result, tx.Commit(ctx)
}

func (r *Repository) RevokeAdminUserSessions(ctx context.Context, record identity.AdminSessionRevocationRecord) (identity.AdminSessionRevocationResult, error) {
	if r == nil || r.pool == nil {
		return identity.AdminSessionRevocationResult{}, identity.ErrAdminEndUserInvalidArgument
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return identity.AdminSessionRevocationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	operation, scopeID := "admin_session_revoke", adminRepositoryScopeID(record.Scope)
	var replay identity.AdminSessionRevocationResult
	found, err := loadAdminIdempotency(ctx, tx, operation, scopeID, record.UserID, record.ActorDigest, record.KeyDigest, record.RequestDigest, &replay)
	if err != nil || found {
		return replay, err
	}
	member, err := adminMembershipInTx(ctx, tx, record.Scope, record.UserID)
	if err != nil {
		return identity.AdminSessionRevocationResult{}, err
	}
	if !member {
		return identity.AdminSessionRevocationResult{}, finishAdminRevocationFailure(ctx, tx, operation, scopeID, record, "not_found", identity.ErrAdminEndUserNotFound)
	}
	var databaseNow time.Time
	if err := tx.QueryRow(ctx, `SELECT CURRENT_TIMESTAMP`).Scan(&databaseNow); err != nil {
		return identity.AdminSessionRevocationResult{}, err
	}
	targets, err := lockAdminSessionTargets(ctx, tx, record, databaseNow)
	if err != nil {
		return identity.AdminSessionRevocationResult{}, err
	}
	if !record.AllActive && len(targets) != len(record.SessionIDs) {
		return identity.AdminSessionRevocationResult{}, finishAdminRevocationFailure(ctx, tx, operation, scopeID, record, "not_found", identity.ErrAdminEndUserNotFound)
	}
	newlyRevoked := make([]string, 0, len(targets))
	for _, target := range targets {
		if target.active {
			newlyRevoked = append(newlyRevoked, target.sessionID)
		}
	}
	if len(newlyRevoked) != 0 {
		if _, err := tx.Exec(ctx, `UPDATE identity.end_user_sessions SET revoked_at=$2,revoke_reason=$3,last_seen_at=GREATEST(last_seen_at,$2) WHERE session_id=ANY($1) AND revoked_at IS NULL`, newlyRevoked, databaseNow, record.ReasonCode); err != nil {
			return identity.AdminSessionRevocationResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE identity.end_user_session_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE session_id=ANY($1)`, newlyRevoked, databaseNow); err != nil {
			return identity.AdminSessionRevocationResult{}, err
		}
	}
	scopeValue := adminRepositoryScopePointer(record.Scope)
	result := identity.AdminSessionRevocationResult{UserID: record.UserID, ScopeType: record.Scope.Type, ScopeID: scopeValue, RevokedCount: len(newlyRevoked), AuditID: record.OutboxEvent.Payload.AuditID}
	record.OutboxEvent.Now = databaseNow.UTC()
	record.OutboxEvent.Payload.OccurredAt = databaseNow.UTC()
	record.OutboxEvent.Payload.RedactedSummary = map[string]any{"user_id": record.UserID, "revoked_count": len(newlyRevoked)}
	if err := insertOutboxStrict(ctx, tx, record.OutboxEvent); err != nil {
		return identity.AdminSessionRevocationResult{}, err
	}
	if err := completeAdminIdempotency(ctx, tx, operation, scopeID, record.UserID, record.ActorDigest, record.KeyDigest, record.RequestDigest, result, databaseNow); err != nil {
		return identity.AdminSessionRevocationResult{}, err
	}
	return result, tx.Commit(ctx)
}

type adminSessionTarget struct {
	sessionID string
	active    bool
}

func lockAdminSessionTargets(ctx context.Context, tx pgx.Tx, record identity.AdminSessionRevocationRecord, now time.Time) ([]adminSessionTarget, error) {
	arguments := []any{record.UserID, now}
	var statement strings.Builder
	statement.WriteString(`SELECT session_id,(revoked_at IS NULL AND refresh_expires_at>$2) FROM identity.end_user_sessions WHERE user_id=$1`)
	appendAdminSessionScope(&statement, &arguments, record.Scope)
	if record.AllActive {
		statement.WriteString(` AND revoked_at IS NULL AND refresh_expires_at>$2`)
	} else {
		arguments = append(arguments, record.SessionIDs)
		statement.WriteString(fmt.Sprintf(` AND session_id=ANY($%d)`, len(arguments)))
	}
	statement.WriteString(` ORDER BY session_id FOR UPDATE`)
	rows, err := tx.Query(ctx, statement.String(), arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]adminSessionTarget, 0)
	for rows.Next() {
		var target adminSessionTarget
		if err := rows.Scan(&target.sessionID, &target.active); err != nil {
			return nil, err
		}
		result = append(result, target)
	}
	return result, rows.Err()
}

func appendAdminSessionScope(statement *strings.Builder, arguments *[]any, scope identity.AdminUserScope) {
	switch scope.Type {
	case identity.AdminUserScopeProduct:
		*arguments = append(*arguments, scope.ProductID)
		statement.WriteString(fmt.Sprintf(` AND product_id=$%d`, len(*arguments)))
	case identity.AdminUserScopeTenant:
		*arguments = append(*arguments, scope.ProductID, scope.TenantID)
		statement.WriteString(fmt.Sprintf(` AND product_id=$%d AND tenant_id=$%d`, len(*arguments)-1, len(*arguments)))
	}
}

func adminMembershipInTx(ctx context.Context, tx pgx.Tx, scope identity.AdminUserScope, userID string) (bool, error) {
	statement := `SELECT EXISTS(SELECT 1 FROM identity.users WHERE user_id=$1)`
	arguments := []any{userID}
	switch scope.Type {
	case identity.AdminUserScopePlatform:
	case identity.AdminUserScopeProduct:
		statement = `SELECT EXISTS(SELECT 1 FROM identity.end_user_sessions WHERE user_id=$1 AND product_id=$2)`
		arguments = append(arguments, scope.ProductID)
	case identity.AdminUserScopeTenant:
		statement = `SELECT EXISTS(SELECT 1 FROM identity.end_user_sessions WHERE user_id=$1 AND product_id=$2 AND tenant_id=$3)`
		arguments = append(arguments, scope.ProductID, scope.TenantID)
	default:
		return false, identity.ErrAdminEndUserInvalidArgument
	}
	var result bool
	err := tx.QueryRow(ctx, statement, arguments...).Scan(&result)
	return result, err
}

func revokeAllIdentitySessionsForUser(ctx context.Context, tx pgx.Tx, userID string, now time.Time, reason string) error {
	if _, err := tx.Exec(ctx, `UPDATE identity.end_user_sessions SET revoked_at=COALESCE(revoked_at,$2),revoke_reason=COALESCE(revoke_reason,$3),last_seen_at=GREATEST(last_seen_at,$2) WHERE user_id=$1`, userID, now, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.end_user_session_tokens t SET revoked_at=COALESCE(t.revoked_at,$2) FROM identity.end_user_sessions s WHERE t.session_id=s.session_id AND s.user_id=$1`, userID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE identity.admin_sessions SET revoked_at=COALESCE(revoked_at,$2),revoke_reason=COALESCE(revoke_reason,$3),last_seen_at=GREATEST(last_seen_at,$2) WHERE user_id=$1`, userID, now, reason); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE identity.admin_session_tokens t SET revoked_at=COALESCE(t.revoked_at,$2) FROM identity.admin_sessions s WHERE t.session_id=s.session_id AND s.user_id=$1`, userID, now)
	return err
}

type adminIdempotencyFailure struct {
	Error string `json:"error"`
}

func loadAdminIdempotency(ctx context.Context, tx pgx.Tx, operation, scopeID, userID string, actorDigest, keyDigest, requestDigest []byte, response any) (bool, error) {
	if err := lockEndUserIdempotency(ctx, tx, operation, scopeID, actorDigest, keyDigest); err != nil {
		return false, err
	}
	var storedRequest []byte
	var state string
	var document []byte
	err := tx.QueryRow(ctx, `SELECT request_digest,state,response_document FROM identity.end_user_idempotency_records WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4`, operation, scopeID, actorDigest, keyDigest).Scan(&storedRequest, &state, &document)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO identity.end_user_idempotency_records(operation,scope_id,actor_digest,key_digest,request_digest,resource_id,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'pending',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, operation, scopeID, actorDigest, keyDigest, requestDigest, userID)
		return false, err
	}
	if err != nil {
		return false, err
	}
	if !bytes.Equal(storedRequest, requestDigest) {
		return false, identity.ErrAdminEndUserIdempotency
	}
	if state == "completed" {
		if err := json.Unmarshal(document, response); err != nil {
			return false, fmt.Errorf("decode administrator end-user idempotency response: %w", err)
		}
		return true, nil
	}
	if state == "failed" {
		var failure adminIdempotencyFailure
		if json.Unmarshal(document, &failure) != nil {
			return false, identity.ErrAdminEndUserIdempotency
		}
		switch failure.Error {
		case "not_found":
			return false, identity.ErrAdminEndUserNotFound
		case "version_conflict":
			return false, identity.ErrAdminEndUserVersionConflict
		default:
			return false, identity.ErrAdminEndUserIdempotency
		}
	}
	return false, identity.ErrAdminEndUserIdempotency
}

func completeAdminIdempotency(ctx context.Context, tx pgx.Tx, operation, scopeID, userID string, actorDigest, keyDigest, requestDigest []byte, response any, now time.Time) error {
	document, err := json.Marshal(response)
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE identity.end_user_idempotency_records SET resource_id=$6,state='completed',response_document=$7,updated_at=$8 WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4 AND request_digest=$5 AND state='pending'`, operation, scopeID, actorDigest, keyDigest, requestDigest, userID, document, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrAdminEndUserIdempotency
	}
	return nil
}

func finishAdminFailure(ctx context.Context, tx pgx.Tx, operation, scopeID string, record identity.AdminGlobalSecurityStatusRecord, reason string, returned error) error {
	if err := storeAdminFailure(ctx, tx, operation, scopeID, record.UserID, record.ActorDigest, record.KeyDigest, record.RequestDigest, reason); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return returned
}

func finishAdminRevocationFailure(ctx context.Context, tx pgx.Tx, operation, scopeID string, record identity.AdminSessionRevocationRecord, reason string, returned error) error {
	if err := storeAdminFailure(ctx, tx, operation, scopeID, record.UserID, record.ActorDigest, record.KeyDigest, record.RequestDigest, reason); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return returned
}

func storeAdminFailure(ctx context.Context, tx pgx.Tx, operation, scopeID, userID string, actorDigest, keyDigest, requestDigest []byte, reason string) error {
	document, err := json.Marshal(adminIdempotencyFailure{Error: reason})
	if err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE identity.end_user_idempotency_records SET resource_id=$6,state='failed',response_document=$7,updated_at=CURRENT_TIMESTAMP WHERE operation=$1 AND scope_id=$2 AND actor_digest=$3 AND key_digest=$4 AND request_digest=$5 AND state='pending'`, operation, scopeID, actorDigest, keyDigest, requestDigest, userID, document)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return identity.ErrAdminEndUserIdempotency
	}
	return nil
}

func adminRepositoryScopeID(scope identity.AdminUserScope) string {
	switch scope.Type {
	case identity.AdminUserScopePlatform:
		return "platform"
	case identity.AdminUserScopeProduct:
		return "product:" + scope.ProductID
	case identity.AdminUserScopeTenant:
		return "tenant:" + scope.ProductID + ":" + scope.TenantID
	default:
		return "invalid"
	}
}

func adminRepositoryScopePointer(scope identity.AdminUserScope) *string {
	value := ""
	if scope.Type == identity.AdminUserScopeProduct {
		value = scope.ProductID
	} else if scope.Type == identity.AdminUserScopeTenant {
		value = scope.TenantID
	} else {
		return nil
	}
	return &value
}
