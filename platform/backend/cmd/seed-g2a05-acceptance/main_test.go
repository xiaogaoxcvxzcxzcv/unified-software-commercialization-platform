package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/platform/config"
	"platform.local/capability-platform/backend/internal/testsupport/g2a05acceptance"
)

type fakeDatabase struct {
	pool   *pgxpool.Pool
	closed bool
}

func (f *fakeDatabase) Close()              { f.closed = true }
func (f *fakeDatabase) Pool() *pgxpool.Pool { return f.pool }

func TestParseArgsRequiresExplicitFixtureAndPasswordFile(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		nil,
		{"--password-file", "x"},
		{"--acceptance-fixture"},
		{"--acceptance-fixture", "--password-file", "x", "extra"},
	} {
		if _, err := parseArgs(args); !errors.Is(err, errUsage) {
			t.Errorf("parseArgs(%v) error = %v, want usage error", args, err)
		}
	}
	path, err := parseArgs([]string{"--acceptance-fixture", "--password-file", ".runtime/G2A-05/password.txt"})
	if err != nil || path != ".runtime/G2A-05/password.txt" {
		t.Fatalf("valid args: path=%q err=%v", path, err)
	}
}

func TestValidatePasswordPathRejectsOutsideAndSymlinkEscape(t *testing.T) {
	t.Parallel()
	root := makeRepository(t)
	allowed := filepath.Join(root, ".runtime", "G2A-05")
	password := filepath.Join(allowed, "password.txt")
	if err := os.WriteFile(password, []byte("password-that-is-long-enough"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := validatePasswordPath(root, password); err != nil || got == "" {
		t.Fatalf("valid path: got=%q err=%v", got, err)
	}
	if got, err := validatePasswordPath(root, filepath.Join(".runtime", "G2A-05", "password.txt")); err != nil || got == "" {
		t.Fatalf("valid relative path: got=%q err=%v", got, err)
	}
	outside := filepath.Join(root, ".runtime", "password.txt")
	if err := os.WriteFile(outside, []byte("password-that-is-long-enough"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validatePasswordPath(root, outside); err == nil {
		t.Fatal("outside password path unexpectedly accepted")
	}
	escape := filepath.Join(allowed, "escape.txt")
	if err := os.Symlink(outside, escape); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := validatePasswordPath(root, escape); err == nil {
		t.Fatal("symlink escape unexpectedly accepted")
	}
	if err := os.RemoveAll(allowed); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), allowed); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	if _, err := validatePasswordPath(root, filepath.Join(".runtime", "G2A-05", "password.txt")); err == nil {
		t.Fatal("symlinked allowed directory unexpectedly accepted")
	}
}

func TestReadPasswordTrimsOnlyTerminalNewlineAndRejectsBounds(t *testing.T) {
	t.Parallel()
	read := func(string) ([]byte, error) {
		return append([]byte{0xef, 0xbb, 0xbf}, []byte("  password-that-is-long-enough  \r\n")...), nil
	}
	got, err := readPassword(read, "ignored")
	if err != nil || string(got) != "  password-that-is-long-enough  " {
		t.Fatalf("read password: got=%q err=%v", got, err)
	}
	for _, value := range [][]byte{[]byte("short"), bytes.Repeat([]byte("x"), 73)} {
		value := value
		if _, err := readPassword(func(string) ([]byte, error) { return value, nil }, "ignored"); err == nil {
			t.Fatalf("password length %d unexpectedly accepted", len(value))
		}
	}
}

func TestRunSeedsWithOfficialConfigAndOutputsOnlyIDs(t *testing.T) {
	t.Parallel()
	root := makeRepository(t)
	passwordPath := filepath.Join(root, ".runtime", "G2A-05", "password.txt")
	secret := "password-that-is-long-enough"
	if err := os.WriteFile(passwordPath, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db := &fakeDatabase{}
	var seededOptions g2a05acceptance.Options
	var output bytes.Buffer
	lookup := func(key string) (string, bool) {
		if key == "PLATFORM_DATABASE_URL" {
			return "postgres://test" + ":test@127.0.0.1:15432/platform_test_control?sslmode=disable", true
		}
		return "", false
	}
	deps := defaultTestDependencies(root, db)
	deps.seed = func(_ context.Context, pool *pgxpool.Pool, options g2a05acceptance.Options) (g2a05acceptance.Result, error) {
		if pool != db.pool {
			t.Fatal("seed received unexpected pool")
		}
		seededOptions = options
		seededOptions.Password = append([]byte(nil), options.Password...)
		return g2a05acceptance.Result{ProductID: "prod_test", TenantID: "tenant_test", ApplicationID: "app_test", UserID: "user_test", BlueprintID: "bp_test", PlanID: "plan_test", RunID: "run_test"}, nil
	}
	if err := run(context.Background(), []string{"--acceptance-fixture", "--password-file", passwordPath}, lookup, &output, deps); err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(seededOptions.Password) != secret || seededOptions.RepositoryRoot != root {
		t.Fatalf("seed options = %#v", seededOptions)
	}
	if db.closed == false {
		t.Fatal("database was not closed")
	}
	if strings.Contains(output.String(), secret) || strings.Contains(output.String(), "postgres://") || !strings.Contains(output.String(), `"product_id":"prod_test"`) {
		t.Fatalf("unsafe or incomplete output: %q", output.String())
	}
}

func TestRunRejectsNonTestDatabaseBeforeOpening(t *testing.T) {
	t.Parallel()
	root := makeRepository(t)
	passwordPath := filepath.Join(root, ".runtime", "G2A-05", "password.txt")
	if err := os.WriteFile(passwordPath, []byte("password-that-is-long-enough"), 0o600); err != nil {
		t.Fatal(err)
	}
	opened := false
	deps := defaultTestDependencies(root, &fakeDatabase{})
	deps.openDB = func(context.Context, config.Database) (databaseHandle, error) { opened = true; return nil, nil }
	lookup := func(string) (string, bool) {
		return "postgres://test" + ":test@remote.example/platform_test_control", true
	}
	err := run(context.Background(), []string{"--acceptance-fixture", "--password-file", passwordPath}, lookup, &bytes.Buffer{}, deps)
	if err == nil || opened || strings.Contains(err.Error(), "remote.example") {
		t.Fatalf("non-test database: err=%v opened=%v", err, opened)
	}
}

func TestRunSanitizesSeedErrors(t *testing.T) {
	t.Parallel()
	root := makeRepository(t)
	passwordPath := filepath.Join(root, ".runtime", "G2A-05", "password.txt")
	if err := os.WriteFile(passwordPath, []byte("password-that-is-long-enough"), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := defaultTestDependencies(root, &fakeDatabase{})
	deps.seed = func(context.Context, *pgxpool.Pool, g2a05acceptance.Options) (g2a05acceptance.Result, error) {
		return g2a05acceptance.Result{}, errors.New("secret-token-and-database-url")
	}
	lookup := func(key string) (string, bool) {
		if key == "PLATFORM_DATABASE_URL" {
			return "postgres://test" + ":test@127.0.0.1:15432/platform_test_control", true
		}
		return "", false
	}
	err := run(context.Background(), []string{"--acceptance-fixture", "--password-file", passwordPath}, lookup, &bytes.Buffer{}, deps)
	if err == nil || strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "database-url") {
		t.Fatalf("seed error leaked: %v", err)
	}
}

func TestWrapSeedErrorPreservesSafeCauseOnly(t *testing.T) {
	t.Parallel()
	safe := errors.New("create acceptance user: duplicate fixture")
	wrapped := wrapSeedError(safe)
	if !errors.Is(wrapped, safe) || !strings.Contains(wrapped.Error(), "duplicate fixture") {
		t.Fatalf("safe cause was not preserved: %v", wrapped)
	}
	for _, unsafe := range []string{"password rejected", "postgres://user" + ":password@host/db", "bearer token rejected"} {
		err := wrapSeedError(errors.New(unsafe))
		if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "postgres://") || strings.Contains(err.Error(), "token") {
			t.Fatalf("unsafe cause leaked: %v", err)
		}
	}
}

func TestLocateRepositoryRoot(t *testing.T) {
	t.Parallel()
	root := makeRepository(t)
	start := filepath.Join(root, "platform", "backend", "cmd", "seed-g2a05-acceptance")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := locateRepositoryRoot(start)
	if err != nil || got != root {
		t.Fatalf("repository root: got=%q err=%v want=%q", got, err, root)
	}
}

func defaultTestDependencies(root string, db *fakeDatabase) dependencies {
	return dependencies{
		getwd: func() (string, error) { return root, nil },
		loadConfig: func(config.LookupEnv) (config.Config, error) {
			return config.Config{Database: config.Database{URL: "postgres://test" + ":test@127.0.0.1:15432/platform_test_control"}}, nil
		},
		openDB:   func(context.Context, config.Database) (databaseHandle, error) { return db, nil },
		readFile: os.ReadFile,
		seed: func(context.Context, *pgxpool.Pool, g2a05acceptance.Options) (g2a05acceptance.Result, error) {
			return g2a05acceptance.Result{}, nil
		},
	}
}

func makeRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, path := range []string{
		filepath.Join(root, ".git"),
		filepath.Join(root, "docs"),
		filepath.Join(root, "platform", "backend"),
		filepath.Join(root, ".runtime", "G2A-05"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "README.md"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "platform", "backend", "go.mod"), []byte("module test"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
