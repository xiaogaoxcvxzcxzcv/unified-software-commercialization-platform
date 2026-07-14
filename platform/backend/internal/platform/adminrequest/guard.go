package adminrequest

import (
	"context"
	"errors"
	"net/http"
	"time"

	"platform.local/capability-platform/backend/internal/platform/httpx"
	"platform.local/capability-platform/backend/internal/platform/requestid"
)

var (
	ErrUnauthenticated = errors.New("administrator request is not authenticated")
	ErrRequestProof    = errors.New("administrator request proof is invalid")
)

type Principal struct {
	AdminUserID string
	SessionID   string
	AuthTime    time.Time
}

type TargetScope struct {
	Type      string
	ID        string
	ProductID string
	TenantID  string
}

type Decision struct {
	Allowed                  bool
	ReasonCode               string
	ReauthenticationRequired bool
}

type Authenticator interface {
	Authenticate(context.Context, *http.Request, bool) (Principal, error)
}

type Authorizer interface {
	Authorize(context.Context, Principal, string, TargetScope) (Decision, error)
}

type Denial struct {
	Principal  Principal
	Permission string
	Target     TargetScope
	ReasonCode string
	TraceID    string
}

type DenialRecorder interface {
	RecordAuthorizationDenial(context.Context, Denial) error
}

type Guard struct {
	authenticator Authenticator
	authorizer    Authorizer
	recorder      DenialRecorder
}

func New(authenticator Authenticator, authorizer Authorizer, recorder DenialRecorder) *Guard {
	return &Guard{authenticator: authenticator, authorizer: authorizer, recorder: recorder}
}

func (g *Guard) Authorize(w http.ResponseWriter, r *http.Request, permission string, target TargetScope, highRisk bool) (Principal, bool) {
	writeRequest := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
	principal, err := g.authenticator.Authenticate(r.Context(), r, writeRequest)
	if err != nil {
		status, code, detail := http.StatusUnauthorized, "admin_auth.session_expired", "administrator session is unavailable"
		if errors.Is(err, ErrRequestProof) {
			status, code, detail = http.StatusForbidden, "admin_auth.request_proof_failed", "request verification failed"
		}
		httpx.Error(w, r, status, code, detail)
		return Principal{}, false
	}
	decision, err := g.authorizer.Authorize(r.Context(), principal, permission, target)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "admin_auth.authorization_unavailable", "authorization is temporarily unavailable")
		return Principal{}, false
	}
	reason := decision.ReasonCode
	if decision.Allowed && highRisk && decision.ReauthenticationRequired {
		decision.Allowed = false
		reason = "reauthentication_required"
	}
	if !decision.Allowed {
		if reason == "" {
			reason = "permission_denied"
		}
		if g.recorder != nil {
			_ = g.recorder.RecordAuthorizationDenial(r.Context(), Denial{Principal: principal, Permission: permission, Target: target, ReasonCode: reason, TraceID: requestid.FromContext(r.Context())})
		}
		code := "admin_auth.permission_denied"
		if reason == "reauthentication_required" {
			code = "admin_auth.reauthentication_required"
		}
		httpx.Error(w, r, http.StatusForbidden, code, "administrator is not authorized for this operation")
		return Principal{}, false
	}
	return principal, true
}
