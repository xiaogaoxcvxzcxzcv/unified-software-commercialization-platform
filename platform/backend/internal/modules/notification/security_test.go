package notification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type secretResolverStub struct {
	values map[string][]byte
	err    error
}

func (s *secretResolverStub) ResolveSecret(_ context.Context, ref string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	value, ok := s.values[ref]
	if !ok {
		return nil, ErrProviderUnavailable
	}
	return append([]byte(nil), value...), nil
}

type securityRepositoryStub struct {
	records      map[string]CreateSecurityDeliveryRecord
	claimed      []SecurityDelivery
	completed    []CompleteSecurityDeliveryRecord
	completeErrs []error
	clock        time.Time
}

func (s *securityRepositoryStub) CreateSecurityDelivery(_ context.Context, record CreateSecurityDeliveryRecord) (bool, error) {
	if s.records == nil {
		s.records = map[string]CreateSecurityDeliveryRecord{}
	}
	if current, ok := s.records[record.Delivery.DeliveryID]; ok {
		if !bytes.Equal(current.Delivery.RequestDigest, record.Delivery.RequestDigest) {
			return false, ErrIdempotencyConflict
		}
		return false, nil
	}
	s.records[record.Delivery.DeliveryID] = record
	return true, nil
}

func (s *securityRepositoryStub) ClaimSecurityDelivery(_ context.Context, worker string, lease time.Duration) (SecurityDelivery, error) {
	if len(s.claimed) == 0 {
		return SecurityDelivery{}, ErrNotFound
	}
	value := s.claimed[0]
	s.claimed = s.claimed[1:]
	if s.clock.IsZero() {
		s.clock = time.Now().UTC()
	}
	value.LeaseOwner = worker
	value.LeaseStartedAt = s.clock
	value.AttemptCount++
	expires := s.clock.Add(lease)
	value.LeaseExpiresAt = &expires
	return value, nil
}

func (s *securityRepositoryStub) CompleteSecurityDelivery(_ context.Context, record CompleteSecurityDeliveryRecord) error {
	if len(s.completeErrs) != 0 {
		err := s.completeErrs[0]
		s.completeErrs = s.completeErrs[1:]
		if err != nil {
			return err
		}
	}
	s.completed = append(s.completed, record)
	return nil
}

type providerStub struct {
	requests      []SecurityProviderRequest
	result        SecurityProviderResult
	err           error
	nonIdempotent bool
}

func (p *providerStub) DeliverSecurity(_ context.Context, request SecurityProviderRequest) (SecurityProviderResult, error) {
	p.requests = append(p.requests, request)
	return p.result, p.err
}

func (p *providerStub) RequireSecurityProvider(_ context.Context, ref string) (SecurityProviderCapability, error) {
	if ref != "provider-a" {
		return SecurityProviderCapability{}, ErrProviderUnavailable
	}
	return SecurityProviderCapability{DeliveryIDIdempotent: !p.nonIdempotent}, nil
}

type idempotentProviderStub struct {
	requests []SecurityProviderRequest
	effects  map[string]int
}

func (p *idempotentProviderStub) DeliverSecurity(_ context.Context, request SecurityProviderRequest) (SecurityProviderResult, error) {
	p.requests = append(p.requests, request)
	if p.effects == nil {
		p.effects = map[string]int{}
	}
	if p.effects[request.DeliveryID] == 0 {
		p.effects[request.DeliveryID]++
	}
	return SecurityProviderResult{MessageRef: "receipt-private-value"}, nil
}

func (p *idempotentProviderStub) RequireSecurityProvider(_ context.Context, ref string) (SecurityProviderCapability, error) {
	if ref != "provider-a" {
		return SecurityProviderCapability{}, ErrProviderUnavailable
	}
	return SecurityProviderCapability{DeliveryIDIdempotent: true}, nil
}

type providerRegistryStub struct {
	enabled    map[string]bool
	idempotent bool
	err        error
}

func (r providerRegistryStub) RequireSecurityProvider(_ context.Context, ref string) (SecurityProviderCapability, error) {
	if r.err != nil || !r.enabled[ref] {
		return SecurityProviderCapability{}, ErrProviderUnavailable
	}
	return SecurityProviderCapability{DeliveryIDIdempotent: r.idempotent}, nil
}

func (r providerRegistryStub) DeliverSecurity(context.Context, SecurityProviderRequest) (SecurityProviderResult, error) {
	return SecurityProviderResult{}, ErrProviderUnavailable
}

func TestAEADSecurityPayloadProtectorBindsAllRowContext(t *testing.T) {
	protector := testProtector(t, "notification-key", 0x42)
	now := time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)
	command := testSecurityCommand(now)
	ctx := payloadContextFromCommand(command)
	payload := SecurityPayload{Destination: command.Destination, Proof: command.Proof}
	sealed, err := protector.Seal(context.Background(), ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	joined := string(sealed.Nonce) + string(sealed.Ciphertext) + string(sealed.Digest)
	assertNoSecuritySecrets(t, joined, payload.Destination, payload.Proof)
	opened, err := protector.Open(context.Background(), ctx, sealed)
	if err != nil || opened != payload {
		t.Fatalf("opened=%+v err=%v", opened, err)
	}

	other := ctx
	other.DeliveryID = "delivery-security-other"
	if _, err := protector.Open(context.Background(), other, sealed); !errors.Is(err, ErrPayloadUnavailable) {
		t.Fatalf("cross-row full-bundle replacement was accepted: %v", err)
	}
	other = ctx
	other.Purpose = "password_recovery"
	if _, err := protector.Open(context.Background(), other, sealed); !errors.Is(err, ErrPayloadUnavailable) {
		t.Fatalf("cross-purpose replacement was accepted: %v", err)
	}
	other = ctx
	other.ExpiresAt = other.ExpiresAt.Add(time.Second)
	if _, err := protector.Open(context.Background(), other, sealed); !errors.Is(err, ErrPayloadUnavailable) {
		t.Fatalf("cross-expiry replacement was accepted: %v", err)
	}
	other = ctx
	other.TraceID = "trace-security-tampered"
	if _, err := protector.Open(context.Background(), other, sealed); !errors.Is(err, ErrPayloadUnavailable) {
		t.Fatalf("cross-trace replacement was accepted: %v", err)
	}
}

func TestSecurityServiceStableDigestSurvivesEncryptionKeyRotation(t *testing.T) {
	now := time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)
	repository := &securityRepositoryStub{}
	digester := testDigester(t)
	registry := providerRegistryStub{enabled: map[string]bool{"provider-a": true}, idempotent: true}
	command := testSecurityCommand(now)

	first, _ := NewService(repository, testProtector(t, "payload-key-v1", 0x11), digester, registry, func() time.Time { return now })
	if err := first.EnqueueSecurityDelivery(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	second, _ := NewService(repository, testProtector(t, "payload-key-v2", 0x12), digester, registry, func() time.Time { return now })
	if err := second.EnqueueSecurityDelivery(context.Background(), command); err != nil {
		t.Fatalf("rotation changed idempotency digest: %v", err)
	}
	if len(repository.records) != 1 {
		t.Fatalf("records=%d", len(repository.records))
	}
	record := repository.records[command.DeliveryID]
	serialized := fmt.Sprintf("%s %+v", record.Event.Payload, record)
	assertNoSecuritySecrets(t, serialized, command.Destination, command.Proof)

	changed := command
	changed.Proof = "different-proof-secret"
	if err := second.EnqueueSecurityDelivery(context.Background(), changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting enqueue error=%v", err)
	}
}

func TestSecurityServiceRequiresProviderDeliveryIDIdempotency(t *testing.T) {
	now := time.Date(2026, 7, 18, 1, 30, 0, 0, time.UTC)
	for name, registry := range map[string]providerRegistryStub{
		"disabled":       {enabled: map[string]bool{}},
		"not idempotent": {enabled: map[string]bool{"provider-a": true}},
	} {
		t.Run(name, func(t *testing.T) {
			repository := &securityRepositoryStub{}
			service, _ := NewService(repository, testProtector(t, "notification-key", 0x12), testDigester(t), registry, func() time.Time { return now })
			err := service.EnqueueSecurityDelivery(context.Background(), testSecurityCommand(now))
			if !errors.Is(err, ErrProviderUnavailable) || len(repository.records) != 0 {
				t.Fatalf("error=%v persisted=%d", err, len(repository.records))
			}
		})
	}
}

func TestSecurityWorkerRetriesSameDeliveryIDAfterCommitFailureAndProviderDeduplicates(t *testing.T) {
	now := time.Date(2026, 7, 18, 2, 0, 0, 0, time.UTC)
	protector := testProtector(t, "notification-key", 0x22)
	digester := testDigester(t)
	base := testDelivery(t, protector, now)
	repository := &securityRepositoryStub{
		claimed:      []SecurityDelivery{base, base},
		completeErrs: []error{errors.New("database commit failed"), nil},
		clock:        now,
	}
	provider := &idempotentProviderStub{}
	worker, _ := NewWorker(repository, protector, digester, provider, providerSecrets(), "worker-a", func() time.Time { return now })
	if worker.RunOne(context.Background()) {
		t.Fatal("failed completion must not report success")
	}
	if !worker.RunOne(context.Background()) {
		t.Fatal("redelivery did not complete")
	}
	if len(provider.requests) != 2 || provider.requests[0].DeliveryID != base.DeliveryID || provider.requests[1].DeliveryID != base.DeliveryID {
		t.Fatalf("provider requests=%+v", provider.requests)
	}
	if provider.effects[base.DeliveryID] != 1 {
		t.Fatalf("external effects=%d", provider.effects[base.DeliveryID])
	}
	attempt := repository.completed[0].Attempt
	if attempt.Outcome != "delivered" || len(attempt.ProviderMessageDigest) != 32 {
		t.Fatalf("attempt=%+v", attempt)
	}
	assertNoSecuritySecrets(t, fmt.Sprintf("%+v", attempt), "receipt-private-value")
}

func TestSecurityWorkerOutcomesAreBoundedAndRedacted(t *testing.T) {
	now := time.Date(2026, 7, 18, 2, 0, 0, 0, time.UTC)
	protector := testProtector(t, "notification-key", 0x23)
	digester := testDigester(t)
	base := testDelivery(t, protector, now)

	tests := []struct {
		name      string
		delivery  SecurityDelivery
		provider  *providerStub
		secrets   SecretResolver
		status    string
		outcome   string
		errorCode string
		wantCalls int
	}{
		{name: "delivered", delivery: base, provider: &providerStub{result: SecurityProviderResult{MessageRef: "opaque-private-receipt"}}, secrets: providerSecrets(), status: "delivered", outcome: "delivered", wantCalls: 1},
		{name: "provider unavailable retries", delivery: base, provider: &providerStub{err: errors.New("dial failed private@example.com private-proof provider-secret-value")}, secrets: providerSecrets(), status: "pending", outcome: "retryable_failure", errorCode: "NOTIFICATION_SECURITY_PROVIDER_UNAVAILABLE", wantCalls: 1},
		{name: "provider rejection terminal", delivery: base, provider: &providerStub{err: ErrDeliveryRejected}, secrets: providerSecrets(), status: "dead", outcome: "terminal_failure", errorCode: "NOTIFICATION_SECURITY_DELIVERY_REJECTED", wantCalls: 1},
		{name: "provider secret unavailable", delivery: base, provider: &providerStub{}, secrets: &secretResolverStub{err: errors.New("disabled provider-secret-value")}, status: "pending", outcome: "retryable_failure", errorCode: "NOTIFICATION_SECURITY_PROVIDER_UNAVAILABLE"},
	}
	last := base
	last.AttemptCount = last.MaxAttempts - 1
	tests = append(tests, struct {
		name                       string
		delivery                   SecurityDelivery
		provider                   *providerStub
		secrets                    SecretResolver
		status, outcome, errorCode string
		wantCalls                  int
	}{"maximum attempt becomes dead", last, &providerStub{err: errors.New("temporary")}, providerSecrets(), "dead", "terminal_failure", "NOTIFICATION_SECURITY_PROVIDER_UNAVAILABLE", 1})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &securityRepositoryStub{claimed: []SecurityDelivery{test.delivery}, clock: now}
			worker, _ := NewWorker(repository, protector, digester, test.provider, test.secrets, "worker-a", func() time.Time { return now })
			worker.RunOne(context.Background())
			if len(repository.completed) != 1 {
				t.Fatalf("completed=%+v", repository.completed)
			}
			record := repository.completed[0]
			if record.NextStatus != test.status || record.Attempt.Outcome != test.outcome || len(test.provider.requests) != test.wantCalls {
				t.Fatalf("record=%+v calls=%d", record, len(test.provider.requests))
			}
			if test.errorCode != "" && (record.Attempt.ErrorCode == nil || *record.Attempt.ErrorCode != test.errorCode) {
				t.Fatalf("error code=%v", record.Attempt.ErrorCode)
			}
			assertNoSecuritySecrets(t, fmt.Sprintf("%+v", record), "private@example.com", "private-proof", "provider-secret-value", "opaque-private-receipt")
		})
	}

	t.Run("swapped protected bundle is terminal", func(t *testing.T) {
		swapped := base
		swapped.DeliveryID = "delivery-worker-swapped"
		repository := &securityRepositoryStub{claimed: []SecurityDelivery{swapped}, clock: now}
		provider := &providerStub{}
		worker, _ := NewWorker(repository, protector, digester, provider, providerSecrets(), "worker-a", func() time.Time { return now })
		worker.RunOne(context.Background())
		if repository.completed[0].NextStatus != "dead" || len(provider.requests) != 0 {
			t.Fatalf("completed=%+v provider=%d", repository.completed, len(provider.requests))
		}
	})

	t.Run("worker gateway rejects non-idempotent provider before delivery", func(t *testing.T) {
		repository := &securityRepositoryStub{claimed: []SecurityDelivery{base}, clock: now}
		provider := &providerStub{nonIdempotent: true}
		worker, _ := NewWorker(repository, protector, digester, provider, providerSecrets(), "worker-a", func() time.Time { return now })
		worker.RunOne(context.Background())
		if len(provider.requests) != 0 || repository.completed[0].NextStatus != "pending" || repository.completed[0].Attempt.ErrorCode == nil || *repository.completed[0].Attempt.ErrorCode != "NOTIFICATION_SECURITY_PROVIDER_UNAVAILABLE" {
			t.Fatalf("provider=%d completed=%+v", len(provider.requests), repository.completed)
		}
	})
}

func testProtector(t *testing.T, keyRef string, fill byte) *AEADSecurityPayloadProtector {
	t.Helper()
	protector, err := NewAEADSecurityPayloadProtector(keyRef, &secretResolverStub{values: map[string][]byte{keyRef: bytes.Repeat([]byte{fill}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	return protector
}

func testDigester(t *testing.T) *HMACSecurityDigester {
	t.Helper()
	digester, err := NewHMACSecurityDigester("request-digest-key", &secretResolverStub{values: map[string][]byte{"request-digest-key": bytes.Repeat([]byte{0x7a}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	return digester
}

func providerSecrets() SecretResolver {
	return &secretResolverStub{values: map[string][]byte{"provider-a": []byte("provider-secret-value")}}
}

func testDelivery(t *testing.T, protector *AEADSecurityPayloadProtector, now time.Time) SecurityDelivery {
	t.Helper()
	delivery := SecurityDelivery{DeliveryID: "delivery-worker", Purpose: "password_recovery", ProductID: "product-a", ApplicationID: "app-a", ProviderRef: "provider-a", DestinationType: "email", MaxAttempts: 3, ExpiresAt: now.Add(time.Hour), TraceID: "trace-a"}
	sealed, err := protector.Seal(context.Background(), payloadContextFromDelivery(delivery), SecurityPayload{Destination: "private@example.com", Proof: "private-proof"})
	if err != nil {
		t.Fatal(err)
	}
	delivery.Payload = sealed
	return delivery
}

func testSecurityCommand(now time.Time) SecurityDeliveryCommand {
	tenant := "tenant-a"
	return SecurityDeliveryCommand{DeliveryID: "delivery-security-0001", Purpose: "registration_verify", ProductID: "product-a", ApplicationID: "application-a", TenantID: &tenant, ProviderRef: "provider-a", DestinationType: "email", Destination: "person@example.com", Proof: "verification-proof-secret", ExpiresAt: now.Add(time.Hour), TraceID: "trace-security-0001"}
}

func assertNoSecuritySecrets(t *testing.T, value string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(value, secret) {
			t.Fatalf("value leaked secret %q", secret)
		}
	}
}
