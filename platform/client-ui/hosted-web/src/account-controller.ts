import {
  HostedAccountClient,
  HostedApiError,
  HostedProtocolError,
  type ChangePasswordRequest,
  type CompleteHostedAccountRequest,
  type CompleteRecoveryRequest,
  type HostedAccountBootstrap,
  type HostedAuthBootstrap,
	type HostedAuthFlow,
  type HostedCompletion,
	type HostedInteraction,
  type HostedPasswordRequest,
  type RegisterHostedUserRequest,
  type StartRecoveryRequest,
  type StartVerificationRequest,
  type UpdateUserProfileRequest,
  type UserProfile,
} from "./account-client.js";

export type HostedAccountState = "idle" | "loading" | "ready" | "submitting" | "success" | "empty" | "failed" | "disabled";
export type HostedAccountData = HostedAuthBootstrap | HostedAccountBootstrap | HostedCompletion | HostedInteraction | HostedAuthFlow | UserProfile;
export interface HostedAccountFailure {
  readonly kind: "protocol" | "api" | "network";
  readonly code: string;
  readonly requestId?: string;
  readonly retryable: boolean;
	readonly fieldErrors?: readonly { readonly field: string; readonly code: string; readonly message?: string }[];
}
export interface HostedAccountSnapshot {
  readonly state: HostedAccountState;
  readonly data?: HostedAccountData;
  readonly error?: HostedAccountFailure;
	readonly terminal?: "expired" | "session_revoked" | "authentication_required" | "terminal";
}
export type HostedAccountListener = (snapshot: Readonly<HostedAccountSnapshot>) => void;
export type HostedMutationKind = "start_verification" | "register" | "start_recovery" | "complete_recovery" | "reset_auth_flow" | "update_profile" | "change_password" | "complete_account" | "cancel" | `revoke_session:${string}`;
interface PendingMutation { readonly key: string; readonly requestDigest: string }

export interface HostedAccountControllerOptions {
  readonly keyFactory?: () => string;
}

export class HostedAccountController {
  readonly #client: HostedAccountClient;
  readonly #keyFactory: () => string;
  readonly #listeners = new Set<HostedAccountListener>();
  readonly #pendingKeys = new Map<HostedMutationKind, PendingMutation>();
  #snapshot: Readonly<HostedAccountSnapshot> = Object.freeze({ state: "idle" });
  #active: AbortController | undefined;
  #generation = 0;
  #disposed = false;
	#route: "hosted.auth" | "hosted.account" | undefined;

  constructor(client: HostedAccountClient, options: HostedAccountControllerOptions = {}) {
    this.#client = client;
    this.#keyFactory = options.keyFactory ?? defaultKey;
  }

  getSnapshot(): Readonly<HostedAccountSnapshot> { return this.#snapshot; }
  subscribe(listener: HostedAccountListener): () => void {
    this.#assertActive();
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  start(): Promise<void> { return this.#bootstrap(); }
  refresh(): Promise<void> { return this.#bootstrap(); }

  authenticatePassword(request: HostedPasswordRequest): Promise<void> {
    return this.#submit(() => this.#client.authenticatePassword(request, this.#active?.signal));
  }
  startRegistrationVerification(request: StartVerificationRequest): Promise<void> {
    return this.#idempotent("start_verification", request, (key, signal) => this.#client.startRegistrationVerification(request, key, signal));
  }
  register(request: RegisterHostedUserRequest): Promise<void> {
    return this.#idempotent("register", request, (key, signal) => this.#client.register(request, key, signal));
  }
  startRecovery(request: StartRecoveryRequest): Promise<void> {
    return this.#idempotent("start_recovery", request, (key, signal) => this.#client.startRecovery(request, key, signal));
  }
	resetAuthFlow(): Promise<void> {
		this.#pendingKeys.delete("start_verification");
		this.#pendingKeys.delete("register");
		this.#pendingKeys.delete("start_recovery");
		this.#pendingKeys.delete("complete_recovery");
		return this.#idempotent("reset_auth_flow", {}, (key, signal) => this.#client.resetAuthFlow(key, signal), true);
	}
  completeRecovery(request: CompleteRecoveryRequest): Promise<void> {
    return this.#idempotent("complete_recovery", request, (key, signal) => this.#client.completeRecovery(request, key, signal), true);
  }
  updateProfile(request: UpdateUserProfileRequest): Promise<void> {
    return this.#idempotent("update_profile", request, (key, signal) => this.#client.updateProfile(request, key, signal), true);
  }
  changePassword(request: ChangePasswordRequest): Promise<void> {
    return this.#idempotent("change_password", request, (key, signal) => this.#client.changePassword(request, key, signal), true);
  }
  revokeSession(sessionId: string): Promise<void> {
    return this.#idempotent(`revoke_session:${sessionId}`, { session_id: sessionId }, (key, signal) => this.#client.revokeSession(sessionId, key, signal), true);
  }
  completeAccount(request: CompleteHostedAccountRequest): Promise<void> {
    return this.#idempotent("complete_account", request, (key, signal) => this.#client.completeAccount(request, key, signal));
  }
  cancel(): Promise<void> { return this.#idempotent("cancel", {}, (key, signal) => this.#client.cancel(key, signal)); }

  discardPendingMutation(kind: HostedMutationKind): void { this.#pendingKeys.delete(kind); }

  reset(): void {
    this.#abort();
    this.#client.clearSession();
    this.#pendingKeys.clear();
		this.#route = undefined;
    this.#publish({ state: "idle" });
  }

  dispose(): void {
    if (this.#disposed) return;
    this.reset();
    this.#disposed = true;
    this.#listeners.clear();
  }

  async #bootstrap(): Promise<void> {
    this.#assertActive();
    const operation = this.#begin("loading");
    try {
		const browser = await this.#client.openBrowserSession(operation.signal);
		this.#route = browser.interaction.route_id;
		if (browser.interaction.status === "completed") {
			if (!this.#current(operation)) return;
			this.#clearMutationState();
			this.#publish({ state: "success", data: browser.completion ?? browser.interaction });
			return;
		}
		if (isTerminalInteraction(browser.interaction)) {
			if (!this.#current(operation)) return;
			this.#clearMutationState();
			this.#publish({ state: "success", data: browser.interaction });
			return;
		}
		const data = await this.#reloadBoundRoute(operation.signal, browser.interaction.route_id);
		if (!this.#current(operation)) return;
		this.#publish({ state: isBootstrapEmpty(data) ? "empty" : "ready", data });
    } catch (error) {
      this.#handle(operation, error);
    }
  }

  async #idempotent<T extends HostedAccountData | void>(kind: HostedMutationKind, request: object, execute: (key: string, signal: AbortSignal) => Promise<T>, reload = false): Promise<void> {
    if (isStableSnapshot(this.#snapshot)) return;
    const requestDigest = await digestRequest(kind, request);
    if (isStableSnapshot(this.#snapshot)) return;
    const previous = this.#pendingKeys.get(kind);
    const pending = previous?.requestDigest === requestDigest ? previous : { key: this.#newKey(), requestDigest };
    this.#pendingKeys.set(kind, pending);
    const operation = this.#begin("submitting");
    try {
      let data = await execute(pending.key, operation.signal);
      if (reload) data = await this.#reloadBoundRoute(operation.signal) as T;
      if (!this.#current(operation)) return;
      this.#deletePending(kind, pending);
      if (reload && data && isBootstrap(data)) {
		this.#publish({ state: isBootstrapEmpty(data) ? "empty" : "ready", data });
		return;
	  }
      this.#publish(data === undefined ? { state: "success" } : { state: "success", data });
    } catch (error) {
      if (isDefinitive(error)) this.#deletePending(kind, pending);
      this.#handle(operation, error);
    }
  }

  async #submit<T extends HostedAccountData | void>(execute: () => Promise<T>): Promise<void> {
    if (isStableSnapshot(this.#snapshot)) return;
    const operation = this.#begin("submitting");
    try {
      const data = await execute();
      if (!this.#current(operation)) return;
      this.#publish(data === undefined ? { state: "success" } : { state: "success", data });
    } catch (error) {
      this.#handle(operation, error);
    }
  }

  #reloadBoundRoute(signal?: AbortSignal, route?: "hosted.auth" | "hosted.account"): Promise<HostedAuthBootstrap | HostedAccountBootstrap> {
	const selected = route ?? this.#route;
    if (selected === "hosted.auth") return this.#client.getAuthBootstrap(signal);
    if (selected === "hosted.account") return this.#client.getAccountBootstrap(signal);
    throw new HostedProtocolError("hosted route is unavailable for recovery");
  }

  #begin(state: "loading" | "submitting"): Operation {
    this.#assertActive();
    this.#abort();
    const controller = new AbortController();
    this.#active = controller;
    const operation = { generation: ++this.#generation, signal: controller.signal };
    this.#publish({ state, ...(this.#snapshot.data ? { data: this.#snapshot.data } : {}) });
    return operation;
  }

  #abort(): void { this.#active?.abort(); this.#active = undefined; ++this.#generation; }
  #clearMutationState(): void {
		this.#pendingKeys.clear();
		this.#client.clearSession();
		this.#route = undefined;
	}
  #deletePending(kind: HostedMutationKind, pending: PendingMutation): void {
    if (this.#pendingKeys.get(kind) === pending) this.#pendingKeys.delete(kind);
  }
  #current(operation: Operation): boolean { return !this.#disposed && operation.generation === this.#generation && !operation.signal.aborted; }
  #handle(operation: Operation, error: unknown): void {
    if (!this.#current(operation) || isAbort(error)) return;
	const failure = mapFailure(error);
	const projection = stableFailureProjection(failure);
	if (projection === "disabled") {
		this.#pendingKeys.clear();
		this.#publish({ state: "disabled", error: failure });
		return;
	}
	if (projection) {
		this.#clearMutationState();
		this.#publish({ state: "failed", error: failure, terminal: projection });
		return;
	}
    this.#publish({ state: "failed", error: failure, ...(this.#snapshot.data ? { data: this.#snapshot.data } : {}) });
  }
  #newKey(): string {
    const key = this.#keyFactory();
    if (key.length < 16 || key.length > 128) throw new HostedProtocolError("idempotency key factory returned an invalid value");
    return key;
  }
  #publish(snapshot: HostedAccountSnapshot): void {
    this.#snapshot = Object.freeze(snapshot);
    for (const listener of this.#listeners) listener(this.#snapshot);
  }
  #assertActive(): void { if (this.#disposed) throw new Error("hosted account controller is disposed"); }
}

interface Operation { readonly generation: number; readonly signal: AbortSignal }
function defaultKey(): string {
  if (!globalThis.crypto?.randomUUID) throw new HostedProtocolError("secure idempotency key generation is unavailable");
  return globalThis.crypto.randomUUID();
}
function isStableSnapshot(snapshot: Readonly<HostedAccountSnapshot>): boolean {
	if (snapshot.state === "disabled" || snapshot.terminal !== undefined) return true;
	const data = snapshot.data;
	if (!data || !("status" in data)) return false;
	if ("return_url" in data) return data.status === "completed";
	return "route_id" in data && "allowed_actions" in data && (data.status === "completed" || isTerminalInteraction(data));
}
async function digestRequest(kind: HostedMutationKind, request: object): Promise<string> {
	if (!globalThis.crypto?.subtle) throw new HostedProtocolError("secure idempotency request digest is unavailable");
	let canonical = canonicalJson({ operation: kind, request });
	const bytes = new TextEncoder().encode(canonical);
	try {
		const digest = new Uint8Array(await globalThis.crypto.subtle.digest("SHA-256", bytes));
		try {
			return Array.from(digest, (value) => value.toString(16).padStart(2, "0")).join("");
		} finally {
			digest.fill(0);
		}
	} finally {
		bytes.fill(0);
		canonical = "";
	}
}
function canonicalJson(value: unknown): string {
	if (value === null || typeof value === "string" || typeof value === "boolean") return JSON.stringify(value);
	if (typeof value === "number") {
		if (!Number.isFinite(value)) throw new HostedProtocolError("idempotency request contains an invalid number");
		return JSON.stringify(value);
	}
	if (Array.isArray(value)) return `[${value.map(canonicalJson).join(",")}]`;
	if (typeof value === "object") {
		const record = value as Record<string, unknown>;
		return `{${Object.keys(record)
			.filter((key) => record[key] !== undefined)
			.sort()
			.map((key) => `${JSON.stringify(key)}:${canonicalJson(record[key])}`)
			.join(",")}}`;
	}
	throw new HostedProtocolError("idempotency request contains an unsupported value");
}
function isBootstrapEmpty(data: HostedAuthBootstrap | HostedAccountBootstrap): boolean {
	if (!("password_enabled" in data)) return false;
	const hasAuthenticateAction = data.interaction.allowed_actions.includes("authenticate");
	const hasCancelAction = data.interaction.allowed_actions.includes("cancel");
	return !hasCancelAction && (!hasAuthenticateAction || (!data.password_enabled && !data.registration_enabled && !data.recovery_enabled));
}
function isBootstrap(data: HostedAccountData): data is HostedAuthBootstrap | HostedAccountBootstrap {
	return "interaction" in data && ("password_enabled" in data || "profile" in data);
}
function isTerminalInteraction(interaction: HostedInteraction): boolean {
	return interaction.status === "cancelled" || interaction.status === "failed" || interaction.status === "expired" || interaction.status === "exchanged";
}
function isAbort(error: unknown): boolean { return error instanceof DOMException ? error.name === "AbortError" : error instanceof Error && error.name === "AbortError"; }
function isDefinitive(error: unknown): boolean { return error instanceof HostedApiError && !error.retryable; }
function mapFailure(error: unknown): HostedAccountFailure {
	if (error instanceof HostedApiError) return Object.freeze({ kind: "api", code: error.code, requestId: error.requestId, retryable: error.retryable, ...(error.fieldErrors.length ? { fieldErrors: error.fieldErrors } : {}) });
  if (error instanceof HostedProtocolError) return Object.freeze({ kind: "protocol", code: "invalid_hosted_response", retryable: false });
  return Object.freeze({ kind: "network", code: "hosted_network_failure", retryable: true });
}
function stableFailureProjection(failure: HostedAccountFailure): "disabled" | HostedAccountSnapshot["terminal"] | undefined {
	if (failure.kind !== "api" || failure.retryable) return undefined;
	const code = failure.code.toLowerCase().split(".").at(-1);
	if (code === "capability_not_available" || code === "capability_disabled") return "disabled";
	if (code === "interaction_expired") return "expired";
	if (code === "session_revoked") return "session_revoked";
	if (code === "authentication_required") return "authentication_required";
	if (code === "interaction_terminal") return "terminal";
	return undefined;
}
