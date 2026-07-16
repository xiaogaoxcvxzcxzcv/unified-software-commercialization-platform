import { IconRefresh } from "@tabler/icons-react";
import { useAppContext } from "../app/AppContext";
import { QueryState } from "../components/QueryState";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";

const dateTime = (value: string) => new Date(value).toLocaleString("zh-CN");

export function IntegrationPage() {
  const { currentProduct, applications, workspaceLoading, workspaceError, refreshWorkspace } = useAppContext();
  return <Shell title="接入配置" subtitle={`${currentProduct?.name ?? "当前软件"} · 已注册应用与发布通道`}>
    <section className="toolbar"><div><span className="eyebrow">Application</span><strong>只展示服务端已登记的接入信息</strong></div><button className="secondary-button" type="button" onClick={() => void refreshWorkspace()}><IconRefresh size={18}/>刷新接入</button></section>
    <QueryState loading={workspaceLoading} error={workspaceError} onRetry={() => void refreshWorkspace()} />
    {!workspaceLoading && !workspaceError && <section className="panel table-panel"><div className="table-heading"><div><h2>应用注册</h2><p>共 {applications.length} 个应用，不展示凭据或密钥</p></div></div><div className="table-scroll"><table><thead><tr><th>应用</th><th>平台</th><th>分发通道</th><th>发布轨道</th><th>状态</th><th>上下文版本</th><th>更新时间</th></tr></thead><tbody>{applications.map((item) => <tr key={item.id}><td><strong>{item.name}</strong><small className="cell-subtitle">{item.code} · {item.id}</small></td><td>{item.platform}</td><td>{item.distributionChannel}</td><td>{item.releaseTrack}</td><td><StatusBadge status={item.status}/></td><td>{item.contextVersion}</td><td>{dateTime(item.updatedAt)}</td></tr>)}</tbody></table></div>{applications.length === 0 && <div className="empty-state"><strong>尚未注册应用</strong><p>应用注册完成后会出现在此列表。</p></div>}</section>}
  </Shell>;
}
