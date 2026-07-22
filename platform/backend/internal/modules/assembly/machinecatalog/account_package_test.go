package machinecatalog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountContractedPackageHasClosedContentAndNoPublishedCatalogEntry(t *testing.T) {
	root := repositoryRoot(t)
	versionRoot := filepath.Join(root, "platform", "contracts", "packages", "package.account", "1.0.0")
	raw, err := os.ReadFile(filepath.Join(versionRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest PackageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.LifecycleStatus != "contracted" || len(manifest.Availability) != 0 {
		t.Fatalf("publication state = %q %#v", manifest.LifecycleStatus, manifest.Availability)
	}
	if err := validateDocumentIntegrity(sourceDocument{
		contents: raw, identity: manifest.PackageID, version: manifest.Version,
		versionRoot: versionRoot, manifestName: "manifest.json",
	}, manifest.ManifestSHA256, manifest.ContentFiles, manifest.ContentTreeSHA256); err != nil {
		t.Fatalf("Account package integrity: %v", err)
	}
	for _, output := range manifest.GeneratedOutputs {
		if output.SourceSHA256 == "" {
			t.Fatalf("generated output source is unsealed: %#v", output)
		}
	}
	for _, catalogRoot := range []string{
		filepath.Join(root, "platform", "capability-packages"),
		filepath.Join(root, "platform", "experimental", "capability-packages"),
	} {
		path := filepath.Join(catalogRoot, "package.account")
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("contracted Account package was published at %s: %v", path, err)
		}
	}
	if !errors.Is(validatePackageLifecycle(manifest.LifecycleStatus, ordinaryView), ErrCatalogState) {
		t.Fatal("ordinary catalog accepted contracted Account package")
	}
	if !errors.Is(validatePackageLifecycle(manifest.LifecycleStatus, experimentalView), ErrCatalogState) {
		t.Fatal("experimental catalog accepted contracted Account package")
	}
}

func TestAccountContractedPackageRejectsDriftExtraFilesAndSourceDigestMismatch(t *testing.T) {
	root := repositoryRoot(t)
	sourceRoot := filepath.Join(root, "platform", "contracts", "packages", "package.account", "1.0.0")
	raw, err := os.ReadFile(filepath.Join(sourceRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest PackageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.GeneratedOutputs) == 0 {
		t.Fatal("Account package has no generated outputs")
	}

	t.Run("content drift", func(t *testing.T) {
		versionRoot := copyAccountPackage(t, sourceRoot)
		path := filepath.Join(versionRoot, filepath.FromSlash(manifest.GeneratedOutputs[0].SourcePath))
		if err := os.WriteFile(path, []byte("drift"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := validateDocumentIntegrity(sourceDocument{
			contents: raw, identity: manifest.PackageID, version: manifest.Version,
			versionRoot: versionRoot, manifestName: "manifest.json",
		}, manifest.ManifestSHA256, manifest.ContentFiles, manifest.ContentTreeSHA256)
		if !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("content drift error = %v", err)
		}
	})

	t.Run("extra file", func(t *testing.T) {
		versionRoot := copyAccountPackage(t, sourceRoot)
		if err := os.WriteFile(filepath.Join(versionRoot, "unexpected.txt"), []byte("extra"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := validateDocumentIntegrity(sourceDocument{
			contents: raw, identity: manifest.PackageID, version: manifest.Version,
			versionRoot: versionRoot, manifestName: "manifest.json",
		}, manifest.ManifestSHA256, manifest.ContentFiles, manifest.ContentTreeSHA256)
		if !errors.Is(err, ErrInvalidLayout) {
			t.Fatalf("extra file error = %v", err)
		}
	})

	t.Run("source digest", func(t *testing.T) {
		mutated := manifest
		mutated.GeneratedOutputs = append([]GeneratedOutput(nil), manifest.GeneratedOutputs...)
		mutated.GeneratedOutputs[0].SourceSHA256 = "sha256:" + strings.Repeat("0", 64)
		if err := validatePackageReferences(mutated); err == nil {
			t.Fatal("generated output source digest mismatch was accepted")
		}
	})
}

func copyAccountPackage(t *testing.T, sourceRoot string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "package.account", "1.0.0")
	if err := os.CopyFS(target, os.DirFS(sourceRoot)); err != nil {
		t.Fatal(err)
	}
	return target
}
