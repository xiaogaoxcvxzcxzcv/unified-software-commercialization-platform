import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { accountAdminClient } from "../api/accountAdminClient";
import { authClient, resetAdminAuthStateForTests } from "../api/authClient";
import type { AdminSession } from "../types";

const session: AdminSession = {
  session_id: "admin-session", session_version: 1, transport: "cookie",
  admin: { admin_user_id: "admin-1", display_name: "平台管理员", account_status: "active", auth_time: "2026-07-18T01:00:00Z", authentication_method: "password" },
  authorization: { authorization_version: 1, permissions: ["identity.user.read", "identity.security.manage", "product.user-access.manage"], scopes: [{ scope_type: "platform", scope_id: null, product_id: null, tenant_id: null }] },
  access_expires_at: "2026-07-18T02:00:00Z", refresh_expires_at: "2026-07-25T01:00:00Z", csrf_token: "csrf-account-admin-1234567890123456",
};
const summary = {
  user_id: "user-1", user_version: 4, account_status: "active", display_name: "张三",
  identifiers: [{ type: "email", masked_value: "z***@example.com", verified: true }], created_at: "2026-07-17T01:00:00Z", member_since: "2026-07-17T02:00:00Z", last_seen_at: "2026-07-18T00:30:00Z",
  active_session_count: 1, total_session_count: 2, access: { scope_type: "tenant", scope_id: "tenant-1", status: "active", explicit: true, version: 3, status_changed_at: "2026-07-17T02:00:00Z" },
};
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json", "X-Request-Id": "request-1" } });

beforeEach(() => resetAdminAuthStateForTests());
afterEach(() => { resetAdminAuthStateForTests(); vi.unstubAllGlobals(); });

describe("accountAdminClient", () => {
  it("使用服务端范围、搜索、筛选和 cursor 并严格投影用户", async () => {
    const requested: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input); if (path.endsWith("/auth/login")) return json(session);
      requested.push(path); return json({ items: [summary], next_cursor: "cursor-2" });
    }));
    await authClient.login("admin", "password");
    const page = await accountAdminClient.listUsers({ type: "tenant", productId: "product:1", tenantId: "tenant:1" }, { query: "z@example.com", accountStatus: "active", accessStatus: "suspended", cursor: "cursor/1", pageSize: 20 });
    expect(requested).toEqual(["/api/v1/admin/products/product%3A1/tenants/tenant%3A1/users?query=z%40example.com&account_status=active&access_status=suspended&cursor=cursor%2F1&page_size=20"]);
    expect(page).toEqual({ items: [expect.objectContaining({ id: "user-1", version: 4, displayName: "张三", accountStatus: "active", activeSessionCount: 1, access: expect.objectContaining({ scopeType: "tenant", scopeId: "tenant-1", version: 3 }) })], nextCursor: "cursor-2" });
  });

  it("拒绝多余字段而不把非契约数据带入页面", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => String(input).endsWith("/auth/login") ? json(session) : json({ items: [{ ...summary, plan: "专业版" }], next_cursor: null })));
    await authClient.login("admin", "password");
    await expect(accountAdminClient.listUsers({ type: "product", productId: "product-1" })).rejects.toThrow("admin user summary shape is invalid");
  });

  it("读取详情和范围会话且不混用当前用户自助接口", async () => {
    const requested: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input); if (path.endsWith("/auth/login")) return json(session); requested.push(path);
      if (path.endsWith("/sessions?page_size=20")) return json({ items: [{ session_id: "session-1", product_id: "product-1", application_id: "app-1", tenant_id: "tenant-1", environment: null, authentication_method: "password", device_label: "Windows", created_at: "2026-07-17T01:00:00Z", last_seen_at: "2026-07-18T01:00:00Z", expires_at: "2026-07-25T01:00:00Z", revoked_at: null }], next_cursor: null });
      return json({ user: summary, profile: { user_id: "user-1", version: 2, display_name: "张三", avatar_url: null, locale: "zh-CN", timezone: "Asia/Shanghai" } });
    }));
    await authClient.login("admin", "password");
    const scope = { type: "tenant" as const, productId: "product-1", tenantId: "tenant-1" };
    expect((await accountAdminClient.getUser(scope, "user-1")).profile.timezone).toBe("Asia/Shanghai");
    expect((await accountAdminClient.listSessions(scope, "user-1")).items[0]).toEqual(expect.objectContaining({ id: "session-1", productId: "product-1", environment: null }));
    expect(requested).toEqual(["/api/v1/admin/products/product-1/tenants/tenant-1/users/user-1", "/api/v1/admin/products/product-1/tenants/tenant-1/users/user-1/sessions?page_size=20"]);
  });

  it("写操作携带幂等键、CSRF、乐观版本并保留服务端 audit_id", async () => {
    const requests: Array<{ path: string; init?: RequestInit }> = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input); if (path.endsWith("/auth/login")) return json(session); requests.push({ path, init });
      if (path.endsWith("/access")) return json({ user_id: "user-1", scope_type: "tenant", scope_id: "tenant-1", status: "suspended", version: 4, audit_id: "audit-access" });
      if (path.endsWith("/sessions/revoke")) return json({ user_id: "user-1", scope_type: "tenant", scope_id: "tenant-1", revoked_count: 1, audit_id: "audit-session" });
      return json({ user_id: "user-1", scope_type: "platform", scope_id: null, status: "disabled", version: 5, audit_id: "audit-global" });
    }));
    await authClient.login("admin", "password");
    const scope = { type: "tenant" as const, productId: "product-1", tenantId: "tenant-1" };
    expect((await accountAdminClient.setAccess(scope, "user-1", 3, "suspended", "operator_request", { idempotencyKey: "access-intent-1" })).auditId).toBe("audit-access");
    expect((await accountAdminClient.revokeSessions(scope, "user-1", { sessionIds: ["session-1"] }, "security_response", { idempotencyKey: "session-intent-1" })).revokedCount).toBe(1);
    expect((await accountAdminClient.setGlobalSecurity("user-1", 4, "disabled", "security_response", { idempotencyKey: "global-intent-1" })).auditId).toBe("audit-global");
    expect(new Headers(requests[0].init?.headers).get("Idempotency-Key")).toBe("access-intent-1");
    expect(new Headers(requests[0].init?.headers).get("X-CSRF-Token")).toBe(session.csrf_token);
    expect(requests[0].init?.body).toBe(JSON.stringify({ expected_version: 3, status: "suspended", reason_code: "operator_request" }));
    expect(requests[1].init?.body).toBe(JSON.stringify({ session_ids: ["session-1"], reason_code: "security_response" }));
  });

  it("严格投影单条审计事件", async () => {
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => String(input).endsWith("/auth/login") ? json(session) : json({ audit_id: "audit-1", occurred_at: "2026-07-18T01:00:00Z", actor_id: "admin-1", permission: "identity.security.manage", scope_type: "platform", action: "identity.user_security_status_changed", target_type: "user", target_id: "user-1", result: "success", trace_id: "trace-1", redacted_summary: { status: "disabled" } })));
    await authClient.login("admin", "password");
    await expect(accountAdminClient.getAuditEvent("audit-1")).resolves.toEqual(expect.objectContaining({ auditId: "audit-1", scopeType: "platform", targetId: "user-1", redactedSummary: { status: "disabled" } }));
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => String(input).endsWith("/auth/login") ? json(session) : json({ audit_id: "audit-1", occurred_at: "bad", actor_id: "admin-1", permission: "identity.security.manage", scope_type: "platform", action: "action", target_type: "user", target_id: "user-1", result: "success", trace_id: "trace-1" })));
    await expect(accountAdminClient.getAuditEvent("audit-1")).rejects.toThrow("audit occurred at is invalid");
  });
});
