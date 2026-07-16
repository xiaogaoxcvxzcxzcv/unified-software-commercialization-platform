import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { adminClient } from "../api/adminClient";
import { authClient, resetAdminAuthStateForTests } from "../api/authClient";
import type { AdminSession } from "../types";

const session: AdminSession = {
  session_id: "session-client", session_version: 1, transport: "cookie",
  admin: { admin_user_id: "admin-1", display_name: "管理员", account_status: "active", auth_time: "2026-07-15T01:00:00Z", authentication_method: "password" },
  authorization: { authorization_version: 1, permissions: ["product.read", "tenant.manage"], scopes: [{ scope_type: "platform", scope_id: null, product_id: null, tenant_id: null }] },
  access_expires_at: "2026-07-15T02:00:00Z", refresh_expires_at: "2026-07-22T01:00:00Z", csrf_token: "csrf-admin-client-12345678901234567890",
};

const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } });

beforeEach(() => resetAdminAuthStateForTests());
afterEach(() => { resetAdminAuthStateForTests(); vi.unstubAllGlobals(); });

describe("adminClient 真实响应投影", () => {
  it("严格映射 Product、Application、Tenant 与公开 CapabilitySet 字段", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith("/auth/login")) return json(session);
      if (path === "/api/v1/admin/products/prod-1") return json({ product_id: "prod-1", code: "image-studio", name: "图片工作台", status: "active", provisioning_state: "ready", official_tenant_id: "tenant-1", context_version: 4, created_at: "2026-07-14T01:00:00Z", updated_at: "2026-07-15T01:00:00Z", audit_id: "audit-product" });
      if (path.endsWith("/capabilities")) return json({ product_id: "prod-1", capability_set: { product_id: "prod-1", version: 3, source_plan_id: "plan-1", catalog_revision: "catalog-7", catalog_snapshot_sha256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", audit_id: "audit-capability", capabilities: [{ capability_id: "account.login", enabled: true, source_package_id: "package.account", source_package_version: "1.0.0" }] } });
      if (path.endsWith("/applications")) return json({ items: [{ application_id: "app-1", product_id: "prod-1", application_code: "web", name: "用户前台", platform: "web", distribution_channel: "official", release_track: "stable", status: "active", context_version: 2, created_at: "2026-07-14T01:00:00Z", updated_at: "2026-07-15T01:00:00Z" }] });
      if (path.endsWith("/tenants")) return json({ items: [{ tenant_id: "tenant-1", product_id: "prod-1", tenant_code: "official", name: "官方直营", tenant_type: "official", status: "active", context_version: 2, created_at: "2026-07-14T01:00:00Z", updated_at: "2026-07-15T01:00:00Z" }] });
      throw new Error(`unexpected request: ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);
    await authClient.login("admin", "password");

    const product = await adminClient.getProduct("prod-1");
    const capabilities = await adminClient.getProductCapabilities("prod-1");
    const applications = await adminClient.listApplications("prod-1");
    const tenants = await adminClient.listTenants("prod-1");

    expect(product).toEqual(expect.objectContaining({ id: "prod-1", provisioningState: "ready", officialTenantId: "tenant-1", contextVersion: 4 }));
    expect(product).not.toHaveProperty("users");
    expect(capabilities.capabilitySet).toEqual(expect.objectContaining({ productId: "prod-1", version: 3, sourcePlanId: "plan-1", auditId: "audit-capability" }));
    expect(capabilities.capabilitySet).not.toHaveProperty("id");
    expect(capabilities.capabilitySet).not.toHaveProperty("createdAt");
    expect(capabilities.capabilitySet?.capabilities[0]).toEqual({ capabilityId: "account.login", enabled: true, sourcePackageId: "package.account", sourcePackageVersion: "1.0.0" });
    expect(applications[0]).toEqual(expect.objectContaining({ id: "app-1", releaseTrack: "stable", contextVersion: 2 }));
    expect(tenants[0]).toEqual(expect.objectContaining({ id: "tenant-1", type: "official", externalAgentRef: null, contextVersion: 2 }));
  });

  it("非平台管理员只按授权 Product scope 去重读取单项", async () => {
    const scopedSession: AdminSession = {
      ...session,
      authorization: { ...session.authorization, scopes: [
        { scope_type: "product", scope_id: "prod-2", product_id: "prod-2", tenant_id: null },
        { scope_type: "tenant", scope_id: "tenant-2", product_id: "prod-2", tenant_id: "tenant-2" },
        { scope_type: "product", scope_id: "prod-1", product_id: "prod-1", tenant_id: null },
      ] },
    };
    const requested: string[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path.endsWith("/auth/login")) return json(session);
      requested.push(path);
      const productId = path.endsWith("prod-1") ? "prod-1" : "prod-2";
      return json({ product_id: productId, code: productId, name: productId, status: "active", provisioning_state: "ready", official_tenant_id: `${productId}-tenant`, context_version: 1, created_at: "2026-07-14T01:00:00Z", updated_at: "2026-07-15T01:00:00Z", audit_id: `audit-${productId}` });
    });
    vi.stubGlobal("fetch", fetchMock);
    await authClient.login("admin", "password");

    const result = await adminClient.listAccessibleProducts(scopedSession);

    expect(result.map((item) => item.id)).toEqual(["prod-1", "prod-2"]);
    expect(requested).toEqual(["/api/v1/admin/products/prod-1", "/api/v1/admin/products/prod-2"]);
    expect(requested).not.toContain("/api/v1/admin/products");
  });
});
