package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"platform.local/capability-platform/backend/internal/modules/audit"
	"platform.local/capability-platform/backend/internal/modules/identity"
	"platform.local/capability-platform/backend/internal/modules/notification"
	"platform.local/capability-platform/backend/internal/platform/config"
)

const (
	notificationPayloadKeyRef = "notification.security.payload.v1"
	notificationDigestKeyRef  = "notification.security.digest.v1"
)

type staticNotificationSecrets map[string][]byte

func (s staticNotificationSecrets) ResolveSecret(_ context.Context, ref string) ([]byte, error) {
	value, ok := s[ref]
	if !ok || len(value) == 0 {
		return nil, notification.ErrProviderUnavailable
	}
	return append([]byte(nil), value...), nil
}

type httpSecurityProviderGateway struct {
	providerRef string
	endpoint    string
	client      *http.Client
}

func (g *httpSecurityProviderGateway) RequireSecurityProvider(_ context.Context, ref string) (notification.SecurityProviderCapability, error) {
	if g == nil || ref != g.providerRef || g.endpoint == "" || g.client == nil {
		return notification.SecurityProviderCapability{}, notification.ErrProviderUnavailable
	}
	return notification.SecurityProviderCapability{DeliveryIDIdempotent: true}, nil
}

func (g *httpSecurityProviderGateway) DeliverSecurity(ctx context.Context, command notification.SecurityProviderRequest) (notification.SecurityProviderResult, error) {
	if _, err := g.RequireSecurityProvider(ctx, command.ProviderRef); err != nil || len(command.Secret) == 0 {
		return notification.SecurityProviderResult{}, notification.ErrProviderUnavailable
	}
	payload, err := json.Marshal(struct {
		DeliveryID      string  `json:"delivery_id"`
		Purpose         string  `json:"purpose"`
		ProductID       string  `json:"product_id"`
		ApplicationID   string  `json:"application_id"`
		TenantID        *string `json:"tenant_id,omitempty"`
		DestinationType string  `json:"destination_type"`
		Destination     string  `json:"destination"`
		Proof           string  `json:"proof"`
		ExpiresAt       string  `json:"expires_at"`
	}{command.DeliveryID, command.Purpose, command.ProductID, command.ApplicationID, command.TenantID, command.DestinationType, command.Destination, command.Proof, command.ExpiresAt.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return notification.SecurityProviderResult{}, notification.ErrDeliveryRejected
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(payload))
	if err != nil {
		return notification.SecurityProviderResult{}, notification.ErrProviderUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+base64.RawURLEncoding.EncodeToString(command.Secret))
	request.Header.Set("Idempotency-Key", command.DeliveryID)
	response, err := g.client.Do(request)
	if err != nil {
		return notification.SecurityProviderResult{}, notification.ErrProviderUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
			return notification.SecurityProviderResult{}, notification.ErrDeliveryRejected
		}
		return notification.SecurityProviderResult{}, notification.ErrProviderUnavailable
	}
	if response.StatusCode == http.StatusNoContent {
		return notification.SecurityProviderResult{}, nil
	}
	var result struct {
		MessageRef string `json:"message_ref"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return notification.SecurityProviderResult{}, notification.ErrDeliveryRejected
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return notification.SecurityProviderResult{}, notification.ErrDeliveryRejected
	}
	return notification.SecurityProviderResult{MessageRef: result.MessageRef}, nil
}

type notificationSecurityDeliveryAdapter struct {
	service     *notification.Service
	providerRef string
}

func (a notificationSecurityDeliveryAdapter) EnqueueSecurity(ctx context.Context, command identity.SecurityDeliveryCommand) error {
	if a.service == nil || a.providerRef == "" {
		return identity.ErrEndUserProviderUnavailable
	}
	destinationType := "email"
	if command.Destination.Type == identity.IdentifierPhone {
		destinationType = "phone"
	}
	err := a.service.EnqueueSecurityDelivery(ctx, notification.SecurityDeliveryCommand{
		DeliveryID: command.DeliveryID, Purpose: command.Purpose, ProductID: command.Scope.ProductID,
		ApplicationID: command.Scope.ApplicationID, TenantID: command.Scope.TenantID,
		ProviderRef: a.providerRef, DestinationType: destinationType, Destination: command.Destination.Value,
		Proof: command.Proof, ExpiresAt: command.ExpiresAt, TraceID: command.TraceID,
	})
	if err != nil {
		return identity.ErrEndUserProviderUnavailable
	}
	return nil
}

type notificationAuditSink struct{ audit *audit.Service }

func (s notificationAuditSink) Publish(ctx context.Context, event notification.ClaimedSecurityOutboxEvent) error {
	if s.audit == nil {
		return errors.New("notification audit sink unavailable")
	}
	var payload struct {
		DeliveryID    string  `json:"delivery_id"`
		Purpose       string  `json:"purpose"`
		ProductID     string  `json:"product_id"`
		ApplicationID string  `json:"application_id"`
		TenantID      *string `json:"tenant_id"`
		ProviderRef   string  `json:"provider_ref"`
		TraceID       string  `json:"trace_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.DeliveryID != event.DeliveryID || payload.ProductID == "" || payload.ApplicationID == "" || payload.TraceID == "" {
		return errors.New("invalid notification outbox payload")
	}
	tenantID := ""
	if payload.TenantID != nil {
		tenantID = *payload.TenantID
	}
	_, err := s.audit.AppendAuditEvent(ctx, audit.Event{
		AuditID: event.EventID, OccurredAt: event.OccurredAt, ActorID: "system.notification",
		ScopeType: "product", ScopeID: payload.ProductID, ProductID: payload.ProductID, TenantID: tenantID,
		Action: "notification.security_delivery_requested", TargetType: "security_delivery", TargetID: payload.DeliveryID,
		Result: "success", TraceID: payload.TraceID, RiskLevel: "high",
		RedactedSummary: map[string]any{"purpose": payload.Purpose, "application_id": payload.ApplicationID, "provider_ref": payload.ProviderRef},
	})
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return nil
	}
	return err
}

type securityNotificationRuntime struct {
	service    *notification.Service
	worker     *notification.Worker
	dispatcher *notification.OutboxDispatcher
	delivery   identity.SecurityDeliveryPort
}

func newSecurityNotificationRuntime(cfg config.SecurityNotification, repository notification.SecurityRepository, outbox notification.SecurityOutboxRepository, auditService *audit.Service) (securityNotificationRuntime, error) {
	if !cfg.Enabled {
		return securityNotificationRuntime{}, nil
	}
	payloadKey := sha256.Sum256([]byte(cfg.PayloadKey))
	digestKey := sha256.Sum256([]byte(cfg.DigestKey))
	secrets := staticNotificationSecrets{
		cfg.ProviderRef:           []byte(cfg.ProviderSecret),
		notificationPayloadKeyRef: payloadKey[:],
		notificationDigestKeyRef:  digestKey[:],
	}
	protector, err := notification.NewAEADSecurityPayloadProtector(notificationPayloadKeyRef, secrets)
	if err != nil {
		return securityNotificationRuntime{}, err
	}
	digester, err := notification.NewHMACSecurityDigester(notificationDigestKeyRef, secrets)
	if err != nil {
		return securityNotificationRuntime{}, err
	}
	gateway := &httpSecurityProviderGateway{providerRef: cfg.ProviderRef, endpoint: cfg.ProviderURL, client: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	service, err := notification.NewService(repository, protector, digester, gateway, nil)
	if err != nil {
		return securityNotificationRuntime{}, err
	}
	worker, err := notification.NewWorker(repository, protector, digester, gateway, secrets, "notification_security_worker", nil)
	if err != nil {
		return securityNotificationRuntime{}, err
	}
	dispatcher, err := notification.NewOutboxDispatcher(outbox, notificationAuditSink{audit: auditService}, "notification_outbox_worker")
	if err != nil {
		return securityNotificationRuntime{}, err
	}
	return securityNotificationRuntime{service: service, worker: worker, dispatcher: dispatcher, delivery: notificationSecurityDeliveryAdapter{service: service, providerRef: cfg.ProviderRef}}, nil
}

var _ notification.SecurityProviderGateway = (*httpSecurityProviderGateway)(nil)
var _ identity.SecurityDeliveryPort = notificationSecurityDeliveryAdapter{}

func clearSecurityNotificationConfig(cfg *config.SecurityNotification) {
	if cfg == nil {
		return
	}
	cfg.ProviderSecret, cfg.PayloadKey, cfg.DigestKey = "", "", ""
}

func securityNotificationInitializationError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("security notification runtime initialization failed: %w", err)
}
