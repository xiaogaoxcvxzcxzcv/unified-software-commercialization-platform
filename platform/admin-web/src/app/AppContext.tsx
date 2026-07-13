import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { adminClient } from "../api/adminClient";
import type { Product, TenantRecord } from "../types";

interface AppContextValue {
  products: Product[];
  currentProduct: Product | null;
  tenants: TenantRecord[];
  currentTenant: TenantRecord | null;
  loading: boolean;
  error: string | null;
  tenantsLoading: boolean;
  tenantsError: string | null;
  selectProduct: (productId: string | null) => void;
  selectTenant: (tenantId: string) => void;
  refreshProducts: () => Promise<void>;
  refreshTenants: () => Promise<void>;
}

const AppContext = createContext<AppContextValue | null>(null);

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [products, setProducts] = useState<Product[]>([]);
  const [currentProductId, setCurrentProductId] = useState<string | null>(null);
  const [tenants, setTenants] = useState<TenantRecord[]>([]);
  const [currentTenantId, setCurrentTenantId] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [tenantsLoading, setTenantsLoading] = useState(false);
  const [tenantsError, setTenantsError] = useState<string | null>(null);
  const tenantRequestRef = useRef(0);

  const refreshProducts = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setProducts(await adminClient.listProducts());
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "软件列表加载失败");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void refreshProducts(); }, [refreshProducts]);

  const currentProduct = products.find((product) => product.id === currentProductId) ?? null;
  const currentTenant = tenants.find((tenant) => tenant.id === currentTenantId) ?? null;

  const refreshTenants = useCallback(async () => {
    const productId = currentProductId;
    const requestId = ++tenantRequestRef.current;
    if (!productId) {
      setTenants([]);
      setCurrentTenantId(null);
      setTenantsLoading(false);
      setTenantsError(null);
      return;
    }
    setTenantsLoading(true);
    setTenantsError(null);
    try {
      const next = await adminClient.listTenants(productId);
      if (tenantRequestRef.current !== requestId) return;
      setTenants(next);
      setCurrentTenantId((selected) => next.some((item) => item.id === selected)
        ? selected
        : next.find((item) => item.type === "official")?.id ?? next[0]?.id ?? null);
    } catch (reason) {
      if (tenantRequestRef.current !== requestId) return;
      setTenantsError(reason instanceof Error ? reason.message : "租户列表加载失败");
      setTenants([]);
      setCurrentTenantId(null);
    } finally {
      if (tenantRequestRef.current === requestId) setTenantsLoading(false);
    }
  }, [currentProductId]);

  useEffect(() => {
    void refreshTenants();
    return () => { tenantRequestRef.current += 1; };
  }, [refreshTenants]);

  const selectProduct = useCallback((productId: string | null) => {
    setCurrentProductId(productId);
    setCurrentTenantId(null);
  }, []);
  const selectTenant = useCallback((tenantId: string) => setCurrentTenantId(tenantId), []);

  const value = useMemo(() => ({
    products, currentProduct, tenants, currentTenant, loading, error, tenantsLoading, tenantsError,
    selectProduct, selectTenant, refreshProducts, refreshTenants,
  }), [products, currentProduct, tenants, currentTenant, loading, error, tenantsLoading, tenantsError, selectProduct, selectTenant, refreshProducts, refreshTenants]);

  return <AppContext.Provider value={value}>{children}</AppContext.Provider>;
}

export function useAppContext() {
  const value = useContext(AppContext);
  if (!value) throw new Error("useAppContext must be used inside AppProvider");
  return value;
}
