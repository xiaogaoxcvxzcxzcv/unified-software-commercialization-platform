import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import route from "./workbench";

describe("custom workbench slot", () => {
  it("keeps product-owned work interactive and supports duplicate labels", () => {
    render(<route.Component />);
    const input = screen.getByLabelText("新事项");
    fireEvent.change(input, { target: { value: "检查交付结果" } });
    fireEvent.click(screen.getByRole("button", { name: "添加事项" }));
    expect(screen.getAllByText("检查交付结果")).toHaveLength(2);
    fireEvent.click(screen.getByRole("button", { name: "删除第 2 项" }));
    expect(screen.getAllByText("检查交付结果")).toHaveLength(1);
  });

  it("announces an empty state and restores focus after deleting items", async () => {
    render(<route.Component />);
    fireEvent.click(screen.getByRole("button", { name: "删除第 1 项" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "删除第 1 项" })).toHaveFocus());
    fireEvent.click(screen.getByRole("button", { name: "删除第 1 项" }));
    expect(screen.getByRole("status")).toHaveTextContent("暂无事项");
    await waitFor(() => expect(screen.getByLabelText("新事项")).toHaveFocus());
  });
});
