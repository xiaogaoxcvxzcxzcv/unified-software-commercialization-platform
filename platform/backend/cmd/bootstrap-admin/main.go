package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	accesspostgres "platform.local/capability-platform/backend/internal/modules/accesscontrol/postgres"
	"platform.local/capability-platform/backend/internal/modules/audit"
	auditpostgres "platform.local/capability-platform/backend/internal/modules/audit/postgres"
	"platform.local/capability-platform/backend/internal/modules/identity"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/platform/database"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

func main() {
	identifier := flag.String("identifier", "", "administrator login identifier")
	displayName := flag.String("display-name", "", "administrator display name")
	passwordStdin := flag.Bool("password-stdin", false, "read the password from standard input")
	flag.Parse()
	if *identifier == "" || *displayName == "" || !*passwordStdin {
		fmt.Fprintln(os.Stderr, "usage: bootstrap-admin --identifier <value> --display-name <value> --password-stdin")
		os.Exit(2)
	}
	passwordBytes, err := readBootstrapPassword(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to read password from standard input")
		os.Exit(2)
	}
	defer func() {
		for i := range passwordBytes {
			passwordBytes[i] = 0
		}
	}()

	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		fail("invalid configuration", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := database.Open(ctx, cfg.Database)
	if err != nil {
		fail("database initialization failed", err)
	}
	defer db.Close()
	hasher, err := securevalue.NewHasher(cfg.AdminAuth.TokenPepper)
	if err != nil {
		fail("administrator authentication initialization failed", err)
	}
	identityRepository := identitypostgres.New(db.Pool())
	accessRepository := accesspostgres.New(db.Pool())
	identityService, err := identity.NewService(identityRepository, accesscontrol.NewService(accessRepository, nil), identity.Bcrypt{Cost: cfg.AdminAuth.BcryptCost}, hasher, identity.Policy{AccessTTL: cfg.AdminAuth.AccessTTL, RefreshTTL: cfg.AdminAuth.RefreshTTL, LoginWindow: cfg.AdminAuth.LoginWindow, LoginMaximumAttempts: cfg.AdminAuth.LoginMaximumAttempts, LoginBlockDuration: cfg.AdminAuth.LoginBlockDuration, AllowBearer: cfg.AdminAuth.BearerEnabled}, nil)
	if err != nil {
		fail("identity service initialization failed", err)
	}
	userID, err := identityService.BootstrapAdminIdentity(ctx, *identifier, *displayName, passwordBytes)
	if err != nil {
		fail("bootstrap identity failed", err)
	}
	bindingID, err := securevalue.ID("bind_")
	if err != nil {
		fail("generate administrator binding identifier", err)
	}
	roleID, err := securevalue.ID("role_")
	if err != nil {
		fail("generate administrator role identifier", err)
	}
	accessService := accesscontrol.NewService(accessRepository, nil)
	if err := accessService.BootstrapPlatformAdmin(ctx, accesscontrol.BootstrapCommand{BindingID: bindingID, RoleID: roleID, AdminUserID: userID, Now: time.Now().UTC()}); err != nil {
		fail("bootstrap access control failed", err)
	}
	auditService := audit.NewService(auditpostgres.New(db.Pool()))
	_, err = auditService.AppendAuditEvent(ctx, audit.Event{ActorID: userID, ScopeType: "platform", Action: "admin.bootstrap.completed", TargetType: "admin_user", TargetID: userID, Result: "success", TraceID: "bootstrap-admin", RiskLevel: "high", RedactedSummary: map[string]any{"method": "password-stdin"}})
	if err != nil {
		fail("bootstrap audit failed", err)
	}
	fmt.Fprintf(os.Stdout, "administrator bootstrapped: %s\n", userID)
}

func readBootstrapPassword(reader io.Reader) ([]byte, error) {
	password, err := io.ReadAll(io.LimitReader(reader, 4100))
	if err != nil {
		return nil, err
	}
	for len(password) > 0 && (password[len(password)-1] == '\r' || password[len(password)-1] == '\n') {
		password = password[:len(password)-1]
	}
	password = bytes.TrimPrefix(password, []byte{0xef, 0xbb, 0xbf})
	if len(password) == 0 || len(password) > 4096 {
		return nil, fmt.Errorf("password must contain between 1 and 4096 bytes")
	}
	return password, nil
}

func fail(message string, err error) {
	slog.Error(message, "error", err)
	os.Exit(1)
}
