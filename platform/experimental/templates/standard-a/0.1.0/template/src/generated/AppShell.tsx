import IconApps from "@tabler/icons-react/dist/esm/icons/IconApps.mjs";
import IconLayoutSidebarLeftCollapse from "@tabler/icons-react/dist/esm/icons/IconLayoutSidebarLeftCollapse.mjs";
import IconLayoutSidebarRightCollapse from "@tabler/icons-react/dist/esm/icons/IconLayoutSidebarRightCollapse.mjs";
import IconMoon from "@tabler/icons-react/dist/esm/icons/IconMoon.mjs";
import IconSun from "@tabler/icons-react/dist/esm/icons/IconSun.mjs";
import IconX from "@tabler/icons-react/dist/esm/icons/IconX.mjs";
import { useEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import type { CustomRoute } from "../integration/routes";

export function AppShell({ productName, routes }: { readonly productName: string; readonly routes: readonly CustomRoute[] }) {
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [dark, setDark] = useState(false);
  const [activeId, setActiveId] = useState(routes[0]?.id ?? "");
  const navigationRef = useRef<HTMLElement>(null);
  const menuButtonRef = useRef<HTMLButtonElement>(null);
  const wasSidebarOpen = useRef(false);
  const active = useMemo(() => routes.find((route) => route.id === activeId) ?? routes[0], [activeId, routes]);
  useEffect(() => { document.documentElement.dataset.theme = dark ? "dark" : "light"; }, [dark]);
  useEffect(() => { if (!active && routes[0]) setActiveId(routes[0].id); }, [active, routes]);
  useEffect(() => {
    const closeNavigation = (event: KeyboardEvent) => { if (event.key === "Escape") setSidebarOpen(false); };
    document.addEventListener("keydown", closeNavigation);
    return () => document.removeEventListener("keydown", closeNavigation);
  }, []);
  useEffect(() => {
    if (sidebarOpen) {
      const currentRoute = navigationRef.current?.querySelector<HTMLElement>('[aria-current="page"]');
      (currentRoute ?? navigationRef.current?.querySelector<HTMLElement>("button"))?.focus();
    }
    else if (wasSidebarOpen.current) menuButtonRef.current?.focus();
    wasSidebarOpen.current = sidebarOpen;
  }, [sidebarOpen]);
  useEffect(() => {
    if (!sidebarOpen) return;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => { document.body.style.overflow = previousOverflow; };
  }, [sidebarOpen]);
  const trapNavigationFocus = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (!sidebarOpen || event.key !== "Tab") return;
    const controls = Array.from(navigationRef.current?.querySelectorAll<HTMLElement>('button:not([disabled])') ?? []);
    const first = controls[0];
    const last = controls.at(-1);
    if (!first || !last) return;
    if (event.shiftKey && (document.activeElement === first || !navigationRef.current?.contains(document.activeElement))) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };
  const ActiveComponent = active?.Component;

  return <div className={`standard-shell ${sidebarOpen ? "sidebar-open" : ""}`}>
    <a className="standard-skip-link" href="#standard-workspace">跳到主要内容</a>
    <aside ref={navigationRef} id="standard-navigation" className="standard-sidebar" aria-label="主导航" onKeyDown={trapNavigationFocus}>
      <div className="standard-brand"><span aria-hidden="true"><IconApps /></span><strong title={productName}>{productName}</strong><button className="standard-icon-button standard-sidebar-close" type="button" title="关闭主导航" aria-label="关闭主导航" onClick={() => setSidebarOpen(false)}><IconX aria-hidden="true" /></button></div>
      <nav>{routes.map((route) => <button type="button" aria-current={route.id === active?.id ? "page" : undefined} className={route.id === active?.id ? "active" : ""} key={route.id} onClick={() => { setActiveId(route.id); setSidebarOpen(false); }}><span className="standard-nav-icon" aria-hidden="true">{route.icon}</span><span className="standard-nav-label">{route.label}</span></button>)}</nav>
    </aside>
    <button className="standard-backdrop" type="button" aria-label="关闭导航遮罩" onClick={() => setSidebarOpen(false)} />
    <div className="standard-main" inert={sidebarOpen ? true : undefined} aria-hidden={sidebarOpen ? true : undefined}>
      <header className="standard-topbar">
        <button ref={menuButtonRef} className="standard-icon-button standard-menu" type="button" title={sidebarOpen ? "关闭主导航" : "打开主导航"} aria-label={sidebarOpen ? "关闭主导航" : "打开主导航"} aria-expanded={sidebarOpen} aria-controls="standard-navigation" onClick={() => setSidebarOpen((value) => !value)}>{sidebarOpen ? <IconLayoutSidebarLeftCollapse aria-hidden="true" /> : <IconLayoutSidebarRightCollapse aria-hidden="true" />}</button>
        <div><h1>{active?.label ?? productName}</h1><p>{productName}</p></div>
        <button className="standard-icon-button" type="button" title={dark ? "使用浅色主题" : "使用深色主题"} aria-label={dark ? "使用浅色主题" : "使用深色主题"} onClick={() => setDark((value) => !value)}>{dark ? <IconSun aria-hidden="true" /> : <IconMoon aria-hidden="true" />}</button>
      </header>
      <main id="standard-workspace" className="standard-workspace" tabIndex={-1}>{ActiveComponent ? <ActiveComponent /> : <section className="standard-empty" role="status"><IconApps aria-hidden="true" /><h2>{productName}</h2><p>当前没有可用工作区</p></section>}</main>
    </div>
  </div>;
}
