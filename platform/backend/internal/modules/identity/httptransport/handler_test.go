package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/modules/identity"
)

type serviceStub struct {
	login          identity.AdminSession
	loginCommand   identity.LoginCommand
	logoutCommand  identity.LogoutCommand
	logoutCalls    int
	refreshCommand identity.RefreshCommand
	refreshCalls   int
	err            error
}

func (s *serviceStub) LoginAdmin(_ context.Context, command identity.LoginCommand) (identity.AdminSession, error) {
	s.loginCommand = command
	return s.login, s.err
}
func (s *serviceStub) CurrentAdminSession(context.Context, string) (identity.AdminSession, error) {
	return s.login, s.err
}
func (s *serviceStub) RefreshAdminSessionWithClient(_ context.Context, command identity.RefreshCommand) (identity.AdminSession, error) {
	s.refreshCommand = command
	s.refreshCalls++
	return s.login, s.err
}
func (s *serviceStub) LogoutAdmin(_ context.Context, command identity.LogoutCommand) error {
	s.logoutCommand = command
	s.logoutCalls++
	return s.err
}

func cookieSession() identity.AdminSession {
	now := time.Now().UTC()
	csrf := "test-csrf-token-material"
	return identity.AdminSession{SessionID: "session", Transport: identity.TransportCookie, Admin: identity.AdminIdentitySummary{AdminUserID: "user", DisplayName: "Admin", AccountStatus: "active", AuthTime: now, AuthenticationMethod: "password"}, Authorization: accesscontrol.Snapshot{AuthorizationVersion: 1, Permissions: []string{"platform.read"}, Scopes: []accesscontrol.Scope{{Type: "platform"}}}, AccessExpiresAt: now.Add(time.Minute), RefreshExpiresAt: now.Add(time.Hour), CSRFToken: &csrf, CookieTokens: &identity.IssuedTokens{AccessToken: "test-access-token-material", RefreshToken: "test-refresh-token-material", AccessExpiresAt: now.Add(time.Minute), RefreshExpiresAt: now.Add(time.Hour)}}
}

func TestCookieLoginSetsFixedSecureCookiesWithoutLeakingTokens(t *testing.T) {
	stub := &serviceStub{login: cookieSession()}
	handler := New(stub, Config{AllowedOrigins: []string{"https://admin.example.com"}})
	body := []byte(`{"identifier":"admin@example.com","credential":"password","transport":"cookie"}`)
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/login", bytes.NewReader(body))
	req.Header.Set("Origin", "https://admin.example.com")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookies=%d", len(cookies))
	}
	for _, cookie := range cookies {
		if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Domain != "" {
			t.Fatalf("unsafe cookie: %+v", cookie)
		}
	}
	if cookies[0].Name != accessCookieName || cookies[0].Path != "/" || cookies[1].Name != refreshCookieName || cookies[1].Path != "/api/v1/admin/auth" {
		t.Fatalf("unexpected cookie paths: %+v", cookies)
	}
	if strings.Contains(rr.Body.String(), "test-access-token-material") || strings.Contains(rr.Body.String(), "test-refresh-token-material") {
		t.Fatal("cookie response leaked opaque token")
	}
}

func TestCookieLoginRejectsUnregisteredOrigin(t *testing.T) {
	handler := New(&serviceStub{login: cookieSession()}, Config{AllowedOrigins: []string{"https://admin.example.com"}})
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/login", bytes.NewBufferString(`{"identifier":"a","credential":"b"}`))
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestCookieRefreshRequiresOriginButNotCSRF(t *testing.T) {
	stub := &serviceStub{login: cookieSession()}
	handler := New(stub, Config{AllowedOrigins: []string{"https://admin.example.com"}})
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/refresh", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "old-refresh"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || stub.refreshCommand.Transport != identity.TransportCookie || stub.refreshCommand.ControlledClient != nil {
		t.Fatalf("status=%d command=%+v", rr.Code, stub.refreshCommand)
	}
}

func TestCookieRefreshWithoutCookieReturnsExpiredSession(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType bool
	}{
		{name: "empty body"},
		{name: "empty object", body: `{}`, contentType: true},
		{name: "explicit cookie transport", body: `{"transport":"cookie"}`, contentType: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &serviceStub{login: cookieSession()}
			handler := New(stub, Config{AllowedOrigins: []string{"https://admin.example.com"}})
			var body *bytes.Reader
			if test.body == "" {
				body = bytes.NewReader(nil)
			} else {
				body = bytes.NewReader([]byte(test.body))
			}
			req := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/refresh", body)
			req.Header.Set("Origin", "https://admin.example.com")
			if test.contentType {
				req.Header.Set("Content-Type", "application/json")
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			assertProblemCode(t, rr, "admin_auth.session_expired")
			if stub.refreshCalls != 0 {
				t.Fatalf("refresh service calls=%d command=%+v", stub.refreshCalls, stub.refreshCommand)
			}
			assertClearedSessionCookies(t, rr)
		})
	}
}

func TestCookieRefreshWithoutCookieRejectsMissingOrUnregisteredOrigin(t *testing.T) {
	for _, origin := range []string{"", "https://evil.example"} {
		t.Run(origin, func(t *testing.T) {
			stub := &serviceStub{login: cookieSession()}
			handler := New(stub, Config{AllowedOrigins: []string{"https://admin.example.com"}})
			req := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/refresh", nil)
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("origin=%q status=%d body=%s", origin, rr.Code, rr.Body.String())
			}
			assertProblemCode(t, rr, "admin_auth.origin_denied")
			if stub.refreshCalls != 0 {
				t.Fatalf("refresh service calls=%d", stub.refreshCalls)
			}
			if cookies := rr.Result().Cookies(); len(cookies) != 0 {
				t.Fatalf("origin rejection changed cookies: %+v", cookies)
			}
		})
	}
}

func TestBearerLoginAndRefreshRequireControlledClientProof(t *testing.T) {
	clientID := "acli_12345678"
	credentialID := "acred_12345678"
	proof := strings.Repeat("p", 43)
	stub := &serviceStub{login: identity.AdminSession{Transport: identity.TransportBearer}}
	handler := New(stub, Config{AllowedOrigins: []string{"https://admin.example.com"}})

	missing := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/login", bytes.NewBufferString(`{"identifier":"a","credential":"b","transport":"bearer"}`))
	missing.Header.Set("Content-Type", "application/json")
	missingResult := httptest.NewRecorder()
	handler.ServeHTTP(missingResult, missing)
	if missingResult.Code != http.StatusBadRequest {
		t.Fatalf("missing proof status=%d", missingResult.Code)
	}

	loginBody, err := json.Marshal(map[string]any{
		"identifier": "a", "credential": "b", "transport": "bearer",
		"controlled_client": map[string]any{"client_id": clientID, "credential_id": credentialID, "proof_type": "shared_secret_v1", "proof": proof},
	})
	if err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/login", bytes.NewReader(loginBody))
	login.Header.Set("Content-Type", "application/json")
	loginResult := httptest.NewRecorder()
	handler.ServeHTTP(loginResult, login)
	if loginResult.Code != http.StatusOK || stub.loginCommand.ControlledClient == nil || stub.loginCommand.ControlledClient.Secret != proof {
		t.Fatalf("login status=%d command=%+v", loginResult.Code, stub.loginCommand)
	}

	refreshBody, err := json.Marshal(map[string]any{
		"transport": "bearer", "refresh_token": "refresh-token-material-at-least-32-bytes",
		"controlled_client": map[string]any{"client_id": clientID, "credential_id": credentialID, "proof_type": "shared_secret_v1", "proof": proof},
	})
	if err != nil {
		t.Fatal(err)
	}
	refresh := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/refresh", bytes.NewReader(refreshBody))
	refresh.Header.Set("Content-Type", "application/json")
	refreshResult := httptest.NewRecorder()
	handler.ServeHTTP(refreshResult, refresh)
	if refreshResult.Code != http.StatusOK || stub.refreshCommand.ControlledClient == nil || stub.refreshCommand.ControlledClient.CredentialID != credentialID {
		t.Fatalf("refresh status=%d command=%+v", refreshResult.Code, stub.refreshCommand)
	}
}

func TestCookieLoginRejectsControlledClientProof(t *testing.T) {
	handler := New(&serviceStub{login: cookieSession()}, Config{AllowedOrigins: []string{"https://admin.example.com"}})
	body := `{"identifier":"a","credential":"b","transport":"cookie","controlled_client":{"client_id":"acli_12345678","credential_id":"acred_12345678","proof_type":"shared_secret_v1","proof":"` + strings.Repeat("p", 43) + `"}}`
	request := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/login", bytes.NewBufferString(body))
	request.Header.Set("Origin", "https://admin.example.com")
	request.Header.Set("Content-Type", "application/json")
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	if result.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", result.Code)
	}
}

func TestCookieLogoutPassesAllSessionProofsToService(t *testing.T) {
	stub := &serviceStub{login: cookieSession()}
	handler := New(stub, Config{AllowedOrigins: []string{"https://admin.example.com"}})
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/logout", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	req.Header.Set(csrfHeader, "csrf-proof")
	req.AddCookie(&http.Cookie{Name: accessCookieName, Value: "expired-access"})
	req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "valid-refresh"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent || stub.logoutCalls != 1 {
		t.Fatalf("unexpected logout: status=%d calls=%d", rr.Code, stub.logoutCalls)
	}
	command := stub.logoutCommand
	if command.Transport != identity.TransportCookie || command.AccessToken != "expired-access" || command.RefreshToken != "valid-refresh" || command.CSRFToken != "csrf-proof" {
		t.Fatalf("logout command=%+v", command)
	}
	assertClearedSessionCookies(t, rr)
}

func TestCookieLogoutRejectsMissingOrUnregisteredOrigin(t *testing.T) {
	for _, origin := range []string{"", "https://evil.example"} {
		t.Run(origin, func(t *testing.T) {
			stub := &serviceStub{login: cookieSession()}
			handler := New(stub, Config{AllowedOrigins: []string{"https://admin.example.com"}})
			req := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/logout", nil)
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			req.AddCookie(&http.Cookie{Name: accessCookieName, Value: "access"})
			req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "refresh"})
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("origin=%q status=%d body=%s", origin, rr.Code, rr.Body.String())
			}
			assertProblemCode(t, rr, "admin_auth.origin_denied")
			if stub.logoutCalls != 0 {
				t.Fatalf("logout service calls=%d", stub.logoutCalls)
			}
			if cookies := rr.Result().Cookies(); len(cookies) != 0 {
				t.Fatalf("origin rejection changed cookies: %+v", cookies)
			}
		})
	}
}

func TestTransientLogoutFailureKeepsCookiesForRetry(t *testing.T) {
	stub := &serviceStub{err: errors.New("temporary database failure")}
	handler := New(stub, Config{AllowedOrigins: []string{"https://admin.example.com"}})
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/logout", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "still-valid"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if stub.logoutCalls != 1 {
		t.Fatalf("logout service calls=%d", stub.logoutCalls)
	}
	if cookies := rr.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("transient logout failure cleared cookies: %+v", cookies)
	}
}

func TestTerminalLogoutFailureClearsCookies(t *testing.T) {
	terminalErrors := []error{identity.ErrSessionExpired, identity.ErrSessionRevoked, identity.ErrRefreshReplayed}
	for _, terminalErr := range terminalErrors {
		t.Run(terminalErr.Error(), func(t *testing.T) {
			stub := &serviceStub{err: terminalErr}
			handler := New(stub, Config{AllowedOrigins: []string{"https://admin.example.com"}})
			req := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/logout", nil)
			req.Header.Set("Origin", "https://admin.example.com")
			req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "terminal-refresh"})
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			assertClearedSessionCookies(t, rr)
		})
	}
}

func TestErrorsUseStableProblemCode(t *testing.T) {
	stub := &serviceStub{err: identity.ErrRefreshReplayed}
	handler := New(stub, Config{AllowedOrigins: []string{"https://admin.example.com"}})
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/refresh", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "old"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "admin_auth.refresh_replayed" {
		t.Fatalf("body=%v", body)
	}
	if len(rr.Result().Cookies()) != 2 {
		t.Fatal("terminal refresh replay must clear both session cookies")
	}
}

func TestTransientRefreshFailureKeepsCookiesForRetry(t *testing.T) {
	stub := &serviceStub{err: errors.New("temporary database failure")}
	handler := New(stub, Config{AllowedOrigins: []string{"https://admin.example.com"}})
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/refresh", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "still-valid"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", rr.Code)
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Fatalf("transient refresh failure cleared cookies: %+v", rr.Result().Cookies())
	}
}

func TestRateLimitIncludesConsistentRetryMetadata(t *testing.T) {
	stub := &serviceStub{err: &identity.RateLimitError{RetryAfter: 90 * time.Second}}
	handler := New(stub, Config{AllowedOrigins: []string{"https://admin.example.com"}})
	req := httptest.NewRequest(http.MethodPost, "https://api.example.com/api/v1/admin/auth/login", bytes.NewBufferString(`{"identifier":"a","credential":"b"}`))
	req.Header.Set("Origin", "https://admin.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") != "90" {
		t.Fatalf("status=%d retry-after=%q", rr.Code, rr.Header().Get("Retry-After"))
	}
	var body struct {
		Retryable        bool `json:"retryable"`
		RetryAfterSecond int  `json:"retry_after_seconds"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Retryable || body.RetryAfterSecond != 90 {
		t.Fatalf("body=%+v", body)
	}
}

func TestAdminAccessTokenAcceptsOnlyCookieOrBearerAccessProof(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/admin/audit/events", nil)
	request.AddCookie(&http.Cookie{Name: accessCookieName, Value: "cookie-access"})
	if token, ok := AdminAccessToken(request); !ok || token != "cookie-access" {
		t.Fatalf("cookie access proof = %q, %v", token, ok)
	}
	request = httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/admin/audit/events", nil)
	request.Header.Set("Authorization", "Bearer bearer-access")
	if token, ok := AdminAccessToken(request); !ok || token != "bearer-access" {
		t.Fatalf("bearer access proof = %q, %v", token, ok)
	}
	request = httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/admin/audit/events", nil)
	request.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "refresh-is-not-access"})
	if _, ok := AdminAccessToken(request); ok {
		t.Fatal("refresh cookie was accepted as an access proof")
	}
}

func assertProblemCode(t *testing.T, rr *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != want {
		t.Fatalf("problem code=%v want=%q body=%v", body["code"], want, body)
	}
}

func assertClearedSessionCookies(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	cookies := rr.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cleared cookies=%d want=2: %+v", len(cookies), cookies)
	}
	wantPaths := map[string]string{accessCookieName: "/", refreshCookieName: "/api/v1/admin/auth"}
	for _, cookie := range cookies {
		wantPath, ok := wantPaths[cookie.Name]
		if !ok {
			t.Fatalf("unexpected cleared cookie: %+v", cookie)
		}
		if cookie.Value != "" || cookie.Path != wantPath || cookie.MaxAge != -1 || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || !cookie.Expires.Before(time.Now()) {
			t.Fatalf("invalid cleared cookie: %+v", cookie)
		}
		delete(wantPaths, cookie.Name)
	}
	if len(wantPaths) != 0 {
		t.Fatalf("missing cleared cookies: %v", wantPaths)
	}
}
