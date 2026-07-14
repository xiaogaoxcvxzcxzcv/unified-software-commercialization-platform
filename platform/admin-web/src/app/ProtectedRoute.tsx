import { Navigate, useLocation } from "react-router-dom";
import { useAuth } from "./AuthContext";

export function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { status, error, retrySession } = useAuth();
  const location = useLocation();

  if (status === "loading") return <div className="route-loading" role="status">正在验证管理会话...</div>;
  if (status === "error") {
    return <div className="route-loading auth-service-error" role="alert"><strong>暂时无法连接认证服务</strong><span>{error}</span><button className="secondary-button" type="button" onClick={() => void retrySession()}>重试</button></div>;
  }
  if (status !== "authenticated") {
    const from = `${location.pathname}${location.search}${location.hash}`;
    return <Navigate replace to="/login" state={{ from }} />;
  }
  return children;
}
