import { IconAlertTriangle, IconArrowLeft, IconLoader2, IconRefresh, IconTool, IconVersions } from "@tabler/icons-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { assemblyClient, type AssemblyRunRecord, type LifecycleVersionRef } from "../api/assemblyClient";
import { useAuth } from "../app/AuthContext";
import { Modal } from "../components/Modal";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";
import { clearLifecycleIntent, lifecycleErrorMessage, lifecycleHasIdempotencyConflict, lifecycleIntent, lifecycleRequiresReauthentication } from "../features/assembly/lifecycleIntent";

type Mode = "upgrade" | "eject";
type ConflictedIntent = { kind: string; id: string; action: "plan" | "cancel"; mode?: Mode };
const artifactId = (value: string | null | undefined, resource: string) => value?.match(new RegExp(`^/api/v1/admin/${resource}/([A-Za-z0-9][A-Za-z0-9._:-]{0,127})$`))?.[1] ?? null;
function versionRefs(value: string, required = false): LifecycleVersionRef[] {
  const items = value.split(",").map((item) => item.trim()).filter(Boolean).map((item) => {
    const index = item.lastIndexOf("@");
    if (index < 1) throw new TypeError(`“${item}”应使用 id@版本 格式`);
    return { id: item.slice(0, index), version: item.slice(index + 1) };
  });
  if (required && items.length === 0) throw new TypeError("至少填写一项");
  return items;
}
function singleVersionRef(value: string) { const refs = versionRefs(value, true); if (refs.length !== 1) throw new TypeError("只能填写一个 id@版本"); return refs[0]; }

export function AssemblyLifecycleEntryPage() {
  const { runId } = useParams();
  const navigate = useNavigate();
  const { session, logout } = useAuth();
  const permissions = new Set(session?.authorization.permissions ?? []);
  const canRead = permissions.has("assembly.read");
  const canPlan = permissions.has("assembly.lifecycle.plan");
  const canExecute = permissions.has("assembly.lifecycle.execute");
  const [run, setRun] = useState<AssemblyRunRecord | null>(null);
  const [checksums, setChecksums] = useState<{ assemblyId: string; manifest: string; lock: string } | null>(null);
  const [mode, setMode] = useState<Mode>("upgrade");
  const [packages, setPackages] = useState("");
  const [templates, setTemplates] = useState("");
  const [generator, setGenerator] = useState("");
  const [sdks, setSdks] = useState("");
  const [paths, setPaths] = useState("");
  const [reason, setReason] = useState("");
  const [cancelOpen, setCancelOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [reauthenticationRequired, setReauthenticationRequired] = useState(false);
  const [conflictedIntent, setConflictedIntent] = useState<ConflictedIntent | null>(null);
  const [preparingLogin, setPreparingLogin] = useState(false);
  const activeRequest = useRef<AbortController | null>(null);
  const sequence = useRef(0);

  const load = useCallback(async () => {
    if (!runId) return;
    if (!canRead) { setError("缺少 assembly.read 权限，无法读取装配生命周期"); setLoading(false); return; }
    const controller = new AbortController();
    activeRequest.current?.abort(); activeRequest.current = controller;
    const current = ++sequence.current;
    setLoading(true); setError(null);
    try {
      const next = await assemblyClient.getRun(runId, { signal: controller.signal, timeoutMs: 20_000 });
      let nextChecksums: { assemblyId: string; manifest: string; lock: string } | null = null;
      if (next.status === "completed") {
        const assemblyId = artifactId(next.manifest_url, "assembly-manifests");
        const lockId = artifactId(next.lock_url, "generated-project-locks");
        if (!assemblyId || !lockId) throw new TypeError("已完成运行缺少可信的装配产物引用");
        const source = await assemblyClient.getLifecycleSource(assemblyId, { signal: controller.signal, timeoutMs: 20_000 });
        nextChecksums = { assemblyId, manifest: source.manifest_checksum, lock: source.lock_checksum };
      }
      if (!controller.signal.aborted && current === sequence.current) { setRun(next); setChecksums(nextChecksums); }
    } catch (reason) { if (!controller.signal.aborted && current === sequence.current) setError(lifecycleErrorMessage(reason, "生命周期入口加载失败")); }
    finally { if (!controller.signal.aborted && current === sequence.current) setLoading(false); }
  }, [canRead, runId]);
  useEffect(() => { void load(); return () => { activeRequest.current?.abort(); sequence.current += 1; }; }, [load]);

  const canCreate = useMemo(() => run?.status === "completed" && checksums !== null && canPlan, [run, checksums, canPlan]);
  const createPlan = async (requestedMode: Mode = mode) => {
    if (!run || !checksums || submitting || !canPlan) return;
    setSubmitting(true); setError(null); setReauthenticationRequired(false); setConflictedIntent(null);
    const intentKind = `plan-${requestedMode}`;
    try {
      const options = { idempotencyKey: lifecycleIntent(intentKind, checksums.assemblyId), timeoutMs: 30_000 };
      const plan = requestedMode === "upgrade"
        ? await assemblyClient.createUpgradePlan(checksums.assemblyId, { expected_manifest_checksum: checksums.manifest, expected_lock_checksum: checksums.lock, target: { packages: versionRefs(packages), templates: versionRefs(templates, true), generator: singleVersionRef(generator), sdks: versionRefs(sdks) } }, options)
        : await assemblyClient.createEjectPlan(checksums.assemblyId, { expected_manifest_checksum: checksums.manifest, expected_lock_checksum: checksums.lock, paths: [...new Set(paths.split(/\r?\n/).map((item) => item.trim()).filter(Boolean))] }, options);
      clearLifecycleIntent(intentKind, checksums.assemblyId);
      navigate(`/assembly-lifecycle/plans/${encodeURIComponent(plan.lifecycle_plan_id)}`);
    } catch (reason) {
      if (lifecycleHasIdempotencyConflict(reason)) setConflictedIntent({ kind: intentKind, id: checksums.assemblyId, action: "plan", mode: requestedMode });
      setError(lifecycleErrorMessage(reason, "生命周期计划创建失败"));
    }
    finally { setSubmitting(false); }
  };
  const cancelRun = async () => {
    if (!run || submitting || !canExecute || run.status !== "planned") return;
    setSubmitting(true); setError(null); setReauthenticationRequired(false); setConflictedIntent(null);
    const intentKind = "run-cancel";
    try {
      const next = await assemblyClient.cancelRun(run.run_id, run.version, reason, { idempotencyKey: lifecycleIntent(intentKind, run.run_id), timeoutMs: 30_000 });
      clearLifecycleIntent(intentKind, run.run_id); setRun(next); setCancelOpen(false); setReason("");
    } catch (failure) {
      const needsReauthentication = lifecycleRequiresReauthentication(failure);
      setReauthenticationRequired(needsReauthentication);
      if (lifecycleHasIdempotencyConflict(failure)) setConflictedIntent({ kind: intentKind, id: run.run_id, action: "cancel" });
      if (needsReauthentication || lifecycleHasIdempotencyConflict(failure)) setCancelOpen(false);
      setError(lifecycleErrorMessage(failure, "取消运行失败"));
    }
    finally { setSubmitting(false); }
  };

  const beginReauthentication = async () => {
    if (!runId || preparingLogin) return;
    setPreparingLogin(true);
    try {
      await logout();
      navigate("/login", { replace: true, state: { from: `/assemblies/${encodeURIComponent(runId)}/lifecycle` } });
    } catch (failure) {
      setError(lifecycleErrorMessage(failure, "暂时无法进入重新登录，请稍后重试"));
      setReauthenticationRequired(true);
      setPreparingLogin(false);
    }
  };

  const restartConflictedIntent = () => {
    if (!conflictedIntent || submitting) return;
    const pending = conflictedIntent;
    clearLifecycleIntent(pending.kind, pending.id);
    setConflictedIntent(null);
    setError(null);
    if (pending.action === "plan") void createPlan(pending.mode);
    else setCancelOpen(true);
  };

  return <Shell title="装配生命周期" subtitle={runId ? `运行 ${runId}` : "升级、退出托管与恢复"}>
    <div className="toolbar"><button className="secondary-button" type="button" onClick={() => navigate(`/assemblies/${encodeURIComponent(runId ?? "")}`)}><IconArrowLeft size={17} />返回运行</button><div className="assembly-toolbar-spacer" /><button className="secondary-button" type="button" disabled={loading || !canRead} onClick={() => void load()}><IconRefresh className={loading ? "spin" : ""} size={17} />刷新</button></div>
    {(error || reauthenticationRequired || conflictedIntent) && <div className="create-global-error" role="alert"><IconAlertTriangle size={18} /><span>{error ?? (reauthenticationRequired ? "此操作需要近期重新认证。" : "旧操作意图需要由管理员明确处理。")}</span>{reauthenticationRequired && <button className="secondary-button" type="button" disabled={preparingLogin} onClick={() => void beginReauthentication()}>{preparingLogin ? "正在退出当前会话..." : "重新登录并返回此页"}</button>}{conflictedIntent && <button className="secondary-button" type="button" onClick={restartConflictedIntent}>放弃旧意图并重新发起</button>}</div>}
    {loading && !run && <div className="query-state" role="status"><IconLoader2 className="spin" size={18} />正在读取生命周期上下文...</div>}
    {run && <div className="lifecycle-layout">
      <section className="panel lifecycle-summary"><header className="panel-title"><div><h2>运行上下文</h2><p>仅使用服务端返回的可信版本与校验和</p></div><StatusBadge status={run.status} /></header><dl className="detail-list"><div><dt>Assembly</dt><dd><code>{checksums?.assemblyId ?? "尚未建立"}</code></dd></div><div><dt>Product</dt><dd><code>{run.product_id ?? "尚未建立"}</code></dd></div><div><dt>运行版本</dt><dd>v{run.version}</dd></div></dl>
        {run.status === "planned" && <div className="lifecycle-actions"><button className="secondary-button danger-button" type="button" disabled={!canExecute} title={canExecute ? "取消尚未领取的运行" : "缺少 assembly.lifecycle.execute 权限"} onClick={() => { setReason(""); setCancelOpen(true); }}>取消计划运行</button></div>}
      </section>
      {run.status === "completed" ? <section className="panel lifecycle-form"><header className="panel-title"><div><h2>创建生命周期计划</h2><p>先生成只读差异与冲突预览，确认后才会执行</p></div></header>
        <div className="lifecycle-segments" role="tablist" aria-label="生命周期类型"><button type="button" className={mode === "upgrade" ? "active" : ""} onClick={() => setMode("upgrade")}><IconVersions size={17} />升级</button><button type="button" className={mode === "eject" ? "active" : ""} onClick={() => setMode("eject")}><IconTool size={17} />退出托管</button></div>
        {mode === "upgrade" ? <div className="lifecycle-fields"><label>能力包版本<input value={packages} onChange={(event) => setPackages(event.target.value)} placeholder="package.account@1.2.0, package.entitlement@1.0.0" /></label><label>模板版本（必填）<input value={templates} onChange={(event) => setTemplates(event.target.value)} placeholder="admin.web@1.1.0" /></label><label>生成器版本（必填）<input value={generator} onChange={(event) => setGenerator(event.target.value)} placeholder="generator.web@1.1.0" /></label><label>SDK 版本<input value={sdks} onChange={(event) => setSdks(event.target.value)} placeholder="sdk.typescript@1.1.0" /></label></div> : <div className="lifecycle-fields"><label>退出托管路径（每行一项）<textarea value={paths} onChange={(event) => setPaths(event.target.value)} placeholder={"apps/admin/src/features/account\napps/client/src/account"} /></label><p className="lifecycle-warning"><IconAlertTriangle size={17} />退出托管会把选定 generated/integration 区域转为 forked，后续升级不再覆盖这些区域。</p></div>}
        <div className="lifecycle-actions"><button className="primary-button" type="button" disabled={!canCreate || submitting} title={canPlan ? "生成差异计划" : "缺少 assembly.lifecycle.plan 权限"} onClick={() => void createPlan()}>{submitting ? <IconLoader2 className="spin" size={17} /> : null}生成计划</button></div>
      </section> : run.status !== "planned" && <div className="assembly-readonly-notice">只有成功完成并产生可信 Manifest 与 Lock 的装配运行可以创建升级或退出托管计划。</div>}
    </div>}
    <Modal title="取消计划运行" open={cancelOpen} onClose={() => { if (!submitting) { setCancelOpen(false); setReason(""); } }}><div className="modal-body lifecycle-modal"><p>此操作只允许取消尚未被执行器领取的运行，并会留下审计记录。</p><label>取消原因<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} /></label></div><div className="modal-actions"><button className="secondary-button" type="button" disabled={submitting} onClick={() => { setCancelOpen(false); setReason(""); }}>返回</button><button className="primary-button danger-primary" type="button" disabled={!canExecute || !reason.trim() || submitting} onClick={() => void cancelRun()}>确认取消</button></div></Modal>
  </Shell>;
}
