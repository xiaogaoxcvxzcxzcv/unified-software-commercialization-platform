import { useState } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { adminClient } from "../api/adminClient";
import { authClient } from "../api/authClient";
import { App } from "../app/App";
import { Modal } from "../components/Modal";
import type { AdminSession, EntitlementRecord, Product, TenantRecord } from "../types";

const videoProduct: Product = {
  id: "prod-video",
  code: "VIDEO_BRAIN",
  name: "视频生产大脑",
  version: "v1.8.2",
  status: "active",
  users: 1286,
  activeUsers: 842,
  enabledCapabilities: ["统一账号", "权益", "设备", "激活码", "代理租户"],
  accent: "#0f9f8f",
};

const testProducts: Product[] = [
  videoProduct,
  { ...videoProduct, id: "prod-copy", code: "copy-studio", name: "智能文案工坊", enabledCapabilities: ["统一账号", "权益", "代理租户"], accent: "#3b82f6" },
  { ...videoProduct, id: "prod-assets", code: "asset-desk", name: "素材管理助手", status: "paused", enabledCapabilities: ["统一账号", "云存储"], accent: "#7c3aed" },
];

const testTenants: TenantRecord[] = [
  { id: "T-OFFICIAL", productId: "prod-video", name: "官方直营", code: "official", type: "official", admins: 3, users: 1094, status: "active" },
  { id: "T-SOUTH", productId: "prod-video", name: "华南代理", code: "south-cn", type: "agent", admins: 2, users: 126, status: "active" },
  { id: "T-EAST", productId: "prod-video", name: "华东代理", code: "east-cn", type: "agent", admins: 1, users: 66, status: "active" },
  { id: "T-COPY", productId: "prod-copy", name: "官方直营", code: "official", type: "official", admins: 2, users: 458, status: "active" },
  { id: "T-ASSETS", productId: "prod-assets", name: "官方直营", code: "official", type: "official", admins: 1, users: 204, status: "active" },
];

const adminSession: AdminSession = {
  session_id: "session-admin-flows",
  session_version: 1,
  transport: "cookie",
  admin: {
    admin_user_id: "U-ADMIN",
    display_name: "测试管理员",
    account_status: "active",
    auth_time: "2026-07-13T12:00:00Z",
    authentication_method: "password",
  },
  authorization: {
    authorization_version: 1,
    permissions: ["product.read", "user.read", "entitlement.write"],
    scopes: [{ scope_type: "platform", scope_id: null, product_id: null, tenant_id: null }],
  },
  access_expires_at: "2026-07-13T12:15:00Z",
  refresh_expires_at: "2026-07-20T12:00:00Z",
  csrf_token: "csrf-token-for-admin-flows-1234567890",
};

function renderApp(path: string) {
  return render(<MemoryRouter initialEntries={[path]}><App /></MemoryRouter>);
}

beforeEach(() => {
  vi.spyOn(authClient, "getSession").mockResolvedValue(adminSession);
  vi.spyOn(adminClient, "listProducts").mockResolvedValue(testProducts);
  vi.spyOn(adminClient, "listTenants").mockImplementation(async (productId) => testTenants.filter((item) => item.productId === productId));
});

afterEach(() => vi.restoreAllMocks());

describe("管理后台关键上下文流程", () => {
  it("切换软件时更新路由与产品工作区", async () => {
    const user = userEvent.setup();
    renderApp("/products/prod-video/users");
    await screen.findByRole("heading", { name: "用户管理" });

    await user.selectOptions(screen.getByLabelText("当前软件"), "prod-copy");

    await screen.findByRole("heading", { name: "智能文案工坊" }, { timeout: 5_000 });
    expect(screen.getByRole("heading", { name: "用户与权益趋势" })).toBeInTheDocument();
    expect(screen.getByLabelText("当前软件")).toHaveValue("prod-copy");
  });

  it("切换租户后只展示所选租户的用户", async () => {
    const user = userEvent.setup();
    renderApp("/products/prod-video/users");
    await screen.findByText(/linxiao@example\.com/);

    await user.selectOptions(screen.getByLabelText("当前租户"), "T-SOUTH");

    await screen.findByText(/zhouqi@example\.com/);
    expect(screen.queryByText(/linxiao@example\.com/)).not.toBeInTheDocument();
  });

  it("直接访问未启用能力的路由会回到软件概览", async () => {
    renderApp("/products/prod-assets/entitlements");

    await screen.findByRole("heading", { name: "素材管理助手" });
    expect(screen.queryByRole("heading", { name: "权益管理" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "权益管理" })).not.toBeInTheDocument();
  });

  it("产品设置使用当前产品 ID 保存受控表单", async () => {
    const user = userEvent.setup();
    const update = vi.spyOn(adminClient, "updateProduct").mockResolvedValue({ ...videoProduct, name: "视频工作台" });
    renderApp("/products/prod-video/settings");
    await screen.findByRole("heading", { name: "产品设置" });
    const nameInput = screen.getByLabelText("软件名称");

    await waitFor(() => expect(nameInput).toHaveValue("视频生产大脑"));
    await user.clear(nameInput);
    await user.type(nameInput, "视频工作台");
    await user.click(screen.getByRole("button", { name: "保存信息" }));

    await waitFor(() => expect(update).toHaveBeenCalledWith("prod-video", { name: "视频工作台", version: "v1.8.2" }));
    expect(await screen.findByText("产品信息已保存到演示 Client")).toBeInTheDocument();
  });

  it("授予权益提交不可变 userId 而不是显示名", async () => {
    const user = userEvent.setup();
    const result: EntitlementRecord = {
      id: "E-TEST",
      productId: "prod-video",
      tenantId: "T-OFFICIAL",
      userId: "U-10028",
      user: "林晓",
      plan: "专业年卡",
      source: "管理员手工授予",
      validUntil: "2027-07-13",
      status: "active",
    };
    const grant = vi.spyOn(adminClient, "grantEntitlement").mockResolvedValue(result);
    renderApp("/products/prod-video/entitlements");
    await screen.findByRole("heading", { name: "权益管理" });
    await screen.findByText(/E-39021/);

    await user.click(screen.getByRole("button", { name: "授予权益" }));
    await user.selectOptions(screen.getByLabelText("目标用户"), "U-10028");
    await user.click(screen.getByRole("button", { name: "确认授予" }));

    await waitFor(() => expect(grant).toHaveBeenCalledWith("prod-video", "T-OFFICIAL", "U-10028", "专业年卡"));
  });

  it("列表加载失败后可以重试并恢复", async () => {
    const user = userEvent.setup();
    const listUsers = vi.spyOn(adminClient, "listUsers")
      .mockRejectedValueOnce(new Error("临时服务故障"))
      .mockResolvedValueOnce([]);
    renderApp("/products/prod-video/users");

    expect(await screen.findByRole("alert")).toHaveTextContent("临时服务故障");
    await user.click(screen.getByRole("button", { name: "重试" }));

    await screen.findByRole("heading", { name: "用户列表" });
    expect(listUsers).toHaveBeenCalledTimes(2);
    expect(screen.getByText("当前租户共 0 位用户")).toBeInTheDocument();
  });
});

describe("弹层与导航可访问性", () => {
  it("Modal 支持 Escape 关闭并恢复触发按钮焦点", async () => {
    const user = userEvent.setup();
    function Harness() {
      const [open, setOpen] = useState(false);
      return <><button type="button" onClick={() => setOpen(true)}>打开编辑</button><Modal title="编辑资料" open={open} onClose={() => setOpen(false)}><label>名称<input /></label></Modal></>;
    }
    render(<Harness />);
    const opener = screen.getByRole("button", { name: "打开编辑" });
    await user.click(opener);
    expect(screen.getByRole("dialog", { name: "编辑资料" })).toBeInTheDocument();

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
