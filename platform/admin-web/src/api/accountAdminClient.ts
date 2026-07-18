import { AuthApiError, authenticatedAdminRequest, getAdminCsrfToken } from "./authClient";

export type AccountScope =
  | { type: "platform" }
  | { type: "product"; productId: string }
  | { type: "tenant"; productId: string; tenantId: string };
export type AccountStatus = "active" | "locked" | "disabled";
export type AccessStatus = "active" | "suspended";

export interface MaskedUserIdentifier { type: "email" | "phone"; maskedValue: string; verified: boolean }
export interface ScopedUserAccess { scopeType: "product" | "tenant"; scopeId: string; status: AccessStatus; explicit: boolean; version: number; statusChangedAt: string | null }
export interface AdminUserSummary {
  id: string;
  version: number;
  accountStatus: AccountStatus;
  displayName: string | null;
  identifiers: MaskedUserIdentifier[];
  createdAt: string;
  memberSince: string | null;
  lastSeenAt: string | null;
  activeSessionCount: number;
  totalSessionCount: number;
  access: ScopedUserAccess | null;
}
export interface AdminUserDetail {
  user: AdminUserSummary;
  profile: { userId: string; version: number; displayName: string | null; avatarUrl: string | null; locale: string | null; timezone: string | null };
}
export interface AdminUserSession {
  id: string;
  productId: string;
  applicationId: string;
  tenantId: string | null;
  environment: "local" | "test" | "production" | null;
  authenticationMethod: "password" | "oidc" | "wechat" | "recovery";
  deviceLabel: string | null;
  createdAt: string;
  lastSeenAt: string;
  expiresAt: string;
  revokedAt: string | null;
}
export interface CursorPage<T> { items: T[]; nextCursor: string | null }
export interface UserAccessMutation { userId: string; scopeType: "platform" | "product" | "tenant"; scopeId: string | null; status: AccountStatus | AccessStatus; version: number; auditId: string }
export interface UserSessionRevocation { userId: string; scopeType: "platform" | "product" | "tenant"; scopeId: string | null; revokedCount: number; auditId: string }
export interface AdminAuditEvent { auditId: string; occurredAt: string; actorId: string; permission: string; scopeType: "platform" | "product" | "tenant"; scopeId?: string; action: string; targetType: string; targetId: string; result: string; reasonCode?: string | null; traceId: string; redactedSummary?: Record<string, unknown> }

interface ListUsersOptions {
  query?: string;
  accountStatus?: AccountStatus;
  accessStatus?: AccessStatus;
  cursor?: string;
  pageSize?: number;
}
interface RequestOptions { idempotencyKey: string }

const identifierPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
const accountStatuses = new Set<AccountStatus>(["active", "locked", "disabled"]);
const accessStatuses = new Set<AccessStatus>(["active", "suspended"]);
const scopeTypes = new Set(["platform", "product", "tenant"]);
const environments = new Set(["local", "test", "production"]);
const authenticationMethods = new Set(["password", "oidc", "wechat", "recovery"]);

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
  const result = text(value, label, 128);
  if (!identifierPattern.test(result)) throw new TypeError(`${label} is invalid`);
  return result;
}
function integer(value: unknown, label: string, minimum = 0) {
  if (!Number.isInteger(value) || (value as number) < minimum) throw new TypeError(`${label} is invalid`);
  return value as number;
}
function nullableText(value: unknown, label: string, maxLength = 320) {
  return value === null ? null : text(value, label, maxLength);
}
function timestamp(value: unknown, label: string) {
  const result = text(value, label, 64);
  if (Number.isNaN(Date.parse(result))) throw new TypeError(`${label} is invalid`);
  return result;
}
function nullableTimestamp(value: unknown, label: string) { return value === null ? null : timestamp(value, label); }

function maskedIdentifier(value: unknown): MaskedUserIdentifier {
  const source = exact(value, ["type", "masked_value", "verified"], [], "masked identifier");
  if (source.type !== "email" && source.type !== "phone" || typeof source.verified !== "boolean") throw new TypeError("masked identifier is invalid");
  return { type: source.type, maskedValue: text(source.masked_value, "masked identifier value"), verified: source.verified };
}
function scopedAccess(value: unknown): ScopedUserAccess | null {
  if (value === null) return null;
  const source = exact(value, ["scope_type", "scope_id", "status", "explicit", "version"], ["status_changed_at"], "scoped user access");
  if ((source.scope_type !== "product" && source.scope_type !== "tenant") || !accessStatuses.has(source.status as AccessStatus) || typeof source.explicit !== "boolean") throw new TypeError("scoped user access is invalid");
  return {
    scopeType: source.scope_type,
    scopeId: id(source.scope_id, "access scope id"),
    status: source.status as AccessStatus,
    explicit: source.explicit,
    version: integer(source.version, "access version"),
    statusChangedAt: source.status_changed_at === undefined ? null : nullableTimestamp(source.status_changed_at, "access status changed at"),
  };
}
function userSummary(value: unknown): AdminUserSummary {
  const source = exact(value, ["user_id", "user_version", "account_status", "display_name", "identifiers", "created_at", "member_since", "last_seen_at", "active_session_count", "total_session_count", "access"], [], "admin user summary");
  if (!accountStatuses.has(source.account_status as AccountStatus) || !Array.isArray(source.identifiers)) throw new TypeError("admin user summary is invalid");
  return {
    id: id(source.user_id, "user id"), version: integer(source.user_version, "user version", 1), accountStatus: source.account_status as AccountStatus,
    displayName: nullableText(source.display_name, "display name", 128), identifiers: source.identifiers.map(maskedIdentifier), createdAt: timestamp(source.created_at, "user created at"),
    memberSince: nullableTimestamp(source.member_since, "member since"), lastSeenAt: nullableTimestamp(source.last_seen_at, "last seen at"),
    activeSessionCount: integer(source.active_session_count, "active session count"), totalSessionCount: integer(source.total_session_count, "total session count"), access: scopedAccess(source.access),
  };
}
function userPage(value: unknown): CursorPage<AdminUserSummary> {
  const source = exact(value, ["items", "next_cursor"], [], "admin user page");
  if (!Array.isArray(source.items)) throw new TypeError("admin user page is invalid");
  return { items: source.items.map(userSummary), nextCursor: nullableText(source.next_cursor, "next cursor", 512) };
}
function userDetail(value: unknown): AdminUserDetail {
  const source = exact(value, ["user", "profile"], [], "admin user detail");
  const profile = exact(source.profile, ["user_id", "version"], ["display_name", "avatar_url", "locale", "timezone"], "user profile");
  const user = userSummary(source.user);
  const userId = id(profile.user_id, "profile user id");
  if (userId !== user.id) throw new TypeError("user detail identifiers do not match");
  return { user, profile: { userId, version: integer(profile.version, "profile version", 1), displayName: profile.display_name === undefined ? null : nullableText(profile.display_name, "profile display name", 128), avatarUrl: profile.avatar_url === undefined ? null : nullableText(profile.avatar_url, "avatar URL", 2048), locale: profile.locale === undefined ? null : nullableText(profile.locale, "locale", 32), timezone: profile.timezone === undefined ? null : nullableText(profile.timezone, "timezone", 64) } };
}
function userSession(value: unknown): AdminUserSession {
  const source = exact(value, ["session_id", "product_id", "application_id", "tenant_id", "environment", "authentication_method", "created_at", "last_seen_at", "expires_at", "revoked_at"], ["device_label"], "admin user session");
  if ((source.environment !== null && !environments.has(source.environment as string)) || !authenticationMethods.has(source.authentication_method as string)) throw new TypeError("admin user session is invalid");
  return { id: id(source.session_id, "session id"), productId: id(source.product_id, "session product id"), applicationId: id(source.application_id, "session application id"), tenantId: source.tenant_id === null ? null : id(source.tenant_id, "session tenant id"), environment: source.environment as AdminUserSession["environment"], authenticationMethod: source.authentication_method as AdminUserSession["authenticationMethod"], deviceLabel: source.device_label === undefined ? null : nullableText(source.device_label, "device label", 120), createdAt: timestamp(source.created_at, "session created at"), lastSeenAt: timestamp(source.last_seen_at, "session last seen at"), expiresAt: timestamp(source.expires_at, "session expires at"), revokedAt: nullableTimestamp(source.revoked_at, "session revoked at") };
}
function sessionPage(value: unknown): CursorPage<AdminUserSession> {
  const source = exact(value, ["items", "next_cursor"], [], "admin user session page");
  if (!Array.isArray(source.items)) throw new TypeError("admin user session page is invalid");
  return { items: source.items.map(userSession), nextCursor: nullableText(source.next_cursor, "session next cursor", 512) };
}
function accessMutation(value: unknown): UserAccessMutation {
  const source = exact(value, ["user_id", "scope_type", "status", "version", "audit_id"], ["scope_id"], "user access mutation");
  if (!scopeTypes.has(source.scope_type as string) || !accountStatuses.has(source.status as AccountStatus) && !accessStatuses.has(source.status as AccessStatus)) throw new TypeError("user access mutation is invalid");
  return { userId: id(source.user_id, "mutation user id"), scopeType: source.scope_type as UserAccessMutation["scopeType"], scopeId: source.scope_id === undefined || source.scope_id === null ? null : id(source.scope_id, "mutation scope id"), status: source.status as UserAccessMutation["status"], version: integer(source.version, "mutation version", 1), auditId: id(source.audit_id, "mutation audit id") };
}
function sessionRevocation(value: unknown): UserSessionRevocation {
  const source = exact(value, ["user_id", "scope_type", "revoked_count", "audit_id"], ["scope_id"], "user session revocation");
  if (!scopeTypes.has(source.scope_type as string)) throw new TypeError("user session revocation is invalid");
  return { userId: id(source.user_id, "revocation user id"), scopeType: source.scope_type as UserSessionRevocation["scopeType"], scopeId: source.scope_id === undefined || source.scope_id === null ? null : id(source.scope_id, "revocation scope id"), revokedCount: integer(source.revoked_count, "revoked count"), auditId: id(source.audit_id, "revocation audit id") };
}
function auditEvent(value: unknown): AdminAuditEvent {
  const source = exact(value, ["audit_id", "occurred_at", "actor_id", "permission", "scope_type", "action", "target_type", "target_id", "result", "trace_id"], ["scope_id", "reason_code", "redacted_summary"], "admin audit event");
  if (!scopeTypes.has(source.scope_type as string) || (source.redacted_summary !== undefined && (!source.redacted_summary || typeof source.redacted_summary !== "object" || Array.isArray(source.redacted_summary)))) throw new TypeError("admin audit event is invalid");
  return { auditId: id(source.audit_id, "audit id"), occurredAt: timestamp(source.occurred_at, "audit occurred at"), actorId: id(source.actor_id, "audit actor id"), permission: text(source.permission, "audit permission", 128), scopeType: source.scope_type as AdminAuditEvent["scopeType"], scopeId: source.scope_id === undefined ? undefined : id(source.scope_id, "audit scope id"), action: text(source.action, "audit action", 128), targetType: text(source.target_type, "audit target type", 128), targetId: id(source.target_id, "audit target id"), result: text(source.result, "audit result", 64), reasonCode: source.reason_code === undefined ? undefined : source.reason_code === null ? null : text(source.reason_code, "audit reason code", 128), traceId: text(source.trace_id, "audit trace id", 128), redactedSummary: source.redacted_summary as Record<string, unknown> | undefined };
}

function scopeBase(scope: AccountScope) {
  if (scope.type === "platform") return "/api/v1/admin";
  const product = `/api/v1/admin/products/${encodeURIComponent(scope.productId)}`;
  return scope.type === "tenant" ? `${product}/tenants/${encodeURIComponent(scope.tenantId)}` : product;
}
function usersPath(scope: AccountScope) { return `${scopeBase(scope)}/users`; }
function queryString(options: ListUsersOptions, allowAccessStatus: boolean) {
  const query = new URLSearchParams();
  if (options.query?.trim()) query.set("query", options.query.trim());
  if (options.accountStatus) query.set("account_status", options.accountStatus);
  if (allowAccessStatus && options.accessStatus) query.set("access_status", options.accessStatus);
  if (options.cursor) query.set("cursor", options.cursor);
  if (options.pageSize) query.set("page_size", String(options.pageSize));
  const encoded = query.toString();
  return encoded ? `?${encoded}` : "";
}
function writeHeaders(options: RequestOptions) { return { "Idempotency-Key": options.idempotencyKey }; }

export function createAccountIntentKey() {
  return globalThis.crypto?.randomUUID?.() ?? `account-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export const accountAdminClient = {
  async listUsers(scope: AccountScope, options: ListUsersOptions = {}) { return userPage(await authenticatedAdminRequest(`${usersPath(scope)}${queryString(options, scope.type !== "platform")}`)); },
  async getUser(scope: AccountScope, userId: string) { return userDetail(await authenticatedAdminRequest(`${usersPath(scope)}/${encodeURIComponent(userId)}`)); },
  async listSessions(scope: AccountScope, userId: string, cursor?: string) {
    const suffix = cursor ? `?cursor=${encodeURIComponent(cursor)}&page_size=20` : "?page_size=20";
    return sessionPage(await authenticatedAdminRequest(`${usersPath(scope)}/${encodeURIComponent(userId)}/sessions${suffix}`));
  },
  async setAccess(scope: Exclude<AccountScope, { type: "platform" }>, userId: string, expectedVersion: number, status: AccessStatus, reasonCode: string, options: RequestOptions) {
    return accessMutation(await authenticatedAdminRequest(`${usersPath(scope)}/${encodeURIComponent(userId)}/access`, { method: "PUT", headers: writeHeaders(options), body: JSON.stringify({ expected_version: expectedVersion, status, reason_code: reasonCode }) }, getAdminCsrfToken()));
  },
  async setGlobalSecurity(userId: string, expectedVersion: number, status: AccountStatus, reasonCode: string, options: RequestOptions) {
    return accessMutation(await authenticatedAdminRequest(`/api/v1/admin/users/${encodeURIComponent(userId)}/security-status`, { method: "PUT", headers: writeHeaders(options), body: JSON.stringify({ expected_version: expectedVersion, status, reason_code: reasonCode }) }, getAdminCsrfToken()));
  },
  async revokeSessions(scope: AccountScope, userId: string, input: { sessionIds: string[] } | { allActive: true }, reasonCode: string, options: RequestOptions) {
    if ("sessionIds" in input && (input.sessionIds.length < 1 || input.sessionIds.length > 100 || new Set(input.sessionIds).size !== input.sessionIds.length)) throw new TypeError("sessionIds must contain 1 to 100 unique IDs");
    const body = "sessionIds" in input ? { session_ids: input.sessionIds, reason_code: reasonCode } : { all_active: true, reason_code: reasonCode };
    return sessionRevocation(await authenticatedAdminRequest(`${usersPath(scope)}/${encodeURIComponent(userId)}/sessions/revoke`, { method: "POST", headers: writeHeaders(options), body: JSON.stringify(body) }, getAdminCsrfToken()));
  },
  async getAuditEvent(auditId: string) { return auditEvent(await authenticatedAdminRequest(`/api/v1/admin/audit/events/${encodeURIComponent(auditId)}`)); },
};

export function accountErrorMessage(reason: unknown, fallback: string) {
  if (!(reason instanceof AuthApiError)) return fallback;
  if (reason.code === "admin_auth.reauthentication_required") return "此操作需要近期重新认证，请重新登录后返回当前页面继续";
  if (reason.status === 409) return "数据已被其他管理员更新，页面将重新读取最新状态";
  if (reason.status === 403) return "当前管理员没有执行此操作所需的权限或范围";
  if (reason.status === 404) return "目标用户在当前管理范围中不存在";
  return reason.detail || reason.message || fallback;
}
export function accountRequiresReauthentication(reason: unknown) { return reason instanceof AuthApiError && reason.code === "admin_auth.reauthentication_required"; }
export function accountHasVersionConflict(reason: unknown) { return reason instanceof AuthApiError && reason.status === 409; }
