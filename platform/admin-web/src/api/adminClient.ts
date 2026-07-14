import type {
  EntitlementRecord,
  Product,
  TenantRecord,
  UserRecord,
} from "../types";
import { authenticatedAdminRequest, getAdminCsrfToken } from "./authClient";

interface ProductSummaryDto {
  product_id: string;
  code: string;
  name: string;
  status: "active" | "suspended";
  provisioning_state: "pending" | "ready" | "failed";
  official_tenant_id?: string | null;
}

interface TenantSummaryDto {
  tenant_id: string;
  product_id: string;
  tenant_code: string;
  name: string;
  tenant_type: "official" | "agent";
  status: "active" | "suspended";
}

interface ProductCreateDto {
  product_id: string;
  code: string;
  name: string;
  status: "active" | "suspended";
}

const idempotencyKey = () => globalThis.crypto?.randomUUID?.() ?? `idem-${Date.now()}-${Math.random().toString(16).slice(2)}`;
const productAccent = (code: string) => {
  const palette = ["#0f9f8f", "#3b82f6", "#7c3aed", "#d97706"];
  const index = [...code].reduce((total, character) => total + character.charCodeAt(0), 0) % palette.length;
  return palette[index];
};
const mapProduct = (item: ProductSummaryDto | ProductCreateDto): Product => ({
  id: item.product_id,
  code: item.code,
  name: item.name,
  version: "未发布",
  status: item.status === "active" ? "active" : "paused",
  users: 0,
  activeUsers: 0,
  enabledCapabilities: ["代理租户"],
  accent: productAccent(item.code),
});
const mapTenant = (item: TenantSummaryDto): TenantRecord => ({
  id: item.tenant_id,
  productId: item.product_id,
  name: item.name,
  code: item.tenant_code,
  type: item.tenant_type,
  admins: 0,
  users: 0,
  status: item.status,
});

let products: Product[] = [
  {
    id: "prod-video",
    code: "VIDEO_BRAIN",
    name: "视频生产大脑",
    version: "v1.8.2",
    status: "active",
    users: 1286,
    activeUsers: 842,
    enabledCapabilities: ["统一账号", "权益", "设备", "激活码", "代理租户"],
    accent: "#0f9f8f",
  },
  {
    id: "prod-copy",
    code: "COPY_STUDIO",
    name: "智能文案工坊",
    version: "v0.9.6",
    status: "active",
    users: 526,
    activeUsers: 319,
    enabledCapabilities: ["统一账号", "权益", "代理租户"],
    accent: "#3b82f6",
  },
  {
    id: "prod-assets",
    code: "ASSET_DESK",
    name: "素材管理助手",
    version: "v2.1.0",
    status: "paused",
    users: 204,
    activeUsers: 0,
    enabledCapabilities: ["统一账号", "云存储"],
    accent: "#7c3aed",
  },
];

let users: UserRecord[] = [
  { id: "U-10028", productId: "prod-video", tenantId: "T-OFFICIAL", name: "林晓", account: "linxiao@example.com", plan: "专业年卡", status: "active", tenant: "官方直营", devices: 2, validUntil: "2027-06-30", lastActive: "3 分钟前" },
  { id: "U-10027", productId: "prod-video", tenantId: "T-SOUTH", name: "周启", account: "zhouqi@example.com", plan: "团队月卡", status: "trial", tenant: "华南代理", devices: 1, validUntil: "2026-07-20", lastActive: "18 分钟前" },
  { id: "U-10026", productId: "prod-video", tenantId: "T-OFFICIAL", name: "陈一", account: "chenyi@example.com", plan: "永久版", status: "active", tenant: "官方直营", devices: 3, validUntil: "永久", lastActive: "2 小时前" },
  { id: "U-10025", productId: "prod-video", tenantId: "T-EAST", name: "沈宁", account: "shenning@example.com", plan: "专业月卡", status: "expired", tenant: "华东代理", devices: 1, validUntil: "2026-07-01", lastActive: "4 天前" },
  { id: "U-20211", productId: "prod-copy", tenantId: "T-COPY", name: "夏川", account: "xiachuan@example.com", plan: "标准年卡", status: "active", tenant: "官方直营", devices: 1, validUntil: "2027-03-18", lastActive: "9 分钟前" },
  { id: "U-20210", productId: "prod-copy", tenantId: "T-WEST", name: "宋元", account: "songyuan@example.com", plan: "试用版", status: "trial", tenant: "西南代理", devices: 1, validUntil: "2026-07-16", lastActive: "1 小时前" },
];

let entitlements: EntitlementRecord[] = [
  { id: "E-39021", productId: "prod-video", tenantId: "T-OFFICIAL", userId: "U-10028", user: "林晓", plan: "专业年卡", source: "订单 ORD-8821", validUntil: "2027-06-30", status: "active" },
  { id: "E-39020", productId: "prod-video", tenantId: "T-SOUTH", userId: "U-10027", user: "周启", plan: "团队月卡", source: "试用活动", validUntil: "2026-07-20", status: "active" },
  { id: "E-39019", productId: "prod-video", tenantId: "T-OFFICIAL", userId: "U-10026", user: "陈一", plan: "永久版", source: "激活码 LC-2026", validUntil: "永久", status: "active" },
  { id: "E-39018", productId: "prod-video", tenantId: "T-EAST", userId: "U-10025", user: "沈宁", plan: "专业月卡", source: "订单 ORD-8710", validUntil: "2026-07-01", status: "expired" },
];

let tenants: TenantRecord[] = [
  { id: "T-OFFICIAL", productId: "prod-video", name: "官方直营", code: "official", type: "official", admins: 3, users: 1094, status: "active" },
  { id: "T-SOUTH", productId: "prod-video", name: "华南代理", code: "south-cn", type: "agent", admins: 2, users: 126, status: "active" },
  { id: "T-EAST", productId: "prod-video", name: "华东代理", code: "east-cn", type: "agent", admins: 1, users: 66, status: "active" },
  { id: "T-COPY", productId: "prod-copy", name: "官方直营", code: "official", type: "official", admins: 2, users: 458, status: "active" },
  { id: "T-WEST", productId: "prod-copy", name: "西南代理", code: "west-cn", type: "agent", admins: 1, users: 68, status: "active" },
  { id: "T-ASSETS", productId: "prod-assets", name: "官方直营", code: "official", type: "official", admins: 1, users: 204, status: "active" },
];

const wait = () => new Promise((resolve) => window.setTimeout(resolve, 120));
const requireCapability = (productId: string, capability: string) => {
  const product = products.find((item) => item.id === productId);
  if (!product) throw new Error("软件不存在或已被删除");
  if (!product.enabledCapabilities.includes(capability)) throw new Error(`当前软件未启用${capability}能力`);
};
const requireTenant = (productId: string, tenantId: string) => {
  const tenant = tenants.find((item) => item.id === tenantId && item.productId === productId);
  if (!tenant) throw new Error("租户不存在或不属于当前软件");
  return tenant;
};

export const adminClient = {
  mode: "api" as const,
  async listProducts() {
    const response = await authenticatedAdminRequest<{ items: ProductSummaryDto[] }>("/api/v1/admin/products");
    products = response.items.map(mapProduct);
    return [...products];
  },
  async listUsers(productId: string, tenantId: string) {
    await wait();
    requireCapability(productId, "统一账号");
    requireTenant(productId, tenantId);
    return users.filter((user) => user.productId === productId && user.tenantId === tenantId);
  },
  async listEntitlements(productId: string, tenantId: string) {
    await wait();
    requireCapability(productId, "权益");
    requireTenant(productId, tenantId);
    return entitlements.filter((item) => item.productId === productId && item.tenantId === tenantId);
  },
  async listTenants(productId: string) {
    const response = await authenticatedAdminRequest<{ items: TenantSummaryDto[] }>(`/api/v1/admin/products/${encodeURIComponent(productId)}/tenants`);
    const scoped = response.items.map(mapTenant);
    tenants = [...tenants.filter((tenant) => tenant.productId !== productId), ...scoped];
    return scoped;
  },
  async listAudits(productId: string, tenantId: string) {
    const response = await authenticatedAdminRequest<{ items: Array<{ audit_id: string; product_id?: string; tenant_id?: string; actor_id: string; action: string; target_type: string; target_id: string; result: "success" | "denied"; occurred_at: string }> }>("/api/v1/admin/audit/events?limit=100");
    return response.items
      .filter((item) => item.product_id === productId && (!item.tenant_id || item.tenant_id === tenantId))
      .map((item) => ({ id: item.audit_id, productId, tenantId: item.tenant_id ?? tenantId, actor: item.actor_id, action: item.action, target: `${item.target_type} / ${item.target_id}`, result: item.result, createdAt: new Date(item.occurred_at).toLocaleString("zh-CN") }));
  },
  async createProduct(input: Pick<Product, "name" | "code">) {
    const result = await authenticatedAdminRequest<ProductCreateDto>("/api/v1/admin/products", {
      method: "POST",
      headers: { "Idempotency-Key": idempotencyKey() },
      body: JSON.stringify({ code: input.code.toLowerCase(), name: input.name, status: "active" }),
    }, getAdminCsrfToken());
    const product = mapProduct(result);
    products = [...products.filter((item) => item.id !== product.id), product];
    return product;
  },
  async updateProduct(productId: string, input: Pick<Product, "name" | "version">): Promise<Product> {
    void productId;
    void input;
    throw new Error("产品资料修改接口尚未进入当前可用能力，请先完成对应公共能力变更");
  },
  async updateCapabilities(productId: string, enabledCapabilities: string[]): Promise<Product> {
    void productId;
    void enabledCapabilities;
    throw new Error("能力只能由可信装配计划启用，当前没有可用的生产能力包");
  },
  async grantEntitlement(productId: string, tenantId: string, userId: string, plan: string) {
    await wait();
    requireCapability(productId, "权益");
    requireTenant(productId, tenantId);
    const targetUser = users.find((item) => item.id === userId && item.productId === productId && item.tenantId === tenantId);
    if (!targetUser) throw new Error("目标用户不属于当前软件和租户");
    const entitlement: EntitlementRecord = {
      id: `E-${Date.now()}`,
      productId,
      tenantId,
      userId,
      user: targetUser.name,
      plan,
      source: "管理员手工授予",
      validUntil: "2027-07-13",
      status: "active",
    };
    entitlements = [entitlement, ...entitlements];
    users = users.map((item) => item.id === userId ? { ...item, plan, status: "active" } : item);
    return entitlement;
  },
  async createUser(productId: string, tenantId: string, name: string, account: string) {
    await wait();
    requireCapability(productId, "统一账号");
    const targetTenant = requireTenant(productId, tenantId);
    if (users.some((item) => item.account === account && item.productId === productId)) throw new Error("该登录账号已存在");
    const user: UserRecord = {
      id: `U-${Date.now()}`,
      productId,
      tenantId,
      name,
      account,
      plan: "未开通",
      status: "trial",
      tenant: targetTenant.name,
      devices: 0,
      validUntil: "未设置",
      lastActive: "尚未登录",
    };
    users = [user, ...users];
    return user;
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
};
