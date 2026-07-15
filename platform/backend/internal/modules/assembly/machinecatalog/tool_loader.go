package machinecatalog

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Masterminds/semver/v3"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

var trustedBuiltinAdapters = map[string]map[string]struct{}{
	"generator": {"assembly.pure-renderer": {}},
	"sdk":       {"assembly.client-sdk": {}},
}

func LoadOrdinaryWithTools(packageRoot, templateRoot, generatorRoot, sdkRoot string, contracts *machinecontract.Registry, permissions PermissionCatalog, blocks *BlockCatalog) (*Catalog, error) {
	return loadWithTools(packageRoot, templateRoot, generatorRoot, sdkRoot, contracts, permissions, blocks, ordinaryView)
}

func LoadExperimentalWithTools(packageRoot, templateRoot, generatorRoot, sdkRoot string, contracts *machinecontract.Registry, permissions PermissionCatalog, blocks *BlockCatalog) (*Catalog, error) {
	return loadWithTools(packageRoot, templateRoot, generatorRoot, sdkRoot, contracts, permissions, blocks, experimentalView)
}

func loadWithTools(packageRoot, templateRoot, generatorRoot, sdkRoot string, contracts *machinecontract.Registry, permissions PermissionCatalog, blocks *BlockCatalog, view catalogView) (*Catalog, error) {
	catalog, err := load(packageRoot, templateRoot, contracts, permissions, blocks, view)
	if err != nil {
		return nil, err
	}
	generators, err := discover(generatorRoot, "manifest.json")
	if err != nil {
		return nil, err
	}
	sdks, err := discover(sdkRoot, "manifest.json")
	if err != nil {
		return nil, err
	}
	if err := catalog.addTools("generator", generators); err != nil {
		return nil, err
	}
	if err := catalog.addTools("sdk", sdks); err != nil {
		return nil, err
	}
	return catalog, nil
}

func (c *Catalog) addTools(expectedKind string, documents []sourceDocument) error {
	for _, document := range documents {
		if err := c.contracts.Validate("tool-manifest", document.contents); err != nil {
			return err
		}
		var manifest ToolManifest
		if err := json.Unmarshal(document.contents, &manifest); err != nil {
			return err
		}
		if manifest.ToolKind != expectedKind || manifest.ToolID != document.identity || manifest.Version != document.version {
			return fmt.Errorf("%w: path=%s/%s@%s manifest=%s/%s@%s", ErrIdentityMismatch, expectedKind, document.identity, document.version, manifest.ToolKind, manifest.ToolID, manifest.Version)
		}
		if manifest.CatalogScope != c.view.visibility || manifest.Readiness != c.view.readiness {
			return fmt.Errorf("%w: tool %s@%s wants %s/%s, got %s/%s", ErrCatalogState, manifest.ToolID, manifest.Version, c.view.visibility, c.view.readiness, manifest.CatalogScope, manifest.Readiness)
		}
		if err := validateDocumentIntegrity(document, manifest.ManifestSHA256, manifest.ContentFiles, manifest.ContentTreeSHA256); err != nil {
			return fmt.Errorf("tool %s/%s@%s: %w", manifest.ToolKind, manifest.ToolID, manifest.Version, err)
		}
		if err := validateToolManifest(manifest); err != nil {
			return fmt.Errorf("tool %s/%s@%s: %w", manifest.ToolKind, manifest.ToolID, manifest.Version, err)
		}
		key := toolKey(manifest.ToolKind, manifest.ToolID)
		for _, existing := range c.tools[key] {
			if existing.Version == manifest.Version {
				return fmt.Errorf("%w: %s/%s@%s", ErrDuplicateToolVersion, manifest.ToolKind, manifest.ToolID, manifest.Version)
			}
		}
		c.tools[key] = append(c.tools[key], manifest)
		c.toolSources[toolVersionKey(manifest.ToolKind, manifest.ToolID, manifest.Version)] = document
	}
	for key := range c.tools {
		sort.Slice(c.tools[key], func(i, j int) bool {
			first, _ := semver.StrictNewVersion(c.tools[key][i].Version)
			second, _ := semver.StrictNewVersion(c.tools[key][j].Version)
			return first.GreaterThan(second)
		})
		for index := 1; index < len(c.tools[key]); index++ {
			first, _ := semver.StrictNewVersion(c.tools[key][index-1].Version)
			second, _ := semver.StrictNewVersion(c.tools[key][index].Version)
			if first.Equal(second) && c.tools[key][index-1].Version != c.tools[key][index].Version {
				return fmt.Errorf("%w: equal SemVer precedence %s and %s", ErrDuplicateToolVersion, c.tools[key][index-1].Version, c.tools[key][index].Version)
			}
		}
	}
	return nil
}

func validateToolManifest(manifest ToolManifest) error {
	platformVersion, err := semver.StrictNewVersion(machinecontract.SchemaVersion)
	if err != nil {
		return err
	}
	contractRange, err := semver.NewConstraint(manifest.PlatformContractRange)
	if err != nil || !contractRange.Check(platformVersion) {
		return fmt.Errorf("%w: platform contract %s does not satisfy %s", ErrToolIncompatible, machinecontract.SchemaVersion, manifest.PlatformContractRange)
	}
	content := make(map[string]ContentFile, len(manifest.ContentFiles))
	for _, file := range manifest.ContentFiles {
		content[file.Path] = file
	}
	if manifest.Execution.Mode == "builtin_adapter" {
		adapters := trustedBuiltinAdapters[manifest.ToolKind]
		if _, trusted := adapters[manifest.Execution.AdapterID]; !trusted {
			return fmt.Errorf("%w: unregistered builtin adapter %s", ErrUnknownTool, manifest.Execution.AdapterID)
		}
	} else {
		if err := machinecontract.ValidateSafeRelativePath(manifest.Execution.Path); err != nil {
			return err
		}
		file, exists := content[manifest.Execution.Path]
		if !exists || file.SHA256 != manifest.Execution.SHA256 {
			return fmt.Errorf("%w: execution entrypoint is not sealed by content_files", ErrChecksumMismatch)
		}
	}
	covered := make(map[string]struct{}, len(manifest.Evidence))
	for _, evidence := range manifest.Evidence {
		if !contains(manifest.SupportedTargets, evidence.Target) || !contains(manifest.SupportedDeliveryModes, evidence.DeliveryMode) || !contains(manifest.SupportedEnvironments, evidence.Environment) {
			return fmt.Errorf("%w: evidence %s/%s/%s", ErrToolIncompatible, evidence.Target, evidence.DeliveryMode, evidence.Environment)
		}
		file, exists := content[evidence.Path]
		if !exists || file.SHA256 != evidence.SHA256 {
			return fmt.Errorf("%w: evidence %s is not sealed by content_files", ErrChecksumMismatch, evidence.Path)
		}
		covered[evidence.Target+"\x00"+evidence.DeliveryMode+"\x00"+evidence.Environment] = struct{}{}
	}
	for _, target := range manifest.SupportedTargets {
		for _, mode := range manifest.SupportedDeliveryModes {
			for _, environment := range manifest.SupportedEnvironments {
				if _, ok := covered[target+"\x00"+mode+"\x00"+environment]; !ok {
					return fmt.Errorf("%w: no passed evidence for %s/%s/%s", ErrToolIncompatible, target, mode, environment)
				}
			}
		}
	}
	return nil
}

func toolKey(kind, id string) string                 { return kind + "\x00" + id }
func toolVersionKey(kind, id, version string) string { return toolKey(kind, id) + "\x00" + version }
