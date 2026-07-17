package identity

import (
	"context"
	"time"
)

// EndUserRepository is separate from the administrator Repository so neither
// session surface can accidentally read or mutate the other's token families.
type EndUserRepository interface {
	CreateEndUser(context.Context, EndUserRegistration) error
	CreateEndUserWithSession(context.Context, EndUserRegistration, NewEndUserSession) error
	CreateEndUserWithSessionIdempotent(context.Context, EndUserRegistration, NewEndUserSession, EndUserIdempotency) (EndUserRegistrationResponse, bool, error)
	RecoverEndUserRegistration(context.Context, EndUserIdempotency) (EndUserRegistrationResponse, bool, error)
	RecoverEndUserIdempotency(context.Context, EndUserIdempotency) (bool, error)
	FailEndUserIdempotency(context.Context, EndUserIdempotency, string) error
	RecoverEndUserPasswordChange(context.Context, []byte, []byte, []byte) (bool, error)
	FindEndUserPasswordCredential(context.Context, IdentifierType, []byte) (EndUserPasswordCredential, error)
	FindEndUserPasswordCredentialByUser(context.Context, string) (EndUserPasswordCredential, error)
	FindEndUserRecoveryTarget(context.Context, IdentifierType, []byte) (EndUserRecoveryTarget, error)
	EndUserLoginThrottle(context.Context, string, []byte, []byte, time.Time) (EndUserLoginThrottle, error)
	RecordEndUserLoginFailure(context.Context, EndUserLoginFailure) (EndUserLoginThrottle, error)
	ClearEndUserLoginFailures(context.Context, string, []byte) error
	GetEndUserProfile(context.Context, string) (EndUserProfile, error)
	UpdateEndUserProfile(context.Context, EndUserProfile, int64, OutboxEvent) (EndUserProfile, error)
	UpdateEndUserProfileIdempotent(context.Context, EndUserProfile, int64, OutboxEvent, EndUserIdempotency) (EndUserProfile, bool, error)
	PatchEndUserProfileIdempotent(context.Context, string, EndUserProfilePatch, int64, OutboxEvent, EndUserIdempotency) (EndUserProfile, bool, error)
	ReplaceEndUserPassword(context.Context, string, string, []byte, string, int64, time.Time, bool, OutboxEvent) error
	ReplaceEndUserPasswordIdempotent(context.Context, string, string, []byte, string, int64, time.Time, bool, OutboxEvent, EndUserIdempotency) (bool, error)
	CreateEndUserSession(context.Context, NewEndUserSession) error
	CreateEndUserSessionAndClearFailures(context.Context, NewEndUserSession, string, []byte) error
	FindEndUserByAccessDigest(context.Context, []byte, EndUserSessionScope, time.Time) (EndUserSession, error)
	ResolveEndUserByAccessDigest(context.Context, []byte, time.Time) (EndUserSession, error)
	ResolveEndUserRefreshScope(context.Context, []byte, time.Time) (EndUserSessionScope, error)
	RotateEndUserRefresh(context.Context, []byte, EndUserSessionScope, EndUserRefreshRotation) (EndUserSession, error)
	RevokeEndUserSession(context.Context, string, string, EndUserSessionScope, string, time.Time, OutboxEvent) error
	RevokeScopedSessions(context.Context, ScopedSessionRevocation) error
	ListEndUserSessions(context.Context, string, string, EndUserSessionScope) ([]EndUserSessionSummary, error)
	CreateRecoveryChallenge(context.Context, RecoveryChallenge) error
	CreateRecoveryChallengeIdempotent(context.Context, RecoveryChallenge, EndUserIdempotency) (RecoveryChallenge, bool, error)
	ActivateRecoveryChallenge(context.Context, string) error
	ConsumeRecoveryChallenge(context.Context, []byte, []byte, time.Time, OutboxEvent) (RecoveryConsumption, error)
	CompleteEndUserRecovery(context.Context, []byte, []byte, []byte, string, time.Time, OutboxEvent) (bool, error)
	CompleteEndUserRecoveryIdempotent(context.Context, string, []byte, []byte, []byte, string, time.Time, OutboxEvent, EndUserIdempotency) (bool, bool, error)
	LinkExternalIdentity(context.Context, ExternalIdentity) error
	FindExternalIdentity(context.Context, string, string, []byte) (ExternalIdentity, error)
}
