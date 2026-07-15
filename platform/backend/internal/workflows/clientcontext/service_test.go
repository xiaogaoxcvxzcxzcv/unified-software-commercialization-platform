package clientcontext

import (
	"context"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/modules/tenant"
)

type productStub struct {
	create product.CreateClientSessionCommand
}

func (s *productStub) ResolveProductContext(context.Context, string, string) (product.ProductContext, error) {
	return product.ProductContext{ProductID: "prod-1", ProductCode: "video-brain", Environment: "production", ContextVersion: 2}, nil
}
func (s *productStub) CreateClientSession(_ context.Context, command product.CreateClientSessionCommand) (product.IssuedClientSession, error) {
	s.create = command
	return product.IssuedClientSession{StoredClientSession: product.StoredClientSession{ExpiresAt: time.Date(2026, 7, 13, 1, 0, 0, 0, time.UTC)}, Token: "client-session-token"}, nil
}

type applicationStub struct{ channel string }

func (s applicationStub) ResolveApplicationContext(context.Context, productapplication.ResolveCommand) (productapplication.ApplicationContext, error) {
	return productapplication.ApplicationContext{ProductID: "prod-1", Environment: productapplication.EnvironmentProduction, ApplicationID: "app-1", DistributionChannel: s.channel, ContextVersion: 3}, nil
}

type tenantStub struct {
	command tenant.ResolveTenantContextCommand
}

func (s *tenantStub) ResolveTenantContext(_ context.Context, command tenant.ResolveTenantContextCommand) (tenant.TenantContext, error) {
	s.command = command
	return tenant.TenantContext{ProductID: "prod-1", TenantID: "ten-official", TenantType: "official", TenantStatus: "active", ResolvedBy: command.Method, ContextVersion: 4}, nil
}

func TestCreateSessionBuildsTrustedScopeInOrder(t *testing.T) {
	now := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	products, tenants := &productStub{}, &tenantStub{}
	service := New(products, applicationStub{channel: "official"}, tenants, time.Hour, 5*time.Minute, func() time.Time { return now })
	result, err := service.CreateSession(context.Background(), CreateCommand{ClientID: "client-1", CredentialID: "credential-1", ProofType: "hmac_sha256_v1", ProofValue: "secret-value-with-at-least-thirty-two-characters", ProofTimestamp: now, ClientVersion: "1.0.0", RequestNonce: "0123456789abcdef", TraceID: "trace-1"})
	if err != nil || result.TenantContext.TenantID != "ten-official" || products.create.Scope.ApplicationID != "app-1" || products.create.Scope.TenantID != "ten-official" || tenants.command.Method != tenant.ResolutionOfficialChannel {
		t.Fatalf("result=%#v error=%v productCommand=%#v tenantCommand=%#v", result, err, products.create, tenants.command)
	}
}

func TestCreateSessionRejectsNonOfficialChannelWithoutDistributionProof(t *testing.T) {
	now := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	service := New(&productStub{}, applicationStub{channel: "agent"}, &tenantStub{}, time.Hour, 5*time.Minute, func() time.Time { return now })
	_, err := service.CreateSession(context.Background(), CreateCommand{ClientID: "client-1", CredentialID: "credential-1", ProofType: "hmac_sha256_v1", ProofValue: "secret-value-with-at-least-thirty-two-characters", ProofTimestamp: now, ClientVersion: "1.0.0", RequestNonce: "0123456789abcdef", TraceID: "trace-1"})
	if err != ErrInvalidClientContext {
		t.Fatalf("CreateSession() error = %v", err)
	}
}
