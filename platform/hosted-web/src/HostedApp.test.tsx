import { cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { HostedApp } from "./HostedApp";

const interactionId = "hint_abcdefghijklmnopqrstuvwxyz";
const now = "2026-07-18T00:00:00Z";
const later = "2026-07-18T01:00:00Z";
const href = (route: "auth" | "account") => `https://ui.example.test/ui/v1/${route}?interaction_id=${interactionId}`;

afterEach(cleanup);

describe("HostedApp runtime shell", () => {
  it("opens the hosted auth browser session and renders the server bootstrap", async () => {
    const fetchMock = sequence(json(browser("hosted.auth")), json(authBootstrap()));
    const view = render(<HostedApp href={href("auth")} fetch={fetchMock as typeof fetch} navigate={vi.fn()} />);

    await waitFor(() => expect(view.container.querySelector(".account-block-auth-login")).toHaveAttribute("data-state", "ready"));
    expect(view.container).toHaveTextContent("Example Product");
    expect(fetchMock).toHaveBeenCalledTimes(2);
    for (const [, init] of fetchMock.mock.calls) {
      expect(init).toMatchObject({ credentials: "include", cache: "no-store", redirect: "error" });
    }
  });

  it("restores a server-persisted registration flow after refresh and remount", async () => {
    const fetchMock = sequence(
      json(browser("hosted.auth")),
      json(authBootstrap()),
      json({ kind: "registration_verification", identifier_hint: "p***@example.test" }, 202),
      json(browser("hosted.auth")),
      json(authBootstrap({ flow: { kind: "registration_verification", identifier_hint: "p***@example.test" } })),
    );
    const first = render(<HostedApp href={href("auth")} fetch={fetchMock as typeof fetch} />);
    await waitFor(() => expect(first.getByRole("button", { name: "创建账号" })).toBeInTheDocument());
    fireEvent.click(first.getByRole("button", { name: "创建账号" }));
    const identifier = first.container.querySelector<HTMLInputElement>("#account-register-identifier")!;
    fireEvent.change(identifier, { target: { value: "person@example.test" } });
    fireEvent.submit(identifier.closest("form")!);
    await waitFor(() => expect(first.container.querySelector("#account-register-proof")).toBeInTheDocument());
    expect(first.container.querySelector(".client-sr-only")).toHaveTextContent("注册验证步骤");
    first.unmount();

    const remountFetch = sequence(
      json(browser("hosted.auth")),
      json(authBootstrap({ flow: { kind: "registration_verification", identifier_hint: "p***@example.test" } })),
    );
    const second = render(<HostedApp href={href("auth")} fetch={remountFetch as typeof fetch} />);
    await waitFor(() => expect(second.container.querySelector("#account-register-proof")).toBeInTheDocument());
    await waitFor(() => expect(document.activeElement).toBe(second.container.querySelector("#account-register-proof")));
    expect(second.container.querySelector("#account-register-identifier")).not.toBeInTheDocument();
  });

  it("completes registration without resending identifier or a browser continuation", async () => {
    const completion = { interaction_id: interactionId, status: "completed", return_url: "sample-app://callback?code=registered&state=opaque", expires_at: later };
    const fetchMock = sequence(
      json(browser("hosted.auth")),
      json(authBootstrap({ flow: { kind: "registration_verification", identifier_hint: "p***" } })),
      json(completion),
    );
    const view = render(<HostedApp href={href("auth")} fetch={fetchMock as typeof fetch} navigate={vi.fn()} />);
    await waitFor(() => expect(view.container.querySelector("#account-register-proof")).toBeInTheDocument());
    change(view, "#account-register-proof", "verification-proof-value");
    change(view, "#account-register-name", "Person");
    change(view, "#account-register-password", "new-password-value");
    change(view, "#account-register-confirm", "new-password-value");
    fireEvent.click(view.container.querySelector<HTMLInputElement>('.account-checkbox input[type="checkbox"]')!);
    fireEvent.submit(view.container.querySelector(".account-block-auth-register form")!);
    await waitFor(() => expect(view.getByRole("button", { name: /返回应用/ })).toBeInTheDocument());

    const [, init] = fetchMock.mock.calls[2];
    expect(JSON.parse(String(init?.body))).toEqual({
      credential: "new-password-value",
      verification_proof: "verification-proof-value",
      display_name: "Person",
    });
  });

  it("restores recovery, completes with proof and credential only, and returns to login", async () => {
    const fetchMock = sequence(
      json(browser("hosted.auth")),
      json(authBootstrap({ flow: { kind: "recovery_verification", identifier_hint: "p***@example.test" } })),
      empty(),
      json(authBootstrap()),
      json(browser("hosted.auth")),
      json(authBootstrap()),
    );
    const view = render(<HostedApp href={href("auth")} fetch={fetchMock as typeof fetch} />);
    await waitFor(() => expect(view.container.querySelector("#account-recovery-proof")).toBeInTheDocument());
    expect(view.container).toHaveTextContent("p***@example.test");
    change(view, "#account-recovery-proof", "recovery-proof-value");
    change(view, "#account-recovery-password", "new-password-value");
    change(view, "#account-recovery-confirm", "new-password-value");
    fireEvent.submit(view.container.querySelector(".account-block-auth-recovery form")!);
    await waitFor(() => expect(view.container.querySelector(".account-block-auth-login")).toHaveAttribute("data-state", "ready"));

    const [, init] = fetchMock.mock.calls[2];
    expect(JSON.parse(String(init?.body))).toEqual({ recovery_proof: "recovery-proof-value", new_credential: "new-password-value" });
  });

  it("resets a persisted auth flow with DELETE before returning to login", async () => {
    const fetchMock = sequence(
      json(browser("hosted.auth")),
      json(authBootstrap({ flow: { kind: "registration_verification", identifier_hint: "p***" } })),
      empty(),
      json(authBootstrap()),
      json(browser("hosted.auth")),
      json(authBootstrap()),
    );
    const view = render(<HostedApp href={href("auth")} fetch={fetchMock as typeof fetch} />);
    await waitFor(() => expect(view.getByRole("button", { name: "返回登录" })).toBeInTheDocument());
    fireEvent.click(view.getByRole("button", { name: "返回登录" }));
    await waitFor(() => expect(view.container.querySelector(".account-block-auth-login")).toHaveAttribute("data-state", "ready"));
    expect(fetchMock.mock.calls[2][0]).toContain("/auth/flow");
    expect(fetchMock.mock.calls[2][1]).toMatchObject({ method: "DELETE" });
  });

  it("keeps registration and recovery available when password login is disabled", async () => {
    const fetchMock = sequence(json(browser("hosted.auth")), json(authBootstrap({ password_enabled: false })));
    const view = render(<HostedApp href={href("auth")} fetch={fetchMock as typeof fetch} />);
    await waitFor(() => expect(view.getByRole("button", { name: "创建账号" })).toBeInTheDocument());
    expect(view.getByRole("button", { name: "忘记密码" })).toBeInTheDocument();
    expect(view.container.querySelector("#account-login-identifier")).not.toBeInTheDocument();
    expect(view.container.querySelector("#account-login-password")).not.toBeInTheDocument();
    expect(view.container.querySelector(".account-block-auth-login")).toHaveAttribute("data-state", "ready");
  });

  it("does not expose authenticate actions for a cancel-only auth subset", async () => {
    const cancelOnly = { ...interaction("hosted.auth"), allowed_actions: ["cancel"] };
    const fetchMock = sequence(json(browser("hosted.auth")), json(authBootstrap({ interaction: cancelOnly })));
    const view = render(<HostedApp href={href("auth")} fetch={fetchMock as typeof fetch} />);
    await waitFor(() => expect(view.container.querySelector(".account-block-auth-login")).toHaveAttribute("data-state", "ready"));
    expect(view.container.querySelector("#account-login-identifier")).not.toBeInTheDocument();
    expect(view.queryByRole("button", { name: "创建账号" })).not.toBeInTheDocument();
    expect(view.queryByRole("button", { name: "忘记密码" })).not.toBeInTheDocument();
		expect(view.getByRole("button", { name: "取消" })).toBeInTheDocument();
  });

  it("does not expose a mutation form for a restored flow without authenticate permission", async () => {
    const noActions = { ...interaction("hosted.auth"), allowed_actions: [] };
    const fetchMock = sequence(
      json(browser("hosted.auth")),
      json(authBootstrap({ interaction: noActions, flow: { kind: "registration_verification", identifier_hint: "p***" } })),
    );
    const view = render(<HostedApp href={href("auth")} fetch={fetchMock as typeof fetch} />);
    await waitFor(() => expect(view.container.querySelector(".account-block-auth-register")).toHaveAttribute("data-state", "disabled"));
    expect(view.container.querySelector("#account-register-proof")).not.toBeInTheDocument();
    expect(view.queryByRole("button", { name: "注册" })).not.toBeInTheDocument();
  });

	it.each([
		{ flow: "registration_verification", flag: "registration_enabled", selector: "#account-register-proof", action: "注册" },
		{ flow: "recovery_verification", flag: "recovery_enabled", selector: "#account-recovery-proof", action: "重置密码" },
	] as const)("fails closed when a restored $flow flow loses its capability", async ({ flow, flag, selector, action }) => {
		const fetchMock = sequence(json(browser("hosted.auth")), json(authBootstrap({ flow: { kind: flow, identifier_hint: "p***" }, [flag]: false })));
		const view = render(<HostedApp href={href("auth")} fetch={fetchMock as typeof fetch} />);
		await waitFor(() => expect(view.container.querySelector(".account-block[data-state=\"disabled\"]")).toBeInTheDocument());
		expect(view.container.querySelector(selector)).not.toBeInTheDocument();
		expect(view.queryByRole("button", { name: action })).not.toBeInTheDocument();
		expect(view.container).toHaveTextContent("当前账户能力不可用");
	});

  it("renders account bootstrap with no sessions or identities and gates each mutation", async () => {
    const fetchMock = sequence(
      json(browser("hosted.account")),
      json(accountBootstrap({ sessions: [], external_identities: [], allowed_actions: [] })),
    );
    const view = render(<HostedApp href={href("account")} fetch={fetchMock as typeof fetch} />);
    await waitFor(() => expect(view.container.querySelector(".account-block-account-center")).toHaveAttribute("data-state", "ready"));
    expect(view.container).toHaveTextContent("Server User");
    expect(view.queryByRole("button", { name: /个人资料/ })).not.toBeInTheDocument();
    expect(view.queryByRole("button", { name: /账号安全/ })).not.toBeInTheDocument();
    expect(view.queryByRole("button", { name: "关闭" })).not.toBeInTheDocument();
    expect(view.container).not.toHaveTextContent("修改密码");
    expect(view.queryByRole("button", { name: /当前权益/ })).not.toBeInTheDocument();
  });

  it("shows hosted.account entitlement summary only from the server bootstrap projection", async () => {
    const fetchMock = sequence(
      json(browser("hosted.account")),
      json(accountBootstrap({ entitlement_summary: entitlementSummary() })),
    );
    const view = render(<HostedApp href={href("account")} fetch={fetchMock as typeof fetch} />);
    await waitFor(() => expect(view.getByRole("button", { name: /当前权益/ })).toBeInTheDocument());
    fireEvent.click(view.getByRole("button", { name: /当前权益/ }));
    expect(view.getByRole("heading", { name: "权益摘要" })).toBeInTheDocument();
    expect(view.container).toHaveTextContent("pro");
    expect(view.container).toHaveTextContent("priority_queue");
    expect(view.container).not.toHaveTextContent("￥");
    expect(view.container).not.toHaveTextContent("paid");
  });

  it("exposes only the account workspace allowed-action subset", async () => {
    const fetchMock = sequence(
      json(browser("hosted.account")),
      json(accountBootstrap({ allowed_actions: ["update_profile"] })),
    );
    const view = render(<HostedApp href={href("account")} fetch={fetchMock as typeof fetch} />);
    await waitFor(() => expect(view.getByRole("button", { name: /个人资料/ })).toBeInTheDocument());
    expect(view.queryByRole("button", { name: /账号安全/ })).not.toBeInTheDocument();
    expect(view.queryByRole("button", { name: "关闭" })).not.toBeInTheDocument();
    fireEvent.click(view.getByRole("button", { name: /个人资料/ }));
    expect(view.container.querySelector("#account-profile-name")).toBeInTheDocument();
  });

  it("clears password fields without calling the network when confirmation mismatches", async () => {
    const fetchMock = sequence(
      json(browser("hosted.account")),
      json(accountBootstrap({ allowed_actions: ["change_password"] })),
    );
    const view = render(<HostedApp href={href("account")} fetch={fetchMock as typeof fetch} />);
    await waitFor(() => expect(view.getByRole("button", { name: /账号安全/ })).toBeInTheDocument());
    fireEvent.click(view.getByRole("button", { name: /账号安全/ }));

    const currentPassword = view.container.querySelector<HTMLInputElement>("#account-security-current-password");
    const newPassword = view.container.querySelector<HTMLInputElement>("#account-security-new-password");
    const confirmation = view.container.querySelector<HTMLInputElement>("#account-security-confirm-password");
    expect(currentPassword).not.toBeNull();
    expect(newPassword).not.toBeNull();
    expect(confirmation).not.toBeNull();

    fireEvent.change(currentPassword!, { target: { value: "current-password-value" } });
    fireEvent.change(newPassword!, { target: { value: "new-password-value" } });
    fireEvent.change(confirmation!, { target: { value: "different-password-value" } });
    fireEvent.submit(currentPassword!.closest("form")!);

    expect(currentPassword).toHaveValue("");
    expect(newPassword).toHaveValue("");
    expect(confirmation).toHaveValue("");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

	it("clears stale account actions when the server disables a capability", async () => {
		const fetchMock = sequence(
			json(browser("hosted.account")),
			json(accountBootstrap({ allowed_actions: ["update_profile"] })),
			problem(403, "hosted.capability_not_available", false),
		);
		const view = render(<HostedApp href={href("account")} fetch={fetchMock as typeof fetch} />);
		await waitFor(() => expect(view.getByRole("button", { name: /个人资料/ })).toBeInTheDocument());
		fireEvent.click(view.getByRole("button", { name: /个人资料/ }));
		change(view, "#account-profile-name", "Blocked Update");
		fireEvent.click(view.getByRole("button", { name: "保存资料" }));
		await waitFor(() => expect(view.container).toHaveTextContent("当前账户能力不可用"));
		expect(view.queryByRole("button", { name: "保存资料" })).not.toBeInTheDocument();
		expect(view.container.querySelector("#account-profile-name")).not.toBeInTheDocument();
	});

	it("renders stable API terminal failures without stale workspace actions", async () => {
		const close = vi.fn();
		const view = render(<HostedApp href={href("account")} fetch={sequence(problem(410, "hosted.interaction_expired", false)) as typeof fetch} close={close} />);
		await waitFor(() => expect(view.getByRole("heading", { name: "链接已过期" })).toBeInTheDocument());
		expect(view.queryByRole("button", { name: /个人资料/ })).not.toBeInTheDocument();
		fireEvent.click(view.getByRole("button", { name: /关闭窗口/ }));
		expect(close).toHaveBeenCalledOnce();
	});

	it("refreshes profile inside the controller without rendering a false terminal", async () => {
		const updated = accountBootstrap({ profile: { user_id: "user_test", version: 2, display_name: "Updated User", avatar_url: null, locale: "zh-CN", timezone: "Asia/Shanghai" }, allowed_actions: ["update_profile"] });
		const fetchMock = sequence(json(browser("hosted.account")), json(accountBootstrap({ allowed_actions: ["update_profile"] })), json((updated as { profile: unknown }).profile), json(updated));
		const view = render(<HostedApp href={href("account")} fetch={fetchMock as typeof fetch} />);
		await waitFor(() => expect(view.getByRole("button", { name: /个人资料/ })).toBeInTheDocument());
		fireEvent.click(view.getByRole("button", { name: /个人资料/ }));
		change(view, "#account-profile-name", "Updated User");
		fireEvent.click(view.getByRole("button", { name: "保存资料" }));
		await waitFor(() => expect(view.container.querySelector("#account-profile-name")).toHaveValue("Updated User"));
		expect(view.queryByRole("heading", { name: "操作已完成" })).not.toBeInTheDocument();
	});

  it("shows close-only terminal UI after cancellation without signing out", async () => {
    const cancelled = { ...interaction("hosted.auth"), status: "cancelled", result_kind: "cancelled", allowed_actions: [] };
    const close = vi.fn();
    const fetchMock = sequence(json(browser("hosted.auth")), json(authBootstrap()), json(cancelled));
    const view = render(<HostedApp href={href("auth")} fetch={fetchMock as typeof fetch} close={close} />);
    await waitFor(() => expect(view.getByRole("button", { name: "取消" })).toBeInTheDocument());
    fireEvent.click(view.getByRole("button", { name: "取消" }));
    await waitFor(() => expect(view.getByRole("button", { name: /关闭窗口/ })).toBeInTheDocument());
    expect(view.container).not.toHaveTextContent("退出当前账号");
    fireEvent.click(view.getByRole("button", { name: /关闭窗口/ }));
    expect(close).toHaveBeenCalledOnce();
  });

  it.each([
    ["cancelled", "操作已取消"],
    ["failed", "操作未完成"],
    ["expired", "链接已过期"],
  ] as const)("restores the %s terminal state without requesting business bootstrap", async (status, title) => {
    const terminal = { ...interaction("hosted.account"), status, allowed_actions: [] };
    const fetchMock = sequence(json({ interaction: terminal, csrf_token: "c".repeat(32), browser_session_expires_at: later }));
    const view = render(<HostedApp href={href("account")} fetch={fetchMock as typeof fetch} close={vi.fn()} />);

    await waitFor(() => expect(view.getByRole("heading", { name: title })).toBeInTheDocument());
    expect(view.getByRole("button", { name: /关闭窗口/ })).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("uses only the server completion return URL after completed refresh", async () => {
    const completion = { interaction_id: interactionId, status: "completed", return_url: "sample-app://callback?code=opaque&state=opaque", expires_at: later };
    const completed = { ...interaction("hosted.account"), status: "completed", result_kind: "account_completed", allowed_actions: ["exchange"] };
    const navigate = vi.fn();
    const view = render(<HostedApp href={href("account")} fetch={sequence(json({ interaction: completed, csrf_token: "c".repeat(32), browser_session_expires_at: later, completion })) as typeof fetch} navigate={navigate} />);
    await waitFor(() => expect(view.getByRole("button", { name: /返回应用/ })).toBeInTheDocument());
    expect(navigate).not.toHaveBeenCalled();
    fireEvent.click(view.getByRole("button", { name: /返回应用/ }));
    expect(navigate).toHaveBeenCalledWith(completion.return_url);
  });

  it("shows a close terminal when completed refresh has no return completion", async () => {
    const completed = { ...interaction("hosted.account"), status: "completed", result_kind: "account_completed", allowed_actions: [] };
    const close = vi.fn();
    const fetchMock = sequence(json({ interaction: completed, csrf_token: "c".repeat(32), browser_session_expires_at: later }));
    const view = render(<HostedApp href={href("account")} fetch={fetchMock as typeof fetch} close={close} />);

    await waitFor(() => expect(view.getByRole("heading", { name: "操作已完成" })).toBeInTheDocument());
    fireEvent.click(view.getByRole("button", { name: /关闭窗口/ }));
    expect(close).toHaveBeenCalledOnce();
    expect(fetchMock).toHaveBeenCalledOnce();
  });

  it("renders a real empty state when every authentication method is unavailable", async () => {
    const noActions = { ...interaction("hosted.auth"), allowed_actions: [] };
    const fetchMock = sequence(
      json(browser("hosted.auth")),
      json(authBootstrap({ interaction: noActions, password_enabled: false, registration_enabled: false, recovery_enabled: false, external_providers: [] })),
    );
    const view = render(<HostedApp href={href("auth")} fetch={fetchMock as typeof fetch} />);

    await waitFor(() => expect(view.container.querySelector(".account-block-auth-login")).toHaveAttribute("data-state", "empty"));
    expect(view.container).toHaveTextContent("当前没有可用内容");
  });

  it.each([
    { name: "flags enabled without authenticate", actions: [] as string[], password: true, registration: true, recovery: true, providers: [] },
    { name: "authenticate with provider-only configuration", actions: ["authenticate"], password: false, registration: false, recovery: false, providers: [{ provider: "oidc", mode: "redirect", display_name: "OIDC" }] },
  ])("renders empty for $name", async ({ actions, password, registration, recovery, providers }) => {
    const scoped = { ...interaction("hosted.auth"), allowed_actions: actions };
    const fetchMock = sequence(
      json(browser("hosted.auth")),
      json(authBootstrap({ interaction: scoped, password_enabled: password, registration_enabled: registration, recovery_enabled: recovery, external_providers: providers })),
    );
    const view = render(<HostedApp href={href("auth")} fetch={fetchMock as typeof fetch} />);

    await waitFor(() => expect(view.container.querySelector(".account-block-auth-login")).toHaveAttribute("data-state", "empty"));
    expect(view.container).toHaveTextContent("当前没有可用内容");
  });

  it("fails closed before network access for legacy or scope-bearing URLs", () => {
    const fetchMock = vi.fn();
    const first = render(<HostedApp href="https://ui.example.test/ui/v1/account?interaction_id=interaction_123456789" fetch={fetchMock as typeof fetch} />);
    expect(first.container).toHaveTextContent("无法打开账户页面");
    first.unmount();
    const second = render(<HostedApp href={`${href("account")}&product_id=forged`} fetch={fetchMock as typeof fetch} />);
    expect(second.container).toHaveTextContent("无法打开账户页面");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

function change(view: ReturnType<typeof render>, selector: string, value: string): void {
  fireEvent.change(view.container.querySelector(selector)!, { target: { value } });
}
function sequence(...responses: Response[]) {
  return vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => {
    const response = responses.shift();
    if (!response) throw new Error("unexpected request");
    return response;
  });
}
function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}
function empty(status = 204): Response { return new Response(null, { status }); }
function problem(status: number, code: string, retryable: boolean): Response { return new Response(JSON.stringify({ type: "about:blank", title: "Safe failure", status, code, request_id: "req_safe", retryable }), { status, headers: { "Content-Type": "application/problem+json" } }); }
function interaction(route: "hosted.auth" | "hosted.account") {
  return { interaction_id: interactionId, route_id: route, channel: "web", status: "opened", allowed_actions: ["authenticate", "complete", "cancel"], created_at: now, expires_at: later };
}
function browser(route: "hosted.auth" | "hosted.account") {
  return { interaction: interaction(route), csrf_token: "c".repeat(32), browser_session_expires_at: later };
}
function authBootstrap(overrides: Record<string, unknown> = {}) {
  return { interaction: interaction("hosted.auth"), presentation: { product_name: "Example Product", theme_variant: null }, flow: { kind: "login" }, password_enabled: true, registration_enabled: true, recovery_enabled: true, external_providers: [], ...overrides };
}
function accountBootstrap(overrides: Record<string, unknown> = {}) {
  return { interaction: interaction("hosted.account"), presentation: { product_name: "Example Product", theme_variant: null }, profile: { user_id: "user_test", version: 1, display_name: "Server User", avatar_url: null, locale: "zh-CN", timezone: "Asia/Shanghai" }, sessions: [{ session_id: "sess_current", current: true, device_label: "Current Browser", created_at: now, last_seen_at: now, expires_at: later }], external_identities: [], allowed_actions: ["update_profile", "change_password", "revoke_session", "complete"], ...overrides };
}
function entitlementSummary() {
  return { revision: 4, plan_code: "pro", features: { priority_queue: true, export_limit: 100 }, valid_until: later, offline_grace_until: later, updated_at: now };
}
