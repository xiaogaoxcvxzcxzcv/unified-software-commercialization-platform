import { describe, expect, it, vi } from "vitest";
import { ClientSdk } from "../src/index.js";

const clientToken = "c".repeat(48);
const accessToken = "a".repeat(48);
const refreshToken = "r".repeat(48);
const futureAccess = "2099-07-20T10:00:00Z";
const futureRefresh = "2099-08-20T10:00:00Z";
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
  access_token: accessToken,
  refresh_token: refreshToken,
  access_expires_at: futureAccess,
  refresh_expires_at: futureRefresh,
  user,
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
  const url = new URL(String(fetch.mock.calls[index]?.[0]));
  return `${url.pathname}${url.search}`;
}

async function ready(fetch: ReturnType<typeof queued>, maxRetries: 0 | 1 | 2 = 1) {
  const sdk = new ClientSdk({
    baseUrl: "https://api.example.test",
    fetch,
    maxRetries,
    requestIdFactory: () => "request-1234567890123456",
  });
  await sdk.establishSession(clientInput);
  return sdk;
}

async function authenticated(fetch: ReturnType<typeof queued>, maxRetries: 0 | 1 | 2 = 1) {
  const sdk = await ready(fetch, maxRetries);
  await sdk.account.login({ identifier: "ada@example.test", credential: "correct horse battery staple" });
  return sdk;
}

describe("ClientSdk entitlement", () => {
  it("requires the SDK-held user session and never accepts caller scope or bearer fields", async () => {
    const fetch = queued(response(clientSession, 201, true));
    const sdk = await ready(fetch);

    await expect(sdk.entitlement.getCurrentEntitlements()).rejects.toMatchObject({
      kind: "authentication",
      code: "user_session_required",
    });

    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("checks entitlements through UserBearer with only requested features and diagnostics", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response(credentials, 200, true),
      response({
        allowed: false,
        decision_stage: "entitlement",
        reason_code: "ENTITLEMENT_REQUIRED",
        revision: 2,
        plan_code: null,
        features: { export_pdf: { allowed: false } },
        valid_until: null,
        offline_grace_until: null,
        server_time: "2026-07-23T10:00:00Z",
        signed_decision: "signed-short-cache",
      }),
    );
    const sdk = await authenticated(fetch);

    await expect(sdk.entitlement.checkEntitlement({
      requestedFeatures: ["export_pdf"],
      deviceId: "device-1",
      clientTime: "2026-07-23T09:59:59Z",
    })).resolves.toMatchObject({
      allowed: false,
      reasonCode: "ENTITLEMENT_REQUIRED",
      revision: 2,
      serverTime: "2026-07-23T10:00:00Z",
      signedDecision: "signed-short-cache",
    });

    expect(path(fetch, 2)).toBe("/api/v1/entitlements/check");
    expect(headers(fetch, 2).get("Authorization")).toBe("Bearer " + accessToken);
    expect(body(fetch, 2)).toEqual({
      requested_features: ["export_pdf"],
      device_id: "device-1",
      client_time: "2026-07-23T09:59:59Z",
    });
    expect(body(fetch, 2)).not.toHaveProperty("product_id");
    expect(body(fetch, 2)).not.toHaveProperty("tenant_id");
    expect(body(fetch, 2)).not.toHaveProperty("user_id");
    expect(body(fetch, 2)).not.toHaveProperty("access_token");
  });

  it("reads current entitlements with current OpenAPI fields and treats cache as a zero-age UI hint", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response(credentials, 200, true),
      response({
        product_id: "prod-1",
        tenant_id: "tenant-1",
        user_id: "user-1",
        revision: 5,
        plan_code: "pro",
        features: { export_pdf: { allowed: true, limit: 100 } },
        valid_until: "2026-08-23T00:00:00Z",
        offline_grace_until: "2026-08-24T00:00:00Z",
        updated_at: "2026-07-23T10:00:00Z",
      }),
    );
    const sdk = await authenticated(fetch);

    await expect(sdk.entitlement.getCurrentEntitlements()).resolves.toMatchObject({
      revision: 5,
      planCode: "pro",
      validUntil: "2026-08-23T00:00:00Z",
      updatedAt: "2026-07-23T10:00:00Z",
    });
    expect(path(fetch, 2)).toBe("/api/v1/entitlements/current");
    expect(headers(fetch, 2).get("Authorization")).toBe("Bearer " + accessToken);
  });

  it("lists append-only history without turning historical grants into current authorization", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response(credentials, 200, true),
      response({
        items: [{
          ledger_id: "ledger-1",
          operation_type: "future-operation",
          operation_id: "op-1",
          source_type: "admin",
          source_id: "source-1",
          grant_id: "grant-1",
          before_revision: 1,
          after_revision: 2,
          audit_id: "audit-1",
          trace_id: "trace-1",
          created_at: "2026-07-23T10:00:00Z",
        }],
        next_cursor: "cursor-2",
      }),
    );
    const sdk = await authenticated(fetch);

    await expect(sdk.entitlement.listEntitlementHistory({ pageSize: 50, cursor: "cursor-1" })).resolves.toEqual({
      items: [expect.objectContaining({
        ledgerId: "ledger-1",
        operationType: "unknown",
        afterRevision: 2,
      })],
      nextCursor: "cursor-2",
    });
    expect(path(fetch, 2)).toBe("/api/v1/entitlements/history?page_size=50&cursor=cursor-1");
  });

  it("returns stable capability_disabled errors and does not replay cache on disabled capability", async () => {
    const fetch = queued(
      response(clientSession, 201, true),
      response(credentials, 200, true),
      response({ code: "capability_disabled", retryable: false, request_id: "req-disabled" }, 403),
    );
    const sdk = await authenticated(fetch);

    await expect(sdk.entitlement.getCurrentEntitlements()).rejects.toMatchObject({
      kind: "capability_disabled",
      code: "capability_disabled",
      requestId: "req-disabled",
      retryable: false,
    });
    expect(fetch).toHaveBeenCalledTimes(3);
  });

  it("validates feature and pagination input before starting requests", async () => {
    const fetch = queued(response(clientSession, 201, true), response(credentials, 200, true));
    const sdk = await authenticated(fetch);

    await expect(sdk.entitlement.checkEntitlement({ requestedFeatures: [] }))
      .rejects.toMatchObject({ kind: "validation", code: "invalid_request" });
    await expect(sdk.entitlement.checkEntitlement({ requestedFeatures: ["export_pdf", "export_pdf"] }))
      .rejects.toMatchObject({ kind: "validation", code: "invalid_request" });
    await expect(sdk.entitlement.listEntitlementHistory({ pageSize: 0 }))
      .rejects.toMatchObject({ kind: "validation", code: "invalid_request" });
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});
