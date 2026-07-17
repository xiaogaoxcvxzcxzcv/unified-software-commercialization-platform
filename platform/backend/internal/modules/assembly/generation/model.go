package generation

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	ErrInvalidInput      = errors.New("invalid generator input")
	ErrPlanMismatch      = errors.New("generator request does not match locked plan")
	ErrTargetChanged     = errors.New("generator target changed after inspection")
	ErrTargetUnsafe      = errors.New("generator target contains an unsafe filesystem entry")
	ErrOwnershipConflict = errors.New("generator output conflicts with product-owned content")
	ErrGeneratedModified = errors.New("generated file differs from its locked baseline")
	ErrIntegrationRegion = errors.New("integration generated region is missing, duplicated, or modified")
	ErrCommitFailed      = errors.New("generator atomic commit failed")
	ErrRollbackFailed    = errors.New("generator rollback failed")
	ErrSourceUnavailable = errors.New("locked generator source is unavailable")
	ErrSourceChecksum    = errors.New("locked generator source checksum mismatch")
	ErrTemplateInvalid   = errors.New("strict template is invalid")
	ErrTemplateValue     = errors.New("strict template value is invalid")
	ErrUnsupportedRender = errors.New("unsupported render strategy")
	ErrDuplicateOutput   = errors.New("duplicate or overlapping generator output")
	ErrArtifactConflict  = errors.New("generator artifact path already contains different content")
	ErrArtifactStore     = errors.New("generator artifact persistence failed")
)

type MergeSpec struct {
	Strategy      string `json:"strategy"`
	RegionID      string `json:"region_id"`
	CommentPrefix string `json:"comment_prefix"`
}

type OutputSpec struct {
	Path           string     `json:"path"`
	Ownership      string     `json:"ownership"`
	SourceID       string     `json:"source_id"`
	SourceVersion  string     `json:"source_version"`
	SourcePath     string     `json:"source_path"`
	SourceSHA256   string     `json:"source_sha256"`
	RenderStrategy string     `json:"render_strategy"`
	ContentType    string     `json:"content_type"`
	Merge          *MergeSpec `json:"merge,omitempty"`
}

type Tool struct {
	GeneratorID string `json:"generator_id"`
	Version     string `json:"version"`
	Checksum    string `json:"checksum"`
}

type SecretRef struct {
	Provider    string `json:"provider"`
	Key         string `json:"key"`
	Environment string `json:"environment"`
}

type InputPaths struct {
	BlueprintPath        string `json:"blueprint_path"`
	PlanPath             string `json:"plan_path"`
	PreviousManifestPath string `json:"previous_manifest_path,omitempty"`
	PreviousLockPath     string `json:"previous_lock_path,omitempty"`
}

type ExistingFile struct {
	Path      string `json:"path"`
	Ownership string `json:"ownership"`
	SHA256    string `json:"sha256"`
}

type Determinism struct {
	Timezone  string `json:"timezone"`
	Locale    string `json:"locale"`
	SortOrder string `json:"sort_order"`
}

type ArtifactApplication struct {
	PlanApplicationID string `json:"plan_application_id"`
	ApplicationID     string `json:"application_id"`
}

type ArtifactProduct struct {
	ProductID        string                `json:"product_id"`
	OfficialTenantID string                `json:"official_tenant_id"`
	Applications     []ArtifactApplication `json:"applications"`
}

type ArtifactBlueprint struct {
	BlueprintID string `json:"blueprint_id"`
	Version     int64  `json:"version"`
	Checksum    string `json:"checksum"`
}

type Evidence struct {
	EvidenceID string `json:"evidence_id"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
}

type ArtifactPaths struct {
	ArtifactStagingPath  string `json:"artifact_staging_path"`
	AssemblyManifestPath string `json:"assembly_manifest_path"`
	GeneratedLockPath    string `json:"generated_lock_path"`
	RollbackPointPath    string `json:"rollback_point_path"`
	CommitJournalPath    string `json:"commit_journal_path"`
	ResultPath           string `json:"result_path"`
	DiagnosticDirectory  string `json:"diagnostic_directory"`
}

type ArtifactContext struct {
	AssemblyID           string            `json:"assembly_id"`
	LockID               string            `json:"lock_id"`
	RollbackID           string            `json:"rollback_id"`
	RunID                string            `json:"run_id,omitempty"`
	LifecycleOperationID string            `json:"lifecycle_operation_id,omitempty"`
	Product              ArtifactProduct   `json:"product"`
	Blueprint            ArtifactBlueprint `json:"blueprint"`
	CatalogChecksum      string            `json:"catalog_checksum"`
	Evidence             []Evidence        `json:"evidence"`
	CreatedAt            string            `json:"created_at"`
	Paths                ArtifactPaths     `json:"paths"`
}

type Request struct {
	SchemaVersion          string          `json:"schema_version"`
	RequestID              string          `json:"request_id"`
	Operation              string          `json:"operation"`
	WorkspaceRef           string          `json:"workspace_ref"`
	PlanChecksum           string          `json:"plan_checksum"`
	TargetSnapshotChecksum string          `json:"target_snapshot_checksum"`
	Generator              Tool            `json:"generator"`
	Inputs                 InputPaths      `json:"inputs"`
	ArtifactContext        ArtifactContext `json:"artifact_context"`
	DesiredOutputs         []OutputSpec    `json:"desired_outputs"`
	ExistingFiles          []ExistingFile  `json:"existing_files"`
	ProtectedPaths         []string        `json:"protected_paths"`
	SecretRefs             []SecretRef     `json:"secret_refs"`
	StagingPath            string          `json:"staging_path"`
	RollbackPointPath      string          `json:"rollback_point_path"`
	EjectPaths             []string        `json:"eject_paths,omitempty"`
	ConflictPolicy         string          `json:"conflict_policy"`
	Determinism            Determinism     `json:"determinism"`
}

type LockedDependency struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Checksum string `json:"checksum"`
}

type Plan struct {
	PlanID           string `json:"plan_id"`
	PlanChecksum     string `json:"plan_checksum"`
	BlueprintID      string `json:"blueprint_id"`
	BlueprintVersion int64  `json:"blueprint_version"`
	CatalogSnapshot  struct {
		Scope    string `json:"scope"`
		Checksum string `json:"checksum"`
	} `json:"catalog_snapshot"`
	Generator          Tool         `json:"generator"`
	ExpectedOutputs    []OutputSpec `json:"expected_outputs"`
	RequiredSecretRefs []SecretRef  `json:"required_secret_refs"`
	Packages           []struct {
		PackageID string `json:"package_id"`
		Version   string `json:"version"`
		Checksum  string `json:"checksum"`
	} `json:"packages"`
	Applications []struct {
		ApplicationID string `json:"application_id"`
		Template      struct {
			TemplateID string `json:"template_id"`
			Version    string `json:"version"`
			Checksum   string `json:"checksum"`
		} `json:"template"`
	} `json:"applications"`
	SDKs []struct {
		SDKID    string `json:"sdk_id"`
		Version  string `json:"version"`
		Checksum string `json:"checksum"`
	} `json:"sdks"`
}

type Input struct {
	Request           Request
	Blueprint         json.RawMessage
	Plan              json.RawMessage
	EvidenceDocuments map[string][]byte
}

type RenderedFile struct {
	OutputSpec
	Bytes                []byte
	SHA256               string
	GeneratedSHA256      string
	SourceManifestSHA256 string
}

type LockedFile struct {
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
	Merge           *MergeSpec `json:"merge,omitempty"`
	UpdatePolicy    string     `json:"update_policy"`
}

type ProjectLock struct {
	SchemaVersion            string             `json:"schema_version"`
	LockID                   string             `json:"lock_id,omitempty"`
	RunID                    string             `json:"run_id,omitempty"`
	LifecycleOperationID     string             `json:"lifecycle_operation_id,omitempty"`
	AssemblyManifestChecksum string             `json:"assembly_manifest_checksum,omitempty"`
	BlueprintChecksum        string             `json:"blueprint_checksum,omitempty"`
	CatalogChecksum          string             `json:"catalog_checksum,omitempty"`
	TargetSnapshotChecksum   string             `json:"target_snapshot_checksum,omitempty"`
	RollbackPointPath        string             `json:"rollback_point_path,omitempty"`
	Generator                Tool               `json:"generator,omitempty"`
	Packages                 []LockedDependency `json:"packages,omitempty"`
	Templates                []LockedDependency `json:"templates,omitempty"`
	SDKs                     []LockedDependency `json:"sdks,omitempty"`
	Files                    []LockedFile       `json:"files"`
	CreatedAt                string             `json:"created_at,omitempty"`
	LockChecksum             string             `json:"lock_checksum,omitempty"`
}

type Diagnostic struct {
	Code              string
	Category          string
	Message           string
	Path              string
	RelatedPaths      []string
	ExpectedOwnership string
	ActualOwnership   string
	ExpectedSHA256    string
	ActualSHA256      string
	Retryable         bool
	Remediation       []string
}

type TargetSnapshot struct {
	Files    []ExistingFile
	Checksum string
}

type FileChange struct {
	Path            string
	Ownership       string
	Action          string
	Bytes           []byte
	SHA256          string
	GeneratedSHA256 string
	PreviousSHA256  string
}

type PreparedChangeSet struct {
	Snapshot    TargetSnapshot
	Changes     []FileChange
	Preserved   []ExistingFile
	Diagnostics []Diagnostic
}

type CommitResult struct {
	FilesWritten            []FileChange
	PreservedFiles          []ExistingFile
	StagingCleanupCompleted bool
	AtomicCommitCompleted   bool
	RollbackAttempted       bool
	RollbackCompleted       bool
	TargetUnchanged         bool
}

type Result struct {
	Files []RenderedFile
}

type SourceStore interface {
	ReadLockedSource(sourceID, version, manifestChecksum, sourcePath, sourceChecksum string) ([]byte, error)
}

type Renderer interface {
	Render(context.Context, Input) (Result, error)
}
