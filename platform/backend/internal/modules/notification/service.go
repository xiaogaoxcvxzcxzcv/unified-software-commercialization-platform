package notification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
	"unicode/utf8"
)

type Service struct {
	repository  SecurityRepository
	protector   SecurityPayloadProtector
	digester    SecurityDigestPort
	gateway     SecurityProviderGateway
	now         func() time.Time
	maxAttempts int
}

func NewService(repository SecurityRepository, protector SecurityPayloadProtector, digester SecurityDigestPort, gateway SecurityProviderGateway, now func() time.Time) (*Service, error) {
	if repository == nil || protector == nil || digester == nil || gateway == nil {
		return nil, ErrProviderUnavailable
	}
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, protector: protector, digester: digester, gateway: gateway, now: now, maxAttempts: 5}, nil
}

func (s *Service) EnqueueSecurityDelivery(ctx context.Context, command SecurityDeliveryCommand) error {
	now := s.now().UTC()
	if !validSecurityCommand(command, now) {
		return ErrInvalidSecurityDelivery
	}
	capability, err := s.gateway.RequireSecurityProvider(ctx, command.ProviderRef)
	if err != nil || !capability.DeliveryIDIdempotent {
		return ErrProviderUnavailable
	}
	payloadContext := payloadContextFromCommand(command)
	requestDigest, err := s.digester.Digest(ctx, "notification.security.request.v1",
		command.DeliveryID, command.Purpose, command.ProductID, command.ApplicationID, optionalString(command.TenantID),
		command.ProviderRef, command.DestinationType, command.ExpiresAt.UTC().Format(time.RFC3339Nano), command.TraceID,
		command.Destination, command.Proof)
	if err != nil || len(requestDigest) != 32 {
		return ErrProviderUnavailable
	}
	protected, err := s.protector.Seal(ctx, payloadContext, SecurityPayload{Destination: command.Destination, Proof: command.Proof})
	if err != nil {
		return normalizeProviderError(err)
	}
	eventPayload, _ := json.Marshal(map[string]any{
		"delivery_id": command.DeliveryID, "purpose": command.Purpose, "product_id": command.ProductID,
		"application_id": command.ApplicationID, "tenant_id": command.TenantID, "provider_ref": command.ProviderRef,
		"expires_at": command.ExpiresAt.UTC(), "trace_id": command.TraceID,
	})
	eventDigest := sha256.Sum256([]byte("notification-security-event\x00" + command.DeliveryID))
	_, err = s.repository.CreateSecurityDelivery(ctx, CreateSecurityDeliveryRecord{
		Delivery: SecurityDelivery{
			DeliveryID: command.DeliveryID, RequestDigest: requestDigest, Purpose: command.Purpose,
			ProductID: command.ProductID, ApplicationID: command.ApplicationID, TenantID: cloneString(command.TenantID),
			ProviderRef: command.ProviderRef, DestinationType: command.DestinationType, Payload: protected,
			Status: "pending", MaxAttempts: s.maxAttempts, NextAttemptAt: now, CreatedAt: now,
			ExpiresAt: command.ExpiresAt.UTC(), TraceID: command.TraceID,
		},
		Event: SecurityOutboxEvent{EventID: "evt_notification_" + hex.EncodeToString(eventDigest[:16]), DeliveryID: command.DeliveryID, EventType: "notification.security-delivery-requested.v1", Payload: eventPayload, OccurredAt: now},
	})
	return err
}

func validSecurityCommand(command SecurityDeliveryCommand, now time.Time) bool {
	validPurpose := command.Purpose == "registration_verify" || command.Purpose == "password_recovery" || command.Purpose == "account_security"
	validDestinationType := command.DestinationType == "email" || command.DestinationType == "phone" || command.DestinationType == "provider_subject"
	return validIdentifier(command.DeliveryID, 1, 160) && validPurpose && validIdentifier(command.ProductID, 1, 160) &&
		validIdentifier(command.ApplicationID, 1, 160) && (command.TenantID == nil || validIdentifier(*command.TenantID, 1, 160)) &&
		validIdentifier(command.ProviderRef, 1, 160) && validDestinationType && validSecretInput(command.Destination, 1, 1024) &&
		validSecretInput(command.Proof, 1, 4096) && command.ExpiresAt.After(now) && validIdentifier(command.TraceID, 1, 256)
}

func validIdentifier(value string, min, max int) bool {
	if !utf8.ValidString(value) || len(value) < min || len(value) > max {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7e || r == ',' {
			return false
		}
	}
	return true
}

func validSecretInput(value string, min, max int) bool {
	return utf8.ValidString(value) && len(value) >= min && len(value) <= max
}

func optionalString(value *string) string {
	if value == nil {
		return "-"
	}
	return "+" + *value
}

func payloadContextFromCommand(command SecurityDeliveryCommand) SecurityPayloadContext {
	return SecurityPayloadContext{DeliveryID: command.DeliveryID, Purpose: command.Purpose, ProductID: command.ProductID,
		ApplicationID: command.ApplicationID, TenantID: cloneString(command.TenantID), ProviderRef: command.ProviderRef,
		DestinationType: command.DestinationType, ExpiresAt: command.ExpiresAt.UTC(), TraceID: command.TraceID}
}

func payloadContextFromDelivery(delivery SecurityDelivery) SecurityPayloadContext {
	return SecurityPayloadContext{DeliveryID: delivery.DeliveryID, Purpose: delivery.Purpose, ProductID: delivery.ProductID,
		ApplicationID: delivery.ApplicationID, TenantID: cloneString(delivery.TenantID), ProviderRef: delivery.ProviderRef,
		DestinationType: delivery.DestinationType, ExpiresAt: delivery.ExpiresAt.UTC(), TraceID: delivery.TraceID}
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
