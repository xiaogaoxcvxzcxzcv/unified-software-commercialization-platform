package machinecatalog

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

func LoadOrdinaryWithExtensions(packageRoot, templateRoot, extensionRoot string, contracts *machinecontract.Registry, permissions PermissionCatalog, blocks *BlockCatalog) (*Catalog, error) {
	return loadWithExtensions(packageRoot, templateRoot, extensionRoot, contracts, permissions, blocks, ordinaryView)
}

func LoadExperimentalWithExtensions(packageRoot, templateRoot, extensionRoot string, contracts *machinecontract.Registry, permissions PermissionCatalog, blocks *BlockCatalog) (*Catalog, error) {
	return loadWithExtensions(packageRoot, templateRoot, extensionRoot, contracts, permissions, blocks, experimentalView)
}

func loadWithExtensions(packageRoot, templateRoot, extensionRoot string, contracts *machinecontract.Registry, permissions PermissionCatalog, blocks *BlockCatalog, view catalogView) (*Catalog, error) {
	catalog, err := load(packageRoot, templateRoot, contracts, permissions, blocks, view)
	if err != nil {
		return nil, err
	}
	documents, err := discover(extensionRoot, "manifest.json")
	if err != nil {
		return nil, err
	}
	if err := catalog.addExtensions(documents); err != nil {
		return nil, err
	}
	return catalog, nil
}

func LoadOrdinaryWithToolsAndExtensions(packageRoot, templateRoot, generatorRoot, sdkRoot, extensionRoot string, contracts *machinecontract.Registry, permissions PermissionCatalog, blocks *BlockCatalog) (*Catalog, error) {
	return loadWithToolsAndExtensions(packageRoot, templateRoot, generatorRoot, sdkRoot, extensionRoot, contracts, permissions, blocks, ordinaryView)
}

func LoadExperimentalWithToolsAndExtensions(packageRoot, templateRoot, generatorRoot, sdkRoot, extensionRoot string, contracts *machinecontract.Registry, permissions PermissionCatalog, blocks *BlockCatalog) (*Catalog, error) {
	return loadWithToolsAndExtensions(packageRoot, templateRoot, generatorRoot, sdkRoot, extensionRoot, contracts, permissions, blocks, experimentalView)
}

func loadWithToolsAndExtensions(packageRoot, templateRoot, generatorRoot, sdkRoot, extensionRoot string, contracts *machinecontract.Registry, permissions PermissionCatalog, blocks *BlockCatalog, view catalogView) (*Catalog, error) {
	catalog, err := loadWithTools(packageRoot, templateRoot, generatorRoot, sdkRoot, contracts, permissions, blocks, view)
	if err != nil {
		return nil, err
	}
	documents, err := discover(extensionRoot, "manifest.json")
	if err != nil {
		return nil, err
	}
	if err := catalog.addExtensions(documents); err != nil {
		return nil, err
	}
	return catalog, nil
}

func (c *Catalog) addExtensions(documents []sourceDocument) error {
	for _, document := range documents {
		if err := c.contracts.Validate("extension-manifest", document.contents); err != nil {
			return err
		}
		var manifest ExtensionManifest
		if err := json.Unmarshal(document.contents, &manifest); err != nil {
			return err
		}
		if manifest.ExtensionID != document.identity || manifest.Version != document.version {
			return fmt.Errorf("%w: path=%s@%s manifest=%s@%s", ErrIdentityMismatch, document.identity, document.version, manifest.ExtensionID, manifest.Version)
		}
		if manifest.CatalogScope != c.view.visibility || manifest.Readiness != c.view.readiness {
			return fmt.Errorf("%w: extension %s@%s wants %s/%s, got %s/%s", ErrCatalogState, manifest.ExtensionID, manifest.Version, c.view.visibility, c.view.readiness, manifest.CatalogScope, manifest.Readiness)
		}
		if err := validateDocumentIntegrity(document, manifest.ManifestSHA256, manifest.ContentFiles, manifest.ContentTreeSHA256); err != nil {
			return fmt.Errorf("extension %s@%s: %w", manifest.ExtensionID, manifest.Version, err)
		}
		manifest.ManifestPath = canonicalExtensionManifestPath(manifest.ExtensionID, manifest.Version)
		if err := c.validateExtensionManifest(manifest); err != nil {
			return fmt.Errorf("extension %s@%s: %w", manifest.ExtensionID, manifest.Version, err)
		}
		for _, existing := range c.extensions[manifest.ExtensionID] {
			if existing.Version == manifest.Version {
				return fmt.Errorf("%w: %s@%s", ErrDuplicateExtensionVersion, manifest.ExtensionID, manifest.Version)
			}
		}
		c.extensions[manifest.ExtensionID] = append(c.extensions[manifest.ExtensionID], manifest)
		c.extensionSources[manifest.ExtensionID+"\x00"+manifest.Version] = document
	}
	for extensionID := range c.extensions {
		sort.Slice(c.extensions[extensionID], func(i, j int) bool {
			first, _ := semver.StrictNewVersion(c.extensions[extensionID][i].Version)
			second, _ := semver.StrictNewVersion(c.extensions[extensionID][j].Version)
			return first.GreaterThan(second)
		})
		for index := 1; index < len(c.extensions[extensionID]); index++ {
			first, _ := semver.StrictNewVersion(c.extensions[extensionID][index-1].Version)
			second, _ := semver.StrictNewVersion(c.extensions[extensionID][index].Version)
			if first.Equal(second) && c.extensions[extensionID][index-1].Version != c.extensions[extensionID][index].Version {
				return fmt.Errorf("%w: equal SemVer precedence %s and %s", ErrDuplicateExtensionVersion, c.extensions[extensionID][index-1].Version, c.extensions[extensionID][index].Version)
			}
		}
	}
	return nil
}

func (c *Catalog) validateExtensionManifest(manifest ExtensionManifest) error {
	if err := c.permissions.ValidateRequiredPermissions(manifest.RequiredPermissions); err != nil {
		return fmt.Errorf("required permissions: %w", err)
	}
	required := make(map[string]struct{}, len(manifest.RequiredPermissions))
	for _, permission := range manifest.RequiredPermissions {
		required[permission] = struct{}{}
	}
	owned := make(map[string]struct{}, len(manifest.OwnedPaths))
	content := make(map[string]struct{}, len(manifest.ContentFiles))
	for _, file := range manifest.ContentFiles {
		content[strings.ToLower(file.Path)] = struct{}{}
	}
	for _, ownedPath := range manifest.OwnedPaths {
		if err := machinecontract.ValidateSafeRelativePath(ownedPath); err != nil {
			return err
		}
		folded := strings.ToLower(ownedPath)
		if _, duplicate := owned[folded]; duplicate {
			return fmt.Errorf("%w: owned path is duplicated by case %s", ErrExtensionConflict, ownedPath)
		}
		if _, sealed := content[folded]; !sealed {
			return fmt.Errorf("%w: owned path %s is not sealed by content_files", ErrExtensionIncompatible, ownedPath)
		}
		owned[folded] = struct{}{}
	}
	checkEntry := func(entryPath, permission string) error {
		if err := machinecontract.ValidateSafeRelativePath(entryPath); err != nil {
			return err
		}
		if _, ok := owned[strings.ToLower(entryPath)]; !ok {
			return fmt.Errorf("%w: entry path %s is not declared in owned_paths", ErrExtensionIncompatible, entryPath)
		}
		if permission != "" {
			if _, ok := required[permission]; !ok {
				return fmt.Errorf("%w: entry permission %s is not in required_permissions", ErrExtensionIncompatible, permission)
			}
		}
		return nil
	}
	routes := make(map[string]ExtensionRoute, len(manifest.Routes))
	routePaths := make(map[string]struct{}, len(manifest.Routes))
	for _, route := range manifest.Routes {
		if !contains(manifest.SupportedTargets, route.Target) {
			return fmt.Errorf("%w: route target %s", ErrUnsupportedTarget, route.Target)
		}
		if err := checkEntry(route.EntryPath, route.RequiredPermission); err != nil {
			return err
		}
		if _, duplicate := routes[route.RouteID]; duplicate {
			return fmt.Errorf("%w: duplicate route id %s", ErrExtensionConflict, route.RouteID)
		}
		key := route.Target + "\x00" + strings.ToLower(route.Path)
		if _, duplicate := routePaths[key]; duplicate {
			return fmt.Errorf("%w: duplicate route path %s", ErrExtensionConflict, route.Path)
		}
		routes[route.RouteID], routePaths[key] = route, struct{}{}
	}
	for _, item := range manifest.NavigationItems {
		route, ok := routes[item.RouteID]
		if !ok || route.Target != item.Target {
			return fmt.Errorf("%w: navigation %s references incompatible route %s", ErrExtensionIncompatible, item.ItemID, item.RouteID)
		}
		if _, ok := required[item.RequiredPermission]; !ok {
			return fmt.Errorf("%w: navigation permission %s is not in required_permissions", ErrExtensionIncompatible, item.RequiredPermission)
		}
	}
	for _, slot := range manifest.Slots {
		if !contains(manifest.SupportedTargets, slot.Target) {
			return fmt.Errorf("%w: slot target %s", ErrUnsupportedTarget, slot.Target)
		}
		if err := checkEntry(slot.EntryPath, ""); err != nil {
			return err
		}
	}
	for _, item := range manifest.AdminItems {
		if err := checkEntry(item.EntryPath, item.RequiredPermission); err != nil {
			return err
		}
	}
	for _, table := range manifest.OwnedTables {
		if !strings.HasPrefix(table, manifest.DataNamespace+".") || strings.Count(table, ".") != 1 {
			return fmt.Errorf("%w: table %s is outside namespace %s", ErrExtensionIncompatible, table, manifest.DataNamespace)
		}
	}
	if err := validateExtensionSelection([]ExtensionManifest{manifest}); err != nil {
		return err
	}
	return nil
}

func canonicalExtensionManifestPath(extensionID, version string) string {
	return path.Join(extensionID, version, "manifest.json")
}
