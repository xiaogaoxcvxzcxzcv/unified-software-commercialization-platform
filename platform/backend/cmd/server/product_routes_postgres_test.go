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
