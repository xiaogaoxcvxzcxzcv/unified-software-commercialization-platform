import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AccountCenterBlock, AccountProfileBlock, AccountSecurityBlock, AuthLoginBlock, AuthRecoveryBlock, AuthRegisterBlock } from "../web-react/src/account/index.js";
import type { AccountBlockState } from "../web-react/src/account/index.js";

const noop = () => {};
const enter = (control: HTMLElement, value: string) => fireEvent.change(control, { target: { value } });
afterEach(cleanup);

describe("Account Block accessible state contract", () => {
  it("exposes all eight public states with text, busy state, and live regions", () => {
    const cases: ReadonlyArray<{ state: AccountBlockState; text: string; role?: "alert" | "status" }> = [
      { state: "idle", text: "邮箱或手机号" },
      { state: "loading", text: "正在加载" },
      { state: "ready", text: "邮箱或手机号" },
      { state: "submitting", text: "正在提交", role: "status" },
      { state: "success", text: "认证完成", role: "status" },
      { state: "empty", text: "暂无认证方式" },
      { state: "failed", text: "认证暂时不可用", role: "alert" },
      { state: "disabled", text: "账号能力已关闭" },
    ];
    for (const item of cases) {
      const view = render(<AuthLoginBlock state={item.state} successMessage="认证完成" emptyMessage="暂无认证方式" disabledMessage="账号能力已关闭" error={{ code: "temporarily_unavailable", message: "认证暂时不可用", retryable: true }} onRetry={noop} actions={{ submit: noop }} />);
      const region = screen.getByRole("region", { name: "登录" });
      expect(region).toHaveAttribute("data-state", item.state);
      expect(within(region).getAllByText(item.text, { exact: false }).length).toBeGreaterThan(0);
      if (item.role) expect(within(region).getByRole(item.role)).toBeInTheDocument();
      if (item.state === "loading" || item.state === "submitting") expect(region.querySelector('[aria-busy="true"]')).not.toBeNull();
      view.unmount();
    }
  });

  it("binds field errors with aria-describedby and keeps retry keyboard-focusable", () => {
    const retry = vi.fn();
    const view = render(<AuthLoginBlock state="ready" fieldErrors={{ identifier: "请输入有效邮箱" }} actions={{ submit: noop }} />);
    const field = screen.getByLabelText("邮箱或手机号");
    expect(field).toHaveAttribute("aria-invalid", "true");
    expect(field).toHaveAccessibleDescription("请输入有效邮箱");
    view.rerender(<AuthLoginBlock state="failed" error={{ code: "temporarily_unavailable", message: "认证暂时不可用", retryable: true }} fieldErrors={{ identifier: "请修正邮箱" }} onRetry={retry} actions={{ submit: noop }} />);
    expect(screen.getByLabelText("邮箱或手机号")).toHaveAccessibleDescription("请修正邮箱");
    const button = screen.getByRole("button", { name: "重试" });
    button.focus();
    expect(button).toHaveFocus();
    fireEvent.click(button);
    expect(retry).toHaveBeenCalledTimes(1);
  });

  it("preserves failed registration and recovery fields with accessible errors", () => {
    const completeRegistration = vi.fn();
    const registration = render(<AuthRegisterBlock state="ready" step="complete" agreementLabel="同意协议" actions={{ complete: completeRegistration }} />);
    enter(screen.getByLabelText("验证码"), "registration-proof");
    enter(screen.getByLabelText("显示名称"), "未保存名称");
    enter(screen.getByLabelText("密码"), "registration-password");
    registration.rerender(<AuthRegisterBlock state="failed" error={{ code: "invalid_registration", message: "请修正注册资料", retryable: false }} step="complete" agreementLabel="同意协议" fieldErrors={{ verificationProof: "验证码无效", displayName: "名称不可用" }} actions={{ complete: completeRegistration }} />);
    expect(screen.getByLabelText("验证码")).toHaveValue("registration-proof");
    expect(screen.getByLabelText("验证码")).toHaveAccessibleDescription("验证码无效");
    expect(screen.getByLabelText("显示名称")).toHaveValue("未保存名称");
    expect(screen.getByLabelText("显示名称")).toHaveAccessibleDescription("名称不可用");
    registration.unmount();

    const completeRecovery = vi.fn();
    const recovery = render(<AuthRecoveryBlock state="ready" step="complete" actions={{ complete: completeRecovery }} />);
    enter(screen.getByLabelText("验证码"), "recovery-proof");
    enter(screen.getByLabelText("新密码"), "recovery-password");
    recovery.rerender(<AuthRecoveryBlock state="failed" error={{ code: "invalid_recovery", message: "请修正找回资料", retryable: false }} step="complete" fieldErrors={{ recoveryProof: "验证码已过期", password: "密码强度不足" }} actions={{ complete: completeRecovery }} />);
    expect(screen.getByLabelText("验证码")).toHaveValue("recovery-proof");
    expect(screen.getByLabelText("验证码")).toHaveAccessibleDescription("验证码已过期");
    expect(screen.getByLabelText("新密码")).toHaveValue("recovery-password");
    expect(screen.getByLabelText("新密码")).toHaveAccessibleDescription("密码强度不足");
  });

  it("preserves failed profile and security fields with accessible errors", () => {
    const submitProfile = vi.fn();
    const profileValue = { displayName: "旧名称", locale: "zh-CN", timezone: "Asia/Shanghai", version: 4 };
    const profile = render(<AccountProfileBlock state="ready" value={profileValue} actions={{ submit: submitProfile }} />);
    enter(screen.getByLabelText("显示名称"), "未保存名称");
    profile.rerender(<AccountProfileBlock state="failed" error={{ code: "version_conflict", message: "资料冲突", retryable: false }} value={{ ...profileValue }} fieldErrors={{ displayName: "名称已被占用" }} actions={{ submit: submitProfile }} />);
    expect(screen.getByLabelText("显示名称")).toHaveValue("未保存名称");
    expect(screen.getByLabelText("显示名称")).toHaveAccessibleDescription("名称已被占用");
    profile.unmount();

    const changePassword = vi.fn();
    const security = render(<AccountSecurityBlock state="ready" sessions={[]} passwordChangeAllowed actions={{ changePassword }} />);
    enter(screen.getByLabelText("当前密码"), "current-password");
    enter(screen.getByLabelText("新密码"), "new-password");
    security.rerender(<AccountSecurityBlock state="failed" error={{ code: "invalid_password", message: "请修正密码", retryable: false }} sessions={[]} passwordChangeAllowed passwordFieldErrors={{ currentPassword: "当前密码错误", newPassword: "新密码过弱" }} actions={{ changePassword }} />);
    expect(screen.getByLabelText("当前密码")).toHaveValue("current-password");
    expect(screen.getByLabelText("当前密码")).toHaveAccessibleDescription("当前密码错误");
    expect(screen.getByLabelText("新密码")).toHaveValue("new-password");
    expect(screen.getByLabelText("新密码")).toHaveAccessibleDescription("新密码过弱");
  });
});

describe("Account auth keyboard and sensitive-value boundaries", () => {
  it("submits a focused form with the keyboard submit path and clears the password", () => {
    const submit = vi.fn();
    render(<AuthLoginBlock state="ready" actions={{ submit }} />);
    enter(screen.getByLabelText("邮箱或手机号"), "person@example.com");
    enter(screen.getByLabelText("密码"), "keyboard-submit-secret");
    const password = screen.getByLabelText("密码");
    password.focus();
    fireEvent.submit(password.closest("form")!);
    expect(submit).toHaveBeenCalledWith({ identifier: "person@example.com", password: "keyboard-submit-secret" });
    expect(password).toHaveValue("");
    expect(document.body).not.toHaveTextContent("keyboard-submit-secret");
  });

  it("keeps cancel as a focusable button and clears registration passwords", () => {
    const cancel = vi.fn();
    render(<AuthRegisterBlock state="ready" step="complete" agreementLabel="同意协议" actions={{ complete: noop, cancel }} />);
    enter(screen.getByLabelText("验证码"), "registration-proof");
    enter(screen.getByLabelText("密码"), "registration-secret");
    enter(screen.getByLabelText("确认密码"), "registration-secret");
    const button = screen.getByRole("button", { name: "取消" });
    button.focus();
    expect(button).toHaveFocus();
    fireEvent.click(button);
    expect(cancel).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("密码")).toHaveValue("");
    expect(screen.getByLabelText("确认密码")).toHaveValue("");
    expect(screen.getByLabelText("验证码")).toHaveValue("");
  });

  it("prevents repeated submission while submitting", () => {
    const submit = vi.fn();
    render(<AuthLoginBlock state="submitting" actions={{ submit }} />);
    const form = screen.getByLabelText("密码").closest("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    expect(submit).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "登录" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "登录" })).toHaveAttribute("aria-busy", "true");
  });

  it("hides Provider controls when the Provider list or action is unavailable", () => {
    const view = render(<AuthLoginBlock state="ready" providers={[]} actions={{ submit: noop, selectProvider: noop }} />);
    expect(screen.queryByLabelText("其他登录方式")).not.toBeInTheDocument();
    view.rerender(<AuthLoginBlock state="ready" providers={[{ id: "oidc", label: "企业登录" }]} actions={{ submit: noop }} />);
    expect(screen.queryByLabelText("其他登录方式")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "企业登录" })).not.toBeInTheDocument();
  });

  it("does not restore passwords or proof after submit, cancel, unmount, or remount", () => {
    const complete = vi.fn();
    const first = render(<AuthRecoveryBlock state="ready" step="complete" actions={{ complete, cancel: noop }} />);
    enter(screen.getByLabelText("验证码"), "912345");
    enter(screen.getByLabelText("新密码"), "replacement-password");
    enter(screen.getByLabelText("确认新密码"), "replacement-password");
    fireEvent.submit(screen.getByLabelText("验证码").closest("form")!);
    expect(complete).toHaveBeenCalledWith({ step: "complete", recoveryProof: "912345", password: "replacement-password" });
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    enter(screen.getByLabelText("验证码"), "923456");
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    enter(screen.getByLabelText("验证码"), "934567");
    first.unmount();
    render(<AuthRecoveryBlock state="ready" step="complete" actions={{ complete: noop }} />);
    expect(screen.getByLabelText("验证码")).toHaveValue("");
    expect(document.body).not.toHaveTextContent(/912345|923456|934567/);
  });

  it("does not restore a verification proof after leaving and returning to the step", () => {
    const view = render(<AuthRecoveryBlock state="ready" step="complete" actions={{ complete: noop }} />);
    enter(screen.getByLabelText("验证码"), "step-switch-secret");
    view.rerender(<AuthRecoveryBlock state="ready" step="identify" actions={{ requestRecovery: noop }} />);
    expect(screen.queryByDisplayValue("step-switch-secret")).not.toBeInTheDocument();
    view.rerender(<AuthRecoveryBlock state="ready" step="complete" actions={{ complete: noop }} />);
    expect(screen.getByLabelText("验证码")).toHaveValue("");
  });

  it("never copies long sensitive strings into title or data attributes", () => {
    const secret = "sensitive-" + "x".repeat(240);
    render(<AuthLoginBlock state="ready" providers={[{ id: "provider-safe", label: "企业登录" }]} actions={{ submit: noop, selectProvider: noop }} />);
    enter(screen.getByLabelText("密码"), secret);
    for (const element of Array.from(document.body.querySelectorAll("*"))) {
      for (const attribute of Array.from(element.attributes)) {
        if (attribute.name === "title" || attribute.name.startsWith("data-")) expect(attribute.value).not.toContain(secret);
      }
    }
  });
});

describe("Account security confirmation accessibility", () => {
  const sessions = [{ id: "session-other", deviceLabel: "桌面客户端", authenticationMethod: "password", applicationLabel: "Desktop", lastSeenLabel: "昨天", expiresLabel: "6 天后", current: false, revoked: false }];

  it("moves focus into danger confirmation and restores it when cancelled", () => {
    render(<AccountSecurityBlock state="ready" sessions={sessions} passwordChangeAllowed={false} actions={{ revokeSession: noop }} />);
    const trigger = screen.getByRole("button", { name: "撤销" });
    trigger.focus();
    fireEvent.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "确认危险操作" });
    expect(dialog.tagName).toBe("DIALOG");
    expect(dialog).toHaveAttribute("open");
    expect(dialog).toHaveFocus();
    const content = document.querySelector(".account-security-content")!;
    expect(content).not.toHaveAttribute("inert");
    expect(content).not.toHaveAttribute("aria-hidden");
    const cancel = within(dialog).getByRole("button", { name: "取消" });
    cancel.focus();
    fireEvent.click(cancel);
    expect(screen.queryByRole("dialog", { name: "确认危险操作" })).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("uses native showModal and close when the dialog API is available", () => {
    const prototype = HTMLDialogElement.prototype;
    const originalShowModal = Object.getOwnPropertyDescriptor(prototype, "showModal");
    const originalClose = Object.getOwnPropertyDescriptor(prototype, "close");
    const showModal = vi.fn(function (this: HTMLDialogElement) { this.setAttribute("open", ""); });
    const close = vi.fn(function (this: HTMLDialogElement) { this.removeAttribute("open"); });
    Object.defineProperty(prototype, "showModal", { configurable: true, value: showModal });
    Object.defineProperty(prototype, "close", { configurable: true, value: close });
    try {
      const view = render(<AccountSecurityBlock state="ready" sessions={sessions} passwordChangeAllowed={false} actions={{ revokeSession: noop }} />);
      fireEvent.click(screen.getByRole("button", { name: "撤销" }));
      expect(showModal).toHaveBeenCalledTimes(1);
      fireEvent.click(within(screen.getByRole("dialog", { name: "确认危险操作" })).getByRole("button", { name: "取消" }));
      expect(close).toHaveBeenCalledTimes(1);
      view.unmount();
    } finally {
      if (originalShowModal) Object.defineProperty(prototype, "showModal", originalShowModal);
      else delete (prototype as { showModal?: unknown }).showModal;
      if (originalClose) Object.defineProperty(prototype, "close", originalClose);
      else delete (prototype as { close?: unknown }).close;
    }
  });

  it("prevents the native cancel event, closes the dialog, and restores focus", () => {
    render(<AccountSecurityBlock state="ready" sessions={sessions} passwordChangeAllowed={false} actions={{ revokeSession: noop }} />);
    const trigger = screen.getByRole("button", { name: "撤销" });
    trigger.focus();
    fireEvent.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "确认危险操作" });
    const cancelEvent = new Event("cancel", { bubbles: false, cancelable: true });
    fireEvent(dialog, cancelEvent);
    expect(cancelEvent.defaultPrevented).toBe(true);
    expect(screen.queryByRole("dialog", { name: "确认危险操作" })).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("closes an open native dialog exactly once when unmounted", () => {
    const prototype = HTMLDialogElement.prototype;
    const originalShowModal = Object.getOwnPropertyDescriptor(prototype, "showModal");
    const originalClose = Object.getOwnPropertyDescriptor(prototype, "close");
    const showModal = vi.fn(function (this: HTMLDialogElement) { this.setAttribute("open", ""); });
    const close = vi.fn(function (this: HTMLDialogElement) { this.removeAttribute("open"); });
    Object.defineProperty(prototype, "showModal", { configurable: true, value: showModal });
    Object.defineProperty(prototype, "close", { configurable: true, value: close });
    try {
      const view = render(<AccountSecurityBlock state="ready" sessions={sessions} passwordChangeAllowed={false} actions={{ revokeSession: noop }} />);
      fireEvent.click(screen.getByRole("button", { name: "撤销" }));
      expect(showModal).toHaveBeenCalledTimes(1);
      view.unmount();
      expect(close).toHaveBeenCalledTimes(1);
    } finally {
      if (originalShowModal) Object.defineProperty(prototype, "showModal", originalShowModal);
      else delete (prototype as { showModal?: unknown }).showModal;
      if (originalClose) Object.defineProperty(prototype, "close", originalClose);
      else delete (prototype as { close?: unknown }).close;
    }
  });

  it("traps Tab in the danger confirmation and closes with Escape", () => {
    render(<AccountSecurityBlock state="ready" sessions={sessions} passwordChangeAllowed={false} actions={{ revokeSession: noop }} />);
    const trigger = screen.getByRole("button", { name: "撤销" });
    trigger.focus();
    fireEvent.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "确认危险操作" });
    const confirm = within(dialog).getByRole("button", { name: "确认执行" });
    const cancel = within(dialog).getByRole("button", { name: "取消" });
    fireEvent.keyDown(dialog, { key: "Tab" });
    expect(confirm).toHaveFocus();
    fireEvent.keyDown(confirm, { key: "Tab", shiftKey: true });
    expect(cancel).toHaveFocus();
    fireEvent.keyDown(cancel, { key: "Tab" });
    expect(confirm).toHaveFocus();
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: "确认危险操作" })).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("closes confirmation, restores focus, and emits only the selected session ID", () => {
    const revoke = vi.fn();
    render(<AccountSecurityBlock state="ready" sessions={sessions} passwordChangeAllowed={false} actions={{ revokeSession: revoke }} />);
    const trigger = screen.getByRole("button", { name: "撤销" });
    trigger.focus();
    fireEvent.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "确认危险操作" });
    fireEvent.click(within(dialog).getByRole("button", { name: "确认执行" }));
    expect(revoke).toHaveBeenCalledWith("session-other");
    expect(screen.queryByRole("dialog", { name: "确认危险操作" })).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("closes a stale session confirmation when revoke permission is removed", () => {
    const revokeSession = vi.fn();
    const view = render(<AccountSecurityBlock state="ready" sessions={sessions} passwordChangeAllowed={false} actions={{ revokeSession }} />);
    const trigger = screen.getByRole("button", { name: "撤销" });
    trigger.focus();
    fireEvent.click(trigger);
    expect(screen.getByRole("dialog", { name: "确认危险操作" })).toBeInTheDocument();
    view.rerender(<AccountSecurityBlock state="ready" sessions={sessions} passwordChangeAllowed={false} actions={{}} />);
    expect(screen.queryByRole("dialog", { name: "确认危险操作" })).not.toBeInTheDocument();
    const content = document.querySelector<HTMLElement>(".account-security-content")!;
    expect(content).not.toHaveAttribute("inert");
    expect(content).not.toHaveAttribute("aria-hidden");
    expect(content).toHaveFocus();
  });

  it("closes a stale identity confirmation when unlink permission is removed", () => {
    const identities = [{ id: "identity-wechat", providerLabel: "微信", subjectMasked: "wx_***", unlinkAllowed: true }];
    const unlinkIdentity = vi.fn();
    const view = render(<AccountSecurityBlock state="ready" sessions={[]} externalIdentities={identities} passwordChangeAllowed={false} actions={{ unlinkIdentity }} />);
    fireEvent.click(screen.getByRole("button", { name: "解除绑定" }));
    expect(screen.getByRole("dialog", { name: "确认危险操作" })).toBeInTheDocument();
    view.rerender(<AccountSecurityBlock state="ready" sessions={[]} externalIdentities={identities} passwordChangeAllowed={false} actions={{}} />);
    expect(screen.queryByRole("dialog", { name: "确认危险操作" })).not.toBeInTheDocument();
    expect(document.querySelector(".account-security-content")).not.toHaveAttribute("inert");
  });
});

describe("Account permission subset accessibility", () => {
  it("keeps registration and recovery escape actions reachable without fake submit actions", () => {
    const register = render(<AuthRegisterBlock state="ready" step="request_verification" providers={[{ id: "oidc", label: "企业注册" }]} actions={{ openLogin: noop, selectProvider: noop }} />);
    expect(screen.queryByRole("textbox", { name: "邮箱或手机号" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "发送验证码" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "返回登录" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "企业注册" })).toBeInTheDocument();
    register.unmount();

    render(<AuthRecoveryBlock state="ready" step="complete" actions={{ cancel: noop }} />);
    expect(screen.queryByLabelText("验证码")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "更新密码" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "取消" })).toBeInTheDocument();
  });

  it("removes unavailable center actions from the accessibility tree", () => {
    render(<AccountCenterBlock state="ready" user={{ displayName: "只读账号" }} security={{ passwordConfigured: false, activeSessionCount: 1, externalIdentityCount: 0 }} actions={{ close: noop }} />);
    expect(screen.queryByLabelText("个人中心功能")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /个人资料|账号安全|退出当前账号/ })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "关闭" })).toBeInTheDocument();
  });

  it("exposes a disabled read-only profile without a fake submit action", () => {
    render(<AccountProfileBlock state="ready" value={{ displayName: "只读名称", locale: "zh-CN", timezone: "Asia/Shanghai", version: 2 }} actions={{ cancel: noop }} />);
    expect(screen.getByLabelText("显示名称")).toBeDisabled();
    expect(screen.queryByRole("button", { name: "保存资料" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "取消" })).toBeInTheDocument();
  });

  it("keeps unavailable security mutations out of keyboard navigation", () => {
    const sessions = [{ id: "session-other", deviceLabel: "桌面客户端", authenticationMethod: "password", applicationLabel: "Desktop", lastSeenLabel: "昨天", expiresLabel: "6 天后", current: false, revoked: false }];
    render(<AccountSecurityBlock state="ready" sessions={sessions} passwordChangeAllowed actions={{}} />);
    expect(screen.queryByRole("button", { name: /更新密码|撤销/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
