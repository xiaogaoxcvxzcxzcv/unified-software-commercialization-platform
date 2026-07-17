package machinecatalog

import "sort"

type CatalogOptions struct {
	CatalogScope    string
	CatalogRevision string
	Target          string
	DeliveryMode    string
	Environment     string
	Packages        []PackageOption
	Templates       []TemplateOption
	Extensions      []ExtensionOption
	Generators      []ToolOption
	SDKs            []ToolOption
}

type PackageOption struct {
	PackageID              string
	Version                string
	Name                   string
	UserValue              string
	Dependencies           []Requirement
	Conflicts              []Requirement
	CompatibleTemplateRefs []VersionRef
}

type VersionRef struct {
	ID      string
	Version string
}

type TemplateOption struct {
	TemplateID      string
	Version         string
	Name            string
	SupportedBlocks []string
}

type ToolOption struct {
	ID      string
	Version string
	Name    string
}

type ExtensionOption struct {
	ExtensionID  string
	Version      string
	ProductCode  string
	ManifestPath string
}

func (c *Catalog) Options(target, deliveryMode, environment string) (CatalogOptions, error) {
	if c == nil {
		return CatalogOptions{}, ErrCatalogState
	}
	if !contains([]string{"web", "desktop_webview", "h5", "wechat_miniprogram", "mobile_app"}, target) ||
		!contains([]string{"hosted", "package", "generated_source"}, deliveryMode) ||
		!contains([]string{"development", "test", "staging", "production"}, environment) {
		return CatalogOptions{}, ErrCatalogState
	}
	snapshot, err := c.Snapshot()
	if err != nil {
		return CatalogOptions{}, err
	}
	result := CatalogOptions{
		CatalogScope: c.view.visibility, CatalogRevision: snapshot.Revision,
		Target: target, DeliveryMode: deliveryMode, Environment: environment,
		Packages: []PackageOption{}, Templates: []TemplateOption{}, Extensions: []ExtensionOption{}, Generators: []ToolOption{}, SDKs: []ToolOption{},
	}

	templates := matchingTemplates(c.templates, target, deliveryMode, environment)
	for _, template := range templates {
		result.Templates = append(result.Templates, TemplateOption{
			TemplateID: template.TemplateID, Version: template.Version, Name: template.Name,
			SupportedBlocks: stableStrings(template.SupportedBlocks),
		})
	}
	for _, versions := range c.packages {
		for _, manifest := range versions {
			request := ResolveRequest{Target: target, DeliveryMode: deliveryMode, Environment: environment}
			if validateSelectionAvailability(manifest.PackageID+"@"+manifest.Version, manifest.Availability, request) != nil {
				continue
			}
			compatible := make([]VersionRef, 0)
			for _, template := range templates {
				_, err := c.Resolve(ResolveRequest{
					Packages:   []Requirement{{PackageID: manifest.PackageID, VersionRange: "=" + manifest.Version}},
					TemplateID: template.TemplateID, TemplateRange: "=" + template.Version,
					Target: target, DeliveryMode: deliveryMode, Environment: environment,
				})
				if err == nil {
					compatible = append(compatible, VersionRef{ID: template.TemplateID, Version: template.Version})
				}
			}
			if len(compatible) == 0 {
				continue
			}
			result.Packages = append(result.Packages, PackageOption{
				PackageID: manifest.PackageID, Version: manifest.Version, Name: manifest.Name, UserValue: manifest.UserValue,
				Dependencies: stableRequirements(manifest.Dependencies), Conflicts: stableRequirements(manifest.Conflicts),
				CompatibleTemplateRefs: compatible,
			})
		}
	}
	sort.Slice(result.Packages, func(i, j int) bool {
		if result.Packages[i].PackageID == result.Packages[j].PackageID {
			return result.Packages[i].Version < result.Packages[j].Version
		}
		return result.Packages[i].PackageID < result.Packages[j].PackageID
	})
	result.Generators = c.matchingTools("generator", target, deliveryMode, environment)
	result.SDKs = c.matchingTools("sdk", target, deliveryMode, environment)
	for _, versions := range c.extensions {
		for _, manifest := range versions {
			if contains(manifest.SupportedTargets, target) && contains(manifest.SupportedDeliveryModes, deliveryMode) && contains(manifest.SupportedEnvironments, environment) {
				result.Extensions = append(result.Extensions, ExtensionOption{ExtensionID: manifest.ExtensionID, Version: manifest.Version, ProductCode: manifest.ProductCode, ManifestPath: manifest.ManifestPath})
			}
		}
	}
	sort.Slice(result.Extensions, func(i, j int) bool {
		if result.Extensions[i].ExtensionID == result.Extensions[j].ExtensionID {
			return result.Extensions[i].Version < result.Extensions[j].Version
		}
		return result.Extensions[i].ExtensionID < result.Extensions[j].ExtensionID
	})
	return result, nil
}

func matchingTemplates(values map[string][]TemplateManifest, target, deliveryMode, environment string) []TemplateManifest {
	result := make([]TemplateManifest, 0)
	request := ResolveRequest{Target: target, DeliveryMode: deliveryMode, Environment: environment}
	for _, versions := range values {
		for _, manifest := range versions {
			if validateSelectionAvailability(manifest.TemplateID+"@"+manifest.Version, manifest.Availability, request) == nil && hasEntrypoint(manifest, target, deliveryMode) {
				result = append(result, manifest)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TemplateID == result[j].TemplateID {
			return result[i].Version < result[j].Version
		}
		return result[i].TemplateID < result[j].TemplateID
	})
	return result
}

func (c *Catalog) matchingTools(kind, target, deliveryMode, environment string) []ToolOption {
	result := make([]ToolOption, 0)
	for key, versions := range c.tools {
		if len(key) <= len(kind) || key[:len(kind)] != kind || key[len(kind)] != '\x00' {
			continue
		}
		for _, manifest := range versions {
			if contains(manifest.SupportedTargets, target) && contains(manifest.SupportedDeliveryModes, deliveryMode) && contains(manifest.SupportedEnvironments, environment) {
				result = append(result, ToolOption{ID: manifest.ToolID, Version: manifest.Version, Name: manifest.Name})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ID == result[j].ID {
			return result[i].Version < result[j].Version
		}
		return result[i].ID < result[j].ID
	})
	return result
}
