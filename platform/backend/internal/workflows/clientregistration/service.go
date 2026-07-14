package clientregistration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/product"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

var ErrInvalidRegistration = errors.New("invalid client registration")

type ProductService interface {
	RegisterClient(context.Context, product.RegisterClientCommand) (product.ClientAuthentication, error)
	RotateClientCredential(context.Context, product.RotateClientCredentialCommand) (product.ClientCredential, error)
	RevokeClientCredential(context.Context, product.RevokeClientCredentialCommand) (product.ClientCredential, error)
	ResolveProductContext(context.Context, string, string) (product.ProductContext, error)
}

type ApplicationService interface {
	BindClientToApplication(context.Context, productapplication.BindClientCommand) (productapplication.ClientBinding, error)
}

type Service struct {
	products     ProductService
	applications ApplicationService
	hasher       securevalue.Hasher
	proofs       product.VersionedProofVerifier
	now          func() time.Time
}

func New(products ProductService, applications ApplicationService, hasher securevalue.Hasher, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{products: products, applications: applications, hasher: hasher, proofs: product.NewVersionedProofVerifier(hasher), now: now}
}

type RegisterCommand struct {
	ProductID, ApplicationID, Environment, ProofType, PublicKey string
	ExpiresAt, NotBefore                                        time.Time
	ActorID, IdempotencyKey, TraceID                            string
}

type CredentialResult struct {
	ClientID, CredentialID, ProductID, ApplicationID, Environment, ProofType string
	Generation                                                               int
	Secret                                                                   string
	PublicKey                                                                string
	NotBefore, ExpiresAt                                                     time.Time
	AuditID                                                                  string
}

func (s *Service) Register(ctx context.Context, command RegisterCommand) (CredentialResult, error) {
	if !s.validBase(command.ProductID, command.ApplicationID, command.ActorID, command.IdempotencyKey, command.TraceID) || command.NotBefore.IsZero() || !command.ExpiresAt.After(command.NotBefore) {
		return CredentialResult{}, ErrInvalidRegistration
	}
	secret, digest, err := s.proofMaterial("register", command.ProductID, command.ApplicationID, "", command.ProofType, command.PublicKey, command.IdempotencyKey)
	if err != nil {
		return CredentialResult{}, err
	}
	authentication, err := s.products.RegisterClient(ctx, product.RegisterClientCommand{
		ProductID: command.ProductID, Environment: command.Environment, ProofType: command.ProofType,
		ProofDigest: digest, PublicKey: command.PublicKey, NotBefore: command.NotBefore, ExpiresAt: command.ExpiresAt,
		ActorID: command.ActorID, IdempotencyKey: derivedKey(command.IdempotencyKey, "product-register"), TraceID: command.TraceID,
	})
	if err != nil {
		return CredentialResult{}, err
	}
	binding, err := s.applications.BindClientToApplication(ctx, productapplication.BindClientCommand{
		Product:       productapplication.ProductContext{ProductID: command.ProductID, Environment: productapplication.Environment(command.Environment)},
		ApplicationID: command.ApplicationID,
		Client:        productapplication.ClientIdentity{ProductID: command.ProductID, ClientID: authentication.Client.ClientID, Environment: productapplication.Environment(command.Environment), CredentialType: command.ProofType},
		ActorID:       command.ActorID, IdempotencyKey: derivedKey(command.IdempotencyKey, "application-bind"), TraceID: command.TraceID,
	})
	if err != nil {
		return CredentialResult{}, err
	}
	return CredentialResult{ClientID: authentication.Client.ClientID, CredentialID: authentication.Credential.CredentialID, ProductID: command.ProductID, ApplicationID: command.ApplicationID, Environment: command.Environment, ProofType: authentication.Credential.ProofType, Generation: authentication.Credential.Generation, Secret: secret, PublicKey: authentication.Credential.PublicKey, NotBefore: authentication.Credential.NotBefore, ExpiresAt: authentication.Credential.ExpiresAt, AuditID: binding.AuditID}, nil
}

type RotateCommand struct {
	ProductID, ApplicationID, ClientID, ProofType, PublicKey string
	ExpectedGeneration                                       int
	NotBefore, ExpiresAt                                     time.Time
	ActorID, IdempotencyKey, TraceID                         string
}

func (s *Service) Rotate(ctx context.Context, command RotateCommand) (CredentialResult, error) {
	if !s.validBase(command.ProductID, command.ClientID, command.ActorID, command.IdempotencyKey, command.TraceID) || strings.TrimSpace(command.ApplicationID) == "" || command.ExpectedGeneration < 1 || command.NotBefore.IsZero() || !command.ExpiresAt.After(command.NotBefore) {
		return CredentialResult{}, ErrInvalidRegistration
	}
	secret, digest, err := s.proofMaterial("rotate", command.ProductID, command.ClientID, strconv.Itoa(command.ExpectedGeneration), command.ProofType, command.PublicKey, command.IdempotencyKey)
	if err != nil {
		return CredentialResult{}, err
	}
	credential, err := s.products.RotateClientCredential(ctx, product.RotateClientCredentialCommand{
		ProductID: command.ProductID, ClientID: command.ClientID, ExpectedGeneration: command.ExpectedGeneration,
		ProofType: command.ProofType, ProofDigest: digest, PublicKey: command.PublicKey,
		NotBefore: command.NotBefore, ExpiresAt: command.ExpiresAt, ActorID: command.ActorID,
		IdempotencyKey: derivedKey(command.IdempotencyKey, "credential-rotate"), TraceID: command.TraceID,
	})
	if err != nil {
		return CredentialResult{}, err
	}
	context, err := s.products.ResolveProductContext(ctx, credential.ClientID, credential.CredentialID)
	if err != nil {
		return CredentialResult{}, err
	}
	return CredentialResult{ClientID: credential.ClientID, CredentialID: credential.CredentialID, ProductID: command.ProductID, ApplicationID: command.ApplicationID, Environment: context.Environment, ProofType: credential.ProofType, Generation: credential.Generation, Secret: secret, PublicKey: credential.PublicKey, NotBefore: credential.NotBefore, ExpiresAt: credential.ExpiresAt, AuditID: credential.AuditID}, nil
}

func (s *Service) Revoke(ctx context.Context, productID, clientID, credentialID, actorID, idempotencyKey, traceID string) (product.ClientCredential, error) {
	if !s.validBase(productID, clientID, actorID, idempotencyKey, traceID) || strings.TrimSpace(credentialID) == "" {
		return product.ClientCredential{}, ErrInvalidRegistration
	}
	return s.products.RevokeClientCredential(ctx, product.RevokeClientCredentialCommand{ProductID: productID, ClientID: clientID, CredentialID: credentialID, ActorID: actorID, IdempotencyKey: derivedKey(idempotencyKey, "credential-revoke"), TraceID: traceID})
}

func (s *Service) proofMaterial(operation, productID, targetID, generation, proofType, publicKey, key string) (string, string, error) {
	switch proofType {
	case "hmac_sha256_v1":
		if publicKey != "" {
			return "", "", ErrInvalidRegistration
		}
		raw := s.hasher.Digest("product-client-secret:" + operation + ":" + productID + ":" + targetID + ":" + generation + ":" + key)
		secret := "pcsec_" + base64.RawURLEncoding.EncodeToString(raw)
		return secret, s.proofs.SharedSecretDigest(secret), nil
	case "ed25519_signature_v1":
		if strings.TrimSpace(publicKey) == "" {
			return "", "", ErrInvalidRegistration
		}
		return "", "", nil
	default:
		return "", "", ErrInvalidRegistration
	}
}

func (s *Service) validBase(productID, targetID, actorID, key, traceID string) bool {
	return s != nil && s.products != nil && s.applications != nil && strings.TrimSpace(productID) != "" && strings.TrimSpace(targetID) != "" && strings.TrimSpace(actorID) != "" && len(key) >= 16 && len(key) <= 128 && strings.TrimSpace(traceID) != ""
}

func derivedKey(root, step string) string {
	digest := sha256.Sum256([]byte(root + "\x00" + step))
	return hex.EncodeToString(digest[:])
}
