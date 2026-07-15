package clientregistration

import (
	"context"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

type productStub struct{ registered product.RegisterClientCommand }

func (s *productStub) RegisterClient(_ context.Context, command product.RegisterClientCommand) (product.ClientAuthentication, error) {
	s.registered = command
	return product.ClientAuthentication{Client: product.Client{ClientID: "client-1", ProductID: command.ProductID, Environment: command.Environment}, Credential: product.ClientCredential{CredentialID: "credential-1", ClientID: "client-1", ProductID: command.ProductID, ProofType: command.ProofType, ProofDigest: command.ProofDigest, Generation: 1, NotBefore: command.NotBefore, ExpiresAt: command.ExpiresAt}}, nil
}
func (s *productStub) RotateClientCredential(context.Context, product.RotateClientCredentialCommand) (product.ClientCredential, error) {
	return product.ClientCredential{}, nil
}
func (s *productStub) RevokeClientCredential(context.Context, product.RevokeClientCredentialCommand) (product.ClientCredential, error) {
	return product.ClientCredential{}, nil
}
func (s *productStub) ResolveProductContext(context.Context, string, string) (product.ProductContext, error) {
	return product.ProductContext{ProductID: "prod-1", Environment: "production"}, nil
}

type applicationStub struct {
	bound productapplication.BindClientCommand
}

func (s *applicationStub) BindClientToApplication(_ context.Context, command productapplication.BindClientCommand) (productapplication.ClientBinding, error) {
	s.bound = command
	return productapplication.ClientBinding{BindingID: "binding-1", AuditID: "audit-binding"}, nil
}

func TestRegisterReturnsDeterministicOneTimeSecretAndBindsExactClient(t *testing.T) {
	hasher, err := securevalue.NewHasher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	products, applications := &productStub{}, &applicationStub{}
	service := New(products, applications, hasher, time.Now)
	command := RegisterCommand{ProductID: "prod-1", ApplicationID: "app-1", Environment: "production", ProofType: "hmac_sha256_v1", NotBefore: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), ActorID: "admin-1", IdempotencyKey: "0123456789abcdef", TraceID: "trace-1"}
	first, err := service.Register(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Register(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.Secret == "" || first.Secret != second.Secret || products.registered.ProofDigest == "" || applications.bound.Client.ClientID != "client-1" || first.AuditID != "audit-binding" {
		t.Fatalf("first=%#v second=%#v registered=%#v bound=%#v", first, second, products.registered, applications.bound)
	}
}
