export type HostedRoute = "hosted.auth" | "hosted.account";
export type HostedChannel = "web" | "h5" | "desktop" | "mini_program" | "app";
export type HostedInteractionStatus = "created" | "opened" | "authenticating" | "completed" | "exchanged" | "cancelled" | "failed" | "expired";
export type HostedInteractionAction = "open" | "authenticate" | "complete" | "cancel" | "exchange" | "restart";

export interface HostedInteraction {
  readonly interaction_id: string;
  readonly route_id: HostedRoute;
  readonly channel: HostedChannel;
  readonly status: HostedInteractionStatus;
  readonly allowed_actions: readonly HostedInteractionAction[];
  readonly result_kind?: "authorization_code" | "account_completed" | "cancelled" | "failed" | null;
  readonly failure_code?: string | null;
  readonly created_at: string;
  readonly expires_at: string;
  readonly opened_at?: string | null;
  readonly completed_at?: string | null;
}

export interface HostedPresentation { readonly product_name: string; readonly theme_variant: string | null }
export interface HostedExternalProvider { readonly provider: "wechat" | "oidc"; readonly mode: "redirect" | "qr"; readonly display_name: string }
export interface HostedAuthFlow { readonly kind: "login" | "registration_verification" | "recovery_verification"; readonly identifier_hint?: string }
export interface HostedAuthBootstrap {
  readonly interaction: HostedInteraction;
  readonly presentation: HostedPresentation;
	readonly flow: HostedAuthFlow;
  readonly password_enabled: boolean;
  readonly registration_enabled: boolean;
  readonly recovery_enabled: boolean;
  readonly external_providers: readonly HostedExternalProvider[];
}
export interface UserProfile {
  readonly user_id: string;
  readonly version: number;
  readonly display_name?: string | null;
  readonly avatar_url?: string | null;
  readonly locale?: string | null;
  readonly timezone?: string | null;
}
export interface UserSessionSummary {
  readonly session_id: string;
  readonly current: boolean;
  readonly device_label?: string | null;
  readonly created_at: string;
  readonly last_seen_at: string;
  readonly expires_at: string;
}
export interface ExternalIdentity {
  readonly external_identity_id: string;
  readonly provider: string;
  readonly masked_subject?: string;
  readonly status: "active" | "revoked";
  readonly linked_at: string;
  readonly audit_id?: string;
}
export type HostedAccountAction = "update_profile" | "change_password" | "revoke_session" | "complete";
export interface HostedAccountBootstrap {
  readonly interaction: HostedInteraction;
  readonly presentation: HostedPresentation;
  readonly profile: UserProfile;
  readonly sessions: readonly UserSessionSummary[];
  readonly external_identities: readonly ExternalIdentity[];
  readonly allowed_actions: readonly HostedAccountAction[];
}
export interface HostedBrowserSession {
  readonly interaction: HostedInteraction;
  readonly csrf_token: string;
  readonly browser_session_expires_at: string;
	readonly completion?: HostedCompletion;
}
export interface HostedCompletion {
  readonly interaction_id: string;
  readonly status: "completed";
  readonly return_url: string;
  readonly expires_at: string;
}
export interface StartVerificationRequest { readonly identifier: string }
export interface RegisterHostedUserRequest {
  readonly credential: string;
  readonly verification_proof: string;
  readonly display_name?: string;
}
export interface StartRecoveryRequest { readonly identifier: string }
export interface CompleteRecoveryRequest { readonly recovery_proof: string; readonly new_credential: string }
export interface HostedPasswordRequest { readonly identifier: string; readonly credential: string; readonly risk_summary?: Readonly<Record<string, string | number | boolean | null>> }
export interface UpdateUserProfileRequest {
  readonly expected_version: number;
  readonly display_name?: string;
  readonly avatar_url?: string | null;
  readonly locale?: string | null;
  readonly timezone?: string | null;
}
export interface ChangePasswordRequest { readonly current_credential: string; readonly new_credential: string; readonly revoke_other_sessions: boolean }
export interface CompleteHostedAccountRequest { readonly result: "closed" | "self_service_completed" }

export class HostedProtocolError extends Error {
  constructor(message: string) { super(message); this.name = "HostedProtocolError"; }
}

export class HostedApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly requestId: string;
  readonly retryable: boolean;
  readonly retryAfterSeconds?: number;
	readonly fieldErrors: readonly HostedFieldError[];

	constructor(message: string, options: { status: number; code: string; requestId: string; retryable: boolean; retryAfterSeconds?: number; fieldErrors?: readonly HostedFieldError[] }) {
    super(message);
    this.name = "HostedApiError";
    this.status = options.status;
    this.code = options.code;
    this.requestId = options.requestId;
    this.retryable = options.retryable;
    this.retryAfterSeconds = options.retryAfterSeconds;
		this.fieldErrors = Object.freeze([...(options.fieldErrors ?? [])]);
  }
}

export interface HostedFieldError { readonly field: string; readonly code: string; readonly message?: string }

export interface HostedAccountClientOptions {
  readonly origin: string | URL;
  readonly interactionId: string;
  readonly fetch?: typeof fetch;
}

const interactionPattern = /^hint_[A-Za-z0-9_-]{24,160}$/;
const identifierPattern = /^[A-Za-z][A-Za-z0-9_-]{2,127}$/;
const idempotencyPattern = /^.{16,128}$/s;
const jsonMediaType = /^application\/json(?:\s*;\s*charset=utf-8)?$/i;
const problemMediaType = /^application\/problem\+json(?:\s*;\s*charset=utf-8)?$/i;

export class HostedAccountClient {
  readonly interactionId: string;
  readonly #origin: string;
  readonly #fetch: typeof fetch;
  #csrfToken: string | undefined;
	#route: HostedRoute | undefined;

  constructor(options: HostedAccountClientOptions) {
    if (!interactionPattern.test(options.interactionId)) throw new TypeError("invalid hosted interaction id");
    const origin = new URL(options.origin.toString());
    if (origin.username || origin.password || origin.pathname !== "/" || origin.search || origin.hash) throw new TypeError("hosted API origin must be exact");
    if (origin.protocol !== "https:" && !(origin.protocol === "http:" && isLoopback(origin.hostname))) throw new TypeError("hosted API requires HTTPS outside loopback development");
    this.interactionId = options.interactionId;
    this.#origin = origin.origin;
    this.#fetch = options.fetch ?? fetch;
  }

  clearSession(): void { this.#csrfToken = undefined; this.#route = undefined; }
  hasBrowserSession(): boolean { return this.#csrfToken !== undefined; }

  async openBrowserSession(signal?: AbortSignal): Promise<HostedBrowserSession> {
    this.clearSession();
    const result = await this.#json("POST", "browser-session", 200, parseBrowserSession, { signal });
	if (result.interaction.interaction_id !== this.interactionId) throw new HostedProtocolError("hosted interaction response does not match request path");
	this.#csrfToken = result.csrf_token;
	this.#route = result.interaction.route_id;
    return result;
  }

  getAuthBootstrap(signal?: AbortSignal): Promise<HostedAuthBootstrap> {
    return this.#sessionJSON("GET", "auth/bootstrap", 200, parseAuthBootstrap, { signal, route: "hosted.auth" });
  }

  authenticatePassword(request: HostedPasswordRequest, signal?: AbortSignal): Promise<HostedCompletion> {
    assertRequest(request, ["identifier", "credential"], ["risk_summary"]);
    return this.#sessionJSON("POST", "auth/password", 200, parseCompletion, { body: request, signal, route: "hosted.auth" });
  }

	startRegistrationVerification(request: StartVerificationRequest, idempotencyKey: string, signal?: AbortSignal): Promise<HostedAuthFlow> {
    assertRequest(request, ["identifier"]);
		return this.#sessionJSON("POST", "auth/verification/start", 202, parseAuthFlow, { body: request, idempotencyKey, signal, route: "hosted.auth" });
  }

  register(request: RegisterHostedUserRequest, idempotencyKey: string, signal?: AbortSignal): Promise<HostedCompletion> {
		assertRequest(request, ["credential", "verification_proof"], ["display_name"]);
    return this.#sessionJSON("POST", "auth/register", 200, parseCompletion, { body: request, idempotencyKey, signal, route: "hosted.auth" });
  }

	startRecovery(request: StartRecoveryRequest, idempotencyKey: string, signal?: AbortSignal): Promise<HostedAuthFlow> {
    assertRequest(request, ["identifier"]);
		return this.#sessionJSON("POST", "auth/recovery/start", 202, parseAuthFlow, { body: request, idempotencyKey, signal, route: "hosted.auth" });
  }

  completeRecovery(request: CompleteRecoveryRequest, idempotencyKey: string, signal?: AbortSignal): Promise<void> {
		assertRequest(request, ["recovery_proof", "new_credential"]);
	return this.#sessionVoid("POST", "auth/recovery/complete", { body: request, idempotencyKey, signal, route: "hosted.auth" });
  }

	resetAuthFlow(idempotencyKey: string, signal?: AbortSignal): Promise<void> {
		return this.#sessionVoid("DELETE", "auth/flow", { idempotencyKey, signal, route: "hosted.auth" });
	}

  getAccountBootstrap(signal?: AbortSignal): Promise<HostedAccountBootstrap> {
    return this.#sessionJSON("GET", "account/bootstrap", 200, parseAccountBootstrap, { signal, route: "hosted.account" });
  }

  updateProfile(request: UpdateUserProfileRequest, idempotencyKey: string, signal?: AbortSignal): Promise<UserProfile> {
    assertRequest(request, ["expected_version"], ["display_name", "avatar_url", "locale", "timezone"]);
    return this.#sessionJSON("PATCH", "account/profile", 200, parseUserProfile, { body: request, idempotencyKey, signal, route: "hosted.account" });
  }

  changePassword(request: ChangePasswordRequest, idempotencyKey: string, signal?: AbortSignal): Promise<void> {
    assertRequest(request, ["current_credential", "new_credential", "revoke_other_sessions"]);
	return this.#sessionVoid("POST", "account/password", { body: request, idempotencyKey, signal, route: "hosted.account" });
  }

  revokeSession(sessionId: string, idempotencyKey: string, signal?: AbortSignal): Promise<void> {
    if (!identifierPattern.test(sessionId)) throw new TypeError("invalid session id");
	return this.#sessionVoid("DELETE", `account/sessions/${encodeURIComponent(sessionId)}`, { idempotencyKey, signal, route: "hosted.account" });
  }

  completeAccount(request: CompleteHostedAccountRequest, idempotencyKey: string, signal?: AbortSignal): Promise<HostedCompletion> {
    assertRequest(request, ["result"]);
    return this.#sessionJSON("POST", "account/complete", 200, parseCompletion, { body: request, idempotencyKey, signal, route: "hosted.account" });
  }

  cancel(idempotencyKey: string, signal?: AbortSignal): Promise<HostedInteraction> {
    return this.#sessionJSON("POST", "cancel", 200, parseInteraction, { idempotencyKey, signal });
  }

	async #sessionVoid(method: string, suffix: string, options: RequestOptions & { route?: HostedRoute }): Promise<void> {
		if (options.route) this.#requireRoute(options.route);
    await this.#void(method, suffix, 204, { ...options, csrf: this.#requireCsrf() });
  }

  #sessionJSON<T>(method: string, suffix: string, status: number, parser: (value: unknown) => T, options: RequestOptions & { route?: HostedRoute }): Promise<T> {
	if (options.route) this.#requireRoute(options.route);
    return this.#json(method, suffix, status, parser, { ...options, csrf: this.#requireCsrf() }).then((value) => {
      const responseId = interactionIdFrom(value);
      if (responseId && responseId !== this.interactionId) throw new HostedProtocolError("hosted interaction response does not match request path");
      const interaction = interactionFrom(value);
      if (interaction && options.route && interaction.route_id !== options.route) throw new HostedProtocolError("hosted route response does not match requested endpoint");
      return value;
    });
  }

  #requireCsrf(): string {
    if (!this.#csrfToken) throw new HostedProtocolError("hosted browser session is not open");
    return this.#csrfToken;
  }
	#requireRoute(route: HostedRoute): void {
		if (this.#route !== route) throw new HostedProtocolError("hosted endpoint does not match the bound interaction route");
	}

  async #json<T>(method: string, suffix: string, status: number, parser: (value: unknown) => T, options: RequestOptions): Promise<T> {
    const response = await this.#request(method, suffix, options);
    if (response.status !== status) return await throwHostedError(response);
    requireContentType(response, jsonMediaType, "application/json");
    return parser(await parseJSON(response));
  }

  async #void(method: string, suffix: string, status: number, options: RequestOptions): Promise<void> {
    const response = await this.#request(method, suffix, options);
    if (response.status !== status) return await throwHostedError(response);
    if (response.headers.get("content-type")) throw new HostedProtocolError("empty hosted response must not declare content type");
    if ((await response.text()).length !== 0) throw new HostedProtocolError("empty hosted response contained a body");
  }

  #request(method: string, suffix: string, options: RequestOptions): Promise<Response> {
    if (options.idempotencyKey !== undefined && !idempotencyPattern.test(options.idempotencyKey)) throw new TypeError("invalid idempotency key");
    const headers = new Headers({ Accept: "application/json, application/problem+json" });
    if (options.csrf) headers.set("X-CSRF-Token", options.csrf);
    if (options.idempotencyKey) headers.set("Idempotency-Key", options.idempotencyKey);
    let body: string | undefined;
    if (options.body !== undefined) {
      headers.set("Content-Type", "application/json");
      body = JSON.stringify(options.body);
    }
    return this.#fetch(`${this.#origin}/api/v1/hosted/interactions/${this.interactionId}/${suffix}`, {
      method, headers, body, credentials: "include", redirect: "error", cache: "no-store", signal: options.signal,
    });
  }
}

interface RequestOptions { readonly body?: object; readonly idempotencyKey?: string; readonly csrf?: string; readonly signal?: AbortSignal }

function isLoopback(hostname: string): boolean { return hostname === "127.0.0.1" || hostname === "localhost" || hostname === "[::1]"; }
function assertRequest(value: object, required: readonly string[], optional: readonly string[] = []): void {
  const record = value as Record<string, unknown>;
  const keys = new Set([...required, ...optional]);
  if (Object.keys(record).some((key) => !keys.has(key)) || required.some((key) => !Object.prototype.hasOwnProperty.call(record, key))) {
    throw new TypeError("hosted request contains unknown or missing fields");
  }
}

function requireContentType(response: Response, expected: RegExp, label: string): void {
  const value = response.headers.get("content-type") ?? "";
  if (!expected.test(value.trim())) throw new HostedProtocolError(`hosted response must use ${label}`);
}
async function parseJSON(response: Response): Promise<unknown> {
  try { return JSON.parse(await response.text()) as unknown; }
  catch { throw new HostedProtocolError("hosted response contained invalid JSON"); }
}
async function throwHostedError(response: Response): Promise<never> {
  requireContentType(response, problemMediaType, "application/problem+json");
  const value = object(await parseJSON(response), ["type", "title", "status", "code", "request_id", "retryable"], ["detail", "retry_after_seconds", "field_errors"]);
  if (integer(value.status, "status") !== response.status) throw new HostedProtocolError("hosted error status does not match HTTP status");
	const fieldErrors = value.field_errors === undefined ? [] : array(value.field_errors, "field_errors").map((item): HostedFieldError => {
		const field = object(item, ["field", "code"], ["message"]);
		return Object.freeze({ field: string(field.field, "field"), code: string(field.code, "code"), ...(field.message === undefined ? {} : { message: string(field.message, "message") }) });
	});
  throw new HostedApiError(string(value.title, "title"), {
    status: response.status, code: matchingString(value.code, "code", /^[A-Za-z0-9_.-]+$/), requestId: string(value.request_id, "request_id"),
    retryable: boolean(value.retryable, "retryable"), retryAfterSeconds: value.retry_after_seconds === undefined ? undefined : nonNegativeInteger(value.retry_after_seconds, "retry_after_seconds"),
		fieldErrors,
  });
}

function parseInteraction(input: unknown): HostedInteraction {
  const value = object(input, ["interaction_id", "route_id", "channel", "status", "allowed_actions", "created_at", "expires_at"], ["result_kind", "failure_code", "opened_at", "completed_at"]);
  const actions = uniqueEnumArray(value.allowed_actions, "allowed_actions", ["open", "authenticate", "complete", "cancel", "exchange", "restart"] as const);
  return Object.freeze({
    interaction_id: matchingString(value.interaction_id, "interaction_id", interactionPattern),
    route_id: enumValue(value.route_id, "route_id", ["hosted.auth", "hosted.account"] as const),
    channel: enumValue(value.channel, "channel", ["web", "h5", "desktop", "mini_program", "app"] as const),
    status: enumValue(value.status, "status", ["created", "opened", "authenticating", "completed", "exchanged", "cancelled", "failed", "expired"] as const),
    allowed_actions: actions,
    ...(value.result_kind === undefined ? {} : { result_kind: nullableEnum(value.result_kind, "result_kind", ["authorization_code", "account_completed", "cancelled", "failed"] as const) }),
    ...(value.failure_code === undefined ? {} : { failure_code: nullableString(value.failure_code, "failure_code") }),
    created_at: timestamp(value.created_at, "created_at"), expires_at: timestamp(value.expires_at, "expires_at"),
    ...(value.opened_at === undefined ? {} : { opened_at: nullableTimestamp(value.opened_at, "opened_at") }),
    ...(value.completed_at === undefined ? {} : { completed_at: nullableTimestamp(value.completed_at, "completed_at") }),
  });
}

function parseBrowserSession(input: unknown): HostedBrowserSession {
	const value = object(input, ["interaction", "csrf_token", "browser_session_expires_at"], ["completion"]);
  const csrf = string(value.csrf_token, "csrf_token");
  if (csrf.length < 32 || csrf.length > 256) throw new HostedProtocolError("invalid csrf_token");
	const interaction = parseInteraction(value.interaction);
	if (interaction.status !== "completed" && value.completion !== undefined) throw new HostedProtocolError("non-completed hosted browser session cannot contain completion");
	const completion = value.completion === undefined ? undefined : parseCompletion(value.completion);
	if (completion && completion.interaction_id !== interaction.interaction_id) throw new HostedProtocolError("hosted completion does not match browser session interaction");
	return Object.freeze({ interaction, csrf_token: csrf, browser_session_expires_at: timestamp(value.browser_session_expires_at, "browser_session_expires_at"), ...(completion ? { completion } : {}) });
}
function parsePresentation(input: unknown): HostedPresentation {
  const value = object(input, ["product_name", "theme_variant"]);
  return Object.freeze({ product_name: string(value.product_name, "product_name"), theme_variant: nullableString(value.theme_variant, "theme_variant") });
}
function parseAuthBootstrap(input: unknown): HostedAuthBootstrap {
	const value = object(input, ["interaction", "presentation", "flow", "password_enabled", "registration_enabled", "recovery_enabled", "external_providers"]);
	return Object.freeze({ interaction: parseInteraction(value.interaction), presentation: parsePresentation(value.presentation), flow: parseAuthFlow(value.flow), password_enabled: boolean(value.password_enabled, "password_enabled"), registration_enabled: boolean(value.registration_enabled, "registration_enabled"), recovery_enabled: boolean(value.recovery_enabled, "recovery_enabled"), external_providers: array(value.external_providers, "external_providers").map(parseExternalProvider) });
}
function parseAuthFlow(input: unknown): HostedAuthFlow {
	const value = object(input, ["kind"], ["identifier_hint"]);
	return Object.freeze({ kind: enumValue(value.kind, "kind", ["login", "registration_verification", "recovery_verification"] as const), ...(value.identifier_hint === undefined ? {} : { identifier_hint: string(value.identifier_hint, "identifier_hint") }) });
}
function parseExternalProvider(input: unknown): HostedExternalProvider {
  const value = object(input, ["provider", "mode", "display_name"]);
  return Object.freeze({ provider: enumValue(value.provider, "provider", ["wechat", "oidc"] as const), mode: enumValue(value.mode, "mode", ["redirect", "qr"] as const), display_name: string(value.display_name, "display_name") });
}
function parseUserProfile(input: unknown): UserProfile {
  const value = object(input, ["user_id", "version"], ["display_name", "avatar_url", "locale", "timezone"]);
  return Object.freeze({ user_id: identifier(value.user_id, "user_id"), version: positiveInteger(value.version, "version"), ...(value.display_name === undefined ? {} : { display_name: nullableString(value.display_name, "display_name") }), ...(value.avatar_url === undefined ? {} : { avatar_url: nullableString(value.avatar_url, "avatar_url") }), ...(value.locale === undefined ? {} : { locale: nullableString(value.locale, "locale") }), ...(value.timezone === undefined ? {} : { timezone: nullableString(value.timezone, "timezone") }) });
}
function parseSession(input: unknown): UserSessionSummary {
  const value = object(input, ["session_id", "current", "created_at", "last_seen_at", "expires_at"], ["device_label"]);
  return Object.freeze({ session_id: identifier(value.session_id, "session_id"), current: boolean(value.current, "current"), ...(value.device_label === undefined ? {} : { device_label: nullableString(value.device_label, "device_label") }), created_at: timestamp(value.created_at, "created_at"), last_seen_at: timestamp(value.last_seen_at, "last_seen_at"), expires_at: timestamp(value.expires_at, "expires_at") });
}
function parseExternalIdentity(input: unknown): ExternalIdentity {
  const value = object(input, ["external_identity_id", "provider", "status", "linked_at"], ["masked_subject", "audit_id"]);
  return Object.freeze({ external_identity_id: identifier(value.external_identity_id, "external_identity_id"), provider: string(value.provider, "provider"), status: enumValue(value.status, "status", ["active", "revoked"] as const), linked_at: timestamp(value.linked_at, "linked_at"), ...(value.masked_subject === undefined ? {} : { masked_subject: string(value.masked_subject, "masked_subject") }), ...(value.audit_id === undefined ? {} : { audit_id: identifier(value.audit_id, "audit_id") }) });
}
function parseAccountBootstrap(input: unknown): HostedAccountBootstrap {
  const value = object(input, ["interaction", "presentation", "profile", "sessions", "external_identities", "allowed_actions"]);
  return Object.freeze({ interaction: parseInteraction(value.interaction), presentation: parsePresentation(value.presentation), profile: parseUserProfile(value.profile), sessions: array(value.sessions, "sessions").map(parseSession), external_identities: array(value.external_identities, "external_identities").map(parseExternalIdentity), allowed_actions: uniqueEnumArray(value.allowed_actions, "allowed_actions", ["update_profile", "change_password", "revoke_session", "complete"] as const) });
}
function parseCompletion(input: unknown): HostedCompletion {
  const value = object(input, ["interaction_id", "status", "return_url", "expires_at"]);
  if (value.status !== "completed") throw new HostedProtocolError("hosted completion has invalid status");
  return Object.freeze({ interaction_id: matchingString(value.interaction_id, "interaction_id", interactionPattern), status: "completed", return_url: string(value.return_url, "return_url"), expires_at: timestamp(value.expires_at, "expires_at") });
}
function interactionFrom(value: unknown): HostedInteraction | undefined {
  if (typeof value !== "object" || value === null) return undefined;
  const record = value as Record<string, unknown>;
  if (record.interaction && typeof record.interaction === "object") return record.interaction as HostedInteraction;
  if (typeof record.interaction_id === "string" && typeof record.route_id === "string") return value as HostedInteraction;
  return undefined;
}
function interactionIdFrom(value: unknown): string | undefined {
  if (typeof value !== "object" || value === null) return undefined;
  const record = value as Record<string, unknown>;
  if (typeof record.interaction_id === "string") return record.interaction_id;
  if (typeof record.interaction === "object" && record.interaction !== null) {
    const nested = record.interaction as Record<string, unknown>;
    return typeof nested.interaction_id === "string" ? nested.interaction_id : undefined;
  }
  return undefined;
}

function object(input: unknown, required: readonly string[], optional: readonly string[] = []): Record<string, unknown> {
  if (typeof input !== "object" || input === null || Array.isArray(input)) throw new HostedProtocolError("hosted response expected an object");
  const value = input as Record<string, unknown>;
  const allowed = new Set([...required, ...optional]);
  if (Object.keys(value).some((key) => !allowed.has(key)) || required.some((key) => !Object.prototype.hasOwnProperty.call(value, key))) throw new HostedProtocolError("hosted response contains unknown or missing fields");
  return value;
}
function array(input: unknown, field: string): unknown[] { if (!Array.isArray(input)) throw new HostedProtocolError(`${field} must be an array`); return input; }
function string(input: unknown, field: string): string { if (typeof input !== "string") throw new HostedProtocolError(`${field} must be a string`); return input; }
function boolean(input: unknown, field: string): boolean { if (typeof input !== "boolean") throw new HostedProtocolError(`${field} must be a boolean`); return input; }
function integer(input: unknown, field: string): number { if (typeof input !== "number" || !Number.isInteger(input)) throw new HostedProtocolError(`${field} must be an integer`); return input; }
function positiveInteger(input: unknown, field: string): number { const value = integer(input, field); if (value < 1) throw new HostedProtocolError(`${field} must be positive`); return value; }
function nonNegativeInteger(input: unknown, field: string): number { const value = integer(input, field); if (value < 0) throw new HostedProtocolError(`${field} must be non-negative`); return value; }
function matchingString(input: unknown, field: string, pattern: RegExp): string { const value = string(input, field); if (!pattern.test(value)) throw new HostedProtocolError(`${field} has invalid format`); return value; }
function identifier(input: unknown, field: string): string { return matchingString(input, field, identifierPattern); }
function nullableString(input: unknown, field: string): string | null { return input === null ? null : string(input, field); }
function timestamp(input: unknown, field: string): string { const value = string(input, field); if (!/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:\d\d)$/.test(value) || Number.isNaN(Date.parse(value))) throw new HostedProtocolError(`${field} must be a timestamp`); return value; }
function nullableTimestamp(input: unknown, field: string): string | null { return input === null ? null : timestamp(input, field); }
function enumValue<const T extends readonly string[]>(input: unknown, field: string, values: T): T[number] { const value = string(input, field); if (!(values as readonly string[]).includes(value)) throw new HostedProtocolError(`${field} has unsupported value`); return value as T[number]; }
function nullableEnum<const T extends readonly string[]>(input: unknown, field: string, values: T): T[number] | null { return input === null ? null : enumValue(input, field, values); }
function uniqueEnumArray<const T extends readonly string[]>(input: unknown, field: string, values: T): readonly T[number][] { const result = array(input, field).map((item) => enumValue(item, field, values)); if (new Set(result).size !== result.length) throw new HostedProtocolError(`${field} contains duplicates`); return Object.freeze(result); }
