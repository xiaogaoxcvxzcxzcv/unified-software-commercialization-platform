import { authenticatedAdminRequest } from "./authClient";
import { parseAssemblyRun, parseAssemblyRunPage } from "./assemblyRunProjection";

export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue };
export type JsonObject = { [key: string]: JsonValue };

export type AssemblyEnvironment = "development" | "test" | "staging" | "production";
export type AssemblyTarget = "web" | "desktop_webview" | "h5" | "wechat_miniprogram" | "mobile_app";
export type AssemblyDeliveryMode = "hosted" | "package" | "generated_source";
export type AssemblyCatalogScope = "ordinary" | "experimental";

export interface AssemblyCatalogFilter {
  target: AssemblyTarget;
  delivery_mode: AssemblyDeliveryMode;
  environment: AssemblyEnvironment;
}

export interface AssemblyCatalogRequirement {
  package_id: string;
  version_range: string;
}

export interface AssemblyCatalogVersionRef {
  id: string;
  version: string;
}

export interface AssemblyCatalogPackageOption {
  package_id: string;
  version: string;
  name: string;
  user_value: string;
  dependencies: AssemblyCatalogRequirement[];
  conflicts: AssemblyCatalogRequirement[];
  compatible_template_refs: AssemblyCatalogVersionRef[];
}

export interface AssemblyCatalogTemplateOption {
  template_id: string;
  version: string;
  name: string;
  supported_blocks: string[];
}

export interface AssemblyCatalogToolOption {
  id: string;
  version: string;
  name: string;
}

export interface AssemblyCatalogOptions extends AssemblyCatalogFilter {
  catalog_scope: AssemblyCatalogScope;
  catalog_revision: string;
  packages: AssemblyCatalogPackageOption[];
  templates: AssemblyCatalogTemplateOption[];
  generators: AssemblyCatalogToolOption[];
  sdks: AssemblyCatalogToolOption[];
}

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
  environments: AssemblyEnvironment[];
  document: ProductBlueprintDocument;
  checksum: string;
  created_at: string;
  updated_at: string;
  audit_id: string;
}

export interface AssemblyPlanReview {
  packages: Array<{ package_id: string; version: string }>;
  applications: Array<{ application_id: string; target: AssemblyTarget; channel: string; delivery_mode: AssemblyDeliveryMode; template_id: string; template_version: string }>;
  risks: Array<{ risk_id: string; level: "low" | "medium" | "high"; category: "security" | "data" | "compatibility" | "provider" | "generation" | "rollback"; summary: string; requires_confirmation: boolean }>;
  blocking_conflict_count: number;
  statements: string[];
}

export interface AssemblyPlanRecord {
  plan_id: string;
  version: number;
  blueprint_id: string;
  blueprint_version: number;
  schema_version: string;
  environment: AssemblyEnvironment;
  confirmation_checksum: string;
  review: AssemblyPlanReview;
  document: JsonObject;
  checksum: string;
  executable: boolean;
  confirmed: boolean;
  created_at: string;
  updated_at: string;
  audit_id: string;
}

export type AssemblyRunStatus = "planned" | "provisioning" | "generating" | "validating" | "completed" | "failed" | "rolling_back" | "rolled_back";

export type AssemblyRunStepStatus = "pending" | "running" | "completed" | "failed" | "compensated" | "skipped";
export type AssemblyRunStepKind = "provision" | "enable_capability" | "generate" | "validate" | "commit" | "rollback";

export interface AssemblyRunStep {
  step_id: string;
  kind: AssemblyRunStepKind;
  status: AssemblyRunStepStatus;
  attempt: number;
  compensation_status: "not_required" | "pending" | "completed" | "failed";
  started_at: string | null;
  finished_at: string | null;
  diagnostic_ids: string[];
}

export interface AssemblyRunRecovery {
  retryable: boolean;
  rollback_required: boolean;
  resume_from_step_id: string | null;
}

export interface AssemblyRunDiagnostic {
  diagnostic_id: string;
  code: string;
  severity: "info" | "warning" | "error";
  category: string;
  message: string;
  blocking: boolean;
  retryable: boolean;
  remediation: string[];
  related_paths: string[];
}

export interface AssemblyRunReport {
  report_id: string;
  type: string;
  status: "passed" | "failed" | "partial";
  summary: string;
  checksum: string | null;
  created_at: string;
}

export interface AssemblyRunSummary {
  run_id: string;
  product_id: string | null;
  plan_id: string;
  version: number;
  root_run_id: string;
  retry_of_run_id: string | null;
  attempt_number: number;
  status: AssemblyRunStatus;
  current_step_id: string | null;
  diagnostic_count: number;
  report_count: number;
  created_at: string;
  updated_at: string;
  completed_at: string | null;
}

export interface AssemblyRunPage {
  items: AssemblyRunSummary[];
  next_cursor: string | null;
}

export interface AssemblyRunRecord {
  run_id: string;
  product_id: string | null;
  plan_id: string;
  plan_version: number;
  version: number;
  plan_checksum: string;
  root_run_id: string;
  retry_of_run_id: string | null;
  attempt_number: number;
  output_target_ref: string;
  status: AssemblyRunStatus;
  current_step_id: string | null;
  steps: readonly AssemblyRunStep[];
  recovery: AssemblyRunRecovery;
  diagnostics: readonly AssemblyRunDiagnostic[];
  reports: readonly AssemblyRunReport[];
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

export interface ListAssemblyRunsInput {
  cursor?: string;
  page_size?: number;
  status?: AssemblyRunStatus;
  product_id?: string;
}

const trustedToolKeys = new Set(["id", "version"]);
const identifierPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
const outputTargetRefPattern = /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/;
const stableCodePattern = /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/;
const packageIdPattern = /^package\.[a-z][a-z0-9-]*$/;
const semverPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;
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

function containsPathLikeDisplayValue(value: string) {
  return value.includes("\\")
    || /^[A-Za-z]:[\\/]/.test(value)
    || value.startsWith("/")
    || /(^|[\\/])\.\.([\\/]|$)/.test(value)
    || [...value].some((character) => {
      const codePoint = character.codePointAt(0)!;
      return codePoint <= 0x1f || codePoint === 0x7f;
    });
}

function exactObject(value: unknown, expectedKeys: readonly string[], label: string) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new TypeError(`${label} is invalid`);
  const source = value as Record<string, unknown>;
  const keys = Object.keys(source).sort();
  const expected = [...expectedKeys].sort();
  if (keys.length !== expected.length || keys.some((key, index) => key !== expected[index])) {
    throw new TypeError(`${label} contains unknown or missing fields`);
  }
  return source;
}

function safeDisplayString(value: unknown, field: string, maxLength: number) {
  if (typeof value !== "string" || !value.trim() || [...value].length > maxLength) {
    throw new TypeError(`${field} is invalid`);
  }
  if (containsPathLikeDisplayValue(value)) throw new TypeError(`${field} must not contain a host path`);
  return value;
}

function safeIdentifier(value: unknown, field: string) {
  if (typeof value !== "string" || value.length > 128 || !identifierPattern.test(value)) {
    throw new TypeError(`${field} is invalid`);
  }
  return value;
}

function safeStableCode(value: unknown, field: string) {
  if (typeof value !== "string" || value.length < 3 || value.length > 64 || !stableCodePattern.test(value)) {
    throw new TypeError(`${field} is invalid`);
  }
  return value;
}

function safeVersion(value: unknown, field: string) {
  if (typeof value !== "string" || value.length < 5 || value.length > 80 || !semverPattern.test(value)) {
    throw new TypeError(`${field} is invalid`);
  }
  return value;
}

function stableUnique<T>(items: T[], key: (item: T) => string, label: string) {
  const keys = items.map(key);
  if (new Set(keys).size !== keys.length) throw new TypeError(`${label} contains duplicate entries`);
  if (keys.some((value, index) => index > 0 && keys[index - 1] >= value)) {
    throw new TypeError(`${label} is not in stable order`);
  }
  return items;
}

function parseRequirement(value: unknown): AssemblyCatalogRequirement {
  const source = exactObject(value, ["package_id", "version_range"], "catalog requirement");
  if (typeof source.package_id !== "string" || !packageIdPattern.test(source.package_id)) {
    throw new TypeError("catalog requirement package_id is invalid");
  }
  const versionRange = safeDisplayString(source.version_range, "catalog requirement version_range", 120);
  return { package_id: source.package_id, version_range: versionRange };
}

function parseVersionRef(value: unknown): AssemblyCatalogVersionRef {
  const source = exactObject(value, ["id", "version"], "catalog version ref");
  return { id: safeIdentifier(source.id, "catalog version ref id"), version: safeVersion(source.version, "catalog version ref version") };
}

function parsePackageOption(value: unknown): AssemblyCatalogPackageOption {
  const source = exactObject(value, ["package_id", "version", "name", "user_value", "dependencies", "conflicts", "compatible_template_refs"], "catalog package");
  if (typeof source.package_id !== "string" || !packageIdPattern.test(source.package_id)) throw new TypeError("catalog package package_id is invalid");
  if (!Array.isArray(source.dependencies) || !Array.isArray(source.conflicts) || !Array.isArray(source.compatible_template_refs)) {
    throw new TypeError("catalog package relationships are invalid");
  }
  return {
    package_id: source.package_id,
    version: safeVersion(source.version, "catalog package version"),
    name: safeDisplayString(source.name, "catalog package name", 120),
    user_value: safeDisplayString(source.user_value, "catalog package user_value", 240),
    dependencies: stableUnique(source.dependencies.map(parseRequirement), (item) => `${item.package_id}@${item.version_range}`, "catalog package dependencies"),
    conflicts: stableUnique(source.conflicts.map(parseRequirement), (item) => `${item.package_id}@${item.version_range}`, "catalog package conflicts"),
    compatible_template_refs: stableUnique(source.compatible_template_refs.map(parseVersionRef), (item) => `${item.id}@${item.version}`, "catalog package template refs"),
  };
}

function parseTemplateOption(value: unknown): AssemblyCatalogTemplateOption {
  const source = exactObject(value, ["template_id", "version", "name", "supported_blocks"], "catalog template");
  if (!Array.isArray(source.supported_blocks)) throw new TypeError("catalog template supported_blocks is invalid");
  return {
    template_id: safeIdentifier(source.template_id, "catalog template template_id"),
    version: safeVersion(source.version, "catalog template version"),
    name: safeDisplayString(source.name, "catalog template name", 120),
    supported_blocks: stableUnique(source.supported_blocks.map((item) => safeStableCode(item, "catalog template block")), (item) => item, "catalog template blocks"),
  };
}

function parseToolOption(value: unknown): AssemblyCatalogToolOption {
  const source = exactObject(value, ["id", "version", "name"], "catalog tool");
  return {
    id: safeIdentifier(source.id, "catalog tool id"),
    version: safeVersion(source.version, "catalog tool version"),
    name: safeDisplayString(source.name, "catalog tool name", 120),
  };
}

function parseCatalogOptions(value: unknown, expectedScope: AssemblyCatalogScope, filter: AssemblyCatalogFilter): AssemblyCatalogOptions {
  const source = exactObject(value, ["catalog_scope", "catalog_revision", "target", "delivery_mode", "environment", "packages", "templates", "generators", "sdks"], "assembly catalog options");
  if (source.catalog_scope !== expectedScope) throw new TypeError("assembly catalog scope does not match the endpoint");
  if (source.target !== filter.target || source.delivery_mode !== filter.delivery_mode || source.environment !== filter.environment) {
    throw new TypeError("assembly catalog filters do not match the request");
  }
  if (!Array.isArray(source.packages) || !Array.isArray(source.templates) || !Array.isArray(source.generators) || !Array.isArray(source.sdks)) {
    throw new TypeError("assembly catalog option lists are invalid");
  }
  return {
    catalog_scope: expectedScope,
    catalog_revision: safeStableCode(source.catalog_revision, "assembly catalog revision"),
    ...filter,
    packages: stableUnique(source.packages.map(parsePackageOption), (item) => `${item.package_id}@${item.version}`, "catalog packages"),
    templates: stableUnique(source.templates.map(parseTemplateOption), (item) => `${item.template_id}@${item.version}`, "catalog templates"),
    generators: stableUnique(source.generators.map(parseToolOption), (item) => `${item.id}@${item.version}`, "catalog generators"),
    sdks: stableUnique(source.sdks.map(parseToolOption), (item) => `${item.id}@${item.version}`, "catalog sdks"),
  };
}

function catalogQuery(filter: AssemblyCatalogFilter) {
  return new URLSearchParams({ target: filter.target, delivery_mode: filter.delivery_mode, environment: filter.environment });
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

function parseEnvironment(value: unknown, field: string): AssemblyEnvironment {
  if (value !== "development" && value !== "test" && value !== "staging" && value !== "production") throw new TypeError(`${field} is invalid`);
  return value;
}

function parsePositiveInteger(value: unknown, field: string) {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 1) throw new TypeError(`${field} is invalid`);
  return value;
}

function parseTimestamp(value: unknown, field: string) {
  if (typeof value !== "string" || !Number.isFinite(Date.parse(value))) throw new TypeError(`${field} is invalid`);
  return value;
}

function parseChecksum(value: unknown, field: string) {
  if (typeof value !== "string" || !/^sha256:[a-f0-9]{64}$/.test(value)) throw new TypeError(`${field} is invalid`);
  return value;
}

function parseSchemaVersion(value: unknown, field: string) {
  if (typeof value !== "string" || !/^1\.\d+\.\d+$/.test(value)) throw new TypeError(`${field} is invalid`);
  return value;
}

function parseDocument(value: unknown, field: string) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new TypeError(`${field} is invalid`);
  return value as JsonObject;
}

function parseBlueprint(value: unknown): BlueprintRecord {
  const source = exactObject(value, ["blueprint_id", "version", "schema_version", "environments", "document", "checksum", "created_at", "updated_at", "audit_id"], "assembly blueprint");
  if (!Array.isArray(source.environments) || source.environments.length < 1) throw new TypeError("assembly blueprint environments is invalid");
  const environments = source.environments.map((item) => parseEnvironment(item, "assembly blueprint environment"));
  if (new Set(environments).size !== environments.length || environments.some((item, index) => index > 0 && environments[index - 1] >= item)) throw new TypeError("assembly blueprint environments is not unique and stably sorted");
  return { blueprint_id: safeIdentifier(source.blueprint_id, "assembly blueprint blueprint_id"), version: parsePositiveInteger(source.version, "assembly blueprint version"),
    schema_version: parseSchemaVersion(source.schema_version, "assembly blueprint schema_version"), environments,
    document: parseDocument(source.document, "assembly blueprint document") as ProductBlueprintDocument, checksum: parseChecksum(source.checksum, "assembly blueprint checksum"),
    created_at: parseTimestamp(source.created_at, "assembly blueprint created_at"), updated_at: parseTimestamp(source.updated_at, "assembly blueprint updated_at"), audit_id: safeIdentifier(source.audit_id, "assembly blueprint audit_id") };
}

function parsePlanReview(value: unknown): AssemblyPlanReview {
  const source = exactObject(value, ["packages", "applications", "risks", "blocking_conflict_count", "statements"], "assembly plan review");
  if (!Array.isArray(source.packages) || !Array.isArray(source.applications) || source.applications.length < 1 || !Array.isArray(source.risks) || !Array.isArray(source.statements) || source.statements.length < 1) throw new TypeError("assembly plan review lists are invalid");
  const packages = source.packages.map((value) => { const item = exactObject(value, ["package_id", "version"], "assembly plan review package"); if (typeof item.package_id !== "string" || !packageIdPattern.test(item.package_id)) throw new TypeError("assembly plan review package_id is invalid"); return { package_id: item.package_id, version: safeVersion(item.version, "assembly plan review package version") }; });
  const targetValues = new Set<AssemblyTarget>(["web", "desktop_webview", "h5", "wechat_miniprogram", "mobile_app"]);
  const deliveryValues = new Set<AssemblyDeliveryMode>(["hosted", "package", "generated_source"]);
  const applications = source.applications.map((value) => { const item = exactObject(value, ["application_id", "target", "channel", "delivery_mode", "template_id", "template_version"], "assembly plan review application"); if (typeof item.target !== "string" || !targetValues.has(item.target as AssemblyTarget) || typeof item.delivery_mode !== "string" || !deliveryValues.has(item.delivery_mode as AssemblyDeliveryMode)) throw new TypeError("assembly plan review application target is invalid"); return { application_id: safeIdentifier(item.application_id, "assembly plan review application_id"), target: item.target as AssemblyTarget, channel: safeIdentifier(item.channel, "assembly plan review channel"), delivery_mode: item.delivery_mode as AssemblyDeliveryMode, template_id: safeIdentifier(item.template_id, "assembly plan review template_id"), template_version: safeVersion(item.template_version, "assembly plan review template_version") }; });
  const levels = new Set(["low", "medium", "high"] as const); const categories = new Set(["security", "data", "compatibility", "provider", "generation", "rollback"] as const);
  const risks = source.risks.map((value) => { const item = exactObject(value, ["risk_id", "level", "category", "summary", "requires_confirmation"], "assembly plan review risk"); if (typeof item.level !== "string" || !levels.has(item.level as never) || typeof item.category !== "string" || !categories.has(item.category as never) || typeof item.requires_confirmation !== "boolean") throw new TypeError("assembly plan review risk is invalid"); return { risk_id: safeIdentifier(item.risk_id, "assembly plan review risk_id"), level: item.level as AssemblyPlanReview["risks"][number]["level"], category: item.category as AssemblyPlanReview["risks"][number]["category"], summary: safeDisplayString(item.summary, "assembly plan review risk summary", 512), requires_confirmation: item.requires_confirmation }; });
  const statements = source.statements.map((item) => safeDisplayString(item, "assembly plan review statement", 512));
  if (new Set(statements).size !== statements.length) throw new TypeError("assembly plan review statements contains duplicates");
  return { packages, applications, risks, blocking_conflict_count: parsePositiveIntegerOrZero(source.blocking_conflict_count, "assembly plan review blocking_conflict_count"), statements };
}

function parsePositiveIntegerOrZero(value: unknown, field: string) { if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) throw new TypeError(`${field} is invalid`); return value; }

function parsePlan(value: unknown): AssemblyPlanRecord {
  const source = exactObject(value, ["plan_id", "version", "blueprint_id", "blueprint_version", "schema_version", "environment", "confirmation_checksum", "review", "document", "checksum", "executable", "confirmed", "created_at", "updated_at", "audit_id"], "assembly plan");
  if (typeof source.executable !== "boolean" || typeof source.confirmed !== "boolean") throw new TypeError("assembly plan state is invalid");
  return { plan_id: safeIdentifier(source.plan_id, "assembly plan plan_id"), version: parsePositiveInteger(source.version, "assembly plan version"),
    blueprint_id: safeIdentifier(source.blueprint_id, "assembly plan blueprint_id"), blueprint_version: parsePositiveInteger(source.blueprint_version, "assembly plan blueprint_version"),
    schema_version: parseSchemaVersion(source.schema_version, "assembly plan schema_version"), environment: parseEnvironment(source.environment, "assembly plan environment"),
    confirmation_checksum: parseChecksum(source.confirmation_checksum, "assembly plan confirmation_checksum"), review: parsePlanReview(source.review), document: parseDocument(source.document, "assembly plan document"),
    checksum: parseChecksum(source.checksum, "assembly plan checksum"), executable: source.executable, confirmed: source.confirmed,
    created_at: parseTimestamp(source.created_at, "assembly plan created_at"), updated_at: parseTimestamp(source.updated_at, "assembly plan updated_at"), audit_id: safeIdentifier(source.audit_id, "assembly plan audit_id") };
}

export const assemblyClient = {
  async listOrdinaryCatalogOptions(filter: AssemblyCatalogFilter, options: AssemblyRequestOptions = {}) {
    const result = await authenticatedAdminRequest<unknown>(`/api/v1/admin/assembly-catalog-options?${catalogQuery(filter)}`, readInit(options));
    return parseCatalogOptions(result, "ordinary", filter);
  },

  async listExperimentalCatalogOptions(filter: AssemblyCatalogFilter, options: AssemblyRequestOptions = {}) {
    const result = await authenticatedAdminRequest<unknown>(`/api/v1/admin/experimental/assembly-catalog-options?${catalogQuery(filter)}`, readInit(options));
    return parseCatalogOptions(result, "experimental", filter);
  },

  async listOutputTargets(environment: AssemblyEnvironment, options: AssemblyRequestOptions = {}) {
    const query = new URLSearchParams({ environment });
    const result = await authenticatedAdminRequest<unknown>(`/api/v1/admin/assembly-output-targets?${query}`, readInit(options));
    const catalog = parseOutputTargetCatalog(result);
    if (catalog.environment !== environment) throw new TypeError("output target catalog environment does not match the request");
    return catalog;
  },

  createBlueprint(document: ProductBlueprintDocument, options: AssemblyWriteOptions) {
    assertTrustedToolSelections(document);
    return authenticatedAdminRequest<unknown>("/api/v1/admin/blueprints", writeInit(document, options)).then(parseBlueprint);
  },

  async getBlueprint(blueprintId: string, options: AssemblyRequestOptions = {}) {
    assertIdentifier(blueprintId, "blueprintId");
    return parseBlueprint(await authenticatedAdminRequest<unknown>(`/api/v1/admin/blueprints/${encodeURIComponent(blueprintId)}`, readInit(options)));
  },

  async createPlan(blueprintId: string, input: CreatePlanInput, options: AssemblyWriteOptions) {
    assertIdentifier(blueprintId, "blueprintId");
    const result = await authenticatedAdminRequest<unknown>(
      `/api/v1/admin/blueprints/${encodeURIComponent(blueprintId)}/plan`,
      writeInit(input as unknown as JsonValue, options),
    );
    return parsePlan(result);
  },

  async getPlan(planId: string, options: AssemblyRequestOptions = {}) {
    assertIdentifier(planId, "planId");
    return parsePlan(await authenticatedAdminRequest<unknown>(`/api/v1/admin/assembly-plans/${encodeURIComponent(planId)}`, readInit(options)));
  },

  async startAssembly(blueprintId: string, input: StartAssemblyInput, options: AssemblyWriteOptions) {
    assertIdentifier(blueprintId, "blueprintId");
    const result = await authenticatedAdminRequest<unknown>(
      `/api/v1/admin/blueprints/${encodeURIComponent(blueprintId)}/assemble`,
      writeInit(input as unknown as JsonValue, options),
    );
    return parseAssemblyRun(result);
  },

  async getRun(runId: string, options: AssemblyRequestOptions = {}) {
    assertIdentifier(runId, "runId");
    const result = await authenticatedAdminRequest<unknown>(`/api/v1/admin/assembly-runs/${encodeURIComponent(runId)}`, readInit(options));
    return parseAssemblyRun(result);
  },

  async listRuns(input: ListAssemblyRunsInput = {}, options: AssemblyRequestOptions = {}) {
    const query = new URLSearchParams();
    if (input.cursor) query.set("cursor", input.cursor);
    if (input.page_size !== undefined) {
      if (!Number.isInteger(input.page_size) || input.page_size < 1 || input.page_size > 100) throw new TypeError("page_size is invalid");
      query.set("page_size", String(input.page_size));
    }
    if (input.status) query.set("status", input.status);
    if (input.product_id) {
      assertIdentifier(input.product_id, "product_id");
      query.set("product_id", input.product_id);
    }
    const result = await authenticatedAdminRequest<unknown>(`/api/v1/admin/assembly-runs${query.size ? `?${query}` : ""}`, readInit(options));
    return parseAssemblyRunPage(result);
  },

  async retryRun(runId: string, expectedVersion: number, options: AssemblyWriteOptions) {
    assertIdentifier(runId, "runId");
    if (!Number.isSafeInteger(expectedVersion) || expectedVersion < 1) throw new TypeError("expectedVersion is invalid");
    const result = await authenticatedAdminRequest<unknown>(`/api/v1/admin/assembly-runs/${encodeURIComponent(runId)}/retry`, writeInit({ expected_version: expectedVersion }, options));
    return parseAssemblyRun(result);
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
