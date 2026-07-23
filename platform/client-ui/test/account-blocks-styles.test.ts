import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const sharedStyles = readFileSync(join(process.cwd(), "web-react/src/styles.css"), "utf8");
const accountStyles = readFileSync(join(process.cwd(), "web-react/src/account/account-blocks.css"), "utf8");
const hostedStyles = readFileSync(join(process.cwd(), "../hosted-web/src/styles.css"), "utf8");

function themeTokens(selector: string): Record<string, string> {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const block = hostedStyles.match(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`))?.[1];
  if (!block) throw new Error(`missing theme block: ${selector}`);
  return Object.fromEntries(Array.from(block.matchAll(/--([a-z-]+):\s*(#[0-9a-f]{6})\s*;/gi), (match) => [match[1], match[2]]));
}

function contrastRatio(first: string, second: string): number {
  const luminance = (hex: string) => {
    const channels = hex.slice(1).match(/.{2}/g)?.map((value) => Number.parseInt(value, 16) / 255);
    if (!channels || channels.length !== 3) throw new Error(`invalid color: ${hex}`);
    const linear = channels.map((value) => value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4);
    return .2126 * linear[0]! + .7152 * linear[1]! + .0722 * linear[2]!;
  };
  const a = luminance(first);
  const b = luminance(second);
  return (Math.max(a, b) + .05) / (Math.min(a, b) + .05);
}

describe("Account Block visual accessibility rules", () => {
  it("exports Account Blocks through the public Client UI stylesheet and Hosted imports that export", () => {
    expect(sharedStyles.trimStart()).toMatch(/^@import "\.\/account\/account-blocks\.css";/);
    expect(hostedStyles.trimStart()).toMatch(/^@import "@capability-platform\/client-ui\/styles\.css";/);
  });

  it("uses host-injectable on-brand and solid focus tokens", () => {
    expect(sharedStyles).toContain("var(--client-on-brand, var(--standard-on-brand, Canvas))");
    expect(sharedStyles).not.toMatch(/\.client-button[\s\S]*?color:\s*#fff(?:fff)?\b/i);
    expect(sharedStyles).toContain("outline: 3px solid var(--client-focus, Highlight)");
    expect(accountStyles).toContain("outline: 3px solid var(--client-focus, Highlight)");
  });

  it("meets numeric WCAG contrast for current light and dark host tokens", () => {
    const light = themeTokens(":root");
    const dark = themeTokens(':root[data-theme="dark"]');
    for (const theme of [light, dark]) {
      expect(contrastRatio(theme["standard-on-brand"]!, theme["standard-brand"]!)).toBeGreaterThanOrEqual(4.5);
      expect(contrastRatio(theme["standard-focus"]!, theme["standard-surface"]!)).toBeGreaterThanOrEqual(3);
      expect(contrastRatio(theme["standard-focus"]!, theme["standard-canvas"]!)).toBeGreaterThanOrEqual(3);
    }
  });

  it("wraps dynamic labels and constrains the native dialog", () => {
    expect(sharedStyles).toMatch(/\.client-button > span \{[^}]*min-width:\s*0;[^}]*overflow-wrap:\s*anywhere;/s);
    expect(accountStyles).toMatch(/\.account-session-list strong[^\n]*overflow-wrap:\s*anywhere;/);
    expect(accountStyles).toMatch(/\.account-confirmation \{[^}]*max-height:\s*calc\(100dvh - 32px\);[^}]*overflow:\s*auto;/s);
    expect(accountStyles).toContain(".account-confirmation::backdrop");
  });

  it("stops spinner motion and exposes forced-color rules", () => {
    expect(sharedStyles).toMatch(/prefers-reduced-motion:\s*reduce[\s\S]*?\.client-spinner \{ animation:\s*none;/);
    expect(sharedStyles).toContain("@media (forced-colors: active)");
    expect(accountStyles).toContain("@media (forced-colors: active)");
    expect(accountStyles).toContain("outline-color: Highlight");
  });
});
