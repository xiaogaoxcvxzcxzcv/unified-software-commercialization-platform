package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	accesspostgres "platform.local/capability-platform/backend/internal/modules/accesscontrol/postgres"
	"platform.local/capability-platform/backend/internal/modules/identity"
	identitypostgres "platform.local/capability-platform/backend/internal/modules/identity/postgres"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/platform/database"
	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

var errUsage = errors.New("invalid controlled administrator client command")

type adminClientService interface {
	RegisterControlledAdminClient(context.Context, identity.RegisterControlledClientCommand) (identity.IssuedControlledClientCredential, error)
	RotateControlledAdminClientCredential(context.Context, identity.RotateControlledClientCredentialCommand) (identity.IssuedControlledClientCredential, error)
	DisableControlledAdminClient(context.Context, string) error
	RevokeControlledAdminClientCredential(context.Context, string, string) error
}

type serviceHandle struct {
	service adminClientService
	close   func()
}

type dependencies struct {
	loadConfig  func(config.LookupEnv) (config.Config, error)
	openService func(context.Context, config.Config) (serviceHandle, error)
}

type command struct {
	action       string
	displayName  string
	clientType   string
	clientID     string
	credentialID string
	expiresAt    *time.Time
}

type credentialOutput struct {
	ClientID     string     `json:"client_id"`
	CredentialID string     `json:"credential_id"`
	ProofType    string     `json:"proof_type"`
	Secret       string     `json:"secret"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

type statusOutput struct {
	Action       string `json:"action"`
	Status       string `json:"status"`
	ClientID     string `json:"client_id"`
	CredentialID string `json:"credential_id,omitempty"`
}

func defaultDependencies() dependencies {
	return dependencies{
		loadConfig: config.Load,
		openService: func(ctx context.Context, cfg config.Config) (serviceHandle, error) {
			db, err := database.Open(ctx, cfg.Database)
			if err != nil {
				return serviceHandle{}, err
			}
			hasher, err := securevalue.NewHasher(cfg.AdminAuth.TokenPepper)
			if err != nil {
				db.Close()
				return serviceHandle{}, err
			}
			access := accesscontrol.NewService(accesspostgres.New(db.Pool()), nil)
			service, err := identity.NewService(
				identitypostgres.New(db.Pool()),
				access,
				identity.Bcrypt{Cost: cfg.AdminAuth.BcryptCost},
				hasher,
				identity.Policy{
					AccessTTL:            cfg.AdminAuth.AccessTTL,
					RefreshTTL:           cfg.AdminAuth.RefreshTTL,
					LoginWindow:          cfg.AdminAuth.LoginWindow,
					LoginMaximumAttempts: cfg.AdminAuth.LoginMaximumAttempts,
					LoginBlockDuration:   cfg.AdminAuth.LoginBlockDuration,
					AllowBearer:          cfg.AdminAuth.BearerEnabled,
				},
				nil,
			)
			if err != nil {
				db.Close()
				return serviceHandle{}, err
			}
			return serviceHandle{service: service, close: db.Close}, nil
		},
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := run(ctx, os.Args[1:], os.LookupEnv, os.Stdout, defaultDependencies())
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	if errors.Is(err, errUsage) {
		os.Exit(2)
	}
	os.Exit(1)
}

func run(ctx context.Context, args []string, lookup config.LookupEnv, stdout io.Writer, deps dependencies) error {
	parsed, err := parseCommand(args)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return errors.New("controlled administrator client operation canceled")
	}
	cfg, err := deps.loadConfig(lookup)
	if err != nil {
		return errors.New("load platform configuration")
	}
	handle, err := deps.openService(ctx, cfg)
	if err != nil {
		return errors.New("initialize controlled administrator client management")
	}
	if handle.close != nil {
		defer handle.close()
	}

	switch parsed.action {
	case "create":
		issued, err := handle.service.RegisterControlledAdminClient(ctx, identity.RegisterControlledClientCommand{
			DisplayName: parsed.displayName,
			ClientType:  parsed.clientType,
			ExpiresAt:   parsed.expiresAt,
		})
		if err != nil {
			return errors.New("create controlled administrator client")
		}
		return writeJSON(stdout, credentialOutput{
			ClientID: issued.ClientID, CredentialID: issued.CredentialID, ProofType: issued.ProofType,
			Secret: issued.Secret, ExpiresAt: issued.ExpiresAt,
		})
	case "rotate":
		issued, err := handle.service.RotateControlledAdminClientCredential(ctx, identity.RotateControlledClientCredentialCommand{
			ClientID: parsed.clientID, ExpiresAt: parsed.expiresAt,
		})
		if err != nil {
			return errors.New("rotate controlled administrator client credential")
		}
		return writeJSON(stdout, credentialOutput{
			ClientID: issued.ClientID, CredentialID: issued.CredentialID, ProofType: issued.ProofType,
			Secret: issued.Secret, ExpiresAt: issued.ExpiresAt,
		})
	case "disable":
		if err := handle.service.DisableControlledAdminClient(ctx, parsed.clientID); err != nil {
			return errors.New("disable controlled administrator client")
		}
		return writeJSON(stdout, statusOutput{Action: "disable", Status: "completed", ClientID: parsed.clientID})
	case "revoke":
		if err := handle.service.RevokeControlledAdminClientCredential(ctx, parsed.clientID, parsed.credentialID); err != nil {
			return errors.New("revoke controlled administrator client credential")
		}
		return writeJSON(stdout, statusOutput{Action: "revoke", Status: "completed", ClientID: parsed.clientID, CredentialID: parsed.credentialID})
	default:
		return errUsage
	}
}

func parseCommand(args []string) (command, error) {
	if len(args) == 0 {
		return command{}, usageError("expected create, rotate, disable, or revoke")
	}
	parsed := command{action: args[0]}
	flags := flag.NewFlagSet("manage-admin-auth-client "+parsed.action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var expiresAt string
	switch parsed.action {
	case "create":
		flags.StringVar(&parsed.displayName, "display-name", "", "controlled client display name")
		flags.StringVar(&parsed.clientType, "client-type", "", "cli or automation")
		flags.StringVar(&expiresAt, "expires-at", "", "RFC3339 expiration time")
	case "rotate":
		flags.StringVar(&parsed.clientID, "client-id", "", "controlled client identifier")
		flags.StringVar(&expiresAt, "expires-at", "", "RFC3339 expiration time")
	case "disable":
		flags.StringVar(&parsed.clientID, "client-id", "", "controlled client identifier")
	case "revoke":
		flags.StringVar(&parsed.clientID, "client-id", "", "controlled client identifier")
		flags.StringVar(&parsed.credentialID, "credential-id", "", "controlled client credential identifier")
	default:
		return command{}, usageError("expected create, rotate, disable, or revoke")
	}
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return command{}, usageError("invalid command options")
	}
	parsed.displayName = strings.TrimSpace(parsed.displayName)
	parsed.clientType = strings.TrimSpace(parsed.clientType)
	parsed.clientID = strings.TrimSpace(parsed.clientID)
	parsed.credentialID = strings.TrimSpace(parsed.credentialID)

	switch parsed.action {
	case "create":
		if parsed.displayName == "" || (parsed.clientType != "cli" && parsed.clientType != "automation") {
			return command{}, usageError("create requires --display-name and --client-type cli|automation")
		}
	case "rotate", "disable":
		if parsed.clientID == "" {
			return command{}, usageError(parsed.action + " requires --client-id")
		}
	case "revoke":
		if parsed.clientID == "" || parsed.credentialID == "" {
			return command{}, usageError("revoke requires --client-id and --credential-id")
		}
	}
	if expiresAt != "" {
		value, err := time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			return command{}, usageError("--expires-at must use RFC3339")
		}
		value = value.UTC()
		parsed.expiresAt = &value
	}
	return parsed, nil
}

func usageError(message string) error {
	return fmt.Errorf("%w: %s", errUsage, message)
}

func writeJSON(writer io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return errors.New("encode command output")
	}
	payload = append(payload, '\n')
	written, err := writer.Write(payload)
	if err != nil || written != len(payload) {
		return errors.New("write command output")
	}
	return nil
}
