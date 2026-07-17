import { beforeEach, describe, expect, it, vi } from "vitest";

const authenticatedRequest = vi.hoisted(() => vi.fn());
vi.mock("../api/authClient", () => ({ authenticatedAdminRequest: authenticatedRequest }));
import { assemblyClient } from "../api/assemblyClient";

const sha = (character: string) => `sha256:${character.repeat(64)}`;
const artifact = { manifest_id: "assembly-1", manifest_checksum: sha("a"), lock_id: "lock-1", lock_checksum: sha("b"), catalog_checksum: sha("c"), target_snapshot_checksum: sha("d") };
const plan = {
  lifecycle_plan_id: "lifecycle-plan-1", assembly_id: "assembly-1", product_id: "product-1", operation: "upgrade", version: 2,
  source: artifact, target_snapshot_checksum: sha("e"), changes: [{ path: "apps/admin/account.tsx", action: "update", ownership: "generated", before_checksum: sha("f"), after_checksum: sha("1"), source_id: "package.account", source_version: "1.1.0" }],
  migrations: [{ migration_id: "migration-provider-1", kind: "provider", reversibility: "compensatable", summary: "Rotate provider binding" }],
  conflicts: [], regression_tests: ["st-032"], rollback: { strategy: "restore_predecessor", automatic: false, predecessor_manifest_checksum: sha("a"), predecessor_lock_checksum: sha("b") }, blocking_conflict_count: 0, executable: true, confirmation_checksum: sha("2"), statements: ["Confirm lifecycle upgrade"], plan_checksum: sha("3"), created_at: "2026-07-16T01:00:00Z", audit_id: "audit-1",
};
const operation = {
  operation_id: "operation-1", root_operation_id: "operation-1", rollback_of_operation_id: null, lifecycle_plan_id: "lifecycle-plan-1", assembly_id: "assembly-1", product_id: "product-1", kind: "upgrade", version: 1, status: "planned", current_step: null,
  source: artifact, target: null, recovery: { retryable: false, rollback_available: false, cancel_allowed: true }, diagnostics: [], reports: [], created_at: "2026-07-16T01:00:00Z", updated_at: "2026-07-16T01:00:00Z", completed_at: null, audit_id: "audit-2",
};
const options = { idempotencyKey: "assembly-lifecycle-key-123" };

beforeEach(() => authenticatedRequest.mockReset());

describe("assembly lifecycle client", () => {
  it("reads the exact current lifecycle source from the stable assembly root", async () => {
    authenticatedRequest.mockResolvedValue(artifact);
    await expect(assemblyClient.getLifecycleSource("assembly:1")).resolves.toEqual(artifact);
    expect(authenticatedRequest).toHaveBeenCalledWith("/api/v1/admin/assemblies/assembly%3A1/lifecycle-source", expect.any(Object));
    authenticatedRequest.mockResolvedValue({ ...artifact, workspace_path: "D:/private" });
    await expect(assemblyClient.getLifecycleSource("assembly:1")).rejects.toThrow("unknown or missing fields");
  });

  it("uses fixed plan paths and sends only checksums and target version references", async () => {
    authenticatedRequest.mockResolvedValue({ ...plan, assembly_id: "assembly:1" });
    await assemblyClient.createUpgradePlan("assembly:1", { expected_manifest_checksum: sha("a"), expected_lock_checksum: sha("b"), target: { packages: [{ id: "package.account", version: "1.1.0" }], templates: [{ id: "template.web", version: "1.1.0" }], generator: { id: "generator.web", version: "1.1.0" }, sdks: [{ id: "sdk.typescript", version: "1.1.0" }] } }, options);
    expect(authenticatedRequest).toHaveBeenCalledWith("/api/v1/admin/assemblies/assembly%3A1/upgrade-plans", expect.objectContaining({ method: "POST", headers: { "Idempotency-Key": options.idempotencyKey } }));
    expect(JSON.parse(authenticatedRequest.mock.calls[0][1].body)).toEqual({ expected_manifest_checksum: sha("a"), expected_lock_checksum: sha("b"), target: { packages: [{ id: "package.account", version: "1.1.0" }], templates: [{ id: "template.web", version: "1.1.0" }], generator: { id: "generator.web", version: "1.1.0" }, sdks: [{ id: "sdk.typescript", version: "1.1.0" }] } });
  });

  it("rejects unsafe eject paths before making a request", async () => {
    await expect(assemblyClient.createEjectPlan("assembly-1", { expected_manifest_checksum: sha("a"), expected_lock_checksum: sha("b"), paths: ["D:\\private\\source.ts"] }, options)).rejects.toThrow("lifecycle path");
    expect(authenticatedRequest).not.toHaveBeenCalled();
  });

  it("rejects unknown response fields instead of exposing raw lifecycle data", async () => {
    authenticatedRequest.mockResolvedValue({ ...plan, document_path: "D:/private/plan.json" });
    await expect(assemblyClient.getLifecyclePlan("lifecycle-plan-1")).rejects.toThrow("unknown or missing fields");
  });

  it("fails closed when lifecycle execution projections disagree", async () => {
    authenticatedRequest.mockResolvedValue({ ...plan, conflicts: [{ conflict_id: "conflict-1", code: "assembly.target_conflict", category: "target", blocking: true, message: "Resolve target conflict", paths: [], remediation: ["Select a compatible target"] }], blocking_conflict_count: 0, executable: true });
    await expect(assemblyClient.getLifecyclePlan("lifecycle-plan-1")).rejects.toThrow("execution projection is inconsistent");
    authenticatedRequest.mockResolvedValue({ ...plan, blocking_conflict_count: 0, executable: false });
    await expect(assemblyClient.getLifecyclePlan("lifecycle-plan-1")).rejects.toThrow("execution projection is inconsistent");
  });

  it("rejects path-like display values even when embedded in lifecycle projections", async () => {
    authenticatedRequest.mockResolvedValue({ ...plan, statements: ["Review failed at D:/private/workspace"] });
    await expect(assemblyClient.getLifecyclePlan("lifecycle-plan-1")).rejects.toThrow("confirmation statement");
    authenticatedRequest.mockResolvedValue({ ...operation, reports: [{ report_id: "report-1", type: "regression", status: "failed", summary: "Output is in /var/private/result", checksum: null, created_at: "2026-07-16T01:00:00Z" }] });
    await expect(assemblyClient.getLifecycleOperation("operation-1")).rejects.toThrow("lifecycle report summary");
  });

  it("rejects control characters, query syntax, whitespace, and embedded host paths in projected repository paths", async () => {
    for (const unsafePath of ["apps/report\nD:/private/result", "apps/account.tsx?debug=true", "apps/private source.ts", "apps/%2e%2e/secret.ts"]) {
      authenticatedRequest.mockResolvedValue({ ...plan, changes: [{ ...plan.changes[0], path: unsafePath }] });
      await expect(assemblyClient.getLifecyclePlan("lifecycle-plan-1")).rejects.toThrow("lifecycle change path");
    }
    authenticatedRequest.mockResolvedValue({ ...plan, conflicts: [{ conflict_id: "conflict-1", code: "assembly.conflict", category: "target", blocking: true, message: "Resolve target conflict", paths: ["apps/readme /var/private/result"], remediation: ["Select a compatible target"] }] });
    await expect(assemblyClient.getLifecyclePlan("lifecycle-plan-1")).rejects.toThrow("conflict path");
  });

  it("fails closed when read responses do not match the requested plan or operation", async () => {
    const upgradeInput = { expected_manifest_checksum: sha("a"), expected_lock_checksum: sha("b"), target: { packages: [], templates: [{ id: "template.web", version: "1.1.0" }], generator: { id: "generator.web", version: "1.1.0" }, sdks: [] } };
    authenticatedRequest.mockResolvedValue({ ...plan, assembly_id: "assembly-other" });
    await expect(assemblyClient.createUpgradePlan("assembly-1", upgradeInput, options)).rejects.toThrow("does not match the request");
    authenticatedRequest.mockResolvedValue({ ...plan, operation: "upgrade" });
    await expect(assemblyClient.createEjectPlan("assembly-1", { expected_manifest_checksum: sha("a"), expected_lock_checksum: sha("b"), paths: ["apps/account.ts"] }, options)).rejects.toThrow("does not match the request");
    authenticatedRequest.mockResolvedValue({ ...plan, lifecycle_plan_id: "lifecycle-plan-other" });
    await expect(assemblyClient.getLifecyclePlan("lifecycle-plan-1")).rejects.toThrow("does not match the request");
    authenticatedRequest.mockResolvedValue({ ...operation, operation_id: "operation-other", root_operation_id: "operation-other" });
    await expect(assemblyClient.getLifecycleOperation("operation-1")).rejects.toThrow("does not match the request");
  });

  it("fails closed on execute, cancel, and rollback response lineage mismatches", async () => {
    authenticatedRequest.mockResolvedValue({ ...operation, lifecycle_plan_id: "lifecycle-plan-other" });
    await expect(assemblyClient.executeLifecyclePlan("lifecycle-plan-1", 2, sha("3"), sha("2"), options)).rejects.toThrow("does not match the executed plan");
    authenticatedRequest.mockResolvedValue({ ...operation, operation_id: "operation-other", root_operation_id: "operation-other" });
    await expect(assemblyClient.cancelLifecycleOperation("operation-1", 1, "Operator cancelled", options)).rejects.toThrow("does not match the request");
    authenticatedRequest.mockResolvedValue({ ...operation, operation_id: "operation-rollback-2", root_operation_id: "operation-1", kind: "rollback", lifecycle_plan_id: null, rollback_of_operation_id: "operation-other" });
    await expect(assemblyClient.rollbackLifecycleOperation("operation-1", 1, "Regression failed", options)).rejects.toThrow("rollback does not match the request");
  });

  it("strictly parses manifest and lock top-level projections", async () => {
    const manifest = { assembly_id: "assembly-1", product_id: "product-1", run_id: "run-1", schema_version: "1.0.0", document: {}, document_checksum: sha("a"), checksum: sha("b"), created_at: "2026-07-16T01:00:00Z" };
    const lock = { lock_id: "lock-1", product_id: "product-1", run_id: "run-1", assembly_id: "assembly-1", schema_version: "1.0.0", document: {}, document_checksum: sha("c"), checksum: sha("d"), created_at: "2026-07-16T01:00:00Z" };
    authenticatedRequest.mockResolvedValueOnce(manifest).mockResolvedValueOnce(lock);
    await expect(assemblyClient.getManifest("assembly-1")).resolves.toEqual(manifest);
    await expect(assemblyClient.getGeneratedProjectLock("lock-1")).resolves.toEqual(lock);

    authenticatedRequest.mockResolvedValue({ ...manifest, workspace_path: "D:/private" });
    await expect(assemblyClient.getManifest("assembly-1")).rejects.toThrow("unknown or missing fields");
    authenticatedRequest.mockResolvedValue({ ...lock, lock_id: "lock-other" });
    await expect(assemblyClient.getGeneratedProjectLock("lock-1")).rejects.toThrow("does not match the request");

    const { run_id: _manifestRun, ...manifestBase } = manifest;
    const { run_id: _lockRun, ...lockBase } = lock;
    authenticatedRequest.mockResolvedValueOnce({ ...manifestBase, lifecycle_operation_id: "operation-2" }).mockResolvedValueOnce({ ...lockBase, lifecycle_operation_id: "operation-2" });
    await expect(assemblyClient.getManifest("assembly-1")).resolves.toMatchObject({ lifecycle_operation_id: "operation-2" });
    await expect(assemblyClient.getGeneratedProjectLock("lock-1")).resolves.toMatchObject({ lifecycle_operation_id: "operation-2" });
    authenticatedRequest.mockResolvedValue({ ...manifest, lifecycle_operation_id: "operation-2" });
    await expect(assemblyClient.getManifest("assembly-1")).rejects.toThrow("exactly one source");
  });

  it("uses exact execute, cancel, and rollback endpoints with expected versions", async () => {
    const rollbackOperation = { ...operation, operation_id: "operation-rollback-2", root_operation_id: "operation-1", rollback_of_operation_id: "operation-1", lifecycle_plan_id: null, kind: "rollback" };
    authenticatedRequest.mockResolvedValueOnce(operation).mockResolvedValueOnce(operation).mockResolvedValueOnce(rollbackOperation);
    await assemblyClient.executeLifecyclePlan("lifecycle-plan-1", 2, sha("3"), sha("2"), options);
    await assemblyClient.cancelLifecycleOperation("operation-1", 1, "Operator cancelled", options);
    await assemblyClient.rollbackLifecycleOperation("operation-1", 1, "Regression failed", options);
    expect(authenticatedRequest.mock.calls.map(([path]) => path)).toEqual([
      "/api/v1/admin/assembly-lifecycle-plans/lifecycle-plan-1/execute",
      "/api/v1/admin/assembly-lifecycle-operations/operation-1/cancel",
      "/api/v1/admin/assembly-lifecycle-operations/operation-1/rollback",
    ]);
    expect(JSON.parse(authenticatedRequest.mock.calls[2][1].body)).toEqual({ expected_version: 1, reason: "Regression failed" });
  });
});
