package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/modules/audit"
	"platform.local/capability-platform/backend/internal/modules/identity"
	identityhttp "platform.local/capability-platform/backend/internal/modules/identity/httptransport"
	"platform.local/capability-platform/backend/internal/platform/adminrequest"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

type adminIdentityService interface {
	CurrentAdminSession(context.Context, string) (identity.AdminSession, error)
	CurrentAdminSessionWithCSRF(context.Context, string, string) (identity.AdminSession, error)
}

type adminRequestAuthenticator struct {
	identity adminIdentityService
	origins  map[string]struct{}
}

func newAdminRequestAuthenticator(service adminIdentityService, allowedOrigins []string) adminRequestAuthenticator {
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origins[origin] = struct{}{}
	}
	return adminRequestAuthenticator{identity: service, origins: origins}
}

func (a adminRequestAuthenticator) Authenticate(ctx context.Context, request *http.Request, requireProof bool) (adminrequest.Principal, error) {
	token, transport, ok := identityhttp.AdminAccessProof(request)
	if !ok {
		return adminrequest.Principal{}, adminrequest.ErrUnauthenticated
	}
	var session identity.AdminSession
	var err error
	if transport == identity.TransportCookie && requireProof {
		origin := strings.TrimSpace(request.Header.Get("Origin"))
		if _, allowed := a.origins[origin]; origin == "" || !allowed {
			return adminrequest.Principal{}, adminrequest.ErrRequestProof
		}
		session, err = a.identity.CurrentAdminSessionWithCSRF(ctx, token, request.Header.Get("X-CSRF-Token"))
	} else {
		session, err = a.identity.CurrentAdminSession(ctx, token)
	}
	if err != nil {
		if errors.Is(err, identity.ErrCSRFFailed) {
			return adminrequest.Principal{}, adminrequest.ErrRequestProof
		}
		return adminrequest.Principal{}, adminrequest.ErrUnauthenticated
	}
	return adminrequest.Principal{AdminUserID: session.Admin.AdminUserID, SessionID: session.SessionID, AuthTime: session.Admin.AuthTime}, nil
}

const adminHighRiskReauthenticationWindow = 5 * time.Minute

type adminRequestAuthorizer struct {
	access       *accesscontrol.Service
	now          func() time.Time
	reauthWindow time.Duration
}

func (a adminRequestAuthorizer) Authorize(ctx context.Context, principal adminrequest.Principal, permission string, target adminrequest.TargetScope) (adminrequest.Decision, error) {
	decision, err := a.access.AuthorizeAdmin(ctx, principal.AdminUserID, principal.SessionID, permission, accesscontrol.TargetScope{
		Type: target.Type, ID: target.ID, ProductID: target.ProductID, TenantID: target.TenantID,
	})
	if err != nil {
		return adminrequest.Decision{}, err
	}
	reauthenticationRequired := decision.ReauthenticationRequired
	if decision.Allowed {
		now := time.Now
		if a.now != nil {
			now = a.now
		}
		window := a.reauthWindow
		if window <= 0 {
			window = adminHighRiskReauthenticationWindow
		}
		authTime := principal.AuthTime.UTC()
		age := now().UTC().Sub(authTime)
		if authTime.IsZero() || age < 0 || age > window {
			reauthenticationRequired = true
		}
	}
	return adminrequest.Decision{Allowed: decision.Allowed, ReasonCode: decision.ReasonCode, ReauthenticationRequired: reauthenticationRequired}, nil
}

type adminDenialRecorder struct{ audit *audit.Service }

func (r adminDenialRecorder) RecordAuthorizationDenial(ctx context.Context, denial adminrequest.Denial) error {
	auditID, err := securevalue.ID("audit_")
	if err != nil {
		return err
	}
	_, err = r.audit.AppendAuditEvent(ctx, audit.Event{
		AuditID: auditID, OccurredAt: time.Now().UTC(), ActorID: denial.Principal.AdminUserID,
		Permission: denial.Permission, ScopeType: denial.Target.Type, ScopeID: denial.Target.ID,
		ProductID: denial.Target.ProductID, TenantID: denial.Target.TenantID,
		Action: "admin.auth.authorization_denied", TargetType: denial.Target.Type,
		TargetID: denial.Target.ID, Result: "denied", ReasonCode: denial.ReasonCode,
		TraceID: denial.TraceID, RiskLevel: adminPermissionRisk(denial.Permission),
	})
	return err
}

func adminPermissionRisk(permission string) string {
	for _, definition := range accesscontrol.CurrentPermissionCatalog().Definitions() {
		if definition.Code == permission && definition.Risk == accesscontrol.PermissionRiskHigh {
			return "high"
		}
	}
	return "normal"
}
