package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

type repositoryFake struct {
	credential           Credential
	findErr              error
	created              NewSession
	failures             int
	stored               StoredSession
	rotateErr            error
	creates              int
	rotations            int
	revokes              int
	bootstraps           int
	claimed              []ClaimedOutboxEvent
	published            []string
	controlledCredential ControlledClientCredential
	controlledErr        error
	controlledDigest     []byte
	controlledProofType  string
	registeredClient     ControlledClientRegistration
	registeredCredential ControlledClientCredentialRegistration
	disabledEvent        OutboxEvent
	revokedEvent         OutboxEvent
}

func (r *repositoryFake) FindCredential(context.Context, []byte) (Credential, error) {
	return r.credential, r.findErr
}
func (*repositoryFake) LoginThrottle(context.Context, []byte, []byte, time.Time) (ThrottleState, error) {
	return ThrottleState{}, nil
}
func (r *repositoryFake) RecordLoginFailure(context.Context, LoginFailure) (ThrottleState, error) {
	r.failures++
	return ThrottleState{FailureCount: r.failures}, nil
}
func (*repositoryFake) ClearLoginFailures(context.Context, []byte) error { return nil }
func (r *repositoryFake) CreateAdminSession(_ context.Context, value NewSession) error {
	r.creates++
	r.created = value
	r.stored = value.StoredSession
	return nil
}
func (r *repositoryFake) FindByAccessDigest(context.Context, []byte, time.Time) (StoredSession, error) {
	return r.stored, nil
}
func (*repositoryFake) TouchSession(context.Context, string, time.Time) error { return nil }
func (r *repositoryFake) RotateCSRF(_ context.Context, _ string, digest []byte, _ time.Time) error {
	r.stored.CSRFDigest = digest
	return nil
}
func (r *repositoryFake) RotateRefresh(_ context.Context, _ []byte, _ Transport, binding *ControlledClientBinding, _ Rotation) (StoredSession, error) {
	r.rotations++
	if binding != nil {
		r.stored.ControlledClientID = binding.ClientID
		r.stored.ControlledCredentialID = binding.CredentialID
	}
	return r.stored, r.rotateErr
}
func (r *repositoryFake) RevokeByToken(context.Context, []byte, time.Time, OutboxEvent) error {
	r.revokes++
	return nil
}
func (r *repositoryFake) BootstrapIdentity(context.Context, BootstrapUser) (string, error) {
	r.bootstraps++
	return "user", nil
}
func (r *repositoryFake) ResolveControlledClientCredential(_ context.Context, _, _ string, proofType string, digest []byte, _ time.Time) (ControlledClientCredential, error) {
	r.controlledDigest = append([]byte(nil), digest...)
	r.controlledProofType = proofType
	return r.controlledCredential, r.controlledErr
}
func (r *repositoryFake) RegisterControlledClient(_ context.Context, registration ControlledClientRegistration) error {
	r.registeredClient = registration
	return nil
}
func (r *repositoryFake) AddControlledClientCredential(_ context.Context, registration ControlledClientCredentialRegistration) error {
	r.registeredCredential = registration
	return nil
}
func (r *repositoryFake) DisableControlledClient(_ context.Context, _ string, _ time.Time, event OutboxEvent) error {
	r.disabledEvent = event
	return nil
}
func (r *repositoryFake) RevokeControlledClientCredential(_ context.Context, _, _ string, _ time.Time, event OutboxEvent) error {
	r.revokedEvent = event
	return nil
}
func (r *repositoryFake) ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error) {
	return r.claimed, nil
}
func (r *repositoryFake) MarkOutboxPublished(_ context.Context, eventID string, _ time.Time) error {
	r.published = append(r.published, eventID)
	return nil
}
func (*repositoryFake) MarkOutboxFailed(context.Context, string, string, time.Time, bool) error {
	return nil
}

type accessFake struct{ err error }

func (a accessFake) ResolveAdminAccessSnapshot(context.Context, string, string) (accesscontrol.Snapshot, error) {
	return accesscontrol.Snapshot{AuthorizationVersion: 1, Permissions: []string{"platform.read"}, Scopes: []accesscontrol.Scope{{Type: "platform"}}}, a.err
}

func newTestService(t *testing.T, repo *repositoryFake, access accessFake) *Service {
	t.Helper()
	hasher, err := securevalue.NewHasher(strings.Repeat("p", 32))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repo, access, Bcrypt{Cost: bcrypt.MinCost}, hasher, Policy{AccessTTL: 15 * time.Minute, RefreshTTL: 24 * time.Hour, LoginWindow: time.Minute, LoginMaximumAttempts: 5, LoginBlockDuration: time.Minute}, func() time.Time { return time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func newBearerTestService(t *testing.T, repo *repositoryFake, access accessFake) *Service {
	t.Helper()
	service := newTestService(t, repo, access)
	service.policy.AllowBearer = true
	return service
}

func TestLoginStoresOnlyTokenDigestsAndCookieResponseHasNoTokenPair(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	repo := &repositoryFake{credential: Credential{UserID: "usr-1", DisplayName: "Admin", AccountStatus: "active", PasswordHash: hash}}
	service := newTestService(t, repo, accessFake{})
	session, err := service.LoginAdmin(context.Background(), LoginCommand{Identifier: "admin@example.com", Credential: "correct-password", Requested: TransportCookie, Source: "127.0.0.1", TraceID: "trace-123"})
	if err != nil {
		t.Fatal(err)
	}
	if session.TokenPair != nil || session.CookieTokens == nil || session.CSRFToken == nil {
		t.Fatalf("unexpected cookie response: %+v", session)
	}
	if bytes.Contains(repo.created.AccessToken.Digest, []byte(session.CookieTokens.AccessToken)) || bytes.Contains(repo.created.RefreshToken.Digest, []byte(session.CookieTokens.RefreshToken)) {
		t.Fatal("repository received plaintext token")
	}
	if len(repo.created.AccessToken.Digest) != 32 || len(repo.created.RefreshToken.Digest) != 32 {
		t.Fatal("expected HMAC-SHA256 digests")
	}
}

func TestBearerLoginRequiresResolvedControlledClientAndStoresExactBinding(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	repo := &repositoryFake{
		credential: Credential{UserID: "usr-1", DisplayName: "Admin", AccountStatus: "active", PasswordHash: hash},
		controlledCredential: ControlledClientCredential{
			ControlledClientBinding: ControlledClientBinding{ClientID: "acli_controlled1", CredentialID: "acred_credential1"},
			DisplayName:             "Automation", ClientType: "automation",
		},
	}
	service := newBearerTestService(t, repo, accessFake{})
	proof := &ControlledClientProof{ClientID: "acli_controlled1", CredentialID: "acred_credential1", ProofType: "shared_secret_v1", Secret: "acsec_abcdefghijklmnopqrstuvwxyz0123456789"}
	session, err := service.LoginAdmin(context.Background(), LoginCommand{Identifier: "admin@example.com", Credential: "correct-password", Requested: TransportBearer, ControlledClient: proof, Source: "127.0.0.1", TraceID: "trace-bearer"})
	if err != nil {
		t.Fatal(err)
	}
	if session.Transport != TransportBearer || session.TokenPair == nil || session.CSRFToken != nil {
		t.Fatalf("unexpected bearer response: %+v", session)
	}
	if repo.created.ControlledClientID != proof.ClientID || repo.created.ControlledCredentialID != proof.CredentialID {
		t.Fatalf("stored binding = %q/%q", repo.created.ControlledClientID, repo.created.ControlledCredentialID)
	}
	if bytes.Contains(repo.controlledDigest, []byte(proof.Secret)) || len(repo.controlledDigest) != 32 {
		t.Fatal("repository received plaintext or invalid controlled-client proof digest")
	}
}

func TestBearerLoginInvalidClientUsesGenericCredentialFailure(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	repo := &repositoryFake{
		credential:    Credential{UserID: "usr-1", DisplayName: "Admin", AccountStatus: "active", PasswordHash: hash},
		controlledErr: ErrNotFound,
	}
	service := newBearerTestService(t, repo, accessFake{})
	_, err := service.LoginAdmin(context.Background(), LoginCommand{
		Identifier: "admin@example.com", Credential: "correct-password", Requested: TransportBearer,
		ControlledClient: &ControlledClientProof{ClientID: "acli_unknown12", CredentialID: "acred_unknown12", ProofType: "shared_secret_v1", Secret: "acsec_abcdefghijklmnopqrstuvwxyz0123456789"},
		Source:           "127.0.0.1",
	})
	if !errors.Is(err, ErrInvalidCredentials) || repo.failures != 1 || repo.creates != 0 {
		t.Fatalf("error=%v failures=%d creates=%d", err, repo.failures, repo.creates)
	}
}

func TestBearerRefreshPassesExactResolvedBinding(t *testing.T) {
	repo := &repositoryFake{
		stored:               StoredSession{SessionID: "session", UserID: "usr-1", Transport: TransportBearer, AuthTime: time.Now().UTC(), AccessExpiresAt: time.Now().Add(time.Minute), RefreshExpiresAt: time.Now().Add(time.Hour)},
		controlledCredential: ControlledClientCredential{ControlledClientBinding: ControlledClientBinding{ClientID: "acli_controlled1", CredentialID: "acred_credential1"}},
	}
	service := newBearerTestService(t, repo, accessFake{})
	proof := &ControlledClientProof{ClientID: "acli_controlled1", CredentialID: "acred_credential1", ProofType: "shared_secret_v1", Secret: "acsec_abcdefghijklmnopqrstuvwxyz0123456789"}
	_, err := service.RefreshAdminSessionWithClient(context.Background(), RefreshCommand{RefreshToken: "adm_rt_old", Transport: TransportBearer, ControlledClient: proof, TraceID: "trace"})
	if err != nil {
		t.Fatal(err)
	}
	if repo.rotations != 1 || repo.stored.ControlledClientID != proof.ClientID || repo.stored.ControlledCredentialID != proof.CredentialID {
		t.Fatalf("refresh binding not propagated: %+v", repo.stored)
	}
}

func TestBearerKillSwitchRejectsExistingAccessAndRefreshSessions(t *testing.T) {
	repo := &repositoryFake{
		stored: StoredSession{
			SessionID:        "session",
			UserID:           "usr-1",
			Transport:        TransportBearer,
			AuthTime:         time.Now().UTC(),
			AccessExpiresAt:  time.Now().Add(time.Minute),
			RefreshExpiresAt: time.Now().Add(time.Hour),
		},
	}
	service := newTestService(t, repo, accessFake{})

	if _, err := service.CurrentAdminSession(context.Background(), "adm_at_existing"); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("existing bearer access error = %v", err)
	}
	if _, err := service.RefreshAdminSessionWithClient(context.Background(), RefreshCommand{
		RefreshToken: "adm_rt_existing",
		Transport:    TransportBearer,
	}); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("existing bearer refresh error = %v", err)
	}
	if repo.rotations != 0 {
		t.Fatalf("disabled bearer transport attempted %d token rotations", repo.rotations)
	}
}

func TestControlledClientSecretDigestIsDomainAndCredentialBound(t *testing.T) {
	repo := &repositoryFake{}
	service := newBearerTestService(t, repo, accessFake{})
	first := service.controlledClientSecretDigest("acli_one00000", "acred_one00000", "same-secret-value-with-at-least-32-bytes")
	second := service.controlledClientSecretDigest("acli_two00000", "acred_one00000", "same-secret-value-with-at-least-32-bytes")
	third := service.controlledClientSecretDigest("acli_one00000", "acred_two00000", "same-secret-value-with-at-least-32-bytes")
	if bytes.Equal(first, second) || bytes.Equal(first, third) {
		t.Fatal("controlled-client digest was not bound to client and credential identifiers")
	}
}

func TestControlledClientLifecycleCreatesRedactedOfflineOperatorEvents(t *testing.T) {
	repo := &repositoryFake{}
	service := newBearerTestService(t, repo, accessFake{})
	registered, err := service.RegisterControlledAdminClient(context.Background(), RegisterControlledClientCommand{DisplayName: "Build Automation", ClientType: "automation"})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := service.RotateControlledAdminClientCredential(context.Background(), RotateControlledClientCredentialCommand{ClientID: registered.ClientID})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DisableControlledAdminClient(context.Background(), registered.ClientID); err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeControlledAdminClientCredential(context.Background(), registered.ClientID, rotated.CredentialID); err != nil {
		t.Fatal(err)
	}

	events := []struct {
		name   string
		event  OutboxEvent
		action string
		secret string
	}{
		{name: "register", event: repo.registeredClient.OutboxEvent, action: "identity.admin_client_registered.v1", secret: registered.Secret},
		{name: "rotate", event: repo.registeredCredential.OutboxEvent, action: "identity.admin_client_credential_rotated.v1", secret: rotated.Secret},
		{name: "disable", event: repo.disabledEvent, action: "identity.admin_client_disabled.v1"},
		{name: "revoke", event: repo.revokedEvent, action: "identity.admin_client_credential_revoked.v1"},
	}
	for _, test := range events {
		t.Run(test.name, func(t *testing.T) {
			payload := test.event.Payload
			if test.event.EventID == "" || payload.AuditID == "" || payload.TraceID == "" {
				t.Fatalf("missing generated identifiers: %+v", test.event)
			}
			if payload.ActorID != "offline_operator" || payload.Action != test.action || payload.Result != "success" || payload.RiskLevel != "high" {
				t.Fatalf("unexpected security event: %+v", payload)
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			encoded := string(raw)
			if strings.Contains(encoded, "secret") || strings.Contains(encoded, "digest") || (test.secret != "" && strings.Contains(encoded, test.secret)) {
				t.Fatalf("security event leaked credential material: %s", encoded)
			}
		})
	}
}

func TestNoAdminScopeUsesGenericInvalidCredentials(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	repo := &repositoryFake{credential: Credential{UserID: "usr-1", DisplayName: "Admin", AccountStatus: "active", PasswordHash: hash}}
	service := newTestService(t, repo, accessFake{err: accesscontrol.ErrNoActiveScope})
	_, err := service.LoginAdmin(context.Background(), LoginCommand{Identifier: "admin@example.com", Credential: "correct-password", Requested: TransportCookie, Source: "127.0.0.1"})
	if !errors.Is(err, ErrInvalidCredentials) || repo.failures != 1 {
		t.Fatalf("expected generic failure, got %v", err)
	}
}

func TestRefreshReplayIsPropagated(t *testing.T) {
	repo := &repositoryFake{rotateErr: ErrRefreshReplayed}
	service := newTestService(t, repo, accessFake{})
	_, err := service.RefreshAdminSession(context.Background(), "adm_rt_old", TransportCookie, "trace")
	if !errors.Is(err, ErrRefreshReplayed) {
		t.Fatalf("expected replay error, got %v", err)
	}
}

type randomFailureReader struct{ err error }

func (r randomFailureReader) Read([]byte) (int, error) { return 0, r.err }

type failAtRead struct {
	failAt int
	calls  int
	err    error
}

func (r *failAtRead) Read(value []byte) (int, error) {
	r.calls++
	if r.calls == r.failAt {
		return 0, r.err
	}
	for index := range value {
		value[index] = byte(r.calls)
	}
	return len(value), nil
}

func TestRandomFailurePreventsIdentityWrites(t *testing.T) {
	sourceErr := errors.New("random source unavailable")
	validHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		setup func(*repositoryFake)
		run   func(context.Context, *Service) error
		calls func(*repositoryFake) int
	}{
		{
			name: "login session",
			setup: func(repo *repositoryFake) {
				repo.credential = Credential{UserID: "usr-1", DisplayName: "Admin", AccountStatus: "active", PasswordHash: validHash}
			},
			run: func(ctx context.Context, service *Service) error {
				_, err := service.LoginAdmin(ctx, LoginCommand{Identifier: "admin@example.com", Credential: "correct-password", Requested: TransportCookie, Source: "127.0.0.1"})
				return err
			},
			calls: func(repo *repositoryFake) int { return repo.creates },
		},
		{
			name: "login failure event",
			setup: func(repo *repositoryFake) {
				repo.findErr = ErrNotFound
			},
			run: func(ctx context.Context, service *Service) error {
				_, err := service.LoginAdmin(ctx, LoginCommand{Identifier: "missing@example.com", Credential: "wrong-password", Requested: TransportCookie, Source: "127.0.0.1"})
				return err
			},
			calls: func(repo *repositoryFake) int { return repo.failures },
		},
		{
			name:  "refresh rotation",
			setup: func(*repositoryFake) {},
			run: func(ctx context.Context, service *Service) error {
				_, err := service.RefreshAdminSession(ctx, "adm_rt_old", TransportCookie, "trace")
				return err
			},
			calls: func(repo *repositoryFake) int { return repo.rotations },
		},
		{
			name:  "logout revocation",
			setup: func(*repositoryFake) {},
			run: func(ctx context.Context, service *Service) error {
				return service.LogoutAdmin(ctx, "adm_at_old", "", "trace", false)
			},
			calls: func(repo *repositoryFake) int { return repo.revokes },
		},
		{
			name:  "bootstrap identity",
			setup: func(*repositoryFake) {},
			run: func(ctx context.Context, service *Service) error {
				_, err := service.BootstrapAdminIdentity(ctx, "admin@example.com", "Admin", []byte("long-password"))
				return err
			},
			calls: func(repo *repositoryFake) int { return repo.bootstraps },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &repositoryFake{}
			test.setup(repo)
			service := newTestService(t, repo, accessFake{})
			service.secrets = securevalue.NewGenerator(randomFailureReader{err: sourceErr})

			err := test.run(context.Background(), service)
			if !errors.Is(err, sourceErr) {
				t.Fatalf("expected random source error, got %v", err)
			}
			if calls := test.calls(repo); calls != 0 {
				t.Fatalf("repository write called %d times after random failure", calls)
			}
		})
	}
}

func TestSessionSecretGenerationPropagatesEveryRandomFailure(t *testing.T) {
	sourceErr := errors.New("random source unavailable")
	for failAt := 1; failAt <= 7; failAt++ {
		t.Run(fmt.Sprintf("step_%d", failAt), func(t *testing.T) {
			reader := &failAtRead{failAt: failAt, err: sourceErr}
			values := make([]string, 7)
			var err error
			values[0], values[1], values[2], values[3], values[4], values[5], values[6], err = generateSessionSecrets(securevalue.NewGenerator(reader), TransportCookie)
			if !errors.Is(err, sourceErr) {
				t.Fatalf("expected source error at step %d, got %v", failAt, err)
			}
			for index, value := range values {
				if value != "" {
					t.Fatalf("value %d leaked partial secret %q after failure", index, value)
				}
			}
		})
	}
}

type auditPortFake struct {
	events []SecurityEvent
}

func (a *auditPortFake) AppendSecurityEvent(_ context.Context, event SecurityEvent) (string, error) {
	a.events = append(a.events, event)
	return event.AuditID, nil
}

func TestOutboxDispatcherUsesIdentityAuditPort(t *testing.T) {
	event := SecurityEvent{AuditID: "aud-1", Action: "admin.auth.login_succeeded", ActorID: "usr-1"}
	repo := &repositoryFake{claimed: []ClaimedOutboxEvent{{EventID: "evt-1", Payload: event}}}
	auditPort := &auditPortFake{}
	dispatcher := NewOutboxDispatcher(repo, auditPort, slog.Default())
	dispatcher.dispatch(context.Background())

	if len(auditPort.events) != 1 || auditPort.events[0].AuditID != event.AuditID {
		t.Fatalf("unexpected audit events: %+v", auditPort.events)
	}
	if len(repo.published) != 1 || repo.published[0] != "evt-1" {
		t.Fatalf("unexpected published events: %+v", repo.published)
	}
}
