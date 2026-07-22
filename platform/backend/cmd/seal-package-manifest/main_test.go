package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunSealsAccountPackageAndGeneratedSources(t *testing.T) {
	repositoryRoot := findRepositoryRoot(t)
	sourceRoot := filepath.Join(repositoryRoot, "platform", "contracts", "packages", "package.account", "1.0.0")
	catalogRoot := t.TempDir()
	versionRoot := filepath.Join(catalogRoot, "package.account", "1.0.0")
	copyDirectory(t, sourceRoot, versionRoot)
	manifestPath := filepath.Join(versionRoot, "manifest.json")
	schemaDirectory := filepath.Join(repositoryRoot, "platform", "contracts", "schemas", "v1")

	if err := run(catalogRoot, manifestPath, schemaDirectory); err != nil {
		t.Fatalf("seal Account package: %v", err)
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["lifecycle_status"] != "contracted" || len(manifest["availability"].([]any)) != 0 {
		t.Fatalf("sealer changed publication state: %v %v", manifest["lifecycle_status"], manifest["availability"])
	}
	files := manifest["content_files"].([]any)
	outputs := manifest["generated_outputs"].([]any)
	if len(files) != 7 || len(outputs) != 6 {
		t.Fatalf("sealed content/output counts = %d/%d", len(files), len(outputs))
	}
	methods := manifest["sdk_methods"].([]any)
	if len(methods) != 22 {
		t.Fatalf("sealed SDK method count = %d", len(methods))
	}
	expectedMethods := []string{
		"startRegistrationVerification", "registerUser", "login", "getCurrentSession", "refreshSession", "restoreSession",
		"logout", "clearSession", "startRecovery", "completeRecovery", "getProfile", "updateProfile", "changePassword",
		"listSessions", "revokeSession", "startExternalLogin", "completeExternalLogin", "exchangeWechatCode",
		"listExternalIdentities", "linkExternalIdentity", "unlinkExternalIdentity", "getAccessSummary",
	}
	for index, expected := range expectedMethods {
		if methods[index] != expected {
			t.Fatalf("sealed SDK method %d = %v, want %s", index, methods[index], expected)
		}
	}
	digests := make(map[string]string, len(files))
	for _, value := range files {
		file := value.(map[string]any)
		digests[file["path"].(string)] = file["sha256"].(string)
	}
	for _, value := range outputs {
		output := value.(map[string]any)
		if digests[output["source_path"].(string)] != output["source_sha256"].(string) {
			t.Fatalf("source digest is not locked: %v", output)
		}
	}
}

func TestRunRejectsWrongManifestPathAndUnsafeGeneratedSource(t *testing.T) {
	repositoryRoot := findRepositoryRoot(t)
	sourceRoot := filepath.Join(repositoryRoot, "platform", "contracts", "packages", "package.account", "1.0.0")
	catalogRoot := t.TempDir()
	versionRoot := filepath.Join(catalogRoot, "package.account", "1.0.0")
	copyDirectory(t, sourceRoot, versionRoot)
	schemaDirectory := filepath.Join(repositoryRoot, "platform", "contracts", "schemas", "v1")
	manifestPath := filepath.Join(versionRoot, "manifest.json")

	if err := run(catalogRoot, filepath.Join(versionRoot, "package.json"), schemaDirectory); err == nil {
		t.Fatal("non-manifest.json path was accepted")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	outputs := manifest["generated_outputs"].([]any)
	if len(outputs) == 0 {
		t.Fatal("fixture must contain generated outputs")
	}
	outputs[0].(map[string]any)["source_path"] = "../outside.tmpl"
	invalid, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(catalogRoot, manifestPath, schemaDirectory); err == nil {
		t.Fatal("unsafe generated source path was accepted")
	}
}

func TestRunRejectsManifestReplacementBeforeWrite(t *testing.T) {
	repositoryRoot := findRepositoryRoot(t)
	sourceRoot := filepath.Join(repositoryRoot, "platform", "contracts", "packages", "package.account", "1.0.0")
	catalogRoot := t.TempDir()
	versionRoot := filepath.Join(catalogRoot, "package.account", "1.0.0")
	copyDirectory(t, sourceRoot, versionRoot)
	manifestPath := filepath.Join(versionRoot, "manifest.json")
	schemaDirectory := filepath.Join(repositoryRoot, "platform", "contracts", "schemas", "v1")

	originalHook := beforeManifestWrite
	t.Cleanup(func() { beforeManifestWrite = originalHook })
	beforeManifestWrite = func() {
		beforeManifestWrite = func() {}
		if err := os.Rename(manifestPath, manifestPath+".replaced"); err != nil {
			t.Skipf("open-file replacement unavailable: %v", err)
		}
		if err := os.WriteFile(manifestPath, []byte("{}"), 0o600); err != nil {
			t.Fatalf("write replacement manifest: %v", err)
		}
	}
	if err := run(catalogRoot, manifestPath, schemaDirectory); err == nil ||
		!strings.Contains(err.Error(), "changed while it was being sealed") {
		t.Fatalf("manifest replacement error = %v", err)
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil || string(raw) != "{}" {
		t.Fatalf("replacement manifest was overwritten: %q err=%v", raw, err)
	}
}

func TestScanFilesRejectsLinksWhenSupported(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "manifest.json")
	target := filepath.Join(root, "target.txt")
	link := filepath.Join(root, "linked.txt")
	if err := os.WriteFile(manifest, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, _, err := scanFiles(root, manifest); err == nil || !strings.Contains(err.Error(), "linked catalog content") {
		t.Fatalf("linked content error = %v", err)
	}
}

func TestReadRegularUnlinkedFileRejectsReplacementLinkWhenSupported(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "content.txt")
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, err := readRegularUnlinkedFile(path, expected); err == nil {
		t.Fatal("replacement content link was accepted")
	}
}

func copyDirectory(t *testing.T, source, target string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr

		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, content, 0o600)
	}); err != nil {
		t.Fatal(err)
	}
}

func findRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	directory := filepath.Dir(file)
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
func TestRunDoesNotFollowManifestLinkReplacementDuringWrite(t *testing.T) {
	repositoryRoot := findRepositoryRoot(t)
	sourceRoot := filepath.Join(repositoryRoot, "platform", "contracts", "packages", "package.account", "1.0.0")
	catalogRoot := t.TempDir()
	versionRoot := filepath.Join(catalogRoot, "package.account", "1.0.0")
	copyDirectory(t, sourceRoot, versionRoot)
	manifestPath := filepath.Join(versionRoot, "manifest.json")
	targetPath := filepath.Join(catalogRoot, "link-target.json")
	targetRaw := []byte("{\"protected\":true}")
	if err := os.WriteFile(targetPath, targetRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	schemaDirectory := filepath.Join(repositoryRoot, "platform", "contracts", "schemas", "v1")

	originalHook := afterManifestPathCheck
	t.Cleanup(func() { afterManifestPathCheck = originalHook })
	afterManifestPathCheck = func() {
		afterManifestPathCheck = func() {}
		if err := os.Rename(manifestPath, manifestPath+".opened"); err != nil {
			t.Skipf("open-file replacement unavailable: %v", err)
		}
		if err := os.Symlink(targetPath, manifestPath); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
	}
	if err := run(catalogRoot, manifestPath, schemaDirectory); err == nil ||
		!strings.Contains(err.Error(), "path changed during write") {
		t.Fatalf("manifest link replacement error = %v", err)
	}
	raw, err := os.ReadFile(targetPath)
	if err != nil || string(raw) != string(targetRaw) {
		t.Fatalf("link target was overwritten: %q err=%v", raw, err)
	}
}
