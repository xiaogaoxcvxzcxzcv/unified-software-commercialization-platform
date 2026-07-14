package generation

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type PreviousArtifacts struct {
	ManifestSHA256 string
	LockSHA256     string
}

type ArtifactBundle struct {
	AssemblyManifest  []byte
	GeneratedLock     []byte
	RollbackPoint     []byte
	CommitJournal     []byte
	GeneratorResult   []byte
	Backups           map[string][]byte
	EvidenceDocuments map[string][]byte
	FinalSnapshot     TargetSnapshot
}

type assemblyManifestDocument struct {
	SchemaVersion    string             `json:"schema_version"`
	AssemblyID       string             `json:"assembly_id"`
	RunID            string             `json:"run_id"`
	Product          ArtifactProduct    `json:"product"`
	Blueprint        ArtifactBlueprint  `json:"blueprint"`
	CatalogChecksum  string             `json:"catalog_checksum"`
	Packages         []manifestPackage  `json:"packages"`
	Templates        []manifestTemplate `json:"templates"`
	SDKs             []manifestSDK      `json:"sdks"`
	Outputs          []manifestOutput   `json:"outputs"`
	Evidence         []Evidence         `json:"evidence"`
	SecretRefs       []SecretRef        `json:"secret_refs"`
	CreatedAt        string             `json:"created_at"`
	ManifestChecksum string             `json:"manifest_checksum"`
}

type manifestPackage struct {
	PackageID string `json:"package_id"`
	Version   string `json:"version"`
	Checksum  string `json:"checksum"`
}

type manifestTemplate struct {
	TemplateID string `json:"template_id"`
	Version    string `json:"version"`
	Checksum   string `json:"checksum"`
}

type manifestSDK struct {
	SDKID    string `json:"sdk_id"`
	Version  string `json:"version"`
	Checksum string `json:"checksum"`
}

type manifestOutput struct {
	Path           string     `json:"path"`
	Ownership      string     `json:"ownership"`
	SHA256         string     `json:"sha256"`
	SourceID       string     `json:"source_id"`
	SourceVersion  string     `json:"source_version"`
	SourcePath     string     `json:"source_path"`
	SourceSHA256   string     `json:"source_sha256"`
	RenderStrategy string     `json:"render_strategy"`
	ContentType    string     `json:"content_type"`
	Merge          *MergeSpec `json:"merge,omitempty"`
}

type generatedLockDocument struct {
	SchemaVersion            string             `json:"schema_version"`
	LockID                   string             `json:"lock_id"`
	AssemblyManifestChecksum string             `json:"assembly_manifest_checksum"`
	BlueprintChecksum        string             `json:"blueprint_checksum"`
	CatalogChecksum          string             `json:"catalog_checksum"`
	TargetSnapshotChecksum   string             `json:"target_snapshot_checksum"`
	RollbackPointPath        string             `json:"rollback_point_path"`
	Generator                Tool               `json:"generator"`
	Packages                 []LockedDependency `json:"packages"`
	Templates                []LockedDependency `json:"templates"`
	SDKs                     []LockedDependency `json:"sdks"`
	Files                    []LockedFile       `json:"files"`
	CreatedAt                string             `json:"created_at"`
	LockChecksum             string             `json:"lock_checksum"`
}

type rollbackPointDocument struct {
	SchemaVersion          string         `json:"schema_version"`
	RollbackID             string         `json:"rollback_id"`
	WorkspaceRef           string         `json:"workspace_ref"`
	TargetSnapshotChecksum string         `json:"target_snapshot_checksum"`
	PreviousState          string         `json:"previous_state"`
	ManifestPath           string         `json:"manifest_path,omitempty"`
	ManifestSHA256         string         `json:"manifest_sha256,omitempty"`
	LockPath               string         `json:"lock_path,omitempty"`
	LockSHA256             string         `json:"lock_sha256,omitempty"`
	Files                  []rollbackFile `json:"files"`
	RollbackChecksum       string         `json:"rollback_checksum"`
}

type rollbackFile struct {
	Path       string `json:"path"`
	Ownership  string `json:"ownership"`
	Action     string `json:"action"`
	SHA256     string `json:"sha256,omitempty"`
	BackupPath string `json:"backup_path,omitempty"`
}

type commitJournalDocument struct {
	SchemaVersion          string        `json:"schema_version"`
	RequestID              string        `json:"request_id"`
	WorkspaceRef           string        `json:"workspace_ref"`
	TargetSnapshotChecksum string        `json:"target_snapshot_checksum"`
	State                  string        `json:"state"`
	Changes                []journalFile `json:"changes"`
	RollbackAttempted      bool          `json:"rollback_attempted"`
	RollbackCompleted      bool          `json:"rollback_completed"`
	JournalChecksum        string        `json:"journal_checksum"`
}

type journalFile struct {
	Path         string `json:"path"`
	Ownership    string `json:"ownership"`
	Action       string `json:"action"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	AfterSHA256  string `json:"after_sha256"`
	BackupPath   string `json:"backup_path,omitempty"`
}

type generatorResultDocument struct {
	SchemaVersion           string          `json:"schema_version"`
	RequestID               string          `json:"request_id"`
	Status                  string          `json:"status"`
	FilesWritten            []writtenFile   `json:"files_written"`
	PreservedFiles          []preservedFile `json:"preserved_files"`
	DiagnosticIDs           []string        `json:"diagnostic_ids"`
	StagingCleanupCompleted bool            `json:"staging_cleanup_completed"`
	AtomicCommitCompleted   bool            `json:"atomic_commit_completed"`
	RollbackAttempted       bool            `json:"rollback_attempted"`
	RollbackCompleted       bool            `json:"rollback_completed"`
	TargetUnchanged         bool            `json:"target_unchanged"`
	RollbackPointPath       string          `json:"rollback_point_path,omitempty"`
	CommitJournalPath       string          `json:"commit_journal_path,omitempty"`
	AssemblyManifestPath    string          `json:"assembly_manifest_path,omitempty"`
	GeneratedLockPath       string          `json:"generated_lock_path,omitempty"`
	ResultChecksum          string          `json:"result_checksum"`
}

type writtenFile struct {
	Path      string `json:"path"`
	Ownership string `json:"ownership"`
	SHA256    string `json:"sha256"`
	Action    string `json:"action"`
}

type preservedFile struct {
	Path      string `json:"path"`
	Ownership string `json:"ownership"`
	SHA256    string `json:"sha256"`
}

type generatorDiagnosticDocument struct {
	SchemaVersion     string   `json:"schema_version"`
	DiagnosticID      string   `json:"diagnostic_id"`
	RequestID         string   `json:"request_id"`
	Severity          string   `json:"severity"`
	Category          string   `json:"category"`
	Code              string   `json:"code"`
	Message           string   `json:"message"`
	Blocking          bool     `json:"blocking"`
	Retryable         bool     `json:"retryable"`
	Path              string   `json:"path,omitempty"`
	RelatedPaths      []string `json:"related_paths"`
	ExpectedOwnership string   `json:"expected_ownership,omitempty"`
	ActualOwnership   string   `json:"actual_ownership,omitempty"`
	ExpectedSHA256    string   `json:"expected_sha256,omitempty"`
	ActualSHA256      string   `json:"actual_sha256,omitempty"`
	Remediation       []string `json:"remediation"`
}

type FailureArtifacts struct {
	Result      []byte
	Diagnostics map[string][]byte
	Status      string
}

func BuildArtifactBundle(targetRoot string, request Request, planRaw json.RawMessage, evidenceDocuments map[string][]byte, prepared PreparedChangeSet, previous PreviousArtifacts) (ArtifactBundle, error) {
	if len(prepared.Diagnostics) != 0 {
		return ArtifactBundle{}, ErrOwnershipConflict
	}
	var planDocument Plan
	if err := json.Unmarshal(planRaw, &planDocument); err != nil || validateRequestPlan(request, planDocument) != nil {
		return ArtifactBundle{}, ErrPlanMismatch
	}
	if err := validateArtifactContext(request, planDocument, previous); err != nil {
		return ArtifactBundle{}, err
	}
	verifiedEvidence, err := verifyEvidenceDocuments(request.ArtifactContext.Evidence, evidenceDocuments)
	if err != nil {
		return ArtifactBundle{}, err
	}

	finalSnapshot, err := snapshotAfterChanges(prepared.Snapshot, prepared.Changes)
	if err != nil {
		return ArtifactBundle{}, err
	}
	rollback, journal, backups, err := buildRollbackDocuments(targetRoot, request, prepared, previous)
	if err != nil {
		return ArtifactBundle{}, err
	}
	manifest := buildAssemblyManifest(request, planDocument, prepared.Changes)
	manifestRaw, manifestChecksum, err := marshalWithEmbeddedDigest(manifest, "manifest_checksum")
	if err != nil {
		return ArtifactBundle{}, err
	}
	lock := buildGeneratedLock(request, planDocument, prepared, finalSnapshot.Checksum, manifestChecksum)
	lockRaw, _, err := marshalWithEmbeddedDigest(lock, "lock_checksum")
	if err != nil {
		return ArtifactBundle{}, err
	}
	rollbackRaw, _, err := marshalWithEmbeddedDigest(rollback, "rollback_checksum")
	if err != nil {
		return ArtifactBundle{}, err
	}
	journalRaw, _, err := marshalWithEmbeddedDigest(journal, "journal_checksum")
	if err != nil {
		return ArtifactBundle{}, err
	}
	result := buildSucceededResult(request, prepared)
	resultRaw, _, err := marshalWithEmbeddedDigest(result, "result_checksum")
	if err != nil {
		return ArtifactBundle{}, err
	}
	return ArtifactBundle{
		AssemblyManifest: manifestRaw, GeneratedLock: lockRaw, RollbackPoint: rollbackRaw,
		CommitJournal: journalRaw, GeneratorResult: resultRaw, Backups: backups, EvidenceDocuments: verifiedEvidence, FinalSnapshot: finalSnapshot,
	}, nil
}

func verifyEvidenceDocuments(evidence []Evidence, documents map[string][]byte) (map[string][]byte, error) {
	if len(evidence) != len(documents) {
		return nil, ErrInvalidInput
	}
	verified := make(map[string][]byte, len(evidence))
	for _, item := range evidence {
		content, ok := documents[item.Path]
		if !ok || len(content) == 0 || !digestEqual(digestBytes(content), item.SHA256) {
			return nil, ErrPlanMismatch
		}
		verified[item.Path] = append([]byte(nil), content...)
	}
	return verified, nil
}

func BuildFailureArtifacts(request Request, prepared PreparedChangeSet, cause error, commit CommitResult) (FailureArtifacts, error) {
	status := "failed"
	if errors.Is(cause, ErrOwnershipConflict) || errors.Is(cause, ErrGeneratedModified) || errors.Is(cause, ErrIntegrationRegion) || errors.Is(cause, ErrTargetChanged) {
		status = "conflict"
	}
	diagnosticValues := prepared.Diagnostics
	if len(diagnosticValues) == 0 {
		diagnosticValues = []Diagnostic{genericDiagnostic(cause)}
	}
	diagnosticDocuments := make(map[string][]byte, len(diagnosticValues))
	diagnosticIDs := make([]string, 0, len(diagnosticValues))
	for index, source := range diagnosticValues {
		id := "diagnostic." + strings.TrimPrefix(digestBytes([]byte(fmt.Sprintf("%s:%d", request.RequestID, index+1))), "sha256:")[:24]
		document := generatorDiagnosticDocument{
			SchemaVersion: "1.0.0", DiagnosticID: id, RequestID: request.RequestID, Severity: "error",
			Category: source.Category, Code: source.Code, Message: source.Message, Blocking: true, Retryable: source.Retryable,
			Path: source.Path, RelatedPaths: append([]string(nil), source.RelatedPaths...), ExpectedOwnership: source.ExpectedOwnership,
			ActualOwnership: source.ActualOwnership, ExpectedSHA256: source.ExpectedSHA256, ActualSHA256: source.ActualSHA256,
			Remediation: append([]string(nil), source.Remediation...),
		}
		if len(document.RelatedPaths) == 0 {
			document.RelatedPaths = []string{}
		}
		if len(document.Remediation) == 0 {
			document.Remediation = []string{"retry after the underlying condition has been corrected"}
		}
		raw, err := json.Marshal(document)
		if err != nil {
			return FailureArtifacts{}, err
		}
		canonical, err := machinecontract.Canonicalize(raw)
		if err != nil {
			return FailureArtifacts{}, err
		}
		finalPath := path.Join(request.ArtifactContext.Paths.DiagnosticDirectory, id+".json")
		diagnosticDocuments[finalPath] = canonical
		diagnosticIDs = append(diagnosticIDs, id)
	}
	preserved := make([]preservedFile, 0, len(prepared.Preserved))
	for _, file := range prepared.Preserved {
		preserved = append(preserved, preservedFile{Path: file.Path, Ownership: file.Ownership, SHA256: file.SHA256})
	}
	result := generatorResultDocument{
		SchemaVersion: "1.0.0", RequestID: request.RequestID, Status: status, FilesWritten: []writtenFile{},
		PreservedFiles: preserved, DiagnosticIDs: diagnosticIDs, StagingCleanupCompleted: commit.StagingCleanupCompleted,
		AtomicCommitCompleted: false, RollbackAttempted: commit.RollbackAttempted, RollbackCompleted: commit.RollbackCompleted,
		TargetUnchanged: commit.TargetUnchanged, ResultChecksum: digestBytes(nil),
	}
	if status == "conflict" {
		result.TargetUnchanged = true
	}
	if commit.RollbackAttempted {
		result.RollbackPointPath = request.ArtifactContext.Paths.RollbackPointPath
		result.CommitJournalPath = request.ArtifactContext.Paths.CommitJournalPath
	}
	resultRaw, _, err := marshalWithEmbeddedDigest(result, "result_checksum")
	if err != nil {
		return FailureArtifacts{}, err
	}
	return FailureArtifacts{Result: resultRaw, Diagnostics: diagnosticDocuments, Status: status}, nil
}

func genericDiagnostic(cause error) Diagnostic {
	switch {
	case errors.Is(cause, ErrTargetChanged):
		return Diagnostic{Code: "GENERATOR_TARGET_CHANGED", Category: "conflict", Message: "the target changed after inspection", Retryable: true, Remediation: []string{"inspect the target changes and create a new generation request"}}
	case errors.Is(cause, ErrTargetUnsafe):
		return Diagnostic{Code: "GENERATOR_TARGET_UNSAFE", Category: "security", Message: "the target contains an unsafe filesystem entry", Retryable: false, Remediation: []string{"remove linked, special, or escaping filesystem entries before retrying"}}
	case errors.Is(cause, ErrPlanMismatch):
		return Diagnostic{Code: "GENERATOR_PLAN_MISMATCH", Category: "checksum", Message: "the request does not match the locked assembly plan", Retryable: false, Remediation: []string{"create a new request from the confirmed plan"}}
	case errors.Is(cause, ErrCommitFailed):
		return Diagnostic{Code: "GENERATOR_COMMIT_FAILED", Category: "io", Message: "the atomic file commit failed", Retryable: true, Remediation: []string{"inspect the commit journal and retry after the filesystem error is resolved"}}
	default:
		return Diagnostic{Code: "GENERATOR_EXECUTION_FAILED", Category: "io", Message: "the generator failed before completion", Retryable: false, Remediation: []string{"inspect the stable diagnostic code and correct the request or environment"}}
	}
}

func validateArtifactContext(request Request, planDocument Plan, previous PreviousArtifacts) error {
	context := request.ArtifactContext
	if !stableIdentifierPattern.MatchString(context.AssemblyID) || !stableIdentifierPattern.MatchString(context.LockID) ||
		!stableIdentifierPattern.MatchString(context.RollbackID) || !stableIdentifierPattern.MatchString(context.RunID) ||
		!stableIdentifierPattern.MatchString(context.Product.ProductID) || !stableIdentifierPattern.MatchString(context.Product.OfficialTenantID) ||
		context.Blueprint.BlueprintID != planDocument.BlueprintID || context.Blueprint.Version != planDocument.BlueprintVersion ||
		!validDigest(context.Blueprint.Checksum) || !digestEqual(context.CatalogChecksum, planDocument.CatalogSnapshot.Checksum) ||
		len(context.Evidence) == 0 || len(context.Product.Applications) == 0 {
		return ErrInvalidInput
	}
	if parsed, err := time.Parse(time.RFC3339Nano, context.CreatedAt); err != nil || parsed.Location() != time.UTC {
		return ErrInvalidInput
	}
	applicationIDs := make([]string, 0, len(planDocument.Applications))
	for _, application := range planDocument.Applications {
		applicationIDs = append(applicationIDs, application.ApplicationID)
	}
	actualApplicationIDs := make([]string, 0, len(context.Product.Applications))
	seenRuntimeApplications := make(map[string]struct{}, len(context.Product.Applications))
	for _, application := range context.Product.Applications {
		if !stableIdentifierPattern.MatchString(application.PlanApplicationID) || !stableIdentifierPattern.MatchString(application.ApplicationID) {
			return ErrInvalidInput
		}
		if _, duplicate := seenRuntimeApplications[application.ApplicationID]; duplicate {
			return ErrInvalidInput
		}
		seenRuntimeApplications[application.ApplicationID] = struct{}{}
		actualApplicationIDs = append(actualApplicationIDs, application.PlanApplicationID)
	}
	if !equalSortedIdentifiers(applicationIDs, actualApplicationIDs) {
		return ErrPlanMismatch
	}
	for _, evidence := range context.Evidence {
		if !stableIdentifierPattern.MatchString(evidence.EvidenceID) || !validDigest(evidence.SHA256) || machinecontract.ValidateSafeRelativePath(evidence.Path) != nil {
			return ErrInvalidInput
		}
	}
	paths := context.Paths
	pathValues := []string{paths.ArtifactStagingPath, paths.AssemblyManifestPath, paths.GeneratedLockPath, paths.RollbackPointPath, paths.CommitJournalPath, paths.ResultPath, paths.DiagnosticDirectory}
	for _, value := range pathValues {
		if machinecontract.ValidateSafeRelativePath(value) != nil {
			return ErrInvalidInput
		}
	}
	finalDirectory := path.Dir(paths.AssemblyManifestPath)
	for _, value := range []string{paths.GeneratedLockPath, paths.RollbackPointPath, paths.CommitJournalPath, paths.ResultPath} {
		if path.Dir(value) != finalDirectory {
			return ErrInvalidInput
		}
	}
	if !strings.HasPrefix(paths.DiagnosticDirectory, finalDirectory+"/") || paths.RollbackPointPath != request.RollbackPointPath || pathsOverlap(paths.ArtifactStagingPath, finalDirectory) {
		return ErrInvalidInput
	}
	presentPaths := request.Inputs.PreviousManifestPath != "" || request.Inputs.PreviousLockPath != ""
	if request.Operation == "generate" {
		if presentPaths || previous != (PreviousArtifacts{}) {
			return ErrInvalidInput
		}
	} else if request.Operation == "upgrade" {
		if request.Inputs.PreviousManifestPath == "" || request.Inputs.PreviousLockPath == "" || !validDigest(previous.ManifestSHA256) || !validDigest(previous.LockSHA256) {
			return ErrInvalidInput
		}
	}
	return nil
}

func snapshotAfterChanges(before TargetSnapshot, changes []FileChange) (TargetSnapshot, error) {
	files := make(map[string]ExistingFile, len(before.Files)+len(changes))
	for _, file := range before.Files {
		files[strings.ToLower(file.Path)] = file
	}
	for _, change := range changes {
		files[strings.ToLower(change.Path)] = ExistingFile{Path: change.Path, Ownership: change.Ownership, SHA256: change.SHA256}
	}
	result := make([]ExistingFile, 0, len(files))
	for _, file := range files {
		result = append(result, file)
	}
	sortExistingFiles(result)
	checksum, err := snapshotChecksum(result)
	if err != nil {
		return TargetSnapshot{}, err
	}
	return TargetSnapshot{Files: result, Checksum: checksum}, nil
}

func buildRollbackDocuments(targetRoot string, request Request, prepared PreparedChangeSet, previous PreviousArtifacts) (rollbackPointDocument, commitJournalDocument, map[string][]byte, error) {
	rollback := rollbackPointDocument{
		SchemaVersion: "1.0.0", RollbackID: request.ArtifactContext.RollbackID, WorkspaceRef: request.WorkspaceRef,
		TargetSnapshotChecksum: prepared.Snapshot.Checksum, PreviousState: "absent",
	}
	if request.Operation == "upgrade" {
		rollback.PreviousState = "present"
		rollback.ManifestPath, rollback.ManifestSHA256 = request.Inputs.PreviousManifestPath, previous.ManifestSHA256
		rollback.LockPath, rollback.LockSHA256 = request.Inputs.PreviousLockPath, previous.LockSHA256
	}
	journal := commitJournalDocument{
		SchemaVersion: "1.0.0", RequestID: request.RequestID, WorkspaceRef: request.WorkspaceRef,
		TargetSnapshotChecksum: prepared.Snapshot.Checksum, State: "prepared", Changes: []journalFile{},
	}
	backups := make(map[string][]byte)
	backupRoot := path.Join(path.Dir(request.ArtifactContext.Paths.RollbackPointPath), "backups")
	for _, change := range prepared.Changes {
		rollbackItem := rollbackFile{Path: change.Path, Ownership: change.Ownership, Action: change.Action}
		journalItem := journalFile{Path: change.Path, Ownership: change.Ownership, Action: change.Action, BeforeSHA256: change.PreviousSHA256, AfterSHA256: change.SHA256}
		if change.Action == "updated" {
			content, err := readSafeWorkspaceFile(targetRoot, change.Path)
			if err != nil || !digestEqual(digestBytes(content), change.PreviousSHA256) {
				return rollbackPointDocument{}, commitJournalDocument{}, nil, ErrTargetChanged
			}
			backupPath := path.Join(backupRoot, change.Path)
			rollbackItem.SHA256, rollbackItem.BackupPath = change.PreviousSHA256, backupPath
			journalItem.BackupPath = backupPath
			backups[backupPath] = append([]byte(nil), content...)
		}
		rollback.Files = append(rollback.Files, rollbackItem)
		journal.Changes = append(journal.Changes, journalItem)
	}
	return rollback, journal, backups, nil
}

func buildAssemblyManifest(request Request, planDocument Plan, changes []FileChange) assemblyManifestDocument {
	packages := make([]manifestPackage, 0, len(planDocument.Packages))
	for _, item := range planDocument.Packages {
		packages = append(packages, manifestPackage{PackageID: item.PackageID, Version: item.Version, Checksum: item.Checksum})
	}
	templatesByID := make(map[string]manifestTemplate)
	for _, application := range planDocument.Applications {
		item := application.Template
		templatesByID[item.TemplateID] = manifestTemplate{TemplateID: item.TemplateID, Version: item.Version, Checksum: item.Checksum}
	}
	templates := make([]manifestTemplate, 0, len(templatesByID))
	for _, item := range templatesByID {
		templates = append(templates, item)
	}
	sdks := make([]manifestSDK, 0, len(planDocument.SDKs))
	for _, item := range planDocument.SDKs {
		sdks = append(sdks, manifestSDK{SDKID: item.SDKID, Version: item.Version, Checksum: item.Checksum})
	}
	changeByName := make(map[string]FileChange, len(changes))
	for _, change := range changes {
		changeByName[change.Path] = change
	}
	outputs := make([]manifestOutput, 0, len(request.DesiredOutputs))
	for _, output := range request.DesiredOutputs {
		change := changeByName[output.Path]
		outputs = append(outputs, manifestOutput{
			Path: output.Path, Ownership: output.Ownership, SHA256: change.SHA256, SourceID: output.SourceID,
			SourceVersion: output.SourceVersion, SourcePath: output.SourcePath, SourceSHA256: output.SourceSHA256,
			RenderStrategy: output.RenderStrategy, ContentType: output.ContentType, Merge: output.Merge,
		})
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].PackageID < packages[j].PackageID })
	sort.Slice(templates, func(i, j int) bool { return templates[i].TemplateID < templates[j].TemplateID })
	sort.Slice(sdks, func(i, j int) bool { return sdks[i].SDKID < sdks[j].SDKID })
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Path < outputs[j].Path })
	evidence := append([]Evidence(nil), request.ArtifactContext.Evidence...)
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].EvidenceID < evidence[j].EvidenceID })
	secretRefs := append([]SecretRef{}, request.SecretRefs...)
	sort.Slice(secretRefs, func(i, j int) bool {
		return secretRefs[i].Provider+"\x00"+secretRefs[i].Key+"\x00"+secretRefs[i].Environment < secretRefs[j].Provider+"\x00"+secretRefs[j].Key+"\x00"+secretRefs[j].Environment
	})
	return assemblyManifestDocument{
		SchemaVersion: "1.0.0", AssemblyID: request.ArtifactContext.AssemblyID, RunID: request.ArtifactContext.RunID,
		Product: request.ArtifactContext.Product, Blueprint: request.ArtifactContext.Blueprint,
		CatalogChecksum: request.ArtifactContext.CatalogChecksum, Packages: packages, Templates: templates, SDKs: sdks,
		Outputs: outputs, Evidence: evidence, SecretRefs: secretRefs, CreatedAt: request.ArtifactContext.CreatedAt,
		ManifestChecksum: digestBytes(nil),
	}
}

func buildGeneratedLock(request Request, planDocument Plan, prepared PreparedChangeSet, finalSnapshotChecksum, manifestChecksum string) generatedLockDocument {
	packages := make([]LockedDependency, 0, len(planDocument.Packages))
	for _, item := range planDocument.Packages {
		packages = append(packages, LockedDependency{ID: item.PackageID, Version: item.Version, Checksum: item.Checksum})
	}
	templatesByID := make(map[string]LockedDependency)
	for _, application := range planDocument.Applications {
		item := application.Template
		templatesByID[item.TemplateID] = LockedDependency{ID: item.TemplateID, Version: item.Version, Checksum: item.Checksum}
	}
	templates := make([]LockedDependency, 0, len(templatesByID))
	for _, item := range templatesByID {
		templates = append(templates, item)
	}
	sdks := make([]LockedDependency, 0, len(planDocument.SDKs))
	for _, item := range planDocument.SDKs {
		sdks = append(sdks, LockedDependency{ID: item.SDKID, Version: item.Version, Checksum: item.Checksum})
	}
	outputs := make(map[string]OutputSpec, len(request.DesiredOutputs))
	for _, output := range request.DesiredOutputs {
		outputs[output.Path] = output
	}
	files := make([]LockedFile, 0, len(prepared.Changes)+len(prepared.Preserved))
	for _, change := range prepared.Changes {
		output := outputs[change.Path]
		files = append(files, LockedFile{
			Path: change.Path, Ownership: change.Ownership, SHA256: change.SHA256, GeneratedSHA256: change.GeneratedSHA256,
			SourceID: output.SourceID, SourceVersion: output.SourceVersion, SourcePath: output.SourcePath, SourceSHA256: output.SourceSHA256,
			RenderStrategy: output.RenderStrategy, ContentType: output.ContentType, Merge: output.Merge, UpdatePolicy: updatePolicy(change.Ownership),
		})
	}
	for _, file := range prepared.Preserved {
		files = append(files, LockedFile{Path: file.Path, Ownership: file.Ownership, SHA256: file.SHA256, UpdatePolicy: updatePolicy(file.Ownership)})
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].ID < packages[j].ID })
	sort.Slice(templates, func(i, j int) bool { return templates[i].ID < templates[j].ID })
	sort.Slice(sdks, func(i, j int) bool { return sdks[i].ID < sdks[j].ID })
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return generatedLockDocument{
		SchemaVersion: "1.0.0", LockID: request.ArtifactContext.LockID, AssemblyManifestChecksum: manifestChecksum,
		BlueprintChecksum: request.ArtifactContext.Blueprint.Checksum, CatalogChecksum: request.ArtifactContext.CatalogChecksum,
		TargetSnapshotChecksum: finalSnapshotChecksum, RollbackPointPath: request.ArtifactContext.Paths.RollbackPointPath,
		Generator: request.Generator, Packages: packages, Templates: templates, SDKs: sdks, Files: files,
		CreatedAt: request.ArtifactContext.CreatedAt, LockChecksum: digestBytes(nil),
	}
}

func buildSucceededResult(request Request, prepared PreparedChangeSet) generatorResultDocument {
	written := make([]writtenFile, 0, len(prepared.Changes))
	targetUnchanged := true
	for _, change := range prepared.Changes {
		written = append(written, writtenFile{Path: change.Path, Ownership: change.Ownership, SHA256: change.SHA256, Action: change.Action})
		if change.Action != "unchanged" {
			targetUnchanged = false
		}
	}
	preserved := make([]preservedFile, 0, len(prepared.Preserved))
	for _, file := range prepared.Preserved {
		preserved = append(preserved, preservedFile{Path: file.Path, Ownership: file.Ownership, SHA256: file.SHA256})
	}
	paths := request.ArtifactContext.Paths
	return generatorResultDocument{
		SchemaVersion: "1.0.0", RequestID: request.RequestID, Status: "succeeded", FilesWritten: written, PreservedFiles: preserved,
		DiagnosticIDs: []string{}, StagingCleanupCompleted: true, AtomicCommitCompleted: true, RollbackAttempted: false,
		RollbackCompleted: false, TargetUnchanged: targetUnchanged, RollbackPointPath: paths.RollbackPointPath,
		CommitJournalPath: paths.CommitJournalPath, AssemblyManifestPath: paths.AssemblyManifestPath,
		GeneratedLockPath: paths.GeneratedLockPath, ResultChecksum: digestBytes(nil),
	}
}

func marshalWithEmbeddedDigest(value any, field string) ([]byte, string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	digest, err := machinecontract.DigestWithoutTopLevelField(raw, field)
	if err != nil {
		return nil, "", err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, "", err
	}
	object[field] = digest
	raw, err = json.Marshal(object)
	if err != nil {
		return nil, "", err
	}
	canonical, err := machinecontract.Canonicalize(raw)
	if err != nil {
		return nil, "", fmt.Errorf("canonicalize %s: %w", field, err)
	}
	return canonical, digest, nil
}

func equalSortedIdentifiers(left, right []string) bool {
	left, right = append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] || (index > 0 && left[index] == left[index-1]) {
			return false
		}
	}
	return true
}
