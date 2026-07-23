import { AuthApiError, authenticatedAdminRequest, getAdminCsrfToken } from "./authClient";

export interface EntitlementScope { productId: string; tenantId: string }
export interface CursorPage<T> { items: T[]; nextCursor: string | null }
export interface EntitlementDecision {
  allowed: boolean;
  revision: number;
  planCode: string | null;
  features: Record<string, unknown>;
  validUntil: string | null;
  offlineGraceUntil: string | null;
  serverTime: string;
}
export interface EntitlementSummary {
  productId: string;
  tenantId: string;
  userId: string;
  revision: number;
  planCode: string | null;
  features: Record<string, unknown>;
  validUntil: string | null;
  offlineGraceUntil: string | null;
  updatedAt: string;
}
export interface EntitlementLedgerEntry {
  ledgerId: string;
  operationType: "grant" | "extend" | "replace" | "revoke" | "expire";
  operationId: string;
  sourceType: string | null;
  sourceId: string | null;
  grantId: string;
  beforeRevision: number;
  afterRevision: number;
  auditId: string;
  traceId: string;
  createdAt: string;
}
export interface EntitlementGrantResult {
  entitlementId: string;
  grantId: string;
  revision: number;
  validUntil: string | null;
  auditId: string;
  decision: EntitlementDecision;
}
export interface EntitlementValidityInput { rule: "fixed_duration" | "fixed_end" | "lifetime"; durationSeconds?: number; fixedUntil?: string }
export interface EntitlementSourceInput { sourceType: "admin" | "trial" | "gift" | "order" | "license"; sourceId: string; sourceEffectId: string }
interface RequestOptions { idempotencyKey: string }

const identifierPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,159}$/;
function exact(value: unknown, required: string[], optional: string[], label: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new TypeError(`${label} is invalid`);
  const source = value as Record<string, unknown>;
  const allowed = new Set([...required, ...optional]);
  if (required.some((key) => !(key in source)) || Object.keys(source).some((key) => !allowed.has(key))) throw new TypeError(`${label} shape is invalid`);
  return source;
}
function text(value: unknown, label: string, maxLength = 320) {
  if (typeof value !== "string" || value.length < 1 || value.length > maxLength) throw new TypeError(`${label} is invalid`);
  return value;
}
function id(value: unknown, label: string) {
  const result = text(value, label, 160);
  if (!identifierPattern.test(result)) throw new TypeError(`${label} is invalid`);
  return result;
}
function integer(value: unknown, label: string, minimum = 0) {
  if (!Number.isInteger(value) || (value as number) < minimum) throw new TypeError(`${label} is invalid`);
  return value as number;
}
function nullableText(value: unknown, label: string, maxLength = 512) { return value === null ? null : text(value, label, maxLength); }
function timestamp(value: unknown, label: string) {
  const result = text(value, label, 64);
  if (Number.isNaN(Date.parse(result))) throw new TypeError(`${label} is invalid`);
  return result;
}
function nullableTimestamp(value: unknown, label: string) { return value === null ? null : timestamp(value, label); }
function featureMap(value: unknown) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new TypeError("entitlement features are invalid");
  return value as Record<string, unknown>;
}
function decision(value: unknown): EntitlementDecision {
  const source = exact(value, ["allowed", "decision_stage", "revision", "features", "server_time"], ["reason_code", "plan_code", "valid_until", "offline_grace_until", "signed_decision"], "entitlement decision");
  if (typeof source.allowed !== "boolean" || source.decision_stage !== "entitlement") throw new TypeError("entitlement decision is invalid");
  return { allowed: source.allowed, revision: integer(source.revision, "decision revision"), planCode: source.plan_code === undefined ? null : nullableText(source.plan_code, "plan code"), features: featureMap(source.features), validUntil: source.valid_until === undefined ? null : nullableTimestamp(source.valid_until, "valid until"), offlineGraceUntil: source.offline_grace_until === undefined ? null : nullableTimestamp(source.offline_grace_until, "offline grace until"), serverTime: timestamp(source.server_time, "server time") };
}
function summary(value: unknown): EntitlementSummary {
  const source = exact(value, ["product_id", "tenant_id", "user_id", "revision", "features", "updated_at"], ["plan_code", "valid_until", "offline_grace_until"], "entitlement summary");
  return { productId: id(source.product_id, "product id"), tenantId: id(source.tenant_id, "tenant id"), userId: id(source.user_id, "user id"), revision: integer(source.revision, "revision"), planCode: source.plan_code === undefined ? null : nullableText(source.plan_code, "plan code"), features: featureMap(source.features), validUntil: source.valid_until === undefined ? null : nullableTimestamp(source.valid_until, "valid until"), offlineGraceUntil: source.offline_grace_until === undefined ? null : nullableTimestamp(source.offline_grace_until, "offline grace until"), updatedAt: timestamp(source.updated_at, "updated at") };
}
function summaryPage(value: unknown): CursorPage<EntitlementSummary> {
  const source = exact(value, ["items"], ["next_cursor"], "entitlement summary page");
  if (!Array.isArray(source.items)) throw new TypeError("entitlement summary page is invalid");
  return { items: source.items.map(summary), nextCursor: source.next_cursor === undefined ? null : nullableText(source.next_cursor, "next cursor") };
}
function ledger(value: unknown): EntitlementLedgerEntry {
  const source = exact(value, ["ledger_id", "operation_type", "operation_id", "grant_id", "before_revision", "after_revision", "audit_id", "trace_id", "created_at"], ["source_type", "source_id"], "entitlement ledger entry");
  if (!["grant", "extend", "replace", "revoke", "expire"].includes(String(source.operation_type))) throw new TypeError("entitlement ledger operation is invalid");
  return { ledgerId: id(source.ledger_id, "ledger id"), operationType: source.operation_type as EntitlementLedgerEntry["operationType"], operationId: id(source.operation_id, "operation id"), sourceType: source.source_type === undefined ? null : nullableText(source.source_type, "source type"), sourceId: source.source_id === undefined ? null : nullableText(source.source_id, "source id"), grantId: id(source.grant_id, "grant id"), beforeRevision: integer(source.before_revision, "before revision"), afterRevision: integer(source.after_revision, "after revision"), auditId: id(source.audit_id, "audit id"), traceId: id(source.trace_id, "trace id"), createdAt: timestamp(source.created_at, "ledger created at") };
}
function historyPage(value: unknown): CursorPage<EntitlementLedgerEntry> {
  const source = exact(value, ["items"], ["next_cursor"], "entitlement history page");
  if (!Array.isArray(source.items)) throw new TypeError("entitlement history page is invalid");
  return { items: source.items.map(ledger), nextCursor: source.next_cursor === undefined ? null : nullableText(source.next_cursor, "next cursor") };
}
function grantResult(value: unknown): EntitlementGrantResult {
  const source = exact(value, ["entitlement_id", "grant_id", "revision", "audit_id", "decision"], ["valid_until"], "entitlement grant result");
  return { entitlementId: id(source.entitlement_id, "entitlement id"), grantId: id(source.grant_id, "grant id"), revision: integer(source.revision, "grant revision", 1), validUntil: source.valid_until === undefined ? null : nullableTimestamp(source.valid_until, "grant valid until"), auditId: id(source.audit_id, "audit id"), decision: decision(source.decision) };
}
function query(params: Record<string, string | number | undefined>) {
  const values = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) if (value !== undefined && value !== "") values.set(key, String(value));
  const encoded = values.toString();
  return encoded ? `?${encoded}` : "";
}
function bodyScope(scope: EntitlementScope) { return { product_id: scope.productId, tenant_id: scope.tenantId }; }
function sourceBody(source: EntitlementSourceInput) { return { source_type: source.sourceType, source_id: source.sourceId, source_effect_id: source.sourceEffectId }; }
function validityBody(validity: EntitlementValidityInput) { return { rule: validity.rule, duration_seconds: validity.durationSeconds, fixed_until: validity.fixedUntil }; }
function writeHeaders(options: RequestOptions) { return { "Idempotency-Key": options.idempotencyKey }; }

export function createEntitlementIntentKey() {
  return globalThis.crypto?.randomUUID?.() ?? `entitlement-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export const entitlementAdminClient = {
  async listCurrent(scope: EntitlementScope, options: { userId?: string; cursor?: string; pageSize?: number } = {}) {
    return summaryPage(await authenticatedAdminRequest(`/api/v1/admin/entitlements${query({ product_id: scope.productId, tenant_id: scope.tenantId, user_id: options.userId?.trim(), cursor: options.cursor, page_size: options.pageSize })}`));
  },
  async listHistory(scope: EntitlementScope, userId: string, options: { cursor?: string; pageSize?: number } = {}) {
    return historyPage(await authenticatedAdminRequest(`/api/v1/admin/entitlements/history${query({ product_id: scope.productId, tenant_id: scope.tenantId, user_id: userId, cursor: options.cursor, page_size: options.pageSize })}`));
  },
  async grant(scope: EntitlementScope, input: { userId: string; policyId: string; policyVersion: number; validity: EntitlementValidityInput; source: EntitlementSourceInput; reasonCode: string }, options: RequestOptions) {
    return grantResult(await authenticatedAdminRequest("/api/v1/admin/entitlements", { method: "POST", headers: writeHeaders(options), body: JSON.stringify({ user_id: input.userId, ...bodyScope(scope), policy_id: input.policyId, policy_version: input.policyVersion, validity: validityBody(input.validity), source: sourceBody(input.source), reason_code: input.reasonCode }) }, getAdminCsrfToken()));
  },
  async extend(scope: EntitlementScope, grantId: string, input: { userId: string; expectedRevision: number; policyId: string; policyVersion: number; validity: EntitlementValidityInput; source: EntitlementSourceInput; reasonCode: string }, options: RequestOptions) {
    return grantResult(await authenticatedAdminRequest(`/api/v1/admin/entitlements/${encodeURIComponent(grantId)}/extend`, { method: "POST", headers: writeHeaders(options), body: JSON.stringify({ user_id: input.userId, ...bodyScope(scope), expected_revision: input.expectedRevision, policy_id: input.policyId, policy_version: input.policyVersion, validity: validityBody(input.validity), source: sourceBody(input.source), reason_code: input.reasonCode }) }, getAdminCsrfToken()));
  },
  async revoke(scope: EntitlementScope, grantId: string, input: { userId: string; expectedRevision: number; reasonCode: string; source?: EntitlementSourceInput }, options: RequestOptions) {
    return grantResult(await authenticatedAdminRequest(`/api/v1/admin/entitlements/${encodeURIComponent(grantId)}/revoke`, { method: "POST", headers: writeHeaders(options), body: JSON.stringify({ user_id: input.userId, ...bodyScope(scope), expected_revision: input.expectedRevision, source: input.source ? sourceBody(input.source) : undefined, reason_code: input.reasonCode }) }, getAdminCsrfToken()));
  },
};

export function entitlementErrorMessage(reason: unknown, fallback: string) {
  if (!(reason instanceof AuthApiError)) return fallback;
  if (reason.code === "admin_auth.reauthentication_required") return "此操作需要近期重新认证，请重新登录后返回当前页面继续";
  if (reason.status === 409) return "权益数据已被其他管理员更新，请重新读取后再提交";
  if (reason.status === 403) return "当前管理员没有执行此权益操作所需的权限或范围";
  return reason.detail || reason.message || fallback;
}
export function entitlementHasVersionConflict(reason: unknown) { return reason instanceof AuthApiError && reason.status === 409; }
