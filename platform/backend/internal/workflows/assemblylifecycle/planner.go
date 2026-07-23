package assemblylifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/assembly/generation"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
	"platform.local/capability-platform/backend/internal/modules/assembly/planning"
)

type CatalogLifecyclePlanBuilder struct {
	resolver *TrustedContextResolver
	now      func() time.Time
}

func NewCatalogLifecyclePlanBuilder(resolver *TrustedContextResolver, now func() time.Time) (*CatalogLifecyclePlanBuilder, error) {
	if resolver == nil {
		return nil, ErrTrustedContextUnavailable
	}
	if now == nil {
		now = time.Now
	}
	return &CatalogLifecyclePlanBuilder{resolver: resolver, now: now}, nil
}

func (p *CatalogLifecyclePlanBuilder) BuildUpgradePlan(ctx context.Context, rootAssemblyID string, manifest core.Manifest, lock core.GeneratedProjectLock, target core.LifecycleTargetVersions) (json.RawMessage, error) {
	resolved, err := p.resolve(ctx, rootAssemblyID, manifest, lock)
	if err != nil {
		return nil, err
	}
	createdAt := p.now().UTC().Truncate(time.Microsecond)
	planID, err := lifecyclePlanID("upgrade", rootAssemblyID, resolved.Manifest.ManifestSHA256, resolved.Lock.LockSHA256, resolved.TargetSnapshot.Checksum, target)
	if err != nil {
		return nil, err
	}
	analysis, err := p.analyzeUpgrade(ctx, resolved, target, planID, createdAt)
	if err != nil {
		return nil, err
	}
	return buildUpgradeLifecycleDocument(resolved, targetDocument(analysis.Plan), analysis, planID, createdAt)
}

func (p *CatalogLifecyclePlanBuilder) BuildEjectPlan(ctx context.Context, rootAssemblyID string, manifest core.Manifest, lock core.GeneratedProjectLock, paths []string) (json.RawMessage, error) {
	resolved, err := p.resolve(ctx, rootAssemblyID, manifest, lock)
	if err != nil {
		return nil, err
	}
	createdAt := p.now().UTC().Truncate(time.Microsecond)
	planID, err := lifecyclePlanID("eject", rootAssemblyID, resolved.Manifest.ManifestSHA256, resolved.Lock.LockSHA256, resolved.TargetSnapshot.Checksum, append([]string(nil), paths...))
	if err != nil {
		return nil, err
	}
	return p.buildEjectDocument(resolved, paths, planID, createdAt)
}

func (p *CatalogLifecyclePlanBuilder) Revalidate(ctx context.Context, plan core.LifecyclePlan, manifest core.Manifest, lock core.GeneratedProjectLock) error {
	resolved, err := p.resolve(ctx, plan.AssemblyID, manifest, lock)
	if err != nil {
		return err
	}
	if !sourceMatches(plan.Source, resolved) {
		return core.ErrConflict
	}
	var persisted lifecyclePlanDocument
	if err := json.Unmarshal(plan.Document, &persisted); err != nil || persisted.LifecyclePlanID != plan.LifecyclePlanID || persisted.Operation != string(plan.Operation) {
		return core.ErrDocumentInvalid
	}
	var rebuilt json.RawMessage
	switch plan.Operation {
	case core.LifecycleUpgrade:
		target := coreTarget(persisted.Target)
		analysis, buildErr := p.analyzeUpgrade(ctx, resolved, target, plan.LifecyclePlanID, plan.CreatedAt)
		if buildErr != nil {
			return buildErr
		}
		rebuilt, err = buildUpgradeLifecycleDocument(resolved, targetDocument(analysis.Plan), analysis, plan.LifecyclePlanID, plan.CreatedAt)
	case core.LifecycleEject:
		rebuilt, err = p.buildEjectDocument(resolved, persisted.EjectPaths, plan.LifecyclePlanID, plan.CreatedAt)
	default:
		return core.ErrDocumentInvalid
	}
	if err != nil || !bytes.Equal(rebuilt, plan.Document) {
		return core.ErrConflict
	}
	return nil
}

// BuildUpgradeInput reconstructs the trusted target plan for the durable worker.
// It repeats the renderer and ownership dry-run before returning any executable input.
func (p *CatalogLifecyclePlanBuilder) BuildUpgradeInput(ctx context.Context, lifecyclePlan core.LifecyclePlan, operation core.LifecycleOperation) (ResolvedLifecycleContext, generation.Input, generation.PreviousArtifacts, error) {
	if p == nil || p.resolver == nil || lifecyclePlan.Operation != core.LifecycleUpgrade || operation.Kind != core.LifecycleUpgrade || operation.LifecyclePlanID != lifecyclePlan.LifecyclePlanID || operation.AssemblyID != lifecyclePlan.AssemblyID {
		return ResolvedLifecycleContext{}, generation.Input{}, generation.PreviousArtifacts{}, core.ErrConflict
	}
	resolved, err := p.resolver.ResolveCurrent(ctx, operation.AssemblyID, operation.Source)
	if err != nil {
		return ResolvedLifecycleContext{}, generation.Input{}, generation.PreviousArtifacts{}, err
	}
	var document lifecyclePlanDocument
	if err := json.Unmarshal(lifecyclePlan.Document, &document); err != nil || document.Target == nil {
		return ResolvedLifecycleContext{}, generation.Input{}, generation.PreviousArtifacts{}, core.ErrDocumentInvalid
	}
	analysis, err := p.analyzeUpgrade(ctx, resolved, coreTarget(document.Target), operation.OperationID, operation.CreatedAt)
	if err != nil || len(analysis.Conflicts) != 0 || !equalDigest(analysis.TargetSnapshotChecksum, lifecyclePlan.TargetSnapshotChecksum) {
		if err == nil {
			err = core.ErrConflict
		}
		return ResolvedLifecycleContext{}, generation.Input{}, generation.PreviousArtifacts{}, err
	}
	return resolved, analysis.Input, analysis.Previous, nil
}

// BuildUpgradeResumeInput reconstructs only the trusted deterministic request.
// It deliberately skips the ownership dry-run because the operation output may
// already be on disk; the executor must require matching durable artifacts.
func (p *CatalogLifecyclePlanBuilder) BuildUpgradeResumeInput(ctx context.Context, lifecyclePlan core.LifecyclePlan, operation core.LifecycleOperation) (ResolvedLifecycleContext, generation.Input, generation.PreviousArtifacts, error) {
	if p == nil || p.resolver == nil || lifecyclePlan.Operation != core.LifecycleUpgrade || operation.Kind != core.LifecycleUpgrade || operation.LifecyclePlanID != lifecyclePlan.LifecyclePlanID || operation.AssemblyID != lifecyclePlan.AssemblyID {
		return ResolvedLifecycleContext{}, generation.Input{}, generation.PreviousArtifacts{}, core.ErrConflict
	}
	resolved, err := p.resolver.ResolveForResume(ctx, operation.AssemblyID, operation.Source)
	if err != nil {
		return ResolvedLifecycleContext{}, generation.Input{}, generation.PreviousArtifacts{}, err
	}
	var document lifecyclePlanDocument
	if err := json.Unmarshal(lifecyclePlan.Document, &document); err != nil || document.Target == nil {
		return ResolvedLifecycleContext{}, generation.Input{}, generation.PreviousArtifacts{}, core.ErrDocumentInvalid
	}
	target := coreTarget(document.Target)
	blueprintDocument, blueprintChecksum, err := targetBlueprint(resolved.Blueprint, resolved.Plan, target)
	if err != nil {
		return ResolvedLifecycleContext{}, generation.Input{}, generation.PreviousArtifacts{}, err
	}
	targetBlueprintValue := resolved.Blueprint
	targetBlueprintValue.Document = blueprintDocument
	targetBlueprintValue.ContentSHA256 = blueprintChecksum
	planned, err := planning.New(resolved.Catalog).BuildPlan(ctx, targetBlueprintValue, resolved.Plan.Environment)
	if err != nil {
		return ResolvedLifecycleContext{}, generation.Input{}, generation.PreviousArtifacts{}, err
	}
	var targetPlan generation.Plan
	if err := json.Unmarshal(planned.Document, &targetPlan); err != nil {
		return ResolvedLifecycleContext{}, generation.Input{}, generation.PreviousArtifacts{}, core.ErrDocumentInvalid
	}
	if err := verifyResolvedTarget(resolved, target, targetPlan); err != nil {
		return ResolvedLifecycleContext{}, generation.Input{}, generation.PreviousArtifacts{}, err
	}
	input, previous, err := generation.BuildRequest(resolved.Workspace.TargetRoot, generation.RequestSpec{
		WorkspaceRef: resolved.Run.OutputTargetRef, LifecycleOperationID: operation.OperationID, RunCreatedAt: operation.CreatedAt.UTC(),
		Product: resolved.Product, Blueprint: generation.ArtifactBlueprint{BlueprintID: resolved.Blueprint.BlueprintID, Version: resolved.Blueprint.Revision, Checksum: blueprintChecksum},
		BlueprintDocument: blueprintDocument, PlanDocument: planned.Document, PreviousLock: resolved.ProjectLock,
		PreviousManifestPath: resolved.PreviousManifestPath, PreviousLockPath: resolved.PreviousLockPath,
	})
	if err != nil {
		return ResolvedLifecycleContext{}, generation.Input{}, generation.PreviousArtifacts{}, err
	}
	return resolved, input, previous, nil
}

func (p *CatalogLifecyclePlanBuilder) resolve(ctx context.Context, rootAssemblyID string, manifest core.Manifest, lock core.GeneratedProjectLock) (ResolvedLifecycleContext, error) {
	if p == nil || p.resolver == nil {
		return ResolvedLifecycleContext{}, ErrTrustedContextUnavailable
	}
	return p.resolver.Resolve(ctx, rootAssemblyID, manifest, lock)
}

type upgradeAnalysis struct {
	Plan                   generation.Plan
	Input                  generation.Input
	Previous               generation.PreviousArtifacts
	Prepared               generation.PreparedChangeSet
	Conflicts              []lifecycleConflictDocument
	TargetSnapshotChecksum string
}

func (p *CatalogLifecyclePlanBuilder) analyzeUpgrade(ctx context.Context, resolved ResolvedLifecycleContext, target core.LifecycleTargetVersions, executionID string, createdAt time.Time) (upgradeAnalysis, error) {
	blueprintDocument, blueprintChecksum, err := targetBlueprint(resolved.Blueprint, resolved.Plan, target)
	if err != nil {
		return upgradeAnalysis{}, err
	}
	targetBlueprintValue := resolved.Blueprint
	targetBlueprintValue.Document = blueprintDocument
	targetBlueprintValue.ContentSHA256 = blueprintChecksum
	planned, err := planning.New(resolved.Catalog).BuildPlan(ctx, targetBlueprintValue, resolved.Plan.Environment)
	if err != nil {
		return upgradeAnalysis{}, err
	}
	var targetPlan generation.Plan
	if err := json.Unmarshal(planned.Document, &targetPlan); err != nil {
		return upgradeAnalysis{}, core.ErrDocumentInvalid
	}
	if err := verifyResolvedTarget(resolved, target, targetPlan); err != nil {
		return upgradeAnalysis{}, err
	}
	input, previous, err := generation.BuildRequest(resolved.Workspace.TargetRoot, generation.RequestSpec{
		WorkspaceRef: resolved.Run.OutputTargetRef, LifecycleOperationID: executionID, RunCreatedAt: createdAt.UTC(),
		Product: resolved.Product, Blueprint: generation.ArtifactBlueprint{BlueprintID: resolved.Blueprint.BlueprintID, Version: resolved.Blueprint.Revision, Checksum: blueprintChecksum},
		BlueprintDocument: blueprintDocument, PlanDocument: planned.Document, PreviousLock: resolved.ProjectLock,
		PreviousManifestPath: resolved.PreviousManifestPath, PreviousLockPath: resolved.PreviousLockPath,
	})
	if err != nil {
		return upgradeAnalysis{}, err
	}
	rendered, err := generation.NewPureRenderer(resolved.Catalog).Render(ctx, input)
	if err != nil {
		return upgradeAnalysis{}, err
	}
	prepared, prepareErr := generation.PrepareTarget(resolved.Workspace.TargetRoot, input.Request, rendered, resolved.ProjectLock)
	conflicts := conflictsFromDiagnostics(prepared.Diagnostics)
	if prepareErr != nil && len(conflicts) == 0 {
		return upgradeAnalysis{}, prepareErr
	}
	targetChecksum, err := predictedSnapshotChecksum(prepared.Snapshot, prepared.Changes)
	if err != nil {
		return upgradeAnalysis{}, err
	}
	return upgradeAnalysis{Plan: targetPlan, Input: input, Previous: previous, Prepared: prepared, Conflicts: conflicts, TargetSnapshotChecksum: targetChecksum}, nil
}

func verifyResolvedTarget(resolved ResolvedLifecycleContext, target core.LifecycleTargetVersions, plan generation.Plan) error {
	snapshot, err := resolved.Catalog.Snapshot()
	if err != nil || !equalDigest(snapshot.SnapshotSHA256, plan.CatalogSnapshot.Checksum) {
		return core.ErrConflict
	}
	applications, err := planApplications(resolved.Plan.Document)
	if err != nil || len(applications) == 0 {
		return core.ErrDocumentInvalid
	}
	for _, application := range applications {
		tool, resolveErr := resolved.Catalog.ResolveTool("generator", target.Generator.ID, target.Generator.Version, application.Target, application.DeliveryMode, resolved.Plan.Environment)
		if resolveErr != nil || tool.Execution.Mode != "builtin_adapter" || tool.Execution.AdapterID != "assembly.pure-renderer" || !equalDigest(tool.ManifestSHA256, plan.Generator.Checksum) {
			return core.ErrPlanUnavailable
		}
	}
	if !exactResolvedRefs(target.Packages, packageRefs(plan)) || !exactResolvedRefs(target.Templates, templateRefs(plan)) || !exactResolvedRefs(target.SDKs, sdkRefs(plan)) || plan.Generator.GeneratorID != target.Generator.ID || plan.Generator.Version != target.Generator.Version {
		return core.ErrConflict
	}
	return nil
}

func targetBlueprint(blueprint core.Blueprint, sourcePlan core.Plan, target core.LifecycleTargetVersions) (json.RawMessage, string, error) {
	var document blueprintTargetDocument
	if err := json.Unmarshal(blueprint.Document, &document); err != nil || document.BlueprintID != blueprint.BlueprintID || len(document.Applications) == 0 || len(target.SDKs) != 1 {
		return nil, "", core.ErrDocumentInvalid
	}
	templates := make(map[string]string, len(target.Templates))
	for _, value := range target.Templates {
		if templates[value.ID] != "" {
			return nil, "", core.ErrInvalidCommand
		}
		templates[value.ID] = value.Version
	}
	usedTemplates := make(map[string]bool, len(templates))
	for index := range document.Applications {
		version := templates[document.Applications[index].UI.TemplateID]
		if version == "" {
			return nil, "", core.ErrInvalidCommand
		}
		document.Applications[index].UI.Version = version
		usedTemplates[document.Applications[index].UI.TemplateID] = true
	}
	if len(usedTemplates) != len(templates) {
		return nil, "", core.ErrInvalidCommand
	}
	document.Packages = make([]blueprintPackageSelection, len(target.Packages))
	for i, value := range target.Packages {
		document.Packages[i] = blueprintPackageSelection{PackageID: value.ID, Version: value.Version}
	}
	sort.Slice(document.Packages, func(i, j int) bool { return document.Packages[i].PackageID < document.Packages[j].PackageID })
	document.Generator = blueprintToolSelection{ID: target.Generator.ID, Version: target.Generator.Version}
	document.SDK = blueprintToolSelection{ID: target.SDKs[0].ID, Version: target.SDKs[0].Version}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, "", err
	}
	canonical, err := machinecontract.Canonicalize(raw)
	if err != nil {
		return nil, "", err
	}
	digest, err := machinecontract.Digest(canonical)
	if err != nil {
		return nil, "", err
	}
	_ = sourcePlan
	return canonical, "sha256:" + digest, nil
}

func buildUpgradeLifecycleDocument(resolved ResolvedLifecycleContext, target *lifecycleTargetDocument, analysis upgradeAnalysis, planID string, createdAt time.Time) (json.RawMessage, error) {
	changes := changesFromPrepared(analysis.Prepared, analysis.Plan)
	statements := []string{"Confirm the locked upgrade changes and regression checks", "Confirm automatic rollback to the predecessor Manifest and lock"}
	document := lifecyclePlanDocument{
		SchemaVersion: "1.0.0", LifecyclePlanID: planID, AssemblyID: resolved.RootAssemblyID, ProductID: resolved.Manifest.ProductID, Operation: "upgrade",
		Source: sourceDocument(resolved), Target: target, TargetSnapshotChecksum: analysis.TargetSnapshotChecksum, Changes: changes,
		Migrations: []lifecycleMigrationDocument{}, RegressionTests: []string{"assembly.lifecycle.contract", "assembly.lifecycle.workspace"}, Conflicts: analysis.Conflicts,
		Rollback:              lifecycleRollbackDocument{Strategy: "restore_predecessor", Automatic: true, PredecessorManifestChecksum: resolved.Manifest.ManifestSHA256, PredecessorLockChecksum: resolved.Lock.LockSHA256},
		BlockingConflictCount: len(analysis.Conflicts), Executable: len(analysis.Conflicts) == 0, Confirmation: lifecycleConfirmationDocument{Statements: statements}, CreatedAt: createdAt.UTC(), PlanChecksum: emptyDigest,
	}
	return marshalLifecyclePlan(document)
}

func (p *CatalogLifecyclePlanBuilder) buildEjectDocument(resolved ResolvedLifecycleContext, paths []string, planID string, createdAt time.Time) (json.RawMessage, error) {
	raw, err := generation.BuildEjectPlan(resolved.Workspace.TargetRoot, resolved.Lock.Document, paths)
	if err != nil {
		return nil, err
	}
	var eject generation.EjectPlan
	if err := json.Unmarshal(raw, &eject); err != nil || !equalDigest(eject.TargetSnapshotChecksum, resolved.TargetSnapshot.Checksum) {
		return nil, core.ErrConflict
	}
	locked := make(map[string]generation.LockedFile, len(resolved.ProjectLock.Files))
	for _, file := range resolved.ProjectLock.Files {
		locked[file.Path] = file
	}
	changes := make([]lifecycleChangeDocument, len(eject.Files))
	predicted := make([]generation.FileChange, len(eject.Files))
	for i, file := range eject.Files {
		baseline, ok := locked[file.Path]
		if !ok || baseline.SourceID == "" || baseline.SourceVersion == "" {
			return nil, core.ErrConflict
		}
		changes[i] = lifecycleChangeDocument{Path: file.Path, Action: "eject", Ownership: "forked", BeforeChecksum: stringPointer(file.CurrentSHA256), AfterChecksum: stringPointer(file.CurrentSHA256), SourceID: baseline.SourceID, SourceVersion: baseline.SourceVersion}
		predicted[i] = generation.FileChange{Path: file.Path, Ownership: "forked", Action: "unchanged", SHA256: file.CurrentSHA256, PreviousSHA256: file.CurrentSHA256}
	}
	targetChecksum, err := predictedSnapshotChecksum(resolved.TargetSnapshot, predicted)
	if err != nil {
		return nil, err
	}
	statements := []string{"Confirm selected managed paths become forked and stop automatic overwrite", "Confirm file contents remain unchanged during eject"}
	document := lifecyclePlanDocument{
		SchemaVersion: "1.0.0", LifecyclePlanID: planID, AssemblyID: resolved.RootAssemblyID, ProductID: resolved.Manifest.ProductID, Operation: "eject",
		Source: sourceDocument(resolved), EjectPaths: append([]string(nil), paths...), TargetSnapshotChecksum: targetChecksum, Changes: changes,
		Migrations: []lifecycleMigrationDocument{}, RegressionTests: []string{"assembly.lifecycle.contract", "assembly.lifecycle.workspace"}, Conflicts: []lifecycleConflictDocument{},
		Rollback:              lifecycleRollbackDocument{Strategy: "restore_predecessor", Automatic: true, PredecessorManifestChecksum: resolved.Manifest.ManifestSHA256, PredecessorLockChecksum: resolved.Lock.LockSHA256},
		BlockingConflictCount: 0, Executable: true, Confirmation: lifecycleConfirmationDocument{Statements: statements}, CreatedAt: createdAt.UTC(), PlanChecksum: emptyDigest,
	}
	return marshalLifecyclePlan(document)
}

const emptyDigest = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func marshalLifecyclePlan(document lifecyclePlanDocument) (json.RawMessage, error) {
	if err := normalizeLifecycleSafety(&document); err != nil {
		return nil, err
	}
	confirmationSeed, err := json.Marshal(struct {
		Operation              string                  `json:"operation"`
		AssemblyID             string                  `json:"assembly_id"`
		Source                 lifecycleSourceDocument `json:"source"`
		TargetSnapshotChecksum string                  `json:"target_snapshot_checksum"`
		Statements             []string                `json:"statements"`
		BlockingConflicts      int                     `json:"blocking_conflict_count"`
	}{document.Operation, document.AssemblyID, document.Source, document.TargetSnapshotChecksum, document.Confirmation.Statements, document.BlockingConflictCount})
	if err != nil {
		return nil, err
	}
	confirmationDigest, err := machinecontract.Digest(confirmationSeed)
	if err != nil {
		return nil, err
	}
	document.Confirmation.SummaryChecksum = "sha256:" + confirmationDigest
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	checksum, err := machinecontract.DigestWithoutTopLevelField(raw, "plan_checksum")
	if err != nil {
		return nil, err
	}
	document.PlanChecksum = checksum
	raw, err = json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return machinecontract.Canonicalize(raw)
}

func normalizeLifecycleSafety(document *lifecyclePlanDocument) error {
	if document == nil {
		return core.ErrDocumentInvalid
	}
	seen := make(map[string]struct{}, len(document.Conflicts))
	for _, conflict := range document.Conflicts {
		seen[conflict.ConflictID] = struct{}{}
	}
	appendBlocking := func(code, category, message string, remediation []string, identity string) error {
		seed, err := json.Marshal(struct {
			Code     string `json:"code"`
			Category string `json:"category"`
			Identity string `json:"identity"`
		}{code, category, identity})
		if err != nil {
			return err
		}
		digest, err := machinecontract.Digest(seed)
		if err != nil {
			return err
		}
		conflictID := "conflict." + digest[:24]
		if _, exists := seen[conflictID]; exists {
			return nil
		}
		document.Conflicts = append(document.Conflicts, lifecycleConflictDocument{
			ConflictID:  conflictID,
			Code:        code,
			Category:    category,
			Blocking:    true,
			Message:     message,
			Paths:       []string{},
			Remediation: append([]string(nil), remediation...),
		})
		seen[conflictID] = struct{}{}
		return nil
	}
	for _, migration := range document.Migrations {
		if migration.Reversibility != "manual" {
			continue
		}
		if err := appendBlocking(
			"migration_manual_rollback_required",
			"migration",
			"Migration "+migration.MigrationID+" requires a manual rollback and cannot be executed by the lifecycle worker",
			[]string{"publish a reversible or compensatable migration with an automated rollback strategy", "run the migration outside the standard lifecycle flow and create a new trusted plan"},
			migration.MigrationID,
		); err != nil {
			return err
		}
	}
	rollback := document.Rollback
	rollbackUnsafe := rollback.Strategy == "manual" || !rollback.Automatic || !validSHA256Digest(rollback.PredecessorManifestChecksum) || !validSHA256Digest(rollback.PredecessorLockChecksum)
	if rollbackUnsafe {
		if err := appendBlocking(
			"rollback_not_automatically_verifiable",
			"rollback",
			"The lifecycle plan has no automatically verifiable rollback to its predecessor Manifest and lock",
			[]string{"select an automatic restore_predecessor or compensate strategy", "provide trusted predecessor Manifest and lock checksums"},
			rollback.Strategy+"\x00"+rollback.PredecessorManifestChecksum+"\x00"+rollback.PredecessorLockChecksum,
		); err != nil {
			return err
		}
	}
	sort.Slice(document.Conflicts, func(i, j int) bool { return document.Conflicts[i].ConflictID < document.Conflicts[j].ConflictID })
	document.BlockingConflictCount = 0
	for _, conflict := range document.Conflicts {
		if conflict.Blocking {
			document.BlockingConflictCount++
		}
	}
	document.Executable = document.BlockingConflictCount == 0
	return nil
}

func validSHA256Digest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func predictedSnapshotChecksum(snapshot generation.TargetSnapshot, changes []generation.FileChange) (string, error) {
	files := make(map[string]generation.ExistingFile, len(snapshot.Files)+len(changes))
	for _, file := range snapshot.Files {
		files[file.Path] = file
	}
	for _, change := range changes {
		files[change.Path] = generation.ExistingFile{Path: change.Path, Ownership: change.Ownership, SHA256: change.SHA256}
	}
	ordered := make([]generation.ExistingFile, 0, len(files))
	for _, file := range files {
		ordered = append(ordered, file)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	raw, err := json.Marshal(ordered)
	if err != nil {
		return "", err
	}
	digest, err := machinecontract.Digest(raw)
	if err != nil {
		return "", err
	}
	return "sha256:" + digest, nil
}

func changesFromPrepared(prepared generation.PreparedChangeSet, plan generation.Plan) []lifecycleChangeDocument {
	outputs := make(map[string]generation.OutputSpec, len(plan.ExpectedOutputs))
	for _, output := range plan.ExpectedOutputs {
		outputs[output.Path] = output
	}
	changes := make([]lifecycleChangeDocument, 0, len(prepared.Changes))
	for _, change := range prepared.Changes {
		output := outputs[change.Path]
		action := change.Action
		if action == "created" {
			action = "create"
		} else if action == "updated" {
			action = "update"
		}
		var before *string
		if change.PreviousSHA256 != "" {
			before = stringPointer(change.PreviousSHA256)
		}
		changes = append(changes, lifecycleChangeDocument{Path: change.Path, Action: action, Ownership: change.Ownership, BeforeChecksum: before, AfterChecksum: stringPointer(change.SHA256), SourceID: output.SourceID, SourceVersion: output.SourceVersion})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

func conflictsFromDiagnostics(values []generation.Diagnostic) []lifecycleConflictDocument {
	result := make([]lifecycleConflictDocument, 0, len(values))
	for _, value := range values {
		paths := append([]string(nil), value.RelatedPaths...)
		if value.Path != "" {
			paths = append(paths, value.Path)
		}
		sort.Strings(paths)
		paths = uniqueStrings(paths)
		category := "target"
		switch {
		case strings.Contains(value.Code, "CUSTOM") || strings.Contains(value.Code, "OWNERSHIP") || strings.Contains(value.Code, "PROTECTED") || strings.Contains(value.Code, "PATH_OVERLAP"):
			category = "custom"
		case strings.Contains(value.Code, "GENERATED") || strings.Contains(value.Code, "MANAGED_FILE"):
			category = "generated_drift"
		case strings.Contains(value.Code, "INTEGRATION"):
			category = "integration"
		}
		seed, _ := json.Marshal(struct {
			Code  string
			Paths []string
		}{value.Code, paths})
		digest, _ := machinecontract.Digest(seed)
		result = append(result, lifecycleConflictDocument{ConflictID: "conflict." + digest[:24], Code: strings.ToLower(value.Code), Category: category, Blocking: true, Message: value.Message, Paths: paths, Remediation: append([]string(nil), value.Remediation...)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ConflictID < result[j].ConflictID })
	return result
}

func lifecyclePlanID(kind, assemblyID, manifestChecksum, lockChecksum, snapshotChecksum string, request any) (string, error) {
	raw, err := json.Marshal(struct {
		Kind, AssemblyID, ManifestChecksum, LockChecksum, SnapshotChecksum string
		Request                                                            any
	}{kind, assemblyID, manifestChecksum, lockChecksum, snapshotChecksum, request})
	if err != nil {
		return "", err
	}
	digest, err := machinecontract.Digest(raw)
	if err != nil {
		return "", err
	}
	return "lifecycle." + digest[:24], nil
}

func sourceDocument(value ResolvedLifecycleContext) lifecycleSourceDocument {
	return lifecycleSourceDocument{ManifestID: value.Manifest.AssemblyID, ManifestChecksum: value.Manifest.ManifestSHA256, LockID: value.Lock.LockID, LockChecksum: value.Lock.LockSHA256, CatalogChecksum: value.ProjectLock.CatalogChecksum, TargetSnapshotChecksum: value.TargetSnapshot.Checksum}
}

func sourceMatches(source core.LifecycleArtifactState, resolved ResolvedLifecycleContext) bool {
	actual := sourceDocument(resolved)
	return source.ManifestID == actual.ManifestID && source.LockID == actual.LockID && equalDigest(source.ManifestChecksum, actual.ManifestChecksum) && equalDigest(source.LockChecksum, actual.LockChecksum) && equalDigest(source.CatalogChecksum, actual.CatalogChecksum) && equalDigest(source.TargetSnapshotChecksum, actual.TargetSnapshotChecksum)
}

func targetDocument(plan generation.Plan) *lifecycleTargetDocument {
	return &lifecycleTargetDocument{CatalogChecksum: plan.CatalogSnapshot.Checksum, Packages: packageRefsWithChecksums(plan), Templates: templateRefsWithChecksums(plan), Generator: lifecycleVersionDocument{ID: plan.Generator.GeneratorID, Version: plan.Generator.Version, Checksum: plan.Generator.Checksum}, SDKs: sdkRefsWithChecksums(plan)}
}

func coreTarget(value *lifecycleTargetDocument) core.LifecycleTargetVersions {
	if value == nil {
		return core.LifecycleTargetVersions{}
	}
	return core.LifecycleTargetVersions{Packages: coreRefs(value.Packages), Templates: coreRefs(value.Templates), Generator: core.LifecycleVersionRef{ID: value.Generator.ID, Version: value.Generator.Version}, SDKs: coreRefs(value.SDKs)}
}

func coreRefs(values []lifecycleVersionDocument) []core.LifecycleVersionRef {
	result := make([]core.LifecycleVersionRef, len(values))
	for i, value := range values {
		result[i] = core.LifecycleVersionRef{ID: value.ID, Version: value.Version}
	}
	return result
}

func exactResolvedRefs(requested []core.LifecycleVersionRef, resolved []core.LifecycleVersionRef) bool {
	if len(requested) != len(resolved) {
		return false
	}
	left, right := append([]core.LifecycleVersionRef(nil), requested...), append([]core.LifecycleVersionRef(nil), resolved...)
	sort.Slice(left, func(i, j int) bool { return left[i].ID < left[j].ID })
	sort.Slice(right, func(i, j int) bool { return right[i].ID < right[j].ID })
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func packageRefs(plan generation.Plan) []core.LifecycleVersionRef {
	result := make([]core.LifecycleVersionRef, len(plan.Packages))
	for i, value := range plan.Packages {
		result[i] = core.LifecycleVersionRef{ID: value.PackageID, Version: value.Version}
	}
	return result
}
func templateRefs(plan generation.Plan) []core.LifecycleVersionRef {
	seen := map[string]core.LifecycleVersionRef{}
	for _, value := range plan.Applications {
		seen[value.Template.TemplateID] = core.LifecycleVersionRef{ID: value.Template.TemplateID, Version: value.Template.Version}
	}
	result := make([]core.LifecycleVersionRef, 0, len(seen))
	for _, value := range seen {
		result = append(result, value)
	}
	return result
}
func sdkRefs(plan generation.Plan) []core.LifecycleVersionRef {
	result := make([]core.LifecycleVersionRef, len(plan.SDKs))
	for i, value := range plan.SDKs {
		result[i] = core.LifecycleVersionRef{ID: value.SDKID, Version: value.Version}
	}
	return result
}

func packageRefsWithChecksums(plan generation.Plan) []lifecycleVersionDocument {
	result := make([]lifecycleVersionDocument, len(plan.Packages))
	for i, value := range plan.Packages {
		result[i] = lifecycleVersionDocument{ID: value.PackageID, Version: value.Version, Checksum: value.Checksum}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func templateRefsWithChecksums(plan generation.Plan) []lifecycleVersionDocument {
	seen := map[string]lifecycleVersionDocument{}
	for _, value := range plan.Applications {
		seen[value.Template.TemplateID] = lifecycleVersionDocument{ID: value.Template.TemplateID, Version: value.Template.Version, Checksum: value.Template.Checksum}
	}
	result := make([]lifecycleVersionDocument, 0, len(seen))
	for _, value := range seen {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func sdkRefsWithChecksums(plan generation.Plan) []lifecycleVersionDocument {
	result := make([]lifecycleVersionDocument, len(plan.SDKs))
	for i, value := range plan.SDKs {
		result[i] = lifecycleVersionDocument{ID: value.SDKID, Version: value.Version, Checksum: value.Checksum}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

type planApplicationContext struct {
	Target       string `json:"target"`
	DeliveryMode string `json:"delivery_mode"`
}

func planApplications(document json.RawMessage) ([]planApplicationContext, error) {
	var value struct {
		Applications []planApplicationContext `json:"applications"`
	}
	if err := json.Unmarshal(document, &value); err != nil {
		return nil, err
	}
	return value.Applications, nil
}

func uniqueStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
func stringPointer(value string) *string { return &value }

type blueprintToolSelection struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}
type blueprintPackageSelection struct {
	PackageID string `json:"package_id"`
	Version   string `json:"version"`
}
type blueprintApplicationSelection struct {
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
}
type blueprintTargetDocument struct {
	SchemaVersion string                          `json:"schema_version"`
	BlueprintID   string                          `json:"blueprint_id"`
	Version       string                          `json:"version"`
	Product       json.RawMessage                 `json:"product"`
	Packages      []blueprintPackageSelection     `json:"packages"`
	Applications  []blueprintApplicationSelection `json:"applications"`
	ProviderRefs  []json.RawMessage               `json:"provider_refs"`
	Extensions    []json.RawMessage               `json:"extensions"`
	Generator     blueprintToolSelection          `json:"generator"`
	SDK           blueprintToolSelection          `json:"sdk"`
	OutputRoot    string                          `json:"output_root"`
}

type lifecyclePlanDocument struct {
	SchemaVersion          string                        `json:"schema_version"`
	LifecyclePlanID        string                        `json:"lifecycle_plan_id"`
	AssemblyID             string                        `json:"assembly_id"`
	ProductID              string                        `json:"product_id"`
	Operation              string                        `json:"operation"`
	Source                 lifecycleSourceDocument       `json:"source"`
	Target                 *lifecycleTargetDocument      `json:"target,omitempty"`
	EjectPaths             []string                      `json:"eject_paths,omitempty"`
	TargetSnapshotChecksum string                        `json:"target_snapshot_checksum"`
	Changes                []lifecycleChangeDocument     `json:"changes"`
	Migrations             []lifecycleMigrationDocument  `json:"migrations"`
	RegressionTests        []string                      `json:"regression_tests"`
	Conflicts              []lifecycleConflictDocument   `json:"conflicts"`
	Rollback               lifecycleRollbackDocument     `json:"rollback"`
	BlockingConflictCount  int                           `json:"blocking_conflict_count"`
	Executable             bool                          `json:"executable"`
	Confirmation           lifecycleConfirmationDocument `json:"confirmation"`
	CreatedAt              time.Time                     `json:"created_at"`
	PlanChecksum           string                        `json:"plan_checksum"`
}
type lifecycleSourceDocument struct {
	ManifestID             string `json:"manifest_id"`
	ManifestChecksum       string `json:"manifest_checksum"`
	LockID                 string `json:"lock_id"`
	LockChecksum           string `json:"lock_checksum"`
	CatalogChecksum        string `json:"catalog_checksum"`
	TargetSnapshotChecksum string `json:"target_snapshot_checksum"`
}
type lifecycleTargetDocument struct {
	CatalogChecksum string                     `json:"catalog_checksum"`
	Packages        []lifecycleVersionDocument `json:"packages"`
	Templates       []lifecycleVersionDocument `json:"templates"`
	Generator       lifecycleVersionDocument   `json:"generator"`
	SDKs            []lifecycleVersionDocument `json:"sdks"`
}
type lifecycleVersionDocument struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Checksum string `json:"checksum"`
}
type lifecycleChangeDocument struct {
	Path           string  `json:"path"`
	Action         string  `json:"action"`
	Ownership      string  `json:"ownership"`
	BeforeChecksum *string `json:"before_checksum"`
	AfterChecksum  *string `json:"after_checksum"`
	SourceID       string  `json:"source_id"`
	SourceVersion  string  `json:"source_version"`
}
type lifecycleMigrationDocument struct {
	MigrationID   string `json:"migration_id"`
	Kind          string `json:"kind"`
	Reversibility string `json:"reversibility"`
	Summary       string `json:"summary"`
}
type lifecycleConflictDocument struct {
	ConflictID  string   `json:"conflict_id"`
	Code        string   `json:"code"`
	Category    string   `json:"category"`
	Blocking    bool     `json:"blocking"`
	Message     string   `json:"message"`
	Paths       []string `json:"paths"`
	Remediation []string `json:"remediation"`
}
type lifecycleRollbackDocument struct {
	Strategy                    string `json:"strategy"`
	Automatic                   bool   `json:"automatic"`
	PredecessorManifestChecksum string `json:"predecessor_manifest_checksum"`
	PredecessorLockChecksum     string `json:"predecessor_lock_checksum"`
}
type lifecycleConfirmationDocument struct {
	Statements      []string `json:"statements"`
	SummaryChecksum string   `json:"summary_checksum"`
}

var _ core.LifecyclePlanBuilder = (*CatalogLifecyclePlanBuilder)(nil)
