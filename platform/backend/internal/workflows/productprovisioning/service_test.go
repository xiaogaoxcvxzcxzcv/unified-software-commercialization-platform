package productprovisioning

import (
	"context"
	"errors"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/tenant"
)

type productStub struct {
	beginCalls    int
	completeCalls int
}

func (s *productStub) BeginProvisioning(_ context.Context, command product.BeginProvisioningCommand) (product.Product, error) {
	s.beginCalls++
	return product.Product{ProductID: "prod-1", ProductCode: command.ProductCode, Name: command.Name, ProvisioningState: "pending"}, nil
}
func (s *productStub) CompleteProvisioning(_ context.Context, command product.CompleteProvisioningCommand) (product.Product, error) {
	s.completeCalls++
	return product.Product{ProductID: command.ProductID, OfficialTenantID: command.OfficialTenantID, ProvisioningState: "ready", AuditID: "audit-1"}, nil
}

type tenantStub struct {
	err   error
	calls int
}

func (s *tenantStub) EnsureOfficialTenant(_ context.Context, command tenant.EnsureOfficialTenantCommand) (tenant.Tenant, error) {
	s.calls++
	return tenant.Tenant{ProductID: command.ProductID, TenantID: "tenant-official"}, s.err
}

func TestCreateProductDoesNotCompleteBeforeOfficialTenantExists(t *testing.T) {
	products := &productStub{}
	tenants := &tenantStub{err: errors.New("tenant temporarily unavailable")}
	workflow := New(products, tenants)
	_, err := workflow.CreateProduct(context.Background(), CreateCommand{ProductCode: "video-brain", Name: "Video Brain", Status: "active", ActorID: "admin-1", IdempotencyKey: "0123456789abcdef", TraceID: "trace-1"})
	if err == nil || products.beginCalls != 1 || products.completeCalls != 0 || tenants.calls != 1 {
		t.Fatalf("error=%v begin=%d complete=%d tenants=%d", err, products.beginCalls, products.completeCalls, tenants.calls)
	}
}

func TestCreateProductReturnsOnlyReadyProduct(t *testing.T) {
	products := &productStub{}
	workflow := New(products, &tenantStub{})
	created, err := workflow.CreateProduct(context.Background(), CreateCommand{ProductCode: "video-brain", Name: "Video Brain", Status: "active", ActorID: "admin-1", IdempotencyKey: "0123456789abcdef", TraceID: "trace-1"})
	if err != nil || created.ProvisioningState != "ready" || created.OfficialTenantID != "tenant-official" || created.AuditID == "" {
		t.Fatalf("CreateProduct() = %#v, %v", created, err)
	}
}
