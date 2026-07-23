import { ClientButton, ClientNotice } from "../components.js";
import type { EntitlementBlockCommonProps, EntitlementSummaryActions, EntitlementSummaryValue } from "./types.js";

export interface EntitlementSummaryBlockProps extends EntitlementBlockCommonProps {
  readonly value?: EntitlementSummaryValue;
  readonly actions?: EntitlementSummaryActions;
}

export function EntitlementSummaryBlock(props: EntitlementSummaryBlockProps) {
  const className = ["entitlement-block", "entitlement-block-summary", props.className].filter(Boolean).join(" ");
  const retry = props.actions?.retry ?? props.onRetry;
  return <section className={className} aria-labelledby="entitlement-summary-title" data-state={props.state}>
    <header className="entitlement-block-header">
      <div>
        <p className="entitlement-kicker">当前会员与权益</p>
        <h2 id="entitlement-summary-title">权益摘要</h2>
        <p>只展示服务端当前权益结论；价格、套餐宣传和支付状态由后续购买能力决定。</p>
      </div>
      {props.value && <span className="entitlement-revision">Revision {props.value.revision}</span>}
    </header>
    {content(props, retry)}
  </section>;
}

function content(props: EntitlementSummaryBlockProps, retry?: () => void) {
  if (props.state === "loading") return <div className="entitlement-state" aria-busy="true"><span className="client-spinner" aria-hidden="true"/><span>正在读取服务端权益...</span></div>;
  if (props.state === "disabled") return <div className="entitlement-state"><span>{props.disabledMessage ?? "当前产品未启用权益能力。"}</span></div>;
  if (props.state === "failed") {
    return <ClientNotice tone="danger"><span>{props.error?.message ?? "权益读取失败，请重试。"}</span>{props.error?.retryable && retry && <ClientButton type="button" onClick={retry}>重试</ClientButton>}</ClientNotice>;
  }
  if (props.state === "empty" || !props.value || props.value.features.length === 0) {
    const expired = props.value?.emptyReason === "expired";
    return <div className="entitlement-empty">
      <strong>{props.emptyMessage ?? (expired ? "权益已到期" : "当前没有可用权益")}</strong>
      <p>{expired ? "服务端记录显示你曾经拥有权益，但当前已按服务端时间到期。" : "服务端当前结论没有可用会员或功能权益。"}</p>
      <EntitlementActions actions={props.actions} retry={retry} />
    </div>;
  }
  return <>
    {props.state === "submitting" && <div className="entitlement-submitting" role="status"><span className="client-spinner" aria-hidden="true"/><span>正在刷新权益...</span></div>}
    {props.state === "success" && <ClientNotice tone="success">{props.successMessage ?? "权益已刷新。"}</ClientNotice>}
    <div className="entitlement-summary-grid" aria-busy={props.state === "submitting"}>
      <div className="entitlement-plan-card">
        <span>当前方案</span>
        <strong>{props.value.planCode ?? "未绑定方案"}</strong>
        <small>{props.value.validUntil ? `有效至 ${formatTime(props.value.validUntil)}` : "长期有效或按功能策略生效"}</small>
      </div>
      <dl className="entitlement-meta">
        <div><dt>服务端更新时间</dt><dd>{formatTime(props.value.updatedAt)}</dd></div>
        <div><dt>离线宽限</dt><dd>{props.value.offlineGraceUntil ? formatTime(props.value.offlineGraceUntil) : "无"}</dd></div>
      </dl>
    </div>
    <ul className="entitlement-feature-list" aria-label="已启用权益功能">
      {props.value.features.map((feature) => <li key={feature.code}>
        <div><strong>{feature.label ?? feature.code}</strong><small>{feature.kind ?? "feature"} · {feature.code}</small></div>
        {feature.value !== undefined && feature.value !== null && <span>{String(feature.value)}</span>}
      </li>)}
    </ul>
    <EntitlementActions actions={props.actions} retry={retry} />
  </>;
}

function EntitlementActions({ actions, retry }: { readonly actions?: EntitlementSummaryActions; readonly retry?: () => void }) {
  if (!actions?.renew && !actions?.upgrade && !retry) return null;
  return <div className="entitlement-actions">
    {actions?.renew && <ClientButton type="button" onClick={actions.renew}>续费</ClientButton>}
    {actions?.upgrade && <ClientButton type="button" onClick={actions.upgrade}>升级</ClientButton>}
    {retry && <ClientButton type="button" className="entitlement-button-secondary" onClick={retry}>刷新</ClientButton>}
  </div>;
}

function formatTime(value: string): string {
  const date = new Date(value);
  if (!Number.isFinite(date.valueOf())) return "未知时间";
  return new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(date);
}
