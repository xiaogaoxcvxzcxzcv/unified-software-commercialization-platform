import { IconArrowRight, IconEye, IconEyeOff, IconLock, IconShieldCheck, IconUser } from "@tabler/icons-react";
import { FormEvent, useEffect, useState } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { getAuthErrorMessage } from "../api/authClient";
import { getDefaultAdminPath, useAuth } from "../app/AuthContext";

function getSafeReturnPath(value: unknown) {
  if (typeof value !== "string" || !value.startsWith("/") || value.startsWith("//") || value.startsWith("/login")) return null;
  try {
    const target = new URL(value, window.location.origin);
    if (target.origin !== window.location.origin || target.pathname === "/login") return null;
    return `${target.pathname}${target.search}${target.hash}`;
  } catch {
    return null;
  }
}

export function LoginPage() {
  const { status, session, error: sessionError, login, retrySession } = useAuth();
  const [identifier, setIdentifier] = useState("");
  const [credential, setCredential] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const location = useLocation();
  const navigate = useNavigate();
  const returnPath = getSafeReturnPath((location.state as { from?: unknown } | null)?.from);

  useEffect(() => {
    if (status === "authenticated" && session) navigate(returnPath ?? getDefaultAdminPath(session), { replace: true });
  }, [status, session, navigate, returnPath]);

  if (status === "loading") return <div className="route-loading" role="status">正在检查管理会话...</div>;
  if (status === "authenticated" && session) return <Navigate replace to={returnPath ?? getDefaultAdminPath(session)} />;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (submitting) return;
    setSubmitting(true);
    setSubmitError(null);
    try {
      const nextSession = await login(identifier.trim(), credential);
      setCredential("");
      navigate(returnPath ?? getDefaultAdminPath(nextSession), { replace: true });
    } catch (reason) {
      setCredential("");
      setSubmitError(getAuthErrorMessage(reason));
    } finally {
      setSubmitting(false);
    }
  };

  return <main className="auth-page">
    <section className="auth-panel" aria-labelledby="login-title">
      <div className="auth-brand"><span className="brand-mark"><IconShieldCheck size={25} /></span><div><strong>商业化底座</strong><small>CONTROL CENTER</small></div></div>
      <div className="auth-heading"><span className="eyebrow">管理控制台</span><h1 id="login-title">管理员登录</h1><p>使用已获得管理权限的账号继续</p></div>
      {status === "error" && <div className="auth-alert" role="alert"><strong>认证服务暂时不可用</strong><span>{sessionError}</span><button className="secondary-button" type="button" onClick={() => void retrySession()}>重新连接</button></div>}
      <form className="auth-form" onSubmit={submit}>
        <label htmlFor="admin-identifier">管理账号</label>
        <div className="auth-field"><IconUser size={18} /><input id="admin-identifier" name="username" autoComplete="username" required maxLength={320} value={identifier} onChange={(event) => setIdentifier(event.target.value)} placeholder="邮箱、手机号或管理员账号" /></div>
        <label htmlFor="admin-credential">密码</label>
        <div className="auth-field"><IconLock size={18} /><input id="admin-credential" name="password" type={showPassword ? "text" : "password"} autoComplete="current-password" required value={credential} onChange={(event) => setCredential(event.target.value)} placeholder="请输入密码" /><button type="button" title={showPassword ? "隐藏密码" : "显示密码"} aria-label={showPassword ? "隐藏密码" : "显示密码"} onClick={() => setShowPassword((value) => !value)}>{showPassword ? <IconEyeOff size={18} /> : <IconEye size={18} />}</button></div>
        {submitError && <p className="auth-form-error" role="alert">{submitError}</p>}
        <button className="primary-button auth-submit" type="submit" disabled={submitting || !identifier.trim() || !credential}>{submitting ? "正在登录..." : <><span>登录控制台</span><IconArrowRight size={18} /></>}</button>
      </form>
      <p className="auth-footer">管理会话由服务器验证和撤销</p>
    </section>
  </main>;
}
