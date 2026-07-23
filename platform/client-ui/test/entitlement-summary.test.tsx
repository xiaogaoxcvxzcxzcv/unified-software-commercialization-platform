import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { EntitlementSummaryBlock } from "../web-react/src/entitlement/index.js";
import type { EntitlementBlockState, EntitlementSummaryValue } from "../web-react/src/entitlement/index.js";

afterEach(cleanup);

const value: EntitlementSummaryValue = {
  revision: 7,
  planCode: "pro",
  validUntil: "2026-08-23T00:00:00Z",
  offlineGraceUntil: "2026-08-24T00:00:00Z",
  updatedAt: "2026-07-23T10:00:00Z",
  features: [
    { code: "export_pdf", label: "PDF 导出", kind: "limit", value: 100 },
    { code: "priority_queue", label: "优先队列", kind: "boolean", value: true },
  ],
};

describe("entitlement.summary", () => {
  it("renders all public states with accessible text", () => {
    const cases: ReadonlyArray<{ state: EntitlementBlockState; text: string }> = [
      { state: "idle", text: "当前没有可用权益" },
      { state: "loading", text: "正在读取服务端权益" },
      { state: "ready", text: "PDF 导出" },
      { state: "submitting", text: "正在刷新权益" },
      { state: "success", text: "权益已刷新" },
      { state: "empty", text: "当前没有可用权益" },
      { state: "failed", text: "权益服务暂时不可用" },
      { state: "disabled", text: "当前产品未启用权益能力" },
    ];
    for (const item of cases) {
      const view = render(<EntitlementSummaryBlock state={item.state} value={item.state === "ready" || item.state === "submitting" || item.state === "success" ? value : undefined} error={{ code: "temporarily_unavailable", message: "权益服务暂时不可用", retryable: true }} />);
      const region = screen.getByRole("region", { name: "权益摘要" });
      expect(region).toHaveAttribute("data-state", item.state);
      expect(within(region).getAllByText(item.text, { exact: false }).length).toBeGreaterThan(0);
      view.unmount();
    }
  });

  it("shows current server projection without accepting product tenant or user scope props", () => {
    render(<EntitlementSummaryBlock state="ready" value={value} />);
    expect(screen.getByText("Revision 7")).toBeInTheDocument();
    expect(screen.getByText("pro")).toBeInTheDocument();
    expect(screen.getByText("PDF 导出")).toBeInTheDocument();
    expect(document.body).toHaveTextContent("priority_queue");
    expect(document.body.textContent).not.toContain("product_id");
    expect(document.body.textContent).not.toContain("tenant_id");
    expect(document.body.textContent).not.toContain("user_id");
  });

  it("distinguishes expired empty state from never-owned empty state", () => {
    const expired = { ...value, features: [], emptyReason: "expired" as const };
    const view = render(<EntitlementSummaryBlock state="empty" value={expired} />);
    expect(screen.getByText("权益已到期")).toBeInTheDocument();
    expect(screen.getByText("曾经拥有权益", { exact: false })).toBeInTheDocument();
    view.rerender(<EntitlementSummaryBlock state="empty" value={{ ...value, features: [], emptyReason: "never_owned" }} />);
    expect(screen.getByText("当前没有可用权益")).toBeInTheDocument();
    expect(screen.getByText("没有可用会员或功能权益", { exact: false })).toBeInTheDocument();
  });

  it("emits only navigation and retry events without deciding prices or plans", () => {
    const renew = vi.fn();
    const upgrade = vi.fn();
    const retry = vi.fn();
    render(<EntitlementSummaryBlock state="ready" value={value} actions={{ renew, upgrade, retry }} />);
    fireEvent.click(screen.getByRole("button", { name: "续费" }));
    fireEvent.click(screen.getByRole("button", { name: "升级" }));
    fireEvent.click(screen.getByRole("button", { name: "刷新" }));
    expect(renew).toHaveBeenCalledTimes(1);
    expect(upgrade).toHaveBeenCalledTimes(1);
    expect(retry).toHaveBeenCalledTimes(1);
    expect(document.body.textContent).not.toMatch(/¥|￥|price|amount/i);
  });

  it("keeps retry available on recoverable failed state and hides stale entitlement content", () => {
    const retry = vi.fn();
    render(<EntitlementSummaryBlock state="failed" value={value} error={{ code: "network_error", message: "权益服务暂时不可用", retryable: true }} onRetry={retry} />);
    expect(screen.getByRole("alert")).toHaveTextContent("权益服务暂时不可用");
    expect(screen.queryByText("PDF 导出")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(retry).toHaveBeenCalledTimes(1);
  });
});
