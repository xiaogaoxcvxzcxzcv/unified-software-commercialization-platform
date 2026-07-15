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
    authenticatedRequest.mockResolvedValue({});
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

  it("uses stable encoded read paths and forwards AbortSignal", async () => {
    authenticatedRequest.mockResolvedValue({});
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

  it("reuses the exact key when the caller repeats an idempotent intent", async () => {
    authenticatedRequest.mockResolvedValue({});
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
    expect(() => assemblyClient.getRun("run-1", { timeoutMs: 0 })).toThrow("timeoutMs must be an integer");
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
