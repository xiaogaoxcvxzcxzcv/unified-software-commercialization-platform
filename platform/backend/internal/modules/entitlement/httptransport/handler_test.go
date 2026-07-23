package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/entitlement"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
)

type serviceStub struct {
	checkCommand   entitlement.CheckEntitlementCommand
	grantCommand   entitlement.GrantEntitlementCommand
	mutateCommand  entitlement.MutateEntitlementCommand
	historyQuery   entitlement.HistoryQuery
	listQuery      entitlement.AdminListQuery
	currentProduct entitlement.ProductContext
	currentTenant  entitlement.TenantContext
	currentUser    entitlement.UserContext
	err            error
}

func (s *serviceStub) CheckEntitlement(_ context.Context, command entitlement.CheckEntitlementCommand) (entitlement.CheckDecision, error) {
	s.checkCommand = command
	return entitlement.CheckDecision{
		Allowed: true, DecisionStage: "entitlement", Revision: 7, Features: map[string]any{"pro.member": true},
		ServerTime: time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC),
	}, s.err
}

func (s *serviceStub) GrantEntitlement(_ context.Context, command entitlement.GrantEntitlementCommand) (entitlement.GrantResult, error) {
	s.grantCommand = command
	return grantResult("grant-a"), s.err
}

func (s *serviceStub) ExtendEntitlement(_ context.Context, command entitlement.MutateEntitlementCommand) (entitlement.GrantResult, error) {
	s.mutateCommand = command
	return grantResult("grant-extend-a"), s.err
}

func (s *serviceStub) ReplaceEntitlement(context.Context, entitlement.MutateEntitlementCommand) (entitlement.GrantResult, error) {
	return entitlement.GrantResult{}, errors.New("not used")
}

func (s *serviceStub) RevokeEntitlement(_ context.Context, command entitlement.MutateEntitlementCommand) (entitlement.GrantResult, error) {
	s.mutateCommand = command
	return grantResult("grant-revoke-a"), s.err
}

func (s *serviceStub) GetCurrentEntitlements(_ context.Context, product entitlement.ProductContext, tenant entitlement.TenantContext, user entitlement.UserContext) (entitlement.EntitlementSummary, error) {
	s.currentProduct, s.currentTenant, s.currentUser = product, tenant, user
	return entitlement.EntitlementSummary{ProductID: product.ProductID, TenantID: tenant.TenantID, UserID: user.UserID, Revision: 3, EffectiveFeatures: map[string]any{"pro.member": true}, UpdatedAt: time.Date(2026, 7, 23, 10, 1, 0, 0, time.UTC)}, s.err
}

func (s *serviceStub) ListCurrentEntitlements(_ context.Context, query entitlement.AdminListQuery) ([]entitlement.EntitlementSummary, error) {
	s.listQuery = query
	return []entitlement.EntitlementSummary{{ProductID: query.ProductID, TenantID: query.TenantID, UserID: "user-a", Revision: 3, PlanCode: "pro", EffectiveFeatures: map[string]any{"pro.member": true}, UpdatedAt: time.Date(2026, 7, 23, 10, 1, 0, 0, time.UTC)}}, s.err
}

func (s *serviceStub) ListHistory(_ context.Context, query entitlement.HistoryQuery) ([]entitlement.LedgerEntry, error) {
	s.historyQuery = query
	return []entitlement.LedgerEntry{{LedgerID: "ledger-a", OperationType: entitlement.EffectGrant, OperationID: "grant-a", GrantID: "grant-a", AfterRevision: 1, AuditID: "audit-a", TraceID: "trace-a", CreatedAt: time.Date(2026, 7, 23, 10, 2, 0, 0, time.UTC)}}, s.err
}

func TestAdminListCurrentEntitlementsUsesReadPermissionAndTenantScope(t *testing.T) {
	service := &serviceStub{}
	authorizer := &authorizerStub{allowed: true}
	handler := New(service, adminrequest.New(authStub{}, authorizer, denialStub{}), nil)
	recorder := serve(handler, http.MethodGet, "/api/v1/admin/entitlements?product_id=product-a&tenant_id=tenant-a&user_id=user-a&page_size=20", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if authorizer.permission != readPermission || authorizer.target.ProductID != "product-a" || authorizer.target.TenantID != "tenant-a" {
		t.Fatalf("authorization target=%+v permission=%s", authorizer.target, authorizer.permission)
	}
	if service.listQuery.UserID != "user-a" || service.listQuery.Limit != 20 {
		t.Fatalf("list query=%+v", service.listQuery)
	}
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil || len(decoded["items"].([]any)) != 1 {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
}

func grantResult(grantID string) entitlement.GrantResult {
	return entitlement.GrantResult{
		EntitlementID: "entitlement-a", GrantID: grantID, Revision: 2, AuditID: "audit-a",
		Decision: entitlement.CheckDecision{Allowed: true, DecisionStage: "entitlement", Revision: 2, Features: map[string]any{"pro.member": true}, ServerTime: time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)},
	}
}

type resolverStub struct {
	value UserSessionContext
	err   error
}

func (s resolverStub) ResolveUserSession(context.Context, string) (UserSessionContext, error) {
	return s.value, s.err
}

type capabilityStub struct {
	enabled   bool
	productID string
	packageID string
}

func (s *capabilityStub) IsPackageEnabled(_ context.Context, productID, packageID string) (bool, error) {
	s.productID, s.packageID = productID, packageID
	return s.enabled, nil
}

type authStub struct{}

func (authStub) Authenticate(context.Context, *http.Request, bool) (adminrequest.Principal, error) {
	return adminrequest.Principal{AdminUserID: "admin-a", SessionID: "session-a", AuthTime: time.Now()}, nil
}

type authorizerStub struct {
	permission string
	target     adminrequest.TargetScope
	allowed    bool
}

func (s *authorizerStub) Authorize(_ context.Context, _ adminrequest.Principal, permission string, target adminrequest.TargetScope) (adminrequest.Decision, error) {
	s.permission, s.target = permission, target
	return adminrequest.Decision{Allowed: s.allowed}, nil
}

type denialStub struct{}

func (denialStub) RecordAuthorizationDenial(context.Context, adminrequest.Denial) error { return nil }

func TestUserRoutesUseResolvedUserScopeAndRejectClientScope(t *testing.T) {
	service := &serviceStub{}
	handler := New(service, nil, resolverStub{value: UserSessionContext{UserID: "user-a", ProductID: "product-a", TenantID: "tenant-a"}})
	recorder := serve(handler, http.MethodPost, "/api/v1/entitlements/check", `{"requested_features":["pro.member"],"product_id":"evil"}`, map[string]string{"Authorization": "Bearer user-token-0000000001", "Content-Type": "application/json"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("client scope should be rejected by strict JSON, status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = serve(handler, http.MethodPost, "/api/v1/entitlements/check", `{"requested_features":["pro.member"]}`, map[string]string{"Authorization": "Bearer user-token-0000000001", "Content-Type": "application/json"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.checkCommand.Product.ProductID != "product-a" || service.checkCommand.Tenant.TenantID != "tenant-a" || service.checkCommand.User.UserID != "user-a" {
		t.Fatalf("resolved scope was not used: %+v", service.checkCommand)
	}
}

func TestUserRoutesRejectWhenEntitlementPackageDisabled(t *testing.T) {
	service := &serviceStub{}
	capabilities := &capabilityStub{enabled: false}
	handler := New(service, nil, resolverStub{value: UserSessionContext{UserID: "user-a", ProductID: "product-a", TenantID: "tenant-a"}})
	handler.ConfigureCapabilityChecker(capabilities)
	recorder := serve(handler, http.MethodPost, "/api/v1/entitlements/check", `{"requested_features":["pro.member"]}`, map[string]string{"Authorization": "Bearer user-token-0000000001", "Content-Type": "application/json"})
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "ENTITLEMENT_CAPABILITY_DISABLED") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if capabilities.productID != "product-a" || capabilities.packageID != "package.entitlement" {
		t.Fatalf("capability check=%s %s", capabilities.productID, capabilities.packageID)
	}
	if service.checkCommand.Product.ProductID != "" {
		t.Fatalf("service should not be called when capability is disabled: %+v", service.checkCommand)
	}
}

func TestAdminGrantAuthorizesTenantScopeAndRequiresIdempotency(t *testing.T) {
	service := &serviceStub{}
	authorizer := &authorizerStub{allowed: true}
	handler := New(service, adminrequest.New(authStub{}, authorizer, denialStub{}), nil)
	body := `{"user_id":"user-a","product_id":"product-a","tenant_id":"tenant-a","policy_id":"policy-a","policy_version":1,"validity":{"rule":"fixed_duration","duration_seconds":3600},"source":{"source_type":"admin","source_id":"manual-a","source_effect_id":"effect-a"}}`
	recorder := serve(handler, http.MethodPost, "/api/v1/admin/entitlements", body, map[string]string{"Content-Type": "application/json"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = serve(handler, http.MethodPost, "/api/v1/admin/entitlements", body, map[string]string{"Content-Type": "application/json", "Idempotency-Key": "idempotency-grant-0001"})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if authorizer.permission != managePermission || authorizer.target.ProductID != "product-a" || authorizer.target.TenantID != "tenant-a" {
		t.Fatalf("authorization target=%+v permission=%s", authorizer.target, authorizer.permission)
	}
	if service.grantCommand.Admin.AdminID != "admin-a" || service.grantCommand.IdempotencyKey != "idempotency-grant-0001" || service.grantCommand.Validity.Duration != time.Hour {
		t.Fatalf("grant command=%+v", service.grantCommand)
	}
}

func TestAdminRoutesRejectWhenEntitlementPackageDisabled(t *testing.T) {
	service := &serviceStub{}
	authorizer := &authorizerStub{allowed: true}
	capabilities := &capabilityStub{enabled: false}
	handler := New(service, adminrequest.New(authStub{}, authorizer, denialStub{}), nil)
	handler.ConfigureCapabilityChecker(capabilities)
	recorder := serve(handler, http.MethodGet, "/api/v1/admin/entitlements?product_id=product-a&tenant_id=tenant-a", "", nil)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "ENTITLEMENT_CAPABILITY_DISABLED") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if authorizer.permission != readPermission {
		t.Fatalf("authorization should still run before capability gate, permission=%s", authorizer.permission)
	}
	if capabilities.productID != "product-a" || capabilities.packageID != "package.entitlement" {
		t.Fatalf("capability check=%s %s", capabilities.productID, capabilities.packageID)
	}
	if service.listQuery.ProductID != "" {
		t.Fatalf("service should not be called when capability is disabled: %+v", service.listQuery)
	}
}

func TestAdminRevokeUsesRevokePermissionAndTargetGrant(t *testing.T) {
	service := &serviceStub{}
	authorizer := &authorizerStub{allowed: true}
	handler := New(service, adminrequest.New(authStub{}, authorizer, denialStub{}), nil)
	body := `{"user_id":"user-a","product_id":"product-a","tenant_id":"tenant-a","expected_revision":2,"reason_code":"manual_revoke"}`
	recorder := serve(handler, http.MethodPost, "/api/v1/admin/entitlements/grant-a/revoke", body, map[string]string{"Content-Type": "application/json", "Idempotency-Key": "idempotency-revoke-0001"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if authorizer.permission != revokePermission {
		t.Fatalf("permission=%s", authorizer.permission)
	}
	if service.mutateCommand.TargetGrantID != "grant-a" || service.mutateCommand.ExpectedRevision != 2 {
		t.Fatalf("mutate command=%+v", service.mutateCommand)
	}
}

func TestHistoryRoutesAreScopedAndStrict(t *testing.T) {
	service := &serviceStub{}
	handler := New(service, nil, resolverStub{value: UserSessionContext{UserID: "user-a", ProductID: "product-a", TenantID: "tenant-a"}})
	recorder := serve(handler, http.MethodGet, "/api/v1/entitlements/history?page_size=20&evil=1", "", map[string]string{"Authorization": "Bearer user-token-0000000001"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = serve(handler, http.MethodGet, "/api/v1/entitlements/history?page_size=20", "", map[string]string{"Authorization": "Bearer user-token-0000000001"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.historyQuery.ProductID != "product-a" || service.historyQuery.TenantID != "tenant-a" || service.historyQuery.UserID != "user-a" || service.historyQuery.Limit != 20 {
		t.Fatalf("history query=%+v", service.historyQuery)
	}
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil || len(decoded["items"].([]any)) != 1 {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
}

func serve(handler http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
