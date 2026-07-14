package product

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type repositoryStub struct {
	Repository
	begin   func(context.Context, BeginProvisioningRecord) (Product, error)
	replace func(context.Context, ReplaceCapabilitySetRecord) (CapabilitySet, error)
}

func (r repositoryStub) BeginProvisioning(ctx context.Context, record BeginProvisioningRecord) (Product, error) {
	return r.begin(ctx, record)
}

func (r repositoryStub) ReplaceCapabilitySet(ctx context.Context, record ReplaceCapabilitySetRecord) (CapabilitySet, error) {
	if r.replace == nil {
		return CapabilitySet{}, errors.New("unexpected repository call")
	}
	return r.replace(ctx, record)
}

func TestBeginProvisioningHashesIdempotencyKeyAndNormalizesEnvironments(t *testing.T) {
	var captured BeginProvisioningRecord
	repository := repositoryStub{begin: func(_ context.Context, record BeginProvisioningRecord) (Product, error) {
		captured = record
		return record.Product, nil
	}}
	service := NewService(repository, nil, nil, fixedIDs(), nil, fixedNow)
	_, err := service.BeginProvisioning(context.Background(), BeginProvisioningCommand{
		ProductCode: "video-brain", Name: "Video Brain", Status: "active",
		Environments: []string{"test", "local"}, ActorID: "admin-1", IdempotencyKey: "raw-idempotency-key", TraceID: "trace-begin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.Idempotency.KeyDigest == "raw-idempotency-key" || !strings.HasPrefix(captured.Idempotency.KeyDigest, "sha256:") {
		t.Fatalf("idempotency key was not digested: %q", captured.Idempotency.KeyDigest)
	}
	if len(captured.Environments) != 2 || captured.Environments[0] != "local" || captured.Environments[1] != "test" {
		t.Fatalf("environments = %v", captured.Environments)
	}
}

func TestCapabilityChangeFailsClosedWithoutTrustedVerifier(t *testing.T) {
	service := NewService(repositoryStub{}, nil, nil, fixedIDs(), nil, fixedNow)
	_, err := service.ReplaceCapabilitySet(context.Background(), ReplaceCapabilitySetCommand{
		Plan:    TrustedCapabilityChangePlan{ProductID: "prod-1", SourcePlanID: "plan-1", CatalogRevision: "revision-1", CatalogSnapshotSHA256: "sha256:" + strings.Repeat("a", 64)},
		ActorID: "admin-1", IdempotencyKey: "idem-1", TraceID: "trace-capability",
	})
	if !errors.Is(err, ErrUntrustedChangePlan) {
		t.Fatalf("error = %v, want ErrUntrustedChangePlan", err)
	}
}

func TestClientSessionFailsClosedWithoutProofVerifier(t *testing.T) {
	service := NewService(repositoryStub{}, nil, nil, fixedIDs(), func() (string, string, error) {
		return "plain", "sha256:" + strings.Repeat("b", 64), nil
	}, fixedNow)
	_, err := service.CreateClientSession(context.Background(), CreateClientSessionCommand{
		ClientID: "client-1", CredentialID: "credential-1", RequestNonce: "nonce-at-least-sixteen", ClientVersion: "1.0.0",
		Scope: ResolvedSessionScope{ProductID: "prod-1", Environment: "test", ApplicationID: "app-1", TenantID: "tenant-1", ApplicationContextVersion: 1, TenantContextVersion: 1},
		TTL:   time.Minute, TraceID: "trace-1",
	})
	if !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error = %v, want fail-closed ErrInvalidCommand", err)
	}
}

func TestIssuedClientSessionNeverSerializesStoredTokenDigest(t *testing.T) {
	issued := IssuedClientSession{StoredClientSession: StoredClientSession{SessionID: "session-1", TokenDigest: "sha256:" + strings.Repeat("a", 64)}, Token: "plain-once"}
	encoded, err := json.Marshal(issued)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "sha256:") || !strings.Contains(string(encoded), "plain-once") {
		t.Fatalf("unsafe session JSON: %s", encoded)
	}
}

func fixedIDs() IDGenerator {
	sequence := 0
	return func(prefix string) (string, error) {
		sequence++
		return prefix + string(rune('a'+sequence)), nil
	}
}

func fixedNow() time.Time { return time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC) }
