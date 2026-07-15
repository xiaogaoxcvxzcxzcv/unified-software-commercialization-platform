import { authenticatedAdminRequest } from "./authClient";

export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };
export type JsonObject = { [key: string]: JsonValue };

export type AssemblyEnvironment = "development" | "test" | "staging" | "production";

export interface TrustedToolSelection {
  id: string;
  version: string;
}

export interface ProductBlueprintDocument extends JsonObject {
  generator: TrustedToolSelection & JsonObject;
  sdk: TrustedToolSelection & JsonObject;
}

export interface OutputTargetSummary {
  output_target_ref: string;
  display_name: string;
  summary: string;
  is_default: boolean;
}

export interface OutputTargetCatalog {
  environment: AssemblyEnvironment;
  default_policy: "explicit";
  default_output_target_ref: string | null;
  items: OutputTargetSummary[];
}

export interface BlueprintRecord {
  blueprint_id: string;
  version: number;
  schema_version: string;
  document: ProductBlueprintDocument;
  checksum: string;
  created_at: string;
  updated_at: string;
  audit_id: string;
}

export interface AssemblyPlanRecord {
  plan_id: string;
  version: number;
  blueprint_id: string;
  blueprint_version: number;
  environment: AssemblyEnvironment;
  document: JsonObject;
  checksum: string;
  executable: boolean;
  confirmed: boolean;
  created_at: string;
  updated_at: string;
  audit_id: string;
}

export type AssemblyRunStatus = "planned" | "provisioning" | "generating" | "validating" | "completed" | "failed" | "rolling_back" | "rolled_back";

export interface AssemblyRunRecord {
  run_id: string;
  plan_id: string;
  plan_version: number;
  plan_checksum: string;
  output_target_ref: string;
  status: AssemblyRunStatus;
  document: JsonObject;
  created_at: string;
  updated_at: string;
  completed_at?: string | null;
  audit_id: string;
  manifest_url?: string | null;
  lock_url?: string | null;
}

export interface AssemblyManifestRecord {
  assembly_id: string;
  product_id: string;
  run_id: string;
  schema_version: string;
  document: JsonObject;
  document_checksum: string;
  checksum: string;
  created_at: string;
}

export interface GeneratedProjectLockRecord {
  lock_id: string;
  product_id: string;
  run_id: string;
  assembly_id: string;
  schema_version: string;
  document: JsonObject;
  document_checksum: string;
  checksum: string;
  created_at: string;
}

export interface AssemblyRequestOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
}

export interface AssemblyWriteOptions extends AssemblyRequestOptions {
  idempotencyKey: string;
}

export interface CreatePlanInput {
  blueprint_version: number;
  environment: AssemblyEnvironment;
}

export interface StartAssemblyInput {
  plan_id: string;
  expected_plan_version: number;
  plan_checksum: string;
  confirmation: {
    accepted: true;
    summary_checksum: string;
  };
  output_target_ref: string;
}

const trustedToolKeys = new Set(["id", "version"]);
const identifierPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
const outputTargetRefPattern = /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/;
const idempotencyKeyPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]{15,127}$/;
const forbiddenToolKeys = new Set([
  "scope",
  "checksum",
  "content",
  "content_files",
  "adapter",
  "command",
  "path",
  "entrypoint",
  "execution",
  "manifest_sha256",
  "tree_sha256",
]);
const forbiddenBlueprintKeys = new Set([
  "catalog_scope",
  "catalog_visibility",
  "catalog_readiness",
  "generator_path",
  "generator_checksum",
  "sdk_path",
  "sdk_checksum",
]);

function assertNonEmpty(value: string, field: string) {
  if (!value.trim()) throw new TypeError(`${field} must not be empty`);
}

function assertIdentifier(value: string, field: string) {
  assertNonEmpty(value, field);
  if (!identifierPattern.test(value)) throw new TypeError(`${field} is invalid`);
}

function containsForbiddenDisplayCharacter(value: string) {
  return value.includes("/") || value.includes("\\")
    || [...value].some((character) => {
      const codePoint = character.codePointAt(0)!;
      return codePoint <= 0x1f || codePoint === 0x7f;
    });
}

function assertToolSelection(value: unknown, field: "generator" | "sdk"): asserts value is TrustedToolSelection {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new TypeError(`${field} must be a trusted tool selection`);
  }
  const candidate = value as Record<string, unknown>;
  for (const key of Object.keys(candidate)) {
    if (!trustedToolKeys.has(key) || forbiddenToolKeys.has(key.toLowerCase())) {
      throw new TypeError(`${field}.${key} is not accepted from the browser`);
    }
  }
  if (Object.keys(candidate).length !== trustedToolKeys.size
    || typeof candidate.id !== "string"
    || typeof candidate.version !== "string") {
    throw new TypeError(`${field} must contain only id and version`);
  }
  assertNonEmpty(candidate.id, `${field}.id`);
  assertNonEmpty(candidate.version, `${field}.version`);
}

export function assertTrustedToolSelections(document: ProductBlueprintDocument) {
  for (const key of Object.keys(document)) {
    if (forbiddenBlueprintKeys.has(key.toLowerCase())) throw new TypeError(`${key} is not accepted from the browser`);
  }
  assertToolSelection(document.generator, "generator");
  assertToolSelection(document.sdk, "sdk");
}

function writeInit(body: JsonValue, options: AssemblyWriteOptions): RequestInit {
  if (!idempotencyKeyPattern.test(options.idempotencyKey)) throw new TypeError("idempotencyKey is invalid");
  return {
    method: "POST",
    headers: { "Idempotency-Key": options.idempotencyKey },
    body: JSON.stringify(body),
    signal: requestSignal(options),
  };
}

function readInit(options: AssemblyRequestOptions): RequestInit {
  return { signal: requestSignal(options) };
}

function requestSignal(options: AssemblyRequestOptions) {
  if (options.timeoutMs === undefined) return options.signal;
  if (!Number.isInteger(options.timeoutMs) || options.timeoutMs < 1 || options.timeoutMs > 120_000) {
    throw new TypeError("timeoutMs must be an integer between 1 and 120000");
  }
  const timeout = AbortSignal.timeout(options.timeoutMs);
  return options.signal ? AbortSignal.any([options.signal, timeout]) : timeout;
}

function stripOutputTarget(item: unknown): OutputTargetSummary {
  if (!item || typeof item !== "object" || Array.isArray(item)) {
    throw new TypeError("output target item is invalid");
  }
  const source = item as Record<string, unknown>;
  const keys = Object.keys(source).sort();
  const expectedKeys = ["display_name", "is_default", "output_target_ref", "summary"];
  if (keys.length !== expectedKeys.length || keys.some((key, index) => key !== expectedKeys[index])) {
    throw new TypeError("output target contains unknown or missing fields");
  }
  for (const field of ["output_target_ref", "display_name", "summary"] as const) {
    if (typeof source[field] !== "string" || !source[field].trim()) throw new TypeError(`output target ${field} is invalid`);
  }
  if ((source.output_target_ref as string).length < 3
    || (source.output_target_ref as string).length > 128
    || [...(source.display_name as string)].length > 120
    || [...(source.summary as string)].length > 240) {
    throw new TypeError("output target field exceeds its contract limit");
  }
  if ([source.display_name, source.summary].some((field) => containsForbiddenDisplayCharacter(field as string))) {
    throw new TypeError("output target display metadata must not contain a host path");
  }
  if (!outputTargetRefPattern.test(source.output_target_ref as string)) throw new TypeError("output target output_target_ref is invalid");
  if (typeof source.is_default !== "boolean") {
    throw new TypeError("output target is_default is invalid");
  }
  return {
    output_target_ref: source.output_target_ref as string,
    display_name: source.display_name as string,
    summary: source.summary as string,
    is_default: source.is_default as boolean,
  };
}

function parseOutputTargetCatalog(value: unknown): OutputTargetCatalog {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new TypeError("output target catalog is invalid");
  }
  const source = value as Record<string, unknown>;
  const keys = Object.keys(source).sort();
  const expectedKeys = ["default_output_target_ref", "default_policy", "environment", "items"];
  if (keys.length !== expectedKeys.length || keys.some((key, index) => key !== expectedKeys[index])) {
    throw new TypeError("output target catalog contains unknown or missing fields");
  }
  if (source.default_policy !== "explicit") {
    throw new TypeError("output target catalog must require an explicit selection");
  }
  if (source.environment !== "development" && source.environment !== "test" && source.environment !== "staging" && source.environment !== "production") {
    throw new TypeError("output target catalog environment is invalid");
  }
  if (source.default_output_target_ref !== null && typeof source.default_output_target_ref !== "string") {
    throw new TypeError("output target catalog default is invalid");
  }
  if (typeof source.default_output_target_ref === "string" && !outputTargetRefPattern.test(source.default_output_target_ref)) {
    throw new TypeError("output target catalog default is invalid");
  }
  if (!Array.isArray(source.items)) throw new TypeError("output target catalog items are invalid");
  const items = source.items.map(stripOutputTarget);
  if (new Set(items.map((item) => item.output_target_ref)).size !== items.length) {
    throw new TypeError("output target catalog contains duplicate references");
  }
  if (items.some((item, index) => index > 0 && items[index - 1].output_target_ref >= item.output_target_ref)) {
    throw new TypeError("output target catalog is not in stable order");
  }
  const defaults = items.filter((item) => item.is_default);
  if (defaults.length > 1 || (source.default_output_target_ref === null && defaults.length !== 0)
    || (typeof source.default_output_target_ref === "string"
      && (defaults.length !== 1 || defaults[0].output_target_ref !== source.default_output_target_ref))) {
    throw new TypeError("output target catalog default is inconsistent");
  }
  return {
    environment: source.environment,
    default_policy: "explicit",
    default_output_target_ref: source.default_output_target_ref as string | null,
    items,
  };
}

export const assemblyClient = {
  async listOutputTargets(environment: AssemblyEnvironment, options: AssemblyRequestOptions = {}) {
    const query = new URLSearchParams({ environment });
    const result = await authenticatedAdminRequest<unknown>(`/api/v1/admin/assembly-output-targets?${query}`, readInit(options));
    const catalog = parseOutputTargetCatalog(result);
    if (catalog.environment !== environment) throw new TypeError("output target catalog environment does not match the request");
    return catalog;
  },

  createBlueprint(document: ProductBlueprintDocument, options: AssemblyWriteOptions) {
    assertTrustedToolSelections(document);
    return authenticatedAdminRequest<BlueprintRecord>("/api/v1/admin/blueprints", writeInit(document, options));
  },

  getBlueprint(blueprintId: string, options: AssemblyRequestOptions = {}) {
    assertIdentifier(blueprintId, "blueprintId");
    return authenticatedAdminRequest<BlueprintRecord>(`/api/v1/admin/blueprints/${encodeURIComponent(blueprintId)}`, readInit(options));
  },

  createPlan(blueprintId: string, input: CreatePlanInput, options: AssemblyWriteOptions) {
    assertIdentifier(blueprintId, "blueprintId");
    return authenticatedAdminRequest<AssemblyPlanRecord>(
      `/api/v1/admin/blueprints/${encodeURIComponent(blueprintId)}/plan`,
      writeInit(input as unknown as JsonValue, options),
    );
  },

  getPlan(planId: string, options: AssemblyRequestOptions = {}) {
    assertIdentifier(planId, "planId");
    return authenticatedAdminRequest<AssemblyPlanRecord>(`/api/v1/admin/assembly-plans/${encodeURIComponent(planId)}`, readInit(options));
  },

  startAssembly(blueprintId: string, input: StartAssemblyInput, options: AssemblyWriteOptions) {
    assertIdentifier(blueprintId, "blueprintId");
    return authenticatedAdminRequest<AssemblyRunRecord>(
      `/api/v1/admin/blueprints/${encodeURIComponent(blueprintId)}/assemble`,
      writeInit(input as unknown as JsonValue, options),
    );
  },

  getRun(runId: string, options: AssemblyRequestOptions = {}) {
    assertIdentifier(runId, "runId");
    return authenticatedAdminRequest<AssemblyRunRecord>(`/api/v1/admin/assembly-runs/${encodeURIComponent(runId)}`, readInit(options));
  },

  getManifest(assemblyId: string, options: AssemblyRequestOptions = {}) {
    assertIdentifier(assemblyId, "assemblyId");
    return authenticatedAdminRequest<AssemblyManifestRecord>(`/api/v1/admin/assembly-manifests/${encodeURIComponent(assemblyId)}`, readInit(options));
  },

  getGeneratedProjectLock(lockId: string, options: AssemblyRequestOptions = {}) {
    assertIdentifier(lockId, "lockId");
    return authenticatedAdminRequest<GeneratedProjectLockRecord>(`/api/v1/admin/generated-project-locks/${encodeURIComponent(lockId)}`, readInit(options));
  },
};
