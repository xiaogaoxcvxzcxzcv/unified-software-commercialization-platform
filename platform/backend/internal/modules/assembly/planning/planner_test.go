package planning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecatalog"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

func TestPlannerBuildsDeterministicMultiApplicationPlan(t *testing.T) {
	registry := loadRegistry(t)
	catalog := loadTestCatalog(t, registry)
	document := loadBlueprint(t)
	blueprintDigest, err := machinecontract.Digest(document)
	if err != nil {
		t.Fatal(err)
	}
	blueprint := core.Blueprint{BlueprintID: "bp_video-brain", Revision: 1, Document: document, ContentSHA256: "sha256:" + blueprintDigest}
	planner := New(catalog)

	first, err := planner.BuildPlan(context.Background(), blueprint, "production")
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	second, err := planner.BuildPlan(context.Background(), blueprint, "production")
	if err != nil {
		t.Fatalf("BuildPlan() second error = %v", err)
	}
	if string(first.Document) != string(second.Document) {
		t.Fatal("equivalent planner inputs produced different plan bytes")
	}
	if err := registry.Validate("assembly-plan", first.Document); err != nil {
		t.Fatalf("plan schema validation failed: %v", err)
	}
	var plan struct {
		Applications      []resolvedApplication `json:"applications"`
		Generator         resolvedGenerator     `json:"generator"`
		Capabilities      []map[string]any      `json:"capabilities"`
		Providers         []resolvedProvider    `json:"providers"`
		RequiredProviders []string              `json:"required_providers"`
		ExpectedOutputs   []expectedOutput      `json:"expected_outputs"`
		PlanChecksum      string                `json:"plan_checksum"`
	}
	if err := json.Unmarshal(first.Document, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Applications) != 2 || plan.Applications[0].ApplicationID != "video-brain.desktop" || plan.Applications[1].ApplicationID != "video-brain.web" {
		t.Fatalf("applications = %#v", plan.Applications)
	}
	if plan.Generator.GeneratorID != "platform.generator" || len(plan.RequiredProviders) != 1 || plan.RequiredProviders[0] != "notification.security" || len(first.Capabilities) != 1 || first.Capabilities[0].CapabilityID != "identity.user-session" {
		t.Fatalf("generator/capabilities = %#v / %#v", plan.Generator, first.Capabilities)
	}
	if len(plan.Capabilities) != 1 || len(plan.Providers) != 1 || plan.Providers[0].ConfigRef != "configs/notification-security.json" || len(plan.ExpectedOutputs) != 4 {
		t.Fatalf("locked capabilities/providers/outputs = %#v / %#v / %#v", plan.Capabilities, plan.Providers, plan.ExpectedOutputs)
	}
	wantChecksum, err := machinecontract.DigestWithoutTopLevelField(first.Document, "plan_checksum")
	if err != nil {
		t.Fatal(err)
	}
	if plan.PlanChecksum != wantChecksum {
		t.Fatalf("plan checksum = %s, want %s", plan.PlanChecksum, wantChecksum)
	}
}

func TestPlannerRejectsBlueprintWithoutCapabilityPackages(t *testing.T) {
	registry := loadRegistry(t)
	document := loadBlueprint(t)
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	value["packages"] = []any{}
	for _, item := range value["applications"].([]any) {
		ui := item.(map[string]any)["ui"].(map[string]any)
		ui["template_id"] = "blank-a"
		ui["version"] = "1.0.0"
	}
	document, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate("product-blueprint", document); err == nil {
		t.Fatal("product blueprint without a capability package unexpectedly passed validation")
	}
}

func TestPlannerFailsClosedWithoutTrustedTool(t *testing.T) {
	registry := loadRegistry(t)
	catalog := loadTestCatalogWithoutTools(t, registry)
	document := loadBlueprint(t)
	digest, err := machinecontract.Digest(document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(catalog).BuildPlan(context.Background(), core.Blueprint{BlueprintID: "bp_video-brain", Revision: 1, Document: document, ContentSHA256: "sha256:" + digest}, "production")
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("BuildPlan() error = %v, want %v", err, ErrUnknownTool)
	}
}

func TestPlannerRejectsDuplicateApplicationIdentity(t *testing.T) {
	registry := loadRegistry(t)
	catalog := loadTestCatalog(t, registry)
	document := loadBlueprint(t)
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	applications := value["applications"].([]any)
	applications[1].(map[string]any)["application_id"] = applications[0].(map[string]any)["application_id"]
	document, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := machinecontract.Digest(document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(catalog).BuildPlan(context.Background(), core.Blueprint{BlueprintID: "bp_video-brain", Revision: 1, Document: document, ContentSHA256: "sha256:" + digest}, "production")
	if !errors.Is(err, ErrBlueprintMismatch) {
		t.Fatalf("BuildPlan() error = %v, want %v", err, ErrBlueprintMismatch)
	}
}

func TestPlannerRejectsUnresolvedExtensionAndOverlappingOutputs(t *testing.T) {
	registry := loadRegistry(t)
	catalog := loadTestCatalog(t, registry)
	base := loadBlueprint(t)
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "extension", mutate: func(value map[string]any) {
			value["extensions"] = []any{map[string]any{"extension_id": "custom.editor", "version": "1.0.0", "manifest_path": "extensions/editor.json"}}
		}},
		{name: "overlapping output", mutate: func(value map[string]any) {
			applications := value["applications"].([]any)
			applications[1].(map[string]any)["output_path"] = "apps/web/admin"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(base, &value); err != nil {
				t.Fatal(err)
			}
			test.mutate(value)
			document, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			digest, err := machinecontract.Digest(document)
			if err != nil {
				t.Fatal(err)
			}
			_, err = New(catalog).BuildPlan(context.Background(), core.Blueprint{BlueprintID: "bp_video-brain", Revision: 1, Document: document, ContentSHA256: "sha256:" + digest}, "production")
			if err == nil {
				t.Fatal("BuildPlan() unexpectedly accepted invalid input")
			}
			if test.name == "overlapping output" && !errors.Is(err, ErrBlueprintMismatch) {
				t.Fatalf("BuildPlan() error = %v, want %v", err, ErrBlueprintMismatch)
			}
		})
	}
}

func TestPlannerLocksTrustedExtensionDeterministically(t *testing.T) {
	registry := loadRegistry(t)
	catalog := loadTestCatalogWithExtensionTargets(t, registry, []string{"web", "desktop_webview"})
	document := blueprintWithExtension(t, loadBlueprint(t), "extension.editor-tools", "1.0.0", "extension.editor-tools/1.0.0/manifest.json")
	digest, err := machinecontract.Digest(document)
	if err != nil {
		t.Fatal(err)
	}
	planner := New(catalog)
	blueprint := core.Blueprint{BlueprintID: "bp_video-brain", Revision: 1, Document: document, ContentSHA256: "sha256:" + digest}
	first, err := planner.BuildPlan(context.Background(), blueprint, "production")
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	second, err := planner.BuildPlan(context.Background(), blueprint, "production")
	if err != nil {
		t.Fatalf("BuildPlan() second error = %v", err)
	}
	if string(first.Document) != string(second.Document) {
		t.Fatal("trusted extension produced nondeterministic plan bytes")
	}
	if err := registry.Validate("assembly-plan", first.Document); err != nil {
		t.Fatalf("plan schema validation failed: %v", err)
	}
	var plan struct {
		Extensions []resolvedExtension `json:"extensions"`
	}
	if err := json.Unmarshal(first.Document, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Extensions) != 1 {
		t.Fatalf("extensions = %#v", plan.Extensions)
	}
	extension := plan.Extensions[0]
	if extension.ExtensionID != "extension.editor-tools" || extension.ProductCode != "video-brain" || extension.ManifestPath != "extension.editor-tools/1.0.0/manifest.json" || extension.DataNamespace != "ext_editor_tools" {
		t.Fatalf("resolved extension = %#v", extension)
	}
	if !strings.HasPrefix(extension.ManifestSHA256, "sha256:") || !strings.HasPrefix(extension.ContentTreeSHA256, "sha256:") {
		t.Fatalf("extension digests are not locked: %#v", extension)
	}
}

func TestPlannerRejectsUntrustedOrIncompatibleExtensionSelection(t *testing.T) {
	registry := loadRegistry(t)
	base := loadBlueprint(t)
	for _, test := range []struct {
		name    string
		catalog *machinecatalog.Catalog
		doc     json.RawMessage
	}{
		{name: "unknown", catalog: loadTestCatalogWithExtensionTargets(t, registry, []string{"web", "desktop_webview"}), doc: blueprintWithExtension(t, base, "extension.unknown", "1.0.0", "extension.unknown/1.0.0/manifest.json")},
		{name: "non-canonical manifest path", catalog: loadTestCatalogWithExtensionTargets(t, registry, []string{"web", "desktop_webview"}), doc: blueprintWithExtension(t, base, "extension.editor-tools", "1.0.0", "extension.editor-tools/1.0.0/other.json")},
		{name: "cross-product", catalog: loadTestCatalogWithExtensionTargets(t, registry, []string{"web", "desktop_webview"}), doc: blueprintWithProductAndExtension(t, base, "another-product")},
		{name: "one application target unsupported", catalog: loadTestCatalogWithExtensionTargets(t, registry, []string{"web"}), doc: blueprintWithExtensionAndSecondTarget(t, base, "desktop_webview")},
	} {
		t.Run(test.name, func(t *testing.T) {
			digest, err := machinecontract.Digest(test.doc)
			if err != nil {
				t.Fatal(err)
			}
			_, err = New(test.catalog).BuildPlan(context.Background(), core.Blueprint{BlueprintID: "bp_video-brain", Revision: 1, Document: test.doc, ContentSHA256: "sha256:" + digest}, "production")
			if err == nil {
				t.Fatal("BuildPlan() unexpectedly accepted an untrusted extension selection")
			}
		})
	}
}

func blueprintWithExtension(t *testing.T, document json.RawMessage, extensionID, version, manifestPath string) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	value["extensions"] = []any{map[string]any{"extension_id": extensionID, "version": version, "manifest_path": manifestPath}}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func blueprintWithProductAndExtension(t *testing.T, document json.RawMessage, productCode string) json.RawMessage {
	t.Helper()
	value := blueprintWithExtension(t, document, "extension.editor-tools", "1.0.0", "extension.editor-tools/1.0.0/manifest.json")
	var decoded map[string]any
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["product"].(map[string]any)["code"] = productCode
	raw, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func blueprintWithExtensionAndSecondTarget(t *testing.T, document json.RawMessage, target string) json.RawMessage {
	t.Helper()
	value := blueprintWithExtension(t, document, "extension.editor-tools", "1.0.0", "extension.editor-tools/1.0.0/manifest.json")
	var decoded map[string]any
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["applications"].([]any)[1].(map[string]any)["target"] = target
	raw, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func loadBlueprint(t *testing.T) json.RawMessage {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(repositoryRoot(t), "platform", "contracts", "schemas", "fixtures", "catalog-blueprint", "product-blueprint.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatal(err)
	}
	applications := document["applications"].([]any)
	web := applications[0].(map[string]any)
	web["application_id"] = "video-brain.web"
	desktop := cloneObject(t, web)
	desktop["application_id"] = "video-brain.desktop"
	desktop["output_path"] = "apps/desktop"
	document["applications"] = []any{web, desktop}
	document["provider_refs"] = []any{map[string]any{
		"provider": "notification.security", "environment": "production",
		"config_ref": "configs/notification-security.json", "secret_refs": []any{},
	}}
	contents, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := loadRegistry(t).Validate("product-blueprint", contents); err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestPlannerRejectsMissingRequiredProvider(t *testing.T) {
	registry := loadRegistry(t)
	catalog := loadTestCatalog(t, registry)
	document := loadBlueprint(t)
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	value["provider_refs"] = []any{}
	document, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := machinecontract.Digest(document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(catalog).BuildPlan(context.Background(), core.Blueprint{BlueprintID: "bp_video-brain", Revision: 1, Document: document, ContentSHA256: "sha256:" + digest}, "production")
	if !errors.Is(err, ErrBlueprintMismatch) {
		t.Fatalf("BuildPlan() error = %v, want %v", err, ErrBlueprintMismatch)
	}
}

func TestPlannerAcceptsDeclaredOptionalProviderAndRejectsProviderInjection(t *testing.T) {
	registry := loadRegistry(t)
	catalog := loadTestCatalog(t, registry)
	base := loadBlueprint(t)
	var value map[string]any
	if err := json.Unmarshal(base, &value); err != nil {
		t.Fatal(err)
	}
	providers := value["provider_refs"].([]any)
	value["provider_refs"] = append(providers, map[string]any{
		"provider": "identity.external.oidc", "environment": "production",
		"config_ref": "configs/identity-oidc.json", "secret_refs": []any{},
	})
	document, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := machinecontract.Digest(document)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := New(catalog).BuildPlan(context.Background(), core.Blueprint{BlueprintID: "bp_video-brain", Revision: 1, Document: document, ContentSHA256: "sha256:" + digest}, "production")
	if err != nil {
		t.Fatalf("declared optional provider rejected: %v", err)
	}
	var plan struct {
		Providers         []resolvedProvider `json:"providers"`
		RequiredProviders []string           `json:"required_providers"`
	}
	if err := json.Unmarshal(planned.Document, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Providers) != 2 || len(plan.RequiredProviders) != 1 || plan.RequiredProviders[0] != "notification.security" {
		t.Fatalf("optional provider plan = %#v / required %#v", plan.Providers, plan.RequiredProviders)
	}

	value["provider_refs"] = append(value["provider_refs"].([]any), map[string]any{
		"provider": "injected.provider", "environment": "production", "config_ref": "configs/injected.json", "secret_refs": []any{},
	})
	document, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest, err = machinecontract.Digest(document)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(catalog).BuildPlan(context.Background(), core.Blueprint{BlueprintID: "bp_video-brain", Revision: 1, Document: document, ContentSHA256: "sha256:" + digest}, "production")
	if !errors.Is(err, ErrBlueprintMismatch) {
		t.Fatalf("provider injection error = %v", err)
	}
}

func TestPlannerRejectsAmbiguousOrCrossProviderSecretConfiguration(t *testing.T) {
	registry := loadRegistry(t)
	catalog := loadTestCatalog(t, registry)
	base := loadBlueprint(t)
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "duplicate provider", mutate: func(value map[string]any) {
			providers := value["provider_refs"].([]any)
			value["provider_refs"] = append(providers, map[string]any{"provider": "notification.security", "environment": "production", "config_ref": "configs/other.json", "secret_refs": []any{}})
		}},
		{name: "cross provider secret", mutate: func(value map[string]any) {
			provider := value["provider_refs"].([]any)[0].(map[string]any)
			provider["secret_refs"] = []any{map[string]any{"provider": "vault.other", "key": "API_KEY", "environment": "production"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(base, &value); err != nil {
				t.Fatal(err)
			}
			test.mutate(value)
			document, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			digest, err := machinecontract.Digest(document)
			if err != nil {
				t.Fatal(err)
			}
			_, err = New(catalog).BuildPlan(context.Background(), core.Blueprint{BlueprintID: "bp_video-brain", Revision: 1, Document: document, ContentSHA256: "sha256:" + digest}, "production")
			if !errors.Is(err, ErrBlueprintMismatch) {
				t.Fatalf("BuildPlan() error=%v, want %v", err, ErrBlueprintMismatch)
			}
		})
	}
}

func loadTestCatalog(t *testing.T, registry *machinecontract.Registry) *machinecatalog.Catalog {
	return loadTestCatalogWithTools(t, registry, true)
}

func loadTestCatalogWithoutTools(t *testing.T, registry *machinecontract.Registry) *machinecatalog.Catalog {
	return loadTestCatalogWithTools(t, registry, false)
}

func loadTestCatalogWithTools(t *testing.T, registry *machinecontract.Registry, includeTools bool) *machinecatalog.Catalog {
	return loadTestCatalogConfigured(t, registry, includeTools, nil)
}

func loadTestCatalogWithExtensionTargets(t *testing.T, registry *machinecontract.Registry, targets []string) *machinecatalog.Catalog {
	return loadTestCatalogConfigured(t, registry, true, targets)
}

func loadTestCatalogConfigured(t *testing.T, registry *machinecontract.Registry, includeTools bool, extensionTargets []string) *machinecatalog.Catalog {
	t.Helper()
	root := t.TempDir()
	packageRoot := filepath.Join(root, "packages")
	templateRoot := filepath.Join(root, "templates")
	generatorRoot := filepath.Join(root, "generators")
	sdkRoot := filepath.Join(root, "sdks")
	extensionRoot := filepath.Join(root, "extensions")
	writeCatalogDocument(t, filepath.Join(repositoryRoot(t), "platform", "contracts", "schemas", "fixtures", "catalog-blueprint", "package-manifest.valid.json"), packageRoot, "package.account", "1.0.0", "manifest.json", "content/account.txt", []byte("account package content"))
	writeCatalogDocument(t, filepath.Join(repositoryRoot(t), "platform", "contracts", "schemas", "fixtures", "catalog-blueprint", "ui-template-manifest.valid.json"), templateRoot, "standard-a", "1.0.0", "template.json", "template/index.tsx", []byte("export const template = 'standard-a';\n"))
	writeCatalogDocument(t, filepath.Join(repositoryRoot(t), "platform", "contracts", "schemas", "fixtures", "catalog-blueprint", "ui-template-manifest.blank.valid.json"), templateRoot, "blank-a", "1.0.0", "template.json", "template/index.tsx", []byte("export const template = 'blank-a';\n"))
	blocks, err := machinecatalog.NewBlockCatalog("1.0.0", []machinecatalog.BlockDefinition{
		{BlockID: "account.profile", Surface: "client", Readiness: "ready"},
		{BlockID: "auth.login", Surface: "client", Readiness: "ready"},
		{BlockID: "entitlement.summary", Surface: "client", Readiness: "ready"},
		{BlockID: "identity.user-table", Surface: "admin", Readiness: "ready"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var catalog *machinecatalog.Catalog
	if includeTools {
		writeToolCatalogDocument(t, generatorRoot, "generator", "platform.generator", "1.0.0")
		writeToolCatalogDocument(t, sdkRoot, "sdk", "platform.sdk", "1.0.0")
		if extensionTargets != nil {
			writeExtensionCatalogDocument(t, extensionRoot, extensionTargets)
			catalog, err = machinecatalog.LoadOrdinaryWithToolsAndExtensions(packageRoot, templateRoot, generatorRoot, sdkRoot, extensionRoot, registry, accesscontrol.CurrentPermissionCatalog(), blocks)
		} else {
			catalog, err = machinecatalog.LoadOrdinaryWithTools(packageRoot, templateRoot, generatorRoot, sdkRoot, registry, accesscontrol.CurrentPermissionCatalog(), blocks)
		}
	} else {
		catalog, err = machinecatalog.LoadOrdinary(packageRoot, templateRoot, registry, accesscontrol.CurrentPermissionCatalog(), blocks)
	}
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func writeExtensionCatalogDocument(t *testing.T, root string, targets []string) {
	t.Helper()
	ownedPaths := []string{"extensions/editor-tools/account-slot.tsx", "extensions/editor-tools/admin.tsx", "extensions/editor-tools/workspace.tsx"}
	files := make([]machinecatalog.ContentFile, 0, len(ownedPaths))
	contentFiles := make([]any, 0, len(ownedPaths))
	for _, ownedPath := range ownedPaths {
		digest := digestOf("export const extension = true;\n")
		files = append(files, machinecatalog.ContentFile{Path: ownedPath, SHA256: digest, Kind: "file"})
		contentFiles = append(contentFiles, map[string]any{"path": ownedPath, "sha256": digest, "kind": "file"})
	}
	treeRaw, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	treeDigest, err := machinecontract.Digest(treeRaw)
	if err != nil {
		t.Fatal(err)
	}
	document := map[string]any{
		"schema_version": "1.0.0", "extension_id": "extension.editor-tools", "version": "1.0.0", "product_code": "video-brain",
		"catalog_scope": "ordinary", "readiness": "available", "supported_targets": targets,
		"supported_delivery_modes": []string{"generated_source"}, "supported_environments": []string{"production"},
		"required_permissions": []string{"assembly.plan"}, "public_api_operations": []string{"getCurrentEntitlements"},
		"published_events": []string{"editor.project_exported.v1"}, "subscribed_events": []string{"identity.logged_in.v1"},
		"routes":           []any{map[string]any{"route_id": "editor.workspace", "target": "web", "path": "/editor/workspace", "entry_path": ownedPaths[2], "required_permission": "assembly.plan"}},
		"navigation_items": []any{map[string]any{"item_id": "editor.workspace-nav", "target": "web", "label_key": "editor.workspace", "route_id": "editor.workspace", "required_permission": "assembly.plan"}},
		"slots":            []any{map[string]any{"slot_id": "client.account.after", "target": "web", "entry_path": ownedPaths[0]}},
		"admin_items":      []any{map[string]any{"item_id": "editor.admin", "label_key": "editor.admin", "path": "/extensions/editor", "entry_path": ownedPaths[1], "required_permission": "assembly.plan"}},
		"data_namespace":   "ext_editor_tools", "owned_tables": []string{"ext_editor_tools.projects"}, "consumed_services": []string{"identity.public-service"}, "owned_paths": ownedPaths,
		"install_plan":     map[string]any{"strategy": "declarative_v1", "steps": []string{"extension.register-routes"}},
		"uninstall_plan":   map[string]any{"strategy": "declarative_v1", "steps": []string{"extension.unregister-routes"}},
		"retention_policy": map[string]any{"mode": "delete", "retention_days": 0}, "content_files": contentFiles,
		"content_tree_sha256": "sha256:" + treeDigest, "manifest_sha256": "sha256:" + strings.Repeat("0", 64),
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest, err := machinecontract.DigestWithoutTopLevelField(raw, "manifest_sha256")
	if err != nil {
		t.Fatal(err)
	}
	document["manifest_sha256"] = manifestDigest
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	versionRoot := filepath.Join(root, "extension.editor-tools", "1.0.0")
	for _, ownedPath := range ownedPaths {
		absolute := filepath.Join(versionRoot, filepath.FromSlash(ownedPath))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte("export const extension = true;\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(versionRoot, "manifest.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeToolCatalogDocument(t *testing.T, root, kind, id, version string) {
	t.Helper()
	versionRoot := filepath.Join(root, id, version)
	evidenceContents := []byte("{\"status\":\"passed\"}\n")
	evidenceDigest := digestOfBytes(evidenceContents)
	evidence := make([]any, 0, 2)
	for _, target := range []string{"web", "desktop_webview"} {
		evidence = append(evidence, map[string]any{
			"type": "test_report", "target": target, "delivery_mode": "generated_source", "environment": "production",
			"status": "passed", "path": "evidence/test-report.json", "sha256": evidenceDigest,
		})
	}
	files := []machinecatalog.ContentFile{{Path: "evidence/test-report.json", SHA256: evidenceDigest, Kind: "file"}}
	treeRaw, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	treeDigest, err := machinecontract.Digest(treeRaw)
	if err != nil {
		t.Fatal(err)
	}
	document := map[string]any{
		"schema_version": "1.0.0", "tool_kind": kind, "tool_id": id, "version": version, "name": id,
		"catalog_scope": "ordinary", "readiness": "available",
		"supported_targets": []string{"web", "desktop_webview"}, "supported_delivery_modes": []string{"generated_source"},
		"supported_environments": []string{"production"}, "protocol": map[string]any{"id": "assembly." + kind, "version": "1.0.0"},
		"platform_contract_range": "^1.0.0", "execution": map[string]any{"mode": "builtin_adapter", "adapter_id": map[string]string{"generator": "assembly.pure-renderer", "sdk": "assembly.client-sdk"}[kind]},
		"evidence": evidence, "content_files": files, "content_tree_sha256": "sha256:" + treeDigest,
		"manifest_sha256": "sha256:" + strings.Repeat("0", 64),
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest, err := machinecontract.DigestWithoutTopLevelField(raw, "manifest_sha256")
	if err != nil {
		t.Fatal(err)
	}
	document["manifest_sha256"] = manifestDigest
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(versionRoot, "evidence"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionRoot, "evidence", "test-report.json"), evidenceContents, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionRoot, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCatalogDocument(t *testing.T, fixture, root, id, version, manifestName, contentPath string, content []byte) {
	t.Helper()
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	fileContents := map[string][]byte{contentPath: content}
	if configPath, ok := document["config_schema_path"].(string); ok {
		fileContents[configPath] = []byte("{\"type\":\"object\"}\n")
	}
	if previewAssets, ok := document["preview_assets"].([]any); ok {
		for _, previewAsset := range previewAssets {
			fileContents[previewAsset.(string)] = []byte("preview-asset")
		}
	}
	if outputs, ok := document["generated_outputs"].([]any); ok {
		for _, item := range outputs {
			output := item.(map[string]any)
			sourcePath := output["source_path"].(string)
			output["source_sha256"] = digestOfBytes(fileContents[sourcePath])
		}
	}
	if entrypoints, ok := document["entrypoints"].([]any); ok {
		for _, item := range entrypoints {
			entrypoint := item.(map[string]any)
			sourcePath := entrypoint["source_path"].(string)
			entrypoint["source_sha256"] = digestOfBytes(fileContents[sourcePath])
		}
	}
	paths := make([]string, 0, len(fileContents))
	for path := range fileContents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	files := make([]machinecatalog.ContentFile, 0, len(paths))
	contentFiles := make([]any, 0, len(paths))
	for _, path := range paths {
		fileDigest := digestOfBytes(fileContents[path])
		files = append(files, machinecatalog.ContentFile{Path: path, SHA256: fileDigest, Kind: "file"})
		contentFiles = append(contentFiles, map[string]any{"path": path, "sha256": fileDigest, "kind": "file"})
	}
	treeRaw, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	treeDigest, err := machinecontract.Digest(treeRaw)
	if err != nil {
		t.Fatal(err)
	}
	document["content_files"] = contentFiles
	document["content_tree_sha256"] = "sha256:" + treeDigest
	document["manifest_sha256"] = "sha256:" + strings.Repeat("0", 64)
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest, err := machinecontract.DigestWithoutTopLevelField(raw, "manifest_sha256")
	if err != nil {
		t.Fatal(err)
	}
	document["manifest_sha256"] = manifestDigest
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	versionRoot := filepath.Join(root, id, version)
	for _, path := range paths {
		absolute := filepath.Join(versionRoot, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, fileContents[path], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(versionRoot, manifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func loadRegistry(t *testing.T) *machinecontract.Registry {
	t.Helper()
	registry, err := machinecontract.LoadDirectory(filepath.Join(repositoryRoot(t), "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", "..", "..", "..", ".."))
}

func cloneObject(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func digestOf(value string) string { return digestOfBytes([]byte(value)) }

func digestOfBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}
