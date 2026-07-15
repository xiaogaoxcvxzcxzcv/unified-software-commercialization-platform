import type { ComponentType, ReactNode } from "react";

export interface CustomRoute { readonly id: string; readonly label: string; readonly icon?: ReactNode; readonly Component: ComponentType; }
interface CustomRouteModule { readonly default?: CustomRoute | readonly CustomRoute[]; }

export function validateCustomRoutes(routes: readonly CustomRoute[]): readonly CustomRoute[] {
  const ids = new Set<string>();
  return routes.map((route) => {
    if (!route || typeof route.id !== "string" || route.id.trim() === "") throw new Error("custom route id must be non-empty");
    if (typeof route.label !== "string" || route.label.trim() === "") throw new Error(`custom route ${route.id} label must be non-empty`);
    if (typeof route.Component !== "function") throw new Error(`custom route ${route.id} component must be callable`);
    if (ids.has(route.id)) throw new Error(`duplicate custom route id: ${route.id}`);
    ids.add(route.id);
    return route;
  });
}

export function discoverCustomRoutes(): readonly CustomRoute[] {
  const modules = import.meta.glob<CustomRouteModule>(["../custom/routes/*.tsx", "!../custom/routes/*.test.tsx", "!../custom/routes/*.spec.tsx", "!../custom/routes/*.stories.tsx"], { eager: true });
  const routes = Object.entries(modules)
    .sort(([left], [right]) => left.localeCompare(right))
    .flatMap(([, module]) => module.default ? Array.isArray(module.default) ? module.default : [module.default] : []);
  return validateCustomRoutes(routes);
}
