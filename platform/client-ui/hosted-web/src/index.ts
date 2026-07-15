export type HostedRouteId = "hosted.auth" | "hosted.account" | "hosted.plans" | "hosted.checkout" | "hosted.cashier" | "hosted.payment-result";

export interface HostedLaunch {
  readonly routeId: HostedRouteId;
  readonly interactionId: string;
}

const routes: Readonly<Record<string, HostedRouteId>> = Object.freeze({
  "/ui/v1/auth": "hosted.auth",
  "/ui/v1/account": "hosted.account",
  "/ui/v1/plans": "hosted.plans",
  "/ui/v1/checkout": "hosted.checkout",
  "/ui/v1/cashier": "hosted.cashier",
  "/ui/v1/payment-result": "hosted.payment-result",
});
const interactionId = /^[A-Za-z0-9_-]{16,128}$/;
const forbidden = new Set(["product_id", "tenant_id", "application_id", "amount", "price", "return_url", "access_token", "refresh_token", "token"]);

export function parseHostedLaunch(input: string | URL): Readonly<HostedLaunch> {
  const url = input instanceof URL ? new URL(input.href) : new URL(input);
  const routeId = routes[url.pathname];
  if (!routeId) throw new TypeError("unsupported hosted UI route");
  if (url.username || url.password) throw new TypeError("hosted UI URL must not contain credentials");
  if (url.protocol !== "https:" && !(url.protocol === "http:" && (url.hostname === "127.0.0.1" || url.hostname === "localhost"))) {
    throw new TypeError("hosted UI requires HTTPS outside loopback development");
  }
  for (const key of url.searchParams.keys()) {
    if (forbidden.has(key.toLowerCase())) throw new TypeError(`hosted UI URL cannot contain ${key}`);
    if (key !== "interaction_id") throw new TypeError(`unsupported hosted UI query parameter: ${key}`);
  }
  const value = url.searchParams.getAll("interaction_id");
  if (value.length !== 1 || !interactionId.test(value[0] ?? "")) throw new TypeError("hosted UI requires one valid interaction_id");
  if (url.hash) throw new TypeError("hosted UI URL must not contain a fragment");
  return Object.freeze({ routeId, interactionId: value[0]! });
}
