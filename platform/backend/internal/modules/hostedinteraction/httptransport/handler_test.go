package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testInteractionID = "hint_abcdefghijklmnopqrstuvwxyz"

type authStub struct{}

func (authStub) ResolveBearer(_ context.Context, token string) (BearerPrincipal, error) {
	scope := Scope{ProductID: "product-a", ApplicationID: "application-a", Environment: "test", Channel: "web"}
	switch token {
	case "client-token-000000000000000000000000":
		return BearerPrincipal{Kind: "client", Scope: scope, SessionID: "client-session"}, nil
	case "user-token-00000000000000000000000000":
		return BearerPrincipal{Kind: "user", Scope: scope, SessionID: "user-session", UserID: "user-a", AuthTime: time.Now()}, nil
	case "client-no-session-000000000000000000000":
		return BearerPrincipal{Kind: "client", Scope: scope}, nil
	case "client-database-error-000000000000000000":
		return BearerPrincipal{}, errors.New("database unavailable")
	default:
		return BearerPrincipal{}, ErrAuthenticationRequired
	}
}

func (authStub) ResolveHostedSession(_ context.Context, interactionID, token string) (HostedPrincipal, error) {
	if token == "hosted-database-error-000000000000000000000000000" {
		return HostedPrincipal{}, errors.New("database unavailable")
	}
	if interactionID != testInteractionID || token != "hosted-cookie-0000000000000000000000000000000000" {
		return HostedPrincipal{}, ErrSessionRevoked
	}
	return HostedPrincipal{InteractionID: interactionID, BrowserSessionID: "browser-session-a", BrowserToken: token, CSRFToken: "csrf-token-0000000000000000000000000000"}, nil
}

type serviceStub struct {
	createPrincipal BearerPrincipal
	createCommand   CreateCommand
	passwordCommand PasswordCommand
	accountCommand  CompleteAccountCommand
	exchangeCommand ExchangeCommand
	getAccess       AccessPrincipal
	openCookieToken string
	launchValue     *InteractionLaunch
	projectionValue *Interaction
	browserValue    *BrowserSession
	completionValue *Completion
	exchangeValue   *ExchangeResult
	error           error
}

func projection() Interaction {
	return Interaction{InteractionID: testInteractionID, RouteID: "hosted.auth", Channel: "web", Status: "opened", AllowedActions: []string{"authenticate", "complete", "cancel"}, CreatedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour)}
}

func (s *serviceStub) Create(_ context.Context, p BearerPrincipal, c CreateCommand) (InteractionLaunch, error) {
	s.createPrincipal, s.createCommand = p, c
	if s.launchValue != nil {
		return *s.launchValue, s.error
	}
	return InteractionLaunch{InteractionID: testInteractionID, InteractionURL: "https://hosted.example/ui/v1/auth?interaction_id=" + testInteractionID, RouteID: c.RouteID, Status: "created", ExpiresAt: time.Now().Add(time.Hour)}, s.error
}
func (s *serviceStub) Get(_ context.Context, a AccessPrincipal, _ string) (Interaction, error) {
	s.getAccess = a
	if s.projectionValue != nil {
		return *s.projectionValue, s.error
	}
	return projection(), s.error
}
func (s *serviceStub) OpenBrowserSession(context.Context, string) (BrowserSession, error) {
	if s.browserValue != nil {
		return *s.browserValue, s.error
	}
	token := s.openCookieToken
	if token == "" {
		token = "hosted-cookie-0000000000000000000000000000000000"
	}
	return BrowserSession{Interaction: projection(), CSRFToken: "csrf-token-0000000000000000000000000000", BrowserSessionExpiresAt: time.Now().Add(time.Hour), CookieToken: token}, s.error
}
func (s *serviceStub) AuthenticatePassword(_ context.Context, _ HostedPrincipal, _ string, c PasswordCommand) (Completion, error) {
	s.passwordCommand = c
	if s.completionValue != nil {
		return *s.completionValue, s.error
	}
	return completion(), s.error
}
func (s *serviceStub) CompleteAccount(_ context.Context, _ HostedPrincipal, _ string, c CompleteAccountCommand) (Completion, error) {
	s.accountCommand = c
	if s.completionValue != nil {
		return *s.completionValue, s.error
	}
	return completion(), s.error
}
func (s *serviceStub) Cancel(context.Context, HostedPrincipal, string, string) (Interaction, error) {
	return projection(), s.error
}
func (s *serviceStub) Exchange(_ context.Context, _ BearerPrincipal, _ string, c ExchangeCommand) (ExchangeResult, error) {
	s.exchangeCommand = c
	if s.exchangeValue != nil {
		return *s.exchangeValue, s.error
	}
	return ExchangeResult{InteractionID: testInteractionID, ResultType: "account_completed", AccountResult: &AccountResult{Result: "closed"}}, s.error
}
func completion() Completion {
	return Completion{InteractionID: testInteractionID, Status: "completed", ReturnURL: "https://app.example/callback?code=code-000000000000000000000000000&state=state-00000000000000000&interaction_id=" + testInteractionID, ExpiresAt: time.Now().Add(time.Minute)}
}

func newTestHandler(t *testing.T, service *serviceStub) *Handler {
	t.Helper()
	h, err := New(service, authStub{}, "https://hosted.example")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func request(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func client(r *http.Request) {
	r.Header.Set("Authorization", "Bearer client-token-000000000000000000000000")
}
func user(r *http.Request) {
	r.Header.Set("Authorization", "Bearer user-token-00000000000000000000000000")
}
func hosted(r *http.Request) {
	r.AddCookie(&http.Cookie{Name: HostedSessionCookieName, Value: "hosted-cookie-0000000000000000000000000000000000"})
	r.Header.Set("Origin", "https://hosted.example")
	r.Header.Set("X-CSRF-Token", "csrf-token-0000000000000000000000000000")
}

func TestCreateUsesRouteSpecificPrincipalAndRejectsUntrustedFields(t *testing.T) {
	s := &serviceStub{}
	h := newTestHandler(t, s)
	body := `{"route_id":"hosted.auth","channel":"web","return_target_code":"login.complete","state":"state-00000000000000000","nonce":"nonce-00000000000000000","code_challenge":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","code_challenge_method":"S256"}`
	r := request(http.MethodPost, "/api/v1/hosted/interactions", body)
	client(r)
	r.Header.Set("Idempotency-Key", "create-key-00000001")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusCreated || s.createPrincipal.Kind != "client" || s.createCommand.RouteID != "hosted.auth" || s.createCommand.IdempotencyKey == "" {
		t.Fatalf("status=%d principal=%+v command=%+v body=%s", w.Code, s.createPrincipal, s.createCommand, w.Body.String())
	}
	assertSecurityHeaders(t, w)

	r = request(http.MethodPost, "/api/v1/hosted/interactions", body)
	user(r)
	r.Header.Set("Idempotency-Key", "create-key-00000001")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusUnauthorized, "hosted.authentication_required")

	r = request(http.MethodPost, "/api/v1/hosted/interactions", strings.Replace(body, `"channel":"web"`, `"channel":"h5"`, 1))
	client(r)
	r.Header.Set("Idempotency-Key", "create-key-00000001")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusBadRequest, "hosted.channel_not_supported")

	r = request(http.MethodPost, "/api/v1/hosted/interactions", body)
	client(r)
	r.AddCookie(&http.Cookie{Name: HostedSessionCookieName, Value: "hosted-cookie-0000000000000000000000000000000000"})
	r.Header.Set("Idempotency-Key", "create-key-00000001")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusUnauthorized, "hosted.authentication_required")

	r = request(http.MethodPost, "/api/v1/hosted/interactions", body)
	r.Header.Set("Authorization", "Bearer client-no-session-000000000000000000000")
	r.Header.Set("Idempotency-Key", "create-key-00000001")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusUnauthorized, "hosted.authentication_required")

	for _, invalid := range []string{
		`{"route_id":"hosted.account","channel":"web","return_target_code":"account","state":"state-00000000000000000","product_id":"evil"}`,
		`{"route_id":"hosted.account","route_id":"hosted.auth","channel":"web","return_target_code":"account","state":"state-00000000000000000"}`,
		`{"route_id":"hosted.account","channel":"web","return_target_code":"account","state":"state-00000000000000000","nonce":""}`,
	} {
		r = request(http.MethodPost, "/api/v1/hosted/interactions", invalid)
		user(r)
		r.Header.Set("Idempotency-Key", "create-key-00000001")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assertProblem(t, w, http.StatusBadRequest, "invalid_request")
	}
}

func TestOpenRotatesSecureHostCookieAndEmitsSecurityHeaders(t *testing.T) {
	h := newTestHandler(t, &serviceStub{})
	r := request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/browser-session", "")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%v", cookies)
	}
	c := cookies[0]
	if c.Name != HostedSessionCookieName || !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie=%+v", c)
	}
	assertSecurityHeaders(t, w)

	r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/browser-session", "")
	client(r)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusUnauthorized, "hosted.authentication_required")

	h = newTestHandler(t, &serviceStub{openCookieToken: "invalid cookie token invalid cookie token"})
	r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/browser-session", "")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusServiceUnavailable, "hosted.temporarily_unavailable")
	if w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("unsafe cookie was set: %s", w.Header().Get("Set-Cookie"))
	}
}

func TestHostedWritesRequireExactCookieOriginAndCSRF(t *testing.T) {
	s := &serviceStub{}
	h := newTestHandler(t, s)
	body := `{"identifier":"person@example.com","credential":"correct-password","risk_summary":{"trusted":true,"score":1}}`
	r := request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/auth/password", body)
	hosted(r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || s.passwordCommand.Identifier != "person@example.com" {
		t.Fatalf("status=%d command=%+v body=%s", w.Code, s.passwordCommand, w.Body.String())
	}
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
		func(r *http.Request) { r.Header.Set("X-CSRF-Token", "csrf-token-0000000000000000000000000001") },
		func(r *http.Request) { r.Header.Del("Origin") },
		func(r *http.Request) { r.Header.Add("Origin", "https://hosted.example") },
		func(r *http.Request) { r.Header.Add("X-CSRF-Token", "csrf-token-0000000000000000000000000000") },
	} {
		r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/auth/password", body)
		hosted(r)
		mutate(r)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assertProblem(t, w, http.StatusForbidden, "hosted.csrf_failed")
	}
	r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/auth/password", body)
	hosted(r)
	r.AddCookie(&http.Cookie{Name: HostedSessionCookieName, Value: "hosted-cookie-duplicate-000000000000000000000000000"})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusUnauthorized, "hosted.authentication_required")

	r = request(http.MethodPost, "/api/v1/hosted/interactions/hint_ABCDEFGHIJKLMNOPQRSTUVWXYZ/auth/password", body)
	hosted(r)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusUnauthorized, "hosted.session_revoked")
}

func TestGetAccountCancelAndExchangeAuthenticationBoundaries(t *testing.T) {
	s := &serviceStub{}
	h := newTestHandler(t, s)
	r := request(http.MethodGet, "/api/v1/hosted/interactions/"+testInteractionID, "")
	client(r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || s.getAccess.Bearer == nil {
		t.Fatalf("status=%d access=%+v", w.Code, s.getAccess)
	}

	r = request(http.MethodGet, "/api/v1/hosted/interactions/"+testInteractionID, "")
	client(r)
	r.AddCookie(&http.Cookie{Name: HostedSessionCookieName, Value: "hosted-cookie-0000000000000000000000000000000000"})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusUnauthorized, "hosted.authentication_required")

	r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/account/complete", `{"result":"closed"}`)
	hosted(r)
	r.Header.Set("Idempotency-Key", "account-key-0000001")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || s.accountCommand.Result != "closed" {
		t.Fatalf("status=%d command=%+v", w.Code, s.accountCommand)
	}

	r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/cancel", "")
	hosted(r)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel=%d %s", w.Code, w.Body.String())
	}

	r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/exchange", `{"code":"code-000000000000000000000000000","code_verifier":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`)
	client(r)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || s.exchangeCommand.CodeVerifier == "" {
		t.Fatalf("exchange=%d command=%+v body=%s", w.Code, s.exchangeCommand, w.Body.String())
	}
	r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/exchange", `{"code":"code-000000000000000000000000000"}`)
	user(r)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusUnauthorized, "hosted.authentication_required")
	r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/exchange", `{"code":"code-000000000000000000000000000"}`)
	client(r)
	r.Header.Add("Authorization", "Bearer client-token-000000000000000000000000")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusUnauthorized, "hosted.authentication_required")
	r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/exchange", `{"code":"code-000000000000000000000000000","code_verifier":""}`)
	client(r)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusBadRequest, "invalid_request")
}

func TestAuthenticationDependencyErrorsRemainRetryable(t *testing.T) {
	h := newTestHandler(t, &serviceStub{})
	r := request(http.MethodGet, "/api/v1/hosted/interactions/"+testInteractionID, "")
	r.Header.Set("Authorization", "Bearer client-database-error-000000000000000000")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusServiceUnavailable, "hosted.temporarily_unavailable")

	r = request(http.MethodGet, "/api/v1/hosted/interactions/"+testInteractionID, "")
	r.AddCookie(&http.Cookie{Name: HostedSessionCookieName, Value: "hosted-database-error-000000000000000000000000000"})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusServiceUnavailable, "hosted.temporarily_unavailable")
}

func TestUnsafeServiceOutputsFailClosed(t *testing.T) {
	validLaunch := InteractionLaunch{InteractionID: testInteractionID, InteractionURL: "https://hosted.example/ui/v1/auth?interaction_id=" + testInteractionID, RouteID: "hosted.auth", Status: "created", ExpiresAt: time.Now().Add(time.Hour)}
	launches := []InteractionLaunch{
		{InteractionID: testInteractionID, InteractionURL: "https://evil.example/ui/v1/auth?interaction_id=" + testInteractionID, RouteID: "hosted.auth", Status: "created", ExpiresAt: time.Now().Add(time.Hour)},
		{InteractionID: testInteractionID, InteractionURL: validLaunch.InteractionURL + "&token=secret", RouteID: "hosted.auth", Status: "created", ExpiresAt: time.Now().Add(time.Hour)},
		{InteractionID: testInteractionID, InteractionURL: "https://user@hosted.example/ui/v1/auth?interaction_id=" + testInteractionID, RouteID: "hosted.auth", Status: "created", ExpiresAt: time.Now().Add(time.Hour)},
	}
	createBody := `{"route_id":"hosted.auth","channel":"web","return_target_code":"login.complete","state":"state-00000000000000000","nonce":"nonce-00000000000000000","code_challenge":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","code_challenge_method":"S256"}`
	for _, value := range launches {
		h := newTestHandler(t, &serviceStub{launchValue: &value})
		r := request(http.MethodPost, "/api/v1/hosted/interactions", createBody)
		client(r)
		r.Header.Set("Idempotency-Key", "create-key-00000001")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assertProblem(t, w, http.StatusServiceUnavailable, "hosted.temporarily_unavailable")
	}

	unsafeCompletion := completion()
	unsafeCompletion.ReturnURL += "&access_token=secret"
	h := newTestHandler(t, &serviceStub{completionValue: &unsafeCompletion})
	r := request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/auth/password", `{"identifier":"person@example.com","credential":"correct-password"}`)
	hosted(r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusServiceUnavailable, "hosted.temporarily_unavailable")

	badProjection := projection()
	badProjection.AllowedActions = []string{"exchange"}
	h = newTestHandler(t, &serviceStub{projectionValue: &badProjection})
	r = request(http.MethodGet, "/api/v1/hosted/interactions/"+testInteractionID, "")
	client(r)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusServiceUnavailable, "hosted.temporarily_unavailable")

	badExchange := ExchangeResult{InteractionID: testInteractionID, ResultType: "user_session", UserSession: &IssuedUserSession{AccessToken: "short", RefreshToken: "short"}, AccountResult: &AccountResult{Result: "closed"}}
	h = newTestHandler(t, &serviceStub{exchangeValue: &badExchange})
	r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/exchange", `{"code":"code-000000000000000000000000000"}`)
	client(r)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusServiceUnavailable, "hosted.temporarily_unavailable")

	expiredBrowser := BrowserSession{Interaction: projection(), CSRFToken: "csrf-token-0000000000000000000000000000", BrowserSessionExpiresAt: time.Now().Add(-time.Second), CookieToken: "hosted-cookie-0000000000000000000000000000000000"}
	h = newTestHandler(t, &serviceStub{browserValue: &expiredBrowser})
	r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/browser-session", "")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusServiceUnavailable, "hosted.temporarily_unavailable")
}

func TestStrictRouteQueryBodyContentTypeAndStableErrors(t *testing.T) {
	h := newTestHandler(t, &serviceStub{error: ErrInteractionExpired})
	for _, r := range []*http.Request{
		request(http.MethodGet, "/api/v1/hosted/interactions/"+testInteractionID+"?product_id=evil", ""),
		request(http.MethodGet, "/api/v1/hosted/interactions/"+testInteractionID+"/extra", ""),
	} {
		client(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assertProblem(t, w, http.StatusNotFound, "hosted.invalid_interaction")
	}
	r := request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/auth/password", `{"identifier":"person@example.com","credential":"correct-password"}`)
	hosted(r)
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusBadRequest, "invalid_request")
	r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/auth/password", `{"identifier":"person@example.com","credential":"correct-password","risk_summary":null}`)
	hosted(r)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusBadRequest, "invalid_request")

	for _, body := range []string{
		`{"route_id":"hosted.account","channel":"web","return_target_code":"account.complete","state":"state-00000000000000000","nonce":null}`,
		`{"route_id":"hosted.account","channel":"web","return_target_code":"account.complete","state":"state-00000000000000000","locale":null}`,
	} {
		r = request(http.MethodPost, "/api/v1/hosted/interactions", body)
		user(r)
		r.Header.Set("Idempotency-Key", "create-null-00000001")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		assertProblem(t, w, http.StatusBadRequest, "invalid_request")
	}
	r = request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/exchange", `{"code":"code-000000000000000000000000000","code_verifier":null}`)
	client(r)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusBadRequest, "invalid_request")

	r = request(http.MethodGet, "/api/v1/hosted/interactions/"+testInteractionID, "")
	client(r)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusGone, "hosted.interaction_expired")

	h = newTestHandler(t, &serviceStub{error: ErrConflict})
	r = request(http.MethodGet, "/api/v1/hosted/interactions/"+testInteractionID, "")
	client(r)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertProblem(t, w, http.StatusConflict, "hosted.idempotency_conflict")
}

func assertSecurityHeaders(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("security headers=%v", w.Header())
	}
}

func assertProblem(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != code {
		t.Fatalf("problem=%s error=%v want=%s", w.Body.String(), err, code)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json") {
		t.Fatalf("content-type=%s", w.Header().Get("Content-Type"))
	}
	assertSecurityHeaders(t, w)
}

var _ = errors.Is
