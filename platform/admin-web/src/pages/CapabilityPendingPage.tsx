import { IconLock } from "@tabler/icons-react";
import { useParams } from "react-router-dom";
import { useAppContext } from "../app/AppContext";
import { Shell } from "../components/Shell";

const labels: Record<string, string> = { users: "用户管理", entitlements: "权益管理", tenants: "代理租户" };
const packages: Record<string, string> = { users: "package.account", entitlements: "package.entitlement", tenants: "package.agent-operation" };

export function CapabilityPendingPage() {
  const { feature = "" } = useParams();
  const { currentProduct, enabledPackageIds, workspaceLoading, workspaceError, refreshWorkspace } = useAppContext();
  const packageId = packages[feature];
  if (workspaceLoading) return <Shell title={labels[feature] ?? "能力管理"} subtitle="正在验证可信能力集"><div className="route-loading">正在验证能力包...</div></Shell>;
  if (workspaceError) return <Shell title={labels[feature] ?? "能力管理"} subtitle="能力验证失败"><div className="route-loading"><span>{workspaceError}</span><button className="secondary-button" type="button" onClick={() => void refreshWorkspace()}>重试</button></div></Shell>;
  const enabled = Boolean(packageId && enabledPackageIds.has(packageId));
  return <Shell title={labels[feature] ?? "能力管理"} subtitle={`${currentProduct?.name ?? "当前软件"} · 能力包管理面`}>
    <section className="panel capability-pending" role={!enabled ? "alert" : undefined}><IconLock size={28}/><strong>{enabled ? "管理页面尚未交付" : "当前软件未启用此能力"}</strong><p>{enabled ? "可信能力包已启用，但该管理页面将在对应完整能力包关口通过九个交付面后开放。当前不会加载演示数据。" : "服务端可信能力集未启用对应能力包。此旧书签已失败关闭，不会发起该能力的业务请求。"}</p>{packageId && <code>{packageId}</code>}</section>
  </Shell>;
}
