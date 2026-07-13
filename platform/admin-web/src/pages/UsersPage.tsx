import { IconFilter, IconSearch, IconUserPlus } from "@tabler/icons-react";
import { FormEvent, useState } from "react";
import { adminClient } from "../api/adminClient";
import { useAppContext } from "../app/AppContext";
import { useScopedQuery } from "../app/useScopedQuery";
import { Modal } from "../components/Modal";
import { QueryState } from "../components/QueryState";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";
import type { UserRecord } from "../types";

export function UsersPage() {
  const { currentProduct, currentTenant } = useAppContext();
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("all");
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [account, setAccount] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const scopeKey = currentProduct && currentTenant ? `${currentProduct.id}:${currentTenant.id}` : null;
  const query = useScopedQuery<UserRecord[]>(scopeKey, () => adminClient.listUsers(currentProduct!.id, currentTenant!.id), []);
  const filtered = query.data.filter((user) => (status === "all" || user.status === status) && `${user.name}${user.account}${user.id}`.toLowerCase().includes(search.toLowerCase()));

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!currentProduct || !currentTenant || submitting) return;
    const productId = currentProduct.id;
    const tenantId = currentTenant.id;
    setSubmitting(true);
    setSubmitError(null);
    try {
      await adminClient.createUser(productId, tenantId, name, account);
      query.retry();
      setOpen(false);
      setName("");
      setAccount("");
    } catch (reason) {
      setSubmitError(reason instanceof Error ? reason.message : "添加用户失败，请重试");
    } finally {
      setSubmitting(false);
    }
  };

  return <Shell title="用户管理" subtitle={`${currentProduct?.name ?? "当前软件"} · ${currentTenant?.name ?? "正在解析租户"}`}>
    <section className="toolbar filters"><div className="search-field"><IconSearch size={18}/><input aria-label="搜索用户" value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索用户、账号或编号" /></div><div className="select-wrap filter-select"><IconFilter size={17}/><select aria-label="用户状态" value={status} onChange={(event) => setStatus(event.target.value)}><option value="all">全部状态</option><option value="active">正常</option><option value="trial">试用中</option><option value="expired">已到期</option><option value="locked">已锁定</option></select></div><button className="primary-button" type="button" disabled={!currentTenant} onClick={() => setOpen(true)}><IconUserPlus size={18}/>添加用户</button></section>
    <QueryState loading={query.loading} error={query.error} onRetry={query.retry} />
    {!query.loading && !query.error && <section className="panel table-panel"><div className="table-heading"><div><h2>用户列表</h2><p>当前租户共 {filtered.length} 位用户</p></div></div><div className="table-scroll"><table><thead><tr><th>用户</th><th>会员</th><th>租户</th><th>设备</th><th>有效期</th><th>最后活跃</th><th>状态</th></tr></thead><tbody>{filtered.map((user) => <tr key={user.id}><td><div className="user-cell"><span>{user.name.slice(0, 1)}</span><div><strong>{user.name}</strong><small>{user.account} · {user.id}</small></div></div></td><td>{user.plan}</td><td>{user.tenant}</td><td>{user.devices} 台</td><td>{user.validUntil}</td><td>{user.lastActive}</td><td><StatusBadge status={user.status}/></td></tr>)}</tbody></table></div>{filtered.length === 0 && <div className="empty-state"><IconSearch size={28}/><strong>没有符合条件的用户</strong><span>调整搜索词或筛选条件</span></div>}</section>}
    <Modal open={open} onClose={() => !submitting && setOpen(false)} title="添加用户"><form className="form" onSubmit={submit}><label>用户名称<input required value={name} onChange={(event) => setName(event.target.value)} placeholder="用户显示名称" /></label><label>登录账号<input required type="email" value={account} onChange={(event) => setAccount(event.target.value)} placeholder="name@example.com" /></label><p className="form-note">用户将加入当前租户：{currentTenant?.name}</p>{submitError && <p className="form-error" role="alert">{submitError}</p>}<footer><button className="secondary-button" type="button" disabled={submitting} onClick={() => setOpen(false)}>取消</button><button className="primary-button" type="submit" disabled={submitting}>{submitting ? "添加中..." : "确认添加"}</button></footer></form></Modal>
  </Shell>;
}
