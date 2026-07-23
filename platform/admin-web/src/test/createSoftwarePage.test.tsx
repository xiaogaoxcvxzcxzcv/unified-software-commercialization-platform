import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { adminClient } from "../api/adminClient";
import { assemblyClient, type AssemblyCatalogOptions, type AssemblyRunRecord, type OutputTargetCatalog } from "../api/assemblyClient";
import { AuthApiError, authClient } from "../api/authClient";
import { App } from "../app/App";
import type { AdminSession } from "../types";

const session: AdminSession = {
  session_id: "session-create-page", session_version: 1, transport: "cookie",
  admin: { admin_user_id: "admin-1", display_name: "创建管理员", account_status: "active", auth_time: "2026-07-15T01:00:00Z", authentication_method: "password" },
  authorization: { authorization_version: 1, permissions: ["assembly.blueprint.manage", "assembly.plan", "assembly.execute"], scopes: [{ scope_type: "platform", scope_id: null, product_id: null, tenant_id: null }] },
  access_expires_at: "2026-07-15T02:00:00Z", refresh_expires_at: "2026-07-22T01:00:00Z", csrf_token: "csrf-create-page-12345678901234567890",
};
const filter = { target: "web", delivery_mode: "generated_source", environment: "development" } as const;
const emptyCatalog: AssemblyCatalogOptions = { catalog_scope: "ordinary", catalog_revision: "catalog-empty-1", ...filter, packages: [], templates: [], generators: [], sdks: [] };
const fullCatalog: AssemblyCatalogOptions = {
  catalog_scope: "ordinary", catalog_revision: "catalog-ordinary-1", ...filter,
  packages: [{ package_id: "package.account", version: "1.0.0", name: "统一账号", user_value: "登录与账号中心", dependencies: [], conflicts: [], compatible_template_refs: [{ id: "standard-a", version: "1.0.0" }] }],
  templates: [{ template_id: "standard-a", version: "1.0.0", name: "标准界面", supported_blocks: ["account.profile"] }],
  generators: [{ id: "platform.generator", version: "1.0.0", name: "平台生成器" }],
  sdks: [{ id: "platform.sdk", version: "1.0.0", name: "TypeScript SDK" }],
};
const outputTargets: OutputTargetCatalog = { environment: "development", default_policy: "explicit", default_output_target_ref: null, items: [] };

function renderApp(path: string) {
  return render(<MemoryRouter initialEntries={[path]}><App /><LocationProbe /></MemoryRouter>);
}

function LocationProbe() { const location = useLocation(); return <output data-testid="route-path">{location.pathname}</output>; }

async function fillConfiguration(user: ReturnType<typeof userEvent.setup>) {
  await screen.findByRole("heading", { name: "创建软件" });
  await user.type(screen.getByLabelText("软件名称"), "图片工作台");
  await user.type(screen.getByLabelText("软件代码"), "image-studio");
  await user.click(screen.getByRole("button", { name: "下一步" }));
  await screen.findByText("统一账号");
  await user.click(screen.getByRole("checkbox", { name: /统一账号/ }));
  await user.click(screen.getByRole("button", { name: "下一步" }));
  await user.selectOptions(screen.getByLabelText("用户前台模板"), "standard-a@1.0.0");
  await user.selectOptions(screen.getByLabelText("Generator"), "platform.generator@1.0.0");
  await user.selectOptions(screen.getByLabelText("SDK"), "platform.sdk@1.0.0");
}

function successfulBlueprint() {
  return {
    blueprint_id: "bp_image-studio", version: 1, schema_version: "1.0.0", environments: ["development" as const], document: {} as never,
    checksum: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    created_at: "2026-07-15T01:00:00Z", updated_at: "2026-07-15T01:00:00Z", audit_id: "audit-blueprint",
  };
}

function successfulPlan() {
  return {
    plan_id: "plan-image", version: 1, blueprint_id: "bp_image-studio", blueprint_version: 1, schema_version: "1.0.0",
    environment: "development" as const,
    confirmation_checksum: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    review: { packages: [{ package_id: "package.account", version: "1.0.0" }], applications: [{ application_id: "application.web", target: "web" as const, channel: "web", delivery_mode: "generated_source" as const, template_id: "standard-a", template_version: "1.0.0" }], risks: [], blocking_conflict_count: 0, statements: ["确认装配计划"] },
    document: {
      dependencies: [], risks: [], conflicts: [], expected_outputs: [{ path: "apps/web/index.ts" }],
      confirmation: { blocking_conflict_count: 0, summary_checksum: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" },
    },
    checksum: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
    executable: true, confirmed: false,
    created_at: "2026-07-15T01:01:00Z", updated_at: "2026-07-15T01:01:00Z", audit_id: "audit-plan",
  };
}

beforeEach(() => {
  vi.spyOn(authClient, "getSession").mockResolvedValue(session);
  vi.spyOn(adminClient, "listProducts").mockResolvedValue([]);
  vi.spyOn(adminClient, "listApplications").mockResolvedValue([]);
  vi.spyOn(adminClient, "getProductCapabilities").mockImplementation(async (productId) => ({ productId, capabilitySet: null }));
  vi.spyOn(adminClient, "listTenants").mockResolvedValue([]);
  vi.spyOn(assemblyClient, "listOutputTargets").mockResolvedValue(outputTargets);
  vi.spyOn(assemblyClient, "getBlueprint").mockImplementation(async () => successfulBlueprint());
  vi.spyOn(assemblyClient, "getPlan").mockImplementation(async () => successfulPlan());
});

afterEach(() => vi.restoreAllMocks());

describe("创建软件向导", () => {
  it("普通目录为空时显示真实失败关闭状态并禁止创建", async () => {
    const user = userEvent.setup();
    const ordinary = vi.spyOn(assemblyClient, "listOrdinaryCatalogOptions").mockResolvedValue(emptyCatalog);
    const experimental = vi.spyOn(assemblyClient, "listExperimentalCatalogOptions");
    const createBlueprint = vi.spyOn(assemblyClient, "createBlueprint");
    renderApp("/create?experimental=1");

    await screen.findByRole("heading", { name: "创建软件" });
    await user.type(screen.getByLabelText("软件名称"), "图片工作台");
    await user.type(screen.getByLabelText("软件代码"), "image-studio");
    await user.click(screen.getByRole("button", { name: "下一步" }));

    expect(await screen.findByText("当前没有可创建的软件组合")).toBeInTheDocument();
    expect(screen.getByText(/普通目录中还没有达到 available/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "下一步" })).toBeDisabled();
    expect(ordinary).toHaveBeenCalledWith(filter, expect.any(Object));
    expect(experimental).not.toHaveBeenCalled();
    expect(createBlueprint).not.toHaveBeenCalled();
  });

  it("实验路由只调用独立服务端入口并显示 403 授权结果", async () => {
    const ordinary = vi.spyOn(assemblyClient, "listOrdinaryCatalogOptions");
    const experimental = vi.spyOn(assemblyClient, "listExperimentalCatalogOptions").mockRejectedValue(new AuthApiError("denied", {
      status: 403, code: "access.denied", retryable: false,
    }));
    renderApp("/create/experimental");

    await screen.findByRole("heading", { name: "实验性创建软件" });
    expect(await screen.findByText("当前管理员未获授权使用受控实验目录。")).toBeInTheDocument();
    expect(experimental).toHaveBeenCalledWith(filter, expect.any(Object));
    expect(ordinary).not.toHaveBeenCalled();
  });

  it("实验路由解析计划时只调用 experimental plan 入口", async () => {
    const experimentalCatalog = { ...fullCatalog, catalog_scope: "experimental" as const, catalog_revision: "catalog-experimental-1" };
    vi.spyOn(assemblyClient, "listExperimentalCatalogOptions").mockResolvedValue(experimentalCatalog);
    vi.spyOn(assemblyClient, "createBlueprint").mockResolvedValue(successfulBlueprint());
    const ordinaryPlan = vi.spyOn(assemblyClient, "createPlan");
    const experimentalPlan = vi.spyOn(assemblyClient, "createExperimentalPlan").mockResolvedValue(successfulPlan());
    const user = userEvent.setup();
    renderApp("/create/experimental");

    await screen.findByRole("heading", { name: "实验性创建软件" });
    await user.type(screen.getByLabelText("软件名称"), "图片工作台");
    await user.type(screen.getByLabelText("软件代码"), "image-studio");
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await screen.findByText("统一账号");
    await user.click(screen.getByRole("checkbox", { name: /统一账号/ }));
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.selectOptions(screen.getByLabelText("用户前台模板"), "standard-a@1.0.0");
    await user.selectOptions(screen.getByLabelText("Generator"), "platform.generator@1.0.0");
    await user.selectOptions(screen.getByLabelText("SDK"), "platform.sdk@1.0.0");
    await user.click(screen.getByRole("button", { name: /保存蓝图并继续/ }));
    await screen.findByText("蓝图已恢复");
    const planButton = await screen.findByRole("button", { name: "解析装配计划" });
    await waitFor(() => expect(planButton).toBeEnabled());
    await user.click(planButton);

    await waitFor(() => expect(experimentalPlan).toHaveBeenCalledTimes(1));
    expect(ordinaryPlan).not.toHaveBeenCalled();
    expect(experimentalPlan.mock.calls[0][0]).toBe("bp_image-studio");
    expect(experimentalPlan.mock.calls[0][1]).toEqual({ blueprint_version: 1, environment: "development" });
  });

  it("软件管理的创建按钮进入向导而不直接创建 Product", async () => {
    vi.spyOn(assemblyClient, "listOrdinaryCatalogOptions").mockResolvedValue(emptyCatalog);
    const createProduct = vi.spyOn(adminClient, "createProduct");
    const user = userEvent.setup();
    renderApp("/products");
    await screen.findByRole("heading", { name: "软件管理" });

    await user.click(screen.getAllByRole("button", { name: "创建软件" }).at(-1)!);

    await screen.findByRole("heading", { name: "创建软件" });
    expect(createProduct).not.toHaveBeenCalled();
  });

  it("可重试的蓝图写请求复用同一幂等键", async () => {
    vi.spyOn(assemblyClient, "listOrdinaryCatalogOptions").mockResolvedValue(fullCatalog);
    const createBlueprint = vi.spyOn(assemblyClient, "createBlueprint")
      .mockRejectedValueOnce(new AuthApiError("temporary", { status: 503, code: "assembly.unavailable", retryable: true }))
      .mockResolvedValueOnce({
        blueprint_id: "bp_image-studio", version: 1, schema_version: "1.0.0", environments: ["development"], document: {} as never,
        checksum: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        created_at: "2026-07-15T01:00:00Z", updated_at: "2026-07-15T01:00:00Z", audit_id: "audit-1",
      });
    const user = userEvent.setup();
    renderApp("/create");

    await screen.findByRole("heading", { name: "创建软件" });
    await user.type(screen.getByLabelText("软件名称"), "图片工作台");
    await user.type(screen.getByLabelText("软件代码"), "image-studio");
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await screen.findByText("统一账号");
    await user.click(screen.getByRole("checkbox", { name: /统一账号/ }));
    await user.click(screen.getByRole("button", { name: "下一步" }));
    await user.selectOptions(screen.getByLabelText("用户前台模板"), "standard-a@1.0.0");
    await user.selectOptions(screen.getByLabelText("Generator"), "platform.generator@1.0.0");
    await user.selectOptions(screen.getByLabelText("SDK"), "platform.sdk@1.0.0");
    await user.click(screen.getByRole("button", { name: /保存蓝图并继续/ }));

    expect(await screen.findByRole("button", { name: "重试请求" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试请求" }));
    await screen.findByText("蓝图已恢复");
    expect(screen.getByTestId("route-path")).toHaveTextContent("/create/blueprints/bp_image-studio");
    await waitFor(() => expect(createBlueprint).toHaveBeenCalledTimes(2));
    expect(createBlueprint.mock.calls[0][1].idempotencyKey).toBe(createBlueprint.mock.calls[1][1].idempotencyKey);
    expect(createBlueprint.mock.calls[0][0]).toMatchObject({ blueprint_id: "bp_image-studio", provider_refs: [] });
  });

  it("把 422 field_errors 显示在向导字段错误区", async () => {
    vi.spyOn(assemblyClient, "listOrdinaryCatalogOptions").mockResolvedValue(fullCatalog);
    vi.spyOn(assemblyClient, "createBlueprint").mockRejectedValue(new AuthApiError("蓝图不合法", {
      status: 422, code: "assembly.document_invalid", retryable: false,
      detail: "Blueprint validation failed",
      fieldErrors: [{ field: "packages", code: "min_items", message: "至少选择一个完整能力包" }],
    }));
    const user = userEvent.setup();
    renderApp("/create");
    await fillConfiguration(user);

    await user.click(screen.getByRole("button", { name: /保存蓝图并继续/ }));

    expect(await screen.findByText("packages：至少选择一个完整能力包")).toBeInTheDocument();
    expect(screen.getByText("Blueprint validation failed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "返回修改" })).toBeInTheDocument();
  });

  it.each([
    ["服务端显式默认", "workspace-primary", true],
    ["没有服务端默认", null, false],
  ])("输出目标：%s", async (_name, defaultRef, expectedChecked) => {
    vi.mocked(assemblyClient.listOutputTargets).mockResolvedValue({
      environment: "development", default_policy: "explicit", default_output_target_ref: defaultRef,
      items: [{ output_target_ref: "workspace-primary", display_name: "受控工作区", summary: "服务端管理的输出目标", is_default: defaultRef !== null }],
    });
    vi.spyOn(assemblyClient, "listOrdinaryCatalogOptions").mockResolvedValue(fullCatalog);
    vi.spyOn(assemblyClient, "createBlueprint").mockResolvedValue(successfulBlueprint());
    vi.spyOn(assemblyClient, "createPlan").mockResolvedValue(successfulPlan());
    const user = userEvent.setup();
    renderApp("/create");
    await fillConfiguration(user);
    await user.click(screen.getByRole("button", { name: /保存蓝图并继续/ }));
    await screen.findByText("蓝图已恢复");
    await user.click(screen.getByRole("button", { name: "解析装配计划" }));

    const target = await screen.findByRole("radio", { name: /受控工作区/ });
    expect(screen.getByTestId("route-path")).toHaveTextContent("/create/plans/plan-image");
    expect(target).toHaveProperty("checked", expectedChecked);
  });

  it("计划恢复只使用顶层确认摘要并忽略 raw document", async () => {
    vi.mocked(assemblyClient.listOutputTargets).mockResolvedValue({
      environment: "development", default_policy: "explicit", default_output_target_ref: "workspace-primary",
      items: [{ output_target_ref: "workspace-primary", display_name: "受控工作区", summary: "服务端管理的输出目标", is_default: true }],
    });
    vi.spyOn(assemblyClient, "listOrdinaryCatalogOptions").mockResolvedValue(fullCatalog);
    vi.spyOn(assemblyClient, "createBlueprint").mockResolvedValue(successfulBlueprint());
    const plan = successfulPlan();
    plan.document = { confirmation: { summary_checksum: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" }, executable: false } as never;
    vi.spyOn(assemblyClient, "createPlan").mockResolvedValue(plan);
    vi.mocked(assemblyClient.getPlan).mockResolvedValue(plan);
    const start = vi.spyOn(assemblyClient, "startAssembly");
    const user = userEvent.setup();
    renderApp("/create");
    await fillConfiguration(user);
    await user.click(screen.getByRole("button", { name: /保存蓝图并继续/ }));
    await user.click(await screen.findByRole("button", { name: "解析装配计划" }));

    expect(await screen.findByText("确认装配计划")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "确认并开始装配" })).toBeEnabled();
    expect(start).not.toHaveBeenCalled();
  });

  it("completed Run 通过同源 manifest_url 读取可信 product_id，双击只启动一次", async () => {
    vi.mocked(adminClient.listProducts).mockResolvedValueOnce([]).mockResolvedValue([{
      id: "product-image", code: "image-studio", name: "图片工作台", status: "active", provisioningState: "ready",
      officialTenantId: "tenant-image", contextVersion: 1, createdAt: "2026-07-15T01:03:00Z", updatedAt: "2026-07-15T01:03:00Z", auditId: "audit-product-image",
    }]);
    vi.mocked(assemblyClient.listOutputTargets).mockResolvedValue({
      environment: "development", default_policy: "explicit", default_output_target_ref: "workspace-primary",
      items: [{ output_target_ref: "workspace-primary", display_name: "受控工作区", summary: "服务端管理的输出目标", is_default: true }],
    });
    vi.spyOn(assemblyClient, "listOrdinaryCatalogOptions").mockResolvedValue(fullCatalog);
    vi.spyOn(assemblyClient, "createBlueprint").mockResolvedValue(successfulBlueprint());
    const plan = successfulPlan();
    vi.spyOn(assemblyClient, "createPlan").mockResolvedValue(plan);
    const completedRun = {
      run_id: "run-image", product_id: "product-image", plan_id: plan.plan_id, plan_version: 2, version: 1, plan_checksum: plan.checksum,
      root_run_id: "run-image", retry_of_run_id: null, attempt_number: 1, current_step_id: null,
      steps: [], recovery: { retryable: false, rollback_required: false, resume_from_step_id: null }, diagnostics: [], reports: [],
      output_target_ref: "workspace-primary", status: "completed", document: {},
      created_at: "2026-07-15T01:02:00Z", updated_at: "2026-07-15T01:03:00Z", completed_at: "2026-07-15T01:03:00Z",
      audit_id: "audit-run", manifest_url: "/api/v1/admin/assembly-manifests/assembly-image", lock_url: "/api/v1/admin/generated-project-locks/lock-image",
    } as const;
    const start = vi.spyOn(assemblyClient, "startAssembly").mockResolvedValue(completedRun);
    vi.spyOn(assemblyClient, "getRun").mockResolvedValue(completedRun);
    const getManifest = vi.spyOn(assemblyClient, "getManifest").mockResolvedValue({
      assembly_id: "assembly-image", product_id: "product-image", run_id: "run-image", schema_version: "1.0.0", document: {},
      document_checksum: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
      checksum: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", created_at: "2026-07-15T01:03:00Z",
    });
    const user = userEvent.setup();
    renderApp("/create");
    await fillConfiguration(user);
    await user.click(screen.getByRole("button", { name: /保存蓝图并继续/ }));
    await screen.findByText("蓝图已恢复");
    await user.click(screen.getByRole("button", { name: "解析装配计划" }));
    const confirm = await screen.findByRole("button", { name: "确认并开始装配" });

    fireEvent.click(confirm);
    fireEvent.click(confirm);

    await waitFor(() => expect(getManifest).toHaveBeenCalledWith("assembly-image", expect.objectContaining({ timeoutMs: 20_000 })));
    expect(start).toHaveBeenCalledTimes(1);
    expect(start.mock.calls[0][0]).toBe("bp_image-studio");
    expect(await screen.findByRole("heading", { name: "图片工作台" })).toBeInTheDocument();
    expect(vi.mocked(adminClient.listProducts).mock.calls.length).toBeGreaterThanOrEqual(2);
  });

  it("completed manifest 的 Product 刷新后不可读时保持在向导且不跳转", async () => {
    vi.mocked(adminClient.listProducts).mockResolvedValue([]);
    vi.mocked(assemblyClient.listOutputTargets).mockResolvedValue({
      environment: "development", default_policy: "explicit", default_output_target_ref: "workspace-primary",
      items: [{ output_target_ref: "workspace-primary", display_name: "受控工作区", summary: "服务端管理的输出目标", is_default: true }],
    });
    vi.spyOn(assemblyClient, "listOrdinaryCatalogOptions").mockResolvedValue(fullCatalog);
    vi.spyOn(assemblyClient, "createBlueprint").mockResolvedValue(successfulBlueprint());
    const plan = successfulPlan();
    vi.spyOn(assemblyClient, "createPlan").mockResolvedValue(plan);
    const completedRun = {
      run_id: "run-hidden", product_id: "product-hidden", plan_id: plan.plan_id, plan_version: 2, version: 1, plan_checksum: plan.checksum,
      root_run_id: "run-hidden", retry_of_run_id: null, attempt_number: 1, current_step_id: null,
      steps: [], recovery: { retryable: false, rollback_required: false, resume_from_step_id: null }, diagnostics: [], reports: [],
      output_target_ref: "workspace-primary", status: "completed", document: {}, created_at: "2026-07-15T01:02:00Z", updated_at: "2026-07-15T01:03:00Z", completed_at: "2026-07-15T01:03:00Z",
      audit_id: "audit-run-hidden", manifest_url: "/api/v1/admin/assembly-manifests/assembly-hidden", lock_url: "/api/v1/admin/generated-project-locks/lock-hidden",
    } as const;
    vi.spyOn(assemblyClient, "startAssembly").mockResolvedValue(completedRun);
    vi.spyOn(assemblyClient, "getRun").mockResolvedValue(completedRun);
    vi.spyOn(assemblyClient, "getManifest").mockResolvedValue({
      assembly_id: "assembly-hidden", product_id: "product-hidden", run_id: "run-hidden", schema_version: "1.0.0", document: {},
      document_checksum: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", checksum: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", created_at: "2026-07-15T01:03:00Z",
    });
    const user = userEvent.setup();
    renderApp("/create");
    await fillConfiguration(user);
    await user.click(screen.getByRole("button", { name: /保存蓝图并继续/ }));
    await user.click(await screen.findByRole("button", { name: "解析装配计划" }));
    await user.click(await screen.findByRole("button", { name: "确认并开始装配" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("当前管理员无权读取新软件");
    expect(screen.getByRole("heading", { name: "装配运行" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "图片工作台" })).not.toBeInTheDocument();
  });

  it("completed manifest 后刷新 Product 列表网络失败时显示局部错误且不跳转", async () => {
    vi.mocked(adminClient.listProducts)
      .mockResolvedValueOnce([])
      .mockRejectedValueOnce(new AuthApiError("软件列表刷新失败，请重试", {
        status: 503, code: "product.list_unavailable", retryable: true,
      }));
    vi.mocked(assemblyClient.listOutputTargets).mockResolvedValue({
      environment: "development", default_policy: "explicit", default_output_target_ref: "workspace-primary",
      items: [{ output_target_ref: "workspace-primary", display_name: "受控工作区", summary: "服务端管理的输出目标", is_default: true }],
    });
    vi.spyOn(assemblyClient, "listOrdinaryCatalogOptions").mockResolvedValue(fullCatalog);
    vi.spyOn(assemblyClient, "createBlueprint").mockResolvedValue(successfulBlueprint());
    const plan = successfulPlan();
    vi.spyOn(assemblyClient, "createPlan").mockResolvedValue(plan);
    const completedRun = {
      run_id: "run-network", product_id: "product-image", plan_id: plan.plan_id, plan_version: 2, version: 1, plan_checksum: plan.checksum,
      root_run_id: "run-network", retry_of_run_id: null, attempt_number: 1, current_step_id: null,
      steps: [], recovery: { retryable: false, rollback_required: false, resume_from_step_id: null }, diagnostics: [], reports: [],
      output_target_ref: "workspace-primary", status: "completed", document: {},
      created_at: "2026-07-15T01:02:00Z", updated_at: "2026-07-15T01:03:00Z", completed_at: "2026-07-15T01:03:00Z",
      audit_id: "audit-run-network", manifest_url: "/api/v1/admin/assembly-manifests/assembly-network", lock_url: "/api/v1/admin/generated-project-locks/lock-network",
    } as const;
    vi.spyOn(assemblyClient, "startAssembly").mockResolvedValue(completedRun);
    vi.spyOn(assemblyClient, "getRun").mockResolvedValue(completedRun);
    vi.spyOn(assemblyClient, "getManifest").mockResolvedValue({
      assembly_id: "assembly-network", product_id: "product-image", run_id: "run-network", schema_version: "1.0.0", document: {},
      document_checksum: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
      checksum: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", created_at: "2026-07-15T01:03:00Z",
    });
    const user = userEvent.setup();
    renderApp("/create");
    await fillConfiguration(user);
    await user.click(screen.getByRole("button", { name: /保存蓝图并继续/ }));
    await user.click(await screen.findByRole("button", { name: "解析装配计划" }));
    await user.click(await screen.findByRole("button", { name: "确认并开始装配" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("软件列表刷新失败，请重试");
    expect(screen.getByRole("heading", { name: "装配运行" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "图片工作台" })).not.toBeInTheDocument();
    expect(adminClient.listProducts).toHaveBeenCalledTimes(2);
  });
});
