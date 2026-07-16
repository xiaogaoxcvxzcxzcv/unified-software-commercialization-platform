package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	accesspostgres "platform.local/capability-platform/backend/internal/modules/accesscontrol/postgres"
	assemblycore "platform.local/capability-platform/backend/internal/modules/assembly/core"
	assemblygeneration "platform.local/capability-platform/backend/internal/modules/assembly/generation"
	assemblyhttp "platform.local/capability-platform/backend/internal/modules/assembly/httptransport"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecatalog"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
	"platform.local/capability-platform/backend/internal/modules/assembly/planning"
	assemblypostgres "platform.local/capability-platform/backend/internal/modules/assembly/postgres"
	"platform.local/capability-platform/backend/internal/modules/audit"
	audithttp "platform.local/capability-platform/backend/internal/modules/audit/httptransport"
	auditpostgres "platform.local/capability-platform/backend/internal/modules/audit/postgres"
	"platform.local/capability-platform/backend/internal/modules/identity"
	identityhttp "platform.local/capability-platform/backend/internal/modules/identity/httptransport"
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
	"platform.local/capability-platform/backend/internal/platform/database"
	"platform.local/capability-platform/backend/internal/platform/logging"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	platformserver "platform.local/capability-platform/backend/internal/platform/server"
	"platform.local/capability-platform/backend/internal/workflows/assemblyexecution"
	"platform.local/capability-platform/backend/internal/workflows/clientcontext"
	clientcontexthttp "platform.local/capability-platform/backend/internal/workflows/clientcontext/httptransport"
	"platform.local/capability-platform/backend/internal/workflows/clientregistration"
	clientregistrationhttp "platform.local/capability-platform/backend/internal/workflows/clientregistration/httptransport"
	"platform.local/capability-platform/backend/internal/workflows/productprovisioning"
	"platform.local/capability-platform/backend/internal/workflows/tenantadmin"
	tenantadminhttp "platform.local/capability-platform/backend/internal/workflows/tenantadmin/httptransport"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

type identityAuditAdapter struct {
	service *audit.Service
}

type adminSessionResolver interface {
	CurrentAdminSession(context.Context, string) (identity.AdminSession, error)
}

type auditAdminContextAdapter struct {
	identity adminSessionResolver
}

type tenantProofDigester struct{ hasher securevalue.Hasher }

func (d tenantProofDigester) DigestHex(value string) string {
	return d.hasher.DigestHex("tenant-distribution-proof:" + value)
}

func (a auditAdminContextAdapter) ResolveAdminContext(ctx context.Context, request *http.Request) (audit.AdminContext, error) {
	token, ok := identityhttp.AdminAccessToken(request)
	if !ok {
		return audit.AdminContext{}, audithttp.ErrAdminContextUnavailable
	}
	session, err := a.identity.CurrentAdminSession(ctx, token)
	if err != nil {
		return audit.AdminContext{}, err
	}
	scope, err := auditScopeFromSnapshot(session.Authorization.Scopes)
	if err != nil {
		return audit.AdminContext{}, err
	}
	return audit.AdminContext{AdminUserID: session.Admin.AdminUserID, SessionID: session.SessionID, TargetScope: scope}, nil
}

type auditAuthorizerAdapter struct {
	access interface {
		AuthorizeAdmin(context.Context, string, string, string, accesscontrol.TargetScope) (accesscontrol.Decision, error)
	}
}

func (a auditAuthorizerAdapter) AuthorizeAdmin(ctx context.Context, command audit.AuthorizationCommand) (audit.AuthorizationDecision, error) {
	decision, err := a.access.AuthorizeAdmin(ctx, command.AdminUserID, command.SessionID, command.Permission, accesscontrol.TargetScope{
		Type: command.TargetScope.Type, ID: command.TargetScope.ID,
		ProductID: command.TargetScope.ProductID, TenantID: command.TargetScope.TenantID,
	})
	if err != nil {
		return audit.AuthorizationDecision{}, err
	}
	return audit.AuthorizationDecision{Allowed: decision.Allowed, ReasonCode: decision.ReasonCode}, nil
}

func auditScopeFromSnapshot(scopes []accesscontrol.Scope) (audit.Scope, error) {
	for _, scope := range scopes {
		if scope.Type == "platform" {
			return audit.Scope{Type: "platform"}, nil
		}
	}
	if len(scopes) != 1 {
		return audit.Scope{}, audithttp.ErrAdminContextUnavailable
	}
	scope := scopes[0]
	return audit.Scope{Type: scope.Type, ID: scope.ID, ProductID: scope.ProductID, TenantID: scope.TenantID}, nil
}

func (a identityAuditAdapter) AppendSecurityEvent(ctx context.Context, event identity.SecurityEvent) (string, error) {
	return a.service.AppendAuditEvent(ctx, audit.Event{
		AuditID:         event.AuditID,
		OccurredAt:      event.OccurredAt,
		ActorID:         event.ActorID,
		Permission:      event.Permission,
		ScopeType:       event.ScopeType,
		ScopeID:         event.ScopeID,
		ProductID:       event.ProductID,
		TenantID:        event.TenantID,
		Action:          event.Action,
		TargetType:      event.TargetType,
		TargetID:        event.TargetID,
		Result:          event.Result,
		ReasonCode:      event.ReasonCode,
		TraceID:         event.TraceID,
		RiskLevel:       event.RiskLevel,
		RedactedSummary: event.RedactedSummary,
	})
}

func main() {
	bootstrapLogger := logging.New(os.Stderr, slog.LevelInfo)
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		bootstrapLogger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	logger := logging.New(os.Stdout, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		logger.Error("database initialization failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	hasher, err := securevalue.NewHasher(cfg.AdminAuth.TokenPepper)
	if err != nil {
		logger.Error("administrator authentication initialization failed", "error", err)
		os.Exit(1)
	}
	accessService := accesscontrol.NewService(accesspostgres.New(db.Pool()), nil)
	assemblyContracts, err := machinecontract.LoadDirectory(cfg.Assembly.SchemaDirectory)
	if err != nil {
		logger.Error("assembly machine contract initialization failed", "error", err)
		os.Exit(1)
	}
	featureBlocks, err := machinecatalog.LoadBlockCatalog(cfg.Assembly.FeatureBlockCatalogPath, assemblyContracts)
	if err != nil {
		logger.Error("assembly feature block catalog initialization failed", "error", err)
		os.Exit(1)
	}
	assemblyCatalog, err := machinecatalog.LoadOrdinaryWithTools(
		cfg.Assembly.CapabilityPackageRoot, cfg.Assembly.TemplateRoot,
		cfg.Assembly.GeneratorToolRoot, cfg.Assembly.SDKToolRoot,
		assemblyContracts, accesscontrol.CurrentPermissionCatalog(), featureBlocks,
	)
	if err != nil {
		logger.Error("assembly production catalog initialization failed", "error", err)
		os.Exit(1)
	}
	experimentalAssemblyCatalog, err := machinecatalog.LoadExperimentalWithTools(
		cfg.Assembly.ExperimentalCapabilityPackageRoot, cfg.Assembly.ExperimentalTemplateRoot,
		cfg.Assembly.ExperimentalGeneratorToolRoot, cfg.Assembly.ExperimentalSDKToolRoot,
		assemblyContracts, accesscontrol.CurrentPermissionCatalog(), featureBlocks,
	)
	if err != nil {
		logger.Error("assembly experimental catalog initialization failed", "error", err)
		os.Exit(1)
	}
	configuredWorkspaces := make([]assemblygeneration.Workspace, 0, len(cfg.Assembly.OutputTargets))
	configuredOutputTargets := make([]assemblyhttp.OutputTarget, 0, len(cfg.Assembly.OutputTargets))
	for _, target := range cfg.Assembly.OutputTargets {
		configuredWorkspaces = append(configuredWorkspaces, assemblygeneration.Workspace{
			Reference: target.Reference, TargetRoot: target.TargetRoot, ArtifactRoot: target.ArtifactRoot,
		})
		configuredOutputTargets = append(configuredOutputTargets, assemblyhttp.OutputTarget{
			OutputTargetRef: target.Reference, Environment: target.Environment, DisplayName: target.DisplayName,
			Summary: target.Summary, IsDefault: target.IsDefault,
		})
	}
	assemblyWorkspaces, err := assemblygeneration.NewWorkspaceCatalog(configuredWorkspaces)
	if err != nil {
		logger.Error("assembly output workspace initialization failed", "error", err)
		os.Exit(1)
	}
	assemblyRepository := assemblypostgres.NewWithCursorKey(db.Pool(), []byte(cfg.AdminAuth.TokenPepper))
	assemblyService := assemblycore.NewService(
		assemblyRepository, assemblycore.NewRegistryValidator(assemblyContracts), planning.New(assemblyCatalog), securevalue.ID, nil,
		assemblycore.WithOutputTargetVerifier(newCoreOutputTargetVerifier(configuredOutputTargets...)),
	)
	auditRepository := auditpostgres.New(db.Pool())
	auditService := audit.NewService(auditRepository)
	identityRepository := identitypostgres.New(db.Pool())
	identityService, err := identity.NewService(identityRepository, accessService, identity.Bcrypt{Cost: cfg.AdminAuth.BcryptCost}, hasher, identity.Policy{
		AccessTTL: cfg.AdminAuth.AccessTTL, RefreshTTL: cfg.AdminAuth.RefreshTTL,
		LoginWindow: cfg.AdminAuth.LoginWindow, LoginMaximumAttempts: cfg.AdminAuth.LoginMaximumAttempts,
		LoginBlockDuration: cfg.AdminAuth.LoginBlockDuration, AllowBearer: cfg.AdminAuth.BearerEnabled,
	}, nil)
	if err != nil {
		logger.Error("administrator identity service initialization failed", "error", err)
		os.Exit(1)
	}
	proofVerifier := product.NewVersionedProofVerifier(hasher)
	productService := product.NewService(productpostgres.New(db.Pool()), assemblyService, proofVerifier, securevalue.ID, func() (string, string, error) {
		token, err := securevalue.Token("client_session_")
		if err != nil {
			return "", "", err
		}
		return token, "sha256:" + hasher.DigestHex("product-client-session:"+token), nil
	}, nil)
	applicationService := productapplication.NewService(applicationpostgres.New(db.Pool()), nil, nil)
	tenantService := tenant.NewService(tenantpostgres.New(db.Pool()), tenant.WithProofDigester(tenantProofDigester{hasher: hasher}))
	adminGuard := adminrequest.New(
		newAdminRequestAuthenticator(identityService, cfg.AdminAuth.AllowedOrigins),
		adminRequestAuthorizer{access: accessService},
		adminDenialRecorder{audit: auditService},
	)
	provisioningWorkflow := productprovisioning.New(productService, tenantService)
	assemblyExecutionWorkflow := assemblyexecution.New(
		assemblyService, provisioningWorkflow, applicationService, productService, assemblyWorkspaces,
		assemblygeneration.NewPureRenderer(assemblyCatalog), assemblyContracts, nil,
	)
	assemblyWorkerID, err := securevalue.ID("assembly_worker_")
	if err != nil {
		logger.Error("assembly worker initialization failed", "error", err)
		os.Exit(1)
	}
	assemblyWorker := assemblyexecution.NewWorker(assemblyRepository, assemblyExecutionWorkflow, assemblyService, assemblyWorkerID, nil)
	clientRegistrationWorkflow := clientregistration.New(productService, applicationService, hasher, nil)
	clientContextWorkflow := clientcontext.New(productService, applicationService, tenantService, 15*time.Minute, 5*time.Minute, nil)
	tenantAdminWorkflow := tenantadmin.New(tenantService, accessService)
	productHandler := producthttp.New(productService, productProvisionerAdapter{workflow: provisioningWorkflow}, adminGuard)
	applicationHandler := applicationhttp.New(applicationService, adminGuard, applicationhttp.Config{Environment: productapplication.Environment(cfg.Environment)})
	tenantHandler := tenanthttp.New(tenantService, adminGuard)
	clientRegistrationHandler := clientregistrationhttp.New(clientRegistrationWorkflow, adminGuard, nil)
	tenantAdminHandler := tenantadminhttp.New(tenantAdminWorkflow, adminGuard)
	assemblyHandler := assemblyhttp.New(newAssemblyAdminAdapterWithCatalogs(assemblyService, assemblyCatalog, experimentalAssemblyCatalog, configuredOutputTargets...), adminGuard)
	clientContextHandler := clientcontexthttp.New(clientContextWorkflow)
	adminAuthHandler := identityhttp.New(identityService, identityhttp.Config{AllowedOrigins: cfg.AdminAuth.AllowedOrigins})
	modules := platformserver.NewModuleRegistrar()
	if err := modules.Register("/api/v1/admin/auth/", adminAuthHandler); err != nil {
		logger.Error("administrator authentication route registration failed", "error", err)
		os.Exit(1)
	}
	auditQueryService := audit.NewQueryService(auditRepository, auditAuthorizerAdapter{access: accessService})
	auditHandler := audithttp.New(auditQueryService, auditAdminContextAdapter{identity: identityService})
	if err := modules.Register("/api/v1/admin/audit/", auditHandler); err != nil {
		logger.Error("audit route registration failed", "error", err)
		os.Exit(1)
	}
	if err := modules.Register("/api/v1/admin/", productAdminRouter{assembly: assemblyHandler, product: productHandler, application: applicationHandler, tenant: tenantHandler, clientRegistration: clientRegistrationHandler, tenantAdmin: tenantAdminHandler}); err != nil {
		logger.Error("product administration route registration failed", "error", err)
		os.Exit(1)
	}
	if err := modules.Register("/api/v1/client/", clientContextHandler); err != nil {
		logger.Error("client context route registration failed", "error", err)
		os.Exit(1)
	}
	outbox := identity.NewOutboxDispatcher(identityRepository, identityAuditAdapter{service: auditService}, logger)
	go outbox.Run(ctx)
	go assemblyWorker.Run(ctx)
	for name, source := range map[string]auditableOutboxSource{
		"access_control":      accessControlOutboxSource{service: accessService},
		"assembly":            assemblyOutboxSource{service: assemblyService},
		"product":             productOutboxSource{service: productService},
		"product_application": productApplicationOutboxSource{service: applicationService},
		"tenant":              tenantOutboxSource{service: tenantService},
	} {
		go (auditOutboxDispatcher{name: name, source: source, audit: auditService, logger: logger}).Run(ctx)
	}

	app := platformserver.New(cfg, logger, db, modules, platformserver.BuildInfo{
		Version: version, Commit: commit, BuildTime: buildTime,
	})
	if err := app.Run(ctx); err != nil {
		logger.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}
