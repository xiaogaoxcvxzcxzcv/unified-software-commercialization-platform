import { AccountSdk, accountUserSession } from "./account.js";
import { EntitlementSdk } from "./entitlement.js";
import { TrustedClientContext } from "./context.js";
import { classifyStatus, ClientSdkError } from "./errors.js";
import type { ClientRequestOptions, ClientSdkOptions, CreateClientSessionInput, FetchLike } from "./types.js";

interface SessionState {
  readonly token: string;
  readonly context: TrustedClientContext;
}

interface ErrorEnvelope {
  readonly title?: unknown;
  readonly detail?: unknown;
  readonly status?: unknown;
  readonly code?: unknown;
  readonly request_id?: unknown;
  readonly retryable?: unknown;
  readonly retry_after_seconds?: unknown;
}

const reservedHeaders = new Set(["authorization", "cookie", "x-product-id", "x-tenant-id", "x-application-id", "x-client-session"]);
const retryStatuses = new Set([429, 502, 503, 504]);

function normalizeBaseUrl(input: string): URL {
  const url = new URL(input);
  if (url.username || url.password || url.search || url.hash) throw new TypeError("baseUrl must not contain credentials, query, or fragment");
  const loopback = url.hostname === "localhost" || url.hostname === "127.0.0.1" || url.hostname === "[::1]";
  if (url.protocol !== "https:" && !(url.protocol === "http:" && loopback)) throw new TypeError("baseUrl must use HTTPS outside loopback development");
  url.pathname = url.pathname.replace(/\/+$/, "");
  return url;
}

function normalizeTimeout(value: number | undefined, fallback: number): number {
  const timeout = value ?? fallback;
  if (!Number.isInteger(timeout) || timeout < 100 || timeout > 60_000) throw new TypeError("timeoutMs must be an integer between 100 and 60000");
  return timeout;
}

function normalizePath(path: string): string {
  if (!path.startsWith("/api/v1/") || path.includes("#")) throw new TypeError("client request path must be an /api/v1/ relative path");
  const parsed = new URL(path, "https://sdk.invalid");
  if (parsed.origin !== "https://sdk.invalid") throw new TypeError("absolute client request URLs are not allowed");
  return `${parsed.pathname}${parsed.search}`;
}

function requestHeaders(input: Readonly<Record<string, string>> | undefined): Headers {
  const headers = new Headers(input);
  for (const name of headers.keys()) {
    if (reservedHeaders.has(name.toLowerCase())) throw new TypeError(`caller cannot set reserved header: ${name}`);
  }
  return headers;
}

function safeMethod(method: string, idempotencyKey?: string): boolean {
  return method === "GET" || method === "HEAD" || Boolean(idempotencyKey);
}

async function safeJson(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) return undefined;
  try { return JSON.parse(text) as unknown; } catch { throw new ClientSdkError("The server returned an invalid response.", { kind: "unknown", code: "invalid_response", status: response.status, retryable: false }); }
}

export class ClientSdk {
  readonly account: AccountSdk;
  readonly entitlement: EntitlementSdk;
  readonly #baseUrl: URL;
  readonly #fetch: FetchLike;
  readonly #timeoutMs: number;
  readonly #maxRetries: number;
  readonly #requestIdFactory: () => string;
  #session?: SessionState;

  constructor(options: ClientSdkOptions) {
    this.#baseUrl = normalizeBaseUrl(options.baseUrl);
    this.#fetch = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.#timeoutMs = normalizeTimeout(options.timeoutMs, 10_000);
    this.#maxRetries = options.maxRetries ?? 1;
    this.#requestIdFactory = options.requestIdFactory ?? (() => globalThis.crypto.randomUUID());
    this.account = new AccountSdk({
      clientToken: () => this.#clientToken(),
      requestId: () => this.#requestIdFactory(),
      send: (path, accountOptions) => this.#send(path, {
        method: accountOptions.method,
        body: accountOptions.body,
        idempotencyKey: accountOptions.idempotencyKey,
        timeoutMs: accountOptions.timeoutMs,
        signal: accountOptions.signal,
      }, accountOptions.token, false, accountOptions.retry === true, true),
    }, options.accountSessionVault);
    this.entitlement = new EntitlementSdk({
      withUser: (operation) => this.account[accountUserSession](operation),
      send: (path, entitlementOptions) => this.#send(path, {
        method: entitlementOptions.method,
        body: entitlementOptions.body,
        timeoutMs: entitlementOptions.timeoutMs,
        signal: entitlementOptions.signal,
      }, entitlementOptions.token, false, entitlementOptions.retry === true, true),
    });
  }

  get context(): TrustedClientContext | null { return this.#session?.context ?? null; }

  clearSession(): void { this.#session = undefined; }

  async establishSession(input: CreateClientSessionInput, signal?: AbortSignal): Promise<TrustedClientContext> {
    const response = await this.#send("/api/v1/client/session", {
      method: "POST",
      body: {
        client_id: input.clientId,
        credential_id: input.credentialId,
        client_proof: input.clientProof,
        client_version: input.clientVersion,
        request_nonce: input.requestNonce,
        ...(input.deviceSummary ? { device_summary: input.deviceSummary } : {}),
        ...(input.channelProof ? { channel_proof: input.channelProof } : {}),
      },
      signal,
    }, undefined, true);
    const cacheControl = response.headers.get("cache-control")?.toLowerCase() ?? "";
    if (!cacheControl.split(",").some((item) => item.trim() === "no-store")) {
      throw new ClientSdkError("The client session response was not marked no-store.", { kind: "unknown", code: "unsafe_session_response", status: response.status, retryable: false });
    }
    const parsed = TrustedClientContext.parse(await safeJson(response));
    this.#session = Object.freeze(parsed);
    return parsed.context;
  }

  async request<T>(path: string, options: ClientRequestOptions = {}): Promise<T> {
    const session = this.#session;
    if (!session) throw new ClientSdkError("A trusted client session is required.", { kind: "authentication", code: "client_session_required", retryable: false });
    if (Date.parse(session.context.expiresAt) <= Date.now()) {
      this.clearSession();
      throw new ClientSdkError("The client session has expired.", { kind: "authentication", code: "client_session_expired", retryable: false });
    }
    const response = await this.#send(normalizePath(path), options, session.token, false);
    return await safeJson(response) as T;
  }

  #clientToken(): string {
    const session = this.#session;
    if (!session) throw new ClientSdkError("A trusted client session is required.", { kind: "authentication", code: "client_session_required", retryable: false });
    if (Date.parse(session.context.expiresAt) <= Date.now()) {
      this.clearSession();
      throw new ClientSdkError("The client session has expired.", { kind: "authentication", code: "client_session_expired", retryable: false });
    }
    return session.token;
  }

  async #send(path: string, options: ClientRequestOptions, token: string | undefined, sessionRequest: boolean, retryOverride?: boolean, redactError = false): Promise<Response> {
    const method = options.method ?? "GET";
    const headers = requestHeaders(options.headers);
    headers.set("Accept", "application/json");
    headers.set("X-Request-ID", this.#requestIdFactory());
    if (token) headers.set("Authorization", `Bearer ${token}`);
    if (options.idempotencyKey) headers.set("Idempotency-Key", options.idempotencyKey);
    let body: string | undefined;
    if (options.body !== undefined) {
      headers.set("Content-Type", "application/json");
      body = JSON.stringify(options.body);
    }
    const canRetry = retryOverride ?? (sessionRequest || safeMethod(method, options.idempotencyKey));
    const attempts = canRetry ? this.#maxRetries + 1 : 1;
    for (let attempt = 0; attempt < attempts; attempt += 1) {
      const controller = new AbortController();
      const timeout = globalThis.setTimeout(() => controller.abort("timeout"), normalizeTimeout(options.timeoutMs, this.#timeoutMs));
      const cancel = () => controller.abort("cancelled");
      options.signal?.addEventListener("abort", cancel, { once: true });
      try {
        const response = await this.#fetch(new URL(path, this.#baseUrl), { method, headers, body, signal: controller.signal });
        if (response.ok) return response;
        const payload = await safeJson(response) as ErrorEnvelope | undefined;
        const code = typeof payload?.code === "string" ? payload.code : "http_error";
        const retryable = payload?.retryable === true && retryStatuses.has(response.status) && attempt + 1 < attempts;
        if (retryable) continue;
        const message = redactError ? "The account request failed." : typeof payload?.detail === "string" ? payload.detail : typeof payload?.title === "string" ? payload.title : "The request failed.";
        throw new ClientSdkError(message, {
          kind: classifyStatus(response.status, code), code, status: response.status,
          requestId: typeof payload?.request_id === "string" ? payload.request_id : response.headers.get("x-request-id") ?? undefined,
          retryable: payload?.retryable === true,
          retryAfterSeconds: typeof payload?.retry_after_seconds === "number" ? payload.retry_after_seconds : undefined,
        });
      } catch (reason) {
        if (reason instanceof ClientSdkError) throw reason;
        if (controller.signal.aborted) {
          const cancelled = options.signal?.aborted === true;
          throw new ClientSdkError(cancelled ? "The request was cancelled." : "The request timed out.", {
            kind: cancelled ? "cancelled" : "timeout", code: cancelled ? "request_cancelled" : "request_timeout", retryable: !cancelled, cause: redactError ? undefined : reason,
          });
        }
        if (attempt + 1 < attempts) continue;
        throw new ClientSdkError("The network request failed.", { kind: "network", code: "network_error", retryable: canRetry, cause: redactError ? undefined : reason });
      } finally {
        globalThis.clearTimeout(timeout);
        options.signal?.removeEventListener("abort", cancel);
      }
    }
    throw new ClientSdkError("The request could not be completed.", { kind: "unknown", code: "request_failed", retryable: false });
  }
}
