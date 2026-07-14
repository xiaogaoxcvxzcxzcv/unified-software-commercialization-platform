import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import route from "./workbench";

describe("custom workbench slot", () => {
  it("keeps product-owned work interactive and supports duplicate labels", () => {
    render(<route.Component />);
    const input = screen.getByLabelText("新事项");
    fireEvent.change(input, { target: { value: "检查交付结果" } });
    fireEvent.click(screen.getByRole("button", { name: "添加事项" }));
    expect(screen.getAllByText("检查交付结果")).toHaveLength(2);
    fireEvent.click(screen.getAllByRole("button", { name: "删除 检查交付结果" })[0]);
    expect(screen.getAllByText("检查交付结果")).toHaveLength(1);
  });
});
