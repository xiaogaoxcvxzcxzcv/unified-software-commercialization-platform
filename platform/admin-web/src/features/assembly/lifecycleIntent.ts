import { AuthApiError } from "../../api/authClient";

const keyPattern = /^assembly-lifecycle-[A-Za-z0-9-]{8,100}$/;

export function lifecycleIntent(kind: string, id: string) {
  const storageKey = `assembly_lifecycle_intent:${kind}:${id}`;
  try {
    const saved = sessionStorage.getItem(storageKey);
    if (saved && keyPattern.test(saved)) return saved;
    const created = `assembly-lifecycle-${crypto.randomUUID()}`;
    sessionStorage.setItem(storageKey, created);
    return created;
  } catch {
    return `assembly-lifecycle-${crypto.randomUUID()}`;
  }
}

export function clearLifecycleIntent(kind: string, id: string) {
  try { sessionStorage.removeItem(`assembly_lifecycle_intent:${kind}:${id}`); } catch { /* Memory-only fallback expires with the page. */ }
}

export function lifecycleRequiresReauthentication(reason: unknown) {
  return reason instanceof AuthApiError
    && reason.status === 403
    && reason.code === "admin_auth.reauthentication_required";
}

export function lifecycleHasIdempotencyConflict(reason: unknown) {
  return reason instanceof AuthApiError
    && reason.status === 409
    && reason.code === "assembly.idempotency_conflict";
}

export function lifecycleErrorMessage(reason: unknown, fallback: string) {
  if (reason instanceof AuthApiError) {
    const localized: Record<string, string> = {
      "assembly.not_found": "未找到该生命周期资源",
      "assembly.lifecycle_unavailable": "受信生命周期服务尚未就绪，请稍后重试",
      "assembly.version_conflict": "资源版本已变化，请刷新后重试",
      "assembly.conflict": "资源状态不允许当前操作，请刷新后重试",
      "assembly.idempotency_conflict": "该操作请求已发生变化，请重新发起",
      "assembly.operation_in_progress": "已有生命周期操作正在执行，请稍后刷新",
      "assembly.plan_not_executable": "该生命周期计划当前不可执行",
      "assembly.plan_not_confirmed": "生命周期计划确认已失效，请刷新后重试",
      "assembly.invalid_request": "提交内容不符合生命周期操作要求",
      "admin_auth.reauthentication_required": "此操作需要近期重新认证。请重新登录后返回当前页面继续。",
    };
    const message = localized[reason.code];
    if (message) return message;
    return reason.requestId ? `${fallback}（请求编号：${reason.requestId}）` : fallback;
  }
  return fallback;
}
