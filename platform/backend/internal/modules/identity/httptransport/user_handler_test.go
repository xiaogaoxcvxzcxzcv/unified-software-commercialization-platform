package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/platform/requestid"
)

type userServiceStub struct {
	calls    []string
	last     any
	err      error
	issued   IssuedUserSession
	current  CurrentUserSession
	access   ProductUserAccessDecision
	recovery RecoveryChallengeResponse
	profile  UserProfileResponse
	sessions []UserSessionSummary
	tokens   TokenPair
}

func (s *userServiceStub) called(name string, value any) error {
	s.calls = append(s.calls, name)
	s.last = value
	return s.err
}
func (s *userServiceStub) RegisterUser(_ context.Context, _ ClientSessionContext, c RegisterUserCommand) (IssuedUserSession, error) {
	return s.issued, s.called("register", c)
}
func (s *userServiceStub) LoginUser(_ context.Context, _ ClientSessionContext, c LoginUserCommand) (IssuedUserSession, error) {
	return s.issued, s.called("login", c)
}
func (s *userServiceStub) CurrentUserSession(_ context.Context, c UserSessionContext) (CurrentUserSession, error) {
	return s.current, s.called("session", c)
}
func (s *userServiceStub) GetCurrentUserAccess(_ context.Context, c UserSessionContext) (ProductUserAccessDecision, error) {
	return s.access, s.called("access", c)
}
func (s *userServiceStub) StartPasswordRecovery(_ context.Context, _ ClientSessionContext, c StartPasswordRecoveryCommand) (RecoveryChallengeResponse, error) {
	return s.recovery, s.called("recovery-start", c)
}
func (s *userServiceStub) CompletePasswordRecovery(_ context.Context, _ ClientSessionContext, c CompletePasswordRecoveryCommand) error {
	return s.called("recovery-complete", c)
}
func (s *userServiceStub) GetCurrentUserProfile(_ context.Context, c UserSessionContext) (UserProfileResponse, error) {
	return s.profile, s.called("profile-get", c)
}
func (s *userServiceStub) UpdateCurrentUserProfile(_ context.Context, _ UserSessionContext, c UpdateUserProfileCommand) (UserProfileResponse, error) {
	return s.profile, s.called("profile-patch", c)
}
func (s *userServiceStub) ChangeCurrentUserPassword(_ context.Context, _ UserSessionContext, c ChangePasswordCommand) error {
	return s.called("password", c)
}
func (s *userServiceStub) ListCurrentUserSessions(_ context.Context, c UserSessionContext) ([]UserSessionSummary, error) {
	return s.sessions, s.called("sessions", c)
}
func (s *userServiceStub) RevokeCurrentUserSession(_ context.Context, _ UserSessionContext, id string) error {
	return s.called("session-delete", id)
}
func (s *userServiceStub) RefreshUserSession(_ context.Context, c RefreshUserSessionCommand) (TokenPair, error) {
	return s.tokens, s.called("refresh", c)
}
func (s *userServiceStub) LogoutUser(_ context.Context, c UserSessionContext) error {
	return s.called("logout", c)
}

type userResolverStub struct {
	clientCalls, userCalls, logoutCalls                   int
	clientToken, userToken, logoutToken                   string
	clientErr, userErr, logoutErr                         error
	userAccessToken, logoutAccessToken                    string
	clientEnvironment, userEnvironment, logoutEnvironment string
}

func (r *userResolverStub) ResolveClientSession(_ context.Context, token string) (ClientSessionContext, error) {
	r.clientCalls++
	r.clientToken = token
	environment := r.clientEnvironment
	if environment == "" {
		environment = "test"
	}
	return ClientSessionContext{SessionID: "client-session-a", ProductID: "product-a", ApplicationID: "application-a", TenantID: "tenant-a", Environment: environment}, r.clientErr
}
func (r *userResolverStub) ResolveUserSession(_ context.Context, token string) (UserSessionContext, error) {
	r.userCalls++
	r.userToken = token
	environment := r.userEnvironment
	if environment == "" {
		environment = "test"
	}
	return UserSessionContext{UserID: "user-a", SessionID: "session-current", ProductID: "product-a", ApplicationID: "application-a", TenantID: "tenant-a", Environment: environment, AccountStatus: "active", AccessToken: r.userAccessToken}, r.userErr
}
func (r *userResolverStub) ResolveLogoutSession(_ context.Context, token string) (UserSessionContext, error) {
	r.logoutCalls++
	r.logoutToken = token
	environment := r.logoutEnvironment
	if environment == "" {
		environment = "test"
	}
	return UserSessionContext{UserID: "user-a", SessionID: "session-current", ProductID: "product-a", ApplicationID: "application-a", TenantID: "tenant-a", Environment: environment, AccountStatus: "active", AccessToken: r.logoutAccessToken}, r.logoutErr
}

func TestUserHandlerRejectsInvalidResolvedEnvironment(t *testing.T) {
	tests := []struct {
		name, method, path, body, auth string
		configure                      func(*userResolverStub)
	}{
		{name: "client", method: http.MethodPost, path: "/api/v1/auth/login", body: `{"identifier":"user@example.com","credential":"password"}`, auth: "client", configure: func(r *userResolverStub) { r.clientEnvironment = "staging" }},
		{name: "user", method: http.MethodGet, path: "/api/v1/auth/session", auth: "user", configure: func(r *userResolverStub) { r.userEnvironment = "staging" }},
		{name: "logout", method: http.MethodPost, path: "/api/v1/auth/logout", auth: "user", configure: func(r *userResolverStub) { r.logoutEnvironment = "staging" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &userServiceStub{}
			resolver := &userResolverStub{}
			test.configure(resolver)
			response := serveUser(NewUserHandler(service, resolver), test.method, test.path, test.body, test.auth, "")
			if response.Code != http.StatusUnauthorized || len(service.calls) != 0 {
				t.Fatalf("status=%d calls=%v body=%s", response.Code, service.calls, response.Body.String())
			}
		})
	}
}

func TestUserHandlerImplementsFrozenRouteSurface(t *testing.T) {
	now := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	display := "User"
	accessReason := "PRODUCT_USER_ACCESS_SUSPENDED"
	service := &userServiceStub{
		issued:   IssuedUserSession{TokenPair: TokenPair{AccessToken: "issued-access-token-000000000000", RefreshToken: "issued-refresh-token-00000000000", AccessExpiresAt: now.Add(time.Minute), RefreshExpiresAt: now.Add(time.Hour)}, User: UserSummary{UserID: "user-a", AccountStatus: "active", DisplayName: &display}},
		current:  CurrentUserSession{SessionID: "session-current", User: UserSummary{UserID: "user-a", AccountStatus: "active"}, AccessExpiresAt: now.Add(time.Minute), RefreshExpiresAt: now.Add(time.Hour)},
		access:   ProductUserAccessDecision{Allowed: false, DecisionStage: "product", ReasonCode: &accessReason},
		recovery: RecoveryChallengeResponse{ContinuationID: "continuation-123456"}, profile: UserProfileResponse{UserID: "user-a", Version: 1, DisplayName: &display},
		sessions: []UserSessionSummary{{SessionID: "session-current", Current: true, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}},
		tokens:   TokenPair{AccessToken: "rotated-access-token-00000000000", RefreshToken: "rotated-refresh-token-000000000", AccessExpiresAt: now.Add(time.Minute), RefreshExpiresAt: now.Add(time.Hour)},
	}
	resolver := &userResolverStub{}
	handler := NewUserHandler(service, resolver)
	tests := []struct {
		name, method, path, body, auth, idem, call string
		status                                     int
	}{
		{"register", http.MethodPost, "/api/v1/auth/register", `{"identifier":"user@example.com","credential":"password-1234","verification_continuation_id":"verification-continuation-1234","verification_proof":"verification-1234"}`, "client", "idempotency-key-0001", "register", 201},
		{"login", http.MethodPost, "/api/v1/auth/login", `{"identifier":"user@example.com","credential":"password"}`, "client", "", "login", 200},
		{"session", http.MethodGet, "/api/v1/auth/session", "", "user", "", "session", 200},
		{"access", http.MethodGet, "/api/v1/account/access", "", "user", "", "access", 200},
		{"recovery start", http.MethodPost, "/api/v1/auth/recovery/start", `{"identifier":"user@example.com"}`, "client", "idempotency-key-0002", "recovery-start", 202},
		{"recovery complete", http.MethodPost, "/api/v1/auth/recovery/complete", `{"continuation_id":"continuation-123456","recovery_proof":"recovery-proof-123","new_credential":"new-password-123"}`, "client", "idempotency-key-0003", "recovery-complete", 204},
		{"profile get", http.MethodGet, "/api/v1/account/profile", "", "user", "", "profile-get", 200},
		{"profile patch", http.MethodPatch, "/api/v1/account/profile", `{"expected_version":1,"display_name":"Updated User"}`, "user", "idempotency-key-0004", "profile-patch", 200},
		{"password", http.MethodPut, "/api/v1/account/password", `{"current_credential":"password","new_credential":"new-password-123","revoke_other_sessions":true}`, "user", "idempotency-key-0005", "password", 204},
		{"sessions", http.MethodGet, "/api/v1/account/sessions", "", "user", "", "sessions", 200},
		{"session delete", http.MethodDelete, "/api/v1/account/sessions/session-123", "", "user", "", "session-delete", 204},
		{"refresh", http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"refresh-token-123456","client_request_id":"client-request-1234"}`, "", "", "refresh", 200},
		{"logout", http.MethodPost, "/api/v1/auth/logout", "", "user", "", "logout", 204},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := len(service.calls)
			response := serveUser(handler, test.method, test.path, test.body, test.auth, test.idem)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Pragma") != "no-cache" {
				t.Fatalf("cache headers=%v", response.Header())
			}
			if response.Header().Get("X-Request-ID") != "request-id-1234" {
				t.Fatalf("request id=%q", response.Header().Get("X-Request-ID"))
			}
			if len(service.calls) != before+1 || service.calls[len(service.calls)-1] != test.call {
				t.Fatalf("calls=%v", service.calls)
			}
			if test.call == "profile-patch" {
				command := service.last.(UpdateUserProfileCommand)
				if !command.DisplayName.Set || command.DisplayName.Value == nil || *command.DisplayName.Value != "Updated User" {
					t.Fatalf("optional null=%+v", command.DisplayName)
				}
			}
		})
	}
	if resolver.clientCalls != 4 || resolver.userCalls != 7 || resolver.logoutCalls != 1 || resolver.clientToken != "client-token-123456" || resolver.userToken != "user-token-1234567" || resolver.logoutToken != "user-token-1234567" {
		t.Fatalf("resolver calls client/user/logout=%d/%d/%d tokens=%q/%q/%q", resolver.clientCalls, resolver.userCalls, resolver.logoutCalls, resolver.clientToken, resolver.userToken, resolver.logoutToken)
	}
}

func TestUserHandlerAccessProjectionAndEncodedAliases(t *testing.T) {
	service := &userServiceStub{access: ProductUserAccessDecision{Allowed: true, DecisionStage: "allowed", ReasonCode: nil}}
	resolver := &userResolverStub{}
	handler := NewUserHandler(service, resolver)

	response := serveUser(handler, http.MethodGet, "/api/v1/account/access", "", "user", "")
	if response.Code != http.StatusOK || response.Body.String() != `{"allowed":true,"decision_stage":"allowed","reason_code":null}`+"\n" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	service.access = ProductUserAccessDecision{Allowed: true, DecisionStage: "product", ReasonCode: nil}
	response = serveUser(handler, http.MethodGet, "/api/v1/account/access", "", "user", "")
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"identity.internal_error"`) {
		t.Fatalf("invalid projection status=%d body=%s", response.Code, response.Body.String())
	}

	beforeCalls, beforeResolvers := len(service.calls), resolver.userCalls
	request := newUserRequest(http.MethodGet, "/api/v1/account/access", "", "user", "")
	request.URL.RawPath = "/api/v1/account/%61ccess"
	recorder := httptest.NewRecorder()
	requestid.Middleware(handler).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"code":"route_not_found"`) {
		t.Fatalf("encoded alias status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(service.calls) != beforeCalls || resolver.userCalls != beforeResolvers {
		t.Fatal("encoded route alias reached resolver or service")
	}
}

func TestUserHandlerStrictBearerAndContextSeparation(t *testing.T) {
	service := &userServiceStub{current: CurrentUserSession{SessionID: "session-current", User: UserSummary{UserID: "user-a", AccountStatus: "active"}}}
	resolver := &userResolverStub{}
	handler := NewUserHandler(service, resolver)
	tests := []struct {
		name, path, auth string
		duplicate        bool
	}{
		{"missing", "/api/v1/auth/session", "", false}, {"basic", "/api/v1/auth/session", "Basic abcdefghijklmnop", false}, {"double space", "/api/v1/auth/session", "Bearer  abcdefghijklmnop", false}, {"embedded comma", "/api/v1/auth/session", "Bearer abcdefghijkl,mnop", false}, {"duplicate", "/api/v1/auth/session", "Bearer abcdefghijklmnop", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.auth != "" {
				request.Header.Add("Authorization", test.auth)
			}
			if test.duplicate {
				request.Header.Add("Authorization", "Bearer second-token-1234")
			}
			response := httptest.NewRecorder()
			requestid.Middleware(handler).ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"identity.unauthorized"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "abcdefghijklmnop") {
				t.Fatal("bearer leaked")
			}
		})
	}
	resolver.clientErr = ErrInvalidBearer
	response := serveUser(handler, http.MethodPost, "/api/v1/auth/login", `{"identifier":"user@example.com","credential":"password"}`, "client", " ")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("client resolver status=%d body=%s", response.Code, response.Body.String())
	}
	if resolver.userCalls != 0 {
		t.Fatalf("client route used user resolver: %d", resolver.userCalls)
	}
}

func TestUserHandlerBindsRequestBearerForApplicationWithoutSerialization(t *testing.T) {
	const injected = "resolver-injected-token-must-not-win"
	const requestToken = "request-access-token-123456"
	service := &userServiceStub{current: CurrentUserSession{SessionID: "session-current", User: UserSummary{UserID: "user-a", AccountStatus: "active"}}}
	resolver := &userResolverStub{userAccessToken: injected, logoutAccessToken: injected}
	handler := NewUserHandler(service, resolver)

	response := serveUser(handler, http.MethodGet, "/api/v1/auth/session", "", "Bearer "+requestToken, "")
	if response.Code != http.StatusOK {
		t.Fatalf("session status=%d", response.Code)
	}
	contextValue, ok := service.last.(UserSessionContext)
	if !ok || contextValue.AccessToken != requestToken {
		t.Fatal("application context did not use the request bearer")
	}
	encoded, err := json.Marshal(contextValue)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(requestToken)) || bytes.Contains(encoded, []byte(injected)) || strings.Contains(response.Body.String(), requestToken) || strings.Contains(response.Body.String(), injected) {
		t.Fatal("access credential appeared in serialized data")
	}

	response = serveUser(handler, http.MethodPost, "/api/v1/auth/logout", "", "Bearer "+requestToken, "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d", response.Code)
	}
	contextValue, ok = service.last.(UserSessionContext)
	if !ok || contextValue.AccessToken != requestToken {
		t.Fatal("logout context did not use the request bearer")
	}
}

func TestUserHandlerLoginUsesOnlyRemoteAddressHostAsRateLimitSource(t *testing.T) {
	tests := []struct {
		name, remoteAddr, want string
	}{
		{name: "ipv4", remoteAddr: "198.51.100.8:43120", want: "198.51.100.8"},
		{name: "ipv6", remoteAddr: "[2001:db8::8]:443", want: "2001:db8::8"},
		{name: "malformed", remoteAddr: "attacker-controlled-value", want: "unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &userServiceStub{}
			handler := NewUserHandler(service, &userResolverStub{})
			request := newUserRequest(http.MethodPost, "/api/v1/auth/login", `{"identifier":"user@example.com","credential":"password"}`, "client", "")
			request.RemoteAddr = test.remoteAddr
			request.Header.Set("X-Forwarded-For", "203.0.113.99")
			response := httptest.NewRecorder()
			requestid.Middleware(handler).ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			command, ok := service.last.(LoginUserCommand)
			if !ok || command.Source != test.want {
				t.Fatalf("command=%+v want source=%q", command, test.want)
			}
			if command.Source == "203.0.113.99" || strings.Contains(response.Body.String(), test.remoteAddr) || strings.Contains(response.Body.String(), "203.0.113.99") {
				t.Fatalf("trusted forwarded or leaked source: command=%+v body=%s", command, response.Body.String())
			}
		})
	}
}

func TestUserHandlerRejectsUnexpectedRangeAndMalformedBodies(t *testing.T) {
	service := &userServiceStub{}
	handler := NewUserHandler(service, &userResolverStub{})
	validRegister := `{"identifier":"user@example.com","credential":"password-1234","verification_proof":"verification-1234"}`
	tests := []struct{ name, method, path, body, auth, idem, contentType string }{
		{"query scope", http.MethodPost, "/api/v1/auth/register?product_id=forged", validRegister, "client", "idempotency-key-0001", "application/json"},
		{"unknown scope field", http.MethodPost, "/api/v1/auth/register", `{"identifier":"user@example.com","credential":"password-1234","verification_proof":"verification-1234","product_id":"forged"}`, "client", "idempotency-key-0001", "application/json"},
		{"duplicate json field", http.MethodPost, "/api/v1/auth/register", `{"identifier":"first@example.com","identifier":"second@example.com","credential":"password-1234","verification_proof":"verification-1234"}`, "client", "idempotency-key-0001", "application/json"},
		{"trailing json", http.MethodPost, "/api/v1/auth/register", validRegister + ` {}`, "client", "idempotency-key-0001", "application/json"},
		{"missing idempotency", http.MethodPost, "/api/v1/auth/register", validRegister, "client", "", "application/json"},
		{"short idempotency", http.MethodPost, "/api/v1/auth/register", validRegister, "client", "short", "application/json"},
		{"wrong content type", http.MethodPost, "/api/v1/auth/register", validRegister, "client", "idempotency-key-0001", "text/plain"},
		{"get body", http.MethodGet, "/api/v1/auth/session", `{"scope":"forged"}`, "user", "", "application/json"},
		{"nested session path", http.MethodDelete, "/api/v1/account/sessions/a/b", "", "user", "", "application/json"},
		{"profile no mutation", http.MethodPatch, "/api/v1/account/profile", `{"expected_version":1}`, "user", "idempotency-key-0001", "application/json"},
		{"profile null display name", http.MethodPatch, "/api/v1/account/profile", `{"expected_version":1,"display_name":null}`, "user", "idempotency-key-0001", "application/json"},
		{"profile unsafe avatar", http.MethodPatch, "/api/v1/account/profile", `{"expected_version":1,"avatar_url":"javascript:alert(1)"}`, "user", "idempotency-key-0001", "application/json"},
		{"refresh short retry key", http.MethodPost, "/api/v1/auth/refresh", `{"refresh_token":"refresh-token-123456","client_request_id":"short"}`, "", "", "application/json"},
		{"oversize", http.MethodPost, "/api/v1/auth/login", `{"identifier":"` + strings.Repeat("a", int(userRequestBodyLimit)) + `","credential":"password"}`, "client", "", "application/json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := newUserRequest(test.method, test.path, test.body, test.auth, test.idem)
			if test.body != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			requestid.Middleware(handler).ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("error response is cacheable")
			}
		})
	}
	if len(service.calls) != 0 {
		t.Fatalf("invalid requests reached service: %v", service.calls)
	}
}

func TestUserHandlerUsesStableErrorsWithoutSecretLeakage(t *testing.T) {
	service := &userServiceStub{err: fmt.Errorf("database failure password=secret refresh_token=top-secret")}
	handler := NewUserHandler(service, &userResolverStub{})
	response := serveUser(handler, http.MethodPost, "/api/v1/auth/login", `{"identifier":"person@example.com","credential":"credential-secret"}`, "client", "")
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"identity.internal_error"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for _, secret := range []string{"secret", "top-secret", "person@example.com", "credential-secret", "password="} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("response leaked %q: %s", secret, response.Body.String())
		}
	}
	service.err = ErrInvalidUserCredentials
	response = serveUser(handler, http.MethodPost, "/api/v1/auth/login", `{"identifier":"missing@example.com","credential":"wrong-password"}`, "client", "")
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"IDENTITY_INVALID_CREDENTIALS"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "missing@example.com") || strings.Contains(response.Body.String(), "wrong-password") {
		t.Fatal("credential error leaked input")
	}

	malformed := bytes.NewBufferString(`{"current_credential":"visible-secret"`)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/account/password", malformed)
	request.Header.Set("Authorization", "Bearer user-token-1234567")
	request.Header.Set("Idempotency-Key", "idempotency-key-0001")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	requestid.Middleware(handler).ServeHTTP(recorder, request)
	if strings.Contains(recorder.Body.String(), "visible-secret") {
		t.Fatal("parser error leaked password")
	}
}

func TestUserHandlerLogoutIsIdempotentWithoutRelaxingUserBearer(t *testing.T) {
	service := &userServiceStub{}
	resolver := &userResolverStub{}
	handler := NewUserHandler(service, resolver)

	response := serveUser(handler, http.MethodPost, "/api/v1/auth/logout", "", "user", "")
	if response.Code != http.StatusNoContent || resolver.logoutCalls != 1 || service.calls[len(service.calls)-1] != "logout" {
		t.Fatalf("first logout status=%d resolver=%d calls=%v", response.Code, resolver.logoutCalls, service.calls)
	}

	before := len(service.calls)
	resolver.logoutErr = ErrUserSessionRevoked
	response = serveUser(handler, http.MethodPost, "/api/v1/auth/logout", "", "user", "")
	if response.Code != http.StatusNoContent || len(service.calls) != before {
		t.Fatalf("repeated logout status=%d calls=%v", response.Code, service.calls)
	}

	resolver.logoutErr = ErrInvalidBearer
	response = serveUser(handler, http.MethodPost, "/api/v1/auth/logout", "", "user", "")
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"identity.unauthorized"`) {
		t.Fatalf("invalid logout proof status=%d body=%s", response.Code, response.Body.String())
	}

	resolver.logoutErr = nil
	service.err = ErrUserSessionRevoked
	response = serveUser(handler, http.MethodPost, "/api/v1/auth/logout", "", "user", "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("racing revoked logout status=%d body=%s", response.Code, response.Body.String())
	}

	service.err = nil
	resolver.userErr = ErrUserSessionRevoked
	response = serveUser(handler, http.MethodGet, "/api/v1/auth/session", "", "user", "")
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"IDENTITY_SESSION_REVOKED"`) {
		t.Fatalf("protected revoked proof status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestUserHandlerStableSecurityErrorMappings(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		status     int
		code       string
		retryable  bool
		retryAfter int
	}{
		{name: "rate limit with retry", err: &UserRateLimitError{RetryAfter: 90*time.Second + time.Millisecond}, status: http.StatusTooManyRequests, code: "identity.rate_limited", retryable: true, retryAfter: 91},
		{name: "rate limit without retry", err: ErrUserRateLimited, status: http.StatusTooManyRequests, code: "identity.rate_limited", retryable: true},
		{name: "invalid user request", err: ErrInvalidUserRequest, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "provider unavailable", err: ErrUserProviderUnavailable, status: http.StatusServiceUnavailable, code: "identity.provider_unavailable", retryable: true},
		{name: "product access suspended", err: ErrProductUserAccessSuspended, status: http.StatusForbidden, code: "PRODUCT_USER_ACCESS_SUSPENDED"},
		{name: "tenant access suspended", err: ErrTenantUserAccessSuspended, status: http.StatusForbidden, code: "TENANT_USER_ACCESS_SUSPENDED"},
		{name: "additional verification", err: ErrAdditionalVerificationRequired, status: http.StatusForbidden, code: "identity.additional_verification_required"},
		{name: "recovery expired", err: ErrRecoveryExpired, status: http.StatusGone, code: "identity.recovery_expired"},
		{name: "recovery invalid", err: ErrRecoveryInvalid, status: http.StatusBadRequest, code: "identity.recovery_invalid"},
		{name: "recovery replayed", err: ErrRecoveryReplayed, status: http.StatusConflict, code: "identity.recovery_replayed"},
		{name: "unknown", err: fmt.Errorf("database secret=hidden"), status: http.StatusInternalServerError, code: "identity.internal_error", retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := &UserHandler{}
			request := httptest.NewRequest(http.MethodPost, "/error", nil)
			response := httptest.NewRecorder()
			requestid.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handler.writeUserError(w, r, test.err)
			})).ServeHTTP(response, request)
			var body struct {
				Code              string `json:"code"`
				Retryable         bool   `json:"retryable"`
				RetryAfterSeconds *int   `json:"retry_after_seconds"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.status || body.Code != test.code || body.Retryable != test.retryable {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Pragma") != "no-cache" {
				t.Fatalf("error response is cacheable: headers=%v", response.Header())
			}
			if test.retryAfter > 0 {
				if body.RetryAfterSeconds == nil || *body.RetryAfterSeconds != test.retryAfter || response.Header().Get("Retry-After") != fmt.Sprint(test.retryAfter) {
					t.Fatalf("retry mapping headers=%v body=%s", response.Header(), response.Body.String())
				}
			} else if body.RetryAfterSeconds != nil || response.Header().Get("Retry-After") != "" {
				t.Fatalf("unexpected retry delay headers=%v body=%s", response.Header(), response.Body.String())
			}
			if strings.Contains(response.Body.String(), "hidden") {
				t.Fatal("unknown error leaked detail")
			}
		})
	}
}

func TestUserHandlerMethodAndEmptySessionListAreStable(t *testing.T) {
	service := &userServiceStub{sessions: nil}
	resolver := &userResolverStub{}
	handler := NewUserHandler(service, resolver)
	response := serveUser(handler, http.MethodPost, "/api/v1/auth/session", "", "user", "")
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("status=%d allow=%q", response.Code, response.Header().Get("Allow"))
	}
	response = serveUser(handler, http.MethodGet, "/api/v1/account/sessions", "", "user", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"items":[]`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	before := resolver.userCalls
	response = serveUser(handler, http.MethodPost, "/api/v1/account/profile", "", "", "")
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, PATCH" {
		t.Fatalf("profile status=%d allow=%q", response.Code, response.Header().Get("Allow"))
	}
	if resolver.userCalls != before {
		t.Fatal("unsupported profile method resolved authentication")
	}
}

func serveUser(handler http.Handler, method, path, body, auth, idem string) *httptest.ResponseRecorder {
	request := newUserRequest(method, path, body, auth, idem)
	response := httptest.NewRecorder()
	requestid.Middleware(handler).ServeHTTP(response, request)
	return response
}
func newUserRequest(method, path, body, auth, idem string) *http.Request {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	switch auth {
	case "client":
		request.Header.Set("Authorization", "Bearer client-token-123456")
	case "user":
		request.Header.Set("Authorization", "Bearer user-token-1234567")
	case "":
	default:
		request.Header.Set("Authorization", auth)
	}
	if idem != "" && idem != " " {
		request.Header.Set("Idempotency-Key", idem)
	}
	request.Header.Set("X-Request-ID", "request-id-1234")
	return request
}

var _ UserService = (*userServiceStub)(nil)
var _ UserSessionResolver = (*userResolverStub)(nil)
