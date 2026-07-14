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
	"strings"
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
	if wrong.StatusCode != http.StatusUnauthorized || noScope.StatusCode != http.StatusUnauthorized || problemCode(t, wrong) != "admin_auth.invalid_credentials" || problemCode(t, noScope) != "admin_auth.invalid_credentials" {
		t.Fatal("invalid credentials and no-scope responses must be indistinguishable")
	}

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
	_ = readBody(t, refresh)
	assertSessionCookies(t, refresh)

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
	ctx := context.Background()
	repository := identitypostgres.New(database.Pool)
	accessService := accesscontrol.NewService(accesspostgres.New(database.Pool), nil)
	hasher, err := securevalue.NewHasher("integration-test-token-pepper-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
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

	login := postJSON(t, client, loginURL, "", map[string]any{
		"identifier":        "bearer-admin@example.test",
		"credential":        "correct-bearer-password-123",
		"transport":         "bearer",
		"controlled_client": controlledClientPayload(primary.ClientID, primary.CredentialID, primary.ProofType, primary.Secret),
	}, nil, nil)
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

	refresh := postJSON(t, client, refreshURL, "", map[string]any{
		"transport":         "bearer",
		"refresh_token":     originalRefresh,
		"controlled_client": controlledClientPayload(primary.ClientID, primary.CredentialID, primary.ProofType, primary.Secret),
	}, nil, nil)
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
