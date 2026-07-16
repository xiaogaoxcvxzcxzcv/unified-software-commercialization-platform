import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { adminClient } from "../api/adminClient";
import { authClient } from "../api/authClient";
import { App } from "../app/App";
import { Modal } from "../components/Modal";
import type { AdminSession, Product, ProductCapabilityProjection, TenantRecord } from "../types";
import { useState } from "react";

const product = (id: string, name: string, code: string, status: Product["status"] = "active"): Product => ({
  id, name, code, status, provisioningState: "ready", officialTenantId: `${id}-official`, contextVersion: 3,
  createdAt: "2026-07-13T10:00:00Z", updatedAt: "2026-07-15T10:00:00Z", auditId: `audit-${id}`,
});
const products = [product("prod-video", "视频生产大脑", "video-brain"), product("prod-copy", "智能文案工坊", "copy-studio"), product("prod-assets", "素材管理助手", "asset-desk", "suspended")];
const tenant = (id: string, productId: string, name: string, type: TenantRecord["type"] = "official"): TenantRecord => ({
  id, productId, name, code: id.toLowerCase(), type, status: "active", externalAgentRef: null, contextVersion: 2,
  createdAt: "2026-07-13T10:00:00Z", updatedAt: "2026-07-15T10:00:00Z",
});
const tenants = [tenant("T-OFFICIAL", "prod-video", "官方直营"), tenant("T-SOUTH", "prod-video", "华南代理", "agent"), tenant("T-COPY", "prod-copy", "文案直营")];
const capabilityProjection = (productId: string, packageIds: string[] = []): ProductCapabilityProjection => ({
  productId,
  capabilitySet: { productId, version: 2, sourcePlanId: `plan-${productId}`, catalogRevision: "catalog-8", catalogSnapshotSha256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", auditId: `audit-cap-${productId}`, capabilities: packageIds.map((packageId, index) => ({ capabilityId: `${packageId}.capability.${index}`, enabled: true, sourcePackageId: packageId, sourcePackageVersion: "1.0.0" })) },
});
const adminSession: AdminSession = {
  session_id: "session-admin-flows", session_version: 1, transport: "cookie",
  admin: { admin_user_id: "U-ADMIN", display_name: "测试管理员", account_status: "active", auth_time: "2026-07-13T12:00:00Z", authentication_method: "password" },
  authorization: { authorization_version: 1, permissions: ["product.read", "tenant.manage", "audit.read"], scopes: [{ scope_type: "platform", scope_id: null, product_id: null, tenant_id: null }] },
  access_expires_at: "2026-07-13T12:15:00Z", refresh_expires_at: "2026-07-20T12:00:00Z", csrf_token: "csrf-token-for-admin-flows-1234567890",
};

function renderApp(path: string) { return render(<MemoryRouter initialEntries={[path]}><App /></MemoryRouter>); }

beforeEach(() => {
  vi.spyOn(authClient, "getSession").mockResolvedValue(adminSession);
  vi.spyOn(adminClient, "listProducts").mockResolvedValue(products);
  vi.spyOn(adminClient, "listApplications").mockImplementation(async (productId) => [{ id: `app-${productId}`, productId, code: "web", name: `${productId} Web`, platform: "web", distributionChannel: "official", releaseTrack: "stable", status: "active", contextVersion: 2, createdAt: "2026-07-13T10:00:00Z", updatedAt: "2026-07-15T10:00:00Z" }]);
  vi.spyOn(adminClient, "getProductCapabilities").mockImplementation(async (productId) => capabilityProjection(productId, productId === "prod-video" ? ["package.account", "package.agent-operation"] : []));
  vi.spyOn(adminClient, "listTenants").mockImplementation(async (productId) => tenants.filter((item) => item.productId === productId));
});

afterEach(() => vi.restoreAllMocks());

describe("真实单款软件工作区", () => {
  it("切换 Product 会更新路由并重新加载 Product 作用域投影", async () => {
    const user = userEvent.setup();
    const applications = vi.mocked(adminClient.listApplications);
    const capabilities = vi.mocked(adminClient.getProductCapabilities);
    renderApp("/products/prod-video/overview");
    await screen.findByRole("heading", { name: "视频生产大脑" });

    await user.selectOptions(screen.getByLabelText("当前软件"), "prod-copy");

    await screen.findByRole("heading", { name: "智能文案工坊" });
    await waitFor(() => expect(applications).toHaveBeenCalledWith("prod-copy"));
    expect(capabilities).toHaveBeenCalledWith("prod-copy");
    expect(screen.getByText("prod-copy Web")).toBeInTheDocument();
  });

  it("切换 Tenant 会改变审计请求作用域", async () => {
    const audits = vi.spyOn(adminClient, "listAudits").mockResolvedValue([]);
    const user = userEvent.setup();
    renderApp("/products/prod-video/audit");
    await waitFor(() => expect(audits).toHaveBeenCalledWith("prod-video", "T-OFFICIAL"));

    await user.selectOptions(screen.getByLabelText("当前租户"), "T-SOUTH");

    await waitFor(() => expect(audits).toHaveBeenCalledWith("prod-video", "T-SOUTH"));
  });

  it("动态目录只由可信 source_package_id 启用", async () => {
    renderApp("/products/prod-video/overview");
    await screen.findByRole("heading", { name: "视频生产大脑" });
    expect(await screen.findByRole("button", { name: "用户管理" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "代理租户" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "权益管理" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "接入配置" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "能力目录" })).toBeInTheDocument();
  });

  it("未启用能力的旧书签失败关闭且不调用业务 API", async () => {
    const listUsers = vi.spyOn(adminClient, "listUsers");
    renderApp("/products/prod-assets/users");

    expect(await screen.findByRole("alert")).toHaveTextContent("当前软件未启用此能力");
    expect(screen.getByText("package.account")).toBeInTheDocument();
    expect(listUsers).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "用户管理" })).not.toBeInTheDocument();
  });

  it("已启用但尚未交付的能力页不加载演示 Client", async () => {
    const listUsers = vi.spyOn(adminClient, "listUsers");
    renderApp("/products/prod-video/users");

    expect(await screen.findByText("管理页面尚未交付")).toBeInTheDocument();
    expect(listUsers).not.toHaveBeenCalled();
  });

  it("产品列表使用真实状态字段并可进入工作区", async () => {
    const user = userEvent.setup();
    renderApp("/products");
    await screen.findByRole("heading", { name: "软件管理" });
    expect(screen.getAllByText("已就绪")).toHaveLength(3);

    await user.click(screen.getByRole("button", { name: "进入 视频生产大脑 工作区" }));

    await screen.findByRole("heading", { name: "视频生产大脑" });
  });

  it("基本信息为只读真实字段，不提供假保存", async () => {
    renderApp("/products/prod-video/settings");
    await screen.findByRole("heading", { name: "基本信息" });
    expect(screen.getByText("video-brain")).toBeInTheDocument();
    expect(screen.getByText("prod-video-official")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "保存信息" })).not.toBeInTheDocument();
  });
});

describe("弹层与导航可访问性", () => {
  it("Modal 支持 Escape 关闭并恢复触发按钮焦点", async () => {
    const user = userEvent.setup();
    function Harness() { const [open, setOpen] = useState(false); return <><button type="button" onClick={() => setOpen(true)}>打开编辑</button><Modal title="编辑资料" open={open} onClose={() => setOpen(false)}><label>名称<input /></label></Modal></>; }
    render(<Harness />);
    const opener = screen.getByRole("button", { name: "打开编辑" });
    await user.click(opener);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: "编辑资料" })).not.toBeInTheDocument();
    expect(opener).toHaveFocus();
  });

  it("通知与账号菜单互斥且支持 Escape 关闭", async () => {
    const user = userEvent.setup();
    renderApp("/overview");
    await screen.findByRole("heading", { name: "平台总览" });
    const notifications = screen.getByRole("button", { name: "系统通知" });
    const profile = screen.getByRole("button", { name: "测试管理员，平台管理员" });
    await user.click(notifications);
    expect(screen.getByRole("dialog", { name: "系统通知" })).toBeInTheDocument();
    await user.click(profile);
    expect(screen.queryByRole("dialog", { name: "系统通知" })).not.toBeInTheDocument();
    expect(screen.getByRole("menu")).toBeInTheDocument();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    expect(profile).toHaveAttribute("aria-expanded", "false");
  });

  it("移动侧栏使用独立抽屉状态并在导航后关闭", async () => {
    const user = userEvent.setup();
    renderApp("/overview");
    await screen.findByRole("heading", { name: "平台总览" });
    const menuButton = screen.getByRole("button", { name: "打开主菜单" });
    expect(menuButton).toHaveAttribute("aria-expanded", "false");
    await user.click(menuButton);
    expect(menuButton).toHaveAttribute("aria-expanded", "true");
    await user.click(screen.getByRole("button", { name: "软件管理" }));
    await screen.findByRole("heading", { name: "软件管理" });
    expect(screen.getByRole("button", { name: "打开主菜单" })).toHaveAttribute("aria-expanded", "false");
  });
});
