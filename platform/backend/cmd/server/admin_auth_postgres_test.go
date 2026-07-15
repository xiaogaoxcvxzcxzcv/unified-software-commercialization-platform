package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	accesspostgres "platform.local/capability-platform/backend/internal/modules/accesscontrol/postgres"
	"platform.local/capability-platform/backend/internal/modules/audit"
	audithttp "platform.local/capability-platform/backend/internal/modules/audit/httptransport"
	auditpostgres "platform.local/capability-platform/backend/internal/modules/audit/postgres"
	"platform.local/capability-platform/backend/internal/modules/identity"
	identityhttp "platform.local/capability-platform/backend/internal/modules/identity/httptransport"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	platformserver "platform.local/capability-platform/backend/internal/platform/server"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

const adminE2EOrigin = "https://admin.integration.test"

type failOnceAuditAdapter struct {
	delegate  identity.AuditPort
	remaining atomic.Int32
}

func (a *failOnceAuditAdapter) AppendSecurityEvent(ctx context.Context, event identity.SecurityEvent) (string, error) {
	if a.remaining.CompareAndSwap(1, 0) {
		return "", io.ErrUnexpectedEOF
	}
	return a.delegate.AppendSecurityEvent(ctx, event)
}

func TestAdminCookieGoldenFlowWithPostgreSQLAndAuditOutbox(t *testing.T) {
	database := testpostgres.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	repository := identitypostgres.New(database.Pool)
	accessService := accesscontrol.NewService(accesspostgres.New(database.Pool), nil)
	hasher, err := securevalue.NewHasher("integration-test-token-pepper-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(repository, accessService, identity.Bcrypt{Cost: 10}, hasher, identity.Policy{
		AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour,
		LoginWindow: 10 * time.Minute, LoginMaximumAttempts: 5, LoginBlockDuration: 10 * time.Minute,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := identityService.BootstrapAdminIdentity(ctx, "admin@example.test", "Test Administrator", []byte("correct-password-123"))
	if err != nil {
		t.Fatal(err)
	}
	if err := accessService.BootstrapPlatformAdmin(ctx, accesscontrol.BootstrapCommand{BindingID: "binding-admin", RoleID: "role-admin", AdminUserID: adminID, Now: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := identityService.BootstrapAdminIdentity(ctx, "no-scope@example.test", "No Scope", []byte("correct-password-456")); err != nil {
		t.Fatal(err)
	}
	disabledAdminID, err := identityService.BootstrapAdminIdentity(ctx, "disabled@example.test", "Disabled Administrator", []byte("correct-password-654"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE identity.users SET account_status='disabled',updated_at=$2 WHERE user_id=$1`, disabledAdminID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	productAdminID, err := identityService.BootstrapAdminIdentity(ctx, "product-admin@example.test", "Product A Administrator", []byte("correct-password-789"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accessService.BindAdminScope(ctx, accesscontrol.BindAdminScopeCommand{
		AdminUserID: productAdminID, RoleCode: "super_admin", Scope: accesscontrol.Scope{Type: "product", ProductID: "prod-a"},
		ActorID: adminID, TraceID: "trace-product-admin-binding", IdempotencyKey: "idem-product-admin-binding-001",
	}); err != nil {
		t.Fatal(err)
	}

	auditRepository := auditpostgres.New(database.Pool)
	auditService := audit.NewService(auditRepository)
	dispatcher := identity.NewOutboxDispatcher(repository, identityAuditAdapter{service: auditService}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	go dispatcher.Run(ctx)
	modules := platformserver.NewModuleRegistrar()
	if err := modules.Register("/api/v1/admin/auth/", identityhttp.New(identityService, identityhttp.Config{AllowedOrigins: []string{adminE2EOrigin}})); err != nil {
		t.Fatal(err)
	}
	auditQueryService := audit.NewQueryService(auditRepository, auditAuthorizerAdapter{access: accessService})
	if err := modules.Register("/api/v1/admin/audit/", audithttp.New(auditQueryService, auditAdminContextAdapter{identity: identityService})); err != nil {
		t.Fatal(err)
	}
	handler := platformserver.NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, platformserver.BuildInfo{Version: "integration"}, modules)
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar

	wrong := postJSON(t, client, server.URL+"/api/v1/admin/auth/login", adminE2EOrigin, map[string]any{"identifier": "admin@example.test", "credential": "wrong-password", "transport": "cookie"}, nil, nil)
	noScope := postJSON(t, client, server.URL+"/api/v1/admin/auth/login", adminE2EOrigin, map[string]any{"identifier": "no-scope@example.test", "credential": "correct-password-456", "transport": "cookie"}, nil, nil)
	unknown := postJSON(t, client, server.URL+"/api/v1/admin/auth/login", adminE2EOrigin, map[string]any{"identifier": "unknown@example.test", "credential": "wrong-password", "transport": "cookie"}, nil, nil)
	disabled := postJSON(t, client, server.URL+"/api/v1/admin/auth/login", adminE2EOrigin, map[string]any{"identifier": "disabled@example.test", "credential": "correct-password-654", "transport": "cookie"}, nil, nil)
	assertIndistinguishableAuthFailures(t, wrong, noScope, unknown, disabled)
	productLogin := postJSON(t, client, server.URL+"/api/v1/admin/auth/login", adminE2EOrigin, map[string]any{"identifier": "product-admin@example.test", "credential": "correct-password-789", "transport": "cookie"}, nil, nil)
	if productLogin.StatusCode != http.StatusOK {
		t.Fatalf("product-scoped login status = %d, body = %s", productLogin.StatusCode, readBody(t, productLogin))
	}
	var productSession identity.AdminSession
	if err := json.Unmarshal([]byte(readBody(t, productLogin)), &productSession); err != nil {
		t.Fatal(err)
	}
	assertCatalogAuthorization(t, productSession.Authorization, "product", "prod-a")

	login := postJSON(t, client, server.URL+"/api/v1/admin/auth/login", adminE2EOrigin, map[string]any{"identifier": "admin@example.test", "credential": "correct-password-123", "transport": "cookie"}, nil, nil)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", login.StatusCode, readBody(t, login))
	}
	loginBody := readBody(t, login)
	if strings.Contains(loginBody, "adm_at_") || strings.Contains(loginBody, "adm_rt_") {
		t.Fatal("cookie login response leaked opaque token material")
	}
	var session identity.AdminSession
	if err := json.Unmarshal([]byte(loginBody), &session); err != nil {
		t.Fatal(err)
	}
	if session.SessionVersion != 1 {
		t.Fatalf("login session_version = %d, want 1", session.SessionVersion)
	}
	assertCatalogAuthorization(t, session.Authorization, "platform", "")
	assertSessionCookies(t, login)

	current := get(t, client, server.URL+"/api/v1/admin/auth/session")
	if current.StatusCode != http.StatusOK {
		t.Fatalf("current session status = %d, body = %s", current.StatusCode, readBody(t, current))
	}
	_ = readBody(t, current)
	refreshURL := server.URL + "/api/v1/admin/auth/refresh"
	oldRefresh := namedCookie(t, client.Jar.Cookies(mustURL(t, refreshURL)), "__Secure-platform_admin_refresh")

	refresh := postJSON(t, client, refreshURL, adminE2EOrigin, map[string]any{"transport": "cookie"}, nil, nil)
	if refresh.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %s", refresh.StatusCode, readBody(t, refresh))
	}
	var refreshedCookieSession identity.AdminSession
	if err := json.Unmarshal([]byte(readBody(t, refresh)), &refreshedCookieSession); err != nil {
		t.Fatal(err)
	}
	if refreshedCookieSession.SessionVersion != session.SessionVersion+1 {
		t.Fatalf("refreshed session_version = %d, want %d", refreshedCookieSession.SessionVersion, session.SessionVersion+1)
	}
	assertSessionCookies(t, refresh)
	currentAfterRefresh := get(t, client, server.URL+"/api/v1/admin/auth/session")
	if currentAfterRefresh.StatusCode != http.StatusOK {
		t.Fatalf("current session after refresh status = %d, body = %s", currentAfterRefresh.StatusCode, readBody(t, currentAfterRefresh))
	}
	var recoveredCookieSession identity.AdminSession
	if err := json.Unmarshal([]byte(readBody(t, currentAfterRefresh)), &recoveredCookieSession); err != nil {
		t.Fatal(err)
	}
	if refreshedCookieSession.CSRFToken == nil || recoveredCookieSession.CSRFToken == nil || *refreshedCookieSession.CSRFToken != *recoveredCookieSession.CSRFToken {
		t.Fatal("refresh and same-version session recovery returned different CSRF tokens")
	}
	if recoveredCookieSession.SessionVersion != refreshedCookieSession.SessionVersion {
		t.Fatalf("recovered session_version = %d, want %d", recoveredCookieSession.SessionVersion, refreshedCookieSession.SessionVersion)
	}

	replayClient := *server.Client()
	replay := postJSON(t, &replayClient, refreshURL, adminE2EOrigin, map[string]any{"transport": "cookie"}, []*http.Cookie{oldRefresh}, nil)
	if replay.StatusCode != http.StatusUnauthorized || problemCode(t, replay) != "admin_auth.refresh_replayed" {
		t.Fatal("old refresh replay was not rejected with the stable replay code")
	}
	currentAfterReplay := get(t, client, server.URL+"/api/v1/admin/auth/session")
	if currentAfterReplay.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session after replay status = %d, want 401", currentAfterReplay.StatusCode)
	}
	_ = readBody(t, currentAfterReplay)

	const secondLoginTraceID = "trace-cookie-second-login"
	secondLogin := postJSON(t, client, server.URL+"/api/v1/admin/auth/login", adminE2EOrigin, map[string]any{"identifier": "admin@example.test", "credential": "correct-password-123", "transport": "cookie"}, nil, map[string]string{"X-Request-ID": secondLoginTraceID})
	if secondLogin.StatusCode != http.StatusOK {
		t.Fatalf("second login status = %d, body = %s", secondLogin.StatusCode, readBody(t, secondLogin))
	}
	var secondSession identity.AdminSession
	if err := json.Unmarshal([]byte(readBody(t, secondLogin)), &secondSession); err != nil {
		t.Fatal(err)
	}
	if secondSession.CSRFToken == nil || *secondSession.CSRFToken == "" {
		t.Fatal("cookie session did not return an in-memory CSRF token")
	}
	auditQueryURL := server.URL + "/api/v1/admin/audit/events?trace_id=" + url.QueryEscape(secondLoginTraceID)
	auditDeadline := time.Now().Add(6 * time.Second)
	for {
		response := get(t, client, auditQueryURL)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("audit query status = %d, body = %s", response.StatusCode, readBody(t, response))
		}
		var page audit.Page
		if err := json.Unmarshal([]byte(readBody(t, response)), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 1 && page.Items[0].TraceID == secondLoginTraceID && page.Items[0].Action == "admin.auth.login_succeeded" {
			break
		}
		if time.Now().After(auditDeadline) {
			t.Fatalf("audit event was not queryable by trace_id: %#v", page.Items)
		}
		time.Sleep(100 * time.Millisecond)
	}
	secondAccess := namedCookie(t, client.Jar.Cookies(mustURL(t, server.URL+"/api/v1/admin/auth/session")), "__Host-platform_admin_access")
	secondRefresh := namedCookie(t, client.Jar.Cookies(mustURL(t, refreshURL)), "__Secure-platform_admin_refresh")
	crossTransportClient := *server.Client()
	crossTransportClient.Jar = nil
	cookieAsBearerLogout := postJSON(t, &crossTransportClient, server.URL+"/api/v1/admin/auth/logout", "", nil, nil, map[string]string{
		"Authorization": "Bearer " + secondAccess.Value,
	})
	if cookieAsBearerLogout.StatusCode != http.StatusUnauthorized || problemCode(t, cookieAsBearerLogout) != "admin_auth.session_revoked" {
		t.Fatal("cookie access submitted as bearer logout was not rejected")
	}
	cookieAfterCrossTransportLogout := getWithCookies(t, server.Client(), server.URL+"/api/v1/admin/auth/session", []*http.Cookie{secondAccess})
	if cookieAfterCrossTransportLogout.StatusCode != http.StatusOK {
		t.Fatalf("cross-transport logout revoked cookie session: status=%d body=%s", cookieAfterCrossTransportLogout.StatusCode, readBody(t, cookieAfterCrossTransportLogout))
	}
	_ = readBody(t, cookieAfterCrossTransportLogout)
	logout := postJSON(t, client, server.URL+"/api/v1/admin/auth/logout", adminE2EOrigin, nil, nil, map[string]string{"X-CSRF-Token": *secondSession.CSRFToken})
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, body = %s", logout.StatusCode, readBody(t, logout))
	}
	_ = logout.Body.Close()
	afterLogout := get(t, client, server.URL+"/api/v1/admin/auth/session")
	if afterLogout.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session after logout status = %d, want 401", afterLogout.StatusCode)
	}
	_ = readBody(t, afterLogout)
	oldAccessAfterLogout := getWithCookies(t, server.Client(), server.URL+"/api/v1/admin/auth/session", []*http.Cookie{secondAccess})
	if oldAccessAfterLogout.StatusCode != http.StatusUnauthorized || problemCode(t, oldAccessAfterLogout) != "admin_auth.session_revoked" {
		t.Fatal("saved access cookie remained usable after logout")
	}
	oldRefreshAfterLogout := postJSON(t, server.Client(), refreshURL, adminE2EOrigin, map[string]any{"transport": "cookie"}, []*http.Cookie{secondRefresh}, nil)
	if oldRefreshAfterLogout.StatusCode != http.StatusUnauthorized || problemCode(t, oldRefreshAfterLogout) != "admin_auth.session_revoked" {
		t.Fatal("saved refresh cookie remained usable after logout")
	}

	concurrentLogin := postJSON(t, client, server.URL+"/api/v1/admin/auth/login", adminE2EOrigin, map[string]any{"identifier": "admin@example.test", "credential": "correct-password-123", "transport": "cookie"}, nil, nil)
	if concurrentLogin.StatusCode != http.StatusOK {
		t.Fatalf("concurrent-flow login status = %d, body = %s", concurrentLogin.StatusCode, readBody(t, concurrentLogin))
	}
	concurrentRefresh := namedCookie(t, concurrentLogin.Cookies(), "__Secure-platform_admin_refresh")
	_ = readBody(t, concurrentLogin)
	requests := make([]*http.Request, 2)
	for index := range requests {
		payload, err := json.Marshal(map[string]any{"transport": "cookie"})
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, refreshURL, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", adminE2EOrigin)
		request.AddCookie(concurrentRefresh)
		requests[index] = request
	}
	type refreshResult struct {
		response *http.Response
		err      error
	}
	start := make(chan struct{})
	results := make(chan refreshResult, len(requests))
	concurrentClient := server.Client()
	for _, request := range requests {
		go func(request *http.Request) {
			<-start
			response, err := concurrentClient.Do(request)
			results <- refreshResult{response: response, err: err}
		}(request)
	}
	close(start)
	succeeded, replayed := 0, 0
	var winningAccess *http.Cookie
	for range requests {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent refresh request failed: %v", result.err)
		}
		switch result.response.StatusCode {
		case http.StatusOK:
			succeeded++
			winningAccess = namedCookie(t, result.response.Cookies(), "__Host-platform_admin_access")
			_ = readBody(t, result.response)
		case http.StatusUnauthorized:
			if problemCode(t, result.response) != "admin_auth.refresh_replayed" {
				t.Fatal("concurrent refresh loser did not return the stable replay code")
			}
			replayed++
		default:
			t.Fatalf("concurrent refresh status = %d", result.response.StatusCode)
		}
	}
	if succeeded != 1 || replayed != 1 || winningAccess == nil {
		t.Fatalf("concurrent refresh results: succeeded=%d replayed=%d", succeeded, replayed)
	}
	winnerAfterReplay := getWithCookies(t, server.Client(), server.URL+"/api/v1/admin/auth/session", []*http.Cookie{winningAccess})
	if winnerAfterReplay.StatusCode != http.StatusUnauthorized || problemCode(t, winnerAfterReplay) != "admin_auth.session_revoked" {
		t.Fatal("concurrent refresh replay did not revoke the token family issued to the winner")
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		var actions, traces int
		err := database.Pool.QueryRow(ctx, `SELECT count(DISTINCT action), count(DISTINCT trace_id) FROM audit.events WHERE action IN ('admin.auth.login_failed','admin.auth.login_succeeded','admin.auth.refresh_replayed','admin.auth.session_revoked')`).Scan(&actions, &traces)
		if err == nil && actions == 4 && traces >= 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit outbox was not fully dispatched: actions=%d traces=%d error=%v", actions, traces, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestAdminControlledBearerGoldenFlowWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	repository := identitypostgres.New(database.Pool)
	accessService := accesscontrol.NewService(accesspostgres.New(database.Pool), nil)
	hasher, err := securevalue.NewHasher("integration-test-token-pepper-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	auditService := audit.NewService(auditpostgres.New(database.Pool))
	dispatcher := identity.NewOutboxDispatcher(repository, identityAuditAdapter{service: auditService}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	go dispatcher.Run(ctx)
	identityService, err := identity.NewService(repository, accessService, identity.Bcrypt{Cost: 10}, hasher, identity.Policy{
		AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour,
		LoginWindow: 10 * time.Minute, LoginMaximumAttempts: 5, LoginBlockDuration: 10 * time.Minute,
		AllowBearer: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := identityService.BootstrapAdminIdentity(ctx, "bearer-admin@example.test", "Bearer Test Administrator", []byte("correct-bearer-password-123"))
	if err != nil {
		t.Fatal(err)
	}
	if err := accessService.BootstrapPlatformAdmin(ctx, accesscontrol.BootstrapCommand{BindingID: "binding-bearer-admin", RoleID: "role-bearer-admin", AdminUserID: adminID, Now: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	primary, err := identityService.RegisterControlledAdminClient(ctx, identity.RegisterControlledClientCommand{DisplayName: "Golden CLI", ClientType: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := identityService.RotateControlledAdminClientCredential(ctx, identity.RotateControlledClientCredentialCommand{ClientID: primary.ClientID})
	if err != nil {
		t.Fatal(err)
	}

	modules := platformserver.NewModuleRegistrar()
	if err := modules.Register("/api/v1/admin/auth/", identityhttp.New(identityService, identityhttp.Config{AllowedOrigins: []string{adminE2EOrigin}})); err != nil {
		t.Fatal(err)
	}
	handler := platformserver.NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, platformserver.BuildInfo{Version: "integration"}, modules)
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client := server.Client()
	loginURL := server.URL + "/api/v1/admin/auth/login"
	refreshURL := server.URL + "/api/v1/admin/auth/refresh"

	invalidProofs := []struct {
		name  string
		proof map[string]any
	}{
		{name: "missing credential", proof: controlledClientPayload(primary.ClientID, "acred_missing", primary.ProofType, strings.Repeat("m", 43))},
		{name: "unknown client", proof: controlledClientPayload("acli_missing", primary.CredentialID, primary.ProofType, strings.Repeat("u", 43))},
		{name: "wrong proof", proof: controlledClientPayload(primary.ClientID, primary.CredentialID, primary.ProofType, strings.Repeat("w", 43))},
	}
	for _, test := range invalidProofs {
		t.Run(test.name, func(t *testing.T) {
			response := postJSON(t, client, loginURL, "", map[string]any{
				"identifier":        "bearer-admin@example.test",
				"credential":        "correct-bearer-password-123",
				"transport":         "bearer",
				"controlled_client": test.proof,
			}, nil, nil)
			if response.StatusCode != http.StatusUnauthorized || problemCode(t, response) != "admin_auth.invalid_credentials" {
				t.Fatal("missing, unknown, and invalid controlled-client proofs must share the generic 401 response")
			}
		})
	}

	const bearerLoginTraceID = "trace-bearer-login"
	login := postJSON(t, client, loginURL, "", map[string]any{
		"identifier":        "bearer-admin@example.test",
		"credential":        "correct-bearer-password-123",
		"transport":         "bearer",
		"controlled_client": controlledClientPayload(primary.ClientID, primary.CredentialID, primary.ProofType, primary.Secret),
	}, nil, map[string]string{"X-Request-ID": bearerLoginTraceID})
	if login.StatusCode != http.StatusOK {
		t.Fatalf("bearer login status = %d, body = %s", login.StatusCode, readBody(t, login))
	}
	var session identity.AdminSession
	if err := json.Unmarshal([]byte(readBody(t, login)), &session); err != nil {
		t.Fatal(err)
	}
	if session.Transport != identity.TransportBearer || session.ControlledClientID == nil || *session.ControlledClientID != primary.ClientID {
		t.Fatalf("unexpected controlled bearer session: %#v", session)
	}
	assertCatalogAuthorization(t, session.Authorization, "platform", "")
	if session.TokenPair == nil || session.TokenPair.AccessToken == "" || session.TokenPair.RefreshToken == "" {
		t.Fatal("bearer login did not return the token pair")
	}
	if len(login.Cookies()) != 0 {
		t.Fatal("bearer login must not set browser session cookies")
	}

	current := getWithBearer(t, client, server.URL+"/api/v1/admin/auth/session", session.TokenPair.AccessToken)
	if current.StatusCode != http.StatusOK {
		t.Fatalf("bearer session status = %d, body = %s", current.StatusCode, readBody(t, current))
	}
	_ = readBody(t, current)

	originalRefresh := session.TokenPair.RefreshToken
	crossCredential := postJSON(t, client, refreshURL, "", map[string]any{
		"transport":         "bearer",
		"refresh_token":     originalRefresh,
		"controlled_client": controlledClientPayload(secondary.ClientID, secondary.CredentialID, secondary.ProofType, secondary.Secret),
	}, nil, nil)
	if crossCredential.StatusCode != http.StatusUnauthorized || problemCode(t, crossCredential) != "admin_auth.session_revoked" {
		t.Fatal("refresh bound to a different controlled credential must be rejected")
	}

	const bearerRefreshTraceID = "trace-bearer-refresh"
	refresh := postJSON(t, client, refreshURL, "", map[string]any{
		"transport":         "bearer",
		"refresh_token":     originalRefresh,
		"controlled_client": controlledClientPayload(primary.ClientID, primary.CredentialID, primary.ProofType, primary.Secret),
	}, nil, map[string]string{"X-Request-ID": bearerRefreshTraceID})
	if refresh.StatusCode != http.StatusOK {
		t.Fatalf("exact-credential refresh status = %d, body = %s", refresh.StatusCode, readBody(t, refresh))
	}
	var refreshed identity.AdminSession
	if err := json.Unmarshal([]byte(readBody(t, refresh)), &refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.TokenPair == nil || refreshed.TokenPair.RefreshToken == "" || refreshed.TokenPair.RefreshToken == originalRefresh {
		t.Fatal("exact-credential refresh did not rotate the refresh token")
	}
	assertCatalogAuthorization(t, refreshed.Authorization, "platform", "")

	const bearerLogoutLoginTraceID = "trace-bearer-logout-login"
	logoutLogin := postJSON(t, client, loginURL, "", map[string]any{
		"identifier":        "bearer-admin@example.test",
		"credential":        "correct-bearer-password-123",
		"transport":         "bearer",
		"controlled_client": controlledClientPayload(primary.ClientID, primary.CredentialID, primary.ProofType, primary.Secret),
	}, nil, map[string]string{"X-Request-ID": bearerLogoutLoginTraceID})
	if logoutLogin.StatusCode != http.StatusOK {
		t.Fatalf("bearer logout-flow login status = %d, body = %s", logoutLogin.StatusCode, readBody(t, logoutLogin))
	}
	var logoutSession identity.AdminSession
	if err := json.Unmarshal([]byte(readBody(t, logoutLogin)), &logoutSession); err != nil {
		t.Fatal(err)
	}
	if logoutSession.TokenPair == nil {
		t.Fatal("bearer logout-flow login did not return a token pair")
	}
	refreshAsBearerLogout := postJSON(t, client, server.URL+"/api/v1/admin/auth/logout", "", nil, nil, map[string]string{
		"Authorization": "Bearer " + logoutSession.TokenPair.RefreshToken,
	})
	if refreshAsBearerLogout.StatusCode != http.StatusUnauthorized || problemCode(t, refreshAsBearerLogout) != "admin_auth.session_revoked" {
		t.Fatal("bearer refresh submitted as bearer access logout was not rejected")
	}
	accessAfterInvalidLogout := getWithBearer(t, client, server.URL+"/api/v1/admin/auth/session", logoutSession.TokenPair.AccessToken)
	if accessAfterInvalidLogout.StatusCode != http.StatusOK {
		t.Fatalf("invalid bearer logout revoked valid access: status=%d body=%s", accessAfterInvalidLogout.StatusCode, readBody(t, accessAfterInvalidLogout))
	}
	_ = readBody(t, accessAfterInvalidLogout)
	const bearerLogoutTraceID = "trace-bearer-logout"
	bearerLogout := postJSON(t, client, server.URL+"/api/v1/admin/auth/logout", "", nil, nil, map[string]string{
		"Authorization": "Bearer " + logoutSession.TokenPair.AccessToken,
		"X-Request-ID":  bearerLogoutTraceID,
	})
	if bearerLogout.StatusCode != http.StatusNoContent {
		t.Fatalf("bearer logout status = %d, body = %s", bearerLogout.StatusCode, readBody(t, bearerLogout))
	}
	_ = bearerLogout.Body.Close()
	loggedOutAccess := getWithBearer(t, client, server.URL+"/api/v1/admin/auth/session", logoutSession.TokenPair.AccessToken)
	if loggedOutAccess.StatusCode != http.StatusUnauthorized || problemCode(t, loggedOutAccess) != "admin_auth.session_revoked" {
		t.Fatal("bearer access remained usable after logout")
	}
	loggedOutRefresh := postJSON(t, client, refreshURL, "", map[string]any{
		"transport":         "bearer",
		"refresh_token":     logoutSession.TokenPair.RefreshToken,
		"controlled_client": controlledClientPayload(primary.ClientID, primary.CredentialID, primary.ProofType, primary.Secret),
	}, nil, nil)
	if loggedOutRefresh.StatusCode != http.StatusUnauthorized || problemCode(t, loggedOutRefresh) != "admin_auth.session_revoked" {
		t.Fatal("bearer refresh remained usable after logout")
	}

	if err := identityService.DisableControlledAdminClient(ctx, primary.ClientID); err != nil {
		t.Fatal(err)
	}
	disabledAccess := getWithBearer(t, client, server.URL+"/api/v1/admin/auth/session", refreshed.TokenPair.AccessToken)
	if disabledAccess.StatusCode != http.StatusUnauthorized || problemCode(t, disabledAccess) != "admin_auth.session_revoked" {
		t.Fatal("disabling a controlled client must invalidate its issued access token")
	}
	disabledRefresh := postJSON(t, client, refreshURL, "", map[string]any{
		"transport":         "bearer",
		"refresh_token":     refreshed.TokenPair.RefreshToken,
		"controlled_client": controlledClientPayload(primary.ClientID, primary.CredentialID, primary.ProofType, primary.Secret),
	}, nil, nil)
	if disabledRefresh.StatusCode != http.StatusUnauthorized || problemCode(t, disabledRefresh) != "admin_auth.session_revoked" {
		t.Fatal("disabling a controlled client must invalidate its issued refresh token")
	}

	expectedAuditActions := map[string]string{
		bearerLoginTraceID:       "admin.auth.login_succeeded",
		bearerRefreshTraceID:     "admin.auth.session_refreshed",
		bearerLogoutLoginTraceID: "admin.auth.login_succeeded",
		bearerLogoutTraceID:      "admin.auth.session_revoked",
	}
	auditDeadline := time.Now().Add(6 * time.Second)
	for {
		matched := 0
		for traceID, expectedAction := range expectedAuditActions {
			var action string
			err := database.Pool.QueryRow(ctx, `SELECT action FROM audit.events WHERE trace_id=$1`, traceID).Scan(&action)
			if err == nil && action == expectedAction {
				matched++
			}
		}
		if matched == len(expectedAuditActions) {
			break
		}
		if time.Now().After(auditDeadline) {
			t.Fatalf("bearer audit events were not queryable by trace_id: matched=%d want=%d", matched, len(expectedAuditActions))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestIdentityAuditOutboxRetriesWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	repository := identitypostgres.New(database.Pool)
	accessService := accesscontrol.NewService(accesspostgres.New(database.Pool), nil)
	hasher, err := securevalue.NewHasher("integration-test-token-pepper-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(repository, accessService, identity.Bcrypt{Cost: 10}, hasher, identity.Policy{
		AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour,
		LoginWindow: 10 * time.Minute, LoginMaximumAttempts: 5, LoginBlockDuration: 10 * time.Minute,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := identityService.BootstrapAdminIdentity(ctx, "retry-admin@example.test", "Retry Administrator", []byte("correct-password-123"))
	if err != nil {
		t.Fatal(err)
	}
	if err := accessService.BootstrapPlatformAdmin(ctx, accesscontrol.BootstrapCommand{BindingID: "binding-retry-admin", RoleID: "role-retry-admin", AdminUserID: adminID, Now: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	auditService := audit.NewService(auditpostgres.New(database.Pool))
	flakyAudit := &failOnceAuditAdapter{delegate: identityAuditAdapter{service: auditService}}
	flakyAudit.remaining.Store(1)
	dispatcher := identity.NewOutboxDispatcher(repository, flakyAudit, slog.New(slog.NewTextHandler(io.Discard, nil)))
	go dispatcher.Run(ctx)

	const traceID = "trace-audit-retry-postgres"
	if _, err := identityService.LoginAdmin(ctx, identity.LoginCommand{
		Identifier: "retry-admin@example.test", Credential: "correct-password-123", Requested: identity.TransportCookie,
		Source: "127.0.0.1", TraceID: traceID,
	}); err != nil {
		t.Fatal(err)
	}

	failureDeadline := time.Now().Add(3 * time.Second)
	failurePersisted := false
	for !failurePersisted {
		var status, lastError string
		var attempts int
		err := database.Pool.QueryRow(ctx, `SELECT status,attempt_count,COALESCE(last_error,'') FROM identity.outbox_events WHERE payload->>'trace_id'=$1`, traceID).Scan(&status, &attempts, &lastError)
		failurePersisted = err == nil && status == "pending" && attempts == 1 && lastError == "audit append failed"
		if failurePersisted {
			break
		}
		if time.Now().After(failureDeadline) {
			t.Fatalf("transient audit failure was not retained for retry: status=%q attempts=%d last_error=%q error=%v", status, attempts, lastError, err)
		}
		time.Sleep(25 * time.Millisecond)
	}

	successDeadline := time.Now().Add(6 * time.Second)
	for {
		var status, lastError string
		var attempts, auditCount int
		err := database.Pool.QueryRow(ctx, `
			SELECT o.status,o.attempt_count,COALESCE(o.last_error,''),
			       (SELECT count(*) FROM audit.events a WHERE a.trace_id=$1 AND a.action='admin.auth.login_succeeded')
			FROM identity.outbox_events o WHERE o.payload->>'trace_id'=$1`, traceID).Scan(&status, &attempts, &lastError, &auditCount)
		if err == nil && status == "published" && attempts == 2 && lastError == "" && auditCount == 1 {
			break
		}
		if time.Now().After(successDeadline) {
			t.Fatalf("audit retry did not publish exactly once: status=%q attempts=%d last_error=%q audit_count=%d error=%v", status, attempts, lastError, auditCount, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func postJSON(t *testing.T, client *http.Client, target, origin string, body any, cookies []*http.Cookie, headers map[string]string) *http.Response {
	t.Helper()
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(http.MethodPost, target, payload)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Origin", origin)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func get(t *testing.T, client *http.Client, target string) *http.Response {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func getWithCookies(t *testing.T, client *http.Client, target string, cookies []*http.Cookie) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func getWithBearer(t *testing.T, client *http.Client, target, accessToken string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func controlledClientPayload(clientID, credentialID, proofType, proof string) map[string]any {
	return map[string]any{"client_id": clientID, "credential_id": credentialID, "proof_type": proofType, "proof": proof}
}

func assertCatalogAuthorization(t *testing.T, snapshot accesscontrol.Snapshot, scopeType, productID string) {
	t.Helper()
	definitions := accesscontrol.CurrentPermissionCatalog().Definitions()
	wantPermissionCount := 0
	for _, definition := range definitions {
		if definition.GrantsPlatformSuperAdminOnBootstrap() {
			wantPermissionCount++
		}
	}
	if len(snapshot.Permissions) != wantPermissionCount {
		t.Fatalf("authorization permissions = %v, want bootstrap-granted catalog permissions", snapshot.Permissions)
	}
	granted := make(map[string]struct{}, len(snapshot.Permissions))
	for _, permission := range snapshot.Permissions {
		granted[permission] = struct{}{}
	}
	for _, definition := range definitions {
		_, ok := granted[definition.Code]
		if definition.GrantsPlatformSuperAdminOnBootstrap() && !ok {
			t.Fatalf("authorization is missing permission %q", definition.Code)
		}
		if !definition.GrantsPlatformSuperAdminOnBootstrap() && ok {
			t.Fatalf("authorization unexpectedly granted permission %q", definition.Code)
		}
	}
	if len(snapshot.Scopes) != 1 || snapshot.Scopes[0].Type != scopeType || snapshot.Scopes[0].ProductID != productID {
		t.Fatalf("authorization scopes = %#v, want type=%q product_id=%q", snapshot.Scopes, scopeType, productID)
	}
}

func assertSessionCookies(t *testing.T, response *http.Response) {
	t.Helper()
	cookies := response.Cookies()
	access := namedCookie(t, cookies, "__Host-platform_admin_access")
	refresh := namedCookie(t, cookies, "__Secure-platform_admin_refresh")
	if !access.Secure || !access.HttpOnly || access.SameSite != http.SameSiteStrictMode || access.Path != "/" || access.Domain != "" {
		t.Fatalf("unsafe access cookie: %#v", access)
	}
	if !refresh.Secure || !refresh.HttpOnly || refresh.SameSite != http.SameSiteStrictMode || refresh.Path != "/api/v1/admin/auth" || refresh.Domain != "" {
		t.Fatalf("unsafe refresh cookie: %#v", refresh)
	}
}

func namedCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			copy := *cookie
			return &copy
		}
	}
	t.Fatalf("cookie %q not found in %#v", name, cookies)
	return nil
}

func problemCode(t *testing.T, response *http.Response) string {
	t.Helper()
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(readBody(t, response)), &problem); err != nil {
		t.Fatal(err)
	}
	return problem.Code
}

func assertIndistinguishableAuthFailures(t *testing.T, responses ...*http.Response) {
	t.Helper()
	var baseline map[string]any
	for index, response := range responses {
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("authentication failure %d status = %d, want 401", index, response.StatusCode)
		}
		var problem map[string]any
		if err := json.Unmarshal([]byte(readBody(t, response)), &problem); err != nil {
			t.Fatal(err)
		}
		delete(problem, "request_id")
		if problem["code"] != "admin_auth.invalid_credentials" {
			t.Fatalf("authentication failure %d code = %v", index, problem["code"])
		}
		if index == 0 {
			baseline = problem
			continue
		}
		if !reflect.DeepEqual(problem, baseline) {
			t.Fatalf("authentication failure %d is distinguishable: got=%v want=%v", index, problem, baseline)
		}
	}
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
