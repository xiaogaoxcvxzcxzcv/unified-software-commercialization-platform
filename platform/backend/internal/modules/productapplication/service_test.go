package productapplication

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fixedIDs struct{ value string }

func (f fixedIDs) ID(prefix string) (string, error) { return prefix + f.value, nil }

type repositoryStub struct {
	redirect RedirectRecord
	resolveA Application
	resolveB ClientBinding
	resolveE error
}

func (*repositoryStub) CreateApplication(context.Context, CreateRecord) (Application, error) {
	return Application{}, nil
}
func (*repositoryStub) ListApplications(context.Context, string) ([]Application, error) {
	return nil, nil
}
func (*repositoryStub) GetApplication(context.Context, string, string) (Application, error) {
	return Application{}, nil
}
func (*repositoryStub) BindClient(context.Context, BindRecord) (ClientBinding, error) {
	return ClientBinding{}, nil
}
func (r *repositoryStub) ReplaceRedirects(_ context.Context, record RedirectRecord) (RedirectPolicyVersion, error) {
	r.redirect = record
	return record.Version, nil
}
func (*repositoryStub) SuspendApplication(context.Context, SuspendRecord) (SuspendResult, error) {
	return SuspendResult{}, nil
}
func (r *repositoryStub) ResolveApplication(context.Context, ResolveQuery) (Application, ClientBinding, error) {
	return r.resolveA, r.resolveB, r.resolveE
}
func (*repositoryStub) ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error) {
	return nil, nil
}
func (*repositoryStub) MarkOutboxPublished(context.Context, string, time.Time) error { return nil }
func (*repositoryStub) MarkOutboxFailed(context.Context, string, string, time.Time, bool) error {
	return nil
}

func TestReplaceRedirectsNormalizesExactAllowlist(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository, fixedIDs{value: "1"}, func() time.Time { return time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC) })
	result, err := service.ReplaceRedirects(context.Background(), ReplaceRedirectsCommand{
		Product: ProductContext{ProductID: "prod-1", Environment: EnvironmentProduction}, ApplicationID: "app-1", ActorID: "admin-1", TraceID: "trace-1",
		Policy: RedirectPolicy{
			WebRedirectURIs: []string{"https://EXAMPLE.com/callback", "https://example.com/callback"},
			AllowedOrigins:  []string{"https://EXAMPLE.com/", "https://example.com"},
			DeepLinks:       []DeepLinkRule{{Scheme: "MYAPP", PathPattern: "/oauth/callback"}, {Scheme: "myapp", PathPattern: "/oauth/callback"}},
		},
	})
	if err != nil {
		t.Fatalf("ReplaceRedirects() error = %v", err)
	}
	if result.ContentSHA256 == "" || len(repository.redirect.Policy.WebRedirectURIs) != 1 || repository.redirect.Policy.WebRedirectURIs[0] != "https://example.com/callback" {
		t.Fatalf("normalized redirects = %+v", repository.redirect.Policy)
	}
	if len(repository.redirect.Policy.AllowedOrigins) != 1 || repository.redirect.Policy.AllowedOrigins[0] != "https://example.com" || len(repository.redirect.Policy.DeepLinks) != 1 {
		t.Fatalf("normalized policy = %+v", repository.redirect.Policy)
	}
}

func TestReplaceRedirectsRejectsUnsafeProductionTargets(t *testing.T) {
	service := NewService(&repositoryStub{}, fixedIDs{value: "1"}, time.Now)
	cases := []RedirectPolicy{
		{WebRedirectURIs: []string{"http://example.com/callback"}},
		{WebRedirectURIs: []string{"https://*.example.com/callback"}},
		{AllowedOrigins: []string{"https://example.com:8443"}},
		{AllowedOrigins: []string{"https://example.com/path"}},
		{DeepLinks: []DeepLinkRule{{Scheme: "javascript", PathPattern: "/callback"}}},
		{DeepLinks: []DeepLinkRule{{Scheme: "myapp", PathPattern: "/*"}}},
	}
	for _, policy := range cases {
		_, err := service.ReplaceRedirects(context.Background(), ReplaceRedirectsCommand{Product: ProductContext{ProductID: "prod-1", Environment: EnvironmentProduction}, ApplicationID: "app-1", Policy: policy, ActorID: "admin-1", TraceID: "trace-1"})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("policy %+v error = %v, want ErrInvalidArgument", policy, err)
		}
	}
}

func TestResolveApplicationContextRejectsForgedScopeAndChannel(t *testing.T) {
	repository := &repositoryStub{
		resolveA: Application{ApplicationID: "app-1", ProductID: "prod-1", ApplicationCode: "web", Platform: PlatformWeb, DistributionChannel: "official", ReleaseTrack: ReleaseTrackStable, Status: StatusActive, ContextVersion: 2},
		resolveB: ClientBinding{BindingID: "binding-1", ProductID: "prod-1", ApplicationID: "app-1", ClientID: "client-1", Environment: EnvironmentProduction, Status: StatusActive},
	}
	service := NewService(repository, fixedIDs{value: "1"}, time.Now)
	base := ResolveCommand{Product: ProductContext{ProductID: "prod-1", Environment: EnvironmentProduction}, Client: ClientIdentity{ProductID: "prod-1", ClientID: "client-1", Environment: EnvironmentProduction, CredentialType: "ed25519_signature_v1"}, ClientVersion: "1.0.0", ObservedDistributionChannel: "official"}
	resolved, err := service.ResolveApplicationContext(context.Background(), base)
	if err != nil || resolved.ApplicationID != "app-1" || resolved.ContextVersion != 2 {
		t.Fatalf("resolved = %+v, error = %v", resolved, err)
	}
	serverResolved := base
	serverResolved.ObservedDistributionChannel = ""
	if resolved, err := service.ResolveApplicationContext(context.Background(), serverResolved); err != nil || resolved.DistributionChannel != "official" {
		t.Fatalf("server-resolved channel = %+v, error = %v", resolved, err)
	}
	forgedProduct := base
	forgedProduct.Client.ProductID = "prod-2"
	if _, err := service.ResolveApplicationContext(context.Background(), forgedProduct); !errors.Is(err, ErrContextRejected) {
		t.Fatalf("forged product error = %v", err)
	}
	forgedChannel := base
	forgedChannel.ObservedDistributionChannel = "agent"
	if _, err := service.ResolveApplicationContext(context.Background(), forgedChannel); !errors.Is(err, ErrContextRejected) {
		t.Fatalf("forged channel error = %v", err)
	}
}
