import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { assemblyClient, type AssemblyPlanRecord, type AssemblyRunRecord, type BlueprintRecord } from "../api/assemblyClient";
import { AuthApiError } from "../api/authClient";
import { AssemblyRunPage } from "../pages/AssemblyRunPage";
import { AssemblyRunsPage } from "../pages/AssemblyRunsPage";
import { CreateBlueprintRecoveryPage, CreatePlanRecoveryPage } from "../pages/CreateRecoveryPage";

const openTrustedProduct = vi.fn();
vi.mock("../app/AppContext", () => ({ useAppContext: () => ({ openTrustedProduct }) }));
vi.mock("../components/Shell", () => ({ Shell: ({ title, children }: { title: string; children: React.ReactNode }) => <main><h1>{title}</h1>{children}</main> }));

const baseRun: AssemblyRunRecord = {
  run_id: "run-1", product_id: null, plan_id: "plan-1", plan_version: 2, version: 3,
  plan_checksum: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  root_run_id: "run-1", retry_of_run_id: null, attempt_number: 1, output_target_ref: "workspace-primary",
  status: "generating", current_step_id: "step-generate", steps: [{ step_id: "step-generate", kind: "generate", status: "running", attempt: 1, compensation_status: "not_required", started_at: "2026-07-16T01:00:00Z", finished_at: null, diagnostic_ids: [] }],
  recovery: { retryable: false, rollback_required: false, resume_from_step_id: null }, diagnostics: [], reports: [], document: {},
  created_at: "2026-07-16T01:00:00Z", updated_at: "2026-07-16T01:01:00Z", completed_at: null, audit_id: "audit-run-1",
};
const recoveryBlueprint: BlueprintRecord = { blueprint_id: "blueprint-1", version: 2, schema_version: "1.0.0", environments: ["test"], document: { generator: { id: "generator-1", version: "1.0.0" }, sdk: { id: "sdk-1", version: "1.0.0" }, applications: [{ environment: "production" }] }, checksum: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", created_at: "2026-07-16T01:00:00Z", updated_at: "2026-07-16T01:00:00Z", audit_id: "audit-blueprint-1" };
const recoveryPlan: AssemblyPlanRecord = { plan_id: "plan-1", version: 2, blueprint_id: "blueprint-1", blueprint_version: 2, schema_version: "1.0.0", environment: "test", confirmation_checksum: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", review: { packages: [{ package_id: "package.account", version: "1.0.0" }], applications: [{ application_id: "application.web", target: "web", channel: "web", delivery_mode: "generated_source", template_id: "standard-a", template_version: "1.0.0" }], risks: [{ risk_id: "risk-1", level: "medium", category: "generation", summary: "生成结果需要验证", requires_confirmation: true }], blocking_conflict_count: 0, statements: ["确认生成源码"] }, document: { confirmation: { summary_checksum: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" }, risks: [] }, checksum: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", executable: true, confirmed: false, created_at: "2026-07-16T01:00:00Z", updated_at: "2026-07-16T01:00:00Z", audit_id: "audit-plan-1" };
const renderDetail = (url = "/assemblies/run-1") => render(<MemoryRouter initialEntries={[url]}><Routes><Route path="/assemblies/:runId" element={<AssemblyRunPage />} /><Route path="/products/:productId/overview" element={<div>软件工作区</div>} /></Routes></MemoryRouter>);

beforeEach(() => { vi.restoreAllMocks(); openTrustedProduct.mockReset(); sessionStorage.clear(); });

describe("装配记录页面", () => {
  it("recovers a blueprint with GET only until the operator continues", async () => {
    const get = vi.spyOn(assemblyClient, "getBlueprint").mockResolvedValue(recoveryBlueprint);
    const create = vi.spyOn(assemblyClient, "createPlan");
    render(<MemoryRouter initialEntries={["/create/blueprints/blueprint-1"]}><Routes><Route path="/create/blueprints/:blueprintId" element={<CreateBlueprintRecoveryPage />} /></Routes></MemoryRouter>);
    expect(await screen.findByText("蓝图已恢复")).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(1);
    expect(create).not.toHaveBeenCalled();
  });

  it("reuses the blueprint plan intent across a failed response and page refresh", async () => {
    vi.spyOn(assemblyClient, "getBlueprint").mockResolvedValue(recoveryBlueprint);
    const create = vi.spyOn(assemblyClient, "createPlan").mockRejectedValueOnce(new Error("计划服务暂时不可用")).mockReturnValueOnce(new Promise(() => undefined));
    const route = <MemoryRouter initialEntries={["/create/blueprints/blueprint-1"]}><Routes><Route path="/create/blueprints/:blueprintId" element={<CreateBlueprintRecoveryPage />} /></Routes></MemoryRouter>;
    const first = render(route);
    fireEvent.click(await screen.findByRole("button", { name: "解析装配计划" }));
    expect(await screen.findByText("计划服务暂时不可用")).toBeInTheDocument();
    const firstKey = create.mock.calls[0][2].idempotencyKey;
    first.unmount();
    render(route);
    fireEvent.click(await screen.findByRole("button", { name: "解析装配计划" }));
    await waitFor(() => expect(create).toHaveBeenCalledTimes(2));
    expect(create.mock.calls[1][2].idempotencyKey).toBe(firstKey);
    expect(sessionStorage.getItem("assembly_plan_intent:blueprint-1")).toBe(firstKey);
  });

  it("fails closed when a blueprint projects more than one environment", async () => {
    vi.spyOn(assemblyClient, "getBlueprint").mockResolvedValue({ ...recoveryBlueprint, environments: ["development", "test"] });
    render(<MemoryRouter initialEntries={["/create/blueprints/blueprint-1"]}><Routes><Route path="/create/blueprints/:blueprintId" element={<CreateBlueprintRecoveryPage />} /></Routes></MemoryRouter>);
    expect(await screen.findByText(/蓝图包含多个环境/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "解析装配计划" })).toBeDisabled();
  });

  it("renders only the safe plan review and reuses the start intent across refresh", async () => {
    vi.spyOn(assemblyClient, "getPlan").mockResolvedValue(recoveryPlan);
    vi.spyOn(assemblyClient, "listOutputTargets").mockResolvedValue({ environment: "test", default_policy: "explicit", default_output_target_ref: "workspace-primary", items: [{ output_target_ref: "workspace-primary", display_name: "受控工作区", summary: "服务端授权目标", is_default: true }] });
    const start = vi.spyOn(assemblyClient, "startAssembly").mockRejectedValueOnce(new Error("启动响应中断")).mockReturnValueOnce(new Promise(() => undefined));
    const route = <MemoryRouter initialEntries={["/create/plans/plan-1"]}><Routes><Route path="/create/plans/:planId" element={<CreatePlanRecoveryPage />} /></Routes></MemoryRouter>;
    const first = render(route);
    expect(await screen.findByText("package.account")).toBeInTheDocument();
    expect(screen.getByText("生成结果需要验证")).toBeInTheDocument();
    expect(screen.getByText("确认生成源码")).toBeInTheDocument();
    expect(screen.queryByText("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "确认并开始装配" }));
    expect(await screen.findByText("启动响应中断")).toBeInTheDocument();
    const firstKey = start.mock.calls[0][2].idempotencyKey;
    first.unmount();
    render(route);
    fireEvent.click(await screen.findByRole("button", { name: "确认并开始装配" }));
    await waitFor(() => expect(start).toHaveBeenCalledTimes(2));
    expect(start.mock.calls[1][2].idempotencyKey).toBe(firstKey);
    expect(start.mock.calls[1][1].confirmation.summary_checksum).toBe(recoveryPlan.confirmation_checksum);
  });

  it("renders the real empty state without placeholder records", async () => {
    const list = vi.spyOn(assemblyClient, "listRuns").mockResolvedValue({ items: [], next_cursor: null });
    render(<MemoryRouter><AssemblyRunsPage /></MemoryRouter>);
    expect(await screen.findByText("还没有装配记录")).toBeInTheDocument();
    expect(list).toHaveBeenCalledWith({ page_size: 30 }, expect.objectContaining({ signal: expect.any(AbortSignal) }));
  });

  it("shows a localized not-found boundary for a missing run", async () => {
    vi.spyOn(assemblyClient, "getRun").mockRejectedValue(new AuthApiError("Not Found", {
      status: 404, code: "assembly.run_not_found", retryable: false,
    }));
    renderDetail("/assemblies/run-missing");
    expect(await screen.findByRole("alert")).toHaveTextContent("未找到该装配运行");
    expect(screen.queryByText("Not Found")).not.toBeInTheDocument();
  });

  it("ignores stale detail responses and aborts the active request on unmount", async () => {
    let resolveOld!: (run: AssemblyRunRecord) => void; let resolveNew!: (run: AssemblyRunRecord) => void;
    const oldResponse = new Promise<AssemblyRunRecord>((resolve) => { resolveOld = resolve; });
    const newResponse = new Promise<AssemblyRunRecord>((resolve) => { resolveNew = resolve; });
    const getRun = vi.spyOn(assemblyClient, "getRun").mockResolvedValueOnce(baseRun).mockReturnValueOnce(oldResponse).mockReturnValueOnce(newResponse);
    const view = renderDetail();
    expect(await screen.findByText("step-generate")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "刷新" }));
    fireEvent.click(screen.getByRole("button", { name: "刷新" }));
    resolveNew({ ...baseRun, current_step_id: "step-validate", updated_at: "2026-07-16T01:03:00Z" });
    expect(await screen.findByText("step-validate")).toBeInTheDocument();
    resolveOld({ ...baseRun, current_step_id: "step-stale", updated_at: "2026-07-16T01:02:00Z" });
    await waitFor(() => expect(screen.queryByText("step-stale")).not.toBeInTheDocument());
    const signal = getRun.mock.calls.at(-1)?.[1]?.signal;
    view.unmount();
    expect(signal?.aborted).toBe(true);
  });

  it("reuses the same persisted retry intent after a transient failure", async () => {
    const failed = { ...baseRun, status: "failed" as const, recovery: { retryable: true, rollback_required: false, resume_from_step_id: "step-generate" }, completed_at: "2026-07-16T01:05:00Z" };
    vi.spyOn(assemblyClient, "getRun").mockResolvedValue(failed);
    const retry = vi.spyOn(assemblyClient, "retryRun").mockRejectedValueOnce(new Error("网络暂时不可用")).mockReturnValueOnce(new Promise(() => undefined));
    renderDetail();
    fireEvent.click(await screen.findByRole("button", { name: "重试此运行" }));
    expect(await screen.findByText("网络暂时不可用")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "重试此运行" }));
    await waitFor(() => expect(retry).toHaveBeenCalledTimes(2));
    expect(retry.mock.calls[0][1]).toBe(3);
    expect(retry.mock.calls[0][2].idempotencyKey).toBe(retry.mock.calls[1][2].idempotencyKey);
    expect(sessionStorage.getItem("assembly_retry_intent:run-1")).toBe(retry.mock.calls[0][2].idempotencyKey);
  });

  it("keeps rollback-required failures read-only", async () => {
    vi.spyOn(assemblyClient, "getRun").mockResolvedValue({ ...baseRun, status: "failed", recovery: { retryable: true, rollback_required: true, resume_from_step_id: null } });
    renderDetail();
    expect(await screen.findByText("该运行需要后续生命周期恢复，当前记录保持只读。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "重试此运行" })).not.toBeInTheDocument();
  });

  it("does not hand off history and allows a failed handoff to be verified again", async () => {
    const completed = { ...baseRun, status: "completed" as const, product_id: "product-1", current_step_id: null, completed_at: "2026-07-16T01:05:00Z", manifest_url: "/api/v1/admin/assembly-manifests/manifest-1" };
    vi.spyOn(assemblyClient, "getRun").mockResolvedValue(completed);
    const manifest = vi.spyOn(assemblyClient, "getManifest").mockRejectedValueOnce(new Error("Manifest 暂时不可用")).mockResolvedValueOnce({ assembly_id: "manifest-1", product_id: "product-1", run_id: "run-1", schema_version: "1.0.0", document: {}, document_checksum: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", checksum: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", created_at: "2026-07-16T01:05:00Z" });
    const history = renderDetail();
    await screen.findByText("已完成");
    expect(manifest).not.toHaveBeenCalled();
    history.unmount();
    openTrustedProduct.mockResolvedValue(undefined);
    renderDetail("/assemblies/run-1?handoff=1");
    expect(await screen.findByText("Manifest 暂时不可用")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "重新验证工作区" }));
    await waitFor(() => expect(manifest).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(openTrustedProduct).toHaveBeenCalledWith("product-1"));
  });
});
