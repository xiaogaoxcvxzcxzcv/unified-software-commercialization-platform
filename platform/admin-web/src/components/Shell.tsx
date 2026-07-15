import {
  IconActivityHeartbeat,
  IconApps,
  IconBell,
  IconBuildingStore,
  IconCirclePlus,
  IconChevronDown,
  IconChevronLeft,
  IconChevronRight,
  IconFileAnalytics,
  IconHome,
  IconKey,
  IconLayoutDashboard,
  IconLogout,
  IconMenu2,
  IconReceipt,
  IconRefresh,
  IconSettings,
  IconShieldCheck,
  IconUsers,
} from "@tabler/icons-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { getAuthErrorMessage } from "../api/authClient";
import { useAppContext } from "../app/AppContext";
import { useAuth } from "../app/AuthContext";

const globalMenu = [
  { path: "/overview", label: "平台概览", icon: IconLayoutDashboard },
  { path: "/products", label: "软件管理", icon: IconApps },
  { path: "/create", label: "创建软件", icon: IconCirclePlus },
  { path: "/system/health", label: "系统状态", icon: IconActivityHeartbeat },
];

const productMenu = [
  { suffix: "overview", label: "软件概览", icon: IconHome, capability: null },
  { suffix: "settings", label: "产品设置", icon: IconSettings, capability: null },
  { suffix: "users", label: "用户管理", icon: IconUsers, capability: "统一账号" },
  { suffix: "entitlements", label: "权益管理", icon: IconKey, capability: "权益" },
  { suffix: "tenants", label: "代理租户", icon: IconBuildingStore, capability: "代理租户" },
  { suffix: "audit", label: "操作审计", icon: IconFileAnalytics, capability: null },
];

export function Shell({ children, title, subtitle }: { children: React.ReactNode; title: string; subtitle: string }) {
  const {
    products, currentProduct, tenants, currentTenant, tenantsLoading, tenantsError,
    selectProduct, selectTenant, refreshProducts, refreshTenants,
  } = useAppContext();
  const { session, logout } = useAuth();
  const [desktopCollapsed, setDesktopCollapsed] = useState(false);
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);
  const [profileOpen, setProfileOpen] = useState(false);
  const [notificationsOpen, setNotificationsOpen] = useState(false);
  const [contextRefreshing, setContextRefreshing] = useState(false);
  const [refreshMessage, setRefreshMessage] = useState("");
  const [logoutPending, setLogoutPending] = useState(false);
  const [logoutError, setLogoutError] = useState<string | null>(null);
  const profileRef = useRef<HTMLDivElement>(null);
  const notificationRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  const location = useLocation();
  const adminName = session?.admin.display_name || "管理员";
  const adminInitials = Array.from(adminName.trim()).slice(0, 2).join("").toUpperCase() || "管";
  const adminRole = session?.authorization.scopes.some((scope) => scope.scope_type === "platform")
    ? "平台管理员"
    : session?.authorization.scopes.some((scope) => scope.scope_type === "product")
      ? "产品管理员"
      : "代理管理员";
  const menu = useMemo(() => currentProduct
    ? productMenu.filter((item) => !item.capability || currentProduct.enabledCapabilities.includes(item.capability))
      .map((item) => ({ ...item, path: `/products/${currentProduct.id}/${item.suffix}` }))
    : globalMenu, [currentProduct]);

  useEffect(() => {
    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target as Node;
      if (profileOpen && !profileRef.current?.contains(target)) setProfileOpen(false);
      if (notificationsOpen && !notificationRef.current?.contains(target)) setNotificationsOpen(false);
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      if (profileOpen) profileRef.current?.querySelector<HTMLButtonElement>(".profile")?.focus();
      if (notificationsOpen) notificationRef.current?.querySelector<HTMLButtonElement>(".icon-button")?.focus();
      setProfileOpen(false);
      setNotificationsOpen(false);
      setMobileSidebarOpen(false);
    };
    document.addEventListener("pointerdown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [profileOpen, notificationsOpen]);

  useEffect(() => {
    setMobileSidebarOpen(false);
    setProfileOpen(false);
    setNotificationsOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    if (profileOpen) profileRef.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
  }, [profileOpen]);

  const handleNavigate = (path: string) => {
    setMobileSidebarOpen(false);
    navigate(path);
  };

  const handleProductChange = (value: string) => {
    setMobileSidebarOpen(false);
    if (value === "all") {
      selectProduct(null);
      navigate("/overview");
      return;
    }
    selectProduct(value);
    navigate(`/products/${value}/overview`);
  };

  const refreshContext = async () => {
    if (contextRefreshing) return;
    setContextRefreshing(true);
    setRefreshMessage("");
    await refreshProducts();
    await refreshTenants();
    setRefreshMessage("产品和租户列表刷新请求已完成");
    setContextRefreshing(false);
  };

  const handleLogout = async () => {
    if (logoutPending) return;
    setLogoutPending(true);
    setLogoutError(null);
    try {
      await logout();
      setProfileOpen(false);
    } catch (reason) {
      setLogoutError(getAuthErrorMessage(reason));
    } finally {
      setLogoutPending(false);
    }
  };

  return (
    <div className={`app-shell ${desktopCollapsed ? "is-collapsed" : ""} ${mobileSidebarOpen ? "mobile-sidebar-open" : ""}`}>
      <aside id="primary-sidebar" className="sidebar" aria-label="主菜单">
        <div className="brand">
          <span className="brand-mark"><IconShieldCheck size={25} /></span>
          <div className="brand-copy"><strong>商业化底座</strong><small>CONTROL CENTER</small></div>
        </div>
        <div className="product-context">
          <label htmlFor="product-select">当前软件</label>
          <button className="icon-button context-shortcut" type="button" title="切换软件" aria-label="展开侧栏以切换软件" onClick={() => setDesktopCollapsed(false)}><IconApps size={20} /></button>
          <div className="select-wrap product-select-wrap">
            <select id="product-select" value={currentProduct?.id ?? "all"} onChange={(event) => handleProductChange(event.target.value)}>
              <option value="all">全部软件</option>
              {products.map((product) => <option value={product.id} key={product.id}>{product.name}</option>)}
            </select>
            <IconChevronDown size={16} />
          </div>
        </div>
        <nav className="side-nav" aria-label="主要导航">
          {menu.map((item) => {
            const Icon = item.icon;
            const active = location.pathname === item.path;
            return <button className={active ? "nav-item active" : "nav-item"} key={item.path} type="button" onClick={() => handleNavigate(item.path)} title={desktopCollapsed ? item.label : undefined}><Icon size={20} /><span>{item.label}</span></button>;
          })}
        </nav>
        <div className="sidebar-bottom">
          <button className="nav-item" type="button" disabled={contextRefreshing} onClick={() => void refreshContext()} title={desktopCollapsed ? "刷新产品和租户" : undefined} aria-busy={contextRefreshing}><IconRefresh size={20} /><span>{contextRefreshing ? "刷新中..." : "刷新产品和租户"}</span></button>
          <span className="sr-only" role="status" aria-live="polite">{refreshMessage}</span>
          <button className="nav-item desktop-collapse" type="button" onClick={() => setDesktopCollapsed((value) => !value)} title={desktopCollapsed ? "展开侧栏" : "收起侧栏"}>{desktopCollapsed ? <IconChevronRight size={20} /> : <IconChevronLeft size={20} />}<span>{desktopCollapsed ? "展开侧栏" : "收起侧栏"}</span></button>
        </div>
      </aside>
      <button className="sidebar-backdrop" type="button" aria-label="关闭主菜单" onClick={() => setMobileSidebarOpen(false)} />
      <div className="main-column">
        <header className="topbar">
          <button className="mobile-menu" type="button" onClick={() => setMobileSidebarOpen((value) => !value)} title="菜单" aria-label={mobileSidebarOpen ? "关闭主菜单" : "打开主菜单"} aria-expanded={mobileSidebarOpen} aria-controls="primary-sidebar"><IconMenu2 size={22} /></button>
          <div className="page-heading"><h1>{title}</h1><p>{subtitle}</p></div>
          <div className="top-actions">
            {currentProduct && <div className="select-wrap compact tenant-select"><select aria-label="当前租户" value={currentTenant?.id ?? ""} disabled={tenantsLoading || Boolean(tenantsError)} onChange={(event) => selectTenant(event.target.value)}><option value="" disabled>{tenantsLoading ? "加载租户..." : tenantsError ?? "请选择租户"}</option>{tenants.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select><IconChevronDown size={15} /></div>}
            <span className="environment" title="当前所有业务数据均为内存演示数据"><span></span>演示环境</span>
            <div className="notification-wrap" ref={notificationRef}>
              <button className="icon-button" type="button" title="通知" aria-label="系统通知" aria-haspopup="dialog" aria-expanded={notificationsOpen} aria-controls="notification-popover" onClick={() => { setProfileOpen(false); setNotificationsOpen((value) => !value); }}><IconBell size={19} /><i /></button>
              {notificationsOpen && <div id="notification-popover" className="notification-menu" role="dialog" aria-labelledby="notification-title"><strong id="notification-title">系统通知</strong><p>当前没有需要处理的告警。</p></div>}
            </div>
            <div className="profile-wrap" ref={profileRef}>
              <button className="profile" type="button" aria-label={`${adminName}，${adminRole}`} aria-haspopup="menu" aria-expanded={profileOpen} aria-controls="profile-menu" onClick={() => { setNotificationsOpen(false); setLogoutError(null); setProfileOpen((value) => !value); }}><span>{adminInitials}</span><div><strong>{adminName}</strong><small>{adminRole}</small></div><IconChevronDown size={16} /></button>
              {profileOpen && <div id="profile-menu" className="profile-menu" role="menu"><button role="menuitem" type="button" onClick={() => { setProfileOpen(false); handleNavigate(currentProduct ? `/products/${currentProduct.id}/audit` : "/system/health"); }}><IconReceipt size={17} />操作记录</button><button role="menuitem" type="button" disabled={logoutPending} onClick={() => void handleLogout()}><IconLogout size={17} />{logoutPending ? "正在退出..." : "退出登录"}</button>{logoutError && <p className="profile-menu-error" role="alert">{logoutError}</p>}</div>}
            </div>
          </div>
        </header>
        <main className="workspace">{children}</main>
      </div>
    </div>
  );
}
