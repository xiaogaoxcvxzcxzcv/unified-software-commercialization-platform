import { lazy, Suspense, useEffect } from "react";
import { Navigate, Route, Routes, useParams } from "react-router-dom";
import { AppProvider } from "./AppContext";
import { useAppContext } from "./AppContext";
import { AuthProvider, getDefaultAdminPath, useAuth } from "./AuthContext";
import { ProtectedRoute } from "./ProtectedRoute";
import { LoginPage } from "../pages/LoginPage";

const AuditPage = lazy(() => import("../pages/AuditPage").then((module) => ({ default: module.AuditPage })));
const AssemblyRunsPage = lazy(() => import("../pages/AssemblyRunsPage").then((module) => ({ default: module.AssemblyRunsPage })));
const AssemblyRunPage = lazy(() => import("../pages/AssemblyRunPage").then((module) => ({ default: module.AssemblyRunPage })));
const AssemblyLifecycleEntryPage = lazy(() => import("../pages/AssemblyLifecycleEntryPage").then((module) => ({ default: module.AssemblyLifecycleEntryPage })));
const AssemblyLifecyclePlanPage = lazy(() => import("../pages/AssemblyLifecyclePlanPage").then((module) => ({ default: module.AssemblyLifecyclePlanPage })));
const AssemblyLifecycleOperationPage = lazy(() => import("../pages/AssemblyLifecycleOperationPage").then((module) => ({ default: module.AssemblyLifecycleOperationPage })));
const CreateSoftwarePage = lazy(() => import("../pages/CreateSoftwarePage").then((module) => ({ default: module.CreateSoftwarePage })));
const CreateBlueprintRecoveryPage = lazy(() => import("../pages/CreateRecoveryPage").then((module) => ({ default: module.CreateBlueprintRecoveryPage })));
const CreatePlanRecoveryPage = lazy(() => import("../pages/CreateRecoveryPage").then((module) => ({ default: module.CreatePlanRecoveryPage })));
const CapabilitiesPage = lazy(() => import("../pages/CapabilitiesPage").then((module) => ({ default: module.CapabilitiesPage })));
const CapabilityPendingPage = lazy(() => import("../pages/CapabilityPendingPage").then((module) => ({ default: module.CapabilityPendingPage })));
const HealthPage = lazy(() => import("../pages/HealthPage").then((module) => ({ default: module.HealthPage })));
const IntegrationPage = lazy(() => import("../pages/IntegrationPage").then((module) => ({ default: module.IntegrationPage })));
const OverviewPage = lazy(() => import("../pages/OverviewPage").then((module) => ({ default: module.OverviewPage })));
const ProductsPage = lazy(() => import("../pages/ProductsPage").then((module) => ({ default: module.ProductsPage })));
const SettingsPage = lazy(() => import("../pages/SettingsPage").then((module) => ({ default: module.SettingsPage })));
const UsersPage = lazy(() => import("../pages/UsersPage").then((module) => ({ default: module.UsersPage })));
const UserDetailPage = lazy(() => import("../pages/UserDetailPage").then((module) => ({ default: module.UserDetailPage })));

function ProductRoute({ children }: { children: React.ReactNode }) {
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

function AuthenticatedRoutes() {
  const { session } = useAuth();
  return <AppProvider><Routes>
    <Route path="/overview" element={<GlobalRoute><OverviewPage /></GlobalRoute>} />
    <Route path="/create" element={<GlobalRoute><CreateSoftwarePage /></GlobalRoute>} />
    <Route path="/create/experimental" element={<GlobalRoute><CreateSoftwarePage catalogScope="experimental" /></GlobalRoute>} />
    <Route path="/create/blueprints/:blueprintId" element={<GlobalRoute><CreateBlueprintRecoveryPage /></GlobalRoute>} />
    <Route path="/create/plans/:planId" element={<GlobalRoute><CreatePlanRecoveryPage /></GlobalRoute>} />
    <Route path="/assemblies" element={<GlobalRoute><AssemblyRunsPage /></GlobalRoute>} />
    <Route path="/assemblies/:runId" element={<GlobalRoute><AssemblyRunPage /></GlobalRoute>} />
    <Route path="/assemblies/:runId/lifecycle" element={<GlobalRoute><AssemblyLifecycleEntryPage /></GlobalRoute>} />
    <Route path="/assembly-lifecycle/plans/:planId" element={<GlobalRoute><AssemblyLifecyclePlanPage /></GlobalRoute>} />
    <Route path="/assembly-lifecycle/operations/:operationId" element={<GlobalRoute><AssemblyLifecycleOperationPage /></GlobalRoute>} />
    <Route path="/products" element={<GlobalRoute><ProductsPage /></GlobalRoute>} />
    <Route path="/system/health" element={<GlobalRoute><HealthPage /></GlobalRoute>} />
    <Route path="/products/:productId/overview" element={<ProductRoute><OverviewPage /></ProductRoute>} />
    <Route path="/products/:productId/settings" element={<ProductRoute><SettingsPage /></ProductRoute>} />
    <Route path="/products/:productId/integration" element={<ProductRoute><IntegrationPage /></ProductRoute>} />
    <Route path="/products/:productId/capabilities" element={<ProductRoute><CapabilitiesPage /></ProductRoute>} />
    <Route path="/products/:productId/audit" element={<ProductRoute><AuditPage /></ProductRoute>} />
    <Route path="/products/:productId/users" element={<ProductRoute><UsersPage /></ProductRoute>} />
    <Route path="/products/:productId/users/:userId" element={<ProductRoute><UserDetailPage /></ProductRoute>} />
    <Route path="/products/:productId/:feature" element={<ProductRoute><CapabilityPendingPage /></ProductRoute>} />
    <Route path="*" element={<Navigate replace to={session ? getDefaultAdminPath(session) : "/login"} />} />
  </Routes></AppProvider>;
}

export function App() {
  return <AuthProvider><Suspense fallback={<div className="route-loading">正在加载工作区...</div>}><Routes>
    <Route path="/login" element={<LoginPage />} />
    <Route path="*" element={<ProtectedRoute><AuthenticatedRoutes /></ProtectedRoute>} />
  </Routes></Suspense></AuthProvider>;
}
