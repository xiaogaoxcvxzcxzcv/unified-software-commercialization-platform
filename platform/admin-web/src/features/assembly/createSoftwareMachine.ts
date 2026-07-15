import type {
  AssemblyPlanRecord,
  AssemblyRunRecord,
  BlueprintRecord,
  OutputTargetCatalog,
  ProductBlueprintDocument,
} from "../../api/assemblyClient";
import { AuthApiError } from "../../api/authClient";

export type CreateSoftwarePhase =
  | "draft"
  | "loading_targets"
  | "validating"
  | "review_ready"
  | "creating_plan"
  | "plan_ready"
  | "executing"
  | "succeeded"
  | "failed";

export type RetryIntent =
  | "load_targets"
  | "validate_blueprint"
  | "get_blueprint"
  | "create_plan"
  | "get_plan"
  | "start_assembly"
  | "get_run";

export interface RequestFailure {
  requestId?: string;
  code: string;
  message: string;
  retryable: boolean;
  retryIntent: RetryIntent | null;
  detail?: string;
  retryAfterSeconds?: number;
  fieldErrors?: Array<{ field: string; code: string; message?: string }>;
}

export function assemblyRequestFailure(reason: unknown, retryIntent: RetryIntent | null): RequestFailure {
  if (reason instanceof AuthApiError) {
    return {
      requestId: reason.requestId,
      code: reason.code,
      message: reason.message,
      retryable: reason.retryable,
      retryIntent: reason.retryable ? retryIntent : null,
      detail: reason.detail,
      retryAfterSeconds: reason.retryAfterSeconds,
      fieldErrors: reason.fieldErrors,
    };
  }
  if (reason && typeof reason === "object" && "name" in reason && reason.name === "AbortError") {
    return { code: "assembly.request_cancelled", message: "Request was cancelled", retryable: false, retryIntent: null };
  }
  if (reason && typeof reason === "object" && "name" in reason && reason.name === "TimeoutError") {
    return { code: "assembly.request_timeout", message: "Assembly request timed out", retryable: true, retryIntent };
  }
  return { code: "assembly.request_failed", message: "Assembly request failed", retryable: true, retryIntent };
}

export interface CreateSoftwareState {
  phase: CreateSoftwarePhase;
  draft: ProductBlueprintDocument;
  draftRevision: number;
  operationToken: string | null;
  outputTargets: OutputTargetCatalog | null;
  selectedOutputTargetRef: string | null;
  blueprint: BlueprintRecord | null;
  validatedRevision: number | null;
  validationIdempotencyKey: string | null;
  plan: AssemblyPlanRecord | null;
  planIdempotencyKey: string | null;
  executionIdempotencyKey: string | null;
  run: AssemblyRunRecord | null;
  productId: string | null;
  failure: RequestFailure | null;
  failedFrom: Exclude<CreateSoftwarePhase, "failed" | "succeeded"> | null;
}

export type CreateSoftwareAction =
  | { type: "draft_changed"; draft: ProductBlueprintDocument }
  | { type: "targets_requested"; operationToken: string }
  | { type: "targets_loaded"; operationToken: string; catalog: OutputTargetCatalog }
  | { type: "validation_requested"; operationToken: string; idempotencyKey: string }
  | { type: "validation_succeeded"; operationToken: string; draftRevision: number; blueprint: BlueprintRecord }
  | { type: "plan_requested"; operationToken: string; idempotencyKey: string }
  | { type: "plan_succeeded"; operationToken: string; plan: AssemblyPlanRecord }
  | { type: "output_target_selected"; outputTargetRef: string }
  | { type: "execution_requested"; operationToken: string; idempotencyKey: string }
  | { type: "run_observed"; operationToken: string; run: AssemblyRunRecord }
  | { type: "execution_succeeded"; operationToken: string; run: AssemblyRunRecord; productId: string }
  | { type: "execution_failed"; operationToken: string; run: AssemblyRunRecord; failure: RequestFailure }
  | { type: "request_failed"; operationToken: string; failure: RequestFailure }
  | { type: "retry_requested"; operationToken: string };

export class InvalidCreateSoftwareTransitionError extends Error {
  constructor(phase: CreateSoftwarePhase, action: CreateSoftwareAction["type"]) {
    super(`cannot apply ${action} while create software is ${phase}`);
    this.name = "InvalidCreateSoftwareTransitionError";
  }
}

export function createInitialCreateSoftwareState(draft: ProductBlueprintDocument): CreateSoftwareState {
  return {
    phase: "draft",
    draft,
    draftRevision: 0,
    operationToken: null,
    outputTargets: null,
    selectedOutputTargetRef: null,
    blueprint: null,
    validatedRevision: null,
    validationIdempotencyKey: null,
    plan: null,
    planIdempotencyKey: null,
    executionIdempotencyKey: null,
    run: null,
    productId: null,
    failure: null,
    failedFrom: null,
  };
}

function requirePhase(state: CreateSoftwareState, action: CreateSoftwareAction, ...phases: CreateSoftwarePhase[]) {
  if (!phases.includes(state.phase)) throw new InvalidCreateSoftwareTransitionError(state.phase, action.type);
}

function requireValue(value: string, field: string) {
  if (!value.trim()) throw new TypeError(`${field} must not be empty`);
}

function retryPhase(intent: RetryIntent): Exclude<CreateSoftwarePhase, "draft" | "review_ready" | "plan_ready" | "succeeded" | "failed"> {
  switch (intent) {
    case "load_targets": return "loading_targets";
    case "validate_blueprint":
    case "get_blueprint": return "validating";
    case "create_plan":
    case "get_plan": return "creating_plan";
    case "start_assembly":
    case "get_run": return "executing";
  }
}

function isStale(state: CreateSoftwareState, operationToken: string) {
  return state.operationToken !== operationToken;
}

function isRetryIntentValid(phase: CreateSoftwarePhase, intent: RetryIntent | null) {
  if (intent === null) return true;
  switch (phase) {
    case "loading_targets": return intent === "load_targets";
    case "validating": return intent === "validate_blueprint" || intent === "get_blueprint";
    case "creating_plan": return intent === "create_plan" || intent === "get_plan";
    case "executing": return intent === "start_assembly" || intent === "get_run";
    default: return false;
  }
}

export function createSoftwareReducer(state: CreateSoftwareState, action: CreateSoftwareAction): CreateSoftwareState {
  switch (action.type) {
    case "draft_changed":
      requirePhase(state, action, "draft", "review_ready", "plan_ready", "failed");
      if (state.phase === "failed" && state.failedFrom === "executing") {
        throw new InvalidCreateSoftwareTransitionError(state.phase, action.type);
      }
      return {
        ...state,
        phase: "draft",
        draft: action.draft,
        draftRevision: state.draftRevision + 1,
        operationToken: null,
        blueprint: null,
        validatedRevision: null,
        validationIdempotencyKey: null,
        plan: null,
        planIdempotencyKey: null,
        executionIdempotencyKey: null,
        run: null,
        productId: null,
        failure: null,
        failedFrom: null,
      };

    case "targets_requested":
      requirePhase(state, action, "draft");
      requireValue(action.operationToken, "operationToken");
      return { ...state, phase: "loading_targets", operationToken: action.operationToken, failure: null, failedFrom: null };

    case "targets_loaded":
      if (isStale(state, action.operationToken)) return state;
      requirePhase(state, action, "loading_targets");
      return { ...state, phase: "draft", operationToken: null, outputTargets: action.catalog, failure: null, failedFrom: null };

    case "validation_requested":
      requirePhase(state, action, "draft");
      requireValue(action.operationToken, "operationToken");
      requireValue(action.idempotencyKey, "idempotencyKey");
      return {
        ...state,
        phase: "validating",
        operationToken: action.operationToken,
        validationIdempotencyKey: action.idempotencyKey,
        failure: null,
        failedFrom: null,
      };

    case "validation_succeeded":
      if (isStale(state, action.operationToken) || action.draftRevision !== state.draftRevision) return state;
      requirePhase(state, action, "validating");
      return {
        ...state,
        phase: "review_ready",
        operationToken: null,
        blueprint: action.blueprint,
        validatedRevision: action.draftRevision,
        plan: null,
        planIdempotencyKey: null,
        executionIdempotencyKey: null,
        failure: null,
        failedFrom: null,
      };

    case "plan_requested":
      requirePhase(state, action, "review_ready");
      if (!state.blueprint || state.validatedRevision !== state.draftRevision) {
        throw new InvalidCreateSoftwareTransitionError(state.phase, action.type);
      }
      requireValue(action.operationToken, "operationToken");
      requireValue(action.idempotencyKey, "idempotencyKey");
      return {
        ...state,
        phase: "creating_plan",
        operationToken: action.operationToken,
        planIdempotencyKey: action.idempotencyKey,
        failure: null,
        failedFrom: null,
      };

    case "plan_succeeded":
      if (isStale(state, action.operationToken)) return state;
      requirePhase(state, action, "creating_plan");
      if (!state.blueprint || action.plan.blueprint_id !== state.blueprint.blueprint_id
        || action.plan.blueprint_version !== state.blueprint.version) {
        throw new TypeError("plan does not match the validated blueprint");
      }
      return {
        ...state,
        phase: "plan_ready",
        operationToken: null,
        plan: action.plan,
        selectedOutputTargetRef: null,
        executionIdempotencyKey: null,
        failure: null,
        failedFrom: null,
      };

    case "output_target_selected":
      requirePhase(state, action, "plan_ready");
      requireValue(action.outputTargetRef, "outputTargetRef");
      if (!state.outputTargets?.items.some((item) => item.output_target_ref === action.outputTargetRef)) {
        throw new TypeError("outputTargetRef is not present in the loaded catalog");
      }
      if (state.outputTargets.environment !== state.plan?.environment) {
        throw new TypeError("output target catalog does not match the plan environment");
      }
      return {
        ...state,
        phase: "plan_ready",
        selectedOutputTargetRef: action.outputTargetRef,
        executionIdempotencyKey: state.selectedOutputTargetRef === action.outputTargetRef
          ? state.executionIdempotencyKey
          : null,
        failure: null,
        failedFrom: null,
      };

    case "execution_requested":
      requirePhase(state, action, "plan_ready");
      if (!state.plan || !state.selectedOutputTargetRef) {
        throw new InvalidCreateSoftwareTransitionError(state.phase, action.type);
      }
      requireValue(action.operationToken, "operationToken");
      requireValue(action.idempotencyKey, "idempotencyKey");
      return {
        ...state,
        phase: "executing",
        operationToken: action.operationToken,
        executionIdempotencyKey: action.idempotencyKey,
        failure: null,
        failedFrom: null,
      };

    case "run_observed":
      if (isStale(state, action.operationToken)) return state;
      requirePhase(state, action, "executing");
      if (state.plan && (action.run.plan_id !== state.plan.plan_id || action.run.plan_checksum !== state.plan.checksum
        || action.run.plan_version !== (state.plan.confirmed ? state.plan.version : state.plan.version + 1))) {
        throw new TypeError("run does not match the confirmed plan");
      }
      if (action.run.status === "completed" || action.run.status === "failed" || action.run.status === "rolled_back") {
        throw new TypeError("terminal runs require an explicit terminal transition");
      }
      return { ...state, run: action.run };

    case "execution_succeeded":
      if (isStale(state, action.operationToken)) return state;
      requirePhase(state, action, "executing");
      if (action.run.status !== "completed") throw new TypeError("successful transition requires a completed run");
      requireValue(action.productId, "productId");
      return {
        ...state,
        phase: "succeeded",
        operationToken: null,
        run: action.run,
        productId: action.productId,
        failure: null,
        failedFrom: null,
      };

    case "execution_failed":
      if (isStale(state, action.operationToken)) return state;
      requirePhase(state, action, "executing");
      if (action.run.status !== "failed" && action.run.status !== "rolled_back") throw new TypeError("failed transition requires a failed or rolled_back run");
      if (action.failure.retryIntent === "start_assembly") throw new TypeError("a persisted failed run can only be reread; business recovery requires a server action");
      if (!isRetryIntentValid("executing", action.failure.retryIntent)) {
        throw new TypeError("retry intent does not match the failed operation");
      }
      return {
        ...state,
        phase: "failed",
        operationToken: null,
        run: action.run,
        failure: action.failure,
        failedFrom: "executing",
      };

    case "request_failed":
      if (isStale(state, action.operationToken)) return state;
      requirePhase(state, action, "loading_targets", "validating", "creating_plan", "executing");
      if (!isRetryIntentValid(state.phase, action.failure.retryIntent)) {
        throw new TypeError("retry intent does not match the failed operation");
      }
      return {
        ...state,
        phase: "failed",
        operationToken: null,
        failure: action.failure,
        failedFrom: state.phase as Exclude<CreateSoftwarePhase, "failed" | "succeeded">,
      };

    case "retry_requested": {
      requirePhase(state, action, "failed");
      if (!state.failure?.retryable || !state.failure.retryIntent) {
        throw new InvalidCreateSoftwareTransitionError(state.phase, action.type);
      }
      requireValue(action.operationToken, "operationToken");
      const phase = retryPhase(state.failure.retryIntent);
      return {
        ...state,
        phase,
        operationToken: action.operationToken,
        failure: null,
        failedFrom: null,
      };
    }
  }
}
