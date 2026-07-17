package machinecatalog

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type catalogView struct {
	visibility string
	readiness  string
}

var (
	ordinaryView     = catalogView{visibility: "ordinary", readiness: "available"}
	experimentalView = catalogView{visibility: "experimental", readiness: "verified"}
)

type Catalog struct {
	packages         map[string][]PackageManifest
	templates        map[string][]TemplateManifest
	tools            map[string][]ToolManifest
	extensions       map[string][]ExtensionManifest
	packageSources   map[string]sourceDocument
	templateSources  map[string]sourceDocument
	toolSources      map[string]sourceDocument
	extensionSources map[string]sourceDocument
	contracts        *machinecontract.Registry
	permissions      PermissionCatalog
	blocks           *BlockCatalog
	view             catalogView
}

type sourceDocument struct {
	contents     []byte
	identity     string
	version      string
	versionRoot  string
	manifestName string
}

func LoadOrdinary(packageRoot, templateRoot string, contracts *machinecontract.Registry, permissions PermissionCatalog, blocks *BlockCatalog) (*Catalog, error) {
	return load(packageRoot, templateRoot, contracts, permissions, blocks, ordinaryView)
}

// LoadExperimental is deliberately separate from LoadOrdinary so a frontend
// request cannot turn on experimental entries by sending a catalog flag.
func LoadExperimental(packageRoot, templateRoot string, contracts *machinecontract.Registry, permissions PermissionCatalog, blocks *BlockCatalog) (*Catalog, error) {
	return load(packageRoot, templateRoot, contracts, permissions, blocks, experimentalView)
}

func load(packageRoot, templateRoot string, contracts *machinecontract.Registry, permissions PermissionCatalog, blocks *BlockCatalog, view catalogView) (*Catalog, error) {
	if contracts == nil || permissions == nil || blocks == nil {
		return nil, fmt.Errorf("machine catalog dependencies are required")
	}
	packageDocuments, err := discover(packageRoot, "manifest.json")
	if err != nil {
		return nil, err
	}
	templateDocuments, err := discover(templateRoot, "template.json")
	if err != nil {
		return nil, err
	}
	return build(packageDocuments, templateDocuments, contracts, permissions, blocks, view)
}

func discover(root, manifestName string) ([]sourceDocument, error) {
	entries, err := os.ReadDir(root)
	if errorsIsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read catalog root %s: %w", root, err)
	}
	documents := make([]sourceDocument, 0)
	for _, identityEntry := range entries {
		if identityEntry.Type()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: symbolic links are not allowed: %s", ErrInvalidLayout, filepath.Join(root, identityEntry.Name()))
		}
		if !identityEntry.IsDir() {
			if strings.EqualFold(identityEntry.Name(), "README.md") {
				continue
			}
			return nil, fmt.Errorf("%w: unexpected file %s", ErrInvalidLayout, filepath.Join(root, identityEntry.Name()))
		}
		identity := identityEntry.Name()
		identityRoot := filepath.Join(root, identity)
		versions, err := os.ReadDir(identityRoot)
		if err != nil {
			return nil, err
		}
		for _, versionEntry := range versions {
			if versionEntry.Type()&fs.ModeSymlink != 0 {
				return nil, fmt.Errorf("%w: symbolic links are not allowed: %s", ErrInvalidLayout, filepath.Join(identityRoot, versionEntry.Name()))
			}
			if !versionEntry.IsDir() {
				return nil, fmt.Errorf("%w: version must be a directory: %s", ErrInvalidLayout, filepath.Join(identityRoot, versionEntry.Name()))
			}
			if _, err := semver.StrictNewVersion(versionEntry.Name()); err != nil {
				return nil, fmt.Errorf("%w: invalid version directory %q", ErrInvalidLayout, versionEntry.Name())
			}
			versionRoot := filepath.Join(identityRoot, versionEntry.Name())
			manifestPath := filepath.Join(versionRoot, manifestName)
			contents, err := os.ReadFile(manifestPath)
			if err != nil {
				return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidLayout, manifestPath, err)
			}
			documents = append(documents, sourceDocument{contents: contents, identity: identity, version: versionEntry.Name(), versionRoot: versionRoot, manifestName: manifestName})
		}
	}
	sort.Slice(documents, func(i, j int) bool {
		if documents[i].identity == documents[j].identity {
			return documents[i].version < documents[j].version
		}
		return documents[i].identity < documents[j].identity
	})
	return documents, nil
}

func build(packageDocuments, templateDocuments []sourceDocument, contracts *machinecontract.Registry, permissions PermissionCatalog, blocks *BlockCatalog, view catalogView) (*Catalog, error) {
	catalog := &Catalog{
		packages: make(map[string][]PackageManifest), templates: make(map[string][]TemplateManifest),
		tools:          make(map[string][]ToolManifest),
		extensions:     make(map[string][]ExtensionManifest),
		packageSources: make(map[string]sourceDocument), templateSources: make(map[string]sourceDocument),
		toolSources:      make(map[string]sourceDocument),
		extensionSources: make(map[string]sourceDocument),
		contracts:        contracts, permissions: permissions, blocks: blocks, view: view,
	}
	for _, document := range packageDocuments {
		if err := contracts.Validate("package-manifest", document.contents); err != nil {
			return nil, err
		}
		var manifest PackageManifest
		if err := json.Unmarshal(document.contents, &manifest); err != nil {
			return nil, err
		}
		if manifest.PackageID != document.identity || manifest.Version != document.version {
			return nil, fmt.Errorf("%w: path=%s@%s manifest=%s@%s", ErrIdentityMismatch, document.identity, document.version, manifest.PackageID, manifest.Version)
		}
		if err := validateDocumentIntegrity(document, manifest.ManifestSHA256, manifest.ContentFiles, manifest.ContentTreeSHA256); err != nil {
			return nil, fmt.Errorf("package %s@%s: %w", manifest.PackageID, manifest.Version, err)
		}
		if err := validatePackageReferences(manifest); err != nil {
			return nil, fmt.Errorf("package %s@%s: %w", manifest.PackageID, manifest.Version, err)
		}
		if err := validateAvailability(manifest.Availability, manifest.SupportedTargets, manifest.SupportedDeliveryModes, view); err != nil {
			return nil, fmt.Errorf("package %s@%s: %w", manifest.PackageID, manifest.Version, err)
		}
		if err := permissions.ValidateRequiredPermissions(manifest.RequiredPermissions); err != nil {
			return nil, fmt.Errorf("package %s@%s permissions: %w", manifest.PackageID, manifest.Version, err)
		}
		if err := blocks.Validate(manifest.AdminBlocks, "admin"); err != nil {
			return nil, fmt.Errorf("package %s@%s: %w", manifest.PackageID, manifest.Version, err)
		}
		if err := blocks.Validate(manifest.ClientBlocks, "client"); err != nil {
			return nil, fmt.Errorf("package %s@%s: %w", manifest.PackageID, manifest.Version, err)
		}
		for _, existing := range catalog.packages[manifest.PackageID] {
			if existing.Version == manifest.Version {
				return nil, fmt.Errorf("%w: %s@%s", ErrDuplicatePackageVersion, manifest.PackageID, manifest.Version)
			}
		}
		catalog.packages[manifest.PackageID] = append(catalog.packages[manifest.PackageID], manifest)
		catalog.packageSources[manifest.PackageID+"\x00"+manifest.Version] = document
	}
	for _, document := range templateDocuments {
		if err := contracts.Validate("ui-template-manifest", document.contents); err != nil {
			return nil, err
		}
		var manifest TemplateManifest
		if err := json.Unmarshal(document.contents, &manifest); err != nil {
			return nil, err
		}
		if manifest.TemplateID != document.identity || manifest.Version != document.version {
			return nil, fmt.Errorf("%w: path=%s@%s manifest=%s@%s", ErrIdentityMismatch, document.identity, document.version, manifest.TemplateID, manifest.Version)
		}
		if err := validateDocumentIntegrity(document, manifest.ManifestSHA256, manifest.ContentFiles, manifest.ContentTreeSHA256); err != nil {
			return nil, fmt.Errorf("template %s@%s: %w", manifest.TemplateID, manifest.Version, err)
		}
		if err := validateAvailability(manifest.Availability, manifest.SupportedTargets, manifest.SupportedDeliveryModes, view); err != nil {
			return nil, fmt.Errorf("template %s@%s: %w", manifest.TemplateID, manifest.Version, err)
		}
		if err := blocks.Validate(manifest.SupportedBlocks, "client"); err != nil {
			return nil, fmt.Errorf("template %s@%s: %w", manifest.TemplateID, manifest.Version, err)
		}
		if err := validateTemplateEntrypoints(manifest); err != nil {
			return nil, fmt.Errorf("template %s@%s: %w", manifest.TemplateID, manifest.Version, err)
		}
		if err := validateTemplateContentReferences(manifest); err != nil {
			return nil, fmt.Errorf("template %s@%s: %w", manifest.TemplateID, manifest.Version, err)
		}
		for _, existing := range catalog.templates[manifest.TemplateID] {
			if existing.Version == manifest.Version {
				return nil, fmt.Errorf("%w: %s@%s", ErrDuplicateTemplateVersion, manifest.TemplateID, manifest.Version)
			}
		}
		catalog.templates[manifest.TemplateID] = append(catalog.templates[manifest.TemplateID], manifest)
		catalog.templateSources[manifest.TemplateID+"\x00"+manifest.Version] = document
	}
	for packageID := range catalog.packages {
		sortPackages(catalog.packages[packageID])
		if err := rejectAmbiguousPackagePrecedence(catalog.packages[packageID]); err != nil {
			return nil, err
		}
	}
	for templateID := range catalog.templates {
		sortTemplates(catalog.templates[templateID])
		if err := rejectAmbiguousTemplatePrecedence(catalog.templates[templateID]); err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

func rejectAmbiguousPackagePrecedence(manifests []PackageManifest) error {
	for index := 1; index < len(manifests); index++ {
		first, _ := semver.StrictNewVersion(manifests[index-1].Version)
		second, _ := semver.StrictNewVersion(manifests[index].Version)
		if first.Equal(second) && manifests[index-1].Version != manifests[index].Version {
			return fmt.Errorf("%w: equal SemVer precedence %s and %s", ErrDuplicatePackageVersion, manifests[index-1].Version, manifests[index].Version)
		}
	}
	return nil
}

func rejectAmbiguousTemplatePrecedence(manifests []TemplateManifest) error {
	for index := 1; index < len(manifests); index++ {
		first, _ := semver.StrictNewVersion(manifests[index-1].Version)
		second, _ := semver.StrictNewVersion(manifests[index].Version)
		if first.Equal(second) && manifests[index-1].Version != manifests[index].Version {
			return fmt.Errorf("%w: equal SemVer precedence %s and %s", ErrDuplicateTemplateVersion, manifests[index-1].Version, manifests[index].Version)
		}
	}
	return nil
}

func validateAvailability(entries []Availability, targets, modes []string, view catalogView) error {
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.Visibility != view.visibility || entry.Readiness != view.readiness {
			return fmt.Errorf("%w: want %s/%s, got %s/%s", ErrCatalogState, view.visibility, view.readiness, entry.Visibility, entry.Readiness)
		}
		if !contains(targets, entry.Target) {
			return fmt.Errorf("%w: availability target %s", ErrUnsupportedTarget, entry.Target)
		}
		if !contains(modes, entry.DeliveryMode) {
			return fmt.Errorf("%w: availability mode %s", ErrUnsupportedDeliveryMode, entry.DeliveryMode)
		}
		for _, environment := range entry.Environments {
			key := entry.Target + "\x00" + entry.DeliveryMode + "\x00" + environment
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate availability combination %s/%s/%s", entry.Target, entry.DeliveryMode, environment)
			}
			seen[key] = struct{}{}
		}
	}
	return nil
}

func validateTemplateEntrypoints(manifest TemplateManifest) error {
	content := make(map[string]ContentFile, len(manifest.ContentFiles))
	for _, file := range manifest.ContentFiles {
		content[file.Path] = file
	}
	seen := make(map[string]struct{}, len(manifest.Entrypoints))
	for _, entrypoint := range manifest.Entrypoints {
		if !contains(manifest.SupportedTargets, entrypoint.Target) {
			return fmt.Errorf("%w: entrypoint target %s", ErrUnsupportedTarget, entrypoint.Target)
		}
		if !contains(manifest.SupportedDeliveryModes, entrypoint.DeliveryMode) {
			return fmt.Errorf("%w: entrypoint mode %s", ErrUnsupportedDeliveryMode, entrypoint.DeliveryMode)
		}
		if err := machinecontract.ValidateSafeRelativePath(entrypoint.Path); err != nil {
			return err
		}
		if err := machinecontract.ValidateSafeRelativePath(entrypoint.SourcePath); err != nil {
			return err
		}
		file, exists := content[entrypoint.SourcePath]
		if !exists || file.SHA256 != entrypoint.SourceSHA256 || !pathWithin(entrypoint.SourcePath, manifest.SourceRoot) {
			return fmt.Errorf("%w: source %s is not locked by content_files/source_root", ErrEntrypointMismatch, entrypoint.SourcePath)
		}
		key := entrypoint.Target + "\x00" + entrypoint.DeliveryMode + "\x00" + strings.ToLower(entrypoint.Path)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: duplicate %s/%s/%s", ErrEntrypointMismatch, entrypoint.Target, entrypoint.DeliveryMode, entrypoint.Path)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validatePackageReferences(manifest PackageManifest) error {
	contentPaths := make(map[string]ContentFile, len(manifest.ContentFiles))
	for _, file := range manifest.ContentFiles {
		contentPaths[file.Path] = file
	}
	if _, exists := contentPaths[manifest.ConfigSchemaPath]; !exists {
		return fmt.Errorf("config_schema_path %q is not listed in content_files", manifest.ConfigSchemaPath)
	}
	providers := make(map[string]struct{}, len(manifest.ProviderRequirements))
	for _, provider := range manifest.ProviderRequirements {
		providers[provider] = struct{}{}
	}
	for _, secret := range manifest.SecretRefs {
		if _, exists := providers[secret.Provider]; !exists {
			return fmt.Errorf("secret_refs provider %q is not declared in provider_requirements", secret.Provider)
		}
	}
	seenOutputs := make(map[string]struct{}, len(manifest.GeneratedOutputs))
	for _, output := range manifest.GeneratedOutputs {
		if err := machinecontract.ValidateSafeRelativePath(output.Path); err != nil {
			return err
		}
		if err := machinecontract.ValidateSafeRelativePath(output.SourcePath); err != nil {
			return err
		}
		file, exists := contentPaths[output.SourcePath]
		if !exists || file.SHA256 != output.SourceSHA256 {
			return fmt.Errorf("generated output source %q is not locked by content_files", output.SourcePath)
		}
		key := strings.ToLower(output.Path)
		if _, duplicate := seenOutputs[key]; duplicate {
			return fmt.Errorf("duplicate generated output path %q", output.Path)
		}
		seenOutputs[key] = struct{}{}
	}
	return nil
}

func validateTemplateContentReferences(manifest TemplateManifest) error {
	contentPaths := make(map[string]struct{}, len(manifest.ContentFiles))
	sourceCovered := false
	for _, file := range manifest.ContentFiles {
		contentPaths[file.Path] = struct{}{}
		if pathWithin(file.Path, manifest.SourceRoot) {
			sourceCovered = true
		}
	}
	if !sourceCovered {
		return fmt.Errorf("source_root %q does not contain a content file", manifest.SourceRoot)
	}
	for _, asset := range manifest.PreviewAssets {
		if _, exists := contentPaths[asset]; !exists {
			return fmt.Errorf("preview_asset %q is not listed in content_files", asset)
		}
	}
	return nil
}

func pathWithin(path, root string) bool {
	return strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/")
}

func validateDocumentIntegrity(document sourceDocument, expectedManifest string, files []ContentFile, expectedTree string) error {
	manifestDigest, err := machinecontract.DigestWithoutTopLevelField(document.contents, "manifest_sha256")
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(manifestDigest), []byte(expectedManifest)) != 1 {
		return ErrChecksumMismatch
	}
	files = append([]ContentFile(nil), files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	seen := make(map[string]struct{}, len(files))
	seenFolded := make(map[string]string, len(files))
	for _, file := range files {
		if err := machinecontract.ValidateSafeRelativePath(file.Path); err != nil {
			return err
		}
		if _, duplicate := seen[file.Path]; duplicate {
			return fmt.Errorf("duplicate content path %s", file.Path)
		}
		seen[file.Path] = struct{}{}
		folded := strings.ToLower(file.Path)
		if previous, collision := seenFolded[folded]; collision {
			return fmt.Errorf("%w: content paths collide by case: %s and %s", ErrInvalidLayout, previous, file.Path)
		}
		seenFolded[folded] = file.Path
		if document.versionRoot == "" {
			continue
		}
		absolute := filepath.Join(document.versionRoot, filepath.FromSlash(file.Path))
		if err := validateRegularFileInside(document.versionRoot, absolute); err != nil {
			return err
		}
		contents, err := os.ReadFile(absolute)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(contents)
		actual := "sha256:" + hex.EncodeToString(digest[:])
		if subtle.ConstantTimeCompare([]byte(actual), []byte(file.SHA256)) != 1 {
			return fmt.Errorf("%w: %s", ErrChecksumMismatch, file.Path)
		}
	}
	if document.versionRoot != "" {
		actualFiles, err := enumerateVersionFiles(document.versionRoot, document.manifestName)
		if err != nil {
			return err
		}
		for path := range actualFiles {
			if _, declared := seen[path]; !declared {
				return fmt.Errorf("%w: unlisted content file %s", ErrInvalidLayout, path)
			}
		}
		for path := range seen {
			if _, exists := actualFiles[path]; !exists {
				return fmt.Errorf("%w: listed content file is missing %s", ErrInvalidLayout, path)
			}
		}
	}
	treeDocument, err := json.Marshal(files)
	if err != nil {
		return err
	}
	treeDigest, err := machinecontract.Digest(treeDocument)
	if err != nil {
		return err
	}
	actualTree := "sha256:" + treeDigest
	if subtle.ConstantTimeCompare([]byte(actualTree), []byte(expectedTree)) != 1 {
		return ErrContentTreeMismatch
	}
	return nil
}

func enumerateVersionFiles(versionRoot, manifestName string) (map[string]struct{}, error) {
	if manifestName == "" {
		return nil, fmt.Errorf("%w: manifest name is required for disk catalogs", ErrInvalidLayout)
	}
	files := make(map[string]struct{})
	folded := make(map[string]string)
	err := filepath.WalkDir(versionRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == versionRoot {
			return nil
		}
		reparse, err := isReparsePoint(path)
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 || reparse {
			return fmt.Errorf("%w: symbolic links are not allowed: %s", ErrInvalidLayout, path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: catalog content is not a regular file: %s", ErrInvalidLayout, path)
		}
		relative, err := filepath.Rel(versionRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		key := strings.ToLower(relative)
		if previous, collision := folded[key]; collision {
			return fmt.Errorf("%w: paths collide by case: %s and %s", ErrInvalidLayout, previous, relative)
		}
		folded[key] = relative
		if relative == manifestName {
			return nil
		}
		files[relative] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func validateRegularFileInside(root, path string) error {
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	pathAbsolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(rootAbsolute, pathAbsolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("content path leaves version root")
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootAbsolute)
	if err != nil {
		return err
	}
	resolvedPath, err := filepath.EvalSymlinks(pathAbsolute)
	if err != nil {
		return err
	}
	resolvedRelative, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("content path escapes version root through a link")
	}
	info, err := os.Lstat(pathAbsolute)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("content path is not a regular file: %s", pathAbsolute)
	}
	return nil
}

func sortPackages(manifests []PackageManifest) {
	sort.Slice(manifests, func(i, j int) bool {
		first, _ := semver.StrictNewVersion(manifests[i].Version)
		second, _ := semver.StrictNewVersion(manifests[j].Version)
		return first.GreaterThan(second)
	})
}

func sortTemplates(manifests []TemplateManifest) {
	sort.Slice(manifests, func(i, j int) bool {
		first, _ := semver.StrictNewVersion(manifests[i].Version)
		second, _ := semver.StrictNewVersion(manifests[j].Version)
		return first.GreaterThan(second)
	})
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func errorsIsNotExist(err error) bool { return err != nil && os.IsNotExist(err) }
