export type ProductStatus = "active" | "paused";
export type TenantType = "official" | "agent";

export interface Product {
  id: string;
  code: string;
  name: string;
  version: string;
  status: ProductStatus;
  users: number;
  activeUsers: number;
  enabledCapabilities: string[];
  accent: string;
}

export interface UserRecord {
  id: string;
  productId: string;
  name: string;
  account: string;
  plan: string;
  status: "active" | "trial" | "expired" | "locked";
  tenantId: string;
  tenant: string;
  devices: number;
  validUntil: string;
  lastActive: string;
}

export interface EntitlementRecord {
  id: string;
  productId: string;
  tenantId: string;
  userId: string;
  user: string;
  plan: string;
  source: string;
  validUntil: string;
  status: "active" | "expired" | "revoked";
}

export interface TenantRecord {
  id: string;
  productId: string;
  name: string;
  code: string;
  type: TenantType;
  admins: number;
  users: number;
  status: "active" | "suspended";
}

export interface AuditRecord {
  id: string;
  productId: string;
  tenantId: string;
  actor: string;
  action: string;
  target: string;
  result: "success" | "denied";
  createdAt: string;
}
