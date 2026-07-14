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

export async function authenticatedAdminRequest<T>(path: string, init: RequestInit = {}, csrfToken: string | null = null) {
  if (!path.startsWith("/api/v1/admin/")) {
    throw new AuthApiError("管理请求地址不在允许范围内", {
      status: 400,
      code: "admin_auth.invalid_admin_api_path",
      retryable: false,
    });
  }
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

let refreshInFlight: Promise<AdminSession> | null = null;
let currentCsrfToken: string | null = null;

function rememberSession(session: AdminSession) {
  currentCsrfToken = session.csrf_token;
  return session;
}

export function getAdminCsrfToken() {
  return currentCsrfToken;
}

export const authClient = {
  getSession() {
    return request<AdminSession>(`${authBasePath}/session`).then(rememberSession);
  },
  login(identifier: string, credential: string) {
    return request<AdminSession>(`${authBasePath}/login`, {
      method: "POST",
      body: JSON.stringify({ identifier, credential, transport: "cookie" }),
    }).then(rememberSession);
  },
  refresh() {
    if (!refreshInFlight) {
      refreshInFlight = request<AdminSession>(`${authBasePath}/refresh`, {
        method: "POST",
        body: JSON.stringify({ transport: "cookie" }),
      }).then(rememberSession).finally(() => {
        refreshInFlight = null;
      });
    }
    return refreshInFlight;
  },
  logout(csrfToken: string | null) {
    return authenticatedAdminRequest<void>(`${authBasePath}/logout`, { method: "POST" }, csrfToken).finally(() => {
      currentCsrfToken = null;
    });
  },
};

export function isAuthenticationFailure(reason: unknown) {
  return reason instanceof AuthApiError && (reason.status === 401 || reason.status === 403);
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
