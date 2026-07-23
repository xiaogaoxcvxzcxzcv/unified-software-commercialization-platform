import { IconArrowLeft, IconArrowRight, IconLoader2 } from "@tabler/icons-react";
import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { assemblyClient, type AssemblyCatalogScope, type AssemblyPlanRecord, type BlueprintRecord, type OutputTargetCatalog } from "../api/assemblyClient";
import { Shell } from "../components/Shell";

type RecoveryIntentKind = "plan" | "assembly";
const intentPattern = /^(?:plan|assembly)-[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
function recoveryIntent(kind: RecoveryIntentKind, resourceId: string) {
  const storageKey = `assembly_${kind}_intent:${resourceId}`;
  try {
    const saved = sessionStorage.getItem(storageKey);
    if (saved && intentPattern.test(saved) && saved.startsWith(`${kind}-`)) return saved;
    const created = `${kind}-${crypto.randomUUID()}`;
    sessionStorage.setItem(storageKey, created);
    return created;
  } catch { return `${kind}-${crypto.randomUUID()}`; }
}
function clearRecoveryIntent(kind: RecoveryIntentKind, resourceId: string) { try { sessionStorage.removeItem(`assembly_${kind}_intent:${resourceId}`); } catch { /* Memory-only browsers discard the fallback with the page. */ } }
function Header({ back }: { back: () => void }) { return <div className="toolbar"><button className="secondary-button" type="button" onClick={back}><IconArrowLeft size={17} />返回</button><div><span className="eyebrow">持久化创建记录</span></div></div>; }
function Loading() { return <div className="query-state" role="status"><IconLoader2 className="spin" size={18} />正在读取持久化记录...</div>; }
function ErrorState({ message }: { message: string }) { return <div className="query-state query-error" role="alert"><span>{message}</span><button className="secondary-button" type="button" onClick={() => location.reload()}>重试读取</button></div>; }

export function CreateBlueprintRecoveryPage({ catalogScope = "ordinary" }: { catalogScope?: AssemblyCatalogScope }) {
  const { blueprintId } = useParams(); const navigate = useNavigate();
  const [record, setRecord] = useState<BlueprintRecord | null>(null); const [error, setError] = useState<string | null>(null); const [busy, setBusy] = useState(false);
  const intentRef = useRef<string | null>(null);
  useEffect(() => { if (!blueprintId) return; const controller = new AbortController(); void assemblyClient.getBlueprint(blueprintId, { signal: controller.signal }).then(setRecord).catch((reason: unknown) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "蓝图恢复失败"); }); return () => controller.abort(); }, [blueprintId]);
  const proceed = async () => { if (!record || busy || record.environments.length !== 1) return; setBusy(true); setError(null); try { intentRef.current ??= recoveryIntent("plan", record.blueprint_id); const createScopedPlan = catalogScope === "experimental" ? assemblyClient.createExperimentalPlan : assemblyClient.createPlan; const plan = await createScopedPlan(record.blueprint_id, { blueprint_version: record.version, environment: record.environments[0] }, { idempotencyKey: intentRef.current }); clearRecoveryIntent("plan", record.blueprint_id); intentRef.current = null; navigate(`${catalogScope === "experimental" ? "/create/experimental/plans" : "/create/plans"}/${encodeURIComponent(plan.plan_id)}`); } catch (reason) { setError(reason instanceof Error ? reason.message : "计划创建失败"); setBusy(false); } };
  return <Shell title={catalogScope === "experimental" ? "恢复实验创建流程" : "恢复创建流程"} subtitle="从持久化蓝图继续"><Header back={() => navigate(catalogScope === "experimental" ? "/create/experimental" : "/create")} />{error && <ErrorState message={error} />}{!record && !error && <Loading />}{record && <section className="panel recovery-panel"><header className="panel-title"><div><h2>蓝图已恢复</h2><p>版本 {record.version}</p></div></header><dl className="detail-list"><div><dt>Blueprint</dt><dd><code>{record.blueprint_id}</code></dd></div><div><dt>环境</dt><dd>{record.environments.join("、")}</dd></div><div><dt>校验摘要</dt><dd><code>{record.checksum}</code></dd></div><div><dt>目录</dt><dd>{catalogScope === "experimental" ? "experimental / verified" : "ordinary / available"}</dd></div></dl>{record.environments.length !== 1 && <div className="assembly-readonly-notice" role="alert">该蓝图包含多个环境，无法继续装配，请重新创建单一环境蓝图。</div>}<footer className="recovery-actions"><button className="primary-button" type="button" disabled={busy || record.environments.length !== 1} onClick={() => void proceed()}>{busy ? "正在解析..." : "解析装配计划"}<IconArrowRight size={17} /></button></footer></section>}</Shell>;
}

export function CreatePlanRecoveryPage({ catalogScope = "ordinary" }: { catalogScope?: AssemblyCatalogScope }) {
  const { planId } = useParams(); const navigate = useNavigate();
  const [plan, setPlan] = useState<AssemblyPlanRecord | null>(null); const [targets, setTargets] = useState<OutputTargetCatalog | null>(null);
  const [target, setTarget] = useState(""); const [error, setError] = useState<string | null>(null); const [busy, setBusy] = useState(false); const sequence = useRef(0);
  const intentRef = useRef<string | null>(null);
  useEffect(() => {
    if (!planId) return;
    const controller = new AbortController(); const current = ++sequence.current;
    void assemblyClient.getPlan(planId, { signal: controller.signal }).then(async (nextPlan) => {
      const nextTargets = await assemblyClient.listOutputTargets(nextPlan.environment, { signal: controller.signal });
      if (controller.signal.aborted || current !== sequence.current) return;
      setPlan(nextPlan); setTargets(nextTargets); setTarget(nextTargets.default_output_target_ref ?? "");
    }).catch((reason: unknown) => { if (!controller.signal.aborted && current === sequence.current) setError(reason instanceof Error ? reason.message : "计划恢复失败"); });
    return () => { controller.abort(); sequence.current += 1; };
  }, [planId]);
  const start = async () => {
    if (!plan || !target || busy) return; setBusy(true); setError(null);
    try {
      intentRef.current ??= recoveryIntent("assembly", plan.plan_id);
      const run = await assemblyClient.startAssembly(plan.blueprint_id, { plan_id: plan.plan_id, expected_plan_version: plan.version, plan_checksum: plan.checksum, confirmation: { accepted: true, summary_checksum: plan.confirmation_checksum }, output_target_ref: target }, { idempotencyKey: intentRef.current });
      clearRecoveryIntent("assembly", plan.plan_id); intentRef.current = null;
      navigate(`/assemblies/${encodeURIComponent(run.run_id)}?handoff=1`);
    } catch (reason) { setError(reason instanceof Error ? reason.message : "装配启动失败"); setBusy(false); }
  };
  return <Shell title={catalogScope === "experimental" ? "恢复实验创建流程" : "恢复创建流程"} subtitle="从持久化计划继续"><Header back={() => plan ? navigate(`${catalogScope === "experimental" ? "/create/experimental/blueprints" : "/create/blueprints"}/${encodeURIComponent(plan.blueprint_id)}`) : navigate(catalogScope === "experimental" ? "/create/experimental" : "/create")} />{error && <ErrorState message={error} />}{!plan && !error && <Loading />}{plan && <section className="panel recovery-panel"><header className="panel-title"><div><h2>计划已恢复</h2><p>版本 {plan.version}</p></div></header><dl className="detail-list"><div><dt>Plan</dt><dd><code>{plan.plan_id}</code></dd></div><div><dt>环境</dt><dd>{plan.environment}</dd></div><div><dt>状态</dt><dd>{plan.executable ? "可执行" : "不可执行"}</dd></div><div><dt>目录</dt><dd>{catalogScope === "experimental" ? "experimental / verified" : "ordinary / available"}</dd></div></dl><PlanReview plan={plan} /><fieldset className="output-targets recovery-targets"><legend>服务端授权输出目标</legend>{targets?.items.map((item) => <label key={item.output_target_ref} className={target === item.output_target_ref ? "selected" : ""}><input type="radio" name="recovery-target" checked={target === item.output_target_ref} onChange={() => setTarget(item.output_target_ref)} /><span><strong>{item.display_name}</strong><small>{item.summary}</small></span></label>)}</fieldset><footer className="recovery-actions"><button className="primary-button" type="button" disabled={!plan.executable || plan.review.blocking_conflict_count > 0 || !target || busy} onClick={() => void start()}>{busy ? "正在启动..." : "确认并开始装配"}<IconArrowRight size={17} /></button></footer></section>}</Shell>;
}

function PlanReview({ plan }: { plan: AssemblyPlanRecord }) {
  return <div className="recovery-review"><section><h3>能力包</h3><ul>{plan.review.packages.map((item) => <li key={`${item.package_id}@${item.version}`}><strong>{item.package_id}</strong><span>{item.version}</span></li>)}</ul></section><section><h3>Application 与模板</h3><ul>{plan.review.applications.map((item) => <li key={item.application_id}><strong>{item.application_id}</strong><span>{item.target} · {item.channel} · {item.delivery_mode} · {item.template_id}@{item.template_version}</span></li>)}</ul></section><section><h3>风险</h3>{plan.review.risks.length ? <ul>{plan.review.risks.map((item) => <li key={item.risk_id}><strong>{item.level} · {item.category}</strong><span>{item.summary}</span></li>)}</ul> : <p>无已识别风险</p>}</section><section><h3>确认声明</h3><ul>{plan.review.statements.map((item) => <li key={item}><span>{item}</span></li>)}</ul>{plan.review.blocking_conflict_count > 0 && <p className="review-blocking" role="alert">存在 {plan.review.blocking_conflict_count} 个阻断冲突</p>}</section></div>;
}
