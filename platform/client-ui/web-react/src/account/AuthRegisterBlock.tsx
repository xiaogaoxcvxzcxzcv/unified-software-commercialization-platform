import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { ClientButton, ClientField } from "../components.js";
import { AccountActions, AccountBlockFrame } from "./AccountBlockFrame.js";
import type { AccountBlockCommonProps, AccountProviderOption } from "./types.js";
import { useSensitiveValue } from "./useSensitiveValue.js";

export type AuthRegisterStep = "request_verification" | "complete";
export type AuthRegisterInput = AuthRegisterRequestVerificationInput | AuthRegisterCompleteInput;

export interface AuthRegisterRequestVerificationInput {
  readonly step: "request_verification";
  readonly identifier: string;
}

export interface AuthRegisterCompleteInput {
  readonly step: "complete";
  readonly verificationProof: string;
  readonly displayName: string;
  readonly password: string;
  readonly agreementAccepted: boolean;
}

export interface AuthRegisterRequestVerificationActions {
  readonly requestVerification?: (input: AuthRegisterRequestVerificationInput) => void | Promise<void>;
  readonly cancel?: () => void;
  readonly openLogin?: () => void;
  readonly selectProvider?: (providerId: string) => void;
}

export interface AuthRegisterCompleteActions {
  readonly complete?: (input: AuthRegisterCompleteInput) => void | Promise<void>;
  readonly cancel?: () => void;
  readonly openLogin?: () => void;
}

interface AuthRegisterBaseProps extends AccountBlockCommonProps {
  readonly step: AuthRegisterStep;
}

export interface AuthRegisterRequestVerificationProps extends AuthRegisterBaseProps {
  readonly step: "request_verification";
  readonly defaultIdentifier?: string;
  readonly providers?: readonly AccountProviderOption[];
  readonly fieldErrors?: Readonly<Partial<Record<"identifier", string>>>;
  readonly actions: AuthRegisterRequestVerificationActions;
}

export interface AuthRegisterCompleteProps extends AuthRegisterBaseProps {
  readonly step: "complete";
  readonly agreementLabel: string;
  readonly fieldErrors?: Readonly<Partial<Record<"verificationProof" | "displayName" | "password" | "confirmPassword" | "agreement", string>>>;
  readonly actions: AuthRegisterCompleteActions;
}

export type AuthRegisterActions = AuthRegisterRequestVerificationActions | AuthRegisterCompleteActions;
export type AuthRegisterBlockProps = AuthRegisterRequestVerificationProps | AuthRegisterCompleteProps;

export function AuthRegisterBlock(props: AuthRegisterBlockProps) {
  const [identifier, setIdentifier] = useState(props.step === "request_verification" ? props.defaultIdentifier ?? "" : "");
  const [displayName, setDisplayName] = useState("");
  const [agreementAccepted, setAgreementAccepted] = useState(false);
  const [agreementValidationError, setAgreementValidationError] = useState<string>();
  const verificationProof = useSensitiveValue();
  const password = useSensitiveValue();
  const confirmation = useSensitiveValue();
  const busy = props.state === "submitting";
  const primaryActionAvailable = props.step === "request_verification" ? Boolean(props.actions.requestVerification) : Boolean(props.actions.complete);
  const clearSecrets = () => { verificationProof.clear(); password.clear(); confirmation.clear(); };

  useEffect(() => {
    clearSecrets();
    setIdentifier(props.step === "request_verification" ? props.defaultIdentifier ?? "" : "");
    setDisplayName("");
    setAgreementAccepted(false);
    setAgreementValidationError(undefined);
  }, [props.step, props.step === "request_verification" ? props.defaultIdentifier : undefined, primaryActionAvailable, verificationProof.clear, password.clear, confirmation.clear]);
  useEffect(() => {
    if (props.state === "loading" || props.state === "empty" || props.state === "disabled" || props.state === "success") clearSecrets();
  }, [props.state, verificationProof.clear, password.clear, confirmation.clear]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (props.step === "request_verification") {
      if (busy) return;
      if (props.actions.requestVerification) void props.actions.requestVerification({ step: "request_verification", identifier: identifier.trim() });
      return;
    }
    const input: AuthRegisterCompleteInput = {
      step: "complete",
      verificationProof: verificationProof.value,
      displayName: displayName.trim(),
      password: password.value,
      agreementAccepted,
    };
    const passwordsMatch = password.value === confirmation.value;
    clearSecrets();
    if (busy || !props.actions.complete || !passwordsMatch) return;
    if (!agreementAccepted) {
      setAgreementValidationError("请先同意服务协议");
      return;
    }
    setAgreementValidationError(undefined);
    void props.actions.complete(input);
  };

  const cancel = () => {
    clearSecrets();
    setIdentifier("");
    setDisplayName("");
    setAgreementAccepted(false);
    setAgreementValidationError(undefined);
    props.actions.cancel?.();
  };
  const openLogin = () => { clearSecrets(); props.actions.openLogin?.(); };
  const selectProvider = (providerId: string) => {
    clearSecrets();
    if (props.step === "request_verification") props.actions.selectProvider?.(providerId);
  };

  const completeFieldErrors = props.step === "complete" ? props.fieldErrors : undefined;
  const confirmError = completeFieldErrors?.confirmPassword ?? (confirmation.value && password.value !== confirmation.value ? "两次输入的密码不一致" : undefined);
  const agreementError = completeFieldErrors?.agreement ?? agreementValidationError;

  return <AccountBlockFrame id="auth.register" title="创建账号" description={props.step === "request_verification" ? "先验证你的邮箱或手机号" : "输入验证码并完成账号资料"} common={props}>
    <div className="account-block-form">
    {primaryActionAvailable && <form className="account-block-form" onSubmit={submit} noValidate>
      <fieldset disabled={busy}>
        {props.step === "request_verification" ? <ClientField label="邮箱或手机号" error={props.fieldErrors?.identifier} inputProps={{ id: "account-register-identifier", name: "identifier", autoComplete: "username", value: identifier, onChange: (event) => setIdentifier(event.currentTarget.value), required: true }} /> : <>
          <ClientField label="验证码" error={completeFieldErrors?.verificationProof} inputProps={{ id: "account-register-proof", name: "verificationProof", inputMode: "numeric", autoComplete: "one-time-code", value: verificationProof.value, onChange: (event) => verificationProof.setValue(event.currentTarget.value), required: true }} />
          <ClientField label="显示名称" error={completeFieldErrors?.displayName} inputProps={{ id: "account-register-name", name: "displayName", autoComplete: "name", value: displayName, onChange: (event) => setDisplayName(event.currentTarget.value), required: true }} />
          <ClientField label="密码" error={completeFieldErrors?.password} inputProps={{ id: "account-register-password", name: "password", type: "password", autoComplete: "new-password", value: password.value, onChange: (event) => password.setValue(event.currentTarget.value), required: true }} />
          <ClientField label="确认密码" error={confirmError} inputProps={{ id: "account-register-confirm", name: "confirmPassword", type: "password", autoComplete: "new-password", value: confirmation.value, onChange: (event) => confirmation.setValue(event.currentTarget.value), required: true }} />
          <label className="account-checkbox"><input type="checkbox" checked={agreementAccepted} onChange={(event) => { setAgreementAccepted(event.currentTarget.checked); if (event.currentTarget.checked) setAgreementValidationError(undefined); }} aria-invalid={Boolean(agreementError)} aria-describedby={agreementError ? "account-register-agreement-error" : undefined}/><span>{props.agreementLabel}</span></label>
          {agreementError && <span id="account-register-agreement-error" className="account-field-error" role="alert">{agreementError}</span>}
        </>}
      </fieldset>
      <AccountActions><ClientButton type="submit" busy={busy}>{props.step === "request_verification" ? "发送验证码" : "注册"}</ClientButton>{props.actions.cancel && <ClientButton type="button" className="account-button-secondary" onClick={cancel}>取消</ClientButton>}</AccountActions>
    </form>}
      {!primaryActionAvailable && props.actions.cancel && <AccountActions><ClientButton type="button" className="account-button-secondary" onClick={cancel}>取消</ClientButton></AccountActions>}
      {props.actions.openLogin && <nav className="account-block-links" aria-label="账号帮助"><button type="button" onClick={openLogin}>返回登录</button></nav>}
      {props.step === "request_verification" && props.providers && props.providers.length > 0 && props.actions.selectProvider && <div className="account-provider-list" aria-label="其他注册方式"><span>其他注册方式</span>{props.providers.map((provider) => <ClientButton key={provider.id} type="button" className="account-button-secondary" onClick={() => selectProvider(provider.id)}>{provider.label}</ClientButton>)}</div>}
    </div>
  </AccountBlockFrame>;
}
