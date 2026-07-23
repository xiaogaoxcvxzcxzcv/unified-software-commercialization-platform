import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { entitlementAdminClient, type EntitlementSummary } from "../api/entitlementAdminClient";
import { EntitlementsPage } from "../pages/EntitlementsPage";

const mocks = vi.hoisted(() => ({ app: {} as Record<string, unknown>, auth: {} as Record<string, unknown> }));
vi.mock("../app/AppContext", () => ({ useAppContext: () => mocks.app }));
vi.mock("../app/AuthContext", () => ({ useAuth: () => mocks.auth }));

const product = { id: "product-1", name: "图片工作台", code: "image-studio", status: "active", provisioningState: "ready", officialTenantId: "tenant-1", contextVersion: 1, createdAt: "2026-07-17T01:00:00Z", updatedAt: "2026-07-18T01:00:00Z", auditId: "audit-product" };
const tenant = { id: "tenant-1", productId: "product-1", name: "官方直营", code: "official", type: "official", status: "active", externalAgentRef: null, contextVersion: 1, createdAt: "2026-07-17T01:00:00Z", updatedAt: "2026-07-18T01:00:00Z" };
const summary: EntitlementSummary = { productId: "product-1", tenantId: "tenant-1", userId: "user-1", revision: 2, planCode: "pro", features: { "pro.member": true }, validUntil: null, offlineGraceUntil: null, updatedAt: "2026-07-23T01:00:00Z" };
const decision = { allowed: true, revision: 3, planCode: "pro", features: { "pro.member": true }, validUntil: null, offlineGraceUntil: null, serverTime: "2026-07-23T01:00:00Z" };

function resetContext(enabled = true, permissions = ["entitlement.read", "entitlement.manage", "entitlement.revoke"]) {
  mocks.app = { currentProduct: product, currentTenant: tenant, enabledPackageIds: new Set(enabled ? ["package.entitlement"] : []), tenants: [tenant], tenantsLoading: false, tenantsError: null, products: [product], selectTenant: vi.fn(), refreshProducts: vi.fn(async () => {}), refreshWorkspace: vi.fn(async () => {}) };
  mocks.auth = { session: { authorization: { permissions, scopes: [{ scope_type: "platform", scope_id: null, product_id: null, tenant_id: null }] }, admin: { display_name: "管理员" } }, logout: vi.fn(async () => {}) };
}
function renderPage() { return render(<MemoryRouter initialEntries={["/products/product-1/entitlements"]}><EntitlementsPage/></MemoryRouter>); }

beforeEach(() => { vi.restoreAllMocks(); resetContext(); });

describe("entitlement admin blocks", () => {
  it("能力未启用时失败关闭且不请求权益 API", async () => {
    resetContext(false);
    const list = vi.spyOn(entitlementAdminClient, "listCurrent");
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("未启用权益能力");
    expect(list).not.toHaveBeenCalled();
  });

  it("用户筛选重新请求服务端并展示流水 grant_id 与审计编号", async () => {
    const list = vi.spyOn(entitlementAdminClient, "listCurrent").mockResolvedValue({ items: [summary], nextCursor: null });
    vi.spyOn(entitlementAdminClient, "listHistory").mockResolvedValue({ items: [{ ledgerId: "ledger-1", operationType: "grant", operationId: "grant-1", grantId: "grant-1", beforeRevision: 0, afterRevision: 2, auditId: "audit-1", traceId: "trace-1", sourceType: "admin", sourceId: "manual-1", createdAt: "2026-07-23T01:00:00Z" }], nextCursor: null });
    const user = userEvent.setup(); renderPage();
    expect(await screen.findByText("pro")).toBeInTheDocument();
    await user.type(screen.getByLabelText("筛选用户 ID"), "user-1");
    await user.click(screen.getByRole("button", { name: "查询" }));
    await waitFor(() => expect(list).toHaveBeenLastCalledWith({ productId: "product-1", tenantId: "tenant-1" }, expect.objectContaining({ userId: "user-1" })));
    await user.click(screen.getByRole("button", { name: /流水/ }));
    expect(await screen.findByText("grant-1")).toBeInTheDocument();
    expect(screen.getByText("audit-1")).toBeInTheDocument();
  });

  it("授予写操作使用幂等输入并显示审计结果", async () => {
    vi.spyOn(entitlementAdminClient, "listCurrent").mockResolvedValue({ items: [], nextCursor: null });
    vi.spyOn(entitlementAdminClient, "listHistory").mockResolvedValue({ items: [], nextCursor: null });
    const grant = vi.spyOn(entitlementAdminClient, "grant").mockResolvedValue({ entitlementId: "entitlement-1", grantId: "grant-1", revision: 1, validUntil: null, auditId: "audit-1", decision });
    const user = userEvent.setup(); renderPage();
    await screen.findByText("当前范围没有权益记录");
    await user.type(screen.getByLabelText("筛选用户 ID"), "user-1");
    await user.click(screen.getByRole("button", { name: "授予权益" }));
    await user.click(within(screen.getByRole("dialog", { name: "授予权益" })).getByRole("button", { name: "确认提交" }));
    await waitFor(() => expect(grant).toHaveBeenCalledWith({ productId: "product-1", tenantId: "tenant-1" }, expect.objectContaining({ userId: "user-1", policyId: "policy-pro" }), expect.objectContaining({ idempotencyKey: expect.any(String) })));
    expect(await screen.findByText("权益授予成功")).toBeInTheDocument();
    expect(screen.getByText("audit-1")).toBeInTheDocument();
  });

  it("撤销要求二次确认并提交 expected revision", async () => {
    vi.spyOn(entitlementAdminClient, "listCurrent").mockResolvedValue({ items: [summary], nextCursor: null });
    vi.spyOn(entitlementAdminClient, "listHistory").mockResolvedValue({ items: [{ ledgerId: "ledger-1", operationType: "grant", operationId: "grant-1", grantId: "grant-1", beforeRevision: 0, afterRevision: 2, auditId: "audit-1", traceId: "trace-1", sourceType: "admin", sourceId: "manual-1", createdAt: "2026-07-23T01:00:00Z" }], nextCursor: null });
    const revoke = vi.spyOn(entitlementAdminClient, "revoke").mockResolvedValue({ entitlementId: "entitlement-1", grantId: "grant-revoke", revision: 3, validUntil: null, auditId: "audit-revoke", decision });
    const user = userEvent.setup(); renderPage();
    await screen.findByText("pro");
    await user.click(screen.getByRole("button", { name: /撤销/ }));
    await user.click(within(screen.getByRole("dialog", { name: "撤销权益" })).getByRole("button", { name: "确认提交" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("二次确认");
    await user.click(within(screen.getByRole("dialog", { name: "撤销权益" })).getByRole("checkbox"));
    await user.click(within(screen.getByRole("dialog", { name: "撤销权益" })).getByRole("button", { name: "确认提交" }));
    await waitFor(() => expect(revoke).toHaveBeenCalledWith({ productId: "product-1", tenantId: "tenant-1" }, "grant-1", expect.objectContaining({ userId: "user-1", expectedRevision: 2 }), expect.anything()));
  });
});
