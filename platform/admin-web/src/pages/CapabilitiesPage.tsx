import { IconRefresh } from "@tabler/icons-react";
import { useAppContext } from "../app/AppContext";
import { QueryState } from "../components/QueryState";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";

export function CapabilitiesPage() {
  const { currentProduct, capabilityProjection, workspaceLoading, workspaceError, refreshWorkspace } = useAppContext();
  const set = capabilityProjection?.capabilitySet ?? null;
  return <Shell title="能力目录" subtitle={`${currentProduct?.name ?? "当前软件"} · 可信 ProductCapabilitySet`}>
    <section className="toolbar"><div><span className="eyebrow">CapabilitySet</span><strong>{set ? `版本 ${set.version} · 目录 ${set.catalogRevision}` : "当前软件尚无可信能力集"}</strong></div><button className="secondary-button" type="button" onClick={() => void refreshWorkspace()}><IconRefresh size={18}/>刷新能力集</button></section>
    <QueryState loading={workspaceLoading} error={workspaceError} onRetry={() => void refreshWorkspace()} />
    {!workspaceLoading && !workspaceError && !set && <section className="panel empty-state workspace-empty"><strong>尚无可信能力集</strong><p>系统不会用演示能力或中文名称推断工作区目录。</p></section>}
    {!workspaceLoading && !workspaceError && set && <section className="panel table-panel"><div className="table-heading"><div><h2>能力投影</h2><p>来源计划 {set.sourcePlanId} · 审计事件 {set.auditId}</p></div></div><div className="table-scroll"><table><thead><tr><th>能力 ID</th><th>来源能力包</th><th>版本</th><th>状态</th></tr></thead><tbody>{set.capabilities.map((item) => <tr key={item.capabilityId}><td><code>{item.capabilityId}</code></td><td><code>{item.sourcePackageId ?? "无来源包"}</code></td><td>{item.sourcePackageVersion ?? "-"}</td><td><StatusBadge status={item.enabled ? "active" : "suspended"}/></td></tr>)}</tbody></table></div>{set.capabilities.length === 0 && <div className="empty-state"><strong>能力集为空</strong></div>}</section>}
  </Shell>;
}
