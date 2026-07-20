package g2a06acceptance_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"platform.local/capability-platform/backend/internal/modules/hostedinteraction"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	"platform.local/capability-platform/backend/internal/testsupport/g2a06acceptance"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPrepareAndCleanupRealPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for the real PostgreSQL acceptance test")
	}
	if g2a06acceptance.ValidateDatabaseURL(dsn) != nil {
		t.Fatal("TEST_DATABASE_URL must be loopback platform_test_control")
	}
	database, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	root := repositoryRoot(t)
	password := []byte("g2a06 CI acceptance password 0001")
	defer clear(password)
	cfg := formalConfig(t, t.TempDir(), dsn)
	options := g2a06acceptance.Options{RepositoryRoot: root, Password: password, AdminTokenPepper: cfg.AdminAuth.TokenPepper, UserAuth: cfg.UserAuth, HostedInteraction: cfg.HostedInteraction, AcceptanceFixture: true}
	result, err := g2a06acceptance.Prepare(context.Background(), database, options)
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthInteractionID == "" || result.AccountInteractionID == "" || result.AuthInteractionID == result.AccountInteractionID || result.AuthState == "" || result.NegativeAuthState == "" || result.AccountState == "" || result.AccountClientSessionID == "" || result.AccountClientToken == "" || result.AccountApplicationID == "" || result.AccountUserSessionID == "" {
		t.Fatalf("result=%+v", result)
	}
	assertURL(t, result.AuthURL, "/ui/v1/auth", result.AuthInteractionID)
	assertURL(t, result.AccountURL, "/ui/v1/account", result.AccountInteractionID)
	assertFormalConfigCompatibility(t, database, cfg, result.AuthInteractionID)
	var accountUser, accountSession, accountApplication, status string
	if err = database.QueryRow(context.Background(), `SELECT initiator_user_id,initiator_user_session_id,application_id,status FROM hosted_interaction.interactions WHERE interaction_id=$1`, result.AccountInteractionID).Scan(&accountUser, &accountSession, &accountApplication, &status); err != nil {
		t.Fatal(err)
	}
	if accountUser != result.UserID || accountSession != result.AccountUserSessionID || accountApplication != result.AccountApplicationID || status != "created" {
		t.Fatalf("account actor=(%q,%q,%q,%q)", accountUser, accountSession, accountApplication, status)
	}
	var clientActor, accountChannel, storedTokenDigest string
	if err = database.QueryRow(context.Background(), `SELECT a.initiator_client_session_id,b.channel,s.token_digest FROM hosted_interaction.interactions a JOIN hosted_interaction.interactions b ON b.interaction_id=$2 JOIN product.client_sessions s ON s.session_id=a.initiator_client_session_id WHERE a.interaction_id=$1`, result.AuthInteractionID, result.AccountInteractionID).Scan(&clientActor, &accountChannel, &storedTokenDigest); err != nil {
		t.Fatal(err)
	}
	if clientActor != result.ClientSessionID || accountChannel != "desktop" || storedTokenDigest == "" || storedTokenDigest == result.ClientToken {
		t.Fatalf("client=%q channel=%q digest=%q", clientActor, accountChannel, storedTokenDigest)
	}
	cleanup := g2a06acceptance.CleanupCommand{AuthInteractionID: result.AuthInteractionID, NegativeAuthInteractionID: result.NegativeAuthInteractionID, AccountInteractionID: result.AccountInteractionID, ClientSessionID: result.ClientSessionID, AccountClientSessionID: result.AccountClientSessionID, ProductID: result.ProductID, ApplicationID: result.ApplicationID, AccountApplicationID: result.AccountApplicationID, TenantID: result.TenantID, UserID: result.UserID, UserSessionID: result.UserSessionID, AccountUserSessionID: result.AccountUserSessionID}
	swapped := cleanup
	swapped.AccountInteractionID = result.AuthInteractionID
	if err = g2a06acceptance.Cleanup(context.Background(), database, options, swapped); err == nil {
		t.Fatal("cleanup accepted swapped interaction IDs")
	}
	if err = g2a06acceptance.Cleanup(context.Background(), database, options, cleanup); err != nil {
		t.Fatal(err)
	}
	if err = g2a06acceptance.Cleanup(context.Background(), database, options, cleanup); err != nil {
		t.Fatalf("terminal cleanup must be idempotent: %v", err)
	}
	for _, id := range []string{result.AuthInteractionID, result.NegativeAuthInteractionID, result.AccountInteractionID} {
		if err = database.QueryRow(context.Background(), `SELECT status FROM hosted_interaction.interactions WHERE interaction_id=$1`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "cancelled" {
			t.Fatalf("%s status=%s", id, status)
		}
	}
	expired, err := g2a06acceptance.Prepare(context.Background(), database, options)
	if err != nil {
		t.Fatal(err)
	}
	expiredCleanup := g2a06acceptance.CleanupCommand{AuthInteractionID: expired.AuthInteractionID, NegativeAuthInteractionID: expired.NegativeAuthInteractionID, AccountInteractionID: expired.AccountInteractionID, ClientSessionID: expired.ClientSessionID, AccountClientSessionID: expired.AccountClientSessionID, ProductID: expired.ProductID, ApplicationID: expired.ApplicationID, AccountApplicationID: expired.AccountApplicationID, TenantID: expired.TenantID, UserID: expired.UserID, UserSessionID: expired.UserSessionID, AccountUserSessionID: expired.AccountUserSessionID}
	if _, err = database.Exec(context.Background(), `UPDATE hosted_interaction.interactions SET status='expired',result_kind='failed',failure_code='hosted.interaction_expired',terminal_at=clock_timestamp(),version=version+1 WHERE interaction_id IN ($1,$2,$3)`, expired.AuthInteractionID, expired.NegativeAuthInteractionID, expired.AccountInteractionID); err != nil {
		t.Fatal(err)
	}
	if err = g2a06acceptance.Cleanup(context.Background(), database, options, expiredCleanup); err != nil {
		t.Fatalf("expired cleanup: %v", err)
	}
	for _, id := range []string{expired.AuthInteractionID, expired.NegativeAuthInteractionID, expired.AccountInteractionID} {
		if err = database.QueryRow(context.Background(), `SELECT status FROM hosted_interaction.interactions WHERE interaction_id=$1`, id).Scan(&status); err != nil || status != "expired" {
			t.Fatalf("expired status id=%s got=%q err=%v", id, status, err)
		}
	}
}

func assertFormalConfigCompatibility(t *testing.T, database *pgxpool.Pool, cfg config.Config, interactionID string) {
	t.Helper()
	var route, productID, applicationID, tenantID, environment, channel, traceID string
	var actorKind, clientSessionID, userID, userSessionID, keyRef string
	var ciphertext, stateDigest, actorDigest []byte
	err := database.QueryRow(context.Background(), `SELECT i.route_id,i.product_id,i.application_id,COALESCE(i.tenant_id,''),i.environment,i.channel,i.trace_id,i.initiator_kind,COALESCE(i.initiator_client_session_id,''),COALESCE(i.initiator_user_id,''),COALESCE(i.initiator_user_session_id,''),i.state_protector_key_ref,i.state_ciphertext,i.state_digest,r.actor_digest FROM hosted_interaction.interactions i JOIN hosted_interaction.idempotency_records r ON r.interaction_id=i.interaction_id AND r.operation='create' WHERE i.interaction_id=$1`, interactionID).Scan(&route, &productID, &applicationID, &tenantID, &environment, &channel, &traceID, &actorKind, &clientSessionID, &userID, &userSessionID, &keyRef, &ciphertext, &stateDigest, &actorDigest)
	if err != nil {
		t.Fatal(err)
	}
	derivedStateKey := sha256.Sum256([]byte("hosted-interaction.state-key.v1\x00" + cfg.HostedInteraction.StateKey))
	protector, err := hostedinteraction.NewAEADStateProtector(cfg.HostedInteraction.StateKeyRef, formalStateResolver{reference: cfg.HostedInteraction.StateKeyRef, key: derivedStateKey})
	if err != nil {
		t.Fatal(err)
	}
	tenant := tenantID
	scope := hostedinteraction.Scope{ProductID: productID, ApplicationID: applicationID, TenantID: &tenant, Environment: environment, Channel: hostedinteraction.Channel(channel)}
	revealed, err := protector.Reveal(context.Background(), hostedinteraction.StateContext{InteractionID: interactionID, Route: hostedinteraction.Route(route), Scope: scope, TraceID: traceID}, hostedinteraction.ProtectedState{KeyRef: keyRef, Ciphertext: ciphertext, Digest: stateDigest})
	if err != nil || revealed == "" {
		t.Fatalf("formal state derivation cannot decrypt fixture state: %v", err)
	}
	digester, err := securevalue.NewHasher(cfg.HostedInteraction.DigestKey)
	if err != nil {
		t.Fatal(err)
	}
	actorKey := strings.Join([]string{actorKind, clientSessionID, userID, userSessionID, productID, applicationID, tenantID, environment, channel, route}, "\x00")
	expectedActorDigest := digester.Digest("hosted-interaction.v1\x00actor\x00" + actorKey)
	if !bytes.Equal(expectedActorDigest, actorDigest) {
		t.Fatal("fixture digest does not match formal server derivation")
	}
}

type formalStateResolver struct {
	reference string
	key       [sha256.Size]byte
}

func (r formalStateResolver) ResolveSecret(_ context.Context, reference string) ([]byte, error) {
	if reference != r.reference {
		return nil, errors.New("unexpected formal state key reference")
	}
	return append([]byte(nil), r.key[:]...), nil
}

func formalConfig(t *testing.T, root, dsn string) config.Config {
	t.Helper()
	pepper := strings.Repeat("g2a06-ci-admin-pepper-", 3)
	outputTargets, err := json.Marshal([]config.AssemblyOutputTarget{{Reference: "workspace.g2a06-acceptance", Environment: "test", DisplayName: "G2A-06 acceptance", Summary: "Controlled local browser acceptance target", IsDefault: true, TargetRoot: filepath.Join(root, "target"), ArtifactRoot: filepath.Join(root, "artifacts")}})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"PLATFORM_ENVIRONMENT": "local", "PLATFORM_DATABASE_URL": dsn, "PLATFORM_ADMIN_TOKEN_PEPPER": pepper, "PLATFORM_ASSEMBLY_OUTPUT_TARGETS": string(outputTargets)}
	cfg, err := config.Load(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
	pepper = ""
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
func assertURL(t *testing.T, raw, path, id string) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "127.0.0.1:5175" || u.Path != path || len(u.Query()) != 1 || u.Query().Get("interaction_id") != id {
		t.Fatalf("url=%q", raw)
	}
}
func repositoryRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for current := wd; ; current = filepath.Dir(current) {
		if _, err = os.Stat(filepath.Join(current, "platform", "backend", "go.mod")); err == nil {
			return current
		}
		if filepath.Dir(current) == current {
			t.Fatal("root not found")
		}
	}
}
func TestDatabaseURLIsLoopbackControlOnly(t *testing.T) {
	postgresURL := "postgres" + "://u:p@127.0.0.1:15432/platform_test_control"
	postgresqlURL := "postgresql" + "://u:p@localhost/platform_test_control"
	for _, v := range []string{postgresURL, postgresqlURL} {
		if err := g2a06acceptance.ValidateDatabaseURL(v); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []string{"postgres" + "://u:p@example.com/platform_test_control", "postgres" + "://u:p@127.0.0.1/production", "https://127.0.0.1/platform_test_control"} {
		if err := g2a06acceptance.ValidateDatabaseURL(v); err == nil {
			t.Fatalf("accepted %s", v)
		}
	}
}
