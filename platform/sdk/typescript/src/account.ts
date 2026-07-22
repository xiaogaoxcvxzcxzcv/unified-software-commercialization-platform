import { ClientSdkError } from "./errors.js";
import type {
  AccountAccessSummary, AccountCallOptions, AccountProfile, AccountSessionRecord, AccountSessionSnapshot,
  AccountSessionSummary, AccountSessionVault, AccountUserSummary, ChangePasswordInput, CompleteRecoveryInput,
  CurrentAccountSession, ExternalIdentitySummary, IdempotentAccountCallOptions, LoginInput, RecoveryChallenge,
  RefreshSessionInput, RegisterUserInput, StartRecoveryInput, UpdateProfileInput,
} from "./types.js";
import type {
  CompleteExternalLoginInput,
  ExternalExchangeResult,
  ExternalLoginFlow,
  ExternalLoginMode,
  ExternalProvider,
  LinkExternalIdentityInput,
  LinkedExternalIdentity,
  RegistrationVerificationChallenge,
  StartExternalLoginInput,
  StartRegistrationVerificationInput,
  WechatCodeExchangeInput,
} from "./types.js";
interface TransportOptions extends AccountCallOptions {
  readonly method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  readonly body?: unknown;
  readonly token?: string;
  readonly idempotencyKey?: string;
  readonly retry?: boolean;
}
interface Transport {
  readonly clientToken: () => string;
  readonly send: (path: string, options: TransportOptions) => Promise<Response>;
  readonly requestId: () => string;
}

const accountStatuses = new Set(["active", "locked", "disabled"]);
const accessStatuses = new Set(["active", "suspended"]);
const identityStatuses = new Set(["active", "revoked"]);
const decisionStages = new Set(["identity", "product", "tenant", "entitlement", "allowed"]);
const reasonCodes = new Set([
  "IDENTITY_ACCOUNT_DISABLED", "PRODUCT_USER_ACCESS_SUSPENDED", "TENANT_USER_ACCESS_SUSPENDED",
  "ENTITLEMENT_REQUIRED", "ENTITLEMENT_EXPIRED",
]);

function invalidResponse(): ClientSdkError {
  return new ClientSdkError("The account service returned an invalid response.", {
    kind: "unknown", code: "invalid_response", retryable: false,
  });
}
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw invalidResponse();
  return value as Record<string, unknown>;
}
function string(value: unknown): string {
  if (typeof value !== "string" || value.length === 0) throw invalidResponse();
  return value;
}
function secret(value: unknown): string {
  const result = string(value);
  if (result.length < 32) throw invalidResponse();
  return result;
}
function timestamp(value: unknown): string {
  const result = string(value);
  if (!Number.isFinite(Date.parse(result))) throw invalidResponse();
  return result;
}
function integer(value: unknown): number {
  if (!Number.isInteger(value) || (value as number) < 1) throw invalidResponse();
  return value as number;
}
function nullableString(value: unknown): string | null {
  return value === undefined || value === null ? null : string(value);
}
function nullableInteger(value: unknown): number | null {
  return value === undefined || value === null ? null : integer(value);
}
function known<T extends string>(value: unknown, values: ReadonlySet<string>): T | "unknown" {
  return values.has(string(value)) ? value as T : "unknown";
}
async function json(response: Response): Promise<unknown> {
  try { return JSON.parse(await response.text()) as unknown; } catch { throw invalidResponse(); }
}
function requireNoStore(response: Response): void {
  const values = response.headers.get("cache-control")?.toLowerCase().split(",").map((value) => value.trim()) ?? [];
  if (!values.includes("no-store")) {
    throw new ClientSdkError("The account credential response was not marked no-store.", {
      kind: "unknown", code: "unsafe_session_response", status: response.status, retryable: false,
    });
  }
}
function field(raw: Record<string, unknown>, snake: string, camel: string): unknown {
  return raw[snake] ?? raw[camel];
}

function invalidInput(): ClientSdkError {
  return new ClientSdkError("The account request is invalid.", {
    kind: "validation", code: "invalid_request", retryable: false,
  });
}
function providerSegment(value: ExternalProvider): string {
  if (!new Set(["wechat", "oidc", "other"]).has(value)) throw invalidInput();
  return value;
}
function identifierSegment(value: string): string {
  if (typeof value !== "string" || value.length > 128 || !/^[A-Za-z0-9][A-Za-z0-9._:-]*$/.test(value)) throw invalidInput();
  return value;
}
function externalMode(value: ExternalLoginMode): ExternalLoginMode {
  if (!new Set(["redirect", "qr", "native"]).has(value)) throw invalidInput();
  return value;
}
function stableCode(value: string): string {
  if (typeof value !== "string" || value.length < 3 || value.length > 64 || !/^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/.test(value)) throw invalidInput();
  return value;
}
function operationKey(value: string): string {
  if (typeof value !== "string" || value.length < 16 || value.length > 128) throw invalidInput();
  return value;
}
function refreshRequestId(value: string): string {
  if (typeof value !== "string" || value.length < 16 || value.length > 128) throw invalidInput();
  return value;
}
function identifier(value: string): string {
  if (typeof value !== "string" || value.length > 128 || !/^[A-Za-z0-9][A-Za-z0-9._:-]*$/.test(value)) throw invalidInput();
  return value;
}
function externalState(value: string): string {
  if (typeof value !== "string" || value.length < 32 || value.length > 1024) throw invalidInput();
  return value;
}
function oneTimeCode(value: string): string {
  if (typeof value !== "string" || value.length < 1 || value.length > 4096) throw invalidInput();
  return value;
}
function optionalString(value: unknown): string | null {
  return value === undefined ? null : string(value);
}
function safeExternalUrl(value: unknown): string {
  const raw = string(value);
  let parsed: URL;
  try { parsed = new URL(raw); } catch { throw invalidResponse(); }
  const loopback = parsed.hostname === "localhost" || parsed.hostname === "127.0.0.1" || parsed.hostname === "[::1]";
  if (parsed.username || parsed.password || parsed.hash) throw invalidResponse();
  if (parsed.protocol !== "https:" && !(parsed.protocol === "http:" && loopback)) throw invalidResponse();
  return raw;
}
function parseExternalIdentity(value: unknown): ExternalIdentitySummary {
  const raw = object(value);
  return Object.freeze({
    externalIdentityId: string(raw.external_identity_id),
    provider: string(raw.provider),
    maskedSubject: nullableString(raw.masked_subject),
    status: known<"active" | "revoked">(raw.status, identityStatuses),
    linkedAt: timestamp(raw.linked_at),
  });
}
function parseUser(value: unknown): AccountUserSummary {
  const raw = object(value);
  return Object.freeze({
    userId: string(field(raw, "user_id", "userId")),
    accountStatus: known<"active" | "locked" | "disabled">(field(raw, "account_status", "accountStatus"), accountStatuses),
    displayName: nullableString(field(raw, "display_name", "displayName")),
    productId: nullableString(field(raw, "product_id", "productId")),
    tenantId: nullableString(field(raw, "tenant_id", "tenantId")),
    accessVersion: nullableInteger(field(raw, "access_version", "accessVersion")),
    productAccessStatus: field(raw, "product_access_status", "productAccessStatus") == null
      ? null : known<"active" | "suspended">(field(raw, "product_access_status", "productAccessStatus"), accessStatuses),
    tenantAccessStatus: field(raw, "tenant_access_status", "tenantAccessStatus") == null
      ? null : known<"active" | "suspended">(field(raw, "tenant_access_status", "tenantAccessStatus"), accessStatuses),
  });
}
function parseRecord(value: unknown): AccountSessionRecord {
  const raw = object(value);
  const version = field(raw, "schema_version", "schemaVersion");
  if (version !== undefined && version !== 1) throw invalidResponse();
  const pendingRefreshRequestId = field(raw, "pending_refresh_request_id", "pendingRefreshRequestId");
  return Object.freeze({
    schemaVersion: 1,
    accessToken: secret(field(raw, "access_token", "accessToken")),
    refreshToken: secret(field(raw, "refresh_token", "refreshToken")),
    ...(pendingRefreshRequestId === undefined ? {} : { pendingRefreshRequestId: refreshRequestId(pendingRefreshRequestId as string) }),
    accessExpiresAt: timestamp(field(raw, "access_expires_at", "accessExpiresAt")),
    refreshExpiresAt: timestamp(field(raw, "refresh_expires_at", "refreshExpiresAt")),
    user: parseUser(raw.user),
  });
}
function snapshot(record: AccountSessionRecord): AccountSessionSnapshot {
  return Object.freeze({
    user: record.user, accessExpiresAt: record.accessExpiresAt, refreshExpiresAt: record.refreshExpiresAt,
  });
}
function calls(options: AccountCallOptions | undefined): AccountCallOptions {
  return { signal: options?.signal, timeoutMs: options?.timeoutMs };
}

export class AccountSdk {
  readonly #transport: Transport;
  readonly #vault?: AccountSessionVault;
  #session?: AccountSessionRecord;
  #currentSessionId?: string;

  constructor(transport: Transport, vault?: AccountSessionVault) {
    this.#transport = transport;
    this.#vault = vault;
  }

  get session(): AccountSessionSnapshot | null {
    return this.#session ? snapshot(this.#session) : null;
  }


  async startRegistrationVerification(
    input: StartRegistrationVerificationInput,
    options: IdempotentAccountCallOptions,
  ): Promise<RegistrationVerificationChallenge> {
    const response = await this.#transport.send("/api/v1/auth/verification/start", {
      method: "POST", token: this.#transport.clientToken(), idempotencyKey: operationKey(options.idempotencyKey), retry: true,
      body: { identifier: input.identifier }, ...calls(options),
    });
    requireNoStore(response);
    const raw = object(await json(response));
    if (raw.accepted !== true) throw invalidResponse();
    return Object.freeze({ accepted: true, continuationId: string(raw.continuation_id) });
  }
  async registerUser(input: RegisterUserInput, options: IdempotentAccountCallOptions): Promise<AccountSessionSnapshot> {
    const response = await this.#transport.send("/api/v1/auth/register", {
      method: "POST", token: this.#transport.clientToken(), idempotencyKey: operationKey(options.idempotencyKey), retry: true,
      body: {
        identifier: input.identifier, credential: input.credential,
        verification_continuation_id: input.verificationContinuationId, verification_proof: input.verificationProof,
        ...(input.displayName === undefined ? {} : { display_name: input.displayName }),
      }, ...calls(options),
    });
    return this.#acceptCredentials(response);
  }

  async login(input: LoginInput, options?: AccountCallOptions): Promise<AccountSessionSnapshot> {
    const response = await this.#transport.send("/api/v1/auth/login", {
      method: "POST", token: this.#transport.clientToken(), retry: false,
      body: {
        identifier: input.identifier, credential: input.credential,
        ...(input.deviceRiskSummary === undefined ? {} : { device_risk_summary: input.deviceRiskSummary }),
      }, ...calls(options),
    });
    return this.#acceptCredentials(response);
  }

  async getCurrentSession(options?: AccountCallOptions): Promise<CurrentAccountSession> {
    return this.#withUser(async (token) => {
      const response = await this.#transport.send("/api/v1/auth/session", { token, retry: true, ...calls(options) });
      requireNoStore(response);
      const raw = object(await json(response));
      const sessionId = string(raw.session_id);
      this.#currentSessionId = sessionId;
      return Object.freeze({
        sessionId, user: parseUser(raw.user),
        accessExpiresAt: timestamp(raw.access_expires_at), refreshExpiresAt: timestamp(raw.refresh_expires_at),
      });
    });
  }

  async refreshSession(input: RefreshSessionInput = {}, options?: AccountCallOptions): Promise<AccountSessionSnapshot> {
    // Reject caller input before entering the recovery state machine so a
    // malformed override cannot discard an already-persisted pending request.
    const requestedId = input.clientRequestId === undefined ? undefined : refreshRequestId(input.clientRequestId);
    try {
      const current = this.#requireSession();
      if (Date.parse(current.refreshExpiresAt) <= Date.now()) {
        throw new ClientSdkError("The user session has expired.", {
          kind: "authentication", code: "user_session_expired", retryable: false,
        });
      }
      if (current.pendingRefreshRequestId && requestedId && current.pendingRefreshRequestId !== requestedId) {
        throw new ClientSdkError("A different refresh request is already pending.", {
          kind: "conflict", code: "refresh_request_id_conflict", retryable: false,
        });
      }
      const clientRequestId = current.pendingRefreshRequestId ?? requestedId ?? refreshRequestId(this.#transport.requestId());
      if (!current.pendingRefreshRequestId) await this.#beginRefresh(current, clientRequestId);
      const active = this.#requireSession();
      const response = await this.#transport.send("/api/v1/auth/refresh", {
        method: "POST", retry: true,
        body: { refresh_token: active.refreshToken, client_request_id: clientRequestId },
        ...calls(options),
      });
      return await this.#acceptCredentials(response, active.user, true);
    } catch (error) {
      const terminal = await this.#clearOnTerminal(error);
      if (!terminal && this.#isDefinitiveRefreshFailure(error)) await this.#clearPendingRefresh();
      throw error;
    }
  }
  async logout(options?: AccountCallOptions): Promise<void> {
    try {
      await this.#withUser((token) => this.#transport.send("/api/v1/auth/logout", {
        method: "POST", token, retry: false, ...calls(options),
      }).then(() => undefined));
      await this.#clear();
    } catch (error) {
      await this.#clearOnTerminal(error);
      throw error;
    }
  }

  async startRecovery(input: StartRecoveryInput, options: IdempotentAccountCallOptions): Promise<RecoveryChallenge> {
    const response = await this.#transport.send("/api/v1/auth/recovery/start", {
      method: "POST", token: this.#transport.clientToken(), idempotencyKey: operationKey(options.idempotencyKey), retry: true,
      body: { identifier: input.identifier }, ...calls(options),
    });
    requireNoStore(response);
    const raw = object(await json(response));
    if (raw.accepted !== true) throw invalidResponse();
    return Object.freeze({ accepted: true, continuationId: string(raw.continuation_id) });
  }

  async completeRecovery(input: CompleteRecoveryInput, options: IdempotentAccountCallOptions): Promise<void> {
    await this.#transport.send("/api/v1/auth/recovery/complete", {
      method: "POST", token: this.#transport.clientToken(), idempotencyKey: operationKey(options.idempotencyKey), retry: true,
      body: {
        continuation_id: input.continuationId, recovery_proof: input.recoveryProof, new_credential: input.newCredential,
      }, ...calls(options),
    });
  }


  async startExternalLogin(input: StartExternalLoginInput, options?: AccountCallOptions): Promise<ExternalLoginFlow> {
    const response = await this.#transport.send(
      "/api/v1/auth/external/" + providerSegment(input.provider) + "/start",
      {
        method: "POST", token: this.#transport.clientToken(), retry: false,
        body: {
          mode: externalMode(input.mode),
          return_target_code: stableCode(input.returnTargetCode),
        },
        ...calls(options),
      },
    );
    requireNoStore(response);
    return this.#parseExternalLoginFlow(await json(response));
  }

  async completeExternalLogin(
    input: CompleteExternalLoginInput,
    options?: AccountCallOptions,
  ): Promise<ExternalExchangeResult> {
    const hasCode = typeof input.code === "string";
    const hasProviderError = typeof input.providerError === "string";
    if (hasCode === hasProviderError) throw invalidInput();
    const response = await this.#transport.send(
      "/api/v1/auth/external/" + providerSegment(input.provider) + "/callback",
      {
        method: "POST", token: this.#transport.clientToken(), retry: false,
        body: {
          flow_id: identifier(input.flowId),
          state: externalState(input.state),
          ...(hasCode ? { code: oneTimeCode(input.code as string) } : { provider_error: stableCode(input.providerError as string) }),
        },
        ...calls(options),
      },
    );
    return this.#acceptExternalExchange(response);
  }

  async exchangeWechatCode(
    input: WechatCodeExchangeInput,
    options?: AccountCallOptions,
  ): Promise<ExternalExchangeResult> {
    const response = await this.#transport.send("/api/v1/auth/external/wechat/exchange", {
      method: "POST", token: this.#transport.clientToken(), retry: false,
      body: { code: oneTimeCode(input.code), flow_id: identifier(input.flowId), state: externalState(input.state) },
      ...calls(options),
    });
    return this.#acceptExternalExchange(response);
  }
  async getProfile(options?: AccountCallOptions): Promise<AccountProfile> {
    return this.#withUser(async (token) => this.#parseProfile(await json(
      await this.#transport.send("/api/v1/account/profile", { token, retry: true, ...calls(options) }),
    )));
  }

  async updateProfile(input: UpdateProfileInput, options: IdempotentAccountCallOptions): Promise<AccountProfile> {
    return this.#withUser(async (token) => this.#parseProfile(await json(
      await this.#transport.send("/api/v1/account/profile", {
        method: "PATCH", token, idempotencyKey: operationKey(options.idempotencyKey), retry: true,
        body: {
          expected_version: input.expectedVersion,
          ...(input.displayName === undefined ? {} : { display_name: input.displayName }),
          ...(input.avatarUrl === undefined ? {} : { avatar_url: input.avatarUrl }),
          ...(input.locale === undefined ? {} : { locale: input.locale }),
          ...(input.timezone === undefined ? {} : { timezone: input.timezone }),
        }, ...calls(options),
      }),
    )));
  }

  async changePassword(input: ChangePasswordInput, options: IdempotentAccountCallOptions): Promise<void> {
    await this.#withUser((token) => this.#transport.send("/api/v1/account/password", {
      method: "PUT", token, idempotencyKey: operationKey(options.idempotencyKey), retry: false,
      body: {
        current_credential: input.currentCredential, new_credential: input.newCredential,
        revoke_other_sessions: input.revokeOtherSessions,
      }, ...calls(options),
    }).then(() => undefined));
  }

  async listSessions(options?: AccountCallOptions): Promise<readonly AccountSessionSummary[]> {
    return this.#withUser(async (token) => {
      const raw = object(await json(await this.#transport.send("/api/v1/account/sessions", {
        token, retry: true, ...calls(options),
      })));
      if (!Array.isArray(raw.items)) throw invalidResponse();
      const sessions = raw.items.map((item) => {
        const value = object(item);
        if (typeof value.current !== "boolean") throw invalidResponse();
        return Object.freeze({
          sessionId: string(value.session_id), current: value.current, deviceLabel: nullableString(value.device_label),
          createdAt: timestamp(value.created_at), lastSeenAt: timestamp(value.last_seen_at),
          expiresAt: timestamp(value.expires_at),
        });
      });
      const current = sessions.find((session) => session.current);
      if (current) this.#currentSessionId = current.sessionId;
      return Object.freeze(sessions);
    });
  }

  async revokeSession(sessionId: string, options?: AccountCallOptions): Promise<void> {
    await this.#withUser(async (token) => {
      await this.#transport.send("/api/v1/account/sessions/" + identifierSegment(sessionId), {
        method: "DELETE", token, retry: false, ...calls(options),
      });
      if (sessionId === this.#currentSessionId) await this.#clear();
    });
  }

  async listExternalIdentities(options?: AccountCallOptions): Promise<readonly ExternalIdentitySummary[]> {
    return this.#withUser(async (token) => {
      const raw = object(await json(await this.#transport.send("/api/v1/account/external-identities", {
        token, retry: true, ...calls(options),
      })));
      if (!Array.isArray(raw.items)) throw invalidResponse();
      return Object.freeze(raw.items.map((item) => {
        const value = object(item);
        return Object.freeze({
          externalIdentityId: string(value.external_identity_id), provider: string(value.provider),
          maskedSubject: nullableString(value.masked_subject), status: known<"active" | "revoked">(value.status, identityStatuses),
          linkedAt: timestamp(value.linked_at),
        });
      }));
    });
  }


  async linkExternalIdentity(
    input: LinkExternalIdentityInput,
    options: IdempotentAccountCallOptions,
  ): Promise<LinkedExternalIdentity> {
    return this.#withUser(async (token) => {
      const raw = object(await json(await this.#transport.send(
        "/api/v1/account/external-identities/" + providerSegment(input.provider) + "/link",
        {
          method: "POST", token, idempotencyKey: operationKey(options.idempotencyKey), retry: true,
          body: { external_proof_id: identifier(input.externalProofId) },
          ...calls(options),
        },
      )));
      return Object.freeze({ ...parseExternalIdentity(raw), auditId: nullableString(raw.audit_id) });
    });
  }

  async unlinkExternalIdentity(externalIdentityId: string, options?: AccountCallOptions): Promise<void> {
    await this.#withUser(async (token) => {
      await this.#transport.send(
        "/api/v1/account/external-identities/" + identifierSegment(externalIdentityId),
        { method: "DELETE", token, retry: false, ...calls(options) },
      );
    });
  }
  async getAccessSummary(options?: AccountCallOptions): Promise<AccountAccessSummary> {
    return this.#withUser(async (token) => {
      const raw = object(await json(await this.#transport.send("/api/v1/account/access", {
        token, retry: true, ...calls(options),
      })));
      if (typeof raw.allowed !== "boolean") throw invalidResponse();
      return Object.freeze({
        allowed: raw.allowed, decisionStage: known<"identity" | "product" | "tenant" | "entitlement" | "allowed">(raw.decision_stage, decisionStages),
        reasonCode: raw.reason_code === null ? null : known<"IDENTITY_ACCOUNT_DISABLED" | "PRODUCT_USER_ACCESS_SUSPENDED" | "TENANT_USER_ACCESS_SUSPENDED" | "ENTITLEMENT_REQUIRED" | "ENTITLEMENT_EXPIRED">(raw.reason_code, reasonCodes),
      });
    });
  }

  async restoreSession(options?: AccountCallOptions): Promise<AccountSessionSnapshot | null> {
    if (!this.#vault) return null;
    let restored: AccountSessionRecord;
    try { restored = parseRecord(await this.#vault.load()); } catch { await this.#clear(); return null; }
    if (Date.parse(restored.refreshExpiresAt) <= Date.now()) { await this.#clear(); return null; }
    this.#session = restored;
    this.#currentSessionId = undefined;
    if (restored.pendingRefreshRequestId || Date.parse(restored.accessExpiresAt) <= Date.now()) {
      const input = restored.pendingRefreshRequestId ? { clientRequestId: restored.pendingRefreshRequestId } : {};
      return this.refreshSession(input, options);
    }
    return snapshot(restored);
  }

  async clearSession(): Promise<void> {
    await this.#clear();
  }

  #parseExternalLoginFlow(value: unknown): ExternalLoginFlow {
    const raw = object(value);
    const mode = known<"redirect" | "qr" | "native">(raw.mode, new Set(["redirect", "qr", "native"]));
    const authorizationUrl = raw.authorization_url === undefined ? null : safeExternalUrl(raw.authorization_url);
    const qrPayload = optionalString(raw.qr_payload);
    if ((mode === "redirect" || mode === "native") && (authorizationUrl === null || qrPayload !== null)) throw invalidResponse();
    if (mode === "qr" && (qrPayload === null || authorizationUrl !== null)) throw invalidResponse();
    return Object.freeze({
      flowId: string(raw.flow_id), mode, authorizationUrl, qrPayload, expiresAt: timestamp(raw.expires_at),
    });
  }

  async #acceptExternalExchange(response: Response): Promise<ExternalExchangeResult> {
    requireNoStore(response);
    const raw = object(await json(response));
    const status = known<"authenticated" | "link_required" | "conflict" | "review_required">(
      raw.status,
      new Set(["authenticated", "link_required", "conflict", "review_required"]),
    );
    if (status === "authenticated") {
      if (raw.session === undefined || raw.proof_id !== undefined) throw invalidResponse();
      return Object.freeze({ status, session: await this.#acceptCredentialValue(raw.session) });
    }
    if (status === "link_required") {
      if (raw.session !== undefined) throw invalidResponse();
      return Object.freeze({ status, proofId: string(raw.proof_id) });
    }
    if ((status === "conflict" || status === "review_required") && (raw.session !== undefined || raw.proof_id !== undefined)) {
      throw invalidResponse();
    }
    return Object.freeze({ status });
  }

  async #acceptCredentialValue(
    value: unknown,
    fallbackUser?: AccountUserSummary,
    preserveCurrentSessionId = false,
  ): Promise<AccountSessionSnapshot> {
    const raw = object(value);
    const record = parseRecord({ ...raw, user: raw.user ?? fallbackUser });
    this.#session = record;
    if (!preserveCurrentSessionId) this.#currentSessionId = undefined;
    try {
      await this.#vault?.save(record);
    } catch {
      this.#session = undefined;
      this.#currentSessionId = undefined;
      try { await this.#vault?.clear(); } catch { /* Keep the stable SDK error authoritative. */ }
      throw new ClientSdkError("The secure session vault could not save the session.", {
        kind: "unknown", code: "session_vault_error", retryable: false,
      });
    }
    return snapshot(record);
  }

  async #acceptCredentials(
    response: Response,
    fallbackUser?: AccountUserSummary,
    preserveCurrentSessionId = false,
  ): Promise<AccountSessionSnapshot> {
    requireNoStore(response);
    return this.#acceptCredentialValue(await json(response), fallbackUser, preserveCurrentSessionId);
  }

  #parseProfile(value: unknown): AccountProfile {
    const raw = object(value);
    return Object.freeze({
      userId: string(raw.user_id), version: integer(raw.version), displayName: nullableString(raw.display_name),
      avatarUrl: nullableString(raw.avatar_url), locale: nullableString(raw.locale), timezone: nullableString(raw.timezone),
    });
  }

  async #beginRefresh(current: AccountSessionRecord, clientRequestId: string): Promise<void> {
    const pending = Object.freeze({ ...current, pendingRefreshRequestId: clientRequestId });
    this.#session = pending;
    try {
      await this.#vault?.save(pending);
    } catch {
      this.#session = current;
      try { await this.#vault?.clear(); } catch { /* Keep the stable SDK error authoritative. */ }
      throw new ClientSdkError("The secure session vault could not save refresh recovery state.", {
        kind: "unknown", code: "session_vault_error", retryable: false,
      });
    }
  }

  async #clearPendingRefresh(): Promise<void> {
    const current = this.#session;
    if (!current?.pendingRefreshRequestId) return;
    const { pendingRefreshRequestId: _pending, ...record } = current;
    const settled = Object.freeze(record) as AccountSessionRecord;
    this.#session = settled;
    try {
      await this.#vault?.save(settled);
    } catch {
      try { await this.#vault?.clear(); } catch { /* Preserve the original business error. */ }
    }
  }

  #isDefinitiveRefreshFailure(error: unknown): boolean {
    if (!(error instanceof ClientSdkError) || error.retryable) return false;
    if (["network", "timeout", "cancelled"].includes(error.kind)) return false;
    return !["invalid_response", "unsafe_session_response", "session_vault_error", "refresh_request_id_conflict"].includes(error.code);
  }

  #requireSession(): AccountSessionRecord {
    if (!this.#session) {
      throw new ClientSdkError("A user session is required.", {
        kind: "authentication", code: "user_session_required", retryable: false,
      });
    }
    return this.#session;
  }

  async #withUser<T>(operation: (accessToken: string) => Promise<T>): Promise<T> {
    try { return await operation(this.#requireSession().accessToken); }
    catch (error) { await this.#clearOnTerminal(error); throw error; }
  }

  async #clearOnTerminal(error: unknown): Promise<boolean> {
    const terminalCodes = new Set([
      "IDENTITY_ACCOUNT_DISABLED", "IDENTITY_SESSION_EXPIRED", "IDENTITY_SESSION_REVOKED",
      "IDENTITY_REFRESH_REPLAYED", "user_session_expired",
    ]);
    if (!(error instanceof ClientSdkError) || !terminalCodes.has(error.code)) return false;
    this.#session = undefined;
    this.#currentSessionId = undefined;
    try { await this.#vault?.clear(); } catch { /* Preserve the terminal business error. */ }
    return true;
  }

  async #clear(): Promise<void> {
    this.#session = undefined;
    this.#currentSessionId = undefined;
    try { await this.#vault?.clear(); } catch {
      throw new ClientSdkError("The secure session vault could not clear the session.", {
        kind: "unknown", code: "session_vault_error", retryable: false,
      });
    }
  }
}
