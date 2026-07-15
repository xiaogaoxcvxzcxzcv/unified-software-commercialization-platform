package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	accesspostgres "platform.local/capability-platform/backend/internal/modules/accesscontrol/postgres"
	"platform.local/capability-platform/backend/internal/modules/audit"
	auditpostgres "platform.local/capability-platform/backend/internal/modules/audit/postgres"
	"platform.local/capability-platform/backend/internal/modules/identity"
	identityhttp "platform.local/capability-platform/backend/internal/modules/identity/httptransport"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	"platform.local/capability-platform/backend/internal/modules/product"
	producthttp "platform.local/capability-platform/backend/internal/modules/product/httptransport"
	productpostgres "platform.local/capability-platform/backend/internal/modules/product/postgres"
	"platform.local/capability-platform/backend/internal/modules/tenant"
	tenantpostgres "platform.local/capability-platform/backend/internal/modules/tenant/postgres"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	platformserver "platform.local/capability-platform/backend/internal/platform/server"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
	"platform.local/capability-platform/backend/internal/workflows/productprovisioning"
)

const adminWriteOrigin = "https://admin-write.integration.test"

type adminWritePlanVerifier struct{}

func (adminWritePlanVerifier) ResolveProductCapabilityChange(context.Context, product.TrustedCapabilityChangePlan) (product.TrustedCapabilityChangePlan, error) {
	return product.TrustedCapabilityChangePlan{}, product.ErrUntrustedChangePlan
}

func TestAdminCookieProductWriteProofWithPostgreSQL(t *testing.T) {
	database := testpostgres.Open(t)
	ctx := context.Background()

	hasher, err := securevalue.NewHasher("admin-write-integration-pepper-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	accessService := accesscontrol.NewService(accesspostgres.New(database.Pool), nil)
	identityService, err := identity.NewService(
		identitypostgres.New(database.Pool),
		accessService,
		identity.Bcrypt{Cost: 10},
		hasher,
		identity.Policy{
			AccessTTL:            15 * time.Minute,
			RefreshTTL:           time.Hour,
			LoginWindow:          10 * time.Minute,
			LoginMaximumAttempts: 5,
			LoginBlockDuration:   10 * time.Minute,
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := identityService.BootstrapAdminIdentity(ctx, "admin-write@example.test", "Admin Write Integration", []byte("correct-password-123"))
	if err != nil {
		t.Fatal(err)
	}
	if err := accessService.BootstrapPlatformAdmin(ctx, accesscontrol.BootstrapCommand{
		BindingID:   "binding-admin-write",
		RoleID:      "role-admin-write",
		AdminUserID: adminID,
		Now:         time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	productService := product.NewService(
		productpostgres.New(database.Pool),
		adminWritePlanVerifier{},
		product.NewVersionedProofVerifier(hasher),
		securevalue.ID,
		func() (string, string, error) {
			token, err := securevalue.Token("client_session_")
			if err != nil {
				return "", "", err
			}
			return token, "sha256:" + hasher.DigestHex("product-client-session:"+token), nil
		},
		nil,
	)
	tenantService := tenant.NewService(tenantpostgres.New(database.Pool), tenant.WithProofDigester(tenantProofDigester{hasher: hasher}))
	provisioning := productprovisioning.New(productService, tenantService)
	auditService := audit.NewService(auditpostgres.New(database.Pool))
	guard := adminrequest.New(
		newAdminRequestAuthenticator(identityService, []string{adminWriteOrigin}),
		adminRequestAuthorizer{access: accessService},
		adminDenialRecorder{audit: auditService},
	)

	modules := platformserver.NewModuleRegistrar()
	if err := modules.Register("/api/v1/admin/auth/", identityhttp.New(identityService, identityhttp.Config{AllowedOrigins: []string{adminWriteOrigin}})); err != nil {
		t.Fatal(err)
	}
	productHandler := producthttp.New(productService, productProvisionerAdapter{workflow: provisioning}, guard)
	if err := modules.Register("/api/v1/admin/", productAdminRouter{product: productHandler}); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewTLSServer(platformserver.NewHandler(logger, nil, time.Second, platformserver.BuildInfo{Version: "integration"}, modules))
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar

	login := adminWriteJSONRequest(t, client, http.MethodPost, server.URL+"/api/v1/admin/auth/login", adminWriteOrigin, "", "", map[string]any{
		"identifier": "admin-write@example.test",
		"credential": "correct-password-123",
		"transport":  "cookie",
	})
	if login.StatusCode != http.StatusOK {
		login.Body.Close()
		t.Fatalf("cookie login status = %d, want %d", login.StatusCode, http.StatusOK)
	}
	var authenticated identity.AdminSession
	if err := json.NewDecoder(login.Body).Decode(&authenticated); err != nil {
		login.Body.Close()
		t.Fatal(err)
	}
	login.Body.Close()
	if authenticated.SessionID == "" || authenticated.CSRFToken == nil || *authenticated.CSRFToken == "" {
		t.Fatal("cookie login did not return the required session proof")
	}

	idempotencyKey := "admin-write-product-0001"
	rejected := []struct {
		name   string
		origin string
		csrf   string
	}{
		{name: "missing origin", csrf: *authenticated.CSRFToken},
		{name: "wrong origin", origin: "https://untrusted.integration.test", csrf: *authenticated.CSRFToken},
		{name: "missing csrf", origin: adminWriteOrigin},
		{name: "wrong csrf", origin: adminWriteOrigin, csrf: "incorrect-csrf-proof"},
	}
	for _, test := range rejected {
		t.Run(test.name, func(t *testing.T) {
			response := adminWriteJSONRequest(t, client, http.MethodPost, server.URL+"/api/v1/admin/products", test.origin, test.csrf, idempotencyKey, map[string]any{
				"code":   "cookie-proof-product",
				"name":   "Cookie Proof Product",
				"status": "active",
			})
			assertAdminWriteProofRejected(t, response)
			assertAdminWriteProductCount(t, productService, 0)
			assertAdminWriteSessionAvailable(t, client, server.URL, authenticated)
		})
	}

	created := adminWriteJSONRequest(t, client, http.MethodPost, server.URL+"/api/v1/admin/products", adminWriteOrigin, *authenticated.CSRFToken, idempotencyKey, map[string]any{
		"code":   "cookie-proof-product",
		"name":   "Cookie Proof Product",
		"status": "active",
	})
	if created.StatusCode != http.StatusCreated {
		created.Body.Close()
		t.Fatalf("authorized product create status = %d, want %d", created.StatusCode, http.StatusCreated)
	}
	var createdBody struct {
		ProductID         string `json:"product_id"`
		OfficialTenantID  string `json:"official_tenant_id"`
		ProvisioningState string `json:"provisioning_state"`
	}
	if err := json.NewDecoder(created.Body).Decode(&createdBody); err != nil {
		created.Body.Close()
		t.Fatal(err)
	}
	created.Body.Close()
	if createdBody.ProductID == "" || createdBody.OfficialTenantID == "" || createdBody.ProvisioningState != "ready" {
		t.Fatal("authorized product create did not finish the Product and official Tenant workflow")
	}
	assertAdminWriteProductCount(t, productService, 1)
	assertAdminWriteSessionAvailable(t, client, server.URL, authenticated)
}

func adminWriteJSONRequest(t *testing.T, client *http.Client, method, target, origin, csrf, idempotencyKey string, body any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(method, target, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertAdminWriteProofRejected(t *testing.T, response *http.Response) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("unsafe cookie write status = %d, want %d", response.StatusCode, http.StatusForbidden)
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "admin_auth.request_proof_failed" {
		t.Fatalf("unsafe cookie write code = %q, want stable request proof rejection", problem.Code)
	}
}

func assertAdminWriteProductCount(t *testing.T, service *product.Service, want int) {
	t.Helper()
	products, err := service.ListProducts(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != want {
		t.Fatalf("product count = %d, want %d", len(products), want)
	}
}

func assertAdminWriteSessionAvailable(t *testing.T, client *http.Client, serverURL string, authenticated identity.AdminSession) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, serverURL+"/api/v1/admin/auth/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("session after rejected write status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var current identity.AdminSession
	if err := json.NewDecoder(response.Body).Decode(&current); err != nil {
		t.Fatal(err)
	}
	if current.SessionID != authenticated.SessionID || current.CSRFToken == nil || authenticated.CSRFToken == nil || *current.CSRFToken != *authenticated.CSRFToken {
		t.Fatal("rejected write changed or invalidated the authenticated cookie session")
	}
}
