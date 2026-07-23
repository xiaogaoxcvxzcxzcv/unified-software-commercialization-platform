import { IconAlertTriangle, IconArrowLeft, IconChevronLeft, IconChevronRight, IconExternalLink, IconRefresh, IconShield, IconTrash, IconUser } from "@tabler/icons-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { accountAdminClient, accountErrorMessage, accountHasVersionConflict, accountRequiresReauthentication, createAccountIntentKey, type AccessStatus, type AccountScope, type AccountStatus, type AdminUserDetail, type AdminUserSession } from "../api/accountAdminClient";
import { useAppContext } from "../app/AppContext";
import { useAuth } from "../app/AuthContext";
import { useScopedQuery } from "../app/useScopedQuery";
import { Modal } from "../components/Modal";
import { QueryState } from "../components/QueryState";
import { Shell } from "../components/Shell";
import { StatusBadge } from "../components/StatusBadge";

type PendingAction =
  | { kind: "access"; scope: Exclude<AccountScope, { type: "platform" }>; label: string; status: AccessStatus; version: number }
  | { kind: "security"; scope: { type: "platform" }; label: string; status: AccountStatus; version: number }
  | { kind: "sessions"; scope: AccountScope; label: string; sessionIds?: string[] };

const emptySessions = { items: [] as AdminUserSession[], nextCursor: null as string | null };
function dateTime(value: string | null) { return value ? new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : "暂无记录"; }
function scopeKey(scope: AccountScope | null) { return scope ? JSON.stringify(scope) : null; }
function hasScope(scope: AccountScope, scopes: Array<{ scope_type: string; scope_id?: string | null; product_id?: string | null; tenant_id?: string | null }>) {
  if (scopes.some((item) => item.scope_type === "platform")) return true;
  if (scope.type === "platform") return false;
  if (scope.type === "product") return scopes.some((item) => item.scope_type === "product" && (item.product_id === scope.productId || item.scope_id === scope.productId));
  return scopes.some((item) => item.scope_type === "tenant" && item.product_id === scope.productId && (item.tenant_id === scope.tenantId || item.scope_id === scope.tenantId));
}

export function UserDetailPage() {
  const { userId } = useParams();
  const navigate = useNavigate();
  const { currentProduct, currentTenant, enabledPackageIds } = useAppContext();
  const { session, logout } = useAuth();
  const permissions = new Set(session?.authorization.permissions ?? []);
  const adminScopes = session?.authorization.scopes ?? [];
  const canRead = permissions.has("identity.user.read");
  const capabilityEnabled = enabledPackageIds.has("package.account");
  const platformScope = adminScopes.some((item) => item.scope_type === "platform") && permissions.has("identity.security.manage");
  const productScope = currentProduct ? { type: "product" as const, productId: currentProduct.id } : null;
  const tenantScope = currentProduct && currentTenant ? { type: "tenant" as const, productId: currentProduct.id, tenantId: currentTenant.id } : null;
  const viewScope = useMemo<AccountScope | null>(() => tenantScope && hasScope(tenantScope, adminScopes) ? tenantScope : productScope && hasScope(productScope, adminScopes) ? productScope : null, [currentProduct?.id, currentTenant?.id, session?.authorization.authorization_version]);
  const canManageCurrent = Boolean(viewScope && viewScope.type !== "platform" && permissions.has("product.user-access.manage") && hasScope(viewScope, adminScopes));
  const canReadProduct = Boolean(productScope && canRead && hasScope(productScope, adminScopes));
  const canManageProduct = Boolean(productScope && permissions.has("product.user-access.manage") && hasScope(productScope, adminScopes));
  const canManageGlobal = platformScope && permissions.has("identity.security.manage");

  const detailQuery = useScopedQuery<AdminUserDetail | null>(capabilityEnabled && canRead && userId && viewScope ? `${scopeKey(viewScope)}:${userId}` : null, () => accountAdminClient.getUser(viewScope!, userId!), null);
  const productQuery = useScopedQuery<AdminUserDetail | null>(capabilityEnabled && canReadProduct && userId && productScope && viewScope?.type === "tenant" ? `${scopeKey(productScope)}:${userId}` : null, () => accountAdminClient.getUser(productScope!, userId!), null);
  const globalQuery = useScopedQuery<AdminUserDetail | null>(capabilityEnabled && platformScope && canRead && userId ? `platform:${userId}` : null, () => accountAdminClient.getUser({ type: "platform" }, userId!), null);
  const [sessionCursor, setSessionCursor] = useState<string | undefined>();
  const [sessionHistory, setSessionHistory] = useState<Array<string | undefined>>([]);
  const sessionQuery = useScopedQuery(capabilityEnabled && canRead && userId && viewScope ? `${scopeKey(viewScope)}:${userId}:sessions:${sessionCursor ?? "first"}` : null, () => accountAdminClient.listSessions(viewScope!, userId!, sessionCursor), emptySessions);
  const [selectedSessions, setSelectedSessions] = useState<string[]>([]);
  const [pendingAction, setPendingAction] = useState<PendingAction | null>(null);
  const [intentKey, setIntentKey] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [reauthenticationRequired, setReauthenticationRequired] = useState(false);
  const [preparingLogin, setPreparingLogin] = useState(false);
  const [result, setResult] = useState<{ message: string; auditId: string; ready: boolean; pending: boolean } | null>(null);
  const pollGeneration = useRef(0);
  useEffect(() => () => { pollGeneration.current += 1; }, []);

  const detail = detailQuery.data;
  const openAction = (action: PendingAction) => { setPendingAction(action); setIntentKey(createAccountIntentKey()); setActionError(null); setReauthenticationRequired(false); };
  const closeAction = () => { if (!submitting) { setPendingAction(null); setActionError(null); setReauthenticationRequired(false); } };
  const refreshAll = () => { detailQuery.retry(); productQuery.retry(); globalQuery.retry(); sessionQuery.retry(); setSelectedSessions([]); };
  const pollAudit = async (auditId: string) => {
    const generation = ++pollGeneration.current;
    setResult((value) => value && value.auditId === auditId ? { ...value, pending: true } : value);
    for (let attempt = 0; attempt < 4; attempt += 1) {
      try { await accountAdminClient.getAuditEvent(auditId); if (generation !== pollGeneration.current) return; setResult((value) => value && value.auditId === auditId ? { ...value, ready: true, pending: false } : value); return; }
      catch (reason) {
        if (!(reason instanceof Error) || !("status" in reason) || (reason as { status?: number }).status !== 404) break;
        if (attempt < 3) await new Promise((resolve) => globalThis.setTimeout(resolve, 350 * (attempt + 1))); if (generation !== pollGeneration.current) return;
      }
    }
    if (generation !== pollGeneration.current) return;
    setResult((value) => value && value.auditId === auditId ? { ...value, ready: false, pending: false } : value);
  };
  const submitAction = async () => {
    if (!pendingAction || !userId || submitting) return;
    setSubmitting(true); setActionError(null); setReauthenticationRequired(false);
    try {
      if (pendingAction.kind === "access") {
        const mutation = await accountAdminClient.setAccess(pendingAction.scope, userId, pendingAction.version, pendingAction.status, "operator_request", { idempotencyKey: intentKey });
        setResult({ message: `${pendingAction.label}已更新为${pendingAction.status === "active" ? "允许访问" : "已停用"}`, auditId: mutation.auditId, ready: false, pending: true });
        void pollAudit(mutation.auditId);
      } else if (pendingAction.kind === "security") {
        const mutation = await accountAdminClient.setGlobalSecurity(userId, pendingAction.version, pendingAction.status, "security_response", { idempotencyKey: intentKey });
        setResult({ message: `全局账号安全状态已更新为${pendingAction.status === "active" ? "正常" : pendingAction.status === "locked" ? "锁定" : "禁用"}`, auditId: mutation.auditId, ready: false, pending: true });
        void pollAudit(mutation.auditId);
      } else {
        const mutation = await accountAdminClient.revokeSessions(pendingAction.scope, userId, pendingAction.sessionIds ? { sessionIds: pendingAction.sessionIds } : { allActive: true }, "security_response", { idempotencyKey: intentKey });
        setResult({ message: `已撤销 ${mutation.revokedCount} 个会话`, auditId: mutation.auditId, ready: false, pending: true });
        void pollAudit(mutation.auditId);
      }
      setPendingAction(null); refreshAll();
    } catch (reason) {
      setReauthenticationRequired(accountRequiresReauthentication(reason));
      setActionError(accountErrorMessage(reason, "操作失败，请重试"));
      if (accountHasVersionConflict(reason)) refreshAll();
    } finally { setSubmitting(false); }
  };
  const beginReauthentication = async () => {
    if (!currentProduct || !userId || preparingLogin) return;
    setPreparingLogin(true);
    try { await logout(); navigate("/login", { replace: true, state: { from: `/products/${encodeURIComponent(currentProduct.id)}/users/${encodeURIComponent(userId)}` } }); }
    catch (reason) { setActionError(accountErrorMessage(reason, "暂时无法重新登录，请稍后重试")); setPreparingLogin(false); }
  };
  const goAudit = () => { if (result && currentProduct) navigate(`/products/${encodeURIComponent(currentProduct.id)}/audit?audit_id=${encodeURIComponent(result.auditId)}`); };

  if (!currentProduct) return null;
  return <Shell title="用户详情" subtitle={`${currentProduct.name} · ${viewScope?.type === "tenant" ? currentTenant?.name : "产品范围"}`}>
    <div className="toolbar"><button className="secondary-button" type="button" onClick={() => navigate(`/products/${encodeURIComponent(currentProduct.id)}/users`)}><IconArrowLeft size={17}/>返回用户列表</button><div className="assembly-toolbar-spacer"/><button className="secondary-button" type="button" disabled={!canRead || detailQuery.loading} onClick={refreshAll}><IconRefresh size={17}/>刷新</button></div>
    {!capabilityEnabled && <div className="query-state query-error" role="alert">当前软件未启用此能力（Account 用户管理），不会加载用户详情。</div>}
    {capabilityEnabled && !canRead && <div className="query-state query-error" role="alert">缺少 identity.user.read 权限，无法读取用户详情。</div>}
    {capabilityEnabled && canRead && !viewScope && <div className="query-state query-error" role="alert">当前管理员没有匹配此软件或租户的读取范围。</div>}
    <QueryState loading={detailQuery.loading} error={detailQuery.error} onRetry={detailQuery.retry}/>
    {result && <div className="feedback feedback-success account-action-result" role="status"><span><strong>{result.message}</strong><small>审计编号 <code>{result.auditId}</code>{result.pending ? " · 正在等待审计投影" : !result.ready ? " · 审计投影暂未出现" : ""}</small></span><button className="secondary-button" type="button" disabled={result.pending} onClick={result.ready ? goAudit : () => void pollAudit(result.auditId)}>{result.ready ? <><IconExternalLink size={16}/>查看审计记录</> : "重试读取审计"}</button></div>}
    {detail && <div className="account-detail-layout">
      <section className="panel account-user-summary"><header className="panel-title"><div><h2>{detail.user.displayName || "未设置显示名称"}</h2><p><code>{detail.user.id}</code></p></div><StatusBadge status={detail.user.accountStatus}/></header><dl className="detail-list"><div><dt>脱敏登录标识</dt><dd>{detail.user.identifiers.length ? detail.user.identifiers.map((item) => <span className="account-identifier" key={`${item.type}:${item.maskedValue}`}>{item.maskedValue} · {item.verified ? "已验证" : "未验证"}</span>) : "未公开"}</dd></div><div><dt>当前范围准入</dt><dd>{detail.user.access ? <><StatusBadge status={detail.user.access.status}/> <small>v{detail.user.access.version}{detail.user.access.explicit ? " · 显式状态" : " · 继承状态"}</small></> : "全局视图不包含准入状态"}</dd></div><div><dt>加入当前范围</dt><dd>{dateTime(detail.user.memberSince)}</dd></div><div><dt>最后活跃</dt><dd>{dateTime(detail.user.lastSeenAt)}</dd></div><div><dt>活跃 / 全部会话</dt><dd>{detail.user.activeSessionCount} / {detail.user.totalSessionCount}</dd></div></dl></section>
      <section className="panel account-profile-summary"><header className="panel-title"><div><h2>资料摘要</h2><p>仅显示 Identity 返回的脱敏公开字段</p></div></header><dl className="detail-list"><div><dt>显示名称</dt><dd>{detail.profile.displayName || "未设置"}</dd></div><div><dt>语言</dt><dd>{detail.profile.locale || "未设置"}</dd></div><div><dt>时区</dt><dd>{detail.profile.timezone || "未设置"}</dd></div><div><dt>资料版本</dt><dd>v{detail.profile.version}</dd></div></dl></section>
      <section className="panel account-scope-actions"><header className="panel-title"><div><h2>范围准入</h2><p>停用只影响标明的软件或租户，不改变全局账号和权益</p></div></header><div className="account-action-list">
        {viewScope && viewScope.type !== "platform" && detail.user.access && <div><span><strong>{viewScope.type === "tenant" ? `租户：${currentTenant?.name}` : `软件：${currentProduct.name}`}</strong><small>当前状态 <StatusBadge status={detail.user.access.status}/></small></span><button className={detail.user.access.status === "active" ? "secondary-button danger-button" : "secondary-button"} type="button" disabled={!canManageCurrent} title={canManageCurrent ? "变更当前范围准入" : "缺少当前范围 product.user-access.manage 权限"} onClick={() => openAction({ kind: "access", scope: viewScope, label: viewScope.type === "tenant" ? `租户 ${currentTenant?.name}` : `软件 ${currentProduct.name}`, status: detail.user.access!.status === "active" ? "suspended" : "active", version: detail.user.access!.version })}>{detail.user.access.status === "active" ? "停用" : "恢复"}</button></div>}
        {viewScope?.type === "tenant" && productQuery.data?.user.access && <div><span><strong>软件：{currentProduct.name}</strong><small>产品级状态 <StatusBadge status={productQuery.data.user.access.status}/></small></span><button className={productQuery.data.user.access.status === "active" ? "secondary-button danger-button" : "secondary-button"} type="button" disabled={!canManageProduct} title={canManageProduct ? "变更产品准入" : "缺少产品范围 product.user-access.manage 权限"} onClick={() => openAction({ kind: "access", scope: productScope!, label: `软件 ${currentProduct.name}`, status: productQuery.data!.user.access!.status === "active" ? "suspended" : "active", version: productQuery.data!.user.access!.version })}>{productQuery.data.user.access.status === "active" ? "停用" : "恢复"}</button></div>}
      </div></section>
      {platformScope && <section className="panel account-global-actions"><header className="panel-title"><div><h2>全局安全操作</h2><p>影响该用户在所有软件和租户中的登录，只有平台安全管理员可执行</p></div><IconShield size={20}/></header>{globalQuery.data ? <div className="account-action-list"><div><span><strong>全局账号状态</strong><small><StatusBadge status={globalQuery.data.user.accountStatus}/> · 用户版本 v{globalQuery.data.user.version}</small></span><div className="account-inline-actions">{globalQuery.data.user.accountStatus !== "active" && <button className="secondary-button" type="button" disabled={!canManageGlobal} onClick={() => openAction({ kind: "security", scope: { type: "platform" }, label: "所有软件和租户", status: "active", version: globalQuery.data!.user.version })}>恢复账号</button>}{globalQuery.data.user.accountStatus !== "locked" && <button className="secondary-button danger-button" type="button" disabled={!canManageGlobal} onClick={() => openAction({ kind: "security", scope: { type: "platform" }, label: "所有软件和租户", status: "locked", version: globalQuery.data!.user.version })}>全局锁定</button>}{globalQuery.data.user.accountStatus !== "disabled" && <button className="secondary-button danger-button" type="button" disabled={!canManageGlobal} onClick={() => openAction({ kind: "security", scope: { type: "platform" }, label: "所有软件和租户", status: "disabled", version: globalQuery.data!.user.version })}>全局禁用</button>}</div></div><div><span><strong>全部范围会话</strong><small>撤销该用户在所有软件、应用和租户中的活跃会话</small></span><button className="secondary-button danger-button" type="button" disabled={!canManageGlobal} onClick={() => openAction({ kind: "sessions", scope: { type: "platform" }, label: "所有软件和租户" })}>全部撤销</button></div></div> : <QueryState loading={globalQuery.loading} error={globalQuery.error} onRetry={globalQuery.retry}/>}</section>}
      <section className="panel account-session-panel"><header className="panel-title"><div><h2>当前范围会话</h2><p>仅列出当前{viewScope?.type === "tenant" ? "租户" : "软件"}的脱敏会话摘要</p></div><div className="account-inline-actions"><button className="secondary-button danger-button" type="button" disabled={!canManageCurrent || selectedSessions.length === 0} onClick={() => openAction({ kind: "sessions", scope: viewScope!, label: `${viewScope?.type === "tenant" ? `租户 ${currentTenant?.name}` : `软件 ${currentProduct.name}`}中的 ${selectedSessions.length} 个会话`, sessionIds: selectedSessions })}><IconTrash size={16}/>撤销所选</button><button className="secondary-button danger-button" type="button" disabled={!canManageCurrent || sessionQuery.data.items.every((item) => item.revokedAt)} onClick={() => openAction({ kind: "sessions", scope: viewScope!, label: `${viewScope?.type === "tenant" ? `租户 ${currentTenant?.name}` : `软件 ${currentProduct.name}`}中的全部活跃会话` })}>撤销全部</button></div></header><QueryState loading={sessionQuery.loading} error={sessionQuery.error} onRetry={sessionQuery.retry}/>{!sessionQuery.loading && !sessionQuery.error && <><div className="table-scroll"><table><thead><tr><th><span className="sr-only">选择</span></th><th>设备与认证</th><th>应用 / 环境</th><th>最后活跃</th><th>到期</th><th>状态</th></tr></thead><tbody>{sessionQuery.data.items.map((item) => <tr key={item.id}><td><input aria-label={`选择会话 ${item.id}`} type="checkbox" disabled={Boolean(item.revokedAt) || !canManageCurrent} checked={selectedSessions.includes(item.id)} onChange={(event) => setSelectedSessions((items) => event.target.checked ? [...items, item.id] : items.filter((id) => id !== item.id))}/></td><td><strong>{item.deviceLabel || "未知设备"}</strong><small>{item.authenticationMethod} · <code>{item.id}</code></small></td><td><strong>{item.applicationId}</strong><small>{item.environment}</small></td><td>{dateTime(item.lastSeenAt)}</td><td>{dateTime(item.expiresAt)}</td><td><StatusBadge status={item.revokedAt ? "revoked" : "active"}/></td></tr>)}</tbody></table></div>{sessionQuery.data.items.length === 0 && <div className="empty-state"><IconUser size={26}/><strong>当前范围暂无会话</strong></div>}{(sessionHistory.length > 0 || sessionQuery.data.nextCursor) && <footer className="table-pagination"><button className="secondary-button" type="button" disabled={!sessionHistory.length} onClick={() => { const previous = sessionHistory[sessionHistory.length - 1]; setSessionHistory((items) => items.slice(0, -1)); setSessionCursor(previous); setSelectedSessions([]); }}><IconChevronLeft size={17}/>上一页</button><span>第 {sessionHistory.length + 1} 页</span><button className="secondary-button" type="button" disabled={!sessionQuery.data.nextCursor} onClick={() => { setSessionHistory((items) => [...items, sessionCursor]); setSessionCursor(sessionQuery.data.nextCursor!); setSelectedSessions([]); }}>下一页<IconChevronRight size={17}/></button></footer>}</>}</section>
    </div>}
    <Modal title="确认危险操作" open={Boolean(pendingAction)} onClose={closeAction}><div className="modal-body account-confirmation"><div className="lifecycle-risk"><IconAlertTriangle size={20}/><div><strong>{pendingAction?.kind === "security" ? "全局账号安全变更" : pendingAction?.kind === "sessions" ? "会话撤销" : "范围准入变更"}</strong><p>目标用户：{detail?.user.displayName || userId}（{userId}）</p><p>影响范围：{pendingAction?.label}</p>{pendingAction?.kind === "security" && <p>该操作会影响所有软件和租户，不等同于产品级停用。</p>}</div></div>{actionError && <div className="form-error" role="alert">{actionError}</div>}{reauthenticationRequired && <button className="secondary-button" type="button" disabled={preparingLogin} onClick={() => void beginReauthentication()}>{preparingLogin ? "正在退出当前会话..." : "重新登录并返回此页"}</button>}</div><div className="modal-actions"><button className="secondary-button" type="button" disabled={submitting} onClick={closeAction}>取消</button><button className="primary-button danger-primary" type="button" disabled={submitting || reauthenticationRequired} onClick={() => void submitAction()}>{submitting ? "正在提交..." : "确认执行"}</button></div></Modal>
  </Shell>;
}
