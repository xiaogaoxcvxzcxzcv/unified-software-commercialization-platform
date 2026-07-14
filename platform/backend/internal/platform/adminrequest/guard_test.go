package adminrequest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type authenticatorStub struct {
	principal Principal
	err       error
	proof     bool
}

func (s *authenticatorStub) Authenticate(_ context.Context, _ *http.Request, proof bool) (Principal, error) {
	s.proof = proof
	return s.principal, s.err
}

type authorizerStub struct{ decision Decision }

func (s authorizerStub) Authorize(context.Context, Principal, string, TargetScope) (Decision, error) {
	return s.decision, nil
}

type recorderStub struct{ denial *Denial }

func (s *recorderStub) RecordAuthorizationDenial(_ context.Context, denial Denial) error {
	s.denial = &denial
	return nil
}

func TestGuardRequiresRequestProofForWrites(t *testing.T) {
	auth := &authenticatorStub{principal: Principal{AdminUserID: "usr_1", SessionID: "ses_1"}}
	guard := New(auth, authorizerStub{decision: Decision{Allowed: true}}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/products", nil)
	if _, ok := guard.Authorize(recorder, request, "product.manage", TargetScope{Type: "platform"}, false); !ok {
		t.Fatal("Authorize() denied an allowed request")
	}
	if !auth.proof {
		t.Fatal("write request did not require request proof")
	}
}

func TestGuardDeniesHighRiskOperationWhenReauthenticationIsRequired(t *testing.T) {
	auth := &authenticatorStub{principal: Principal{AdminUserID: "usr_1", SessionID: "ses_1"}}
	record := &recorderStub{}
	guard := New(auth, authorizerStub{decision: Decision{Allowed: true, ReauthenticationRequired: true}}, record)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/products/prod_1/applications/app_1/clients", nil)
	if _, ok := guard.Authorize(recorder, request, "product.application.security.manage", TargetScope{Type: "product", ProductID: "prod_1"}, true); ok {
		t.Fatal("Authorize() allowed a high-risk request without reauthentication")
	}
	if recorder.Code != http.StatusForbidden || record.denial == nil || record.denial.ReasonCode != "reauthentication_required" {
		t.Fatalf("denial = %#v, status = %d", record.denial, recorder.Code)
	}
}

func TestGuardMapsInvalidRequestProofToForbidden(t *testing.T) {
	guard := New(&authenticatorStub{err: errors.Join(ErrRequestProof, errors.New("csrf"))}, authorizerStub{}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/products/prod_1/capabilities", nil)
	if _, ok := guard.Authorize(recorder, request, "product.manage", TargetScope{Type: "product", ProductID: "prod_1"}, false); ok {
		t.Fatal("Authorize() allowed an invalid request proof")
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}
