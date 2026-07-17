import { IconChevronRight, IconHistory, IconRefresh } from "@tabler/icons-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { assemblyClient, type AssemblyRunStatus, type AssemblyRunSummary } from "../api/assemblyClient";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";

const statusOptions: Array<{ value: "" | AssemblyRunStatus; label: string }> = [
  { value: "", label: "全部状态" }, { value: "planned", label: "等待执行" }, { value: "provisioning", label: "准备资源" },
  { value: "generating", label: "生成中" }, { value: "validating", label: "验证中" }, { value: "completed", label: "已完成" },
  { value: "failed", label: "失败" }, { value: "cancelled", label: "已取消" }, { value: "rolling_back", label: "回滚中" }, { value: "rolled_back", label: "已回滚" },
];
const formatTime = (value: string) => new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));

export function AssemblyRunsPage() {
  const navigate = useNavigate();
  const [items, setItems] = useState<AssemblyRunSummary[]>([]);
  const [status, setStatus] = useState<"" | AssemblyRunStatus>("");
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const requestSequence = useRef(0);
  const activeRequest = useRef<AbortController | null>(null);

  const load = useCallback((nextCursor?: string, append = false) => {
    const controller = new AbortController();
    activeRequest.current?.abort();
    activeRequest.current = controller;
    const sequence = ++requestSequence.current;
    setLoading(true); setError(null);
    void assemblyClient.listRuns({ page_size: 30, ...(status ? { status } : {}), ...(nextCursor ? { cursor: nextCursor } : {}) }, { signal: controller.signal, timeoutMs: 20_000 })
      .then((page) => {
        if (controller.signal.aborted || sequence !== requestSequence.current) return;
        setItems((current) => append ? [...current, ...page.items] : page.items);
        setCursor(page.next_cursor);
      }).catch((reason: unknown) => {
        if (controller.signal.aborted || sequence !== requestSequence.current) return;
        setError(reason instanceof Error ? reason.message : "装配记录加载失败");
      }).finally(() => { if (!controller.signal.aborted && sequence === requestSequence.current) setLoading(false); });
    return controller;
  }, [status]);

  useEffect(() => { load(); return () => { activeRequest.current?.abort(); requestSequence.current += 1; }; }, [load]);

  return <Shell title="装配记录" subtitle="查看持久化运行、诊断、报告与恢复入口">
    <div className="toolbar assembly-toolbar">
      <div><span className="eyebrow">平台范围</span><strong>{items.length} 条装配运行</strong></div>
      <label className="assembly-filter">状态<select aria-label="装配状态" value={status} onChange={(event) => setStatus(event.target.value as "" | AssemblyRunStatus)}>{statusOptions.map((item) => <option value={item.value} key={item.value}>{item.label}</option>)}</select></label>
      <button className="secondary-button" type="button" disabled={loading} onClick={() => load()}><IconRefresh className={loading ? "spin" : ""} size={17} />刷新</button>
    </div>
    {error && <div className="query-state query-error" role="alert"><span>{error}</span><button className="secondary-button" type="button" onClick={() => load()}>重试</button></div>}
    {!error && loading && items.length === 0 && <div className="query-state" role="status">正在加载装配记录...</div>}
    {!error && !loading && items.length === 0 && <div className="panel empty-state assembly-empty"><IconHistory size={34} /><strong>还没有装配记录</strong><span>从创建软件开始后，持久化运行会显示在这里。</span></div>}
    {items.length > 0 && <section className="panel table-panel"><div className="table-scroll"><table className="assembly-table"><thead><tr><th>运行</th><th>状态</th><th>进度</th><th>诊断 / 报告</th><th>更新时间</th><th aria-label="操作" /></tr></thead><tbody>
      {items.map((item) => <tr key={item.run_id} tabIndex={0} onClick={() => navigate(`/assemblies/${encodeURIComponent(item.run_id)}`)} onKeyDown={(event) => { if (event.key === "Enter") navigate(`/assemblies/${encodeURIComponent(item.run_id)}`); }}>
        <td><strong>{item.run_id}</strong><span className="cell-subtitle">第 {item.attempt_number} 次 · Plan {item.plan_id}</span></td>
        <td><StatusBadge status={item.status} /></td><td>{item.current_step_id ?? "等待调度"}</td><td>{item.diagnostic_count} / {item.report_count}</td><td>{formatTime(item.updated_at)}</td>
        <td><button className="table-action" type="button" aria-label={`查看 ${item.run_id}`} onClick={(event) => { event.stopPropagation(); navigate(`/assemblies/${encodeURIComponent(item.run_id)}`); }}><IconChevronRight size={18} /></button></td>
      </tr>)}</tbody></table></div>{cursor && <footer className="assembly-pagination"><button className="secondary-button" type="button" disabled={loading} onClick={() => load(cursor, true)}>加载更多</button></footer>}</section>}
  </Shell>;
}
