package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/testsupport/g2a06acceptance"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFormalServerOpensPreparedInteractionsRealPostgres(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL is required for the formal server compatibility test")
	}
	root := testRepositoryRoot(t)
	dsn := os.Getenv("TEST_DATABASE_URL")
	if g2a06acceptance.ValidateDatabaseURL(dsn) != nil {
		t.Fatal("TEST_DATABASE_URL must be loopback platform_test_control")
	}
	cfg := ciFormalConfig(t, t.TempDir(), dsn)
	outputTargets, err := json.Marshal(cfg.Assembly.OutputTargets)
	if err != nil {
		t.Fatal(err)
	}
	backendRoot := filepath.Join(root, "platform", "backend")
	processEnvironment := append(os.Environ(),
		"PLATFORM_ENVIRONMENT=local",
		"PLATFORM_DATABASE_URL="+cfg.Database.URL,
		"PLATFORM_ADMIN_TOKEN_PEPPER="+cfg.AdminAuth.TokenPepper,
		"PLATFORM_ASSEMBLY_OUTPUT_TARGETS="+string(outputTargets),
	)
	toolRoot := t.TempDir()
	migrationBinary := filepath.Join(toolRoot, "g2a06-formal-migrate.exe")
	buildFormalCommand(t, backendRoot, migrationBinary, "./cmd/migrate")
	migrate := exec.Command(migrationBinary, "up")
	migrate.Dir, migrate.Env = backendRoot, processEnvironment
	if output, migrateErr := migrate.CombinedOutput(); migrateErr != nil {
		t.Fatalf("apply formal migrations: %v: %s", migrateErr, output)
	}
	pool, err := pgxpool.New(context.Background(), cfg.Database.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	password := []byte("g2a06 CI acceptance password 0001")
	defer clear(password)
	options := g2a06acceptance.Options{RepositoryRoot: root, Password: password, AdminTokenPepper: cfg.AdminAuth.TokenPepper, UserAuth: cfg.UserAuth, HostedInteraction: cfg.HostedInteraction, AcceptanceFixture: true}
	prepared, err := g2a06acceptance.Prepare(context.Background(), pool, options)
	if err != nil {
		t.Fatal(err)
	}
	formalRevocableSessionID := "uses_formal_revoke_" + prepared.AccountInteractionID
	formalRevocableFamilyID := "family_formal_revoke_" + prepared.AccountInteractionID
	seeded, err := pool.Exec(context.Background(), `INSERT INTO identity.end_user_sessions(session_id,user_id,product_id,application_id,tenant_id,token_family_id,authentication_method,session_version,auth_time,created_at,last_seen_at,access_expires_at,refresh_expires_at,absolute_expires_at,risk_summary_digest,revoked_at,revoke_reason,environment)
		SELECT $1,user_id,product_id,application_id,tenant_id,$2,authentication_method,session_version,auth_time,created_at-INTERVAL '1 minute',last_seen_at-INTERVAL '1 minute',access_expires_at,refresh_expires_at,absolute_expires_at,risk_summary_digest,NULL,NULL,environment
		FROM identity.end_user_sessions WHERE session_id=$3`, formalRevocableSessionID, formalRevocableFamilyID, prepared.AccountUserSessionID)
	if err != nil || seeded.RowsAffected() != 1 {
		t.Fatalf("seed formal revocable session rows=%d err=%v", seeded.RowsAffected(), err)
	}
	defer func() {
		if _, cleanupErr := pool.Exec(context.Background(), `DELETE FROM identity.end_user_sessions WHERE session_id=$1`, formalRevocableSessionID); cleanupErr != nil {
			t.Errorf("cleanup formal revocable session: %v", cleanupErr)
		}
	}()
	defer func() {
		cleanup := resultCleanupCommand(prepared)
		if cleanupErr := g2a06acceptance.Cleanup(context.Background(), pool, options, cleanup); cleanupErr != nil {
			t.Errorf("cleanup: %v", cleanupErr)
		}
	}()

	serverBinary := filepath.Join(toolRoot, "g2a06-formal-server.exe")
	buildFormalCommand(t, backendRoot, serverBinary, "./cmd/server")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	serverContext, stopServer := context.WithCancel(context.Background())
	server := exec.CommandContext(serverContext, serverBinary)
	server.Dir = backendRoot
	server.Env = append(processEnvironment, "PLATFORM_HTTP_ADDRESS="+address)
	var serverLog bytes.Buffer
	server.Stdout, server.Stderr = &serverLog, &serverLog
	if err = server.Start(); err != nil {
		stopServer()
		t.Fatal(err)
	}
	defer func() {
		stopServer()
		_ = server.Wait()
	}()
	baseURL := "http://" + address
	previousBase := formalHTTPBase
	formalHTTPBase = baseURL
	defer func() { formalHTTPBase = previousBase }()
	waitForFormalServer(t, baseURL, &serverLog)
	verifyFormalBootstrap(t, baseURL, cfg.HostedInteraction.AllowedOrigin, prepared.AuthInteractionID, "auth", &serverLog)
	verifyFormalBootstrap(t, baseURL, cfg.HostedInteraction.AllowedOrigin, prepared.AccountInteractionID, "account", &serverLog)
	opened, cookie, openErr := openAcceptance(context.Background(), prepared.AuthInteractionID)
	if openErr != nil {
		t.Fatal(openErr)
	}
	body, _ := json.Marshal(map[string]any{"identifier": "g2a05-account-acceptance@example.test", "credential": string(password), "risk_summary": map[string]any{}})
	completed, completeErr := acceptanceRequest(context.Background(), http.MethodPost, "/api/v1/hosted/interactions/"+prepared.AuthInteractionID+"/auth/password", body, cookie, opened.CSRFToken, "")
	clear(body)
	if completeErr != nil || completed.status != http.StatusOK {
		t.Fatalf("complete positive auth status=%d err=%v body=%s", completed.status, completeErr, completed.raw)
	}
	clear(completed.raw)
	accountOpened, accountCookie, accountOpenErr := openAcceptance(context.Background(), prepared.AccountInteractionID)
	if accountOpenErr != nil {
		t.Fatal(accountOpenErr)
	}
	verifyFormalAccountSessionRevocation(t, prepared.AccountInteractionID, formalRevocableSessionID, accountCookie, accountOpened.CSRFToken, pool)
	accountBody, _ := json.Marshal(map[string]string{"result": "self_service_completed"})
	accountCompleted, accountCompleteErr := acceptanceRequest(context.Background(), http.MethodPost, "/api/v1/hosted/interactions/"+prepared.AccountInteractionID+"/account/complete", accountBody, accountCookie, accountOpened.CSRFToken, "", "g2a06-account-complete-0001")
	clear(accountBody)
	if accountCompleteErr != nil || accountCompleted.status != http.StatusOK {
		clear(accountCompleted.raw)
		t.Fatalf("complete account status=%d err=%v", accountCompleted.status, accountCompleteErr)
	}
	clear(accountCompleted.raw)
	manifest := acceptanceManifest{Version: manifestVersion, AuthInteractionID: prepared.AuthInteractionID, NegativeAuthInteractionID: prepared.NegativeAuthInteractionID, AccountInteractionID: prepared.AccountInteractionID}
	payload := resultPayload(prepared, password)
	cleanupCommand := resultCleanupCommand(prepared)
	persist := func(next manifestPayload) error { payload = next; return nil }
	status := func() (g2a06acceptance.InteractionStatuses, error) {
		return g2a06acceptance.Statuses(context.Background(), pool, options, cleanupCommand)
	}
	if err = verifyAcceptance(context.Background(), manifest, payload, persist, status); err != nil {
		t.Fatal(err)
	}
}

func verifyFormalAccountSessionRevocation(t *testing.T, interactionID, targetSessionID string, cookie *http.Cookie, csrfToken string, pool *pgxpool.Pool) {
	t.Helper()
	bootstrapPath := "/api/v1/hosted/interactions/" + interactionID + "/account/bootstrap"
	type bootstrapPayload struct {
		Sessions []struct {
			SessionID string `json:"session_id"`
			Current   bool   `json:"current"`
		} `json:"sessions"`
		AllowedActions []string `json:"allowed_actions"`
	}
	bootstrap := func() bootstrapPayload {
		response, err := acceptanceRequest(context.Background(), http.MethodGet, bootstrapPath, nil, cookie, "", "")
		if err != nil || response.status != http.StatusOK {
			clear(response.raw)
			t.Fatalf("formal account bootstrap status=%d err=%v", response.status, err)
		}
		defer clear(response.raw)
		var payload bootstrapPayload
		if err = json.Unmarshal(response.raw, &payload); err != nil {
			t.Fatalf("decode formal account bootstrap: %v", err)
		}
		return payload
	}
	initial := bootstrap()
	foundTarget, foundCurrent, foundRevokeAction := false, false, false
	for _, action := range initial.AllowedActions {
		foundRevokeAction = foundRevokeAction || action == "revoke_session"
	}
	for _, session := range initial.Sessions {
		if session.Current {
			foundCurrent = true
		}
		if session.SessionID == targetSessionID {
			foundTarget = true
			if session.Current {
				t.Fatal("formal revocation target was projected as the current session")
			}
		}
	}
	if !foundTarget || !foundCurrent || !foundRevokeAction {
		t.Fatalf("formal account bootstrap missing revocation projection: current=%t target=%t action=%t payload=%+v", foundCurrent, foundTarget, foundRevokeAction, initial)
	}
	revokePath := "/api/v1/hosted/interactions/" + interactionID + "/account/sessions/" + targetSessionID
	const idempotencyKey = "g2a06-formal-session-revoke-0001"
	revoked, err := acceptanceRequest(context.Background(), http.MethodDelete, revokePath, nil, cookie, csrfToken, "", idempotencyKey)
	if err != nil || revoked.status != http.StatusNoContent {
		clear(revoked.raw)
		t.Fatalf("formal account session revoke status=%d err=%v", revoked.status, err)
	}
	clear(revoked.raw)
	for _, session := range bootstrap().Sessions {
		if session.SessionID == targetSessionID {
			t.Fatalf("revoked formal session remained in active bootstrap projection: %+v", session)
		}
	}
	replayed, err := acceptanceRequest(context.Background(), http.MethodDelete, revokePath, nil, cookie, csrfToken, "", idempotencyKey)
	if err != nil || replayed.status != http.StatusNoContent {
		clear(replayed.raw)
		t.Fatalf("formal account session revoke replay status=%d err=%v", replayed.status, err)
	}
	clear(replayed.raw)
	var revokedAt *time.Time
	if err = pool.QueryRow(context.Background(), `SELECT revoked_at FROM identity.end_user_sessions WHERE session_id=$1`, targetSessionID).Scan(&revokedAt); err != nil || revokedAt == nil {
		t.Fatalf("formal revoked_at missing session=%s err=%v", targetSessionID, err)
	}
}

func ciFormalConfig(t *testing.T, root, dsn string) config.Config {
	t.Helper()
	target, artifacts := filepath.Join(root, "target"), filepath.Join(root, "artifacts")
	if os.MkdirAll(target, 0700) != nil || os.MkdirAll(artifacts, 0700) != nil {
		t.Fatal("create CI output roots")
	}
	pepper := strings.Repeat("g2a06-ci-admin-pepper-", 3)
	output, _ := json.Marshal([]config.AssemblyOutputTarget{{Reference: "workspace.g2a06-ci", Environment: "test", DisplayName: "G2A06 CI", Summary: "CI acceptance target", IsDefault: true, TargetRoot: target, ArtifactRoot: artifacts}})
	values := map[string]string{"PLATFORM_ENVIRONMENT": "local", "PLATFORM_DATABASE_URL": dsn, "PLATFORM_ADMIN_TOKEN_PEPPER": pepper, "PLATFORM_ASSEMBLY_OUTPUT_TARGETS": string(output)}
	cfg, err := config.Load(func(name string) (string, bool) { v, ok := values[name]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func buildFormalCommand(t *testing.T, backendRoot, output, command string) {
	t.Helper()
	build := exec.Command("go", "build", "-trimpath", "-o", output, command)
	build.Dir = backendRoot
	runtimeRoot := filepath.Join(backendRoot, "..", "..", ".runtime")
	build.Env = append(os.Environ(),
		"GOMODCACHE="+filepath.Join(runtimeRoot, "go-mod-cache"),
		"GOCACHE="+filepath.Join(runtimeRoot, "go-build-cache"),
		"GOPROXY=off",
	)
	if buildOutput, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build formal command %s: %v: %s", command, err, buildOutput)
	}
}

func waitForFormalServer(t *testing.T, baseURL string, log *bytes.Buffer) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(baseURL + "/health/ready")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("formal server did not become ready: %s", log.String())
}

func verifyFormalBootstrap(t *testing.T, baseURL, allowedOrigin, interactionID, route string, serverLog *bytes.Buffer) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	openRequest, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/hosted/interactions/%s/browser-session", baseURL, interactionID), nil)
	if err != nil {
		t.Fatal(err)
	}
	openResponse, err := client.Do(openRequest)
	if err != nil {
		t.Fatal(err)
	}
	openBody, _ := io.ReadAll(io.LimitReader(openResponse.Body, 4096))
	_ = openResponse.Body.Close()
	if openResponse.StatusCode != http.StatusOK || len(openResponse.Cookies()) != 1 {
		t.Fatalf("formal server open %s status=%d body=%s", route, openResponse.StatusCode, openBody)
	}
	bootstrapRequest, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/hosted/interactions/%s/%s/bootstrap", baseURL, interactionID, route), nil)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapRequest.AddCookie(openResponse.Cookies()[0])
	if bootstrapRequest.Header.Get("Origin") != "" {
		t.Fatal("formal browser-style bootstrap unexpectedly contains Origin")
	}
	bootstrapResponse, err := client.Do(bootstrapRequest)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapBody, _ := io.ReadAll(io.LimitReader(bootstrapResponse.Body, 8192))
	_ = bootstrapResponse.Body.Close()
	if bootstrapResponse.StatusCode != http.StatusOK {
		t.Fatalf("formal server %s bootstrap status=%d body=%s log=%s", route, bootstrapResponse.StatusCode, bootstrapBody, serverLog.String())
	}
	verifyPrivateBootstrapHeaders(t, bootstrapResponse.Header, route+" bootstrap without Origin")
	var payload map[string]any
	if json.Unmarshal(bootstrapBody, &payload) != nil || payload["interaction"] == nil || payload["presentation"] == nil {
		t.Fatalf("formal server %s bootstrap shape rejected", route)
	}
	allowedRequest, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/hosted/interactions/%s/%s/bootstrap", baseURL, interactionID, route), nil)
	if err != nil {
		t.Fatal(err)
	}
	allowedRequest.Header.Set("Origin", allowedOrigin)
	allowedRequest.AddCookie(openResponse.Cookies()[0])
	allowedResponse, err := client.Do(allowedRequest)
	if err != nil {
		t.Fatal(err)
	}
	allowedBody, _ := io.ReadAll(io.LimitReader(allowedResponse.Body, 8192))
	_ = allowedResponse.Body.Close()
	if allowedResponse.StatusCode != http.StatusOK {
		t.Fatalf("formal server %s bootstrap with exact allowed Origin status=%d body=%s", route, allowedResponse.StatusCode, allowedBody)
	}
	verifyPrivateBootstrapHeaders(t, allowedResponse.Header, route+" bootstrap with exact allowed Origin")
	var allowedPayload map[string]any
	if json.Unmarshal(allowedBody, &allowedPayload) != nil || allowedPayload["interaction"] == nil || allowedPayload["presentation"] == nil {
		t.Fatalf("formal server %s allowed-Origin bootstrap shape rejected", route)
	}
	for _, test := range []struct {
		name    string
		origins []string
	}{
		{name: "evil", origins: []string{"https://evil.example.test"}},
		{name: "null", origins: []string{"null"}},
		{name: "empty", origins: []string{""}},
		{name: "duplicate", origins: []string{allowedOrigin, allowedOrigin}},
	} {
		t.Run(route+"_rejects_"+test.name+"_origin", func(t *testing.T) {
			verifyRejectedBootstrapOrigin(t, client, baseURL, interactionID, route, openResponse.Cookies()[0], test.origins)
		})
	}
	if route == "auth" {
		csrfToken, _ := payloadString(openBody, "csrf_token")
		if csrfToken == "" {
			t.Fatal("formal browser session did not return a CSRF token")
		}
		writeRequest, requestErr := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/hosted/interactions/%s/auth/password", baseURL, interactionID), strings.NewReader(`{}`))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		writeRequest.Header.Set("Content-Type", "application/json")
		writeRequest.Header.Set("X-CSRF-Token", csrfToken)
		writeRequest.AddCookie(openResponse.Cookies()[0])
		if writeRequest.Header.Get("Origin") != "" {
			t.Fatal("formal browser-style write unexpectedly contains Origin")
		}
		writeResponse, requestErr := client.Do(writeRequest)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		writeBody, _ := io.ReadAll(io.LimitReader(writeResponse.Body, 4096))
		_ = writeResponse.Body.Close()
		var problem struct {
			Code string `json:"code"`
		}
		if writeResponse.StatusCode != http.StatusForbidden || json.Unmarshal(writeBody, &problem) != nil || problem.Code != "hosted.csrf_failed" {
			t.Fatalf("formal server accepted write without Origin status=%d code=%q body=%s", writeResponse.StatusCode, problem.Code, writeBody)
		}
	}
}

func verifyRejectedBootstrapOrigin(t *testing.T, client *http.Client, baseURL, interactionID, route string, cookie *http.Cookie, origins []string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/hosted/interactions/%s/%s/bootstrap", baseURL, interactionID, route), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header["Origin"] = append([]string(nil), origins...)
	request.AddCookie(cookie)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	var problem struct {
		Code string `json:"code"`
	}
	if response.StatusCode != http.StatusForbidden || json.Unmarshal(body, &problem) != nil || problem.Code != "hosted.csrf_failed" {
		t.Fatalf("formal server accepted %q Origin bootstrap status=%d code=%q body=%s", origins, response.StatusCode, problem.Code, body)
	}
	verifyPrivateBootstrapHeaders(t, response.Header, "rejected Origin bootstrap")
}

func verifyPrivateBootstrapHeaders(t *testing.T, header http.Header, context string) {
	t.Helper()
	if len(header.Values("Access-Control-Allow-Origin")) != 0 || len(header.Values("Access-Control-Allow-Credentials")) != 0 {
		t.Fatalf("formal server exposed CORS allow headers for %s: origin=%q credentials=%q", context, header.Values("Access-Control-Allow-Origin"), header.Values("Access-Control-Allow-Credentials"))
	}
	if !strings.Contains(strings.ToLower(header.Get("Cache-Control")), "no-store") {
		t.Fatalf("formal server returned %s without no-store cache control: %q", context, header.Get("Cache-Control"))
	}
}

func payloadString(raw []byte, field string) (string, bool) {
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return "", false
	}
	value, ok := payload[field].(string)
	return value, ok
}

func testRepositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := locateRoot(workingDirectory)
	if err != nil {
		t.Fatal(err)
	}
	return root
}
