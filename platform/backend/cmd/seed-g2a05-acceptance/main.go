package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/platform/database"
	"platform.local/capability-platform/backend/internal/testsupport/g2a05acceptance"
)

var errUsage = errors.New("invalid G2A-05 acceptance fixture command")

type databaseHandle interface {
	Close()
	Pool() *pgxpool.Pool
}

type dependencies struct {
	getwd      func() (string, error)
	loadConfig func(config.LookupEnv) (config.Config, error)
	openDB     func(context.Context, config.Database) (databaseHandle, error)
	readFile   func(string) ([]byte, error)
	seed       func(context.Context, *pgxpool.Pool, g2a05acceptance.Options) (g2a05acceptance.Result, error)
}

type output struct {
	ProductID     string `json:"product_id"`
	TenantID      string `json:"tenant_id"`
	ApplicationID string `json:"application_id"`
	UserID        string `json:"user_id"`
	BlueprintID   string `json:"blueprint_id"`
	PlanID        string `json:"plan_id"`
	RunID         string `json:"run_id"`
}

func defaultDependencies() dependencies {
	return dependencies{
		getwd:      os.Getwd,
		loadConfig: config.Load,
		openDB: func(ctx context.Context, cfg config.Database) (databaseHandle, error) {
			return database.Open(ctx, cfg)
		},
		readFile: os.ReadFile,
		seed:     g2a05acceptance.Seed,
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
	passwordPath, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return errors.New("G2A-05 acceptance fixture canceled")
	}

	workingDirectory, err := deps.getwd()
	if err != nil {
		return errors.New("locate repository root")
	}
	repositoryRoot, err := locateRepositoryRoot(workingDirectory)
	if err != nil {
		return err
	}
	passwordPath, err = validatePasswordPath(repositoryRoot, passwordPath)
	if err != nil {
		return err
	}
	password, err := readPassword(deps.readFile, passwordPath)
	if err != nil {
		return err
	}
	defer clear(password)

	rawDatabaseURL, ok := lookup("PLATFORM_DATABASE_URL")
	if !ok || strings.TrimSpace(rawDatabaseURL) == "" {
		return errors.New("PLATFORM_DATABASE_URL is required for the G2A-05 acceptance fixture")
	}
	if err := g2a05acceptance.ValidateDatabaseURL(rawDatabaseURL); err != nil {
		return errors.New("PLATFORM_DATABASE_URL must target the local platform_test_control database")
	}
	cfg, err := deps.loadConfig(lookup)
	if err != nil {
		return errors.New("load platform configuration")
	}
	// Validate the value loaded by the official config path as well. This avoids
	// accidentally opening a different database if an injected lookup changes.
	if err := g2a05acceptance.ValidateDatabaseURL(cfg.Database.URL); err != nil {
		return errors.New("configured database must target the local platform_test_control database")
	}

	db, err := deps.openDB(ctx, cfg.Database)
	if err != nil {
		return errors.New("connect to acceptance database")
	}
	defer db.Close()
	seeded, err := deps.seed(ctx, db.Pool(), g2a05acceptance.Options{RepositoryRoot: repositoryRoot, Password: password})
	if err != nil {
		return wrapSeedError(err)
	}
	return writeJSON(stdout, output{
		ProductID: seeded.ProductID, TenantID: seeded.TenantID, ApplicationID: seeded.ApplicationID,
		UserID: seeded.UserID, BlueprintID: seeded.BlueprintID, PlanID: seeded.PlanID, RunID: seeded.RunID,
	})
}

func parseArgs(args []string) (string, error) {
	flags := flag.NewFlagSet("seed-g2a05-acceptance", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	acceptance := flags.Bool("acceptance-fixture", false, "explicitly confirm the test-only fixture")
	passwordPath := flags.String("password-file", "", "password file under .runtime/G2A-05")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return "", usageError("expected --acceptance-fixture --password-file <path>")
	}
	if !*acceptance {
		return "", usageError("--acceptance-fixture is required")
	}
	if strings.TrimSpace(*passwordPath) == "" {
		return "", usageError("--password-file is required")
	}
	return *passwordPath, nil
}

func usageError(message string) error { return fmt.Errorf("%w: %s", errUsage, message) }

func locateRepositoryRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", errors.New("locate repository root")
	}
	if info, statErr := os.Stat(current); statErr == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		if isRepositoryRoot(current) {
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", errors.New("resolve repository root")
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", errors.New("repository root not found")
}

func isRepositoryRoot(path string) bool {
	if _, err := os.Stat(filepath.Join(path, "platform", "backend", "go.mod")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, "docs", "README.md")); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

func validatePasswordPath(repositoryRoot, raw string) (string, error) {
	allowedRoot := filepath.Join(repositoryRoot, ".runtime", "G2A-05")
	lexicalAllowedRoot := filepath.Clean(allowedRoot)
	allowedRoot, err := filepath.EvalSymlinks(allowedRoot)
	if err != nil {
		return "", errors.New("password file directory .runtime/G2A-05 is unavailable")
	}
	if !samePath(lexicalAllowedRoot, allowedRoot) || !pathWithin(repositoryRoot, allowedRoot) {
		return "", errors.New("password file directory must remain inside repository .runtime/G2A-05")
	}
	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(repositoryRoot, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", errors.New("invalid password file path")
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.New("password file does not exist")
	}
	relative, err := filepath.Rel(allowedRoot, path)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("password file must be inside repository .runtime/G2A-05")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("password file must be a regular file")
	}
	return path, nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func wrapSeedError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"postgres://", "postgresql://", "password", "token", "secret", "authorization", "bearer"} {
		if strings.Contains(message, marker) {
			return errors.New("seed G2A-05 acceptance fixture")
		}
	}
	return fmt.Errorf("seed G2A-05 acceptance fixture: %w", err)
}

func readPassword(readFile func(string) ([]byte, error), path string) ([]byte, error) {
	data, err := readFile(path)
	if err != nil {
		return nil, errors.New("read acceptance password file")
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	for len(data) > 0 && (data[len(data)-1] == '\r' || data[len(data)-1] == '\n') {
		data = data[:len(data)-1]
	}
	if len(data) < 16 || len(data) > 72 {
		clear(data)
		return nil, errors.New("acceptance password must contain 16-72 bytes")
	}
	return data, nil
}

func clear(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func writeJSON(writer io.Writer, value output) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return errors.New("encode acceptance fixture result")
	}
	payload = append(payload, '\n')
	written, err := writer.Write(payload)
	if err != nil || written != len(payload) {
		return errors.New("write acceptance fixture result")
	}
	return nil
}
