import type { TrustedClientContext } from "@capability-platform/client-sdk";
import { createContext, useContext, useMemo } from "react";
import type { CSSProperties, ReactNode } from "react";
import { defaultClientTheme } from "../../contracts/src/index.js";
import type { ClientThemeTokens } from "../../contracts/src/index.js";

interface ClientUiContextValue {
  readonly context: TrustedClientContext;
  readonly enabledCapabilities: ReadonlySet<string>;
  readonly theme: ClientThemeTokens;
}

const ClientUiContext = createContext<ClientUiContextValue | null>(null);

export function ClientUiProvider({ context, enabledCapabilities, theme, children }: {
  readonly context: TrustedClientContext;
  readonly enabledCapabilities: readonly string[];
  readonly theme?: Partial<ClientThemeTokens>;
  readonly children: ReactNode;
}) {
  const value = useMemo<ClientUiContextValue>(() => ({
    context,
    enabledCapabilities: new Set(enabledCapabilities),
    theme: Object.freeze({ ...defaultClientTheme, ...theme }),
  }), [context, enabledCapabilities, theme]);
  const style = {
    "--client-brand": value.theme.brandColor,
    "--client-accent": value.theme.accentColor,
    "--client-text": value.theme.textColor,
    "--client-muted": value.theme.mutedTextColor,
    "--client-surface": value.theme.surfaceColor,
    "--client-canvas": value.theme.canvasColor,
    "--client-border": value.theme.borderColor,
    "--client-danger": value.theme.dangerColor,
    "--client-success": value.theme.successColor,
    "--client-focus": value.theme.focusColor,
    "--client-radius": `${value.theme.radius}px`,
    "--client-font": value.theme.fontFamily,
  } as CSSProperties;
  return <ClientUiContext.Provider value={value}><div className="client-ui-root" style={style}>{children}</div></ClientUiContext.Provider>;
}

export function useClientUi(): ClientUiContextValue {
  const value = useContext(ClientUiContext);
  if (!value) throw new Error("useClientUi must be used inside ClientUiProvider");
  return value;
}

export function useCapability(capabilityId: string): boolean {
  return useClientUi().enabledCapabilities.has(capabilityId);
}
