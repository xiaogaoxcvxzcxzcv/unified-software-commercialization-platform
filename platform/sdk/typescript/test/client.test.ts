import { describe, expect, it, vi } from "vitest";
import { ClientSdk, ClientSdkError } from "../src/index.js";

const session = (overrides: Record<string, unknown> = {}) => ({
  client_session_token: "t".repeat(48),
  expires_at: "2099-07-14T00:00:00Z",
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
  ...overrides,
});

const ok = (body: unknown, headers: Record<string, string> = {}) => new Response(JSON.stringify(body), {
  status: 201,
  headers: { "Content-Type": "application/json", "Cache-Control": "no-store", ...headers },
});

const input = {
  clientId: "client-1",
  credentialId: "credential-1",
  clientVersion: "1.0.0",
  requestNonce: "nonce-1234567890123456",
  clientProof: { schema_version: 1 as const, type: "hmac_sha256_v1" as const, value: "p".repeat(64), timestamp: "2026-07-14T00:00:00Z" },
};

describe("ClientSdk", () => {
  it("establishes one immutable trusted scope and ignores unknown response fields", async () => {
    const fetch = vi.fn(async () => ok({ ...session(), future_field: "ignored" }));
    const sdk = new ClientSdk({ baseUrl: "https://api.example.test", fetch, requestIdFactory: () => "request-1" });
    const context = await sdk.establishSession(input);

    expect(context.product.productId).toBe("prod-1");
    expect(context.tenant.tenantId).toBe("tenant-1");
    expect(Object.isFrozen(context.toJSON())).toBe(true);
    const request = fetch.mock.calls[0]?.[1] as RequestInit;
    expect(new Headers(request.headers).get("Authorization")).toBeNull();
    expect(request.body).not.toContain("product_id");
    expect(request.body).not.toContain("tenant_id");
  });

  it("maps unknown enum values to unknown without granting a known state", async () => {
    const value = session();
    (value.application_context as Record<string, unknown>).platform = "future-platform";
    (value.tenant_context as Record<string, unknown>).tenant_status = "future-status";
    const sdk = new ClientSdk({ baseUrl: "https://api.example.test", fetch: async () => ok(value) });
    const context = await sdk.establishSession(input);
    expect(context.application.platform).toBe("unknown");
    expect(context.tenant.tenantStatus).toBe("unknown");
  });

  it("rejects mismatched Product/Application/Tenant scope", async () => {
    const value = session();
    (value.tenant_context as Record<string, unknown>).product_id = "prod-2";
    const sdk = new ClientSdk({ baseUrl: "https://api.example.test", fetch: async () => ok(value) });
    await expect(sdk.establishSession(input)).rejects.toThrow("scope mismatch");
    expect(sdk.context).toBeNull();
  });

  it("keeps the token in the authorization header and blocks caller scope overrides", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(ok(session()))
      .mockResolvedValueOnce(new Response(JSON.stringify({ enabled: true }), { status: 200, headers: { "Content-Type": "application/json" } }));
    const sdk = new ClientSdk({ baseUrl: "https://api.example.test", fetch });
    await sdk.establishSession(input);
    await expect(sdk.request("/api/v1/config", { headers: { "X-Tenant-ID": "tenant-2" } })).rejects.toThrow("reserved header");
    await expect(sdk.request("https://other.example/api/v1/config")).rejects.toThrow("/api/v1/ relative path");
    await sdk.request("/api/v1/config");
    const request = fetch.mock.calls[1]?.[1] as RequestInit;
    expect(new Headers(request.headers).get("Authorization")).toBe(`Bearer ${"t".repeat(48)}`);
  });

  it("retries safe network failures but never retries an unkeyed write", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(ok(session()))
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(new Response("{}", { status: 200 }))
      .mockRejectedValueOnce(new Error("offline"));
    const sdk = new ClientSdk({ baseUrl: "https://api.example.test", fetch, maxRetries: 1 });
    await sdk.establishSession(input);
    await expect(sdk.request("/api/v1/config")).resolves.toEqual({});
    await expect(sdk.request("/api/v1/action", { method: "POST", body: {} })).rejects.toMatchObject({ kind: "network" });
    expect(fetch).toHaveBeenCalledTimes(4);
  });

  it("returns stable classified server errors without leaking request bodies", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(ok(session()))
      .mockResolvedValueOnce(new Response(JSON.stringify({ title: "Disabled", status: 403, code: "capability_disabled", request_id: "req-2", retryable: false }), { status: 403, headers: { "Content-Type": "application/problem+json" } }));
    const sdk = new ClientSdk({ baseUrl: "https://api.example.test", fetch });
    await sdk.establishSession(input);
    const failure = await sdk.request("/api/v1/entitlements").catch((reason: unknown) => reason);
    expect(failure).toBeInstanceOf(ClientSdkError);
    expect(failure).toMatchObject({ kind: "capability_disabled", code: "capability_disabled", requestId: "req-2", retryable: false });
  });

  it("requires no-store on client-session responses and rejects insecure remote base URLs", async () => {
    expect(() => new ClientSdk({ baseUrl: "http://api.example.test" })).toThrow("HTTPS");
    const sdk = new ClientSdk({ baseUrl: "http://127.0.0.1:8080", fetch: async () => ok(session(), { "Cache-Control": "private" }) });
    await expect(sdk.establishSession(input)).rejects.toMatchObject({ code: "unsafe_session_response" });
  });

  it("distinguishes caller cancellation from timeout", async () => {
    const pendingResponse = (_input: string | URL | Request, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")), { once: true });
    });
    const fetch = vi.fn()
      .mockResolvedValueOnce(ok(session()))
      .mockImplementation(pendingResponse);
    const sdk = new ClientSdk({ baseUrl: "https://api.example.test", fetch, timeoutMs: 100 });
    await sdk.establishSession(input);

    const caller = new AbortController();
    const cancelled = sdk.request("/api/v1/config", { signal: caller.signal });
    caller.abort();
    await expect(cancelled).rejects.toMatchObject({ kind: "cancelled", code: "request_cancelled", retryable: false });

    await expect(sdk.request("/api/v1/config", { timeoutMs: 100 })).rejects.toMatchObject({ kind: "timeout", code: "request_timeout", retryable: true });
  });
});
