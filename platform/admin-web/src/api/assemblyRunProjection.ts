import type {
  AssemblyRunDiagnostic, AssemblyRunPage, AssemblyRunRecord, AssemblyRunRecovery,
  AssemblyRunReport, AssemblyRunStatus, AssemblyRunStep, AssemblyRunStepKind, AssemblyRunStepStatus,
  AssemblyRunSummary, JsonObject,
} from "./assemblyClient";

const identifier = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
const stableCode = /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/;
const sha256 = /^sha256:[a-f0-9]{64}$/;
const outputRef = /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/;
const statuses = new Set<AssemblyRunStatus>(["planned", "provisioning", "generating", "validating", "completed", "failed", "cancelled", "rolling_back", "rolled_back"]);
const stepKinds = new Set<AssemblyRunStepKind>(["provision", "enable_capability", "generate", "validate", "commit", "rollback"]);
const stepStatuses = new Set<AssemblyRunStepStatus>(["pending", "running", "completed", "failed", "compensated", "skipped"]);
const compensationStatuses = new Set<AssemblyRunStep["compensation_status"]>(["not_required", "pending", "completed", "failed"]);

function object(value: unknown, required: string[], optional: string[], label: string) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new TypeError(`${label} is invalid`);
  const source = value as Record<string, unknown>;
  const allowed = new Set([...required, ...optional]);
  if (required.some((key) => !(key in source)) || Object.keys(source).some((key) => !allowed.has(key))) {
    throw new TypeError(`${label} contains unknown or missing fields`);
  }
  return source;
}

function id(value: unknown, field: string) {
  if (typeof value !== "string" || !identifier.test(value)) throw new TypeError(`${field} is invalid`);
  return value;
}
function nullableId(value: unknown, field: string) { return value === null ? null : id(value, field); }
function integer(value: unknown, field: string, minimum = 0) {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < minimum) throw new TypeError(`${field} is invalid`);
  return value;
}
function bool(value: unknown, field: string) {
  if (typeof value !== "boolean") throw new TypeError(`${field} is invalid`);
  return value;
}
function timestamp(value: unknown, field: string) {
  if (typeof value !== "string" || !Number.isFinite(Date.parse(value))) throw new TypeError(`${field} is invalid`);
  return value;
}
function nullableTimestamp(value: unknown, field: string) { return value === null ? null : timestamp(value, field); }
function enumeration<T extends string>(value: unknown, values: Set<T>, field: string) {
  if (typeof value !== "string" || !values.has(value as T)) throw new TypeError(`${field} is invalid`);
  return value as T;
}
function code(value: unknown, field: string) {
  if (typeof value !== "string" || !stableCode.test(value)) throw new TypeError(`${field} is invalid`);
  return value;
}
function text(value: unknown, field: string, max: number) {
  if (typeof value !== "string" || !value.trim() || value.length > max || /[\r\n\t]/.test(value)
    || /^[A-Za-z]:[\\/]/.test(value) || value.startsWith("/") || value.startsWith("\\")
    || /(^|[\\/])\.\.([\\/]|$)/.test(value)) throw new TypeError(`${field} is invalid`);
  return value;
}
function relativePath(value: unknown, field: string) {
  if (typeof value !== "string" || !value || value.length > 500 || value.startsWith("/") || value.startsWith("\\")
    || /^[A-Za-z]:/.test(value) || value.includes("\\") || value.split("/").some((part) => !part || part === "." || part === "..")) {
    throw new TypeError(`${field} is invalid`);
  }
  return value;
}
function jsonObject(value: unknown, field: string) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new TypeError(`${field} is invalid`);
  return value as JsonObject;
}

function parseStep(value: unknown): AssemblyRunStep {
  const s = object(value, ["step_id", "kind", "status", "attempt", "compensation_status", "started_at", "finished_at", "diagnostic_ids"], [], "assembly run step");
  if (!Array.isArray(s.diagnostic_ids)) throw new TypeError("assembly run step diagnostic_ids is invalid");
  const diagnosticIds = s.diagnostic_ids.map((item) => id(item, "assembly run step diagnostic_id"));
  if (new Set(diagnosticIds).size !== diagnosticIds.length) throw new TypeError("assembly run step diagnostic_ids contains duplicates");
  return { step_id: id(s.step_id, "assembly run step_id"), kind: enumeration(s.kind, stepKinds, "assembly run step kind"),
    status: enumeration(s.status, stepStatuses, "assembly run step status"), attempt: integer(s.attempt, "assembly run step attempt"),
    compensation_status: enumeration(s.compensation_status, compensationStatuses, "assembly run step compensation_status"),
    started_at: nullableTimestamp(s.started_at, "assembly run step started_at"), finished_at: nullableTimestamp(s.finished_at, "assembly run step finished_at"), diagnostic_ids: diagnosticIds };
}

function parseRecovery(value: unknown): AssemblyRunRecovery {
  const s = object(value, ["retryable", "rollback_required", "resume_from_step_id"], [], "assembly run recovery");
  return { retryable: bool(s.retryable, "assembly run recovery retryable"), rollback_required: bool(s.rollback_required, "assembly run recovery rollback_required"), resume_from_step_id: nullableId(s.resume_from_step_id, "assembly run recovery resume_from_step_id") };
}

export function parseAssemblyRunDiagnostic(value: unknown): AssemblyRunDiagnostic {
  const s = object(value, ["diagnostic_id", "code", "severity", "category", "message", "blocking", "retryable", "remediation", "related_paths"], [], "assembly run diagnostic");
  if (!Array.isArray(s.remediation) || !Array.isArray(s.related_paths)) throw new TypeError("assembly run diagnostic lists are invalid");
  const paths = s.related_paths.map((item) => relativePath(item, "assembly run diagnostic related_path"));
  if (new Set(paths).size !== paths.length) throw new TypeError("assembly run diagnostic related_paths contains duplicates");
  return {
    diagnostic_id: id(s.diagnostic_id, "assembly run diagnostic_id"), code: code(s.code, "assembly run diagnostic code"),
    severity: enumeration(s.severity, new Set(["info", "warning", "error"]), "assembly run diagnostic severity"),
    category: code(s.category, "assembly run diagnostic category"), message: text(s.message, "assembly run diagnostic message", 500),
    blocking: bool(s.blocking, "assembly run diagnostic blocking"), retryable: bool(s.retryable, "assembly run diagnostic retryable"),
    remediation: s.remediation.map((item) => text(item, "assembly run diagnostic remediation", 300)), related_paths: paths,
  };
}

export function parseAssemblyRunReport(value: unknown): AssemblyRunReport {
  const s = object(value, ["report_id", "type", "status", "summary", "checksum", "created_at"], [], "assembly run report");
  if (s.checksum !== null && (typeof s.checksum !== "string" || !sha256.test(s.checksum))) throw new TypeError("assembly run report checksum is invalid");
  return { report_id: id(s.report_id, "assembly run report_id"), type: code(s.type, "assembly run report type"),
    status: enumeration(s.status, new Set(["passed", "failed", "partial"]), "assembly run report status"),
    summary: text(s.summary, "assembly run report summary", 500), checksum: s.checksum as string | null,
    created_at: timestamp(s.created_at, "assembly run report created_at") };
}

export function parseAssemblyRunSummary(value: unknown): AssemblyRunSummary {
  const s = object(value, ["run_id", "product_id", "plan_id", "version", "root_run_id", "retry_of_run_id", "attempt_number", "status", "current_step_id", "diagnostic_count", "report_count", "created_at", "updated_at", "completed_at"], [], "assembly run summary");
  return { run_id: id(s.run_id, "assembly run summary run_id"), product_id: nullableId(s.product_id, "assembly run summary product_id"),
    plan_id: id(s.plan_id, "assembly run summary plan_id"), version: integer(s.version, "assembly run summary version", 1), root_run_id: id(s.root_run_id, "assembly run summary root_run_id"),
    retry_of_run_id: nullableId(s.retry_of_run_id, "assembly run summary retry_of_run_id"), attempt_number: integer(s.attempt_number, "assembly run summary attempt_number", 1),
    status: enumeration(s.status, statuses, "assembly run summary status"), current_step_id: nullableId(s.current_step_id, "assembly run summary current_step_id"),
    diagnostic_count: integer(s.diagnostic_count, "assembly run summary diagnostic_count"), report_count: integer(s.report_count, "assembly run summary report_count"),
    created_at: timestamp(s.created_at, "assembly run summary created_at"), updated_at: timestamp(s.updated_at, "assembly run summary updated_at"), completed_at: nullableTimestamp(s.completed_at, "assembly run summary completed_at") };
}

export function parseAssemblyRun(value: unknown): AssemblyRunRecord {
  const required = ["run_id", "product_id", "plan_id", "plan_version", "version", "plan_checksum", "root_run_id", "retry_of_run_id", "attempt_number", "output_target_ref", "status", "current_step_id", "steps", "recovery", "diagnostics", "reports", "document", "created_at", "updated_at", "completed_at", "audit_id"];
  const s = object(value, required, ["manifest_url", "lock_url"], "assembly run");
  if (!Array.isArray(s.steps) || !Array.isArray(s.diagnostics) || !Array.isArray(s.reports)) throw new TypeError("assembly run projections are invalid");
  if (typeof s.plan_checksum !== "string" || !sha256.test(s.plan_checksum)) throw new TypeError("assembly run plan_checksum is invalid");
  if (typeof s.output_target_ref !== "string" || !outputRef.test(s.output_target_ref)) throw new TypeError("assembly run output_target_ref is invalid");
  const artifact = (value: unknown, resource: string, field: string) => {
    if (typeof value !== "string" || !new RegExp(`^/api/v1/admin/${resource}/[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`).test(value)) throw new TypeError(`${field} is invalid`);
    return value;
  };
  const result: AssemblyRunRecord = {
    run_id: id(s.run_id, "assembly run run_id"), product_id: nullableId(s.product_id, "assembly run product_id"), plan_id: id(s.plan_id, "assembly run plan_id"),
    plan_version: integer(s.plan_version, "assembly run plan_version", 1), version: integer(s.version, "assembly run version", 1), plan_checksum: s.plan_checksum,
    root_run_id: id(s.root_run_id, "assembly run root_run_id"), retry_of_run_id: nullableId(s.retry_of_run_id, "assembly run retry_of_run_id"), attempt_number: integer(s.attempt_number, "assembly run attempt_number", 1),
    output_target_ref: s.output_target_ref, status: enumeration(s.status, statuses, "assembly run status"), current_step_id: nullableId(s.current_step_id, "assembly run current_step_id"),
    steps: s.steps.map(parseStep), recovery: parseRecovery(s.recovery), diagnostics: s.diagnostics.map(parseAssemblyRunDiagnostic), reports: s.reports.map(parseAssemblyRunReport),
    document: jsonObject(s.document, "assembly run document"), created_at: timestamp(s.created_at, "assembly run created_at"), updated_at: timestamp(s.updated_at, "assembly run updated_at"),
    completed_at: nullableTimestamp(s.completed_at, "assembly run completed_at"), audit_id: id(s.audit_id, "assembly run audit_id"),
  };
  if (s.manifest_url !== undefined) result.manifest_url = artifact(s.manifest_url, "assembly-manifests", "assembly run manifest_url");
  if (s.lock_url !== undefined) result.lock_url = artifact(s.lock_url, "generated-project-locks", "assembly run lock_url");
  return result;
}

export function parseAssemblyRunPage(value: unknown): AssemblyRunPage {
  const s = object(value, ["items", "next_cursor"], [], "assembly run page");
  if (!Array.isArray(s.items) || (s.next_cursor !== null && (typeof s.next_cursor !== "string" || s.next_cursor.length > 1024))) throw new TypeError("assembly run page is invalid");
  return { items: s.items.map(parseAssemblyRunSummary), next_cursor: s.next_cursor as string | null };
}
