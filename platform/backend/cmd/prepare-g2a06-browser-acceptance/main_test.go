package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"platform.local/capability-platform/backend/internal/modules/hostedinteraction"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/testsupport/g2a06acceptance"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func requireAcceptanceFilesystem(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("G2A-06 browser acceptance filesystem is intentionally Windows-only")
	}
}

func TestNonWindowsAcceptanceFilesystemFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows fail-closed behavior")
	}
	if _, err := openNewRuntimeFile(filepath.Join(t.TempDir(), "state")); err == nil || !strings.Contains(err.Error(), "only on Windows") {
		t.Fatalf("non-Windows acceptance filesystem did not fail closed: %v", err)
	}
}

func TestOutputHasExactPublicWhitelistAndNoSecrets(t *testing.T) {
	value := g2a06acceptance.Result{AuthInteractionID: "hint_auth", AuthURL: "https://127.0.0.1:5175/ui/v1/auth?interaction_id=hint_auth", AccountInteractionID: "hint_account", AccountURL: "https://127.0.0.1:5175/ui/v1/account?interaction_id=hint_account", AuthState: "hidden-auth-state", NegativeAuthState: "hidden-negative-state", AccountState: "hidden-account-state"}
	var out bytes.Buffer
	if err := writeOutput(&out, value); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if json.Unmarshal(out.Bytes(), &got) != nil {
		t.Fatal(out.String())
	}
	want := map[string]bool{"auth_interaction_id": true, "auth_url": true, "account_interaction_id": true, "account_url": true}
	if len(got) != len(want) {
		t.Fatalf("keys=%v", got)
	}
	for key := range got {
		if !want[key] {
			t.Fatalf("unexpected key %q", key)
		}
	}
	for _, secret := range []string{"password", "pepper", "verifier", "csrf", "postgres://", "token"} {
		if strings.Contains(strings.ToLower(out.String()), secret) {
			t.Fatalf("secret marker %q in %s", secret, out.String())
		}
	}
}
func TestControlledFileRejectsTraversalAndSymlinkEscape(t *testing.T) {
	requireAcceptanceFilesystem(t)
	root := t.TempDir()
	allowed := filepath.Join(root, ".runtime")
	if err := os.MkdirAll(allowed, 0700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(allowed, "secret.txt")
	outside := filepath.Join(root, "outside.txt")
	if os.WriteFile(inside, []byte("x"), 0600) != nil || os.WriteFile(outside, []byte("x"), 0600) != nil {
		t.Fatal("write")
	}
	if got, err := controlledFile(root, allowed, inside); err != nil || got == "" {
		t.Fatalf("inside=(%q,%v)", got, err)
	}
	if _, err := controlledFile(root, allowed, outside); err == nil {
		t.Fatal("accepted traversal")
	}
	link := filepath.Join(allowed, "link.txt")
	if err := os.Symlink(outside, link); err == nil {
		if _, err = controlledFile(root, allowed, link); err == nil {
			t.Fatal("accepted escaping symlink")
		}
	}
}
func TestParseRequiresExplicitFixtureAndFixedRuntimeSecrets(t *testing.T) {
	if _, err := parse([]string{"prepare", "--acceptance-fixture", "--password-file", "p"}); err != nil {
		t.Fatal(err)
	}
	if _, err := parse([]string{"cleanup", "--acceptance-fixture", "--auth-interaction-id", "a", "--account-interaction-id", "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := parse([]string{"verify", "--acceptance-fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err := parse([]string{"recover", "--acceptance-fixture"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"prepare", "--password-file", "p"}, {"prepare", "--acceptance-fixture"}, {"cleanup", "--acceptance-fixture", "--auth-interaction-id", "a"}, {"cleanup", "--acceptance-fixture", "--database-url-file", "postgres://secret", "--auth-interaction-id", "a", "--account-interaction-id", "b"}, {"verify", "--acceptance-fixture", "--auth-interaction-id", "a"}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestEncryptedManifestRejectsTamperAndContainsNoPlaintextSecrets(t *testing.T) {
	requireAcceptanceFilesystem(t)
	root := t.TempDir()
	if os.MkdirAll(filepath.Join(root, ".runtime"), 0700) != nil {
		t.Fatal("runtime")
	}
	pepper := strings.Repeat("manifest-admin-pepper-", 3)
	prepared := g2a06acceptance.Result{AuthInteractionID: "hint_auth_manifest", NegativeAuthInteractionID: "hint_negative_manifest", AccountInteractionID: "hint_account_manifest", ClientSessionID: "csess_manifest", ClientToken: "client-token-plaintext-marker", CodeVerifier: "verifier-plaintext-marker", NegativeCodeVerifier: "negative-verifier-marker", AuthState: "auth-state-plaintext-marker", NegativeAuthState: "negative-state-plaintext-marker", AccountState: "account-state-plaintext-marker", ProductID: "prod_manifest", ApplicationID: "app_manifest", TenantID: "tenant_manifest", UserID: "usr_manifest", UserSessionID: "uses_manifest"}
	password := []byte("password-plaintext-marker")
	if err := writeManifest(root, pepper, prepared, password); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(manifestFile(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{prepared.ClientToken, prepared.CodeVerifier, prepared.NegativeCodeVerifier, prepared.AuthState, prepared.NegativeAuthState, prepared.AccountState, string(password), "postgres://"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("manifest leaked %q", forbidden)
		}
	}
	m, p, err := readManifest(root, pepper)
	if err != nil || m.AuthInteractionID != prepared.AuthInteractionID || p.ClientToken != prepared.ClientToken {
		t.Fatalf("roundtrip=(%+v,%+v,%v)", m, p, err)
	}
	var stored acceptanceManifest
	if json.Unmarshal(raw, &stored) != nil {
		t.Fatal("decode")
	}
	stored.AuthInteractionID = "hint_swapped"
	tampered, _ := json.Marshal(stored)
	if os.WriteFile(manifestFile(root), tampered, 0600) != nil || os.WriteFile(reservationFile(root), tampered, 0600) != nil {
		t.Fatal("tamper")
	}
	if _, _, err = readManifest(root, pepper); err == nil {
		t.Fatal("accepted tampered manifest")
	}
}

func TestPrepareReservationRejectsBeforePrepareCallback(t *testing.T) {
	requireAcceptanceFilesystem(t)
	for _, fixture := range []struct {
		name string
		path func(string) string
	}{{"reservation", reservationFile}, {"manifest", manifestFile}, {"temporary", func(root string) string {
		return filepath.Join(root, ".runtime", "G2A-06", ".acceptance-existing.tmp-state")
	}}, {"reservation prefix orphan", func(root string) string {
		return reservationFile(root) + "malformed-orphan"
	}}} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			if os.MkdirAll(filepath.Join(root, ".runtime", "G2A-06"), 0700) != nil || os.WriteFile(fixture.path(root), []byte("existing"), 0600) != nil {
				t.Fatal("fixture")
			}
			called := 0
			d := defaults()
			d.prepare = func(context.Context, *pgxpool.Pool, g2a06acceptance.Options) (g2a06acceptance.Result, error) {
				called++
				return g2a06acceptance.Result{}, errors.New("must not run")
			}
			if _, err := prepareFixture(context.Background(), root, nil, g2a06acceptance.Options{AcceptanceFixture: true}, []byte("controlled password marker"), strings.Repeat("p", 48), d); err == nil {
				t.Fatal("prepare accepted existing acceptance state")
			}
			if called != 0 {
				t.Fatalf("prepare callback calls=%d", called)
			}
		})
	}
}

func TestPrepareMarkPostcheckFailureNeverCallsDatabaseAndLeavesNoState(t *testing.T) {
	requireAcceptanceFilesystem(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	d := defaults()
	prepareCalls := 0
	d.prepare = func(context.Context, *pgxpool.Pool, g2a06acceptance.Options) (g2a06acceptance.Result, error) {
		prepareCalls++
		return g2a06acceptance.Result{}, errors.New("database prepare must not run")
	}
	secondReserveRejected := false
	d.markPreparing = func(root string) error {
		return markAcceptancePreparingWithHooks(root, &controlledRaceHooks{replacementPostcheck: func() error {
			if err := reserveAcceptance(root); err != nil {
				secondReserveRejected = true
			}
			return errors.New("injected replacement postcheck failure")
		}})
	}
	if _, err := prepareFixture(context.Background(), root, nil, g2a06acceptance.Options{AcceptanceFixture: true}, []byte("controlled password marker"), strings.Repeat("p", 48), d); err == nil {
		t.Fatal("injected mark postcheck failure was ignored")
	}
	if prepareCalls != 0 || !secondReserveRejected {
		t.Fatalf("prepare_calls=%d second_reserve_rejected=%v", prepareCalls, secondReserveRejected)
	}
	assertNoAcceptanceState(t, root)
	if err := reserveAcceptance(root); err != nil {
		t.Fatalf("reserve failed after identity-safe mark cleanup: %v", err)
	}
	if err := removeAcceptanceState(root); err != nil {
		t.Fatal(err)
	}
}

func TestReserveRejectsPrefixCreatedAfterPreflightWithoutDatabase(t *testing.T) {
	requireAcceptanceFilesystem(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	orphan := reservationFile(root) + "racing-orphan"
	d := defaults()
	markCalls := 0
	prepareCalls := 0
	d.reserveState = func(root string) error {
		return reserveAcceptanceWithHooks(root, &controlledRaceHooks{afterValidateBeforeCreate: func(path string) {
			if !strings.EqualFold(filepath.Clean(path), filepath.Clean(reservationFile(root))) {
				return
			}
			if err := os.WriteFile(orphan, []byte("attacker orphan"), 0600); err != nil {
				t.Fatal(err)
			}
		}})
	}
	d.markPreparing = func(string) error {
		markCalls++
		return errors.New("mark must not run")
	}
	d.prepare = func(context.Context, *pgxpool.Pool, g2a06acceptance.Options) (g2a06acceptance.Result, error) {
		prepareCalls++
		return g2a06acceptance.Result{}, errors.New("database prepare must not run")
	}
	if _, err := prepareFixture(context.Background(), root, nil, g2a06acceptance.Options{AcceptanceFixture: true}, []byte("controlled password marker"), strings.Repeat("p", 48), d); err == nil {
		t.Fatal("racing prefix orphan was accepted")
	}
	if markCalls != 0 || prepareCalls != 0 {
		t.Fatalf("mark_calls=%d prepare_calls=%d", markCalls, prepareCalls)
	}
	if _, err := os.Lstat(reservationFile(root)); !os.IsNotExist(err) {
		t.Fatalf("exact reservation was not identity-cleaned: %v", err)
	}
	raw, err := os.ReadFile(orphan)
	if err != nil || string(raw) != "attacker orphan" {
		t.Fatalf("orphan was changed or removed: raw=%q err=%v", raw, err)
	}
	if err := reserveAcceptance(root); err == nil {
		t.Fatal("second reserve bypassed retained prefix orphan")
	}
	if err := os.Remove(orphan); err != nil {
		t.Fatal(err)
	}
	assertNoAcceptanceState(t, root)
}

func TestPreparingGateRetainsAuditStateWhenPrefixAppearsWithoutDatabase(t *testing.T) {
	requireAcceptanceFilesystem(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	orphan := reservationFile(root) + "after-mark-orphan"
	d := defaults()
	prepareCalls := 0
	d.validatePreparing = func(root string) error {
		return validatePreparingReservationWithHooks(root, &controlledRaceHooks{afterPreparingValidated: func(string) {
			if err := os.WriteFile(orphan, []byte("attacker after mark"), 0600); err != nil {
				t.Fatal(err)
			}
		}})
	}
	d.prepare = func(context.Context, *pgxpool.Pool, g2a06acceptance.Options) (g2a06acceptance.Result, error) {
		prepareCalls++
		return g2a06acceptance.Result{}, errors.New("database prepare must not run")
	}
	if _, err := prepareFixture(context.Background(), root, nil, g2a06acceptance.Options{AcceptanceFixture: true}, []byte("controlled password marker"), strings.Repeat("p", 48), d); err == nil {
		t.Fatal("preparing namespace race was accepted")
	}
	if prepareCalls != 0 {
		t.Fatalf("database prepare calls=%d", prepareCalls)
	}
	raw, err := os.ReadFile(reservationFile(root))
	if err != nil || string(raw) != reservationPreparingRecord {
		t.Fatalf("preparing audit state was changed or removed: raw=%q err=%v", raw, err)
	}
	if recovered, recoverErr := recoverReservationOnly(root); recovered || recoverErr == nil || !strings.Contains(recoverErr.Error(), "manual audit required") {
		t.Fatalf("preparing recovery=(%v,%v)", recovered, recoverErr)
	}
	if err := reserveAcceptance(root); err == nil {
		t.Fatal("second reserve bypassed post-mark prefix orphan")
	}
	if err := removeAcceptanceState(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(orphan); err != nil {
		t.Fatal(err)
	}
	assertNoAcceptanceState(t, root)
}

func TestVerifyResumesAfterExchangeBeforeStagePersistence(t *testing.T) {
	authExchanged := false
	accountExchanged := false
	exchangeCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "hint_account_resume") && strings.HasSuffix(r.URL.Path, "/browser-session") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"csrf_token":"test-csrf","completion":{"return_url":"https://127.0.0.1:5174/login?code=test-account-code&state=test-account-state"}}`))
			return
		}
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/exchange") {
			http.NotFound(w, r)
			return
		}
		exchangeCalls++
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "hint_auth_resume") && !authExchanged {
			authExchanged = true
			_, _ = w.Write([]byte(`{"interaction_id":"hint_auth_resume","result_type":"user_session","user_session":{"access_token":"test-access","refresh_token":"test-refresh","access_expires_at":"2099-01-01T00:00:00Z","refresh_expires_at":"2099-01-02T00:00:00Z","user":{"user_id":"test-user","account_status":"active"}}}`))
			return
		}
		if strings.Contains(r.URL.Path, "hint_account_resume") && !accountExchanged {
			accountExchanged = true
			_, _ = w.Write([]byte(`{"interaction_id":"hint_account_resume","result_type":"account_completed","account_result":{"result":"self_service_completed"}}`))
			return
		}
		writeTestProblem(w, http.StatusConflict, "hosted.invalid_grant")
	}))
	defer server.Close()
	previous := formalHTTPBase
	formalHTTPBase = server.URL
	defer func() { formalHTTPBase = previous }()

	manifest := acceptanceManifest{Version: manifestVersion, AuthInteractionID: "hint_auth_resume", NegativeAuthInteractionID: "hint_negative_resume", AccountInteractionID: "hint_account_resume"}
	ready := manifestPayload{ClientToken: "test-client-token", AccountClientToken: "test-client-token", CodeVerifier: "test-code-verifier", AuthState: "test-auth-state", AccountState: "test-account-state", PositiveCode: "test-completion-code", PositiveState: "test-auth-state", Stage: stageReady}
	crashPersist := func(next manifestPayload) error {
		if next.Stage == stageExchanged {
			return errors.New("injected persistence failure")
		}
		return nil
	}
	status := func() (g2a06acceptance.InteractionStatuses, error) {
		return g2a06acceptance.InteractionStatuses{Auth: hostedinteraction.StatusExchanged, Account: hostedinteraction.StatusExchanged}, nil
	}
	if err := verifyAcceptance(context.Background(), manifest, ready, crashPersist, status); err == nil {
		t.Fatal("injected exchange persistence failure was ignored")
	}
	var recovered manifestPayload
	if err := verifyAcceptance(context.Background(), manifest, ready, func(next manifestPayload) error { recovered = next; return nil }, status); err != nil {
		t.Fatal(err)
	}
	if recovered.Stage != stageAccountReplayVerified || exchangeCalls != 5 {
		t.Fatalf("recovery stage=%q exchange_calls=%d", recovered.Stage, exchangeCalls)
	}
	before := exchangeCalls
	if err := verifyAcceptance(context.Background(), manifest, recovered, func(manifestPayload) error { return nil }, status); err != nil || exchangeCalls != before {
		t.Fatalf("verified replay was not idempotent: err=%v calls=%d", err, exchangeCalls)
	}
}

func TestVerifyRejectsPersistedWrongCompletionStateBeforeExchange(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	previous := formalHTTPBase
	formalHTTPBase = server.URL
	defer func() { formalHTTPBase = previous }()
	manifest := acceptanceManifest{AuthInteractionID: "hint_auth_wrong_state", NegativeAuthInteractionID: "hint_negative_wrong_state", AccountInteractionID: "hint_account_wrong_state"}
	payload := manifestPayload{AuthState: "expected-state", PositiveState: "attacker-state", PositiveCode: "test-code", Stage: stageReady}
	status := func() (g2a06acceptance.InteractionStatuses, error) { return g2a06acceptance.InteractionStatuses{}, nil }
	if err := verifyAcceptance(context.Background(), manifest, payload, func(manifestPayload) error { return nil }, status); err == nil {
		t.Fatal("wrong completion state was accepted")
	}
	if requests != 0 {
		t.Fatalf("wrong state reached exchange requests=%d", requests)
	}
}

func TestVerifyResumesAccountExchangeBeforeStagePersistence(t *testing.T) {
	exchanged := false
	calls := 0
	rejectedUserBearer := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-client-token" {
			rejectedUserBearer++
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		if !exchanged {
			exchanged = true
			_, _ = w.Write([]byte(`{"interaction_id":"hint_account_crash","result_type":"account_completed","account_result":{"result":"closed"}}`))
			return
		}
		writeTestProblem(w, http.StatusConflict, "hosted.invalid_grant")
	}))
	defer server.Close()
	previous := formalHTTPBase
	formalHTTPBase = server.URL
	defer func() { formalHTTPBase = previous }()
	if status, _ := exchangeAcceptance(context.Background(), "hint_account_crash", "account-code", "", "test-user-token-00000000"); status != http.StatusUnauthorized {
		t.Fatalf("account exchange accepted user bearer status=%d", status)
	}
	manifest := acceptanceManifest{AuthInteractionID: "hint_auth_crash", NegativeAuthInteractionID: "hint_negative_crash", AccountInteractionID: "hint_account_crash"}
	ready := manifestPayload{ClientToken: "test-client-token", AccountClientToken: "test-client-token", AuthState: "auth-state", PositiveState: "auth-state", PositiveCode: "auth-code", AccountState: "account-state", AccountCompletionState: "account-state", AccountCode: "account-code", Stage: stageAccountReady}
	status := func() (g2a06acceptance.InteractionStatuses, error) {
		return g2a06acceptance.InteractionStatuses{Auth: hostedinteraction.StatusExchanged, Account: hostedinteraction.StatusExchanged}, nil
	}
	if err := verifyAcceptance(context.Background(), manifest, ready, func(next manifestPayload) error {
		if next.Stage == stageAccountExchanged {
			return errors.New("injected account persistence failure")
		}
		return nil
	}, status); err == nil {
		t.Fatal("injected account persistence failure was ignored")
	}
	var recovered manifestPayload
	if err := verifyAcceptance(context.Background(), manifest, ready, func(next manifestPayload) error { recovered = next; return nil }, status); err != nil {
		t.Fatal(err)
	}
	if recovered.Stage != stageAccountReplayVerified || calls != 3 || rejectedUserBearer != 1 {
		t.Fatalf("account recovery stage=%q calls=%d rejected_user_bearer=%d", recovered.Stage, calls, rejectedUserBearer)
	}
}

func TestStableExchangeProblemRejectsUnrelatedClientErrors(t *testing.T) {
	for _, test := range []struct {
		status int
		code   string
	}{
		{http.StatusUnauthorized, "hosted.authentication_required"},
		{http.StatusNotFound, "hosted.invalid_interaction"},
		{http.StatusConflict, "hosted.interaction_terminal"},
	} {
		recorder := httptest.NewRecorder()
		writeTestProblem(recorder, test.status, test.code)
		if isStableProblem(test.status, recorder.Body.Bytes(), http.StatusConflict, "hosted.invalid_grant") {
			t.Fatalf("accepted unrelated problem status=%d code=%s", test.status, test.code)
		}
	}
	recorder := httptest.NewRecorder()
	writeTestProblem(recorder, http.StatusConflict, "hosted.invalid_grant")
	if !isStableProblem(http.StatusConflict, recorder.Body.Bytes(), http.StatusConflict, "hosted.invalid_grant") {
		t.Fatal("rejected exact non-retryable invalid grant problem")
	}
}

func writeTestProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "detail": "test problem", "request_id": "test-request-id", "retryable": false})
}

func TestPrepareStateWriteFailureCompensatesAndLeavesEncryptedRecovery(t *testing.T) {
	requireAcceptanceFilesystem(t)
	root := t.TempDir()
	if os.MkdirAll(filepath.Join(root, ".runtime"), 0700) != nil {
		t.Fatal("runtime")
	}
	pepper := strings.Repeat("compensation-pepper-", 3)
	prepared := g2a06acceptance.Result{AuthInteractionID: "hint_auth_compensate", NegativeAuthInteractionID: "hint_negative_compensate", AccountInteractionID: "hint_account_compensate", ClientSessionID: "csess_compensate", ClientToken: "test-client-token", CodeVerifier: "test-code-verifier", NegativeCodeVerifier: "test-negative-verifier", ProductID: "prod_compensate", ApplicationID: "app_compensate", TenantID: "tenant_compensate", UserID: "usr_compensate", UserSessionID: "uses_compensate"}
	d := defaults()
	d.prepare = func(context.Context, *pgxpool.Pool, g2a06acceptance.Options) (g2a06acceptance.Result, error) {
		return prepared, nil
	}
	d.writeState = func(string, string, g2a06acceptance.Result, []byte) error {
		return errors.New("injected write failure")
	}
	cleanupCalls := 0
	d.cleanup = func(_ context.Context, _ *pgxpool.Pool, _ g2a06acceptance.Options, command g2a06acceptance.CleanupCommand) error {
		cleanupCalls++
		if command != resultCleanupCommand(prepared) {
			return errors.New("imprecise compensation")
		}
		return nil
	}
	if _, err := prepareFixture(context.Background(), root, nil, g2a06acceptance.Options{AcceptanceFixture: true}, []byte("controlled password marker"), pepper, d); err == nil {
		t.Fatal("injected state write failure was ignored")
	}
	manifest, payload, err := readManifest(root, pepper)
	if err != nil || payload.Stage != stageCompensated || manifest.AuthInteractionID != prepared.AuthInteractionID || cleanupCalls != 1 {
		t.Fatalf("compensation manifest=%+v stage=%q cleanup_calls=%d err=%v", manifest, payload.Stage, cleanupCalls, err)
	}
}

func TestPrepareCallbackFailureLeavesReservationAndBlocksSecondFixture(t *testing.T) {
	requireAcceptanceFilesystem(t)
	root := t.TempDir()
	if os.MkdirAll(filepath.Join(root, ".runtime"), 0700) != nil {
		t.Fatal("runtime")
	}
	d := defaults()
	prepareCalls := 0
	d.prepare = func(context.Context, *pgxpool.Pool, g2a06acceptance.Options) (g2a06acceptance.Result, error) {
		prepareCalls++
		return g2a06acceptance.Result{}, errors.New("injected prepare failure")
	}
	options := g2a06acceptance.Options{AcceptanceFixture: true}
	password := []byte("controlled password marker")
	pepper := strings.Repeat("prepare-failure-pepper-", 3)
	if _, err := prepareFixture(context.Background(), root, nil, options, password, pepper, d); err == nil {
		t.Fatal("injected prepare failure was ignored")
	}
	if _, err := prepareFixture(context.Background(), root, nil, options, password, pepper, d); err == nil {
		t.Fatal("second prepare accepted retained reservation")
	}
	if prepareCalls != 1 {
		t.Fatalf("prepare callback calls=%d", prepareCalls)
	}
	raw, err := os.ReadFile(reservationFile(root))
	if err != nil || string(raw) != reservationPreparingRecord {
		t.Fatalf("prepare boundary was not retained exactly: raw=%q err=%v", raw, err)
	}
}

func TestRunRecoverReservationOnlyStateMachine(t *testing.T) {
	requireAcceptanceFilesystem(t)
	const pepper = "reservation-only-admin-pepper-000000000000000000000000"

	t.Run("reserved is removed and permits a second reserve", func(t *testing.T) {
		root := controlledRunRoot(t, pepper)
		if err := reserveAcceptance(root); err != nil {
			t.Fatal(err)
		}
		counts := recoveryDependencyCounts{}
		d := recoverRunDependencies(root, &counts)
		if err := run(context.Background(), []string{"recover", "--acceptance-fixture"}, io.Discard, d); err != nil {
			t.Fatal(err)
		}
		assertNoRecoveryDependencies(t, counts)
		assertNoAcceptanceState(t, root)
		if err := reserveAcceptance(root); err != nil {
			t.Fatalf("second reserve failed after recovery: %v", err)
		}
		if err := removeAcceptanceState(root); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("preparing requires manual audit and remains", func(t *testing.T) {
		root := controlledRunRoot(t, pepper)
		if err := reserveAcceptance(root); err != nil {
			t.Fatal(err)
		}
		if err := markAcceptancePreparing(root); err != nil {
			t.Fatal(err)
		}
		counts := recoveryDependencyCounts{}
		err := run(context.Background(), []string{"recover", "--acceptance-fixture"}, io.Discard, recoverRunDependencies(root, &counts))
		if err == nil || !strings.Contains(err.Error(), "manual audit required") {
			t.Fatalf("preparing reservation was not rejected explicitly: %v", err)
		}
		assertNoRecoveryDependencies(t, counts)
		raw, readErr := os.ReadFile(reservationFile(root))
		if readErr != nil || string(raw) != reservationPreparingRecord {
			t.Fatalf("preparing marker was changed or removed: raw=%q err=%v", raw, readErr)
		}
		if err := reserveAcceptance(root); err == nil {
			t.Fatal("second reserve bypassed preparing audit boundary")
		}
		if err := removeAcceptanceState(root); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unknown marker is rejected without deletion", func(t *testing.T) {
		root := controlledRunRoot(t, pepper)
		if err := reserveAcceptance(root); err != nil {
			t.Fatal(err)
		}
		unknown := []byte(`{"version":"attacker","stage":"reserved"}`)
		if err := atomicWriteControlled(root, reservationFile(root), unknown); err != nil {
			t.Fatal(err)
		}
		counts := recoveryDependencyCounts{}
		err := run(context.Background(), []string{"recover", "--acceptance-fixture"}, io.Discard, recoverRunDependencies(root, &counts))
		if err == nil || !strings.Contains(err.Error(), "unrecognized reservation-only state") {
			t.Fatalf("unknown marker was not rejected: %v", err)
		}
		assertNoRecoveryDependencies(t, counts)
		raw, readErr := os.ReadFile(reservationFile(root))
		if readErr != nil || !bytes.Equal(raw, unknown) {
			t.Fatalf("unknown marker was changed or removed: raw=%q err=%v", raw, readErr)
		}
		if err := removeAcceptanceState(root); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("hardlinked marker is rejected without deleting either entry", func(t *testing.T) {
		root := controlledRunRoot(t, pepper)
		directory := filepath.Dir(reservationFile(root))
		if err := ensureControlledDirectory(root, directory); err != nil {
			t.Fatal(err)
		}
		external := filepath.Join(root, ".runtime", "attacker-reservation-marker.json")
		if err := os.WriteFile(external, []byte(reservationRecord), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(external, reservationFile(root)); err != nil {
			t.Fatal(err)
		}
		counts := recoveryDependencyCounts{}
		err := run(context.Background(), []string{"recover", "--acceptance-fixture"}, io.Discard, recoverRunDependencies(root, &counts))
		if err == nil || !strings.Contains(err.Error(), "controlled reservation-only state is unavailable") {
			t.Fatalf("hardlinked marker was not rejected: %v", err)
		}
		assertNoRecoveryDependencies(t, counts)
		for _, path := range []string{external, reservationFile(root)} {
			raw, readErr := os.ReadFile(path)
			if readErr != nil || string(raw) != reservationRecord {
				t.Fatalf("hardlink entry was changed or removed at %s: raw=%q err=%v", filepath.Base(path), raw, readErr)
			}
		}
		if err := os.Remove(reservationFile(root)); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(external); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("parse delete replacement is blocked and unknown file remains", func(t *testing.T) {
		root := controlledRunRoot(t, pepper)
		if err := reserveAcceptance(root); err != nil {
			t.Fatal(err)
		}
		attacker := filepath.Join(root, ".runtime", "attacker-unknown-marker.json")
		unknown := []byte(`{"version":"attacker","stage":"unknown"}`)
		if err := os.WriteFile(attacker, unknown, 0600); err != nil {
			t.Fatal(err)
		}
		counts := recoveryDependencyCounts{}
		d := recoverRunDependencies(root, &counts)
		attackBlocked := false
		d.recoverLocal = func(root string) (bool, error) {
			return recoverReservationOnlyWithHooks(root, &controlledRaceHooks{afterReservationParse: func(path string) {
				backup := path + ".attacker-backup"
				if err := os.Rename(path, backup); err != nil {
					attackBlocked = true
					return
				}
				_ = os.Rename(attacker, path)
			}})
		}
		if err := run(context.Background(), []string{"recover", "--acceptance-fixture"}, io.Discard, d); err != nil {
			t.Fatal(err)
		}
		if !attackBlocked {
			t.Fatal("reservation path replacement was not blocked by live recovery handle")
		}
		assertNoRecoveryDependencies(t, counts)
		assertNoAcceptanceState(t, root)
		raw, err := os.ReadFile(attacker)
		if err != nil || !bytes.Equal(raw, unknown) {
			t.Fatalf("unknown attacker file was changed or deleted: raw=%q err=%v", raw, err)
		}
		if err := os.Remove(attacker); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("encrypted manifest continues through configured cleanup", func(t *testing.T) {
		root := controlledRunRoot(t, pepper)
		prepared := g2a06acceptance.Result{AuthInteractionID: "hint_auth_encrypted_recover", NegativeAuthInteractionID: "hint_negative_encrypted_recover", AccountInteractionID: "hint_account_encrypted_recover", ClientSessionID: "csess_encrypted_recover", AccountClientSessionID: "csess_account_encrypted_recover", ProductID: "prod_encrypted_recover", ApplicationID: "app_encrypted_recover", AccountApplicationID: "app_account_encrypted_recover", TenantID: "tenant_encrypted_recover", UserID: "usr_encrypted_recover", UserSessionID: "uses_encrypted_recover", AccountUserSessionID: "uses_account_encrypted_recover"}
		if err := writeManifest(root, pepper, prepared, []byte("controlled password marker")); err != nil {
			t.Fatal(err)
		}
		if err := removeRuntimeFile(manifestFile(root)); err != nil {
			t.Fatal(err)
		}
		counts := recoveryDependencyCounts{}
		d := encryptedRecoverDependencies(root, &counts)
		if err := run(context.Background(), []string{"recover", "--acceptance-fixture"}, io.Discard, d); err != nil {
			t.Fatal(err)
		}
		if counts.loadConfig != 1 || counts.openPool != 1 || counts.cleanup != 1 {
			t.Fatalf("encrypted recovery dependencies=%+v", counts)
		}
		assertNoAcceptanceState(t, root)
	})

	for _, forbidden := range []string{"password", "token", "pepper", "secret", "interaction", "postgres"} {
		if strings.Contains(strings.ToLower(reservationRecord+reservationPreparingRecord), forbidden) {
			t.Fatalf("reservation marker contains secret-bearing field %q", forbidden)
		}
	}
}

func controlledRunRoot(t *testing.T, pepper string) string {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{filepath.Join(root, ".git"), filepath.Join(root, "docs"), filepath.Join(root, "platform", "backend"), filepath.Join(root, ".runtime", "postgres")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, raw := range map[string]string{
		filepath.Join(root, "docs", "README.md"):                         "test root",
		filepath.Join(root, "platform", "backend", "go.mod"):             "module test.local/root",
		filepath.Join(root, ".runtime", "postgres", "test-password.txt"): "controlled-test-password",
		filepath.Join(root, ".runtime", "admin-token-pepper.txt"):        pepper,
	} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

type recoveryDependencyCounts struct {
	loadConfig int
	openPool   int
	cleanup    int
}

func recoverRunDependencies(root string, counts *recoveryDependencyCounts) dependencies {
	d := defaults()
	d.getwd = func() (string, error) { return root, nil }
	d.loadConfig = func(config.LookupEnv) (config.Config, error) {
		counts.loadConfig++
		return config.Config{}, errors.New("local reservation recovery must not load configuration")
	}
	d.openPool = func(context.Context, string) (*pgxpool.Pool, error) {
		counts.openPool++
		return nil, errors.New("local reservation recovery must not open PostgreSQL")
	}
	d.cleanup = func(context.Context, *pgxpool.Pool, g2a06acceptance.Options, g2a06acceptance.CleanupCommand) error {
		counts.cleanup++
		return nil
	}
	return d
}

func encryptedRecoverDependencies(root string, counts *recoveryDependencyCounts) dependencies {
	d := defaults()
	d.getwd = func() (string, error) { return root, nil }
	loadConfig := d.loadConfig
	d.loadConfig = func(lookup config.LookupEnv) (config.Config, error) {
		counts.loadConfig++
		return loadConfig(lookup)
	}
	openPool := d.openPool
	d.openPool = func(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
		counts.openPool++
		return openPool(ctx, dsn)
	}
	d.cleanup = func(context.Context, *pgxpool.Pool, g2a06acceptance.Options, g2a06acceptance.CleanupCommand) error {
		counts.cleanup++
		return nil
	}
	return d
}

func assertNoRecoveryDependencies(t *testing.T, counts recoveryDependencyCounts) {
	t.Helper()
	if counts != (recoveryDependencyCounts{}) {
		t.Fatalf("local reservation recovery used external dependencies: %+v", counts)
	}
}

func assertNoAcceptanceState(t *testing.T, root string) {
	t.Helper()
	for _, path := range []string{reservationFile(root), manifestFile(root)} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("acceptance state remains at %s: %v", filepath.Base(path), err)
		}
	}
	matches, err := filepath.Glob(filepath.Join(root, ".runtime", "G2A-06", ".acceptance-*.tmp-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("acceptance temporary state remains: matches=%v err=%v", matches, err)
	}
	prefixMatches, err := filepath.Glob(reservationFile(root) + "*")
	if err != nil || len(prefixMatches) != 0 {
		t.Fatalf("acceptance reservation prefix state remains: matches=%v err=%v", prefixMatches, err)
	}
}

func TestFixedRuntimeConfigUsesOnlyControlledFilesAndRejectsLinkedOutputRoot(t *testing.T) {
	requireAcceptanceFilesystem(t)
	root := t.TempDir()
	if os.MkdirAll(filepath.Join(root, ".runtime", "postgres"), 0700) != nil {
		t.Fatal("mkdir")
	}
	if os.WriteFile(filepath.Join(root, ".runtime", "postgres", "test-password.txt"), []byte("test database password marker"), 0600) != nil || os.WriteFile(filepath.Join(root, ".runtime", "admin-token-pepper.txt"), []byte(strings.Repeat("a", 48)), 0600) != nil {
		t.Fatal("write")
	}
	cfg, err := loadAcceptanceConfig(root, defaults())
	if err != nil {
		t.Fatal(err)
	}
	if g2a06acceptance.ValidateDatabaseURL(cfg.Database.URL) != nil {
		t.Fatal("unsafe fixed database URL")
	}
	linkedRoot := t.TempDir()
	linked := filepath.Join(root, ".runtime", "linked-output")
	if err = os.Symlink(linkedRoot, linked); err == nil {
		if err = ensureControlledDirectory(root, filepath.Join(linked, "child")); err == nil {
			t.Fatal("accepted linked output root")
		}
	}
}
