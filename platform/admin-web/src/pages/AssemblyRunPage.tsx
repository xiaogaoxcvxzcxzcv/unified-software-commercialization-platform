import { IconAlertTriangle, IconArrowLeft, IconCheck, IconExternalLink, IconFileAnalytics, IconLoader2, IconRefresh, IconVersions } from "@tabler/icons-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { assemblyClient, type AssemblyRunRecord } from "../api/assemblyClient";
import { AuthApiError } from "../api/authClient";
import { useAppContext } from "../app/AppContext";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";

const terminalStatuses = new Set(["completed", "failed", "cancelled", "rolled_back"]);
const time = (value: string | null) => value ? new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "medium" }).format(new Date(value)) : "--";
const retryKeyPattern = /^assembly-retry-[A-Za-z0-9-]{8,100}$/;
function persistedRetryKey(runId: string) {
  const storageKey = `assembly_retry_intent:${runId}`;
  try {
    const saved = sessionStorage.getItem(storageKey);
    if (saved && retryKeyPattern.test(saved)) return saved;
    const created = `assembly-retry-${crypto.randomUUID()}`;
    sessionStorage.setItem(storageKey, created);
    return created;
  } catch { return `assembly-retry-${crypto.randomUUID()}`; }
}
function clearRetryKey(runId: string) { try { sessionStorage.removeItem(`assembly_retry_intent:${runId}`); } catch { /* Memory-only fallback expires with the page. */ } }
function artifactId(value: string | null | undefined, resource: string) {
  if (!value) return null;
  const match = value.match(new RegExp(`^/api/v1/admin/${resource}/([A-Za-z0-9][A-Za-z0-9._:-]{0,127})$`));
  return match?.[1] ?? null;
}

function loadErrorMessage(reason: unknown) {
  if (reason instanceof AuthApiError && reason.status === 404) return "未找到该装配运行";
  return reason instanceof Error ? reason.message : "装配运行加载失败";
}

export function AssemblyRunPage() {
  const { runId } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const { openTrustedProduct } = useAppContext();
  const [run, setRun] = useState<AssemblyRunRecord | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [retrying, setRetrying] = useState(false);
  const [handoffError, setHandoffError] = useState<string | null>(null);
  const [handoffRevision, setHandoffRevision] = useState(0);
  const sequence = useRef(0);
  const activeRequest = useRef<AbortController | null>(null);
  const handoffDone = useRef(false);
  const retryIntent = useRef<string | null>(null);
  const handoff = new URLSearchParams(location.search).get("handoff") === "1";

  const load = useCallback(() => {
    if (!runId) return null;
    const controller = new AbortController();
    activeRequest.current?.abort();
    activeRequest.current = controller;
    const current = ++sequence.current;
    setError(null);
    void assemblyClient.getRun(runId, { signal: controller.signal, timeoutMs: 20_000 }).then((next) => {
      if (controller.signal.aborted || current !== sequence.current) return;
      setRun(next); setLoading(false);
    }).catch((reason: unknown) => {
      if (controller.signal.aborted || current !== sequence.current) return;
      setError(loadErrorMessage(reason)); setLoading(false);
    });
    return controller;
  }, [runId]);

  useEffect(() => { load(); return () => { activeRequest.current?.abort(); sequence.current += 1; }; }, [load]);
  useEffect(() => {
    if (!run || terminalStatuses.has(run.status)) return;
    const timer = window.setTimeout(() => load(), 2_000);
    return () => window.clearTimeout(timer);
  }, [run, load]);

  useEffect(() => {
    if (!handoff || handoffDone.current || run?.status !== "completed") return;
    const manifestId = artifactId(run.manifest_url, "assembly-manifests");
    if (!manifestId) { setHandoffError("已完成运行缺少可信 Manifest，已停止进入软件工作区。"); return; }
    handoffDone.current = true;
    const controller = new AbortController();
    void assemblyClient.getManifest(manifestId, { signal: controller.signal, timeoutMs: 20_000 }).then(async (manifest) => {
      if (controller.signal.aborted || manifest.run_id !== run.run_id || manifest.product_id !== run.product_id) throw new TypeError("Manifest 与当前运行不一致");
      await openTrustedProduct(manifest.product_id);
      if (!controller.signal.aborted) navigate(`/products/${encodeURIComponent(manifest.product_id)}/overview`, { replace: true });
    }).catch((reason: unknown) => { if (!controller.signal.aborted) { handoffDone.current = false; setHandoffError(reason instanceof Error ? reason.message : "软件工作区验证失败"); } });
    return () => controller.abort();
  }, [handoff, handoffRevision, navigate, openTrustedProduct, run]);

  const retry = async () => {
    if (!run || retrying || run.status !== "failed" || !run.recovery.retryable || run.recovery.rollback_required) return;
    setRetrying(true); setError(null);
    try {
      retryIntent.current ??= persistedRetryKey(run.run_id);
      const next = await assemblyClient.retryRun(run.run_id, run.version, { idempotencyKey: retryIntent.current, timeoutMs: 30_000 });
      clearRetryKey(run.run_id); retryIntent.current = null;
      navigate(`/assemblies/${encodeURIComponent(next.run_id)}?handoff=${handoff ? "1" : "0"}`);
    } catch (reason) {
      if (reason instanceof AuthApiError && reason.status === 409) setError("运行状态已变化，请刷新后再决定是否重试。");
      else setError(reason instanceof Error ? reason.message : "重试提交失败");
    } finally { setRetrying(false); }
  };

  return <Shell title="装配运行" subtitle={runId ? `运行 ${runId}` : "持久化运行详情"}>
    <div className="toolbar"><button className="secondary-button" type="button" onClick={() => navigate("/assemblies")}><IconArrowLeft size={17} />返回记录</button><div className="assembly-toolbar-spacer" />
      <button className="secondary-button" type="button" disabled={loading} onClick={() => load()}><IconRefresh className={loading ? "spin" : ""} size={17} />刷新</button>
      {run && <button className="secondary-button" type="button" onClick={() => navigate(`/assemblies/${encodeURIComponent(run.run_id)}/lifecycle`)}><IconVersions size={17} />生命周期</button>}
      {run?.status === "failed" && run.recovery.retryable && !run.recovery.rollback_required && <button className="primary-button" type="button" disabled={retrying} onClick={() => void retry()}><IconRefresh size={17} />{retrying ? "正在创建新尝试..." : "重试此运行"}</button>}
    </div>
    {error && <div className="query-state query-error" role="alert"><span>{error}</span><button className="secondary-button" type="button" onClick={() => load()}>重新读取</button></div>}
    {handoffError && <div className="create-global-error" role="alert"><IconAlertTriangle size={18} /><span>{handoffError}</span><button className="secondary-button" type="button" onClick={() => { handoffDone.current = false; setHandoffError(null); setHandoffRevision((value) => value + 1); load(); }}>重新验证工作区</button></div>}
    {loading && !run && <div className="query-state" role="status"><IconLoader2 className="spin" size={18} />正在读取持久化运行...</div>}
    {run && <RunDetail run={run} />}
  </Shell>;
}

function RunDetail({ run }: { run: AssemblyRunRecord }) {
  return <div className="assembly-detail-layout">
    <section className="panel assembly-overview"><header className="panel-title"><div><h2>运行概况</h2><p>第 {run.attempt_number} 次尝试，版本 {run.version}</p></div><StatusBadge status={run.status} /></header>
      <dl className="detail-list"><div><dt>Run ID</dt><dd><code>{run.run_id}</code></dd></div><div><dt>Root Run</dt><dd><code>{run.root_run_id}</code></dd></div><div><dt>Plan</dt><dd><code>{run.plan_id}</code> · v{run.plan_version}</dd></div><div><dt>软件</dt><dd>{run.product_id ?? "尚未建立"}</dd></div><div><dt>当前步骤</dt><dd>{run.current_step_id ?? "等待调度"}</dd></div><div><dt>更新时间</dt><dd>{time(run.updated_at)}</dd></div></dl>
      {(run.manifest_url || run.lock_url) && <div className="artifact-actions">{run.manifest_url && <a className="secondary-button" href={run.manifest_url} target="_blank" rel="noreferrer"><IconExternalLink size={16} />Manifest</a>}{run.lock_url && <a className="secondary-button" href={run.lock_url} target="_blank" rel="noreferrer"><IconExternalLink size={16} />Generated Lock</a>}</div>}
    </section>
    {run.status === "failed" && run.recovery.rollback_required && <div className="assembly-readonly-notice" role="status">该运行需要后续生命周期恢复，当前记录保持只读。</div>}
    <section className="panel assembly-timeline"><header className="panel-title"><div><h2>步骤时间线</h2><p>按服务端持久化状态展示</p></div></header>
      <ol>{run.steps.map((step) => <li key={step.step_id} className={`step-${step.status}`}><span>{step.status === "completed" ? <IconCheck size={16} /> : step.status === "running" ? <IconLoader2 className="spin" size={16} /> : <span />}</span><div><strong>{step.kind}</strong><small>{step.status} · 尝试 {step.attempt}</small><time>{time(step.started_at)} - {time(step.finished_at)}</time></div></li>)}</ol>
    </section>
    <section className="panel assembly-projections"><header className="panel-title"><div><h2>诊断</h2><p>仅展示服务端允许的安全投影</p></div></header>{run.diagnostics.length === 0 ? <div className="empty-state compact-empty">暂无诊断</div> : <ul>{run.diagnostics.map((item) => <li key={item.diagnostic_id}><IconAlertTriangle size={18} /><div><strong>{item.code}</strong><p>{item.message}</p>{item.remediation.map((line) => <small key={line}>{line}</small>)}</div></li>)}</ul>}</section>
    <section className="panel assembly-projections"><header className="panel-title"><div><h2>验证报告</h2><p>报告摘要与完整性校验</p></div></header>{run.reports.length === 0 ? <div className="empty-state compact-empty">暂无报告</div> : <ul>{run.reports.map((item) => <li key={item.report_id}><IconFileAnalytics size={18} /><div><strong>{item.type} · {item.status}</strong><p>{item.summary}</p><small>{time(item.created_at)}</small></div></li>)}</ul>}</section>
  </div>;
}
