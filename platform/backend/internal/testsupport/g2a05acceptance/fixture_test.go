package g2a05acceptance

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"platform.local/capability-platform/backend/internal/modules/product"
	productpostgres "platform.local/capability-platform/backend/internal/modules/product/postgres"
)

func TestValidateDatabaseURLOnlyAcceptsLoopbackControlDatabase(t *testing.T) {
	valid := []string{
		"postgres://platform_test" + ":password@127.0.0.1:15432/platform_test_control?sslmode=disable",
		"postgresql://platform_test" + ":password@localhost/platform_test_control",
	}
	for _, value := range valid {
		if err := ValidateDatabaseURL(value); err != nil {
			t.Fatalf("ValidateDatabaseURL(%q) = %v", value, err)
		}
	}
	invalid := []string{
		"postgres://user" + ":password@db.example/platform_test_control",
		"postgres://user" + ":password@127.0.0.1/platform_production",
		"postgres://user" + ":password@127.0.0.1/platform_test_control/extra",
	}
	for _, value := range invalid {
		if err := ValidateDatabaseURL(value); !errors.Is(err, errNotTestDatabase) {
			t.Errorf("ValidateDatabaseURL(%q) = %v, want test database rejection", value, err)
		}
	}
}

func TestSeedRequiresExplicitTestDatabaseAndPassword(t *testing.T) {
	if err := validateOptions(Options{RepositoryRoot: t.TempDir(), Password: []byte("short")}); err == nil {
		t.Fatal("short fixture password was accepted")
	}
	if err := validatePool(nil); !errors.Is(err, errNotTestDatabase) {
		t.Fatalf("validatePool(nil) = %v", err)
	}
}

// This test is opt-in because it writes the explicitly named local control
// database. It never runs against an isolated or production database.
func TestSeedControlDatabase(t *testing.T) {
	if os.Getenv("G2A05_ACCEPTANCE_SEED") != "1" {
		t.Skip("set G2A05_ACCEPTANCE_SEED=1 to seed platform_test_control")
	}
	raw := os.Getenv("TEST_DATABASE_URL")
	if err := ValidateDatabaseURL(raw); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := Seed(context.Background(), pool, Options{RepositoryRoot: repositoryRoot(t), Password: []byte("g2a05-acceptance-user-password")})
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"product": result.ProductID, "tenant": result.TenantID, "application": result.ApplicationID,
		"user": result.UserID, "blueprint": result.BlueprintID, "plan": result.PlanID, "run": result.RunID,
	} {
		if value == "" {
			t.Errorf("result.%s is empty", name)
		}
	}
	capabilityService := product.NewService(productpostgres.New(pool), nil, nil, nil, nil, nil)
	capabilitySet, err := capabilityService.CurrentCapabilitySet(context.Background(), result.ProductID)
	if err != nil {
		t.Fatalf("read persisted capability set through Product service: %v", err)
	}
	foundAccount := false
	for _, item := range capabilitySet.Items {
		if item.SourcePackageID == "package.account" && item.Enabled {
			foundAccount = true
		}
	}
	if !foundAccount {
		t.Fatalf("persisted capability set does not contain enabled package.account: %+v", capabilitySet.Items)
	}
	t.Logf("acceptance fixture IDs: product=%s tenant=%s application=%s user=%s blueprint=%s plan=%s run=%s", result.ProductID, result.TenantID, result.ApplicationID, result.UserID, result.BlueprintID, result.PlanID, result.RunID)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("G2A05_REPOSITORY_ROOT")
	if root == "" {
		t.Fatal("G2A05_REPOSITORY_ROOT is required for the opt-in PostgreSQL fixture test")
	}
	return root
}
