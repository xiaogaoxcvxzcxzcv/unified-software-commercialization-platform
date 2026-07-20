import { describe, expect, it, vi } from "vitest";
import { HostedAccountClient, HostedAccountController } from "../hosted-web/src/index.js";

const interactionId = "hint_abcdefghijklmnopqrstuvwxyz";
const now = "2026-07-18T00:00:00Z";
const later = "2026-07-18T01:00:00Z";

describe("HostedAccountController", () => {
  it.each(["cancelled", "failed", "expired"] as const)("projects %s as a stable terminal state without loading account bootstrap", async (status) => {
    const terminal = { ...interaction(), status, allowed_actions: [] };
    const fetchMock = vi.fn(async () => json({ interaction: terminal, csrf_token: "c".repeat(32), browser_session_expires_at: later }));
    const controller = createController(fetchMock);

    await controller.start();

    expect(fetchMock).toHaveBeenCalledOnce();
    expect(controller.getSnapshot()).toMatchObject({ state: "success", data: { status } });
  });

  it("projects completed as a stable server completion without loading account bootstrap", async () => {
    const completed = { ...interaction(), status: "completed", result_kind: "account_completed", allowed_actions: ["exchange"] };
    const completion = { interaction_id: interactionId, status: "completed", return_url: "sample-app://callback?code=opaque&state=opaque", expires_at: later };
    const fetchMock = vi.fn(async () => json({ interaction: completed, csrf_token: "c".repeat(32), browser_session_expires_at: later, completion }));
    const controller = createController(fetchMock);

    await controller.start();

    expect(fetchMock).toHaveBeenCalledOnce();
    expect(controller.getSnapshot()).toMatchObject({ state: "success", data: { return_url: completion.return_url } });
  });

  it("projects completed without a return completion as a closable terminal interaction", async () => {
    const completed = { ...interaction(), status: "completed", result_kind: "account_completed", allowed_actions: [] };
    const fetchMock = vi.fn(async () => json({ interaction: completed, csrf_token: "c".repeat(32), browser_session_expires_at: later }));
    const controller = createController(fetchMock);

    await controller.start();

    expect(fetchMock).toHaveBeenCalledOnce();
    expect(controller.getSnapshot()).toMatchObject({ state: "success", data: { status: "completed" } });
  });

  it.each([
    { name: "flags enabled without authenticate", actions: [] as string[], password: true, registration: true, recovery: true, providers: [] },
    { name: "authenticate without an implemented method", actions: ["authenticate"], password: false, registration: false, recovery: false, providers: [] },
    { name: "provider-only without a public provider flow", actions: ["authenticate"], password: false, registration: false, recovery: false, providers: [{ provider: "oidc", mode: "redirect", display_name: "OIDC" }] },
  ])("marks auth empty for $name", async ({ actions, password, registration, recovery, providers }) => {
    const authInteraction = { ...interaction(), route_id: "hosted.auth", allowed_actions: actions };
    const auth = { interaction: authInteraction, presentation: { product_name: "Product", theme_variant: null }, flow: { kind: "login" }, password_enabled: password, registration_enabled: registration, recovery_enabled: recovery, external_providers: providers };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(json({ interaction: authInteraction, csrf_token: "c".repeat(32), browser_session_expires_at: later }))
      .mockResolvedValueOnce(json(auth));
    const controller = createController(fetchMock);

    await controller.start();

    expect(controller.getSnapshot()).toMatchObject({ state: "empty" });
  });

  it("cancels obsolete startup work and publishes only the newest recovered server state", async () => {
    let firstSignal: AbortSignal | undefined;
    const fetchMock = vi.fn((_: RequestInfo | URL, init?: RequestInit) => {
      if (!firstSignal) {
        firstSignal = init?.signal ?? undefined;
        return new Promise<Response>((_, reject) => init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true }));
      }
      return Promise.resolve(fetchMock.mock.calls.length === 2 ? json(browser()) : json(bootstrap()));
    });
    const controller = createController(fetchMock);
    const first = controller.start();
    const second = controller.refresh();
    await Promise.all([first, second]);
    expect(firstSignal?.aborted).toBe(true);
    expect(controller.getSnapshot()).toMatchObject({ state: "ready", data: { profile: { user_id: "user_test" } } });
  });

  it("retains the same idempotency key across response loss, refresh recovery, and retry", async () => {
    const updateKeys: string[] = [];
    const responses: Array<Response | Error> = [
      json(browser()), json(bootstrap()),
      new TypeError("response lost"),
      json(browser()), json(bootstrap()),
		json(profile(2)), json(bootstrap(2)),
    ];
    const fetchMock = vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "PATCH") updateKeys.push(new Headers(init.headers).get("Idempotency-Key") ?? "");
      const result = responses.shift();
      if (result instanceof Error) throw result;
      if (!result) throw new Error("unexpected request");
      return result;
    });
    const keyFactory = vi.fn(() => "stable-idempotency-key-0001");
    const controller = createController(fetchMock, keyFactory);
    await controller.start();
    await controller.updateProfile({ expected_version: 1, display_name: "Updated" });
    expect(controller.getSnapshot()).toMatchObject({ state: "failed", error: { kind: "network", retryable: true } });
    await controller.refresh();
    expect(controller.getSnapshot().state).toBe("ready");
    await controller.updateProfile({ expected_version: 1, display_name: "Updated" });
    expect(updateKeys).toEqual(["stable-idempotency-key-0001", "stable-idempotency-key-0001"]);
    expect(keyFactory).toHaveBeenCalledTimes(1);
		expect(controller.getSnapshot()).toMatchObject({ state: "ready", data: { profile: { version: 2 } } });
  });

	it("retains the key after mutation success and reload loss when the explicit retry payload is unchanged", async () => {
		const updateKeys: string[] = [];
		const responses: Array<Response | Error> = [
			json(browser()), json(bootstrap()),
			json(profile(2)), new TypeError("reload response lost"),
			json(browser()), json(bootstrap(2)),
			json(profile(2)), json(bootstrap(2)),
		];
		const fetchMock = vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => {
			if (init?.method === "PATCH") updateKeys.push(new Headers(init.headers).get("Idempotency-Key") ?? "");
			const result = responses.shift();
			if (result instanceof Error) throw result;
			if (!result) throw new Error("unexpected request");
			return result;
		});
		const keyFactory = vi.fn(() => "reload-loss-stable-key-0001");
		const controller = createController(fetchMock, keyFactory);
		const request = { expected_version: 1, display_name: "Updated" };

		await controller.start();
		await controller.updateProfile(request);
		expect(controller.getSnapshot()).toMatchObject({ state: "failed", error: { kind: "network" } });
		await controller.refresh();
		await controller.updateProfile(request);

		expect(updateKeys).toEqual(["reload-loss-stable-key-0001", "reload-loss-stable-key-0001"]);
		expect(keyFactory).toHaveBeenCalledOnce();
	});

	it("drops the pending key when the same mutation kind is retried with a changed payload", async () => {
		const updateKeys: string[] = [];
		const responses: Array<Response | Error> = [
			json(browser()), json(bootstrap()),
			json(profile(2)), new TypeError("reload response lost"),
			json(browser()), json(bootstrap(2)),
			json(profile(3)), json(bootstrap(3)),
		];
		const fetchMock = vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => {
			if (init?.method === "PATCH") updateKeys.push(new Headers(init.headers).get("Idempotency-Key") ?? "");
			const result = responses.shift();
			if (result instanceof Error) throw result;
			if (!result) throw new Error("unexpected request");
			return result;
		});
		const keys = ["changed-payload-key-0001", "changed-payload-key-0002"];
		const keyFactory = vi.fn(() => keys.shift() ?? "unexpected-key-value");
		const controller = createController(fetchMock, keyFactory);

		await controller.start();
		await controller.updateProfile({ expected_version: 1, display_name: "First" });
		await controller.refresh();
		await controller.updateProfile({ expected_version: 2, display_name: "Edited" });

		expect(updateKeys).toEqual(["changed-payload-key-0001", "changed-payload-key-0002"]);
		expect(keyFactory).toHaveBeenCalledTimes(2);
	});

	it("clears pending retries and the browser session when refresh observes a terminal interaction", async () => {
		const terminal = { ...interaction(), status: "cancelled", allowed_actions: [] };
		const responses: Array<Response | Error> = [json(browser()), json(bootstrap()), new TypeError("response lost"), json({ ...browser(), interaction: terminal })];
		const fetchMock = vi.fn(async () => {
			const result = responses.shift();
			if (result instanceof Error) throw result;
			if (!result) throw new Error("unexpected request");
			return result;
		});
		const keyFactory = vi.fn(() => "terminal-recovery-key-0001");
		const api = new HostedAccountClient({ origin: "https://ui.example.test", interactionId, fetch: fetchMock as typeof fetch });
		const controller = new HostedAccountController(api, { keyFactory });

		await controller.start();
		await controller.updateProfile({ expected_version: 1, display_name: "Updated" });
		await controller.refresh();
		const callsAtTerminal = fetchMock.mock.calls.length;
		await controller.updateProfile({ expected_version: 1, display_name: "Updated" });

		expect(controller.getSnapshot()).toMatchObject({ state: "success", data: { status: "cancelled" } });
		expect(api.hasBrowserSession()).toBe(false);
		expect(fetchMock).toHaveBeenCalledTimes(callsAtTerminal);
		expect(keyFactory).toHaveBeenCalledOnce();
	});

	it("does not expose password plaintext while changed password payloads receive distinct pending keys", async () => {
		const passwordKeys: string[] = [];
		const responses: Array<Response | Error> = [
			json(browser()), json(bootstrap()),
			new TypeError("response lost"),
			json(browser()), json(bootstrap()),
			empty(), json(bootstrap(2)),
		];
		const fetchMock = vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => {
			if (String(_).endsWith("/account/password")) passwordKeys.push(new Headers(init?.headers).get("Idempotency-Key") ?? "");
			const result = responses.shift();
			if (result instanceof Error) throw result;
			if (!result) throw new Error("unexpected request");
			return result;
		});
		const keys = ["password-payload-key-0001", "password-payload-key-0002"];
		const controller = createController(fetchMock, () => keys.shift() ?? "unexpected-key-value");

		await controller.start();
		await controller.changePassword({ current_credential: "old-secret", new_credential: "first-new-secret", revoke_other_sessions: true });
		expect(JSON.stringify(controller.getSnapshot())).not.toContain("secret");
		await controller.refresh();
		await controller.changePassword({ current_credential: "old-secret", new_credential: "edited-new-secret", revoke_other_sessions: true });

		expect(passwordKeys).toEqual(["password-payload-key-0001", "password-payload-key-0002"]);
		expect(JSON.stringify(controller.getSnapshot())).not.toContain("secret");
	});

  it("does not persist credentials and clears session and pending mutations on reset", async () => {
    const bodies: string[] = [];
    const fetchMock = vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => {
      if (typeof init?.body === "string") bodies.push(init.body);
      if (fetchMock.mock.calls.length === 1) return json(browser());
      if (fetchMock.mock.calls.length === 2) return json(bootstrap());
      throw new TypeError("response lost");
    });
    const api = new HostedAccountClient({ origin: "https://ui.example.test", interactionId, fetch: fetchMock as typeof fetch });
    const controller = new HostedAccountController(api, { keyFactory: () => "stable-password-key-0001" });
    await controller.start();
    await controller.changePassword({ current_credential: "old-secret", new_credential: "new-secret-value", revoke_other_sessions: true });
    expect(controller.getSnapshot().state).toBe("failed");
    controller.reset();
    expect(controller.getSnapshot().state).toBe("idle");
    expect(api.hasBrowserSession()).toBe(false);
    expect(JSON.stringify(controller.getSnapshot())).not.toContain("secret");
    expect(bodies.at(-1)).toContain("new-secret-value");
  });

	it.each([
		{ kind: "reset", route: "hosted.auth", path: "/auth/flow", method: "DELETE" },
		{ kind: "revoke", route: "hosted.account", path: "/account/sessions/sess_other", method: "DELETE" },
		{ kind: "cancel", route: "hosted.account", path: "/cancel", method: "POST" },
	] as const)("reuses the pending idempotency key for $kind after response loss", async ({ kind, route, path, method }) => {
		const keys: string[] = [];
		let mutationAttempts = 0;
		const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
			const url = String(input);
			if (url.endsWith(path) && init?.method === method) {
				keys.push(new Headers(init.headers).get("Idempotency-Key") ?? "");
				mutationAttempts++;
				if (mutationAttempts === 1) throw new TypeError("response lost");
				return kind === "cancel" ? json({ ...interaction(route), status: "cancelled", result_kind: "cancelled", allowed_actions: [] }) : empty();
			}
			if (url.endsWith("/browser-session")) return json(browser(route));
			if (url.endsWith("/auth/bootstrap")) return json(authBootstrap());
			if (url.endsWith("/account/bootstrap")) return json(bootstrap());
			throw new Error(`unexpected request ${url}`);
		});
		const controller = createControllerForRoute(fetchMock, route, () => `stable-${kind}-key-000001`);
		await controller.start();
		if (kind === "reset") await controller.resetAuthFlow();
		else if (kind === "revoke") await controller.revokeSession("sess_other");
		else await controller.cancel();
		expect(controller.getSnapshot().state).toBe("failed");
		await controller.refresh();
		if (kind === "reset") await controller.resetAuthFlow();
		else if (kind === "revoke") await controller.revokeSession("sess_other");
		else await controller.cancel();
		expect(keys).toEqual([`stable-${kind}-key-000001`, `stable-${kind}-key-000001`]);
	});

	it("clears stale bootstrap for stable capability and terminal failures but retains it for retryable failures", async () => {
		const capability = createController(sequence(json(browser()), json(bootstrap()), problem(403, "hosted.capability_not_available", false)));
		await capability.start();
		await capability.updateProfile({ expected_version: 1, display_name: "Blocked" });
		expect(capability.getSnapshot()).toMatchObject({ state: "disabled", error: { code: "hosted.capability_not_available" } });
		expect(capability.getSnapshot().data).toBeUndefined();

		const expired = createController(sequence(json(browser()), json(bootstrap()), problem(410, "hosted.interaction_expired", false)));
		await expired.start();
		await expired.cancel();
		expect(expired.getSnapshot()).toMatchObject({ terminal: "expired", error: { code: "hosted.interaction_expired" } });
		expect(expired.getSnapshot().data).toBeUndefined();

		const retryable = createController(sequence(json(browser()), json(bootstrap()), problem(503, "hosted.temporarily_unavailable", true)));
		await retryable.start();
		await retryable.updateProfile({ expected_version: 1, display_name: "Retry" });
		expect(retryable.getSnapshot()).toMatchObject({ state: "failed", data: { profile: { user_id: "user_test" } }, error: { retryable: true } });
	});
});

function createController(fetchMock: ReturnType<typeof vi.fn>, keyFactory?: () => string): HostedAccountController {
  return new HostedAccountController(new HostedAccountClient({ origin: "https://ui.example.test", interactionId, fetch: fetchMock as typeof fetch }), keyFactory ? { keyFactory } : {});
}
function createControllerForRoute(fetchMock: ReturnType<typeof vi.fn>, _route: "hosted.auth" | "hosted.account", keyFactory?: () => string): HostedAccountController {
	return new HostedAccountController(new HostedAccountClient({ origin: "https://ui.example.test", interactionId, fetch: fetchMock as typeof fetch }), keyFactory ? { keyFactory } : {});
}
function json(value: unknown): Response { return new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } }); }
function empty(): Response { return new Response(null, { status: 204 }); }
function problem(status: number, code: string, retryable: boolean): Response { return new Response(JSON.stringify({ type: "about:blank", title: "Safe failure", status, code, request_id: "req_safe", retryable }), { status, headers: { "Content-Type": "application/problem+json" } }); }
function sequence(...responses: Response[]): ReturnType<typeof vi.fn> { return vi.fn(async () => { const response = responses.shift(); if (!response) throw new Error("unexpected request"); return response; }); }
function interaction(route: "hosted.auth" | "hosted.account" = "hosted.account") { return { interaction_id: interactionId, route_id: route, channel: "web", status: "opened", allowed_actions: route === "hosted.auth" ? ["authenticate", "cancel"] : ["complete", "cancel"], created_at: now, expires_at: later }; }
function browser(route: "hosted.auth" | "hosted.account" = "hosted.account") { return { interaction: interaction(route), csrf_token: "c".repeat(32), browser_session_expires_at: later }; }
function authBootstrap() { return { interaction: interaction("hosted.auth"), presentation: { product_name: "Product", theme_variant: null }, flow: { kind: "login" }, password_enabled: true, registration_enabled: true, recovery_enabled: true, external_providers: [] }; }
function profile(version = 1) { return { user_id: "user_test", version, display_name: "User", avatar_url: null, locale: null, timezone: null }; }
function bootstrap(version = 1) { return { interaction: interaction(), presentation: { product_name: "Product", theme_variant: null }, profile: profile(version), sessions: [{ session_id: "sess_current", current: true, created_at: now, last_seen_at: now, expires_at: later }], external_identities: [], allowed_actions: ["update_profile", "change_password", "revoke_session", "complete"] }; }
