package machinecatalog

import (
	"encoding/json"
	"fmt"
	"sort"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

func (c *Catalog) snapshot(packages []PackageManifest, templates []TemplateManifest, extensions []ExtensionManifest) (CatalogSnapshot, error) {
	packageItems := make([]SnapshotItem, 0, len(packages))
	for _, manifest := range packages {
		packageItems = append(packageItems, snapshotPackage(manifest))
	}
	templateItems := make([]SnapshotItem, 0, len(templates))
	for _, manifest := range templates {
		templateItems = append(templateItems, snapshotTemplate(manifest))
	}
	extensionItems := make([]ExtensionSnapshotItem, 0, len(extensions))
	for _, manifest := range extensions {
		extensionItems = append(extensionItems, snapshotExtension(manifest))
	}
	sort.Slice(packageItems, func(i, j int) bool {
		if packageItems[i].ID == packageItems[j].ID {
			return packageItems[i].Version < packageItems[j].Version
		}
		return packageItems[i].ID < packageItems[j].ID
	})
	sort.Slice(templateItems, func(i, j int) bool {
		if templateItems[i].ID == templateItems[j].ID {
			return templateItems[i].Version < templateItems[j].Version
		}
		return templateItems[i].ID < templateItems[j].ID
	})
	sort.Slice(extensionItems, func(i, j int) bool {
		if extensionItems[i].ExtensionID == extensionItems[j].ExtensionID {
			return extensionItems[i].Version < extensionItems[j].Version
		}
		return extensionItems[i].ExtensionID < extensionItems[j].ExtensionID
	})
	generatorItems := c.snapshotTools("generator")
	sdkItems := c.snapshotTools("sdk")
	snapshot := CatalogSnapshot{
		SchemaVersion:       machinecontract.SchemaVersion,
		Revision:            "catalog-pending",
		CatalogScope:        c.view.visibility,
		Packages:            packageItems,
		Templates:           templateItems,
		Extensions:          extensionItems,
		Generators:          generatorItems,
		SDKs:                sdkItems,
		PermissionCatalog:   VersionedInput{Version: c.permissions.Version(), SHA256: c.permissions.Checksum()},
		FeatureBlockCatalog: VersionedInput{Version: c.blocks.Version(), SHA256: c.blocks.Checksum()},
		SchemaCatalog:       VersionedInput{Version: c.contracts.Version(), SHA256: c.contracts.Checksum()},
		SnapshotSHA256:      "sha256:0000000000000000000000000000000000000000000000000000000000000000",
	}
	seed, err := snapshotDigest(snapshot)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	snapshot.Revision = "catalog-" + seed[len("sha256:"):len("sha256:")+12]
	digest, err := snapshotDigest(snapshot)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	snapshot.SnapshotSHA256 = digest
	contents, err := json.Marshal(snapshot)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	if err := c.contracts.Validate("catalog-snapshot", contents); err != nil {
		return CatalogSnapshot{}, fmt.Errorf("validate catalog snapshot: %w", err)
	}
	return snapshot, nil
}

func snapshotExtension(manifest ExtensionManifest) ExtensionSnapshotItem {
	return ExtensionSnapshotItem{
		ExtensionID: manifest.ExtensionID, Version: manifest.Version, ProductCode: manifest.ProductCode,
		ManifestPath: manifest.ManifestPath, ManifestSHA256: manifest.ManifestSHA256, ContentTreeSHA256: manifest.ContentTreeSHA256,
		SupportedTargets: stableStrings(manifest.SupportedTargets), SupportedDeliveryModes: stableStrings(manifest.SupportedDeliveryModes),
		SupportedEnvironments: stableStrings(manifest.SupportedEnvironments), DataNamespace: manifest.DataNamespace,
	}
}

func (c *Catalog) snapshotTools(kind string) []ToolSnapshotItem {
	items := make([]ToolSnapshotItem, 0)
	for key, manifests := range c.tools {
		if len(key) <= len(kind) || key[:len(kind)] != kind || key[len(kind)] != '\x00' {
			continue
		}
		for _, manifest := range manifests {
			items = append(items, ToolSnapshotItem{
				ToolID: manifest.ToolID, Version: manifest.Version,
				ManifestSHA256: manifest.ManifestSHA256, ContentTreeSHA256: manifest.ContentTreeSHA256,
				Protocol: manifest.Protocol, Execution: manifest.Execution,
				SupportedTargets:       stableStrings(manifest.SupportedTargets),
				SupportedDeliveryModes: stableStrings(manifest.SupportedDeliveryModes),
				SupportedEnvironments:  stableStrings(manifest.SupportedEnvironments),
			})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ToolID == items[j].ToolID {
			return items[i].Version < items[j].Version
		}
		return items[i].ToolID < items[j].ToolID
	})
	return items
}

func snapshotDigest(snapshot CatalogSnapshot) (string, error) {
	contents, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return machinecontract.DigestWithoutTopLevelField(contents, "snapshot_sha256")
}

func snapshotPackage(manifest PackageManifest) SnapshotItem {
	return SnapshotItem{
		ID: manifest.PackageID, Version: manifest.Version,
		ManifestSHA256: manifest.ManifestSHA256, ContentTreeSHA256: manifest.ContentTreeSHA256,
		Availability: stableAvailability(manifest.Availability), Dependencies: stableRequirements(manifest.Dependencies), Conflicts: stableRequirements(manifest.Conflicts),
		SupportedTargets: stableStrings(manifest.SupportedTargets), SupportedDeliveryModes: stableStrings(manifest.SupportedDeliveryModes),
		BackendCapabilities: stableStrings(manifest.BackendCapabilities),
	}
}

func snapshotTemplate(manifest TemplateManifest) SnapshotItem {
	return SnapshotItem{
		ID: manifest.TemplateID, Version: manifest.Version,
		ManifestSHA256: manifest.ManifestSHA256, ContentTreeSHA256: manifest.ContentTreeSHA256,
		Availability: stableAvailability(manifest.Availability), Dependencies: stableRequirements(manifest.PackageCompatibility), Conflicts: []Requirement{},
		SupportedTargets: stableStrings(manifest.SupportedTargets), SupportedDeliveryModes: stableStrings(manifest.SupportedDeliveryModes),
		BackendCapabilities: []string{},
	}
}

func stableAvailability(values []Availability) []Availability {
	result := append(make([]Availability, 0, len(values)), values...)
	for index := range result {
		result[index].Environments = stableStrings(result[index].Environments)
		result[index].EvidenceRefs = nil
	}
	sort.Slice(result, func(i, j int) bool {
		first := result[i].Target + "\x00" + result[i].DeliveryMode + "\x00" + result[i].Visibility + "\x00" + result[i].Readiness
		second := result[j].Target + "\x00" + result[j].DeliveryMode + "\x00" + result[j].Visibility + "\x00" + result[j].Readiness
		return first < second
	})
	return result
}

func stableRequirements(values []Requirement) []Requirement {
	result := append(make([]Requirement, 0, len(values)), values...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].PackageID == result[j].PackageID {
			return result[i].VersionRange < result[j].VersionRange
		}
		return result[i].PackageID < result[j].PackageID
	})
	return result
}

func stableStrings(values []string) []string {
	result := append(make([]string, 0, len(values)), values...)
	sort.Strings(result)
	return result
}
