package core

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type completionIdentity struct {
	AssemblyID        string
	LockID            string
	ManifestCreatedAt time.Time
	LockCreatedAt     time.Time
}

type lockedPackage struct {
	PackageID string `json:"package_id"`
	Version   string `json:"version"`
	Checksum  string `json:"checksum"`
}

type lockedTemplate struct {
	TemplateID string `json:"template_id"`
	Version    string `json:"version"`
	Checksum   string `json:"checksum"`
}

type lockedSDK struct {
	SDKID    string `json:"sdk_id"`
	Version  string `json:"version"`
	Checksum string `json:"checksum"`
}

type lockedTool struct {
	GeneratorID string `json:"generator_id"`
	Version     string `json:"version"`
	Checksum    string `json:"checksum"`
}

type lockedSecretRef struct {
	Provider    string `json:"provider"`
	Key         string `json:"key"`
	Environment string `json:"environment"`
}

type plannedOutput struct {
	Path           string     `json:"path"`
	Ownership      string     `json:"ownership"`
	SourceID       string     `json:"source_id"`
	SourceVersion  string     `json:"source_version"`
	SourcePath     string     `json:"source_path"`
	SourceSHA256   string     `json:"source_sha256"`
	RenderStrategy string     `json:"render_strategy"`
	ContentType    string     `json:"content_type"`
	Merge          *mergeSpec `json:"merge,omitempty"`
}

type manifestOutput struct {
	Path           string     `json:"path"`
	Ownership      string     `json:"ownership"`
	SHA256         string     `json:"sha256"`
	SourceID       string     `json:"source_id"`
	SourceVersion  string     `json:"source_version"`
	SourcePath     string     `json:"source_path"`
	SourceSHA256   string     `json:"source_sha256"`
	RenderStrategy string     `json:"render_strategy"`
	ContentType    string     `json:"content_type"`
	Merge          *mergeSpec `json:"merge,omitempty"`
}

type mergeSpec struct {
	Strategy      string `json:"strategy"`
	RegionID      string `json:"region_id"`
	CommentPrefix string `json:"comment_prefix"`
}

type lockDependency struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Checksum string `json:"checksum"`
}

type lockedFile struct {
	Path            string     `json:"path"`
	Ownership       string     `json:"ownership"`
	SHA256          string     `json:"sha256"`
	GeneratedSHA256 string     `json:"generated_sha256,omitempty"`
	SourceID        string     `json:"source_id,omitempty"`
	SourceVersion   string     `json:"source_version,omitempty"`
	SourcePath      string     `json:"source_path,omitempty"`
	SourceSHA256    string     `json:"source_sha256,omitempty"`
	RenderStrategy  string     `json:"render_strategy,omitempty"`
	ContentType     string     `json:"content_type,omitempty"`
	Merge           *mergeSpec `json:"merge,omitempty"`
	UpdatePolicy    string     `json:"update_policy"`
}

func validateCompletedArtifacts(plan Plan, blueprint Blueprint, run Run, manifestDocument, lockDocument json.RawMessage, manifestChecksum, lockChecksum string) (completionIdentity, error) {
	var planned struct {
		BlueprintID      string `json:"blueprint_id"`
		BlueprintVersion int64  `json:"blueprint_version"`
		CatalogSnapshot  struct {
			Checksum string `json:"checksum"`
		} `json:"catalog_snapshot"`
		Packages     []lockedPackage `json:"packages"`
		Applications []struct {
			ApplicationID string         `json:"application_id"`
			Template      lockedTemplate `json:"template"`
		} `json:"applications"`
		Generator          lockedTool        `json:"generator"`
		SDKs               []lockedSDK       `json:"sdks"`
		RequiredSecretRefs []lockedSecretRef `json:"required_secret_refs"`
		ExpectedOutputs    []plannedOutput   `json:"expected_outputs"`
	}
	var manifest struct {
		AssemblyID string `json:"assembly_id"`
		RunID      string `json:"run_id"`
		Product    struct {
			ProductID    string `json:"product_id"`
			Applications []struct {
				PlanApplicationID string `json:"plan_application_id"`
				ApplicationID     string `json:"application_id"`
			} `json:"applications"`
		} `json:"product"`
		Blueprint struct {
			BlueprintID string `json:"blueprint_id"`
			Version     int64  `json:"version"`
			Checksum    string `json:"checksum"`
		} `json:"blueprint"`
		CatalogChecksum  string            `json:"catalog_checksum"`
		Packages         []lockedPackage   `json:"packages"`
		Templates        []lockedTemplate  `json:"templates"`
		SDKs             []lockedSDK       `json:"sdks"`
		Outputs          []manifestOutput  `json:"outputs"`
		SecretRefs       []lockedSecretRef `json:"secret_refs"`
		CreatedAt        time.Time         `json:"created_at"`
		ManifestChecksum string            `json:"manifest_checksum"`
	}
	var lock struct {
		LockID                   string           `json:"lock_id"`
		AssemblyManifestChecksum string           `json:"assembly_manifest_checksum"`
		BlueprintChecksum        string           `json:"blueprint_checksum"`
		CatalogChecksum          string           `json:"catalog_checksum"`
		Generator                lockedTool       `json:"generator"`
		Packages                 []lockDependency `json:"packages"`
		Templates                []lockDependency `json:"templates"`
		SDKs                     []lockDependency `json:"sdks"`
		Files                    []lockedFile     `json:"files"`
		CreatedAt                time.Time        `json:"created_at"`
		LockChecksum             string           `json:"lock_checksum"`
	}
	if json.Unmarshal(plan.Document, &planned) != nil || json.Unmarshal(manifestDocument, &manifest) != nil || json.Unmarshal(lockDocument, &lock) != nil {
		return completionIdentity{}, ErrDocumentInvalid
	}
	if planned.BlueprintID != plan.BlueprintID || planned.BlueprintVersion != plan.BlueprintRevision || !digestsEqual(planned.CatalogSnapshot.Checksum, plan.CatalogSnapshotSHA256) || !digestsEqual(blueprint.ContentSHA256, plan.BlueprintSHA256) ||
		manifest.AssemblyID == "" || lock.LockID == "" || manifest.RunID != run.RunID || manifest.Product.ProductID != run.ProductID ||
		manifest.Blueprint.BlueprintID != plan.BlueprintID || manifest.Blueprint.Version != plan.BlueprintRevision ||
		!digestsEqual(manifest.Blueprint.Checksum, blueprint.ContentSHA256) || !digestsEqual(manifest.CatalogChecksum, plan.CatalogSnapshotSHA256) ||
		!digestsEqual(lock.AssemblyManifestChecksum, manifestChecksum) || !digestsEqual(lock.BlueprintChecksum, blueprint.ContentSHA256) ||
		!digestsEqual(lock.CatalogChecksum, plan.CatalogSnapshotSHA256) || !digestsEqual(manifest.ManifestChecksum, manifestChecksum) ||
		!digestsEqual(lock.LockChecksum, lockChecksum) || manifest.CreatedAt.Before(run.CreatedAt) || lock.CreatedAt.Before(run.CreatedAt) {
		return completionIdentity{}, ErrDocumentInvalid
	}
	applicationIDs := make([]string, 0, len(planned.Applications))
	templateCandidates := make([]lockedTemplate, 0, len(planned.Applications))
	for _, application := range planned.Applications {
		applicationIDs = append(applicationIDs, application.ApplicationID)
		templateCandidates = append(templateCandidates, application.Template)
	}
	templates, ok := uniqueTemplates(templateCandidates)
	if !ok {
		return completionIdentity{}, ErrDocumentInvalid
	}
	manifestApplicationIDs := make([]string, 0, len(manifest.Product.Applications))
	runtimeApplicationIDs := make([]string, 0, len(manifest.Product.Applications))
	for _, application := range manifest.Product.Applications {
		manifestApplicationIDs = append(manifestApplicationIDs, application.PlanApplicationID)
		runtimeApplicationIDs = append(runtimeApplicationIDs, application.ApplicationID)
	}
	if !stringSetEqual(applicationIDs, manifestApplicationIDs) || !uniqueStrings(runtimeApplicationIDs) ||
		!semanticEqualPackages(planned.Packages, manifest.Packages) ||
		!semanticEqualTemplates(templates, manifest.Templates) ||
		!semanticEqualSDKs(planned.SDKs, manifest.SDKs) ||
		!semanticEqualSecrets(planned.RequiredSecretRefs, manifest.SecretRefs) ||
		!semanticEqualTool(planned.Generator, lock.Generator) ||
		!semanticEqualLockDependencies(planned.Packages, lock.Packages) ||
		!semanticEqualTemplateLocks(templates, lock.Templates) ||
		!semanticEqualSDKLocks(planned.SDKs, lock.SDKs) ||
		!validateOutputClosure(planned.ExpectedOutputs, manifest.Outputs, lock.Files) {
		return completionIdentity{}, ErrDocumentInvalid
	}
	return completionIdentity{AssemblyID: manifest.AssemblyID, LockID: lock.LockID, ManifestCreatedAt: manifest.CreatedAt, LockCreatedAt: lock.CreatedAt}, nil
}

func uniqueStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return len(values) != 0
}

func uniqueTemplates(items []lockedTemplate) ([]lockedTemplate, bool) {
	byID := make(map[string]lockedTemplate, len(items))
	for _, item := range items {
		if existing, found := byID[item.TemplateID]; found && existing != item {
			return nil, false
		}
		byID[item.TemplateID] = item
	}
	result := make([]lockedTemplate, 0, len(byID))
	for _, item := range byID {
		result = append(result, item)
	}
	return result, true
}

func semanticEqualPackages(left, right []lockedPackage) bool {
	return semanticEqual(left, right, func(value lockedPackage) string { return value.PackageID })
}

func semanticEqualTemplates(left, right []lockedTemplate) bool {
	return semanticEqual(left, right, func(value lockedTemplate) string { return value.TemplateID })
}

func semanticEqualSDKs(left, right []lockedSDK) bool {
	return semanticEqual(left, right, func(value lockedSDK) string { return value.SDKID })
}

func semanticEqualSecrets(left, right []lockedSecretRef) bool {
	return semanticEqual(left, right, func(value lockedSecretRef) string {
		return value.Provider + "\x00" + value.Key + "\x00" + value.Environment
	})
}

func semanticEqualTool(left, right lockedTool) bool {
	return left == right
}

func semanticEqualLockDependencies(planned []lockedPackage, locked []lockDependency) bool {
	converted := make([]lockDependency, 0, len(planned))
	for _, item := range planned {
		converted = append(converted, lockDependency{ID: item.PackageID, Version: item.Version, Checksum: item.Checksum})
	}
	return semanticEqual(converted, locked, func(value lockDependency) string { return value.ID })
}

func semanticEqualTemplateLocks(planned []lockedTemplate, locked []lockDependency) bool {
	converted := make([]lockDependency, 0, len(planned))
	for _, item := range planned {
		converted = append(converted, lockDependency{ID: item.TemplateID, Version: item.Version, Checksum: item.Checksum})
	}
	return semanticEqual(converted, locked, func(value lockDependency) string { return value.ID })
}

func semanticEqualSDKLocks(planned []lockedSDK, locked []lockDependency) bool {
	converted := make([]lockDependency, 0, len(planned))
	for _, item := range planned {
		converted = append(converted, lockDependency{ID: item.SDKID, Version: item.Version, Checksum: item.Checksum})
	}
	return semanticEqual(converted, locked, func(value lockDependency) string { return value.ID })
}

func validateOutputClosure(planned []plannedOutput, manifest []manifestOutput, files []lockedFile) bool {
	if !uniqueNonOverlappingPaths(planned, func(value plannedOutput) string { return value.Path }) ||
		!uniqueNonOverlappingPaths(manifest, func(value manifestOutput) string { return value.Path }) ||
		!uniqueNonOverlappingPaths(files, func(value lockedFile) string { return value.Path }) || len(planned) != len(manifest) {
		return false
	}
	plannedByPath := make(map[string]plannedOutput, len(planned))
	for _, output := range planned {
		plannedByPath[normalizedPath(output.Path)] = output
	}
	manifestByPath := make(map[string]manifestOutput, len(manifest))
	for _, output := range manifest {
		plannedOutput, exists := plannedByPath[normalizedPath(output.Path)]
		if !exists || plannedOutput.Path != output.Path || plannedOutput.Ownership != output.Ownership || !outputSourceEqual(plannedOutput, output) {
			return false
		}
		manifestByPath[normalizedPath(output.Path)] = output
	}
	managedFiles := 0
	for _, file := range files {
		if file.Ownership != "generated" && file.Ownership != "integration" {
			continue
		}
		managedFiles++
		output, exists := manifestByPath[normalizedPath(file.Path)]
		if !exists || output.Path != file.Path || output.Ownership != file.Ownership || !digestsEqual(output.SHA256, file.SHA256) || !lockSourceEqual(output, file) {
			return false
		}
		if file.Ownership == "generated" && (file.UpdatePolicy != "replace_generated" || !digestsEqual(file.GeneratedSHA256, file.SHA256)) {
			return false
		}
		if file.Ownership == "integration" && (file.UpdatePolicy != "merge_integration" || file.Merge == nil || !digestPattern.MatchString(file.GeneratedSHA256)) {
			return false
		}
	}
	return managedFiles == len(manifest)
}

func outputSourceEqual(planned plannedOutput, actual manifestOutput) bool {
	return planned.SourceID == actual.SourceID && planned.SourceVersion == actual.SourceVersion && planned.SourcePath == actual.SourcePath &&
		digestsEqual(planned.SourceSHA256, actual.SourceSHA256) && planned.RenderStrategy == actual.RenderStrategy &&
		planned.ContentType == actual.ContentType && mergeEqual(planned.Merge, actual.Merge)
}

func lockSourceEqual(output manifestOutput, file lockedFile) bool {
	return output.SourceID == file.SourceID && output.SourceVersion == file.SourceVersion && output.SourcePath == file.SourcePath &&
		digestsEqual(output.SourceSHA256, file.SourceSHA256) && output.RenderStrategy == file.RenderStrategy &&
		output.ContentType == file.ContentType && mergeEqual(output.Merge, file.Merge)
}

func mergeEqual(left, right *mergeSpec) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func stringSetEqual(left, right []string) bool {
	return semanticEqual(left, right, func(value string) string { return value })
}

func semanticEqual[T any](left, right []T, key func(T) string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy, rightCopy := append([]T(nil), left...), append([]T(nil), right...)
	sort.Slice(leftCopy, func(i, j int) bool { return key(leftCopy[i]) < key(leftCopy[j]) })
	sort.Slice(rightCopy, func(i, j int) bool { return key(rightCopy[i]) < key(rightCopy[j]) })
	for index := range leftCopy {
		if index > 0 && strings.EqualFold(key(leftCopy[index-1]), key(leftCopy[index])) {
			return false
		}
	}
	leftRaw, leftErr := json.Marshal(leftCopy)
	rightRaw, rightErr := json.Marshal(rightCopy)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftDigest, leftErr := machinecontract.Digest(leftRaw)
	rightDigest, rightErr := machinecontract.Digest(rightRaw)
	return leftErr == nil && rightErr == nil && digestsEqual(leftDigest, rightDigest)
}

func uniqueNonOverlappingPaths[T any](items []T, path func(T) string) bool {
	values := make([]string, 0, len(items))
	for _, item := range items {
		values = append(values, path(item))
	}
	sort.Slice(values, func(i, j int) bool { return normalizedPath(values[i]) < normalizedPath(values[j]) })
	for index := range values {
		if index > 0 && pathsOverlap(values[index-1], values[index]) {
			return false
		}
	}
	return true
}

func normalizedPath(value string) string {
	return strings.ToLower(strings.Trim(strings.ReplaceAll(value, "\\", "/"), "/"))
}

func pathsOverlap(left, right string) bool {
	left, right = normalizedPath(left), normalizedPath(right)
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}
