package assemblylifecycle

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"path"

	"platform.local/capability-platform/backend/internal/modules/assembly/core"
	"platform.local/capability-platform/backend/internal/modules/assembly/generation"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecatalog"
	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

var ErrTrustedContextUnavailable = errors.New("trusted assembly lifecycle context is unavailable")

type LifecycleContextRepository interface {
	GetLifecycleSource(context.Context, string) (core.Manifest, core.GeneratedProjectLock, error)
	GetManifest(context.Context, string, string) (core.Manifest, error)
	GetRun(context.Context, string, string) (core.Run, error)
	GetPlan(context.Context, string, string) (core.Plan, error)
	GetBlueprint(context.Context, string, string, int64) (core.Blueprint, error)
}

func (r *TrustedContextResolver) ResolveCurrent(ctx context.Context, rootAssemblyID string, expected core.LifecycleArtifactState) (ResolvedLifecycleContext, error) {
	resolved, err := r.resolveExpected(ctx, rootAssemblyID, expected)
	if err != nil {
		return ResolvedLifecycleContext{}, err
	}
	if !equalDigest(resolved.TargetSnapshot.Checksum, expected.TargetSnapshotChecksum) {
		return ResolvedLifecycleContext{}, core.ErrConflict
	}
	return resolved, nil
}

// ResolveForResume verifies the immutable source lineage while allowing the
// workspace to already contain this operation's committed output. Callers must
// still prove that output from operation-owned durable artifacts.
func (r *TrustedContextResolver) ResolveForResume(ctx context.Context, rootAssemblyID string, expected core.LifecycleArtifactState) (ResolvedLifecycleContext, error) {
	return r.resolveExpected(ctx, rootAssemblyID, expected)
}

func (r *TrustedContextResolver) resolveExpected(ctx context.Context, rootAssemblyID string, expected core.LifecycleArtifactState) (ResolvedLifecycleContext, error) {
	if r == nil || r.repository == nil || rootAssemblyID == "" || expected.ManifestID == "" || expected.LockID == "" {
		return ResolvedLifecycleContext{}, ErrTrustedContextUnavailable
	}
	manifest, lock, err := r.repository.GetLifecycleSource(ctx, rootAssemblyID)
	if err != nil {
		return ResolvedLifecycleContext{}, err
	}
	if manifest.AssemblyID != expected.ManifestID || lock.LockID != expected.LockID ||
		!equalDigest(manifest.ManifestSHA256, expected.ManifestChecksum) || !equalDigest(lock.LockSHA256, expected.LockChecksum) {
		return ResolvedLifecycleContext{}, core.ErrConflict
	}
	resolved, err := r.Resolve(ctx, rootAssemblyID, manifest, lock)
	if err != nil {
		return ResolvedLifecycleContext{}, err
	}
	if !equalDigest(resolved.ProjectLock.CatalogChecksum, expected.CatalogChecksum) {
		return ResolvedLifecycleContext{}, core.ErrConflict
	}
	return resolved, nil
}

type ResolvedLifecycleContext struct {
	RootAssemblyID       string
	Manifest             core.Manifest
	Lock                 core.GeneratedProjectLock
	Run                  core.Run
	Plan                 core.Plan
	Blueprint            core.Blueprint
	Workspace            generation.Workspace
	Catalog              *machinecatalog.Catalog
	CatalogScope         string
	ProjectLock          generation.ProjectLock
	TargetSnapshot       generation.TargetSnapshot
	Product              generation.ArtifactProduct
	PreviousManifestPath string
	PreviousLockPath     string
}

type TrustedContextResolver struct {
	repository   LifecycleContextRepository
	workspaces   *generation.WorkspaceCatalog
	ordinary     *machinecatalog.Catalog
	experimental *machinecatalog.Catalog
}

func NewTrustedContextResolver(repository LifecycleContextRepository, workspaces *generation.WorkspaceCatalog, ordinary, experimental *machinecatalog.Catalog) (*TrustedContextResolver, error) {
	if repository == nil || workspaces == nil || (ordinary == nil && experimental == nil) {
		return nil, ErrTrustedContextUnavailable
	}
	return &TrustedContextResolver{repository: repository, workspaces: workspaces, ordinary: ordinary, experimental: experimental}, nil
}

func (r *TrustedContextResolver) Resolve(ctx context.Context, rootAssemblyID string, manifest core.Manifest, lock core.GeneratedProjectLock) (ResolvedLifecycleContext, error) {
	if r == nil || r.repository == nil || r.workspaces == nil || rootAssemblyID == "" || manifest.AssemblyID == "" || manifest.ProductID == "" || lock.LockID == "" {
		return ResolvedLifecycleContext{}, ErrTrustedContextUnavailable
	}
	if lock.AssemblyID != manifest.AssemblyID || lock.ProductID != manifest.ProductID || !equalDigest(lock.DocumentSHA256, digestDocument(lock.Document)) || !equalDigest(manifest.DocumentSHA256, digestDocument(manifest.Document)) {
		return ResolvedLifecycleContext{}, core.ErrConflict
	}

	manifestDocument, err := decodeManifestDocument(manifest.Document)
	manifestChecksum, checksumErr := machinecontract.DigestWithoutTopLevelField(manifest.Document, "manifest_checksum")
	if err != nil || checksumErr != nil || manifestDocument.AssemblyID != manifest.AssemblyID || manifestDocument.Product.ProductID != manifest.ProductID || !equalDigest(manifestDocument.ManifestChecksum, manifest.ManifestSHA256) || !equalDigest(manifestChecksum, manifest.ManifestSHA256) {
		return ResolvedLifecycleContext{}, core.ErrConflict
	}
	projectLock, err := generation.DecodeProjectLock(lock.Document)
	if err != nil || projectLock.LockID != lock.LockID || !equalDigest(projectLock.LockChecksum, lock.LockSHA256) || !equalDigest(projectLock.AssemblyManifestChecksum, manifest.ManifestSHA256) || !equalDigest(projectLock.CatalogChecksum, manifestDocument.CatalogChecksum) {
		return ResolvedLifecycleContext{}, core.ErrConflict
	}

	rootManifest, err := r.repository.GetManifest(ctx, manifest.ProductID, rootAssemblyID)
	if err != nil || rootManifest.AssemblyID != rootAssemblyID || rootManifest.ProductID != manifest.ProductID || rootManifest.RunID == "" {
		return ResolvedLifecycleContext{}, trustedContextError(err)
	}
	rootManifestChecksum, checksumErr := machinecontract.DigestWithoutTopLevelField(rootManifest.Document, "manifest_checksum")
	if checksumErr != nil || !equalDigest(rootManifestChecksum, rootManifest.ManifestSHA256) || !equalDigest(rootManifest.DocumentSHA256, digestDocument(rootManifest.Document)) {
		return ResolvedLifecycleContext{}, core.ErrConflict
	}
	run, err := r.repository.GetRun(ctx, manifest.ProductID, rootManifest.RunID)
	if err != nil || run.ProductID != manifest.ProductID || run.Status != core.RunStatusCompleted || run.ManifestID != rootManifest.AssemblyID || run.PlanID == "" || !equalDigest(run.DocumentSHA256, digestDocument(run.Document)) {
		return ResolvedLifecycleContext{}, trustedContextError(err)
	}
	planValue, err := r.repository.GetPlan(ctx, manifest.ProductID, run.PlanID)
	if err != nil || planValue.ProductID != manifest.ProductID || planValue.PlanID != run.PlanID || planValue.Version != run.PlanVersion || !equalDigest(planValue.PlanSHA256, run.PlanSHA256) || planValue.ConfirmedAt == nil {
		return ResolvedLifecycleContext{}, trustedContextError(err)
	}
	planChecksum, checksumErr := machinecontract.DigestWithoutTopLevelField(planValue.Document, "plan_checksum")
	if checksumErr != nil || !equalDigest(planChecksum, planValue.PlanSHA256) {
		return ResolvedLifecycleContext{}, core.ErrConflict
	}
	rootManifestDocument, err := decodeManifestDocument(rootManifest.Document)
	if err != nil {
		return ResolvedLifecycleContext{}, err
	}
	planScope, planCatalogChecksum, err := decodePlanCatalogContext(planValue.Document)
	if err != nil || !equalDigest(planCatalogChecksum, planValue.CatalogSnapshotSHA256) || !equalDigest(rootManifestDocument.CatalogChecksum, planValue.CatalogSnapshotSHA256) {
		return ResolvedLifecycleContext{}, trustedContextError(err)
	}
	blueprint, err := r.repository.GetBlueprint(ctx, manifest.ProductID, planValue.BlueprintID, planValue.BlueprintRevision)
	if err != nil || blueprint.ProductID != manifest.ProductID || blueprint.BlueprintID != planValue.BlueprintID || blueprint.Revision != planValue.BlueprintRevision || !equalDigest(blueprint.ContentSHA256, planValue.BlueprintSHA256) || !equalDigest(blueprint.ContentSHA256, digestDocument(blueprint.Document)) {
		return ResolvedLifecycleContext{}, trustedContextError(err)
	}
	workspace, err := r.workspaces.Resolve(run.OutputTargetRef)
	if err != nil {
		return ResolvedLifecycleContext{}, ErrTrustedContextUnavailable
	}
	catalog, scope, err := r.resolveCatalog(planScope)
	if err != nil || !equalDigest(manifestDocument.CatalogChecksum, projectLock.CatalogChecksum) {
		return ResolvedLifecycleContext{}, trustedContextError(err)
	}
	snapshot, err := generation.InspectTarget(workspace.TargetRoot, projectLock)
	if err != nil {
		return ResolvedLifecycleContext{}, err
	}
	return ResolvedLifecycleContext{
		RootAssemblyID: rootAssemblyID, Manifest: manifest, Lock: lock, Run: run, Plan: planValue, Blueprint: blueprint,
		Workspace: workspace, Catalog: catalog, CatalogScope: scope, ProjectLock: projectLock, TargetSnapshot: snapshot,
		Product:              manifestDocument.Product,
		PreviousManifestPath: path.Join("artifacts", "assembly", manifest.AssemblyID, "assembly-manifest.json"),
		PreviousLockPath:     path.Join("artifacts", "assembly", manifest.AssemblyID, "generated-project-lock.json"),
	}, nil
}

func (r *TrustedContextResolver) resolveCatalog(scope string) (*machinecatalog.Catalog, string, error) {
	switch scope {
	case "ordinary":
		if r.ordinary != nil {
			return r.ordinary, scope, nil
		}
	case "experimental":
		if r.experimental != nil {
			return r.experimental, scope, nil
		}
	}
	return nil, "", core.ErrConflict
}

type manifestContextDocument struct {
	AssemblyID       string                     `json:"assembly_id"`
	Product          generation.ArtifactProduct `json:"product"`
	CatalogChecksum  string                     `json:"catalog_checksum"`
	ManifestChecksum string                     `json:"manifest_checksum"`
}

func decodeManifestDocument(document json.RawMessage) (manifestContextDocument, error) {
	var value manifestContextDocument
	if err := json.Unmarshal(document, &value); err != nil || value.AssemblyID == "" || value.Product.ProductID == "" || value.Product.OfficialTenantID == "" || len(value.Product.Applications) == 0 || value.CatalogChecksum == "" || value.ManifestChecksum == "" {
		return manifestContextDocument{}, core.ErrDocumentInvalid
	}
	return value, nil
}

func decodePlanCatalogContext(document json.RawMessage) (string, string, error) {
	var value struct {
		CatalogSnapshot struct {
			Scope    string `json:"scope"`
			Checksum string `json:"checksum"`
		} `json:"catalog_snapshot"`
	}
	if err := json.Unmarshal(document, &value); err != nil || (value.CatalogSnapshot.Scope != "ordinary" && value.CatalogSnapshot.Scope != "experimental") || value.CatalogSnapshot.Checksum == "" {
		return "", "", core.ErrDocumentInvalid
	}
	return value.CatalogSnapshot.Scope, value.CatalogSnapshot.Checksum, nil
}

func digestDocument(document json.RawMessage) string {
	digest, err := machinecontract.Digest(document)
	if err != nil {
		return ""
	}
	return "sha256:" + digest
}

func equalDigest(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func trustedContextError(err error) error {
	if err != nil {
		return err
	}
	return core.ErrConflict
}
