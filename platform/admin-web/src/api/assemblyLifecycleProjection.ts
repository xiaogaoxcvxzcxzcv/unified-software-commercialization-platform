import type { AssemblyRunDiagnostic, AssemblyRunReport } from "./assemblyClient";
import { parseAssemblyRunDiagnostic, parseAssemblyRunReport } from "./assemblyRunProjection";

export type LifecycleKind = "upgrade" | "eject" | "rollback";
export type LifecycleOperationStatus = "planned" | "executing" | "completed" | "failed" | "cancelled" | "rolling_back" | "rolled_back" | "rollback_failed";
export interface LifecycleVersionRef { id: string; version: string }
export interface LifecycleTargetVersions { packages: LifecycleVersionRef[]; templates: LifecycleVersionRef[]; generator: LifecycleVersionRef; sdks: LifecycleVersionRef[] }
export interface LifecycleArtifactState { manifest_id: string; manifest_checksum: string; lock_id: string; lock_checksum: string; catalog_checksum: string; target_snapshot_checksum: string }
export interface LifecycleChange { path: string; action: "create" | "update" | "delete" | "unchanged" | "eject"; ownership: "generated" | "integration" | "forked"; before_checksum: string | null; after_checksum: string | null; source_id: string; source_version: string }
export interface LifecycleMigration { migration_id: string; kind: "database" | "provider" | "configuration"; reversibility: "reversible" | "compensatable" | "manual"; summary: string }
export interface LifecycleRollbackPolicy { strategy: "restore_predecessor" | "compensate" | "manual"; automatic: boolean; predecessor_manifest_checksum: string; predecessor_lock_checksum: string }
export interface LifecycleConflict { conflict_id: string; code: string; category: "custom" | "generated_drift" | "integration" | "catalog" | "migration" | "rollback" | "target"; blocking: boolean; message: string; paths: string[]; remediation: string[] }
export interface AssemblyLifecyclePlan { lifecycle_plan_id: string; assembly_id: string; product_id: string; operation: "upgrade" | "eject"; version: number; source: LifecycleArtifactState; target_snapshot_checksum: string; changes: LifecycleChange[]; migrations: LifecycleMigration[]; conflicts: LifecycleConflict[]; regression_tests: string[]; rollback: LifecycleRollbackPolicy; blocking_conflict_count: number; executable: boolean; confirmation_checksum: string; statements: string[]; plan_checksum: string; created_at: string; audit_id: string }
export interface LifecycleRecovery { retryable: boolean; rollback_available: boolean; cancel_allowed: boolean }
export interface AssemblyLifecycleOperation { operation_id: string; root_operation_id: string; rollback_of_operation_id: string | null; lifecycle_plan_id: string | null; assembly_id: string; product_id: string; kind: LifecycleKind; version: number; status: LifecycleOperationStatus; current_step: string | null; source: LifecycleArtifactState; target: LifecycleArtifactState | null; recovery: LifecycleRecovery; diagnostics: AssemblyRunDiagnostic[]; reports: AssemblyRunReport[]; manifest_url?: string; lock_url?: string; created_at: string; updated_at: string; completed_at: string | null; audit_id: string }

const idPattern = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
const codePattern = /^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$/;
const shaPattern = /^sha256:[a-f0-9]{64}$/;
const semverPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;
const relativePathPattern = /^[A-Za-z0-9._/-]+$/;
function exact(value: unknown, keys: string[], optional: string[], label: string) { if (!value || typeof value !== "object" || Array.isArray(value)) throw new TypeError(`${label} is invalid`); const source = value as Record<string, unknown>; const allowed = new Set([...keys, ...optional]); if (keys.some((key) => !(key in source)) || Object.keys(source).some((key) => !allowed.has(key))) throw new TypeError(`${label} contains unknown or missing fields`); return source; }
function id(value: unknown, field: string) { if (typeof value !== "string" || !idPattern.test(value)) throw new TypeError(`${field} is invalid`); return value; }
function nullableId(value: unknown, field: string) { return value === null ? null : id(value, field); }
function sha(value: unknown, field: string) { if (typeof value !== "string" || !shaPattern.test(value)) throw new TypeError(`${field} is invalid`); return value; }
function nullableSha(value: unknown, field: string) { return value === null ? null : sha(value, field); }
function integer(value: unknown, field: string, min = 0) { if (typeof value !== "number" || !Number.isSafeInteger(value) || value < min) throw new TypeError(`${field} is invalid`); return value; }
function bool(value: unknown, field: string) { if (typeof value !== "boolean") throw new TypeError(`${field} is invalid`); return value; }
function containsPathLikeValue(value: string) {
  return /[\u0000-\u001f\u007f]/.test(value)
    || value.includes("\\")
    || /[A-Za-z]:[\\/]/.test(value)
    || /(^|[\s("'=:;,])(?:[A-Za-z]:[\\/]|\/{1,2}(?:[^/\s]+\/)*[^/\s]+)/.test(value)
    || /(^|[\s("'=:;,])\.\.?\/(?:[^/\s]+\/)*[^/\s]+/.test(value);
}
function text(value: unknown, field: string, max: number) { if (typeof value !== "string" || !value.trim() || [...value].length > max || containsPathLikeValue(value)) throw new TypeError(`${field} is invalid`); return value; }
function path(value: unknown, field: string) { if (typeof value !== "string" || !value || value.length > 500 || !relativePathPattern.test(value) || value.startsWith("/") || value.split("/").some((part) => !part || part === "." || part === "..")) throw new TypeError(`${field} is invalid`); return value; }
function timestamp(value: unknown, field: string) { if (typeof value !== "string" || !Number.isFinite(Date.parse(value))) throw new TypeError(`${field} is invalid`); return value; }
function nullableTimestamp(value: unknown, field: string) { return value === null ? null : timestamp(value, field); }
function versionRef(value: unknown): LifecycleVersionRef { const s = exact(value, ["id", "version"], [], "lifecycle version ref"); if (typeof s.version !== "string" || !semverPattern.test(s.version)) throw new TypeError("lifecycle version is invalid"); return { id: id(s.id, "lifecycle version id"), version: s.version }; }
export function parseLifecycleArtifactState(value: unknown): LifecycleArtifactState { const s = exact(value, ["manifest_id", "manifest_checksum", "lock_id", "lock_checksum", "catalog_checksum", "target_snapshot_checksum"], [], "lifecycle artifact state"); return { manifest_id: id(s.manifest_id, "manifest_id"), manifest_checksum: sha(s.manifest_checksum, "manifest_checksum"), lock_id: id(s.lock_id, "lock_id"), lock_checksum: sha(s.lock_checksum, "lock_checksum"), catalog_checksum: sha(s.catalog_checksum, "catalog_checksum"), target_snapshot_checksum: sha(s.target_snapshot_checksum, "target_snapshot_checksum") }; }
function change(value: unknown): LifecycleChange { const s = exact(value, ["path", "action", "ownership", "before_checksum", "after_checksum", "source_id", "source_version"], [], "lifecycle change"); if (!["create", "update", "delete", "unchanged", "eject"].includes(String(s.action)) || !["generated", "integration", "forked"].includes(String(s.ownership)) || typeof s.source_version !== "string" || !semverPattern.test(s.source_version)) throw new TypeError("lifecycle change is invalid"); return { path: path(s.path, "lifecycle change path"), action: s.action as LifecycleChange["action"], ownership: s.ownership as LifecycleChange["ownership"], before_checksum: nullableSha(s.before_checksum, "before_checksum"), after_checksum: nullableSha(s.after_checksum, "after_checksum"), source_id: id(s.source_id, "source_id"), source_version: s.source_version }; }
function migration(value: unknown): LifecycleMigration { const s = exact(value, ["migration_id", "kind", "reversibility", "summary"], [], "lifecycle migration"); if (!["database", "provider", "configuration"].includes(String(s.kind)) || !["reversible", "compensatable", "manual"].includes(String(s.reversibility))) throw new TypeError("lifecycle migration is invalid"); return { migration_id: id(s.migration_id, "migration_id"), kind: s.kind as LifecycleMigration["kind"], reversibility: s.reversibility as LifecycleMigration["reversibility"], summary: text(s.summary, "migration summary", 300) }; }
function rollbackPolicy(value: unknown): LifecycleRollbackPolicy { const s = exact(value, ["strategy", "automatic", "predecessor_manifest_checksum", "predecessor_lock_checksum"], [], "lifecycle rollback policy"); if (!["restore_predecessor", "compensate", "manual"].includes(String(s.strategy))) throw new TypeError("lifecycle rollback policy is invalid"); return { strategy: s.strategy as LifecycleRollbackPolicy["strategy"], automatic: bool(s.automatic, "rollback automatic"), predecessor_manifest_checksum: sha(s.predecessor_manifest_checksum, "predecessor_manifest_checksum"), predecessor_lock_checksum: sha(s.predecessor_lock_checksum, "predecessor_lock_checksum") }; }
function conflict(value: unknown): LifecycleConflict { const s = exact(value, ["conflict_id", "code", "category", "blocking", "message", "paths", "remediation"], [], "lifecycle conflict"); if (typeof s.code !== "string" || !codePattern.test(s.code) || !["custom", "generated_drift", "integration", "catalog", "migration", "rollback", "target"].includes(String(s.category)) || !Array.isArray(s.paths) || !Array.isArray(s.remediation)) throw new TypeError("lifecycle conflict is invalid"); return { conflict_id: id(s.conflict_id, "conflict_id"), code: s.code, category: s.category as LifecycleConflict["category"], blocking: bool(s.blocking, "conflict blocking"), message: text(s.message, "conflict message", 500), paths: s.paths.map((item) => path(item, "conflict path")), remediation: s.remediation.map((item) => text(item, "conflict remediation", 300)) }; }

export function parseLifecyclePlan(value: unknown): AssemblyLifecyclePlan {
  const s = exact(value, ["lifecycle_plan_id", "assembly_id", "product_id", "operation", "version", "source", "target_snapshot_checksum", "changes", "migrations", "conflicts", "regression_tests", "rollback", "blocking_conflict_count", "executable", "confirmation_checksum", "statements", "plan_checksum", "created_at", "audit_id"], [], "lifecycle plan");
  if ((s.operation !== "upgrade" && s.operation !== "eject") || !Array.isArray(s.changes) || !Array.isArray(s.migrations) || !Array.isArray(s.conflicts) || !Array.isArray(s.regression_tests) || !Array.isArray(s.statements) || s.statements.length < 1) throw new TypeError("lifecycle plan projections are invalid");
  const conflicts = s.conflicts.map(conflict);
  const blockingConflictCount = integer(s.blocking_conflict_count, "blocking_conflict_count");
  const executable = bool(s.executable, "executable");
  if (blockingConflictCount !== conflicts.filter((item) => item.blocking).length || executable !== (blockingConflictCount === 0)) {
    throw new TypeError("lifecycle plan execution projection is inconsistent");
  }
  return { lifecycle_plan_id: id(s.lifecycle_plan_id, "lifecycle_plan_id"), assembly_id: id(s.assembly_id, "assembly_id"), product_id: id(s.product_id, "product_id"), operation: s.operation, version: integer(s.version, "version", 1), source: parseLifecycleArtifactState(s.source), target_snapshot_checksum: sha(s.target_snapshot_checksum, "target_snapshot_checksum"), changes: s.changes.map(change), migrations: s.migrations.map(migration), conflicts, regression_tests: s.regression_tests.map((item) => id(item, "regression test")), rollback: rollbackPolicy(s.rollback), blocking_conflict_count: blockingConflictCount, executable, confirmation_checksum: sha(s.confirmation_checksum, "confirmation_checksum"), statements: s.statements.map((item) => text(item, "confirmation statement", 300)), plan_checksum: sha(s.plan_checksum, "plan_checksum"), created_at: timestamp(s.created_at, "created_at"), audit_id: id(s.audit_id, "audit_id") };
}

function recovery(value: unknown): LifecycleRecovery { const s = exact(value, ["retryable", "rollback_available", "cancel_allowed"], [], "lifecycle recovery"); return { retryable: bool(s.retryable, "retryable"), rollback_available: bool(s.rollback_available, "rollback_available"), cancel_allowed: bool(s.cancel_allowed, "cancel_allowed") }; }
function safeArtifactUrl(value: unknown, resource: string) { if (typeof value !== "string" || !new RegExp(`^/api/v1/admin/${resource}/[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`).test(value)) throw new TypeError("lifecycle artifact URL is invalid"); return value; }
export function parseLifecycleOperation(value: unknown): AssemblyLifecycleOperation {
  const required = ["operation_id", "root_operation_id", "rollback_of_operation_id", "lifecycle_plan_id", "assembly_id", "product_id", "kind", "version", "status", "current_step", "source", "target", "recovery", "diagnostics", "reports", "created_at", "updated_at", "completed_at", "audit_id"];
  const s = exact(value, required, ["manifest_url", "lock_url"], "lifecycle operation");
  if (!["upgrade", "eject", "rollback"].includes(String(s.kind)) || !["planned", "executing", "completed", "failed", "cancelled", "rolling_back", "rolled_back", "rollback_failed"].includes(String(s.status)) || !Array.isArray(s.diagnostics) || !Array.isArray(s.reports)) throw new TypeError("lifecycle operation projection is invalid");
  const diagnostics = s.diagnostics.map(parseAssemblyRunDiagnostic).map((item) => {
    text(item.message, "lifecycle diagnostic message", 1000);
    item.remediation.forEach((line) => text(line, "lifecycle diagnostic remediation", 500));
    return item;
  });
  const reports = s.reports.map(parseAssemblyRunReport).map((item) => {
    text(item.summary, "lifecycle report summary", 1000);
    return item;
  });
  const result: AssemblyLifecycleOperation = { operation_id: id(s.operation_id, "operation_id"), root_operation_id: id(s.root_operation_id, "root_operation_id"), rollback_of_operation_id: nullableId(s.rollback_of_operation_id, "rollback_of_operation_id"), lifecycle_plan_id: nullableId(s.lifecycle_plan_id, "lifecycle_plan_id"), assembly_id: id(s.assembly_id, "assembly_id"), product_id: id(s.product_id, "product_id"), kind: s.kind as LifecycleKind, version: integer(s.version, "version", 1), status: s.status as LifecycleOperationStatus, current_step: nullableId(s.current_step, "current_step"), source: parseLifecycleArtifactState(s.source), target: s.target === null ? null : parseLifecycleArtifactState(s.target), recovery: recovery(s.recovery), diagnostics, reports, created_at: timestamp(s.created_at, "created_at"), updated_at: timestamp(s.updated_at, "updated_at"), completed_at: nullableTimestamp(s.completed_at, "completed_at"), audit_id: id(s.audit_id, "audit_id") };
  if (s.manifest_url !== undefined) result.manifest_url = safeArtifactUrl(s.manifest_url, "assembly-manifests"); if (s.lock_url !== undefined) result.lock_url = safeArtifactUrl(s.lock_url, "generated-project-locks"); return result;
}

export function validateLifecycleTarget(value: LifecycleTargetVersions) {
  const target = exact(value, ["packages", "templates", "generator", "sdks"], [], "lifecycle target");
  if (!Array.isArray(target.packages) || !Array.isArray(target.templates) || !Array.isArray(target.sdks) || target.templates.length < 1) throw new TypeError("target templates is required");
  const groups = [target.packages, target.templates, target.sdks] as unknown[][];
  groups.forEach((group) => { const refs = group.map(versionRef); if (new Set(refs.map((item) => `${item.id}@${item.version}`)).size !== refs.length) throw new TypeError("lifecycle target contains duplicate versions"); });
  versionRef(target.generator);
  return value;
}
export function validateLifecyclePath(value: string) { return path(value, "lifecycle path"); }
