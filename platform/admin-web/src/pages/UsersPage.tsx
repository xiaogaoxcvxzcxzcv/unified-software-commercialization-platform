import { IconChevronLeft, IconChevronRight, IconFilter, IconSearch, IconUser } from "@tabler/icons-react";
import { FormEvent, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { accountAdminClient, type AccountScope, type AccountStatus, type AccessStatus, type AdminUserSummary } from "../api/accountAdminClient";
import { useAppContext } from "../app/AppContext";
import { useAuth } from "../app/AuthContext";
import { useScopedQuery } from "../app/useScopedQuery";
import { QueryState } from "../components/QueryState";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";

interface Filters { query: string; accountStatus: "all" | AccountStatus; accessStatus: "all" | AccessStatus }
const emptyPage = { items: [] as AdminUserSummary[], nextCursor: null as string | null };

function displayName(user: AdminUserSummary) { return user.displayName?.trim() || "未设置显示名称"; }
function identifierSummary(user: AdminUserSummary) {
  if (user.identifiers.length === 0) return "未公开登录标识";
  return user.identifiers.map((item) => `${item.maskedValue}${item.verified ? " · 已验证" : ""}`).join(" / ");
}
function dateTime(value: string | null) { return value ? new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : "暂无记录"; }

export function UsersPage() {
  const { currentProduct, currentTenant, enabledPackageIds } = useAppContext();
  const { session } = useAuth();
  const navigate = useNavigate();
  const canRead = session?.authorization.permissions.includes("identity.user.read") ?? false;
  const capabilityEnabled = enabledPackageIds.has("package.account");
  const scope = useMemo<AccountScope | null>(() => currentProduct
    ? currentTenant
      ? { type: "tenant", productId: currentProduct.id, tenantId: currentTenant.id }
      : { type: "product", productId: currentProduct.id }
    : null, [currentProduct, currentTenant]);
  const [draft, setDraft] = useState<Filters>({ query: "", accountStatus: "all", accessStatus: "all" });
  const [filters, setFilters] = useState<Filters>(draft);
  const [cursor, setCursor] = useState<string | undefined>();
  const [history, setHistory] = useState<Array<string | undefined>>([]);
  const queryKey = scope && canRead && capabilityEnabled ? JSON.stringify({ scope, filters, cursor }) : null;
  const query = useScopedQuery(queryKey, () => accountAdminClient.listUsers(scope!, {
    query: filters.query || undefined,
    accountStatus: filters.accountStatus === "all" ? undefined : filters.accountStatus,
    accessStatus: filters.accessStatus === "all" ? undefined : filters.accessStatus,
    cursor,
    pageSize: 20,
  }), emptyPage);
  useEffect(() => { setCursor(undefined); setHistory([]); }, [scope ? JSON.stringify(scope) : null]);

  const applyFilters = (event: FormEvent) => {
    event.preventDefault();
    setFilters({ ...draft, query: draft.query.trim() });
    setCursor(undefined);
    setHistory([]);
  };
  const nextPage = () => {
    if (!query.data.nextCursor) return;
    setHistory((items) => [...items, cursor]);
    setCursor(query.data.nextCursor);
  };
  const previousPage = () => {
    const previous = history.at(-1);
    setHistory((items) => items.slice(0, -1));
    setCursor(previous);
  };

  return <Shell title="用户管理" subtitle={`${currentProduct?.name ?? "当前软件"} · ${currentTenant?.name ?? "产品范围"}`}>
    {!capabilityEnabled && <div className="query-state query-error" role="alert">未启用账号能力：当前软件未启用此能力（Account 用户管理，能力包 <code>package.account</code>），不会加载用户数据。</div>}
    {capabilityEnabled && !canRead && <div className="query-state query-error" role="alert">缺少 identity.user.read 权限，无法读取当前范围用户。</div>}
    {capabilityEnabled && canRead && <>
      <form className="toolbar filters account-user-filters" onSubmit={applyFilters}>
        <div className="search-field"><IconSearch size={18}/><input aria-label="搜索用户" maxLength={320} value={draft.query} onChange={(event) => setDraft((value) => ({ ...value, query: event.target.value }))} placeholder="用户编号、显示名称或完整邮箱/手机号" /></div>
        <div className="select-wrap filter-select"><IconFilter size={17}/><select aria-label="账号安全状态" value={draft.accountStatus} onChange={(event) => setDraft((value) => ({ ...value, accountStatus: event.target.value as Filters["accountStatus"] }))}><option value="all">全部安全状态</option><option value="active">账号正常</option><option value="locked">全局锁定</option><option value="disabled">全局禁用</option></select></div>
        <div className="select-wrap filter-select"><IconFilter size={17}/><select aria-label="当前范围准入状态" value={draft.accessStatus} onChange={(event) => setDraft((value) => ({ ...value, accessStatus: event.target.value as Filters["accessStatus"] }))}><option value="all">全部准入状态</option><option value="active">允许访问</option><option value="suspended">已停用</option></select></div>
        <button className="primary-button" type="submit" disabled={query.loading}><IconSearch size={18}/>查询</button>
      </form>
      <QueryState loading={query.loading} error={query.error} onRetry={query.retry} />
      {!query.loading && !query.error && <section className="panel table-panel account-user-table"><div className="table-heading"><div><h2>用户列表</h2><p>结果由服务端按当前软件与租户范围筛选，本页显示 {query.data.items.length} 位用户</p></div></div><div className="table-scroll"><table><thead><tr><th>用户</th><th>全局安全</th><th>当前范围准入</th><th>加入时间</th><th>最后活跃</th><th>会话</th><th><span className="sr-only">操作</span></th></tr></thead><tbody>{query.data.items.map((user) => <tr key={user.id}><td><div className="user-cell"><span>{Array.from(displayName(user))[0] ?? "用"}</span><div><strong>{displayName(user)}</strong><small>{identifierSummary(user)}</small><small><code>{user.id}</code></small></div></div></td><td><StatusBadge status={user.accountStatus}/></td><td>{user.access ? <StatusBadge status={user.access.status}/> : <span className="muted-copy">继承允许</span>}</td><td>{dateTime(user.memberSince)}</td><td>{dateTime(user.lastSeenAt)}</td><td>{user.activeSessionCount} / {user.totalSessionCount}</td><td><button className="table-link" type="button" aria-label={`查看 ${displayName(user)} 详情`} onClick={() => navigate(`/products/${encodeURIComponent(currentProduct!.id)}/users/${encodeURIComponent(user.id)}`)}>查看详情<IconChevronRight size={16}/></button></td></tr>)}</tbody></table></div>
        {query.data.items.length === 0 && <div className="empty-state"><IconUser size={28}/><strong>当前范围没有符合条件的用户</strong><span>可调整服务端搜索词或状态筛选后重试</span></div>}
        {(history.length > 0 || query.data.nextCursor) && <footer className="table-pagination"><button className="secondary-button" type="button" disabled={history.length === 0 || query.loading} onClick={previousPage}><IconChevronLeft size={17}/>上一页</button><span>第 {history.length + 1} 页</span><button className="secondary-button" type="button" disabled={!query.data.nextCursor || query.loading} onClick={nextPage}>下一页<IconChevronRight size={17}/></button></footer>}
      </section>}
    </>}
  </Shell>;
}
