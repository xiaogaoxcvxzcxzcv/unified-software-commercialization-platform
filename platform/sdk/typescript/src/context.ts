import type {
  ApplicationContext,
  ClientEnvironment,
  ClientPlatform,
  ReleaseTrack,
  TenantResolution,
  TenantStatus,
  TenantType,
  TrustedContextSnapshot,
} from "./types.js";

interface RawClientSession {
  client_session_token: unknown;
  expires_at: unknown;
  product_context: Record<string, unknown>;
  application_context: Record<string, unknown>;
  tenant_context: Record<string, unknown>;
}

const environments = new Set(["local", "test", "production"]);
const platforms = new Set(["windows", "macos", "linux", "web", "h5", "android", "ios", "wechat_miniprogram", "other"]);
const releaseTracks = new Set(["stable", "beta", "internal", "custom"]);
const tenantTypes = new Set(["official", "agent"]);
const tenantStatuses = new Set(["active", "suspended"]);
const tenantResolutions = new Set(["official_channel", "distribution", "license", "membership", "admin"]);

function requiredString(value: unknown, field: string): string {
  if (typeof value !== "string" || value.trim() === "") throw new TypeError(`invalid client session field: ${field}`);
  return value;
}

function requiredVersion(value: unknown, field: string): number {
  if (!Number.isInteger(value) || (value as number) < 1) throw new TypeError(`invalid client session field: ${field}`);
  return value as number;
}

function known<T extends string>(value: unknown, values: ReadonlySet<string>): T | "unknown" {
  return typeof value === "string" && values.has(value) ? value as T : "unknown";
}

function record(value: unknown, field: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new TypeError(`invalid client session field: ${field}`);
  return value as Record<string, unknown>;
}

export class TrustedClientContext {
  readonly #snapshot: TrustedContextSnapshot;

  private constructor(snapshot: TrustedContextSnapshot) {
    this.#snapshot = Object.freeze(snapshot);
  }

  get product() { return this.#snapshot.product; }
  get application() { return this.#snapshot.application; }
  get tenant() { return this.#snapshot.tenant; }
  get expiresAt() { return this.#snapshot.expiresAt; }

  toJSON(): TrustedContextSnapshot {
    return this.#snapshot;
  }

  static parse(value: unknown): { context: TrustedClientContext; token: string } {
    const root = record(value, "response") as unknown as RawClientSession;
    const product = record(root.product_context, "product_context");
    const application = record(root.application_context, "application_context");
    const tenant = record(root.tenant_context, "tenant_context");
    const productId = requiredString(product.product_id, "product_context.product_id");
    const applicationProductId = requiredString(application.product_id, "application_context.product_id");
    const tenantProductId = requiredString(tenant.product_id, "tenant_context.product_id");
    if (productId !== applicationProductId || productId !== tenantProductId) throw new TypeError("client session context scope mismatch");
    const productEnvironment = known<Exclude<ClientEnvironment, "unknown">>(product.environment, environments);
    const applicationEnvironment = known<Exclude<ClientEnvironment, "unknown">>(application.environment, environments);
    if (productEnvironment !== "unknown" && applicationEnvironment !== "unknown" && productEnvironment !== applicationEnvironment) {
      throw new TypeError("client session environment mismatch");
    }
    const expiresAt = requiredString(root.expires_at, "expires_at");
    if (!Number.isFinite(Date.parse(expiresAt))) throw new TypeError("invalid client session field: expires_at");
    const applicationContext: ApplicationContext = Object.freeze({
      productId,
      environment: applicationEnvironment,
      applicationId: requiredString(application.application_id, "application_context.application_id"),
      applicationCode: requiredString(application.application_code, "application_context.application_code"),
      platform: known<Exclude<ClientPlatform, "unknown">>(application.platform, platforms),
      distributionChannel: requiredString(application.distribution_channel, "application_context.distribution_channel"),
      clientId: requiredString(application.client_id, "application_context.client_id"),
      clientVersion: requiredString(application.client_version, "application_context.client_version"),
      releaseTrack: known<Exclude<ReleaseTrack, "unknown">>(application.release_track, releaseTracks),
      contextVersion: requiredVersion(application.context_version, "application_context.context_version"),
    });
    const snapshot: TrustedContextSnapshot = Object.freeze({
      product: Object.freeze({
        productId,
        productCode: requiredString(product.product_code, "product_context.product_code"),
        environment: productEnvironment,
      }),
      application: applicationContext,
      tenant: Object.freeze({
        productId,
        tenantId: requiredString(tenant.tenant_id, "tenant_context.tenant_id"),
        tenantType: known<Exclude<TenantType, "unknown">>(tenant.tenant_type, tenantTypes),
        tenantStatus: known<Exclude<TenantStatus, "unknown">>(tenant.tenant_status, tenantStatuses),
        resolvedBy: known<Exclude<TenantResolution, "unknown">>(tenant.resolved_by, tenantResolutions),
        contextVersion: requiredVersion(tenant.context_version, "tenant_context.context_version"),
      }),
      expiresAt,
    });
    return { context: new TrustedClientContext(snapshot), token: requiredString(root.client_session_token, "client_session_token") };
  }
}
