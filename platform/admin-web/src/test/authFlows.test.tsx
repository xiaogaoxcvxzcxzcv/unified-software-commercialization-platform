import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { adminClient } from "../api/adminClient";
import { AuthApiError, authClient, authenticatedAdminRequest } from "../api/authClient";
import { App } from "../app/App";
import type { AdminSession } from "../types";

const session: AdminSession = {
  session_id: "session-auth-tests",
  transport: "cookie",
  admin: {
    admin_user_id: "U-ADMIN-1",
    display_name: "张敏",
    account_status: "active",
    auth_time: "2026-07-13T12:00:00Z",
    authentication_method: "password",
  },
  authorization: {
    authorization_version: 3,
    permissions: ["product.read", "audit.read"],
    scopes: [{ scope_type: "platform", scope_id: null, product_id: null, tenant_id: null }],
  },
  access_expires_at: "2026-07-13T12:15:00Z",
  refresh_expires_at: "2026-07-20T12:00:00Z",
  csrf_token: "csrf-token-for-auth-tests-123456789012",
};

const unauthorized = () => new AuthApiError("unauthorized", {
  status: 401,
  code: "admin_auth.session_expired",
  retryable: false,
});

function renderApp(path: string) {
  return render(<MemoryRouter initialEntries={[path]}><App /></MemoryRouter>);
}

beforeEach(() => {
  vi.spyOn(adminClient, "listProducts").mockResolvedValue([{
    id: "prod-video", code: "video-brain", name: "视频生产大脑", version: "v1.8.2", status: "active",
    users: 0, activeUsers: 0, enabledCapabilities: ["统一账号", "权益", "代理租户"], accent: "#0f9f8f",
  }]);
  vi.spyOn(adminClient, "listTenants").mockResolvedValue([{
    id: "T-OFFICIAL", productId: "prod-video", name: "官方直营", code: "official", type: "official",
    admins: 1, users: 0, status: "active",
  }]);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("管理员会话保护", () => {
  it("启动时恢复真实会话并显示管理员摘要", async () => {
    vi.spyOn(authClient, "getSession").mockResolvedValue(session);
    renderApp("/overview");

    await screen.findByRole("heading", { name: "平台总览" }, { timeout: 3000 });
    expect(screen.getByRole("button", { name: "张敏，平台管理员" })).toBeInTheDocument();
  });

  it("GET session 返回 401 时只刷新一次并重新查询会话", async () => {
    const getSession = vi.spyOn(authClient, "getSession")
      .mockRejectedValueOnce(unauthorized())
      .mockResolvedValueOnce(session);
    const refresh = vi.spyOn(authClient, "refresh").mockResolvedValue(session);
    renderApp("/overview");

    await screen.findByRole("heading", { name: "平台总览" });
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(getSession).toHaveBeenCalledTimes(2);
  });

  it("刷新也失效时转到登录页且不加载后台业务数据", async () => {
    vi.spyOn(authClient, "getSession").mockRejectedValue(unauthorized());
    vi.spyOn(authClient, "refresh").mockRejectedValue(unauthorized());
    renderApp("/products/prod-video/users");

    await screen.findByRole("heading", { name: "管理员登录" });
    expect(screen.queryByRole("heading", { name: "用户管理" })).not.toBeInTheDocument();
  });

  it("认证服务不可用时明确报错并可重试，不会放行", async () => {
    const getSession = vi.spyOn(authClient, "getSession")
      .mockRejectedValueOnce(new AuthApiError("offline", { status: 0, code: "admin_auth.service_unavailable", retryable: true }))
      .mockResolvedValueOnce(session);
    renderApp("/overview");

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("暂时无法连接认证服务");
    expect(screen.queryByRole("heading", { name: "平台总览" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "重试" }));

    await screen.findByRole("heading", { name: "平台总览" });
    expect(getSession).toHaveBeenCalledTimes(2);
  });

  it("后端尚未实现认证路由时显示准确阻塞原因", async () => {
    vi.spyOn(authClient, "getSession").mockRejectedValue(new AuthApiError("not found", {
      status: 404,
      code: "request.not_found",
      retryable: false,
    }));
    renderApp("/overview");

    expect(await screen.findByRole("alert")).toHaveTextContent("管理员认证接口尚未由后端提供");
    expect(screen.queryByRole("heading", { name: "平台总览" })).not.toBeInTheDocument();
  });

  it("登录后回到原内部路径，凭据失败只显示通用错误", async () => {
    const user = userEvent.setup();
    vi.spyOn(authClient, "getSession").mockRejectedValue(unauthorized());
    vi.spyOn(authClient, "refresh").mockRejectedValue(unauthorized());
    const login = vi.spyOn(authClient, "login")
      .mockRejectedValueOnce(new AuthApiError("specific backend detail", { status: 401, code: "admin_auth.invalid_credentials", retryable: false }))
      .mockResolvedValueOnce(session);
    renderApp("/products/prod-video/users");
    await screen.findByRole("heading", { name: "管理员登录" });

    await user.type(screen.getByLabelText("管理账号"), "admin@example.com");
    await user.type(screen.getByLabelText("密码"), "wrong-password");
    await user.click(screen.getByRole("button", { name: "登录控制台" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("登录信息不正确或会话已经失效");
    expect(screen.queryByText("specific backend detail")).not.toBeInTheDocument();

    await user.type(screen.getByLabelText("密码"), "correct-password");
    await user.click(screen.getByRole("button", { name: "登录控制台" }));
    await screen.findByRole("heading", { name: "用户管理" });
    expect(login).toHaveBeenLastCalledWith("admin@example.com", "correct-password");
  });

  it("退出失败保留当前会话，重试成功后回到登录页", async () => {
    const user = userEvent.setup();
    vi.spyOn(authClient, "getSession").mockResolvedValue(session);
    const logout = vi.spyOn(authClient, "logout")
      .mockRejectedValueOnce(new AuthApiError("offline", { status: 0, code: "admin_auth.service_unavailable", retryable: true }))
      .mockResolvedValueOnce(undefined);
    renderApp("/overview");
    await screen.findByRole("heading", { name: "平台总览" });

    await user.click(screen.getByRole("button", { name: "张敏，平台管理员" }));
    await user.click(screen.getByRole("menuitem", { name: "退出登录" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("认证服务不可用");
    expect(screen.getByRole("heading", { name: "平台总览" })).toBeInTheDocument();

    await user.click(screen.getByRole("menuitem", { name: "退出登录" }));
    await screen.findByRole("heading", { name: "管理员登录" });
    expect(logout).toHaveBeenCalledWith(session.csrf_token);
    expect(logout).toHaveBeenCalledTimes(2);
  });
});

describe("认证请求传输", () => {
  it("登录使用同源 Cookie 凭据且不返回前端持久化 token", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(session), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    await authClient.login("admin@example.com", "password");

    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/v1/admin/auth/login");
    expect(init.credentials).toBe("include");
    expect(JSON.parse(String(init.body))).toEqual({ identifier: "admin@example.com", credential: "password", transport: "cookie" });
  });

  it("并发 refresh 复用同一请求，且 refresh 不要求 CSRF", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(session), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    const first = authClient.refresh();
    const second = authClient.refresh();
    expect(first).toBe(second);
    await Promise.all([first, second]);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(new Headers(init.headers).has("X-CSRF-Token")).toBe(false);
    expect(init.credentials).toBe("include");
  });

  it("统一管理写请求没有内存 CSRF 时会在前端拒绝", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    await expect(authenticatedAdminRequest("/api/v1/admin/products", { method: "POST" }, null))
      .rejects.toMatchObject({ code: "admin_auth.csrf_missing" });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("统一管理请求拒绝把 Cookie 或 CSRF 发送到非管理 API", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    await expect(authenticatedAdminRequest("https://example.com/collect", { method: "POST" }, session.csrf_token))
      .rejects.toMatchObject({ code: "admin_auth.invalid_admin_api_path" });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("统一管理写请求携带 Cookie 与内存 CSRF，不写入请求正文", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);

    await authenticatedAdminRequest<void>("/api/v1/admin/example", { method: "DELETE" }, session.csrf_token);

    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(init.credentials).toBe("include");
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe(session.csrf_token);
    expect(init.body).toBeUndefined();
  });
});
