import { IconCirclePlus, IconHistory, IconRefresh, IconShieldX } from "@tabler/icons-react";
import { FormEvent, useMemo, useState } from "react";
import {
  createEntitlementIntentKey,
  entitlementAdminClient,
  entitlementErrorMessage,
  entitlementHasVersionConflict,
  type EntitlementLedgerEntry,
  type EntitlementSummary,
  type EntitlementValidityInput,
} from "../api/entitlementAdminClient";
import { useAppContext } from "../app/AppContext";
import { useAuth } from "../app/AuthContext";
import { useScopedQuery } from "../app/useScopedQuery";
import { Modal } from "../components/Modal";
import { QueryState } from "../components/QueryState";
import { Shell } from "../components/Shell";

type MutationKind = "grant" | "extend" | "revoke";

const sourceTypes = ["admin", "trial", "gift", "order", "license"] as const;
const dateTime = (value: string | null) => value ? new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : "长期有效";
const featuresText = (features: Record<string, unknown>) => Object.keys(features).length ? Object.entries(features).map(([key, value]) => `${key}:${String(value)}`).join("，") : "无有效功能";

function hasPermission(permissions: string[], permission: string) { return permissions.includes(permission); }

export function EntitlementsPage() {
  const { currentProduct, currentTenant, enabledPackageIds } = useAppContext();
  const { session } = useAuth();
  const permissions = session?.authorization.permissions ?? [];
  const capabilityEnabled = enabledPackageIds.has("package.entitlement");
  const canRead = hasPermission(permissions, "entitlement.read");
  const canManage = hasPermission(permissions, "entitlement.manage");
  const canRevoke = hasPermission(permissions, "entitlement.revoke");
  const scope = currentProduct && currentTenant ? { productId: currentProduct.id, tenantId: currentTenant.id } : null;
  const [filterUserId, setFilterUserId] = useState("");
  const [appliedUserId, setAppliedUserId] = useState("");
  const [selected, setSelected] = useState<EntitlementSummary | null>(null);
  const [selectedGrantId, setSelectedGrantId] = useState("");
  const [mutation, setMutation] = useState<MutationKind | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [result, setResult] = useState<{ message: string; auditId: string; grantId: string; revision: number } | null>(null);
  const [policyId, setPolicyId] = useState("policy-pro");
  const [policyVersion, setPolicyVersion] = useState(1);
  const [validityRule, setValidityRule] = useState<EntitlementValidityInput["rule"]>("fixed_duration");
  const [durationSeconds, setDurationSeconds] = useState(2592000);
  const [fixedUntil, setFixedUntil] = useState("");
  const [sourceType, setSourceType] = useState<(typeof sourceTypes)[number]>("admin");
  const [sourceId, setSourceId] = useState("manual-grant");
  const [sourceEffectId, setSourceEffectId] = useState("effect-1");
  const [reasonCode, setReasonCode] = useState("manual_grant");
  const [revokeConfirm, setRevokeConfirm] = useState(false);

  const queryKey = scope && capabilityEnabled && canRead ? JSON.stringify({ scope, appliedUserId }) : null;
  const listQuery = useScopedQuery(queryKey, () => entitlementAdminClient.listCurrent(scope!, { userId: appliedUserId || undefined, pageSize: 50 }), { items: [], nextCursor: null });
  const historyKey = scope && selected ? JSON.stringify({ scope, userId: selected.userId, revision: selected.revision }) : null;
  const historyQuery = useScopedQuery(historyKey, () => entitlementAdminClient.listHistory(scope!, selected!.userId, { pageSize: 50 }), { items: [], nextCursor: null });
  const latestGrant = useMemo(() => historyQuery.data.items.find((item) => item.operationType !== "revoke") ?? null, [historyQuery.data.items]);

  if (!capabilityEnabled || !canRead) {
    return <Shell title="权益管理" subtitle={currentProduct?.name ?? "当前软件"}>
      <div className="query-state query-error" role="alert">
        <strong>{!capabilityEnabled ? "未启用权益能力" : "缺少权益读取权限"}</strong>
        <span>{!capabilityEnabled ? "当前软件没有启用 package.entitlement，旧书签不会渲染业务数据。" : "需要 entitlement.read 权限才能查询当前软件权益。"}</span>
      </div>
    </Shell>;
  }

  const applyFilters = (event: FormEvent) => {
    event.preventDefault();
    setAppliedUserId(filterUserId.trim());
    setSelected(null);
  };
  const validity = (): EntitlementValidityInput => {
    if (validityRule === "fixed_duration") return { rule: "fixed_duration", durationSeconds };
    if (validityRule === "fixed_end") return { rule: "fixed_end", fixedUntil: fixedUntil ? new Date(fixedUntil).toISOString() : undefined };
    return { rule: "lifetime" };
  };
  const openMutation = (kind: MutationKind, item?: EntitlementSummary) => {
    setMutation(kind);
    setSelected(item ?? selected);
    setSelectedGrantId(item ? "" : selectedGrantId);
    setReasonCode(kind === "grant" ? "manual_grant" : kind === "extend" ? "manual_extend" : "manual_revoke");
    setSubmitError(null);
    setRevokeConfirm(false);
  };
  const submitMutation = async (event: FormEvent) => {
    event.preventDefault();
    if (!scope || !mutation || submitting) return;
    const target = mutation === "grant" ? null : selected;
    if (mutation !== "grant" && !target) return;
    const grantId = selectedGrantId || latestGrant?.grantId || "";
    if (mutation !== "grant" && !grantId) { setSubmitError("请先从流水中选择或输入 grant_id"); return; }
    if (mutation === "revoke" && !revokeConfirm) { setSubmitError("撤销是高风险操作，请先勾选二次确认"); return; }
    setSubmitting(true);
    setSubmitError(null);
    try {
      const source = { sourceType, sourceId, sourceEffectId };
      const response = mutation === "grant"
        ? await entitlementAdminClient.grant(scope, { userId: filterUserId.trim(), policyId, policyVersion, validity: validity(), source, reasonCode }, { idempotencyKey: createEntitlementIntentKey() })
        : mutation === "extend"
          ? await entitlementAdminClient.extend(scope, grantId, { userId: target!.userId, expectedRevision: target!.revision, policyId, policyVersion, validity: validity(), source, reasonCode }, { idempotencyKey: createEntitlementIntentKey() })
          : await entitlementAdminClient.revoke(scope, grantId, { userId: target!.userId, expectedRevision: target!.revision, reasonCode }, { idempotencyKey: createEntitlementIntentKey() });
      setResult({ message: mutation === "grant" ? "权益授予成功" : mutation === "extend" ? "权益延长成功" : "权益撤销成功", auditId: response.auditId, grantId: response.grantId, revision: response.revision });
      setMutation(null);
      listQuery.retry();
      historyQuery.retry();
    } catch (reason) {
      setSubmitError(entitlementErrorMessage(reason, "权益操作失败，请重试"));
      if (entitlementHasVersionConflict(reason)) {
        listQuery.retry();
        historyQuery.retry();
      }
    } finally {
      setSubmitting(false);
    }
  };

  return <Shell title="权益管理" subtitle={`${currentProduct?.name ?? "当前软件"} · ${currentTenant?.name ?? "当前租户"}`}>
    <section className="toolbar">
      <div><span className="eyebrow">package.entitlement</span><strong>查询、授予、延长、撤销权益并查看审计流水</strong></div>
      <button className="primary-button" type="button" disabled={!canManage} onClick={() => openMutation("grant")}><IconCirclePlus size={18}/>授予权益</button>
    </section>
    <form className="filters account-user-filters" onSubmit={applyFilters}>
      <label className="filter-select">用户 ID<input aria-label="筛选用户 ID" value={filterUserId} onChange={(event) => setFilterUserId(event.target.value)} placeholder="user_id，可留空"/></label>
      <button className="secondary-button" type="submit">查询</button>
      <button className="secondary-button" type="button" onClick={() => listQuery.retry()}><IconRefresh size={16}/>刷新</button>
    </form>
    {result && <div className="feedback feedback-success account-action-result" role="status"><span><strong>{result.message}</strong><small>grant <code>{result.grantId}</code> · revision v{result.revision} · 审计 <code>{result.auditId}</code></small></span></div>}
    <QueryState loading={listQuery.loading} error={listQuery.error} onRetry={listQuery.retry} />
    {!listQuery.loading && !listQuery.error && <section className="panel table-panel"><div className="table-heading"><div><h2>当前权益</h2><p>来源于服务端 entitlement.revisions，按当前产品和租户授权范围读取</p></div></div><div className="table-scroll"><table><thead><tr><th>用户</th><th>计划</th><th>Revision</th><th>功能</th><th>有效期</th><th>更新时间</th><th>操作</th></tr></thead><tbody>{listQuery.data.items.map((item) => <tr key={item.userId}><td><code>{item.userId}</code></td><td>{item.planCode ?? "未命名计划"}</td><td>v{item.revision}</td><td>{featuresText(item.features)}</td><td>{dateTime(item.validUntil)}</td><td>{dateTime(item.updatedAt)}</td><td><div className="account-inline-actions"><button className="secondary-button" type="button" onClick={() => { setSelected(item); setSelectedGrantId(""); }}><IconHistory size={15}/>流水</button><button className="secondary-button" type="button" disabled={!canManage} onClick={() => openMutation("extend", item)}>延长</button><button className="secondary-button danger-button" type="button" disabled={!canRevoke} onClick={() => openMutation("revoke", item)}><IconShieldX size={15}/>撤销</button></div></td></tr>)}</tbody></table></div>{listQuery.data.items.length === 0 && <div className="empty-state"><strong>当前范围没有权益记录</strong></div>}</section>}
    {selected && <section className="panel table-panel"><div className="table-heading"><div><h2>权益流水</h2><p>用户 <code>{selected.userId}</code> · 用流水中的 grant_id 执行延长或撤销</p></div></div><QueryState loading={historyQuery.loading} error={historyQuery.error} onRetry={historyQuery.retry}/>{!historyQuery.loading && !historyQuery.error && <div className="table-scroll"><table><thead><tr><th>操作</th><th>grant_id</th><th>来源</th><th>Revision</th><th>审计</th><th>时间</th><th>选择</th></tr></thead><tbody>{historyQuery.data.items.map((item: EntitlementLedgerEntry) => <tr key={item.ledgerId}><td>{item.operationType}</td><td><code>{item.grantId}</code></td><td>{item.sourceType ?? "无"} / {item.sourceId ?? "无"}</td><td>{item.beforeRevision} → {item.afterRevision}</td><td><code>{item.auditId}</code></td><td>{dateTime(item.createdAt)}</td><td><button className="secondary-button" type="button" onClick={() => setSelectedGrantId(item.grantId)}>选择该 Grant</button></td></tr>)}</tbody></table></div>}</section>}
    <Modal open={Boolean(mutation)} onClose={() => !submitting && setMutation(null)} title={mutation === "grant" ? "授予权益" : mutation === "extend" ? "延长权益" : "撤销权益"}>
      <form className="form" onSubmit={submitMutation}>
        <label>用户 ID<input required disabled={mutation !== "grant"} value={mutation === "grant" ? filterUserId : selected?.userId ?? ""} onChange={(event) => setFilterUserId(event.target.value)} /></label>
        {mutation !== "grant" && <label>目标 grant_id<input required value={selectedGrantId || latestGrant?.grantId || ""} onChange={(event) => setSelectedGrantId(event.target.value)} /></label>}
        {mutation !== "revoke" && <><label>Policy ID<input required value={policyId} onChange={(event) => setPolicyId(event.target.value)} /></label><label>Policy Version<input required type="number" min={1} value={policyVersion} onChange={(event) => setPolicyVersion(Number(event.target.value))} /></label><label>有效期规则<select value={validityRule} onChange={(event) => setValidityRule(event.target.value as EntitlementValidityInput["rule"])}><option value="fixed_duration">固定时长</option><option value="fixed_end">固定结束时间</option><option value="lifetime">长期有效</option></select></label>{validityRule === "fixed_duration" && <label>时长秒数<input type="number" min={1} value={durationSeconds} onChange={(event) => setDurationSeconds(Number(event.target.value))} /></label>}{validityRule === "fixed_end" && <label>结束时间<input type="datetime-local" value={fixedUntil} onChange={(event) => setFixedUntil(event.target.value)} /></label>}</>}
        <label>来源类型<select value={sourceType} onChange={(event) => setSourceType(event.target.value as typeof sourceType)}>{sourceTypes.map((item) => <option key={item} value={item}>{item}</option>)}</select></label>
        {mutation !== "revoke" && <><label>Source ID<input required value={sourceId} onChange={(event) => setSourceId(event.target.value)} /></label><label>Source Effect ID<input required value={sourceEffectId} onChange={(event) => setSourceEffectId(event.target.value)} /></label></>}
        <label>原因码<input required value={reasonCode} onChange={(event) => setReasonCode(event.target.value)} /></label>
        {mutation === "revoke" && <label className="checkbox-line"><input type="checkbox" checked={revokeConfirm} onChange={(event) => setRevokeConfirm(event.target.checked)} />我确认撤销会改变用户当前权益，并已核对产品、租户、用户和 grant_id</label>}
        {submitError && <p className="form-error" role="alert">{submitError}</p>}
        <footer><button className="secondary-button" type="button" disabled={submitting} onClick={() => setMutation(null)}>取消</button><button className={mutation === "revoke" ? "primary-button danger-primary" : "primary-button"} type="submit" disabled={submitting || (mutation === "grant" && !filterUserId.trim())}>{submitting ? "提交中..." : "确认提交"}</button></footer>
      </form>
    </Modal>
  </Shell>;
}
