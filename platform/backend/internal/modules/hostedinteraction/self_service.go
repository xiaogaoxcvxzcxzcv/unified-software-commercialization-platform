package hostedinteraction

import (
	"context"
	"errors"
	"strings"
	"time"
)

type HostedCapabilities struct {
	Password, Registration, Recovery, Profile, Sessions, AccountCompletion, Entitlement bool
}
type HostedCapabilityPort interface {
	Capabilities(context.Context, Scope) HostedCapabilities
}

func (s *Service) capability(ctx context.Context, scope Scope, name string) bool {
	if s == nil || s.capabilities == nil {
		return false
	}
	c := s.capabilities.Capabilities(ctx, scope)
	switch name {
	case "password":
		return c.Password
	case "registration":
		return c.Registration
	case "recovery":
		return c.Recovery
	case "update_profile":
		return c.Profile
	case "revoke_session":
		return c.Sessions
	case "complete":
		return c.AccountCompletion
	case "entitlement":
		return c.Entitlement
	}
	return false
}
func (s *Service) accountActions(ctx context.Context, scope Scope) []string {
	actions := []string{}
	for _, a := range []string{"update_profile", "change_password", "revoke_session", "complete"} {
		name := a
		if a == "change_password" {
			name = "password"
		}
		if s.capability(ctx, scope, name) {
			actions = append(actions, a)
		}
	}
	return actions
}
func (s *Service) requireAccountAction(ctx context.Context, scope Scope, action string) error {
	for _, v := range s.accountActions(ctx, scope) {
		if v == action {
			return nil
		}
	}
	return ErrCapabilityUnavailable
}

type HostedPresentation struct {
	ProductName  string  `json:"product_name"`
	ThemeVariant *string `json:"theme_variant"`
}

type HostedUserProfile struct {
	UserID      string  `json:"user_id"`
	Version     int64   `json:"version"`
	DisplayName *string `json:"display_name,omitempty"`
	AvatarURL   *string `json:"avatar_url,omitempty"`
	Locale      *string `json:"locale,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
}

type HostedProfilePatchValue struct {
	Set   bool
	Value *string
}
type HostedProfilePatch struct {
	DisplayName, AvatarURL, Locale, Timezone HostedProfilePatchValue
}

type HostedSessionSummary struct {
	SessionID   string    `json:"session_id"`
	Current     bool      `json:"current"`
	DeviceLabel *string   `json:"device_label,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type HostedExternalIdentity struct {
	ExternalIdentityID string    `json:"external_identity_id"`
	Provider           string    `json:"provider"`
	MaskedSubject      *string   `json:"masked_subject,omitempty"`
	Status             string    `json:"status"`
	LinkedAt           time.Time `json:"linked_at"`
	AuditID            *string   `json:"audit_id,omitempty"`
}

type HostedExternalProvider struct{ Provider, Mode, DisplayName string }

type HostedEntitlementSummary struct {
	Revision          int64
	PlanCode          string
	Features          map[string]any
	ValidUntil        *time.Time
	OfflineGraceUntil *time.Time
	UpdatedAt         time.Time
}

type HostedEntitlementPort interface {
	CurrentEntitlementSummary(context.Context, Scope, Actor) (*HostedEntitlementSummary, error)
}

type HostedSelfServicePort interface {
	Capabilities(context.Context, Scope) HostedCapabilities
	StartRegistrationVerification(context.Context, Scope, string, string, string) (string, error)
	RegisterHosted(context.Context, Scope, string, string, string, string, string, string, string) (HostedAuthProof, error)
	StartRecovery(context.Context, Scope, string, string, string) (string, error)
	CompleteRecovery(context.Context, Scope, string, string, string, string, string) error
	GetProfile(context.Context, Scope, Actor) (HostedUserProfile, error)
	PatchProfile(context.Context, Scope, Actor, HostedProfilePatch, int64, string, string) (HostedUserProfile, error)
	ListSessions(context.Context, Scope, Actor) ([]HostedSessionSummary, error)
	ListExternalIdentities(context.Context, Scope, Actor) ([]HostedExternalIdentity, error)
	ChangePassword(context.Context, Scope, Actor, string, string, bool, string, string) error
	RevokeSession(context.Context, Scope, Actor, string, string, string) error
}

type HostedPresentationPort interface {
	ResolveHostedPresentation(context.Context, Scope) (HostedPresentation, error)
}

type HostedAuthBootstrap struct {
	Interaction         Projection               `json:"interaction"`
	Presentation        HostedPresentation       `json:"presentation"`
	Flow                HostedAuthFlow           `json:"flow"`
	PasswordEnabled     bool                     `json:"password_enabled"`
	RegistrationEnabled bool                     `json:"registration_enabled"`
	RecoveryEnabled     bool                     `json:"recovery_enabled"`
	ExternalProviders   []HostedExternalProvider `json:"external_providers"`
}

type HostedAccountBootstrap struct {
	Interaction        Projection                `json:"interaction"`
	Presentation       HostedPresentation        `json:"presentation"`
	Profile            HostedUserProfile         `json:"profile"`
	Sessions           []HostedSessionSummary    `json:"sessions"`
	ExternalIdentities []HostedExternalIdentity  `json:"external_identities"`
	AllowedActions     []string                  `json:"allowed_actions"`
	EntitlementSummary *HostedEntitlementSummary `json:"entitlement_summary,omitempty"`
}

func (s *Service) selfServiceAccess(ctx context.Context, interactionID, browserToken string, route Route) (Interaction, error) {
	interaction, err := s.selfServiceInteractionAccess(ctx, interactionID, browserToken, route)
	if err != nil {
		return Interaction{}, err
	}
	if route == RouteAccount {
		if s.sessions == nil {
			return Interaction{}, ErrAuthenticationNeeded
		}
		if err := s.sessions.ValidateHostedAccountSession(ctx, interaction.Scope, interaction.Actor); err != nil {
			return Interaction{}, err
		}
	}
	return interaction, nil
}

func (s *Service) selfServiceInteractionAccess(ctx context.Context, interactionID, browserToken string, route Route) (Interaction, error) {
	if s == nil {
		return Interaction{}, ErrTemporarilyUnavailable
	}
	access, err := s.repository.ValidateBrowserSession(ctx, interactionID, s.digest("browser-token", browserToken))
	if err != nil {
		return Interaction{}, err
	}
	if access.Interaction.Route != route {
		return Interaction{}, ErrAuthenticationNeeded
	}
	switch access.Interaction.Status {
	case StatusOpened:
	case StatusExpired:
		return Interaction{}, ErrInteractionExpired
	case StatusCompleted, StatusCancelled, StatusFailed, StatusExchanged:
		return Interaction{}, ErrInteractionTerminal
	default:
		return Interaction{}, ErrInvalidGrant
	}
	return access.Interaction, nil
}

func (s *Service) AuthBootstrap(ctx context.Context, interactionID, browserToken string) (HostedAuthBootstrap, error) {
	v, err := s.selfServiceAccess(ctx, interactionID, browserToken, RouteAuth)
	if err != nil {
		return HostedAuthBootstrap{}, err
	}
	p, err := s.presentation.ResolveHostedPresentation(ctx, v.Scope)
	if err != nil {
		return HostedAuthBootstrap{}, err
	}
	if strings.TrimSpace(v.ThemeVariant) != "" {
		theme := v.ThemeVariant
		p.ThemeVariant = &theme
	}
	c := s.selfService.Capabilities(ctx, v.Scope)
	flow, err := s.authFlow(ctx, v)
	if err != nil {
		return HostedAuthBootstrap{}, err
	}
	return HostedAuthBootstrap{Interaction: Project(v), Presentation: p, Flow: flow, PasswordEnabled: c.Password, RegistrationEnabled: c.Registration, RecoveryEnabled: c.Recovery, ExternalProviders: []HostedExternalProvider{}}, nil
}

func (s *Service) StartRegistrationVerification(ctx context.Context, interactionID, browserToken, identifier, key, traceID string) (HostedAuthFlow, error) {
	v, err := s.selfServiceAccess(ctx, interactionID, browserToken, RouteAuth)
	if err != nil {
		return HostedAuthFlow{}, err
	}
	if !s.capability(ctx, v.Scope, "registration") {
		return HostedAuthFlow{}, ErrCapabilityUnavailable
	}
	continuation, err := s.selfService.StartRegistrationVerification(ctx, v.Scope, identifier, key, traceID)
	if err != nil {
		return HostedAuthFlow{}, err
	}
	return s.persistAuthFlow(ctx, v, "registration_verification", identifier, continuation)
}

type RegisterHostedCommand struct{ InteractionID, BrowserToken, Credential, Proof, DisplayName, IdempotencyKey, TraceID string }

func (s *Service) RegisterHosted(ctx context.Context, c RegisterHostedCommand) (Completion, error) {
	browserDigest := s.digest("browser-token", c.BrowserToken)
	access, err := s.repository.ValidateBrowserSession(ctx, c.InteractionID, browserDigest)
	if err != nil {
		return Completion{}, err
	}
	v := access.Interaction
	if v.Status == StatusCompleted {
		grant, grantErr := s.repository.GetCompletionGrant(ctx, v.InteractionID, browserDigest)
		if grantErr != nil || grant.GrantType != "authorization_code" {
			return Completion{}, ErrInvalidGrant
		}
		if s.flows != nil {
			if err := s.flows.DeleteSelfServiceFlow(ctx, v.InteractionID); err != nil {
				return Completion{}, err
			}
		}
		return s.completion(ctx, v, grant)
	}
	if !s.capability(ctx, v.Scope, "registration") {
		return Completion{}, ErrCapabilityUnavailable
	}
	if s.selfService == nil || v.Route != RouteAuth {
		return Completion{}, ErrAuthenticationNeeded
	}
	if v.Status != StatusOpened {
		return Completion{}, ErrInvalidGrant
	}
	secret, err := s.revealAuthFlow(ctx, v, "registration_verification")
	if err != nil {
		return Completion{}, err
	}
	leaseToken, err := s.ids.Token("")
	if err != nil {
		return Completion{}, err
	}
	leaseDigest := s.digest("auth-processing-lease", leaseToken)
	v, _, err = s.repository.BeginAuthentication(ctx, c.InteractionID, browserDigest, leaseDigest, s.authLeaseTTL)
	if err != nil {
		return Completion{}, err
	}
	proof, err := s.selfService.RegisterHosted(ctx, v.Scope, secret.Identifier, c.Credential, secret.Continuation, c.Proof, c.DisplayName, c.IdempotencyKey, c.TraceID)
	if err != nil {
		_ = s.repository.ResetAuthentication(ctx, c.InteractionID, browserDigest, leaseDigest)
		return Completion{}, err
	}
	if proof.ProofID == "" || !proof.ExpiresAt.After(proof.AuthTime) {
		_ = s.repository.ResetAuthentication(ctx, c.InteractionID, browserDigest, leaseDigest)
		return Completion{}, ErrAuthenticationNeeded
	}
	completed, err := s.complete(ctx, v, browserDigest, leaseDigest, "", "", proof.ProofID, "authorization_code", nil)
	if err != nil {
		return Completion{}, err
	}
	if err = s.flows.DeleteSelfServiceFlow(ctx, v.InteractionID); err != nil {
		return Completion{}, err
	}
	return completed, nil
}

func (s *Service) StartRecovery(ctx context.Context, interactionID, browserToken, identifier, key, traceID string) (HostedAuthFlow, error) {
	v, err := s.selfServiceAccess(ctx, interactionID, browserToken, RouteAuth)
	if err != nil {
		return HostedAuthFlow{}, err
	}
	if !s.capability(ctx, v.Scope, "recovery") {
		return HostedAuthFlow{}, ErrCapabilityUnavailable
	}
	continuation, err := s.selfService.StartRecovery(ctx, v.Scope, identifier, key, traceID)
	if err != nil {
		return HostedAuthFlow{}, err
	}
	return s.persistAuthFlow(ctx, v, "recovery_verification", identifier, continuation)
}
func (s *Service) CompleteRecovery(ctx context.Context, interactionID, browserToken, proof, credential, key, traceID string) error {
	v, err := s.selfServiceAccess(ctx, interactionID, browserToken, RouteAuth)
	if err != nil {
		return err
	}
	if !s.capability(ctx, v.Scope, "recovery") {
		return ErrCapabilityUnavailable
	}
	secret, err := s.revealAuthFlow(ctx, v, "recovery_verification")
	if err != nil {
		return err
	}
	if err = s.selfService.CompleteRecovery(ctx, v.Scope, secret.Continuation, proof, credential, key, traceID); err != nil {
		return err
	}
	return s.flows.DeleteSelfServiceFlow(ctx, interactionID)
}

func (s *Service) AccountBootstrap(ctx context.Context, interactionID, browserToken string) (HostedAccountBootstrap, error) {
	v, err := s.selfServiceAccess(ctx, interactionID, browserToken, RouteAccount)
	if err != nil {
		return HostedAccountBootstrap{}, err
	}
	p, err := s.presentation.ResolveHostedPresentation(ctx, v.Scope)
	if err != nil {
		return HostedAccountBootstrap{}, err
	}
	if strings.TrimSpace(v.ThemeVariant) != "" {
		theme := v.ThemeVariant
		p.ThemeVariant = &theme
	}
	profile, err := s.selfService.GetProfile(ctx, v.Scope, v.Actor)
	if err != nil {
		return HostedAccountBootstrap{}, err
	}
	sessions, err := s.selfService.ListSessions(ctx, v.Scope, v.Actor)
	if err != nil {
		return HostedAccountBootstrap{}, err
	}
	external, err := s.selfService.ListExternalIdentities(ctx, v.Scope, v.Actor)
	if err != nil {
		return HostedAccountBootstrap{}, err
	}
	summary, err := s.accountEntitlementSummary(ctx, v)
	if err != nil {
		return HostedAccountBootstrap{}, err
	}
	return HostedAccountBootstrap{Interaction: Project(v), Presentation: p, Profile: profile, Sessions: sessions, ExternalIdentities: external, AllowedActions: s.accountActions(ctx, v.Scope), EntitlementSummary: summary}, nil
}

func (s *Service) accountEntitlementSummary(ctx context.Context, v Interaction) (*HostedEntitlementSummary, error) {
	if s == nil || s.entitlements == nil {
		return nil, nil
	}
	if !s.capability(ctx, v.Scope, "entitlement") {
		return nil, nil
	}
	summary, err := s.entitlements.CurrentEntitlementSummary(ctx, v.Scope, v.Actor)
	if err == nil || summary == nil {
		return summary, err
	}
	if errors.Is(err, ErrCapabilityUnavailable) {
		return nil, nil
	}
	return nil, err
}
func (s *Service) PatchAccountProfile(ctx context.Context, interactionID, browserToken string, patch HostedProfilePatch, version int64, key, traceID string) (HostedUserProfile, error) {
	v, err := s.selfServiceAccess(ctx, interactionID, browserToken, RouteAccount)
	if err != nil {
		return HostedUserProfile{}, err
	}
	if err := s.requireAccountAction(ctx, v.Scope, "update_profile"); err != nil {
		return HostedUserProfile{}, err
	}
	return s.selfService.PatchProfile(ctx, v.Scope, v.Actor, patch, version, key, traceID)
}
func (s *Service) ChangeAccountPassword(ctx context.Context, interactionID, browserToken, current, next string, revokeOthers bool, key, traceID string) error {
	v, err := s.selfServiceAccess(ctx, interactionID, browserToken, RouteAccount)
	if err != nil {
		return err
	}
	if err := s.requireAccountAction(ctx, v.Scope, "change_password"); err != nil {
		return err
	}
	return s.selfService.ChangePassword(ctx, v.Scope, v.Actor, current, next, revokeOthers, key, traceID)
}
func (s *Service) RevokeAccountSession(ctx context.Context, interactionID, browserToken, target, key, traceID string) error {
	if len(key) < 16 || len(key) > 128 {
		return ErrInvalidArgument
	}
	// Session revoke is the sole account-write exception to the outer active-session
	// check: Identity must recover an exact persisted replay after revoking the actor.
	v, err := s.selfServiceInteractionAccess(ctx, interactionID, browserToken, RouteAccount)
	if err != nil {
		return err
	}
	if err := s.requireAccountAction(ctx, v.Scope, "revoke_session"); err != nil {
		return err
	}
	return s.selfService.RevokeSession(ctx, v.Scope, v.Actor, target, key, traceID)
}
