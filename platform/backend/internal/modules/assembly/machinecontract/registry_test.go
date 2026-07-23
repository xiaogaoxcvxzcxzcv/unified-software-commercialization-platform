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
		GeneratedOutputs []struct {
			Path         string `json:"path"`
			SourcePath   string `json:"source_path"`
			SourceSHA256 string `json:"source_sha256"`
		} `json:"generated_outputs"`
		SDKMethods   []string `json:"sdk_methods"`
		StableErrors []string `json:"stable_errors"`
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
	if len(manifest.ContentFiles) != 7 || len(manifest.GeneratedOutputs) != 6 {
		t.Fatalf("contract content/output closure = %d/%d", len(manifest.ContentFiles), len(manifest.GeneratedOutputs))
	}
	if len(manifest.SDKMethods) != 22 || manifest.SDKMethods[5] != "restoreSession" || manifest.SDKMethods[7] != "clearSession" {
		t.Fatalf("contract SDK method closure = %#v", manifest.SDKMethods)
	}
	errorSet := make(map[string]bool, len(manifest.StableErrors))
	for _, code := range manifest.StableErrors {
		errorSet[code] = true
	}
	if !errorSet["IDENTITY_SESSION_EXPIRED"] || !errorSet["IDENTITY_REFRESH_REPLAYED"] {
		t.Fatalf("contract stable error closure = %#v", manifest.StableErrors)
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
	for _, output := range manifest.GeneratedOutputs {
		if contentDigests[output.SourcePath] != output.SourceSHA256 {
			t.Fatalf("generated output source is not content-locked: %#v", output)
		}
		if !strings.HasPrefix(output.Path, "src/generated/packages/account/") &&
			output.Path != "docs/generated/account-integration.md" {
			t.Fatalf("generated output escaped Account roots: %q", output.Path)
		}
	}
}

func TestAccountPackageG2A08NineFaceEvidenceIsTraceable(t *testing.T) {
	root := repositoryRoot(t)
	versionRoot := filepath.Join(root, "platform", "contracts", "packages", "package.account", "1.0.0")
	manifestRaw, err := os.ReadFile(filepath.Join(versionRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		LifecycleStatus              string   `json:"lifecycle_status"`
		Availability                 []any    `json:"availability"`
		BackendCapabilities          []string `json:"backend_capabilities"`
		Migrations                   []string `json:"migrations"`
		Events                       []string `json:"events"`
		AuditActions                 []string `json:"audit_actions"`
		AdminBlocks                  []string `json:"admin_blocks"`
		ClientBlocks                 []string `json:"client_blocks"`
		HostedRoutes                 []string `json:"hosted_routes"`
		UITemplateCompatibility      []any    `json:"ui_template_compatibility"`
		PublicAPIOperations          []string `json:"public_api_operations"`
		SDKModules                   []string `json:"sdk_modules"`
		SDKMethods                   []string `json:"sdk_methods"`
		StableErrors                 []string `json:"stable_errors"`
		ConfigSchemaPath             string   `json:"config_schema_path"`
		ProviderRequirements         []string `json:"provider_requirements"`
		OptionalProviderRequirements []string `json:"optional_provider_requirements"`
		GeneratedOutputs             []any    `json:"generated_outputs"`
		SourceLocations              []string `json:"source_locations"`
		ExtensionPoints              []string `json:"extension_points"`
		TestPaths                    []string `json:"test_paths"`
		SmokeTests                   []string `json:"smoke_tests"`
		DocumentationPaths           []string `json:"documentation_paths"`
		UpgradePolicy                any      `json:"upgrade_policy"`
		RollbackPolicy               any      `json:"rollback_policy"`
		DataRetention                []any    `json:"data_retention"`
	}
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.LifecycleStatus != "contracted" || len(manifest.Availability) != 0 {
		t.Fatalf("G2A-08 must start from contracted unpublished source package, got %q %#v", manifest.LifecycleStatus, manifest.Availability)
	}
	mustHave := map[string]int{
		"product result/backend capabilities": len(manifest.BackendCapabilities),
		"migrations":                          len(manifest.Migrations),
		"events":                              len(manifest.Events),
		"audit actions":                       len(manifest.AuditActions),
		"admin blocks":                        len(manifest.AdminBlocks),
		"client blocks":                       len(manifest.ClientBlocks),
		"hosted routes":                       len(manifest.HostedRoutes),
		"template compatibility":              len(manifest.UITemplateCompatibility),
		"public api operations":               len(manifest.PublicAPIOperations),
		"sdk modules":                         len(manifest.SDKModules),
		"sdk methods":                         len(manifest.SDKMethods),
		"stable errors":                       len(manifest.StableErrors),
		"provider requirements":               len(manifest.ProviderRequirements),
		"generated outputs":                   len(manifest.GeneratedOutputs),
		"source locations":                    len(manifest.SourceLocations),
		"extension points":                    len(manifest.ExtensionPoints),
		"test paths":                          len(manifest.TestPaths),
		"smoke tests":                         len(manifest.SmokeTests),
		"documentation paths":                 len(manifest.DocumentationPaths),
		"data retention":                      len(manifest.DataRetention),
	}
	for name, count := range mustHave {
		if count == 0 {
			t.Fatalf("package.account missing G2A-08 nine-face evidence field: %s", name)
		}
	}
	if manifest.ConfigSchemaPath == "" || manifest.UpgradePolicy == nil || manifest.RollbackPolicy == nil {
		t.Fatalf("package.account missing config or lifecycle evidence: config=%q upgrade=%#v rollback=%#v", manifest.ConfigSchemaPath, manifest.UpgradePolicy, manifest.RollbackPolicy)
	}
	assertContainsAll(t, "admin blocks", manifest.AdminBlocks, []string{"identity.user-table", "identity.user-detail"})
	assertContainsAll(t, "client blocks", manifest.ClientBlocks, []string{"auth.login", "auth.register", "auth.recovery", "account.center", "account.profile", "account.security"})
	assertContainsAll(t, "hosted routes", manifest.HostedRoutes, []string{"hosted.auth", "hosted.account"})
	assertContainsAll(t, "smoke tests", manifest.SmokeTests, []string{"st-003", "st-004", "st-022", "st-025-auth-account", "st-038"})
	assertContainsAll(t, "stable errors", manifest.StableErrors, []string{"IDENTITY_ACCOUNT_DISABLED", "PRODUCT_USER_ACCESS_SUSPENDED", "TENANT_USER_ACCESS_SUSPENDED", "ENTITLEMENT_REQUIRED", "ENTITLEMENT_EXPIRED"})

	catalogRaw, err := os.ReadFile(filepath.Join(root, "platform", "contracts", "catalogs", "v1", "feature-blocks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Blocks []struct {
			BlockID   string `json:"block_id"`
			Surface   string `json:"surface"`
			Readiness string `json:"readiness"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(catalogRaw, &catalog); err != nil {
		t.Fatal(err)
	}
	readyBlocks := map[string]string{}
	for _, block := range catalog.Blocks {
		if block.Readiness == "ready" {
			readyBlocks[block.BlockID] = block.Surface
		}
	}
	for _, blockID := range manifest.AdminBlocks {
		if readyBlocks[blockID] != "admin" {
			t.Fatalf("admin block %q is not ready in machine catalog: surface=%q", blockID, readyBlocks[blockID])
		}
	}
	for _, blockID := range manifest.ClientBlocks {
		if readyBlocks[blockID] != "client" {
			t.Fatalf("client block %q is not ready in machine catalog: surface=%q", blockID, readyBlocks[blockID])
		}
	}
	for _, candidate := range append(append([]string{}, manifest.DocumentationPaths...), manifest.SourceLocations...) {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(candidate))); err != nil {
			t.Fatalf("package.account references missing path %q: %v", candidate, err)
		}
	}
	for _, candidate := range manifest.TestPaths {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(candidate))); err != nil {
			t.Fatalf("package.account references missing test path %q: %v", candidate, err)
		}
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
		"hosted": map[string]any{
			"origin":              "https://account.example.test",
			"return_target_codes": map[string]any{"auth": "auth.default", "account": "account.default"},
		},
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
	wechatEnabled := cloneJSONMap(t, base)
	wechatEnabled["external_providers"].(map[string]any)["wechat"] = map[string]any{
		"enabled": true, "provider_application_ref": "wechat.primary", "client_secret_ref": "secret.wechat.primary",
		"return_target_codes": []string{"account.callback"},
	}
	if err := validate(wechatEnabled); err != nil {
		t.Fatalf("enabled WeChat provider without issuer rejected: %v", err)
	}
	disabledOIDCMetadata := cloneJSONMap(t, base)
	disabledOIDCMetadata["external_providers"].(map[string]any)["oidc"] = map[string]any{
		"enabled": false, "issuer": "https://identity.example.test/tenant",
		"return_target_codes": []string{"account.callback"},
	}
	if err := validate(disabledOIDCMetadata); err != nil {
		t.Fatalf("safe inactive OIDC metadata rejected: %v", err)
	}

	for _, issuer := range []string{
		"https://identity.example.test", "https://identity.example.test:65535/tenant/v2.0",
		"http://localhost:5556/oidc", "http://[::1]:5556/issuer",
	} {
		candidate := cloneJSONMap(t, enabled)
		candidate["external_providers"].(map[string]any)["oidc"].(map[string]any)["issuer"] = issuer
		if err := validate(candidate); err != nil {
			t.Fatalf("valid OIDC issuer %q rejected: %v", issuer, err)
		}
	}
	missingIssuer := cloneJSONMap(t, enabled)
	delete(missingIssuer["external_providers"].(map[string]any)["oidc"].(map[string]any), "issuer")
	if err := validate(missingIssuer); err == nil {
		t.Fatal("enabled OIDC provider without issuer accepted")
	}
	for _, issuer := range []string{
		"http://identity.example.test", "https://user@identity.example.test",
		"https://identity.example.test/tenant?query=1", "https://identity.example.test/tenant#fragment",
		"https://identity.example.test:0/tenant", "https://identity.example.test:65536/tenant", "https://identity.example.test:123456/tenant",
	} {
		candidate := cloneJSONMap(t, enabled)
		candidate["external_providers"].(map[string]any)["oidc"].(map[string]any)["issuer"] = issuer
		if err := validate(candidate); err == nil {
			t.Fatalf("unsafe OIDC issuer %q accepted", issuer)
		}
	}

	for _, origin := range []string{
		"https://account.example.test", "https://account.example.test:65535",
		"http://localhost:5174", "http://127.0.0.1:5174", "http://[::1]:5174",
	} {
		candidate := cloneJSONMap(t, base)
		candidate["hosted"].(map[string]any)["origin"] = origin
		if err := validate(candidate); err != nil {
			t.Fatalf("valid Hosted Origin %q rejected: %v", origin, err)
		}
	}
	for _, origin := range []string{
		"http://account.example.test", "https://user@example.test", "https://user:pass@example.test",
		"https://account.example.test/path", "https://account.example.test?query=1",
		"https://account.example.test#fragment", "https://account.example.test:0",
		"https://account.example.test:65536", "https://account.example.test:99999",
		"https://account.example.test:123456", "http://192.168.1.10:5174",
	} {
		candidate := cloneJSONMap(t, base)
		candidate["hosted"].(map[string]any)["origin"] = origin
		if err := validate(candidate); err == nil {
			t.Fatalf("invalid Hosted Origin %q accepted", origin)
		}
	}
	missingSecret := cloneJSONMap(t, enabled)
	delete(missingSecret["external_providers"].(map[string]any)["oidc"].(map[string]any), "client_secret_ref")
	if err := validate(missingSecret); err == nil {
		t.Fatal("enabled provider without secret ref accepted")
	}
	missingApplication := cloneJSONMap(t, enabled)
	delete(missingApplication["external_providers"].(map[string]any)["oidc"].(map[string]any), "provider_application_ref")
	if err := validate(missingApplication); err == nil {
		t.Fatal("enabled provider without application ref accepted")
	}
	credentialOnDisabled := cloneJSONMap(t, base)
	credentialOnDisabled["external_providers"].(map[string]any)["wechat"] = map[string]any{
		"enabled": false, "provider_application_ref": "wechat.primary", "client_secret_ref": "secret.wechat.primary",
	}
	if err := validate(credentialOnDisabled); err == nil {
		t.Fatal("disabled provider with active credential refs accepted")
	}

	missingReturnTarget := cloneJSONMap(t, base)
	delete(missingReturnTarget["hosted"].(map[string]any)["return_target_codes"].(map[string]any), "account")
	if err := validate(missingReturnTarget); err == nil {
		t.Fatal("missing account return target code accepted")
	}
	invalidReturnTarget := cloneJSONMap(t, base)
	invalidReturnTarget["hosted"].(map[string]any)["return_target_codes"].(map[string]any)["auth"] =
		"https://app.example.test/callback"
	if err := validate(invalidReturnTarget); err == nil {
		t.Fatal("final return URI accepted in place of a return target code")
	}

	missingProviderTargets := cloneJSONMap(t, enabled)
	delete(missingProviderTargets["external_providers"].(map[string]any)["oidc"].(map[string]any), "return_target_codes")
	if err := validate(missingProviderTargets); err == nil {
		t.Fatal("enabled provider without return target codes accepted")
	}
}

func assertContainsAll(t *testing.T, name string, got []string, want []string) {
	t.Helper()
	seen := make(map[string]bool, len(got))
	for _, value := range got {
		seen[value] = true
	}
	for _, value := range want {
		if !seen[value] {
			t.Fatalf("%s missing %q in %#v", name, value, got)
		}
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
