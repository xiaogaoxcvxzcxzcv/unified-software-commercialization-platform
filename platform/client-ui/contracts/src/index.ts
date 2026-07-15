export type ClientBlockState = "idle" | "loading" | "ready" | "submitting" | "success" | "empty" | "failed" | "disabled";
export type ClientBlockErrorKind = "retryable" | "authentication_required" | "insufficient_entitlement" | "capability_disabled" | "terminal" | "unknown";

export interface ClientBlockError {
  readonly kind: ClientBlockErrorKind;
  readonly code: string;
  readonly message: string;
  readonly requestId?: string;
  readonly retryable: boolean;
}

export interface ClientBlockSnapshot<T> {
  readonly state: ClientBlockState;
  readonly data?: T;
  readonly error?: ClientBlockError;
  readonly revision: number;
}

export interface ClientBlockEvent<TPayload = unknown> {
  readonly blockId: string;
  readonly name: string;
  readonly payload: TPayload;
}

export interface ClientBlockContract {
  readonly blockId: string;
  readonly requiredCapability?: string;
  readonly events: readonly string[];
}

export interface ClientThemeTokens {
  readonly brandColor: string;
  readonly accentColor: string;
  readonly textColor: string;
  readonly mutedTextColor: string;
  readonly surfaceColor: string;
  readonly canvasColor: string;
  readonly borderColor: string;
  readonly dangerColor: string;
  readonly successColor: string;
  readonly focusColor: string;
  readonly radius: 0 | 2 | 4 | 6 | 8;
  readonly fontFamily: string;
}

export const defaultClientTheme: ClientThemeTokens = Object.freeze({
  brandColor: "#0f766e",
  accentColor: "#2563eb",
  textColor: "#18201f",
  mutedTextColor: "#5f6b69",
  surfaceColor: "#ffffff",
  canvasColor: "#f5f7f7",
  borderColor: "#d9e1df",
  dangerColor: "#b42318",
  successColor: "#157f3d",
  focusColor: "#1d4ed8",
  radius: 6,
  fontFamily: "Inter, system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif",
});

const stableId = /^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$/;

export function defineClientBlock(contract: ClientBlockContract): Readonly<ClientBlockContract> {
  if (!stableId.test(contract.blockId)) throw new TypeError("blockId must be a stable dotted identifier");
  if (contract.requiredCapability && !stableId.test(contract.requiredCapability)) throw new TypeError("requiredCapability must be a stable identifier");
  const events = [...new Set(contract.events)];
  if (events.some((event) => !stableId.test(event))) throw new TypeError("event names must be stable identifiers");
  return Object.freeze({ ...contract, events: Object.freeze(events) });
}
