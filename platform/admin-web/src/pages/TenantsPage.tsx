import { IconBuildingStore } from "@tabler/icons-react";
import { FormEvent, useState } from "react";
import { adminClient } from "../api/adminClient";
import { useAppContext } from "../app/AppContext";
import { useScopedQuery } from "../app/useScopedQuery";
import { Modal } from "../components/Modal";
import { QueryState } from "../components/QueryState";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";
import type { TenantRecord } from "../types";

export function TenantsPage() {
  const { currentProduct, refreshTenants } = useAppContext();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const query = useScopedQuery<TenantRecord[]>(currentProduct?.id ?? null, () => adminClient.listTenants(currentProduct!.id), []);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!currentProduct || submitting) return;
    const productId = currentProduct.id;
    setSubmitting(true);
    setSubmitError(null);
    try {
      await adminClient.createTenant(productId, name, code);
      query.retry();
      await refreshTenants();
      setOpen(false);
      setName("");
      setCode("");
    } catch (reason) {
      setSubmitError(reason instanceof Error ? reason.message : "创建代理失败，请重试");
    } finally {
      setSubmitting(false);
    }
  };

  return <Shell title="代理租户" subtitle={`${currentProduct?.name ?? "当前软件"} · 官方与代理经营范围隔离`}>
    <section className="toolbar"><div><span className="eyebrow">产品级管理视图</span><strong>展示当前软件全部租户，不受顶部租户筛选影响</strong></div><button className="primary-button" type="button" onClick={() => setOpen(true)}><IconBuildingStore size={18}/>创建代理</button></section>
    <QueryState loading={query.loading} error={query.error} onRetry={query.retry} />
    {!query.loading && !query.error && <section className="panel table-panel"><div className="table-heading"><div><h2>租户列表</h2><p>官方租户由系统自动创建</p></div></div><div className="table-scroll"><table><thead><tr><th>租户</th><th>稳定代码</th><th>类型</th><th>管理员</th><th>用户</th><th>状态</th></tr></thead><tbody>{query.data.map((item) => <tr key={item.id}><td><strong>{item.name}</strong></td><td><code>{item.code}</code></td><td><StatusBadge status={item.type}/></td><td>{item.admins}</td><td>{item.users}</td><td><StatusBadge status={item.status}/></td></tr>)}</tbody></table></div></section>}
    <Modal open={open} onClose={() => !submitting && setOpen(false)} title="创建代理租户"><form className="form" onSubmit={submit}><label>代理名称<input required value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：华北代理" /></label><label>稳定代码<input required pattern="[a-z0-9-]+" value={code} onChange={(event) => setCode(event.target.value.toLowerCase())} placeholder="例如：north-cn" /></label><p className="form-note">代理租户只能属于当前软件，创建后自动启用数据隔离。</p>{submitError && <p className="form-error" role="alert">{submitError}</p>}<footer><button className="secondary-button" type="button" disabled={submitting} onClick={() => setOpen(false)}>取消</button><button className="primary-button" type="submit" disabled={submitting}>{submitting ? "创建中..." : "确认创建"}</button></footer></form></Modal>
  </Shell>;
}
