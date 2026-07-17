package accountaccess

import (
	"context"
	"errors"

	"platform.local/capability-platform/backend/internal/modules/productuseraccess"
)

var (
	ErrInvalidContext         = errors.New("invalid account access context")
	ErrEntitlementUnavailable = errors.New("entitlement decision is not available")
)

type UserContext struct {
	UserID        string
	AccountStatus string
}

type Scope struct {
	ProductID     string
	ApplicationID string
	TenantID      *string
}

type OperationPolicy struct {
	RequiresEntitlement bool
}

type Decision struct {
	Allowed       bool   `json:"allowed"`
	DecisionStage string `json:"decision_stage"`
	ReasonCode    string `json:"reason_code,omitempty"`
}

type AdmissionEvaluator interface {
	EvaluateScopedAdmission(context.Context, productuseraccess.ProductContext, *productuseraccess.TenantContext, productuseraccess.UserContext) (productuseraccess.Admission, error)
}

type Service struct {
	admission AdmissionEvaluator
}

func New(admission AdmissionEvaluator) *Service {
	return &Service{admission: admission}
}

// Decide evaluates server-owned facts in the fixed Account contract order.
// Entitlement is deliberately fail-closed until package.entitlement supplies
// its public decision port in G2B.
func (s *Service) Decide(ctx context.Context, user UserContext, scope Scope, policy OperationPolicy) (Decision, error) {
	if s == nil || s.admission == nil || user.UserID == "" || scope.ProductID == "" || scope.ApplicationID == "" || (scope.TenantID != nil && *scope.TenantID == "") {
		return Decision{}, ErrInvalidContext
	}
	if user.AccountStatus != "active" {
		return Decision{Allowed: false, DecisionStage: "identity", ReasonCode: "IDENTITY_ACCOUNT_DISABLED"}, nil
	}

	var tenant *productuseraccess.TenantContext
	if scope.TenantID != nil {
		tenant = &productuseraccess.TenantContext{ProductID: scope.ProductID, TenantID: *scope.TenantID}
	}
	admission, err := s.admission.EvaluateScopedAdmission(ctx,
		productuseraccess.ProductContext{ProductID: scope.ProductID}, tenant,
		productuseraccess.UserContext{UserID: user.UserID},
	)
	if err != nil {
		return Decision{}, err
	}
	if !admission.Allowed {
		stage := ""
		switch admission.Code {
		case "PRODUCT_USER_ACCESS_SUSPENDED":
			stage = "product"
		case "TENANT_USER_ACCESS_SUSPENDED":
			stage = "tenant"
		default:
			return Decision{}, ErrInvalidContext
		}
		return Decision{Allowed: false, DecisionStage: stage, ReasonCode: admission.Code}, nil
	}
	if policy.RequiresEntitlement {
		return Decision{}, ErrEntitlementUnavailable
	}
	return Decision{Allowed: true, DecisionStage: "allowed"}, nil
}
