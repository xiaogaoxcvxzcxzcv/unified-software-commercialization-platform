import { ClientSdkError } from "./errors.js";
import type {
  AccountCallOptions,
  EntitlementCheckDecision,
  EntitlementCheckInput,
  EntitlementHistoryEntry,
  EntitlementHistoryPage,
  EntitlementSummary,
  ListEntitlementHistoryInput,
} from "./types.js";

interface EntitlementTransportOptions extends AccountCallOptions {
  readonly method?: "GET" | "POST";
  readonly body?: unknown;
  readonly token?: string;
  readonly retry?: boolean;
}

interface EntitlementTransport {
  readonly withUser: <T>(operation: (accessToken: string) => Promise<T>) => Promise<T>;
  readonly send: (path: string, options: EntitlementTransportOptions) => Promise<Response>;
}

const reasonCodes = new Set([
  "ENTITLEMENT_REQUIRED",
  "ENTITLEMENT_EXPIRED",
  "ENTITLEMENT_DEVICE_LIMITED",
  "ENTITLEMENT_CAPABILITY_DISABLED",
]);
const operationTypes = new Set(["grant", "extend", "replace", "revoke", "expire"]);

function invalidResponse(): ClientSdkError {
  return new ClientSdkError("The entitlement service returned an invalid response.", {
    kind: "unknown", code: "invalid_response", retryable: false,
  });
}

function invalidInput(): ClientSdkError {
  return new ClientSdkError("The entitlement request is invalid.", {
    kind: "validation", code: "invalid_request", retryable: false,
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

function nullableString(value: unknown): string | null {
  return value === undefined || value === null ? null : string(value);
}

function timestamp(value: unknown): string {
  const result = string(value);
  if (!Number.isFinite(Date.parse(result))) throw invalidResponse();
  return result;
}

function nullableTimestamp(value: unknown): string | null {
  return value === undefined || value === null ? null : timestamp(value);
}

function revision(value: unknown): number {
  if (!Number.isInteger(value) || (value as number) < 0) throw invalidResponse();
  return value as number;
}

function featureMap(value: unknown): Readonly<Record<string, unknown>> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw invalidResponse();
  return Object.freeze({ ...(value as Record<string, unknown>) });
}

function known<T extends string>(value: unknown, values: ReadonlySet<string>): T | "unknown" {
  const parsed = string(value);
  return values.has(parsed) ? parsed as T : "unknown";
}

async function json(response: Response): Promise<unknown> {
  try { return JSON.parse(await response.text()) as unknown; } catch { throw invalidResponse(); }
}

function stableIdentifier(value: string): string {
  if (typeof value !== "string" || value.length < 1 || value.length > 128 || !/^[A-Za-z0-9][A-Za-z0-9._:-]*$/.test(value)) {
    throw invalidInput();
  }
  return value;
}

function query(input: ListEntitlementHistoryInput | undefined): string {
  const params = new URLSearchParams();
  if (input?.pageSize !== undefined) {
    if (!Number.isInteger(input.pageSize) || input.pageSize < 1 || input.pageSize > 200) throw invalidInput();
    params.set("page_size", String(input.pageSize));
  }
  if (input?.cursor !== undefined) params.set("cursor", stableIdentifier(input.cursor));
  const suffix = params.toString();
  return suffix ? `?${suffix}` : "";
}

function calls(options: AccountCallOptions | undefined): AccountCallOptions {
  return { signal: options?.signal, timeoutMs: options?.timeoutMs };
}

function parseDecision(value: unknown): EntitlementCheckDecision {
  const raw = object(value);
  if (raw.allowed !== true && raw.allowed !== false) throw invalidResponse();
  if (raw.decision_stage !== "entitlement") throw invalidResponse();
  const reason = raw.reason_code === null ? null : known<NonNullable<EntitlementCheckDecision["reasonCode"]>>(raw.reason_code, reasonCodes);
  return Object.freeze({
    allowed: raw.allowed,
    decisionStage: "entitlement",
    reasonCode: reason,
    revision: revision(raw.revision),
    planCode: nullableString(raw.plan_code),
    features: featureMap(raw.features),
    validUntil: nullableTimestamp(raw.valid_until),
    offlineGraceUntil: nullableTimestamp(raw.offline_grace_until),
    serverTime: timestamp(raw.server_time),
    signedDecision: raw.signed_decision === undefined ? null : string(raw.signed_decision),
  });
}

function parseSummary(value: unknown): EntitlementSummary {
  const raw = object(value);
  return Object.freeze({
    revision: revision(raw.revision),
    planCode: nullableString(raw.plan_code),
    features: featureMap(raw.features),
    validUntil: nullableTimestamp(raw.valid_until),
    offlineGraceUntil: nullableTimestamp(raw.offline_grace_until),
    updatedAt: timestamp(raw.updated_at),
  });
}

function parseHistoryEntry(value: unknown): EntitlementHistoryEntry {
  const raw = object(value);
  return Object.freeze({
    ledgerId: string(raw.ledger_id),
    operationType: known<Exclude<EntitlementHistoryEntry["operationType"], "unknown">>(raw.operation_type, operationTypes),
    operationId: string(raw.operation_id),
    sourceType: nullableString(raw.source_type),
    sourceId: nullableString(raw.source_id),
    grantId: string(raw.grant_id),
    beforeRevision: revision(raw.before_revision),
    afterRevision: revision(raw.after_revision),
    auditId: string(raw.audit_id),
    traceId: string(raw.trace_id),
    createdAt: timestamp(raw.created_at),
  });
}

export class EntitlementSdk {
  readonly #transport: EntitlementTransport;

  constructor(transport: EntitlementTransport) {
    this.#transport = transport;
  }

  async checkEntitlement(input: EntitlementCheckInput, options?: AccountCallOptions): Promise<EntitlementCheckDecision> {
    if (!Array.isArray(input.requestedFeatures) || input.requestedFeatures.length < 1 || input.requestedFeatures.length > 100) {
      throw invalidInput();
    }
    const unique = [...new Set(input.requestedFeatures.map(stableIdentifier))];
    if (unique.length !== input.requestedFeatures.length) throw invalidInput();
    const clientTime = input.clientTime === undefined ? undefined : timestamp(input.clientTime);
    const body = {
      requested_features: unique,
      ...(input.deviceId === undefined ? {} : { device_id: stableIdentifier(input.deviceId) }),
      ...(clientTime === undefined ? {} : { client_time: clientTime }),
    };
    return this.#transport.withUser(async (token) => parseDecision(await json(await this.#transport.send(
      "/api/v1/entitlements/check",
      { method: "POST", token, body, retry: true, ...calls(options) },
    ))));
  }

  async getCurrentEntitlements(options?: AccountCallOptions): Promise<EntitlementSummary> {
    return this.#transport.withUser(async (token) => parseSummary(await json(await this.#transport.send(
      "/api/v1/entitlements/current",
      { token, retry: true, ...calls(options) },
    ))));
  }

  async listEntitlementHistory(input: ListEntitlementHistoryInput = {}, options?: AccountCallOptions): Promise<EntitlementHistoryPage> {
    return this.#transport.withUser(async (token) => {
      const raw = object(await json(await this.#transport.send(
        `/api/v1/entitlements/history${query(input)}`,
        { token, retry: true, ...calls(options) },
      )));
      if (!Array.isArray(raw.items)) throw invalidResponse();
      return Object.freeze({
        items: Object.freeze(raw.items.map(parseHistoryEntry)),
        nextCursor: nullableString(raw.next_cursor),
      });
    });
  }
}
