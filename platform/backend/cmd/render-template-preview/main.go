package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/accesscontrol"
	"platform.local/capability-platform/backend/internal/modules/assembly/generation"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecatalog"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

func main() {
	repositoryRoot := flag.String("repository-root", "", "repository root containing platform and .runtime")
	templateID := flag.String("template-id", "standard-a", "experimental template id")
	templateVersion := flag.String("template-version", "0.1.0", "exact experimental template version")
	target := flag.String("target", "web", "template target")
	output := flag.String("output", "", "fresh output directory below repository .runtime")
	productName := flag.String("product-name", "Standard A Preview", "preview product name")
	flag.Parse()

	if err := run(*repositoryRoot, *templateID, *templateVersion, *target, *output, *productName); err != nil {
		fmt.Fprintln(os.Stderr, "render template preview:", err)
		os.Exit(1)
	}
}

func run(repositoryRoot, templateID, templateVersion, target, output, productName string) error {
	if strings.TrimSpace(repositoryRoot) == "" || strings.TrimSpace(output) == "" || strings.TrimSpace(productName) == "" {
		return errors.New("repository root, output, and product name are required")
	}
	repositoryRoot, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return err
	}
	runtimeRoot := filepath.Join(repositoryRoot, ".runtime")
	if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
		return err
	}
	output, err = filepath.Abs(output)
	if err != nil || !within(output, runtimeRoot) {
		return errors.New("output must be below repository .runtime")
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		return errors.New("output must be a fresh empty directory")
	}

	contracts, err := machinecontract.LoadDirectory(filepath.Join(repositoryRoot, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		return err
	}
	blocks, err := machinecatalog.LoadBlockCatalog(filepath.Join(repositoryRoot, "platform", "contracts", "catalogs", "v1", "feature-blocks.json"), contracts)
	if err != nil {
		return err
	}
	catalog, err := machinecatalog.LoadExperimentalWithTools(
		filepath.Join(repositoryRoot, "platform", "experimental", "capability-packages"),
		filepath.Join(repositoryRoot, "platform", "experimental", "templates"),
		filepath.Join(repositoryRoot, "platform", "experimental", "tools", "generators"),
		filepath.Join(repositoryRoot, "platform", "experimental", "tools", "sdks"),
		contracts, accesscontrol.CurrentPermissionCatalog(), blocks,
	)
	if err != nil {
		return err
	}
	resolution, err := catalog.Resolve(machinecatalog.ResolveRequest{
		TemplateID: templateID, TemplateRange: templateVersion, Target: target,
		DeliveryMode: "generated_source", Environment: "test",
	})
	if err != nil {
		return err
	}
	if len(resolution.Packages) != 0 {
		return errors.New("preview command only accepts a package-free template")
	}

	outputs := make([]generation.OutputSpec, 0)
	for _, entrypoint := range resolution.Template.Entrypoints {
		if entrypoint.Target != target || entrypoint.DeliveryMode != "generated_source" {
			continue
		}
		var merge *generation.MergeSpec
		if entrypoint.Merge != nil {
			merge = &generation.MergeSpec{Strategy: entrypoint.Merge.Strategy, RegionID: entrypoint.Merge.RegionID, CommentPrefix: entrypoint.Merge.CommentPrefix}
		}
		outputs = append(outputs, generation.OutputSpec{
			Path: entrypoint.Path, Ownership: entrypoint.Ownership,
			SourceID: resolution.Template.TemplateID, SourceVersion: resolution.Template.Version,
			SourcePath: entrypoint.SourcePath, SourceSHA256: entrypoint.SourceSHA256,
			RenderStrategy: entrypoint.RenderStrategy, ContentType: entrypoint.ContentType, Merge: merge,
		})
	}
	if len(outputs) == 0 {
		return errors.New("template has no entrypoints for target")
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Path < outputs[j].Path })

	planChecksum := digest([]byte("template-preview-plan\x00" + templateID + "\x00" + templateVersion + "\x00" + target))
	tool := generation.Tool{GeneratorID: "platform.generator", Version: "1.0.0", Checksum: digest([]byte("platform.generator@1.0.0"))}
	blueprint, err := json.Marshal(map[string]any{
		"blueprint_id": "template.preview", "version": 1,
		"product": map[string]any{"name": productName, "code": "template-preview"},
	})
	if err != nil {
		return err
	}
	plan, err := json.Marshal(map[string]any{
		"plan_id": "template.preview." + target, "plan_checksum": planChecksum,
		"blueprint_id": "template.preview", "blueprint_version": 1,
		"catalog_snapshot": map[string]any{"scope": resolution.Snapshot.CatalogScope, "checksum": resolution.Snapshot.SnapshotSHA256},
		"generator":        tool, "expected_outputs": outputs,
		"required_secret_refs": []generation.SecretRef{}, "packages": []any{}, "sdks": []any{},
		"applications": []any{map[string]any{
			"application_id": "preview." + target,
			"template":       map[string]any{"template_id": resolution.Template.TemplateID, "version": resolution.Template.Version, "checksum": resolution.Template.ManifestSHA256},
		}},
	})
	if err != nil {
		return err
	}

	emptyLock := generation.ProjectLock{SchemaVersion: "1.0.0", Files: []generation.LockedFile{}}
	snapshot, err := generation.InspectTarget(output, emptyLock)
	if err != nil {
		return err
	}
	request := generation.Request{
		SchemaVersion: "1.0.0", RequestID: "template.preview." + target, Operation: "generate", WorkspaceRef: "template.preview",
		PlanChecksum: planChecksum, TargetSnapshotChecksum: snapshot.Checksum, Generator: tool,
		Inputs:         generation.InputPaths{BlueprintPath: "platform-inputs/blueprint.json", PlanPath: "platform-inputs/plan.json"},
		DesiredOutputs: outputs, ExistingFiles: snapshot.Files, ProtectedPaths: []string{"src/custom"}, SecretRefs: []generation.SecretRef{},
		StagingPath: "platform-staging", RollbackPointPath: "platform-rollback/point.json", ConflictPolicy: "stop",
		Determinism: generation.Determinism{Timezone: "UTC", Locale: "C", SortOrder: "bytewise"},
	}
	rendered, err := generation.NewPureRenderer(catalog).Render(context.Background(), generation.Input{Request: request, Blueprint: blueprint, Plan: plan})
	if err != nil {
		return err
	}
	prepared, err := generation.PrepareTarget(output, request, rendered, emptyLock)
	if err != nil {
		return err
	}
	committed, err := generation.NewFileCommitter().Commit(context.Background(), output, request, prepared)
	if err != nil {
		return err
	}
	if !committed.AtomicCommitCompleted || len(committed.FilesWritten) != len(outputs) {
		return errors.New("preview output did not commit completely")
	}
	fmt.Printf("rendered template=%s version=%s target=%s files=%d output=%s\n", templateID, templateVersion, target, len(outputs), output)
	return nil
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func within(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
