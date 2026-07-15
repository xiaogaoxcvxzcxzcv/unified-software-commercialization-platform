import IconApps from "@tabler/icons-react/dist/esm/icons/IconApps.mjs";
import IconLayoutSidebarLeftCollapse from "@tabler/icons-react/dist/esm/icons/IconLayoutSidebarLeftCollapse.mjs";
import IconLayoutSidebarRightCollapse from "@tabler/icons-react/dist/esm/icons/IconLayoutSidebarRightCollapse.mjs";
import IconMoon from "@tabler/icons-react/dist/esm/icons/IconMoon.mjs";
import IconSun from "@tabler/icons-react/dist/esm/icons/IconSun.mjs";
import { useEffect, useMemo, useState } from "react";
import type { CustomRoute } from "../integration/routes";

export function AppShell({ productName, routes }: { readonly productName: string; readonly routes: readonly CustomRoute[] }) {
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [dark, setDark] = useState(false);
  const [activeId, setActiveId] = useState(routes[0]?.id ?? "");
  const active = useMemo(() => routes.find((route) => route.id === activeId) ?? routes[0], [activeId, routes]);
  useEffect(() => { document.documentElement.dataset.theme = dark ? "dark" : "light"; }, [dark]);
  useEffect(() => { if (!active && routes[0]) setActiveId(routes[0].id); }, [active, routes]);
  useEffect(() => {
    const closeNavigation = (event: KeyboardEvent) => { if (event.key === "Escape") setSidebarOpen(false); };
    document.addEventListener("keydown", closeNavigation);
    return () => document.removeEventListener("keydown", closeNavigation);
  }, []);
  const ActiveComponent = active?.Component;

  return <div className={`standard-shell ${sidebarOpen ? "sidebar-open" : ""}`}>
    <aside id="standard-navigation" className="standard-sidebar" aria-label="主导航">
      <div className="standard-brand"><span aria-hidden="true"><IconApps /></span><strong title={productName}>{productName}</strong></div>
      <nav>{routes.map((route) => <button type="button" aria-current={route.id === active?.id ? "page" : undefined} className={route.id === active?.id ? "active" : ""} key={route.id} onClick={() => { setActiveId(route.id); setSidebarOpen(false); }}>{route.icon}{route.label}</button>)}</nav>
    </aside>
    <button className="standard-backdrop" type="button" aria-label="关闭主导航" onClick={() => setSidebarOpen(false)} />
    <div className="standard-main">
      <header className="standard-topbar">
        <button className="standard-icon-button standard-menu" type="button" title={sidebarOpen ? "关闭主导航" : "打开主导航"} aria-label={sidebarOpen ? "关闭主导航" : "打开主导航"} aria-expanded={sidebarOpen} aria-controls="standard-navigation" onClick={() => setSidebarOpen((value) => !value)}>{sidebarOpen ? <IconLayoutSidebarLeftCollapse /> : <IconLayoutSidebarRightCollapse />}</button>
        <div><h1>{active?.label ?? productName}</h1><p>{productName}</p></div>
        <button className="standard-icon-button" type="button" title={dark ? "使用浅色主题" : "使用深色主题"} aria-label={dark ? "使用浅色主题" : "使用深色主题"} onClick={() => setDark((value) => !value)}>{dark ? <IconSun /> : <IconMoon />}</button>
      </header>
      <main className="standard-workspace">{ActiveComponent ? <ActiveComponent /> : <section className="standard-empty" role="status"><IconApps aria-hidden="true" /><h2>{productName}</h2><p>当前没有可用工作区</p></section>}</main>
    </div>
  </div>;
}
