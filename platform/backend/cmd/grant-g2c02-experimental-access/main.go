package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/platform/database"
)

const (
	permissionExperimentalUse = "assembly.experimental.use"
	experimentalRoleCode      = "g2c02_experimental_operator"
	experimentalRoleID        = "role_g2c02_experimental_operator"
	operationName             = "grant G2C-02 experimental access"
)

var errUsage = errors.New("invalid G2C-02 experimental access command")

type databaseHandle interface {
	Close()
	Pool() *pgxpool.Pool
}

type dependencies struct {
	loadConfig func(config.LookupEnv) (config.Config, error)
	openDB     func(context.Context, config.Database) (databaseHandle, error)
	grant      func(context.Context, *pgxpool.Pool, grantOptions) (grantResult, error)
	now        func() time.Time
}

type grantOptions struct {
	AdminUserID string
	Now         time.Time
}

type grantResult struct {
	AdminUserID          string
	RoleCode             string
	PermissionCode       string
	AuthorizationVersion int64
}

func defaultDependencies() dependencies {
	return dependencies{
		loadConfig: config.Load,
		openDB: func(ctx context.Context, cfg config.Database) (databaseHandle, error) {
			return database.Open(ctx, cfg)
		},
		grant: grantExperimentalAccess,
		now:   time.Now,
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.LookupEnv, os.Stdout, defaultDependencies()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, lookup config.LookupEnv, stdout io.Writer, deps dependencies) error {
	adminUserID, err := parseArgs(args)
	if err != nil {
		return err
	}
	rawDatabaseURL, ok := lookup("PLATFORM_DATABASE_URL")
	if !ok || strings.TrimSpace(rawDatabaseURL) == "" {
		return errors.New("PLATFORM_DATABASE_URL is required for G2C-02 experimental access")
	}
	if err := validateLocalPlatformDatabaseURL(rawDatabaseURL); err != nil {
		return err
	}
	cfg, err := deps.loadConfig(lookup)
	if err != nil {
		return fmt.Errorf("load platform configuration: %w", wrapGrantError(err))
	}
	if cfg.Environment != "local" {
		return errors.New("G2C-02 experimental access can only run with PLATFORM_ENVIRONMENT=local")
	}
	if err := validateLocalPlatformDatabaseURL(cfg.Database.URL); err != nil {
		return errors.New("configured database must target an allowed local G2C-02 database")
	}
	db, err := deps.openDB(ctx, cfg.Database)
	if err != nil {
		return errors.New("connect to local platform database")
	}
	defer db.Close()
	result, err := deps.grant(ctx, db.Pool(), grantOptions{AdminUserID: adminUserID, Now: deps.now().UTC()})
	if err != nil {
		return wrapGrantError(err)
	}
	_, err = fmt.Fprintf(stdout, "g2c02 experimental access granted admin_user_id=%s role_code=%s permission=%s authorization_version=%d\n", result.AdminUserID, result.RoleCode, result.PermissionCode, result.AuthorizationVersion)
	if err != nil {
		return errors.New("write G2C-02 experimental access result")
	}
	return nil
}

func parseArgs(args []string) (string, error) {
	flags := flag.NewFlagSet("grant-g2c02-experimental-access", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	acceptance := flags.Bool("acceptance-g2c02", false, "explicitly confirm this local G2C-02 acceptance operation")
	adminUserID := flags.String("admin-user-id", "", "existing administrator user_id")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return "", usageError("expected --acceptance-g2c02 --admin-user-id <user_id>")
	}
	if !*acceptance {
		return "", usageError("--acceptance-g2c02 is required")
	}
	value := strings.TrimSpace(*adminUserID)
	if !isSafeIdentifier(value) {
		return "", usageError("--admin-user-id is invalid")
	}
	return value, nil
}

func usageError(message string) error { return fmt.Errorf("%w: %s", errUsage, message) }

func validateLocalPlatformDatabaseURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return errors.New("PLATFORM_DATABASE_URL must be a PostgreSQL URL")
	}
	host := parsed.Hostname()
	if host == "" {
		return errors.New("PLATFORM_DATABASE_URL must include a host")
	}
	if parsed.Path != "/platform_local" && parsed.Path != "/platform_g2c02_acceptance" {
		return errors.New("G2C-02 experimental access requires database platform_local or platform_g2c02_acceptance")
	}
	if host != "localhost" && net.ParseIP(host) == nil {
		return errors.New("G2C-02 experimental access requires a loopback database host")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return errors.New("resolve database host")
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return errors.New("G2C-02 experimental access requires a loopback database host")
		}
	}
	return nil
}

func grantExperimentalAccess(ctx context.Context, pool *pgxpool.Pool, options grantOptions) (grantResult, error) {
	if pool == nil || !isSafeIdentifier(options.AdminUserID) || options.Now.IsZero() {
		return grantResult{}, errors.New("invalid G2C-02 grant options")
	}
	var permission accesscontrol.PermissionDefinition
	for _, definition := range accesscontrol.CurrentPermissionCatalog().Definitions() {
		if definition.Code == permissionExperimentalUse {
			permission = definition
			break
		}
	}
	if permission.Code == "" || permission.GrantsPlatformSuperAdminOnBootstrap() {
		return grantResult{}, errors.New("experimental permission catalog policy is invalid")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return grantResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var adminExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity.users u JOIN access_control.admin_authorization_versions v ON v.admin_user_id=u.user_id WHERE u.user_id=$1 AND u.account_status='active')`, options.AdminUserID).Scan(&adminExists); err != nil {
		return grantResult{}, err
	}
	if !adminExists {
		return grantResult{}, errors.New("active administrator authorization not found")
	}

	if _, err := tx.Exec(ctx, `INSERT INTO access_control.admin_permissions(permission_code,description,risk_level) VALUES($1,$2,$3) ON CONFLICT(permission_code) DO UPDATE SET description=EXCLUDED.description,risk_level=EXCLUDED.risk_level`, permission.Code, permission.Description, permission.Risk); err != nil {
		return grantResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO access_control.admin_roles(role_id,role_code,display_name,status,created_at,updated_at) VALUES($1,$2,'G2C-02 Experimental Operator','active',$3,$3) ON CONFLICT(role_code) DO UPDATE SET status='active',updated_at=EXCLUDED.updated_at`, experimentalRoleID, experimentalRoleCode, options.Now); err != nil {
		return grantResult{}, err
	}
	var roleID string
	if err := tx.QueryRow(ctx, `SELECT role_id FROM access_control.admin_roles WHERE role_code=$1 AND status='active'`, experimentalRoleCode).Scan(&roleID); err != nil {
		return grantResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO access_control.admin_role_permissions(role_id,permission_code) VALUES($1,$2) ON CONFLICT DO NOTHING`, roleID, permission.Code); err != nil {
		return grantResult{}, err
	}
	bindingID := "scopebind_g2c02_" + stableDigest(options.AdminUserID)[:24]
	if _, err := tx.Exec(ctx, `INSERT INTO access_control.admin_scope_bindings(binding_id,admin_user_id,role_id,scope_type,status,effective_from,created_at,updated_at) VALUES($1,$2,$3,'platform','active',$4,$4,$4) ON CONFLICT DO NOTHING`, bindingID, options.AdminUserID, roleID, options.Now); err != nil {
		return grantResult{}, err
	}
	var authorizationVersion int64
	if err := tx.QueryRow(ctx, `INSERT INTO access_control.admin_authorization_versions(admin_user_id,authorization_version,updated_at) VALUES($1,1,$2) ON CONFLICT(admin_user_id) DO UPDATE SET authorization_version=access_control.admin_authorization_versions.authorization_version+1,updated_at=EXCLUDED.updated_at RETURNING authorization_version`, options.AdminUserID, options.Now).Scan(&authorizationVersion); err != nil {
		return grantResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return grantResult{}, err
	}
	return grantResult{AdminUserID: options.AdminUserID, RoleCode: experimentalRoleCode, PermissionCode: permission.Code, AuthorizationVersion: authorizationVersion}, nil
}

func stableDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func isSafeIdentifier(value string) bool {
	if len(value) < 3 || len(value) > 128 {
		return false
	}
	for index, r := range value {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			continue
		}
		if index > 0 && (r == '_' || r == '-' || r == '.' || r == ':') {
			continue
		}
		return false
	}
	return true
}

func wrapGrantError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"postgres://", "postgresql://", "password", "token", "secret", "authorization", "bearer"} {
		if strings.Contains(message, marker) {
			return errors.New(operationName)
		}
	}
	return fmt.Errorf("%s: %w", operationName, err)
}
