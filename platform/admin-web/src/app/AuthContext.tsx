import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { authClient, getAuthErrorMessage, isAuthenticationFailure } from "../api/authClient";
import type { AdminSession } from "../types";

type AuthStatus = "loading" | "authenticated" | "anonymous" | "error";

interface AuthContextValue {
  status: AuthStatus;
  session: AdminSession | null;
  error: string | null;
  login: (identifier: string, credential: string) => Promise<AdminSession>;
  logout: () => Promise<void>;
  retrySession: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function getDefaultAdminPath(session: AdminSession) {
  if (session.authorization.scopes.some((scope) => scope.scope_type === "platform")) return "/overview";
  const productId = session.authorization.scopes
    .map((scope) => scope.product_id ?? (scope.scope_type === "product" ? scope.scope_id : null))
    .find((value): value is string => Boolean(value));
  return productId ? `/products/${encodeURIComponent(productId)}/overview` : "/overview";
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AuthStatus>("loading");
  const [session, setSession] = useState<AdminSession | null>(null);
  const [error, setError] = useState<string | null>(null);
  const operationRef = useRef(0);
  const initialLoadRef = useRef(false);

  const retrySession = useCallback(async () => {
    const operationId = ++operationRef.current;
    setStatus("loading");
    setError(null);
    try {
      let nextSession: AdminSession;
      try {
        nextSession = await authClient.getSession();
      } catch (reason) {
        if (!isAuthenticationFailure(reason)) throw reason;
        try {
          await authClient.refresh();
        } catch (refreshReason) {
          if (isAuthenticationFailure(refreshReason)) {
            if (operationRef.current !== operationId) return;
            setSession(null);
            setStatus("anonymous");
            return;
          }
          throw refreshReason;
        }
        nextSession = await authClient.getSession();
      }
      if (operationRef.current !== operationId) return;
      setSession(nextSession);
      setStatus("authenticated");
    } catch (reason) {
      if (operationRef.current !== operationId) return;
      if (isAuthenticationFailure(reason)) {
        setSession(null);
        setStatus("anonymous");
        return;
      }
      setSession(null);
      setError(getAuthErrorMessage(reason));
      setStatus("error");
    }
  }, []);

  useEffect(() => {
    if (initialLoadRef.current) return;
    initialLoadRef.current = true;
    void retrySession();
  }, [retrySession]);

  const login = useCallback(async (identifier: string, credential: string) => {
    const operationId = ++operationRef.current;
    setError(null);
    try {
      const nextSession = await authClient.login(identifier, credential);
      if (operationRef.current === operationId) {
        setSession(nextSession);
        setStatus("authenticated");
      }
      return nextSession;
    } catch (reason) {
      if (operationRef.current === operationId) {
        setSession(null);
        setStatus("anonymous");
      }
      throw reason;
    }
  }, []);

  const logout = useCallback(async () => {
    if (!session) return;
    const operationId = ++operationRef.current;
    await authClient.logout(session.csrf_token);
    if (operationRef.current !== operationId) return;
    setSession(null);
    setError(null);
    setStatus("anonymous");
  }, [session]);

  const value = useMemo(() => ({ status, session, error, login, logout, retrySession }), [status, session, error, login, logout, retrySession]);
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const value = useContext(AuthContext);
  if (!value) throw new Error("useAuth must be used inside AuthProvider");
  return value;
}
