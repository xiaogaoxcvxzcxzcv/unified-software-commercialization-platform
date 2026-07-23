import { IconArrowRight, IconCheck, IconMoon, IconRefresh, IconSun, IconX } from "@tabler/icons-react";
import {
  AccountCenterBlock,
  AccountProfileBlock,
  AccountSecurityBlock,
  AuthLoginBlock,
  AuthRecoveryBlock,
  AuthRegisterBlock,
  EntitlementSummaryBlock,
  type AccountBlockError,
  type AccountBlockState,
  type AccountExternalIdentity,
  type AccountSessionSummary,
  type EntitlementFeatureItem,
  type EntitlementSummaryValue,
} from "@capability-platform/client-ui/web-react";
import {
  HostedAccountClient,
  HostedAccountController,
  parseHostedLaunch,
  type HostedAccountBootstrap,
  type HostedAccountData,
  type HostedAccountSnapshot,
  type HostedAuthBootstrap,
  type HostedCompletion,
  type HostedInteraction,
} from "@capability-platform/client-ui/hosted-web";
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";

type AuthScreen = "login" | "register" | "recovery";
type AccountScreen = "center" | "profile" | "security" | "entitlements";

export interface HostedAppProps {
  readonly href?: string;
  readonly fetch?: typeof fetch;
  readonly navigate?: (returnURL: string) => void;
	readonly close?: () => void;
}

export function HostedApp(props: HostedAppProps) {
	const runtime = useMemo(() => createRuntime(props), [props.href, props.fetch]);
  if (runtime instanceof Error) return <LaunchFailure error={runtime} />;
	return <HostedRuntime runtime={runtime} navigate={props.navigate ?? ((target) => window.location.assign(target))} close={props.close ?? (() => window.close())} />;
}

function HostedRuntime({ runtime, navigate, close }: { readonly runtime: Runtime; readonly navigate: (target: string) => void; readonly close: () => void }) {
  const snapshot = useSyncExternalStore((listener) => runtime.controller.subscribe(listener), () => runtime.controller.getSnapshot(), () => runtime.controller.getSnapshot());
  const [dark, setDark] = useState(false);
  useEffect(() => {
    document.documentElement.dataset.theme = dark ? "dark" : "light";
    return () => { delete document.documentElement.dataset.theme; };
  }, [dark]);
  useEffect(() => {
    void runtime.controller.start();
    return () => runtime.controller.reset();
  }, [runtime]);
	const completion = asCompletion(snapshot.data);
	const terminal = asTerminalInteraction(snapshot.data);
  const bootstrap = asBootstrap(snapshot.data);
  const productName = bootstrap?.presentation.product_name ?? "账户服务";

  return <div className="hosted-root client-ui-root">
    <a className="hosted-skip" href="#hosted-content">跳到主要内容</a>
    <header className="hosted-header">
      <div className="hosted-brand"><span aria-hidden="true">{productName.slice(0, 1)}</span><div><strong>{productName}</strong><small>{runtime.launch.routeId === "hosted.auth" ? "安全认证" : "账户管理"}</small></div></div>
      <button className="hosted-icon-button" type="button" title={dark ? "使用浅色主题" : "使用深色主题"} aria-label={dark ? "使用浅色主题" : "使用深色主题"} onClick={() => setDark((value) => !value)}>{dark ? <IconSun aria-hidden="true" /> : <IconMoon aria-hidden="true" />}</button>
    </header>
    <main id="hosted-content" className="hosted-main" tabIndex={-1}>
		{completion ? <CompletionState completion={completion} navigate={navigate} /> : terminal ? <InteractionTerminalState interaction={terminal} close={close} /> : snapshot.terminal ? <FailureTerminalState reason={snapshot.terminal} close={close} /> : runtime.launch.routeId === "hosted.auth"
        ? <AuthWorkspace snapshot={snapshot} controller={runtime.controller} bootstrap={asAuthBootstrap(snapshot.data)} />
        : <AccountWorkspace snapshot={snapshot} controller={runtime.controller} bootstrap={asAccountBootstrap(snapshot.data)} />}
    </main>
  </div>;
}

function AuthWorkspace({ snapshot, controller, bootstrap }: { readonly snapshot: Readonly<HostedAccountSnapshot>; readonly controller: HostedAccountController; readonly bootstrap?: HostedAuthBootstrap }) {
	const [requestedScreen, setRequestedScreen] = useState<AuthScreen>("login");
	const workspace = useRef<HTMLDivElement>(null);
  const state = blockState(snapshot, Boolean(bootstrap));
  const common = commonProps(snapshot, controller);
  const providers = bootstrap?.external_providers.map((item) => ({ id: item.provider, label: item.display_name })) ?? [];
	const interactionActions = new Set(bootstrap?.interaction.allowed_actions ?? []);
	const authenticateAllowed = interactionActions.has("authenticate");
	const cancelAllowed = interactionActions.has("cancel");
	const flow = bootstrap?.flow.kind ?? "login";
	const screen: AuthScreen = flow === "registration_verification" ? "register" : flow === "recovery_verification" ? "recovery" : requestedScreen;
	const viewKey = `${screen}:${flow}:${state}`;
	useFocusOnView(workspace, viewKey);
	const resetToLogin = async () => {
		setRequestedScreen("login");
		await controller.resetAuthFlow();
		await controller.refresh();
	};

	let content;
	if (screen === "register") {
		const registrationAllowed = authenticateAllowed && bootstrap?.registration_enabled === true;
		content = flow === "registration_verification"
			? <AuthRegisterBlock {...common} state={registrationAllowed ? state : "disabled"} step="complete" agreementLabel="我同意当前产品的服务协议" fieldErrors={mapFieldErrors(snapshot, { verification_proof: "verificationProof", display_name: "displayName", credential: "password" })} actions={{ complete: registrationAllowed ? (input) => controller.register({ credential: input.password, verification_proof: input.verificationProof, display_name: input.displayName }) : undefined, cancel: authenticateAllowed ? resetToLogin : undefined, openLogin: authenticateAllowed ? resetToLogin : undefined }} />
			: <AuthRegisterBlock {...common} state={registrationAllowed ? state : "disabled"} step="request_verification" providers={providers} fieldErrors={mapFieldErrors(snapshot, { identifier: "identifier" })} actions={{ requestVerification: registrationAllowed ? async (input) => { await controller.startRegistrationVerification({ identifier: input.identifier }); await controller.refresh(); } : undefined, cancel: authenticateAllowed ? resetToLogin : undefined, openLogin: authenticateAllowed ? resetToLogin : undefined }} />;
	} else if (screen === "recovery") {
		const recoveryAllowed = authenticateAllowed && bootstrap?.recovery_enabled === true;
		content = flow === "recovery_verification"
			? <AuthRecoveryBlock {...common} state={recoveryAllowed ? state : "disabled"} step="complete" deliveryTargetMasked={bootstrap?.flow.identifier_hint} fieldErrors={mapFieldErrors(snapshot, { recovery_proof: "recoveryProof", new_credential: "password" })} actions={{ complete: recoveryAllowed ? async (input) => { await controller.completeRecovery({ recovery_proof: input.recoveryProof, new_credential: input.password }); setRequestedScreen("login"); } : undefined, cancel: authenticateAllowed ? resetToLogin : undefined }} />
			: <AuthRecoveryBlock {...common} state={recoveryAllowed ? state : "disabled"} step="identify" fieldErrors={mapFieldErrors(snapshot, { identifier: "identifier" })} actions={{ requestRecovery: recoveryAllowed ? async (input) => { await controller.startRecovery({ identifier: input.identifier }); await controller.refresh(); } : undefined, cancel: authenticateAllowed ? resetToLogin : undefined }} />;
	} else {
		const passwordEnabled = bootstrap?.password_enabled ?? true;
		content = <AuthLoginBlock {...common} state={state} passwordEnabled={passwordEnabled} providers={providers} fieldErrors={mapFieldErrors(snapshot, { identifier: "identifier", credential: "password" })} actions={{ submit: passwordEnabled && authenticateAllowed ? ({ identifier, password }) => controller.authenticatePassword({ identifier, credential: password }) : undefined, cancel: cancelAllowed ? () => void controller.cancel() : undefined, openRegister: bootstrap?.registration_enabled && authenticateAllowed ? () => setRequestedScreen("register") : undefined, openRecovery: bootstrap?.recovery_enabled && authenticateAllowed ? () => setRequestedScreen("recovery") : undefined }} />;
	}
	return <div ref={workspace} className="hosted-workspace-view"><div className="client-sr-only" aria-live="polite">{announcement(screen, flow)}</div>{content}</div>;
}

function AccountWorkspace({ snapshot, controller, bootstrap }: { readonly snapshot: Readonly<HostedAccountSnapshot>; readonly controller: HostedAccountController; readonly bootstrap?: HostedAccountBootstrap }) {
  const [screen, setScreen] = useState<AccountScreen>("center");
	const workspace = useRef<HTMLDivElement>(null);
  const state = blockState(snapshot, Boolean(bootstrap));
	useFocusOnView(workspace, `${screen}:${state}`);
  const common = commonProps(snapshot, controller);
  const profile = bootstrap?.profile;
  const sessions = mapSessions(bootstrap);
	const identities = mapIdentities(bootstrap);
  const allowed = new Set(bootstrap?.allowed_actions ?? []);
	let content;
	if (screen === "profile") content = <AccountProfileBlock {...common} state={allowed.has("update_profile") ? state : "disabled"} value={{ displayName: profile?.display_name ?? "", locale: profile?.locale ?? "", timezone: profile?.timezone ?? "", avatarUrl: profile?.avatar_url ?? undefined, version: profile?.version ?? 1 }} fieldErrors={mapFieldErrors(snapshot, { display_name: "displayName", locale: "locale", timezone: "timezone" })} actions={{ submit: allowed.has("update_profile") ? (input) => controller.updateProfile({ expected_version: input.expectedVersion, display_name: input.displayName, locale: input.locale || null, timezone: input.timezone || null }) : undefined, cancel: () => { controller.discardPendingMutation("update_profile"); setScreen("center"); } }} />;
	else if (screen === "security") {
		const revokeAllowed = allowed.has("revoke_session");
		const passwordAllowed = allowed.has("change_password");
		content = <AccountSecurityBlock {...common} state={passwordAllowed || revokeAllowed ? state : "disabled"} sessions={revokeAllowed ? sessions : sessions.filter((item) => item.current)} externalIdentities={identities} passwordChangeAllowed={passwordAllowed} passwordFieldErrors={mapFieldErrors(snapshot, { current_credential: "currentPassword", new_credential: "newPassword" })} actions={{ changePassword: passwordAllowed ? (input) => controller.changePassword({ current_credential: input.currentPassword, new_credential: input.newPassword, revoke_other_sessions: true }) : undefined, revokeSession: revokeAllowed ? (sessionId) => controller.revokeSession(sessionId) : undefined, revokeAllOtherSessions: revokeAllowed ? async () => { for (const session of sessions.filter((item) => !item.current && !item.revoked)) await controller.revokeSession(session.id); } : undefined, cancel: () => { controller.discardPendingMutation("change_password"); setScreen("center"); } }} />;
	} else if (screen === "entitlements") {
		const summary = mapEntitlementSummary(bootstrap);
		content = <EntitlementSummaryBlock {...common} state={summary ? state : "disabled"} value={summary ?? undefined} disabledMessage="当前产品未启用权益能力。" actions={{ retry: () => void controller.refresh() }} />;
	} else {
		const summary = mapEntitlementSummary(bootstrap);
		content = <>
			<AccountCenterBlock {...common} state={state} user={{ displayName: profile?.display_name ?? "账户用户", avatarUrl: profile?.avatar_url ?? undefined }} security={{ passwordConfigured: allowed.has("change_password"), activeSessionCount: sessions.filter((item) => !item.revoked).length, externalIdentityCount: identities.length }} actions={{ openProfile: allowed.has("update_profile") ? () => setScreen("profile") : undefined, openSecurity: allowed.has("change_password") || allowed.has("revoke_session") ? () => setScreen("security") : undefined, close: allowed.has("complete") ? () => void controller.completeAccount({ result: "closed" }) : undefined }} />
			{summary && <nav className="account-center-nav hosted-entitlement-nav" aria-label="当前产品能力"><button type="button" disabled={state === "submitting"} onClick={() => setScreen("entitlements")}><span><strong>当前权益</strong><small>Revision {summary.revision} · {summary.features.length} 项功能</small></span><span aria-hidden="true">›</span></button></nav>}
		</>;
	}
	return <div ref={workspace} className="hosted-workspace-view"><div className="client-sr-only" aria-live="polite">{announcement(screen)}</div>{content}</div>;
}

function CompletionState({ completion, navigate }: { readonly completion: HostedCompletion; readonly navigate: (target: string) => void }) {
	return <section className="hosted-terminal" role="status"><IconCheck aria-hidden="true"/><h1>操作已完成</h1><p>返回原应用继续。</p><button className="hosted-return-button" type="button" onClick={() => navigate(completion.return_url)}>返回应用<IconArrowRight aria-hidden="true"/></button></section>;
}
function InteractionTerminalState({ interaction, close }: { readonly interaction: HostedInteraction; readonly close: () => void }) {
	const content = terminalContent(interaction.status);
	return <section className="hosted-terminal" role="status">{interaction.status === "completed" || interaction.status === "exchanged" ? <IconCheck aria-hidden="true"/> : <IconX aria-hidden="true"/>}<h1>{content.title}</h1><p>{content.message}</p><button className="hosted-return-button hosted-close-button" type="button" onClick={close}>关闭窗口<IconX aria-hidden="true"/></button></section>;
}
function FailureTerminalState({ reason, close }: { readonly reason: NonNullable<HostedAccountSnapshot["terminal"]>; readonly close: () => void }) {
	const content = reason === "expired" ? { title: "链接已过期", message: "请返回原应用重新发起操作。" } : reason === "session_revoked" ? { title: "会话已失效", message: "请返回原应用重新登录。" } : reason === "authentication_required" ? { title: "需要重新认证", message: "请返回原应用重新发起操作。" } : { title: "操作已结束", message: "请关闭窗口后从原应用继续。" };
	return <section className="hosted-terminal" role="status"><IconX aria-hidden="true"/><h1>{content.title}</h1><p>{content.message}</p><button className="hosted-return-button hosted-close-button" type="button" onClick={close}>关闭窗口<IconX aria-hidden="true"/></button></section>;
}
function LaunchFailure({ error }: { readonly error: Error }) {
  return <main className="hosted-launch-failure"><section role="alert"><h1>无法打开账户页面</h1><p>{error instanceof TypeError ? "链接无效或已经过期。" : "页面初始化失败。"}</p><button type="button" onClick={() => window.location.reload()}><IconRefresh aria-hidden="true"/>重试</button></section></main>;
}

interface Runtime { readonly launch: ReturnType<typeof parseHostedLaunch>; readonly controller: HostedAccountController }
function createRuntime(props: HostedAppProps): Runtime | Error {
  try {
    const launch = parseHostedLaunch(props.href ?? window.location.href);
    if (launch.routeId !== "hosted.auth" && launch.routeId !== "hosted.account") throw new TypeError("unsupported account hosted route");
		const client = new HostedAccountClient({ origin: window.location.origin, interactionId: launch.interactionId, fetch: props.fetch });
    return { launch, controller: new HostedAccountController(client) };
  } catch (error) { return error instanceof Error ? error : new Error("hosted runtime failed"); }
}
function commonProps(snapshot: Readonly<HostedAccountSnapshot>, controller: HostedAccountController) {
  return { error: blockError(snapshot), onRetry: () => void controller.refresh(), emptyMessage: "当前没有可用内容。", disabledMessage: "当前账户能力不可用。", successMessage: "操作已完成。" };
}
function blockState(snapshot: Readonly<HostedAccountSnapshot>, hasBootstrap: boolean): AccountBlockState {
  if ((snapshot.state === "success" || snapshot.state === "submitting") && hasBootstrap) return snapshot.state;
  return snapshot.state;
}
function blockError(snapshot: Readonly<HostedAccountSnapshot>): AccountBlockError | undefined {
  if (!snapshot.error) return undefined;
  return { code: snapshot.error.code, message: snapshot.error.retryable ? "服务暂时不可用，请重试。" : "当前操作无法继续。", retryable: snapshot.error.retryable };
}
function mapFieldErrors<T extends string>(snapshot: Readonly<HostedAccountSnapshot>, fields: Readonly<Record<string, T>>): Readonly<Partial<Record<T, string>>> | undefined {
	const result: Partial<Record<T, string>> = {};
	for (const item of snapshot.error?.fieldErrors ?? []) {
		const target = fields[item.field];
		if (target) result[target] = item.message ?? item.code;
	}
	return Object.keys(result).length ? result : undefined;
}
function asBootstrap(data: HostedAccountData | undefined): HostedAuthBootstrap | HostedAccountBootstrap | undefined { return asAuthBootstrap(data) ?? asAccountBootstrap(data); }
function asAuthBootstrap(data: HostedAccountData | undefined): HostedAuthBootstrap | undefined { return data && "password_enabled" in data ? data : undefined; }
function asAccountBootstrap(data: HostedAccountData | undefined): HostedAccountBootstrap | undefined { return data && "profile" in data && "sessions" in data ? data : undefined; }
function asCompletion(data: HostedAccountData | undefined): HostedCompletion | undefined { return data && "return_url" in data ? data : undefined; }
function asTerminalInteraction(data: HostedAccountData | undefined): HostedInteraction | undefined {
	if (!data || !("status" in data) || !("route_id" in data)) return undefined;
	return data.status === "completed" || data.status === "cancelled" || data.status === "failed" || data.status === "expired" || data.status === "exchanged" ? data : undefined;
}
function terminalContent(status: HostedInteraction["status"]): { readonly title: string; readonly message: string } {
	if (status === "cancelled") return { title: "操作已取消", message: "此窗口不会退出或修改你的账户。" };
	if (status === "expired") return { title: "链接已过期", message: "请返回原应用重新发起操作。" };
	if (status === "failed") return { title: "操作未完成", message: "请关闭窗口后从原应用重试。" };
	return { title: "操作已完成", message: "可以安全关闭此窗口。" };
}
function mapSessions(bootstrap?: HostedAccountBootstrap): readonly AccountSessionSummary[] {
  return bootstrap?.sessions.map((item) => ({ id: item.session_id, deviceLabel: item.device_label ?? (item.current ? "当前浏览器" : "已登录设备"), authenticationMethod: "账户认证", applicationLabel: bootstrap.interaction.channel === "desktop" ? "Desktop" : "Web", lastSeenLabel: formatTime(item.last_seen_at), expiresLabel: formatTime(item.expires_at), current: item.current, revoked: false })) ?? [];
}
function mapIdentities(bootstrap?: HostedAccountBootstrap): readonly AccountExternalIdentity[] {
  return bootstrap?.external_identities.filter((item) => item.status === "active").map((item) => ({ id: item.external_identity_id, providerLabel: item.provider, subjectMasked: item.masked_subject ?? "已绑定", unlinkAllowed: false })) ?? [];
}
function mapEntitlementSummary(bootstrap?: HostedAccountBootstrap): EntitlementSummaryValue | null {
	const summary = bootstrap?.entitlement_summary;
	if (!summary) return null;
	const entries = Object.entries(summary.features);
	return {
		revision: summary.revision,
		planCode: summary.plan_code,
		validUntil: summary.valid_until,
		offlineGraceUntil: summary.offline_grace_until,
		updatedAt: summary.updated_at,
		features: entries.map(([code, value]): EntitlementFeatureItem => ({ code, label: code, value: primitiveFeatureValue(value) })),
		emptyReason: entries.length === 0 ? "never_owned" : undefined,
	};
}
function primitiveFeatureValue(value: unknown): string | number | boolean | null {
	if (typeof value === "string" || typeof value === "number" || typeof value === "boolean" || value === null) return value;
	if (value === undefined) return null;
	return JSON.stringify(value);
}
function formatTime(value: string): string { return new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)); }

function useFocusOnView(root: { readonly current: HTMLDivElement | null }, key: string): void {
	useEffect(() => {
		const timer = window.setTimeout(() => root.current?.querySelector<HTMLElement>('input:not([disabled]), button:not([disabled]), [tabindex="-1"]')?.focus(), 0);
		return () => window.clearTimeout(timer);
	}, [root, key]);
}
function announcement(screen: AuthScreen | AccountScreen, flow?: HostedAuthBootstrap["flow"]["kind"]): string {
	if (flow === "registration_verification") return "注册验证步骤";
	if (flow === "recovery_verification") return "密码找回验证步骤";
	const labels: Record<AuthScreen | AccountScreen, string> = { login: "登录", register: "创建账户", recovery: "找回密码", center: "个人中心", profile: "个人资料", security: "账户安全", entitlements: "当前权益" };
	return labels[screen];
}
