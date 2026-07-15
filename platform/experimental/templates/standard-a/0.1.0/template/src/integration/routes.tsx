import type { ComponentType, ReactNode } from "react";

export interface CustomRoute { readonly id: string; readonly label: string; readonly icon?: ReactNode; readonly Component: ComponentType; }
interface CustomRouteModule { readonly default?: CustomRoute | readonly CustomRoute[]; }

export function discoverCustomRoutes(): readonly CustomRoute[] {
  const modules = import.meta.glob<CustomRouteModule>(["../custom/routes/*.tsx", "!../custom/routes/*.test.tsx"], { eager: true });
  return Object.entries(modules)
    .sort(([left], [right]) => left.localeCompare(right))
    .flatMap(([, module]) => module.default ? Array.isArray(module.default) ? module.default : [module.default] : [])
    .filter((route, index, all) => route.id !== "" && all.findIndex((candidate) => candidate.id === route.id) === index);
}
