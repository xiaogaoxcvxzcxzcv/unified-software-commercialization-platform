import { describe, expect, it, vi } from "vitest";
import { ClientSdkError } from "@capability-platform/client-sdk";
import { ClientBlockController } from "../headless/src/index.js";

describe("ClientBlockController", () => {
  it("publishes loading, ready, empty, submitting, and success states", async () => {
    const controller = new ClientBlockController<string[]>();
    const states: string[] = [];
    controller.subscribe(() => states.push(controller.getSnapshot().state));
    await controller.load(async () => ["one"]);
    await controller.load(async () => [], (value) => value.length === 0);
    await controller.submit(async () => ["saved"]);
    expect(states).toEqual(["loading", "ready", "loading", "empty", "submitting", "success"]);
  });

  it("prevents an obsolete request from overwriting the latest result", async () => {
    const controller = new ClientBlockController<string>();
    let resolveFirst!: (value: string) => void;
    const first = controller.load(() => new Promise((resolve) => { resolveFirst = resolve; }));
    await controller.load(async () => "latest");
    resolveFirst("obsolete");
    await first;
    expect(controller.getSnapshot()).toMatchObject({ state: "ready", data: "latest" });
  });

  it("maps stable SDK failures and exposes retryability", async () => {
    const controller = new ClientBlockController<string>();
    await controller.load(async () => { throw new ClientSdkError("Try again", { kind: "server", code: "temporarily_unavailable", retryable: true, requestId: "req-1" }); });
    expect(controller.getSnapshot()).toMatchObject({ state: "failed", error: { kind: "retryable", code: "temporarily_unavailable", requestId: "req-1", retryable: true } });
  });

  it("cancels in-flight work without publishing a failure", async () => {
    vi.useFakeTimers();
    const controller = new ClientBlockController<string>();
    const pending = controller.load((signal) => new Promise((_, reject) => signal.addEventListener("abort", () => reject(new Error("aborted")), { once: true })));
    controller.reset();
    await pending;
    expect(controller.getSnapshot().state).toBe("idle");
    vi.useRealTimers();
  });
});
