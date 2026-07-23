package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

const entitlementPackageRelative = "platform/contracts/packages/package.entitlement/1.0.0"

func TestEntitlementPackageManifestGeneratesDeterministicallyAndPreservesProductFiles(t *testing.T) {
	repositoryRoot := findEntitlementPackageRepositoryRoot(t)
	packageRoot, err := validateWorkspaceRoot(filepath.Join(repositoryRoot, filepath.FromSlash(entitlementPackageRelative)))
	if err != nil {
		t.Fatalf("entitlement package root is unsafe: %v", err)
	}
	manifest, manifestRaw := readAccountPackageManifest(t, packageRoot)
	registry, err := machinecontract.LoadDirectory(filepath.Join(repositoryRoot, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate("package-manifest", manifestRaw); err != nil {
		t.Fatalf("package.entitlement Manifest schema: %v", err)
	}
	if manifest.PackageID != "package.entitlement" || manifest.Version != "1.0.0" || manifest.SchemaVersion != "1.0.0" {
		t.Fatalf("unexpected package identity: %s@%s schema=%s", manifest.PackageID, manifest.Version, manifest.SchemaVersion)
	}
	if len(manifest.GeneratedOutputs) == 0 {
		t.Fatal("package.entitlement generated_outputs is empty")
	}
	actualManifestDigest, err := machinecontract.DigestWithoutTopLevelField(manifestRaw, "manifest_sha256")
	if err != nil || !digestEqual(actualManifestDigest, manifest.ManifestSHA256) {
		t.Fatalf("manifest digest mismatch: actual=%s declared=%s err=%v", actualManifestDigest, manifest.ManifestSHA256, err)
	}
	contentFilesRaw, err := json.Marshal(manifest.ContentFiles)
	if err != nil {
		t.Fatal(err)
	}
	contentTreeDigest, err := machinecontract.Digest(contentFilesRaw)
	if err != nil || !digestEqual("sha256:"+contentTreeDigest, manifest.ContentTreeSHA256) {
		t.Fatalf("content tree digest mismatch: actual=sha256:%s declared=%s err=%v", contentTreeDigest, manifest.ContentTreeSHA256, err)
	}
	contentFiles := make(map[string]string, len(manifest.ContentFiles))
	for _, file := range manifest.ContentFiles {
		content, readErr := readSafeWorkspaceFile(packageRoot, file.Path)
		if readErr != nil || !digestEqual(digestBytes(content), file.SHA256) {
			t.Fatalf("content file %q does not match locked digest: %v", file.Path, readErr)
		}
		contentFiles[file.Path] = file.SHA256
	}
	outputs := append([]OutputSpec(nil), manifest.GeneratedOutputs...)
	for index := range outputs {
		outputs[index].SourceID = manifest.PackageID
		outputs[index].SourceVersion = manifest.Version
		if !strings.HasPrefix(outputs[index].Path, "src/generated/packages/entitlement/") && outputs[index].Path != "docs/generated/entitlement-integration.md" {
			t.Fatalf("generated output escapes the frozen entitlement roots: %s", outputs[index].Path)
		}
		if expected, ok := contentFiles[outputs[index].SourcePath]; !ok || machinecontract.ValidateSafeRelativePath(outputs[index].SourcePath) != nil || !digestEqual(expected, outputs[index].SourceSHA256) {
			t.Fatalf("generated output source is absent from content_files: %s", outputs[index].SourcePath)
		}
	}

	targetRoot := t.TempDir()
	writeTestFile(t, targetRoot, "custom/entitlement-extension.ts", []byte("export const productOwned = 'before';\n"))
	rootUnknown := []byte("product-owned unknown file\n")
	writeTestFile(t, targetRoot, "README.product.md", rootUnknown)

	blueprint := entitlementPackageBlueprint()
	if err := registry.Validate("product-blueprint", blueprint); err != nil {
		t.Fatalf("G2B-04 sample Blueprint schema: %v", err)
	}
	plan := entitlementPackagePlan(t, outputs, manifest.ManifestSHA256)
	if err := registry.Validate("assembly-plan", plan); err != nil {
		t.Fatalf("G2B-04 sample Assembly Plan schema: %v", err)
	}
	store := accountPackageSourceStore{root: packageRoot, packageID: manifest.PackageID, version: manifest.Version, manifestChecksum: manifest.ManifestSHA256, contentFiles: contentFiles}
	renderer := NewPureRenderer(store)

	initialSnapshot, err := InspectTarget(targetRoot, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	firstRequest := entitlementPackageRequest(outputs, initialSnapshot)
	firstRendered, err := renderer.Render(context.Background(), Input{Request: firstRequest, Blueprint: blueprint, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	assertEntitlementPackageRenderedFiles(t, firstRendered, outputs, targetRoot, repositoryRoot)
	firstPrepared, err := PrepareTarget(targetRoot, firstRequest, firstRendered, ProjectLock{})
	if err != nil {
		t.Fatalf("prepare initial entitlement package: %v diagnostics=%#v", err, firstPrepared.Diagnostics)
	}
	firstCommit, err := NewFileCommitter().Commit(context.Background(), targetRoot, firstRequest, firstPrepared)
	if err != nil || !firstCommit.AtomicCommitCompleted || !firstCommit.StagingCleanupCompleted {
		t.Fatalf("commit initial entitlement package: result=%#v err=%v", firstCommit, err)
	}
	firstBytes := readManagedOutputBytes(t, targetRoot, outputs)
	if got := []byte(readTestFile(t, targetRoot, "README.product.md")); !bytes.Equal(got, rootUnknown) {
		t.Fatal("initial generation overwrote an unknown product file")
	}

	outputByPath := make(map[string]OutputSpec, len(outputs))
	for _, output := range outputs {
		outputByPath[output.Path] = output
	}
	lock := lockFromChanges(firstPrepared.Changes, firstPrepared.Preserved, outputByPath)
	customAfter := []byte("export const productOwned = 'after';\n")
	unknownAfter := []byte("new product-owned file\n")
	writeTestFile(t, targetRoot, "custom/entitlement-extension.ts", customAfter)
	writeTestFile(t, targetRoot, "custom/new-entitlement-feature.ts", unknownAfter)

	repeatedSnapshot, err := InspectTarget(targetRoot, lock)
	if err != nil {
		t.Fatal(err)
	}
	repeatedRequest := entitlementPackageRequest(outputs, repeatedSnapshot)
	repeatedRendered, err := renderer.Render(context.Background(), Input{Request: repeatedRequest, Blueprint: blueprint, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	assertRenderedResultsEqual(t, firstRendered, repeatedRendered)
	repeatedPrepared, err := PrepareTarget(targetRoot, repeatedRequest, repeatedRendered, lock)
	if err != nil {
		t.Fatalf("prepare repeated entitlement package: %v diagnostics=%#v", err, repeatedPrepared.Diagnostics)
	}
	repeatedCommit, err := NewFileCommitter().Commit(context.Background(), targetRoot, repeatedRequest, repeatedPrepared)
	if err != nil || !repeatedCommit.AtomicCommitCompleted || !repeatedCommit.StagingCleanupCompleted || !repeatedCommit.TargetUnchanged {
		t.Fatalf("commit repeated entitlement package: result=%#v err=%v", repeatedCommit, err)
	}
	secondBytes := readManagedOutputBytes(t, targetRoot, outputs)
	for path, first := range firstBytes {
		if !bytes.Equal(first, secondBytes[path]) {
			t.Fatalf("repeated generation changed bytes for %s", path)
		}
	}
	if got := []byte(readTestFile(t, targetRoot, "custom/entitlement-extension.ts")); !bytes.Equal(got, customAfter) {
		t.Fatal("repeated generation overwrote modified custom content")
	}
	if got := []byte(readTestFile(t, targetRoot, "README.product.md")); !bytes.Equal(got, rootUnknown) {
		t.Fatal("repeated generation overwrote an existing unknown product file")
	}
	if got := []byte(readTestFile(t, targetRoot, "custom/new-entitlement-feature.ts")); !bytes.Equal(got, unknownAfter) {
		t.Fatal("repeated generation overwrote an unknown product file")
	}
}

func findEntitlementPackageRepositoryRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(current, filepath.FromSlash(entitlementPackageRelative), "manifest.json")
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatalf("repository root containing %s was not found", entitlementPackageRelative)
		}
		current = parent
	}
}

func entitlementPackageBlueprint() json.RawMessage {
	return json.RawMessage(`{
		"schema_version":"1.0.0",
		"blueprint_id":"bp_g2b04-entitlement-sample",
		"version":"1.0.0",
		"product":{"name":"G2B04 Entitlement Sample","code":"g2b04-entitlement-sample"},
		"packages":[{"package_id":"package.account","version":"1.0.0"},{"package_id":"package.entitlement","version":"1.0.0"}],
		"applications":[{
			"application_id":"application.web",
			"target":"web",
			"channel":"official",
			"environment":"test",
			"ui":{"template_id":"standard-a","version":"0.1.0","delivery_mode":"generated_source"},
			"output_path":"apps/web"
		}],
		"provider_refs":[{"provider":"notification.security","environment":"test","config_ref":"configs/notification-security.json","secret_refs":[]}],
		"extensions":[],
		"generator":{"id":"platform.generator","version":"1.0.0"},
		"sdk":{"id":"sdk.typescript","version":"0.1.0"},
		"output_root":"generated-products/g2b04-entitlement-sample"
	}`)
}

func entitlementPackagePlan(t *testing.T, outputs []OutputSpec, manifestChecksum string) json.RawMessage {
	t.Helper()
	value := map[string]any{
		"schema_version":    "1.0.0",
		"plan_id":           "plan.g2b04-entitlement-sample",
		"plan_checksum":     rawDigest([]byte("g2b04-entitlement-plan")),
		"blueprint_id":      "bp_g2b04-entitlement-sample",
		"blueprint_version": 1,
		"environment":       "test",
		"catalog_snapshot": map[string]any{
			"revision": "catalog.g2b04-entitlement", "scope": "experimental", "checksum": rawDigest([]byte("g2b04-entitlement-catalog")),
		},
		"generator":        map[string]any{"generator_id": "platform.generator", "version": "1.0.0", "checksum": rawDigest([]byte("g2b04-generator"))},
		"expected_outputs": outputs,
		"packages": []any{
			map[string]any{"package_id": "package.account", "version": "1.0.0", "checksum": rawDigest([]byte("g2b04-account-manifest"))},
			map[string]any{"package_id": "package.entitlement", "version": "1.0.0", "checksum": manifestChecksum},
		},
		"applications": []any{map[string]any{
			"application_id": "application.web", "target": "web", "channel": "official", "environment": "test",
			"delivery_mode": "generated_source", "output_path": "apps/web",
			"template": map[string]any{"template_id": "standard-a", "version": "0.1.0", "checksum": rawDigest([]byte("g2b04-standard-a"))},
		}},
		"extensions": []any{},
		"sdks":       []any{map[string]any{"sdk_id": "sdk.typescript", "version": "0.1.0", "checksum": rawDigest([]byte("g2b04-sdk"))}},
		"capabilities": []any{
			map[string]any{"capability_id": "identity.user-session", "enabled": true, "policy": map[string]any{}, "source_package_id": "package.account", "source_package_version": "1.0.0"},
			map[string]any{"capability_id": "entitlement.check", "enabled": true, "policy": map[string]any{}, "source_package_id": "package.entitlement", "source_package_version": "1.0.0"},
		},
		"dependencies": []any{},
		"conflicts":    []any{},
		"risks": []any{map[string]any{
			"risk_id": "risk.g2b04-generated-ownership", "level": "medium", "category": "generation",
			"summary": "Generated Entitlement files are updated only from their locked source.", "requires_confirmation": true,
		}},
		"providers": []any{map[string]any{
			"provider": "notification.security", "environment": "test",
			"config_ref": "configs/notification-security.json", "secret_refs": []SecretRef{},
		}},
		"required_providers":   []string{"notification.security"},
		"required_secret_refs": []SecretRef{},
		"confirmation": map[string]any{
			"required": true, "blocking_conflict_count": 0, "risk_count": 1,
			"statements":       []string{"Confirm locked generated Entitlement file ownership."},
			"summary_checksum": rawDigest([]byte("g2b04-entitlement-confirmation")),
		},
		"executable": true,
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func entitlementPackageRequest(outputs []OutputSpec, snapshot TargetSnapshot) Request {
	return Request{
		SchemaVersion: "1.0.0", RequestID: "request.g2b04-entitlement", Operation: "generate", WorkspaceRef: "workspace.g2b04-entitlement",
		PlanChecksum: rawDigest([]byte("g2b04-entitlement-plan")), TargetSnapshotChecksum: snapshot.Checksum,
		Generator:      Tool{GeneratorID: "platform.generator", Version: "1.0.0", Checksum: rawDigest([]byte("g2b04-generator"))},
		Inputs:         InputPaths{BlueprintPath: "contracts/blueprint.json", PlanPath: "contracts/plan.json"},
		DesiredOutputs: append([]OutputSpec(nil), outputs...), ExistingFiles: append([]ExistingFile(nil), snapshot.Files...),
		ProtectedPaths: []string{"custom"}, SecretRefs: []SecretRef{},
		StagingPath: ".runtime/generator/g2b04-entitlement", RollbackPointPath: "artifacts/rollback/g2b04-entitlement.json", ConflictPolicy: "stop",
		Determinism: Determinism{Timezone: "UTC", Locale: "C", SortOrder: "bytewise"},
	}
}

func assertEntitlementPackageRenderedFiles(t *testing.T, result Result, outputs []OutputSpec, targetRoot, repositoryRoot string) {
	t.Helper()
	if len(result.Files) != len(outputs) {
		t.Fatalf("rendered file count=%d, want=%d", len(result.Files), len(outputs))
	}
	drivePath := regexp.MustCompile(`(?i)(?:^|[\s"'(=])[a-z]:[\\/]`)
	unixHostPath := regexp.MustCompile(`(?i)/(?:users|home|tmp|var/tmp)/`)
	credentialLiteral := regexp.MustCompile(`(?i)["']?(?:access[_-]?token|refresh[_-]?token|password|credential|proof|client[_-]?secret)["']?[[:space:]]*[:=][[:space:]]*["']([^"']{8,})["']`)
	for _, file := range result.Files {
		if file.Path == "" || !validDigest(file.SHA256) || !validDigest(file.GeneratedSHA256) || !validDigest(file.SourceManifestSHA256) {
			t.Fatalf("incomplete rendered metadata for %q", file.Path)
		}
		if bytes.Contains(file.Bytes, []byte(filepath.Clean(targetRoot))) || bytes.Contains(file.Bytes, []byte(filepath.Clean(repositoryRoot))) || drivePath.Match(file.Bytes) || unixHostPath.Match(file.Bytes) {
			t.Fatalf("generated output %q contains a host absolute path", file.Path)
		}
		lower := strings.ToLower(string(file.Bytes))
		if !strings.HasSuffix(file.Path, ".test.ts") && (strings.Contains(lower, "product_id") || strings.Contains(lower, "tenant_id") || strings.Contains(lower, "user_id")) {
			t.Fatalf("generated runtime output %q contains caller-owned scope literals", file.Path)
		}
		if !strings.HasSuffix(file.Path, ".test.ts") {
			for _, match := range credentialLiteral.FindAllSubmatch(file.Bytes, -1) {
				value := strings.ToLower(string(match[1]))
				if !strings.Contains(value, "fixture") && !strings.Contains(value, "example") && !strings.Contains(value, "generated") {
					t.Fatalf("generated runtime output %q contains a credential-shaped literal", file.Path)
				}
			}
		}
	}
}
