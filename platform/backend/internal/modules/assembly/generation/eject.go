package generation

import (
	"encoding/json"
	"sort"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type EjectPlan struct {
	SchemaVersion          string      `json:"schema_version"`
	PlanID                 string      `json:"plan_id"`
	PreviousLockChecksum   string      `json:"previous_lock_checksum"`
	TargetSnapshotChecksum string      `json:"target_snapshot_checksum"`
	Files                  []EjectFile `json:"files"`
	Executable             bool        `json:"executable"`
	PlanChecksum           string      `json:"plan_checksum"`
}

type EjectFile struct {
	Path                 string `json:"path"`
	PreviousOwnership    string `json:"previous_ownership"`
	NewOwnership         string `json:"new_ownership"`
	BaselineSHA256       string `json:"baseline_sha256"`
	CurrentSHA256        string `json:"current_sha256"`
	ModifiedFromBaseline bool   `json:"modified_from_baseline"`
}

func BuildEjectPlan(targetRoot string, lockDocument []byte, ejectPaths []string) ([]byte, error) {
	lock, err := DecodeProjectLock(lockDocument)
	if err != nil || len(ejectPaths) == 0 {
		return nil, ErrInvalidInput
	}
	locked, err := lockedFilesByPath(lock.Files)
	if err != nil {
		return nil, err
	}
	snapshot, err := InspectTarget(targetRoot, lock)
	if err != nil {
		return nil, err
	}
	current := existingFilesByPath(snapshot.Files)
	paths := append([]string(nil), ejectPaths...)
	sort.Strings(paths)
	files := make([]EjectFile, 0, len(paths))
	for index, filePath := range paths {
		if machinecontract.ValidateSafeRelativePath(filePath) != nil || (index > 0 && strings.EqualFold(paths[index-1], filePath)) {
			return nil, ErrInvalidInput
		}
		baseline, exists := locked[strings.ToLower(filePath)]
		actual, present := current[strings.ToLower(filePath)]
		if !exists || !present || baseline.Path != filePath || actual.Path != filePath || (baseline.Ownership != "generated" && baseline.Ownership != "integration") {
			return nil, ErrOwnershipConflict
		}
		files = append(files, EjectFile{
			Path: filePath, PreviousOwnership: baseline.Ownership, NewOwnership: "forked", BaselineSHA256: baseline.SHA256,
			CurrentSHA256: actual.SHA256, ModifiedFromBaseline: !digestEqual(baseline.SHA256, actual.SHA256),
		})
	}
	seedRaw, err := json.Marshal(struct {
		PreviousLockChecksum   string      `json:"previous_lock_checksum"`
		TargetSnapshotChecksum string      `json:"target_snapshot_checksum"`
		Files                  []EjectFile `json:"files"`
	}{lock.LockChecksum, snapshot.Checksum, files})
	if err != nil {
		return nil, err
	}
	seed, err := machinecontract.Digest(seedRaw)
	if err != nil {
		return nil, err
	}
	plan := EjectPlan{
		SchemaVersion: "1.0.0", PlanID: "eject." + seed[:24], PreviousLockChecksum: lock.LockChecksum,
		TargetSnapshotChecksum: snapshot.Checksum, Files: files, Executable: true, PlanChecksum: digestBytes(nil),
	}
	return marshalEjectPlan(plan)
}

func marshalEjectPlan(plan EjectPlan) ([]byte, error) {
	raw, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	digest, err := machinecontract.DigestWithoutTopLevelField(raw, "plan_checksum")
	if err != nil {
		return nil, err
	}
	plan.PlanChecksum = digest
	raw, err = json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	return machinecontract.Canonicalize(raw)
}
