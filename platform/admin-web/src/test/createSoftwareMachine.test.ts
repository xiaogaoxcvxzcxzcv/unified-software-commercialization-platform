import { describe, expect, it } from "vitest";
import type {
  AssemblyPlanRecord,
  AssemblyRunRecord,
  BlueprintRecord,
  OutputTargetCatalog,
  ProductBlueprintDocument,
} from "../api/assemblyClient";
import {
  assemblyRequestFailure,
  createInitialCreateSoftwareState,
  createSoftwareReducer,
  InvalidCreateSoftwareTransitionError,
  type CreateSoftwareState,
  type RequestFailure,
} from "../features/assembly/createSoftwareMachine";
import { AuthApiError } from "../api/authClient";

const draft: ProductBlueprintDocument = {
  schema_version: "1.0.0",
  product: { code: "video-studio", name: "Video Studio" },
  generator: { id: "generator.web-react", version: "1.0.0" },
  sdk: { id: "sdk.typescript", version: "1.0.0" },
};

const targets: OutputTargetCatalog = {
  environment: "test",
  default_policy: "explicit",
  default_output_target_ref: null,
  items: [{
    output_target_ref: "target-1",
    display_name: "Test workspace",
    summary: "Server-managed test output",
    is_default: false,
  }, {
    output_target_ref: "target-2",
    display_name: "Second workspace",
    summary: "Server-managed secondary output",
    is_default: false,
  }],
};

const blueprint: BlueprintRecord = {
  blueprint_id: "blueprint-1",
  version: 1,
  schema_version: "1.0.0",
  document: draft,
  checksum: "blueprint-checksum",
  created_at: "2026-07-15T01:00:00Z",
  updated_at: "2026-07-15T01:00:00Z",
  audit_id: "audit-blueprint",
};

const plan: AssemblyPlanRecord = {
  plan_id: "plan-1",
  version: 1,
  blueprint_id: blueprint.blueprint_id,
  blueprint_version: blueprint.version,
  environment: "test",
  document: { summary_checksum: "summary-checksum" },
  checksum: "plan-checksum",
  executable: true,
  confirmed: false,
  created_at: "2026-07-15T01:01:00Z",
  updated_at: "2026-07-15T01:01:00Z",
  audit_id: "audit-plan",
};

const running: AssemblyRunRecord = {
  run_id: "run-1",
  plan_id: plan.plan_id,
  plan_version: plan.version + 1,
  plan_checksum: plan.checksum,
  output_target_ref: "target-1",
  status: "generating",
  document: {},
  created_at: "2026-07-15T01:02:00Z",
  updated_at: "2026-07-15T01:03:00Z",
  audit_id: "audit-run",
};

function toPlanReady() {
  let state = createInitialCreateSoftwareState(draft);
  state = createSoftwareReducer(state, { type: "targets_requested", operationToken: "targets-op" });
  state = createSoftwareReducer(state, { type: "targets_loaded", operationToken: "targets-op", catalog: targets });
  state = createSoftwareReducer(state, { type: "validation_requested", operationToken: "validate-op", idempotencyKey: "blueprint-intent" });
  state = createSoftwareReducer(state, { type: "validation_succeeded", operationToken: "validate-op", draftRevision: 0, blueprint });
  state = createSoftwareReducer(state, { type: "plan_requested", operationToken: "plan-op", idempotencyKey: "plan-intent" });
  state = createSoftwareReducer(state, { type: "plan_succeeded", operationToken: "plan-op", plan });
  return state;
}

function toExecuting() {
  let state = toPlanReady();
  state = createSoftwareReducer(state, { type: "output_target_selected", outputTargetRef: "target-1" });
  state = createSoftwareReducer(state, { type: "execution_requested", operationToken: "run-op", idempotencyKey: "run-intent" });
  return state;
}

describe("createSoftwareReducer happy path", () => {
  it("moves through targets, validation, review, plan, execution, and success", () => {
    let state = toExecuting();
    expect(state).toMatchObject({
      phase: "executing",
      draftRevision: 0,
      validatedRevision: 0,
      validationIdempotencyKey: "blueprint-intent",
      planIdempotencyKey: "plan-intent",
      executionIdempotencyKey: "run-intent",
      selectedOutputTargetRef: "target-1",
    });

    state = createSoftwareReducer(state, { type: "run_observed", operationToken: "run-op", run: running });
    const succeededRun = { ...running, status: "completed" as const, completed_at: "2026-07-15T01:05:00Z" };
    state = createSoftwareReducer(state, {
      type: "execution_succeeded",
      operationToken: "run-op",
      run: succeededRun,
      productId: "product-1",
    });

    expect(state).toMatchObject({ phase: "succeeded", run: succeededRun, productId: "product-1", operationToken: null });
  });

  it("editing after validation invalidates the validation and plan closure", () => {
    const ready = toPlanReady();
    const changed = createSoftwareReducer(ready, {
      type: "draft_changed",
      draft: { ...draft, product: { code: "video-studio", name: "Changed" } },
    });

    expect(changed).toMatchObject({
      phase: "draft",
      draftRevision: 1,
      blueprint: null,
      validatedRevision: null,
      plan: null,
      validationIdempotencyKey: null,
      planIdempotencyKey: null,
      executionIdempotencyKey: null,
    });
  });

  it("changing the output target keeps the plan but clears the prior execution intent", () => {
    const state: CreateSoftwareState = {
      ...toPlanReady(),
      selectedOutputTargetRef: "target-1",
      executionIdempotencyKey: "old-run-intent",
    };

    const changed = createSoftwareReducer(state, { type: "output_target_selected", outputTargetRef: "target-2" });

    expect(changed.phase).toBe("plan_ready");
    expect(changed.plan).toBe(plan);
    expect(changed.selectedOutputTargetRef).toBe("target-2");
    expect(changed.executionIdempotencyKey).toBeNull();
  });

  it("rejects a target catalog from another plan environment", () => {
    const state = { ...toPlanReady(), outputTargets: { ...targets, environment: "production" as const } };
    expect(() => createSoftwareReducer(state, { type: "output_target_selected", outputTargetRef: "target-1" }))
      .toThrow("does not match the plan environment");
  });
});

describe("createSoftwareReducer concurrency and transition guards", () => {
  it("ignores stale operation tokens and stale draft revisions", () => {
    const loading = createSoftwareReducer(createInitialCreateSoftwareState(draft), {
      type: "targets_requested",
      operationToken: "current",
    });
    expect(createSoftwareReducer(loading, { type: "targets_loaded", operationToken: "stale", catalog: targets })).toBe(loading);

    let validating = createInitialCreateSoftwareState(draft);
    validating = createSoftwareReducer(validating, { type: "validation_requested", operationToken: "validate", idempotencyKey: "key" });
    expect(createSoftwareReducer(validating, {
      type: "validation_succeeded",
      operationToken: "validate",
      draftRevision: 99,
      blueprint,
    })).toBe(validating);
  });

  it("rejects illegal transitions and plans for a different blueprint revision", () => {
    expect(() => createSoftwareReducer(createInitialCreateSoftwareState(draft), {
      type: "plan_requested",
      operationToken: "plan",
      idempotencyKey: "key",
    })).toThrow(InvalidCreateSoftwareTransitionError);

    let state = createInitialCreateSoftwareState(draft);
    state = createSoftwareReducer(state, { type: "validation_requested", operationToken: "validate", idempotencyKey: "key" });
    state = createSoftwareReducer(state, { type: "validation_succeeded", operationToken: "validate", draftRevision: 0, blueprint });
    state = createSoftwareReducer(state, { type: "plan_requested", operationToken: "plan", idempotencyKey: "plan-key" });
    expect(() => createSoftwareReducer(state, {
      type: "plan_succeeded",
      operationToken: "plan",
      plan: { ...plan, blueprint_version: 2 },
    })).toThrow("plan does not match");
  });

  it("rejects double execution submission without changing the first intent", () => {
    const executing = toExecuting();
    expect(() => createSoftwareReducer(executing, {
      type: "execution_requested",
      operationToken: "second-run-op",
      idempotencyKey: "second-run-intent",
    })).toThrow(InvalidCreateSoftwareTransitionError);
  });

  it("requires explicit terminal actions instead of treating terminal runs as polling updates", () => {
    const executing = toExecuting();
    expect(() => createSoftwareReducer(executing, {
      type: "run_observed",
      operationToken: "run-op",
      run: { ...running, status: "failed" },
    })).toThrow("terminal runs require an explicit terminal transition");
  });

  it("rejects a run that is not locked to the confirmed plan version and checksum", () => {
    const executing = toExecuting();
    expect(() => createSoftwareReducer(executing, {
      type: "run_observed",
      operationToken: "run-op",
      run: { ...running, plan_version: plan.version, plan_checksum: "other" },
    })).toThrow("run does not match");
  });
});

describe("createSoftwareReducer failure recovery", () => {
  const retryableRunRead: RequestFailure = {
    requestId: "request-1",
    code: "assembly.run_read_unavailable",
    message: "Run state is temporarily unavailable",
    retryable: true,
    retryIntent: "get_run",
  };

  it("retains run identity, error evidence, and idempotency key while rereading a run", () => {
    let state = createSoftwareReducer(toExecuting(), { type: "run_observed", operationToken: "run-op", run: running });
    state = createSoftwareReducer(state, { type: "request_failed", operationToken: "run-op", failure: retryableRunRead });

    expect(state).toMatchObject({
      phase: "failed",
      run: running,
      executionIdempotencyKey: "run-intent",
      failure: retryableRunRead,
      failedFrom: "executing",
    });

    state = createSoftwareReducer(state, { type: "retry_requested", operationToken: "read-run-again" });
    expect(state).toMatchObject({
      phase: "executing",
      operationToken: "read-run-again",
      run: running,
      executionIdempotencyKey: "run-intent",
      failure: null,
    });
  });

  it("records a failed backend run and only permits a declared reread", () => {
    const failedRun = { ...running, status: "failed" as const, completed_at: "2026-07-15T01:06:00Z" };
    const failed = createSoftwareReducer(toExecuting(), {
      type: "execution_failed",
      operationToken: "run-op",
      run: failedRun,
      failure: retryableRunRead,
    });
    expect(failed).toMatchObject({ phase: "failed", run: failedRun, failure: retryableRunRead });
    expect(() => createSoftwareReducer(failed, { type: "draft_changed", draft })).toThrow(InvalidCreateSoftwareTransitionError);
    expect(() => createSoftwareReducer(failed, { type: "output_target_selected", outputTargetRef: "target-2" }))
      .toThrow(InvalidCreateSoftwareTransitionError);
  });

  it("rejects mismatched or terminal retry intents", () => {
    const loading = createSoftwareReducer(createInitialCreateSoftwareState(draft), {
      type: "targets_requested",
      operationToken: "targets",
    });
    expect(() => createSoftwareReducer(loading, {
      type: "request_failed",
      operationToken: "targets",
      failure: { ...retryableRunRead, retryIntent: "start_assembly" },
    })).toThrow("retry intent does not match");

    const terminal = createSoftwareReducer(loading, {
      type: "request_failed",
      operationToken: "targets",
      failure: { code: "catalog.denied", message: "Denied", retryable: false, retryIntent: null },
    });
    expect(() => createSoftwareReducer(terminal, { type: "retry_requested", operationToken: "retry" }))
      .toThrow(InvalidCreateSoftwareTransitionError);
  });

  it("does not let a persisted failed run masquerade as a fresh assembly retry", () => {
    const failedRun = { ...running, status: "failed" as const };
    expect(() => createSoftwareReducer(toExecuting(), {
      type: "execution_failed",
      operationToken: "run-op",
      run: failedRun,
      failure: { code: "assembly.failed", message: "Failed", retryable: true, retryIntent: "start_assembly" },
    })).toThrow("business recovery requires a server action");
  });

  it("preserves structured API field errors and does not retry terminal errors", () => {
    const failure = assemblyRequestFailure(new AuthApiError("Invalid blueprint", {
      status: 422,
      code: "assembly.document_invalid",
      retryable: false,
      requestId: "request-422",
      detail: "Blueprint validation failed",
      fieldErrors: [{ field: "packages", code: "min_items", message: "Select a package" }],
    }), "validate_blueprint");
    expect(failure).toEqual({
      requestId: "request-422",
      code: "assembly.document_invalid",
      message: "Invalid blueprint",
      retryable: false,
      retryIntent: null,
      detail: "Blueprint validation failed",
      retryAfterSeconds: undefined,
      fieldErrors: [{ field: "packages", code: "min_items", message: "Select a package" }],
    });
  });

  it("distinguishes retryable timeouts from terminal user cancellation", () => {
    expect(assemblyRequestFailure(new DOMException("Timed out", "TimeoutError"), "get_run"))
      .toMatchObject({ code: "assembly.request_timeout", retryable: true, retryIntent: "get_run" });
    expect(assemblyRequestFailure(new DOMException("Cancelled", "AbortError"), "get_run"))
      .toMatchObject({ code: "assembly.request_cancelled", retryable: false, retryIntent: null });
  });
});
