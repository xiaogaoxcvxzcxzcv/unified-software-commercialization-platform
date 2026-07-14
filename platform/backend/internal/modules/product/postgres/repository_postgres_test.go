package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/product"
	productpostgres "platform.local/capability-platform/backend/internal/modules/product/postgres"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestProductProvisioningIsIdempotentAndCreatesScopedContexts(t *testing.T) {
	database := testpostgres.Open(t)
	service := testService(productpostgres.New(database.Pool))
	ctx := context.Background()

	command := product.BeginProvisioningCommand{ProductCode: "video-brain", Name: "Video Brain", Status: "active", ActorID: "admin-1", IdempotencyKey: "create-video-brain", TraceID: "trace-create-video-brain"}
	first, err := service.BeginProvisioning(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.BeginProvisioning(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if first.ProductID != second.ProductID || first.ProvisioningState != "pending" {
		t.Fatalf("idempotent products = %#v and %#v", first, second)
	}
	changed := command
	changed.Name = "Changed"
	if _, err := service.BeginProvisioning(ctx, changed); !errors.Is(err, product.ErrIdempotencyConflict) {
		t.Fatalf("changed idempotent request error = %v", err)
	}

	completed, err := service.CompleteProvisioning(ctx, product.CompleteProvisioningCommand{ProductID: first.ProductID, OfficialTenantID: "tenant-official-1", ActorID: "admin-1", IdempotencyKey: "complete-video-brain", TraceID: "trace-complete-video-brain"})
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := service.CompleteProvisioning(ctx, product.CompleteProvisioningCommand{ProductID: first.ProductID, OfficialTenantID: "tenant-official-1", ActorID: "admin-1", IdempotencyKey: "complete-video-brain", TraceID: "trace-complete-video-brain"})
	if err != nil {
		t.Fatal(err)
	}
	if completed.ProvisioningState != "ready" || completed.OfficialTenantID != "tenant-official-1" || repeated.ProductID != completed.ProductID {
		t.Fatalf("completed products = %#v and %#v", completed, repeated)
	}
	items, err := service.ListProducts(ctx, 20)
	if err != nil || len(items) != 1 {
		t.Fatalf("ListProducts() = %v, %v", items, err)
	}
	loaded, err := service.GetProduct(ctx, completed.ProductID)
	if err != nil || loaded.ProductCode != "video-brain" {
		t.Fatalf("GetProduct() = %#v, %v", loaded, err)
	}
}

func TestClientCredentialsAreProductScopedRotatedAndRevoked(t *testing.T) {
	database := testpostgres.Open(t)
	service := testService(productpostgres.New(database.Pool))
	ctx := context.Background()
	firstProduct := provisionProduct(t, service, "product-a")
	secondProduct := provisionProduct(t, service, "product-b")
	now := testNow()
	digestOne := "sha256:" + strings.Repeat("1", 64)
	authentication, err := service.RegisterClient(ctx, product.RegisterClientCommand{
		ProductID: firstProduct.ProductID, Environment: "test", ProofType: "hmac_sha256_v1", ProofDigest: digestOne,
		NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), ActorID: "admin-1", IdempotencyKey: "register-client-a", TraceID: "trace-register-client-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if authentication.Credential.ProofDigest != digestOne || authentication.Context.ProductID != firstProduct.ProductID {
		t.Fatalf("authentication = %#v", authentication)
	}
	if _, err := service.RotateClientCredential(ctx, product.RotateClientCredentialCommand{
		ProductID: secondProduct.ProductID, ClientID: authentication.Client.ClientID, ExpectedGeneration: 1,
		ProofType: "hmac_sha256_v1", ProofDigest: "sha256:" + strings.Repeat("2", 64), NotBefore: now, ExpiresAt: now.Add(time.Hour),
		ActorID: "admin-1", IdempotencyKey: "wrong-product-rotate", TraceID: "trace-wrong-product-rotate",
	}); !errors.Is(err, product.ErrNotFound) {
		t.Fatalf("cross-product rotate error = %v", err)
	}
	rotated, err := service.RotateClientCredential(ctx, product.RotateClientCredentialCommand{
		ProductID: firstProduct.ProductID, ClientID: authentication.Client.ClientID, ExpectedGeneration: 1,
		ProofType: "hmac_sha256_v1", ProofDigest: "sha256:" + strings.Repeat("2", 64), NotBefore: now, ExpiresAt: now.Add(time.Hour),
		ActorID: "admin-1", IdempotencyKey: "rotate-client-a", TraceID: "trace-rotate-client-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Generation != 2 {
		t.Fatalf("generation = %d", rotated.Generation)
	}
	if _, err := service.ResolveProductContext(ctx, authentication.Client.ClientID, authentication.Credential.CredentialID); !errors.Is(err, product.ErrCredentialUnavailable) {
		t.Fatalf("old credential resolve error = %v", err)
	}
	if _, err := service.RotateClientCredential(ctx, product.RotateClientCredentialCommand{
		ProductID: firstProduct.ProductID, ClientID: authentication.Client.ClientID, ExpectedGeneration: 1,
		ProofType: "hmac_sha256_v1", ProofDigest: "sha256:" + strings.Repeat("3", 64), NotBefore: now, ExpiresAt: now.Add(time.Hour),
		ActorID: "admin-1", IdempotencyKey: "stale-rotate-client-a", TraceID: "trace-stale-rotate-client-a",
	}); !errors.Is(err, product.ErrCredentialVersionConflict) {
		t.Fatalf("stale rotate error = %v", err)
	}
	revoked, err := service.RevokeClientCredential(ctx, product.RevokeClientCredentialCommand{ProductID: firstProduct.ProductID, ClientID: authentication.Client.ClientID, CredentialID: rotated.CredentialID, ActorID: "admin-1", IdempotencyKey: "revoke-client-a", TraceID: "trace-revoke-client-a"})
	if err != nil || revoked.Status != "revoked" {
		t.Fatalf("revoke = %#v, %v", revoked, err)
	}
	if _, err := service.ResolveProductContext(ctx, authentication.Client.ClientID, rotated.CredentialID); !errors.Is(err, product.ErrCredentialUnavailable) {
		t.Fatalf("revoked credential resolve error = %v", err)
	}
	var storedDigest string
	if err := database.Pool.QueryRow(ctx, `SELECT proof_digest FROM product.product_client_credentials WHERE credential_id=$1`, authentication.Credential.CredentialID).Scan(&storedDigest); err != nil {
		t.Fatal(err)
	}
	if storedDigest != digestOne {
		t.Fatalf("stored digest = %q", storedDigest)
	}
}

func TestClientSessionPersistsOnlyDigestRejectsNonceReplayAndPublishesOutbox(t *testing.T) {
	database := testpostgres.Open(t)
	repository := productpostgres.New(database.Pool)
	service := testService(repository)
	ctx := context.Background()
	createdProduct := provisionProduct(t, service, "session-product")
	now := testNow()
	authentication, err := service.RegisterClient(ctx, product.RegisterClientCommand{
		ProductID: createdProduct.ProductID, Environment: "test", ProofType: "hmac_sha256_v1", ProofDigest: "sha256:" + strings.Repeat("4", 64),
		NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), ActorID: "admin-1", IdempotencyKey: "register-session-client", TraceID: "trace-register-session-client",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := product.CreateClientSessionCommand{
		ClientID: authentication.Client.ClientID, CredentialID: authentication.Credential.CredentialID,
		Proof: product.ClientProof{Type: "hmac_sha256_v1", Value: "valid-proof"}, RequestNonce: "nonce-value-at-least-16", ClientVersion: "1.2.3",
		Scope: product.ResolvedSessionScope{ProductID: createdProduct.ProductID, Environment: "test", ApplicationID: "app-1", TenantID: "tenant-official-1", ApplicationContextVersion: 2, TenantContextVersion: 3},
		TTL:   15 * time.Minute, TraceID: "trace-session-1",
	}
	issued, err := service.CreateClientSession(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if issued.Token != "client-session-plain" {
		t.Fatalf("token = %q", issued.Token)
	}
	var storedDigest string
	if err := database.Pool.QueryRow(ctx, `SELECT token_digest FROM product.client_sessions WHERE session_id=$1`, issued.SessionID).Scan(&storedDigest); err != nil {
		t.Fatal(err)
	}
	if storedDigest != "sha256:"+strings.Repeat("5", 64) || storedDigest == issued.Token {
		t.Fatalf("stored token digest = %q", storedDigest)
	}
	if _, err := service.CreateClientSession(ctx, command); !errors.Is(err, product.ErrNonceReplayed) {
		t.Fatalf("nonce replay error = %v", err)
	}
	loaded, err := service.FindClientSession(ctx, storedDigest)
	if err != nil || loaded.ProductID != createdProduct.ProductID || loaded.ApplicationID != "app-1" || loaded.TenantID != "tenant-official-1" {
		t.Fatalf("loaded session = %#v, %v", loaded, err)
	}
	if err := service.RevokeClientSession(ctx, issued.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.FindClientSession(ctx, storedDigest); !errors.Is(err, product.ErrSessionUnavailable) {
		t.Fatalf("revoked session error = %v", err)
	}

	claimed, err := service.ClaimOutbox(ctx, now.Add(time.Minute), 100)
	if err != nil || len(claimed) == 0 {
		t.Fatalf("ClaimOutbox() = %v, %v", claimed, err)
	}
	foundCreated, foundDenied := false, false
	for _, event := range claimed {
		if event.EventType == "client.session_created.v1" {
			foundCreated = true
			if event.Payload.Action != "client.session_created" || event.Payload.TraceID != command.TraceID || event.Payload.ProductID != createdProduct.ProductID {
				t.Fatalf("created event payload = %#v", event.Payload)
			}
			if err := service.MarkOutboxPublished(ctx, event.EventID, now.Add(2*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		if event.EventType == "client.session_denied.v1" {
			foundDenied = true
			if event.Payload.Result != "denied" || event.Payload.ReasonCode != "nonce_replayed" || event.Payload.TraceID != command.TraceID {
				t.Fatalf("denied event payload = %#v", event.Payload)
			}
			if err := service.MarkOutboxFailed(ctx, event.EventID, "temporary audit failure", now.Add(3*time.Minute), false); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !foundCreated || !foundDenied {
		t.Fatalf("session outbox events missing: created=%v denied=%v", foundCreated, foundDenied)
	}
}

func TestCapabilitySetRequiresTrustedPlanAndUsesOptimisticVersion(t *testing.T) {
	database := testpostgres.Open(t)
	service := testService(productpostgres.New(database.Pool))
	ctx := context.Background()
	createdProduct := provisionProduct(t, service, "capability-product")
	plan := product.TrustedCapabilityChangePlan{
		ProductID: createdProduct.ProductID, SourcePlanID: "plan-1", CatalogRevision: "revision-1",
		CatalogSnapshotSHA256: "sha256:" + strings.Repeat("a", 64),
		Items: []product.CapabilityItem{
			{CapabilityID: "identity.login", Enabled: true, Policy: []byte(`{"mode":"password"}`), SourcePackageID: "package.account", SourcePackageVersion: "1.0.0"},
		},
	}
	first, err := service.ReplaceCapabilitySet(ctx, product.ReplaceCapabilitySetCommand{Plan: plan, ExpectedVersion: 0, ActorID: "admin-1", IdempotencyKey: "capabilities-v1", TraceID: "trace-capabilities-v1"})
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := service.ReplaceCapabilitySet(ctx, product.ReplaceCapabilitySetCommand{Plan: plan, ExpectedVersion: 0, ActorID: "admin-1", IdempotencyKey: "capabilities-v1", TraceID: "trace-capabilities-v1"})
	if err != nil || repeated.CapabilitySetID != first.CapabilitySetID {
		t.Fatalf("idempotent capability set = %#v, %v", repeated, err)
	}
	if _, err := service.ReplaceCapabilitySet(ctx, product.ReplaceCapabilitySetCommand{Plan: plan, ExpectedVersion: 0, ActorID: "admin-1", IdempotencyKey: "stale-capabilities", TraceID: "trace-stale-capabilities"}); !errors.Is(err, product.ErrCapabilityVersionConflict) {
		t.Fatalf("stale capability version error = %v", err)
	}
	current, err := service.CurrentCapabilitySet(ctx, createdProduct.ProductID)
	if err != nil || current.Version != 1 || len(current.Items) != 1 || current.Items[0].CapabilityID != "identity.login" {
		t.Fatalf("current capability set = %#v, %v", current, err)
	}
}

func TestConcurrentCapabilityWritersOnlyOneWinsExpectedVersion(t *testing.T) {
	database := testpostgres.Open(t)
	service := testService(productpostgres.New(database.Pool))
	createdProduct := provisionProduct(t, service, "concurrent-product")
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for index := 0; index < 2; index++ {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			plan := product.TrustedCapabilityChangePlan{ProductID: createdProduct.ProductID, SourcePlanID: fmt.Sprintf("plan-%d", index), CatalogRevision: "revision-1", CatalogSnapshotSHA256: "sha256:" + strings.Repeat("b", 64), Items: []product.CapabilityItem{{CapabilityID: fmt.Sprintf("feature.%d", index), Enabled: true, Policy: []byte(`{}`), SourcePackageID: "package.account", SourcePackageVersion: "1.0.0"}}}
			_, err := service.ReplaceCapabilitySet(context.Background(), product.ReplaceCapabilitySetCommand{Plan: plan, ExpectedVersion: 0, ActorID: "admin-1", IdempotencyKey: fmt.Sprintf("concurrent-%d", index), TraceID: fmt.Sprintf("trace-concurrent-%d", index)})
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, product.ErrCapabilityVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

type allowPlanVerifier struct{}

func (allowPlanVerifier) ResolveProductCapabilityChange(_ context.Context, plan product.TrustedCapabilityChangePlan) (product.TrustedCapabilityChangePlan, error) {
	return plan, nil
}

type allowProofVerifier struct{}

func (allowProofVerifier) VerifyClientProof(_ context.Context, _ product.ClientAuthentication, proof product.ClientProof) error {
	if proof.Value != "valid-proof" {
		return errors.New("invalid proof")
	}
	return nil
}

func testService(repository product.Repository) *product.Service {
	var sequence atomic.Uint64
	ids := func(prefix string) (string, error) {
		return fmt.Sprintf("%s%06d", prefix, sequence.Add(1)), nil
	}
	tokens := func() (string, string, error) {
		return "client-session-plain", "sha256:" + strings.Repeat("5", 64), nil
	}
	return product.NewService(repository, allowPlanVerifier{}, allowProofVerifier{}, ids, tokens, testNow)
}

func provisionProduct(t *testing.T, service *product.Service, code string) product.Product {
	t.Helper()
	ctx := context.Background()
	created, err := service.BeginProvisioning(ctx, product.BeginProvisioningCommand{ProductCode: code, Name: code, Status: "active", ActorID: "admin-1", IdempotencyKey: "begin-" + code, TraceID: "trace-begin-" + code})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := service.CompleteProvisioning(ctx, product.CompleteProvisioningCommand{ProductID: created.ProductID, OfficialTenantID: "official-" + code, ActorID: "admin-1", IdempotencyKey: "complete-" + code, TraceID: "trace-complete-" + code})
	if err != nil {
		t.Fatal(err)
	}
	return completed
}

func testNow() time.Time { return time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC) }
