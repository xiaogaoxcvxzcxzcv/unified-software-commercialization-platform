package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type selfServiceStub struct {
	*serviceStub
	allowedActions      []string
	authCapabilities    *bool
	resetKey, revokeKey string
}

func (s *selfServiceStub) AuthBootstrap(context.Context, HostedPrincipal, string) (HostedAuthBootstrap, error) {
	enabled := true
	if s.authCapabilities != nil {
		enabled = *s.authCapabilities
	}
	return HostedAuthBootstrap{Interaction: projection(), Presentation: HostedPresentation{ProductName: "Product"}, Flow: HostedAuthFlow{Kind: "login"}, PasswordEnabled: enabled, RegistrationEnabled: enabled, RecoveryEnabled: enabled, ExternalProviders: []HostedExternalProvider{}}, s.error
}
func (s *selfServiceStub) StartRegistrationVerification(context.Context, HostedPrincipal, string, VerificationStartCommand) (HostedAuthFlow, error) {
	return HostedAuthFlow{Kind: "registration_verification"}, s.error
}
func (s *selfServiceStub) RegisterHosted(context.Context, HostedPrincipal, string, RegisterCommand) (Completion, error) {
	return completion(), s.error
}
func (s *selfServiceStub) StartRecovery(context.Context, HostedPrincipal, string, RecoveryStartCommand) (HostedAuthFlow, error) {
	return HostedAuthFlow{Kind: "recovery_verification"}, s.error
}
func (s *selfServiceStub) CompleteRecovery(context.Context, HostedPrincipal, string, RecoveryCompleteCommand) error {
	return s.error
}
func (s *selfServiceStub) AccountBootstrap(context.Context, HostedPrincipal, string) (HostedAccountBootstrap, error) {
	v := projection()
	v.RouteID = "hosted.account"
	actions := s.allowedActions
	if actions == nil {
		actions = []string{"update_profile", "change_password", "revoke_session", "complete"}
	}
	return HostedAccountBootstrap{Interaction: v, Presentation: HostedPresentation{ProductName: "Product"}, Profile: UserProfile{UserID: "user", Version: 1}, Sessions: []UserSessionSummary{}, ExternalIdentities: []ExternalIdentity{}, AllowedActions: actions}, s.error
}
func (s *selfServiceStub) PatchAccountProfile(context.Context, HostedPrincipal, string, ProfilePatchCommand) (UserProfile, error) {
	return UserProfile{UserID: "user", Version: 2}, s.error
}
func (s *selfServiceStub) ChangeAccountPassword(context.Context, HostedPrincipal, string, PasswordChangeCommand) error {
	return s.error
}
func (s *selfServiceStub) RevokeAccountSession(_ context.Context, _ HostedPrincipal, _, _, key, _ string) error {
	s.revokeKey = key
	return s.error
}
func (s *selfServiceStub) ResetAuthFlow(_ context.Context, _ HostedPrincipal, _, key string) error {
	s.resetKey = key
	return s.error
}

func newSelfServiceHandler(t *testing.T) *Handler {
	t.Helper()
	h, e := New(&selfServiceStub{serviceStub: &serviceStub{}}, authStub{}, "https://hosted.example")
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func selfServiceRequest(method, path, body string) *http.Request {
	r := request(method, "/api/v1/hosted/interactions/"+testInteractionID+path, body)
	hosted(r)
	return r
}

func TestHostedSelfServiceRoutesRequireCookieAndCSRF(t *testing.T) {
	h := newSelfServiceHandler(t)
	tests := []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/auth/bootstrap", "", http.StatusOK},
		{http.MethodPost, "/auth/verification/start", `{"identifier":"user@example.com"}`, http.StatusAccepted},
		{http.MethodPost, "/auth/register", `{"credential":"password-000000","verification_proof":"proof-00000000000","display_name":"User"}`, http.StatusOK},
		{http.MethodPost, "/auth/recovery/start", `{"identifier":"user@example.com"}`, http.StatusAccepted},
		{http.MethodPost, "/auth/recovery/complete", `{"recovery_proof":"proof-00000000000","new_credential":"password-000001"}`, http.StatusNoContent},
		{http.MethodDelete, "/auth/flow", "", http.StatusNoContent},
		{http.MethodGet, "/account/bootstrap", "", http.StatusOK},
		{http.MethodPatch, "/account/profile", `{"expected_version":1,"display_name":"Updated"}`, http.StatusOK},
		{http.MethodPost, "/account/password", `{"current_credential":"password-000000","new_credential":"password-000001","revoke_other_sessions":true}`, http.StatusNoContent},
		{http.MethodDelete, "/account/sessions/uses_abc", "", http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			r := selfServiceRequest(tt.method, tt.path, tt.body)
			if tt.method != http.MethodGet {
				r.Header.Set("Idempotency-Key", "idempotency-key-0001")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tt.status, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing no-store")
			}
			if strings.HasSuffix(tt.path, "/start") && strings.Contains(w.Body.String(), "continuation") {
				t.Fatalf("continuation leaked: %s", w.Body.String())
			}
			if tt.method != http.MethodGet {
				bad := selfServiceRequest(tt.method, tt.path, tt.body)
				bad.Header.Del("X-CSRF-Token")
				if tt.method != http.MethodDelete {
					bad.Header.Set("Idempotency-Key", "idempotency-key-0001")
				}
				bw := httptest.NewRecorder()
				h.ServeHTTP(bw, bad)
				if bw.Code != http.StatusForbidden {
					t.Fatalf("missing csrf status=%d", bw.Code)
				}
			}
		})
	}
}

func TestDeleteSelfServiceWritesRequireSingleIdempotencyKey(t *testing.T) {
	stub := &selfServiceStub{serviceStub: &serviceStub{}}
	h, e := New(stub, authStub{}, "https://hosted.example")
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/auth/flow", "/account/sessions/uses_abc"} {
		for _, values := range [][]string{nil, {"short"}, {"delete-key-00000001", "delete-key-00000002"}} {
			r := selfServiceRequest(http.MethodDelete, path, "")
			for _, v := range values {
				r.Header.Add("Idempotency-Key", v)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("path=%s values=%v status=%d", path, values, w.Code)
			}
		}
		r := selfServiceRequest(http.MethodDelete, path, "")
		r.Header.Set("Idempotency-Key", "delete-key-00000001")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("path=%s status=%d", path, w.Code)
		}
	}
	if stub.resetKey != "delete-key-00000001" || stub.revokeKey != "delete-key-00000001" {
		t.Fatalf("reset=%q revoke=%q", stub.resetKey, stub.revokeKey)
	}
}

func TestHostedRegisterAndRecoveryRejectBrowserContinuationFields(t *testing.T) {
	h := newSelfServiceHandler(t)
	for _, tc := range []struct{ path, body string }{{"/auth/register", `{"identifier":"user@example.com","credential":"password-000000","verification_continuation_id":"secret","verification_proof":"proof-00000000000"}`}, {"/auth/recovery/complete", `{"continuation_id":"secret","recovery_proof":"proof-00000000000","new_credential":"password-000001"}`}} {
		r := selfServiceRequest(http.MethodPost, tc.path, tc.body)
		r.Header.Set("Idempotency-Key", "idempotency-key-0001")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", tc.path, w.Code, w.Body.String())
		}
	}
}

func handlerWithSelfServiceError(t *testing.T, err error) *Handler {
	t.Helper()
	h, e := New(&selfServiceStub{serviceStub: &serviceStub{error: err}}, authStub{}, "https://hosted.example")
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func TestDirectHTTPRejectsDisabledAuthCapabilities(t *testing.T) {
	h := handlerWithSelfServiceError(t, ErrCapabilityUnavailable)
	tests := []struct{ path, body string }{{"/auth/password", `{"identifier":"user@example.com","credential":"password-000000","risk_summary":{}}`}, {"/auth/verification/start", `{"identifier":"user@example.com"}`}, {"/auth/recovery/start", `{"identifier":"user@example.com"}`}}
	for _, tt := range tests {
		r := selfServiceRequest(http.MethodPost, tt.path, tt.body)
		r.Header.Set("Idempotency-Key", "idempotency-key-0001")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "hosted.capability_not_available") {
			t.Fatalf("path=%s status=%d body=%s", tt.path, w.Code, w.Body.String())
		}
	}
}
func TestBootstrapFalseAndDirectCapabilityRejectionAreConsistent(t *testing.T) {
	disabled := false
	h, e := New(&selfServiceStub{serviceStub: &serviceStub{}, authCapabilities: &disabled}, authStub{}, "https://hosted.example")
	if e != nil {
		t.Fatal(e)
	}
	r := selfServiceRequest(http.MethodGet, "/auth/bootstrap", "")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"password_enabled":false`) || !strings.Contains(w.Body.String(), `"registration_enabled":false`) || !strings.Contains(w.Body.String(), `"recovery_enabled":false`) {
		t.Fatalf("bootstrap=%s", w.Body.String())
	}
	reject := handlerWithSelfServiceError(t, ErrCapabilityUnavailable)
	r = selfServiceRequest(http.MethodPost, "/auth/recovery/start", `{"identifier":"user@example.com"}`)
	r.Header.Set("Idempotency-Key", "idempotency-key-0001")
	w = httptest.NewRecorder()
	reject.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "hosted.capability_not_available") {
		t.Fatalf("reject=%s", w.Body.String())
	}
}
func TestDirectHTTPRejectsEveryDisabledAccountAction(t *testing.T) {
	h := handlerWithSelfServiceError(t, ErrCapabilityUnavailable)
	tests := []struct{ method, path, body string }{{http.MethodPatch, "/account/profile", `{"expected_version":1,"display_name":"Updated"}`}, {http.MethodPost, "/account/password", `{"current_credential":"password-000000","new_credential":"password-000001","revoke_other_sessions":false}`}, {http.MethodDelete, "/account/sessions/uses_abc", ""}, {http.MethodPost, "/account/complete", `{"result":"closed"}`}}
	for _, tt := range tests {
		r := selfServiceRequest(tt.method, tt.path, tt.body)
		r.Header.Set("Idempotency-Key", "idempotency-key-0001")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "hosted.capability_not_available") {
			t.Fatalf("path=%s status=%d body=%s", tt.path, w.Code, w.Body.String())
		}
	}
}
func TestVersionAndIdempotencyConflictsUseDistinctCodes(t *testing.T) {
	for _, tt := range []struct {
		err  error
		code string
	}{{ErrVersionConflict, "hosted.version_conflict"}, {ErrConflict, "hosted.idempotency_conflict"}} {
		h := handlerWithSelfServiceError(t, tt.err)
		r := selfServiceRequest(http.MethodPatch, "/account/profile", `{"expected_version":1,"display_name":"Updated"}`)
		r.Header.Set("Idempotency-Key", "idempotency-key-0001")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"`+tt.code+`"`) {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestTerminalSelfServiceInteractionUsesStableConflict(t *testing.T) {
	h := handlerWithSelfServiceError(t, ErrInteractionTerminal)
	r := selfServiceRequest(http.MethodGet, "/auth/bootstrap", "")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"hosted.interaction_terminal"`) || !strings.Contains(w.Body.String(), `"retryable":false`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
func TestValidationErrorIncludesSafeFieldErrors(t *testing.T) {
	h := newSelfServiceHandler(t)
	r := selfServiceRequest(http.MethodPatch, "/account/profile", `{"expected_version":0}`)
	r.Header.Set("Idempotency-Key", "idempotency-key-0001")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"field_errors":[{"field":"expected_version","code":"minimum"}]`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHostedBootstrapRejectsBearerEvenWithValidToken(t *testing.T) {
	h := newSelfServiceHandler(t)
	r := selfServiceRequest(http.MethodGet, "/auth/bootstrap", "")
	r.Header.Set("Authorization", "Bearer client-token-000000000000000000000000")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHostedBootstrapNeedsCookieButNotOriginOrCSRF(t *testing.T) {
	h := newSelfServiceHandler(t)
	for _, path := range []string{"/auth/bootstrap", "/account/bootstrap"} {
		for _, origin := range []string{"", "https://hosted.example"} {
			r := selfServiceRequest(http.MethodGet, path, "")
			r.Header.Del("X-CSRF-Token")
			if origin == "" {
				r.Header.Del("Origin")
			} else {
				r.Header.Set("Origin", origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("path=%s origin=%q status=%d body=%s", path, origin, w.Code, w.Body.String())
			}
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{name: "missing cookie", mutate: func(r *http.Request) { r.Header.Del("Cookie") }},
		{name: "duplicate cookie", mutate: func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: HostedSessionCookieName, Value: "hosted-cookie-0000000000000000000000000000000000"})
		}},
		{name: "authorization with valid cookie", mutate: func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer attacker-token")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := selfServiceRequest(http.MethodGet, "/auth/bootstrap", "")
			r.Header.Set("Origin", "https://evil.example")
			test.mutate(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s headers=%v", w.Code, w.Body.String(), r.Header)
			}
		})
	}
	for _, test := range []struct {
		name   string
		values []string
	}{
		{name: "evil", values: []string{"https://evil.example"}},
		{name: "null", values: []string{"null"}},
		{name: "empty", values: []string{""}},
		{name: "duplicate", values: []string{"https://hosted.example", "https://hosted.example"}},
	} {
		t.Run("origin "+test.name, func(t *testing.T) {
			r := selfServiceRequest(http.MethodGet, "/account/bootstrap", "")
			r.Header.Del("Origin")
			for _, value := range test.values {
				r.Header.Add("Origin", value)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"hosted.csrf_failed"`) {
				t.Fatalf("values=%q status=%d body=%s", test.values, w.Code, w.Body.String())
			}
		})
	}
	t.Run("cross interaction precedes origin", func(t *testing.T) {
		r := request(http.MethodGet, "/api/v1/hosted/interactions/"+testInteractionID+"x/auth/bootstrap", "")
		hosted(r)
		r.Header.Set("Origin", "https://evil.example")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestHostedWriteStillRequiresExactOrigin(t *testing.T) {
	h := newSelfServiceHandler(t)
	r := selfServiceRequest(http.MethodPost, "/auth/verification/start", `{"identifier":"user@example.com"}`)
	r.Header.Set("Idempotency-Key", "idempotency-key-0001")
	r.Header.Del("Origin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"hosted.csrf_failed"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHostedProfileRejectsUnsafeOrOversizedFields(t *testing.T) {
	h := newSelfServiceHandler(t)
	for _, body := range []string{`{"expected_version":1,"avatar_url":"javascript:alert(1)"}`, `{"expected_version":1,"locale":"abcdefghijklmnopqrstuvwxyz123456789"}`, `{"expected_version":1,"timezone":"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmn"}`} {
		r := selfServiceRequest(http.MethodPatch, "/account/profile", body)
		r.Header.Set("Idempotency-Key", "idempotency-key-0001")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestOpenCompletedInteractionReturnsStableCompletion(t *testing.T) {
	completedAt := time.Now()
	resultKind := "authorization_code"
	v := projection()
	v.Status = "completed"
	v.ResultKind = &resultKind
	v.CompletedAt = &completedAt
	v.AllowedActions = []string{"exchange"}
	c := completion()
	s := &serviceStub{browserValue: &BrowserSession{Interaction: v, CSRFToken: "csrf-token-0000000000000000000000000000", BrowserSessionExpiresAt: time.Now().Add(time.Hour), CookieToken: "hosted-cookie-0000000000000000000000000000000000", Completion: &c}}
	h := newTestHandler(t, s)
	for range 2 {
		r := request(http.MethodPost, "/api/v1/hosted/interactions/"+testInteractionID+"/browser-session", "")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"completion"`) {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestAccountBootstrapPreservesServerAllowedActionSubsets(t *testing.T) {
	for _, actions := range [][]string{{}, {"update_profile"}, {"change_password", "complete"}, {"revoke_session"}} {
		h, e := New(&selfServiceStub{serviceStub: &serviceStub{}, allowedActions: actions}, authStub{}, "https://hosted.example")
		if e != nil {
			t.Fatal(e)
		}
		r := selfServiceRequest(http.MethodGet, "/account/bootstrap", "")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("actions=%v status=%d body=%s", actions, w.Code, w.Body.String())
		}
		var body HostedAccountBootstrap
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if !sameUniqueStrings(body.AllowedActions, actions) {
			t.Fatalf("actions=%v body-actions=%v", actions, body.AllowedActions)
		}
	}
}
