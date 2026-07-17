package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/notification"
)

func TestHTTPSecurityProviderGatewayBindsDeliveryIdempotencyAndSecret(t *testing.T) {
	secret := []byte(strings.Repeat("provider-secret-", 3))
	var calls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.Header.Get("Idempotency-Key") != "delivery-123456" || r.Header.Get("Authorization") != "Bearer "+base64.RawURLEncoding.EncodeToString(secret) {
			t.Fatalf("unexpected provider request headers: method=%s idempotency=%q authorization=%q", r.Method, r.Header.Get("Idempotency-Key"), r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), `"destination":"person@example.com"`) || !strings.Contains(string(raw), `"proof":"private-proof-123456"`) {
			t.Fatalf("provider request omitted protected payload: %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message_ref": "provider-message-123"})
	}))
	defer server.Close()
	gateway := &httpSecurityProviderGateway{providerRef: "notification.security.primary", endpoint: server.URL, client: server.Client()}
	capability, err := gateway.RequireSecurityProvider(context.Background(), "notification.security.primary")
	if err != nil || !capability.DeliveryIDIdempotent {
		t.Fatalf("RequireSecurityProvider() = %+v, %v", capability, err)
	}
	result, err := gateway.DeliverSecurity(context.Background(), notification.SecurityProviderRequest{
		DeliveryID: "delivery-123456", Purpose: "registration_verify", ProductID: "product-a", ApplicationID: "application-a",
		ProviderRef: "notification.security.primary", DestinationType: "email", Destination: "person@example.com",
		Proof: "private-proof-123456", ExpiresAt: time.Now().Add(time.Minute), Secret: append([]byte(nil), secret...),
	})
	if err != nil || result.MessageRef != "provider-message-123" || calls != 1 {
		t.Fatalf("DeliverSecurity() = %+v, %v; calls=%d", result, err, calls)
	}
}

func TestHTTPSecurityProviderGatewayClassifiesWithoutLeakingBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("private-provider-response"))
	}))
	defer server.Close()
	gateway := &httpSecurityProviderGateway{providerRef: "notification.security.primary", endpoint: server.URL, client: server.Client()}
	_, err := gateway.DeliverSecurity(context.Background(), notification.SecurityProviderRequest{DeliveryID: "delivery-123456", ProviderRef: "notification.security.primary", Secret: []byte(strings.Repeat("secret", 8))})
	if err != notification.ErrDeliveryRejected || strings.Contains(err.Error(), "private-provider-response") {
		t.Fatalf("provider error = %v", err)
	}
}
