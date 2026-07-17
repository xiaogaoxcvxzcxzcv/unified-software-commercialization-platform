import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { assemblyClient, type AssemblyLifecycleOperation, type AssemblyLifecyclePlan, type AssemblyRunRecord } from "../api/assemblyClient";
import { AuthApiError } from "../api/authClient";
import { AssemblyLifecycleOperationPage } from "../pages/AssemblyLifecycleOperationPage";
import { AssemblyLifecyclePlanPage } from "../pages/AssemblyLifecyclePlanPage";
import { AssemblyLifecycleEntryPage } from "../pages/AssemblyLifecycleEntryPage";

const permissionState = vi.hoisted(() => ({ permissions: ["assembly.read", "assembly.lifecycle.plan", "assembly.lifecycle.execute"] }));
vi.mock("../app/AuthContext", () => ({ useAuth: () => ({ session: { authorization: { permissions: permissionState.permissions } } }) }));
vi.mock("../components/Shell", () => ({ Shell: ({ title, children }: { title: string; children: React.ReactNode }) => <main><h1>{title}</h1>{children}</main> }));
const sha = (character: string) => `sha256:${character.repeat(64)}`;
const artifact = { manifest_id: "assembly-1", manifest_checksum: sha("a"), lock_id: "lock-1", lock_checksum: sha("b"), catalog_checksum: sha("c"), target_snapshot_checksum: sha("d") };
const plan: AssemblyLifecyclePlan = { lifecycle_plan_id: "lifecycle-plan-1", assembly_id: "assembly-1", product_id: "product-1", operation: "upgrade", version: 2, source: artifact, target_snapshot_checksum: sha("e"), changes: [{ path: "apps/admin/account.tsx", action: "update", ownership: "generated", before_checksum: sha("f"), after_checksum: sha("1"), source_id: "package.account", source_version: "1.1.0" }], migrations: [{ migration_id: "migration-provider-1", kind: "provider", reversibility: "compensatable", summary: "Rotate provider binding" }], conflicts: [], regression_tests: ["st-032"], rollback: { strategy: "restore_predecessor", automatic: false, predecessor_manifest_checksum: sha("a"), predecessor_lock_checksum: sha("b") }, blocking_conflict_count: 0, executable: true, confirmation_checksum: sha("2"), statements: ["确认升级并运行回归检查"], plan_checksum: sha("3"), created_at: "2026-07-16T01:00:00Z", audit_id: "audit-1" };
const operation: AssemblyLifecycleOperation = { operation_id: "operation-1", root_operation_id: "operation-1", rollback_of_operation_id: null, lifecycle_plan_id: "lifecycle-plan-1", assembly_id: "assembly-1", product_id: "product-1", kind: "upgrade", version: 1, status: "failed", current_step: "regression", source: artifact, target: null, recovery: { retryable: true, rollback_available: true, cancel_allowed: false }, diagnostics: [], reports: [], created_at: "2026-07-16T01:00:00Z", updated_at: "2026-07-16T01:01:00Z", completed_at: "2026-07-16T01:01:00Z", audit_id: "audit-2" };
const run: AssemblyRunRecord = { run_id: "run-1", product_id: null, plan_id: "plan-1", plan_version: 1, version: 1, plan_checksum: sha("4"), root_run_id: "run-1", retry_of_run_id: null, attempt_number: 1, output_target_ref: "workspace-primary", status: "planned", current_step_id: null, steps: [], recovery: { retryable: false, rollback_required: false, resume_from_step_id: null }, diagnostics: [], reports: [], document: {}, created_at: "2026-07-16T01:00:00Z", updated_at: "2026-07-16T01:00:00Z", completed_at: null, audit_id: "audit-run-1" };
function LocationProbe() { return <output aria-label="current route">{useLocation().pathname}</output>; }
function EntryHarness() { const navigate = useNavigate(); return <><button type="button" onClick={() => navigate("/assemblies/run-2/lifecycle")}>切换运行</button><AssemblyLifecycleEntryPage /></>; }
const renderPlan = () => render(<MemoryRouter initialEntries={["/assembly-lifecycle/plans/lifecycle-plan-1"]}><Routes><Route path="/assembly-lifecycle/plans/:planId" element={<AssemblyLifecyclePlanPage />} /><Route path="/assembly-lifecycle/operations/:operationId" element={<div>操作页</div>} /></Routes></MemoryRouter>);
const renderOperation = () => render(<MemoryRouter initialEntries={["/assembly-lifecycle/operations/operation-1"]}><Routes><Route path="/assembly-lifecycle/operations/:operationId" element={<><AssemblyLifecycleOperationPage /><LocationProbe /></>} /></Routes></MemoryRouter>);
const renderEntry = () => render(<MemoryRouter initialEntries={["/assemblies/run-1/lifecycle"]}><Routes><Route path="/assemblies/:runId/lifecycle" element={<EntryHarness />} /></Routes></MemoryRouter>);

beforeEach(() => { vi.restoreAllMocks(); sessionStorage.clear(); permissionState.permissions = ["assembly.read", "assembly.lifecycle.plan", "assembly.lifecycle.execute"]; });

describe("assembly lifecycle pages", () => {
  it("renders safe changes and executes only after high-risk confirmation", async () => {
    vi.spyOn(assemblyClient, "getLifecyclePlan").mockResolvedValue(plan);
    const execute = vi.spyOn(assemblyClient, "executeLifecyclePlan").mockResolvedValue({ ...operation, status: "planned", recovery: { retryable: false, rollback_available: false, cancel_allowed: true } });
    renderPlan();
    expect(await screen.findByText("apps/admin/account.tsx")).toBeInTheDocument();
    expect(screen.getByText("确认升级并运行回归检查")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "执行计划" }));
    expect(screen.getByRole("button", { name: "确认执行" })).toBeDisabled();
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "返回审查" }));
    fireEvent.click(screen.getByRole("button", { name: "执行计划" }));
    expect(screen.getByRole("checkbox")).not.toBeChecked();
    fireEvent.click(screen.getByRole("button", { name: "确认执行" }));
    expect(execute).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("checkbox"));
    fireEvent.click(screen.getByRole("button", { name: "确认执行" }));
    await waitFor(() => expect(execute).toHaveBeenCalledWith("lifecycle-plan-1", 2, sha("3"), sha("2"), expect.objectContaining({ idempotencyKey: expect.any(String) })));
  });

  it("disables execute when the operator lacks lifecycle execute permission", async () => {
    permissionState.permissions = ["assembly.read"];
    vi.spyOn(assemblyClient, "getLifecyclePlan").mockResolvedValue(plan);
    renderPlan();
    expect(await screen.findByRole("button", { name: "执行计划" })).toBeDisabled();
  });

  it("shows failed recovery with an accurate lifecycle status", async () => {
    vi.spyOn(assemblyClient, "getLifecycleOperation").mockResolvedValue(operation);
    renderOperation();
    expect(await screen.findByText(/当前契约未提供浏览器端/)).toBeInTheDocument();
    expect(screen.getByText("执行失败")).toBeInTheDocument();
  });

  it("reads lifecycle artifacts through the API client and shows only verified summaries", async () => {
    const completed = {
      ...operation,
      status: "completed" as const,
      target: artifact,
      manifest_url: "/api/v1/admin/assembly-manifests/assembly-1",
      lock_url: "/api/v1/admin/generated-project-locks/lock-1",
    };
    vi.spyOn(assemblyClient, "getLifecycleOperation").mockResolvedValue(completed);
    const getManifest = vi.spyOn(assemblyClient, "getManifest").mockResolvedValue({
      assembly_id: "assembly-1", product_id: "product-1", lifecycle_operation_id: "operation-1", schema_version: "1.0.0",
      document: { private_value: "must-not-render" }, document_checksum: sha("8"), checksum: sha("a"), created_at: "2026-07-16T01:01:00Z",
    });
    const getLock = vi.spyOn(assemblyClient, "getGeneratedProjectLock").mockResolvedValue({
      lock_id: "lock-1", product_id: "product-1", lifecycle_operation_id: "operation-1", assembly_id: "assembly-1", schema_version: "1.0.0",
      document: { private_value: "must-not-render" }, document_checksum: sha("9"), checksum: sha("b"), created_at: "2026-07-16T01:01:00Z",
    });
    renderOperation();
    fireEvent.click(await screen.findByRole("button", { name: "验证 Manifest" }));
    fireEvent.click(screen.getByRole("button", { name: "验证 Generated Lock" }));
    await waitFor(() => expect(getManifest).toHaveBeenCalledWith("assembly-1", { timeoutMs: 20_000 }));
    await waitFor(() => expect(getLock).toHaveBeenCalledWith("lock-1", { timeoutMs: 20_000 }));
    expect(await screen.findByText("Manifest assembly-1 已验证")).toBeInTheDocument();
    expect(await screen.findByText("Generated Lock lock-1 已验证")).toBeInTheDocument();
    expect(screen.queryByText("must-not-render")).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /Manifest|Generated Lock/ })).not.toBeInTheDocument();
  });

  it("fails closed when a lifecycle artifact does not match operation lineage", async () => {
    vi.spyOn(assemblyClient, "getLifecycleOperation").mockResolvedValue({
      ...operation,
      target: artifact,
      manifest_url: "/api/v1/admin/assembly-manifests/assembly-1",
    });
    const getManifest = vi.spyOn(assemblyClient, "getManifest").mockResolvedValue({
      assembly_id: "assembly-1", product_id: "product-other", lifecycle_operation_id: "operation-other", schema_version: "1.0.0",
      document: {}, document_checksum: sha("8"), checksum: sha("a"), created_at: "2026-07-16T01:01:00Z",
    });
    renderOperation();
    fireEvent.click(await screen.findByRole("button", { name: "验证 Manifest" }));
    await waitFor(() => expect(getManifest).toHaveBeenCalled());
    expect(await screen.findByText("Manifest 验证失败")).toBeInTheDocument();
    expect(screen.queryByText(/已验证/)).not.toBeInTheDocument();
  });

  it("rejects an untrusted lifecycle artifact URL before calling the API client", async () => {
    vi.spyOn(assemblyClient, "getLifecycleOperation").mockResolvedValue({
      ...operation,
      target: artifact,
      manifest_url: "/api/v1/admin/assembly-manifests/assembly-other",
    });
    const getManifest = vi.spyOn(assemblyClient, "getManifest");
    renderOperation();
    fireEvent.click(await screen.findByRole("button", { name: "验证 Manifest" }));
    expect(await screen.findByText("Manifest 验证失败")).toBeInTheDocument();
    expect(getManifest).not.toHaveBeenCalled();
  });

  it("replaces the route with the new rollback operation id and stops reading the predecessor", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const executing = { ...operation, status: "executing" as const, completed_at: null, recovery: { retryable: false, rollback_available: true, cancel_allowed: false } };
    const rollbackOperation = { ...operation, operation_id: "operation-rollback-2", rollback_of_operation_id: "operation-1", kind: "rollback" as const, status: "rolled_back" as const, recovery: { retryable: false, rollback_available: false, cancel_allowed: false } };
    const get = vi.spyOn(assemblyClient, "getLifecycleOperation").mockImplementation(async (id) => id === rollbackOperation.operation_id ? rollbackOperation : executing);
    const rollback = vi.spyOn(assemblyClient, "rollbackLifecycleOperation").mockResolvedValue(rollbackOperation);
    try {
      renderOperation();
      await waitFor(() => expect(screen.getByText("执行中")).toBeInTheDocument());
      fireEvent.click(screen.getByRole("button", { name: "回滚" }));
      fireEvent.change(screen.getByLabelText("操作原因"), { target: { value: "Regression failed" } });
      fireEvent.click(screen.getByRole("checkbox"));
      fireEvent.click(screen.getByRole("button", { name: "确认回滚" }));
      await waitFor(() => expect(rollback).toHaveBeenCalledWith("operation-1", 1, "Regression failed", expect.objectContaining({ idempotencyKey: expect.any(String) })));
      await waitFor(() => expect(screen.getByLabelText("current route")).toHaveTextContent("/assembly-lifecycle/operations/operation-rollback-2"));
      await waitFor(() => expect(get).toHaveBeenLastCalledWith("operation-rollback-2", expect.any(Object)));
      const predecessorReads = get.mock.calls.filter(([id]) => id === "operation-1").length;
      await vi.advanceTimersByTimeAsync(2_500);
      expect(get.mock.calls.filter(([id]) => id === "operation-1")).toHaveLength(predecessorReads);
    } finally {
      vi.useRealTimers();
    }
  });

  it("rechecks rollback confirmation inside the submit handler", async () => {
    vi.spyOn(assemblyClient, "getLifecycleOperation").mockResolvedValue(operation);
    const rollback = vi.spyOn(assemblyClient, "rollbackLifecycleOperation");
    renderOperation();
    await screen.findByText("执行失败");
    fireEvent.click(screen.getByRole("button", { name: "回滚" }));
    fireEvent.change(screen.getByLabelText("操作原因"), { target: { value: "Regression failed" } });
    const submit = screen.getByRole("button", { name: "确认回滚" });
    expect(submit).toBeDisabled();
    submit.removeAttribute("disabled");
    fireEvent.click(submit);
    expect(rollback).not.toHaveBeenCalled();
  });

  it("fails closed when read permission is missing", async () => {
    permissionState.permissions = ["assembly.lifecycle.execute"];
    const get = vi.spyOn(assemblyClient, "getLifecycleOperation");
    renderOperation();
    expect(await screen.findByRole("alert")).toHaveTextContent("缺少 assembly.read 权限");
    expect(get).not.toHaveBeenCalled();
  });

  it("renders a localized not-found lifecycle error", async () => {
    vi.spyOn(assemblyClient, "getLifecycleOperation").mockRejectedValue(new AuthApiError("Not Found", { status: 404, code: "assembly.not_found", retryable: false }));
    renderOperation();
    expect(await screen.findByRole("alert")).toHaveTextContent("未找到该生命周期资源");
  });

  it("ignores a late Entry response and aborts the superseded request", async () => {
    let resolveOld!: (value: AssemblyRunRecord) => void; let resolveNew!: (value: AssemblyRunRecord) => void;
    const oldResponse = new Promise<AssemblyRunRecord>((resolve) => { resolveOld = resolve; });
    const newResponse = new Promise<AssemblyRunRecord>((resolve) => { resolveNew = resolve; });
    const get = vi.spyOn(assemblyClient, "getRun").mockReturnValueOnce(oldResponse).mockReturnValueOnce(newResponse);
    renderEntry();
    fireEvent.click(screen.getByRole("button", { name: "切换运行" }));
    resolveNew({ ...run, run_id: "run-2", root_run_id: "run-2", status: "cancelled", version: 3, updated_at: "2026-07-16T01:03:00Z" });
    expect(await screen.findByText("已取消")).toBeInTheDocument();
    resolveOld({ ...run, version: 2, updated_at: "2026-07-16T01:02:00Z" });
    await waitFor(() => expect(screen.queryByText("等待执行")).not.toBeInTheDocument());
    expect(get.mock.calls[0][1]?.signal?.aborted).toBe(true);
  });

  it("loads current lifecycle head checksums instead of rereading initial run artifacts", async () => {
    const completed = { ...run, status: "completed" as const, product_id: "product-1", completed_at: "2026-07-16T01:02:00Z", manifest_url: "/api/v1/admin/assembly-manifests/assembly-1", lock_url: "/api/v1/admin/generated-project-locks/lock-initial" };
    vi.spyOn(assemblyClient, "getRun").mockResolvedValue(completed);
    const source = vi.spyOn(assemblyClient, "getLifecycleSource").mockResolvedValue({ ...artifact, manifest_id: "assembly-successor-2", manifest_checksum: sha("8"), lock_id: "lock-successor-2", lock_checksum: sha("9") });
    const create = vi.spyOn(assemblyClient, "createUpgradePlan").mockResolvedValue(plan);
    const manifest = vi.spyOn(assemblyClient, "getManifest");
    const lock = vi.spyOn(assemblyClient, "getGeneratedProjectLock");
    renderEntry();
    await waitFor(() => expect(source).toHaveBeenCalledWith("assembly-1", expect.objectContaining({ signal: expect.any(AbortSignal) })));
    expect(manifest).not.toHaveBeenCalled();
    expect(lock).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "生成计划" })).toBeEnabled();
    fireEvent.change(screen.getByLabelText("模板版本（必填）"), { target: { value: "admin.web@1.1.0" } });
    fireEvent.change(screen.getByLabelText("生成器版本（必填）"), { target: { value: "generator.web@1.1.0" } });
    fireEvent.click(screen.getByRole("button", { name: "生成计划" }));
    await waitFor(() => expect(create).toHaveBeenCalledWith("assembly-1", expect.objectContaining({ expected_manifest_checksum: sha("8"), expected_lock_checksum: sha("9") }), expect.any(Object)));
  });
});
