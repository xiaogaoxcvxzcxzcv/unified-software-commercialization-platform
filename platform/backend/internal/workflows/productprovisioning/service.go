package productprovisioning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/tenant"
)

type ProductService interface {
	BeginProvisioning(context.Context, product.BeginProvisioningCommand) (product.Product, error)
	CompleteProvisioning(context.Context, product.CompleteProvisioningCommand) (product.Product, error)
}

type TenantService interface {
	EnsureOfficialTenant(context.Context, tenant.EnsureOfficialTenantCommand) (tenant.Tenant, error)
}

type Service struct {
	products ProductService
	tenants  TenantService
}

func New(products ProductService, tenants TenantService) *Service {
	return &Service{products: products, tenants: tenants}
}

type CreateCommand struct {
	ProductCode    string
	Name           string
	Status         string
	Environments   []string
	ActorID        string
	IdempotencyKey string
	TraceID        string
}

func (s *Service) CreateProduct(ctx context.Context, command CreateCommand) (product.Product, error) {
	if s == nil || s.products == nil || s.tenants == nil || len(command.IdempotencyKey) < 16 || len(command.IdempotencyKey) > 128 || strings.TrimSpace(command.ActorID) == "" || strings.TrimSpace(command.TraceID) == "" {
		return product.Product{}, errors.New("product provisioning workflow is not configured")
	}
	created, err := s.products.BeginProvisioning(ctx, product.BeginProvisioningCommand{
		ProductCode: command.ProductCode, Name: command.Name, Status: command.Status,
		Environments: command.Environments, ActorID: command.ActorID,
		IdempotencyKey: derivedKey(command.IdempotencyKey, "product"), TraceID: command.TraceID,
	})
	if err != nil {
		return product.Product{}, err
	}
	official, err := s.tenants.EnsureOfficialTenant(ctx, tenant.EnsureOfficialTenantCommand{
		ProductID: created.ProductID, Name: "官方直营", ActorID: command.ActorID,
		IdempotencyKey: derivedKey(command.IdempotencyKey, "official-tenant"), TraceID: command.TraceID,
	})
	if err != nil {
		return product.Product{}, err
	}
	return s.products.CompleteProvisioning(ctx, product.CompleteProvisioningCommand{
		ProductID: created.ProductID, OfficialTenantID: official.TenantID, ActorID: command.ActorID,
		IdempotencyKey: derivedKey(command.IdempotencyKey, "complete"), TraceID: command.TraceID,
	})
}

func derivedKey(root, step string) string {
	digest := sha256.Sum256([]byte(root + "\x00" + step))
	return hex.EncodeToString(digest[:])
}
