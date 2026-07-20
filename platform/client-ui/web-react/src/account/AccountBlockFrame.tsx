import type { ReactNode } from "react";
import { ClientButton, ClientNotice } from "../components.js";
import type { AccountBlockCommonProps } from "./types.js";

export function AccountBlockFrame({ id, title, description, common, children }: {
  readonly id: string;
  readonly title: string;
  readonly description?: string;
  readonly common: AccountBlockCommonProps;
  readonly children: ReactNode;
}) {
  const className = ["account-block", `account-block-${id.replaceAll(".", "-")}`, common.className].filter(Boolean).join(" ");
  const content = (() => {
    if (common.state === "loading") return <div className="account-block-state" aria-busy="true"><span className="client-spinner" aria-hidden="true"/><span>正在加载...</span></div>;
    if (common.state === "empty") return <div className="account-block-state"><span>{common.emptyMessage ?? "暂无可显示内容"}</span></div>;
    if (common.state === "disabled") return <div className="account-block-state"><span>{common.disabledMessage ?? "当前能力不可用"}</span></div>;
    if (common.state === "failed") return <><ClientNotice tone="danger"><span>{common.error?.message ?? "操作未完成，请重试"}</span>{common.error?.retryable && common.onRetry && <ClientButton type="button" onClick={common.onRetry}>重试</ClientButton>}</ClientNotice><div>{children}</div></>;
    if (common.state === "success") return <ClientNotice tone="success">{common.successMessage ?? "操作已完成"}</ClientNotice>;
    return <>{common.state === "submitting" && <div className="account-submitting-status" role="status"><span className="client-spinner" aria-hidden="true"/><span>正在提交...</span></div>}<div aria-busy={common.state === "submitting"}>{children}</div></>;
  })();
  return <section className={className} aria-labelledby={`${id}-title`} data-state={common.state}>
    <header className="account-block-header"><div><h2 id={`${id}-title`}>{title}</h2>{description && <p>{description}</p>}</div></header>
    {content}
  </section>;
}

export function AccountActions({ children }: { readonly children: ReactNode }) {
  return <div className="account-block-actions">{children}</div>;
}
