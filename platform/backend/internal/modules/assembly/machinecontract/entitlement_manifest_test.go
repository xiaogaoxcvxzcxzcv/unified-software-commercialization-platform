package machinecontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractedEntitlementManifestDefinesG2B01ModelAndIsUnpublished(t *testing.T) {
	root := repositoryRoot(t)
	versionRoot := filepath.Join(root, "platform", "contracts", "packages", "package.entitlement", "1.0.0")
	manifestRaw, err := os.ReadFile(filepath.Join(versionRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := LoadDirectory(filepath.Join(root, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate("package-manifest", manifestRaw); err != nil {
		t.Fatalf("contracted Entitlement manifest schema: %v", err)
	}
	var manifest struct {
		LifecycleStatus   string `json:"lifecycle_status"`
		Availability      []any  `json:"availability"`
		ConfigSchemaPath  string `json:"config_schema_path"`
		ManifestSHA256    string `json:"manifest_sha256"`
		ContentTreeSHA256 string `json:"content_tree_sha256"`
		Dependencies      []struct {
			PackageID    string `json:"package_id"`
			VersionRange string `json:"version_range"`
		} `json:"dependencies"`
		Migrations          []string `json:"migrations"`
		BackendCapabilities []string `json:"backend_capabilities"`
		AdminBlocks         []string `json:"admin_blocks"`
		ClientBlocks        []string `json:"client_blocks"`
		StableErrors        []string `json:"stable_errors"`
		ContentFiles        []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Kind   string `json:"kind"`
		} `json:"content_files"`
	}
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.LifecycleStatus != "contracted" || len(manifest.Availability) != 0 {
		t.Fatalf("Entitlement contract publication state = %q %#v", manifest.LifecycleStatus, manifest.Availability)
	}
	if len(manifest.Dependencies) != 1 || manifest.Dependencies[0].PackageID != "package.account" || manifest.Dependencies[0].VersionRange != "^1.0.0" {
		t.Fatalf("Entitlement dependency closure = %#v", manifest.Dependencies)
	}
	assertContainsAll(t, "backend capabilities", manifest.BackendCapabilities, []string{"entitlement.check", "entitlement.grant", "entitlement.query", "entitlement.extend", "entitlement.revoke", "entitlement.history"})
	assertContainsAll(t, "admin blocks", manifest.AdminBlocks, []string{"entitlement.table", "entitlement.grant-panel", "entitlement.history"})
	assertContainsAll(t, "client blocks", manifest.ClientBlocks, []string{"entitlement.summary"})
	assertContainsAll(t, "stable errors", manifest.StableErrors, []string{"ENTITLEMENT_REQUIRED", "ENTITLEMENT_EXPIRED", "ENTITLEMENT_OPERATION_CONFLICT", "ENTITLEMENT_POLICY_CONFLICT"})
	if len(manifest.Migrations) != 1 || manifest.Migrations[0] != "platform/backend/migrations/000026_entitlement.up.sql" {
		t.Fatalf("Entitlement migration contract = %#v", manifest.Migrations)
	}
	manifestDigest, err := DigestWithoutTopLevelField(manifestRaw, "manifest_sha256")
	if err != nil || manifestDigest != manifest.ManifestSHA256 {
		t.Fatalf("manifest digest = %q, want %q, err=%v", manifestDigest, manifest.ManifestSHA256, err)
	}
	filesRaw, err := json.Marshal(manifest.ContentFiles)
	if err != nil {
		t.Fatal(err)
	}
	treeDigest, err := Digest(filesRaw)
	if err != nil || "sha256:"+treeDigest != manifest.ContentTreeSHA256 {
		t.Fatalf("content tree digest = %q, want %q, err=%v", treeDigest, manifest.ContentTreeSHA256, err)
	}
	contentDigests := make(map[string]string, len(manifest.ContentFiles))
	for _, file := range manifest.ContentFiles {
		raw, readErr := os.ReadFile(filepath.Join(versionRoot, filepath.FromSlash(file.Path)))
		if readErr != nil {
			t.Fatal(readErr)
		}
		digest := sha256.Sum256(raw)
		got := "sha256:" + hex.EncodeToString(digest[:])
		if got != file.SHA256 {
			t.Fatalf("content digest for %q = %q, want %q", file.Path, got, file.SHA256)
		}
		contentDigests[file.Path] = file.SHA256
	}
	if contentDigests[manifest.ConfigSchemaPath] == "" {
		t.Fatalf("config schema is not content-locked: %q", manifest.ConfigSchemaPath)
	}
	contractRaw, err := os.ReadFile(filepath.Join(root, "docs", "features", "entitlement", "contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	contract := string(contractRaw)
	for _, required := range []string{
		"Feature", "Policy", "Validity", "Grant", "Revision", "Ledger", "Check Decision",
		"unique `(product_id, tenant_id, user_id, source_type, source_id, source_effect_id)`",
		"所有写操作按 `product_id + tenant_id + user_id` 串行化",
		"000026_entitlement",
	} {
		if !strings.Contains(contract, required) {
			t.Fatalf("Entitlement G2B-01 contract missing %q", required)
		}
	}
}