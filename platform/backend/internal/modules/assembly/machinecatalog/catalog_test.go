package machinecatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

func TestProductionFeatureBlockCatalogOnlyMarksVerifiedBlocksReady(t *testing.T) {
	root := repositoryRoot(t)
	contracts := loadContracts(t)
	catalog, err := LoadBlockCatalog(filepath.Join(root, "platform", "contracts", "catalogs", "v1", "feature-blocks.json"), contracts)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version() != "1.4.1" || len(catalog.Checksum()) != len("sha256:")+64 {
		t.Fatalf("catalog identity = %s %s", catalog.Version(), catalog.Checksum())
	}
	ready := map[string]struct{}{
		"account.center":            {},
		"account.profile":           {},
		"account.security":          {},
		"auth.login":                {},
		"auth.recovery":             {},
		"auth.register":             {},
		"assembly.blueprint-wizard": {},
		"assembly.plan-review":      {},
		"assembly.run-status":       {},
		"assembly.upgrade-plan":     {},
		"entitlement.grant-panel":   {},
		"entitlement.history":       {},
		"entitlement.summary":       {},
		"entitlement.table":         {},
		"product.capability-menu":   {},
		"product.overview":          {},
		"product.switcher":          {},
		"product.table":             {},
		"identity.user-detail":      {},
		"identity.user-table":       {},
	}
	for blockID, definition := range catalog.byID {
		_, allowed := ready[blockID]
		if allowed && definition.Readiness != "ready" {
			t.Fatalf("verified block %q readiness = %q", blockID, definition.Readiness)
		}
		if !allowed && definition.Readiness != "not_ready" {
			t.Fatalf("unverified block %q readiness = %q", blockID, definition.Readiness)
		}
	}
	if err := catalog.Validate([]string{"entitlement.summary"}, "client"); err != nil {
		t.Fatalf("verified entitlement client block should be usable: %v", err)
	}
	if err := catalog.Validate([]string{"auth.login", "auth.register", "auth.recovery", "account.center", "account.profile", "account.security"}, "client"); err != nil {
		t.Fatalf("verified account client blocks should be usable: %v", err)
	}
	if err := catalog.Validate([]string{"assembly.blueprint-wizard", "assembly.plan-review"}, "admin"); err != nil {
		t.Fatalf("verified wizard blocks should be usable: %v", err)
	}
	if err := catalog.Validate([]string{"product.switcher", "product.overview", "product.capability-menu"}, "admin"); err != nil {
		t.Fatalf("verified product workspace blocks should be usable: %v", err)
	}
}

func TestFeatureBlockMachineCatalogMatchesDocumentedBlockIDs(t *testing.T) {
	root := repositoryRoot(t)
	catalog, err := LoadBlockCatalog(filepath.Join(root, "platform", "contracts", "catalogs", "v1", "feature-blocks.json"), loadContracts(t))
	if err != nil {
		t.Fatal(err)
	}
	want := documentedBlockIDs(t, filepath.Join(root, "docs", "feature-block-catalog.md"), filepath.Join(root, "docs", "client-ui-feature-block-catalog.md"))
	got := make([]string, 0, len(catalog.byID))
	for blockID := range catalog.byID {
		got = append(got, blockID)
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("machine/documented Feature Blocks differ\nmachine:\n%s\ndocumented:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestEmptyOrdinaryCatalogProducesDeterministicSnapshot(t *testing.T) {
	catalog := buildCatalog(t, nil, nil, ordinaryView)
	first, err := catalog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotSHA256 != second.SnapshotSHA256 || len(first.Packages) != 0 || len(first.Templates) != 0 {
		t.Fatalf("empty snapshots differ: %#v %#v", first, second)
	}
}

func TestCatalogOptionsAreStableFilteredAndRedacted(t *testing.T) {
	account := packageDocument(t, "package.account", "1.0.0", nil, nil, []string{"auth.login", "account.profile"}, ordinaryView)
	entitlement := packageDocument(t, "package.entitlement", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "^1.0.0"}}, nil, []string{"entitlement.summary"}, ordinaryView)
	template := templateDocument(t, "standard-a", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "^1.0.0"}, {PackageID: "package.entitlement", VersionRange: "^1.0.0"}}, []string{"auth.login", "account.profile", "entitlement.summary"}, ordinaryView)
	catalog := buildCatalog(t, []sourceDocument{entitlement, account}, []sourceDocument{template}, ordinaryView)
	if err := catalog.addTools("generator", []sourceDocument{toolDocument(t, "generator", "platform.generator", "1.0.0", ordinaryView)}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.addTools("sdk", []sourceDocument{toolDocument(t, "sdk", "platform.sdk", "1.0.0", ordinaryView)}); err != nil {
		t.Fatal(err)
	}

	first, err := catalog.Options("web", "generated_source", "test")
	if err != nil {
		t.Fatal(err)
	}
	second, err := catalog.Options("web", "generated_source", "test")
	if err != nil {
		t.Fatal(err)
	}
	if first.CatalogScope != "ordinary" || first.CatalogRevision != second.CatalogRevision || len(first.Packages) != 2 || len(first.Templates) != 1 || len(first.Generators) != 1 || len(first.SDKs) != 1 {
		t.Fatalf("options = %#v", first)
	}
	if first.Packages[0].PackageID != "package.account" || first.Packages[1].PackageID != "package.entitlement" || len(first.Packages[1].CompatibleTemplateRefs) != 1 {
		t.Fatalf("package options = %#v", first.Packages)
	}
	if first.Generators[0].ID != "platform.generator" || first.SDKs[0].ID != "platform.sdk" {
		t.Fatalf("tool options = %#v / %#v", first.Generators, first.SDKs)
	}
	if _, err := catalog.Options("invalid", "generated_source", "test"); !errors.Is(err, ErrCatalogState) {
		t.Fatalf("invalid filter error = %v", err)
	}
	empty, err := catalog.Options("web", "generated_source", "production")
	if err != nil || empty.Packages == nil || empty.Templates == nil || empty.Generators == nil || empty.SDKs == nil || len(empty.Packages)+len(empty.Templates)+len(empty.Generators)+len(empty.SDKs) != 0 {
		t.Fatalf("filtered empty options = %#v, %v", empty, err)
	}
}

func TestCatalogResolvesVerifiedTemplateWithoutCapabilityPackages(t *testing.T) {
	template := templateDocument(t, "blank-a", "1.0.0", []Requirement{}, []string{}, experimentalView)
	catalog := buildCatalog(t, nil, []sourceDocument{template}, experimentalView)
	resolution, err := catalog.Resolve(ResolveRequest{
		TemplateID: "blank-a", TemplateRange: "1.0.0", Target: "web",
		DeliveryMode: "generated_source", Environment: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Packages) != 0 || resolution.Template.TemplateID != "blank-a" || len(resolution.Snapshot.Packages) != 0 {
		t.Fatalf("blank resolution = %#v", resolution)
	}
}

func TestOrdinaryCatalogResolvesDependencyTemplateAndStableSnapshot(t *testing.T) {
	account := packageDocument(t, "package.account", "1.0.0", nil, nil, []string{"auth.login", "account.profile"}, ordinaryView)
	entitlement := packageDocument(t, "package.entitlement", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "^1.0.0"}}, nil, []string{"entitlement.summary"}, ordinaryView)
	template := templateDocument(t, "standard-a", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "^1.0.0"}, {PackageID: "package.entitlement", VersionRange: "^1.0.0"}}, []string{"auth.login", "account.profile", "entitlement.summary"}, ordinaryView)

	first := buildCatalog(t, []sourceDocument{entitlement, account}, []sourceDocument{template}, ordinaryView)
	second := buildCatalog(t, []sourceDocument{account, entitlement}, []sourceDocument{template}, ordinaryView)
	request := ResolveRequest{Packages: []Requirement{{PackageID: "package.entitlement", VersionRange: "^1.0.0"}}, TemplateID: "standard-a", TemplateRange: "^1.0.0", Target: "web", DeliveryMode: "generated_source", Environment: "test"}
	firstResolution, err := first.Resolve(request)
	if err != nil {
		t.Fatal(err)
	}
	secondResolution, err := second.Resolve(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstResolution.Packages) != 2 || firstResolution.Packages[0].PackageID != "package.account" || firstResolution.Packages[1].PackageID != "package.entitlement" {
		t.Fatalf("resolved packages = %#v", firstResolution.Packages)
	}
	if firstResolution.Snapshot.SnapshotSHA256 != secondResolution.Snapshot.SnapshotSHA256 {
		t.Fatalf("snapshot depends on discovery order: %s != %s", firstResolution.Snapshot.SnapshotSHA256, secondResolution.Snapshot.SnapshotSHA256)
	}
	if got := firstResolution.Snapshot.Packages[0].BackendCapabilities; len(got) != 1 || got[0] != "identity.user-session" {
		t.Fatalf("backend capability closure was not locked: %v", got)
	}
}

func TestExperimentalCatalogRequiresExplicitLoader(t *testing.T) {
	candidate := packageDocument(t, "package.candidate", "1.0.0", nil, nil, []string{"auth.login"}, experimentalView)
	template := templateDocument(t, "candidate-a", "1.0.0", []Requirement{{PackageID: "package.candidate", VersionRange: "^1.0.0"}}, []string{"auth.login"}, experimentalView)
	if _, err := build([]sourceDocument{candidate}, []sourceDocument{template}, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t), ordinaryView); !errors.Is(err, ErrCatalogState) {
		t.Fatalf("experimental entry entered ordinary view: %v", err)
	}
	catalog := buildCatalog(t, []sourceDocument{candidate}, []sourceDocument{template}, experimentalView)
	_, err := catalog.Resolve(ResolveRequest{Packages: []Requirement{{PackageID: "package.candidate", VersionRange: "*"}}, TemplateID: "candidate-a", TemplateRange: "*", Target: "web", DeliveryMode: "generated_source", Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPackageLifecycleCannotLeakIntoRuntimeCatalogs(t *testing.T) {
	for _, test := range []struct {
		name   string
		view   catalogView
		status string
		empty  bool
	}{
		{name: "contracted ordinary", view: ordinaryView, status: "contracted", empty: true},
		{name: "implemented experimental", view: experimentalView, status: "implemented", empty: true},
		{name: "deprecated ordinary", view: ordinaryView, status: "deprecated", empty: true},
		{name: "available experimental", view: experimentalView, status: "available"},
		{name: "verified ordinary", view: ordinaryView, status: "verified"},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := packageDocument(t, "package.account", "1.0.0", nil, nil, []string{"auth.login"}, test.view)
			document = mutateDocument(t, document, func(value map[string]any) {
				value["lifecycle_status"] = test.status
				if test.empty {
					value["availability"] = []any{}
				}
			}, true)
			_, err := build([]sourceDocument{document}, nil, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t), test.view)
			if err == nil || (test.empty && !errors.Is(err, ErrCatalogState)) {
				t.Fatalf("runtime catalog accepted or misclassified lifecycle %q: %v", test.status, err)
			}
		})
	}
}

func TestPackageRejectsProviderDeclaredRequiredAndOptional(t *testing.T) {
	document := packageDocument(t, "package.account", "1.0.0", nil, nil, []string{"auth.login"}, ordinaryView)
	document = mutateDocument(t, document, func(value map[string]any) {
		value["provider_requirements"] = []string{"identity.external.oidc"}
		value["optional_provider_requirements"] = []string{"identity.external.oidc"}
	}, true)
	_, err := build([]sourceDocument{document}, nil, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t), ordinaryView)
	if err == nil || !strings.Contains(err.Error(), "both required and optional") {
		t.Fatalf("overlapping provider declarations accepted: %v", err)
	}
}

func TestExperimentalStandardATemplateCatalog(t *testing.T) {
	root := repositoryRoot(t)
	blocks, err := LoadBlockCatalog(filepath.Join(root, "platform", "contracts", "catalogs", "v1", "feature-blocks.json"), loadContracts(t))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadExperimental(
		filepath.Join(root, "platform", "experimental", "capability-packages"),
		filepath.Join(root, "platform", "experimental", "templates"),
		loadContracts(t), accesscontrol.CurrentPermissionCatalog(), blocks,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"web", "desktop_webview"} {
		resolution, err := catalog.Resolve(ResolveRequest{
			TemplateID: "standard-a", TemplateRange: "0.1.0", Target: target,
			DeliveryMode: "generated_source", Environment: "test",
		})
		if err != nil {
			t.Fatalf("resolve %s: %v", target, err)
		}
		if len(resolution.Packages) != 0 || len(resolution.Template.Entrypoints) != 22 || len(resolution.Snapshot.Packages) != 0 {
			t.Fatalf("%s resolution closure = %#v", target, resolution)
		}
		matching, integration := 0, 0
		for _, entrypoint := range resolution.Template.Entrypoints {
			if entrypoint.Target != target {
				continue
			}
			matching++
			if entrypoint.Ownership == "integration" {
				integration++
			}
		}
		if matching != 11 || integration != 1 {
			t.Fatalf("%s entrypoints = %d, integration = %d", target, matching, integration)
		}
	}
}

func TestCatalogRejectsIntegrityIdentityPermissionBlockAndDuplicates(t *testing.T) {
	valid := packageDocument(t, "package.account", "1.0.0", nil, nil, []string{"auth.login"}, ordinaryView)
	tests := []struct {
		name string
		docs []sourceDocument
		want error
	}{
		{name: "duplicate", docs: []sourceDocument{valid, valid}, want: ErrDuplicatePackageVersion},
		{name: "identity", docs: []sourceDocument{withIdentity(valid, "package.other")}, want: ErrIdentityMismatch},
		{name: "checksum", docs: []sourceDocument{mutateDocument(t, valid, func(value map[string]any) { value["name"] = "tampered" }, false)}, want: ErrChecksumMismatch},
		{name: "content tree", docs: []sourceDocument{mutateAndRehashManifest(t, valid, func(value map[string]any) { value["content_tree_sha256"] = zeroDigest() })}, want: ErrContentTreeMismatch},
		{name: "permission", docs: []sourceDocument{mutateDocument(t, valid, func(value map[string]any) { value["required_permissions"] = []string{"manifest.injected"} }, true)}, want: accesscontrol.ErrUnknownPermission},
		{name: "block", docs: []sourceDocument{mutateDocument(t, valid, func(value map[string]any) { value["client_blocks"] = []string{"missing.block"} }, true)}, want: ErrUnknownBlock},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := build(test.docs, nil, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t), ordinaryView)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestResolverRejectsDependencyVersionConflictCycleAndPackageConflict(t *testing.T) {
	request := ResolveRequest{Packages: []Requirement{{PackageID: "package.entitlement", VersionRange: "*"}}, TemplateID: "standard-a", TemplateRange: "*", Target: "web", DeliveryMode: "generated_source", Environment: "test"}
	t.Run("unknown dependency", func(t *testing.T) {
		entitlement := packageDocument(t, "package.entitlement", "1.0.0", []Requirement{{PackageID: "package.missing", VersionRange: "*"}}, nil, []string{"entitlement.summary"}, ordinaryView)
		catalog := buildCatalog(t, []sourceDocument{entitlement}, []sourceDocument{templateDocument(t, "standard-a", "1.0.0", []Requirement{{PackageID: "package.entitlement", VersionRange: "*"}}, []string{"entitlement.summary"}, ordinaryView)}, ordinaryView)
		if _, err := catalog.Resolve(request); !errors.Is(err, ErrUnknownPackage) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("version conflict", func(t *testing.T) {
		account := packageDocument(t, "package.account", "1.0.0", nil, nil, []string{"auth.login"}, ordinaryView)
		entitlement := packageDocument(t, "package.entitlement", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: ">=2.0.0"}}, nil, []string{"entitlement.summary"}, ordinaryView)
		catalog := buildCatalog(t, []sourceDocument{account, entitlement}, []sourceDocument{templateDocument(t, "standard-a", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "*"}, {PackageID: "package.entitlement", VersionRange: "*"}}, []string{"auth.login", "entitlement.summary"}, ordinaryView)}, ordinaryView)
		if _, err := catalog.Resolve(request); !errors.Is(err, ErrVersionConflict) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		account := packageDocument(t, "package.account", "1.0.0", []Requirement{{PackageID: "package.entitlement", VersionRange: "*"}}, nil, []string{"auth.login"}, ordinaryView)
		entitlement := packageDocument(t, "package.entitlement", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "*"}}, nil, []string{"entitlement.summary"}, ordinaryView)
		catalog := buildCatalog(t, []sourceDocument{account, entitlement}, []sourceDocument{templateDocument(t, "standard-a", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "*"}, {PackageID: "package.entitlement", VersionRange: "*"}}, []string{"auth.login", "entitlement.summary"}, ordinaryView)}, ordinaryView)
		if _, err := catalog.Resolve(request); !errors.Is(err, ErrDependencyCycle) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("conflict", func(t *testing.T) {
		account := packageDocument(t, "package.account", "1.0.0", nil, []Requirement{{PackageID: "package.entitlement", VersionRange: "*"}}, []string{"auth.login"}, ordinaryView)
		entitlement := packageDocument(t, "package.entitlement", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "*"}}, nil, []string{"entitlement.summary"}, ordinaryView)
		catalog := buildCatalog(t, []sourceDocument{account, entitlement}, []sourceDocument{templateDocument(t, "standard-a", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "*"}, {PackageID: "package.entitlement", VersionRange: "*"}}, []string{"auth.login", "entitlement.summary"}, ordinaryView)}, ordinaryView)
		if _, err := catalog.Resolve(request); !errors.Is(err, ErrPackageConflict) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestResolverRejectsTargetDeliveryEnvironmentAndTemplateMismatch(t *testing.T) {
	account := packageDocument(t, "package.account", "1.0.0", nil, nil, []string{"auth.login", "account.profile"}, ordinaryView)
	baseTemplate := templateDocument(t, "standard-a", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "*"}}, []string{"auth.login", "account.profile"}, ordinaryView)
	baseRequest := ResolveRequest{Packages: []Requirement{{PackageID: "package.account", VersionRange: "*"}}, TemplateID: "standard-a", TemplateRange: "*", Target: "web", DeliveryMode: "generated_source", Environment: "test"}
	tests := []struct {
		name     string
		template sourceDocument
		request  ResolveRequest
		want     error
	}{
		{name: "target", template: baseTemplate, request: withRequest(baseRequest, "desktop_webview", "generated_source", "test"), want: ErrUnsupportedTarget},
		{name: "delivery", template: baseTemplate, request: withRequest(baseRequest, "web", "hosted", "test"), want: ErrUnsupportedDeliveryMode},
		{name: "environment", template: baseTemplate, request: withRequest(baseRequest, "web", "generated_source", "production"), want: ErrUnavailableEnvironment},
		{name: "missing block", template: mutateDocument(t, baseTemplate, func(value map[string]any) { value["supported_blocks"] = []string{"auth.login"} }, true), request: baseRequest, want: ErrTemplateMissingBlock},
		{name: "package compatibility", template: mutateDocument(t, baseTemplate, func(value map[string]any) {
			value["package_compatibility"] = []any{map[string]any{"package_id": "package.other", "version_range": "*"}}
		}, true), request: baseRequest, want: ErrTemplateIncompatible},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := buildCatalog(t, []sourceDocument{account}, []sourceDocument{test.template}, ordinaryView)
			_, err := catalog.Resolve(test.request)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestLoadOrdinaryChecksDiskLayoutAndContentFiles(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "capability-packages")
	templateRoot := filepath.Join(root, "templates")
	account := packageDocument(t, "package.account", "1.0.0", nil, nil, []string{"auth.login"}, ordinaryView)
	template := templateDocument(t, "standard-a", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "*"}}, []string{"auth.login"}, ordinaryView)
	writeDiskDocument(t, packageRoot, account, "manifest.json")
	writeDiskDocument(t, templateRoot, template, "template.json")
	if _, err := LoadOrdinary(packageRoot, templateRoot, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t)); err != nil {
		t.Fatal(err)
	}
	documents, err := discover(packageRoot, "manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 1 || documents[0].manifestName != "manifest.json" {
		t.Fatalf("discovered manifest name = %#v", documents)
	}
}

func TestCatalogValidatesManifestContentReferences(t *testing.T) {
	validPackage := packageDocument(t, "package.account", "1.0.0", nil, nil, []string{"auth.login"}, ordinaryView)
	validTemplate := templateDocument(t, "standard-a", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "*"}}, []string{"auth.login"}, ordinaryView)
	tests := []struct {
		name      string
		packages  []sourceDocument
		templates []sourceDocument
		contains  string
	}{
		{
			name: "config schema must be content",
			packages: []sourceDocument{mutateDocument(t, validPackage, func(value map[string]any) {
				value["config_schema_path"] = "contracts/unlisted.schema.json"
			}, true)},
			contains: "config_schema_path",
		},
		{
			name: "secret provider must be required",
			packages: []sourceDocument{mutateDocument(t, validPackage, func(value map[string]any) {
				value["secret_refs"] = []any{map[string]any{"provider": "notification.email", "key": "API_KEY", "environment": "test"}}
			}, true)},
			contains: "provider_requirements",
		},
		{
			name: "preview asset must be content",
			templates: []sourceDocument{mutateDocument(t, validTemplate, func(value map[string]any) {
				value["preview_assets"] = []string{"preview/missing.png"}
			}, true)},
			contains: "preview_asset",
		},
		{
			name: "source root must cover content",
			templates: []sourceDocument{mutateDocument(t, validTemplate, func(value map[string]any) {
				value["source_root"] = "other"
			}, true)},
			contains: "source_root",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := build(test.packages, test.templates, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t), ordinaryView)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want substring %q", err, test.contains)
			}
		})
	}

	t.Run("entrypoint is a generated target", func(t *testing.T) {
		catalog := buildCatalog(t, []sourceDocument{validPackage}, []sourceDocument{validTemplate}, ordinaryView)
		if got := catalog.templates["standard-a"][0].Entrypoints[0].Path; got != "src/generated/index.tsx" {
			t.Fatalf("entrypoint = %q", got)
		}
	})

	t.Run("generated outputs are retained", func(t *testing.T) {
		withOutput := mutateDocument(t, validPackage, func(value map[string]any) {
			value["generated_outputs"] = []any{map[string]any{
				"path": "src/generated/account.ts", "ownership": "generated", "source_path": "content/payload.txt",
				"source_sha256": payloadDigest(), "render_strategy": "strict_template", "content_type": "text",
			}}
		}, true)
		catalog := buildCatalog(t, []sourceDocument{withOutput}, nil, ordinaryView)
		outputs := catalog.packages["package.account"][0].GeneratedOutputs
		if len(outputs) != 1 || outputs[0].Path != "src/generated/account.ts" || outputs[0].Ownership != "generated" {
			t.Fatalf("generated outputs = %#v", outputs)
		}
	})
}

func TestDiskCatalogRejectsIncompleteOrUnsafeContentTree(t *testing.T) {
	newRoots := func(t *testing.T) (string, string, sourceDocument, sourceDocument) {
		t.Helper()
		root := t.TempDir()
		packageRoot := filepath.Join(root, "capability-packages")
		templateRoot := filepath.Join(root, "templates")
		account := packageDocument(t, "package.account", "1.0.0", nil, nil, []string{"auth.login"}, ordinaryView)
		template := templateDocument(t, "standard-a", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "*"}}, []string{"auth.login"}, ordinaryView)
		writeDiskDocument(t, packageRoot, account, "manifest.json")
		writeDiskDocument(t, templateRoot, template, "template.json")
		return packageRoot, templateRoot, account, template
	}

	t.Run("extra unlisted file", func(t *testing.T) {
		packageRoot, templateRoot, account, _ := newRoots(t)
		extra := filepath.Join(packageRoot, account.identity, account.version, "content", "extra.txt")
		if err := os.WriteFile(extra, []byte("extra"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadOrdinary(packageRoot, templateRoot, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t))
		if !errors.Is(err, ErrInvalidLayout) || !strings.Contains(err.Error(), "unlisted content file") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("symbolic link", func(t *testing.T) {
		packageRoot, templateRoot, account, _ := newRoots(t)
		versionRoot := filepath.Join(packageRoot, account.identity, account.version)
		link := filepath.Join(versionRoot, "content", "payload-link.txt")
		if err := os.Symlink(filepath.Join(versionRoot, "content", "payload.txt"), link); err != nil {
			t.Skipf("symbolic links are unavailable: %v", err)
		}
		_, err := LoadOrdinary(packageRoot, templateRoot, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t))
		if !errors.Is(err, ErrInvalidLayout) || !strings.Contains(err.Error(), "symbolic links") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("case collision in declared tree", func(t *testing.T) {
		valid := packageDocument(t, "package.account", "1.0.0", nil, nil, []string{"auth.login"}, ordinaryView)
		colliding := mutateDocument(t, valid, func(value map[string]any) {
			value["content_files"] = contentFiles("content/payload.txt", "content/PAYLOAD.txt", "contracts/config.schema.json")
		}, true)
		_, err := build([]sourceDocument{colliding}, nil, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t), ordinaryView)
		if !errors.Is(err, ErrInvalidLayout) || !strings.Contains(err.Error(), "collide by case") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestResolverBacktracksAcrossRuntimeAndTemplateConstraints(t *testing.T) {
	request := ResolveRequest{Packages: []Requirement{{PackageID: "package.account", VersionRange: "*"}}, TemplateID: "standard-a", TemplateRange: "*", Target: "web", DeliveryMode: "generated_source", Environment: "test"}
	accountV1 := packageDocument(t, "package.account", "1.0.0", nil, nil, []string{"auth.login"}, ordinaryView)
	baseTemplateV1 := templateDocument(t, "standard-a", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "*"}}, []string{"auth.login"}, ordinaryView)

	t.Run("package availability falls back", func(t *testing.T) {
		accountV2 := mutateAvailabilityEnvironment(t, packageDocument(t, "package.account", "2.0.0", nil, nil, []string{"auth.login"}, ordinaryView), "production")
		resolution, err := buildCatalog(t, []sourceDocument{accountV2, accountV1}, []sourceDocument{baseTemplateV1}, ordinaryView).Resolve(request)
		if err != nil {
			t.Fatal(err)
		}
		if resolution.Packages[0].Version != "1.0.0" {
			t.Fatalf("selected package version = %s", resolution.Packages[0].Version)
		}
	})

	t.Run("package conflict falls back", func(t *testing.T) {
		accountV2 := packageDocument(t, "package.account", "2.0.0", nil, []Requirement{{PackageID: "package.entitlement", VersionRange: "*"}}, []string{"auth.login"}, ordinaryView)
		entitlement := packageDocument(t, "package.entitlement", "1.0.0", nil, nil, []string{"entitlement.summary"}, ordinaryView)
		template := templateDocument(t, "standard-a", "1.0.0", []Requirement{{PackageID: "package.account", VersionRange: "*"}, {PackageID: "package.entitlement", VersionRange: "*"}}, []string{"auth.login", "entitlement.summary"}, ordinaryView)
		conflictRequest := request
		conflictRequest.Packages = append(conflictRequest.Packages, Requirement{PackageID: "package.entitlement", VersionRange: "*"})
		resolution, err := buildCatalog(t, []sourceDocument{accountV2, accountV1, entitlement}, []sourceDocument{template}, ordinaryView).Resolve(conflictRequest)
		if err != nil {
			t.Fatal(err)
		}
		if resolution.Packages[0].PackageID != "package.account" || resolution.Packages[0].Version != "1.0.0" {
			t.Fatalf("selected packages = %#v", resolution.Packages)
		}
	})

	t.Run("package template declaration falls back", func(t *testing.T) {
		accountV2 := mutateDocument(t, packageDocument(t, "package.account", "2.0.0", nil, nil, []string{"auth.login"}, ordinaryView), func(value map[string]any) {
			value["ui_template_compatibility"] = []any{map[string]any{"template_id": "other-a", "version_range": "*"}}
		}, true)
		resolution, err := buildCatalog(t, []sourceDocument{accountV2, accountV1}, []sourceDocument{baseTemplateV1}, ordinaryView).Resolve(request)
		if err != nil {
			t.Fatal(err)
		}
		if resolution.Packages[0].Version != "1.0.0" {
			t.Fatalf("selected package version = %s", resolution.Packages[0].Version)
		}
	})

	templateCases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "template availability", mutate: func(value map[string]any) { setAvailabilityEnvironment(value, "production") }},
		{name: "template package compatibility", mutate: func(value map[string]any) {
			value["package_compatibility"] = []any{map[string]any{"package_id": "package.other", "version_range": "*"}}
		}},
		{name: "template blocks", mutate: func(value map[string]any) { value["supported_blocks"] = []string{"account.profile"} }},
		{name: "template entrypoint", mutate: func(value map[string]any) {
			value["supported_targets"] = []string{"web", "desktop_webview"}
			value["entrypoints"] = []any{map[string]any{
				"target": "desktop_webview", "delivery_mode": "generated_source", "path": "src/generated/desktop.tsx", "ownership": "generated",
				"source_path": "template/payload.txt", "source_sha256": payloadDigest(), "render_strategy": "strict_template", "content_type": "text",
			}}
		}},
	}
	for _, test := range templateCases {
		t.Run(test.name+" falls back", func(t *testing.T) {
			templateV2 := mutateDocument(t, templateDocument(t, "standard-a", "2.0.0", []Requirement{{PackageID: "package.account", VersionRange: "*"}}, []string{"auth.login"}, ordinaryView), test.mutate, true)
			resolution, err := buildCatalog(t, []sourceDocument{accountV1}, []sourceDocument{templateV2, baseTemplateV1}, ordinaryView).Resolve(request)
			if err != nil {
				t.Fatal(err)
			}
			if resolution.Template.Version != "1.0.0" {
				t.Fatalf("selected template version = %s", resolution.Template.Version)
			}
		})
	}
}

func buildCatalog(t *testing.T, packages, templates []sourceDocument, view catalogView) *Catalog {
	t.Helper()
	catalog, err := build(packages, templates, loadContracts(t), accesscontrol.CurrentPermissionCatalog(), readyBlocks(t), view)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestTrustedToolCatalogLoadsAndFailsClosed(t *testing.T) {
	registry := loadContracts(t)
	blocks := readyBlocks(t)

	t.Run("loads sealed tool and snapshots scope", func(t *testing.T) {
		root := t.TempDir()
		generatorRoot := filepath.Join(root, "generators")
		writeDiskDocument(t, generatorRoot, toolDocument(t, "generator", "platform.generator", "1.0.0", ordinaryView), "manifest.json")
		catalog, err := LoadOrdinaryWithTools(filepath.Join(root, "packages"), filepath.Join(root, "templates"), generatorRoot, filepath.Join(root, "sdks"), registry, accesscontrol.CurrentPermissionCatalog(), blocks)
		if err != nil {
			t.Fatal(err)
		}
		tool, err := catalog.ResolveTool("generator", "platform.generator", "1.0.0", "web", "generated_source", "test")
		if err != nil || tool.ManifestSHA256 == "" {
			t.Fatalf("ResolveTool() = %#v, %v", tool, err)
		}
		snapshot, err := catalog.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.CatalogScope != "ordinary" || len(snapshot.Generators) != 1 || len(snapshot.SDKs) != 0 {
			t.Fatalf("tool snapshot = %#v", snapshot)
		}
		if _, err := catalog.ResolveTool("generator", "platform.generator", "1.0.0", "desktop_webview", "generated_source", "test"); !errors.Is(err, ErrToolIncompatible) {
			t.Fatalf("incompatible target error = %v, want %v", err, ErrToolIncompatible)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
		want   error
	}{
		{name: "wrong scope", mutate: func(value map[string]any) { value["catalog_scope"] = "experimental" }, want: ErrCatalogState},
		{name: "unregistered builtin adapter", mutate: func(value map[string]any) {
			value["execution"] = map[string]any{"mode": "builtin_adapter", "adapter_id": "assembly.untrusted"}
		}, want: ErrUnknownTool},
		{name: "unsafe executable path", mutate: func(value map[string]any) {
			value["execution"] = map[string]any{"mode": "node", "path": "../outside.js", "sha256": payloadDigest()}
		}, want: nil},
		{name: "unsealed evidence", mutate: func(value map[string]any) {
			value["evidence"].([]any)[0].(map[string]any)["sha256"] = zeroDigest()
		}, want: ErrChecksumMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			document := mutateDocument(t, toolDocument(t, "generator", "platform.generator", "1.0.0", ordinaryView), test.mutate, true)
			writeDiskDocument(t, filepath.Join(root, "generators"), document, "manifest.json")
			_, err := LoadOrdinaryWithTools(filepath.Join(root, "packages"), filepath.Join(root, "templates"), filepath.Join(root, "generators"), filepath.Join(root, "sdks"), registry, accesscontrol.CurrentPermissionCatalog(), blocks)
			if err == nil || (test.want != nil && !errors.Is(err, test.want)) {
				t.Fatalf("LoadOrdinaryWithTools() error = %v, want %v", err, test.want)
			}
		})
	}
}

func toolDocument(t *testing.T, kind, id, version string, view catalogView) sourceDocument {
	t.Helper()
	value := map[string]any{
		"schema_version": "1.0.0", "tool_kind": kind, "tool_id": id, "version": version, "name": id,
		"catalog_scope": view.visibility, "readiness": view.readiness,
		"supported_targets": []string{"web"}, "supported_delivery_modes": []string{"generated_source"}, "supported_environments": []string{"test"},
		"protocol": map[string]any{"id": "assembly." + kind, "version": "1.0.0"}, "platform_contract_range": "^1.0.0",
		"execution":     map[string]any{"mode": "builtin_adapter", "adapter_id": map[string]string{"generator": "assembly.pure-renderer", "sdk": "assembly.client-sdk"}[kind]},
		"evidence":      []any{map[string]any{"type": "test_report", "target": "web", "delivery_mode": "generated_source", "environment": "test", "status": "passed", "path": "evidence/test-report.json", "sha256": payloadDigest()}},
		"content_files": contentFiles("evidence/test-report.json"), "content_tree_sha256": zeroDigest(), "manifest_sha256": zeroDigest(),
	}
	return finalizeDocument(t, value, id, version)
}

func packageDocument(t *testing.T, packageID, version string, dependencies, conflicts []Requirement, clientBlocks []string, view catalogView) sourceDocument {
	t.Helper()
	compatibility := strings.TrimPrefix(packageID, "package.")
	dependencies = append(make([]Requirement, 0, len(dependencies)), dependencies...)
	conflicts = append(make([]Requirement, 0, len(conflicts)), conflicts...)
	lifecycleStatus := "available"
	if view.visibility == "experimental" {
		lifecycleStatus = "verified"
	}
	value := map[string]any{
		"schema_version": "1.0.0", "package_id": packageID, "version": version, "name": packageID, "user_value": "Reusable capability package.",
		"lifecycle_status": lifecycleStatus,
		"availability":     []any{map[string]any{"target": "web", "delivery_mode": "generated_source", "environments": []string{"test"}, "visibility": view.visibility, "readiness": view.readiness, "evidence_refs": []string{"artifacts/reviews/test/evidence.md"}}},
		"dependencies":     dependencies, "conflicts": conflicts, "supported_targets": []string{"web"}, "supported_delivery_modes": []string{"generated_source"},
		"required_permissions": []string{"identity.manage"}, "backend_capabilities": []string{"identity.user-session"}, "migrations": []string{}, "events": []string{"identity.changed.v1"}, "audit_actions": []string{"identity.changed"},
		"admin_blocks": []string{"identity.user-table"}, "client_blocks": clientBlocks, "hosted_routes": []string{"hosted.auth"},
		"ui_template_compatibility": []any{map[string]any{"template_id": templateForPackage(packageID), "version_range": "*"}},
		"public_api_operations":     []string{"getCurrentUser"}, "sdk_modules": []string{"sdk." + compatibility}, "sdk_methods": []string{"getCurrentUser"}, "stable_errors": []string{"CAPABILITY_ERROR"},
		"config_schema_path": "contracts/config.schema.json", "secret_refs": []any{}, "provider_requirements": []string{}, "optional_provider_requirements": []string{}, "generated_outputs": []any{},
		"source_locations": []string{"platform/backend/internal/modules/identity"}, "extension_points": []string{"capability.slot"}, "test_paths": []string{"tests/golden.test.ts"}, "smoke_tests": []string{"st.capability"}, "documentation_paths": []string{"docs/package.md"},
		"upgrade_policy": map[string]any{"strategy": "compatible", "guide_path": "docs/upgrade.md"}, "rollback_policy": map[string]any{"strategy": "automatic", "guide_path": "docs/rollback.md"},
		"data_retention": []any{map[string]any{"data_set": "capability.data", "policy": "Retain for the active product lifecycle.", "guide_path": "docs/data-retention.md"}},
		"content_files":  contentFiles("content/payload.txt", "contracts/config.schema.json"), "content_tree_sha256": zeroDigest(), "manifest_sha256": zeroDigest(),
	}
	return finalizeDocument(t, value, packageID, version)
}

func templateDocument(t *testing.T, templateID, version string, compatibility []Requirement, blocks []string, view catalogView) sourceDocument {
	t.Helper()
	value := map[string]any{
		"schema_version": "1.0.0", "template_id": templateID, "version": version, "name": templateID,
		"availability":      []any{map[string]any{"target": "web", "delivery_mode": "generated_source", "environments": []string{"test"}, "visibility": view.visibility, "readiness": view.readiness, "evidence_refs": []string{"artifacts/reviews/test/template.md"}}},
		"supported_targets": []string{"web"}, "supported_delivery_modes": []string{"generated_source"}, "supported_blocks": blocks, "package_compatibility": compatibility,
		"entrypoints": []any{map[string]any{
			"target": "web", "delivery_mode": "generated_source", "path": "src/generated/index.tsx", "ownership": "generated",
			"source_path": "template/payload.txt", "source_sha256": payloadDigest(), "render_strategy": "strict_template", "content_type": "text",
		}}, "source_root": "template", "preview_assets": []string{},
		"content_files": contentFiles("template/payload.txt"), "content_tree_sha256": zeroDigest(), "manifest_sha256": zeroDigest(),
	}
	return finalizeDocument(t, value, templateID, version)
}

func contentFiles(paths ...string) []any {
	files := make([]any, 0, len(paths))
	for _, path := range paths {
		files = append(files, map[string]any{"path": path, "sha256": payloadDigest(), "kind": "file"})
	}
	return files
}

func payloadDigest() string {
	digest := sha256.Sum256([]byte("payload"))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func finalizeDocument(t *testing.T, value map[string]any, identity, version string) sourceDocument {
	t.Helper()
	files, err := json.Marshal(value["content_files"])
	if err != nil {
		t.Fatal(err)
	}
	treeDigest, err := machinecontract.Digest(files)
	if err != nil {
		t.Fatal(err)
	}
	value["content_tree_sha256"] = "sha256:" + treeDigest
	value["manifest_sha256"] = zeroDigest()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest, err := machinecontract.DigestWithoutTopLevelField(contents, "manifest_sha256")
	if err != nil {
		t.Fatal(err)
	}
	value["manifest_sha256"] = manifestDigest
	contents, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return sourceDocument{contents: contents, identity: identity, version: version}
}

func mutateDocument(t *testing.T, document sourceDocument, mutate func(map[string]any), recompute bool) sourceDocument {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document.contents, &value); err != nil {
		t.Fatal(err)
	}
	mutate(value)
	if recompute {
		updated := finalizeDocument(t, value, document.identity, document.version)
		updated.versionRoot = document.versionRoot
		return updated
	}
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	document.contents = contents
	return document
}

func mutateAndRehashManifest(t *testing.T, document sourceDocument, mutate func(map[string]any)) sourceDocument {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document.contents, &value); err != nil {
		t.Fatal(err)
	}
	mutate(value)
	value["manifest_sha256"] = zeroDigest()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := machinecontract.DigestWithoutTopLevelField(contents, "manifest_sha256")
	if err != nil {
		t.Fatal(err)
	}
	value["manifest_sha256"] = digest
	contents, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	document.contents = contents
	return document
}

func mutateAvailabilityEnvironment(t *testing.T, document sourceDocument, environment string) sourceDocument {
	t.Helper()
	return mutateDocument(t, document, func(value map[string]any) {
		setAvailabilityEnvironment(value, environment)
	}, true)
}

func setAvailabilityEnvironment(value map[string]any, environment string) {
	entries := value["availability"].([]any)
	entries[0].(map[string]any)["environments"] = []string{environment}
}

func withIdentity(document sourceDocument, identity string) sourceDocument {
	document.identity = identity
	return document
}

func withRequest(request ResolveRequest, target, mode, environment string) ResolveRequest {
	request.Target, request.DeliveryMode, request.Environment = target, mode, environment
	return request
}

func writeDiskDocument(t *testing.T, root string, document sourceDocument, manifestName string) {
	t.Helper()
	versionRoot := filepath.Join(root, document.identity, document.version)
	var value struct {
		ContentFiles []ContentFile `json:"content_files"`
	}
	if err := json.Unmarshal(document.contents, &value); err != nil {
		t.Fatal(err)
	}
	for _, file := range value.ContentFiles {
		path := filepath.Join(versionRoot, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(versionRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionRoot, manifestName), document.contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func loadContracts(t *testing.T) *machinecontract.Registry {
	t.Helper()
	registry, err := machinecontract.LoadDirectory(filepath.Join(repositoryRoot(t), "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func readyBlocks(t *testing.T) *BlockCatalog {
	t.Helper()
	catalog, err := NewBlockCatalog("1.0.0", []BlockDefinition{
		{BlockID: "account.profile", Surface: "client", Readiness: "ready"},
		{BlockID: "auth.login", Surface: "client", Readiness: "ready"},
		{BlockID: "entitlement.summary", Surface: "client", Readiness: "ready"},
		{BlockID: "identity.user-table", Surface: "admin", Readiness: "ready"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	directory := filepath.Dir(filename)
	for {
		candidate := filepath.Join(directory, "platform", "contracts", "schemas", "v1")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root not found")
		}
		directory = parent
	}
}

func templateForPackage(packageID string) string {
	if packageID == "package.candidate" {
		return "candidate-a"
	}
	return "standard-a"
}

func zeroDigest() string { return "sha256:" + strings.Repeat("0", 64) }

func documentedBlockIDs(t *testing.T, paths ...string) []string {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^\| ([a-z][a-z0-9]*(?:[._-][a-z0-9]+)+) \|`)
	seen := make(map[string]struct{})
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(contents), -1) {
			blockID := match[1]
			if blockID == "block_id" || blockID == "component_id" || blockID == "hosted_route_id" || blockID == "package_id" || blockID == "shell_id" || strings.HasPrefix(blockID, "package.") || strings.HasPrefix(blockID, "client.") || strings.HasPrefix(blockID, "hosted.") {
				continue
			}
			seen[blockID] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for blockID := range seen {
		result = append(result, blockID)
	}
	sort.Strings(result)
	return result
}
