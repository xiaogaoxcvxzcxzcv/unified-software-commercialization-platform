import { IconCirclePlus } from "@tabler/icons-react";
import { FormEvent, useState } from "react";
import { adminClient } from "../api/adminClient";
import { useAppContext } from "../app/AppContext";
import { useScopedQuery } from "../app/useScopedQuery";
import { Modal } from "../components/Modal";
import { QueryState } from "../components/QueryState";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";
import type { EntitlementRecord, UserRecord } from "../types";

interface EntitlementData { items: EntitlementRecord[]; users: UserRecord[] }

export function EntitlementsPage() {
  const { currentProduct, currentTenant } = useAppContext();
  const [open, setOpen] = useState(false);
  const [userId, setUserId] = useState("");
  const [plan, setPlan] = useState("专业年卡");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const scopeKey = currentProduct && currentTenant ? `${currentProduct.id}:${currentTenant.id}` : null;
  const query = useScopedQuery<EntitlementData>(scopeKey, async () => {
    const [items, users] = await Promise.all([
      adminClient.listEntitlements(currentProduct!.id, currentTenant!.id),
      adminClient.listUsers(currentProduct!.id, currentTenant!.id),
    ]);
    return { items, users };
  }, { items: [], users: [] });

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!currentProduct || !currentTenant || !userId || submitting) return;
    const productId = currentProduct.id;
    const tenantId = currentTenant.id;
    setSubmitting(true);
    setSubmitError(null);
    try {
      await adminClient.grantEntitlement(productId, tenantId, userId, plan);
      query.retry();
      setOpen(false);
      setUserId("");
    } catch (reason) {
      setSubmitError(reason instanceof Error ? reason.message : "授予权益失败，请重试");
    } finally {
      setSubmitting(false);
    }
  };

  return <Shell title="权益管理" subtitle={`${currentProduct?.name ?? "当前软件"} · ${currentTenant?.name ?? "正在解析租户"}`}>
    <section className="toolbar"><div><span className="eyebrow">权益流水</span><strong>所有来源统一归集到当前租户权益中心</strong></div><button className="primary-button" type="button" disabled={!currentTenant} onClick={() => setOpen(true)}><IconCirclePlus size={18}/>授予权益</button></section>
    <QueryState loading={query.loading} error={query.error} onRetry={query.retry} />
    {!query.loading && !query.error && <section className="panel table-panel"><div className="table-heading"><div><h2>有效权益</h2><p>付款、激活码、试用和人工授予统一展示</p></div></div><div className="table-scroll"><table><thead><tr><th>权益编号</th><th>用户</th><th>套餐</th><th>来源</th><th>有效期</th><th>状态</th></tr></thead><tbody>{query.data.items.map((item) => <tr key={item.id}><td><code>{item.id}</code></td><td>{item.user}</td><td><strong>{item.plan}</strong></td><td>{item.source}</td><td>{item.validUntil}</td><td><StatusBadge status={item.status}/></td></tr>)}</tbody></table></div>{query.data.items.length === 0 && <div className="empty-state"><strong>当前租户没有权益记录</strong></div>}</section>}
    <Modal open={open} onClose={() => !submitting && setOpen(false)} title="授予权益"><form className="form" onSubmit={submit}><label>目标用户<select required value={userId} onChange={(event) => setUserId(event.target.value)}><option value="">请选择用户</option>{query.data.users.map((item) => <option key={item.id} value={item.id}>{item.name} · {item.account} · {item.id}</option>)}</select></label><label>权益套餐<select value={plan} onChange={(event) => setPlan(event.target.value)}><option>专业年卡</option><option>专业月卡</option><option>永久版</option><option>试用版</option></select></label><p className="form-note">演示操作只更新当前产品和租户的内存数据。</p>{submitError && <p className="form-error" role="alert">{submitError}</p>}<footer><button className="secondary-button" type="button" disabled={submitting} onClick={() => setOpen(false)}>取消</button><button className="primary-button" type="submit" disabled={submitting || !userId}>{submitting ? "授予中..." : "确认授予"}</button></footer></form></Modal>
  </Shell>;
}
