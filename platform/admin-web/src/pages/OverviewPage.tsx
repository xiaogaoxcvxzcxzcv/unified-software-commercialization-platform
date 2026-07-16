import { IconApps, IconBuildingStore, IconLayersLinked, IconRefresh } from "@tabler/icons-react";
import { useAppContext } from "../app/AppContext";
import { QueryState } from "../components/QueryState";
import { Shell } from "../components/Shell";
import { StatCard } from "../components/StatCard";
import { StatusBadge } from "../components/StatusBadge";

const dateTime = (value: string) => new Date(value).toLocaleString("zh-CN");

export function OverviewPage() {
  const {
    products, currentProduct, applications, capabilityProjection, enabledPackageIds, tenants,
    refreshProducts, refreshWorkspace, workspaceLoading, workspaceError,
  } = useAppContext();
  if (!currentProduct) return <Shell title="平台总览" subtitle="全部可访问软件的真实接入状态">
    <section className="toolbar"><div><span className="eyebrow">平台范围</span><strong>来自 Product 服务的当前状态</strong></div><button className="secondary-button" type="button" onClick={() => void refreshProducts()}><IconRefresh size={18} />刷新软件</button></section>
    <section className="stats-grid workspace-stats">
      <StatCard icon={IconApps} label="可访问软件" value={String(products.length)} detail={`${products.filter((item) => item.status === "active").length} 款处于启用状态`} tone="blue" />
      <StatCard icon={IconLayersLinked} label="装配就绪" value={String(products.filter((item) => item.provisioningState === "ready").length)} detail="由服务端装配状态判定" tone="teal" />
    </section>
    <section className="panel table-panel"><div className="table-heading"><div><h2>软件状态</h2><p>选择软件后进入独立管理工作区</p></div></div><div className="table-scroll"><table><thead><tr><th>软件</th><th>状态</th><th>装配</th><th>上下文版本</th><th>更新时间</th></tr></thead><tbody>{products.map((product) => <tr key={product.id}><td><strong>{product.name}</strong><small className="cell-subtitle">{product.code}</small></td><td><StatusBadge status={product.status} /></td><td><StatusBadge status={product.provisioningState} /></td><td>{product.contextVersion}</td><td>{dateTime(product.updatedAt)}</td></tr>)}</tbody></table></div></section>
  </Shell>;

  return <Shell title={currentProduct.name} subtitle={`${currentProduct.code} · 产品级真实工作区`}>
    <QueryState loading={workspaceLoading} error={workspaceError} onRetry={() => void refreshWorkspace()} />
    {!workspaceLoading && !workspaceError && <>
      <section className="toolbar"><div><span className="eyebrow">软件概览</span><strong>Product、Application、Tenant 与 CapabilitySet 实时投影</strong></div><button className="secondary-button" type="button" onClick={() => void refreshWorkspace()}><IconRefresh size={18} />刷新工作区</button></section>
      <section className="stats-grid">
        <StatCard icon={IconApps} label="应用" value={String(applications.length)} detail={`${applications.filter((item) => item.status === "active").length} 个启用`} tone="blue" />
        <StatCard icon={IconBuildingStore} label="租户" value={String(tenants.length)} detail={`${tenants.filter((item) => item.type === "official").length} 个官方租户`} tone="green" />
        <StatCard icon={IconLayersLinked} label="能力包" value={String(enabledPackageIds.size)} detail={capabilityProjection?.capabilitySet ? `能力集 v${capabilityProjection.capabilitySet.version}` : "尚无可信能力集"} tone="purple" />
        <StatCard icon={IconRefresh} label="上下文版本" value={String(currentProduct.contextVersion)} detail={`更新于 ${dateTime(currentProduct.updatedAt)}`} tone="teal" />
      </section>
      <section className="workspace-overview-grid">
        <article className="panel workspace-summary"><div className="panel-title"><div><h2>软件身份</h2><p>服务端分配的不可变标识与运行状态</p></div><StatusBadge status={currentProduct.status} /></div><dl className="detail-list"><div><dt>Product ID</dt><dd><code>{currentProduct.id}</code></dd></div><div><dt>产品代码</dt><dd><code>{currentProduct.code}</code></dd></div><div><dt>装配状态</dt><dd><StatusBadge status={currentProduct.provisioningState} /></dd></div><div><dt>官方租户</dt><dd><code>{currentProduct.officialTenantId ?? "尚未建立"}</code></dd></div></dl></article>
        <article className="panel workspace-summary"><div className="panel-title"><div><h2>接入状态</h2><p>当前软件下的真实应用注册</p></div></div>{applications.length ? <ul className="compact-record-list">{applications.slice(0, 5).map((item) => <li key={item.id}><div><strong>{item.name}</strong><small>{item.platform} · {item.distributionChannel}</small></div><StatusBadge status={item.status} /></li>)}</ul> : <div className="empty-state"><strong>尚未注册应用</strong><p>应用创建后会显示在这里。</p></div>}</article>
      </section>
    </>}
  </Shell>;
}
