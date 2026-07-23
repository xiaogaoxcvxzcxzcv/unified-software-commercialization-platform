package machinecatalog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
)

func TestEntitlementSourcePackageRemainsContractedAndOrdinaryUnpublished(t *testing.T) {
	root := repositoryRoot(t)
	versionRoot := filepath.Join(root, "platform", "contracts", "packages", "package.entitlement", "1.0.0")
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
		t.Fatalf("Entitlement source package integrity: %v", err)
	}
	ordinaryPath := filepath.Join(root, "platform", "capability-packages", "package.entitlement")
	if _, err := os.Stat(ordinaryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Entitlement package entered ordinary catalog at %s: %v", ordinaryPath, err)
	}
	if !errors.Is(validatePackageLifecycle(manifest.LifecycleStatus, ordinaryView), ErrCatalogState) {
		t.Fatal("ordinary catalog accepted contracted Entitlement source package")
	}
	if !errors.Is(validatePackageLifecycle(manifest.LifecycleStatus, experimentalView), ErrCatalogState) {
		t.Fatal("experimental catalog accepted contracted Entitlement source package without candidate publication")
	}
}

func TestEntitlementExperimentalVerifiedCandidateIsIsolatedFromOrdinaryCatalog(t *testing.T) {
	root := repositoryRoot(t)
	candidateRoot := filepath.Join(root, "platform", "experimental", "capability-packages", "package.entitlement", "1.0.0")
	raw, err := os.ReadFile(filepath.Join(candidateRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest PackageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.PackageID != "package.entitlement" || manifest.Version != "1.0.0" {
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
		if len(entry.EvidenceRefs) == 0 || entry.EvidenceRefs[0] != "artifacts/reviews/G2B-05/entitlement-package-nine-face-verification.md" {
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
		t.Fatalf("Entitlement experimental candidate integrity: %v", err)
	}
	if !errors.Is(validatePackageLifecycle(manifest.LifecycleStatus, ordinaryView), ErrCatalogState) {
		t.Fatal("ordinary lifecycle accepted experimental verified Entitlement candidate")
	}
	if err := validatePackageLifecycle(manifest.LifecycleStatus, experimentalView); err != nil {
		t.Fatalf("experimental lifecycle rejected Entitlement candidate: %v", err)
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
		if item.ID == "package.entitlement" {
			t.Fatalf("ordinary snapshot exposed Entitlement package: %#v", item)
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
		if experimentalSnapshot.Packages[index].ID == "package.entitlement" {
			found = &experimentalSnapshot.Packages[index]
		}
	}
	if found == nil {
		t.Fatal("experimental snapshot did not expose Entitlement candidate")
	}
	if found.Version != "1.0.0" || len(found.Availability) != 2 {
		t.Fatalf("experimental snapshot Entitlement item = %#v", found)
	}
}
