import type { AdminSession, ApiErrorEnvelope } from "../types";

const authBasePath = "/api/v1/admin/auth";
const unsafeMethods = new Set(["POST", "PUT", "PATCH", "DELETE"]);

interface AuthApiErrorOptions {
  status: number;
  code: string;
  retryable: boolean;
  requestId?: string;
  retryAfterSeconds?: number;
}

export class AuthApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;
  readonly requestId?: string;
  readonly retryAfterSeconds?: number;

  constructor(message: string, options: AuthApiErrorOptions) {
    super(message);
    this.name = "AuthApiError";
    this.status = options.status;
    this.code = options.code;
    this.retryable = options.retryable;
    this.requestId = options.requestId;
    this.retryAfterSeconds = options.retryAfterSeconds;
  }
}

function isErrorEnvelope(value: unknown): value is ApiErrorEnvelope {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Partial<ApiErrorEnvelope>;
  return typeof candidate.status === "number"
    && typeof candidate.code === "string"
    && typeof candidate.title === "string"
    && typeof candidate.request_id === "string"
    && typeof candidate.retryable === "boolean";
}

async function readError(response: Response) {
  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    payload = null;
  }
  if (isErrorEnvelope(payload)) {
    return new AuthApiError(payload.title, {
      status: response.status,
      code: payload.code,
      retryable: payload.retryable,
      requestId: payload.request_id,
      retryAfterSeconds: payload.retry_after_seconds,
    });
  }
  return new AuthApiError("认证服务请求失败", {
    status: response.status,
    code: "admin_auth.request_failed",
    retryable: response.status >= 500,
    requestId: response.headers.get("X-Request-Id") ?? undefined,
  });
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");

  let response: Response;
  try {
    response = await fetch(path, { ...init, headers, credentials: "include" });
  } catch {
    throw new AuthApiError("认证服务不可用，请确认后端已经启动后重试", {
      status: 0,
      code: "admin_auth.service_unavailable",
      retryable: true,
    });
  }

  if (!response.ok) throw await readError(response);
  if (response.status === 204) return undefined as T;
  try {
    return await response.json() as T;
  } catch {
    throw new AuthApiError("认证服务返回了无法识别的数据", {
      status: response.status,
      code: "admin_auth.invalid_response",
      retryable: true,
      requestId: response.headers.get("X-Request-Id") ?? undefined,
    });
  }
}

let refreshInFlight: Promise<AdminSession> | null = null;
let sessionSyncInFlight: Promise<AdminSession | null> | null = null;
let currentAdminSession: AdminSession | null = null;
const sessionInvalidatedListeners = new Set<() => void>();
const sessionEpochStorageKey = "platform_admin_session_epoch_v1";
const refreshLockName = "platform-admin-auth-refresh-v1";
const terminalSessionCodes = new Set([
  "admin_auth.session_expired",
  "admin_auth.session_revoked",
  "admin_auth.refresh_replayed",
]);
let observedSessionEpoch = readSharedSessionEpoch();

interface BrowserLockManager {
  request<T>(name: string, callback: () => T | PromiseLike<T>): Promise<T>;
}

function readSharedSessionEpoch() {
  try {
    return typeof window === "undefined" ? null : window.localStorage.getItem(sessionEpochStorageKey);
  } catch {
    return null;
  }
}

function publishSharedSessionEpoch() {
  const epoch = typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random()}`;
  try {
    window.localStorage.setItem(sessionEpochStorageKey, epoch);
    observedSessionEpoch = epoch;
  } catch {
    // The epoch is coordination metadata only; memory-only auth remains valid.
  }
}

function getBrowserLockManager() {
  if (typeof navigator === "undefined") return null;
  return (navigator as Navigator & { locks?: BrowserLockManager }).locks ?? null;
}

function rememberSession(session: AdminSession, publish = false) {
  if (currentAdminSession && session.session_version < currentAdminSession.session_version) {
    return currentAdminSession;
  }
  currentAdminSession = session;
  if (publish) publishSharedSessionEpoch();
  return session;
}

function isTerminalSessionFailure(reason: unknown) {
  return reason instanceof AuthApiError
    && reason.status === 401
    && terminalSessionCodes.has(reason.code);
}

function invalidateRememberedSession(publish = false) {
  currentAdminSession = null;
  if (publish) publishSharedSessionEpoch();
  for (const listener of sessionInvalidatedListeners) listener();
}

function handleTerminalSessionFailure(reason: unknown) {
  if (isTerminalSessionFailure(reason)) invalidateRememberedSession(true);
  return reason;
}

async function refreshAfterCheckingCurrentSession() {
  try {
    const recovered = await request<AdminSession>(`${authBasePath}/session`);
    observedSessionEpoch = readSharedSessionEpoch();
    return rememberSession(recovered);
  } catch (reason) {
    if (!(reason instanceof AuthApiError)
      || reason.status !== 401
      || reason.code !== "admin_auth.session_expired") {
      throw handleTerminalSessionFailure(reason);
    }
  }
  return request<AdminSession>(`${authBasePath}/refresh`, {
    method: "POST",
    body: JSON.stringify({ transport: "cookie" }),
  }).then((session) => rememberSession(session, true)).catch((reason: unknown) => {
    throw handleTerminalSessionFailure(reason);
  });
}

function refreshAdminSession() {
  if (!refreshInFlight) {
    const locks = getBrowserLockManager();
    const refresh = locks
      ? locks.request<AdminSession>(refreshLockName, refreshAfterCheckingCurrentSession)
      : request<AdminSession>(`${authBasePath}/refresh`, {
        method: "POST",
        body: JSON.stringify({ transport: "cookie" }),
      }).then((session) => rememberSession(session, true)).catch((reason: unknown) => {
        throw handleTerminalSessionFailure(reason);
      });
    refreshInFlight = refresh.finally(() => {
      refreshInFlight = null;
    });
  }
  return refreshInFlight;
}

function synchronizeSharedSession() {
  const sharedEpoch = readSharedSessionEpoch();
  if (!sharedEpoch || sharedEpoch === observedSessionEpoch) return Promise.resolve<AdminSession | null>(null);
  if (!sessionSyncInFlight) {
    sessionSyncInFlight = request<AdminSession>(`${authBasePath}/session`).then((session) => {
      observedSessionEpoch = sharedEpoch;
      return rememberSession(session);
    }).catch((reason: unknown) => {
      if (reason instanceof AuthApiError
        && reason.status === 401
        && reason.code === "admin_auth.session_expired") {
        observedSessionEpoch = sharedEpoch;
        return null;
      }
      throw handleTerminalSessionFailure(reason);
    }).finally(() => {
      sessionSyncInFlight = null;
    });
  }
  return sessionSyncInFlight;
}

async function sendAuthenticatedAdminRequest<T>(path: string, init: RequestInit, csrfToken: string | null) {
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  if (unsafeMethods.has(method)) {
    if (!csrfToken) {
      throw new AuthApiError("管理会话缺少安全校验信息，请重新登录", {
        status: 401,
        code: "admin_auth.csrf_missing",
        retryable: false,
      });
    }
    headers.set("X-CSRF-Token", csrfToken);
  }
  return request<T>(path, { ...init, headers });
}

export async function authenticatedAdminRequest<T>(path: string, init: RequestInit = {}, csrfToken: string | null = null) {
  if (!path.startsWith("/api/v1/admin/")) {
    throw new AuthApiError("管理请求地址不在允许范围内", {
      status: 400,
      code: "admin_auth.invalid_admin_api_path",
      retryable: false,
    });
  }

  await synchronizeSharedSession();
  const requestCsrfToken = unsafeMethods.has((init.method ?? "GET").toUpperCase())
    ? currentAdminSession?.csrf_token ?? csrfToken
    : csrfToken;
  try {
    return await sendAuthenticatedAdminRequest<T>(path, init, requestCsrfToken);
  } catch (reason) {
    if (!(reason instanceof AuthApiError)
      || reason.status !== 401
      || reason.code !== "admin_auth.session_expired") {
      throw handleTerminalSessionFailure(reason);
    }

    await refreshAdminSession();
    try {
      return await sendAuthenticatedAdminRequest<T>(path, init, currentAdminSession?.csrf_token ?? null);
    } catch (replayReason) {
      throw handleTerminalSessionFailure(replayReason);
    }
  }
}

export function getAdminCsrfToken() {
  return currentAdminSession?.csrf_token ?? null;
}

export function resetAdminAuthStateForTests() {
  refreshInFlight = null;
  sessionSyncInFlight = null;
  currentAdminSession = null;
  observedSessionEpoch = null;
  sessionInvalidatedListeners.clear();
  try {
    if (typeof window !== "undefined") window.localStorage.removeItem(sessionEpochStorageKey);
  } catch {
    // Tests may provide a window without storage access.
  }
}

export function subscribeAdminSessionInvalidated(listener: () => void) {
  sessionInvalidatedListeners.add(listener);
  return () => {
    sessionInvalidatedListeners.delete(listener);
  };
}

export const authClient = {
  getSession() {
    // An expired access cookie is recoverable while the refresh cookie is
    // still valid. AuthContext owns that initial recovery decision.
    return request<AdminSession>(`${authBasePath}/session`).then((session) => {
      observedSessionEpoch = readSharedSessionEpoch();
      return rememberSession(session);
    });
  },
  login(identifier: string, credential: string) {
    return request<AdminSession>(`${authBasePath}/login`, {
      method: "POST",
      body: JSON.stringify({ identifier, credential, transport: "cookie" }),
    }).then((session) => rememberSession(session, true));
  },
  refresh() {
    return refreshAdminSession();
  },
  logout(csrfToken: string | null) {
    return authenticatedAdminRequest<void>(`${authBasePath}/logout`, { method: "POST" }, currentAdminSession?.csrf_token ?? csrfToken).then(() => {
      currentAdminSession = null;
      publishSharedSessionEpoch();
    });
  },
};

export function isAuthenticationFailure(reason: unknown) {
  return isTerminalSessionFailure(reason);
}

export function getAuthErrorMessage(reason: unknown) {
  if (!(reason instanceof AuthApiError)) return "认证请求失败，请重试";
  if (reason.code === "admin_auth.invalid_credentials" || reason.status === 401) return "登录信息不正确或会话已经失效，请重新登录";
  if (reason.code === "admin_auth.rate_limited" || reason.status === 429) {
    return reason.retryAfterSeconds
      ? `登录尝试过于频繁，请在 ${reason.retryAfterSeconds} 秒后重试`
      : "登录尝试过于频繁，请稍后重试";
  }
  if (reason.code === "admin_auth.additional_verification_required") return "当前登录需要额外安全验证";
  if (reason.code === "admin_auth.service_unavailable") return "认证服务不可用，请确认后端已经启动后重试";
  if (reason.status === 404) return "管理员认证接口尚未由后端提供，请启动包含认证模块的后端服务";
  if (reason.status >= 500 || reason.status === 0) return "认证服务暂时不可用，请确认后端已经启动后重试";
  return "认证请求未完成，请重试";
}
