import { adminClient } from "../api/adminClient";
import { useAppContext } from "../app/AppContext";
import { useScopedQuery } from "../app/useScopedQuery";
import { QueryState } from "../components/QueryState";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";
import type { AuditRecord } from "../types";

export function AuditPage() {
  const { currentProduct, currentTenant } = useAppContext();
  const scopeKey = currentProduct && currentTenant ? `${currentProduct.id}:${currentTenant.id}` : null;
  const query = useScopedQuery<AuditRecord[]>(scopeKey, () => adminClient.listAudits(currentProduct!.id, currentTenant!.id), []);
  return <Shell title="操作审计" subtitle={`${currentProduct?.name ?? "当前软件"} · ${currentTenant?.name ?? "正在解析租户"}`}>
    <QueryState loading={query.loading} error={query.error} onRetry={query.retry} />
    {!query.loading && !query.error && <section className="panel table-panel"><div className="table-heading"><div><h2>审计事件</h2><p>写操作、权限拒绝和敏感变更不可静默</p></div></div><div className="table-scroll"><table><thead><tr><th>时间</th><th>操作者</th><th>操作</th><th>对象</th><th>结果</th><th>事件编号</th></tr></thead><tbody>{query.data.map((item) => <tr key={item.id}><td>{item.createdAt}</td><td><strong>{item.actor}</strong></td><td>{item.action}</td><td>{item.target}</td><td><StatusBadge status={item.result}/></td><td><code>{item.id}</code></td></tr>)}</tbody></table></div>{query.data.length === 0 && <div className="empty-state"><strong>当前租户暂无审计事件</strong></div>}</section>}
  </Shell>;
}
