// Package g2a05acceptance contains an intentionally test-only data fixture for
// the G2A-05 browser acceptance run. It is not imported by the server runtime.
package g2a05acceptance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	assemblymachine "platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
	assemblypostgres "platform.local/capability-platform/backend/internal/modules/assembly/postgres"
	"platform.local/capability-platform/backend/internal/modules/identity"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	"platform.local/capability-platform/backend/internal/modules/product"
	productpostgres "platform.local/capability-platform/backend/internal/modules/product/postgres"
	"platform.local/capability-platform/backend/internal/modules/productapplication"
	applicationpostgres "platform.local/capability-platform/backend/internal/modules/productapplication/postgres"
	"platform.local/capability-platform/backend/internal/modules/tenant"
	tenantpostgres "platform.local/capability-platform/backend/internal/modules/tenant/postgres"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
	"platform.local/capability-platform/backend/internal/workflows/productprovisioning"
)

const (
	fixtureProductCode = "g2a05-account-acceptance"
	fixtureProductName = "[ACCEPTANCE FIXTURE] G2A-05 Account"
	fixtureBlueprintID = "bp_g2a05-account-acceptance"
	fixturePlanID      = "plan_g2a05-account-acceptance"
	fixtureApplication = "g2a05-account-acceptance.web"
	fixtureUserEmail   = "g2a05-account-acceptance@example.test"
	fixtureActor       = "acceptance.g2a05.fixture"
	fixtureTrace       = "trace.g2a05.acceptance.fixture"
	catalogRevision    = "g2a05-acceptance-catalog-v1"
	fixtureOutput      = "workspace.g2a05-acceptance"
)

var errNotTestDatabase = errors.New("g2a05 acceptance fixture requires the local platform_test_control database")

// Options deliberately has no caller-controlled product or user identifiers.
// The fixture must remain deterministic and unmistakably test-only.
type Options struct {
	RepositoryRoot string
	Password       []byte
	TokenPepper    []byte
	UserPepper     []byte
}

type Result struct {
	ProductID     string
	TenantID      string
	ApplicationID string
	UserID        string
	UserSessionID string
	BlueprintID   string
	PlanID        string
	RunID         string
}

// ValidateDatabaseURL rejects every database except the local test database.
func ValidateDatabaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Hostname() == "" {
		return errNotTestDatabase
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return errNotTestDatabase
	}
	if strings.TrimPrefix(parsed.Path, "/") != "platform_test_control" {
		return errNotTestDatabase
	}
	return nil
}

func validatePool(pool *pgxpool.Pool) error {
	if pool == nil || pool.Config() == nil || pool.Config().ConnConfig == nil {
		return errNotTestDatabase
	}
	config := pool.Config().ConnConfig
	host := strings.ToLower(config.Host)
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return errNotTestDatabase
	}
	if config.Database != "platform_test_control" {
		return errNotTestDatabase
	}
	return nil
}

// Seed creates one deterministic, idempotent acceptance dataset. It never
// calls a Repository directly; all writes go through public application ports.
func Seed(ctx context.Context, pool *pgxpool.Pool, options Options) (Result, error) {
	if err := validatePool(pool); err != nil {
		return Result{}, err
	}
	if err := validateOptions(options); err != nil {
		return Result{}, err
	}
	root, err := filepath.Abs(options.RepositoryRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve repository root: %w", err)
	}
	contracts, err := assemblymachine.LoadDirectory(filepath.Join(root, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		return Result{}, fmt.Errorf("load assembly contracts: %w", err)
	}
	tokenPepper := "g2a05-acceptance-token-pepper-v1-g2a05-acceptance-token-pepper-v1"
	if len(options.TokenPepper) > 0 {
		tokenPepper = string(options.TokenPepper)
	}
	userPepper := "g2a05-acceptance-user-pepper-v1-g2a05-acceptance-user-pepper-v1"
	if len(options.UserPepper) > 0 {
		userPepper = string(options.UserPepper)
	}
	hasher, err := securevalue.NewHasher(tokenPepper)
	if err != nil {
		return Result{}, err
	}
	userHasher, err := securevalue.NewHasher(userPepper)
	if err != nil {
		return Result{}, err
	}
	assemblyRepository := assemblypostgres.NewWithCursorKey(pool, []byte("g2a05-acceptance-cursor-key"))
	assemblyService := core.NewService(assemblyRepository, core.NewRegistryValidator(contracts), acceptancePlanner{}, securevalue.ID, nil, core.WithOutputTargetVerifier(acceptanceOutputTarget{}))
	productService := product.NewService(productpostgres.New(pool), assemblyService, product.NewVersionedProofVerifier(hasher), securevalue.ID, nil, nil)
	tenantService := tenant.NewService(tenantpostgres.New(pool))
	provisioning := productprovisioning.New(productService, tenantService)
	createdProduct, err := provisioning.CreateProduct(ctx, productprovisioning.CreateCommand{
		ProductCode: fixtureProductCode, Name: fixtureProductName, Status: "active", Environments: []string{"test"}, ActorID: fixtureActor,
		IdempotencyKey: "g2a05-acceptance-product-v1", TraceID: fixtureTrace,
	})
	if err != nil {
		return Result{}, fmt.Errorf("create acceptance product: %w", err)
	}
	applicationService := productapplication.NewService(applicationpostgres.New(pool), nil, nil)
	createdApplication, err := applicationService.CreateApplication(ctx, productapplication.CreateCommand{
		Product:         productapplication.ProductContext{ProductID: createdProduct.ProductID, Environment: productapplication.EnvironmentTest},
		ApplicationCode: fixtureApplication, Name: "[ACCEPTANCE FIXTURE] G2A-05 Web", Platform: productapplication.PlatformWeb,
		DistributionChannel: "official", ReleaseTrack: productapplication.ReleaseTrackStable, Status: productapplication.StatusActive,
		ActorID: fixtureActor, TraceID: fixtureTrace, IdempotencyKey: "g2a05-acceptance-application-v1",
	})
	if err != nil {
		return Result{}, fmt.Errorf("create acceptance application: %w", err)
	}
	blueprint, err := assemblyService.CreateBlueprint(ctx, core.CreateBlueprintCommand{ActorID: fixtureActor, IdempotencyKey: "g2a05-acceptance-blueprint-v1", TraceID: fixtureTrace, Document: acceptanceBlueprint()})
	if err != nil {
		return Result{}, fmt.Errorf("create acceptance blueprint: %w", err)
	}
	plan, err := assemblyService.CreatePlan(ctx, core.CreatePlanCommand{BlueprintID: blueprint.BlueprintID, BlueprintVersion: blueprint.Revision, Environment: "test", ActorID: fixtureActor, IdempotencyKey: "g2a05-acceptance-plan-v1", TraceID: fixtureTrace})
	if err != nil {
		return Result{}, fmt.Errorf("create acceptance plan: %w", err)
	}
	plan, err = assemblyService.ConfirmPlan(ctx, core.ConfirmPlanCommand{PlanID: plan.PlanID, ConfirmationChecksum: plan.ConfirmationChecksum, ExpectedVersion: plan.Version, ActorID: fixtureActor, IdempotencyKey: "g2a05-acceptance-confirm-v1", TraceID: fixtureTrace})
	if err != nil {
		return Result{}, fmt.Errorf("confirm acceptance plan: %w", err)
	}
	// ConfirmPlan returns the repository projection; re-read through the
	// application service so the derived confirmation checksum is populated.
	plan, err = assemblyService.GetPlan(ctx, plan.PlanID)
	if err != nil {
		return Result{}, fmt.Errorf("reload confirmed acceptance plan: %w", err)
	}
	run, err := assemblyService.StartAssembly(ctx, core.StartAssemblyCommand{PlanID: plan.PlanID, PlanChecksum: plan.PlanSHA256, ConfirmationChecksum: plan.ConfirmationChecksum, ExpectedPlanVersion: plan.Version, OutputTargetRef: fixtureOutput, ActorID: fixtureActor, IdempotencyKey: "g2a05-acceptance-run-v1", TraceID: fixtureTrace})
	if err != nil {
		return Result{}, fmt.Errorf("start acceptance run: %w", err)
	}
	run, err = assemblyService.BindProduct(ctx, core.BindProductCommand{ProductID: createdProduct.ProductID, RunID: run.RunID, ExpectedVersion: run.Version, ActorID: fixtureActor, IdempotencyKey: "g2a05-acceptance-bind-v1", TraceID: fixtureTrace})
	if err != nil {
		return Result{}, fmt.Errorf("bind acceptance product: %w", err)
	}
	if _, err := productService.ReplaceCapabilitySet(ctx, product.ReplaceCapabilitySetCommand{Plan: product.TrustedCapabilityChangePlan{ProductID: createdProduct.ProductID, SourcePlanID: plan.PlanID, CatalogRevision: plan.CatalogRevision, CatalogSnapshotSHA256: plan.CatalogSnapshotSHA256}, ExpectedVersion: 0, ActorID: fixtureActor, IdempotencyKey: "g2a05-acceptance-capabilities-v1", TraceID: fixtureTrace}); err != nil {
		return Result{}, fmt.Errorf("enable acceptance capability: %w", err)
	}
	recovery := &acceptanceRecoveryDelivery{}
	endUsers, err := identity.NewEndUserService(identitypostgres.New(pool), identity.StrictIdentifierNormalizer{}, identity.Bcrypt{Cost: 10}, userHasher, acceptanceRegistrationProof{}, recovery, identity.EndUserPolicy{AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour, RefreshAbsoluteTTL: 48 * time.Hour, RefreshRecoveryWindow: time.Hour, RecoveryTTL: time.Hour, RecoveryMaxAttempts: 3, LoginWindow: time.Minute, LoginMaximumAttempts: 5, LoginBlockDuration: time.Minute, RecentAuthTTL: 10 * time.Minute}, nil)
	if err != nil {
		return Result{}, fmt.Errorf("configure acceptance identity: %w", err)
	}
	tenantID := createdProduct.OfficialTenantID
	password := string(options.Password)
	registered, err := endUsers.Register(ctx, identity.EndUserRegisterCommand{Scope: identity.EndUserSessionScope{ProductID: createdProduct.ProductID, ApplicationID: createdApplication.ApplicationID, TenantID: &tenantID, Environment: "test"}, Identifier: fixtureUserEmail, Credential: password, VerificationContinuationID: "g2a05-acceptance-continuation", VerificationProof: "g2a05-acceptance-proof", DisplayName: "[ACCEPTANCE FIXTURE] G2A-05 User", TraceID: fixtureTrace, IdempotencyKey: "g2a05-acceptance-user-v1"})
	if errors.Is(err, identity.ErrEndUserVersionConflict) {
		registered, err = endUsers.Login(ctx, identity.EndUserLoginCommand{
			Scope:      identity.EndUserSessionScope{ProductID: createdProduct.ProductID, ApplicationID: createdApplication.ApplicationID, TenantID: &tenantID, Environment: "test"},
			Identifier: fixtureUserEmail, Credential: password, Source: "loopback", TraceID: fixtureTrace,
		})
		if errors.Is(err, identity.ErrEndUserInvalidCredentials) {
			scope := identity.EndUserSessionScope{ProductID: createdProduct.ProductID, ApplicationID: createdApplication.ApplicationID, TenantID: &tenantID, Environment: "test"}
			continuation, recoveryErr := endUsers.StartRecovery(ctx, identity.StartEndUserRecoveryCommand{Scope: scope, Identifier: fixtureUserEmail, IdempotencyKey: "g2a05-acceptance-recovery-start-v1", TraceID: fixtureTrace})
			if recoveryErr == nil {
				recoveryErr = endUsers.CompleteRecovery(ctx, identity.CompleteEndUserRecoveryCommand{Scope: scope, Continuation: continuation, Proof: recovery.proof, NewCredential: password, IdempotencyKey: "g2a05-acceptance-recovery-complete-v1", TraceID: fixtureTrace})
			}
			if recoveryErr == nil {
				registered, err = endUsers.Login(ctx, identity.EndUserLoginCommand{Scope: scope, Identifier: fixtureUserEmail, Credential: password, Source: "loopback", TraceID: fixtureTrace})
			} else {
				err = recoveryErr
			}
		}
	}
	if err != nil {
		return Result{}, fmt.Errorf("create acceptance user: %w", err)
	}
	return Result{ProductID: createdProduct.ProductID, TenantID: tenantID, ApplicationID: createdApplication.ApplicationID, UserID: registered.Session.UserID, UserSessionID: registered.Session.SessionID, BlueprintID: blueprint.BlueprintID, PlanID: plan.PlanID, RunID: run.RunID}, nil
}

func validateOptions(options Options) error {
	if strings.TrimSpace(options.RepositoryRoot) == "" || len(options.Password) < 16 || len(options.Password) > 72 {
		return errors.New("g2a05 acceptance fixture requires repository root and a 16-72 byte password")
	}
	return nil
}

type acceptanceOutputTarget struct{}

func (acceptanceOutputTarget) VerifyOutputTarget(_ context.Context, environment, reference string) error {
	if environment != "test" || reference != fixtureOutput {
		return errors.New("acceptance output target rejected")
	}
	return nil
}

type acceptanceRegistrationProof struct{}

func (acceptanceRegistrationProof) VerifyRegistration(_ context.Context, scope identity.EndUserSessionScope, identifier identity.NormalizedIdentifier, continuation, proof string, _, _ []byte) error {
	if scope.Environment != "test" || scope.ProductID == "" || scope.ApplicationID == "" || scope.TenantID == nil || *scope.TenantID == "" || identifier.Value != fixtureUserEmail || continuation != "g2a05-acceptance-continuation" || proof != "g2a05-acceptance-proof" {
		return identity.ErrEndUserInvalidCredentials
	}
	return nil
}

type acceptanceRecoveryDelivery struct{ proof string }

func (d *acceptanceRecoveryDelivery) EnqueueSecurity(_ context.Context, command identity.SecurityDeliveryCommand) error {
	if command.Purpose != "password_recovery" || command.Scope.Environment != "test" || command.Destination.Value != fixtureUserEmail {
		return errors.New("acceptance recovery delivery rejected")
	}
	d.proof = command.Proof
	return nil
}

type acceptancePlanner struct{}

func (acceptancePlanner) BuildPlan(_ context.Context, blueprint core.Blueprint, environment string) (core.PlannedDocument, error) {
	if blueprint.BlueprintID != fixtureBlueprintID || environment != "test" {
		return core.PlannedDocument{}, errors.New("acceptance planner received an unexpected blueprint")
	}
	packageChecksum := digest("g2a05 acceptance package marker")
	templateChecksum := digest("g2a05 acceptance template marker")
	generatorChecksum := digest("g2a05 acceptance generator marker")
	sdkChecksum := digest("g2a05 acceptance sdk marker")
	outputChecksum := digest("g2a05 acceptance output marker")
	document := map[string]any{
		"schema_version": "1.0.0", "plan_id": fixturePlanID, "blueprint_id": blueprint.BlueprintID, "blueprint_version": blueprint.Revision, "environment": "test",
		"catalog_snapshot": map[string]any{"revision": catalogRevision, "scope": "experimental", "checksum": digest("g2a05 acceptance catalog marker")},
		"packages":         []any{map[string]any{"package_id": "package.account", "version": "1.0.0", "checksum": packageChecksum}},
		"applications":     []any{map[string]any{"application_id": fixtureApplication, "target": "web", "channel": "official", "environment": "test", "delivery_mode": "generated_source", "output_path": "acceptance/g2a05-account", "template": map[string]any{"template_id": "standard-a", "version": "1.0.0", "checksum": templateChecksum}}},
		"extensions":       []any{}, "generator": map[string]any{"generator_id": "g2a05-acceptance-generator", "version": "1.0.0", "checksum": generatorChecksum},
		"sdks":         []any{map[string]any{"sdk_id": "g2a05-acceptance-sdk", "version": "1.0.0", "checksum": sdkChecksum}},
		"capabilities": []any{map[string]any{"capability_id": "identity.user-session", "enabled": true, "policy": map[string]any{}, "source_package_id": "package.account", "source_package_version": "1.0.0"}},
		"dependencies": []any{}, "conflicts": []any{}, "risks": []any{map[string]any{"risk_id": "g2a05-acceptance-risk", "level": "low", "category": "generation", "summary": "Acceptance-only metadata; generation is intentionally not executed.", "requires_confirmation": true}},
		"providers": []any{}, "required_providers": []any{}, "required_secret_refs": []any{},
		"expected_outputs": []any{map[string]any{"path": "acceptance/g2a05-account-marker.txt", "ownership": "generated", "source_id": "package.account", "source_version": "1.0.0", "source_path": "acceptance/g2a05-account-marker.txt", "source_sha256": outputChecksum, "render_strategy": "strict_template", "content_type": "text"}},
		"confirmation":     map[string]any{"required": true, "blocking_conflict_count": 0, "risk_count": 1, "statements": []string{"This is a G2A-05 acceptance fixture; no generator is executed."}, "summary_checksum": "sha256:" + strings.Repeat("0", 64)},
		"executable":       true, "plan_checksum": "sha256:" + strings.Repeat("0", 64),
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return core.PlannedDocument{}, err
	}
	confirmation, err := core.ConfirmationSummaryChecksum(0, 1, []string{"This is a G2A-05 acceptance fixture; no generator is executed."})
	if err != nil {
		return core.PlannedDocument{}, err
	}
	document["confirmation"].(map[string]any)["summary_checksum"] = confirmation
	raw, err = json.Marshal(document)
	if err != nil {
		return core.PlannedDocument{}, err
	}
	checksum, err := assemblymachine.DigestWithoutTopLevelField(raw, "plan_checksum")
	if err != nil {
		return core.PlannedDocument{}, err
	}
	document["plan_checksum"] = checksum
	raw, err = json.Marshal(document)
	if err != nil {
		return core.PlannedDocument{}, err
	}
	return core.PlannedDocument{Document: raw, Capabilities: []product.CapabilityItem{{CapabilityID: "identity.user-session", Enabled: true, Policy: json.RawMessage(`{}`), SourcePackageID: "package.account", SourcePackageVersion: "1.0.0"}}}, nil
}

func acceptanceBlueprint() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"schema_version":"1.0.0","blueprint_id":%q,"version":"1.0.0","product":{"code":%q,"name":%q,"brand_name":%q},"packages":[{"package_id":"package.account","version":"1.0.0"}],"applications":[{"application_id":%q,"target":"web","channel":"official","environment":"test","ui":{"template_id":"standard-a","version":"1.0.0","delivery_mode":"generated_source"},"output_path":"acceptance/g2a05-account"}],"provider_refs":[],"extensions":[],"generator":{"id":"g2a05-acceptance-generator","version":"1.0.0"},"sdk":{"id":"g2a05-acceptance-sdk","version":"1.0.0"},"output_root":"acceptance/g2a05-account"}`, fixtureBlueprintID, fixtureProductCode, fixtureProductName, fixtureProductName, fixtureApplication))
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
