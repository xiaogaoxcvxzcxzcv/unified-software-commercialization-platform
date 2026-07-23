package generation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecatalog"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
	"platform.local/capability-platform/backend/internal/modules/assembly/planning"
)

func TestG2C02ExperimentalCatalogPlanRendersAndCommitsHandoffOutputs(t *testing.T) {
	repositoryRoot := findAccountPackageRepositoryRoot(t)
	registry, err := machinecontract.LoadDirectory(filepath.Join(repositoryRoot, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := machinecatalog.LoadBlockCatalog(filepath.Join(repositoryRoot, "platform", "contracts", "catalogs", "v1", "feature-blocks.json"), registry)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := machinecatalog.LoadExperimentalWithToolsAndExtensions(
		filepath.Join(repositoryRoot, "platform", "experimental", "capability-packages"),
		filepath.Join(repositoryRoot, "platform", "experimental", "templates"),
		filepath.Join(repositoryRoot, "platform", "experimental", "tools", "generators"),
		filepath.Join(repositoryRoot, "platform", "experimental", "tools", "sdks"),
		filepath.Join(repositoryRoot, "platform", "experimental", "extensions"),
		registry, accesscontrol.CurrentPermissionCatalog(), blocks,
	)
	if err != nil {
		t.Fatal(err)
	}
	blueprintDocument := g2c02ExperimentalBlueprint(t, repositoryRoot, registry)
	blueprintDigest, err := machinecontract.Digest(blueprintDocument)
	if err != nil {
		t.Fatal(err)
	}
	blueprint := core.Blueprint{BlueprintID: "bp_video-brain", Revision: 1, Document: blueprintDocument, ContentSHA256: "sha256:" + blueprintDigest}
	planned, err := planning.New(catalog).BuildPlan(context.Background(), blueprint, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate("assembly-plan", planned.Document); err != nil {
		t.Fatalf("G2C-02 real experimental plan schema: %v", err)
	}
	applications := g2c02RuntimeApplications(t, planned.Document)

	targetRoot := t.TempDir()
	artifactRoot := t.TempDir()
	input, previous, err := BuildRequest(targetRoot, RequestSpec{
		WorkspaceRef: "workspace.g2c02.a", RunID: "run.g2c02.a", RunCreatedAt: time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC),
		Product: ArtifactProduct{
			ProductID: "prod.g2c02.a", OfficialTenantID: "tenant.g2c02.a", Applications: applications,
		},
		Blueprint:         ArtifactBlueprint{BlueprintID: blueprint.BlueprintID, Version: blueprint.Revision, Checksum: blueprint.ContentSHA256},
		BlueprintDocument: blueprintDocument,
		PlanDocument:      planned.Document,
	})
	if err != nil {
		t.Fatal(err)
	}
	renderer := NewPureRenderer(catalog)
	rendered, err := renderer.Render(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	renderedByPath := make(map[string]RenderedFile, len(rendered.Files))
	for _, file := range rendered.Files {
		renderedByPath[file.Path] = file
	}
	for _, path := range []string{"generated-products/video-brain/AGENTS.md", "generated-products/video-brain/docs/software-development-handoff.md"} {
		file, ok := renderedByPath[path]
		if !ok {
			t.Fatalf("G2C-02 handoff output %s was not rendered", path)
		}
		if file.SourceID != "standard-a" || file.SourceVersion != "0.1.0" || file.Ownership != "generated" || len(file.Bytes) == 0 {
			t.Fatalf("G2C-02 handoff output %s was not sealed by standard-a: %#v", path, file.OutputSpec)
		}
	}

	artifactStore, err := NewArtifactStore(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewExecutor(renderer, NewFileCommitter(), artifactStore, registry)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := executor.Execute(context.Background(), targetRoot, input, ProjectLock{}, previous)
	if err != nil {
		t.Fatalf("G2C-02 experimental execution failed: %v diagnostics=%#v", err, outcome.Failure.Diagnostics)
	}
	if !outcome.Commit.AtomicCommitCompleted || !outcome.Commit.StagingCleanupCompleted || outcome.Bundle.AssemblyManifest == nil || outcome.Bundle.GeneratedLock == nil {
		t.Fatalf("G2C-02 commit outcome incomplete: %#v", outcome.Commit)
	}
	for _, path := range []string{"generated-products/video-brain/AGENTS.md", "generated-products/video-brain/docs/software-development-handoff.md"} {
		content, readErr := os.ReadFile(filepath.Join(targetRoot, filepath.FromSlash(path)))
		if readErr != nil || len(content) == 0 {
			t.Fatalf("G2C-02 committed handoff output %s is missing: %v", path, readErr)
		}
	}
}

func g2c02RuntimeApplications(t *testing.T, plan json.RawMessage) []ArtifactApplication {
	t.Helper()
	var value struct {
		Applications []struct {
			ApplicationID string `json:"application_id"`
		} `json:"applications"`
	}
	if err := json.Unmarshal(plan, &value); err != nil {
		t.Fatal(err)
	}
	if len(value.Applications) == 0 {
		t.Fatal("G2C-02 plan has no applications")
	}
	applications := make([]ArtifactApplication, 0, len(value.Applications))
	for _, application := range value.Applications {
		applications = append(applications, ArtifactApplication{
			PlanApplicationID: application.ApplicationID,
			ApplicationID:     "app.g2c02." + application.ApplicationID,
		})
	}
	return applications
}

func g2c02ExperimentalBlueprint(t *testing.T, repositoryRoot string, registry *machinecontract.Registry) json.RawMessage {
	t.Helper()
	document, err := os.ReadFile(filepath.Join(repositoryRoot, "platform", "contracts", "schemas", "fixtures", "catalog-blueprint", "product-blueprint.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	value["packages"] = []any{
		map[string]any{"package_id": "package.account", "version": "1.0.0"},
		map[string]any{"package_id": "package.entitlement", "version": "1.0.0"},
	}
	for _, item := range value["applications"].([]any) {
		application := item.(map[string]any)
		application["environment"] = "test"
		ui := application["ui"].(map[string]any)
		ui["template_id"] = "standard-a"
		ui["version"] = "0.1.0"
		ui["delivery_mode"] = "generated_source"
	}
	value["provider_refs"] = []any{map[string]any{
		"provider": "notification.security", "environment": "test",
		"config_ref": "configs/notification-security.json", "secret_refs": []any{},
	}}
	value["extensions"] = []any{map[string]any{
		"extension_id": "extension.editor-tools", "version": "1.0.0",
		"manifest_path": "extension.editor-tools/1.0.0/manifest.json",
	}}
	document, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate("product-blueprint", document); err != nil {
		t.Fatal(err)
	}
	return document
}
