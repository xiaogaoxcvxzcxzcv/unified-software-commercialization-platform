import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AppShell } from "./AppShell";
import { validateCustomRoutes } from "../integration/routes";

const route = { id: "custom.test", label: "工作台", Component: () => <div>自定义内容</div> };

describe("standard-a shell", () => {
  it("renders a custom route and toggles the theme", () => {
    render(<AppShell productName="测试软件" routes={[route]} />);
    expect(screen.getByRole("heading", { name: "工作台" })).toBeInTheDocument();
    expect(screen.getByText("自定义内容")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "使用深色主题" }));
    expect(document.documentElement.dataset.theme).toBe("dark");
  });

  it("opens and closes mobile navigation with explicit controls", () => {
    render(<AppShell productName="测试软件" routes={[route]} />);
    const menu = screen.getByRole("button", { name: "打开主导航" });
    fireEvent.click(menu);
    expect(menu).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("button", { name: "工作台" })).toHaveFocus();
    fireEvent.click(screen.getByRole("button", { name: "关闭主导航" }));
    expect(menu).toHaveAttribute("aria-expanded", "false");
    expect(menu).toHaveFocus();
  });

  it("closes mobile navigation with Escape and renders a non-blank empty state", () => {
    const { rerender } = render(<AppShell productName="测试软件" routes={[route]} />);
    const menu = screen.getByRole("button", { name: "打开主导航" });
    fireEvent.click(menu);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(menu).toHaveAttribute("aria-expanded", "false");
    expect(menu).toHaveFocus();

    rerender(<AppShell productName="测试软件" routes={[]} />);
    expect(screen.getByRole("status")).toHaveTextContent("当前没有可用工作区");
  });

  it("keeps forward and reverse tab focus inside the open navigation", () => {
    render(<AppShell productName="测试软件" routes={[route]} />);
    fireEvent.click(screen.getByRole("button", { name: "打开主导航" }));
    const routeButton = screen.getByRole("button", { name: "工作台" });
    const closeButton = screen.getByRole("button", { name: "关闭主导航" });

    routeButton.focus();
    fireEvent.keyDown(routeButton, { key: "Tab" });
    expect(closeButton).toHaveFocus();

    closeButton.focus();
    fireEvent.keyDown(closeButton, { key: "Tab", shiftKey: true });
    expect(routeButton).toHaveFocus();
  });

  it("fails closed for invalid or duplicate custom routes", () => {
    expect(() => validateCustomRoutes([{ ...route, id: "" }])).toThrow("id must be non-empty");
    expect(() => validateCustomRoutes([{ ...route, label: " " }])).toThrow("label must be non-empty");
    expect(() => validateCustomRoutes([route, { ...route }])).toThrow("duplicate custom route id");
  });
});
