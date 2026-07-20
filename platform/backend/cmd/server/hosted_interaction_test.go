package main

import (
	"context"
	"errors"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/hostedinteraction"
	hostedhttp "platform.local/capability-platform/backend/internal/modules/hostedinteraction/httptransport"
	"platform.local/capability-platform/backend/internal/modules/identity"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
)

func TestHostedChannelMappingIsClosed(t *testing.T) {
	tests := []struct {
		platform productapplication.Platform
		want     string
	}{
		{productapplication.PlatformWeb, "web"},
		{productapplication.PlatformH5, "h5"},
		{productapplication.PlatformWindows, "desktop"},
		{productapplication.PlatformMacOS, "desktop"},
		{productapplication.PlatformLinux, "desktop"},
		{productapplication.PlatformAndroid, "app"},
		{productapplication.PlatformIOS, "app"},
		{productapplication.PlatformWechatMiniProgram, "app"},
	}
	for _, test := range tests {
		got, err := hostedChannel(test.platform)
		if err != nil || got != test.want {
			t.Fatalf("hostedChannel(%q)=(%q,%v), want %q", test.platform, got, err, test.want)
		}
	}
	if got, err := hostedChannel(productapplication.Platform("unknown")); got != "" || !errors.Is(err, hostedhttp.ErrChannelNotSupported) {
		t.Fatalf("unknown hosted channel=(%q,%v)", got, err)
	}
}

func TestHostedSelfServiceIdentityErrorsAreExplicitlyMapped(t *testing.T) {
	tests := []struct{ source, target error }{{identity.ErrEndUserVersionConflict, hostedinteraction.ErrIdempotencyConflict}, {identity.ErrEndUserInvalidCredentials, hostedinteraction.ErrAuthenticationNeeded}, {identity.ErrEndUserReauthenticationRequired, hostedinteraction.ErrAuthenticationNeeded}, {identity.ErrEndUserRateLimited, hostedinteraction.ErrAuthenticationNeeded}, {identity.ErrRegistrationVerificationInvalid, hostedinteraction.ErrAuthenticationNeeded}, {identity.ErrEndUserSessionExpired, hostedinteraction.ErrSessionRevoked}, {identity.ErrEndUserAccountDisabled, hostedinteraction.ErrSessionRevoked}, {identity.ErrEndUserProviderUnavailable, hostedinteraction.ErrTemporarilyUnavailable}}
	for _, tt := range tests {
		if got := mapHostedIdentitySelfServiceError(tt.source); !errors.Is(got, tt.target) {
			t.Fatalf("source=%v got=%v want=%v", tt.source, got, tt.target)
		}
	}
	httpTests := []struct{ source, target error }{{identity.ErrEndUserVersionConflict, hostedhttp.ErrConflict}, {identity.ErrEndUserInvalidCredentials, hostedhttp.ErrAuthenticationRequired}, {identity.ErrEndUserReauthenticationRequired, hostedhttp.ErrAuthenticationRequired}, {identity.ErrEndUserSessionExpired, hostedhttp.ErrSessionRevoked}, {identity.ErrEndUserProviderUnavailable, hostedhttp.ErrTemporarilyUnavailable}}
	for _, tt := range httpTests {
		if got := mapHostedCoreError(mapHostedIdentitySelfServiceError(tt.source)); !errors.Is(got, tt.target) {
			t.Fatalf("http source=%v got=%v want=%v", tt.source, got, tt.target)
		}
	}
}

func TestHostedStateSecretResolverIsReferenceBoundAndCopiesKey(t *testing.T) {
	resolver := hostedStateSecretResolver{reference: "hosted.state.v1"}
	for index := range resolver.key {
		resolver.key[index] = byte(index + 1)
	}
	first, err := resolver.ResolveSecret(context.Background(), "hosted.state.v1")
	if err != nil || len(first) != len(resolver.key) {
		t.Fatalf("resolve state secret=(%d,%v)", len(first), err)
	}
	first[0] = 0
	second, err := resolver.ResolveSecret(context.Background(), "hosted.state.v1")
	if err != nil || second[0] != 1 {
		t.Fatalf("resolver exposed mutable key: first byte=%d error=%v", second[0], err)
	}
	if _, err = resolver.ResolveSecret(context.Background(), "other.state.v1"); !errors.Is(err, hostedinteraction.ErrTemporarilyUnavailable) {
		t.Fatalf("wrong state reference error=%v", err)
	}
}

func TestHostedScopeAndErrorMappingPreserveSecurityContext(t *testing.T) {
	tenantID := "tenant-1"
	scope := coreHostedScope(hostedhttp.Scope{ProductID: "product-1", ApplicationID: "application-1", TenantID: &tenantID, Environment: "production", Channel: "desktop"})
	if scope.ProductID != "product-1" || scope.ApplicationID != "application-1" || scope.TenantID == nil || *scope.TenantID != tenantID || scope.Environment != "production" || scope.Channel != hostedinteraction.ChannelDesktop {
		t.Fatalf("hosted scope mapping lost context: %+v", scope)
	}

	stable := []struct {
		core error
		http error
	}{
		{hostedinteraction.ErrInvalidArgument, hostedhttp.ErrInvalidInteraction},
		{hostedinteraction.ErrInteractionExpired, hostedhttp.ErrInteractionExpired},
		{hostedinteraction.ErrInteractionTerminal, hostedhttp.ErrInteractionTerminal},
		{hostedinteraction.ErrInvalidReturnTarget, hostedhttp.ErrInvalidReturnTarget},
		{hostedinteraction.ErrStateMismatch, hostedhttp.ErrStateMismatch},
		{hostedinteraction.ErrPKCERequired, hostedhttp.ErrPKCERequired},
		{hostedinteraction.ErrInvalidGrant, hostedhttp.ErrInvalidGrant},
		{hostedinteraction.ErrAuthenticationNeeded, hostedhttp.ErrAuthenticationRequired},
		{hostedinteraction.ErrChannelNotSupported, hostedhttp.ErrChannelNotSupported},
		{hostedinteraction.ErrSessionRevoked, hostedhttp.ErrSessionRevoked},
		{hostedinteraction.ErrCSRF, hostedhttp.ErrCSRFFailed},
		{hostedinteraction.ErrIdempotencyConflict, hostedhttp.ErrConflict},
		{hostedinteraction.ErrLeaseLost, hostedhttp.ErrTemporarilyUnavailable},
	}
	for _, test := range stable {
		if got := mapHostedCoreError(test.core); !errors.Is(got, test.http) {
			t.Fatalf("mapHostedCoreError(%v)=%v, want %v", test.core, got, test.http)
		}
	}
	dependency := errors.New("database unavailable")
	if got := mapHostedCoreError(dependency); !errors.Is(got, dependency) {
		t.Fatalf("dependency error was discarded: %v", got)
	}
}

func TestHostedIdentityErrorMappingSeparatesAuthenticationAndInfrastructure(t *testing.T) {
	if got := mapHostedIdentityAuthenticationError(identity.ErrEndUserInvalidCredentials); !errors.Is(got, hostedinteraction.ErrAuthenticationNeeded) {
		t.Fatalf("invalid credential mapping=%v", got)
	}
	if got := mapHostedIdentityRedemptionError(identity.ErrHostedAuthProofReplayed); !errors.Is(got, hostedinteraction.ErrInvalidGrant) {
		t.Fatalf("replayed proof mapping=%v", got)
	}
	dependency := errors.New("identity database unavailable")
	if got := mapHostedIdentityAuthenticationError(dependency); !errors.Is(got, dependency) {
		t.Fatalf("authentication dependency error was discarded: %v", got)
	}
	if got := mapHostedIdentityRedemptionError(dependency); !errors.Is(got, dependency) {
		t.Fatalf("redemption dependency error was discarded: %v", got)
	}
}

func TestHostedSessionAuthenticationErrorPreservesExpiry(t *testing.T) {
	if got := mapHostedSessionAuthenticationError(hostedinteraction.ErrInteractionExpired); !errors.Is(got, hostedhttp.ErrInteractionExpired) {
		t.Fatalf("expired interaction mapping=%v", got)
	}
	if got := mapHostedSessionAuthenticationError(hostedinteraction.ErrSessionRevoked); !errors.Is(got, hostedhttp.ErrSessionRevoked) {
		t.Fatalf("revoked browser mapping=%v", got)
	}
	dependency := errors.New("hosted database unavailable")
	if got := mapHostedSessionAuthenticationError(dependency); !errors.Is(got, dependency) {
		t.Fatalf("session dependency error was discarded: %v", got)
	}
}
