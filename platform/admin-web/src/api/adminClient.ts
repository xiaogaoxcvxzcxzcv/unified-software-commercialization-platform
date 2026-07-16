import type {
  AdminSession,
  EntitlementRecord,
  Product,
  ProductApplication,
  ProductCapabilityProjection,
  TenantRecord,
  UserRecord,
} from "../types";
import { authenticatedAdminRequest, getAdminCsrfToken } from "./authClient";

interface ProductDto {
  product_id: string;
  code: string;
  name: string;
  status: "active" | "suspended";
  provisioning_state: "pending" | "ready" | "failed";
  official_tenant_id?: string | null;
  context_version: number;
  created_at: string;
  updated_at: string;
  audit_id?: string | null;
}

interface TenantDto {
  tenant_id: string;
  product_id: string;
  tenant_code: string;
  name: string;
  tenant_type: "official" | "agent";
  status: "active" | "suspended";
  external_agent_ref?: string | null;
  context_version: number;
  created_at: string;
  updated_at: string;
}

interface ProductApplicationDto {
  application_id: string;
  product_id: string;
  application_code: string;
  name: string;
  platform: string;
  distribution_channel: string;
  release_track: string;
  status: "active" | "suspended";
  context_version: number;
  created_at: string;
  updated_at: string;
}

interface ProductCapabilityProjectionDto {
  product_id: string;
  capability_set: null | {
    product_id: string;
    version: number;
    source_plan_id: string;
    catalog_revision: string;
    catalog_snapshot_sha256: string;
    audit_id: string;
    capabilities: Array<{
      capability_id: string;
      enabled: boolean;
      source_package_id?: string | null;
      source_package_version?: string | null;
    }>;
  };
}

const idempotencyKey = () => globalThis.crypto?.randomUUID?.() ?? `idem-${Date.now()}-${Math.random().toString(16).slice(2)}`;
const mapProduct = (item: ProductDto): Product => ({
  id: item.product_id,
  code: item.code,
  name: item.name,
  status: item.status,
  provisioningState: item.provisioning_state,
  officialTenantId: item.official_tenant_id ?? null,
  contextVersion: item.context_version,
  createdAt: item.created_at,
  updatedAt: item.updated_at,
  auditId: item.audit_id ?? null,
});
const mapTenant = (item: TenantDto): TenantRecord => ({
  id: item.tenant_id,
  productId: item.product_id,
  name: item.name,
  code: item.tenant_code,
  type: item.tenant_type,
  status: item.status,
  externalAgentRef: item.external_agent_ref ?? null,
  contextVersion: item.context_version,
  createdAt: item.created_at,
  updatedAt: item.updated_at,
});
const mapApplication = (item: ProductApplicationDto): ProductApplication => ({
  id: item.application_id,
  productId: item.product_id,
  code: item.application_code,
  name: item.name,
  platform: item.platform,
  distributionChannel: item.distribution_channel,
  releaseTrack: item.release_track,
  status: item.status,
  contextVersion: item.context_version,
  createdAt: item.created_at,
  updatedAt: item.updated_at,
});

function productScopeIds(session: AdminSession) {
  return [...new Set(session.authorization.scopes.flatMap((scope) => {
    const productId = scope.product_id ?? (scope.scope_type === "product" ? scope.scope_id : null);
    return productId ? [productId] : [];
  }))].sort();
}

export const adminClient = {
  mode: "api" as const,
  async listProducts() {
    const response = await authenticatedAdminRequest<{ items: ProductDto[] }>("/api/v1/admin/products");
    return response.items.map(mapProduct);
  },
  async getProduct(productId: string) {
    return mapProduct(await authenticatedAdminRequest<ProductDto>(`/api/v1/admin/products/${encodeURIComponent(productId)}`));
  },
  async listAccessibleProducts(session: AdminSession) {
    if (session.authorization.scopes.some((scope) => scope.scope_type === "platform")) return adminClient.listProducts();
    return Promise.all(productScopeIds(session).map((productId) => adminClient.getProduct(productId)));
  },
  async getProductCapabilities(productId: string): Promise<ProductCapabilityProjection> {
    const response = await authenticatedAdminRequest<ProductCapabilityProjectionDto>(`/api/v1/admin/products/${encodeURIComponent(productId)}/capabilities`);
    return {
      productId: response.product_id,
      capabilitySet: response.capability_set ? {
        productId: response.capability_set.product_id,
        version: response.capability_set.version,
        sourcePlanId: response.capability_set.source_plan_id,
        catalogRevision: response.capability_set.catalog_revision,
        catalogSnapshotSha256: response.capability_set.catalog_snapshot_sha256,
        auditId: response.capability_set.audit_id,
        capabilities: response.capability_set.capabilities.map((item) => ({
          capabilityId: item.capability_id,
          enabled: item.enabled,
          sourcePackageId: item.source_package_id ?? null,
          sourcePackageVersion: item.source_package_version ?? null,
        })),
      } : null,
    };
  },
  async listApplications(productId: string) {
    const response = await authenticatedAdminRequest<{ items: ProductApplicationDto[] }>(`/api/v1/admin/products/${encodeURIComponent(productId)}/applications`);
    return response.items.map(mapApplication);
  },
  async listTenants(productId: string) {
    const response = await authenticatedAdminRequest<{ items: TenantDto[] }>(`/api/v1/admin/products/${encodeURIComponent(productId)}/tenants`);
    return response.items.map(mapTenant);
  },
  async listAudits(productId: string, tenantId: string) {
    const response = await authenticatedAdminRequest<{ items: Array<{ audit_id: string; product_id?: string; tenant_id?: string; actor_id: string; action: string; target_type: string; target_id: string; result: "success" | "denied"; occurred_at: string }> }>("/api/v1/admin/audit/events?limit=100");
    return response.items
      .filter((item) => item.product_id === productId && (!item.tenant_id || item.tenant_id === tenantId))
      .map((item) => ({ id: item.audit_id, productId, tenantId: item.tenant_id ?? tenantId, actor: item.actor_id, action: item.action, target: `${item.target_type} / ${item.target_id}`, result: item.result, createdAt: new Date(item.occurred_at).toLocaleString("zh-CN") }));
  },
  async createProduct(input: Pick<Product, "name" | "code">) {
    const result = await authenticatedAdminRequest<ProductDto>("/api/v1/admin/products", {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey() },
      body: JSON.stringify({ code: input.code.toLowerCase(), name: input.name, status: "active" }),
    }, getAdminCsrfToken());
    return mapProduct(result);
  },
  async createTenant(productId: string, name: string, code: string) {
    await authenticatedAdminRequest(`/api/v1/admin/products/${encodeURIComponent(productId)}/tenants`, {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey() },
      body: JSON.stringify({ name, tenant_code: code.toLowerCase(), status: "active" }),
    }, getAdminCsrfToken());
    const refreshed = await adminClient.listTenants(productId);
    const tenant = refreshed.find((item) => item.code === code.toLowerCase());
    if (!tenant) throw new Error("租户已创建，但刷新列表时未找到结果");
    return tenant;
  },
  async listUsers(_productId: string, _tenantId: string): Promise<UserRecord[]> { throw new Error("package.account 管理页面尚未交付"); },
  async listEntitlements(_productId: string, _tenantId: string): Promise<EntitlementRecord[]> { throw new Error("package.entitlement 管理页面尚未交付"); },
  async grantEntitlement(_productId: string, _tenantId: string, _userId: string, _plan: string): Promise<EntitlementRecord> { throw new Error("package.entitlement 管理页面尚未交付"); },
  async createUser(_productId: string, _tenantId: string, _name: string, _account: string): Promise<UserRecord> { throw new Error("package.account 管理页面尚未交付"); },
};
