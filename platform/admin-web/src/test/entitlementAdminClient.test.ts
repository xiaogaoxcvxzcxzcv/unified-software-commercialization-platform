import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { entitlementAdminClient } from "../api/entitlementAdminClient";
import { authClient, resetAdminAuthStateForTests } from "../api/authClient";
import type { AdminSession } from "../types";

const session: AdminSession = {
  session_id: "admin-session", session_version: 1, transport: "cookie",
  admin: { admin_user_id: "admin-1", display_name: "平台管理员", account_status: "active", auth_time: "2026-07-23T01:00:00Z", authentication_method: "password" },
  authorization: { authorization_version: 1, permissions: ["entitlement.read", "entitlement.manage", "entitlement.revoke"], scopes: [{ scope_type: "platform", scope_id: null, product_id: null, tenant_id: null }] },
  access_expires_at: "2026-07-23T02:00:00Z", refresh_expires_at: "2026-07-30T01:00:00Z", csrf_token: "csrf-entitlement-admin-1234567890",
};
const decision = { allowed: true, decision_stage: "entitlement", revision: 2, reason_code: null, plan_code: "pro", features: { "pro.member": true }, valid_until: null, offline_grace_until: null, server_time: "2026-07-23T01:00:00Z" };
const summary = { product_id: "product-1", tenant_id: "tenant-1", user_id: "user-1", revision: 2, plan_code: "pro", features: { "pro.member": true }, valid_until: null, offline_grace_until: null, updated_at: "2026-07-23T01:00:00Z" };
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json", "X-Request-Id": "request-1" } });

beforeEach(() => resetAdminAuthStateForTests());
afterEach(() => { resetAdminAuthStateForTests(); vi.unstubAllGlobals(); });

describe("entitlementAdminClient", () => {
  it("列表和历史使用管理范围查询且严格投影", async () => {
    const requested: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input); if (path.endsWith("/auth/login")) return json(session); requested.push(path);
      return path.includes("/history") ? json({ items: [{ ledger_id: "ledger-1", operation_type: "grant", operation_id: "grant-1", grant_id: "grant-1", before_revision: 0, after_revision: 1, audit_id: "audit-1", trace_id: "trace-1", created_at: "2026-07-23T01:00:00Z" }], next_cursor: null }) : json({ items: [summary], next_cursor: null });
    }));
    await authClient.login("admin", "password");
    expect((await entitlementAdminClient.listCurrent({ productId: "product:1", tenantId: "tenant:1" }, { userId: "user:1", pageSize: 20 })).items[0].planCode).toBe("pro");
    expect((await entitlementAdminClient.listHistory({ productId: "product:1", tenantId: "tenant:1" }, "user:1")).items[0].auditId).toBe("audit-1");
    expect(requested).toEqual([
      "/api/v1/admin/entitlements?product_id=product%3A1&tenant_id=tenant%3A1&user_id=user%3A1&page_size=20",
      "/api/v1/admin/entitlements/history?product_id=product%3A1&tenant_id=tenant%3A1&user_id=user%3A1",
    ]);
  });

  it("写操作携带幂等键、CSRF、expected_revision 和审计编号", async () => {
    const requests: RequestInit[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input); if (path.endsWith("/auth/login")) return json(session); requests.push(init ?? {});
      return json({ entitlement_id: "entitlement-1", grant_id: "grant-1", revision: 3, valid_until: null, audit_id: "audit-1", decision });
    }));
    await authClient.login("admin", "password");
    const scope = { productId: "product-1", tenantId: "tenant-1" };
    await entitlementAdminClient.grant(scope, { userId: "user-1", policyId: "policy-1", policyVersion: 1, validity: { rule: "fixed_duration", durationSeconds: 3600 }, source: { sourceType: "admin", sourceId: "manual-1", sourceEffectId: "effect-1" }, reasonCode: "manual_grant" }, { idempotencyKey: "grant-intent-0001" });
    await entitlementAdminClient.extend(scope, "grant-1", { userId: "user-1", expectedRevision: 2, policyId: "policy-1", policyVersion: 1, validity: { rule: "lifetime" }, source: { sourceType: "admin", sourceId: "extend-1", sourceEffectId: "effect-1" }, reasonCode: "manual_extend" }, { idempotencyKey: "extend-intent-0001" });
    await entitlementAdminClient.revoke(scope, "grant-1", { userId: "user-1", expectedRevision: 3, reasonCode: "manual_revoke" }, { idempotencyKey: "revoke-intent-0001" });
    expect(new Headers(requests[0].headers).get("Idempotency-Key")).toBe("grant-intent-0001");
    expect(new Headers(requests[0].headers).get("X-CSRF-Token")).toBe(session.csrf_token);
    expect(requests[1].body).toContain('"expected_revision":2');
    expect(requests[2].body).toContain('"reason_code":"manual_revoke"');
  });
});
