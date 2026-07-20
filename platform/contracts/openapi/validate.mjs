import { readFile } from "node:fs/promises";

const contractUrl = new URL("./public-api.v1.json", import.meta.url);
const bytes = await readFile(contractUrl);
const decoded = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
const document = JSON.parse(decoded);
const errors = [];

if (!document.openapi?.startsWith("3.1.")) {
  errors.push(`expected OpenAPI 3.1.x, received ${document.openapi ?? "missing"}`);
}

const operationIds = new Map();
const methods = new Set(["get", "post", "put", "patch", "delete", "head", "options", "trace"]);
const requiredOperations = new Set([
  "GET /health/live",
  "GET /health/ready",
  "POST /api/v1/admin/products",
  "POST /api/v1/client/session",
  "PUT /api/v1/admin/products/{product_id}/capabilities",
  "POST /api/v1/admin/products/{product_id}/applications",
  "PUT /api/v1/admin/products/{product_id}/applications/{application_id}/redirects",
  "POST /api/v1/admin/products/{product_id}/applications/{application_id}/suspend",
  "POST /api/v1/admin/products/{product_id}/tenants",
  "POST /api/v1/admin/products/{product_id}/tenants/{tenant_id}/admins",
  "POST /api/v1/auth/register",
  "POST /api/v1/auth/login",
  "GET /api/v1/auth/session",
  "POST /api/v1/auth/recovery/start",
  "POST /api/v1/auth/recovery/complete",
  "POST /api/v1/auth/external/{provider}/start",
  "POST /api/v1/auth/external/{provider}/callback",
  "POST /api/v1/auth/verification/start",
  "POST /api/v1/auth/external/wechat/exchange",
  "POST /api/v1/account/external-identities/{provider}/link",
  "DELETE /api/v1/account/external-identities/{external_identity_id}",
  "GET /api/v1/account/external-identities",
  "GET /api/v1/account/profile",
  "PATCH /api/v1/account/profile",
  "PUT /api/v1/account/password",
  "GET /api/v1/account/sessions",
  "DELETE /api/v1/account/sessions/{session_id}",
  "GET /api/v1/account/access",
  "POST /api/v1/auth/refresh",
  "POST /api/v1/auth/logout",
  "POST /api/v1/hosted/interactions",
  "GET /api/v1/hosted/interactions/{interaction_id}",
  "POST /api/v1/hosted/interactions/{interaction_id}/browser-session",
  "POST /api/v1/hosted/interactions/{interaction_id}/auth/password",
  "POST /api/v1/hosted/interactions/{interaction_id}/account/complete",
  "POST /api/v1/hosted/interactions/{interaction_id}/cancel",
  "POST /api/v1/hosted/interactions/{interaction_id}/exchange",
  "POST /api/v1/admin/auth/login",
  "GET /api/v1/admin/auth/session",
  "POST /api/v1/admin/auth/refresh",
  "POST /api/v1/admin/auth/logout",
  "GET /api/v1/admin/users",
  "GET /api/v1/admin/products/{product_id}/users",
  "GET /api/v1/admin/products/{product_id}/tenants/{tenant_id}/users",
  "PUT /api/v1/admin/users/{user_id}/security-status",
  "PUT /api/v1/admin/products/{product_id}/users/{user_id}/access",
  "PUT /api/v1/admin/products/{product_id}/tenants/{tenant_id}/users/{user_id}/access",
  "POST /api/v1/admin/assembly-runs/{run_id}/cancel",
  "GET /api/v1/admin/assemblies/{assembly_id}/lifecycle-source",
  "POST /api/v1/admin/assemblies/{assembly_id}/upgrade-plans",
  "POST /api/v1/admin/assemblies/{assembly_id}/eject-plans",
  "GET /api/v1/admin/assembly-lifecycle-plans/{lifecycle_plan_id}",
  "POST /api/v1/admin/assembly-lifecycle-plans/{lifecycle_plan_id}/execute",
  "GET /api/v1/admin/assembly-lifecycle-operations/{operation_id}",
  "POST /api/v1/admin/assembly-lifecycle-operations/{operation_id}/cancel",
  "POST /api/v1/admin/assembly-lifecycle-operations/{operation_id}/rollback",
  "POST /api/v1/entitlements/check",
  "POST /api/v1/admin/entitlements",
  "GET /api/v1/devices",
  "POST /api/v1/devices/bind",
  "POST /api/v1/devices/{device_id}/heartbeat",
  "POST /api/v1/devices/{device_id}/revoke",
  "POST /api/v1/admin/license-batches",
  "POST /api/v1/admin/license-batches/{batch_id}/delivery",
  "POST /api/v1/admin/license-batches/{batch_id}/suspend",
  "POST /api/v1/licenses/redeem",
  "GET /api/v1/licenses/redemptions/{redemption_id}",
  "GET /api/v1/catalog/offers",
  "POST /api/v1/admin/products/{product_id}/catalog/offers",
  "POST /api/v1/admin/products/{product_id}/catalog/offers/{offer_id}/publish",
  "POST /api/v1/orders",
  "GET /api/v1/orders",
  "GET /api/v1/orders/{order_id}",
  "POST /api/v1/orders/{order_id}/cancel",
  "POST /api/v1/payments/intents",
  "POST /api/v1/payments/intents/{payment_intent_id}/cashier-sessions",
  "POST /api/v1/admin/payments/{payment_intent_id}/refunds",
  "POST /api/v1/ai/responses",
  "PUT /api/v1/admin/ai/model-routes/{logical_model_code}",
  "POST /api/v1/admin/usage/prices",
  "GET /api/v1/usage/records",
  "PUT /api/v1/admin/access/role-bindings/{binding_id}",
  "GET /api/v1/admin/audit/events"
]);
const foundOperations = new Set();
const accountAdminPolicies = new Map([
  ["GET /api/v1/admin/users", ["identity.user.read", "platform", false]],
  ["GET /api/v1/admin/products/{product_id}/users", ["identity.user.read", "product", false]],
  ["GET /api/v1/admin/products/{product_id}/tenants/{tenant_id}/users", ["identity.user.read", "tenant", false]],
  ["PUT /api/v1/admin/users/{user_id}/security-status", ["identity.security.manage", "platform", true]],
  ["PUT /api/v1/admin/products/{product_id}/users/{user_id}/access", ["product.user-access.manage", "product", true]],
  ["PUT /api/v1/admin/products/{product_id}/tenants/{tenant_id}/users/{user_id}/access", ["product.user-access.manage", "tenant", true]]
]);

for (const [path, pathItem] of Object.entries(document.paths ?? {})) {
  if (!path.startsWith("/")) errors.push(`invalid path: ${path}`);
  for (const [method, operation] of Object.entries(pathItem)) {
    if (!methods.has(method)) continue;
    const location = `${method.toUpperCase()} ${path}`;
    foundOperations.add(location);
    if (!operation.operationId) {
      errors.push(`${location} has no operationId`);
    } else if (operationIds.has(operation.operationId)) {
      errors.push(`${location} duplicates operationId ${operation.operationId} from ${operationIds.get(operation.operationId)}`);
    } else {
      operationIds.set(operation.operationId, location);
    }
    const parameters = [...(pathItem.parameters ?? []), ...(operation.parameters ?? [])];
    if (!parameters.some((parameter) => parameter.$ref === "#/components/parameters/RequestId" || parameter.name === "X-Request-Id")) {
      errors.push(`${location} does not declare X-Request-Id`);
    }
    if (!operation.responses?.default) errors.push(`${location} has no default error response`);
    const declaredPathParameters = new Set(parameters.filter((parameter) => parameter.in === "path").map((parameter) => parameter.name));
    for (const parameter of parameters.filter((parameter) => parameter.$ref?.startsWith("#/components/parameters/"))) {
      const key = parameter.$ref.split("/").at(-1);
      const resolved = document.components?.parameters?.[key];
      if (resolved?.in === "path") declaredPathParameters.add(resolved.name);
    }
    const pathVariables = [...path.matchAll(/\{([^}]+)\}/g)].map((match) => match[1]);
    for (const variable of pathVariables) {
      if (!declaredPathParameters.has(variable)) errors.push(`${location} does not declare path parameter ${variable}`);
    }
    if (!["get", "head", "options", "trace", "delete"].includes(method)) {
      const hasIdempotencyKey = parameters.some((parameter) => parameter.$ref === "#/components/parameters/IdempotencyKey" || parameter.name === "Idempotency-Key");
      if (!hasIdempotencyKey && !operation["x-idempotency-exemption"]) {
        errors.push(`${location} needs Idempotency-Key or x-idempotency-exemption`);
      }
    }
    const security = operation.security ?? [];
    const supportsAdminBearer = security.some((requirement) => Object.hasOwn(requirement, "AdminBearer"));
    const supportsAdminCookie = security.some((requirement) => Object.hasOwn(requirement, "AdminAccessCookie"));
    if (supportsAdminBearer && !supportsAdminCookie) {
      errors.push(`${location} supports AdminBearer but not the default AdminAccessCookie browser transport`);
    }
    const isUnsafeAdminCookieOperation = supportsAdminCookie
      && !["get", "head", "options", "trace"].includes(method)
      && location !== "POST /api/v1/admin/auth/refresh";
    if (isUnsafeAdminCookieOperation) {
      const hasAdminCsrfToken = parameters.some((parameter) => parameter.$ref === "#/components/parameters/AdminCsrfToken" || parameter.name === "X-CSRF-Token");
      if (!hasAdminCsrfToken) errors.push(`${location} needs conditional X-CSRF-Token for administrator Cookie transport`);
    }
    const accountPolicy = accountAdminPolicies.get(location);
    if (accountPolicy) {
      const [permission, scope, recentAuth] = accountPolicy;
      if (operation["x-required-permission"] !== permission) errors.push(`${location} must require permission ${permission}`);
      if (operation["x-required-scope"] !== scope) errors.push(`${location} must require ${scope} scope`);
      if (recentAuth && operation["x-recent-auth-required"] !== true) errors.push(`${location} must require recent authentication`);
    }
  }
}

for (const requiredOperation of requiredOperations) {
  if (!foundOperations.has(requiredOperation)) errors.push(`required operation is missing: ${requiredOperation}`);
}

const currentSessionResponse = document.paths?.["/api/v1/auth/session"]?.get?.responses?.["200"]?.$ref;
if (currentSessionResponse !== "#/components/responses/CurrentUserSession") {
  errors.push("GET /api/v1/auth/session must return the credential-free CurrentUserSession response");
}
const currentSessionProperties = document.components?.schemas?.CurrentUserSession?.properties ?? {};
for (const forbidden of ["access_token", "refresh_token", "token_pair"]) {
  if (Object.hasOwn(currentSessionProperties, forbidden)) errors.push(`CurrentUserSession must not contain ${forbidden}`);
}
for (const responseName of ["IssuedUserSession", "CurrentUserSession", "TokenPair", "RecoveryChallenge", "VerificationChallenge", "ExternalLoginFlow", "ExternalExchange", "HostedInteractionLaunch", "HostedInteraction", "HostedBrowserSession", "HostedCompletion", "HostedExchange"]) {
  const cacheControl = document.components?.responses?.[responseName]?.headers?.["Cache-Control"]?.schema?.const;
  if (cacheControl !== "no-store") errors.push(`${responseName} response must declare Cache-Control: no-store`);
}
if (document.components?.responses?.HostedError?.headers?.["Cache-Control"]?.schema?.const !== "no-store") {
  errors.push("HostedError response must declare Cache-Control: no-store");
}
const recoveryRequired = document.components?.schemas?.RecoveryChallenge?.required ?? [];
if (!recoveryRequired.includes("continuation_id")) errors.push("RecoveryChallenge must always return an opaque continuation_id");
const registerRequired = document.components?.schemas?.RegisterUserRequest?.required ?? [];
if (!registerRequired.includes("verification_continuation_id") || !registerRequired.includes("verification_proof")) {
  errors.push("RegisterUserRequest must bind both verification_continuation_id and verification_proof");
}
const registerOperation = document.paths?.["/api/v1/auth/register"]?.post;
const registerParameters = registerOperation?.parameters ?? [];
if (registerOperation?.["x-idempotency-exemption"] || !registerParameters.some((parameter) => parameter.$ref === "#/components/parameters/IdempotencyKey" || parameter.name === "Idempotency-Key")) {
  errors.push("user registration must require Idempotency-Key and cannot use a one-time exchange exemption");
}
const callbackSecurity = document.paths?.["/api/v1/auth/external/{provider}/callback"]?.post?.security ?? [];
if (!callbackSecurity.some((requirement) => Object.hasOwn(requirement, "ClientSessionBearer"))) {
  errors.push("external provider callback exchange must be bound to ClientSessionBearer");
}
const wechatExchange = document.paths?.["/api/v1/auth/external/wechat/exchange"]?.post;
const wechatParameters = wechatExchange?.parameters ?? [];
if (!wechatExchange?.["x-idempotency-exemption"] || wechatParameters.some((parameter) => parameter.$ref === "#/components/parameters/IdempotencyKey" || parameter.name === "Idempotency-Key")) {
  errors.push("WeChat exchange must use the one-time flow replay exemption rather than an Idempotency-Key");
}
const linkExternalProperties = document.components?.schemas?.LinkExternalIdentityRequest?.properties ?? {};
if (Object.hasOwn(linkExternalProperties, "recent_auth_proof")) {
  errors.push("external identity link must use server session auth_time, not client recent_auth_proof");
}
const externalStartCodeRef = document.components?.schemas?.StartExternalLoginRequest?.properties?.return_target_code?.$ref;
if (externalStartCodeRef !== "#/components/schemas/StableCode") {
  errors.push("external return_target_code must use StableCode");
}
const hostedCreate = document.paths?.["/api/v1/hosted/interactions"]?.post;
const hostedCreateSecurity = hostedCreate?.security ?? [];
if (!hostedCreateSecurity.some((item) => Object.hasOwn(item, "ClientSessionBearer")) || !hostedCreateSecurity.some((item) => Object.hasOwn(item, "UserBearer"))) {
  errors.push("hosted interaction creation must declare both route-specific ClientSessionBearer and UserBearer transports");
}
const hostedCreateProperties = document.components?.schemas?.CreateHostedInteractionRequest?.properties ?? {};
for (const forbidden of ["product_id", "application_id", "tenant_id", "user_id", "session_id", "return_target", "return_url", "return_uri"]) {
  if (Object.hasOwn(hostedCreateProperties, forbidden)) errors.push(`CreateHostedInteractionRequest must not accept ${forbidden}`);
}
for (const required of ["route_id", "channel", "return_target_code", "state"]) {
  if (!(document.components?.schemas?.CreateHostedInteractionRequest?.required ?? []).includes(required)) errors.push(`CreateHostedInteractionRequest must require ${required}`);
}
const hostedPassword = document.paths?.["/api/v1/hosted/interactions/{interaction_id}/auth/password"]?.post;
const hostedPasswordSecurity = hostedPassword?.security ?? [];
const hostedPasswordParameters = hostedPassword?.parameters ?? [];
if (hostedPasswordSecurity.length !== 1 || !Object.hasOwn(hostedPasswordSecurity[0] ?? {}, "HostedSessionCookie")) {
  errors.push("hosted password authentication must use only HostedSessionCookie");
}
if (!hostedPasswordParameters.some((parameter) => parameter.$ref === "#/components/parameters/HostedCsrfToken")) {
  errors.push("hosted password authentication must require HostedCsrfToken");
}
const hostedOpen = document.paths?.["/api/v1/hosted/interactions/{interaction_id}/browser-session"]?.post;
if ((hostedOpen?.security ?? null)?.length !== 0 || !hostedOpen?.["x-idempotency-exemption"]) {
  errors.push("hosted browser-session open must be anonymous interaction launch with explicit rotation exemption");
}
const hostedExchange = document.paths?.["/api/v1/hosted/interactions/{interaction_id}/exchange"]?.post;
const hostedExchangeSecurity = hostedExchange?.security ?? [];
if (hostedExchangeSecurity.length !== 1 || !Object.hasOwn(hostedExchangeSecurity[0] ?? {}, "ClientSessionBearer") || !hostedExchange?.["x-idempotency-exemption"]) {
  errors.push("hosted completion exchange must use only ClientSessionBearer and the one-time grant exemption");
}
for (const [path, method] of [
  ["/api/v1/hosted/interactions/{interaction_id}/auth/flow", "delete"],
  ["/api/v1/hosted/interactions/{interaction_id}/account/sessions/{session_id}", "delete"],
  ["/api/v1/hosted/interactions/{interaction_id}/cancel", "post"],
]) {
  const operation = document.paths?.[path]?.[method];
  const parameters = operation?.parameters ?? [];
  if (operation?.["x-idempotency-exemption"] || !parameters.some((parameter) => parameter.$ref === "#/components/parameters/IdempotencyKey" || parameter.name === "Idempotency-Key")) {
    errors.push(`${method.toUpperCase()} ${path} must require Idempotency-Key and cannot declare an exemption`);
  }
}const hostedCompletionProperties = Object.keys(document.components?.schemas?.HostedCompletion?.properties ?? {}).sort();
if (hostedCompletionProperties.join(",") !== ["expires_at", "interaction_id", "return_url", "status"].sort().join(",")) {
  errors.push("HostedCompletion may expose only interaction_id, status, return_url and expires_at");
}
if (document.components?.schemas?.HostedInteractionLaunch?.properties?.status?.const !== "created") {
  errors.push("HostedInteractionLaunch status must be const created");
}
if ((document.components?.schemas?.HostedExchangeResult?.oneOf ?? []).length !== 2) {
  errors.push("HostedExchangeResult must define exclusive user_session/account_completed oneOf branches");
}
for (const path of Object.keys(document.paths).filter((value) => value.startsWith("/api/v1/hosted/"))) {
  for (const operation of Object.values(document.paths[path] ?? {})) {
    if (!operation || typeof operation !== "object" || !operation.responses) continue;
    if (operation.responses.default?.$ref !== "#/components/responses/HostedError") {
      errors.push(`${path} Hosted operation must use the no-store HostedError response`);
    }
  }
}

function visit(value, location = "#") {
  if (!value || typeof value !== "object") return;
  if (typeof value.$ref === "string" && value.$ref.startsWith("#/")) {
    let target = document;
    for (const token of value.$ref.slice(2).split("/")) {
      const key = token.replaceAll("~1", "/").replaceAll("~0", "~");
      target = target?.[key];
    }
    if (target === undefined) errors.push(`${location} has unresolved ref ${value.$ref}`);
  }
  for (const [key, child] of Object.entries(value)) visit(child, `${location}/${key}`);
}
visit(document);

if (errors.length) {
  console.error(errors.map((error) => `- ${error}`).join("\n"));
  process.exitCode = 1;
} else {
  console.log(`OpenAPI contract valid: ${Object.keys(document.paths).length} paths, ${operationIds.size} operations, ${operationIds.size} unique operationIds.`);
}
