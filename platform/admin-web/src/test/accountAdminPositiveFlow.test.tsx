import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { accountAdminClient, type AccountScope, type AdminUserDetail, type AdminUserSession, type AdminUserSummary } from "../api/accountAdminClient";
import { UserDetailPage } from "../pages/UserDetailPage";
import { UsersPage } from "../pages/UsersPage";

const mocks = vi.hoisted(() => ({ app: {} as Record<string, unknown>, auth: {} as Record<string, unknown> }));
vi.mock("../app/AppContext", () => ({ useAppContext: () => mocks.app }));
vi.mock("../app/AuthContext", () => ({ useAuth: () => mocks.auth }));

const product = {
  id: "product-account-positive", name: "账号验收工作台", code: "account-positive",
  status: "active", provisioningState: "ready", officialTenantId: "tenant-account-official",
  contextVersion: 1, createdAt: "2026-07-18T01:00:00Z", updatedAt: "2026-07-18T01:00:00Z", auditId: "audit-product",
};
const tenant = {
  id: "tenant-account-official", productId: product.id, name: "官方直营", code: "official",
  type: "official", status: "active", externalAgentRef: null, contextVersion: 1,
  createdAt: "2026-07-18T01:00:00Z", updatedAt: "2026-07-18T01:00:00Z",
};

type AccessState = { status: "active" | "suspended"; explicit: boolean; version: number };
let tenantAccess: AccessState;
let productAccess: AccessState;
let globalStatus: "active" | "locked" | "disabled";
let globalVersion: number;
let sessionRevoked: boolean;

const session = (): AdminUserSession => ({
  id: "end-session-1", productId: product.id, applicationId: "application-web", tenantId: tenant.id,
  environment: "production", authenticationMethod: "password", deviceLabel: "浏览器验收",
  createdAt: "2026-07-18T01:05:00Z", lastSeenAt: "2026-07-18T01:10:00Z", expiresAt: "2026-07-25T01:05:00Z",
  revokedAt: sessionRevoked ? "2026-07-18T01:20:00Z" : null,
});

const summary = (scopeType: "product" | "tenant" = "tenant"): AdminUserSummary => ({
  id: "user-account-1", version: globalVersion, accountStatus: globalStatus, displayName: "真实账号用户",
  identifiers: [{ type: "email", maskedValue: "u***@example.com", verified: true }],
  createdAt: "2026-07-18T01:00:00Z", memberSince: "2026-07-18T01:05:00Z", lastSeenAt: "2026-07-18T01:10:00Z",
  activeSessionCount: sessionRevoked ? 0 : 1, totalSessionCount: 1,
  access: { scopeType, scopeId: scopeType === "tenant" ? tenant.id : product.id, status: scopeType === "tenant" ? tenantAccess.status : productAccess.status, explicit: scopeType === "tenant" ? tenantAccess.explicit : productAccess.explicit, version: scopeType === "tenant" ? tenantAccess.version : productAccess.version, statusChangedAt: null },
});

const detail = (scope: AccountScope): AdminUserDetail => ({
  user: scope.type === "platform" ? { ...summary("product"), access: null } : summary(scope.type),
  profile: { userId: "user-account-1", version: 1, displayName: "真实账号用户", avatarUrl: null, locale: "zh-CN", timezone: "Asia/Shanghai" },
});

function adminSession() {
  return {
    session_id: "admin-session-positive", session_version: 1, transport: "cookie",
    admin: { admin_user_id: "admin-positive", display_name: "平台管理员", account_status: "active", auth_time: new Date().toISOString(), authentication_method: "password" },
    authorization: { authorization_version: 1, permissions: ["product.read", "tenant.manage", "identity.user.read", "product.user-access.manage", "identity.security.manage", "audit.read"], scopes: [{ scope_type: "platform", scope_id: null, product_id: null, tenant_id: null }] },
    access_expires_at: "2026-07-25T01:00:00Z", refresh_expires_at: "2026-08-01T01:00:00Z", csrf_token: "csrf-positive-token",
  };
}

function resetState() {
  tenantAccess = { status: "active", explicit: false, version: 0 };
  productAccess = { status: "active", explicit: false, version: 0 };
  globalStatus = "active";
  globalVersion = 1;
  sessionRevoked = false;
  mocks.app = { products: [product], currentProduct: product, enabledPackageIds: new Set(["package.account"]), tenants: [tenant], currentTenant: tenant, tenantsLoading: false, tenantsError: null, selectTenant: vi.fn(), refreshProducts: vi.fn(async () => []), refreshWorkspace: vi.fn(async () => {}), refreshTenants: vi.fn(async () => {}) };
  mocks.auth = { session: adminSession(), logout: vi.fn(async () => {}) };
}

function renderWorkspace() {
  return render(<MemoryRouter initialEntries={[`/products/${product.id}/users`]}><Routes>
    <Route path="/products/:productId/users" element={<UsersPage />} />
    <Route path="/products/:productId/users/:userId" element={<UserDetailPage />} />
    <Route path="/products/:productId/audit" element={<div>审计工作区</div>} />
  </Routes></MemoryRouter>);
}

function mockPositiveApi() {
  vi.spyOn(accountAdminClient, "listUsers").mockResolvedValue({ items: [summary()], nextCursor: null });
  vi.spyOn(accountAdminClient, "getUser").mockImplementation(async (scope) => detail(scope));
  vi.spyOn(accountAdminClient, "listSessions").mockImplementation(async () => ({ items: [session()], nextCursor: null }));
  vi.spyOn(accountAdminClient, "setAccess").mockImplementation(async (scope, _userId, expectedVersion, status) => {
    const target = scope.type === "tenant" ? tenantAccess : productAccess;
    if (target.version !== expectedVersion) throw new Error("version conflict");
    target.status = status;
    target.explicit = true;
    target.version += 1;
    return { userId: "user-account-1", scopeType: scope.type, scopeId: scope.type === "tenant" ? tenant.id : product.id, status, version: target.version, auditId: `audit-access-v${target.version}` };
  });
  vi.spyOn(accountAdminClient, "revokeSessions").mockImplementation(async () => {
    sessionRevoked = true;
    return { userId: "user-account-1", scopeType: "tenant", scopeId: tenant.id, revokedCount: 1, auditId: "audit-session-revoke" };
  });
  vi.spyOn(accountAdminClient, "setGlobalSecurity").mockImplementation(async (_userId, expectedVersion, status) => {
    if (expectedVersion !== globalVersion) throw new Error("version conflict");
    globalStatus = status;
    globalVersion += 1;
    return { userId: "user-account-1", scopeType: "platform", scopeId: null, status, version: globalVersion, auditId: `audit-global-v${globalVersion}` };
  });
  vi.spyOn(accountAdminClient, "getAuditEvent").mockResolvedValue({ auditId: "audit-ready", occurredAt: "2026-07-18T01:20:00Z", actorId: "admin-positive", permission: "product.user-access.manage", scopeType: "tenant", action: "product_user_access.status_changed", targetType: "user", targetId: "user-account-1", result: "success", traceId: "trace-positive" });
}

beforeEach(() => { vi.restoreAllMocks(); resetState(); });

describe("Account positive management flow", () => {
  it("lists the tenant-scoped user and completes v0 -> v1 -> v2, session revoke, and global security", async () => {
    mockPositiveApi();
    const user = userEvent.setup();
    renderWorkspace();

    expect(await screen.findByText("真实账号用户")).toBeInTheDocument();
    expect(screen.getAllByText("正常").length).toBeGreaterThanOrEqual(1);
    await user.click(screen.getByRole("button", { name: /查看 真实账号用户 详情/ }));
    expect(await screen.findByRole("heading", { name: "范围准入" })).toBeInTheDocument();
    expect(screen.getByText("v0 · 继承状态")).toBeInTheDocument();
    expect(screen.getByText("application-web")).toBeInTheDocument();

    const scopePanel = screen.getByRole("heading", { name: "范围准入" }).closest("section")!;
    await user.click(within(scopePanel).getByTitle("变更当前范围准入"));
    const firstDialog = screen.getByRole("dialog", { name: "确认危险操作" });
    await user.click(within(firstDialog).getByRole("button", { name: "确认执行" }));
    await waitFor(() => expect(accountAdminClient.setAccess).toHaveBeenCalledWith({ type: "tenant", productId: product.id, tenantId: tenant.id }, "user-account-1", 0, "suspended", "operator_request", expect.anything()));
    await waitFor(() => expect(screen.getByText("v1 · 显式状态")).toBeInTheDocument());
    expect(screen.getAllByText("已暂停").length).toBeGreaterThanOrEqual(1);

    await user.click(within(scopePanel).getByRole("button", { name: "恢复" }));
    await user.click(within(screen.getByRole("dialog", { name: "确认危险操作" })).getByRole("button", { name: "确认执行" }));
    await waitFor(() => expect(accountAdminClient.setAccess).toHaveBeenCalledWith({ type: "tenant", productId: product.id, tenantId: tenant.id }, "user-account-1", 1, "active", "operator_request", expect.anything()));
    await waitFor(() => expect(screen.getByText("v2 · 显式状态")).toBeInTheDocument());

    await user.click(screen.getByLabelText("选择会话 end-session-1"));
    await user.click(screen.getByRole("button", { name: "撤销所选" }));
    await user.click(within(screen.getByRole("dialog", { name: "确认危险操作" })).getByRole("button", { name: "确认执行" }));
    await waitFor(() => expect(accountAdminClient.revokeSessions).toHaveBeenCalledWith({ type: "tenant", productId: product.id, tenantId: tenant.id }, "user-account-1", { sessionIds: ["end-session-1"] }, "security_response", expect.anything()));
    await waitFor(() => expect(screen.getAllByText("已撤销").length).toBeGreaterThanOrEqual(1));

    await user.click(screen.getByRole("button", { name: "全局锁定" }));
    await user.click(within(screen.getByRole("dialog", { name: "确认危险操作" })).getByRole("button", { name: "确认执行" }));
    await waitFor(() => expect(accountAdminClient.setGlobalSecurity).toHaveBeenCalledWith("user-account-1", 1, "locked", "security_response", expect.anything()));
    await waitFor(() => expect(screen.getByText(/用户版本 v2/)).toBeInTheDocument());
    expect(screen.getAllByText("已锁定").length).toBeGreaterThanOrEqual(1);

    await user.click(screen.getByRole("button", { name: "恢复账号" }));
    await user.click(within(screen.getByRole("dialog", { name: "确认危险操作" })).getByRole("button", { name: "确认执行" }));
    await waitFor(() => expect(accountAdminClient.setGlobalSecurity).toHaveBeenCalledWith("user-account-1", 2, "active", "security_response", expect.anything()));
  });

  it("retries a projected audit with a bounded four-attempt loop", async () => {
    mockPositiveApi();
    const audit = vi.mocked(accountAdminClient.getAuditEvent);
    audit.mockRejectedValueOnce(Object.assign(new Error("not yet"), { status: 404 }))
      .mockRejectedValueOnce(Object.assign(new Error("not yet"), { status: 404 }))
      .mockRejectedValueOnce(Object.assign(new Error("not yet"), { status: 404 }))
      .mockResolvedValueOnce({ auditId: "audit-access-v1", occurredAt: "2026-07-18T01:20:00Z", actorId: "admin-positive", permission: "product.user-access.manage", scopeType: "tenant", action: "product_user_access.status_changed", targetType: "user", targetId: "user-account-1", result: "success", traceId: "trace-positive" });
    const user = userEvent.setup();
    renderWorkspace();
    await user.click(await screen.findByRole("button", { name: /查看 真实账号用户 详情/ }));
    const scopePanel = await screen.findByRole("heading", { name: "范围准入" }).then((heading) => heading.closest("section")!);
    await user.click(within(scopePanel).getByTitle("变更当前范围准入"));
    await user.click(within(screen.getByRole("dialog", { name: "确认危险操作" })).getByRole("button", { name: "确认执行" }));
    await waitFor(() => expect(audit).toHaveBeenCalledTimes(1));
    await new Promise((resolve) => setTimeout(resolve, 2300));
    await waitFor(() => expect(audit).toHaveBeenCalledTimes(4));
    expect(await screen.findByRole("button", { name: "查看审计记录" })).toBeEnabled();
  });

  it("cancels pending audit retries after detail unmount", async () => {
    mockPositiveApi();
    const audit = vi.mocked(accountAdminClient.getAuditEvent);
    audit.mockRejectedValue(Object.assign(new Error("not yet"), { status: 404 }));
    const user = userEvent.setup();
    const view = renderWorkspace();
    await user.click(await screen.findByRole("button", { name: /查看 真实账号用户 详情/ }));
    const scopePanel = await screen.findByRole("heading", { name: "范围准入" }).then((heading) => heading.closest("section")!);
    await user.click(within(scopePanel).getByTitle("变更当前范围准入"));
    await user.click(within(screen.getByRole("dialog", { name: "确认危险操作" })).getByRole("button", { name: "确认执行" }));
    await waitFor(() => expect(audit).toHaveBeenCalledTimes(1));
    view.unmount();
    await new Promise((resolve) => setTimeout(resolve, 500));
    expect(audit).toHaveBeenCalledTimes(1);
  });
});
