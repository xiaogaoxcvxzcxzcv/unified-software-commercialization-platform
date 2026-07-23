import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { accountAdminClient, type AccountScope, type AdminUserDetail, type AdminUserSummary } from "../api/accountAdminClient";
import { AuthApiError } from "../api/authClient";
import { UserDetailPage } from "../pages/UserDetailPage";
import { UsersPage } from "../pages/UsersPage";

const mocks = vi.hoisted(() => ({ app: {} as Record<string, unknown>, auth: {} as Record<string, unknown> }));
vi.mock("../app/AppContext", () => ({ useAppContext: () => mocks.app }));
vi.mock("../app/AuthContext", () => ({ useAuth: () => mocks.auth }));

const product = { id: "product-1", name: "图片工作台", code: "image-studio", status: "active", provisioningState: "ready", officialTenantId: "tenant-1", contextVersion: 1, createdAt: "2026-07-17T01:00:00Z", updatedAt: "2026-07-18T01:00:00Z", auditId: "audit-product" };
const tenant = { id: "tenant-1", productId: "product-1", name: "官方直营", code: "official", type: "official", status: "active", externalAgentRef: null, contextVersion: 1, createdAt: "2026-07-17T01:00:00Z", updatedAt: "2026-07-18T01:00:00Z" };
const summary = (scopeType: "product" | "tenant" = "tenant"): AdminUserSummary => ({
  id: "user-1", version: 4, accountStatus: "active", displayName: "张三", identifiers: [{ type: "email", maskedValue: "z***@example.com", verified: true }],
  createdAt: "2026-07-17T01:00:00Z", memberSince: "2026-07-17T02:00:00Z", lastSeenAt: "2026-07-18T00:30:00Z", activeSessionCount: 1, totalSessionCount: 2,
  access: { scopeType, scopeId: scopeType === "tenant" ? "tenant-1" : "product-1", status: "active", explicit: true, version: scopeType === "tenant" ? 3 : 2, statusChangedAt: "2026-07-17T02:00:00Z" },
});
const detail = (scope: AccountScope): AdminUserDetail => ({ user: scope.type === "platform" ? { ...summary("product"), access: null } : summary(scope.type), profile: { userId: "user-1", version: 2, displayName: "张三", avatarUrl: null, locale: "zh-CN", timezone: "Asia/Shanghai" } });

function adminSession(platform = true) {
  return {
    session_id: "admin-session", session_version: 1, transport: "cookie", admin: { admin_user_id: "admin-1", display_name: "管理员", account_status: "active", auth_time: "2026-07-18T01:00:00Z", authentication_method: "password" },
    authorization: { authorization_version: 1, permissions: ["identity.user.read", "product.user-access.manage", ...(platform ? ["identity.security.manage"] : [])], scopes: platform ? [{ scope_type: "platform", scope_id: null, product_id: null, tenant_id: null }] : [{ scope_type: "product", scope_id: "product-1", product_id: "product-1", tenant_id: null }] },
    access_expires_at: "2026-07-18T02:00:00Z", refresh_expires_at: "2026-07-25T01:00:00Z", csrf_token: "csrf-token",
  };
}
function resetContext(platform = true) {
  mocks.app = { products: [product], currentProduct: product, enabledPackageIds: new Set(["package.account"]), tenants: [tenant], currentTenant: tenant, tenantsLoading: false, tenantsError: null, selectTenant: vi.fn(), refreshProducts: vi.fn(async () => {}), refreshWorkspace: vi.fn(async () => {}) };
  mocks.auth = { session: adminSession(platform), logout: vi.fn(async () => {}) };
}
function renderList() { return render(<MemoryRouter initialEntries={["/products/product-1/users"]}><UsersPage/></MemoryRouter>); }
function renderDetail() { return render(<MemoryRouter initialEntries={["/products/product-1/users/user-1"]}><Routes><Route path="/products/:productId/users/:userId" element={<UserDetailPage/>}/><Route path="/login" element={<div>登录页</div>}/><Route path="/products/:productId/audit" element={<div>审计页</div>}/></Routes></MemoryRouter>); }

beforeEach(() => { vi.restoreAllMocks(); resetContext(true); });

describe("identity.user-table", () => {
  it("搜索、筛选和分页均重新请求服务端而不在本地过滤", async () => {
    const list = vi.spyOn(accountAdminClient, "listUsers").mockImplementation(async (_scope, options) => ({ items: [summary()], nextCursor: options?.cursor ? null : "cursor-2" }));
    const user = userEvent.setup(); renderList();
    expect((await screen.findAllByText("张三")).length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: "添加用户" })).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("搜索用户"), "z@example.com");
    await user.selectOptions(screen.getByLabelText("账号安全状态"), "locked");
    await user.selectOptions(screen.getByLabelText("当前范围准入状态"), "suspended");
    await user.click(screen.getByRole("button", { name: "查询" }));
    await waitFor(() => expect(list).toHaveBeenLastCalledWith({ type: "tenant", productId: "product-1", tenantId: "tenant-1" }, expect.objectContaining({ query: "z@example.com", accountStatus: "locked", accessStatus: "suspended", pageSize: 20 })));
    await user.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(list).toHaveBeenLastCalledWith(expect.anything(), expect.objectContaining({ cursor: "cursor-2" })));
  });

  it("空状态和服务端错误重试完整", async () => {
    const list = vi.spyOn(accountAdminClient, "listUsers").mockRejectedValueOnce(new Error("网络暂时不可用")).mockResolvedValue({ items: [], nextCursor: null });
    const user = userEvent.setup(); renderList();
    expect(await screen.findByRole("alert")).toHaveTextContent("网络暂时不可用");
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("当前范围没有符合条件的用户")).toBeInTheDocument();
    expect(list).toHaveBeenCalledTimes(2);
  });

  it("旧书签在能力未启用时失败关闭且不请求用户 API", async () => {
    mocks.app = { ...mocks.app, enabledPackageIds: new Set<string>() };
    const list = vi.spyOn(accountAdminClient, "listUsers");
    renderList();
    expect(await screen.findByRole("alert")).toHaveTextContent("未启用账号能力");
    expect(list).not.toHaveBeenCalled();
  });
});

describe("identity.user-detail", () => {
  function mockDetailData() {
    vi.spyOn(accountAdminClient, "getUser").mockImplementation(async (scope) => detail(scope));
    vi.spyOn(accountAdminClient, "listSessions").mockResolvedValue({ items: [{ id: "session-1", productId: "product-1", applicationId: "app-web", tenantId: "tenant-1", environment: "production", authenticationMethod: "password", deviceLabel: "Windows", createdAt: "2026-07-17T01:00:00Z", lastSeenAt: "2026-07-18T00:30:00Z", expiresAt: "2026-07-25T01:00:00Z", revokedAt: null }], nextCursor: null });
  }

  it("区分租户、产品与平台全局影响，并在危险确认中显示用户和范围", async () => {
    mockDetailData();
    const mutation = vi.spyOn(accountAdminClient, "setAccess").mockResolvedValue({ userId: "user-1", scopeType: "tenant", scopeId: "tenant-1", status: "suspended", version: 4, auditId: "audit-access" });
    vi.spyOn(accountAdminClient, "getAuditEvent").mockResolvedValue({ auditId: "audit-access", occurredAt: "2026-07-18T01:00:00Z", actorId: "admin-1", permission: "product.user-access.manage", scopeType: "tenant", action: "product_user_access.status_changed", targetType: "user", targetId: "user-1", result: "success", traceId: "trace-1" });
    const user = userEvent.setup(); renderDetail();
    expect(await screen.findByRole("heading", { name: "范围准入" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "全局安全操作" })).toBeInTheDocument();
    const scopePanel = screen.getByRole("heading", { name: "范围准入" }).closest("section")!;
    await user.click(within(scopePanel).getAllByRole("button", { name: "停用" })[0]);
    const dialog = screen.getByRole("dialog", { name: "确认危险操作" });
    expect(dialog).toHaveTextContent("张三（user-1）");
    expect(dialog).toHaveTextContent("租户 官方直营");
    await user.click(within(dialog).getByRole("button", { name: "确认执行" }));
    await waitFor(() => expect(mutation).toHaveBeenCalledWith({ type: "tenant", productId: "product-1", tenantId: "tenant-1" }, "user-1", 3, "suspended", "operator_request", expect.objectContaining({ idempotencyKey: expect.any(String) })));
    expect(await screen.findByText("租户 官方直营已更新为已停用")).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "查看审计记录" })).toBeEnabled();
  });

  it("产品管理员不呈现平台级禁用和全局会话操作", async () => {
    resetContext(false); mockDetailData(); renderDetail();
    expect((await screen.findAllByText("张三")).length).toBeGreaterThan(0);
    expect(screen.queryByRole("heading", { name: "全局安全操作" })).not.toBeInTheDocument();
    expect(accountAdminClient.getUser).toHaveBeenCalledWith({ type: "product", productId: "product-1" }, "user-1");
    expect(accountAdminClient.getUser).not.toHaveBeenCalledWith({ type: "platform" }, "user-1");
  });

  it("只有平台安全权限才呈现全局操作", async () => {
    resetContext(true);
    mocks.auth = { ...mocks.auth, session: { ...adminSession(true), authorization: { ...adminSession(true).authorization, permissions: ["identity.user.read"] } } };
    mockDetailData(); renderDetail();
    expect(await screen.findAllByText("张三")).not.toHaveLength(0);
    expect(screen.queryByRole("heading", { name: "全局安全操作" })).not.toBeInTheDocument();
  });

  it("409 版本冲突显示稳定错误并重新读取详情", async () => {
    mockDetailData();
    vi.spyOn(accountAdminClient, "setAccess").mockRejectedValue(new AuthApiError("Conflict", { status: 409, code: "product_user_access.version_conflict", retryable: false }));
    const user = userEvent.setup(); renderDetail();
    const scopePanel = (await screen.findByRole("heading", { name: "范围准入" })).closest("section")!;
    await user.click(within(scopePanel).getAllByRole("button", { name: "停用" })[0]);
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "确认执行" }));
    expect(await within(screen.getByRole("dialog")).findByRole("alert")).toHaveTextContent("数据已被其他管理员更新");
    await waitFor(() => expect(accountAdminClient.getUser).toHaveBeenCalledTimes(6));
  });

  it("会话撤销只提交所选服务端 session_id", async () => {
    mockDetailData();
    const revoke = vi.spyOn(accountAdminClient, "revokeSessions").mockResolvedValue({ userId: "user-1", scopeType: "tenant", scopeId: "tenant-1", revokedCount: 1, auditId: "audit-session" });
    vi.spyOn(accountAdminClient, "getAuditEvent").mockResolvedValue({ auditId: "audit-session", occurredAt: "2026-07-18T01:00:00Z", actorId: "admin-1", permission: "product.user-access.manage", scopeType: "tenant", action: "identity.session_revoked", targetType: "user", targetId: "user-1", result: "success", traceId: "trace-2" });
    const user = userEvent.setup(); renderDetail();
    await user.click(await screen.findByLabelText("选择会话 session-1"));
    await user.click(screen.getByRole("button", { name: "撤销所选" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "确认执行" }));
    await waitFor(() => expect(revoke).toHaveBeenCalledWith({ type: "tenant", productId: "product-1", tenantId: "tenant-1" }, "user-1", { sessionIds: ["session-1"] }, "security_response", expect.anything()));
  });
});
