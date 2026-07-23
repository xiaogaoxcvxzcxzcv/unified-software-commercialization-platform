package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/testsupport/g2a06acceptance"
	"strings"
	"syscall"
)

var errUsage = errors.New("invalid G2A-06 browser acceptance command")

type command struct {
	mode, passwordFile, authID, accountID string
	acceptance                            bool
}
type dependencies struct {
	getwd             func() (string, error)
	readFile          func(string) ([]byte, error)
	loadConfig        func(config.LookupEnv) (config.Config, error)
	openPool          func(context.Context, string) (*pgxpool.Pool, error)
	prepare           func(context.Context, *pgxpool.Pool, g2a06acceptance.Options) (g2a06acceptance.Result, error)
	cleanup           func(context.Context, *pgxpool.Pool, g2a06acceptance.Options, g2a06acceptance.CleanupCommand) error
	statuses          func(context.Context, *pgxpool.Pool, g2a06acceptance.Options, g2a06acceptance.CleanupCommand) (g2a06acceptance.InteractionStatuses, error)
	auditOrphans      func(context.Context, *pgxpool.Pool, g2a06acceptance.Options) (g2a06acceptance.OrphanCounts, error)
	cleanupOrphans    func(context.Context, *pgxpool.Pool, g2a06acceptance.Options) (g2a06acceptance.OrphanCleanupResult, error)
	writeState        func(string, string, g2a06acceptance.Result, []byte) error
	recoverLocal      func(string) (bool, error)
	markPreparing     func(string) error
	reserveState      func(string) error
	validatePreparing func(string) error
}

func defaults() dependencies {
	return dependencies{getwd: os.Getwd, readFile: readControlledFile, loadConfig: config.Load, openPool: func(ctx context.Context, dsn string) (*pgxpool.Pool, error) { return pgxpool.New(ctx, dsn) }, prepare: g2a06acceptance.Prepare, cleanup: g2a06acceptance.Cleanup, statuses: g2a06acceptance.Statuses, auditOrphans: g2a06acceptance.AuditOrphans, cleanupOrphans: g2a06acceptance.CleanupOrphans, writeState: writeManifest, recoverLocal: recoverReservationOnly, markPreparing: markAcceptancePreparing, reserveState: reserveAcceptance, validatePreparing: validatePreparingReservation}
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, defaults()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, stdout io.Writer, d dependencies) error {
	c, err := parse(args)
	if err != nil {
		return err
	}
	wd, err := d.getwd()
	if err != nil {
		return errors.New("locate repository root")
	}
	root, err := locateRoot(wd)
	if err != nil {
		return err
	}
	if c.mode == "recover" {
		recovered, recoverErr := d.recoverLocal(root)
		if recoverErr != nil {
			return recoverErr
		}
		if recovered {
			return nil
		}
	}
	cfg, err := loadAcceptanceConfig(root, d)
	if err != nil {
		return err
	}
	pool, err := d.openPool(ctx, cfg.Database.URL)
	if err != nil {
		return errors.New("open controlled test database")
	}
	defer pool.Close()
	o := g2a06acceptance.Options{RepositoryRoot: root, AdminTokenPepper: cfg.AdminAuth.TokenPepper, UserAuth: cfg.UserAuth, HostedInteraction: cfg.HostedInteraction, AcceptanceFixture: c.acceptance}
	defer func() {
		o.UserAuth.TokenPepper = ""
		o.HostedInteraction.StateKey = ""
		o.HostedInteraction.DigestKey = ""
		cfg.AdminAuth.TokenPepper = ""
		cfg.Database.URL = ""
	}()
	if c.mode == "audit-orphans" {
		counts, auditErr := d.auditOrphans(ctx, pool, o)
		if auditErr != nil {
			return fmt.Errorf("audit G2A-06 acceptance orphans: %w", auditErr)
		}
		return writeJSONOutput(stdout, counts)
	}
	if c.mode == "cleanup-orphans" {
		result, cleanupErr := d.cleanupOrphans(ctx, pool, o)
		if cleanupErr != nil {
			return fmt.Errorf("cleanup G2A-06 acceptance orphans: %w", cleanupErr)
		}
		return writeJSONOutput(stdout, result)
	}
	if c.mode == "verify" {
		manifest, payload, readErr := readManifest(root, cfg.AdminAuth.TokenPepper)
		if readErr != nil {
			return errors.New("read encrypted G2A-06 acceptance manifest")
		}
		cleanup := cleanupCommand(manifest, payload)
		persist := func(next manifestPayload) error {
			return persistManifest(root, cfg.AdminAuth.TokenPepper, manifest, next)
		}
		status := func() (g2a06acceptance.InteractionStatuses, error) { return d.statuses(ctx, pool, o, cleanup) }
		return verifyAcceptance(ctx, manifest, payload, persist, status)
	}
	if c.mode == "prepare" {
		passwordPath, err := controlledFile(root, filepath.Join(root, ".runtime", "G2A-05"), c.passwordFile)
		if err != nil {
			return err
		}
		rawPassword, err := d.readFile(passwordPath)
		if err != nil {
			return errors.New("read controlled acceptance password")
		}
		defer clear(rawPassword)
		password := []byte(strings.TrimSpace(string(rawPassword)))
		defer clear(password)
		o.Password = password
		result, err := prepareFixture(ctx, root, pool, o, password, cfg.AdminAuth.TokenPepper, d)
		if err != nil {
			return err
		}
		return writeOutput(stdout, result)
	}
	manifest, payload, err := readManifest(root, cfg.AdminAuth.TokenPepper)
	if err != nil || c.mode == "cleanup" && (manifest.AuthInteractionID != c.authID || manifest.AccountInteractionID != c.accountID) {
		return errors.New("acceptance manifest does not match cleanup request")
	}
	cleanup := cleanupCommand(manifest, payload)
	if err = d.cleanup(ctx, pool, o, cleanup); err != nil {
		return errors.New("cleanup G2A-06 browser acceptance fixture")
	}
	if err = removeAcceptanceState(root); err != nil {
		return errors.New("remove G2A-06 acceptance state")
	}
	return nil
}

func prepareFixture(ctx context.Context, root string, pool *pgxpool.Pool, o g2a06acceptance.Options, password []byte, pepper string, d dependencies) (g2a06acceptance.Result, error) {
	if err := d.reserveState(root); err != nil {
		return g2a06acceptance.Result{}, errors.New("reserve G2A-06 browser acceptance state")
	}
	if err := d.markPreparing(root); err != nil {
		_, _ = recoverReservationOnly(root)
		return g2a06acceptance.Result{}, fmt.Errorf("mark G2A-06 browser acceptance prepare boundary: %w", err)
	}
	if err := d.validatePreparing(root); err != nil {
		return g2a06acceptance.Result{}, fmt.Errorf("validate G2A-06 browser acceptance prepare boundary: %w", err)
	}
	// A process kill after this boundary may leave partial database facts.
	// Recovery deliberately retains the preparing marker for manual audit.
	result, err := d.prepare(ctx, pool, o)
	if err != nil {
		return g2a06acceptance.Result{}, errors.New("prepare G2A-06 browser acceptance fixture")
	}
	if err = d.writeState(root, pepper, result, password); err == nil {
		return result, nil
	}
	cleanup := resultCleanupCommand(result)
	if cleanupErr := d.cleanup(ctx, pool, o, cleanup); cleanupErr == nil {
		m := acceptanceManifest{Version: manifestVersion, AuthInteractionID: result.AuthInteractionID, NegativeAuthInteractionID: result.NegativeAuthInteractionID, AccountInteractionID: result.AccountInteractionID}
		p := resultPayload(result, password)
		p.Stage = stageCompensated
		_ = persistManifest(root, pepper, m, p)
	}
	return g2a06acceptance.Result{}, errors.New("write encrypted G2A-06 acceptance manifest")
}

func resultPayload(result g2a06acceptance.Result, password []byte) manifestPayload {
	return manifestPayload{ClientSessionID: result.ClientSessionID, ClientToken: result.ClientToken, AccountClientSessionID: result.AccountClientSessionID, AccountClientToken: result.AccountClientToken, CodeVerifier: result.CodeVerifier, NegativeCodeVerifier: result.NegativeCodeVerifier, AuthState: result.AuthState, NegativeAuthState: result.NegativeAuthState, AccountState: result.AccountState, ProductID: result.ProductID, ApplicationID: result.ApplicationID, AccountApplicationID: result.AccountApplicationID, TenantID: result.TenantID, UserID: result.UserID, UserSessionID: result.UserSessionID, AccountUserSessionID: result.AccountUserSessionID, Password: string(password), Stage: stagePrepared}
}

func resultCleanupCommand(result g2a06acceptance.Result) g2a06acceptance.CleanupCommand {
	return g2a06acceptance.CleanupCommand{AuthInteractionID: result.AuthInteractionID, NegativeAuthInteractionID: result.NegativeAuthInteractionID, AccountInteractionID: result.AccountInteractionID, ClientSessionID: result.ClientSessionID, AccountClientSessionID: result.AccountClientSessionID, ProductID: result.ProductID, ApplicationID: result.ApplicationID, AccountApplicationID: result.AccountApplicationID, TenantID: result.TenantID, UserID: result.UserID, UserSessionID: result.UserSessionID, AccountUserSessionID: result.AccountUserSessionID}
}

func cleanupCommand(manifest acceptanceManifest, payload manifestPayload) g2a06acceptance.CleanupCommand {
	return g2a06acceptance.CleanupCommand{AuthInteractionID: manifest.AuthInteractionID, NegativeAuthInteractionID: manifest.NegativeAuthInteractionID, AccountInteractionID: manifest.AccountInteractionID, ClientSessionID: payload.ClientSessionID, AccountClientSessionID: payload.AccountClientSessionID, ProductID: payload.ProductID, ApplicationID: payload.ApplicationID, AccountApplicationID: payload.AccountApplicationID, TenantID: payload.TenantID, UserID: payload.UserID, UserSessionID: payload.UserSessionID, AccountUserSessionID: payload.AccountUserSessionID}
}
func writeJSONOutput(w io.Writer, value any) error {
	return json.NewEncoder(w).Encode(value)
}

func writeOutput(w io.Writer, result g2a06acceptance.Result) error {
	return writeJSONOutput(w, result)
}
func parse(args []string) (command, error) {
	if len(args) < 1 {
		return command{}, errUsage
	}
	c := command{mode: args[0]}
	fs := flag.NewFlagSet("g2a06", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&c.acceptance, "acceptance-fixture", false, "")
	fs.StringVar(&c.passwordFile, "password-file", "", "")
	fs.StringVar(&c.authID, "auth-interaction-id", "", "")
	fs.StringVar(&c.accountID, "account-interaction-id", "", "")
	if fs.Parse(args[1:]) != nil || fs.NArg() != 0 || !c.acceptance {
		return command{}, errUsage
	}
	switch c.mode {
	case "prepare":
		if c.passwordFile == "" || c.authID != "" || c.accountID != "" {
			return command{}, errUsage
		}
	case "cleanup":
		if c.passwordFile != "" || c.authID == "" || c.accountID == "" {
			return command{}, errUsage
		}
	case "verify":
		if c.passwordFile != "" || c.authID != "" || c.accountID != "" {
			return command{}, errUsage
		}
	case "audit-orphans", "cleanup-orphans":
		if c.passwordFile != "" || c.authID != "" || c.accountID != "" {
			return command{}, errUsage
		}
	case "recover":
		if c.passwordFile != "" || c.authID != "" || c.accountID != "" {
			return command{}, errUsage
		}
	default:
		return command{}, errUsage
	}
	return c, nil
}

func loadAcceptanceConfig(root string, d dependencies) (config.Config, error) {
	databasePasswordPath, err := controlledFile(root, filepath.Join(root, ".runtime", "postgres"), filepath.Join(root, ".runtime", "postgres", "test-password.txt"))
	if err != nil {
		return config.Config{}, err
	}
	adminPepperPath, err := controlledFile(root, filepath.Join(root, ".runtime"), filepath.Join(root, ".runtime", "admin-token-pepper.txt"))
	if err != nil {
		return config.Config{}, err
	}
	databasePassword, err := d.readFile(databasePasswordPath)
	if err != nil {
		return config.Config{}, errors.New("read controlled PostgreSQL test password")
	}
	defer clear(databasePassword)
	adminPepper, err := d.readFile(adminPepperPath)
	if err != nil {
		return config.Config{}, errors.New("read controlled administrator pepper")
	}
	defer clear(adminPepper)
	databaseURL := &url.URL{Scheme: "postgres", User: url.UserPassword("platform_test", strings.TrimSpace(string(databasePassword))), Host: "127.0.0.1:15432", Path: "/platform_test_control"}
	query := databaseURL.Query()
	query.Set("sslmode", "disable")
	databaseURL.RawQuery = query.Encode()
	values := map[string]string{
		"PLATFORM_ENVIRONMENT":        "local",
		"PLATFORM_DATABASE_URL":       databaseURL.String(),
		"PLATFORM_ADMIN_TOKEN_PEPPER": strings.TrimSpace(string(adminPepper)),
	}
	targetRoot := filepath.Join(root, ".runtime", "G2A-06", "target")
	artifactRoot := filepath.Join(root, ".runtime", "G2A-06", "artifacts")
	if err := ensureControlledDirectory(root, targetRoot); err != nil {
		return config.Config{}, errors.New("create controlled assembly target")
	}
	if err := ensureControlledDirectory(root, artifactRoot); err != nil {
		return config.Config{}, errors.New("create controlled assembly artifact root")
	}
	outputTargets, err := json.Marshal([]config.AssemblyOutputTarget{{Reference: "workspace.g2a06-acceptance", Environment: "test", DisplayName: "G2A-06 acceptance", Summary: "Controlled local browser acceptance target", IsDefault: true, TargetRoot: targetRoot, ArtifactRoot: artifactRoot}})
	if err != nil {
		return config.Config{}, errors.New("build controlled assembly output target")
	}
	values["PLATFORM_ASSEMBLY_OUTPUT_TARGETS"] = string(outputTargets)
	cfg, err := d.loadConfig(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
	for key := range values {
		values[key] = ""
		delete(values, key)
	}
	if err != nil {
		return config.Config{}, errors.New("load formal local runtime configuration")
	}
	if err := g2a06acceptance.ValidateDatabaseURL(cfg.Database.URL); err != nil {
		return config.Config{}, errors.New("formal configuration must target local platform_test_control")
	}
	return cfg, nil
}
func locateRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		_, gitErr := os.Stat(filepath.Join(current, ".git"))
		_, modErr := os.Stat(filepath.Join(current, "platform", "backend", "go.mod"))
		_, docsErr := os.Stat(filepath.Join(current, "docs", "README.md"))
		if gitErr == nil && modErr == nil && docsErr == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("repository root not found")
		}
		current = parent
	}
}
func controlledFile(repositoryRoot, allowedRoot, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errUsage
	}
	root, err := filepath.Abs(allowedRoot)
	if err != nil {
		return "", errUsage
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", errors.New("controlled runtime directory is unavailable")
	}
	candidate := raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(repositoryRoot, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", errUsage
	}
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", errors.New("controlled file is unavailable")
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("controlled file escapes acceptance runtime")
	}
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("controlled file must be regular")
	}
	if err = validateRuntimeFile(candidate); err != nil {
		return "", errors.New("controlled file identity is unsafe")
	}
	if err = secureRuntimePath(candidate, false); err != nil {
		return "", errors.New("controlled file permissions are unsafe")
	}
	if err = validateRuntimeFile(candidate); err != nil {
		return "", errors.New("controlled file identity changed")
	}
	return candidate, nil
}
