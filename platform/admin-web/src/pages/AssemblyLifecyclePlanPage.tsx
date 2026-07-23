import { IconAlertTriangle, IconArrowLeft, IconChecks, IconLoader2, IconPlayerPlay, IconRefresh } from "@tabler/icons-react";
import { useCallback, useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { assemblyClient, type AssemblyLifecyclePlan } from "../api/assemblyClient";
import { useAuth } from "../app/AuthContext";
import { Modal } from "../components/Modal";
import { Shell } from "../components/Shell";
import { clearLifecycleIntent, lifecycleErrorMessage, lifecycleHasIdempotencyConflict, lifecycleIntent, lifecycleRequiresReauthentication } from "../features/assembly/lifecycleIntent";

const actionLabels = { create: "新增", update: "更新", delete: "删除", unchanged: "不变", eject: "退出托管" };
const operationLabels = { upgrade: "升级", eject: "退出托管" };
const migrationKindLabels = { database: "数据库迁移", provider: "Provider 动作", configuration: "配置变更" };
const reversibilityLabels = { reversible: "可逆", compensatable: "可补偿", manual: "需人工恢复" };
const rollbackStrategyLabels = { restore_predecessor: "恢复前序版本", compensate: "执行补偿", manual: "人工回滚" };
export function AssemblyLifecyclePlanPage() {
  const { planId } = useParams();
  const navigate = useNavigate();
  const { session, logout } = useAuth();
  const permissions = new Set(session?.authorization.permissions ?? []);
  const canRead = permissions.has("assembly.read");
  const canExecute = permissions.has("assembly.lifecycle.execute");
  const [plan, setPlan] = useState<AssemblyLifecyclePlan | null>(null);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [accepted, setAccepted] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [reauthenticationRequired, setReauthenticationRequired] = useState(false);
  const [idempotencyConflict, setIdempotencyConflict] = useState(false);
  const [preparingLogin, setPreparingLogin] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    if (!planId) return;
    if (!canRead) { setError("缺少 assembly.read 权限，无法读取生命周期计划"); setLoading(false); return; }
    setLoading(true); setError(null);
    try { setPlan(await assemblyClient.getLifecyclePlan(planId, { signal, timeoutMs: 20_000 })); }
    catch (reason) { if (!signal?.aborted && !(reason instanceof DOMException && reason.name === "AbortError")) setError(lifecycleErrorMessage(reason, "生命周期计划加载失败")); }
    finally { if (!signal?.aborted) setLoading(false); }
  }, [canRead, planId]);
  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort(); }, [load]);

  const execute = async () => {
    if (!plan || submitting || !accepted || !canExecute || !plan.executable || plan.blocking_conflict_count > 0) return;
    setSubmitting(true); setError(null); setReauthenticationRequired(false); setIdempotencyConflict(false);
    try {
      const operation = await assemblyClient.executeLifecyclePlan(plan.lifecycle_plan_id, plan.version, plan.plan_checksum, plan.confirmation_checksum, { idempotencyKey: lifecycleIntent("execute", plan.lifecycle_plan_id), timeoutMs: 30_000 });
      clearLifecycleIntent("execute", plan.lifecycle_plan_id);
      navigate(`/assembly-lifecycle/operations/${encodeURIComponent(operation.operation_id)}`);
    } catch (reason) {
      setReauthenticationRequired(lifecycleRequiresReauthentication(reason));
      setIdempotencyConflict(lifecycleHasIdempotencyConflict(reason));
      setConfirmOpen(false);
      setAccepted(false);
      setError(lifecycleErrorMessage(reason, "生命周期操作启动失败"));
    }
    finally { setSubmitting(false); }
  };

  const beginReauthentication = async () => {
    if (!planId || preparingLogin) return;
    setPreparingLogin(true);
    try {
      await logout();
      navigate("/login", { replace: true, state: { from: `/assembly-lifecycle/plans/${encodeURIComponent(planId)}` } });
    } catch (reason) {
      setError(lifecycleErrorMessage(reason, "暂时无法进入重新登录，请稍后重试"));
      setReauthenticationRequired(true);
      setPreparingLogin(false);
    }
  };

  const restartExecuteIntent = () => {
    if (!plan || submitting) return;
    clearLifecycleIntent("execute", plan.lifecycle_plan_id);
    setIdempotencyConflict(false);
    setError(null);
    setAccepted(false);
    setConfirmOpen(true);
  };

  return <Shell title="生命周期计划" subtitle={planId ?? "差异、冲突与回滚审查"}>
    <div className="toolbar"><button className="secondary-button" type="button" onClick={() => navigate(-1)}><IconArrowLeft size={17} />返回</button><div className="assembly-toolbar-spacer" /><button className="secondary-button" type="button" disabled={loading || !canRead} onClick={() => void load()}><IconRefresh className={loading ? "spin" : ""} size={17} />刷新</button>{plan && <button className="primary-button" type="button" disabled={!canExecute || !plan.executable || plan.blocking_conflict_count > 0} title={canExecute ? "执行已审查的计划" : "缺少 assembly.lifecycle.execute 权限"} onClick={() => { setAccepted(false); setConfirmOpen(true); }}><IconPlayerPlay size={17} />执行计划</button>}</div>
    {(error || reauthenticationRequired || idempotencyConflict) && <div className="create-global-error" role="alert"><IconAlertTriangle size={18} /><span>{error ?? (reauthenticationRequired ? "此操作需要近期重新认证。" : "旧操作意图需要由管理员明确处理。")}</span>{reauthenticationRequired && <button className="secondary-button" type="button" disabled={preparingLogin} onClick={() => void beginReauthentication()}>{preparingLogin ? "正在退出当前会话..." : "重新登录并返回计划"}</button>}{idempotencyConflict && <button className="secondary-button" type="button" onClick={restartExecuteIntent}>放弃旧意图并重新发起</button>}</div>}
    {loading && !plan && <div className="query-state" role="status"><IconLoader2 className="spin" size={18} />正在读取差异计划...</div>}
    {plan && <div className="lifecycle-plan-layout">
      <section className="panel lifecycle-summary"><header className="panel-title"><div><h2>{operationLabels[plan.operation]}计划</h2><p>计划 v{plan.version} · 创建于 {new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(new Date(plan.created_at))}</p></div><span className={`status ${plan.executable ? "status-completed" : "status-failed"}`}>{plan.executable ? "可执行" : "已阻止"}</span></header><div className="plan-metrics"><span><strong>{plan.changes.length}</strong>文件变更</span><span className={plan.blocking_conflict_count ? "danger" : ""}><strong>{plan.blocking_conflict_count}</strong>阻断冲突</span><span><strong>{plan.conflicts.length}</strong>全部冲突</span><span><strong>{plan.regression_tests.length}</strong>回归检查</span></div><dl className="detail-list"><div><dt>Assembly</dt><dd><code>{plan.assembly_id}</code></dd></div><div><dt>Product</dt><dd><code>{plan.product_id}</code></dd></div><div><dt>源 Manifest</dt><dd><code>{plan.source.manifest_id}</code></dd></div><div><dt>源 Lock</dt><dd><code>{plan.source.lock_id}</code></dd></div></dl></section>
      <section className="panel lifecycle-table-panel"><header className="panel-title"><div><h2>变更清单</h2><p>仅展示仓库相对路径与所有权，不展示文件内容</p></div></header>{plan.changes.length === 0 ? <div className="empty-state compact-empty">没有文件变更</div> : <div className="table-scroll"><table className="data-table lifecycle-table"><thead><tr><th>路径</th><th>动作</th><th>所有权</th><th>来源</th></tr></thead><tbody>{plan.changes.map((change) => <tr key={`${change.path}:${change.action}`}><td><code>{change.path}</code></td><td>{actionLabels[change.action]}</td><td>{change.ownership}</td><td>{change.source_id}@{change.source_version}</td></tr>)}</tbody></table></div>}</section>
      <section className="panel lifecycle-table-panel"><header className="panel-title"><div><h2>迁移与 Provider 动作</h2><p>执行前审查外部副作用及其可逆性</p></div></header>{plan.migrations.length === 0 ? <div className="empty-state compact-empty">本计划没有数据库迁移、Provider 动作或配置变更</div> : <div className="table-scroll"><table className="data-table lifecycle-table"><thead><tr><th>动作</th><th>类型</th><th>可逆性</th><th>摘要</th></tr></thead><tbody>{plan.migrations.map((migration) => <tr key={migration.migration_id}><td><code>{migration.migration_id}</code></td><td>{migrationKindLabels[migration.kind]}</td><td>{reversibilityLabels[migration.reversibility]}</td><td>{migration.summary}</td></tr>)}</tbody></table></div>}</section>
      <section className="panel assembly-projections"><header className="panel-title"><div><h2>冲突与处理建议</h2><p>阻断冲突必须先处理，不能强制越过</p></div></header>{plan.conflicts.length === 0 ? <div className="empty-state compact-empty"><IconChecks size={22} />未发现冲突</div> : <ul>{plan.conflicts.map((conflict) => <li key={conflict.conflict_id}><IconAlertTriangle size={18} /><div><strong>{conflict.code}{conflict.blocking ? " · 阻断" : ""}</strong><p>{conflict.message}</p>{conflict.paths.map((path) => <small key={path}><code>{path}</code></small>)}{conflict.remediation.map((line) => <small key={line}>{line}</small>)}</div></li>)}</ul>}</section>
      <section className="panel lifecycle-confirmation"><header className="panel-title"><div><h2>执行与回滚摘要</h2><p>执行前必须明确接受全部服务端确认声明</p></div></header><ul>{plan.statements.map((statement) => <li key={statement}>{statement}</li>)}</ul><dl className="detail-list"><div><dt>回滚策略</dt><dd>{rollbackStrategyLabels[plan.rollback.strategy]}</dd></div><div><dt>自动回滚</dt><dd>{plan.rollback.automatic ? "允许" : "不允许"}</dd></div></dl><div className="lifecycle-regression"><strong>执行后回归检查</strong>{plan.regression_tests.length ? plan.regression_tests.map((test) => <code key={test}>{test}</code>) : <span>服务端未声明额外回归检查</span>}</div><p>执行会以当前 Manifest、Lock 与目标快照校验和做并发保护。前序校验和仅用于服务端并发与回滚校验，不在页面暴露原始产物。</p></section>
    </div>}
    <Modal title={`确认执行${plan ? operationLabels[plan.operation] : ""}计划`} open={confirmOpen} onClose={() => { if (!submitting) { setConfirmOpen(false); setAccepted(false); } }}><div className="modal-body lifecycle-modal"><div className="lifecycle-risk"><IconAlertTriangle size={20} /><div><strong>这是高风险操作</strong><p>执行期间浏览器断开不会取消操作。请确认你已审查文件变更、迁移与 Provider 动作、冲突、回滚策略和回归检查。</p></div></div><label className="lifecycle-checkbox"><input type="checkbox" checked={accepted} onChange={(event) => setAccepted(event.target.checked)} />我已审查迁移和回滚策略，并接受计划中的全部确认声明</label></div><div className="modal-actions"><button className="secondary-button" type="button" disabled={submitting} onClick={() => { setConfirmOpen(false); setAccepted(false); }}>返回审查</button><button className="primary-button" type="button" disabled={!canExecute || !accepted || submitting} onClick={() => void execute()}>{submitting ? "正在启动..." : "确认执行"}</button></div></Modal>
  </Shell>;
}
