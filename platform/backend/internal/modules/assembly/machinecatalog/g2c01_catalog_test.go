package machinecatalog

import (
	"errors"
	"path/filepath"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
)

func TestG2C01ExperimentalCatalogLocksAccountEntitlementTemplateToolsAndExtension(t *testing.T) {
	root := repositoryRoot(t)
	blocks, err := LoadBlockCatalog(filepath.Join(root, "platform", "contracts", "catalogs", "v1", "feature-blocks.json"), loadContracts(t))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadExperimentalWithToolsAndExtensions(
		filepath.Join(root, "platform", "experimental", "capability-packages"),
		filepath.Join(root, "platform", "experimental", "templates"),
		filepath.Join(root, "platform", "experimental", "tools", "generators"),
		filepath.Join(root, "platform", "experimental", "tools", "sdks"),
		filepath.Join(root, "platform", "experimental", "extensions"),
		loadContracts(t), accesscontrol.CurrentPermissionCatalog(), blocks,
	)
	if err != nil {
		t.Fatal(err)
	}

	request := ResolveRequest{
		Packages:      []Requirement{{PackageID: "package.entitlement", VersionRange: "1.0.0"}},
		Extensions:    []ExtensionRequirement{{ExtensionID: "extension.editor-tools", Version: "1.0.0", ManifestPath: "extension.editor-tools/1.0.0/manifest.json"}},
		ProductCode:   "video-brain",
		TemplateID:    "standard-a",
		TemplateRange: "0.1.0",
		Target:        "web",
		DeliveryMode:  "generated_source",
		Environment:   "test",
	}
	first, err := catalog.Resolve(request)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	second, err := catalog.Resolve(request)
	if err != nil {
		t.Fatalf("Resolve() second error = %v", err)
	}
	if first.Snapshot.SnapshotSHA256 != second.Snapshot.SnapshotSHA256 {
		t.Fatalf("experimental snapshot is not deterministic: %s != %s", first.Snapshot.SnapshotSHA256, second.Snapshot.SnapshotSHA256)
	}
	if len(first.Packages) != 2 || first.Packages[0].PackageID != "package.account" || first.Packages[1].PackageID != "package.entitlement" {
		t.Fatalf("package closure = %#v", first.Packages)
	}
	if first.Template.TemplateID != "standard-a" || first.Template.Version != "0.1.0" {
		t.Fatalf("template = %#v", first.Template)
	}
	if len(first.Extensions) != 1 || first.Extensions[0].ExtensionID != "extension.editor-tools" || first.Extensions[0].DataNamespace != "ext_editor_tools" {
		t.Fatalf("extensions = %#v", first.Extensions)
	}

	generator, err := catalog.ResolveTool("generator", "platform.generator", "1.0.0", "web", "generated_source", "test")
	if err != nil {
		t.Fatalf("ResolveTool(generator) error = %v", err)
	}
	sdk, err := catalog.ResolveTool("sdk", "platform.sdk", "1.0.0", "web", "generated_source", "test")
	if err != nil {
		t.Fatalf("ResolveTool(sdk) error = %v", err)
	}
	if generator.Execution.Mode != "builtin_adapter" || generator.Execution.AdapterID != "assembly.pure-renderer" {
		t.Fatalf("generator execution = %#v", generator.Execution)
	}
	if sdk.Execution.Mode != "builtin_adapter" || sdk.Execution.AdapterID != "assembly.client-sdk" {
		t.Fatalf("sdk execution = %#v", sdk.Execution)
	}

	snapshot, err := catalog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CatalogScope != "experimental" || len(snapshot.Packages) != 2 || len(snapshot.Templates) != 1 || len(snapshot.Generators) != 1 || len(snapshot.SDKs) != 1 || len(snapshot.Extensions) != 1 {
		t.Fatalf("snapshot counts/scope = %#v", snapshot)
	}
	for _, digest := range []string{generator.ManifestSHA256, generator.ContentTreeSHA256, sdk.ManifestSHA256, sdk.ContentTreeSHA256, first.Extensions[0].ManifestSHA256, first.Extensions[0].ContentTreeSHA256} {
		if len(digest) != len("sha256:")+64 {
			t.Fatalf("unsealed digest %q", digest)
		}
	}
}

func TestG2C01OrdinaryCatalogCannotSeeExperimentalCandidateClosure(t *testing.T) {
	root := repositoryRoot(t)
	blocks, err := LoadBlockCatalog(filepath.Join(root, "platform", "contracts", "catalogs", "v1", "feature-blocks.json"), loadContracts(t))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadOrdinaryWithToolsAndExtensions(
		filepath.Join(root, "platform", "capability-packages"),
		filepath.Join(root, "platform", "templates"),
		filepath.Join(root, "platform", "tools", "generators"),
		filepath.Join(root, "platform", "tools", "sdks"),
		filepath.Join(root, "platform", "extensions"),
		loadContracts(t), accesscontrol.CurrentPermissionCatalog(), blocks,
	)
	if err != nil {
		t.Fatal(err)
	}
	options, err := catalog.Options("web", "generated_source", "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(options.Packages) != 0 || len(options.Templates) != 0 || len(options.Generators) != 0 || len(options.SDKs) != 0 {
		t.Fatalf("ordinary options leaked experimental candidates: %#v", options)
	}
	if _, err = catalog.Resolve(ResolveRequest{
		Packages:      []Requirement{{PackageID: "package.entitlement", VersionRange: "1.0.0"}},
		TemplateID:    "standard-a",
		TemplateRange: "0.1.0",
		Target:        "web",
		DeliveryMode:  "generated_source",
		Environment:   "test",
	}); err == nil || !(errors.Is(err, ErrUnknownPackage) || errors.Is(err, ErrUnknownTemplate)) {
		t.Fatalf("ordinary Resolve() error = %v, want unknown package or template", err)
	}
}
