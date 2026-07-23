import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AccountCenterBlock, AccountProfileBlock, AccountSecurityBlock, AuthLoginBlock, AuthRecoveryBlock, AuthRegisterBlock } from "../web-react/src/account/index.js";
import type { AccountBlockState } from "../web-react/src/account/index.js";

const noop = () => {};
const common = { state: "ready" as const };
const enter = (control: HTMLElement, value: string) => fireEvent.change(control, { target: { value } });
afterEach(cleanup);

describe("Account Block common states", () => {
  const cases: ReadonlyArray<{ state: AccountBlockState; expected: string }> = [
    { state: "idle", expected: "登录" },
    { state: "loading", expected: "正在加载" },
    { state: "ready", expected: "邮箱或手机号" },
    { state: "submitting", expected: "正在提交" },
    { state: "success", expected: "登录完成" },
    { state: "empty", expected: "没有可用登录方式" },
    { state: "failed", expected: "认证服务暂时不可用" },
    { state: "disabled", expected: "账号能力已关闭" },
  ];

  for (const item of cases) {
    it(`renders ${item.state}`, () => {
      render(<AuthLoginBlock state={item.state} successMessage="登录完成" emptyMessage="没有可用登录方式" disabledMessage="账号能力已关闭" error={{ code: "temporarily_unavailable", message: "认证服务暂时不可用", retryable: true }} onRetry={noop} actions={{ submit: noop }} />);
      const region = screen.getByRole("region");
      expect(within(region).getAllByText(item.expected, { exact: false }).length).toBeGreaterThan(0);
      expect(region).toHaveAttribute("data-state", item.state);
    });
  }

  it("keeps failed forms and field errors available for correction", () => {
    render(<AuthLoginBlock state="failed" error={{ code: "invalid_identifier", message: "请修正表单", retryable: false }} fieldErrors={{ identifier: "请输入有效邮箱" }} actions={{ submit: noop }} />);
    expect(screen.getByText("请修正表单")).toBeInTheDocument();
    const identifier = screen.getByLabelText("邮箱或手机号");
    expect(identifier).toHaveAccessibleDescription("请输入有效邮箱");
    enter(identifier, "corrected@example.com");
    expect(identifier).toHaveValue("corrected@example.com");
  });
});

describe("auth.login", () => {
  it("submits typed credentials and clears password after submit and cancel", async () => {
    const submit = vi.fn();
    const cancel = vi.fn();
    render(<AuthLoginBlock {...common} actions={{ submit, cancel }} />);
    enter(screen.getByLabelText("邮箱或手机号"), " user@example.com ");
    enter(screen.getByLabelText("密码"), "private-password");
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    expect(submit).toHaveBeenCalledWith({ identifier: "user@example.com", password: "private-password" });
    expect(screen.getByLabelText("密码")).toHaveValue("");
    enter(screen.getByLabelText("密码"), "another-password");
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(cancel).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("密码")).toHaveValue("");
  });

  it("hides an empty or actionless Provider list and emits selected Provider IDs", async () => {
    const selected = vi.fn();
    const { rerender } = render(<AuthLoginBlock {...common} providers={[]} actions={{ submit: noop, selectProvider: selected }} />);
    expect(screen.queryByLabelText("其他登录方式")).not.toBeInTheDocument();
    rerender(<AuthLoginBlock {...common} providers={[{ id: "wechat", label: "微信登录" }]} actions={{ submit: noop }} />);
    expect(screen.queryByLabelText("其他登录方式")).not.toBeInTheDocument();
    rerender(<AuthLoginBlock {...common} providers={[{ id: "wechat", label: "微信登录" }]} actions={{ submit: noop, selectProvider: selected }} />);
    fireEvent.click(screen.getByRole("button", { name: "微信登录" }));
    expect(selected).toHaveBeenCalledWith("wechat");
  });

  it("does not retain a password when remounted", async () => {
    const first = render(<AuthLoginBlock {...common} actions={{ submit: noop }} />);
    enter(screen.getByLabelText("密码"), "must-not-survive");
    first.unmount();
    render(<AuthLoginBlock {...common} actions={{ submit: noop }} />);
    expect(screen.getByLabelText("密码")).toHaveValue("");
  });

  it("clears the password when the submit action is dynamically revoked", () => {
    const submit = vi.fn();
    const view = render(<AuthLoginBlock {...common} actions={{ submit, cancel: noop }} />);
    enter(screen.getByLabelText("密码"), "revoked-login-password");
    view.rerender(<AuthLoginBlock {...common} actions={{ cancel: noop }} />);
    expect(screen.queryByDisplayValue("revoked-login-password")).not.toBeInTheDocument();
    view.rerender(<AuthLoginBlock {...common} actions={{ submit, cancel: noop }} />);
    expect(screen.getByLabelText("密码")).toHaveValue("");
  });

  it("keeps registration, recovery, and Provider actions when password login is disabled", () => {
    const openRegister = vi.fn();
    const openRecovery = vi.fn();
    const selectProvider = vi.fn();
    render(<AuthLoginBlock {...common} passwordEnabled={false} providers={[{ id: "oidc", label: "企业登录" }]} actions={{ openRegister, openRecovery, selectProvider }} />);
    expect(screen.queryByLabelText("邮箱或手机号")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("密码")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "登录" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "创建账号" }));
    fireEvent.click(screen.getByRole("button", { name: "忘记密码" }));
    fireEvent.click(screen.getByRole("button", { name: "企业登录" }));
    expect(openRegister).toHaveBeenCalledTimes(1);
    expect(openRecovery).toHaveBeenCalledTimes(1);
    expect(selectProvider).toHaveBeenCalledWith("oidc");
  });

  it("fails closed when password login is enabled without a submit action", () => {
    const cancel = vi.fn();
    render(<AuthLoginBlock {...common} passwordEnabled actions={{ cancel, openRegister: noop }} />);
    expect(screen.queryByLabelText("邮箱或手机号")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("密码")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "登录" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "创建账号" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it("keeps cancel independent from a disabled password capability", () => {
    const cancel = vi.fn();
    render(<AuthLoginBlock {...common} passwordEnabled={false} actions={{ cancel }} />);
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it("clears the password before login navigation or Provider handoff", () => {
    const openRegister = vi.fn();
    const selectProvider = vi.fn();
    const view = render(<AuthLoginBlock {...common} providers={[{ id: "oidc", label: "企业登录" }]} actions={{ submit: noop, openRegister, selectProvider }} />);
    enter(screen.getByLabelText("密码"), "navigation-secret");
    fireEvent.click(screen.getByRole("button", { name: "创建账号" }));
    expect(openRegister).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("密码")).toHaveValue("");
    enter(screen.getByLabelText("密码"), "provider-secret");
    fireEvent.click(screen.getByRole("button", { name: "企业登录" }));
    expect(selectProvider).toHaveBeenCalledWith("oidc");
    expect(screen.getByLabelText("密码")).toHaveValue("");

    view.rerender(<AuthLoginBlock {...common} actions={{ submit: noop }} />);
  });
});

describe("auth.register and auth.recovery", () => {
  it("requests verification without exposing registration credentials", () => {
    const requestVerification = vi.fn();
    render(<AuthRegisterBlock {...common} step="request_verification" actions={{ requestVerification }} />);
    enter(screen.getByLabelText("邮箱或手机号"), " user@example.com ");
    fireEvent.click(screen.getByRole("button", { name: "发送验证码" }));
    expect(requestVerification).toHaveBeenCalledWith({ step: "request_verification", identifier: "user@example.com" });
    expect(screen.queryByLabelText("验证码")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("密码")).not.toBeInTheDocument();
  });

  it("completes registration with proof and clears sensitive values on submit and cancel", () => {
    const complete = vi.fn();
    const view = render(<AuthRegisterBlock {...common} step="complete" agreementLabel="我同意服务协议" actions={{ complete, cancel: noop }} />);
    enter(screen.getByLabelText("验证码"), "proof-value");
    enter(screen.getByLabelText("显示名称"), " 新用户 ");
    enter(screen.getByLabelText("密码"), "first-password");
    enter(screen.getByLabelText("确认密码"), "other-password");
    expect(screen.getByRole("alert")).toHaveTextContent("两次输入的密码不一致");
    fireEvent.click(screen.getByRole("button", { name: "注册" }));
    expect(complete).not.toHaveBeenCalled();
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("密码")).toHaveValue("");
    expect(screen.getByLabelText("确认密码")).toHaveValue("");
    expect(screen.getByLabelText("显示名称")).toHaveValue(" 新用户 ");
    enter(screen.getByLabelText("验证码"), "proof-value");
    enter(screen.getByLabelText("密码"), "first-password");
    enter(screen.getByLabelText("确认密码"), "first-password");
    fireEvent.click(screen.getByRole("button", { name: "注册" }));
    expect(complete).not.toHaveBeenCalled();
    expect(screen.getByRole("alert")).toHaveTextContent("请先同意服务协议");
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("密码")).toHaveValue("");
    expect(screen.getByLabelText("确认密码")).toHaveValue("");
    expect(screen.getByLabelText("显示名称")).toHaveValue(" 新用户 ");
    fireEvent.click(screen.getByLabelText("我同意服务协议"));
    enter(screen.getByLabelText("验证码"), "proof-value");
    enter(screen.getByLabelText("密码"), "first-password");
    enter(screen.getByLabelText("确认密码"), "first-password");
    fireEvent.click(screen.getByRole("button", { name: "注册" }));
    expect(complete).toHaveBeenCalledWith({ step: "complete", verificationProof: "proof-value", displayName: "新用户", password: "first-password", agreementAccepted: true });
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("密码")).toHaveValue("");
    expect(screen.getByLabelText("确认密码")).toHaveValue("");
    enter(screen.getByLabelText("验证码"), "second-proof");
    enter(screen.getByLabelText("密码"), "second-password");
    enter(screen.getByLabelText("确认密码"), "second-password");
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("密码")).toHaveValue("");
    expect(screen.getByLabelText("确认密码")).toHaveValue("");

    view.rerender(<AuthRegisterBlock {...common} step="request_verification" actions={{ requestVerification: noop }} />);
    expect(screen.queryByDisplayValue("second-proof")).not.toBeInTheDocument();
  });

  it("clears registration proof and passwords when the step changes", () => {
    const view = render(<AuthRegisterBlock {...common} step="complete" agreementLabel="我同意服务协议" actions={{ complete: noop }} />);
    enter(screen.getByLabelText("验证码"), "step-proof");
    enter(screen.getByLabelText("密码"), "first-password");
    view.rerender(<AuthRegisterBlock {...common} step="request_verification" actions={{ requestVerification: noop }} />);
    view.rerender(<AuthRegisterBlock {...common} step="complete" agreementLabel="我同意服务协议" actions={{ complete: noop }} />);
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("密码")).toHaveValue("");
    expect(screen.getByLabelText("确认密码")).toHaveValue("");
  });

  it("keeps independent registration exits and Providers when registration actions are absent", () => {
    const openLogin = vi.fn();
    const selectProvider = vi.fn();
    const view = render(<AuthRegisterBlock {...common} step="request_verification" providers={[{ id: "oidc", label: "企业注册" }]} actions={{ openLogin, selectProvider }} />);
    expect(screen.queryByLabelText("邮箱或手机号")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "发送验证码" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "返回登录" }));
    fireEvent.click(screen.getByRole("button", { name: "企业注册" }));
    expect(openLogin).toHaveBeenCalledTimes(1);
    expect(selectProvider).toHaveBeenCalledWith("oidc");

    view.rerender(<AuthRegisterBlock {...common} step="complete" agreementLabel="同意协议" actions={{ openLogin }} />);
    expect(screen.queryByLabelText("验证码")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("密码")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "注册" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "返回登录" })).toBeInTheDocument();
  });

  it("requests recovery, then completes with proof and password in one sensitive step", () => {
    const requestRecovery = vi.fn();
    const complete = vi.fn();
    const view = render(<AuthRecoveryBlock {...common} step="identify" actions={{ requestRecovery }} />);
    enter(screen.getByLabelText("邮箱或手机号"), " user@example.com ");
    fireEvent.click(screen.getByRole("button", { name: "继续" }));
    expect(requestRecovery).toHaveBeenCalledWith({ step: "identify", identifier: "user@example.com" });
    view.rerender(<AuthRecoveryBlock {...common} step="complete" deliveryTargetMasked="u***@example.com" actions={{ complete }} />);
    enter(screen.getByLabelText("验证码"), "123456");
    enter(screen.getByLabelText("新密码"), "replacement-password");
    enter(screen.getByLabelText("确认新密码"), "replacement-password");
    fireEvent.click(screen.getByRole("button", { name: "更新密码" }));
    expect(complete).toHaveBeenCalledWith({ step: "complete", recoveryProof: "123456", password: "replacement-password" });
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("新密码")).toHaveValue("");
    expect(screen.getByLabelText("确认新密码")).toHaveValue("");
  });

  it("clears recovery secrets when password confirmation does not match", () => {
    const complete = vi.fn();
    render(<AuthRecoveryBlock {...common} step="complete" actions={{ complete }} />);
    enter(screen.getByLabelText("验证码"), "recovery-proof");
    enter(screen.getByLabelText("新密码"), "replacement-password");
    enter(screen.getByLabelText("确认新密码"), "different-password");
    fireEvent.click(screen.getByRole("button", { name: "更新密码" }));
    expect(complete).not.toHaveBeenCalled();
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("新密码")).toHaveValue("");
    expect(screen.getByLabelText("确认新密码")).toHaveValue("");
  });

  it("clears registration and recovery secrets if their actions disappear during submit", () => {
    const registerComplete = vi.fn();
    const registerActions = { complete: registerComplete };
    const register = render(<AuthRegisterBlock {...common} step="complete" agreementLabel="同意协议" actions={registerActions} />);
    enter(screen.getByLabelText("验证码"), "register-race-proof");
    enter(screen.getByLabelText("显示名称"), "保留名称");
    enter(screen.getByLabelText("密码"), "register-race-password");
    enter(screen.getByLabelText("确认密码"), "register-race-password");
    fireEvent.click(screen.getByLabelText("同意协议"));
    Reflect.deleteProperty(registerActions, "complete");
    fireEvent.click(screen.getByRole("button", { name: "注册" }));
    expect(registerComplete).not.toHaveBeenCalled();
    expect(screen.queryByLabelText("验证码")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("密码")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("确认密码")).not.toBeInTheDocument();
    register.unmount();

    const recoveryComplete = vi.fn();
    const recoveryActions = { complete: recoveryComplete };
    render(<AuthRecoveryBlock {...common} step="complete" actions={recoveryActions} />);
    enter(screen.getByLabelText("验证码"), "recovery-race-proof");
    enter(screen.getByLabelText("新密码"), "recovery-race-password");
    enter(screen.getByLabelText("确认新密码"), "recovery-race-password");
    Reflect.deleteProperty(recoveryActions, "complete");
    fireEvent.click(screen.getByRole("button", { name: "更新密码" }));
    expect(recoveryComplete).not.toHaveBeenCalled();
    expect(screen.queryByLabelText("验证码")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("新密码")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("确认新密码")).not.toBeInTheDocument();
  });

  it("clears registration and recovery secrets when a submit arrives while busy", () => {
    const registerComplete = vi.fn();
    const register = render(<AuthRegisterBlock {...common} step="complete" agreementLabel="同意协议" actions={{ complete: registerComplete }} />);
    enter(screen.getByLabelText("验证码"), "busy-register-proof");
    enter(screen.getByLabelText("显示名称"), "忙碌时保留");
    enter(screen.getByLabelText("密码"), "busy-register-password");
    enter(screen.getByLabelText("确认密码"), "busy-register-password");
    register.rerender(<AuthRegisterBlock state="submitting" step="complete" agreementLabel="同意协议" actions={{ complete: registerComplete }} />);
    fireEvent.submit(screen.getByLabelText("验证码").closest("form")!);
    expect(registerComplete).not.toHaveBeenCalled();
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("密码")).toHaveValue("");
    expect(screen.getByLabelText("确认密码")).toHaveValue("");
    expect(screen.getByLabelText("显示名称")).toHaveValue("忙碌时保留");
    register.unmount();

    const recoveryComplete = vi.fn();
    const recovery = render(<AuthRecoveryBlock {...common} step="complete" actions={{ complete: recoveryComplete }} />);
    enter(screen.getByLabelText("验证码"), "busy-recovery-proof");
    enter(screen.getByLabelText("新密码"), "busy-recovery-password");
    enter(screen.getByLabelText("确认新密码"), "busy-recovery-password");
    recovery.rerender(<AuthRecoveryBlock state="submitting" step="complete" actions={{ complete: recoveryComplete }} />);
    fireEvent.submit(screen.getByLabelText("验证码").closest("form")!);
    expect(recoveryComplete).not.toHaveBeenCalled();
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("新密码")).toHaveValue("");
    expect(screen.getByLabelText("确认新密码")).toHaveValue("");
  });

  it("hides recovery forms without their exact actions while preserving cancel and resend", () => {
    const cancel = vi.fn();
    const resend = vi.fn();
    const view = render(<AuthRecoveryBlock {...common} step="identify" actions={{ cancel }} />);
    expect(screen.queryByLabelText("邮箱或手机号")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "继续" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(cancel).toHaveBeenCalledTimes(1);

    view.rerender(<AuthRecoveryBlock {...common} step="complete" actions={{ cancel, resend }} />);
    expect(screen.queryByLabelText("验证码")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("新密码")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "更新密码" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "重新发送" }));
    expect(resend).toHaveBeenCalledTimes(1);
  });

  it("clears registration secrets before returning to login", () => {
    const openLogin = vi.fn();
    render(<AuthRegisterBlock {...common} step="complete" agreementLabel="同意协议" actions={{ complete: noop, openLogin }} />);
    enter(screen.getByLabelText("验证码"), "register-navigation-proof");
    enter(screen.getByLabelText("密码"), "register-navigation-secret");
    fireEvent.click(screen.getByRole("button", { name: "返回登录" }));
    expect(openLogin).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("密码")).toHaveValue("");
  });

  it("clears registration and recovery secrets when their primary actions are revoked", () => {
    const register = render(<AuthRegisterBlock {...common} step="complete" agreementLabel="同意协议" actions={{ complete: noop, cancel: noop }} />);
    enter(screen.getByLabelText("验证码"), "revoked-register-proof");
    enter(screen.getByLabelText("密码"), "revoked-register-password");
    register.rerender(<AuthRegisterBlock {...common} step="complete" agreementLabel="同意协议" actions={{ cancel: noop }} />);
    expect(screen.queryByDisplayValue("revoked-register-proof")).not.toBeInTheDocument();
    register.unmount();

    const recovery = render(<AuthRecoveryBlock {...common} step="complete" actions={{ complete: noop, cancel: noop }} />);
    enter(screen.getByLabelText("验证码"), "revoked-recovery-proof");
    enter(screen.getByLabelText("新密码"), "revoked-recovery-password");
    recovery.rerender(<AuthRecoveryBlock {...common} step="complete" actions={{ cancel: noop }} />);
    expect(screen.queryByDisplayValue("revoked-recovery-proof")).not.toBeInTheDocument();
  });

  it.each(["loading", "empty", "disabled", "success"] as const)("does not restore registration or recovery secrets after entering %s", (state) => {
    const register = render(<AuthRegisterBlock {...common} step="complete" agreementLabel="同意协议" actions={{ complete: noop }} />);
    enter(screen.getByLabelText("验证码"), "hidden-register-proof");
    enter(screen.getByLabelText("密码"), "hidden-register-password");
    register.rerender(<AuthRegisterBlock state={state} step="complete" agreementLabel="同意协议" actions={{ complete: noop }} />);
    register.rerender(<AuthRegisterBlock {...common} step="complete" agreementLabel="同意协议" actions={{ complete: noop }} />);
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("密码")).toHaveValue("");
    register.unmount();

    const recovery = render(<AuthRecoveryBlock {...common} step="complete" actions={{ complete: noop }} />);
    enter(screen.getByLabelText("验证码"), "hidden-recovery-proof");
    enter(screen.getByLabelText("新密码"), "hidden-recovery-password");
    recovery.rerender(<AuthRecoveryBlock state={state} step="complete" actions={{ complete: noop }} />);
    recovery.rerender(<AuthRecoveryBlock {...common} step="complete" actions={{ complete: noop }} />);
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("新密码")).toHaveValue("");
    recovery.unmount();
  });

  it("does not retain registration or recovery secrets across unmount and remount", () => {
    const register = render(<AuthRegisterBlock {...common} step="complete" agreementLabel="同意协议" actions={{ complete: noop }} />);
    enter(screen.getByLabelText("验证码"), "remount-register-proof");
    enter(screen.getByLabelText("密码"), "remount-register-password");
    register.unmount();
    const remountedRegister = render(<AuthRegisterBlock {...common} step="complete" agreementLabel="同意协议" actions={{ complete: noop }} />);
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("密码")).toHaveValue("");
    remountedRegister.unmount();

    const recovery = render(<AuthRecoveryBlock {...common} step="complete" actions={{ complete: noop }} />);
    enter(screen.getByLabelText("验证码"), "remount-recovery-proof");
    enter(screen.getByLabelText("新密码"), "remount-recovery-password");
    recovery.unmount();
    render(<AuthRecoveryBlock {...common} step="complete" actions={{ complete: noop }} />);
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("新密码")).toHaveValue("");
    expect(screen.getByLabelText("确认新密码")).toHaveValue("");
  });

  it("clears recovery secrets before resending a proof", () => {
    const resend = vi.fn();
    render(<AuthRecoveryBlock {...common} step="complete" actions={{ complete: noop, resend }} />);
    enter(screen.getByLabelText("验证码"), "stale-recovery-proof");
    enter(screen.getByLabelText("新密码"), "stale-recovery-password");
    enter(screen.getByLabelText("确认新密码"), "stale-recovery-password");
    fireEvent.click(screen.getByRole("button", { name: "重新发送" }));
    expect(resend).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(screen.getByLabelText("新密码")).toHaveValue("");
    expect(screen.getByLabelText("确认新密码")).toHaveValue("");
  });
});

describe("account.center and account.profile", () => {
  it("renders only delivered Account entries", () => {
    render(<AccountCenterBlock {...common} user={{ displayName: "账号用户", maskedIdentifier: "u***@example.com" }} security={{ passwordConfigured: true, activeSessionCount: 2, externalIdentityCount: 0 }} actions={{ openProfile: noop, openSecurity: noop }} />);
    expect(screen.getByRole("button", { name: /个人资料/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /账号安全/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "退出当前账号" })).not.toBeInTheDocument();
    expect(screen.queryByText(/权益|设备|订单|通知/)).not.toBeInTheDocument();
  });

  it("renders profile-only and security-only center action subsets", () => {
    const centerProps = { ...common, user: { displayName: "账号用户" }, security: { passwordConfigured: true, activeSessionCount: 2, externalIdentityCount: 0 } };
    const view = render(<AccountCenterBlock {...centerProps} actions={{ openProfile: noop }} />);
    expect(screen.getByRole("button", { name: /个人资料/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /账号安全/ })).not.toBeInTheDocument();
    view.rerender(<AccountCenterBlock {...centerProps} actions={{ openSecurity: noop }} />);
    expect(screen.queryByRole("button", { name: /个人资料/ })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /账号安全/ })).toBeInTheDocument();
  });

  it("hides profile and security entries when their actions are absent", () => {
    render(<AccountCenterBlock {...common} user={{ displayName: "只读账号" }} security={{ passwordConfigured: false, activeSessionCount: 1, externalIdentityCount: 0 }} actions={{}} />);
    expect(screen.queryByLabelText("个人中心功能")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /个人资料/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /账号安全/ })).not.toBeInTheDocument();
  });

  it("renders sign out only when a real action is supplied", () => {
    const signOut = vi.fn();
    render(<AccountCenterBlock {...common} user={{ displayName: "账号用户" }} security={{ passwordConfigured: false, activeSessionCount: 1, externalIdentityCount: 0 }} actions={{ openProfile: noop, openSecurity: noop, signOut }} />);
    fireEvent.click(screen.getByRole("button", { name: "退出当前账号" }));
    expect(signOut).toHaveBeenCalledTimes(1);
  });

  it("submits a versioned profile patch through typed actions", async () => {
    const submit = vi.fn();
    render(<AccountProfileBlock {...common} value={{ displayName: "旧名称", locale: "zh-CN", timezone: "Asia/Shanghai", version: 7 }} actions={{ submit }} />);
    enter(screen.getByLabelText("显示名称"), "新名称");
    fireEvent.click(screen.getByRole("button", { name: "保存资料" }));
    expect(submit).toHaveBeenCalledWith({ displayName: "新名称", locale: "zh-CN", timezone: "Asia/Shanghai", expectedVersion: 7 });
  });

  it("keeps profile read-only and hides save when submit is absent", () => {
    render(<AccountProfileBlock {...common} value={{ displayName: "只读名称", locale: "zh-CN", timezone: "Asia/Shanghai", version: 3 }} actions={{}} />);
    expect(screen.getByLabelText("显示名称")).toBeDisabled();
    expect(screen.getByLabelText("语言")).toBeDisabled();
    expect(screen.getByLabelText("时区")).toBeDisabled();
    expect(screen.queryByRole("button", { name: "保存资料" })).not.toBeInTheDocument();
  });

  it("keeps unsaved profile fields across an equal-value failed rerender and syncs a real version change", () => {
    const submit = vi.fn();
    const original = { displayName: "旧名称", locale: "zh-CN", timezone: "Asia/Shanghai", version: 7 };
    const view = render(<AccountProfileBlock {...common} value={original} actions={{ submit }} />);
    enter(screen.getByLabelText("显示名称"), "未保存名称");
    enter(screen.getByLabelText("语言"), "en-US");
    view.rerender(<AccountProfileBlock state="failed" error={{ code: "version_conflict", message: "请修正资料", retryable: false }} fieldErrors={{ displayName: "名称已被使用" }} value={{ ...original }} actions={{ submit }} />);
    expect(screen.getByLabelText("显示名称")).toHaveValue("未保存名称");
    expect(screen.getByLabelText("语言")).toHaveValue("en-US");
    expect(screen.getByLabelText("显示名称")).toHaveAccessibleDescription("名称已被使用");

    view.rerender(<AccountProfileBlock {...common} value={{ displayName: "服务端名称", locale: "zh-CN", timezone: "UTC", version: 8 }} actions={{ submit }} />);
    expect(screen.getByLabelText("显示名称")).toHaveValue("服务端名称");
    expect(screen.getByLabelText("时区")).toHaveValue("UTC");
  });
});

describe("account.security", () => {
  const sessions = [
    { id: "session-current", deviceLabel: "当前浏览器", authenticationMethod: "password", applicationLabel: "Web", lastSeenLabel: "刚刚", expiresLabel: "7 天后", current: true, revoked: false },
    { id: "session-other", deviceLabel: "桌面客户端", authenticationMethod: "password", applicationLabel: "Desktop", environmentLabel: "production", lastSeenLabel: "昨天", expiresLabel: "6 天后", current: false, revoked: false },
  ];

  it("clears passwords after submit and confirms scoped session revocation", async () => {
    const changePassword = vi.fn();
    const revokeSession = vi.fn();
    render(<AccountSecurityBlock {...common} sessions={sessions} externalIdentities={[]} passwordChangeAllowed actions={{ changePassword, revokeSession, revokeAllOtherSessions: noop }} />);
    expect(screen.queryByRole("heading", { name: "外部身份" })).not.toBeInTheDocument();
    enter(screen.getByLabelText("当前密码"), "old-password");
    enter(screen.getByLabelText("新密码"), "new-password-value");
    enter(screen.getByLabelText("确认新密码"), "new-password-value");
    fireEvent.click(screen.getByRole("button", { name: "更新密码" }));
    expect(changePassword).toHaveBeenCalledWith({ currentPassword: "old-password", newPassword: "new-password-value" });
    expect(screen.getByLabelText("当前密码")).toHaveValue("");
    expect(screen.getByLabelText("新密码")).toHaveValue("");

    const sessionRow = screen.getByText("桌面客户端").closest("li")!;
    const revokeButton = within(sessionRow).getByRole("button", { name: "撤销" });
    fireEvent.click(revokeButton);
    const dialog = screen.getByRole("dialog", { name: "确认危险操作" });
    expect(dialog).toHaveFocus();
    expect(dialog).toHaveTextContent("桌面客户端");
    fireEvent.click(within(dialog).getByRole("button", { name: "确认执行" }));
    expect(revokeSession).toHaveBeenCalledWith("session-other");
  });

  it("clears all passwords after a mismatched password-change submission", () => {
    const changePassword = vi.fn();
    render(<AccountSecurityBlock {...common} sessions={[]} passwordChangeAllowed actions={{ changePassword }} />);
    enter(screen.getByLabelText("当前密码"), "old-password");
    enter(screen.getByLabelText("新密码"), "new-password");
    enter(screen.getByLabelText("确认新密码"), "different-password");
    fireEvent.click(screen.getByRole("button", { name: "更新密码" }));
    expect(changePassword).not.toHaveBeenCalled();
    expect(screen.getByLabelText("当前密码")).toHaveValue("");
    expect(screen.getByLabelText("新密码")).toHaveValue("");
    expect(screen.getByLabelText("确认新密码")).toHaveValue("");
  });

  it("clears all passwords if the password-change action disappears during submit", () => {
    const changePassword = vi.fn();
    const actions = { changePassword };
    render(<AccountSecurityBlock {...common} sessions={[]} passwordChangeAllowed actions={actions} />);
    enter(screen.getByLabelText("当前密码"), "old-race-password");
    enter(screen.getByLabelText("新密码"), "new-race-password");
    enter(screen.getByLabelText("确认新密码"), "new-race-password");
    Reflect.deleteProperty(actions, "changePassword");
    fireEvent.click(screen.getByRole("button", { name: "更新密码" }));
    expect(changePassword).not.toHaveBeenCalled();
    expect(screen.queryByLabelText("当前密码")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("新密码")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("确认新密码")).not.toBeInTheDocument();
  });

  it("clears all passwords when a password-change submit arrives while busy", () => {
    const changePassword = vi.fn();
    const view = render(<AccountSecurityBlock {...common} sessions={[]} passwordChangeAllowed actions={{ changePassword }} />);
    enter(screen.getByLabelText("当前密码"), "busy-old-password");
    enter(screen.getByLabelText("新密码"), "busy-new-password");
    enter(screen.getByLabelText("确认新密码"), "busy-new-password");
    view.rerender(<AccountSecurityBlock state="submitting" sessions={[]} passwordChangeAllowed actions={{ changePassword }} />);
    fireEvent.submit(screen.getByLabelText("当前密码").closest("form")!);
    expect(changePassword).not.toHaveBeenCalled();
    expect(screen.getByLabelText("当前密码")).toHaveValue("");
    expect(screen.getByLabelText("新密码")).toHaveValue("");
    expect(screen.getByLabelText("确认新密码")).toHaveValue("");
  });

  it("renders a Provider identity only when supplied and unlink is actionable", async () => {
    const unlinkIdentity = vi.fn();
    render(<AccountSecurityBlock {...common} sessions={[]} externalIdentities={[{ id: "identity-wechat", providerLabel: "微信", subjectMasked: "wx_***", unlinkAllowed: true }]} passwordChangeAllowed={false} actions={{ unlinkIdentity }} />);
    fireEvent.click(screen.getByRole("button", { name: "解除绑定" }));
    fireEvent.click(within(screen.getByRole("dialog", { name: "确认危险操作" })).getByRole("button", { name: "确认执行" }));
    expect(unlinkIdentity).toHaveBeenCalledWith("identity-wechat");
  });

  it("fails closed when an identity allows unlink but no unlink action is supplied", () => {
    render(<AccountSecurityBlock {...common} sessions={[]} externalIdentities={[{ id: "identity-wechat", providerLabel: "微信", subjectMasked: "wx_***", unlinkAllowed: true }]} passwordChangeAllowed={false} actions={{}} />);
    expect(screen.getByText("微信")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "解除绑定" })).not.toBeInTheDocument();
  });

  it("keeps security cancel independent from password-change capability", () => {
    const cancel = vi.fn();
    render(<AccountSecurityBlock {...common} sessions={[]} passwordChangeAllowed={false} actions={{ cancel }} />);
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it("clears passwords when password-change capability or action is revoked", () => {
    const changePassword = vi.fn();
    const view = render(<AccountSecurityBlock {...common} sessions={[]} passwordChangeAllowed actions={{ changePassword }} />);
    enter(screen.getByLabelText("当前密码"), "old-secret");
    enter(screen.getByLabelText("新密码"), "new-secret");
    view.rerender(<AccountSecurityBlock {...common} sessions={[]} passwordChangeAllowed={false} actions={{ changePassword }} />);
    view.rerender(<AccountSecurityBlock {...common} sessions={[]} passwordChangeAllowed actions={{ changePassword }} />);
    expect(screen.getByLabelText("当前密码")).toHaveValue("");
    expect(screen.getByLabelText("新密码")).toHaveValue("");

    enter(screen.getByLabelText("当前密码"), "second-old-secret");
    view.rerender(<AccountSecurityBlock {...common} sessions={[]} passwordChangeAllowed actions={{}} />);
    view.rerender(<AccountSecurityBlock {...common} sessions={[]} passwordChangeAllowed actions={{ changePassword }} />);
    expect(screen.getByLabelText("当前密码")).toHaveValue("");
  });

  it.each(["disabled", "empty"] as const)("clears passwords when the block enters %s and does not restore them", (state) => {
    const changePassword = vi.fn();
    const view = render(<AccountSecurityBlock {...common} sessions={[]} passwordChangeAllowed actions={{ changePassword }} />);
    enter(screen.getByLabelText("当前密码"), `${state}-current-secret`);
    enter(screen.getByLabelText("新密码"), `${state}-new-secret`);
    enter(screen.getByLabelText("确认新密码"), `${state}-new-secret`);
    view.rerender(<AccountSecurityBlock state={state} sessions={[]} passwordChangeAllowed actions={{ changePassword }} />);
    expect(screen.queryByLabelText("当前密码")).not.toBeInTheDocument();
    view.rerender(<AccountSecurityBlock {...common} sessions={[]} passwordChangeAllowed actions={{ changePassword }} />);
    expect(screen.getByLabelText("当前密码")).toHaveValue("");
    expect(screen.getByLabelText("新密码")).toHaveValue("");
    expect(screen.getByLabelText("确认新密码")).toHaveValue("");
  });

  it("revalidates a removed, current, or revoked session at confirmation time", () => {
    for (const change of ["removed", "current", "revoked"] as const) {
      const revokeSession = vi.fn();
      const session = { id: "session-race", deviceLabel: "竞态会话", authenticationMethod: "password", applicationLabel: "Web", lastSeenLabel: "刚刚", expiresLabel: "1 天后", current: false, revoked: false };
      const projectedSessions = [session];
      const view = render(<AccountSecurityBlock {...common} sessions={projectedSessions} passwordChangeAllowed={false} actions={{ revokeSession }} />);
      fireEvent.click(screen.getByRole("button", { name: "撤销" }));
      if (change === "removed") projectedSessions.splice(0, 1);
      else session[change] = true;
      fireEvent.click(within(screen.getByRole("dialog", { name: "确认危险操作" })).getByRole("button", { name: "确认执行" }));
      expect(revokeSession).not.toHaveBeenCalled();
      expect(screen.queryByRole("dialog", { name: "确认危险操作" })).not.toBeInTheDocument();
      view.unmount();
    }
  });

  it("revalidates identity unlink permission at confirmation time", () => {
    const unlinkIdentity = vi.fn();
    const identity = { id: "identity-race", providerLabel: "竞态身份", subjectMasked: "id_***", unlinkAllowed: true };
    render(<AccountSecurityBlock {...common} sessions={[]} externalIdentities={[identity]} passwordChangeAllowed={false} actions={{ unlinkIdentity }} />);
    fireEvent.click(screen.getByRole("button", { name: "解除绑定" }));
    identity.unlinkAllowed = false;
    fireEvent.click(within(screen.getByRole("dialog", { name: "确认危险操作" })).getByRole("button", { name: "确认执行" }));
    expect(unlinkIdentity).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog", { name: "确认危险操作" })).not.toBeInTheDocument();
  });

  it("revalidates that revoke-all still has an eligible target at confirmation time", () => {
    const revokeAllOtherSessions = vi.fn();
    const session = { id: "session-race-all", deviceLabel: "其他会话", authenticationMethod: "password", applicationLabel: "Web", lastSeenLabel: "刚刚", expiresLabel: "1 天后", current: false, revoked: false };
    render(<AccountSecurityBlock {...common} sessions={[session]} passwordChangeAllowed={false} actions={{ revokeAllOtherSessions }} />);
    fireEvent.click(screen.getByRole("button", { name: "撤销其他全部会话" }));
    session.current = true;
    fireEvent.click(within(screen.getByRole("dialog", { name: "确认危险操作" })).getByRole("button", { name: "确认执行" }));
    expect(revokeAllOtherSessions).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog", { name: "确认危险操作" })).not.toBeInTheDocument();
  });

  it("renders password, single revoke, and revoke-all controls only for their exact actions", () => {
    const changePassword = vi.fn();
    const revokeSession = vi.fn();
    const revokeAllOtherSessions = vi.fn();
    const view = render(<AccountSecurityBlock {...common} sessions={sessions} passwordChangeAllowed actions={{}} />);
    expect(screen.queryByRole("button", { name: "更新密码" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "撤销" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "撤销其他全部会话" })).not.toBeInTheDocument();
    expect(screen.getByText("已登录")).toBeInTheDocument();

    view.rerender(<AccountSecurityBlock {...common} sessions={sessions} passwordChangeAllowed actions={{ changePassword }} />);
    expect(screen.getByRole("button", { name: "更新密码" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "撤销" })).not.toBeInTheDocument();

    view.rerender(<AccountSecurityBlock {...common} sessions={sessions} passwordChangeAllowed actions={{ revokeSession }} />);
    expect(screen.queryByRole("button", { name: "更新密码" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "撤销" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "撤销其他全部会话" })).not.toBeInTheDocument();

    view.rerender(<AccountSecurityBlock {...common} sessions={sessions} passwordChangeAllowed actions={{ revokeAllOtherSessions }} />);
    expect(screen.queryByRole("button", { name: "撤销" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "撤销其他全部会话" })).toBeInTheDocument();
  });
});
