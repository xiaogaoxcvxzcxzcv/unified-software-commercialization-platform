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
  readonly accountSessionVault?: AccountSessionVault;
}

export type AccountStatus = "active" | "locked" | "disabled" | "unknown";
export type ScopedAccessStatus = "active" | "suspended" | "unknown";

export interface AccountUserSummary {
  readonly userId: string;
  readonly accountStatus: AccountStatus;
  readonly displayName: string | null;
  readonly productId: string | null;
  readonly tenantId: string | null;
  readonly accessVersion: number | null;
  readonly productAccessStatus: ScopedAccessStatus | null;
  readonly tenantAccessStatus: ScopedAccessStatus | null;
}

export interface AccountSessionSnapshot {
  readonly user: AccountUserSummary;
  readonly accessExpiresAt: string;
  readonly refreshExpiresAt: string;
}

export interface CurrentAccountSession extends AccountSessionSnapshot {
  readonly sessionId: string;
}

export interface AccountSessionRecord extends AccountSessionSnapshot {
  readonly schemaVersion: 1;
  readonly accessToken: string;
  readonly refreshToken: string;
  readonly pendingRefreshRequestId?: string;
}

export interface AccountSessionVault {
  load(): Promise<unknown>;
  save(record: AccountSessionRecord): Promise<void>;
  clear(): Promise<void>;
}

export interface AccountCallOptions {
  readonly signal?: AbortSignal;
  readonly timeoutMs?: number;
}

export interface IdempotentAccountCallOptions extends AccountCallOptions {
  readonly idempotencyKey: string;
}

export interface RegisterUserInput {
  readonly identifier: string;
  readonly credential: string;
  readonly verificationContinuationId: string;
  readonly verificationProof: string;
  readonly displayName?: string;
}

export interface LoginInput {
  readonly identifier: string;
  readonly credential: string;
  readonly deviceRiskSummary?: Readonly<Record<string, unknown>>;
}

export interface StartRecoveryInput { readonly identifier: string; }

export interface RecoveryChallenge {
  readonly accepted: true;
  readonly continuationId: string;
}

export interface CompleteRecoveryInput {
  readonly continuationId: string;
  readonly recoveryProof: string;
  readonly newCredential: string;
}

export interface RefreshSessionInput { readonly clientRequestId?: string; }

export interface AccountProfile {
  readonly userId: string;
  readonly version: number;
  readonly displayName: string | null;
  readonly avatarUrl: string | null;
  readonly locale: string | null;
  readonly timezone: string | null;
}

export interface UpdateProfileInput {
  readonly expectedVersion: number;
  readonly displayName?: string;
  readonly avatarUrl?: string | null;
  readonly locale?: string | null;
  readonly timezone?: string | null;
}

export interface ChangePasswordInput {
  readonly currentCredential: string;
  readonly newCredential: string;
  readonly revokeOtherSessions: boolean;
}

export interface AccountSessionSummary {
  readonly sessionId: string;
  readonly current: boolean;
  readonly deviceLabel: string | null;
  readonly createdAt: string;
  readonly lastSeenAt: string;
  readonly expiresAt: string;
}

export interface ExternalIdentitySummary {
  readonly externalIdentityId: string;
  readonly provider: string;
  readonly maskedSubject: string | null;
  readonly status: "active" | "revoked" | "unknown";
  readonly linkedAt: string;
}

export interface AccountAccessSummary {
  readonly allowed: boolean;
  readonly decisionStage: "identity" | "product" | "tenant" | "entitlement" | "allowed" | "unknown";
  readonly reasonCode:
    | "IDENTITY_ACCOUNT_DISABLED"
    | "PRODUCT_USER_ACCESS_SUSPENDED"
    | "TENANT_USER_ACCESS_SUSPENDED"
    | "ENTITLEMENT_REQUIRED"
    | "ENTITLEMENT_EXPIRED"
    | "unknown"
    | null;
}

export interface StartRegistrationVerificationInput {
  readonly identifier: string;
}

export interface RegistrationVerificationChallenge {
  readonly accepted: true;
  readonly continuationId: string;
}

export type ExternalLoginMode = "redirect" | "qr" | "native";
export type ExternalProvider = "wechat" | "oidc" | "other";

export interface StartExternalLoginInput {
  readonly provider: ExternalProvider;
  readonly mode: ExternalLoginMode;
  readonly returnTargetCode: string;
}

export interface ExternalLoginFlow {
  readonly flowId: string;
  readonly mode: ExternalLoginMode | "unknown";
  readonly authorizationUrl: string | null;
  readonly qrPayload: string | null;
  readonly expiresAt: string;
}

interface ExternalCallbackBase {
  readonly provider: ExternalProvider;
  readonly flowId: string;
  readonly state: string;
}

export type CompleteExternalLoginInput = ExternalCallbackBase & (
  | { readonly code: string; readonly providerError?: never }
  | { readonly providerError: string; readonly code?: never }
);

export interface WechatCodeExchangeInput {
  readonly flowId: string;
  readonly state: string;
  readonly code: string;
}

export type ExternalExchangeResult =
  | { readonly status: "authenticated"; readonly session: AccountSessionSnapshot }
  | { readonly status: "link_required"; readonly proofId: string }
  | { readonly status: "conflict" | "review_required" | "unknown" };

export interface LinkExternalIdentityInput {
  readonly provider: ExternalProvider;
  readonly externalProofId: string;
}

export interface LinkedExternalIdentity extends ExternalIdentitySummary {
  readonly auditId: string | null;
}
