import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { ClientButton, ClientField } from "../components.js";
import { AccountActions, AccountBlockFrame } from "./AccountBlockFrame.js";
import type { AccountBlockCommonProps, AccountProviderOption } from "./types.js";
import { useSensitiveValue } from "./useSensitiveValue.js";

export interface AuthLoginInput { readonly identifier: string; readonly password: string }
export interface AuthLoginActions {
  readonly submit?: (input: AuthLoginInput) => void | Promise<void>;
  readonly cancel?: () => void;
  readonly openRegister?: () => void;
  readonly openRecovery?: () => void;
  readonly selectProvider?: (providerId: string) => void;
}
export interface AuthLoginBlockProps extends AccountBlockCommonProps {
  readonly title?: string;
  readonly description?: string;
  readonly identifierLabel?: string;
  readonly identifierPlaceholder?: string;
  readonly passwordEnabled?: boolean;
  readonly providers?: readonly AccountProviderOption[];
  readonly fieldErrors?: Readonly<Partial<Record<"identifier" | "password", string>>>;
  readonly actions: AuthLoginActions;
}

export function AuthLoginBlock(props: AuthLoginBlockProps) {
  const [identifier, setIdentifier] = useState("");
  const password = useSensitiveValue();
  const busy = props.state === "submitting";
  const passwordFormEnabled = props.passwordEnabled !== false && Boolean(props.actions.submit);
  useEffect(() => {
    if (!passwordFormEnabled) password.clear();
  }, [passwordFormEnabled, password.clear]);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (busy || !passwordFormEnabled || !props.actions.submit) return;
    const input = { identifier: identifier.trim(), password: password.value };
    password.clear();
    void props.actions.submit(input);
  };
  const cancel = () => { password.clear(); props.actions.cancel?.(); };
  const openRegister = () => { password.clear(); props.actions.openRegister?.(); };
  const openRecovery = () => { password.clear(); props.actions.openRecovery?.(); };
  const selectProvider = (providerId: string) => { password.clear(); props.actions.selectProvider?.(providerId); };
  return <AccountBlockFrame id="auth.login" title={props.title ?? "登录"} description={props.description ?? "使用你的账号继续"} common={props}>
    <div className="account-block-form">
      {passwordFormEnabled && <form className="account-block-form" onSubmit={submit} noValidate>
        <fieldset disabled={busy}>
          <ClientField label={props.identifierLabel ?? "邮箱或手机号"} error={props.fieldErrors?.identifier} inputProps={{ id: "account-login-identifier", name: "identifier", autoComplete: "username", value: identifier, placeholder: props.identifierPlaceholder, onChange: (event) => setIdentifier(event.currentTarget.value), required: true }} />
          <ClientField label="密码" error={props.fieldErrors?.password} inputProps={{ id: "account-login-password", name: "password", type: "password", autoComplete: "current-password", value: password.value, onChange: (event) => password.setValue(event.currentTarget.value), required: true }} />
        </fieldset>
        <AccountActions><ClientButton type="submit" busy={busy}>登录</ClientButton>{props.actions.cancel && <ClientButton type="button" className="account-button-secondary" onClick={cancel}>取消</ClientButton>}</AccountActions>
      </form>}
      {!passwordFormEnabled && props.actions.cancel && <AccountActions><ClientButton type="button" className="account-button-secondary" onClick={cancel}>取消</ClientButton></AccountActions>}
      {(props.actions.openRegister || props.actions.openRecovery) && <nav className="account-block-links" aria-label="账号帮助">{props.actions.openRegister && <button type="button" onClick={openRegister}>创建账号</button>}{props.actions.openRecovery && <button type="button" onClick={openRecovery}>忘记密码</button>}</nav>}
      {props.providers && props.providers.length > 0 && props.actions.selectProvider && <div className="account-provider-list" aria-label="其他登录方式"><span>其他登录方式</span>{props.providers.map((provider) => <ClientButton key={provider.id} type="button" className="account-button-secondary" onClick={() => selectProvider(provider.id)}>{provider.label}</ClientButton>)}</div>}
    </div>
  </AccountBlockFrame>;
}
