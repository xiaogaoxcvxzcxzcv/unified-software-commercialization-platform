export type ClientErrorKind = "network" | "timeout" | "cancelled" | "rate_limited" | "server" | "authentication" | "authorization" | "not_found" | "conflict" | "validation" | "capability_disabled" | "unknown";

export interface ClientErrorDetails {
  readonly kind: ClientErrorKind;
  readonly code: string;
  readonly status?: number;
  readonly requestId?: string;
  readonly retryable: boolean;
  readonly retryAfterSeconds?: number;
  readonly cause?: unknown;
}

export class ClientSdkError extends Error {
  readonly kind: ClientErrorKind;
  readonly code: string;
  readonly status?: number;
  readonly requestId?: string;
  readonly retryable: boolean;
  readonly retryAfterSeconds?: number;

  constructor(message: string, details: ClientErrorDetails) {
    super(message, { cause: details.cause });
    this.name = "ClientSdkError";
    this.kind = details.kind;
    this.code = details.code;
    this.status = details.status;
    this.requestId = details.requestId;
    this.retryable = details.retryable;
    this.retryAfterSeconds = details.retryAfterSeconds;
  }
}

export function classifyStatus(status: number, code: string): ClientErrorKind {
  if (code === "capability_disabled") return "capability_disabled";
  if (status === 401) return "authentication";
  if (status === 403) return "authorization";
  if (status === 404) return "not_found";
  if (status === 409) return "conflict";
  if (status === 422 || status === 400) return "validation";
  if (status === 429) return "rate_limited";
  if (status >= 500) return "server";
  return "unknown";
}
