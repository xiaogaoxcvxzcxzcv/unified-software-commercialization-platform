package machinecatalog

type Availability struct {
	Target       string   `json:"target"`
	DeliveryMode string   `json:"delivery_mode"`
	Environments []string `json:"environments"`
	Visibility   string   `json:"visibility"`
	Readiness    string   `json:"readiness"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

type Requirement struct {
	PackageID    string `json:"package_id"`
	VersionRange string `json:"version_range"`
	Reason       string `json:"reason,omitempty"`
}

type TemplateRequirement struct {
	TemplateID   string `json:"template_id"`
	VersionRange string `json:"version_range"`
}

type ContentFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Kind   string `json:"kind"`
}

type SecretReference struct {
	Provider    string `json:"provider"`
	Key         string `json:"key"`
	Environment string `json:"environment"`
}

type GeneratedOutput struct {
	Path           string            `json:"path"`
	Ownership      string            `json:"ownership"`
	SourcePath     string            `json:"source_path"`
	SourceSHA256   string            `json:"source_sha256"`
	RenderStrategy string            `json:"render_strategy"`
	ContentType    string            `json:"content_type"`
	Merge          *IntegrationMerge `json:"merge,omitempty"`
}

type IntegrationMerge struct {
	Strategy      string `json:"strategy"`
	RegionID      string `json:"region_id"`
	CommentPrefix string `json:"comment_prefix"`
}

type PackageManifest struct {
	SchemaVersion                string                `json:"schema_version"`
	PackageID                    string                `json:"package_id"`
	Version                      string                `json:"version"`
	Name                         string                `json:"name"`
	UserValue                    string                `json:"user_value"`
	LifecycleStatus              string                `json:"lifecycle_status"`
	Availability                 []Availability        `json:"availability"`
	Dependencies                 []Requirement         `json:"dependencies"`
	Conflicts                    []Requirement         `json:"conflicts"`
	SupportedTargets             []string              `json:"supported_targets"`
	SupportedDeliveryModes       []string              `json:"supported_delivery_modes"`
	RequiredPermissions          []string              `json:"required_permissions"`
	BackendCapabilities          []string              `json:"backend_capabilities"`
	ConfigSchemaPath             string                `json:"config_schema_path"`
	SecretRefs                   []SecretReference     `json:"secret_refs"`
	ProviderRequirements         []string              `json:"provider_requirements"`
	OptionalProviderRequirements []string              `json:"optional_provider_requirements"`
	GeneratedOutputs             []GeneratedOutput     `json:"generated_outputs"`
	AdminBlocks                  []string              `json:"admin_blocks"`
	ClientBlocks                 []string              `json:"client_blocks"`
	UITemplateCompatibility      []TemplateRequirement `json:"ui_template_compatibility"`
	ContentFiles                 []ContentFile         `json:"content_files"`
	ContentTreeSHA256            string                `json:"content_tree_sha256"`
	ManifestSHA256               string                `json:"manifest_sha256"`
}

type Entrypoint struct {
	Target         string            `json:"target"`
	DeliveryMode   string            `json:"delivery_mode"`
	Path           string            `json:"path"`
	Ownership      string            `json:"ownership"`
	SourcePath     string            `json:"source_path"`
	SourceSHA256   string            `json:"source_sha256"`
	RenderStrategy string            `json:"render_strategy"`
	ContentType    string            `json:"content_type"`
	Merge          *IntegrationMerge `json:"merge,omitempty"`
}

type TemplateManifest struct {
	SchemaVersion          string         `json:"schema_version"`
	TemplateID             string         `json:"template_id"`
	Version                string         `json:"version"`
	Name                   string         `json:"name"`
	Availability           []Availability `json:"availability"`
	SupportedTargets       []string       `json:"supported_targets"`
	SupportedDeliveryModes []string       `json:"supported_delivery_modes"`
	SupportedBlocks        []string       `json:"supported_blocks"`
	PackageCompatibility   []Requirement  `json:"package_compatibility"`
	Entrypoints            []Entrypoint   `json:"entrypoints"`
	SourceRoot             string         `json:"source_root"`
	PreviewAssets          []string       `json:"preview_assets"`
	ContentFiles           []ContentFile  `json:"content_files"`
	ContentTreeSHA256      string         `json:"content_tree_sha256"`
	ManifestSHA256         string         `json:"manifest_sha256"`
}

type ToolProtocol struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type ToolExecution struct {
	Mode      string `json:"mode"`
	AdapterID string `json:"adapter_id,omitempty"`
	Path      string `json:"path,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type ToolEvidence struct {
	Type         string `json:"type"`
	Target       string `json:"target"`
	DeliveryMode string `json:"delivery_mode"`
	Environment  string `json:"environment"`
	Status       string `json:"status"`
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
}

type ToolManifest struct {
	SchemaVersion          string         `json:"schema_version"`
	ToolKind               string         `json:"tool_kind"`
	ToolID                 string         `json:"tool_id"`
	Version                string         `json:"version"`
	Name                   string         `json:"name"`
	CatalogScope           string         `json:"catalog_scope"`
	Readiness              string         `json:"readiness"`
	SupportedTargets       []string       `json:"supported_targets"`
	SupportedDeliveryModes []string       `json:"supported_delivery_modes"`
	SupportedEnvironments  []string       `json:"supported_environments"`
	Protocol               ToolProtocol   `json:"protocol"`
	PlatformContractRange  string         `json:"platform_contract_range"`
	Execution              ToolExecution  `json:"execution"`
	Evidence               []ToolEvidence `json:"evidence"`
	ContentFiles           []ContentFile  `json:"content_files"`
	ContentTreeSHA256      string         `json:"content_tree_sha256"`
	ManifestSHA256         string         `json:"manifest_sha256"`
}

type ExtensionRoute struct {
	RouteID            string `json:"route_id"`
	Target             string `json:"target"`
	Path               string `json:"path"`
	EntryPath          string `json:"entry_path"`
	RequiredPermission string `json:"required_permission"`
}

type ExtensionNavigationItem struct {
	ItemID             string `json:"item_id"`
	Target             string `json:"target"`
	LabelKey           string `json:"label_key"`
	RouteID            string `json:"route_id"`
	RequiredPermission string `json:"required_permission"`
}

type ExtensionSlot struct {
	SlotID    string `json:"slot_id"`
	Target    string `json:"target"`
	EntryPath string `json:"entry_path"`
}

type ExtensionAdminItem struct {
	ItemID             string `json:"item_id"`
	LabelKey           string `json:"label_key"`
	Path               string `json:"path"`
	EntryPath          string `json:"entry_path"`
	RequiredPermission string `json:"required_permission"`
}

type ExtensionLifecyclePlan struct {
	Strategy string   `json:"strategy"`
	Steps    []string `json:"steps"`
}

type ExtensionRetentionPolicy struct {
	Mode          string `json:"mode"`
	RetentionDays int    `json:"retention_days"`
}

type ExtensionManifest struct {
	SchemaVersion          string                    `json:"schema_version"`
	ExtensionID            string                    `json:"extension_id"`
	Version                string                    `json:"version"`
	ProductCode            string                    `json:"product_code"`
	CatalogScope           string                    `json:"catalog_scope"`
	Readiness              string                    `json:"readiness"`
	SupportedTargets       []string                  `json:"supported_targets"`
	SupportedDeliveryModes []string                  `json:"supported_delivery_modes"`
	SupportedEnvironments  []string                  `json:"supported_environments"`
	RequiredPermissions    []string                  `json:"required_permissions"`
	PublicAPIOperations    []string                  `json:"public_api_operations"`
	PublishedEvents        []string                  `json:"published_events"`
	SubscribedEvents       []string                  `json:"subscribed_events"`
	Routes                 []ExtensionRoute          `json:"routes"`
	NavigationItems        []ExtensionNavigationItem `json:"navigation_items"`
	Slots                  []ExtensionSlot           `json:"slots"`
	AdminItems             []ExtensionAdminItem      `json:"admin_items"`
	DataNamespace          string                    `json:"data_namespace"`
	OwnedTables            []string                  `json:"owned_tables"`
	ConsumedServices       []string                  `json:"consumed_services"`
	OwnedPaths             []string                  `json:"owned_paths"`
	InstallPlan            ExtensionLifecyclePlan    `json:"install_plan"`
	UninstallPlan          ExtensionLifecyclePlan    `json:"uninstall_plan"`
	RetentionPolicy        ExtensionRetentionPolicy  `json:"retention_policy"`
	ContentFiles           []ContentFile             `json:"content_files"`
	ContentTreeSHA256      string                    `json:"content_tree_sha256"`
	ManifestSHA256         string                    `json:"manifest_sha256"`
	ManifestPath           string                    `json:"-"`
}

type ExtensionRequirement struct {
	ExtensionID  string
	Version      string
	ManifestPath string
}

type ResolveRequest struct {
	Packages      []Requirement
	Extensions    []ExtensionRequirement
	ProductCode   string
	TemplateID    string
	TemplateRange string
	Target        string
	DeliveryMode  string
	Environment   string
}

type Resolution struct {
	Packages   []PackageManifest
	Template   TemplateManifest
	Extensions []ExtensionManifest
	Snapshot   CatalogSnapshot
}

type CatalogSnapshot struct {
	SchemaVersion       string                  `json:"schema_version"`
	Revision            string                  `json:"revision"`
	CatalogScope        string                  `json:"catalog_scope"`
	Packages            []SnapshotItem          `json:"packages"`
	Templates           []SnapshotItem          `json:"templates"`
	Extensions          []ExtensionSnapshotItem `json:"extensions"`
	Generators          []ToolSnapshotItem      `json:"generators"`
	SDKs                []ToolSnapshotItem      `json:"sdks"`
	PermissionCatalog   VersionedInput          `json:"permission_catalog"`
	FeatureBlockCatalog VersionedInput          `json:"feature_block_catalog"`
	SchemaCatalog       VersionedInput          `json:"schema_catalog"`
	SnapshotSHA256      string                  `json:"snapshot_sha256"`
}

type ExtensionSnapshotItem struct {
	ExtensionID            string   `json:"extension_id"`
	Version                string   `json:"version"`
	ProductCode            string   `json:"product_code"`
	ManifestPath           string   `json:"manifest_path"`
	ManifestSHA256         string   `json:"manifest_sha256"`
	ContentTreeSHA256      string   `json:"content_tree_sha256"`
	SupportedTargets       []string `json:"supported_targets"`
	SupportedDeliveryModes []string `json:"supported_delivery_modes"`
	SupportedEnvironments  []string `json:"supported_environments"`
	DataNamespace          string   `json:"data_namespace"`
}

type ToolSnapshotItem struct {
	ToolID                 string        `json:"tool_id"`
	Version                string        `json:"version"`
	ManifestSHA256         string        `json:"manifest_sha256"`
	ContentTreeSHA256      string        `json:"content_tree_sha256"`
	Protocol               ToolProtocol  `json:"protocol"`
	Execution              ToolExecution `json:"execution"`
	SupportedTargets       []string      `json:"supported_targets"`
	SupportedDeliveryModes []string      `json:"supported_delivery_modes"`
	SupportedEnvironments  []string      `json:"supported_environments"`
}

type VersionedInput struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type SnapshotItem struct {
	ID                     string         `json:"id"`
	Version                string         `json:"version"`
	ManifestSHA256         string         `json:"manifest_sha256"`
	ContentTreeSHA256      string         `json:"content_tree_sha256"`
	Availability           []Availability `json:"availability"`
	Dependencies           []Requirement  `json:"dependencies"`
	Conflicts              []Requirement  `json:"conflicts"`
	SupportedTargets       []string       `json:"supported_targets"`
	SupportedDeliveryModes []string       `json:"supported_delivery_modes"`
	BackendCapabilities    []string       `json:"backend_capabilities"`
}

type PermissionCatalog interface {
	Version() string
	Checksum() string
	ValidateRequiredPermissions([]string) error
}
