package identity

import (
	"context"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

type hostedAuthRepositoryStub struct {
	*antiEnumerationRepository
	profile EndUserProfile
	proof   HostedAuthProof
}

func (r *hostedAuthRepositoryStub) GetEndUserProfile(context.Context, string) (EndUserProfile, error) {
	return r.profile, nil
}

func (r *hostedAuthRepositoryStub) CreateHostedAuthProofAndClearFailures(_ context.Context, proof HostedAuthProof, _ string, _ []byte) (HostedAuthProof, error) {
	proof.CreatedAt = time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)
	proof.ExpiresAt = proof.CreatedAt.Add(proof.TTL)
	r.proof = proof
	return proof, nil
}

func (*hostedAuthRepositoryStub) RedeemHostedAuthGrant(context.Context, HostedAuthGrantRedemption) (EndUserSession, bool, error) {
	return EndUserSession{}, false, ErrHostedAuthUnavailable
}

func (*hostedAuthRepositoryStub) ValidateHostedSession(context.Context, HostedSessionExpectation) error {
	return ErrHostedAuthUnavailable
}

func TestAuthenticateHostedReusesPasswordControlsWithoutCreatingSession(t *testing.T) {
	now := time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct hosted password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	repository := &hostedAuthRepositoryStub{
		antiEnumerationRepository: &antiEnumerationRepository{credential: EndUserPasswordCredential{UserID: "user.hosted", AccountStatus: "active", PasswordHash: passwordHash}},
		profile:                   EndUserProfile{UserID: "user.hosted", DisplayName: "Hosted User"},
	}
	hasher, err := securevalue.NewHasher(strings.Repeat("hosted-auth-unit-pepper-", 2))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewEndUserService(repository, StrictIdentifierNormalizer{}, &countingPasswordVerifier{}, hasher, nil, nil, EndUserPolicy{AccessTTL: time.Minute, RefreshTTL: time.Hour, RefreshAbsoluteTTL: 2 * time.Hour, RefreshRecoveryWindow: time.Minute, RecoveryTTL: time.Minute, RecoveryMaxAttempts: 3, LoginWindow: time.Minute, LoginMaximumAttempts: 3, LoginBlockDuration: time.Minute, RecentAuthTTL: 10 * time.Minute, HostedAuthProofTTL: 2 * time.Minute}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.AuthenticateHosted(context.Background(), AuthenticateHostedCommand{Scope: EndUserSessionScope{ProductID: "product.hosted", ApplicationID: "application.hosted", Environment: "test"}, Identifier: "hosted@example.com", Credential: "correct hosted password", Source: "loopback", RiskSummary: map[string]any{"device": "known"}, TraceID: "trace.hosted.authenticate"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProofID == "" || result.User.DisplayName != "Hosted User" || !result.AuthTime.Equal(now) || !result.ExpiresAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("AuthenticateHosted() = %+v", result)
	}
	if repository.sessions != 0 || repository.proof.UserID != "user.hosted" || repository.proof.AuthenticationMethod != "password" || len(repository.proof.RiskSummaryDigest) != 32 {
		t.Fatalf("sessions=%d proof=%+v", repository.sessions, repository.proof)
	}
}
