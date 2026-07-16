export type ProductStatus = "active" | "suspended";
export type ProvisioningState = "pending" | "ready" | "failed";
export type TenantType = "official" | "agent";

export interface AdminScope {
  scope_type: "platform" | "product" | "tenant";
  scope_id?: string | null;
  product_id?: string | null;
  tenant_id?: string | null;
}

export interface AdminAuthorizationSnapshot {
  authorization_version: number;
  permissions: string[];
  scopes: AdminScope[];
  reauthentication_required?: boolean;
}

export interface AdminIdentitySummary {
  admin_user_id: string;
  display_name: string;
  account_status: "active" | "locked" | "disabled";
  auth_time: string;
  authentication_method?: "password" | "oidc" | "recovery";
}

export interface AdminSession {
  session_id: string;
  session_version: number;
  transport: "cookie" | "bearer";
  admin: AdminIdentitySummary;
  authorization: AdminAuthorizationSnapshot;
  access_expires_at: string;
  refresh_expires_at: string;
  csrf_token: string | null;
}

export interface ApiErrorEnvelope {
  type: string;
  title: string;
  status: number;
  code: string;
  detail?: string;
  request_id: string;
  retryable: boolean;
  retry_after_seconds?: number;
  field_errors?: Array<{ field: string; code: string; message?: string }>;
}

export interface Product {
  id: string;
  code: string;
  name: string;
  status: ProductStatus;
  provisioningState: ProvisioningState;
  officialTenantId: string | null;
  contextVersion: number;
  createdAt: string;
  updatedAt: string;
  auditId: string | null;
}

export interface ProductApplication {
  id: string;
  productId: string;
  code: string;
  name: string;
  platform: string;
  distributionChannel: string;
  releaseTrack: string;
  status: "active" | "suspended";
  contextVersion: number;
  createdAt: string;
  updatedAt: string;
}

export interface ProductCapabilityItem {
  capabilityId: string;
  enabled: boolean;
  sourcePackageId: string | null;
  sourcePackageVersion: string | null;
}

export interface ProductCapabilitySet {
  productId: string;
  version: number;
  sourcePlanId: string;
  catalogRevision: string;
  catalogSnapshotSha256: string;
  auditId: string;
  capabilities: ProductCapabilityItem[];
}

export interface ProductCapabilityProjection {
  productId: string;
  capabilitySet: ProductCapabilitySet | null;
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
  status: "active" | "suspended";
  externalAgentRef: string | null;
  contextVersion: number;
  createdAt: string;
  updatedAt: string;
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
