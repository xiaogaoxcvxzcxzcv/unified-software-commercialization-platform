import { describe, expect, it, vi } from "vitest";
import { ClientSdk, ClientSdkError } from "../src/index.js";
import type { AccountSessionRecord, AccountSessionVault } from "../src/index.js";

const clientToken = "c".repeat(48);
const accessToken = "a".repeat(48);
const refreshToken = "r".repeat(48);
const futureAccess = "2099-07-20T10:00:00Z";
const futureRefresh = "2099-08-20T10:00:00Z";
const registrationKey = "register-operation-1";
const recoveryStartKey = "recovery-start-01";
const recoveryCompleteKey = "recovery-complete-1";
const profileUpdateKey = "profile-update-01";
const passwordChangeKey = "password-change-1";
const verificationStartKey = "verification-start-1";
const linkIdentityKey = "link-identity-001";
const clientSession = {
  client_session_token: clientToken,
  expires_at: "2099-07-20T00:00:00Z",
  product_context: { product_id: "prod-1", product_code: "product-one", environment: "production" },
  application_context: {
    product_id: "prod-1", environment: "production", application_id: "app-1", application_code: "desktop",
    platform: "windows", distribution_channel: "official", client_id: "client-1", client_version: "1.0.0",
    release_track: "stable", context_version: 1,
  },
  tenant_context: {
    product_id: "prod-1", tenant_id: "tenant-1", tenant_type: "official", tenant_status: "active",
    resolved_by: "official_channel", context_version: 1,
  },
};
const user = {
  user_id: "user-1", account_status: "active", display_name: "Ada",
  product_id: "prod-1", tenant_id: "tenant-1", access_version: 2,
  product_access_status: "active", tenant_access_status: "active",
};
const credentials = {
  access_token: accessToken, refresh_token: refreshToken,
  access_expires_at: futureAccess, refresh_expires_at: futureRefresh, user,
};
const clientInput = {
  clientId: "client-1", credentialId: "credential-1", clientVersion: "1.0.0",
  requestNonce: "nonce-1234567890123456",
  clientProof: {
    schema_version: 1 as const, type: "hmac_sha256_v1" as const,
    value: "p".repeat(64), timestamp: "2026-07-20T00:00:00Z",
  },
};

function response(body: unknown, status = 200, noStore = false): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: {
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      ...(noStore ? { "Cache-Control": "private, no-store" } : {}),
    },
  });
}
function queued(...items: Array<Response | Error>) {
  return vi.fn(async () => {
    const item = items.shift();
    if (!item) throw new Error("unexpected request");
    if (item instanceof Error) throw item;
    return item;
  });
}
function headers(fetch: ReturnType<typeof queued>, index: number): Headers {
  return new Headers((fetch.mock.calls[index]?.[1] as RequestInit).headers);
}
function body(fetch: ReturnType<typeof queued>, index: number): Record<string, unknown> {
  return JSON.parse((fetch.mock.calls[index]?.[1] as RequestInit).body as string) as Record<string, unknown>;
}
function path(fetch: ReturnType<typeof queued>, index: number): string {
  return new URL(String(fetch.mock.calls[index]?.[0])).pathname;
}
async function ready(fetch: ReturnType<typeof queued>, vault?: AccountSessionVault, maxRetries: 0 | 1 | 2 = 1) {
  const sdk = new ClientSdk({
    baseUrl: "https://api.example.test", fetch, accountSessionVault: vault,
    maxRetries, requestIdFactory: () => "request-1234567890123456",
  });
  await sdk.establishSession(clientInput);
  return sdk;
}
async function authenticated(fetch: ReturnType<typeof queued>, vault?: AccountSessionVault, maxRetries: 0 | 1 | 2 = 1) {
  const sdk = await ready(fetch, vault, maxRetries);
  await sdk.account.login({ identifier: "ada@example.test", credential: "correct horse battery staple" });
  return sdk;
}

describe("ClientSdk account", () => {
  it("uses the trusted client bearer for login and keeps credentials private", async () => {
    const fetch = queued(response(clientSession, 201, true), response({ ...credentials, future: "ignored" }, 200, true));
    const sdk = await ready(fetch);
    const result = await sdk.account.login({ identifier: "ada@example.test", credential: "password" });

    expect(result).toEqual({ user: expect.objectContaining({ userId: "user-1" }), accessExpiresAt: futureAccess, refreshExpiresAt: futureRefresh });
    expect(result).not.toHaveProperty("accessToken");
    expect(headers(fetch, 1).get("Authorization")).toBe("Bearer " + clientToken);
    expect(body(fetch, 1)).not.toHaveProperty("product_id");
    expect(body(fetch, 1)).not.toHaveProperty("tenant_id");
  });

  it("retries keyed registration but never retries login", async () => {
    const retry = response({ code: "temporarily_unavailable", retryable: true }, 503);
    const fetch = queued(
      response(clientSession, 201, true), retry, response(credentials, 201, true),
      new Error("offline"),
    );
    const sdk = await ready(fetch, undefined, 1);
    await sdk.account.registerUser({
      identifier: "ada@example.test", credential: "long password",
      verificationContinuationId: "verify-1", verificationProof: "proof-1234567890",
    }, { idempotencyKey: registrationKey });
    await expect(sdk.account.login({ identifier: "ada@example.test", credential: "bad" })).rejects.toMatchObject({ kind: "network" });
    expect(headers(fetch, 1).get("Idempotency-Key")).toBe(registrationKey);
    expect(fetch).toHaveBeenCalledTimes(4);
    expect(path(fetch, 1)).toBe("/api/v1/auth/register");
    expect(path(fetch, 3)).toBe("/api/v1/auth/login");
  });

  it("sends recovery through ClientSessionBearer and does not retry password completion", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response({ accepted: true, continuation_id: "recovery-1" }, 202, true),
      response(undefined, 204),
    );
    const sdk = await ready(fetch);
    await expect(sdk.account.startRecovery({ identifier: "ada@example.test" }, { idempotencyKey: recoveryStartKey }))
      .resolves.toEqual({ accepted: true, continuationId: "recovery-1" });
    await sdk.account.completeRecovery({
      continuationId: "recovery-1", recoveryProof: "proof-1234567890", newCredential: "new long password",
    }, { idempotencyKey: recoveryCompleteKey });
    expect(headers(fetch, 1).get("Authorization")).toBe("Bearer " + clientToken);
    expect(headers(fetch, 2).get("Authorization")).toBe("Bearer " + clientToken);
    expect([path(fetch, 1), path(fetch, 2)]).toEqual(["/api/v1/auth/recovery/start", "/api/v1/auth/recovery/complete"]);
  });

  it("covers every UserBearer account method and maps response shapes", async () => {
    const profile = { user_id: "user-1", version: 3, display_name: "Ada", avatar_url: null, locale: "en", timezone: "UTC" };
    const fetch = queued(
      response(clientSession, 201, true), response(credentials, 200, true),
      response({ session_id: "session-1", user, access_expires_at: futureAccess, refresh_expires_at: futureRefresh }, 200, true),
      response(profile), response({ ...profile, version: 4, display_name: "Grace" }),
      response(undefined, 204),
      response({ items: [{ session_id: "session-1", current: true, device_label: null, created_at: futureAccess, last_seen_at: futureAccess, expires_at: futureRefresh }] }),
      response(undefined, 204),
      response({ items: [{ external_identity_id: "external-1", provider: "oidc", masked_subject: "a***", status: "active", linked_at: futureAccess }] }),
      response({ allowed: true, decision_stage: "allowed", reason_code: null }),
      response(undefined, 204),
    );
    const sdk = await authenticated(fetch);
    await expect(sdk.account.getCurrentSession()).resolves.toMatchObject({ sessionId: "session-1" });
    await expect(sdk.account.getProfile()).resolves.toMatchObject({ version: 3 });
    await expect(sdk.account.updateProfile({ expectedVersion: 3, displayName: "Grace" }, { idempotencyKey: profileUpdateKey })).resolves.toMatchObject({ version: 4 });
    await sdk.account.changePassword({ currentCredential: "old", newCredential: "new long password", revokeOtherSessions: true }, { idempotencyKey: passwordChangeKey });
    await expect(sdk.account.listSessions()).resolves.toEqual([expect.objectContaining({ sessionId: "session-1", current: true })]);
    await sdk.account.revokeSession("session-other");
    await expect(sdk.account.listExternalIdentities()).resolves.toEqual([expect.objectContaining({ externalIdentityId: "external-1" })]);
    await expect(sdk.account.getAccessSummary()).resolves.toEqual({ allowed: true, decisionStage: "allowed", reasonCode: null });
    await sdk.account.logout();

    for (let index = 2; index <= 10; index += 1) expect(headers(fetch, index).get("Authorization")).toBe("Bearer " + accessToken);
    expect(fetch.mock.calls.map((_call, index) => path(fetch, index))).toEqual([
      "/api/v1/client/session", "/api/v1/auth/login", "/api/v1/auth/session", "/api/v1/account/profile",
      "/api/v1/account/profile", "/api/v1/account/password", "/api/v1/account/sessions",
      "/api/v1/account/sessions/session-other", "/api/v1/account/external-identities",
      "/api/v1/account/access", "/api/v1/auth/logout",
    ]);
    expect(sdk.account.session).toBeNull();
  });

  it("rotates refresh without Authorization and reuses one client_request_id across retry", async () => {
    const fetch = queued(
      response(clientSession, 201, true), response(credentials, 200, true),
      new Error("connection reset"), response({
        access_token: "b".repeat(48), refresh_token: "s".repeat(48),
        access_expires_at: futureAccess, refresh_expires_at: futureRefresh,
      }, 200, true),
    );
    const sdk = await authenticated(fetch, undefined, 1);
    await sdk.account.refreshSession({ clientRequestId: "refresh-request-123456" });
    expect(headers(fetch, 2).get("Authorization")).toBeNull();
    expect(path(fetch, 2)).toBe("/api/v1/auth/refresh");
    expect(body(fetch, 2)).toEqual({ refresh_token: refreshToken, client_request_id: "refresh-request-123456" });
    expect(body(fetch, 3)).toEqual(body(fetch, 2));
  });

  it("rejects unsafe credential responses and malformed required fields without leaking secrets", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response(credentials, 200, false),
      response({ ...credentials, access_token: undefined }, 200, true),
    );
    const sdk = await ready(fetch);
    await expect(sdk.account.login({ identifier: "secret@example.test", credential: "secret-password" }))
      .rejects.toMatchObject({ code: "unsafe_session_response" });
    const failure = await sdk.account.login({ identifier: "secret@example.test", credential: "secret-password" }).catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(ClientSdkError);
    expect(String((failure as Error).message)).not.toContain("secret");
  });

  it("maps unknown response enums to unknown", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response({ ...credentials, user: { ...user, account_status: "future" } }, 200, true),
      response({ allowed: false, decision_stage: "future", reason_code: "FUTURE_REASON" }),
    );
    const sdk = await authenticated(fetch);
    expect(sdk.account.session?.user.accountStatus).toBe("unknown");
    await expect(sdk.account.getAccessSummary()).resolves.toEqual({ allowed: false, decisionStage: "unknown", reasonCode: "unknown" });
  });

  it("preserves a valid session on network failure, timeout, and cancellation", async () => {
    const pending = (_input: string | URL | Request, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
    });
    const fetch = vi.fn()
      .mockResolvedValueOnce(response(clientSession, 201, true))
      .mockResolvedValueOnce(response(credentials, 200, true))
      .mockRejectedValueOnce(new Error("contains " + accessToken))
      .mockImplementation(pending);
    const sdk = new ClientSdk({ baseUrl: "https://api.example.test", fetch, timeoutMs: 100, maxRetries: 0 });
    await sdk.establishSession(clientInput);
    await sdk.account.login({ identifier: "ada@example.test", credential: "password" });
    const networkFailure = await sdk.account.getProfile().catch((error: unknown) => error);
    expect(networkFailure).toMatchObject({ kind: "network" });
    expect((networkFailure as Error).cause).toBeUndefined();
    expect(sdk.account.session).not.toBeNull();

    const controller = new AbortController();
    const cancelled = sdk.account.getProfile({ signal: controller.signal });
    controller.abort();
    await expect(cancelled).rejects.toMatchObject({ kind: "cancelled" });
    expect(sdk.account.session).not.toBeNull();
    await expect(sdk.account.getProfile({ timeoutMs: 100 })).rejects.toMatchObject({ kind: "timeout" });
    expect(sdk.account.session).not.toBeNull();
  });

  it("clears memory and Vault on terminal authentication failure", async () => {
    const vault: AccountSessionVault = { load: vi.fn(), save: vi.fn(), clear: vi.fn() };
    const fetch = queued(
      response(clientSession, 201, true), response(credentials, 200, true),
      response({ code: "IDENTITY_SESSION_REVOKED", detail: "contains " + accessToken, retryable: false }, 401),
    );
    const sdk = await authenticated(fetch, vault);
    const failure = await sdk.account.getProfile().catch((error: unknown) => error);
    expect(failure).toMatchObject({ kind: "authentication", code: "IDENTITY_SESSION_REVOKED" });
    expect((failure as Error).message).toBe("The account request failed.");
    expect(sdk.account.session).toBeNull();
    expect(vault.clear).toHaveBeenCalled();
  });

  it("restores a valid Vault record without exposing or refreshing tokens", async () => {
    const record: AccountSessionRecord = {
      schemaVersion: 1, accessToken, refreshToken, accessExpiresAt: futureAccess, refreshExpiresAt: futureRefresh,
      user: {
        userId: "user-1", accountStatus: "active", displayName: "Ada", productId: null, tenantId: null,
        accessVersion: null, productAccessStatus: null, tenantAccessStatus: null,
      },
    };
    const vault: AccountSessionVault = { load: vi.fn(async () => record), save: vi.fn(), clear: vi.fn() };
    const fetch = queued();
    const sdk = new ClientSdk({ baseUrl: "https://api.example.test", fetch, accountSessionVault: vault });
    await expect(sdk.account.restoreSession()).resolves.toMatchObject({ user: { userId: "user-1" } });
    expect(fetch).not.toHaveBeenCalled();
  });

  it("treats Vault load failure as no restorable session and clears stale storage", async () => {
    const vault: AccountSessionVault = {
      load: vi.fn(async () => { throw new Error("secure storage unavailable"); }),
      save: vi.fn(),
      clear: vi.fn(),
    };
    const sdk = new ClientSdk({ baseUrl: "https://api.example.test", fetch: queued(), accountSessionVault: vault });

    await expect(sdk.account.restoreSession()).resolves.toBeNull();
    expect(vault.clear).toHaveBeenCalledTimes(1);
    expect(sdk.account.session).toBeNull();
  });

  it("clearSession clears memory and reports a stable Vault failure", async () => {
    const vault: AccountSessionVault = {
      load: vi.fn(),
      save: vi.fn(),
      clear: vi.fn(async () => { throw new Error("secure storage unavailable"); }),
    };
    const fetch = queued(response(clientSession, 201, true), response(credentials, 200, true));
    const sdk = await authenticated(fetch, vault);

    await expect(sdk.account.clearSession()).rejects.toMatchObject({
      kind: "unknown", code: "session_vault_error", retryable: false,
    });
    expect(sdk.account.session).toBeNull();
    expect(vault.clear).toHaveBeenCalledTimes(1);
  });

  it("refreshes an access-expired Vault record with one generated recovery request id", async () => {
    const record = {
      schemaVersion: 1, accessToken, refreshToken, accessExpiresAt: "2020-01-01T00:00:00Z",
      refreshExpiresAt: futureRefresh, user: {
        userId: "user-1", accountStatus: "active", displayName: null, productId: null, tenantId: null,
        accessVersion: null, productAccessStatus: null, tenantAccessStatus: null,
      },
    };
    const vault: AccountSessionVault = { load: vi.fn(async () => record), save: vi.fn(), clear: vi.fn() };
    const fetch = queued(response({
      access_token: "b".repeat(48), refresh_token: "s".repeat(48),
      access_expires_at: futureAccess, refresh_expires_at: futureRefresh,
    }, 200, true));
    const sdk = new ClientSdk({
      baseUrl: "https://api.example.test", fetch, accountSessionVault: vault,
      requestIdFactory: () => "r".repeat(16),
    });
    await sdk.account.restoreSession();
    expect(body(fetch, 0)).toMatchObject({ client_request_id: "r".repeat(16) });
    expect(vault.save).toHaveBeenCalled();
  });

  it("clears invalid or refresh-expired Vault records", async () => {
    for (const stored of [
      { schemaVersion: 2, accessToken, refreshToken },
      { schemaVersion: 1, accessToken, refreshToken, accessExpiresAt: futureAccess, refreshExpiresAt: "2020-01-01T00:00:00Z", user },
    ]) {
      const vault: AccountSessionVault = { load: vi.fn(async () => stored), save: vi.fn(), clear: vi.fn() };
      const sdk = new ClientSdk({ baseUrl: "https://api.example.test", fetch: queued(), accountSessionVault: vault });
      await expect(sdk.account.restoreSession()).resolves.toBeNull();
      expect(vault.clear).toHaveBeenCalled();
    }
  });

  it("preserves the session when logout has an indeterminate network failure", async () => {
    const fetch = queued(response(clientSession, 201, true), response(credentials, 200, true), new Error("offline"));
    const sdk = await authenticated(fetch, undefined, 0);
    await expect(sdk.account.logout()).rejects.toMatchObject({ kind: "network" });
    expect(sdk.account.session).not.toBeNull();
  });
  it("retries idempotent recovery completion with the same key", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response({ code: "temporarily_unavailable", retryable: true }, 503),
      response(undefined, 204),
    );
    const sdk = await ready(fetch, undefined, 1);
    await sdk.account.completeRecovery({
      continuationId: "recovery-1", recoveryProof: "proof-1234567890", newCredential: "new long password",
    }, { idempotencyKey: recoveryCompleteKey });
    expect(fetch).toHaveBeenCalledTimes(3);
    expect(headers(fetch, 1).get("Idempotency-Key")).toBe(recoveryCompleteKey);
    expect(headers(fetch, 2).get("Idempotency-Key")).toBe(recoveryCompleteKey);
  });

  it("clears a partially written Vault when saving credentials fails", async () => {
    const vault: AccountSessionVault = {
      load: vi.fn(),
      save: vi.fn(async () => { throw new Error("secure store failed"); }),
      clear: vi.fn(),
    };
    const fetch = queued(response(clientSession, 201, true), response(credentials, 200, true));
    const sdk = await ready(fetch, vault);
    await expect(sdk.account.login({ identifier: "ada@example.test", credential: "password" }))
      .rejects.toMatchObject({ code: "session_vault_error" });
    expect(sdk.account.session).toBeNull();
    expect(vault.clear).toHaveBeenCalled();
  });

  it("retains sessions for scoped suspension but clears global terminal identity failures", async () => {
    const vault: AccountSessionVault = { load: vi.fn(), save: vi.fn(), clear: vi.fn() };
    const fetch = queued(
      response(clientSession, 201, true), response(credentials, 200, true),
      response({ code: "PRODUCT_USER_ACCESS_SUSPENDED", retryable: false }, 403),
      response({ code: "IDENTITY_ACCOUNT_DISABLED", retryable: false }, 403),
    );
    const sdk = await authenticated(fetch, vault);
    await expect(sdk.account.getProfile()).rejects.toMatchObject({ code: "PRODUCT_USER_ACCESS_SUSPENDED" });
    expect(sdk.account.session).not.toBeNull();
    await expect(sdk.account.getProfile()).rejects.toMatchObject({ code: "IDENTITY_ACCOUNT_DISABLED" });
    expect(sdk.account.session).toBeNull();
    expect(vault.clear).toHaveBeenCalledTimes(1);
  });
  it("starts registration verification with ClientSessionBearer and keyed retry", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response({ code: "temporarily_unavailable", retryable: true }, 503),
      response({ accepted: true, continuation_id: "verification-1", future: true }, 202, true),
    );
    const sdk = await ready(fetch, undefined, 1);
    await expect(sdk.account.startRegistrationVerification(
      { identifier: "ada@example.test" },
      { idempotencyKey: verificationStartKey },
    )).resolves.toEqual({ accepted: true, continuationId: "verification-1" });
    expect(path(fetch, 1)).toBe("/api/v1/auth/verification/start");
    expect(headers(fetch, 1).get("Authorization")).toBe("Bearer " + clientToken);
    expect(headers(fetch, 2).get("Idempotency-Key")).toBe(verificationStartKey);
  });

  it("starts external login without replay and strictly parses the flow", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response({
        flow_id: "flow-1", mode: "redirect", authorization_url: "https://identity.example.test/authorize?client_id=public",
        expires_at: futureAccess, future: "ignored",
      }, 201, true),
    );
    const sdk = await ready(fetch, undefined, 2);
    await expect(sdk.account.startExternalLogin({
      provider: "oidc", mode: "redirect", returnTargetCode: "auth.default",
    })).resolves.toEqual({
      flowId: "flow-1", mode: "redirect", authorizationUrl: "https://identity.example.test/authorize?client_id=public",
      qrPayload: null, expiresAt: futureAccess,
    });
    expect(path(fetch, 1)).toBe("/api/v1/auth/external/oidc/start");
    expect(headers(fetch, 1).get("Authorization")).toBe("Bearer " + clientToken);
    expect(body(fetch, 1)).toEqual({ mode: "redirect", return_target_code: "auth.default" });
  });

  it("does not retry an external authentication attempt", async () => {
    const fetch = queued(response(clientSession, 201, true), new Error("indeterminate"));
    const sdk = await ready(fetch, undefined, 2);
    await expect(sdk.account.startExternalLogin({
      provider: "oidc", mode: "qr", returnTargetCode: "auth.default",
    })).rejects.toMatchObject({ kind: "network" });
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("keeps non-authenticated external exchange states explicit and accepts only valid authenticated sessions", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response({ status: "link_required", proof_id: "proof-1" }, 200, true),
      response({ status: "conflict" }, 200, true),
      response({ status: "review_required" }, 200, true),
      response({ status: "future_status", session: credentials }, 200, true),
      response({ status: "authenticated", session: credentials }, 200, true),
    );
    const sdk = await ready(fetch);
    await expect(sdk.account.completeExternalLogin({
      provider: "oidc", flowId: "flow-1", state: "s".repeat(32), code: "provider-code",
    })).resolves.toEqual({ status: "link_required", proofId: "proof-1" });
    expect(sdk.account.session).toBeNull();
    await expect(sdk.account.completeExternalLogin({
      provider: "oidc", flowId: "flow-2", state: "s".repeat(32), providerError: "access_denied",
    })).resolves.toEqual({ status: "conflict" });
    await expect(sdk.account.exchangeWechatCode({
      flowId: "flow-3", state: "s".repeat(32), code: "wechat-code",
    })).resolves.toEqual({ status: "review_required" });
    await expect(sdk.account.exchangeWechatCode({
      flowId: "flow-4", state: "s".repeat(32), code: "wechat-code",
    })).resolves.toEqual({ status: "unknown" });
    expect(sdk.account.session).toBeNull();
    await expect(sdk.account.completeExternalLogin({
      provider: "oidc", flowId: "flow-5", state: "s".repeat(32), code: "provider-code",
    })).resolves.toMatchObject({ status: "authenticated", session: { user: { userId: "user-1" } } });
    expect(sdk.account.session).not.toBeNull();
    expect([path(fetch, 1), path(fetch, 2), path(fetch, 3)]).toEqual([
      "/api/v1/auth/external/oidc/callback",
      "/api/v1/auth/external/oidc/callback",
      "/api/v1/auth/external/wechat/exchange",
    ]);
  });

  it("links with a keyed UserBearer write and unlinks without retry", async () => {
    const fetch = queued(
      response(clientSession, 201, true), response(credentials, 200, true),
      response({
        external_identity_id: "external-1", provider: "oidc", masked_subject: "a***",
        status: "active", linked_at: futureAccess, audit_id: "audit-1",
      }),
      response(undefined, 204),
    );
    const sdk = await authenticated(fetch);
    await expect(sdk.account.linkExternalIdentity(
      { provider: "oidc", externalProofId: "proof-1" },
      { idempotencyKey: linkIdentityKey },
    )).resolves.toMatchObject({ externalIdentityId: "external-1", auditId: "audit-1" });
    await sdk.account.unlinkExternalIdentity("external-1");
    expect([path(fetch, 2), path(fetch, 3)]).toEqual([
      "/api/v1/account/external-identities/oidc/link",
      "/api/v1/account/external-identities/external-1",
    ]);
    expect(headers(fetch, 2).get("Authorization")).toBe("Bearer " + accessToken);
    expect(headers(fetch, 2).get("Idempotency-Key")).toBe(linkIdentityKey);
    expect(headers(fetch, 3).get("Authorization")).toBe("Bearer " + accessToken);
  });
  it("rejects unsafe or contradictory authenticated external exchange responses", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response({ status: "authenticated", session: credentials }, 200, false),
      response({ status: "authenticated", session: credentials, proof_id: "proof-1" }, 200, true),
    );
    const sdk = await ready(fetch);
    await expect(sdk.account.completeExternalLogin({
      provider: "oidc", flowId: "flow-1", state: "s".repeat(32), code: "provider-code",
    })).rejects.toMatchObject({ code: "unsafe_session_response" });
    expect(sdk.account.session).toBeNull();
    await expect(sdk.account.completeExternalLogin({
      provider: "oidc", flowId: "flow-2", state: "s".repeat(32), code: "provider-code",
    })).rejects.toMatchObject({ code: "invalid_response" });
    expect(sdk.account.session).toBeNull();
  });
  it("rejects out-of-contract providers, external identity IDs, and unsafe authorization URLs", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response(credentials, 200, true),
      response({
        flow_id: "flow-unsafe", mode: "redirect",
        authorization_url: "https://user:password@identity.example.test/authorize#token",
        expires_at: futureAccess,
      }, 201, true),
    );
    const sdk = await authenticated(fetch);
    await expect(sdk.account.startExternalLogin({
      provider: "oidc/work" as never, mode: "redirect", returnTargetCode: "auth.default",
    })).rejects.toMatchObject({ kind: "validation", code: "invalid_request" });
    await expect(sdk.account.linkExternalIdentity(
      { provider: "oidc/work" as never, externalProofId: "proof-1" },
      { idempotencyKey: "link-invalid-0001" },
    )).rejects.toMatchObject({ kind: "validation", code: "invalid_request" });
    await expect(sdk.account.unlinkExternalIdentity("external/1"))
      .rejects.toMatchObject({ kind: "validation", code: "invalid_request" });
    expect(fetch).toHaveBeenCalledTimes(2);
    await expect(sdk.account.startExternalLogin({
      provider: "oidc", mode: "redirect", returnTargetCode: "https://evil.example/callback",
    })).rejects.toMatchObject({ kind: "validation", code: "invalid_request" });
    await expect(sdk.account.completeExternalLogin({
      provider: "oidc", flowId: "flow/invalid", state: "s".repeat(32), code: "provider-code",
    })).rejects.toMatchObject({ kind: "validation", code: "invalid_request" });
    expect(fetch).toHaveBeenCalledTimes(2);

    await expect(sdk.account.startExternalLogin({
      provider: "oidc", mode: "redirect", returnTargetCode: "auth.default",
    })).rejects.toMatchObject({ code: "invalid_response" });
    expect(fetch).toHaveBeenCalledTimes(3);
  });
  it("validates idempotency keys before a retryable request starts", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response({ accepted: true, continuation_id: "recovery-16" }, 202, true),
      response({ accepted: true, continuation_id: "recovery-128" }, 202, true),
    );
    const sdk = await ready(fetch);
    const start = (idempotencyKey: string) => sdk.account.startRecovery(
      { identifier: "ada@example.test" },
      { idempotencyKey },
    );

    await expect(start("k".repeat(15))).rejects.toMatchObject({ kind: "validation", code: "invalid_request" });
    await expect(start("k".repeat(129))).rejects.toMatchObject({ kind: "validation", code: "invalid_request" });
    expect(fetch).toHaveBeenCalledTimes(1);
    await expect(start("k".repeat(16))).resolves.toMatchObject({ continuationId: "recovery-16" });
    await expect(start("k".repeat(128))).resolves.toMatchObject({ continuationId: "recovery-128" });
    expect(headers(fetch, 1).get("Idempotency-Key")).toBe("k".repeat(16));
    expect(headers(fetch, 2).get("Idempotency-Key")).toBe("k".repeat(128));
  });

  it("persists one pending refresh request id across calls until success", async () => {
    const pendingRequestId = "p".repeat(128);
    const vault: AccountSessionVault = { load: vi.fn(), save: vi.fn(), clear: vi.fn() };
    const fetch = queued(
      response(clientSession, 201, true), response(credentials, 200, true),
      new Error("response lost"),
      response({
        access_token: "b".repeat(48), refresh_token: "s".repeat(48),
        access_expires_at: futureAccess, refresh_expires_at: futureRefresh,
      }, 200, true),
    );
    const sdk = await authenticated(fetch, vault, 0);

    await expect(sdk.account.refreshSession({ clientRequestId: "p".repeat(129) }))
      .rejects.toMatchObject({ kind: "validation", code: "invalid_request" });
    expect(fetch).toHaveBeenCalledTimes(2);
    await expect(sdk.account.refreshSession({ clientRequestId: pendingRequestId }))
      .rejects.toMatchObject({ kind: "network" });
    expect(vault.save).toHaveBeenLastCalledWith(expect.objectContaining({ pendingRefreshRequestId: pendingRequestId }));
    await expect(sdk.account.refreshSession({ clientRequestId: "x".repeat(15) }))
      .rejects.toMatchObject({ kind: "validation", code: "invalid_request" });
    expect(fetch).toHaveBeenCalledTimes(3);

    await expect(sdk.account.refreshSession()).resolves.toMatchObject({ user: { userId: "user-1" } });
    expect(body(fetch, 2)).toMatchObject({ client_request_id: pendingRequestId });
    expect(body(fetch, 3)).toEqual(body(fetch, 2));
    expect(vault.save).toHaveBeenLastCalledWith(expect.not.objectContaining({ pendingRefreshRequestId: expect.anything() }));
  });

  it("preserves a terminal identity error when Vault clearing fails", async () => {
    const vault: AccountSessionVault = {
      load: vi.fn(), save: vi.fn(), clear: vi.fn(async () => { throw new Error("vault unavailable"); }),
    };
    const fetch = queued(
      response(clientSession, 201, true), response(credentials, 200, true),
      response({ code: "IDENTITY_ACCOUNT_DISABLED", retryable: false }, 403),
    );
    const sdk = await authenticated(fetch, vault);

    await expect(sdk.account.getProfile()).rejects.toMatchObject({
      kind: "authentication", code: "IDENTITY_ACCOUNT_DISABLED",
    });
    expect(sdk.account.session).toBeNull();
    expect(vault.clear).toHaveBeenCalledTimes(1);
  });

  it("clears the local session after revoking the current session discovered by listing", async () => {
    const vault: AccountSessionVault = { load: vi.fn(), save: vi.fn(), clear: vi.fn() };
    const fetch = queued(
      response(clientSession, 201, true), response(credentials, 200, true),
      response({ items: [{
        session_id: "session-current", current: true, device_label: null,
        created_at: futureAccess, last_seen_at: futureAccess, expires_at: futureRefresh,
      }] }),
      response(undefined, 204),
    );
    const sdk = await authenticated(fetch, vault);

    await sdk.account.listSessions();
    await sdk.account.revokeSession("session-current");
    expect(sdk.account.session).toBeNull();
    expect(vault.clear).toHaveBeenCalledTimes(1);
  });
});
