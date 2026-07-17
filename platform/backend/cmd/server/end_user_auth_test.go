package main

import (
	"context"
	"errors"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/identity"
	identityhttp "platform.local/capability-platform/backend/internal/modules/identity/httptransport"
	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
	"platform.local/capability-platform/backend/internal/workflows/accountaccess"
)

type endUserAdmissionStub struct {
	result productuseraccess.Admission
	err    error
	seen   struct {
		product productuseraccess.ProductContext
		tenant  *productuseraccess.TenantContext
		user    productuseraccess.UserContext
	}
}

func (s *endUserAdmissionStub) EvaluateScopedAdmission(_ context.Context, product productuseraccess.ProductContext, tenant *productuseraccess.TenantContext, user productuseraccess.UserContext) (productuseraccess.Admission, error) {
	s.seen.product, s.seen.tenant, s.seen.user = product, tenant, user
	return s.result, s.err
}

func TestEndUserAdmissionAdapterUsesTrustedScopeAndMapsDenials(t *testing.T) {
	tenantID := "tenant-a"
	request := identity.EndUserAdmissionRequest{
		Scope:  identity.EndUserSessionScope{ProductID: "product-a", ApplicationID: "application-a", TenantID: &tenantID},
		UserID: "user-a",
	}
	tests := []struct {
		name string
		code string
		want error
	}{
		{name: "product suspended", code: "PRODUCT_USER_ACCESS_SUSPENDED", want: productuseraccess.ErrProductSuspended},
		{name: "tenant suspended", code: "TENANT_USER_ACCESS_SUSPENDED", want: productuseraccess.ErrTenantSuspended},
		{name: "unknown denial", code: "UNKNOWN_DENIAL", want: accountaccess.ErrInvalidContext},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &endUserAdmissionStub{result: productuseraccess.Admission{Allowed: false, Code: test.code}}
			adapter := endUserAdmissionAdapter{access: accountaccess.New(stub)}
			if err := adapter.AdmitEndUser(context.Background(), request); !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
			if stub.seen.product.ProductID != request.Scope.ProductID || stub.seen.user.UserID != request.UserID || stub.seen.tenant == nil || stub.seen.tenant.TenantID != tenantID {
				t.Fatalf("trusted context not preserved: %+v", stub.seen)
			}
		})
	}
}

func TestEndUserAdmissionAdapterAllowsAndPropagatesInfrastructureFailure(t *testing.T) {
	request := identity.EndUserAdmissionRequest{Scope: identity.EndUserSessionScope{ProductID: "product-a", ApplicationID: "application-a"}, UserID: "user-a"}
	allowed := &endUserAdmissionStub{result: productuseraccess.Admission{Allowed: true}}
	if err := (endUserAdmissionAdapter{access: accountaccess.New(allowed)}).AdmitEndUser(context.Background(), request); err != nil {
		t.Fatalf("allowed admission error=%v", err)
	}
	want := errors.New("database unavailable")
	failing := &endUserAdmissionStub{err: want}
	if err := (endUserAdmissionAdapter{access: accountaccess.New(failing)}).AdmitEndUser(context.Background(), request); !errors.Is(err, want) {
		t.Fatalf("infrastructure error=%v want=%v", err, want)
	}
}

func TestEndUserProfilePatchMappingPreservesThreeStates(t *testing.T) {
	value := "updated"
	tests := []struct {
		name  string
		input identityhttp.OptionalString
		set   bool
		value *string
	}{
		{name: "omitted"},
		{name: "null", input: identityhttp.OptionalString{Set: true}, set: true},
		{name: "value", input: identityhttp.OptionalString{Set: true, Value: &value}, set: true, value: &value},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := mapEndUserProfilePatchValue(test.input)
			if got.Set != test.set || got.Value != test.value {
				t.Fatalf("patch=%+v want set=%t value=%v", got, test.set, test.value)
			}
		})
	}
}

func TestEndUserHTTPErrorMapsProductAccessDenials(t *testing.T) {
	if got := mapEndUserHTTPError(productuseraccess.ErrProductSuspended); !errors.Is(got, identityhttp.ErrProductUserAccessSuspended) {
		t.Fatalf("product denial=%v", got)
	}
	if got := mapEndUserHTTPError(productuseraccess.ErrTenantSuspended); !errors.Is(got, identityhttp.ErrTenantUserAccessSuspended) {
		t.Fatalf("tenant denial=%v", got)
	}
}
