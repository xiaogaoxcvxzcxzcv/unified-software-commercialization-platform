import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { adminClient } from "../api/adminClient";
import { assemblyClient } from "../api/assemblyClient";
import { AuthApiError, authClient, authenticatedAdminRequest, getAdminCsrfToken, resetAdminAuthStateForTests } from "../api/authClient";
import { App } from "../app/App";
import type { AdminSession } from "../types";

const session: AdminSession = {
  session_id: "session-auth-tests",
  session_version: 1,
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

function errorResponse(status: number, code: string) {
  return new Response(JSON.stringify({
    status,
    code,
    title: "request rejected",
    request_id: `request-${code}`,
    retryable: false,
  }), { status, headers: { "Content-Type": "application/json" } });
}

function renderApp(path: string) {
  return render(<MemoryRouter initialEntries={[path]}><App /></MemoryRouter>);
}

beforeEach(() => {
  resetAdminAuthStateForTests();
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
  resetAdminAuthStateForTests();
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

  it("recovers the initial route through the real auth client without premature invalidation", async () => {
    const refreshedSession = { ...session, csrf_token: "csrf-after-initial-recovery-123456789" };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(errorResponse(401, "admin_auth.session_expired"))
      .mockResolvedValueOnce(new Response(JSON.stringify(refreshedSession), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify(refreshedSession), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    renderApp("/overview");

    await waitFor(() => expect(adminClient.listProducts).toHaveBeenCalled());
    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      "/api/v1/admin/auth/session",
      "/api/v1/admin/auth/refresh",
      "/api/v1/admin/auth/session",
    ]);
    expect(getAdminCsrfToken()).toBe(refreshedSession.csrf_token);
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

  it("并发管理请求遇到 access 过期时只刷新一次并使用新 CSRF 各自重放", async () => {
    let resolveRefresh!: (response: Response) => void;
    const refreshedSession = { ...session, csrf_token: "csrf-token-after-refresh-123456789012" };
    const refreshResponse = new Promise<Response>((resolve) => { resolveRefresh = resolve; });
    const fetchMock = vi.fn().mockImplementation((path: string, init: RequestInit) => {
      if (path === "/api/v1/admin/auth/refresh") return refreshResponse;
      const csrf = new Headers(init.headers).get("X-CSRF-Token");
      if (csrf === refreshedSession.csrf_token || path.endsWith("/audit/events") && fetchMock.mock.calls.filter(([calledPath]) => calledPath === path).length > 1) {
        return Promise.resolve(new Response(JSON.stringify({ ok: true }), { status: 200, headers: { "Content-Type": "application/json" } }));
      }
      return Promise.resolve(errorResponse(401, "admin_auth.session_expired"));
    });
    vi.stubGlobal("fetch", fetchMock);

    const writeRequest = authenticatedAdminRequest<{ ok: boolean }>("/api/v1/admin/products", {
      method: "POST",
      body: JSON.stringify({ name: "A" }),
    }, session.csrf_token);
    const readRequest = authenticatedAdminRequest<{ ok: boolean }>("/api/v1/admin/audit/events");

    await waitFor(() => expect(fetchMock.mock.calls.filter(([path]) => path === "/api/v1/admin/auth/refresh")).toHaveLength(1));
    resolveRefresh(new Response(JSON.stringify(refreshedSession), { status: 200, headers: { "Content-Type": "application/json" } }));
    await expect(Promise.all([writeRequest, readRequest])).resolves.toEqual([{ ok: true }, { ok: true }]);

    expect(fetchMock.mock.calls.filter(([path]) => path === "/api/v1/admin/auth/refresh")).toHaveLength(1);
    expect(fetchMock.mock.calls.filter(([path]) => path === "/api/v1/admin/products")).toHaveLength(2);
    expect(fetchMock.mock.calls.filter(([path]) => path === "/api/v1/admin/audit/events")).toHaveLength(2);
    const replayWrite = fetchMock.mock.calls.filter(([path]) => path === "/api/v1/admin/products")[1][1] as RequestInit;
    expect(new Headers(replayWrite.headers).get("X-CSRF-Token")).toBe(refreshedSession.csrf_token);
  });

  it("rechecks the session under the browser lock instead of consuming refresh twice", async () => {
    const refreshedSession = { ...session, csrf_token: "csrf-from-other-tab-refresh-123456789" };
    const lockManager = {
      request: vi.fn(async (_name: string, callback: () => Promise<AdminSession>) => callback()),
    };
    vi.stubGlobal("navigator", Object.assign(Object.create(navigator), { locks: lockManager }));
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(refreshedSession), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(authClient.refresh()).resolves.toEqual(refreshedSession);

    expect(lockManager.request).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/admin/auth/session");
  });

  it("does not let a late lower-version session response replace the latest session or CSRF", async () => {
    let resolveOldSession!: (response: Response) => void;
    const oldSessionResponse = new Promise<Response>((resolve) => { resolveOldSession = resolve; });
    const latestSession = {
      ...session,
      session_version: 2,
      csrf_token: "csrf-from-latest-refresh-123456789012",
    };
    const fetchMock = vi.fn()
      .mockReturnValueOnce(oldSessionResponse)
      .mockResolvedValueOnce(new Response(JSON.stringify(latestSession), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }));
    vi.stubGlobal("fetch", fetchMock);

    const delayedSession = authClient.getSession();
    await expect(authClient.refresh()).resolves.toEqual(latestSession);
    resolveOldSession(new Response(JSON.stringify(session), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));

    await expect(delayedSession).resolves.toEqual(latestSession);
    expect(getAdminCsrfToken()).toBe(latestSession.csrf_token);
  });

  it("does not let a late lower-version refresh response replace the latest session or CSRF", async () => {
    let resolveOldRefresh!: (response: Response) => void;
    const oldRefreshResponse = new Promise<Response>((resolve) => { resolveOldRefresh = resolve; });
    const latestSession = {
      ...session,
      session_version: 2,
      csrf_token: "csrf-from-latest-session-123456789012",
    };
    const fetchMock = vi.fn()
      .mockReturnValueOnce(oldRefreshResponse)
      .mockResolvedValueOnce(new Response(JSON.stringify(latestSession), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }));
    vi.stubGlobal("fetch", fetchMock);

    const delayedRefresh = authClient.refresh();
    await expect(authClient.getSession()).resolves.toEqual(latestSession);
    resolveOldRefresh(new Response(JSON.stringify(session), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }));

    await expect(delayedRefresh).resolves.toEqual(latestSession);
    expect(getAdminCsrfToken()).toBe(latestSession.csrf_token);
  });

  it("synchronizes a changed cross-tab epoch before sending an admin write", async () => {
    const refreshedSession = { ...session, csrf_token: "csrf-synchronized-from-other-tab-123456" };
    window.localStorage.setItem("platform_admin_session_epoch_v1", `other-tab-${Date.now()}`);
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(refreshedSession), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await authenticatedAdminRequest("/api/v1/admin/products", { method: "POST", body: "{}" }, session.csrf_token);

    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      "/api/v1/admin/auth/session",
      "/api/v1/admin/products",
    ]);
    const writeInit = fetchMock.mock.calls[1][1] as RequestInit;
    expect(new Headers(writeInit.headers).get("X-CSRF-Token")).toBe(refreshedSession.csrf_token);
  });

  it("continues into the single refresh flow when a changed epoch is already expired", async () => {
    const refreshedSession = { ...session, csrf_token: "csrf-after-expired-shared-epoch-123456" };
    const lockManager = {
      request: vi.fn(async (_name: string, callback: () => Promise<AdminSession>) => callback()),
    };
    vi.stubGlobal("navigator", Object.assign(Object.create(navigator), { locks: lockManager }));
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(session), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockResolvedValueOnce(errorResponse(401, "admin_auth.session_expired"))
      .mockResolvedValueOnce(errorResponse(401, "admin_auth.session_expired"))
      .mockResolvedValueOnce(errorResponse(401, "admin_auth.session_expired"))
      .mockResolvedValueOnce(new Response(JSON.stringify(refreshedSession), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    await authClient.login("admin@example.com", "password");
    window.localStorage.setItem("platform_admin_session_epoch_v1", `other-tab-expired-${Date.now()}`);

    await expect(authenticatedAdminRequest<{ ok: boolean }>(
      "/api/v1/admin/products",
      { method: "POST", body: "{}" },
    )).resolves.toEqual({ ok: true });

    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      "/api/v1/admin/auth/login",
      "/api/v1/admin/auth/session",
      "/api/v1/admin/products",
      "/api/v1/admin/auth/session",
      "/api/v1/admin/auth/refresh",
      "/api/v1/admin/products",
    ]);
    expect(lockManager.request).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls.filter(([path]) => path === "/api/v1/admin/auth/refresh")).toHaveLength(1);
    const replayWrite = fetchMock.mock.calls[5][1] as RequestInit;
    expect(new Headers(replayWrite.headers).get("X-CSRF-Token")).toBe(refreshedSession.csrf_token);
  });

  it("403 安全拒绝不触发 refresh", async () => {
    const fetchMock = vi.fn().mockResolvedValue(errorResponse(403, "admin_auth.csrf_failed"));
    vi.stubGlobal("fetch", fetchMock);

    await expect(authenticatedAdminRequest("/api/v1/admin/products", { method: "POST" }, session.csrf_token))
      .rejects.toMatchObject({ status: 403, code: "admin_auth.csrf_failed" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls.some(([path]) => path === "/api/v1/admin/auth/refresh")).toBe(false);
  });

  it("replays an Assembly write with the exact body and caller-owned idempotency key after one refresh", async () => {
    const refreshedSession = { ...session, session_version: 2, csrf_token: "csrf-after-assembly-refresh-123456789" };
    let planCalls = 0;
    const fetchMock = vi.fn().mockImplementation((path: string) => {
      if (path === "/api/v1/admin/auth/login") {
        return Promise.resolve(new Response(JSON.stringify(session), { status: 200, headers: { "Content-Type": "application/json" } }));
      }
      if (path === "/api/v1/admin/auth/refresh") {
        return Promise.resolve(new Response(JSON.stringify(refreshedSession), { status: 200, headers: { "Content-Type": "application/json" } }));
      }
      if (path === "/api/v1/admin/blueprints/blueprint-1/plan" && ++planCalls === 1) {
        return Promise.resolve(errorResponse(401, "admin_auth.session_expired"));
      }
      return Promise.resolve(new Response(JSON.stringify({ plan_id: "plan-1" }), { status: 200, headers: { "Content-Type": "application/json" } }));
    });
    vi.stubGlobal("fetch", fetchMock);
    await authClient.login("admin@example.com", "password");

    await assemblyClient.createPlan("blueprint-1", { blueprint_version: 1, environment: "test" }, { idempotencyKey: "assembly-plan-key-0001" });

    const writes = fetchMock.mock.calls.filter(([path]) => path === "/api/v1/admin/blueprints/blueprint-1/plan");
    expect(writes).toHaveLength(2);
    const first = writes[0][1] as RequestInit;
    const replay = writes[1][1] as RequestInit;
    expect(first.body).toBe(replay.body);
    expect(new Headers(first.headers).get("Idempotency-Key")).toBe("assembly-plan-key-0001");
    expect(new Headers(replay.headers).get("Idempotency-Key")).toBe("assembly-plan-key-0001");
    expect(new Headers(replay.headers).get("X-CSRF-Token")).toBe(refreshedSession.csrf_token);
    expect(fetchMock.mock.calls.filter(([path]) => path === "/api/v1/admin/auth/refresh")).toHaveLength(1);
  });

  it("preserves structured field errors from an admin API problem response", async () => {
    const payload = {
      status: 422,
      code: "assembly.document_invalid",
      title: "Invalid blueprint",
      detail: "Blueprint validation failed",
      request_id: "request-field-errors",
      retryable: false,
      field_errors: [{ field: "packages", code: "min_items", message: "Select a package" }],
    };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(payload), {
      status: 422,
      headers: { "Content-Type": "application/json" },
    })));

    await expect(authenticatedAdminRequest("/api/v1/admin/blueprints", { method: "POST" }, session.csrf_token))
      .rejects.toMatchObject({
        code: payload.code,
        requestId: payload.request_id,
        detail: payload.detail,
        fieldErrors: payload.field_errors,
      });
  });

  it("退出瞬时失败保留内存 CSRF 以便安全重试", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(session), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockRejectedValueOnce(new TypeError("network offline"))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    await authClient.login("admin@example.com", "password");

    await expect(authClient.logout(session.csrf_token)).rejects.toMatchObject({ code: "admin_auth.service_unavailable" });
    expect(getAdminCsrfToken()).toBe(session.csrf_token);
    await expect(authClient.logout(session.csrf_token)).resolves.toBeUndefined();
    expect(getAdminCsrfToken()).toBeNull();
  });

  it("logout uses the latest in-memory CSRF after refresh", async () => {
    const refreshedSession = { ...session, csrf_token: "csrf-token-current-after-refresh-123456" };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(session), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify(refreshedSession), { status: 200, headers: { "Content-Type": "application/json" } }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);

    await authClient.login("admin@example.com", "password");
    await authClient.refresh();
    await authClient.logout(session.csrf_token);

    const logoutInit = fetchMock.mock.calls[2][1] as RequestInit;
    expect(new Headers(logoutInit.headers).get("X-CSRF-Token")).toBe(refreshedSession.csrf_token);
  });

  it("refresh 终态失败通知 AuthContext 回到登录页", async () => {
    vi.spyOn(authClient, "getSession").mockResolvedValue(session);
    renderApp("/overview");
    await screen.findByRole("heading", { name: "平台总览" });
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(errorResponse(401, "admin_auth.session_expired"))
      .mockResolvedValueOnce(errorResponse(401, "admin_auth.session_revoked"));
    vi.stubGlobal("fetch", fetchMock);

    await expect(authenticatedAdminRequest("/api/v1/admin/products"))
      .rejects.toMatchObject({ code: "admin_auth.session_revoked" });
    await screen.findByRole("heading", { name: "管理员登录" });
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
