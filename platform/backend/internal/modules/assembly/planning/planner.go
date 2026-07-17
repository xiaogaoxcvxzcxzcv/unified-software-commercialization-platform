package planning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecatalog"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
	"platform.local/capability-platform/backend/internal/modules/product"
)

var (
	ErrPlannerUnavailable = errors.New("assembly planner is unavailable")
	ErrUnknownTool        = machinecatalog.ErrUnknownTool
	ErrBlueprintMismatch  = errors.New("product blueprint does not match the requested plan")
)

type Planner struct {
	catalog *machinecatalog.Catalog
}

func New(catalog *machinecatalog.Catalog) *Planner {
	return &Planner{catalog: catalog}
}

type blueprintDocument struct {
	SchemaVersion string `json:"schema_version"`
	BlueprintID   string `json:"blueprint_id"`
	Product       struct {
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"product"`
	Packages []struct {
		PackageID string `json:"package_id"`
		Version   string `json:"version"`
	} `json:"packages"`
	Applications []struct {
		ApplicationID string `json:"application_id"`
		Target        string `json:"target"`
		Channel       string `json:"channel"`
		Environment   string `json:"environment"`
		UI            struct {
			TemplateID   string `json:"template_id"`
			Version      string `json:"version"`
			DeliveryMode string `json:"delivery_mode"`
		} `json:"ui"`
		OutputPath string `json:"output_path"`
	} `json:"applications"`
	ProviderRefs []struct {
		Provider    string      `json:"provider"`
		Environment string      `json:"environment"`
		ConfigRef   string      `json:"config_ref"`
		SecretRefs  []secretRef `json:"secret_refs"`
	} `json:"provider_refs"`
	Extensions []struct {
		ExtensionID  string `json:"extension_id"`
		Version      string `json:"version"`
		ManifestPath string `json:"manifest_path"`
	} `json:"extensions"`
	Generator  toolSelection `json:"generator"`
	SDK        toolSelection `json:"sdk"`
	OutputRoot string        `json:"output_root"`
}

type toolSelection struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type secretRef struct {
	Provider    string `json:"provider"`
	Key         string `json:"key"`
	Environment string `json:"environment"`
}

type resolvedPackage struct {
	PackageID string `json:"package_id"`
	Version   string `json:"version"`
	Checksum  string `json:"checksum"`
}

type resolvedTemplate struct {
	TemplateID string `json:"template_id"`
	Version    string `json:"version"`
	Checksum   string `json:"checksum"`
}

type resolvedApplication struct {
	ApplicationID string           `json:"application_id"`
	Target        string           `json:"target"`
	Channel       string           `json:"channel"`
	Environment   string           `json:"environment"`
	DeliveryMode  string           `json:"delivery_mode"`
	OutputPath    string           `json:"output_path"`
	Template      resolvedTemplate `json:"template"`
}

type resolvedGenerator struct {
	GeneratorID string `json:"generator_id"`
	Version     string `json:"version"`
	Checksum    string `json:"checksum"`
}

type resolvedSDK struct {
	SDKID    string `json:"sdk_id"`
	Version  string `json:"version"`
	Checksum string `json:"checksum"`
}

type resolvedExtension struct {
	ExtensionID  string `json:"extension_id"`
	Version      string `json:"version"`
	ManifestPath string `json:"manifest_path"`
	Checksum     string `json:"checksum"`
}

type resolvedProvider struct {
	Provider    string      `json:"provider"`
	Environment string      `json:"environment"`
	ConfigRef   string      `json:"config_ref"`
	SecretRefs  []secretRef `json:"secret_refs"`
}

type expectedOutput struct {
	Path           string                           `json:"path"`
	Ownership      string                           `json:"ownership"`
	SourceID       string                           `json:"source_id"`
	SourceVersion  string                           `json:"source_version"`
	SourcePath     string                           `json:"source_path"`
	SourceSHA256   string                           `json:"source_sha256"`
	RenderStrategy string                           `json:"render_strategy"`
	ContentType    string                           `json:"content_type"`
	Merge          *machinecatalog.IntegrationMerge `json:"merge,omitempty"`
}

type resolvedDependency struct {
	RequiredBy      string `json:"required_by"`
	PackageID       string `json:"package_id"`
	VersionRange    string `json:"version_range"`
	ResolvedVersion string `json:"resolved_version"`
	Reason          string `json:"reason"`
}

type planDocument struct {
	SchemaVersion      string                   `json:"schema_version"`
	PlanID             string                   `json:"plan_id"`
	BlueprintID        string                   `json:"blueprint_id"`
	BlueprintVersion   int64                    `json:"blueprint_version"`
	Environment        string                   `json:"environment"`
	CatalogSnapshot    catalogSnapshotRef       `json:"catalog_snapshot"`
	Packages           []resolvedPackage        `json:"packages"`
	Applications       []resolvedApplication    `json:"applications"`
	Extensions         []resolvedExtension      `json:"extensions"`
	Generator          resolvedGenerator        `json:"generator"`
	SDKs               []resolvedSDK            `json:"sdks"`
	Capabilities       []product.CapabilityItem `json:"capabilities"`
	Dependencies       []resolvedDependency     `json:"dependencies"`
	Conflicts          []any                    `json:"conflicts"`
	Risks              []planRisk               `json:"risks"`
	Providers          []resolvedProvider       `json:"providers"`
	RequiredProviders  []string                 `json:"required_providers"`
	RequiredSecretRefs []secretRef              `json:"required_secret_refs"`
	ExpectedOutputs    []expectedOutput         `json:"expected_outputs"`
	Confirmation       planConfirmation         `json:"confirmation"`
	Executable         bool                     `json:"executable"`
	PlanChecksum       string                   `json:"plan_checksum"`
}

type catalogSnapshotRef struct {
	Revision string `json:"revision"`
	Scope    string `json:"scope"`
	Checksum string `json:"checksum"`
}

type planRisk struct {
	RiskID               string `json:"risk_id"`
	Level                string `json:"level"`
	Category             string `json:"category"`
	Summary              string `json:"summary"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
}

type planConfirmation struct {
	Required              bool     `json:"required"`
	BlockingConflictCount int      `json:"blocking_conflict_count"`
	RiskCount             int      `json:"risk_count"`
	Statements            []string `json:"statements"`
	SummaryChecksum       string   `json:"summary_checksum"`
}

func (p *Planner) BuildPlan(_ context.Context, blueprint core.Blueprint, environment string) (core.PlannedDocument, error) {
	if p == nil || p.catalog == nil {
		return core.PlannedDocument{}, ErrPlannerUnavailable
	}
	if environment != "development" && environment != "test" && environment != "staging" && environment != "production" {
		return core.PlannedDocument{}, ErrBlueprintMismatch
	}
	var input blueprintDocument
	if err := json.Unmarshal(blueprint.Document, &input); err != nil || input.BlueprintID != blueprint.BlueprintID || len(input.Applications) == 0 {
		return core.PlannedDocument{}, ErrBlueprintMismatch
	}
	if len(input.Extensions) != 0 {
		return core.PlannedDocument{}, fmt.Errorf("%w: extension manifests require a trusted extension catalog", ErrBlueprintMismatch)
	}
	requirements := make([]machinecatalog.Requirement, 0, len(input.Packages))
	for _, selected := range input.Packages {
		requirements = append(requirements, machinecatalog.Requirement{PackageID: selected.PackageID, VersionRange: selected.Version})
	}
	sort.Slice(requirements, func(i, j int) bool { return requirements[i].PackageID < requirements[j].PackageID })
	applications := append([]struct {
		ApplicationID string `json:"application_id"`
		Target        string `json:"target"`
		Channel       string `json:"channel"`
		Environment   string `json:"environment"`
		UI            struct {
			TemplateID   string `json:"template_id"`
			Version      string `json:"version"`
			DeliveryMode string `json:"delivery_mode"`
		} `json:"ui"`
		OutputPath string `json:"output_path"`
	}(nil), input.Applications...)
	sort.Slice(applications, func(i, j int) bool { return applications[i].ApplicationID < applications[j].ApplicationID })

	resolvedApplications := make([]resolvedApplication, 0, len(applications))
	expectedOutputs := make([]expectedOutput, 0)
	packageByID := make(map[string]machinecatalog.PackageManifest)
	applicationIDs := make(map[string]struct{}, len(applications))
	outputPaths := make([]string, 0, len(applications))
	var generator machinecatalog.ToolManifest
	var sdk machinecatalog.ToolManifest
	for _, application := range applications {
		if _, duplicate := applicationIDs[application.ApplicationID]; duplicate {
			return core.PlannedDocument{}, fmt.Errorf("%w: duplicate application_id %s", ErrBlueprintMismatch, application.ApplicationID)
		}
		applicationIDs[application.ApplicationID] = struct{}{}
		for _, existing := range outputPaths {
			if pathsOverlap(existing, application.OutputPath) {
				return core.PlannedDocument{}, fmt.Errorf("%w: overlapping application output paths %s and %s", ErrBlueprintMismatch, existing, application.OutputPath)
			}
		}
		outputPaths = append(outputPaths, application.OutputPath)
		if application.Environment != environment {
			return core.PlannedDocument{}, fmt.Errorf("%w: application %s environment is %s, requested %s", ErrBlueprintMismatch, application.ApplicationID, application.Environment, environment)
		}
		resolvedGeneratorTool, err := p.catalog.ResolveTool("generator", input.Generator.ID, input.Generator.Version, application.Target, application.UI.DeliveryMode, environment)
		if err != nil {
			return core.PlannedDocument{}, err
		}
		resolvedSDKTool, err := p.catalog.ResolveTool("sdk", input.SDK.ID, input.SDK.Version, application.Target, application.UI.DeliveryMode, environment)
		if err != nil {
			return core.PlannedDocument{}, err
		}
		generator = resolvedGeneratorTool
		sdk = resolvedSDKTool
		resolution, err := p.catalog.Resolve(machinecatalog.ResolveRequest{
			Packages: requirements, TemplateID: application.UI.TemplateID, TemplateRange: application.UI.Version,
			Target: application.Target, DeliveryMode: application.UI.DeliveryMode, Environment: environment,
		})
		if err != nil {
			return core.PlannedDocument{}, err
		}
		for _, manifest := range resolution.Packages {
			if previous, exists := packageByID[manifest.PackageID]; exists && previous.Version != manifest.Version {
				return core.PlannedDocument{}, machinecatalog.ErrVersionConflict
			}
			packageByID[manifest.PackageID] = manifest
			for _, output := range manifest.GeneratedOutputs {
				path := strings.TrimSuffix(input.OutputRoot, "/") + "/" + strings.Trim(application.OutputPath, "/") + "/" + strings.TrimPrefix(output.Path, "/")
				if err := machinecontract.ValidateSafeRelativePath(path); err != nil {
					return core.PlannedDocument{}, err
				}
				expectedOutputs = append(expectedOutputs, expectedOutput{
					Path: path, Ownership: output.Ownership, SourceID: manifest.PackageID, SourceVersion: manifest.Version,
					SourcePath: output.SourcePath, SourceSHA256: output.SourceSHA256, RenderStrategy: output.RenderStrategy,
					ContentType: output.ContentType, Merge: output.Merge,
				})
			}
		}
		entrypointFound := false
		for _, entrypoint := range resolution.Template.Entrypoints {
			if entrypoint.Target != application.Target || entrypoint.DeliveryMode != application.UI.DeliveryMode {
				continue
			}
			path := strings.TrimSuffix(input.OutputRoot, "/") + "/" + strings.Trim(application.OutputPath, "/") + "/" + strings.TrimPrefix(entrypoint.Path, "/")
			if err := machinecontract.ValidateSafeRelativePath(path); err != nil {
				return core.PlannedDocument{}, err
			}
			expectedOutputs = append(expectedOutputs, expectedOutput{
				Path: path, Ownership: entrypoint.Ownership, SourceID: resolution.Template.TemplateID, SourceVersion: resolution.Template.Version,
				SourcePath: entrypoint.SourcePath, SourceSHA256: entrypoint.SourceSHA256, RenderStrategy: entrypoint.RenderStrategy,
				ContentType: entrypoint.ContentType, Merge: entrypoint.Merge,
			})
			entrypointFound = true
		}
		if !entrypointFound {
			return core.PlannedDocument{}, machinecatalog.ErrEntrypointMismatch
		}
		resolvedApplications = append(resolvedApplications, resolvedApplication{
			ApplicationID: application.ApplicationID, Target: application.Target, Channel: application.Channel,
			Environment: environment, DeliveryMode: application.UI.DeliveryMode, OutputPath: application.OutputPath,
			Template: resolvedTemplate{TemplateID: resolution.Template.TemplateID, Version: resolution.Template.Version, Checksum: resolution.Template.ManifestSHA256},
		})
	}

	snapshot, err := p.catalog.Snapshot()
	if err != nil {
		return core.PlannedDocument{}, err
	}
	packageIDs := make([]string, 0, len(packageByID))
	for packageID := range packageByID {
		packageIDs = append(packageIDs, packageID)
	}
	sort.Strings(packageIDs)
	resolvedPackages := make([]resolvedPackage, 0, len(packageIDs))
	dependencies := make([]resolvedDependency, 0)
	capabilities := make([]product.CapabilityItem, 0)
	capabilityIDs := make(map[string]struct{})
	for _, packageID := range packageIDs {
		manifest := packageByID[packageID]
		resolvedPackages = append(resolvedPackages, resolvedPackage{PackageID: packageID, Version: manifest.Version, Checksum: manifest.ManifestSHA256})
		for _, dependency := range manifest.Dependencies {
			reason := strings.TrimSpace(dependency.Reason)
			if reason == "" {
				reason = "Required by " + manifest.PackageID
			}
			dependencies = append(dependencies, resolvedDependency{RequiredBy: manifest.PackageID, PackageID: dependency.PackageID, VersionRange: dependency.VersionRange, ResolvedVersion: packageByID[dependency.PackageID].Version, Reason: reason})
		}
		for _, capabilityID := range manifest.BackendCapabilities {
			if _, duplicate := capabilityIDs[capabilityID]; duplicate {
				return core.PlannedDocument{}, fmt.Errorf("%w: duplicate capability %s", ErrBlueprintMismatch, capabilityID)
			}
			capabilityIDs[capabilityID] = struct{}{}
			capabilities = append(capabilities, product.CapabilityItem{CapabilityID: capabilityID, Enabled: true, Policy: json.RawMessage(`{}`), SourcePackageID: manifest.PackageID, SourcePackageVersion: manifest.Version})
		}
	}
	sort.Slice(dependencies, func(i, j int) bool {
		if dependencies[i].RequiredBy == dependencies[j].RequiredBy {
			return dependencies[i].PackageID < dependencies[j].PackageID
		}
		return dependencies[i].RequiredBy < dependencies[j].RequiredBy
	})
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].CapabilityID < capabilities[j].CapabilityID })

	providedProviders := make(map[string]struct{})
	providedSecrets := make(map[string]secretRef)
	providerKeys := make(map[string]struct{})
	providers := make([]resolvedProvider, 0)
	for _, provider := range input.ProviderRefs {
		if provider.Environment != environment {
			continue
		}
		if provider.Provider == "" || provider.ConfigRef == "" {
			return core.PlannedDocument{}, ErrBlueprintMismatch
		}
		providerKey := provider.Provider + "\x00" + provider.Environment
		if _, duplicate := providerKeys[providerKey]; duplicate {
			return core.PlannedDocument{}, fmt.Errorf("%w: duplicate provider configuration %s/%s", ErrBlueprintMismatch, provider.Provider, provider.Environment)
		}
		providerKeys[providerKey] = struct{}{}
		providedProviders[provider.Provider] = struct{}{}
		resolvedSecrets := make([]secretRef, 0, len(provider.SecretRefs))
		for _, reference := range provider.SecretRefs {
			if reference.Provider != provider.Provider || reference.Environment != provider.Environment {
				return core.PlannedDocument{}, ErrBlueprintMismatch
			}
			key := reference.Provider + "\x00" + reference.Key + "\x00" + reference.Environment
			if _, duplicate := providedSecrets[key]; duplicate {
				return core.PlannedDocument{}, fmt.Errorf("%w: duplicate secret reference %s/%s", ErrBlueprintMismatch, reference.Provider, reference.Key)
			}
			providedSecrets[key] = reference
			resolvedSecrets = append(resolvedSecrets, reference)
		}
		sort.Slice(resolvedSecrets, func(i, j int) bool { return resolvedSecrets[i].Key < resolvedSecrets[j].Key })
		providers = append(providers, resolvedProvider{Provider: provider.Provider, Environment: provider.Environment, ConfigRef: provider.ConfigRef, SecretRefs: resolvedSecrets})
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Provider < providers[j].Provider })
	requiredProviderSet := make(map[string]struct{})
	requiredSecretSet := make(map[string]secretRef)
	for _, packageID := range packageIDs {
		manifest := packageByID[packageID]
		for _, providerID := range manifest.ProviderRequirements {
			if _, configured := providedProviders[providerID]; !configured {
				return core.PlannedDocument{}, fmt.Errorf("%w: package %s requires provider %s", ErrBlueprintMismatch, manifest.PackageID, providerID)
			}
			requiredProviderSet[providerID] = struct{}{}
		}
		for _, reference := range manifest.SecretRefs {
			if reference.Environment != environment {
				continue
			}
			key := reference.Provider + "\x00" + reference.Key + "\x00" + reference.Environment
			provided, configured := providedSecrets[key]
			if !configured {
				return core.PlannedDocument{}, fmt.Errorf("%w: package %s requires secret reference %s/%s", ErrBlueprintMismatch, manifest.PackageID, reference.Provider, reference.Key)
			}
			requiredSecretSet[key] = provided
		}
	}
	requiredProviders := make([]string, 0, len(requiredProviderSet))
	for providerID := range requiredProviderSet {
		requiredProviders = append(requiredProviders, providerID)
	}
	sort.Strings(requiredProviders)
	secretRefs := make([]secretRef, 0, len(requiredSecretSet))
	for _, reference := range requiredSecretSet {
		secretRefs = append(secretRefs, reference)
	}
	sort.Slice(secretRefs, func(i, j int) bool {
		if secretRefs[i].Provider == secretRefs[j].Provider {
			return secretRefs[i].Key < secretRefs[j].Key
		}
		return secretRefs[i].Provider < secretRefs[j].Provider
	})
	sort.Slice(expectedOutputs, func(i, j int) bool { return expectedOutputs[i].Path < expectedOutputs[j].Path })
	for index := range expectedOutputs {
		for other := index + 1; other < len(expectedOutputs); other++ {
			if pathsOverlap(expectedOutputs[index].Path, expectedOutputs[other].Path) {
				return core.PlannedDocument{}, fmt.Errorf("%w: output paths overlap: %s and %s", ErrBlueprintMismatch, expectedOutputs[index].Path, expectedOutputs[other].Path)
			}
		}
	}

	risks := []planRisk{{RiskID: "risk.generated-ownership", Level: "medium", Category: "generation", Summary: "Generated files are updated only when their locked baseline checksum matches.", RequiresConfirmation: true}}
	statements := []string{"Confirm product provisioning and generated file ownership for the locked plan."}
	confirmationDigest, err := core.ConfirmationSummaryChecksum(0, len(risks), statements)
	if err != nil {
		return core.PlannedDocument{}, err
	}
	seedDigest, err := digestJSON(struct {
		BlueprintSHA256 string `json:"blueprint_sha256"`
		Environment     string `json:"environment"`
		CatalogSHA256   string `json:"catalog_sha256"`
		GeneratorSHA256 string `json:"generator_sha256"`
		SDKSHa256       string `json:"sdk_sha256"`
	}{blueprint.ContentSHA256, environment, snapshot.SnapshotSHA256, generator.ManifestSHA256, sdk.ManifestSHA256})
	if err != nil {
		return core.PlannedDocument{}, err
	}
	planID := "plan_" + strings.TrimPrefix(seedDigest, "sha256:")[:24]
	document := planDocument{
		SchemaVersion: "1.0.0", PlanID: planID, BlueprintID: blueprint.BlueprintID, BlueprintVersion: blueprint.Revision,
		Environment: environment, CatalogSnapshot: catalogSnapshotRef{Revision: snapshot.Revision, Scope: snapshot.CatalogScope, Checksum: snapshot.SnapshotSHA256},
		Packages: resolvedPackages, Applications: resolvedApplications, Extensions: []resolvedExtension{},
		Generator: resolvedGenerator{GeneratorID: generator.ToolID, Version: generator.Version, Checksum: generator.ManifestSHA256},
		SDKs:      []resolvedSDK{{SDKID: sdk.ToolID, Version: sdk.Version, Checksum: sdk.ManifestSHA256}}, Capabilities: capabilities, Dependencies: dependencies,
		Conflicts: []any{}, Risks: risks, Providers: providers, RequiredProviders: requiredProviders, RequiredSecretRefs: secretRefs, ExpectedOutputs: expectedOutputs,
		Confirmation: planConfirmation{Required: true, BlockingConflictCount: 0, RiskCount: len(risks), Statements: statements, SummaryChecksum: confirmationDigest},
		Executable:   true, PlanChecksum: "sha256:" + strings.Repeat("0", 64),
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return core.PlannedDocument{}, err
	}
	checksum, err := machinecontract.DigestWithoutTopLevelField(raw, "plan_checksum")
	if err != nil {
		return core.PlannedDocument{}, err
	}
	document.PlanChecksum = checksum
	raw, err = json.Marshal(document)
	if err != nil {
		return core.PlannedDocument{}, err
	}
	return core.PlannedDocument{Document: raw, Capabilities: capabilities}, nil
}

func digestJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest, err := machinecontract.Digest(raw)
	if err != nil {
		return "", err
	}
	return "sha256:" + digest, nil
}

func pathsOverlap(first, second string) bool {
	first = strings.ToLower(strings.Trim(first, "/"))
	second = strings.ToLower(strings.Trim(second, "/"))
	return first == second || strings.HasPrefix(first, second+"/") || strings.HasPrefix(second, first+"/")
}
