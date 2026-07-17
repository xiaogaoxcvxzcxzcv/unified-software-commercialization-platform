package identity

import (
	"context"
	"time"
)

// EndUserRepository is separate from the administrator Repository so neither
// session surface can accidentally read or mutate the other's token families.
type EndUserRepository interface {
	CreateEndUser(context.Context, EndUserRegistration) error
	FindEndUserPasswordCredential(context.Context, IdentifierType, []byte) (EndUserPasswordCredential, error)
	UpdateEndUserProfile(context.Context, EndUserProfile, int64, OutboxEvent) (EndUserProfile, error)
	ReplaceEndUserPassword(context.Context, string, []byte, string, int64, time.Time, bool, OutboxEvent) error
	CreateEndUserSession(context.Context, NewEndUserSession) error
	FindEndUserByAccessDigest(context.Context, []byte, EndUserSessionScope, time.Time) (EndUserSession, error)
	RotateEndUserRefresh(context.Context, []byte, EndUserSessionScope, EndUserRefreshRotation) (EndUserSession, error)
	RevokeEndUserSession(context.Context, string, string, EndUserSessionScope, string, time.Time, OutboxEvent) error
	RevokeScopedSessions(context.Context, ScopedSessionRevocation) error
	CreateRecoveryChallenge(context.Context, RecoveryChallenge) error
	ConsumeRecoveryChallenge(context.Context, []byte, []byte, time.Time, OutboxEvent) (RecoveryConsumption, error)
	LinkExternalIdentity(context.Context, ExternalIdentity) error
	FindExternalIdentity(context.Context, string, string, []byte) (ExternalIdentity, error)
}
