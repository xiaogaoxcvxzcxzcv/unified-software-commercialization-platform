package clientcontext

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/modules/tenant"
)

var ErrInvalidClientContext = errors.New("invalid client context request")

type ProductService interface {
	ResolveProductContext(context.Context, string, string) (product.ProductContext, error)
	CreateClientSession(context.Context, product.CreateClientSessionCommand) (product.IssuedClientSession, error)
}

type ApplicationService interface {
	ResolveApplicationContext(context.Context, productapplication.ResolveCommand) (productapplication.ApplicationContext, error)
}

type TenantService interface {
	ResolveTenantContext(context.Context, tenant.ResolveTenantContextCommand) (tenant.TenantContext, error)
}

type Service struct {
	products     ProductService
	applications ApplicationService
	tenants      TenantService
	now          func() time.Time
	sessionTTL   time.Duration
	proofSkew    time.Duration
}

func New(products ProductService, applications ApplicationService, tenants TenantService, sessionTTL, proofSkew time.Duration, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{products: products, applications: applications, tenants: tenants, sessionTTL: sessionTTL, proofSkew: proofSkew, now: now}
}

type CreateCommand struct {
	ClientID       string
	CredentialID   string
	ProofType      string
	ProofValue     string
	ProofTimestamp time.Time
	ClientVersion  string
	RequestNonce   string
	ChannelProof   string
	TraceID        string
}

type Session struct {
	Token              string                                `json:"client_session_token"`
	ExpiresAt          time.Time                             `json:"expires_at"`
	ProductContext     product.ProductContext                `json:"product_context"`
	ApplicationContext productapplication.ApplicationContext `json:"application_context"`
	TenantContext      tenant.TenantContext                  `json:"tenant_context"`
}

func (s *Service) CreateSession(ctx context.Context, command CreateCommand) (Session, error) {
	if s == nil || s.products == nil || s.applications == nil || s.tenants == nil || s.sessionTTL <= 0 || s.proofSkew <= 0 {
		return Session{}, ErrInvalidClientContext
	}
	command.ClientID = strings.TrimSpace(command.ClientID)
	command.CredentialID = strings.TrimSpace(command.CredentialID)
	command.ClientVersion = strings.TrimSpace(command.ClientVersion)
	command.RequestNonce = strings.TrimSpace(command.RequestNonce)
	command.TraceID = strings.TrimSpace(command.TraceID)
	if command.ClientID == "" || command.CredentialID == "" || command.ProofValue == "" || command.ProofTimestamp.IsZero() || len(command.ClientVersion) < 1 || len(command.ClientVersion) > 64 || len(command.RequestNonce) < 16 || len(command.RequestNonce) > 256 || command.TraceID == "" {
		return Session{}, ErrInvalidClientContext
	}
	now := s.now().UTC()
	delta := now.Sub(command.ProofTimestamp.UTC())
	if delta < -s.proofSkew || delta > s.proofSkew {
		return Session{}, ErrInvalidClientContext
	}
	productContext, err := s.products.ResolveProductContext(ctx, command.ClientID, command.CredentialID)
	if err != nil {
		return Session{}, err
	}
	applicationContext, err := s.applications.ResolveApplicationContext(ctx, productapplication.ResolveCommand{
		Product:       productapplication.ProductContext{ProductID: productContext.ProductID, Environment: productapplication.Environment(productContext.Environment)},
		Client:        productapplication.ClientIdentity{ProductID: productContext.ProductID, ClientID: command.ClientID, Environment: productapplication.Environment(productContext.Environment), CredentialType: command.ProofType},
		ClientVersion: command.ClientVersion,
	})
	if err != nil {
		return Session{}, err
	}
	resolution := tenant.ResolutionOfficialChannel
	if command.ChannelProof != "" {
		resolution = tenant.ResolutionDistribution
	} else if applicationContext.DistributionChannel != "official" {
		return Session{}, ErrInvalidClientContext
	}
	tenantContext, err := s.tenants.ResolveTenantContext(ctx, tenant.ResolveTenantContextCommand{
		ProductID: productContext.ProductID, ApplicationID: applicationContext.ApplicationID,
		Method: resolution, ChannelCode: applicationContext.DistributionChannel, ProofSubject: command.ChannelProof,
	})
	if err != nil {
		return Session{}, err
	}
	proofPayload := []byte(fmt.Sprintf("%s\n%s\n%s\n%s\n%s", command.ClientID, command.CredentialID, command.RequestNonce, command.ClientVersion, command.ProofTimestamp.UTC().Format(time.RFC3339Nano)))
	issued, err := s.products.CreateClientSession(ctx, product.CreateClientSessionCommand{
		ClientID: command.ClientID, CredentialID: command.CredentialID,
		Proof:        product.ClientProof{Type: command.ProofType, Value: command.ProofValue, Payload: proofPayload},
		RequestNonce: command.RequestNonce, ClientVersion: command.ClientVersion,
		Scope: product.ResolvedSessionScope{ProductID: productContext.ProductID, Environment: productContext.Environment, ApplicationID: applicationContext.ApplicationID, TenantID: tenantContext.TenantID, ApplicationContextVersion: applicationContext.ContextVersion, TenantContextVersion: tenantContext.ContextVersion},
		TTL:   s.sessionTTL, TraceID: command.TraceID,
	})
	if err != nil {
		return Session{}, err
	}
	return Session{Token: issued.Token, ExpiresAt: issued.ExpiresAt, ProductContext: productContext, ApplicationContext: applicationContext, TenantContext: tenantContext}, nil
}
