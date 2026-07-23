import { beforeEach, describe, expect, it, vi } from "vitest";

const authenticatedRequest = vi.hoisted(() => vi.fn());

vi.mock("../api/authClient", () => ({
  authenticatedAdminRequest: authenticatedRequest,
}));

import {
  assemblyClient,
  assertTrustedToolSelections,
  type ProductBlueprintDocument,
} from "../api/assemblyClient";

const blueprint: ProductBlueprintDocument = {
  schema_version: "1.0.0",
  product: { code: "video-studio", name: "Video Studio" },
  generator: { id: "generator.web-react", version: "1.0.0" },
  sdk: { id: "sdk.typescript", version: "1.0.0" },
};

const catalogFilter = { target: "web", delivery_mode: "generated_source", environment: "development" } as const;
const ordinaryCatalog = {
  catalog_scope: "ordinary",
  catalog_revision: "catalog-ordinary-1",
  ...catalogFilter,
  packages: [{
    package_id: "package.account", version: "1.0.0", name: "统一账号", user_value: "Web / H5 登录与账号中心",
    dependencies: [], conflicts: [], compatible_template_refs: [{ id: "standard-a", version: "1.0.0" }],
  }],
  templates: [{ template_id: "standard-a", version: "1.0.0", name: "标准界面", supported_blocks: ["account.profile"] }],
  generators: [{ id: "platform.generator", version: "1.0.0", name: "平台生成器" }],
  sdks: [{ id: "platform.sdk", version: "1.0.0", name: "TypeScript SDK" }],
};
const runResponse = {
  run_id: "run-1", product_id: null, plan_id: "plan-1", plan_version: 3, version: 1,
  plan_checksum: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  root_run_id: "run-1", retry_of_run_id: null, attempt_number: 1, output_target_ref: "target-test-1",
  status: "planned", current_step_id: null, steps: [], recovery: { retryable: false, rollback_required: false, resume_from_step_id: null },
  diagnostics: [], reports: [], document: {}, created_at: "2026-07-16T01:00:00Z", updated_at: "2026-07-16T01:00:00Z",
  completed_at: null, audit_id: "audit-run-1",
};
const blueprintResponse = {
  blueprint_id: "blueprint-1", version: 2, schema_version: "1.0.0", environments: ["test"], document: blueprint,
  checksum: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  created_at: "2026-07-16T01:00:00Z", updated_at: "2026-07-16T01:00:00Z", audit_id: "audit-blueprint-1",
};
const planResponse = {
  plan_id: "plan-1", version: 3, blueprint_id: "blueprint-1", blueprint_version: 2, schema_version: "1.0.0", environment: "test",
  confirmation_checksum: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", document: {},
  review: { packages: [{ package_id: "package.account", version: "1.0.0" }], applications: [{ application_id: "application.web", target: "web", channel: "web", delivery_mode: "generated_source", template_id: "standard-a", template_version: "1.0.0" }], risks: [], blocking_conflict_count: 0, statements: ["Confirm assembly plan"] },
  checksum: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", executable: true, confirmed: false,
  created_at: "2026-07-16T01:00:00Z", updated_at: "2026-07-16T01:00:00Z", audit_id: "audit-plan-1",
};

beforeEach(() => authenticatedRequest.mockReset());

describe("assemblyClient request contract", () => {
  it("uses separate fixed ordinary and experimental catalog paths without a scope parameter", async () => {
    authenticatedRequest.mockResolvedValueOnce(ordinaryCatalog).mockResolvedValueOnce({ ...ordinaryCatalog, catalog_scope: "experimental" });
    await assemblyClient.listOrdinaryCatalogOptions(catalogFilter);
    await assemblyClient.listExperimentalCatalogOptions(catalogFilter);
    expect(authenticatedRequest.mock.calls.map(([path]) => path)).toEqual([
      "/api/v1/admin/assembly-catalog-options?target=web&delivery_mode=generated_source&environment=development",
      "/api/v1/admin/experimental/assembly-catalog-options?target=web&delivery_mode=generated_source&environment=development",
    ]);
    expect(authenticatedRequest.mock.calls.every(([path]) => !String(path).includes("scope="))).toBe(true);
  });

  it("accepts prose slashes but rejects host paths and unknown catalog fields", async () => {
    authenticatedRequest.mockResolvedValueOnce(ordinaryCatalog);
    await expect(assemblyClient.listOrdinaryCatalogOptions(catalogFilter)).resolves.toEqual(ordinaryCatalog);
    authenticatedRequest.mockResolvedValueOnce({ ...ordinaryCatalog, packages: [{ ...ordinaryCatalog.packages[0], user_value: "D:/private/source" }] });
    await expect(assemblyClient.listOrdinaryCatalogOptions(catalogFilter)).rejects.toThrow("host path");
    authenticatedRequest.mockResolvedValueOnce({ ...ordinaryCatalog, catalog_root: "private" });
    await expect(assemblyClient.listOrdinaryCatalogOptions(catalogFilter)).rejects.toThrow("unknown or missing fields");
  });

  it("rejects endpoint scope/filter mismatches and unstable option order", async () => {
    authenticatedRequest.mockResolvedValueOnce({ ...ordinaryCatalog, catalog_scope: "experimental" });
    await expect(assemblyClient.listOrdinaryCatalogOptions(catalogFilter)).rejects.toThrow("scope does not match");
    authenticatedRequest.mockResolvedValueOnce({ ...ordinaryCatalog, environment: "test" });
    await expect(assemblyClient.listOrdinaryCatalogOptions(catalogFilter)).rejects.toThrow("filters do not match");
    authenticatedRequest.mockResolvedValueOnce({ ...ordinaryCatalog, generators: [{ id: "z-generator", version: "1.0.0", name: "Z" }, { id: "a-generator", version: "1.0.0", name: "A" }] });
    await expect(assemblyClient.listOrdinaryCatalogOptions(catalogFilter)).rejects.toThrow("stable order");
  });

  it("loads the exact redacted environment-scoped output target catalog", async () => {
    authenticatedRequest.mockResolvedValue({
      environment: "production",
      default_policy: "explicit",
      default_output_target_ref: null,
      items: [{
        output_target_ref: "target-prod-1",
        display_name: "Production workspace",
        summary: "Approved server-managed output",
        is_default: false,
      }],
    });
    const controller = new AbortController();

    const result = await assemblyClient.listOutputTargets("production", { signal: controller.signal });

    expect(authenticatedRequest).toHaveBeenCalledWith(
      "/api/v1/admin/assembly-output-targets?environment=production",
      { signal: controller.signal },
    );
    expect(result).toEqual({
      environment: "production",
      default_policy: "explicit",
      default_output_target_ref: null,
      items: [{
        output_target_ref: "target-prod-1",
        display_name: "Production workspace",
        summary: "Approved server-managed output",
        is_default: false,
      }],
    });
  });

  it("rejects any leaked path or unknown output-target field", async () => {
    authenticatedRequest.mockResolvedValue({
      environment: "production", default_policy: "explicit", default_output_target_ref: null,
      items: [{ output_target_ref: "target-prod-1", display_name: "Production", summary: "Managed", is_default: false, target_root: "D:/private" }],
    });
    await expect(assemblyClient.listOutputTargets("production")).rejects.toThrow("unknown or missing fields");
  });

  it("rejects a host path smuggled through an otherwise allowed display field", async () => {
    authenticatedRequest.mockResolvedValue({
      environment: "production", default_policy: "explicit", default_output_target_ref: null,
      items: [{ output_target_ref: "target-prod-1", display_name: "Production", summary: "D:/private/source", is_default: false }],
    });
    await expect(assemblyClient.listOutputTargets("production")).rejects.toThrow("must not contain a host path");
  });

  it("rejects control characters and references shorter than the contract minimum", async () => {
    authenticatedRequest.mockResolvedValueOnce({
      environment: "production", default_policy: "explicit", default_output_target_ref: null,
      items: [{ output_target_ref: "target-prod-1", display_name: "Production\u0000workspace", summary: "Managed", is_default: false }],
    });
    await expect(assemblyClient.listOutputTargets("production")).rejects.toThrow("must not contain a host path");

    authenticatedRequest.mockResolvedValueOnce({
      environment: "production", default_policy: "explicit", default_output_target_ref: null,
      items: [{ output_target_ref: "a", display_name: "Production", summary: "Managed", is_default: false }],
    });
    await expect(assemblyClient.listOutputTargets("production")).rejects.toThrow("exceeds its contract limit");
  });

  it("rejects an inconsistent explicit default instead of choosing the first item", async () => {
    authenticatedRequest.mockResolvedValue({
      environment: "staging", default_policy: "explicit", default_output_target_ref: "target-staging-2",
      items: [{ output_target_ref: "target-staging-1", display_name: "Staging", summary: "Managed", is_default: true }],
    });
    await expect(assemblyClient.listOutputTargets("staging")).rejects.toThrow("default is inconsistent");
  });

  it("rejects a response for another environment or unstable target order", async () => {
    authenticatedRequest.mockResolvedValueOnce({
      environment: "test", default_policy: "explicit", default_output_target_ref: null, items: [],
    });
    await expect(assemblyClient.listOutputTargets("production")).rejects.toThrow("does not match the request");

    authenticatedRequest.mockResolvedValueOnce({
      environment: "production", default_policy: "explicit", default_output_target_ref: null,
      items: [
        { output_target_ref: "workspace.zeta", display_name: "Zeta", summary: "Managed", is_default: false },
        { output_target_ref: "workspace.alpha", display_name: "Alpha", summary: "Managed", is_default: false },
      ],
    });
    await expect(assemblyClient.listOutputTargets("production")).rejects.toThrow("not in stable order");
  });

  it("sends every POST with the caller-owned idempotency key, body, and AbortSignal", async () => {
    authenticatedRequest.mockImplementation(async (path?: string) => String(path).endsWith("/assemble") ? runResponse : String(path).endsWith("/plan") ? planResponse : blueprintResponse);
    const controller = new AbortController();
    const options = { idempotencyKey: "intent-key-00001", signal: controller.signal };

    await assemblyClient.createBlueprint(blueprint, options);
    await assemblyClient.createPlan("blueprint-1", { blueprint_version: 2, environment: "test" }, options);
    await assemblyClient.startAssembly("blueprint-1", {
      plan_id: "plan-1",
      expected_plan_version: 3,
      plan_checksum: "sha256-plan",
      confirmation: { accepted: true, summary_checksum: "sha256-summary" },
      output_target_ref: "target-test-1",
    }, options);

    expect(authenticatedRequest).toHaveBeenNthCalledWith(1, "/api/v1/admin/blueprints", {
      method: "POST",
      headers: { "Idempotency-Key": "intent-key-00001" },
      body: JSON.stringify(blueprint),
      signal: controller.signal,
    });
    expect(authenticatedRequest).toHaveBeenNthCalledWith(2, "/api/v1/admin/blueprints/blueprint-1/plan", {
      method: "POST",
      headers: { "Idempotency-Key": "intent-key-00001" },
      body: JSON.stringify({ blueprint_version: 2, environment: "test" }),
      signal: controller.signal,
    });
    expect(authenticatedRequest).toHaveBeenNthCalledWith(3, "/api/v1/admin/blueprints/blueprint-1/assemble", {
      method: "POST",
      headers: { "Idempotency-Key": "intent-key-00001" },
      body: JSON.stringify({
        plan_id: "plan-1",
        expected_plan_version: 3,
        plan_checksum: "sha256-plan",
        confirmation: { accepted: true, summary_checksum: "sha256-summary" },
        output_target_ref: "target-test-1",
      }),
      signal: controller.signal,
    });
  });

  it("uses the separate experimental plan route without putting catalog scope in the body", async () => {
    authenticatedRequest.mockResolvedValue(planResponse);
    const controller = new AbortController();
    await assemblyClient.createExperimentalPlan("blueprint-1", { blueprint_version: 2, environment: "test" }, { idempotencyKey: "intent-key-00001", signal: controller.signal });
    expect(authenticatedRequest).toHaveBeenCalledWith("/api/v1/admin/experimental/blueprints/blueprint-1/plan", {
      method: "POST",
      headers: { "Idempotency-Key": "intent-key-00001" },
      body: JSON.stringify({ blueprint_version: 2, environment: "test" }),
      signal: controller.signal,
    });
    expect(String(authenticatedRequest.mock.calls[0][1].body)).not.toContain("catalog_scope");
  });

  it("accepts stable nullable run step timestamps from the backend projection", async () => {
    authenticatedRequest.mockResolvedValue({
      ...runResponse,
      steps: [{
        step_id: "step.provision",
        kind: "provision",
        status: "pending",
        attempt: 0,
        compensation_status: "pending",
        started_at: null,
        finished_at: null,
        diagnostic_ids: [],
      }],
    });

    const result = await assemblyClient.getRun("run-1");

    expect(result.steps[0]).toMatchObject({
      step_id: "step.provision",
      started_at: null,
      finished_at: null,
    });
  });

  it("uses stable encoded read paths and forwards AbortSignal", async () => {
    const manifestResponse = { assembly_id: "assembly:1", product_id: "product-1", run_id: "run-1", schema_version: "1.0.0", document: {}, document_checksum: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", checksum: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", created_at: "2026-07-16T01:00:00Z" };
    const lockResponse = { lock_id: "lock:1", product_id: "product-1", run_id: "run-1", assembly_id: "assembly:1", schema_version: "1.0.0", document: {}, document_checksum: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", checksum: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", created_at: "2026-07-16T01:00:00Z" };
    authenticatedRequest.mockImplementation(async (path?: string) => String(path).includes("/assembly-runs/") ? runResponse : String(path).includes("/assembly-plans/") ? planResponse : String(path).includes("/blueprints/") ? blueprintResponse : String(path).includes("/assembly-manifests/") ? manifestResponse : String(path).includes("/generated-project-locks/") ? lockResponse : {});
    const controller = new AbortController();
    const options = { signal: controller.signal };

    await assemblyClient.getBlueprint("blueprint:1", options);
    await assemblyClient.getPlan("plan:1", options);
    await assemblyClient.getRun("run:1", options);
    await assemblyClient.getManifest("assembly:1", options);
    await assemblyClient.getGeneratedProjectLock("lock:1", options);

    expect(authenticatedRequest.mock.calls.map(([path]) => path)).toEqual([
      "/api/v1/admin/blueprints/blueprint%3A1",
      "/api/v1/admin/assembly-plans/plan%3A1",
      "/api/v1/admin/assembly-runs/run%3A1",
      "/api/v1/admin/assembly-manifests/assembly%3A1",
      "/api/v1/admin/generated-project-locks/lock%3A1",
    ]);
    for (const [, init] of authenticatedRequest.mock.calls) expect(init).toEqual(options);
  });

  it("strictly parses blueprint recovery environments without trusting raw document", async () => {
    authenticatedRequest.mockResolvedValueOnce({ ...blueprintResponse, environments: ["test"], document: { ...blueprint, applications: [{ environment: "production" }] } });
    await expect(assemblyClient.getBlueprint("blueprint-1")).resolves.toMatchObject({ environments: ["test"] });
    const { environments: _missing, ...withoutEnvironments } = blueprintResponse;
    authenticatedRequest.mockResolvedValueOnce(withoutEnvironments);
    await expect(assemblyClient.getBlueprint("blueprint-1")).rejects.toThrow("unknown or missing fields");
    authenticatedRequest.mockResolvedValueOnce({ ...blueprintResponse, environments: ["test", "development"] });
    await expect(assemblyClient.getBlueprint("blueprint-1")).rejects.toThrow("unique and stably sorted");
  });

  it("strictly parses the top-level confirmation checksum and closed plan review", async () => {
    authenticatedRequest.mockResolvedValueOnce({ ...planResponse, document: { confirmation: { summary_checksum: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" } } });
    await expect(assemblyClient.getPlan("plan-1")).resolves.toMatchObject({ confirmation_checksum: planResponse.confirmation_checksum, review: planResponse.review });
    const { review: _missing, ...withoutReview } = planResponse;
    authenticatedRequest.mockResolvedValueOnce(withoutReview);
    await expect(assemblyClient.getPlan("plan-1")).rejects.toThrow("unknown or missing fields");
    authenticatedRequest.mockResolvedValueOnce({ ...planResponse, review: { ...planResponse.review, raw_path: "D:/private" } });
    await expect(assemblyClient.getPlan("plan-1")).rejects.toThrow("unknown or missing fields");
  });

  it("reuses the exact key when the caller repeats an idempotent intent", async () => {
    authenticatedRequest.mockResolvedValue(planResponse);
    const input = { blueprint_version: 1, environment: "development" as const };
    const options = { idempotencyKey: "same-plan-intent" };

    await assemblyClient.createPlan("blueprint-1", input, options);
    await assemblyClient.createPlan("blueprint-1", input, options);

    expect(authenticatedRequest).toHaveBeenCalledTimes(2);
    expect(authenticatedRequest.mock.calls[0][1]).toEqual(authenticatedRequest.mock.calls[1][1]);
  });

  it("aborts a timed-out request and rejects invalid timeout bounds", async () => {
    authenticatedRequest.mockResolvedValue({
      environment: "test", default_policy: "explicit", default_output_target_ref: null, items: [],
    });
    await assemblyClient.listOutputTargets("test", { timeoutMs: 5 });
    const signal = (authenticatedRequest.mock.calls[0][1] as RequestInit).signal!;
    await new Promise<void>((resolve) => signal.aborted ? resolve() : signal.addEventListener("abort", () => resolve(), { once: true }));
    expect(signal.reason).toMatchObject({ name: "TimeoutError" });
    await expect(assemblyClient.getRun("run-1", { timeoutMs: 0 })).rejects.toThrow("timeoutMs must be an integer");
  });

  it("lists typed durable runs and retries with the optimistic version", async () => {
    const summary = {
      run_id: "run-1", product_id: null, plan_id: "plan-1", version: 2, root_run_id: "run-1", retry_of_run_id: null,
      attempt_number: 1, status: "failed", current_step_id: "step-generate", diagnostic_count: 1, report_count: 0,
      created_at: "2026-07-16T01:00:00Z", updated_at: "2026-07-16T01:05:00Z", completed_at: "2026-07-16T01:05:00Z",
    };
    authenticatedRequest.mockResolvedValueOnce({ items: [summary], next_cursor: "cursor-2" }).mockResolvedValueOnce({
      ...runResponse, status: "planned", version: 1, run_id: "run-2", root_run_id: "run-1", retry_of_run_id: "run-1", attempt_number: 2,
    });
    const page = await assemblyClient.listRuns({ page_size: 20, status: "failed" });
    const retried = await assemblyClient.retryRun("run-1", 2, { idempotencyKey: "assembly-retry-00001" });
    expect(page).toEqual({ items: [summary], next_cursor: "cursor-2" });
    expect(retried.run_id).toBe("run-2");
    expect(authenticatedRequest).toHaveBeenNthCalledWith(1, "/api/v1/admin/assembly-runs?page_size=20&status=failed", { signal: undefined });
    expect(authenticatedRequest).toHaveBeenNthCalledWith(2, "/api/v1/admin/assembly-runs/run-1/retry", expect.objectContaining({
      method: "POST", headers: { "Idempotency-Key": "assembly-retry-00001" }, body: JSON.stringify({ expected_version: 2 }),
    }));
  });

  it("accepts the persisted cancelled run terminal state", async () => {
    authenticatedRequest.mockResolvedValue({ ...runResponse, status: "cancelled", version: 2, completed_at: "2026-07-16T01:01:00Z" });
    await expect(assemblyClient.getRun("run-1")).resolves.toMatchObject({ status: "cancelled", version: 2 });
  });

  it("rejects unknown fields and host paths in diagnostic projections", async () => {
    authenticatedRequest.mockResolvedValueOnce({ ...runResponse, diagnostics: [{
      diagnostic_id: "diagnostic-1", code: "assembly.render_failed", severity: "error", category: "generator",
      message: "Render failed", blocking: true, retryable: true, remediation: ["Review generated output"], related_paths: ["D:/private/source"],
    }] });
    await expect(assemblyClient.getRun("run-1")).rejects.toThrow("related_path is invalid");
    authenticatedRequest.mockResolvedValueOnce({ ...runResponse, internal_error: "secret" });
    await expect(assemblyClient.getRun("run-1")).rejects.toThrow("unknown or missing fields");
  });

  it("rejects missing idempotency keys before making a write request", () => {
    expect(() => assemblyClient.createBlueprint(blueprint, { idempotencyKey: " " }))
      .toThrow("idempotencyKey is invalid");
    expect(authenticatedRequest).not.toHaveBeenCalled();
  });
});

describe("trusted tool browser boundary", () => {
  it("accepts only id and version selections", () => {
    expect(() => assertTrustedToolSelections(blueprint)).not.toThrow();
  });

  it.each([
    ["scope", { scope: "ordinary" }],
    ["checksum", { checksum: "sha256-secret" }],
    ["content", { content: { entrypoint: "index.js" } }],
    ["adapter", { adapter: "builtin" }],
    ["command", { command: "node index.js" }],
    ["path", { path: "../../outside" }],
  ])("rejects browser-supplied generator %s", (_field, extra) => {
    const document = {
      ...blueprint,
      generator: { ...blueprint.generator, ...extra },
    } as ProductBlueprintDocument;

    expect(() => assemblyClient.createBlueprint(document, { idempotencyKey: "blueprint-intent" }))
      .toThrow("is not accepted from the browser");
    expect(authenticatedRequest).not.toHaveBeenCalled();
  });

  it("rejects incomplete or extra SDK selections", () => {
    expect(() => assertTrustedToolSelections({ ...blueprint, sdk: { id: "sdk.typescript" } } as ProductBlueprintDocument))
      .toThrow("must contain only id and version");
    expect(() => assertTrustedToolSelections({ ...blueprint, sdk: { ...blueprint.sdk, label: "fake" } } as ProductBlueprintDocument))
      .toThrow("is not accepted from the browser");
  });

  it("rejects browser-supplied top-level catalog and tool location fields", () => {
    for (const field of ["catalog_scope", "catalog_readiness", "generator_path", "sdk_checksum"]) {
      const document = { ...blueprint, [field]: "untrusted" } as ProductBlueprintDocument;
      expect(() => assemblyClient.createBlueprint(document, { idempotencyKey: "blueprint-intent" }))
        .toThrow("is not accepted from the browser");
    }
    expect(authenticatedRequest).not.toHaveBeenCalled();
  });
});
