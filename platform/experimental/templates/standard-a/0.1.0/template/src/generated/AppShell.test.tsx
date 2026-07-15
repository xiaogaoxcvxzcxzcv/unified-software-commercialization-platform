import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AppShell } from "./AppShell";

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
    const closeControls = screen.getAllByRole("button", { name: "关闭主导航" });
    expect(closeControls.find((control) => control.hasAttribute("aria-expanded"))).toHaveAttribute("aria-expanded", "true");
    fireEvent.click(closeControls.find((control) => !control.hasAttribute("aria-expanded"))!);
    expect(screen.getByRole("button", { name: "打开主导航" })).toHaveAttribute("aria-expanded", "false");
  });

  it("closes mobile navigation with Escape and renders a non-blank empty state", () => {
    const { rerender } = render(<AppShell productName="测试软件" routes={[route]} />);
    fireEvent.click(screen.getByRole("button", { name: "打开主导航" }));
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.getByRole("button", { name: "打开主导航" })).toHaveAttribute("aria-expanded", "false");

    rerender(<AppShell productName="测试软件" routes={[]} />);
    expect(screen.getByRole("status")).toHaveTextContent("当前没有可用工作区");
  });
});
