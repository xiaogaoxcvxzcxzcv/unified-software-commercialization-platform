import { describe, expect, it, vi } from "vitest";
import { HostedAccountClient, HostedApiError, HostedProtocolError } from "../hosted-web/src/index.js";

const interactionId = "hint_abcdefghijklmnopqrstuvwxyz";
const now = "2026-07-18T00:00:00Z";
const later = "2026-07-18T01:00:00Z";

describe("HostedAccountClient", () => {
  it("uses only hosted Cookie credentials and in-memory CSRF for all nine new APIs", async () => {
    const requests: Array<[RequestInfo | URL, RequestInit | undefined]> = [];
    const responses = [
      json(browser("hosted.auth")),
      json(authBootstrap()),
			json({ kind: "registration_verification", identifier_hint: "p***@example.test" }, 202),
      json(completion()),
			json({ kind: "recovery_verification", identifier_hint: "p***@example.test" }, 202),
      empty(),
			empty(),
			json({ ...interaction("hosted.auth"), status: "cancelled", result_kind: "cancelled", allowed_actions: [] }),
      json(browser("hosted.account")),
      json(accountBootstrap()),
      json(profile(2)),
      empty(),
      empty(),
    ];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      requests.push([input, init]);
      const response = responses.shift();
      if (!response) throw new Error("unexpected request");
      return response;
    });

    const auth = client(fetchMock);
    await auth.openBrowserSession();
    await auth.getAuthBootstrap();
    await auth.startRegistrationVerification({ identifier: "person@example.test" }, "idem-verification-0001");
		await auth.register({ credential: "password-value", verification_proof: "verification-proof-value" }, "idem-register-0000001");
    await auth.startRecovery({ identifier: "person@example.test" }, "idem-recovery-start-01");
		await auth.completeRecovery({ recovery_proof: "recovery-proof-value", new_credential: "new-password-value" }, "idem-recovery-done-001");
		await auth.resetAuthFlow("idem-flow-reset-000001");
		await auth.cancel("idem-cancel-auth-00001");

    const account = client(fetchMock);
    await account.openBrowserSession();
    await account.getAccountBootstrap();
    await account.updateProfile({ expected_version: 1, display_name: "Updated" }, "idem-profile-update-01");
    await account.changePassword({ current_credential: "old-password", new_credential: "new-password-value", revoke_other_sessions: true }, "idem-password-change-1");
    await account.revokeSession("sess_other", "idem-session-revoke-01");

		expect(requests).toHaveLength(13);
    for (const [, init] of requests) {
      expect(init?.credentials).toBe("include");
      expect(new Headers(init?.headers).has("Authorization")).toBe(false);
      expect(init?.redirect).toBe("error");
      expect(init?.cache).toBe("no-store");
    }
    expect(new Headers(requests[2]![1]?.headers).get("X-CSRF-Token")).toBe("c".repeat(32));
		for (const [index, key] of [
			[2, "idem-verification-0001"], [3, "idem-register-0000001"], [4, "idem-recovery-start-01"],
			[5, "idem-recovery-done-001"], [6, "idem-flow-reset-000001"], [7, "idem-cancel-auth-00001"],
			[10, "idem-profile-update-01"], [11, "idem-password-change-1"], [12, "idem-session-revoke-01"],
		] as const) expect(new Headers(requests[index]![1]?.headers).get("Idempotency-Key")).toBe(key);
  });

  it("rejects wrong content types, unknown fields, path mismatches, route mismatches, and malformed errors", async () => {
    const wrongType = client(vi.fn(async () => new Response(JSON.stringify(browser("hosted.auth")), { status: 200, headers: { "Content-Type": "text/plain" } })));
    await expect(wrongType.openBrowserSession()).rejects.toBeInstanceOf(HostedProtocolError);

    const unknown = client(vi.fn(async () => json({ ...browser("hosted.auth"), token: "must-not-be-accepted" })));
    await expect(unknown.openBrowserSession()).rejects.toThrow("unknown or missing");

    const mismatch = client(vi.fn(async () => json({ ...browser("hosted.auth"), interaction: { ...interaction("hosted.auth"), interaction_id: "hint_zyxwvutsrqponmlkjihgfedcba" } })));
    await expect(mismatch.openBrowserSession()).rejects.toThrow("does not match request path");

    const route = client(sequence(json(browser("hosted.auth"))));
    await route.openBrowserSession();
    expect(() => route.getAccountBootstrap()).toThrow("bound interaction route");

		const errorClient = client(sequence(json(browser("hosted.auth")), problem({ type: "about:blank", title: "Conflict", status: 409, code: "version_conflict", request_id: "req_safe", retryable: false, field_errors: [{ field: "identifier", code: "invalid", message: "Invalid identifier" }] })));
    await errorClient.openBrowserSession();
		await expect(errorClient.startRecovery({ identifier: "person@example.test" }, "idem-error-test-0001")).rejects.toMatchObject({ status: 409, code: "version_conflict", requestId: "req_safe", fieldErrors: [{ field: "identifier", code: "invalid", message: "Invalid identifier" }] } satisfies Partial<HostedApiError>);

    const malformed = client(sequence(json(browser("hosted.auth")), problem({ type: "about:blank", title: "Bad", status: 400, code: "bad", request_id: "req", retryable: false, leaked: true })));
    await malformed.openBrowserSession();
    await expect(malformed.getAuthBootstrap()).rejects.toThrow("unknown or missing");
  });

  it("rejects caller-injected scope fields and never follows server navigation", async () => {
    const fetchMock = sequence(json(browser("hosted.auth")), json(completion()));
    const api = client(fetchMock);
    await api.openBrowserSession();
		expect(() => api.register({ credential: "password-value", verification_proof: "verification-proof-value", product_id: "forged" } as never, "idem-register-0000001")).toThrow("unknown or missing");
    const result = await api.authenticatePassword({ identifier: "person@example.test", credential: "password-value" });
    expect(result.return_url).toBe("custom-app://callback?code=opaque&state=opaque");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

	it("strictly binds completion to a completed browser-session refresh", async () => {
		const completed = { ...interaction("hosted.account"), status: "completed", result_kind: "account_completed", allowed_actions: ["exchange"] };
		const valid = client(sequence(json({ interaction: completed, csrf_token: "c".repeat(32), browser_session_expires_at: later, completion: completion() })));
		await expect(valid.openBrowserSession()).resolves.toMatchObject({ completion: { return_url: "custom-app://callback?code=opaque&state=opaque" } });

		const missing = client(sequence(json({ interaction: completed, csrf_token: "c".repeat(32), browser_session_expires_at: later })));
		await expect(missing.openBrowserSession()).resolves.toMatchObject({ interaction: { status: "completed" } });

		const premature = client(sequence(json({ ...browser("hosted.account"), completion: completion() })));
		await expect(premature.openBrowserSession()).rejects.toThrow("cannot contain completion");
	});
});

function client(fetchMock: typeof fetch | ReturnType<typeof vi.fn>): HostedAccountClient {
  return new HostedAccountClient({ origin: "https://ui.example.test", interactionId, fetch: fetchMock as typeof fetch });
}
function sequence(...responses: Response[]): ReturnType<typeof vi.fn> {
  return vi.fn(async () => { const response = responses.shift(); if (!response) throw new Error("unexpected request"); return response; });
}
function json(value: unknown, status = 200): Response { return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json; charset=utf-8" } }); }
function problem(value: unknown): Response { return new Response(JSON.stringify(value), { status: (value as { status: number }).status, headers: { "Content-Type": "application/problem+json" } }); }
function empty(): Response { return new Response(null, { status: 204 }); }
function interaction(route: "hosted.auth" | "hosted.account") {
  return { interaction_id: interactionId, route_id: route, channel: "web", status: "opened", allowed_actions: ["authenticate", "cancel"], created_at: now, expires_at: later };
}
function browser(route: "hosted.auth" | "hosted.account") { return { interaction: interaction(route), csrf_token: "c".repeat(32), browser_session_expires_at: later }; }
function authBootstrap() { return { interaction: interaction("hosted.auth"), presentation: { product_name: "Product", theme_variant: null }, flow: { kind: "login" }, password_enabled: true, registration_enabled: true, recovery_enabled: true, external_providers: [] }; }
function profile(version = 1) { return { user_id: "user_test", version, display_name: "User", avatar_url: null, locale: null, timezone: null }; }
function accountBootstrap() { return { interaction: interaction("hosted.account"), presentation: { product_name: "Product", theme_variant: null }, profile: profile(), sessions: [{ session_id: "sess_other", current: false, device_label: null, created_at: now, last_seen_at: now, expires_at: later }], external_identities: [], allowed_actions: ["update_profile", "change_password", "revoke_session", "complete"] }; }
function completion() { return { interaction_id: interactionId, status: "completed", return_url: "custom-app://callback?code=opaque&state=opaque", expires_at: later }; }
