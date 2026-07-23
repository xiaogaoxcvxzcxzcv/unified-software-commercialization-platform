package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/identity"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

type adminEndUserPostgresIDs struct{ next int }

func (g *adminEndUserPostgresIDs) ID(prefix string) (string, error) {
	g.next++
	return prefix + "admin_pg_" + strings.Repeat("0", 20) + string(rune('a'+g.next)), nil
}

func newAdminEndUserPostgresService(t *testing.T, database testpostgres.Database) *identity.AdminEndUserService {
	t.Helper()
	hasher, err := securevalue.NewHasher(strings.Repeat("admin-end-user-pg-pepper-", 2))
	if err != nil {
		t.Fatal(err)
	}
	service, err := identity.NewAdminEndUserService(New(database.Pool), identity.StrictIdentifierNormalizer{}, hasher, &adminEndUserPostgresIDs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func seedAdminEndUser(t *testing.T, database testpostgres.Database, userID, displayName, accountStatus string, now time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := database.Pool.Exec(ctx, `INSERT INTO identity.users(user_id,display_name,account_status,user_version,created_at,updated_at) VALUES($1,$2,$3,1,$4,$4)`, userID, displayName, accountStatus, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `INSERT INTO identity.user_profiles(user_id,profile_version,display_name,locale,timezone,created_at,updated_at) VALUES($1,1,$2,'zh-CN','Asia/Shanghai',$3,$3)`, userID, displayName, now); err != nil {
		t.Fatal(err)
	}
}

func seedAdminEndUserSession(t *testing.T, database testpostgres.Database, sessionID, userID, productID, applicationID string, tenantID *string, created, lastSeen, refresh, absolute time.Time) {
	t.Helper()
	_, err := database.Pool.Exec(context.Background(), `INSERT INTO identity.end_user_sessions(session_id,user_id,product_id,application_id,tenant_id,environment,token_family_id,authentication_method,session_version,auth_time,created_at,last_seen_at,access_expires_at,refresh_expires_at,absolute_expires_at) VALUES($1,$2,$3,$4,$5,'test',$6,'password',1,$7,$8,$9,$10,$11,$12)`, sessionID, userID, productID, applicationID, tenantID, "family-"+sessionID, created, created, lastSeen, refresh.Add(-time.Hour), refresh, absolute)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAdminEndUserPostgresListsHistoricalScopedMembersAndSessions(t *testing.T) {
	database := testpostgres.Open(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedAdminEndUser(t, database, "admin-query-user", "Query User", "active", now.Add(-72*time.Hour))
	seedAdminEndUser(t, database, "other-query-user", "Other User", "active", now.Add(-24*time.Hour))
	seedAdminEndUserSession(t, database, "query-session-old", "admin-query-user", "product-query", "app-query", nil, now.Add(-72*time.Hour), now.Add(-48*time.Hour), now.Add(time.Hour), now.Add(24*time.Hour))
	seedAdminEndUserSession(t, database, "query-session-revoked", "admin-query-user", "product-query", "app-query", nil, now.Add(-48*time.Hour), now.Add(-47*time.Hour), now.Add(24*time.Hour), now.Add(48*time.Hour))
	if _, err := database.Pool.Exec(context.Background(), `UPDATE identity.end_user_sessions SET revoked_at=$1,revoke_reason='test' WHERE session_id='query-session-revoked'`, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	seedAdminEndUserSession(t, database, "query-session-other", "admin-query-user", "other-product", "app-other", nil, now.Add(-24*time.Hour), now.Add(-23*time.Hour), now.Add(time.Hour), now.Add(48*time.Hour))
	hasher, err := securevalue.NewHasher(strings.Repeat("admin-end-user-pg-pepper-", 2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(context.Background(), `INSERT INTO identity.user_identifiers(identifier_id,user_id,identifier_type,normalization_version,normalized_digest,masked_value,verification_status,verified_at,created_at,updated_at) VALUES('query-email','admin-query-user','email',1,$1,'q***@example.com','verified',$2,$2,$2)`, hasher.Digest("identifier\x00query@example.com"), now); err != nil {
		t.Fatal(err)
	}
	service := newAdminEndUserPostgresService(t, database)
	page, err := service.ListUsers(context.Background(), identity.AdminUserQuery{Scope: identity.AdminUserScope{Type: identity.AdminUserScopeProduct, ProductID: "product-query"}, Query: "query@example.com", Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	item := page.Items[0]
	if item.UserID != "admin-query-user" || item.TotalSessionCount != 2 || item.ActiveSessionCount != 1 || item.MemberSince == nil || len(item.Identifiers) != 1 || item.Identifiers[0].MaskedValue != "q***@example.com" {
		t.Fatalf("item=%#v", item)
	}
	if _, err := service.GetUser(context.Background(), identity.AdminUserScope{Type: identity.AdminUserScopeTenant, ProductID: "product-query", TenantID: "tenant-missing"}, "admin-query-user"); !errors.Is(err, identity.ErrAdminEndUserNotFound) {
		t.Fatalf("tenant detail error=%v", err)
	}
	sessions, err := service.ListSessions(context.Background(), identity.AdminUserSessionQuery{Scope: identity.AdminUserScope{Type: identity.AdminUserScopeProduct, ProductID: "product-query"}, UserID: "admin-query-user", Limit: 10})
	if err != nil || len(sessions.Items) != 2 {
		t.Fatalf("sessions=%#v err=%v", sessions, err)
	}
}

func TestAdminEndUserPostgresGlobalSecurityStatusIsAtomicAndIdempotent(t *testing.T) {
	database := testpostgres.Open(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedAdminEndUser(t, database, "global-security-user", "Global Security User", "active", now)
	seedAdminEndUserSession(t, database, "global-end-session", "global-security-user", "global-product", "global-app", nil, now, now, now.Add(time.Hour), now.Add(24*time.Hour))
	if _, err := database.Pool.Exec(context.Background(), `INSERT INTO identity.admin_sessions(session_id,user_id,token_family_id,transport,authentication_method,session_version,auth_time,created_at,last_seen_at,access_expires_at,refresh_expires_at,absolute_expires_at,csrf_digest) VALUES('global-admin-session','global-security-user','global-admin-family','cookie','password',1,$1,$1,$1,$2,$3,$4,decode(repeat('a',64),'hex'))`, now, now.Add(time.Hour), now.Add(24*time.Hour), now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	service := newAdminEndUserPostgresService(t, database)
	command := identity.AdminGlobalSecurityStatusCommand{UserID: "global-security-user", Status: "disabled", ExpectedVersion: 1, ReasonCode: "security.review", OperatorNote: "never persist this note", IdempotencyKey: "global-security-pg-key-01", ActorID: "admin-security", TraceID: "trace-global-security"}
	first, err := service.SetGlobalSecurityStatus(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.SetGlobalSecurityStatus(context.Background(), command)
	if err != nil || first.AuditID != second.AuditID || first.Version != second.Version {
		t.Fatalf("first=%#v second=%#v err=%v", first, second, err)
	}
	var status string
	var version int64
	if err := database.Pool.QueryRow(context.Background(), `SELECT account_status,user_version FROM identity.users WHERE user_id='global-security-user'`).Scan(&status, &version); err != nil || status != "disabled" || version != 2 {
		t.Fatalf("status=%q version=%d err=%v", status, version, err)
	}
	var endRevoked, adminRevoked *time.Time
	if err := database.Pool.QueryRow(context.Background(), `SELECT revoked_at FROM identity.end_user_sessions WHERE session_id='global-end-session'`).Scan(&endRevoked); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(context.Background(), `SELECT revoked_at FROM identity.admin_sessions WHERE session_id='global-admin-session'`).Scan(&adminRevoked); err != nil {
		t.Fatal(err)
	}
	if endRevoked == nil || adminRevoked == nil {
		t.Fatalf("end revoked=%v admin revoked=%v", endRevoked, adminRevoked)
	}
	var payload []byte
	if err := database.Pool.QueryRow(context.Background(), `SELECT payload FROM identity.outbox_events WHERE topic='identity.global_user_security_status_changed.v1'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "never persist this note") {
		t.Fatal("operator note leaked into outbox")
	}
	var responseDocument []byte
	if err := database.Pool.QueryRow(context.Background(), `SELECT response_document FROM identity.end_user_idempotency_records WHERE operation='admin_global_security_status'`).Scan(&responseDocument); err != nil {
		t.Fatal(err)
	}
	var persisted identity.AdminGlobalSecurityStatusResult
	if err := json.Unmarshal(responseDocument, &persisted); err != nil || persisted.AuditID != first.AuditID {
		t.Fatalf("persisted=%#v err=%v", persisted, err)
	}
}

func TestAdminEndUserPostgresScopedRevocationDoesNotCrossProduct(t *testing.T) {
	database := testpostgres.Open(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	seedAdminEndUser(t, database, "scoped-revoke-user", "Scoped Revoke User", "active", now)
	seedAdminEndUserSession(t, database, "scoped-session-a", "scoped-revoke-user", "scope-product-a", "app-a", nil, now, now, now.Add(time.Hour), now.Add(24*time.Hour))
	seedAdminEndUserSession(t, database, "scoped-session-b", "scoped-revoke-user", "scope-product-b", "app-b", nil, now, now, now.Add(time.Hour), now.Add(24*time.Hour))
	service := newAdminEndUserPostgresService(t, database)
	command := identity.AdminSessionRevocationCommand{Scope: identity.AdminUserScope{Type: identity.AdminUserScopeProduct, ProductID: "scope-product-a"}, UserID: "scoped-revoke-user", AllActive: true, ReasonCode: "security.review", IdempotencyKey: "scoped-revoke-pg-key-01", ActorID: "admin-security", TraceID: "trace-scoped-revoke"}
	first, err := service.RevokeSessions(context.Background(), command)
	if err != nil || first.RevokedCount != 1 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := service.RevokeSessions(context.Background(), command)
	if err != nil || second.AuditID != first.AuditID || second.RevokedCount != first.RevokedCount {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	var revokedA, revokedB *time.Time
	if err := database.Pool.QueryRow(context.Background(), `SELECT revoked_at FROM identity.end_user_sessions WHERE session_id='scoped-session-a'`).Scan(&revokedA); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(context.Background(), `SELECT revoked_at FROM identity.end_user_sessions WHERE session_id='scoped-session-b'`).Scan(&revokedB); err != nil {
		t.Fatal(err)
	}
	if revokedA == nil || revokedB != nil {
		t.Fatalf("revokedA=%v revokedB=%v", revokedA, revokedB)
	}
	command.SessionIDs = []string{"scoped-session-b"}
	command.AllActive = false
	command.IdempotencyKey = "scoped-revoke-pg-key-02"
	if _, err := service.RevokeSessions(context.Background(), command); !errors.Is(err, identity.ErrAdminEndUserNotFound) {
		t.Fatalf("cross-product explicit session error=%v", err)
	}
}
