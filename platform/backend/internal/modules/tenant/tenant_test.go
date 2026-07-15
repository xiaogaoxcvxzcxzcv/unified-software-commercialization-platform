package tenant

import (
	"context"
	"errors"
	"testing"
	"time"
)

type repositoryStub struct {
	ensureValue             Tenant
	createValue             Tenant
	listValues              []Tenant
	official                Tenant
	distributed             Tenant
	distributionProduct     string
	distributionApplication string
	distributionChannel     string
	distributionDigest      string
	ensureRecord            IdempotencyRecord
	createRecord            IdempotencyRecord
}

func (r *repositoryStub) EnsureOfficialTenant(_ context.Context, value Tenant, record IdempotencyRecord, _ OutboxEvent) (Tenant, error) {
	r.ensureRecord = record
	if r.ensureValue.TenantID != "" {
		return r.ensureValue, nil
	}
	return value, nil
}

func (r *repositoryStub) CreateAgentTenant(_ context.Context, value Tenant, record IdempotencyRecord, _ OutboxEvent) (Tenant, error) {
	r.createRecord = record
	if r.createValue.TenantID != "" {
		return r.createValue, nil
	}
	return value, nil
}

func (r *repositoryStub) ListTenants(context.Context, string) ([]Tenant, error) {
	return r.listValues, nil
}

func (r *repositoryStub) FindOfficialTenant(context.Context, string) (Tenant, error) {
	if r.official.TenantID == "" {
		return Tenant{}, ErrTenantNotFound
	}
	return r.official, nil
}

func (r *repositoryStub) FindTenantByDistribution(_ context.Context, productID, applicationID, channel, digest string) (Tenant, error) {
	r.distributionProduct = productID
	r.distributionApplication = applicationID
	r.distributionChannel = channel
	r.distributionDigest = digest
	if r.distributed.TenantID == "" {
		return Tenant{}, ErrTenantNotFound
	}
	return r.distributed, nil
}

func (r *repositoryStub) ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error) {
	return nil, nil
}

func (r *repositoryStub) MarkOutboxPublished(context.Context, string, time.Time) error { return nil }

func (r *repositoryStub) MarkOutboxFailed(context.Context, string, string, time.Time, bool) error {
	return nil
}

type fixedDigester struct{ digest string }

func (d fixedDigester) DigestHex(string) string { return d.digest }

func TestCreateAgentTenantBuildsProductScopedIdempotentWrite(t *testing.T) {
	repository := &repositoryStub{}
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	ids := []string{"ten_agent", "audit_agent", "evt_agent"}
	service := NewService(repository, WithClock(func() time.Time { return now }), WithIDGenerator(func(string) (string, error) {
		value := ids[0]
		ids = ids[1:]
		return value, nil
	}))
	created, err := service.CreateAgentTenant(context.Background(), CreateAgentTenantCommand{
		ProductID: " product-a ", TenantCode: "agent-one", Name: " Agent One ", Status: TenantStatusActive,
		ExternalAgentRef: "external-1", ActorID: "admin-1", IdempotencyKey: "idempotency-key-0001", TraceID: "trace-1",
	})
	if err != nil {
		t.Fatalf("CreateAgentTenant() error = %v", err)
	}
	if created.ProductID != "product-a" || created.TenantID != "ten_agent" || created.TenantType != TenantTypeAgent || created.AuditID != "audit_agent" {
		t.Fatalf("created tenant = %+v", created)
	}
	if repository.createRecord.ScopeID != "product-a" || repository.createRecord.Operation != "tenant.create_agent" {
		t.Fatalf("idempotency record = %+v", repository.createRecord)
	}
	if repository.createRecord.KeyDigest == "idempotency-key-0001" || repository.createRecord.RequestDigest == "" {
		t.Fatalf("idempotency digests were not protected: %+v", repository.createRecord)
	}
}

func TestResolveTenantContextUsesServerSideProofAndRejectsScopeMismatch(t *testing.T) {
	repository := &repositoryStub{distributed: Tenant{
		TenantID: "tenant-a1", ProductID: "product-a", TenantType: TenantTypeAgent,
		Status: TenantStatusActive, ContextVersion: 3,
	}}
	service := NewService(repository, WithProofDigester(fixedDigester{digest: "proof-digest"}))
	resolved, err := service.ResolveTenantContext(context.Background(), ResolveTenantContextCommand{
		ProductID: "product-a", ApplicationID: "app-a", Method: ResolutionDistribution,
		ChannelCode: "partner", ProofSubject: "raw-proof",
	})
	if err != nil {
		t.Fatalf("ResolveTenantContext() error = %v", err)
	}
	if resolved.TenantID != "tenant-a1" || resolved.ResolvedBy != ResolutionDistribution {
		t.Fatalf("resolved context = %+v", resolved)
	}
	if repository.distributionProduct != "product-a" || repository.distributionApplication != "app-a" || repository.distributionDigest != "hmac-sha256:proof-digest" {
		t.Fatalf("repository distribution lookup = product %q app %q digest %q", repository.distributionProduct, repository.distributionApplication, repository.distributionDigest)
	}

	repository.distributed.ProductID = "product-b"
	if _, err := service.ResolveTenantContext(context.Background(), ResolveTenantContextCommand{
		ProductID: "product-a", Method: ResolutionDistribution, ChannelCode: "partner", ProofSubject: "raw-proof",
	}); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("scope-mismatched ResolveTenantContext() error = %v, want ErrTenantNotFound", err)
	}
}

func TestResolveTenantContextRejectsSuspendedAndUnprovedTenant(t *testing.T) {
	repository := &repositoryStub{official: Tenant{
		TenantID: "official", ProductID: "product-a", TenantType: TenantTypeOfficial,
		Status: TenantStatusSuspended, ContextVersion: 2,
	}}
	service := NewService(repository)
	if _, err := service.ResolveTenantContext(context.Background(), ResolveTenantContextCommand{
		ProductID: "product-a", Method: ResolutionOfficialChannel,
	}); !errors.Is(err, ErrTenantSuspended) {
		t.Fatalf("suspended ResolveTenantContext() error = %v", err)
	}
	if _, err := service.ResolveTenantContext(context.Background(), ResolveTenantContextCommand{
		ProductID: "product-a", Method: ResolutionDistribution, ChannelCode: "partner", ProofSubject: "proof",
	}); !errors.Is(err, ErrInvalidTenantProof) {
		t.Fatalf("unconfigured proof ResolveTenantContext() error = %v", err)
	}
}

func TestTenantCommandsRejectInvalidStableInputs(t *testing.T) {
	service := NewService(&repositoryStub{})
	invalid := []CreateAgentTenantCommand{
		{ProductID: "product-a", TenantCode: "official", Name: "Reserved", Status: TenantStatusActive, ActorID: "admin", IdempotencyKey: "idempotency-key-0001"},
		{ProductID: "product-a", TenantCode: "Agent Upper", Name: "Bad", Status: TenantStatusActive, ActorID: "admin", IdempotencyKey: "idempotency-key-0001"},
		{ProductID: "product-a", TenantCode: "agent", Name: "Bad", Status: TenantStatusActive, ActorID: "admin", IdempotencyKey: "short"},
	}
	for _, command := range invalid {
		if _, err := service.CreateAgentTenant(context.Background(), command); !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("CreateAgentTenant(%+v) error = %v", command, err)
		}
	}
}
