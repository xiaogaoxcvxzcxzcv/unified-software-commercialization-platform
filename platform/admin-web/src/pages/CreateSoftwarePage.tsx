import {
  IconAlertTriangle,
  IconArrowLeft,
  IconArrowRight,
  IconBox,
  IconCheck,
  IconDeviceDesktop,
  IconFileDescription,
  IconLoader2,
  IconRefresh,
  IconServer,
  IconShieldLock,
} from "@tabler/icons-react";
import { FormEvent, useEffect, useMemo, useReducer, useRef, useState } from "react";
import {
  assemblyClient,
  type AssemblyCatalogFilter,
  type AssemblyCatalogOptions,
  type AssemblyCatalogScope,
  type AssemblyDeliveryMode,
  type AssemblyEnvironment,
  type AssemblyTarget,
  type JsonObject,
  type ProductBlueprintDocument,
} from "../api/assemblyClient";
import { AuthApiError } from "../api/authClient";
import { Shell } from "../components/Shell";
import {
  assemblyRequestFailure,
  createInitialCreateSoftwareState,
  createSoftwareReducer,
} from "../features/assembly/createSoftwareMachine";

const steps = ["基本资料", "目标与能力", "界面与配置", "计划审阅", "确认"];
const targets: Array<{ value: AssemblyTarget; label: string }> = [
  { value: "web", label: "Web" },
  { value: "desktop_webview", label: "桌面 WebView" },
  { value: "h5", label: "H5" },
  { value: "wechat_miniprogram", label: "微信小程序" },
  { value: "mobile_app", label: "移动 App" },
];
const deliveryModes: Array<{ value: AssemblyDeliveryMode; label: string }> = [
  { value: "generated_source", label: "生成源码" },
  { value: "package", label: "版本化组件" },
  { value: "hosted", label: "托管界面" },
];
const environments: Array<{ value: AssemblyEnvironment; label: string }> = [
  { value: "development", label: "开发" },
  { value: "test", label: "测试" },
  { value: "staging", label: "预发布" },
  { value: "production", label: "生产" },
];

interface WizardDraft {
  name: string;
  code: string;
  brandName: string;
  target: AssemblyTarget;
  deliveryMode: AssemblyDeliveryMode;
  environment: AssemblyEnvironment;
  applicationId: string;
  channel: string;
  outputPath: string;
  outputRoot: string;
  packageRefs: string[];
  templateRef: string;
  generatorRef: string;
  sdkRef: string;
  provider: string;
  providerConfigRef: string;
  providerSecretKeys: string;
}

const initialDraft: WizardDraft = {
  name: "",
  code: "",
  brandName: "",
  target: "web",
  deliveryMode: "generated_source",
  environment: "development",
  applicationId: "application.web",
  channel: "web",
  outputPath: "apps/web",
  outputRoot: "generated/software",
  packageRefs: [],
  templateRef: "",
  generatorRef: "",
  sdkRef: "",
  provider: "",
  providerConfigRef: "",
  providerSecretKeys: "",
};

const placeholderDocument: ProductBlueprintDocument = {
  schema_version: "1.0.0",
  generator: { id: "pending.generator", version: "0.0.0" },
  sdk: { id: "pending.sdk", version: "0.0.0" },
};

const refKey = (id: string, version: string) => `${id}@${version}`;
const splitRef = (value: string) => {
  const index = value.lastIndexOf("@");
  return { id: value.slice(0, index), version: value.slice(index + 1) };
};
const createIntentKey = (kind: string) => `${kind}-${globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`}`;
const safeRelativePath = (value: string) => Boolean(value)
  && !value.startsWith("/")
  && !value.startsWith("\\")
  && !/^[A-Za-z]:/.test(value)
  && !value.includes("\\")
  && value.split("/").every((part) => part && part !== "." && part !== "..");
const isIdentifier = (value: string) => /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value);

function manifestIdFromURL(value: string | null | undefined) {
  if (!value) return null;
  const parsed = new URL(value, window.location.origin);
  if (parsed.origin !== window.location.origin || parsed.search || parsed.hash) return null;
  const match = parsed.pathname.match(/^\/api\/v1\/admin\/assembly-manifests\/([^/]+)$/);
  if (!match) return null;
  const manifestId = decodeURIComponent(match[1]);
  return isIdentifier(manifestId) ? manifestId : null;
}

function draftDocument(draft: WizardDraft): ProductBlueprintDocument {
  const generator = splitRef(draft.generatorRef);
  const sdk = splitRef(draft.sdkRef);
  const template = splitRef(draft.templateRef);
  const providerRefs = draft.provider.trim() ? [{
    provider: draft.provider.trim(),
    environment: draft.environment,
    config_ref: draft.providerConfigRef.trim(),
    secret_refs: draft.providerSecretKeys.split(",").map((item) => item.trim()).filter(Boolean).map((key) => ({
      provider: draft.provider.trim(), key, environment: draft.environment,
    })),
  }] : [];
  return {
    schema_version: "1.0.0",
    blueprint_id: `bp_${draft.code}`,
    version: "1.0.0",
    product: { code: draft.code, name: draft.name.trim(), ...(draft.brandName.trim() ? { brand_name: draft.brandName.trim() } : {}) },
    packages: draft.packageRefs.map((item) => {
      const value = splitRef(item);
      return { package_id: value.id, version: value.version };
    }),
    applications: [{
      application_id: draft.applicationId,
      target: draft.target,
      channel: draft.channel,
      environment: draft.environment,
      ui: { template_id: template.id, version: template.version, delivery_mode: draft.deliveryMode },
      output_path: draft.outputPath,
    }],
    provider_refs: providerRefs,
    extensions: [],
    generator,
    sdk,
    output_root: draft.outputRoot,
  } as ProductBlueprintDocument;
}

function arrayField(document: JsonObject | null | undefined, field: string) {
  const value = document?.[field];
  return Array.isArray(value) ? value : [];
}

function confirmationField(document: JsonObject | null | undefined) {
  const value = document?.confirmation;
  return value && typeof value === "object" && !Array.isArray(value) ? value as JsonObject : null;
}

export function CreateSoftwarePage({ catalogScope = "ordinary" }: { catalogScope?: AssemblyCatalogScope }) {
  const [step, setStep] = useState(0);
  const [draft, setDraft] = useState(initialDraft);
  const [catalog, setCatalog] = useState<AssemblyCatalogOptions | null>(null);
  const [outputTargetRef, setOutputTargetRef] = useState("");
  const [catalogLoading, setCatalogLoading] = useState(true);
  const [catalogError, setCatalogError] = useState<string | null>(null);
  const [reloadVersion, setReloadVersion] = useState(0);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [state, dispatch] = useReducer(createSoftwareReducer, placeholderDocument, createInitialCreateSoftwareState);
  const activeRequest = useRef<AbortController | null>(null);
  const executionSubmitting = useRef(false);
  const filter = useMemo<AssemblyCatalogFilter>(() => ({
    target: draft.target,
    delivery_mode: draft.deliveryMode,
    environment: draft.environment,
  }), [draft.target, draft.deliveryMode, draft.environment]);

  useEffect(() => {
    const controller = new AbortController();
    activeRequest.current?.abort();
    activeRequest.current = controller;
    setCatalogLoading(true);
    setCatalogError(null);
    setCatalog(null);
    setOutputTargetRef("");
    setDraft((current) => ({ ...current, packageRefs: [], templateRef: "", generatorRef: "", sdkRef: "" }));
    const listCatalog = catalogScope === "experimental"
      ? assemblyClient.listExperimentalCatalogOptions
      : assemblyClient.listOrdinaryCatalogOptions;
    void Promise.all([
      listCatalog(filter, { signal: controller.signal, timeoutMs: 20_000 }),
      assemblyClient.listOutputTargets(filter.environment, { signal: controller.signal, timeoutMs: 20_000 }),
    ]).then(([nextCatalog, nextTargets]) => {
      if (controller.signal.aborted) return;
      setCatalog(nextCatalog);
      const defaultRef = nextTargets.default_output_target_ref ?? "";
      setOutputTargetRef(defaultRef);
      const token = createIntentKey("targets");
      dispatch({ type: "targets_requested", operationToken: token });
      dispatch({ type: "targets_loaded", operationToken: token, catalog: nextTargets });
    }).catch((reason: unknown) => {
      if (controller.signal.aborted) return;
      if (catalogScope === "experimental" && reason instanceof AuthApiError && reason.status === 403) {
        setCatalogError("当前管理员未获授权使用受控实验目录。");
      } else {
        setCatalogError(reason instanceof Error ? reason.message : "创建选项目录加载失败");
      }
    }).finally(() => {
      if (!controller.signal.aborted) setCatalogLoading(false);
    });
    return () => controller.abort();
  }, [catalogScope, filter, reloadVersion]);

  const updateDraft = <K extends keyof WizardDraft>(field: K, value: WizardDraft[K]) => {
    setDraft((current) => ({ ...current, [field]: value }));
    setFieldErrors((current) => {
      const next = { ...current };
      delete next[field];
      return next;
    });
  };

  const selectedPackages = catalog?.packages.filter((item) => draft.packageRefs.includes(refKey(item.package_id, item.version))) ?? [];
  const compatibleTemplateKeys = selectedPackages.length === 0
    ? new Set<string>()
    : selectedPackages.reduce<Set<string> | null>((current, item) => {
      const own = new Set(item.compatible_template_refs.map((entry) => refKey(entry.id, entry.version)));
      return current === null ? own : new Set([...current].filter((entry) => own.has(entry)));
    }, null) ?? new Set<string>();
  const compatibleTemplates = catalog?.templates.filter((item) => compatibleTemplateKeys.has(refKey(item.template_id, item.version))) ?? [];
  const catalogReady = Boolean(catalog && catalog.packages.length && catalog.templates.length && catalog.generators.length && catalog.sdks.length);
  const basicValid = Boolean(draft.name.trim() && /^[a-z][a-z0-9-]{2,63}$/.test(draft.code));
  const targetValid = Boolean(catalogReady && draft.packageRefs.length);
  const configurationValid = Boolean(
    draft.templateRef && draft.generatorRef && draft.sdkRef
    && isIdentifier(draft.applicationId) && isIdentifier(draft.channel)
    && safeRelativePath(draft.outputPath) && safeRelativePath(draft.outputRoot)
    && (!draft.provider.trim() || isIdentifier(draft.provider.trim())
      && safeRelativePath(draft.providerConfigRef.trim())
      && draft.providerSecretKeys.split(",").map((item) => item.trim()).filter(Boolean).every((key) => /^[A-Z][A-Z0-9_]{0,127}$/.test(key))),
  );

  const validateBlueprint = async (event: FormEvent) => {
    event.preventDefault();
    if (!catalog || !basicValid || !targetValid || !configurationValid || state.phase !== "draft") return;
    const document = draftDocument(draft);
    const revision = state.draftRevision + 1;
    const operationToken = createIntentKey("validate-op");
    const idempotencyKey = createIntentKey("blueprint");
    dispatch({ type: "draft_changed", draft: document });
    dispatch({ type: "validation_requested", operationToken, idempotencyKey });
    setFieldErrors({});
    try {
      const blueprint = await assemblyClient.createBlueprint(document, { idempotencyKey, timeoutMs: 30_000 });
      dispatch({ type: "validation_succeeded", operationToken, draftRevision: revision, blueprint });
      setStep(3);
    } catch (reason) {
      const failure = assemblyRequestFailure(reason, "validate_blueprint");
      dispatch({ type: "request_failed", operationToken, failure });
      setFieldErrors(Object.fromEntries((failure.fieldErrors ?? []).map((item) => [item.field, item.message ?? item.code])));
    }
  };

  const createPlan = async () => {
    if (!state.blueprint || state.phase !== "review_ready") return;
    const operationToken = createIntentKey("plan-op");
    const idempotencyKey = createIntentKey("plan");
    dispatch({ type: "plan_requested", operationToken, idempotencyKey });
    try {
      const plan = await assemblyClient.createPlan(state.blueprint.blueprint_id, {
        blueprint_version: state.blueprint.version,
        environment: draft.environment,
      }, { idempotencyKey, timeoutMs: 45_000 });
      dispatch({ type: "plan_succeeded", operationToken, plan });
      setStep(4);
    } catch (reason) {
      dispatch({ type: "request_failed", operationToken, failure: assemblyRequestFailure(reason, "create_plan") });
    }
  };

  const selectOutputTarget = (value: string) => {
    setOutputTargetRef(value);
    if (value && state.phase === "plan_ready") dispatch({ type: "output_target_selected", outputTargetRef: value });
  };

  const applyRunResult = async (operationToken: string, run: Awaited<ReturnType<typeof assemblyClient.startAssembly>>) => {
    if (run.status === "failed" || run.status === "rolled_back") {
      dispatch({ type: "execution_failed", operationToken, run, failure: {
        code: "assembly.run_failed", message: "装配运行未完成，请查看装配记录。", retryable: true, retryIntent: "get_run",
      } });
      return;
    }
    if (run.status === "completed") {
      const manifestId = manifestIdFromURL(run.manifest_url);
      if (!manifestId) throw new TypeError("completed assembly run is missing a valid manifest URL");
      const manifest = await assemblyClient.getManifest(manifestId, { timeoutMs: 30_000 });
      if (manifest.run_id !== run.run_id) throw new TypeError("assembly manifest does not match the completed run");
      dispatch({ type: "execution_succeeded", operationToken, run, productId: manifest.product_id });
      return;
    }
    dispatch({ type: "run_observed", operationToken, run });
  };

  const startAssembly = async () => {
    if (executionSubmitting.current || !state.blueprint || !state.plan || state.phase !== "plan_ready" || !outputTargetRef) return;
    const confirmation = confirmationField(state.plan.document);
    const summaryChecksum = confirmation?.summary_checksum;
    if (typeof summaryChecksum !== "string") return;
    executionSubmitting.current = true;
    if (state.selectedOutputTargetRef !== outputTargetRef) dispatch({ type: "output_target_selected", outputTargetRef });
    const operationToken = createIntentKey("execute-op");
    const idempotencyKey = createIntentKey("assembly");
    dispatch({ type: "execution_requested", operationToken, idempotencyKey });
    try {
      const run = await assemblyClient.startAssembly(state.blueprint.blueprint_id, {
        plan_id: state.plan.plan_id,
        expected_plan_version: state.plan.version,
        plan_checksum: state.plan.checksum,
        confirmation: { accepted: true, summary_checksum: summaryChecksum },
        output_target_ref: outputTargetRef,
      }, { idempotencyKey, timeoutMs: 60_000 });
      await applyRunResult(operationToken, run);
    } catch (reason) {
      executionSubmitting.current = false;
      dispatch({ type: "request_failed", operationToken, failure: assemblyRequestFailure(reason, "start_assembly") });
    }
  };

  const retryFailedRequest = async () => {
    const intent = state.failure?.retryIntent;
    if (state.phase !== "failed" || !state.failure?.retryable || !intent) return;
    const operationToken = createIntentKey("retry-op");
    dispatch({ type: "retry_requested", operationToken });
    try {
      if (intent === "validate_blueprint" && state.validationIdempotencyKey) {
        const blueprint = await assemblyClient.createBlueprint(state.draft, {
          idempotencyKey: state.validationIdempotencyKey, timeoutMs: 30_000,
        });
        dispatch({ type: "validation_succeeded", operationToken, draftRevision: state.draftRevision, blueprint });
        setStep(3);
        return;
      }
      if (intent === "create_plan" && state.planIdempotencyKey && state.blueprint) {
        const plan = await assemblyClient.createPlan(state.blueprint.blueprint_id, {
          blueprint_version: state.blueprint.version, environment: draft.environment,
        }, { idempotencyKey: state.planIdempotencyKey, timeoutMs: 45_000 });
        dispatch({ type: "plan_succeeded", operationToken, plan });
        setStep(4);
        return;
      }
      if (intent === "start_assembly" && state.executionIdempotencyKey && state.blueprint && state.plan && outputTargetRef) {
        const confirmation = confirmationField(state.plan.document);
        if (typeof confirmation?.summary_checksum !== "string") throw new TypeError("plan confirmation is missing");
        const run = await assemblyClient.startAssembly(state.blueprint.blueprint_id, {
          plan_id: state.plan.plan_id,
          expected_plan_version: state.plan.version,
          plan_checksum: state.plan.checksum,
          confirmation: { accepted: true, summary_checksum: confirmation.summary_checksum },
          output_target_ref: outputTargetRef,
        }, { idempotencyKey: state.executionIdempotencyKey, timeoutMs: 60_000 });
        await applyRunResult(operationToken, run);
        return;
      }
      if (intent === "get_run" && state.run) {
        const run = await assemblyClient.getRun(state.run.run_id, { timeoutMs: 30_000 });
        await applyRunResult(operationToken, run);
        return;
      }
      throw new TypeError("retry intent is not available on this page");
    } catch (reason) {
      dispatch({ type: "request_failed", operationToken, failure: assemblyRequestFailure(reason, intent) });
    }
  };

  const returnToDraft = () => {
    if (state.phase !== "failed" || state.failedFrom === "executing") return;
    dispatch({ type: "draft_changed", draft: state.draft });
    setStep(2);
  };

  const planDocument = state.plan?.document;
  const conflicts = arrayField(planDocument, "conflicts");
  const risks = arrayField(planDocument, "risks");
  const dependencies = arrayField(planDocument, "dependencies");
  const outputs = arrayField(planDocument, "expected_outputs");
  const confirmation = confirmationField(planDocument);
  const blockingConflictCount = typeof confirmation?.blocking_conflict_count === "number" ? confirmation.blocking_conflict_count : conflicts.length;
  const confirmationReady = typeof confirmation?.summary_checksum === "string";
  const busy = state.phase === "validating" || state.phase === "creating_plan" || state.phase === "executing";

  return <Shell
    title={catalogScope === "experimental" ? "实验性创建软件" : "创建软件"}
    subtitle={catalogScope === "experimental" ? "仅使用服务端授权的 verified 候选" : "从 available 能力包装配可运行软件"}
  >
    <ol className="create-steps" aria-label="创建步骤">
      {steps.map((label, index) => <li key={label} className={index === step ? "active" : index < step ? "complete" : ""} aria-current={index === step ? "step" : undefined}>
        <span>{index < step ? <IconCheck size={15} /> : index + 1}</span><strong>{label}</strong>
      </li>)}
    </ol>

    {catalogError && step !== 1 && <div className="create-global-error" role="alert"><IconShieldLock size={19} /><span>{catalogError}</span></div>}

    <div className="create-layout">
      <form className="panel create-main" onSubmit={validateBlueprint}>
        {step === 0 && <section className="create-section">
          <header><span className="create-section-icon"><IconFileDescription size={20} /></span><div><h2>软件基本资料</h2><p>这些信息将进入不可变 Product Blueprint。</p></div></header>
          <div className="create-form-grid">
            <label>软件名称<input required value={draft.name} maxLength={128} onChange={(event) => updateDraft("name", event.target.value)} placeholder="例如：图片处理工作台" /></label>
            <label>软件代码<input aria-label="软件代码" required value={draft.code} pattern="[a-z][a-z0-9-]{2,63}" onChange={(event) => updateDraft("code", event.target.value.toLowerCase())} placeholder="image-studio" /><small>小写字母开头，可包含数字和连字符。</small></label>
            <label className="span-2">品牌名称（可选）<input value={draft.brandName} maxLength={128} onChange={(event) => updateDraft("brandName", event.target.value)} placeholder="用户可见的品牌名称" /></label>
          </div>
        </section>}

        {step === 1 && <section className="create-section">
          <header><span className="create-section-icon"><IconDeviceDesktop size={20} /></span><div><h2>目标端与能力组合</h2><p>目录由服务端按目标、交付形态和环境筛选。</p></div></header>
          <div className="create-form-grid compact-fields">
            <label>目标端<select value={draft.target} disabled={catalogLoading} onChange={(event) => updateDraft("target", event.target.value as AssemblyTarget)}>{targets.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
            <label>交付形态<select value={draft.deliveryMode} disabled={catalogLoading} onChange={(event) => updateDraft("deliveryMode", event.target.value as AssemblyDeliveryMode)}>{deliveryModes.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
            <label>环境<select value={draft.environment} disabled={catalogLoading} onChange={(event) => updateDraft("environment", event.target.value as AssemblyEnvironment)}>{environments.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
            <button className="secondary-button create-refresh" type="button" disabled={catalogLoading} onClick={() => setReloadVersion((value) => value + 1)}><IconRefresh size={17} />刷新目录</button>
          </div>
          {catalogLoading && <div className="create-query" role="status"><IconLoader2 className="spin" size={20} />正在读取服务端创建目录...</div>}
          {!catalogLoading && catalogError && <div className="create-query error" role="alert"><IconShieldLock size={20} /><div><strong>目录不可用</strong><p>{catalogError}</p></div><button className="secondary-button" type="button" onClick={() => setReloadVersion((value) => value + 1)}>重试</button></div>}
          {!catalogLoading && !catalogError && catalog && !catalogReady && <div className="create-empty" role="status"><IconBox size={34} /><strong>当前没有可创建的软件组合</strong><p>{catalogScope === "ordinary" ? "普通目录中还没有达到 available 的完整能力包、兼容模板和受信工具。" : "实验目录中没有满足当前目标与环境的 verified 候选。"}</p><span>创建已安全关闭，不会使用演示数据或空白能力包。</span></div>}
          {!catalogLoading && catalogReady && <fieldset className="package-list"><legend>完整能力包</legend>{catalog!.packages.map((item) => {
            const key = refKey(item.package_id, item.version);
            return <label key={key} className={draft.packageRefs.includes(key) ? "selected" : ""}><input type="checkbox" checked={draft.packageRefs.includes(key)} onChange={(event) => updateDraft("packageRefs", event.target.checked ? [...draft.packageRefs, key].sort() : draft.packageRefs.filter((entry) => entry !== key))} /><span><strong>{item.name}</strong><small>{item.package_id} · {item.version}</small><p>{item.user_value}</p></span></label>;
          })}</fieldset>}
        </section>}

        {step === 2 && <section className="create-section">
          <header><span className="create-section-icon"><IconServer size={20} /></span><div><h2>界面、工具与配置</h2><p>只能选择服务端目录公开的精确版本，不接受执行路径或摘要。</p></div></header>
          <div className="create-form-grid">
            <label>用户前台模板<select required value={draft.templateRef} onChange={(event) => updateDraft("templateRef", event.target.value)}><option value="">请选择兼容模板</option>{compatibleTemplates.map((item) => <option key={refKey(item.template_id, item.version)} value={refKey(item.template_id, item.version)}>{item.name} · {item.version}</option>)}</select></label>
            <label>Generator<select required value={draft.generatorRef} onChange={(event) => updateDraft("generatorRef", event.target.value)}><option value="">请选择生成器</option>{catalog?.generators.map((item) => <option key={refKey(item.id, item.version)} value={refKey(item.id, item.version)}>{item.name} · {item.version}</option>)}</select></label>
            <label>SDK<select required value={draft.sdkRef} onChange={(event) => updateDraft("sdkRef", event.target.value)}><option value="">请选择 SDK</option>{catalog?.sdks.map((item) => <option key={refKey(item.id, item.version)} value={refKey(item.id, item.version)}>{item.name} · {item.version}</option>)}</select></label>
            <label>Application ID<input required value={draft.applicationId} onChange={(event) => updateDraft("applicationId", event.target.value)} /></label>
            <label>渠道<input required value={draft.channel} onChange={(event) => updateDraft("channel", event.target.value)} /></label>
            <label>项目内输出目录<input required value={draft.outputPath} onChange={(event) => updateDraft("outputPath", event.target.value)} /><small>仅允许项目内相对路径，不接受磁盘或宿主路径。</small></label>
            <label>生成根目录<input required value={draft.outputRoot} onChange={(event) => updateDraft("outputRoot", event.target.value)} /></label>
          </div>
          <details className="provider-config"><summary>Provider 配置（可选）</summary><div className="create-form-grid">
            <label>Provider ID<input value={draft.provider} onChange={(event) => updateDraft("provider", event.target.value)} placeholder="例如：mail.primary" /></label>
            <label>配置引用<input value={draft.providerConfigRef} onChange={(event) => updateDraft("providerConfigRef", event.target.value)} placeholder="configs/mail.json" /></label>
            <label className="span-2">Secret Key（逗号分隔）<input value={draft.providerSecretKeys} onChange={(event) => updateDraft("providerSecretKeys", event.target.value.toUpperCase())} placeholder="MAIL_API_KEY" /><small>只提交密钥引用名，不提交秘密值。</small></label>
          </div></details>
          {Object.keys(fieldErrors).length > 0 && <div className="create-inline-error" role="alert"><strong>蓝图校验未通过</strong>{Object.entries(fieldErrors).map(([field, message]) => <p key={field}>{field}：{message}</p>)}</div>}
        </section>}

        {step === 3 && <section className="create-section">
          <header><span className="create-section-icon"><IconFileDescription size={20} /></span><div><h2>蓝图已保存</h2><p>下一步由服务端重新解析目录、依赖、冲突和预期输出。</p></div></header>
          <dl className="review-list"><div><dt>Blueprint</dt><dd>{state.blueprint?.blueprint_id}</dd></div><div><dt>版本</dt><dd>{state.blueprint?.version}</dd></div><div><dt>目录范围</dt><dd>{catalog?.catalog_scope}</dd></div><div><dt>能力包</dt><dd>{draft.packageRefs.length} 个</dd></div></dl>
          <button className="primary-button" type="button" disabled={state.phase !== "review_ready"} onClick={() => void createPlan()}>{state.phase === "creating_plan" ? "正在解析计划..." : "解析装配计划"}<IconArrowRight size={17} /></button>
        </section>}

        {step === 4 && <section className="create-section">
          <header><span className="create-section-icon"><IconCheck size={20} /></span><div><h2>装配计划审阅</h2><p>确认前核对依赖、风险、冲突和输出目标。</p></div></header>
          <div className="plan-metrics"><span><strong>{dependencies.length}</strong>依赖</span><span><strong>{risks.length}</strong>风险</span><span className={blockingConflictCount ? "danger" : ""}><strong>{blockingConflictCount}</strong>阻断冲突</span><span><strong>{outputs.length}</strong>预期输出</span></div>
          {blockingConflictCount > 0 && <div className="create-inline-error" role="alert"><IconAlertTriangle size={19} /><div><strong>计划存在阻断冲突</strong><p>请返回修改蓝图，当前不能执行。</p></div></div>}
          {!confirmationReady && <div className="create-inline-error" role="alert"><IconAlertTriangle size={19} /><div><strong>计划缺少确认摘要</strong><p>服务端没有返回可验证的确认摘要，当前不能执行。</p></div></div>}
          <fieldset className="output-targets"><legend>服务端授权输出目标</legend>{state.outputTargets?.items.length ? state.outputTargets.items.map((item) => <label key={item.output_target_ref} className={outputTargetRef === item.output_target_ref ? "selected" : ""}><input type="radio" name="output-target" checked={outputTargetRef === item.output_target_ref} onChange={() => selectOutputTarget(item.output_target_ref)} /><span><strong>{item.display_name}</strong><small>{item.summary}</small></span>{item.is_default && <b>服务端默认</b>}</label>) : <p className="muted-copy">当前环境没有授权输出目标，无法执行装配。</p>}</fieldset>
          <button className="primary-button" type="button" disabled={!state.plan?.executable || blockingConflictCount > 0 || !confirmationReady || !outputTargetRef || state.phase !== "plan_ready"} onClick={() => void startAssembly()}>{state.phase === "executing" ? "正在启动装配..." : "确认并开始装配"}</button>
        </section>}

        {state.phase === "failed" && <div className="create-request-error" role="alert"><strong>{state.failure?.message ?? "请求失败"}</strong>{state.failure?.detail && <p>{state.failure.detail}</p>}{state.failure?.requestId && <small>请求编号：{state.failure.requestId}</small>}<div className="create-error-actions">{state.failure?.retryable && state.failure.retryIntent && <button className="secondary-button" type="button" onClick={() => void retryFailedRequest()}><IconRefresh size={16} />重试请求</button>}{state.failedFrom !== "executing" && <button className="secondary-button" type="button" onClick={returnToDraft}>返回修改</button>}</div></div>}
        <footer className="create-actions">
          <button className="secondary-button" type="button" disabled={step === 0 || busy} onClick={() => setStep((value) => Math.max(0, value - 1))}><IconArrowLeft size={17} />上一步</button>
          {step < 2 && <button className="primary-button" type="button" disabled={busy || (step === 0 ? !basicValid : !targetValid)} onClick={() => setStep((value) => value + 1)}>下一步<IconArrowRight size={17} /></button>}
          {step === 2 && <button className="primary-button" type="submit" disabled={busy || !configurationValid || !targetValid}>{state.phase === "validating" ? "正在保存蓝图..." : "保存蓝图并继续"}<IconArrowRight size={17} /></button>}
        </footer>
      </form>

      <aside className="panel create-summary" aria-label="创建摘要">
        <h2>创建摘要</h2>
        <dl><div><dt>软件</dt><dd>{draft.name || "尚未填写"}</dd></div><div><dt>目标</dt><dd>{targets.find((item) => item.value === draft.target)?.label}</dd></div><div><dt>环境</dt><dd>{environments.find((item) => item.value === draft.environment)?.label}</dd></div><div><dt>能力包</dt><dd>{draft.packageRefs.length} 个</dd></div><div><dt>模板</dt><dd>{draft.templateRef ? splitRef(draft.templateRef).id : "尚未选择"}</dd></div><div><dt>目录</dt><dd>{catalogScope === "ordinary" ? "ordinary / available" : "experimental / verified"}</dd></div></dl>
        <p><IconShieldLock size={17} />浏览器只能提交目录 ID、版本和服务端授权引用，不接触宿主路径。</p>
      </aside>
    </div>
  </Shell>;
}
