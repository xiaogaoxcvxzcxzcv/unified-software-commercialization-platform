package machinecatalog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
)

func TestAccountSourcePackageRemainsContractedAndOrdinaryUnpublished(t *testing.T) {
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
		t.Fatalf("source publication state = %q %#v", manifest.LifecycleStatus, manifest.Availability)
	}
	if err := validateDocumentIntegrity(sourceDocument{
		contents: raw, identity: manifest.PackageID, version: manifest.Version,
		versionRoot: versionRoot, manifestName: "manifest.json",
	}, manifest.ManifestSHA256, manifest.ContentFiles, manifest.ContentTreeSHA256); err != nil {
		t.Fatalf("Account source package integrity: %v", err)
	}
	for _, output := range manifest.GeneratedOutputs {
		if output.SourceSHA256 == "" {
			t.Fatalf("generated output source is unsealed: %#v", output)
		}
	}
	ordinaryPath := filepath.Join(root, "platform", "capability-packages", "package.account")
	if _, err := os.Stat(ordinaryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Account package entered ordinary catalog at %s: %v", ordinaryPath, err)
	}
	if !errors.Is(validatePackageLifecycle(manifest.LifecycleStatus, ordinaryView), ErrCatalogState) {
		t.Fatal("ordinary catalog accepted contracted Account source package")
	}
	if !errors.Is(validatePackageLifecycle(manifest.LifecycleStatus, experimentalView), ErrCatalogState) {
		t.Fatal("experimental catalog accepted contracted Account source package without candidate publication")
	}
}

func TestAccountExperimentalVerifiedCandidateIsIsolatedFromOrdinaryCatalog(t *testing.T) {
	root := repositoryRoot(t)
	candidateRoot := filepath.Join(root, "platform", "experimental", "capability-packages", "package.account", "1.0.0")
	raw, err := os.ReadFile(filepath.Join(candidateRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest PackageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.PackageID != "package.account" || manifest.Version != "1.0.0" {
		t.Fatalf("candidate identity = %s@%s", manifest.PackageID, manifest.Version)
	}
	if manifest.LifecycleStatus != "verified" {
		t.Fatalf("candidate lifecycle = %s", manifest.LifecycleStatus)
	}
	if len(manifest.Availability) != 2 {
		t.Fatalf("candidate availability = %#v", manifest.Availability)
	}
	seenTargets := map[string]bool{"web": false, "desktop_webview": false}
	for _, entry := range manifest.Availability {
		if entry.Visibility != "experimental" || entry.Readiness != "verified" || entry.DeliveryMode != "generated_source" {
			t.Fatalf("candidate availability entry = %#v", entry)
		}
		if len(entry.Environments) != 1 || entry.Environments[0] != "test" {
			t.Fatalf("candidate environments = %#v", entry.Environments)
		}
		if len(entry.EvidenceRefs) == 0 || entry.EvidenceRefs[0] != "artifacts/reviews/G2A-08/account-package-nine-face-verification.md" {
			t.Fatalf("candidate evidence refs = %#v", entry.EvidenceRefs)
		}
		if _, ok := seenTargets[entry.Target]; !ok {
			t.Fatalf("unexpected candidate target %q", entry.Target)
		}
		seenTargets[entry.Target] = true
	}
	for target, seen := range seenTargets {
		if !seen {
			t.Fatalf("candidate missing target %s", target)
		}
	}
	if err := validateDocumentIntegrity(sourceDocument{
		contents: raw, identity: manifest.PackageID, version: manifest.Version,
		versionRoot: candidateRoot, manifestName: "manifest.json",
	}, manifest.ManifestSHA256, manifest.ContentFiles, manifest.ContentTreeSHA256); err != nil {
		t.Fatalf("Account experimental candidate integrity: %v", err)
	}
	if !errors.Is(validatePackageLifecycle(manifest.LifecycleStatus, ordinaryView), ErrCatalogState) {
		t.Fatal("ordinary lifecycle accepted experimental verified Account candidate")
	}
	if err := validatePackageLifecycle(manifest.LifecycleStatus, experimentalView); err != nil {
		t.Fatalf("experimental lifecycle rejected Account candidate: %v", err)
	}

	blocks, err := LoadBlockCatalog(filepath.Join(root, "platform", "contracts", "catalogs", "v1", "feature-blocks.json"), loadContracts(t))
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := LoadOrdinary(
		filepath.Join(root, "platform", "capability-packages"),
		filepath.Join(root, "platform", "templates"),
		loadContracts(t), accesscontrol.CurrentPermissionCatalog(), blocks,
	)
	if err != nil {
		t.Fatal(err)
	}
	ordinarySnapshot, err := ordinary.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range ordinarySnapshot.Packages {
		if item.ID == "package.account" {
			t.Fatalf("ordinary snapshot exposed Account package: %#v", item)
		}
	}

	experimental, err := LoadExperimental(
		filepath.Join(root, "platform", "experimental", "capability-packages"),
		filepath.Join(root, "platform", "experimental", "templates"),
		loadContracts(t), accesscontrol.CurrentPermissionCatalog(), blocks,
	)
	if err != nil {
		t.Fatal(err)
	}
	experimentalSnapshot, err := experimental.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var found *SnapshotItem
	for index := range experimentalSnapshot.Packages {
		if experimentalSnapshot.Packages[index].ID == "package.account" {
			found = &experimentalSnapshot.Packages[index]
		}
	}
	if found == nil {
		t.Fatal("experimental snapshot did not expose Account candidate")
	}
	if found.Version != "1.0.0" || len(found.Availability) != 2 {
		t.Fatalf("experimental snapshot Account item = %#v", found)
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
