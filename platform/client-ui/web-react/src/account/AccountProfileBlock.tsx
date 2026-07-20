import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { ClientButton, ClientField } from "../components.js";
import { AccountActions, AccountBlockFrame } from "./AccountBlockFrame.js";
import type { AccountBlockCommonProps, AccountProfileValue } from "./types.js";

export interface AccountProfileInput { readonly displayName: string; readonly locale: string; readonly timezone: string; readonly expectedVersion: number }
export interface AccountProfileActions { readonly submit?: (input: AccountProfileInput) => void | Promise<void>; readonly cancel?: () => void; readonly selectAvatar?: () => void }
export interface AccountProfileBlockProps extends AccountBlockCommonProps { readonly value: AccountProfileValue; readonly fieldErrors?: Readonly<Partial<Record<"displayName" | "locale" | "timezone", string>>>; readonly actions: AccountProfileActions }

export function AccountProfileBlock(props: AccountProfileBlockProps) {
  const [displayName, setDisplayName] = useState(props.value.displayName);
  const [locale, setLocale] = useState(props.value.locale);
  const [timezone, setTimezone] = useState(props.value.timezone);
  useEffect(() => {
    setDisplayName(props.value.displayName);
    setLocale(props.value.locale);
    setTimezone(props.value.timezone);
  }, [props.value.displayName, props.value.locale, props.value.timezone, props.value.version]);
  const busy = props.state === "submitting";
  const submit = (event: FormEvent) => { event.preventDefault(); if (!busy && props.actions.submit) void props.actions.submit({ displayName: displayName.trim(), locale: locale.trim(), timezone: timezone.trim(), expectedVersion: props.value.version }); };
  return <AccountBlockFrame id="account.profile" title="个人资料" description="这些资料属于你的全局账号" common={props}>
    <form className="account-block-form" onSubmit={submit} noValidate>
      <div className="account-profile-avatar">{props.value.avatarUrl ? <img src={props.value.avatarUrl} alt="当前头像"/> : <span aria-hidden="true">{Array.from(displayName)[0] ?? "用"}</span>}{props.actions.selectAvatar && <ClientButton type="button" className="account-button-secondary" disabled={busy} onClick={props.actions.selectAvatar}>更换头像</ClientButton>}</div>
      <fieldset disabled={busy || !props.actions.submit}><ClientField label="显示名称" error={props.fieldErrors?.displayName} inputProps={{ id: "account-profile-name", name: "displayName", autoComplete: "name", value: displayName, onChange: (event) => setDisplayName(event.currentTarget.value), required: true }} /><ClientField label="语言" error={props.fieldErrors?.locale} inputProps={{ id: "account-profile-locale", name: "locale", value: locale, onChange: (event) => setLocale(event.currentTarget.value), required: true }} /><ClientField label="时区" error={props.fieldErrors?.timezone} inputProps={{ id: "account-profile-timezone", name: "timezone", value: timezone, onChange: (event) => setTimezone(event.currentTarget.value), required: true }} /></fieldset>
      {(props.actions.submit || props.actions.cancel) && <AccountActions>{props.actions.submit && <ClientButton type="submit" busy={busy}>保存资料</ClientButton>}{props.actions.cancel && <ClientButton type="button" className="account-button-secondary" onClick={props.actions.cancel}>取消</ClientButton>}</AccountActions>}
    </form>
  </AccountBlockFrame>;
}
