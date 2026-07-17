package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

type antiEnumerationRepository struct {
	EndUserRepository
	credential EndUserPasswordCredential
	findErr    error
	failures   int
	throttle   EndUserLoginThrottle
	sessions   int
}

func (r *antiEnumerationRepository) EndUserLoginThrottle(context.Context, string, []byte, []byte, time.Time) (EndUserLoginThrottle, error) {
	return r.throttle, nil
}

func TestEndUserLoginRateLimitCarriesRetryAfter(t *testing.T) {
	now := time.Date(2026, 7, 18, 3, 30, 0, 0, time.UTC)
	blockedUntil := now.Add(37 * time.Second)
	repository := &antiEnumerationRepository{throttle: EndUserLoginThrottle{BlockedUntil: &blockedUntil}}
	passwords := &countingPasswordVerifier{}
	hasher, err := securevalue.NewHasher(strings.Repeat("rate-limit-pepper-", 3))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewEndUserService(repository, StrictIdentifierNormalizer{}, passwords, hasher, nil, nil, EndUserPolicy{AccessTTL: time.Minute, RefreshTTL: time.Hour, RefreshAbsoluteTTL: 2 * time.Hour, RefreshRecoveryWindow: time.Minute, RecoveryTTL: time.Minute, RecoveryMaxAttempts: 3, LoginWindow: time.Minute, LoginMaximumAttempts: 3, LoginBlockDuration: time.Minute, RecentAuthTTL: 10 * time.Minute}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Login(context.Background(), EndUserLoginCommand{Scope: EndUserSessionScope{ProductID: "product.rate", ApplicationID: "application.rate"}, Identifier: "person@example.com", Credential: "password", Source: "source"})
	var rateLimit *EndUserRateLimitError
	if !errors.As(err, &rateLimit) || !errors.Is(err, ErrEndUserRateLimited) || rateLimit.RetryAfter != 37*time.Second || !rateLimit.BlockedUntil.Equal(blockedUntil) {
		t.Fatalf("rate limit error = %#v", err)
	}
}
func (r *antiEnumerationRepository) FindEndUserPasswordCredential(context.Context, IdentifierType, []byte) (EndUserPasswordCredential, error) {
	return r.credential, r.findErr
}
func (r *antiEnumerationRepository) RecordEndUserLoginFailure(context.Context, EndUserLoginFailure) (EndUserLoginThrottle, error) {
	r.failures++
	return EndUserLoginThrottle{}, nil
}
func (r *antiEnumerationRepository) CreateEndUserSessionAndClearFailures(context.Context, NewEndUserSession, string, []byte) error {
	r.sessions++
	return nil
}

type admissionPortStub struct {
	err      error
	requests []EndUserAdmissionRequest
}

func (p *admissionPortStub) AdmitEndUser(_ context.Context, request EndUserAdmissionRequest) error {
	p.requests = append(p.requests, request)
	return p.err
}

type countingPasswordVerifier struct {
	compares int
}

func (v *countingPasswordVerifier) Compare(hash, password []byte) error {
	v.compares++
	return bcrypt.CompareHashAndPassword(hash, password)
}
func (*countingPasswordVerifier) Hash(password []byte) ([]byte, error) {
	return bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)
}

func TestEndUserLoginUsesOneAdaptiveCompareForMissingAndWrongCredential(t *testing.T) {
	hasher, err := securevalue.NewHasher(strings.Repeat("anti-enumeration-pepper-", 2))
	if err != nil {
		t.Fatal(err)
	}
	validHash, err := bcrypt.GenerateFromPassword([]byte("correct password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		credential EndUserPasswordCredential
		findErr    error
	}{
		{name: "missing", findErr: ErrNotFound},
		{name: "wrong", credential: EndUserPasswordCredential{UserID: "user.one", AccountStatus: "active", PasswordHash: validHash}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &antiEnumerationRepository{credential: test.credential, findErr: test.findErr}
			passwords := &countingPasswordVerifier{}
			service, err := NewEndUserService(repository, StrictIdentifierNormalizer{}, passwords, hasher, nil, nil, EndUserPolicy{AccessTTL: time.Minute, RefreshTTL: time.Hour, RefreshAbsoluteTTL: 2 * time.Hour, RefreshRecoveryWindow: time.Minute, RecoveryTTL: time.Minute, RecoveryMaxAttempts: 3, LoginWindow: time.Minute, LoginMaximumAttempts: 3, LoginBlockDuration: time.Minute, RecentAuthTTL: 10 * time.Minute}, func() time.Time { return time.Date(2026, 7, 18, 3, 0, 0, 0, time.UTC) })
			if err != nil {
				t.Fatal(err)
			}
			passwords.compares = 0
			_, err = service.Login(context.Background(), EndUserLoginCommand{Scope: EndUserSessionScope{ProductID: "product.one", ApplicationID: "application.one"}, Identifier: "person@example.com", Credential: "wrong password", Source: "source", TraceID: "trace"})
			if !errors.Is(err, ErrEndUserInvalidCredentials) || passwords.compares != 1 || repository.failures != 1 {
				t.Fatalf("Login() error=%v compares=%d failures=%d", err, passwords.compares, repository.failures)
			}
		})
	}
}

func TestEndUserLoginAdmissionFailureNeverCreatesSession(t *testing.T) {
	now := time.Date(2026, 7, 18, 3, 45, 0, 0, time.UTC)
	hash, err := bcrypt.GenerateFromPassword([]byte("correct password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := securevalue.NewHasher(strings.Repeat("admission-pepper-", 3))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "denied", err: errors.New("end-user admission denied")},
		{name: "transient", err: errors.New("admission provider unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &antiEnumerationRepository{credential: EndUserPasswordCredential{UserID: "user.admission", AccountStatus: "active", PasswordHash: hash}}
			admission := &admissionPortStub{err: test.err}
			service, err := NewEndUserService(repository, StrictIdentifierNormalizer{}, &countingPasswordVerifier{}, hasher, nil, nil, EndUserPolicy{AccessTTL: time.Minute, RefreshTTL: time.Hour, RefreshAbsoluteTTL: 2 * time.Hour, RefreshRecoveryWindow: time.Minute, RecoveryTTL: time.Minute, RecoveryMaxAttempts: 3, LoginWindow: time.Minute, LoginMaximumAttempts: 3, LoginBlockDuration: time.Minute, RecentAuthTTL: 10 * time.Minute}, func() time.Time { return now }, WithEndUserAdmissionPort(admission))
			if err != nil {
				t.Fatal(err)
			}
			scope := EndUserSessionScope{ProductID: "product.admission", ApplicationID: "application.admission"}
			_, err = service.Login(context.Background(), EndUserLoginCommand{Scope: scope, Identifier: "admission@example.com", Credential: "correct password", Source: "loopback"})
			if !errors.Is(err, test.err) || repository.sessions != 0 || len(admission.requests) != 1 || admission.requests[0].UserID != "user.admission" || !admission.requests[0].Scope.Matches(EndUserSession{ProductID: scope.ProductID, ApplicationID: scope.ApplicationID}) || !admission.requests[0].At.Equal(now) {
				t.Fatalf("Login() err=%v sessions=%d requests=%+v", err, repository.sessions, admission.requests)
			}
		})
	}
}
