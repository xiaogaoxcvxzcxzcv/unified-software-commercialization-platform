export type KnownEnvironment = "local" | "test" | "production";
export type ClientEnvironment = KnownEnvironment | "unknown";
export type KnownPlatform = "windows" | "macos" | "linux" | "web" | "h5" | "android" | "ios" | "wechat_miniprogram" | "other";
export type ClientPlatform = KnownPlatform | "unknown";
export type KnownReleaseTrack = "stable" | "beta" | "internal" | "custom";
export type ReleaseTrack = KnownReleaseTrack | "unknown";
export type TenantType = "official" | "agent" | "unknown";
export type TenantStatus = "active" | "suspended" | "unknown";
export type TenantResolution = "official_channel" | "distribution" | "license" | "membership" | "admin" | "unknown";

export interface ProductContext {
  readonly productId: string;
  readonly productCode: string;
  readonly environment: ClientEnvironment;
}

export interface ApplicationContext {
  readonly productId: string;
  readonly environment: ClientEnvironment;
  readonly applicationId: string;
  readonly applicationCode: string;
  readonly platform: ClientPlatform;
  readonly distributionChannel: string;
  readonly clientId: string;
  readonly clientVersion: string;
  readonly releaseTrack: ReleaseTrack;
  readonly contextVersion: number;
}

export interface TenantContext {
  readonly productId: string;
  readonly tenantId: string;
  readonly tenantType: TenantType;
  readonly tenantStatus: TenantStatus;
  readonly resolvedBy: TenantResolution;
  readonly contextVersion: number;
}

export interface TrustedContextSnapshot {
  readonly product: ProductContext;
  readonly application: ApplicationContext;
  readonly tenant: TenantContext;
  readonly expiresAt: string;
}

export interface ProofEnvelope {
  readonly schema_version: 1;
  readonly type: "hmac_sha256_v1" | "ed25519_signature_v1";
  readonly value: string;
  readonly timestamp: string;
}

export interface BoundProofSummary {
  readonly schema_version: 1;
  readonly digest: `sha256:${string}`;
}

export interface CreateClientSessionInput {
  readonly clientId: string;
  readonly credentialId: string;
  readonly clientProof: ProofEnvelope;
  readonly clientVersion: string;
  readonly requestNonce: string;
  readonly deviceSummary?: BoundProofSummary;
  readonly channelProof?: BoundProofSummary;
}

export interface ClientRequestOptions {
  readonly method?: "GET" | "HEAD" | "POST" | "PUT" | "PATCH" | "DELETE";
  readonly body?: unknown;
  readonly headers?: Readonly<Record<string, string>>;
  readonly idempotencyKey?: string;
  readonly timeoutMs?: number;
  readonly signal?: AbortSignal;
}

export type FetchLike = (input: string | URL | Request, init?: RequestInit) => Promise<Response>;

export interface ClientSdkOptions {
  readonly baseUrl: string;
  readonly fetch?: FetchLike;
  readonly timeoutMs?: number;
  readonly maxRetries?: 0 | 1 | 2;
  readonly requestIdFactory?: () => string;
}
