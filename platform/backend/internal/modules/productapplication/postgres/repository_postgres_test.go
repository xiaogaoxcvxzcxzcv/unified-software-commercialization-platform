package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/productapplication"
	applicationpostgres "platform.local/capability-platform/backend/internal/modules/productapplication/postgres"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

type sequenceIDs struct {
	mu sync.Mutex
	n  int
}

func (s *sequenceIDs) ID(prefix string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return fmt.Sprintf("%s%032d", prefix, s.n), nil
}

func TestRepositoryCreateIsIdempotentAndProductScoped(t *testing.T) {
	database := testpostgres.Open(t)
	service := productapplication.NewService(applicationpostgres.New(database.Pool), &sequenceIDs{}, fixedNow)
	ctx := context.Background()
	productA := productapplication.ProductContext{ProductID: "prod-a", Environment: productapplication.EnvironmentProduction}
	command := createCommand(productA, "desktop", "idem-create-000001")
	created, err := service.CreateApplication(ctx, command)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}
	replayed, err := service.CreateApplication(ctx, command)
	if err != nil || replayed.ApplicationID != created.ApplicationID || replayed.AuditID != created.AuditID {
		t.Fatalf("idempotent replay = %+v, error = %v", replayed, err)
	}
	changed := command
	changed.Name = "Changed"
	if _, err := service.CreateApplication(ctx, changed); !errors.Is(err, productapplication.ErrIdempotencyConflict) {
		t.Fatalf("changed idempotency payload error = %v", err)
	}
	duplicate := createCommand(productA, "desktop", "idem-create-000002")
	if _, err := service.CreateApplication(ctx, duplicate); !errors.Is(err, productapplication.ErrConflict) {
		t.Fatalf("duplicate code error = %v", err)
	}
	productB := productapplication.ProductContext{ProductID: "prod-b", Environment: productapplication.EnvironmentProduction}
	other, err := service.CreateApplication(ctx, createCommand(productB, "desktop", "idem-create-000003"))
	if err != nil {
		t.Fatalf("same code in another product: %v", err)
	}
	items, err := service.ListApplications(ctx, productA)
	if err != nil || len(items) != 1 || items[0].ApplicationID != created.ApplicationID {
		t.Fatalf("product A list = %+v, error = %v", items, err)
	}
	if _, err := service.GetApplication(ctx, productB, created.ApplicationID); !errors.Is(err, productapplication.ErrNotFound) {
		t.Fatalf("cross-product get error = %v", err)
	}
	if _, err := service.GetApplication(ctx, productB, other.ApplicationID); err != nil {
		t.Fatalf("product B own application: %v", err)
	}
}

func TestRepositoryConcurrentIdempotencyReturnsOneApplication(t *testing.T) {
	database := testpostgres.Open(t)
	service := productapplication.NewService(applicationpostgres.New(database.Pool), &sequenceIDs{}, fixedNow)
	command := createCommand(productapplication.ProductContext{ProductID: "prod-a", Environment: productapplication.EnvironmentProduction}, "desktop", "idem-concurrent-0001")
	start := make(chan struct{})
	type result struct {
		application productapplication.Application
		err         error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			application, err := service.CreateApplication(context.Background(), command)
			results <- result{application: application, err: err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.application.ApplicationID != second.application.ApplicationID || first.application.AuditID != second.application.AuditID {
		t.Fatalf("concurrent results = %+v / %+v", first, second)
	}
	var count int
	if err := database.Pool.QueryRow(context.Background(), `SELECT count(*) FROM product_application.product_applications WHERE product_id='prod-a' AND application_code='desktop'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("application count = %d, error = %v", count, err)
	}
}

func TestRepositoryBindingResolutionRedirectsAndSuspend(t *testing.T) {
	database := testpostgres.Open(t)
	repository := applicationpostgres.New(database.Pool)
	service := productapplication.NewService(repository, &sequenceIDs{}, fixedNow)
	ctx := context.Background()
	product := productapplication.ProductContext{ProductID: "prod-a", Environment: productapplication.EnvironmentProduction}
	application, err := service.CreateApplication(ctx, createCommand(product, "web", "idem-create-100001"))
	if err != nil {
		t.Fatal(err)
	}
	client := productapplication.ClientIdentity{ProductID: product.ProductID, ClientID: "client-a", Environment: product.Environment, CredentialType: "ed25519_signature_v1"}
	bind := productapplication.BindClientCommand{Product: product, ApplicationID: application.ApplicationID, Client: client, ActorID: "admin-1", TraceID: "trace-bind", IdempotencyKey: "idem-bind-00000001"}
	binding, err := service.BindClientToApplication(ctx, bind)
	if err != nil {
		t.Fatalf("BindClientToApplication() error = %v", err)
	}
	replayed, err := service.BindClientToApplication(ctx, bind)
	if err != nil || replayed.BindingID != binding.BindingID {
		t.Fatalf("binding replay = %+v, error = %v", replayed, err)
	}
	second, err := service.CreateApplication(ctx, createCommand(product, "mobile", "idem-create-100002"))
	if err != nil {
		t.Fatal(err)
	}
	rebind := bind
	rebind.ApplicationID = second.ApplicationID
	rebind.IdempotencyKey = "idem-bind-00000002"
	if _, err := service.BindClientToApplication(ctx, rebind); !errors.Is(err, productapplication.ErrConflict) {
		t.Fatalf("cross-application client binding error = %v", err)
	}
	resolved, err := service.ResolveApplicationContext(ctx, productapplication.ResolveCommand{Product: product, Client: client, ClientVersion: "1.0.0", ObservedDistributionChannel: "official"})
	if err != nil || resolved.ApplicationID != application.ApplicationID {
		t.Fatalf("resolve = %+v, error = %v", resolved, err)
	}
	wrongProduct := productapplication.ProductContext{ProductID: "prod-b", Environment: product.Environment}
	if _, err := service.ResolveApplicationContext(ctx, productapplication.ResolveCommand{Product: wrongProduct, Client: productapplication.ClientIdentity{ProductID: "prod-b", ClientID: client.ClientID, Environment: product.Environment, CredentialType: client.CredentialType}, ClientVersion: "1.0.0", ObservedDistributionChannel: "official"}); !errors.Is(err, productapplication.ErrContextRejected) {
		t.Fatalf("cross-product resolve error = %v", err)
	}
	redirects := productapplication.ReplaceRedirectsCommand{Product: product, ApplicationID: application.ApplicationID, ActorID: "admin-1", TraceID: "trace-redirect", Policy: productapplication.RedirectPolicy{WebRedirectURIs: []string{"https://example.com/oauth/callback"}, AllowedOrigins: []string{"https://example.com"}, DeepLinks: []productapplication.DeepLinkRule{{Scheme: "videoapp", PathPattern: "/oauth/callback"}}, AuthReturnTargets: []productapplication.AuthReturnTarget{{Code: "login.complete", URI: "https://example.com/oauth/callback"}}}}
	first, err := service.ReplaceRedirects(ctx, redirects)
	if err != nil || first.Version != 1 {
		t.Fatalf("first redirect version = %+v, error = %v", first, err)
	}
	resolvedTarget, err := service.ResolveAuthReturnTarget(ctx, product, application.ApplicationID, "login.complete")
	if err != nil || resolvedTarget.URI != "https://example.com/oauth/callback" || resolvedTarget.Kind != productapplication.AuthReturnTargetWebRedirect || resolvedTarget.PolicyVersion != 1 {
		t.Fatalf("ResolveAuthReturnTarget() = %+v, error = %v", resolvedTarget, err)
	}
	var storedCode, storedValue string
	if err := database.Pool.QueryRow(ctx, `SELECT target_code,value FROM product_application.redirect_policy_entries WHERE policy_id=$1 AND entry_type='auth_return_target'`, first.PolicyID).Scan(&storedCode, &storedValue); err != nil || storedCode != "login.complete" || storedValue != resolvedTarget.URI {
		t.Fatalf("stored auth target code=%q value=%q error=%v", storedCode, storedValue, err)
	}
	if _, err := database.Pool.Exec(ctx, `INSERT INTO product_application.redirect_policy_entries(policy_id,entry_type,target_code,value) VALUES($1,'auth_return_target','orphan.target','https://attacker.example/callback')`, first.PolicyID); err != nil {
		t.Fatalf("seed orphan auth target: %v", err)
	}
	if _, err := service.ResolveAuthReturnTarget(ctx, product, application.ApplicationID, "orphan.target"); !errors.Is(err, productapplication.ErrContextRejected) {
		t.Fatalf("orphan auth target error = %v", err)
	}
	same, err := service.ReplaceRedirects(ctx, redirects)
	if err != nil || same.Version != first.Version || same.PolicyID != first.PolicyID || same.AuditID != first.AuditID {
		t.Fatalf("same redirect version = %+v, error = %v", same, err)
	}
	redirects.Policy.AllowedOrigins = []string{"https://app.example.com"}
	redirects.Policy.AuthReturnTargets = []productapplication.AuthReturnTarget{{Code: "account.complete", URI: "https://example.com/oauth/callback"}}
	secondVersion, err := service.ReplaceRedirects(ctx, redirects)
	if err != nil || secondVersion.Version != 2 {
		t.Fatalf("second redirect version = %+v, error = %v", secondVersion, err)
	}
	if _, err := service.ResolveAuthReturnTarget(ctx, product, application.ApplicationID, "login.complete"); !errors.Is(err, productapplication.ErrNotFound) {
		t.Fatalf("old policy target resolved after replacement: %v", err)
	}
	if target, err := service.ResolveAuthReturnTarget(ctx, product, application.ApplicationID, "account.complete"); err != nil || target.PolicyVersion != 2 {
		t.Fatalf("current policy target = %+v, error = %v", target, err)
	}
	suspend := productapplication.SuspendCommand{Product: product, ApplicationID: application.ApplicationID, Reason: "security maintenance", SessionPolicy: productapplication.SessionPolicyRevokeExisting, ActorID: "admin-1", TraceID: "trace-suspend", IdempotencyKey: "idem-suspend-00001"}
	suspended, err := service.SuspendApplication(ctx, suspend)
	if err != nil || suspended.AffectedClientBindings != 1 || suspended.AuditID == "" {
		t.Fatalf("SuspendApplication() = %+v, error = %v", suspended, err)
	}
	if _, err := service.ResolveApplicationContext(ctx, productapplication.ResolveCommand{Product: product, Client: client, ClientVersion: "1.0.0", ObservedDistributionChannel: "official"}); !errors.Is(err, productapplication.ErrApplicationSuspended) {
		t.Fatalf("resolve suspended error = %v", err)
	}
	if _, err := service.ResolveAuthReturnTarget(ctx, product, application.ApplicationID, "account.complete"); !errors.Is(err, productapplication.ErrApplicationSuspended) {
		t.Fatalf("suspended application auth target error = %v", err)
	}
}

func TestRepositoryOutboxClaimFailureAndPublish(t *testing.T) {
	database := testpostgres.Open(t)
	repository := applicationpostgres.New(database.Pool)
	service := productapplication.NewService(repository, &sequenceIDs{}, fixedNow)
	product := productapplication.ProductContext{ProductID: "prod-a", Environment: productapplication.EnvironmentProduction}
	if _, err := service.CreateApplication(context.Background(), createCommand(product, "desktop", "idem-create-200001")); err != nil {
		t.Fatal(err)
	}
	events, err := service.ClaimOutbox(context.Background(), fixedNow().Add(time.Second), 10)
	if err != nil || len(events) != 1 || events[0].AttemptCount != 1 {
		t.Fatalf("ClaimOutbox() = %+v, error = %v", events, err)
	}
	stale := events[0]
	if _, err := database.Pool.Exec(context.Background(), `UPDATE product_application.outbox_events SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE event_id=$1`, stale.EventID); err != nil {
		t.Fatal(err)
	}
	events, err = service.ClaimOutbox(context.Background(), time.Now(), 10)
	if err != nil || len(events) != 1 || events[0].AttemptCount != 2 || events[0].LeaseToken == stale.LeaseToken {
		t.Fatalf("lease reclaim = %+v, error = %v", events, err)
	}
	if err := service.MarkOutboxPublished(context.Background(), stale.EventID, stale.LeaseToken, time.Now()); !errors.Is(err, productapplication.ErrConflict) {
		t.Fatalf("stale publish error = %v", err)
	}
	if err := service.MarkOutboxFailed(context.Background(), events[0].EventID, events[0].LeaseToken, "temporary audit failure", time.Now().Add(-time.Second), false); err != nil {
		t.Fatal(err)
	}
	events, err = service.ClaimOutbox(context.Background(), fixedNow().Add(2*time.Minute), 10)
	if err != nil || len(events) != 1 || events[0].AttemptCount != 3 {
		t.Fatalf("reclaim = %+v, error = %v", events, err)
	}
	if err := service.MarkOutboxPublished(context.Background(), events[0].EventID, events[0].LeaseToken, time.Now()); err != nil {
		t.Fatal(err)
	}
	events, err = service.ClaimOutbox(context.Background(), fixedNow().Add(4*time.Minute), 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("published event reclaimed = %+v, error = %v", events, err)
	}
}

func createCommand(product productapplication.ProductContext, code, key string) productapplication.CreateCommand {
	return productapplication.CreateCommand{Product: product, ApplicationCode: code, Name: "Application " + code, Platform: productapplication.PlatformWeb, DistributionChannel: "official", ReleaseTrack: productapplication.ReleaseTrackStable, Status: productapplication.StatusActive, ActorID: "admin-1", TraceID: "trace-" + code, IdempotencyKey: key}
}

func fixedNow() time.Time { return time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC) }
