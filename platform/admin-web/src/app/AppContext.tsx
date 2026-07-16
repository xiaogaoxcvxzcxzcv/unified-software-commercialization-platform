import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { adminClient } from "../api/adminClient";
import { AuthApiError } from "../api/authClient";
import type { Product, ProductApplication, ProductCapabilityProjection, TenantRecord } from "../types";
import { useAuth } from "./AuthContext";

interface AppContextValue {
  products: Product[];
  currentProduct: Product | null;
  applications: ProductApplication[];
  capabilityProjection: ProductCapabilityProjection | null;
  enabledPackageIds: Set<string>;
  tenants: TenantRecord[];
  currentTenant: TenantRecord | null;
  loading: boolean;
  error: string | null;
  workspaceLoading: boolean;
  workspaceError: string | null;
  tenantsLoading: boolean;
  tenantsError: string | null;
  selectProduct: (productId: string | null) => void;
  selectTenant: (tenantId: string) => void;
  refreshProducts: () => Promise<Product[]>;
  refreshWorkspace: () => Promise<void>;
  refreshTenants: () => Promise<void>;
  openTrustedProduct: (productId: string) => Promise<Product>;
}

const AppContext = createContext<AppContextValue | null>(null);
const errorMessage = (reason: unknown, fallback: string) => reason instanceof Error ? reason.message : fallback;

export function AppProvider({ children }: { children: React.ReactNode }) {
  const { session } = useAuth();
  const [products, setProducts] = useState<Product[]>([]);
  const [currentProductId, setCurrentProductId] = useState<string | null>(null);
  const [applications, setApplications] = useState<ProductApplication[]>([]);
  const [capabilityProjection, setCapabilityProjection] = useState<ProductCapabilityProjection | null>(null);
  const [tenants, setTenants] = useState<TenantRecord[]>([]);
  const [currentTenantId, setCurrentTenantId] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [workspaceLoading, setWorkspaceLoading] = useState(false);
  const [workspaceError, setWorkspaceError] = useState<string | null>(null);
  const [tenantsLoading, setTenantsLoading] = useState(false);
  const [tenantsError, setTenantsError] = useState<string | null>(null);
  const workspaceRequestRef = useRef(0);

  const loadProducts = useCallback(() => session ? adminClient.listAccessibleProducts(session) : Promise.resolve([]), [session]);

  const refreshProducts = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const next = await loadProducts();
      setProducts(next);
      return next;
    } catch (reason) {
      setError(errorMessage(reason, "软件列表加载失败"));
      return [];
    } finally {
      setLoading(false);
    }
  }, [loadProducts]);

  useEffect(() => { void refreshProducts(); }, [refreshProducts]);

  const currentProduct = products.find((product) => product.id === currentProductId) ?? null;
  const currentTenant = tenants.find((tenant) => tenant.id === currentTenantId) ?? null;

  const refreshWorkspace = useCallback(async () => {
    const productId = currentProductId;
    const requestId = ++workspaceRequestRef.current;
    if (!productId) {
      setApplications([]);
      setCapabilityProjection(null);
      setTenants([]);
      setCurrentTenantId(null);
      setWorkspaceLoading(false);
      setWorkspaceError(null);
      setTenantsLoading(false);
      setTenantsError(null);
      return;
    }
    setWorkspaceLoading(true);
    setWorkspaceError(null);
    setTenantsLoading(true);
    setTenantsError(null);
    const [workspaceResult, tenantResult] = await Promise.allSettled([
      Promise.all([adminClient.listApplications(productId), adminClient.getProductCapabilities(productId)]),
      adminClient.listTenants(productId),
    ]);
    if (workspaceRequestRef.current !== requestId) return;
    if (workspaceResult.status === "fulfilled") {
      setApplications(workspaceResult.value[0]);
      setCapabilityProjection(workspaceResult.value[1]);
    } else {
      setApplications([]);
      setCapabilityProjection(null);
      setWorkspaceError(errorMessage(workspaceResult.reason, "软件工作区加载失败"));
    }
    if (tenantResult.status === "fulfilled") {
      setTenants(tenantResult.value);
      setCurrentTenantId((selected) => tenantResult.value.some((item) => item.id === selected)
        ? selected
        : tenantResult.value.find((item) => item.type === "official")?.id ?? tenantResult.value[0]?.id ?? null);
    } else {
      setTenants([]);
      setCurrentTenantId(null);
      setTenantsError(errorMessage(tenantResult.reason, "租户列表加载失败"));
    }
    setWorkspaceLoading(false);
    setTenantsLoading(false);
  }, [currentProductId]);

  useEffect(() => {
    void refreshWorkspace();
    return () => { workspaceRequestRef.current += 1; };
  }, [refreshWorkspace]);

  const refreshTenants = useCallback(async () => {
    const productId = currentProductId;
    if (!productId) return;
    setTenantsLoading(true);
    setTenantsError(null);
    try {
      const next = await adminClient.listTenants(productId);
      setTenants(next);
      setCurrentTenantId((selected) => next.some((item) => item.id === selected)
        ? selected
        : next.find((item) => item.type === "official")?.id ?? next[0]?.id ?? null);
    } catch (reason) {
      setTenantsError(errorMessage(reason, "租户列表加载失败"));
    } finally {
      setTenantsLoading(false);
    }
  }, [currentProductId]);

  const selectProduct = useCallback((productId: string | null) => {
    setCurrentProductId(productId);
    setCurrentTenantId(null);
  }, []);
  const selectTenant = useCallback((tenantId: string) => setCurrentTenantId(tenantId), []);
  const openTrustedProduct = useCallback(async (productId: string) => {
    const next = await loadProducts();
    setProducts(next);
    const product = next.find((item) => item.id === productId);
    if (!product) throw new AuthApiError("装配已完成，但当前管理员无权读取新软件", {
      status: 403, code: "product.workspace_unreadable", retryable: false,
    });
    selectProduct(product.id);
    return product;
  }, [loadProducts, selectProduct]);

  const enabledPackageIds = useMemo(() => new Set(
    (capabilityProjection?.capabilitySet?.capabilities ?? [])
      .filter((item) => item.enabled)
      .flatMap((item) => item.sourcePackageId ? [item.sourcePackageId] : []),
  ), [capabilityProjection]);
  const value = useMemo(() => ({
    products, currentProduct, applications, capabilityProjection, enabledPackageIds,
    tenants, currentTenant, loading, error, workspaceLoading, workspaceError, tenantsLoading, tenantsError,
    selectProduct, selectTenant, refreshProducts, refreshWorkspace, refreshTenants, openTrustedProduct,
  }), [products, currentProduct, applications, capabilityProjection, enabledPackageIds, tenants, currentTenant, loading, error, workspaceLoading, workspaceError, tenantsLoading, tenantsError, selectProduct, selectTenant, refreshProducts, refreshWorkspace, refreshTenants, openTrustedProduct]);

  return <AppContext.Provider value={value}>{children}</AppContext.Provider>;
}

export function useAppContext() {
  const value = useContext(AppContext);
  if (!value) throw new Error("useAppContext must be used inside AppProvider");
  return value;
}
