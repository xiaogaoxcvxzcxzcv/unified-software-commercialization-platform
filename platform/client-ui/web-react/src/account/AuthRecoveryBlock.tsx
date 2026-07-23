import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { ClientButton, ClientField } from "../components.js";
import { AccountActions, AccountBlockFrame } from "./AccountBlockFrame.js";
import type { AccountBlockCommonProps } from "./types.js";
import { useSensitiveValue } from "./useSensitiveValue.js";

export type AuthRecoveryStep = "identify" | "complete";
export type AuthRecoveryInput = AuthRecoveryIdentifyInput | AuthRecoveryCompleteInput;

export interface AuthRecoveryIdentifyInput {
  readonly step: "identify";
  readonly identifier: string;
}

export interface AuthRecoveryCompleteInput {
  readonly step: "complete";
  readonly recoveryProof: string;
  readonly password: string;
}

export interface AuthRecoveryIdentifyActions {
  readonly requestRecovery?: (input: AuthRecoveryIdentifyInput) => void | Promise<void>;
  readonly cancel?: () => void;
}

export interface AuthRecoveryCompleteActions {
  readonly complete?: (input: AuthRecoveryCompleteInput) => void | Promise<void>;
  readonly cancel?: () => void;
  readonly resend?: () => void;
}

interface AuthRecoveryBaseProps extends AccountBlockCommonProps {
  readonly step: AuthRecoveryStep;
}

export interface AuthRecoveryIdentifyProps extends AuthRecoveryBaseProps {
  readonly step: "identify";
  readonly fieldErrors?: Readonly<Partial<Record<"identifier", string>>>;
  readonly actions: AuthRecoveryIdentifyActions;
}

export interface AuthRecoveryCompleteProps extends AuthRecoveryBaseProps {
  readonly step: "complete";
  readonly deliveryTargetMasked?: string;
  readonly cooldownLabel?: string;
  readonly fieldErrors?: Readonly<Partial<Record<"recoveryProof" | "password" | "confirmPassword", string>>>;
  readonly actions: AuthRecoveryCompleteActions;
}

export type AuthRecoveryActions = AuthRecoveryIdentifyActions | AuthRecoveryCompleteActions;
export type AuthRecoveryBlockProps = AuthRecoveryIdentifyProps | AuthRecoveryCompleteProps;

export function AuthRecoveryBlock(props: AuthRecoveryBlockProps) {
  const [identifier, setIdentifier] = useState("");
  const recoveryProof = useSensitiveValue();
  const password = useSensitiveValue();
  const confirmation = useSensitiveValue();
  const busy = props.state === "submitting";
  const primaryActionAvailable = props.step === "identify" ? Boolean(props.actions.requestRecovery) : Boolean(props.actions.complete);
  const clearSecrets = () => { recoveryProof.clear(); password.clear(); confirmation.clear(); };

  useEffect(() => {
    clearSecrets();
    setIdentifier("");
  }, [props.step, primaryActionAvailable, recoveryProof.clear, password.clear, confirmation.clear]);
  useEffect(() => {
    if (props.state === "loading" || props.state === "empty" || props.state === "disabled" || props.state === "success") clearSecrets();
  }, [props.state, recoveryProof.clear, password.clear, confirmation.clear]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (props.step === "identify") {
      if (busy) return;
      if (props.actions.requestRecovery) void props.actions.requestRecovery({ step: "identify", identifier: identifier.trim() });
      return;
    }
    const input: AuthRecoveryCompleteInput = { step: "complete", recoveryProof: recoveryProof.value, password: password.value };
    const passwordsMatch = password.value === confirmation.value;
    clearSecrets();
    if (busy || !props.actions.complete || !passwordsMatch) return;
    void props.actions.complete(input);
  };

  const cancel = () => {
    clearSecrets();
    setIdentifier("");
    props.actions.cancel?.();
  };
  const resend = () => {
    clearSecrets();
    if (props.step === "complete") props.actions.resend?.();
  };

  const completeFieldErrors = props.step === "complete" ? props.fieldErrors : undefined;
  const confirmError = completeFieldErrors?.confirmPassword ?? (confirmation.value && password.value !== confirmation.value ? "两次输入的密码不一致" : undefined);

  return <AccountBlockFrame id="auth.recovery" title="找回密码" description={props.step === "identify" ? "先确认你的账号" : `输入发送至 ${props.deliveryTargetMasked ?? "已验证联系方式"} 的验证码并设置新密码`} common={props}>
    <div className="account-block-form">
    {primaryActionAvailable && <form className="account-block-form" onSubmit={submit} noValidate>
      <fieldset disabled={busy}>
        {props.step === "identify" ? <ClientField label="邮箱或手机号" error={props.fieldErrors?.identifier} inputProps={{ id: "account-recovery-identifier", name: "identifier", autoComplete: "username", value: identifier, onChange: (event) => setIdentifier(event.currentTarget.value), required: true }} /> : <>
          <ClientField label="验证码" error={completeFieldErrors?.recoveryProof} inputProps={{ id: "account-recovery-proof", name: "recoveryProof", inputMode: "numeric", autoComplete: "one-time-code", value: recoveryProof.value, onChange: (event) => recoveryProof.setValue(event.currentTarget.value), required: true }} />
          <ClientField label="新密码" error={completeFieldErrors?.password} inputProps={{ id: "account-recovery-password", name: "password", type: "password", autoComplete: "new-password", value: password.value, onChange: (event) => password.setValue(event.currentTarget.value), required: true }} />
          <ClientField label="确认新密码" error={confirmError} inputProps={{ id: "account-recovery-confirm", name: "confirmPassword", type: "password", autoComplete: "new-password", value: confirmation.value, onChange: (event) => confirmation.setValue(event.currentTarget.value), required: true }} />
        </>}
      </fieldset>
      <AccountActions><ClientButton type="submit" busy={busy}>{props.step === "identify" ? "继续" : "更新密码"}</ClientButton>{props.actions.cancel && <ClientButton type="button" className="account-button-secondary" onClick={cancel}>取消</ClientButton>}</AccountActions>
    </form>}
      {!primaryActionAvailable && props.actions.cancel && <AccountActions><ClientButton type="button" className="account-button-secondary" onClick={cancel}>取消</ClientButton></AccountActions>}
      {props.step === "complete" && props.actions.resend && <div className="account-resend"><button type="button" disabled={busy || Boolean(props.cooldownLabel)} onClick={resend}>重新发送</button>{props.cooldownLabel && <span role="status">{props.cooldownLabel}</span>}</div>}
    </div>
  </AccountBlockFrame>;
}
