package identity

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"strings"
	"time"
)

const controlledClientSecretDomain = "identity.admin-controlled-client.shared-secret.v1"

var (
	ErrControlledClientConflict = errors.New("controlled administrator client already exists")
	ErrInvalidControlledClient  = errors.New("invalid controlled administrator client")
)

type RegisterControlledClientCommand struct {
	DisplayName string
	ClientType  string
	ExpiresAt   *time.Time
}

type RotateControlledClientCredentialCommand struct {
	ClientID  string
	ExpiresAt *time.Time
}

type IssuedControlledClientCredential struct {
	ClientID     string
	CredentialID string
	ProofType    string
	Secret       string
	ExpiresAt    *time.Time
}

func (s *Service) RegisterControlledAdminClient(ctx context.Context, command RegisterControlledClientCommand) (IssuedControlledClientCredential, error) {
	now := s.now().UTC()
	displayName := strings.TrimSpace(command.DisplayName)
	if displayName == "" || len(displayName) > 128 || (command.ClientType != "cli" && command.ClientType != "automation") {
		return IssuedControlledClientCredential{}, ErrInvalidControlledClient
	}
	if command.ExpiresAt != nil && !command.ExpiresAt.After(now) {
		return IssuedControlledClientCredential{}, ErrInvalidControlledClient
	}
	clientID, err := s.secrets.ID("acli_")
	if err != nil {
		return IssuedControlledClientCredential{}, err
	}
	credentialID, err := s.secrets.ID("acred_")
	if err != nil {
		return IssuedControlledClientCredential{}, err
	}
	secret, err := s.secrets.Token("acsec_")
	if err != nil {
		return IssuedControlledClientCredential{}, err
	}
	event, err := s.controlledClientSecurityEvent("identity.admin_client_registered.v1", "admin_auth_client", clientID, now)
	if err != nil {
		return IssuedControlledClientCredential{}, err
	}
	digest := s.controlledClientSecretDigest(clientID, credentialID, secret)
	if err := s.repository.RegisterControlledClient(ctx, ControlledClientRegistration{
		ClientID: clientID, DisplayName: displayName, ClientType: command.ClientType,
		CredentialID: credentialID, ProofType: "shared_secret_v1", SecretDigest: digest,
		CreatedAt: now, NotBefore: now, ExpiresAt: command.ExpiresAt, OutboxEvent: event,
	}); err != nil {
		return IssuedControlledClientCredential{}, err
	}
	return IssuedControlledClientCredential{ClientID: clientID, CredentialID: credentialID, ProofType: "shared_secret_v1", Secret: secret, ExpiresAt: command.ExpiresAt}, nil
}

func (s *Service) RotateControlledAdminClientCredential(ctx context.Context, command RotateControlledClientCredentialCommand) (IssuedControlledClientCredential, error) {
	now := s.now().UTC()
	clientID := strings.TrimSpace(command.ClientID)
	if clientID == "" || (command.ExpiresAt != nil && !command.ExpiresAt.After(now)) {
		return IssuedControlledClientCredential{}, ErrInvalidControlledClient
	}
	credentialID, err := s.secrets.ID("acred_")
	if err != nil {
		return IssuedControlledClientCredential{}, err
	}
	secret, err := s.secrets.Token("acsec_")
	if err != nil {
		return IssuedControlledClientCredential{}, err
	}
	event, err := s.controlledClientSecurityEvent("identity.admin_client_credential_rotated.v1", "admin_auth_client_credential", credentialID, now)
	if err != nil {
		return IssuedControlledClientCredential{}, err
	}
	digest := s.controlledClientSecretDigest(clientID, credentialID, secret)
	if err := s.repository.AddControlledClientCredential(ctx, ControlledClientCredentialRegistration{
		ClientID: clientID, CredentialID: credentialID, ProofType: "shared_secret_v1",
		SecretDigest: digest, CreatedAt: now, NotBefore: now, ExpiresAt: command.ExpiresAt, OutboxEvent: event,
	}); err != nil {
		return IssuedControlledClientCredential{}, err
	}
	return IssuedControlledClientCredential{ClientID: clientID, CredentialID: credentialID, ProofType: "shared_secret_v1", Secret: secret, ExpiresAt: command.ExpiresAt}, nil
}

func (s *Service) DisableControlledAdminClient(ctx context.Context, clientID string) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ErrInvalidControlledClient
	}
	now := s.now().UTC()
	event, err := s.controlledClientSecurityEvent("identity.admin_client_disabled.v1", "admin_auth_client", clientID, now)
	if err != nil {
		return err
	}
	return s.repository.DisableControlledClient(ctx, clientID, now, event)
}

func (s *Service) RevokeControlledAdminClientCredential(ctx context.Context, clientID, credentialID string) error {
	clientID, credentialID = strings.TrimSpace(clientID), strings.TrimSpace(credentialID)
	if clientID == "" || credentialID == "" {
		return ErrInvalidControlledClient
	}
	now := s.now().UTC()
	event, err := s.controlledClientSecurityEvent("identity.admin_client_credential_revoked.v1", "admin_auth_client_credential", credentialID, now)
	if err != nil {
		return err
	}
	return s.repository.RevokeControlledClientCredential(ctx, clientID, credentialID, now, event)
}

func (s *Service) resolveControlledClient(ctx context.Context, proof *ControlledClientProof, now time.Time) (ControlledClientCredential, error) {
	if proof == nil || proof.ProofType != "shared_secret_v1" || strings.TrimSpace(proof.ClientID) == "" || strings.TrimSpace(proof.CredentialID) == "" || len(proof.Secret) < 32 || len(proof.Secret) > 512 {
		return ControlledClientCredential{}, ErrBearerNotAllowed
	}
	clientID, credentialID := strings.TrimSpace(proof.ClientID), strings.TrimSpace(proof.CredentialID)
	digest := s.controlledClientSecretDigest(clientID, credentialID, proof.Secret)
	credential, err := s.repository.ResolveControlledClientCredential(ctx, clientID, credentialID, proof.ProofType, digest, now)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ControlledClientCredential{}, ErrBearerNotAllowed
		}
		return ControlledClientCredential{}, err
	}
	if !hmac.Equal([]byte(credential.ClientID), []byte(clientID)) || !hmac.Equal([]byte(credential.CredentialID), []byte(credentialID)) {
		return ControlledClientCredential{}, ErrBearerNotAllowed
	}
	return credential, nil
}

func (s *Service) controlledClientSecretDigest(clientID, credentialID, secret string) []byte {
	value := fmt.Sprintf("%s\x00%s\x00%s\x00%s", controlledClientSecretDomain, clientID, credentialID, secret)
	return s.hasher.Digest(value)
}

func (s *Service) controlledClientSecurityEvent(action, targetType, targetID string, now time.Time) (OutboxEvent, error) {
	traceID, err := s.secrets.ID("trace_")
	if err != nil {
		return OutboxEvent{}, err
	}
	event, err := s.securityEvent(action, "offline_operator", targetID, "success", "", traceID, "high", now, nil)
	if err != nil {
		return OutboxEvent{}, err
	}
	event.Payload.TargetType = targetType
	return event, nil
}
