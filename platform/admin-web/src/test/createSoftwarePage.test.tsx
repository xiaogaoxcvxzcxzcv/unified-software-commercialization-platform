import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { adminClient } from "../api/adminClient";
import { assemblyClient, type AssemblyCatalogOptions, type OutputTargetCatalog } from "../api/assemblyClient";
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
  return render(<MemoryRouter initialEntries={[path]}><App /></MemoryRouter>);
}

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
    blueprint_id: "bp_image-studio", version: 1, schema_version: "1.0.0", document: {} as never,
    checksum: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    created_at: "2026-07-15T01:00:00Z", updated_at: "2026-07-15T01:00:00Z", audit_id: "audit-blueprint",
  };
}

function successfulPlan() {
  return {
    plan_id: "plan-image", version: 1, blueprint_id: "bp_image-studio", blueprint_version: 1,
    environment: "development" as const,
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
  vi.spyOn(adminClient, "listTenants").mockResolvedValue([]);
  vi.spyOn(assemblyClient, "listOutputTargets").mockResolvedValue(outputTargets);
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
        blueprint_id: "bp_image-studio", version: 1, schema_version: "1.0.0", document: {} as never,
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
    await screen.findByText("蓝图已保存");
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
    await screen.findByText("蓝图已保存");
    await user.click(screen.getByRole("button", { name: "解析装配计划" }));

    const target = await screen.findByRole("radio", { name: /受控工作区/ });
    expect(target).toHaveProperty("checked", expectedChecked);
  });

  it("计划缺少服务端确认摘要时失败关闭", async () => {
    vi.mocked(assemblyClient.listOutputTargets).mockResolvedValue({
      environment: "development", default_policy: "explicit", default_output_target_ref: "workspace-primary",
      items: [{ output_target_ref: "workspace-primary", display_name: "受控工作区", summary: "服务端管理的输出目标", is_default: true }],
    });
    vi.spyOn(assemblyClient, "listOrdinaryCatalogOptions").mockResolvedValue(fullCatalog);
    vi.spyOn(assemblyClient, "createBlueprint").mockResolvedValue(successfulBlueprint());
    const plan = successfulPlan();
    plan.document = { ...plan.document, confirmation: undefined } as never;
    vi.spyOn(assemblyClient, "createPlan").mockResolvedValue(plan);
    const start = vi.spyOn(assemblyClient, "startAssembly");
    const user = userEvent.setup();
    renderApp("/create");
    await fillConfiguration(user);
    await user.click(screen.getByRole("button", { name: /保存蓝图并继续/ }));
    await user.click(await screen.findByRole("button", { name: "解析装配计划" }));

    expect(await screen.findByText("计划缺少确认摘要")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "确认并开始装配" })).toBeDisabled();
    expect(start).not.toHaveBeenCalled();
  });

  it("completed Run 通过同源 manifest_url 读取可信 product_id，双击只启动一次", async () => {
    vi.mocked(assemblyClient.listOutputTargets).mockResolvedValue({
      environment: "development", default_policy: "explicit", default_output_target_ref: "workspace-primary",
      items: [{ output_target_ref: "workspace-primary", display_name: "受控工作区", summary: "服务端管理的输出目标", is_default: true }],
    });
    vi.spyOn(assemblyClient, "listOrdinaryCatalogOptions").mockResolvedValue(fullCatalog);
    vi.spyOn(assemblyClient, "createBlueprint").mockResolvedValue(successfulBlueprint());
    const plan = successfulPlan();
    vi.spyOn(assemblyClient, "createPlan").mockResolvedValue(plan);
    const start = vi.spyOn(assemblyClient, "startAssembly").mockResolvedValue({
      run_id: "run-image", plan_id: plan.plan_id, plan_version: 2, plan_checksum: plan.checksum,
      output_target_ref: "workspace-primary", status: "completed", document: {},
      created_at: "2026-07-15T01:02:00Z", updated_at: "2026-07-15T01:03:00Z", completed_at: "2026-07-15T01:03:00Z",
      audit_id: "audit-run", manifest_url: "/api/v1/admin/assembly-manifests/assembly-image", lock_url: "/api/v1/admin/generated-project-locks/lock-image",
    });
    const getManifest = vi.spyOn(assemblyClient, "getManifest").mockResolvedValue({
      assembly_id: "assembly-image", product_id: "product-image", run_id: "run-image", schema_version: "1.0.0", document: {},
      document_checksum: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
      checksum: "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", created_at: "2026-07-15T01:03:00Z",
    });
    const user = userEvent.setup();
    renderApp("/create");
    await fillConfiguration(user);
    await user.click(screen.getByRole("button", { name: /保存蓝图并继续/ }));
    await screen.findByText("蓝图已保存");
    await user.click(screen.getByRole("button", { name: "解析装配计划" }));
    const confirm = await screen.findByRole("button", { name: "确认并开始装配" });

    fireEvent.click(confirm);
    fireEvent.click(confirm);

    await waitFor(() => expect(getManifest).toHaveBeenCalledWith("assembly-image", { timeoutMs: 30_000 }));
    expect(start).toHaveBeenCalledTimes(1);
    expect(start.mock.calls[0][0]).toBe("bp_image-studio");
  });
});
