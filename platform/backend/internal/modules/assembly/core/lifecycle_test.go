package core

import (
	"encoding/json"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

func TestProjectLifecyclePlanDocumentPreservesTrustedSource(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	body := map[string]any{"schema_version": "1.0.0", "lifecycle_plan_id": "lifecycle.plan-test", "assembly_id": "assembly.test", "product_id": "product.test", "operation": "eject", "source": map[string]any{"manifest_id": "assembly.test", "manifest_checksum": testDigestA, "lock_id": "lock.test", "lock_checksum": testDigestB, "catalog_checksum": testDigestA, "target_snapshot_checksum": testDigestB}, "eject_paths": []string{"generated/main.go"}, "target_snapshot_checksum": testDigestB, "changes": []any{}, "migrations": []any{map[string]any{"migration_id": "migration.provider-test", "kind": "provider", "reversibility": "compensatable", "summary": "Rotate the provider routing binding"}}, "regression_tests": []string{}, "conflicts": []any{}, "rollback": map[string]any{"strategy": "restore_predecessor", "automatic": true, "predecessor_manifest_checksum": testDigestA, "predecessor_lock_checksum": testDigestB}, "blocking_conflict_count": 0, "executable": true, "confirmation": map[string]any{"statements": []string{"Ejected files stop receiving automatic updates"}, "summary_checksum": testDigestA}, "created_at": now.Format(time.RFC3339Nano), "plan_checksum": testDigestA}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	checksum, err := machinecontract.DigestWithoutTopLevelField(raw, "plan_checksum")
	if err != nil {
		t.Fatal(err)
	}
	body["plan_checksum"] = checksum
	raw, err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = machinecontract.Canonicalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ProjectLifecyclePlanDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Source.ManifestID != "assembly.test" || plan.Source.LockID != "lock.test" || plan.Source.CatalogChecksum != testDigestA || plan.Source.TargetSnapshotChecksum != testDigestB {
		t.Fatalf("trusted source projection lost fields: %+v", plan.Source)
	}
	if len(plan.Migrations) != 1 || plan.Migrations[0].Kind != "provider" || plan.Migrations[0].Reversibility != "compensatable" || plan.Rollback.Strategy != "restore_predecessor" || !plan.Rollback.Automatic || plan.Rollback.PredecessorLockChecksum != testDigestB {
		t.Fatalf("browser-safe migration/rollback projection lost fields: migrations=%+v rollback=%+v", plan.Migrations, plan.Rollback)
	}
}

func TestEvolveLifecycleOperationEnforcesMonotonicState(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	current := LifecycleOperation{OperationID: "operation.test", RootOperationID: "operation.test", LifecyclePlanID: "lifecycle.test", AssemblyID: "assembly.test", ProductID: "product.test", Kind: LifecycleUpgrade, Version: 1, Status: LifecyclePlanned, Source: LifecycleArtifactState{ManifestID: "assembly.test", ManifestChecksum: testDigestA, LockID: "lock.test", LockChecksum: testDigestB, CatalogChecksum: testDigestA, TargetSnapshotChecksum: testDigestB}, Recovery: LifecycleRecovery{CancelAllowed: true}, IdempotencyKeyDigest: testDigestA, CreatedBy: "admin.test", CreatedAt: now, UpdatedAt: now}
	if err := rebuildLifecycleOperationDocument(&current); err != nil {
		t.Fatal(err)
	}
	executing, err := EvolveLifecycleOperation(current, LifecycleExecuting, "step.prepare", nil, LifecycleRecovery{}, nil, nil, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if executing.Version != 2 || executing.Status != LifecycleExecuting || executing.OperationChecksum == current.OperationChecksum {
		t.Fatalf("unexpected evolution: %+v", executing)
	}
	if _, err = EvolveLifecycleOperation(executing, LifecycleCancelled, "", nil, LifecycleRecovery{}, nil, nil, now.Add(2*time.Second)); err == nil {
		t.Fatal("executing operation must not be cancellable")
	}
}

func TestLifecycleTargetVersionsAcceptOnlyIdentifierAndSemver(t *testing.T) {
	valid := LifecycleTargetVersions{Templates: []LifecycleVersionRef{{ID: "template.standard-a", Version: "1.2.3"}}, Generator: LifecycleVersionRef{ID: "generator.platform", Version: "2.0.0-beta.1"}}
	if !validTargetVersions(valid) {
		t.Fatal("trusted catalog coordinates were rejected")
	}
	invalidVersion := valid
	invalidVersion.Generator.Version = "latest"
	if validTargetVersions(invalidVersion) {
		t.Fatal("non-semver target version accepted")
	}
	invalidID := valid
	invalidID.Generator.ID = "../generator"
	if validTargetVersions(invalidID) {
		t.Fatal("unsafe target identifier accepted")
	}
}

func TestProjectLifecycleSourceReturnsOnlyVerifiedHeadState(t *testing.T) {
	manifestDocument := lifecycleDocumentWithDigest(t, map[string]any{"schema_version": "1.0.0", "assembly_id": "assembly.current"}, "manifest_checksum")
	manifestChecksum, err := machinecontract.DigestWithoutTopLevelField(manifestDocument, "manifest_checksum")
	if err != nil {
		t.Fatal(err)
	}
	lockDocument := lifecycleDocumentWithDigest(t, map[string]any{
		"schema_version": "1.0.0", "lock_id": "lock.current", "assembly_manifest_checksum": manifestChecksum,
		"catalog_checksum": testDigestA, "target_snapshot_checksum": testDigestB,
	}, "lock_checksum")
	lockChecksum, err := machinecontract.DigestWithoutTopLevelField(lockDocument, "lock_checksum")
	if err != nil {
		t.Fatal(err)
	}
	manifestDocumentChecksum, _ := machinecontract.Digest(manifestDocument)
	lockDocumentChecksum, _ := machinecontract.Digest(lockDocument)
	state, err := projectLifecycleSource(
		Manifest{AssemblyID: "assembly.current", ProductID: "product.test", Document: manifestDocument, DocumentSHA256: "sha256:" + manifestDocumentChecksum, ManifestSHA256: manifestChecksum},
		GeneratedProjectLock{LockID: "lock.current", AssemblyID: "assembly.current", ProductID: "product.test", Document: lockDocument, DocumentSHA256: "sha256:" + lockDocumentChecksum, LockSHA256: lockChecksum},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.ManifestID != "assembly.current" || state.LockID != "lock.current" || state.CatalogChecksum != testDigestA || state.TargetSnapshotChecksum != testDigestB {
		t.Fatalf("unexpected lifecycle source: %+v", state)
	}

	tampered := append(json.RawMessage(nil), lockDocument...)
	tampered[len(tampered)-2] ^= 1
	if _, err = projectLifecycleSource(
		Manifest{AssemblyID: "assembly.current", ProductID: "product.test", Document: manifestDocument, DocumentSHA256: "sha256:" + manifestDocumentChecksum, ManifestSHA256: manifestChecksum},
		GeneratedProjectLock{LockID: "lock.current", AssemblyID: "assembly.current", ProductID: "product.test", Document: tampered, DocumentSHA256: "sha256:" + lockDocumentChecksum, LockSHA256: lockChecksum},
	); err == nil {
		t.Fatal("tampered current lock was accepted")
	}
}

func lifecycleDocumentWithDigest(t *testing.T, body map[string]any, field string) json.RawMessage {
	t.Helper()
	body[field] = testDigestA
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := machinecontract.DigestWithoutTopLevelField(raw, field)
	if err != nil {
		t.Fatal(err)
	}
	body[field] = digest
	raw, err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
