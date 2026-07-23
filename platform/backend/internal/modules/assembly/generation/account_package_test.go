package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

const (
	accountPackageRelative          = "platform/contracts/packages/package.account/1.0.0"
	accountPackageSensitiveSentinel = "g2a07-sensitive-value-6f864336-that-must-never-render"
	accountPackageWindowsSentinel   = "C:\\Users\\g2a07-sensitive\\private-config.json"
	accountPackageUnixSentinel      = "/home/g2a07-sensitive/private-config.json"
)

type accountPackageManifest struct {
	SchemaVersion    string       `json:"schema_version"`
	PackageID        string       `json:"package_id"`
	Version          string       `json:"version"`
	GeneratedOutputs []OutputSpec `json:"generated_outputs"`
	ContentFiles     []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Kind   string `json:"kind"`
	} `json:"content_files"`
	ContentTreeSHA256 string `json:"content_tree_sha256"`
	ManifestSHA256    string `json:"manifest_sha256"`
}

type accountPackageSourceStore struct {
	root             string
	packageID        string
	version          string
	manifestChecksum string
	contentFiles     map[string]string
}

func (s accountPackageSourceStore) ReadLockedSource(sourceID, version, manifestChecksum, sourcePath, sourceChecksum string) ([]byte, error) {
	if sourceID != s.packageID || version != s.version || !digestEqual(manifestChecksum, s.manifestChecksum) {
		return nil, errors.New("locked package identity mismatch")
	}
	if machinecontract.ValidateSafeRelativePath(sourcePath) != nil {
		return nil, errors.New("locked source path is unsafe")
	}
	expected, ok := s.contentFiles[sourcePath]
	if !ok || !digestEqual(expected, sourceChecksum) {
		return nil, errors.New("source is not locked by package content_files")
	}
	content, err := readSafeWorkspaceFile(s.root, sourcePath)
	if err != nil || !digestEqual(digestBytes(content), sourceChecksum) {
		return nil, errors.New("locked source bytes unavailable")
	}
	return content, nil
}

func TestAccountPackageManifestGeneratesDeterministicallyAndPreservesProductFiles(t *testing.T) {
	repositoryRoot := findAccountPackageRepositoryRoot(t)
	packageRoot, err := validateWorkspaceRoot(filepath.Join(repositoryRoot, filepath.FromSlash(accountPackageRelative)))
	if err != nil {
		t.Fatalf("account package root is unsafe: %v", err)
	}
	manifest, manifestRaw := readAccountPackageManifest(t, packageRoot)
	registry, err := machinecontract.LoadDirectory(filepath.Join(repositoryRoot, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate("package-manifest", manifestRaw); err != nil {
		t.Fatalf("package.account Manifest schema: %v", err)
	}
	if manifest.PackageID != "package.account" || manifest.Version != "1.0.0" || manifest.SchemaVersion != "1.0.0" {
		t.Fatalf("unexpected package identity: %s@%s schema=%s", manifest.PackageID, manifest.Version, manifest.SchemaVersion)
	}
	if len(manifest.GeneratedOutputs) == 0 {
		t.Fatal("package.account generated_outputs is empty")
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
		if file.Kind != "file" || machinecontract.ValidateSafeRelativePath(file.Path) != nil || !validDigest(file.SHA256) {
			t.Fatalf("invalid content_files entry for %q", file.Path)
		}
		if _, duplicate := contentFiles[file.Path]; duplicate {
			t.Fatalf("duplicate content_files path %q", file.Path)
		}
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
		if !strings.HasPrefix(outputs[index].Path, "src/generated/packages/account/") && outputs[index].Path != "docs/generated/account-integration.md" {
			t.Fatalf("generated output escapes the frozen account roots: %s", outputs[index].Path)
		}
		if expected, ok := contentFiles[outputs[index].SourcePath]; !ok || machinecontract.ValidateSafeRelativePath(outputs[index].SourcePath) != nil || !digestEqual(expected, outputs[index].SourceSHA256) {
			t.Fatalf("generated output source is absent from content_files: %s", outputs[index].SourcePath)
		}
	}

	targetRoot := accountPackageTargetRoot(t, repositoryRoot)
	writeTestFile(t, targetRoot, "custom/account-extension.ts", []byte("export const productOwned = 'before';\n"))
	rootUnknown := []byte("product-owned unknown file\n")
	writeTestFile(t, targetRoot, "README.product.md", rootUnknown)

	blueprint := accountPackageBlueprint()
	t.Setenv("G2A07_ACCOUNT_SENSITIVE_SENTINEL", accountPackageSensitiveSentinel)
	t.Setenv("G2A07_ACCOUNT_WINDOWS_PATH_SENTINEL", accountPackageWindowsSentinel)
	t.Setenv("G2A07_ACCOUNT_UNIX_PATH_SENTINEL", accountPackageUnixSentinel)
	if err := registry.Validate("product-blueprint", blueprint); err != nil {
		t.Fatalf("G2A-07 sample Blueprint schema: %v", err)
	}
	plan := accountPackagePlan(t, outputs, manifest.ManifestSHA256)
	if err := registry.Validate("assembly-plan", plan); err != nil {
		t.Fatalf("G2A-07 sample Assembly Plan schema: %v", err)
	}
	store := accountPackageSourceStore{root: packageRoot, packageID: manifest.PackageID, version: manifest.Version, manifestChecksum: manifest.ManifestSHA256, contentFiles: contentFiles}
	renderer := NewPureRenderer(store)

	initialSnapshot, err := InspectTarget(targetRoot, ProjectLock{})
	if err != nil {
		t.Fatal(err)
	}
	firstRequest := accountPackageRequest(outputs, initialSnapshot)
	firstRendered, err := renderer.Render(context.Background(), Input{Request: firstRequest, Blueprint: blueprint, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	assertAccountPackageRenderedFiles(t, firstRendered, outputs, targetRoot, repositoryRoot)
	firstPrepared, err := PrepareTarget(targetRoot, firstRequest, firstRendered, ProjectLock{})
	if err != nil {
		t.Fatalf("prepare initial account package: %v diagnostics=%#v", err, firstPrepared.Diagnostics)
	}
	firstCommit, err := NewFileCommitter().Commit(context.Background(), targetRoot, firstRequest, firstPrepared)
	if err != nil || !firstCommit.AtomicCommitCompleted || !firstCommit.StagingCleanupCompleted {
		t.Fatalf("commit initial account package: result=%#v err=%v", firstCommit, err)
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
	writeTestFile(t, targetRoot, "custom/account-extension.ts", customAfter)
	writeTestFile(t, targetRoot, "custom/new-feature.ts", unknownAfter)

	repeatedSnapshot, err := InspectTarget(targetRoot, lock)
	if err != nil {
		t.Fatal(err)
	}
	repeatedRequest := accountPackageRequest(outputs, repeatedSnapshot)
	repeatedRendered, err := renderer.Render(context.Background(), Input{Request: repeatedRequest, Blueprint: blueprint, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	assertRenderedResultsEqual(t, firstRendered, repeatedRendered)
	repeatedPrepared, err := PrepareTarget(targetRoot, repeatedRequest, repeatedRendered, lock)
	if err != nil {
		t.Fatalf("prepare repeated account package: %v diagnostics=%#v", err, repeatedPrepared.Diagnostics)
	}
	repeatedCommit, err := NewFileCommitter().Commit(context.Background(), targetRoot, repeatedRequest, repeatedPrepared)
	if err != nil || !repeatedCommit.AtomicCommitCompleted || !repeatedCommit.StagingCleanupCompleted || !repeatedCommit.TargetUnchanged {
		t.Fatalf("commit repeated account package: result=%#v err=%v", repeatedCommit, err)
	}
	secondBytes := readManagedOutputBytes(t, targetRoot, outputs)
	for path, first := range firstBytes {
		if !bytes.Equal(first, secondBytes[path]) {
			t.Fatalf("repeated generation changed bytes for %s", path)
		}
	}
	if got := []byte(readTestFile(t, targetRoot, "custom/account-extension.ts")); !bytes.Equal(got, customAfter) {
		t.Fatal("repeated generation overwrote modified custom content")
	}
	if got := []byte(readTestFile(t, targetRoot, "README.product.md")); !bytes.Equal(got, rootUnknown) {
		t.Fatal("repeated generation overwrote an existing unknown product file")
	}
	if got := []byte(readTestFile(t, targetRoot, "custom/new-feature.ts")); !bytes.Equal(got, unknownAfter) {
		t.Fatal("repeated generation overwrote an unknown product file")
	}
}

func TestAccountPackageSourceStoreRejectsEscapingAndLinkedSources(t *testing.T) {
	manifestChecksum := rawDigest([]byte("manifest"))
	sourceChecksum := rawDigest([]byte("source"))
	store := accountPackageSourceStore{
		root: t.TempDir(), packageID: "package.account", version: "1.0.0", manifestChecksum: manifestChecksum,
		contentFiles: map[string]string{"content/source.tmpl": sourceChecksum},
	}
	if _, err := store.ReadLockedSource("package.account", "1.0.0", manifestChecksum, "../source.tmpl", sourceChecksum); err == nil {
		t.Fatal("source store accepted a path escaping the package root")
	}

	t.Run("linked source", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "content"), 0o755); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "source.tmpl")
		if err := os.WriteFile(outside, []byte("source"), 0o644); err != nil {
			t.Fatal(err)
		}
		linked := filepath.Join(root, "content", "source.tmpl")
		if err := os.Symlink(outside, linked); err != nil {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		linkedStore := accountPackageSourceStore{
			root: root, packageID: "package.account", version: "1.0.0", manifestChecksum: manifestChecksum,
			contentFiles: map[string]string{"content/source.tmpl": sourceChecksum},
		}
		if _, err := linkedStore.ReadLockedSource("package.account", "1.0.0", manifestChecksum, "content/source.tmpl", sourceChecksum); err == nil {
			t.Fatal("source store followed a linked package source")
		}
	})
}

func findAccountPackageRepositoryRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(current, filepath.FromSlash(accountPackageRelative), "manifest.json")
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatalf("repository root containing %s was not found", accountPackageRelative)
		}
		current = parent
	}
}

func readAccountPackageManifest(t *testing.T, packageRoot string) (accountPackageManifest, []byte) {
	t.Helper()
	raw, err := readSafeWorkspaceFile(packageRoot, "manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest accountPackageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest, raw
}

func accountPackageTargetRoot(t *testing.T, repositoryRoot string) string {
	t.Helper()
	if configured := os.Getenv("G2A07_ACCOUNT_OUTPUT_ROOT"); configured != "" {
		runtimeParent, err := validateWorkspaceRoot(filepath.Join(repositoryRoot, ".runtime", "G2A-07"))
		if err != nil {
			t.Fatalf("G2A-07 runtime parent is unsafe: %v", err)
		}
		absolute, err := filepath.Abs(configured)
		if err != nil || filepath.Clean(filepath.Dir(absolute)) != runtimeParent || !strings.HasPrefix(filepath.Base(absolute), "account-generated-") {
			t.Fatalf("G2A07_ACCOUNT_OUTPUT_ROOT must be a new account-generated-* child of .runtime/G2A-07")
		}
		if _, statErr := os.Lstat(absolute); !os.IsNotExist(statErr) {
			t.Fatalf("G2A07_ACCOUNT_OUTPUT_ROOT must not already exist")
		}
		resolved, err := resolveNonexistentPath(runtimeParent, filepath.Base(absolute))
		if err != nil {
			t.Fatalf("G2A07_ACCOUNT_OUTPUT_ROOT is unsafe: %v", err)
		}
		if mkdirErr := createSafeDirectory(runtimeParent, resolved, 0o755); mkdirErr != nil {
			t.Fatal(mkdirErr)
		}
		return resolved
	}
	return t.TempDir()
}

func accountPackageBlueprint() json.RawMessage {
	return json.RawMessage(`{
		"schema_version":"1.0.0",
		"blueprint_id":"bp_g2a07-account-sample",
		"version":"1.0.0",
		"product":{"name":"G2A07 Account Sample","code":"g2a07-account-sample"},
		"packages":[{"package_id":"package.account","version":"1.0.0"}],
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
		"output_root":"generated-products/g2a07-account-sample"
	}`)
}

func accountPackagePlan(t *testing.T, outputs []OutputSpec, manifestChecksum string) json.RawMessage {
	t.Helper()
	value := map[string]any{
		"schema_version":    "1.0.0",
		"plan_id":           "plan.g2a07-account-sample",
		"plan_checksum":     rawDigest([]byte("g2a07-account-plan")),
		"blueprint_id":      "bp_g2a07-account-sample",
		"blueprint_version": 1,
		"environment":       "test",
		"catalog_snapshot": map[string]any{
			"revision": "catalog.g2a07-account", "scope": "experimental", "checksum": rawDigest([]byte("g2a07-account-catalog")),
		},
		"generator":        map[string]any{"generator_id": "platform.generator", "version": "1.0.0", "checksum": rawDigest([]byte("g2a07-generator"))},
		"expected_outputs": outputs,
		"packages":         []any{map[string]any{"package_id": "package.account", "version": "1.0.0", "checksum": manifestChecksum}},
		"applications": []any{map[string]any{
			"application_id": "application.web", "target": "web", "channel": "official", "environment": "test",
			"delivery_mode": "generated_source", "output_path": "apps/web",
			"template": map[string]any{"template_id": "standard-a", "version": "0.1.0", "checksum": rawDigest([]byte("g2a07-standard-a"))},
		}},
		"extensions": []any{},
		"sdks":       []any{map[string]any{"sdk_id": "sdk.typescript", "version": "0.1.0", "checksum": rawDigest([]byte("g2a07-sdk"))}},
		"capabilities": []any{map[string]any{
			"capability_id": "identity.user-session", "enabled": true, "policy": map[string]any{},
			"source_package_id": "package.account", "source_package_version": "1.0.0",
		}},
		"dependencies": []any{},
		"conflicts":    []any{},
		"risks": []any{map[string]any{
			"risk_id": "risk.g2a07-generated-ownership", "level": "medium", "category": "generation",
			"summary": "Generated Account files are updated only from their locked source.", "requires_confirmation": true,
		}},
		"providers": []any{map[string]any{
			"provider": "notification.security", "environment": "test",
			"config_ref": "configs/notification-security.json", "secret_refs": []SecretRef{},
		}},
		"required_providers":   []string{"notification.security"},
		"required_secret_refs": []SecretRef{},
		"confirmation": map[string]any{
			"required": true, "blocking_conflict_count": 0, "risk_count": 1,
			"statements":       []string{"Confirm locked generated Account file ownership."},
			"summary_checksum": rawDigest([]byte("g2a07-account-confirmation")),
		},
		"executable": true,
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func accountPackageRequest(outputs []OutputSpec, snapshot TargetSnapshot) Request {
	return Request{
		SchemaVersion: "1.0.0", RequestID: "request.g2a07-account", Operation: "generate", WorkspaceRef: "workspace.g2a07-account",
		PlanChecksum: rawDigest([]byte("g2a07-account-plan")), TargetSnapshotChecksum: snapshot.Checksum,
		Generator:      Tool{GeneratorID: "platform.generator", Version: "1.0.0", Checksum: rawDigest([]byte("g2a07-generator"))},
		Inputs:         InputPaths{BlueprintPath: "contracts/blueprint.json", PlanPath: "contracts/plan.json"},
		DesiredOutputs: append([]OutputSpec(nil), outputs...), ExistingFiles: append([]ExistingFile(nil), snapshot.Files...),
		ProtectedPaths: []string{"custom"}, SecretRefs: []SecretRef{},
		StagingPath: ".runtime/generator/g2a07-account", RollbackPointPath: "artifacts/rollback/g2a07-account.json", ConflictPolicy: "stop",
		Determinism: Determinism{Timezone: "UTC", Locale: "C", SortOrder: "bytewise"},
	}
}

func assertAccountPackageRenderedFiles(t *testing.T, result Result, outputs []OutputSpec, targetRoot, repositoryRoot string) {
	t.Helper()
	if len(result.Files) != len(outputs) {
		t.Fatalf("rendered file count=%d, want=%d", len(result.Files), len(outputs))
	}
	tempRoot := filepath.Clean(targetRoot)
	drivePath := regexp.MustCompile(`(?i)(?:^|[\s"'(=])[a-z]:[\\/]`)
	unixHostPath := regexp.MustCompile(`(?i)/(?:users|home|tmp|var/tmp)/`)
	uncPath := regexp.MustCompile(`\\\\[A-Za-z0-9_-][^\\/[:space:]]+[\\/]`)
	credentialLiteral := regexp.MustCompile(`(?i)["']?(?:access[_-]?token|refresh[_-]?token|password|credential|proof|client[_-]?secret)["']?[[:space:]]*[:=][[:space:]]*["']([^"']{8,})["']`)
	jwtValue := regexp.MustCompile(`eyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}`)
	forbiddenValues := []string{accountPackageSensitiveSentinel, accountPackageWindowsSentinel, accountPackageUnixSentinel, filepath.Clean(targetRoot), filepath.Clean(repositoryRoot)}
	for _, file := range result.Files {
		if file.Path == "" || !validDigest(file.SHA256) || !validDigest(file.GeneratedSHA256) || !validDigest(file.SourceManifestSHA256) {
			t.Fatalf("incomplete rendered metadata for %q", file.Path)
		}
		if bytes.Contains(file.Bytes, []byte(tempRoot)) || bytes.Contains(file.Bytes, []byte(filepath.ToSlash(tempRoot))) || drivePath.Match(file.Bytes) || unixHostPath.Match(file.Bytes) || uncPath.Match(file.Bytes) {
			t.Fatalf("generated output %q contains a host absolute path: drive=%q unix=%q unc=%q", file.Path, drivePath.Find(file.Bytes), unixHostPath.Find(file.Bytes), uncPath.Find(file.Bytes))
		}
		lower := strings.ToLower(string(file.Bytes))
		for _, forbidden := range forbiddenValues {
			if strings.Contains(lower, strings.ToLower(forbidden)) || strings.Contains(lower, strings.ToLower(filepath.ToSlash(forbidden))) {
				t.Fatalf("generated output %q contains a sensitive sentinel or host path", file.Path)
			}
		}
		if strings.Contains(lower, "-----begin private key-----") || jwtValue.Match(file.Bytes) {
			t.Fatalf("generated output %q contains a credential-shaped literal", file.Path)
		}
		for _, match := range credentialLiteral.FindAllSubmatch(file.Bytes, -1) {
			value := strings.ToLower(string(match[1]))
			approvedMarker := strings.Contains(value, "fixture") || strings.Contains(value, "example") || strings.Contains(value, "dummy")
			approvedConstructedTestValue := strings.Contains(file.Path, ".test.") && (value == "access-session-" ||
				value == "refresh-session-" ||
				value == "proof-value")
			if !approvedMarker && !approvedConstructedTestValue {
				t.Fatalf("generated output %q contains a credential-shaped literal", file.Path)
			}
		}
	}
}

func assertRenderedResultsEqual(t *testing.T, first, second Result) {
	t.Helper()
	if len(first.Files) != len(second.Files) {
		t.Fatalf("render count changed: %d != %d", len(first.Files), len(second.Files))
	}
	for index := range first.Files {
		left, right := first.Files[index], second.Files[index]
		if left.OutputSpec != right.OutputSpec || left.SHA256 != right.SHA256 || left.GeneratedSHA256 != right.GeneratedSHA256 || !bytes.Equal(left.Bytes, right.Bytes) {
			t.Fatalf("rendered output is not deterministic at %q", left.Path)
		}
	}
}

func readManagedOutputBytes(t *testing.T, root string, outputs []OutputSpec) map[string][]byte {
	t.Helper()
	paths := make([]string, 0, len(outputs))
	for _, output := range outputs {
		paths = append(paths, output.Path)
	}
	sort.Strings(paths)
	result := make(map[string][]byte, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(fmt.Errorf("read managed output %s: %w", path, err))
		}
		result[path] = content
	}
	return result
}
