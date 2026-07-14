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
  "POST /api/v1/auth/login",
  "POST /api/v1/auth/external/{provider}/start",
  "POST /api/v1/auth/external/wechat/exchange",
  "POST /api/v1/account/external-identities/{provider}/link",
  "DELETE /api/v1/account/external-identities/{external_identity_id}",
  "POST /api/v1/auth/refresh",
  "POST /api/v1/auth/logout",
  "POST /api/v1/admin/auth/login",
  "GET /api/v1/admin/auth/session",
  "POST /api/v1/admin/auth/refresh",
  "POST /api/v1/admin/auth/logout",
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
  }
}

for (const requiredOperation of requiredOperations) {
  if (!foundOperations.has(requiredOperation)) errors.push(`required operation is missing: ${requiredOperation}`);
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
