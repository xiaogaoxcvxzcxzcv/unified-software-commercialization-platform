import { lazy, Suspense, useEffect } from "react";
import { Navigate, Route, Routes, useParams } from "react-router-dom";
import { AppProvider } from "./AppContext";
import { useAppContext } from "./AppContext";

const AuditPage = lazy(() => import("../pages/AuditPage").then((module) => ({ default: module.AuditPage })));
const EntitlementsPage = lazy(() => import("../pages/EntitlementsPage").then((module) => ({ default: module.EntitlementsPage })));
const HealthPage = lazy(() => import("../pages/HealthPage").then((module) => ({ default: module.HealthPage })));
const OverviewPage = lazy(() => import("../pages/OverviewPage").then((module) => ({ default: module.OverviewPage })));
const ProductsPage = lazy(() => import("../pages/ProductsPage").then((module) => ({ default: module.ProductsPage })));
const SettingsPage = lazy(() => import("../pages/SettingsPage").then((module) => ({ default: module.SettingsPage })));
const TenantsPage = lazy(() => import("../pages/TenantsPage").then((module) => ({ default: module.TenantsPage })));
const UsersPage = lazy(() => import("../pages/UsersPage").then((module) => ({ default: module.UsersPage })));

function ProductRoute({ children, capability }: { children: React.ReactNode; capability?: string }) {
  const { productId } = useParams();
  const { products, currentProduct, loading, error, selectProduct, refreshProducts } = useAppContext();
  const productExists = products.some((product) => product.id === productId);
  useEffect(() => {
    if (!loading && productId && productExists && currentProduct?.id !== productId) selectProduct(productId);
  }, [loading, productId, productExists, currentProduct?.id, selectProduct]);
  if (loading && products.length === 0) return <div className="route-loading">正在验证软件上下文...</div>;
  if (error && products.length === 0) return <div className="route-loading"><span>{error}</span><button className="secondary-button" type="button" onClick={() => void refreshProducts()}>重试</button></div>;
  if (!productId || !productExists) return <Navigate replace to="/products" />;
  if (currentProduct?.id !== productId) return <div className="route-loading">正在切换软件上下文...</div>;
  if (capability && !currentProduct.enabledCapabilities.includes(capability)) return <Navigate replace to={`/products/${productId}/overview`} />;
  return children;
}

function GlobalRoute({ children }: { children: React.ReactNode }) {
  const { products, currentProduct, loading, error, selectProduct, refreshProducts } = useAppContext();
  useEffect(() => { if (currentProduct) selectProduct(null); }, [currentProduct, selectProduct]);
  if (loading && products.length === 0) return <div className="route-loading">正在加载平台上下文...</div>;
  if (error && products.length === 0) return <div className="route-loading"><span>{error}</span><button className="secondary-button" type="button" onClick={() => void refreshProducts()}>重试</button></div>;
  if (currentProduct) return <div className="route-loading">正在切换平台上下文...</div>;
  return children;
}

export function App() {
  return <AppProvider><Suspense fallback={<div className="route-loading">正在加载工作区...</div>}><Routes>
    <Route path="/overview" element={<GlobalRoute><OverviewPage /></GlobalRoute>} />
    <Route path="/products" element={<GlobalRoute><ProductsPage /></GlobalRoute>} />
    <Route path="/system/health" element={<GlobalRoute><HealthPage /></GlobalRoute>} />
    <Route path="/products/:productId/overview" element={<ProductRoute><OverviewPage /></ProductRoute>} />
    <Route path="/products/:productId/settings" element={<ProductRoute><SettingsPage /></ProductRoute>} />
    <Route path="/products/:productId/users" element={<ProductRoute capability="统一账号"><UsersPage /></ProductRoute>} />
    <Route path="/products/:productId/entitlements" element={<ProductRoute capability="权益"><EntitlementsPage /></ProductRoute>} />
    <Route path="/products/:productId/tenants" element={<ProductRoute capability="代理租户"><TenantsPage /></ProductRoute>} />
    <Route path="/products/:productId/audit" element={<ProductRoute><AuditPage /></ProductRoute>} />
    <Route path="*" element={<Navigate replace to="/products/prod-video/overview" />} />
  </Routes></Suspense></AppProvider>;
}
