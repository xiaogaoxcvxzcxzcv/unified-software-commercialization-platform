package accountaccess

import (
	"context"
	"errors"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
)

type admissionStub struct {
	result productuseraccess.Admission
	err    error
	seen   struct {
		product productuseraccess.ProductContext
		tenant  *productuseraccess.TenantContext
		user    productuseraccess.UserContext
	}
}

func (s *admissionStub) EvaluateScopedAdmission(_ context.Context, product productuseraccess.ProductContext, tenant *productuseraccess.TenantContext, user productuseraccess.UserContext) (productuseraccess.Admission, error) {
	s.seen.product, s.seen.tenant, s.seen.user = product, tenant, user
	return s.result, s.err
}

func TestDecisionUsesFixedIdentityProductTenantOrder(t *testing.T) {
	stub := &admissionStub{result: productuseraccess.Admission{Allowed: false, Code: "PRODUCT_USER_ACCESS_SUSPENDED"}}
	service := New(stub)

	decision, err := service.Decide(context.Background(), UserContext{UserID: "user-a", AccountStatus: "disabled"}, Scope{ProductID: "product-a", ApplicationID: "app-a"}, OperationPolicy{})
	if err != nil || decision.ReasonCode != "IDENTITY_ACCOUNT_DISABLED" || stub.seen.user.UserID != "" {
		t.Fatalf("identity decision = %#v, err=%v, admission was called=%t", decision, err, stub.seen.user.UserID != "")
	}

	decision, err = service.Decide(context.Background(), UserContext{UserID: "user-a", AccountStatus: "active"}, Scope{ProductID: "product-a", ApplicationID: "app-a"}, OperationPolicy{})
	if err != nil || decision.DecisionStage != "product" || decision.ReasonCode != "PRODUCT_USER_ACCESS_SUSPENDED" {
		t.Fatalf("product decision = %#v, err=%v", decision, err)
	}

	stub.result = productuseraccess.Admission{Allowed: false, Code: "TENANT_USER_ACCESS_SUSPENDED"}
	tenantID := "tenant-a"
	decision, err = service.Decide(context.Background(), UserContext{UserID: "user-a", AccountStatus: "active"}, Scope{ProductID: "product-a", ApplicationID: "app-a", TenantID: &tenantID}, OperationPolicy{})
	if err != nil || decision.DecisionStage != "tenant" || stub.seen.tenant == nil || stub.seen.tenant.TenantID != tenantID {
		t.Fatalf("tenant decision = %#v, seen=%#v, err=%v", decision, stub.seen.tenant, err)
	}
}

func TestDecisionAllowsSelfServiceAndFailsClosedForEntitlement(t *testing.T) {
	stub := &admissionStub{result: productuseraccess.Admission{Allowed: true}}
	service := New(stub)
	user := UserContext{UserID: "user-a", AccountStatus: "active"}
	scope := Scope{ProductID: "product-a", ApplicationID: "app-a"}

	decision, err := service.Decide(context.Background(), user, scope, OperationPolicy{})
	if err != nil || !decision.Allowed || decision.DecisionStage != "allowed" {
		t.Fatalf("self-service decision = %#v, err=%v", decision, err)
	}
	if _, err := service.Decide(context.Background(), user, scope, OperationPolicy{RequiresEntitlement: true}); !errors.Is(err, ErrEntitlementUnavailable) {
		t.Fatalf("entitlement-required error = %v", err)
	}
}

func TestDecisionPropagatesAdmissionFailureWithoutInventingAllow(t *testing.T) {
	want := errors.New("database unavailable")
	service := New(&admissionStub{err: want})
	_, err := service.Decide(context.Background(), UserContext{UserID: "user-a", AccountStatus: "active"}, Scope{ProductID: "product-a", ApplicationID: "app-a"}, OperationPolicy{})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestDecisionRejectsUnknownAdmissionDenial(t *testing.T) {
	service := New(&admissionStub{result: productuseraccess.Admission{Allowed: false, Code: "UNKNOWN_DENIAL"}})
	_, err := service.Decide(context.Background(), UserContext{UserID: "user-a", AccountStatus: "active"}, Scope{ProductID: "product-a", ApplicationID: "app-a"}, OperationPolicy{})
	if !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("unknown denial error = %v", err)
	}
}
