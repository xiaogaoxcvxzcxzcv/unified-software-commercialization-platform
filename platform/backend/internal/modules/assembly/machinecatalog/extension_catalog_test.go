package machinecatalog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
)

func TestTrustedExtensionCatalogLoadsResolvesAndSnapshots(t *testing.T) {
	root := t.TempDir()
	document := extensionDocument(t, "extension.editor-tools", "1.0.0", "video-brain", ordinaryView)
	writeDiskDocument(t, root, document, "manifest.json")
	catalog, err := LoadOrdinaryWithExtensions(filepath.Join(root, "packages"), filepath.Join(root, "templates"), root, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t))
	if err != nil {
		t.Fatalf("LoadOrdinaryWithExtensions() error = %v", err)
	}
	resolved, err := catalog.ResolveExtensions([]ExtensionRequirement{{ExtensionID: "extension.editor-tools", Version: "1.0.0", ManifestPath: "extension.editor-tools/1.0.0/manifest.json"}}, "video-brain", "web", "generated_source", "test")
	if err != nil || len(resolved) != 1 {
		t.Fatalf("ResolveExtensions() = %#v, %v", resolved, err)
	}
	if resolved[0].ManifestPath != "extension.editor-tools/1.0.0/manifest.json" {
		t.Fatalf("ManifestPath = %q", resolved[0].ManifestPath)
	}
	snapshot, err := catalog.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Extensions) != 1 || snapshot.Extensions[0].ManifestPath != resolved[0].ManifestPath || snapshot.Extensions[0].ManifestSHA256 != resolved[0].ManifestSHA256 {
		t.Fatalf("snapshot extensions = %#v", snapshot.Extensions)
	}
}

func TestTrustedExtensionCatalogSeparatesScopeAndRejectsDrift(t *testing.T) {
	t.Run("scope", func(t *testing.T) {
		root := t.TempDir()
		writeDiskDocument(t, root, extensionDocument(t, "extension.editor-tools", "1.0.0", "video-brain", experimentalView), "manifest.json")
		_, err := LoadOrdinaryWithExtensions(filepath.Join(root, "packages"), filepath.Join(root, "templates"), root, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t))
		if !errors.Is(err, ErrCatalogState) {
			t.Fatalf("error = %v, want %v", err, ErrCatalogState)
		}
		if _, err := LoadExperimentalWithExtensions(filepath.Join(root, "packages"), filepath.Join(root, "templates"), root, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t)); err != nil {
			t.Fatalf("LoadExperimentalWithExtensions() error = %v", err)
		}
	})
	t.Run("digest drift", func(t *testing.T) {
		root := t.TempDir()
		document := extensionDocument(t, "extension.editor-tools", "1.0.0", "video-brain", ordinaryView)
		writeDiskDocument(t, root, document, "manifest.json")
		contentPath := filepath.Join(root, document.identity, document.version, "extensions", "editor-tools", "entry.tsx")
		if err := os.WriteFile(contentPath, []byte("tampered"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadOrdinaryWithExtensions(filepath.Join(root, "packages"), filepath.Join(root, "templates"), root, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t))
		if !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("error = %v, want %v", err, ErrChecksumMismatch)
		}
	})
}

func TestTrustedExtensionCatalogRejectsInvalidAuthorityDeclarations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   error
	}{
		{name: "permission", mutate: func(value map[string]any) {
			value["required_permissions"] = []string{"extension.root"}
			value["routes"].([]any)[0].(map[string]any)["required_permission"] = "extension.root"
			value["navigation_items"].([]any)[0].(map[string]any)["required_permission"] = "extension.root"
			value["admin_items"].([]any)[0].(map[string]any)["required_permission"] = "extension.root"
		}, want: accesscontrol.ErrUnknownPermission},
		{name: "entry permission subset", mutate: func(value map[string]any) {
			value["routes"].([]any)[0].(map[string]any)["required_permission"] = "identity.manage"
		}, want: ErrExtensionIncompatible},
		{name: "table namespace", mutate: func(value map[string]any) {
			value["owned_tables"] = []string{"ext_identity.users"}
		}, want: ErrExtensionIncompatible},
		{name: "entry outside owned paths", mutate: func(value map[string]any) {
			value["owned_paths"] = []string{"extensions/editor-tools/other.tsx"}
		}, want: ErrExtensionIncompatible},
		{name: "unsealed owned path", mutate: func(value map[string]any) {
			value["owned_paths"] = []string{"extensions/editor-tools/entry.tsx", "extensions/editor-tools/unsealed.tsx"}
		}, want: ErrExtensionIncompatible},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := mutateDocument(t, extensionDocument(t, "extension.editor-tools", "1.0.0", "video-brain", ordinaryView), test.mutate, true)
			catalog, err := build(nil, nil, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t), ordinaryView)
			if err != nil {
				t.Fatal(err)
			}
			err = catalog.addExtensions([]sourceDocument{document})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestResolveExtensionsFailsClosedOnSelectionAndConflicts(t *testing.T) {
	first := extensionDocument(t, "extension.editor-tools", "1.0.0", "video-brain", ordinaryView)
	second := mutateDocument(t, extensionDocument(t, "extension.analytics", "1.0.0", "video-brain", ordinaryView), func(value map[string]any) {
		value["data_namespace"] = "ext_analytics"
		value["owned_tables"] = []string{"ext_analytics.events"}
		value["owned_paths"] = []string{"extensions/analytics/entry.tsx"}
		value["routes"].([]any)[0].(map[string]any)["entry_path"] = "extensions/analytics/entry.tsx"
		value["slots"].([]any)[0].(map[string]any)["entry_path"] = "extensions/analytics/entry.tsx"
		value["admin_items"].([]any)[0].(map[string]any)["entry_path"] = "extensions/analytics/entry.tsx"
		value["content_files"] = contentFiles("extensions/analytics/entry.tsx")
	}, true)
	catalog, err := build(nil, nil, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t), ordinaryView)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.addExtensions([]sourceDocument{first, second}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name         string
		requirements []ExtensionRequirement
		product      string
		want         error
	}{
		{name: "unknown", requirements: []ExtensionRequirement{{ExtensionID: "extension.unknown", Version: "1.0.0", ManifestPath: "extension.unknown/1.0.0/manifest.json"}}, product: "video-brain", want: ErrUnknownExtension},
		{name: "canonical path", requirements: []ExtensionRequirement{{ExtensionID: "extension.editor-tools", Version: "1.0.0", ManifestPath: "elsewhere/manifest.json"}}, product: "video-brain", want: ErrExtensionIncompatible},
		{name: "product", requirements: []ExtensionRequirement{{ExtensionID: "extension.editor-tools", Version: "1.0.0", ManifestPath: "extension.editor-tools/1.0.0/manifest.json"}}, product: "other-product", want: ErrExtensionIncompatible},
		{name: "route conflict", requirements: []ExtensionRequirement{
			{ExtensionID: "extension.editor-tools", Version: "1.0.0", ManifestPath: "extension.editor-tools/1.0.0/manifest.json"},
			{ExtensionID: "extension.analytics", Version: "1.0.0", ManifestPath: "extension.analytics/1.0.0/manifest.json"},
		}, product: "video-brain", want: ErrExtensionConflict},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := catalog.ResolveExtensions(test.requirements, test.product, "web", "generated_source", "test")
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateExtensionSelectionRejectsStableIDConflicts(t *testing.T) {
	first := decodedExtension(t, extensionDocument(t, "extension.editor-tools", "1.0.0", "video-brain", ordinaryView))
	second := decodedExtension(t, extensionDocument(t, "extension.analytics", "1.0.0", "video-brain", ordinaryView))
	second.Routes[0].RouteID, second.Routes[0].Path, second.Routes[0].EntryPath = "analytics.workspace", "/analytics/workspace", "extensions/analytics/entry.tsx"
	second.NavigationItems[0].ItemID, second.NavigationItems[0].RouteID = "analytics.workspace-nav", "analytics.workspace"
	second.Slots[0].SlotID, second.Slots[0].EntryPath = "client.analytics.after", "extensions/analytics/entry.tsx"
	second.AdminItems[0].ItemID, second.AdminItems[0].Path, second.AdminItems[0].EntryPath = "analytics.admin", "/extensions/analytics", "extensions/analytics/entry.tsx"
	second.DataNamespace, second.OwnedTables, second.OwnedPaths = "ext_analytics", []string{"ext_analytics.events"}, []string{"extensions/analytics/entry.tsx"}
	second.PublicAPIOperations, second.PublishedEvents, second.SubscribedEvents = []string{"getAnalytics"}, []string{"analytics.exported.v1"}, []string{"identity.logged_in.v1"}
	if err := validateExtensionSelection([]ExtensionManifest{first, second}); err != nil {
		t.Fatalf("unique selection error = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*ExtensionManifest)
	}{
		{name: "route id", mutate: func(value *ExtensionManifest) { value.Routes[0].RouteID = first.Routes[0].RouteID }},
		{name: "navigation id", mutate: func(value *ExtensionManifest) { value.NavigationItems[0].ItemID = first.NavigationItems[0].ItemID }},
		{name: "admin id", mutate: func(value *ExtensionManifest) { value.AdminItems[0].ItemID = first.AdminItems[0].ItemID }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := second
			candidate.Routes = append([]ExtensionRoute(nil), second.Routes...)
			candidate.NavigationItems = append([]ExtensionNavigationItem(nil), second.NavigationItems...)
			candidate.AdminItems = append([]ExtensionAdminItem(nil), second.AdminItems...)
			test.mutate(&candidate)
			if err := validateExtensionSelection([]ExtensionManifest{first, candidate}); !errors.Is(err, ErrExtensionConflict) {
				t.Fatalf("error = %v, want %v", err, ErrExtensionConflict)
			}
		})
	}
}

func TestValidateExtensionSelectionAllowsSharedSubscriptionsAndSelfSubscription(t *testing.T) {
	first := decodedExtension(t, extensionDocument(t, "extension.editor-tools", "1.0.0", "video-brain", ordinaryView))
	first.SubscribedEvents = append(first.SubscribedEvents, first.PublishedEvents[0])
	second := first
	second.ExtensionID = "extension.subscriber"
	second.Routes, second.NavigationItems, second.Slots, second.AdminItems = nil, nil, nil, nil
	second.DataNamespace, second.OwnedTables, second.OwnedPaths = "ext_subscriber", []string{"ext_subscriber.events"}, []string{"extensions/subscriber/entry.tsx"}
	second.PublicAPIOperations, second.PublishedEvents = []string{}, []string{}
	if err := validateExtensionSelection([]ExtensionManifest{first, second}); err != nil {
		t.Fatalf("shared subscriptions should not conflict: %v", err)
	}
}

func extensionDocument(t *testing.T, extensionID, version, productCode string, view catalogView) sourceDocument {
	t.Helper()
	entryPath := "extensions/editor-tools/entry.tsx"
	value := map[string]any{
		"schema_version": "1.0.0", "extension_id": extensionID, "version": version, "product_code": productCode,
		"catalog_scope": view.visibility, "readiness": view.readiness,
		"supported_targets": []string{"web"}, "supported_delivery_modes": []string{"generated_source"}, "supported_environments": []string{"test"},
		"required_permissions": []string{"assembly.plan"}, "public_api_operations": []string{"getCurrentEntitlements"},
		"published_events": []string{"editor.project_exported.v1"}, "subscribed_events": []string{"identity.logged_in.v1"},
		"routes":           []any{map[string]any{"route_id": "editor.workspace", "target": "web", "path": "/editor/workspace", "entry_path": entryPath, "required_permission": "assembly.plan"}},
		"navigation_items": []any{map[string]any{"item_id": "editor.workspace-nav", "target": "web", "label_key": "editor.workspace", "route_id": "editor.workspace", "required_permission": "assembly.plan"}},
		"slots":            []any{map[string]any{"slot_id": "client.account.after", "target": "web", "entry_path": entryPath}},
		"admin_items":      []any{map[string]any{"item_id": "editor.admin", "label_key": "editor.admin", "path": "/extensions/editor", "entry_path": entryPath, "required_permission": "assembly.plan"}},
		"data_namespace":   "ext_editor_tools", "owned_tables": []string{"ext_editor_tools.projects"}, "consumed_services": []string{"identity.public-service"}, "owned_paths": []string{entryPath},
		"install_plan":     map[string]any{"strategy": "declarative_v1", "steps": []string{"extension.register-routes"}},
		"uninstall_plan":   map[string]any{"strategy": "declarative_v1", "steps": []string{"extension.unregister-routes"}},
		"retention_policy": map[string]any{"mode": "delete", "retention_days": 0},
		"content_files":    contentFiles(entryPath), "content_tree_sha256": zeroDigest(), "manifest_sha256": zeroDigest(),
	}
	return finalizeDocument(t, value, extensionID, version)
}

func decodedExtension(t *testing.T, document sourceDocument) ExtensionManifest {
	t.Helper()
	var manifest ExtensionManifest
	if err := json.Unmarshal(document.contents, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.ManifestPath = canonicalExtensionManifestPath(manifest.ExtensionID, manifest.Version)
	return manifest
}
