package machinecontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestContractedAccountManifestIsClosedButNotPublished(t *testing.T) {
	root := repositoryRoot(t)
	versionRoot := filepath.Join(root, "platform", "contracts", "packages", "package.account", "1.0.0")
	manifestRaw, err := os.ReadFile(filepath.Join(versionRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := LoadDirectory(filepath.Join(root, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate("package-manifest", manifestRaw); err != nil {
		t.Fatalf("contracted Account manifest schema: %v", err)
	}
	var manifest struct {
		LifecycleStatus   string `json:"lifecycle_status"`
		Availability      []any  `json:"availability"`
		ConfigSchemaPath  string `json:"config_schema_path"`
		ManifestSHA256    string `json:"manifest_sha256"`
		ContentTreeSHA256 string `json:"content_tree_sha256"`
		ContentFiles      []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Kind   string `json:"kind"`
		} `json:"content_files"`
	}
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.LifecycleStatus != "contracted" || len(manifest.Availability) != 0 {
		t.Fatalf("contract manifest publication state = %q %#v", manifest.LifecycleStatus, manifest.Availability)
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
	if len(manifest.ContentFiles) != 1 || manifest.ContentFiles[0].Path != manifest.ConfigSchemaPath {
		t.Fatalf("contract content closure = %#v", manifest.ContentFiles)
	}
	configRaw, err := os.ReadFile(filepath.Join(versionRoot, manifest.ConfigSchemaPath))
	if err != nil {
		t.Fatal(err)
	}
	var configSchema any
	if err := json.Unmarshal(configRaw, &configSchema); err != nil {
		t.Fatalf("config schema JSON: %v", err)
	}
	configDigest := sha256.Sum256(configRaw)
	if got := "sha256:" + hex.EncodeToString(configDigest[:]); got != manifest.ContentFiles[0].SHA256 {
		t.Fatalf("config digest = %q, want %q", got, manifest.ContentFiles[0].SHA256)
	}
}

func TestAccountConfigFailsClosedForOptionalProviders(t *testing.T) {
	root := repositoryRoot(t)
	configPath := filepath.Join(root, "platform", "contracts", "packages", "package.account", "1.0.0", "config.schema.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "account-config.schema.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := LoadDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{
		"password_policy": map[string]any{"minimum_length": 12, "revoke_other_sessions_on_change": true},
		"recovery":        map[string]any{"security_notification_provider_ref": "notification.security", "challenge_ttl_seconds": 600},
		"external_providers": map[string]any{
			"wechat": map[string]any{"enabled": false},
			"oidc":   map[string]any{"enabled": false},
		},
	}
	validate := func(value map[string]any) error {
		document, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return registry.Validate("account-config", document)
	}
	if err := validate(base); err != nil {
		t.Fatalf("disabled providers rejected: %v", err)
	}
	enabled := cloneJSONMap(t, base)
	enabled["external_providers"].(map[string]any)["oidc"] = map[string]any{
		"enabled": true, "provider_application_ref": "oidc.primary", "client_secret_ref": "secret.oidc.primary",
		"issuer": "https://identity.example.test", "return_target_codes": []string{"account.callback"},
	}
	if err := validate(enabled); err != nil {
		t.Fatalf("configured optional provider rejected: %v", err)
	}
	missingSecret := cloneJSONMap(t, enabled)
	delete(missingSecret["external_providers"].(map[string]any)["oidc"].(map[string]any), "client_secret_ref")
	if err := validate(missingSecret); err == nil {
		t.Fatal("enabled provider without secret ref accepted")
	}
	credentialOnDisabled := cloneJSONMap(t, base)
	credentialOnDisabled["external_providers"].(map[string]any)["wechat"] = map[string]any{
		"enabled": false, "provider_application_ref": "wechat.primary", "client_secret_ref": "secret.wechat.primary",
	}
	if err := validate(credentialOnDisabled); err == nil {
		t.Fatal("disabled provider with active credential refs accepted")
	}
}

func cloneJSONMap(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var cloned map[string]any
	if err := json.Unmarshal(raw, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func TestRegistryCompilesEverySchemaAndValidatesFixtures(t *testing.T) {
	root := repositoryRoot(t)
	registry, err := LoadDirectory(filepath.Join(root, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{
		"assembly-lifecycle-operation", "assembly-lifecycle-plan", "assembly-manifest", "assembly-plan", "assembly-run", "catalog-snapshot", "common",
		"extension-manifest", "feature-block-catalog", "generated-project-lock", "generator-commit-journal", "generator-diagnostic", "generator-eject-plan",
		"generator-request", "generator-result", "generator-rollback-point", "package-manifest",
		"product-blueprint", "tool-manifest", "ui-template-manifest",
	}
	if names := registry.Names(); !equalStrings(names, wantNames) {
		t.Fatalf("schema names = %v, want %v", names, wantNames)
	}

	fixtureRoot := filepath.Join(root, "platform", "contracts", "schemas", "fixtures")
	validated := 0
	err = filepath.WalkDir(fixtureRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return nil
		}
		validated++
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(fixtureRoot, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		schemaName := strings.SplitN(entry.Name(), ".", 2)[0]
		wantValid := strings.HasSuffix(entry.Name(), ".valid.json")
		if len(parts) >= 3 && parts[0] == "assembly-generator" {
			schemaName = parts[1]
			wantValid = strings.HasPrefix(entry.Name(), "valid")
		}
		validationErr := registry.Validate(schemaName, contents)
		if wantValid && validationErr != nil {
			t.Errorf("valid fixture %s rejected: %v", filepath.ToSlash(path), validationErr)
		}
		if !wantValid && validationErr == nil {
			t.Errorf("invalid fixture %s accepted", filepath.ToSlash(path))
		}
		if wantValid {
			assertStableDigestAfterRemarshal(t, contents)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if validated < 70 {
		t.Fatalf("only %d machine contract fixtures were validated", validated)
	}
}

func TestRegistryRejectsUnknownSchema(t *testing.T) {
	registry, err := LoadDirectory(filepath.Join(repositoryRoot(t), "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate("missing", []byte(`{}`)); !errors.Is(err, ErrUnknownSchema) {
		t.Fatalf("unknown schema error = %v", err)
	}
}

func TestGeneratedProjectLockRequiresExactlyOneProvenanceSource(t *testing.T) {
	root := repositoryRoot(t)
	registry, err := LoadDirectory(filepath.Join(root, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "platform", "contracts", "schemas", "fixtures", "assembly-generator", "generated-project-lock", "valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	delete(document, "run_id")
	missing, _ := json.Marshal(document)
	if err = registry.Validate("generated-project-lock", missing); err == nil {
		t.Fatal("generated project lock without provenance was accepted")
	}
	document["run_id"] = "run.demo"
	document["lifecycle_operation_id"] = "operation.demo"
	ambiguous, _ := json.Marshal(document)
	if err = registry.Validate("generated-project-lock", ambiguous); err == nil {
		t.Fatal("generated project lock with ambiguous provenance was accepted")
	}
}

func TestLifecycleOperationSchemaEnforcesKindAndTerminalStateRelations(t *testing.T) {
	root := repositoryRoot(t)
	registry, err := LoadDirectory(filepath.Join(root, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "platform", "contracts", "schemas", "fixtures", "assembly-generator", "assembly-lifecycle-operation", "valid.completed.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["target"] = nil
	invalidTarget, _ := json.Marshal(document)
	if err = registry.Validate("assembly-lifecycle-operation", invalidTarget); err == nil {
		t.Fatal("completed lifecycle operation without target was accepted")
	}
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["kind"] = "rollback"
	document["rollback_of_operation_id"] = "operation.predecessor"
	invalidPlan, _ := json.Marshal(document)
	if err = registry.Validate("assembly-lifecycle-operation", invalidPlan); err == nil {
		t.Fatal("rollback lifecycle operation with lifecycle plan was accepted")
	}
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["status"] = "planned"
	document["target"] = nil
	invalidCompletion, _ := json.Marshal(document)
	if err = registry.Validate("assembly-lifecycle-operation", invalidCompletion); err == nil {
		t.Fatal("planned lifecycle operation with completed_at was accepted")
	}
}

func assertStableDigestAfterRemarshal(t *testing.T, contents []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(contents, &value); err != nil {
		t.Fatal(err)
	}
	remarshaled, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Digest(contents)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Digest(remarshaled)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical digest changed after equivalent JSON remarshal: %s != %s", first, second)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve machine contract test path")
	}
	directory := filepath.Dir(filename)
	for {
		candidate := filepath.Join(directory, "platform", "contracts", "schemas", "v1")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root not found")
		}
		directory = parent
	}
}

func equalStrings(first, second []string) bool {
	first = append([]string(nil), first...)
	second = append([]string(nil), second...)
	sort.Strings(first)
	sort.Strings(second)
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}
