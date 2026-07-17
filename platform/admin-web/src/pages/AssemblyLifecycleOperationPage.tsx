import { IconAlertTriangle, IconArrowLeft, IconFileAnalytics, IconLoader2, IconRefresh, IconRotateClockwise, IconShieldCheck } from "@tabler/icons-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { assemblyClient, type AssemblyLifecycleOperation } from "../api/assemblyClient";
import { useAuth } from "../app/AuthContext";
import { Modal } from "../components/Modal";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";
import { clearLifecycleIntent, lifecycleErrorMessage, lifecycleIntent } from "../features/assembly/lifecycleIntent";

const terminal = new Set(["completed", "failed", "cancelled", "rolled_back", "rollback_failed"]);
const kindLabels = { upgrade: "升级", eject: "退出托管", rollback: "回滚" };
const time = (value: string | null) => value ? new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "medium" }).format(new Date(value)) : "--";
type Action = "cancel" | "rollback";
type ArtifactKind = "manifest" | "lock";
type ArtifactReadState = { status: "idle" | "loading" | "verified" | "failed"; summary?: string };

const artifactResource = { manifest: "assembly-manifests", lock: "generated-project-locks" } as const;

function lifecycleArtifactId(url: string | undefined, kind: ArtifactKind) {
  if (!url) throw new TypeError("生命周期工件地址缺失");
  const match = new RegExp(`^/api/v1/admin/${artifactResource[kind]}/([A-Za-z0-9][A-Za-z0-9._:-]{0,127})$`).exec(url);
  if (!match) throw new TypeError("生命周期工件地址不可信");
  return match[1];
}

export function AssemblyLifecycleOperationPage() {
  const { operationId } = useParams();
  const navigate = useNavigate();
  const { session } = useAuth();
  const permissions = new Set(session?.authorization.permissions ?? []);
  const canRead = permissions.has("assembly.read");
  const canExecute = permissions.has("assembly.lifecycle.execute");
  const [operation, setOperation] = useState<AssemblyLifecycleOperation | null>(null);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [action, setAction] = useState<Action | null>(null);
  const [reason, setReason] = useState("");
  const [accepted, setAccepted] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [artifacts, setArtifacts] = useState<Record<ArtifactKind, ArtifactReadState>>({ manifest: { status: "idle" }, lock: { status: "idle" } });
  const activeRequest = useRef<AbortController | null>(null);
  const sequence = useRef(0);

  const load = useCallback(async () => {
    if (!operationId) return;
    if (!canRead) { setError("缺少 assembly.read 权限，无法读取生命周期操作"); setLoading(false); return; }
    const controller = new AbortController();
    activeRequest.current?.abort(); activeRequest.current = controller;
    const current = ++sequence.current;
    setError(null);
    try { const next = await assemblyClient.getLifecycleOperation(operationId, { signal: controller.signal, timeoutMs: 20_000 }); if (!controller.signal.aborted && current === sequence.current) setOperation(next); }
    catch (reason) { if (!controller.signal.aborted && current === sequence.current) setError(lifecycleErrorMessage(reason, "生命周期操作加载失败")); }
    finally { if (!controller.signal.aborted && current === sequence.current) setLoading(false); }
  }, [canRead, operationId]);
  useEffect(() => { void load(); return () => { activeRequest.current?.abort(); sequence.current += 1; }; }, [load]);
  useEffect(() => { setArtifacts({ manifest: { status: "idle" }, lock: { status: "idle" } }); }, [operationId]);
  useEffect(() => { if (!operation || terminal.has(operation.status)) return; const timer = window.setTimeout(() => void load(), 2_000); return () => window.clearTimeout(timer); }, [load, operation]);

  const readArtifact = async (kind: ArtifactKind) => {
    if (!operation?.target || artifacts[kind].status === "loading") return;
    setArtifacts((current) => ({ ...current, [kind]: { status: "loading" } }));
    try {
      const expectedId = kind === "manifest" ? operation.target.manifest_id : operation.target.lock_id;
      const id = lifecycleArtifactId(kind === "manifest" ? operation.manifest_url : operation.lock_url, kind);
      if (id !== expectedId) throw new TypeError("生命周期工件 ID 与操作目标不一致");
      if (kind === "manifest") {
        const manifest = await assemblyClient.getManifest(id, { timeoutMs: 20_000 });
        if (manifest.product_id !== operation.product_id || manifest.lifecycle_operation_id !== operation.operation_id || manifest.checksum !== operation.target.manifest_checksum) throw new TypeError("Manifest 归属或校验和不一致");
        setArtifacts((current) => ({ ...current, manifest: { status: "verified", summary: `Manifest ${manifest.assembly_id} 已验证` } }));
      } else {
        const lock = await assemblyClient.getGeneratedProjectLock(id, { timeoutMs: 20_000 });
        if (lock.product_id !== operation.product_id || lock.lifecycle_operation_id !== operation.operation_id || lock.assembly_id !== operation.target.manifest_id || lock.checksum !== operation.target.lock_checksum) throw new TypeError("Generated Lock 归属或校验和不一致");
        setArtifacts((current) => ({ ...current, lock: { status: "verified", summary: `Generated Lock ${lock.lock_id} 已验证` } }));
      }
    } catch {
      setArtifacts((current) => ({ ...current, [kind]: { status: "failed", summary: kind === "manifest" ? "Manifest 验证失败" : "Generated Lock 验证失败" } }));
    }
  };

  const submitAction = async () => {
    if (!operation || !action || submitting || !canExecute) return;
    if (action === "cancel" && (operation.status !== "planned" || !operation.recovery.cancel_allowed)) return;
    if (action === "rollback" && (!accepted || !operation.recovery.rollback_available)) return;
    setSubmitting(true); setError(null);
    try {
      const sourceOperationId = operation.operation_id;
      const next = action === "cancel"
        ? await assemblyClient.cancelLifecycleOperation(sourceOperationId, operation.version, reason, { idempotencyKey: lifecycleIntent("operation-cancel", sourceOperationId), timeoutMs: 30_000 })
        : await assemblyClient.rollbackLifecycleOperation(sourceOperationId, operation.version, reason, { idempotencyKey: lifecycleIntent("operation-rollback", sourceOperationId), timeoutMs: 30_000 });
      clearLifecycleIntent(action === "cancel" ? "operation-cancel" : "operation-rollback", sourceOperationId);
      activeRequest.current?.abort(); sequence.current += 1;
      setOperation(next); setAction(null); setReason(""); setAccepted(false);
      if (action === "rollback" && next.operation_id !== sourceOperationId) navigate(`/assembly-lifecycle/operations/${encodeURIComponent(next.operation_id)}`, { replace: true });
    } catch (reason) { setError(lifecycleErrorMessage(reason, `${action === "cancel" ? "取消" : "回滚"}操作失败`)); }
    finally { setSubmitting(false); }
  };

  return <Shell title="生命周期操作" subtitle={operationId ?? "持久化执行与恢复"}>
    <div className="toolbar"><button className="secondary-button" type="button" onClick={() => operation?.lifecycle_plan_id ? navigate(`/assembly-lifecycle/plans/${encodeURIComponent(operation.lifecycle_plan_id)}`) : navigate(-1)}><IconArrowLeft size={17} />返回计划</button><div className="assembly-toolbar-spacer" /><button className="secondary-button" type="button" disabled={loading || !canRead} onClick={() => void load()}><IconRefresh className={loading ? "spin" : ""} size={17} />刷新</button>{operation?.recovery.cancel_allowed && operation.status === "planned" && <button className="secondary-button danger-button" type="button" disabled={!canExecute} title={canExecute ? "取消尚未领取的操作" : "缺少 assembly.lifecycle.execute 权限"} onClick={() => { setReason(""); setAccepted(false); setAction("cancel"); }}>取消操作</button>}{operation?.recovery.rollback_available && <button className="primary-button" type="button" disabled={!canExecute || operation.status === "rolling_back"} title={canExecute ? "创建回滚操作" : "缺少 assembly.lifecycle.execute 权限"} onClick={() => { setReason(""); setAccepted(false); setAction("rollback"); }}><IconRotateClockwise size={17} />回滚</button>}</div>
    {error && <div className="create-global-error" role="alert"><IconAlertTriangle size={18} /><span>{error}</span><button className="secondary-button" type="button" onClick={() => void load()}>重新读取</button></div>}
    {loading && !operation && <div className="query-state" role="status"><IconLoader2 className="spin" size={18} />正在读取持久化操作...</div>}
    {operation && <div className="lifecycle-plan-layout">
      <section className="panel lifecycle-summary"><header className="panel-title"><div><h2>{kindLabels[operation.kind]}操作</h2><p>操作 v{operation.version} · 浏览器断开不会取消执行</p></div><StatusBadge status={operation.status} failedLabel="执行失败" /></header><dl className="detail-list"><div><dt>Operation</dt><dd><code>{operation.operation_id}</code></dd></div><div><dt>Root Operation</dt><dd><code>{operation.root_operation_id}</code></dd></div><div><dt>当前步骤</dt><dd>{operation.current_step ?? "等待调度"}</dd></div><div><dt>Assembly</dt><dd><code>{operation.assembly_id}</code></dd></div><div><dt>更新时间</dt><dd>{time(operation.updated_at)}</dd></div><div><dt>完成时间</dt><dd>{time(operation.completed_at)}</dd></div></dl>{(operation.manifest_url || operation.lock_url) && <div className="artifact-actions">{operation.manifest_url && <button className="secondary-button" type="button" disabled={artifacts.manifest.status === "loading"} onClick={() => void readArtifact("manifest")}><IconShieldCheck size={16} />{artifacts.manifest.status === "loading" ? "正在验证 Manifest" : "验证 Manifest"}</button>}{operation.lock_url && <button className="secondary-button" type="button" disabled={artifacts.lock.status === "loading"} onClick={() => void readArtifact("lock")}><IconShieldCheck size={16} />{artifacts.lock.status === "loading" ? "正在验证 Generated Lock" : "验证 Generated Lock"}</button>}{artifacts.manifest.summary && <span role="status">{artifacts.manifest.summary}</span>}{artifacts.lock.summary && <span role="status">{artifacts.lock.summary}</span>}</div>}</section>
      <section className="panel lifecycle-recovery"><header className="panel-title"><div><h2>恢复能力</h2><p>动作是否可用完全以服务端 recovery 投影为准</p></div></header><div className="lifecycle-recovery-grid"><div><strong>{operation.recovery.retryable ? "可恢复" : "不可重试"}</strong><span>失败恢复</span></div><div><strong>{operation.recovery.rollback_available ? "可回滚" : "不可回滚"}</strong><span>回滚能力</span></div><div><strong>{operation.recovery.cancel_allowed ? "可取消" : "不可取消"}</strong><span>计划取消</span></div></div>{operation.status === "failed" && operation.recovery.retryable && <p className="lifecycle-recovery-note">操作失败且允许恢复。请先查看诊断并重新读取最新状态；当前契约未提供浏览器端“原地重试”端点，因此不会伪造重试动作。</p>}{operation.status === "rollback_failed" && <p className="lifecycle-recovery-note danger-copy">回滚失败，当前状态保持可追踪。请依据诊断处理后再执行服务端允许的恢复动作。</p>}</section>
      <section className="panel assembly-projections"><header className="panel-title"><div><h2>诊断</h2><p>仅展示服务端安全诊断投影</p></div></header>{operation.diagnostics.length === 0 ? <div className="empty-state compact-empty">暂无诊断</div> : <ul>{operation.diagnostics.map((item) => <li key={item.diagnostic_id}><IconAlertTriangle size={18} /><div><strong>{item.code}</strong><p>{item.message}</p>{item.remediation.map((line) => <small key={line}>{line}</small>)}</div></li>)}</ul>}</section>
      <section className="panel assembly-projections"><header className="panel-title"><div><h2>验证报告</h2><p>报告摘要与完整性状态</p></div></header>{operation.reports.length === 0 ? <div className="empty-state compact-empty">暂无报告</div> : <ul>{operation.reports.map((item) => <li key={item.report_id}><IconFileAnalytics size={18} /><div><strong>{item.type} · {item.status}</strong><p>{item.summary}</p><small>{time(item.created_at)}</small></div></li>)}</ul>}</section>
    </div>}
    <Modal title={action === "rollback" ? "确认创建回滚操作" : "取消计划操作"} open={action !== null} onClose={() => { if (!submitting) { setAction(null); setReason(""); setAccepted(false); } }}><div className="modal-body lifecycle-modal"><p>{action === "rollback" ? "回滚会创建新的持久化操作，并以当前版本做并发保护。" : "只能取消尚未被执行器领取的计划操作。"}</p><label>操作原因<textarea value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} /></label>{action === "rollback" && <label className="lifecycle-checkbox"><input type="checkbox" checked={accepted} onChange={(event) => setAccepted(event.target.checked)} />我已查看诊断并确认执行回滚</label>}</div><div className="modal-actions"><button className="secondary-button" type="button" disabled={submitting} onClick={() => { setAction(null); setReason(""); setAccepted(false); }}>返回</button><button className="primary-button danger-primary" type="button" disabled={!canExecute || !reason.trim() || submitting || (action === "rollback" && !accepted)} onClick={() => void submitAction()}>{submitting ? "正在提交..." : action === "rollback" ? "确认回滚" : "确认取消"}</button></div></Modal>
  </Shell>;
}
