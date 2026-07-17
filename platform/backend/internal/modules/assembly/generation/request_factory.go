package generation

import (
	"encoding/json"
	"path"
	"sort"
	"strings"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type RequestSpec struct {
	WorkspaceRef         string
	RunID                string
	LifecycleOperationID string
	RunCreatedAt         time.Time
	Product              ArtifactProduct
	Blueprint            ArtifactBlueprint
	BlueprintDocument    json.RawMessage
	PlanDocument         json.RawMessage
	PreviousLock         ProjectLock
	PreviousManifestPath string
	PreviousLockPath     string
}

func BuildRequest(targetRoot string, spec RequestSpec) (Input, PreviousArtifacts, error) {
	var planDocument Plan
	if json.Unmarshal(spec.PlanDocument, &planDocument) != nil || planDocument.PlanID == "" || planDocument.PlanChecksum == "" || len(spec.BlueprintDocument) == 0 {
		return Input{}, PreviousArtifacts{}, ErrInvalidInput
	}
	executionID := spec.RunID
	if spec.LifecycleOperationID != "" {
		executionID = spec.LifecycleOperationID
	}
	validSource := (stableIdentifierPattern.MatchString(spec.RunID) && spec.LifecycleOperationID == "") ||
		(stableIdentifierPattern.MatchString(spec.LifecycleOperationID) && spec.RunID == "")
	if !stableIdentifierPattern.MatchString(spec.WorkspaceRef) || !validSource || spec.RunCreatedAt.IsZero() ||
		!validDigest(spec.Blueprint.Checksum) || spec.Blueprint.BlueprintID != planDocument.BlueprintID || spec.Blueprint.Version != planDocument.BlueprintVersion {
		return Input{}, PreviousArtifacts{}, ErrPlanMismatch
	}
	snapshot, err := InspectTarget(targetRoot, spec.PreviousLock)
	if err != nil {
		return Input{}, PreviousArtifacts{}, err
	}
	seed := strings.TrimPrefix(digestBytes([]byte(executionID+"\x00"+planDocument.PlanChecksum+"\x00"+spec.WorkspaceRef)), "sha256:")[:24]
	requestID := "request." + seed
	assemblyID := "assembly." + seed
	artifactRoot := path.Join("artifacts", "assembly", assemblyID)
	reportPath := path.Join(artifactRoot, "reports", "generator-contract.json")
	reportRaw, err := json.Marshal(map[string]any{
		"schema_version": "1.0.0", "request_id": requestID, "execution_id": executionID,
		"plan_checksum": planDocument.PlanChecksum, "blueprint_checksum": spec.Blueprint.Checksum,
		"catalog_checksum": planDocument.CatalogSnapshot.Checksum, "target_snapshot_checksum": snapshot.Checksum,
		"generator_checksum": planDocument.Generator.Checksum, "status": "passed",
	})
	if err != nil {
		return Input{}, PreviousArtifacts{}, ErrInvalidInput
	}
	report, err := machinecontract.Canonicalize(reportRaw)
	if err != nil {
		return Input{}, PreviousArtifacts{}, ErrInvalidInput
	}
	protected := make([]string, 0)
	for _, file := range snapshot.Files {
		if file.Ownership == "custom" || file.Ownership == "forked" {
			protected = append(protected, file.Path)
		}
	}
	sort.Strings(protected)
	operation := "generate"
	previous := PreviousArtifacts{}
	inputs := InputPaths{
		BlueprintPath: path.Join("contracts", "blueprints", spec.Blueprint.BlueprintID+".json"),
		PlanPath:      path.Join("contracts", "plans", planDocument.PlanID+".json"),
	}
	if len(spec.PreviousLock.Files) != 0 || spec.PreviousManifestPath != "" || spec.PreviousLockPath != "" {
		if spec.PreviousManifestPath == "" || spec.PreviousLockPath == "" || !validDigest(spec.PreviousLock.AssemblyManifestChecksum) || !validDigest(spec.PreviousLock.LockChecksum) {
			return Input{}, PreviousArtifacts{}, ErrInvalidInput
		}
		operation = "upgrade"
		inputs.PreviousManifestPath = spec.PreviousManifestPath
		inputs.PreviousLockPath = spec.PreviousLockPath
		previous = PreviousArtifacts{ManifestSHA256: spec.PreviousLock.AssemblyManifestChecksum, LockSHA256: spec.PreviousLock.LockChecksum}
	}
	request := Request{
		SchemaVersion: "1.0.0", RequestID: requestID, Operation: operation, WorkspaceRef: spec.WorkspaceRef,
		PlanChecksum: planDocument.PlanChecksum, TargetSnapshotChecksum: snapshot.Checksum, Generator: planDocument.Generator,
		Inputs: inputs, DesiredOutputs: append([]OutputSpec{}, planDocument.ExpectedOutputs...), ExistingFiles: append([]ExistingFile{}, snapshot.Files...),
		ProtectedPaths: protected, SecretRefs: append([]SecretRef{}, planDocument.RequiredSecretRefs...),
		StagingPath: path.Join(".runtime", "generator", requestID), RollbackPointPath: path.Join(artifactRoot, "rollback-point.json"),
		ConflictPolicy: "stop", Determinism: Determinism{Timezone: "UTC", Locale: "C", SortOrder: "bytewise"},
		ArtifactContext: ArtifactContext{
			AssemblyID: assemblyID, LockID: "lock." + seed, RollbackID: "rollback." + seed, RunID: spec.RunID, LifecycleOperationID: spec.LifecycleOperationID,
			Product: spec.Product, Blueprint: spec.Blueprint, CatalogChecksum: planDocument.CatalogSnapshot.Checksum,
			Evidence:  []Evidence{{EvidenceID: "evidence.generator-contract", Type: "contract_report", Status: "passed", Path: reportPath, SHA256: digestBytes(report)}},
			CreatedAt: spec.RunCreatedAt.UTC().Format(time.RFC3339Nano),
			Paths: ArtifactPaths{
				ArtifactStagingPath: path.Join(".runtime", "assembly", requestID), AssemblyManifestPath: path.Join(artifactRoot, "assembly-manifest.json"),
				GeneratedLockPath: path.Join(artifactRoot, "generated-project-lock.json"), RollbackPointPath: path.Join(artifactRoot, "rollback-point.json"),
				CommitJournalPath: path.Join(artifactRoot, "commit-journal.json"), ResultPath: path.Join(artifactRoot, "generator-result.json"),
				DiagnosticDirectory: path.Join(artifactRoot, "diagnostics"),
			},
		},
	}
	return Input{
		Request: request, Blueprint: append(json.RawMessage(nil), spec.BlueprintDocument...), Plan: append(json.RawMessage(nil), spec.PlanDocument...),
		EvidenceDocuments: map[string][]byte{reportPath: report},
	}, previous, nil
}
