import { IconCopy } from "@tabler/icons-react";
import { useAppContext } from "../app/AppContext";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";

const dateTime = (value: string) => new Date(value).toLocaleString("zh-CN");

export function SettingsPage() {
  const { currentProduct } = useAppContext();
  return <Shell title="基本信息" subtitle={`${currentProduct?.name ?? "当前软件"} · 服务端真实 Product 资料`}>
    <section className="settings-grid readonly-settings">
      <article className="panel settings-panel"><div className="panel-title"><div><h2>软件身份</h2><p>本页只读，避免在缺少更新契约时制造假保存</p></div></div><dl className="detail-list"><div><dt>软件名称</dt><dd>{currentProduct?.name}</dd></div><div><dt>Product ID</dt><dd className="copy-value"><code>{currentProduct?.id}</code><button type="button" title="复制 Product ID" onClick={() => void navigator.clipboard.writeText(currentProduct?.id ?? "")}><IconCopy size={17}/></button></dd></div><div><dt>产品代码</dt><dd className="copy-value"><code>{currentProduct?.code}</code><button type="button" title="复制产品代码" onClick={() => void navigator.clipboard.writeText(currentProduct?.code ?? "")}><IconCopy size={17}/></button></dd></div><div><dt>官方租户</dt><dd><code>{currentProduct?.officialTenantId ?? "尚未建立"}</code></dd></div></dl></article>
      <article className="panel settings-panel"><div className="panel-title"><div><h2>运行上下文</h2><p>状态与版本均由服务端控制</p></div></div><dl className="detail-list"><div><dt>运行状态</dt><dd>{currentProduct && <StatusBadge status={currentProduct.status}/>}</dd></div><div><dt>装配状态</dt><dd>{currentProduct && <StatusBadge status={currentProduct.provisioningState}/>}</dd></div><div><dt>上下文版本</dt><dd>{currentProduct?.contextVersion}</dd></div><div><dt>创建时间</dt><dd>{currentProduct ? dateTime(currentProduct.createdAt) : ""}</dd></div><div><dt>更新时间</dt><dd>{currentProduct ? dateTime(currentProduct.updatedAt) : ""}</dd></div><div><dt>审计事件</dt><dd><code>{currentProduct?.auditId ?? "未返回"}</code></dd></div></dl></article>
    </section>
  </Shell>;
}
