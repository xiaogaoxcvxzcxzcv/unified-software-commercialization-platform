import { useEffect, useRef, useState } from "react";
import type { FormEvent, KeyboardEvent } from "react";
import { ClientButton, ClientField, ClientNotice } from "../components.js";
import { AccountActions, AccountBlockFrame } from "./AccountBlockFrame.js";
import type { AccountBlockCommonProps, AccountExternalIdentity, AccountSessionSummary } from "./types.js";
import { useSensitiveValue } from "./useSensitiveValue.js";

export interface ChangePasswordInput { readonly currentPassword: string; readonly newPassword: string }
export interface AccountSecurityActions {
  readonly changePassword?: (input: ChangePasswordInput) => void | Promise<void>;
  readonly revokeSession?: (sessionId: string) => void | Promise<void>;
  readonly revokeAllOtherSessions?: () => void | Promise<void>;
  readonly unlinkIdentity?: (identityId: string) => void | Promise<void>;
  readonly cancel?: () => void;
}
export interface AccountSecurityBlockProps extends AccountBlockCommonProps {
  readonly sessions: readonly AccountSessionSummary[];
  readonly externalIdentities?: readonly AccountExternalIdentity[];
  readonly passwordChangeAllowed: boolean;
  readonly passwordFieldErrors?: Readonly<Partial<Record<"currentPassword" | "newPassword" | "confirmPassword", string>>>;
  readonly actions: AccountSecurityActions;
}

type ConfirmationAction = { kind: "session"; id: string; label: string } | { kind: "all" } | { kind: "identity"; id: string; label: string };

function isConfirmationAvailable(action: ConfirmationAction, props: AccountSecurityBlockProps): boolean {
  if (action.kind === "session") return Boolean(props.actions.revokeSession) && props.sessions.some((item) => item.id === action.id && !item.current && !item.revoked);
  if (action.kind === "identity") return Boolean(props.actions.unlinkIdentity) && Boolean(props.externalIdentities?.some((item) => item.id === action.id && item.unlinkAllowed));
  return Boolean(props.actions.revokeAllOtherSessions) && props.sessions.some((item) => !item.current && !item.revoked);
}

export function AccountSecurityBlock(props: AccountSecurityBlockProps) {
  const currentPassword = useSensitiveValue();
  const newPassword = useSensitiveValue();
  const confirmation = useSensitiveValue();
  const [confirmationAction, setConfirmationAction] = useState<ConfirmationAction | null>(null);
  const confirmationRef = useRef<HTMLDialogElement>(null);
  const contentRef = useRef<HTMLDivElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const busy = props.state === "submitting";
  const passwordChangeAvailable = props.state !== "disabled" && props.state !== "empty" && props.passwordChangeAllowed && Boolean(props.actions.changePassword);
  const clearSecrets = () => { currentPassword.clear(); newPassword.clear(); confirmation.clear(); };
  const submitPassword = (event: FormEvent) => {
    event.preventDefault();
    const input = { currentPassword: currentPassword.value, newPassword: newPassword.value };
    const passwordsMatch = newPassword.value === confirmation.value;
    clearSecrets();
    if (busy || !props.actions.changePassword || !passwordsMatch) return;
    void props.actions.changePassword(input);
  };
  useEffect(() => {
    if (!passwordChangeAvailable) clearSecrets();
  }, [passwordChangeAvailable, currentPassword.clear, newPassword.clear, confirmation.clear]);
  useEffect(() => {
    if (!confirmationAction) return;
    if (!isConfirmationAvailable(confirmationAction, props)) setConfirmationAction(null);
  }, [confirmationAction, props.actions.revokeSession, props.actions.revokeAllOtherSessions, props.actions.unlinkIdentity, props.sessions, props.externalIdentities]);
  useEffect(() => {
    const dialog = confirmationRef.current;
    if (!confirmationAction || !dialog) return;
    try {
      if (typeof dialog.showModal === "function") dialog.showModal();
      else dialog.setAttribute("open", "");
    } catch {
      dialog.setAttribute("open", "");
    }
    dialog.focus();
    return () => {
      try {
        if (dialog.open && typeof dialog.close === "function") dialog.close();
        else dialog.removeAttribute("open");
      } catch {
        dialog.removeAttribute("open");
      }
    };
  }, [confirmationAction]);
  useEffect(() => {
    if (!confirmationAction && returnFocusRef.current) {
      const target = returnFocusRef.current;
      returnFocusRef.current = null;
      if (target.isConnected) target.focus();
      else contentRef.current?.focus();
    }
  }, [confirmationAction]);
  const openConfirmation = (action: NonNullable<typeof confirmationAction>) => {
    returnFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setConfirmationAction(action);
  };
  const closeConfirmation = () => {
    setConfirmationAction(null);
  };
  const handleConfirmationKeyDown = (event: KeyboardEvent<HTMLDialogElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      closeConfirmation();
      return;
    }
    if (event.key !== "Tab") return;
    const dialog = confirmationRef.current;
    if (!dialog) return;
    const controls = Array.from(dialog.querySelectorAll<HTMLElement>('button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'));
    const first = controls[0];
    const last = controls.at(-1);
    if (!first || !last) { event.preventDefault(); return; }
    const active = document.activeElement;
    if (event.shiftKey && (active === first || active === dialog)) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && (active === last || active === dialog)) {
      event.preventDefault();
      first.focus();
    }
  };
  const cancel = () => { clearSecrets(); closeConfirmation(); props.actions.cancel?.(); };
  const confirm = () => {
    const action = confirmationAction;
    closeConfirmation();
    if (!action || !isConfirmationAvailable(action, props)) return;
    if (action.kind === "session") void props.actions.revokeSession?.(action.id);
    else if (action.kind === "identity") void props.actions.unlinkIdentity?.(action.id);
    else void props.actions.revokeAllOtherSessions?.();
  };
  const activeOtherSessions = props.sessions.filter((item) => !item.current && !item.revoked);
  return <AccountBlockFrame id="account.security" title="账号安全" description="管理密码、登录会话和已绑定身份" common={props}>
    <div className="account-security-layout">
      <div ref={contentRef} tabIndex={-1} className="account-security-content">
      {passwordChangeAvailable && <section className="account-security-section" aria-labelledby="account-password-title"><header><h3 id="account-password-title">修改密码</h3><p>提交后当前表单中的密码会立即清空</p></header><form className="account-block-form" onSubmit={submitPassword} noValidate><fieldset disabled={busy}><ClientField label="当前密码" error={props.passwordFieldErrors?.currentPassword} inputProps={{ id: "account-security-current-password", name: "currentPassword", type: "password", autoComplete: "current-password", value: currentPassword.value, onChange: (event) => currentPassword.setValue(event.currentTarget.value), required: true }} /><ClientField label="新密码" error={props.passwordFieldErrors?.newPassword} inputProps={{ id: "account-security-new-password", name: "newPassword", type: "password", autoComplete: "new-password", value: newPassword.value, onChange: (event) => newPassword.setValue(event.currentTarget.value), required: true }} /><ClientField label="确认新密码" error={props.passwordFieldErrors?.confirmPassword ?? (confirmation.value && newPassword.value !== confirmation.value ? "两次输入的密码不一致" : undefined)} inputProps={{ id: "account-security-confirm-password", name: "confirmPassword", type: "password", autoComplete: "new-password", value: confirmation.value, onChange: (event) => confirmation.setValue(event.currentTarget.value), required: true }} /></fieldset><AccountActions><ClientButton type="submit" busy={busy}>更新密码</ClientButton>{props.actions.cancel && <ClientButton type="button" className="account-button-secondary" onClick={cancel}>取消</ClientButton>}</AccountActions></form></section>}
      <section className="account-security-section" aria-labelledby="account-sessions-title"><header><h3 id="account-sessions-title">登录会话</h3><p>只显示服务端返回的脱敏会话摘要</p></header>{props.sessions.length === 0 ? <div className="account-inline-empty">暂无登录会话</div> : <ul className="account-session-list">{props.sessions.map((item) => <li key={item.id}><div><strong>{item.deviceLabel}{item.current && <span className="account-current-badge">当前</span>}</strong><small>{item.authenticationMethod} · {item.applicationLabel}{item.environmentLabel ? ` · ${item.environmentLabel}` : ""}</small><small>最后活跃 {item.lastSeenLabel} · 到期 {item.expiresLabel}</small></div><span>{item.revoked ? "已撤销" : item.current ? "正在使用" : props.actions.revokeSession ? <ClientButton type="button" className="account-button-danger" disabled={busy} onClick={() => openConfirmation({ kind: "session", id: item.id, label: item.deviceLabel })}>撤销</ClientButton> : "已登录"}</span></li>)}</ul>}{activeOtherSessions.length > 0 && props.actions.revokeAllOtherSessions && <ClientButton type="button" className="account-button-danger" disabled={busy} onClick={() => openConfirmation({ kind: "all" })}>撤销其他全部会话</ClientButton>}</section>
      {props.externalIdentities && props.externalIdentities.length > 0 && <section className="account-security-section" aria-labelledby="account-identities-title"><header><h3 id="account-identities-title">外部身份</h3><p>已绑定的第三方登录方式</p></header><ul className="account-identity-list">{props.externalIdentities.map((item) => <li key={item.id}><span><strong>{item.providerLabel}</strong><small>{item.subjectMasked}</small></span>{item.unlinkAllowed && props.actions.unlinkIdentity && <ClientButton type="button" className="account-button-danger" disabled={busy} onClick={() => openConfirmation({ kind: "identity", id: item.id, label: item.providerLabel })}>解除绑定</ClientButton>}</li>)}</ul></section>}
      {!passwordChangeAvailable && props.actions.cancel && <AccountActions><ClientButton type="button" className="account-button-secondary" onClick={cancel}>取消</ClientButton></AccountActions>}
      </div>
      {confirmationAction && <dialog ref={confirmationRef} tabIndex={-1} onCancel={(event) => { event.preventDefault(); closeConfirmation(); }} onKeyDown={handleConfirmationKeyDown} className="account-confirmation" aria-modal="true" aria-labelledby="account-confirmation-title"><ClientNotice tone="danger"><div><strong id="account-confirmation-title">确认危险操作</strong><p>{confirmationAction.kind === "session" ? `撤销 ${confirmationAction.label} 的登录会话？` : confirmationAction.kind === "identity" ? `解除 ${confirmationAction.label} 身份绑定？` : "撤销除当前设备外的全部登录会话？"}</p></div></ClientNotice><AccountActions><ClientButton type="button" className="account-button-danger" onClick={confirm}>确认执行</ClientButton><ClientButton type="button" className="account-button-secondary" onClick={closeConfirmation}>取消</ClientButton></AccountActions></dialog>}
    </div>
  </AccountBlockFrame>;
}
