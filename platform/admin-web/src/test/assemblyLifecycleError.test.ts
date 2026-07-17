import { describe, expect, it } from "vitest";
import { AuthApiError } from "../api/authClient";
import { lifecycleErrorMessage } from "../features/assembly/lifecycleIntent";

describe("lifecycle error projection", () => {
  it("localizes whitelisted codes without exposing detail", () => {
    const error = new AuthApiError("Version Conflict", { status: 409, code: "assembly.version_conflict", retryable: false, requestId: "request-1", detail: "D:/private/workspace" });
    expect(lifecycleErrorMessage(error, "操作失败")).toBe("资源版本已变化，请刷新后重试");
  });

  it("uses a fixed fallback and request id for unknown errors", () => {
    const error = new AuthApiError("Internal path D:/private", { status: 500, code: "assembly.unexpected", retryable: true, requestId: "request-2", detail: "D:/private/workspace" });
    const message = lifecycleErrorMessage(error, "生命周期操作失败");
    expect(message).toBe("生命周期操作失败（请求编号：request-2）");
    expect(message).not.toContain("private");
    expect(message).not.toContain("Internal path");
  });

  it("does not expose non-API exception messages", () => {
    expect(lifecycleErrorMessage(new Error("C:/secret/source"), "生命周期请求失败")).toBe("生命周期请求失败");
  });
});
