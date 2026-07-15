package accesscontrol

import (
	"errors"
	"reflect"
	"testing"
)

func TestCurrentPermissionCatalogIsVersionedUniqueAndStable(t *testing.T) {
	catalog := CurrentPermissionCatalog()
	if catalog.Version() != PermissionCatalogVersion {
		t.Fatalf("version = %q, want %q", catalog.Version(), PermissionCatalogVersion)
	}

	want := []string{
		"access.manage",
		"assembly.blueprint.manage",
		"assembly.execute",
		"assembly.plan",
		"assembly.read",
		"audit.read",
		"entitlement.manage",
		"identity.manage",
		"platform.read",
		"product.application.manage",
		"product.application.security.manage",
		"product.manage",
		"product.read",
		"tenant.manage",
	}
	definitions := catalog.Definitions()
	got := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		got = append(got, definition.Code)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("permission codes = %v, want %v", got, want)
	}
	if err := catalog.ValidateRequiredPermissions(got); err != nil {
		t.Fatalf("current catalog does not validate: %v", err)
	}
	const wantChecksum = "sha256:d720a506d7c68f5d95fc4d20d9e7f7736139f180d28d4d9e2107b76f66031669"
	if checksum := catalog.Checksum(); checksum != wantChecksum {
		t.Fatalf("checksum = %q, want %q", checksum, wantChecksum)
	}
}

func TestAssemblyPermissionsHaveExplicitRiskAndBootstrapPolicy(t *testing.T) {
	catalog := CurrentPermissionCatalog()
	want := map[string]PermissionRisk{
		"assembly.blueprint.manage": PermissionRiskNormal,
		"assembly.execute":          PermissionRiskHigh,
		"assembly.plan":             PermissionRiskNormal,
		"assembly.read":             PermissionRiskNormal,
	}

	for _, definition := range catalog.Definitions() {
		risk, assemblyPermission := want[definition.Code]
		if !assemblyPermission {
			continue
		}
		if definition.Risk != risk {
			t.Errorf("permission %q risk = %q, want %q", definition.Code, definition.Risk, risk)
		}
		if !definition.GrantsPlatformSuperAdminOnBootstrap() {
			t.Errorf("permission %q is not granted to the bootstrap platform super administrator", definition.Code)
		}
		delete(want, definition.Code)
	}
	if len(want) != 0 {
		t.Fatalf("assembly permissions missing from catalog: %v", want)
	}

	if err := catalog.ValidateRequiredPermissions([]string{
		"assembly.blueprint.manage",
		"assembly.execute",
		"assembly.plan",
		"assembly.read",
	}); err != nil {
		t.Fatalf("assembly permission declarations do not validate: %v", err)
	}
}

func TestPermissionCatalogRejectsDuplicateAndUnsortedDefinitions(t *testing.T) {
	definition := func(code string) PermissionDefinition {
		return PermissionDefinition{Code: code, Description: code, Risk: PermissionRiskNormal}
	}

	if _, err := newPermissionCatalog("test", []PermissionDefinition{definition("product.read"), definition("audit.read")}); !errors.Is(err, ErrInvalidPermissionCatalog) {
		t.Fatalf("expected unsorted catalog error, got %v", err)
	}
	if _, err := newPermissionCatalog("test", []PermissionDefinition{definition("audit.read"), definition("audit.read")}); !errors.Is(err, ErrInvalidPermissionCatalog) {
		t.Fatalf("expected duplicate catalog error, got %v", err)
	}
}

func TestManifestPermissionDeclarationsValidateWithoutChangingGrants(t *testing.T) {
	catalog := CurrentPermissionCatalog()
	before := catalog.Definitions()

	if err := catalog.ValidateRequiredPermissions([]string{"product.read", "product.manage"}); err != nil {
		t.Fatalf("known manifest declaration: %v", err)
	}
	if err := catalog.ValidateRequiredPermissions([]string{"manifest.injected"}); !errors.Is(err, ErrUnknownPermission) {
		t.Fatalf("expected unknown permission error, got %v", err)
	}
	if !reflect.DeepEqual(catalog.Definitions(), before) {
		t.Fatal("validating a manifest declaration changed the catalog")
	}
}
