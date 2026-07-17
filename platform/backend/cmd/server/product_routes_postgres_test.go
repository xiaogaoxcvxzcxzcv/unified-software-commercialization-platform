package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/identity"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	"platform.local/capability-platform/backend/internal/modules/product"
	producthttp "platform.local/capability-platform/backend/internal/modules/product/httptransport"
	productpostgres "platform.local/capability-platform/backend/internal/modules/product/postgres"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	applicationhttp "platform.local/capability-platform/backend/internal/modules/productapplication/httptransport"
	applicationpostgres "platform.local/capability-platform/backend/internal/modules/productapplication/postgres"
	"platform.local/capability-platform/backend/internal/modules/tenant"
	tenanthttp "platform.local/capability-platform/backend/internal/modules/tenant/httptransport"
	tenantpostgres "platform.local/capability-platform/backend/internal/modules/tenant/postgres"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/platform/requestid"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	testpostgres "platform.local/capability-platform/backend/internal/testsupport/postgres"
	"platform.local/capability-platform/backend/internal/workflows/clientcontext"
	clientcontexthttp "platform.local/capability-platform/backend/internal/workflows/clientcontext/httptransport"
	"platform.local/capability-platform/backend/internal/workflows/clientregistration"
	clientregistrationhttp "platform.local/capability-platform/backend/internal/workflows/clientregistration/httptransport"
	"platform.local/capability-platform/backend/internal/workflows/productprovisioning"
)

type integrationAuthenticator struct{}

func (integrationAuthenticator) Authenticate(context.Context, *http.Request, bool) (adminrequest.Principal, error) {
	return adminrequest.Principal{AdminUserID: "admin-integration", SessionID: "session-integration"}, nil
}

type integrationAuthorizer struct{}

func (integrationAuthorizer) Authorize(context.Context, adminrequest.Principal, string, adminrequest.TargetScope) (adminrequest.Decision, error) {
	return adminrequest.Decision{Allowed: true}, nil
}

type integrationPlanVerifier struct{}

func (integrationPlanVerifier) ResolveProductCapabilityChange(context.Context, product.TrustedCapabilityChangePlan) (product.TrustedCapabilityChangePlan, error) {
	return product.TrustedCapabilityChangePlan{}, product.ErrUntrustedChangePlan
}

func TestProductApplicationTenantAndClientSessionHTTPFlow(t *testing.T) {
	database := testpostgres.Open(t)
	hasher, err := securevalue.NewHasher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	proofs := product.NewVersionedProofVerifier(hasher)
	products := product.NewService(productpostgres.New(database.Pool), integrationPlanVerifier{}, proofs, securevalue.ID, func() (string, string, error) {
		token, err := securevalue.Token("client_session_")
		if err != nil {
			return "", "", err
		}
		return token, "sha256:" + hasher.DigestHex("product-client-session:"+token), nil
	}, nil)
	applications := productapplication.NewService(applicationpostgres.New(database.Pool), nil, nil)
	tenants := tenant.NewService(tenantpostgres.New(database.Pool), tenant.WithProofDigester(tenantProofDigester{hasher: hasher}))
	guard := adminrequest.New(integrationAuthenticator{}, integrationAuthorizer{}, nil)
	provisioning := productprovisioning.New(products, tenants)
	registration := clientregistration.New(products, applications, hasher, nil)
	clientSessions := clientcontext.New(products, applications, tenants, 15*time.Minute, 5*time.Minute, nil)
	adminHandler := productAdminRouter{
		product:            producthttp.New(products, productProvisionerAdapter{workflow: provisioning}, guard),
		application:        applicationhttp.New(applications, guard, applicationhttp.Config{Environment: productapplication.EnvironmentProduction}),
		tenant:             tenanthttp.New(tenants, guard),
		clientRegistration: clientregistrationhttp.New(registration, guard, nil),
		tenantAdmin:        http.NotFoundHandler(),
	}
	admin := requestid.Middleware(adminHandler)

	created := doJSON(t, admin, http.MethodPost, "/api/v1/admin/products", `{"code":"video-brain","name":"Video Brain","status":"active"}`, "product-create-0001")
	productID := stringField(t, created, "product_id")
	officialTenantID := stringField(t, created, "official_tenant_id")
	if stringField(t, created, "provisioning_state") != "ready" || stringField(t, created, "audit_id") == "" {
		t.Fatalf("product response=%v", created)
	}

	application := doJSON(t, admin, http.MethodPost, "/api/v1/admin/products/"+productID+"/applications", `{"application_code":"desktop","name":"Desktop","platform":"windows","distribution_channel":"official","release_track":"stable","status":"active"}`, "application-create-0001")
	applicationID := stringField(t, application, "application_id")
	doJSON(t, admin, http.MethodPut, "/api/v1/admin/products/"+productID+"/applications/"+applicationID+"/redirects", `{"web_redirect_uris":["https://client.example/callback"],"allowed_origins":["https://client.example"],"deep_links":[],"auth_return_targets":[{"code":"login.complete","uri":"https://client.example/callback"}]}`, "application-redirects-0001")
	expires := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	credential := doJSON(t, admin, http.MethodPost, "/api/v1/admin/products/"+productID+"/applications/"+applicationID+"/clients", fmt.Sprintf(`{"environment":"production","proof_type":"hmac_sha256_v1","expires_at":%q}`, expires), "client-register-0001")
	clientID, credentialID, secret := stringField(t, credential, "client_id"), stringField(t, credential, "credential_id"), stringField(t, credential, "secret")

	proofTime := time.Now().UTC()
	body := fmt.Sprintf(`{"client_id":%q,"credential_id":%q,"client_proof":{"schema_version":1,"type":"hmac_sha256_v1","value":%q,"timestamp":%q},"client_version":"1.0.0","request_nonce":"0123456789abcdef"}`, clientID, credentialID, secret, proofTime.Format(time.RFC3339Nano))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/client/session", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	requestid.Middleware(clientcontexthttp.New(clientSessions)).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("client session status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var session map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	productContext := session["product_context"].(map[string]any)
	applicationContext := session["application_context"].(map[string]any)
	tenantContext := session["tenant_context"].(map[string]any)
	if productContext["product_id"] != productID || applicationContext["application_id"] != applicationID || tenantContext["tenant_id"] != officialTenantID || tenantContext["resolved_by"] != "official_channel" {
		t.Fatalf("session=%v", session)
	}
	clientSessionToken := stringField(t, session, "client_session_token")

	userHasher, err := securevalue.NewHasher(strings.Repeat("identity-hosted-pepper-", 3))
	if err != nil {
		t.Fatal(err)
	}
	endUsers, err := identity.NewEndUserService(
		identitypostgres.New(database.Pool), identity.StrictIdentifierNormalizer{}, identity.Bcrypt{Cost: 10}, userHasher, nil, nil,
		identity.EndUserPolicy{
			AccessTTL: time.Minute, RefreshTTL: time.Hour, RefreshAbsoluteTTL: 2 * time.Hour,
			RefreshRecoveryWindow: time.Minute, RecoveryTTL: time.Minute, RecoveryMaxAttempts: 3,
			LoginWindow: time.Minute, LoginMaximumAttempts: 3, LoginBlockDuration: time.Minute,
			RecentAuthTTL: 10 * time.Minute, HostedAuthProofTTL: 5 * time.Minute,
		}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	hostedRuntime, err := newHostedInteractionRuntime(config.HostedInteraction{
		BaseURL: "https://hosted.example", AllowedOrigin: "https://hosted.example",
		StateKeyRef: "hosted.state.integration.v1", StateKey: strings.Repeat("hosted-state-key-", 3), DigestKey: strings.Repeat("hosted-digest-key-", 3),
		InteractionTTL: 10 * time.Minute, BrowserTTL: 10 * time.Minute, AuthLeaseTTL: 30 * time.Second,
		GrantTTL: 2 * time.Minute, GrantLeaseTTL: 30 * time.Second, AuthProofTTL: 5 * time.Minute,
	}, database.Pool, products, applications, endUsers, hasher)
	if err != nil {
		t.Fatal(err)
	}
	hostedHandler := requestid.Middleware(hostedRuntime.handler)
	createHosted := httptest.NewRequest(http.MethodPost, "/api/v1/hosted/interactions", strings.NewReader(`{"route_id":"hosted.auth","channel":"desktop","return_target_code":"login.complete","state":"state-00000000000000000","nonce":"nonce-00000000000000000","code_challenge":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","code_challenge_method":"S256"}`))
	createHosted.Header.Set("Content-Type", "application/json")
	createHosted.Header.Set("Authorization", "Bearer "+clientSessionToken)
	createHosted.Header.Set("Idempotency-Key", "hosted-create-integration-0001")
	createRecorder := httptest.NewRecorder()
	hostedHandler.ServeHTTP(createRecorder, createHosted)
	if createRecorder.Code != http.StatusCreated || createRecorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("hosted create status=%d body=%s", createRecorder.Code, createRecorder.Body.String())
	}
	var launch map[string]any
	if err = json.Unmarshal(createRecorder.Body.Bytes(), &launch); err != nil {
		t.Fatal(err)
	}
	interactionID := stringField(t, launch, "interaction_id")
	if interactionURL := stringField(t, launch, "interaction_url"); interactionURL != "https://hosted.example/ui/v1/auth?interaction_id="+interactionID {
		t.Fatalf("hosted interaction URL=%q", interactionURL)
	}

	openPath := "/api/v1/hosted/interactions/" + interactionID + "/browser-session"
	firstOpen := httptest.NewRequest(http.MethodPost, openPath, nil)
	firstOpenRecorder := httptest.NewRecorder()
	hostedHandler.ServeHTTP(firstOpenRecorder, firstOpen)
	if firstOpenRecorder.Code != http.StatusOK || len(firstOpenRecorder.Result().Cookies()) != 1 {
		t.Fatalf("hosted first open status=%d body=%s", firstOpenRecorder.Code, firstOpenRecorder.Body.String())
	}
	firstCookie := firstOpenRecorder.Result().Cookies()[0]
	secondOpen := httptest.NewRequest(http.MethodPost, openPath, nil)
	secondOpenRecorder := httptest.NewRecorder()
	hostedHandler.ServeHTTP(secondOpenRecorder, secondOpen)
	if secondOpenRecorder.Code != http.StatusOK || len(secondOpenRecorder.Result().Cookies()) != 1 {
		t.Fatalf("hosted reopen status=%d body=%s", secondOpenRecorder.Code, secondOpenRecorder.Body.String())
	}
	secondCookie := secondOpenRecorder.Result().Cookies()[0]
	if firstCookie.Value == secondCookie.Value || !firstCookie.Secure || !firstCookie.HttpOnly || firstCookie.Path != "/" {
		t.Fatalf("hosted cookie rotation invalid: first=%+v second=%+v", firstCookie, secondCookie)
	}

	readPath := "/api/v1/hosted/interactions/" + interactionID
	oldRead := httptest.NewRequest(http.MethodGet, readPath, nil)
	oldRead.AddCookie(firstCookie)
	oldReadRecorder := httptest.NewRecorder()
	hostedHandler.ServeHTTP(oldReadRecorder, oldRead)
	if oldReadRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("revoked hosted cookie status=%d body=%s", oldReadRecorder.Code, oldReadRecorder.Body.String())
	}
	newRead := httptest.NewRequest(http.MethodGet, readPath, nil)
	newRead.AddCookie(secondCookie)
	newReadRecorder := httptest.NewRecorder()
	hostedHandler.ServeHTTP(newReadRecorder, newRead)
	if newReadRecorder.Code != http.StatusOK || !strings.Contains(newReadRecorder.Body.String(), `"status":"opened"`) || strings.Contains(newReadRecorder.Body.String(), "state-000") {
		t.Fatalf("recovered hosted interaction status=%d body=%s", newReadRecorder.Code, newReadRecorder.Body.String())
	}
}

func doJSON(t *testing.T, handler http.Handler, method, path, body, key string) map[string]any {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code < 200 || recorder.Code >= 300 {
		t.Fatalf("%s %s status=%d body=%s", method, path, recorder.Code, recorder.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func stringField(t *testing.T, value map[string]any, key string) string {
	t.Helper()
	result, ok := value[key].(string)
	if !ok || result == "" {
		t.Fatalf("field %q missing from %v", key, value)
	}
	return result
}
