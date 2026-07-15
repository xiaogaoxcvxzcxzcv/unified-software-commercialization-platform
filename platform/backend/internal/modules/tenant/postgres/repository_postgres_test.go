package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/tenant"
	tenantpostgres "platform.local/capability-platform/backend/internal/modules/tenant/postgres"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
)

func TestOfficialTenantIsUniqueAndRetrySafe(t *testing.T) {
	database := testpostgres.Open(t)
	repository := tenantpostgres.New(database.Pool)
	now := time.Date(2026, 7, 13, 13, 0, 0, 0, time.UTC)
	service := tenant.NewService(repository, tenant.WithClock(func() time.Time { return now }), tenant.WithIDGenerator(sequenceGenerator()))
	ctx := context.Background()

	first, err := service.EnsureOfficialTenant(ctx, tenant.EnsureOfficialTenantCommand{
		ProductID: "product-a", Name: "Product A Official", ActorID: "assembly",
		IdempotencyKey: "official-retry-key-0001", TraceID: "trace-official",
	})
	if err != nil {
		t.Fatalf("EnsureOfficialTenant() error = %v", err)
	}
	replayed, err := service.EnsureOfficialTenant(ctx, tenant.EnsureOfficialTenantCommand{
		ProductID: "product-a", Name: "Product A Official", ActorID: "assembly",
		IdempotencyKey: "official-retry-key-0001", TraceID: "trace-official-retry",
	})
	if err != nil {
		t.Fatalf("retry EnsureOfficialTenant() error = %v", err)
	}
	if replayed.TenantID != first.TenantID {
		t.Fatalf("retry tenant = %q, want %q", replayed.TenantID, first.TenantID)
	}

	const workers = 12
	results := make(chan string, workers)
	errorsFound := make(chan error, workers)
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			value, err := service.EnsureOfficialTenant(ctx, tenant.EnsureOfficialTenantCommand{
				ProductID: "product-a", Name: "Product A Official", ActorID: "assembly",
				IdempotencyKey: fmt.Sprintf("official-concurrent-%04d", index), TraceID: "trace-concurrent",
			})
			if err != nil {
				errorsFound <- err
				return
			}
			results <- value.TenantID
		}(i)
	}
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent EnsureOfficialTenant() error = %v", err)
	}
	for tenantID := range results {
		if tenantID != first.TenantID {
			t.Errorf("concurrent official tenant = %q, want %q", tenantID, first.TenantID)
		}
	}

	var tenantCount, outboxCount int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM tenant.product_tenants WHERE product_id='product-a' AND tenant_type='official'`).Scan(&tenantCount); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM tenant.outbox_events WHERE aggregate_id=$1 AND event_type='tenant.created.v1'`, first.TenantID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if tenantCount != 1 || outboxCount != 1 {
		t.Fatalf("official tenants = %d, creation events = %d; want 1/1", tenantCount, outboxCount)
	}
}

func TestAgentTenantIdempotencyAndProductIsolation(t *testing.T) {
	database := testpostgres.Open(t)
	repository := tenantpostgres.New(database.Pool)
	now := time.Date(2026, 7, 13, 14, 0, 0, 0, time.UTC)
	service := tenant.NewService(repository, tenant.WithClock(func() time.Time { return now }), tenant.WithIDGenerator(sequenceGenerator()))
	ctx := context.Background()

	for _, productID := range []string{"product-a", "product-b"} {
		if _, err := service.EnsureOfficialTenant(ctx, tenant.EnsureOfficialTenantCommand{
			ProductID: productID, Name: productID + " official", ActorID: "assembly",
			IdempotencyKey: "official-" + productID + "-0001",
		}); err != nil {
			t.Fatal(err)
		}
	}
	command := tenant.CreateAgentTenantCommand{
		ProductID: "product-a", TenantCode: "partner", Name: "Partner A1", Status: tenant.TenantStatusActive,
		ActorID: "admin-a", IdempotencyKey: "agent-create-key-0001", TraceID: "trace-agent",
	}
	first, err := service.CreateAgentTenant(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.CreateAgentTenant(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if first.TenantID != replayed.TenantID {
		t.Fatalf("replayed tenant = %q, want %q", replayed.TenantID, first.TenantID)
	}
	changed := command
	changed.Name = "Changed payload"
	if _, err := service.CreateAgentTenant(ctx, changed); !errors.Is(err, tenant.ErrIdempotencyConflict) {
		t.Fatalf("changed replay error = %v, want ErrIdempotencyConflict", err)
	}
	if _, err := service.CreateAgentTenant(ctx, tenant.CreateAgentTenantCommand{
		ProductID: "product-a", TenantCode: "partner", Name: "Duplicate code", Status: tenant.TenantStatusActive,
		ActorID: "admin-a", IdempotencyKey: "agent-create-key-0002",
	}); !errors.Is(err, tenant.ErrTenantCodeConflict) {
		t.Fatalf("duplicate product tenant code error = %v", err)
	}
	otherProduct, err := service.CreateAgentTenant(ctx, tenant.CreateAgentTenantCommand{
		ProductID: "product-b", TenantCode: "partner", Name: "Partner B1", Status: tenant.TenantStatusActive,
		ActorID: "admin-b", IdempotencyKey: "agent-create-key-0003",
	})
	if err != nil {
		t.Fatalf("same code in another product error = %v", err)
	}

	productA, err := service.ListTenants(ctx, tenant.ListTenantsCommand{ProductID: "product-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(productA) != 2 {
		t.Fatalf("product-a tenants = %+v, want official + one agent", productA)
	}
	for _, value := range productA {
		if value.ProductID != "product-a" || value.TenantID == otherProduct.TenantID {
			t.Fatalf("cross-product tenant leaked into product-a list: %+v", value)
		}
	}
	var agentCount, eventCount int
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM tenant.product_tenants WHERE product_id='product-a' AND tenant_type='agent'`).Scan(&agentCount); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT count(*) FROM tenant.outbox_events WHERE aggregate_id=$1 AND event_type='tenant.created.v1'`, first.TenantID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if agentCount != 1 || eventCount != 1 {
		t.Fatalf("agent tenants = %d, events = %d; want 1/1", agentCount, eventCount)
	}
}

func TestTenantResolutionIsProductApplicationAndProofScoped(t *testing.T) {
	database := testpostgres.Open(t)
	repository := tenantpostgres.New(database.Pool)
	now := time.Date(2026, 7, 13, 15, 0, 0, 0, time.UTC)
	hasher, err := securevalue.NewHasher("tenant-test-proof-pepper-32-bytes-minimum-value")
	if err != nil {
		t.Fatal(err)
	}
	service := tenant.NewService(repository, tenant.WithClock(func() time.Time { return now }), tenant.WithIDGenerator(sequenceGenerator()), tenant.WithProofDigester(hasher))
	ctx := context.Background()

	official, err := service.EnsureOfficialTenant(ctx, tenant.EnsureOfficialTenantCommand{
		ProductID: "product-a", Name: "Official", ActorID: "assembly", IdempotencyKey: "official-resolve-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	agentOne, err := service.CreateAgentTenant(ctx, tenant.CreateAgentTenantCommand{
		ProductID: "product-a", TenantCode: "agent-one", Name: "Agent One", Status: tenant.TenantStatusActive,
		ActorID: "admin-a", IdempotencyKey: "agent-resolve-key-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	agentTwo, err := service.CreateAgentTenant(ctx, tenant.CreateAgentTenantCommand{
		ProductID: "product-a", TenantCode: "agent-two", Name: "Agent Two", Status: tenant.TenantStatusSuspended,
		ActorID: "admin-a", IdempotencyKey: "agent-resolve-key-0002",
	})
	if err != nil {
		t.Fatal(err)
	}
	proofOne := "high-entropy-proof-one"
	proofTwo := "high-entropy-proof-two"
	bindings := []struct{ id, tenantID, applicationID, proof string }{
		{"binding-one", agentOne.TenantID, "app-a", proofOne},
		{"binding-two", agentTwo.TenantID, "app-a", proofTwo},
	}
	for _, binding := range bindings {
		digest := "hmac-sha256:" + hasher.DigestHex(binding.proof)
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO tenant.distribution_bindings(
				binding_id,product_id,tenant_id,application_id,channel_code,proof_subject_digest,status,created_at,updated_at
			) VALUES($1,'product-a',$2,$3,'partner',$4,'active',$5,$5)`,
			binding.id, binding.tenantID, binding.applicationID, digest, now); err != nil {
			t.Fatal(err)
		}
	}

	officialContext, err := service.ResolveTenantContext(ctx, tenant.ResolveTenantContextCommand{ProductID: "product-a", Method: tenant.ResolutionOfficialChannel})
	if err != nil || officialContext.TenantID != official.TenantID {
		t.Fatalf("official context = %+v, err = %v", officialContext, err)
	}
	agentContext, err := service.ResolveTenantContext(ctx, tenant.ResolveTenantContextCommand{
		ProductID: "product-a", ApplicationID: "app-a", Method: tenant.ResolutionDistribution,
		ChannelCode: "partner", ProofSubject: proofOne,
	})
	if err != nil || agentContext.TenantID != agentOne.TenantID {
		t.Fatalf("agent context = %+v, err = %v", agentContext, err)
	}
	for name, command := range map[string]tenant.ResolveTenantContextCommand{
		"other product":      {ProductID: "product-b", ApplicationID: "app-a", Method: tenant.ResolutionDistribution, ChannelCode: "partner", ProofSubject: proofOne},
		"other application":  {ProductID: "product-a", ApplicationID: "app-b", Method: tenant.ResolutionDistribution, ChannelCode: "partner", ProofSubject: proofOne},
		"other tenant proof": {ProductID: "product-a", ApplicationID: "app-a", Method: tenant.ResolutionDistribution, ChannelCode: "partner", ProofSubject: "not-a-proof"},
	} {
		if _, err := service.ResolveTenantContext(ctx, command); !errors.Is(err, tenant.ErrInvalidTenantProof) {
			t.Errorf("%s resolution error = %v, want ErrInvalidTenantProof", name, err)
		}
	}
	if _, err := service.ResolveTenantContext(ctx, tenant.ResolveTenantContextCommand{
		ProductID: "product-a", ApplicationID: "app-a", Method: tenant.ResolutionDistribution,
		ChannelCode: "partner", ProofSubject: proofTwo,
	}); !errors.Is(err, tenant.ErrTenantSuspended) {
		t.Fatalf("suspended tenant resolution error = %v", err)
	}
}

func TestTenantOutboxClaimRetryAndPublish(t *testing.T) {
	database := testpostgres.Open(t)
	repository := tenantpostgres.New(database.Pool)
	now := time.Date(2026, 7, 13, 16, 0, 0, 0, time.UTC)
	service := tenant.NewService(repository, tenant.WithClock(func() time.Time { return now }), tenant.WithIDGenerator(sequenceGenerator()))
	ctx := context.Background()
	if _, err := service.EnsureOfficialTenant(ctx, tenant.EnsureOfficialTenantCommand{
		ProductID: "product-outbox", Name: "Official", ActorID: "assembly", IdempotencyKey: "official-outbox-0001",
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := service.ClaimOutbox(ctx, 10)
	if err != nil || len(claimed) != 1 || claimed[0].AttemptCount != 0 {
		t.Fatalf("first ClaimOutbox() = %+v, err = %v", claimed, err)
	}
	if again, err := service.ClaimOutbox(ctx, 10); err != nil || len(again) != 0 {
		t.Fatalf("immediate ClaimOutbox() = %+v, err = %v", again, err)
	}
	if err := service.MarkOutboxFailed(ctx, claimed[0].EventID, "temporary audit failure", now.Add(-time.Second), false); err != nil {
		t.Fatal(err)
	}
	retried, err := service.ClaimOutbox(ctx, 10)
	if err != nil || len(retried) != 1 || retried[0].AttemptCount != 1 {
		t.Fatalf("retry ClaimOutbox() = %+v, err = %v", retried, err)
	}
	if err := service.MarkOutboxPublished(ctx, retried[0].EventID); err != nil {
		t.Fatal(err)
	}
	if afterPublish, err := service.ClaimOutbox(ctx, 10); err != nil || len(afterPublish) != 0 {
		t.Fatalf("published ClaimOutbox() = %+v, err = %v", afterPublish, err)
	}
}

func sequenceGenerator() func(string) (string, error) {
	var sequence atomic.Uint64
	return func(prefix string) (string, error) {
		return fmt.Sprintf("%s%032x", prefix, sequence.Add(1)), nil
	}
}
