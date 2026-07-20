import { ClientButton } from "../components.js";
import { AccountBlockFrame } from "./AccountBlockFrame.js";
import type { AccountBlockCommonProps, AccountSecuritySummary, AccountUserSummary } from "./types.js";

export interface AccountCenterActions { readonly openProfile?: () => void; readonly openSecurity?: () => void; readonly signOut?: () => void | Promise<void>; readonly close?: () => void }
export interface AccountCenterBlockProps extends AccountBlockCommonProps { readonly user: AccountUserSummary; readonly security: AccountSecuritySummary; readonly actions: AccountCenterActions }

export function AccountCenterBlock(props: AccountCenterBlockProps) {
  const busy = props.state === "submitting";
  return <AccountBlockFrame id="account.center" title="个人中心" description="管理全局账号资料和当前登录安全" common={props}>
    <div className="account-center-layout">
      <div className="account-identity-summary">{props.user.avatarUrl ? <img src={props.user.avatarUrl} alt=""/> : <span aria-hidden="true">{Array.from(props.user.displayName)[0] ?? "用"}</span>}<div><strong>{props.user.displayName}</strong>{props.user.maskedIdentifier && <small>{props.user.maskedIdentifier}</small>}</div></div>
      {(props.actions.openProfile || props.actions.openSecurity) && <nav className="account-center-nav" aria-label="个人中心功能">{props.actions.openProfile && <button type="button" disabled={busy} onClick={props.actions.openProfile}><span><strong>个人资料</strong><small>名称、语言和时区</small></span><span aria-hidden="true">›</span></button>}{props.actions.openSecurity && <button type="button" disabled={busy} onClick={props.actions.openSecurity}><span><strong>账号安全</strong><small>{props.security.activeSessionCount} 个活跃会话 · {props.security.externalIdentityCount} 个外部身份</small></span><span aria-hidden="true">›</span></button>}</nav>}
      {(props.actions.signOut || props.actions.close) && <div className="account-center-footer">{props.actions.signOut && <ClientButton type="button" className="account-button-danger" busy={busy} onClick={() => void props.actions.signOut?.()}>退出当前账号</ClientButton>}{props.actions.close && <ClientButton type="button" className="account-button-secondary" onClick={props.actions.close}>关闭</ClientButton>}</div>}
    </div>
  </AccountBlockFrame>;
}
