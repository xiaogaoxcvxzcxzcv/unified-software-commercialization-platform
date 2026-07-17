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
	getA     Application
	getE     error
	resolveA Application
	resolveB ClientBinding
	resolveE error
	target   StoredAuthReturnTarget
	targetE  error
}

func (*repositoryStub) CreateApplication(context.Context, CreateRecord) (Application, error) {
	return Application{}, nil
}
func (*repositoryStub) ListApplications(context.Context, string) ([]Application, error) {
	return nil, nil
}
func (r *repositoryStub) GetApplication(context.Context, string, string) (Application, error) {
	if r.getA.Platform == "" {
		r.getA.Platform = PlatformWeb
	}
	return r.getA, r.getE
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
func (r *repositoryStub) ResolveAuthReturnTarget(context.Context, AuthReturnTargetQuery) (StoredAuthReturnTarget, error) {
	return r.target, r.targetE
}
func (*repositoryStub) ClaimOutbox(context.Context, time.Time, int) ([]ClaimedOutboxEvent, error) {
	return nil, nil
}
func (*repositoryStub) MarkOutboxPublished(context.Context, string, string, time.Time) error {
	return nil
}
func (*repositoryStub) MarkOutboxFailed(context.Context, string, string, string, time.Time, bool) error {
	return nil
}

func TestReplaceRedirectsNormalizesExactAllowlist(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository, fixedIDs{value: "1"}, func() time.Time { return time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC) })
	result, err := service.ReplaceRedirects(context.Background(), ReplaceRedirectsCommand{
		Product: ProductContext{ProductID: "prod-1", Environment: EnvironmentProduction}, ApplicationID: "app-1", ActorID: "admin-1", TraceID: "trace-1",
		Policy: RedirectPolicy{
			WebRedirectURIs:   []string{"https://EXAMPLE.com/callback"},
			AllowedOrigins:    []string{"https://EXAMPLE.com/"},
			DeepLinks:         []DeepLinkRule{{Scheme: "myapp", PathPattern: "/oauth/callback"}},
			AuthReturnTargets: []AuthReturnTarget{{Code: "login.complete", URI: "https://EXAMPLE.com/callback"}},
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
	if len(repository.redirect.Policy.AuthReturnTargets) != 1 || repository.redirect.Policy.AuthReturnTargets[0] != (AuthReturnTarget{Code: "login.complete", URI: "https://example.com/callback"}) {
		t.Fatalf("normalized auth return targets = %+v", repository.redirect.Policy.AuthReturnTargets)
	}
}

func TestReplaceRedirectsRejectsUnlistedDuplicateAndPlatformUnsafeAuthTargets(t *testing.T) {
	tests := []struct {
		name     string
		platform Platform
		policy   RedirectPolicy
	}{
		{name: "missing base whitelist", platform: PlatformWeb, policy: RedirectPolicy{AuthReturnTargets: []AuthReturnTarget{{Code: "login", URI: "https://example.com/callback"}}}},
		{name: "unlisted web", platform: PlatformWeb, policy: RedirectPolicy{WebRedirectURIs: []string{"https://example.com/callback"}, AuthReturnTargets: []AuthReturnTarget{{Code: "login", URI: "https://other.example/callback"}}}},
		{name: "duplicate code", platform: PlatformWeb, policy: RedirectPolicy{WebRedirectURIs: []string{"https://example.com/a", "https://example.com/b"}, AuthReturnTargets: []AuthReturnTarget{{Code: "login", URI: "https://example.com/a"}, {Code: "login", URI: "https://example.com/b"}}}},
		{name: "web deep link", platform: PlatformWeb, policy: RedirectPolicy{DeepLinks: []DeepLinkRule{{Scheme: "myapp", PathPattern: "/callback"}}, AuthReturnTargets: []AuthReturnTarget{{Code: "login", URI: "myapp:/callback"}}}},
		{name: "deep link host", platform: PlatformIOS, policy: RedirectPolicy{DeepLinks: []DeepLinkRule{{Scheme: "myapp", PathPattern: "/callback"}}, AuthReturnTargets: []AuthReturnTarget{{Code: "login", URI: "myapp://attacker/callback"}}}},
		{name: "whitespace wrapped code", platform: PlatformWeb, policy: RedirectPolicy{WebRedirectURIs: []string{"https://example.com/callback"}, AuthReturnTargets: []AuthReturnTarget{{Code: " login ", URI: "https://example.com/callback"}}}},
		{name: "uppercase deep link scheme", platform: PlatformIOS, policy: RedirectPolicy{DeepLinks: []DeepLinkRule{{Scheme: "MYAPP", PathPattern: "/callback"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &repositoryStub{getA: Application{Platform: test.platform}}
			service := NewService(repository, fixedIDs{value: "1"}, time.Now)
			_, err := service.ReplaceRedirects(context.Background(), ReplaceRedirectsCommand{Product: ProductContext{ProductID: "prod-1", Environment: EnvironmentProduction}, ApplicationID: "app-1", Policy: test.policy, ActorID: "admin-1", TraceID: "trace-1"})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("ReplaceRedirects() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestResolveAuthReturnTargetUsesTrustedCurrentRecord(t *testing.T) {
	repository := &repositoryStub{target: StoredAuthReturnTarget{ProductID: "prod-1", ApplicationID: "app-1", Platform: PlatformIOS, Status: StatusActive, Code: "login.complete", URI: "myapp:/callback", PolicyVersion: 3, DeepLinks: []DeepLinkRule{{Scheme: "myapp", PathPattern: "/callback"}}}}
	service := NewService(repository, fixedIDs{value: "1"}, time.Now)
	resolved, err := service.ResolveAuthReturnTarget(context.Background(), ProductContext{ProductID: "prod-1", Environment: EnvironmentProduction}, "app-1", "login.complete")
	if err != nil || resolved.URI != "myapp:/callback" || resolved.Kind != AuthReturnTargetDeepLink || resolved.PolicyVersion != 3 {
		t.Fatalf("ResolveAuthReturnTarget() = %+v, %v", resolved, err)
	}
	repository.target.ProductID = "prod-forged"
	if _, err := service.ResolveAuthReturnTarget(context.Background(), ProductContext{ProductID: "prod-1", Environment: EnvironmentProduction}, "app-1", "login.complete"); !errors.Is(err, ErrContextRejected) {
		t.Fatalf("forged stored scope error = %v", err)
	}
	repository.target = StoredAuthReturnTarget{ProductID: "prod-1", ApplicationID: "app-1", Platform: PlatformWeb, Status: StatusActive, Code: "login.complete", URI: "http://localhost:3000/callback", PolicyVersion: 4, WebRedirectURIs: []string{"http://localhost:3000/callback"}}
	if _, err := service.ResolveAuthReturnTarget(context.Background(), ProductContext{ProductID: "prod-1", Environment: EnvironmentProduction}, "app-1", "login.complete"); !errors.Is(err, ErrContextRejected) {
		t.Fatalf("local target resolved in production: %v", err)
	}
	if _, err := service.ResolveAuthReturnTarget(context.Background(), ProductContext{ProductID: "prod-1", Environment: EnvironmentProduction}, "app-1", " login.complete "); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("whitespace-wrapped code error = %v", err)
	}
}

func TestReplaceRedirectsAcceptsExactNativeDeepLinkTarget(t *testing.T) {
	repository := &repositoryStub{getA: Application{Platform: PlatformIOS}}
	service := NewService(repository, fixedIDs{value: "1"}, time.Now)
	_, err := service.ReplaceRedirects(context.Background(), ReplaceRedirectsCommand{
		Product: ProductContext{ProductID: "prod-1", Environment: EnvironmentProduction}, ApplicationID: "app-1", ActorID: "admin-1", TraceID: "trace-1",
		Policy: RedirectPolicy{DeepLinks: []DeepLinkRule{{Scheme: "myapp", PathPattern: "/oauth/callback"}}, AuthReturnTargets: []AuthReturnTarget{{Code: "login.complete", URI: "myapp:/oauth/callback"}}},
	})
	if err != nil || len(repository.redirect.Policy.AuthReturnTargets) != 1 || repository.redirect.Policy.AuthReturnTargets[0].URI != "myapp:/oauth/callback" {
		t.Fatalf("native auth return target = %+v, error = %v", repository.redirect.Policy.AuthReturnTargets, err)
	}
}

func TestReplaceRedirectsRejectsOversizedAndDuplicateArrays(t *testing.T) {
	oversizedStrings := make([]string, 101)
	oversizedDeepLinks := make([]DeepLinkRule, 101)
	oversizedTargets := make([]AuthReturnTarget, 101)
	tests := []struct {
		name   string
		policy RedirectPolicy
	}{
		{name: "web redirects over max", policy: RedirectPolicy{WebRedirectURIs: oversizedStrings}},
		{name: "origins over max", policy: RedirectPolicy{AllowedOrigins: oversizedStrings}},
		{name: "deep links over max", policy: RedirectPolicy{DeepLinks: oversizedDeepLinks}},
		{name: "auth targets over max", policy: RedirectPolicy{AuthReturnTargets: oversizedTargets}},
		{name: "duplicate web redirect", policy: RedirectPolicy{WebRedirectURIs: []string{"https://example.com/callback", "https://example.com/callback"}}},
		{name: "normalized duplicate web redirect", policy: RedirectPolicy{WebRedirectURIs: []string{"https://EXAMPLE.com/callback", "https://example.com/callback"}}},
		{name: "duplicate origin", policy: RedirectPolicy{AllowedOrigins: []string{"https://example.com", "https://example.com"}}},
		{name: "duplicate deep link", policy: RedirectPolicy{DeepLinks: []DeepLinkRule{{Scheme: "myapp", PathPattern: "/callback"}, {Scheme: "myapp", PathPattern: "/callback"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(&repositoryStub{}, fixedIDs{value: "1"}, time.Now)
			_, err := service.ReplaceRedirects(context.Background(), ReplaceRedirectsCommand{Product: ProductContext{ProductID: "prod-1", Environment: EnvironmentProduction}, ApplicationID: "app-1", Policy: test.policy, ActorID: "admin-1", TraceID: "trace-1"})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("ReplaceRedirects() error = %v, want ErrInvalidArgument", err)
			}
		})
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
