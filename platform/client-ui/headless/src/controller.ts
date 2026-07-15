import { ClientSdkError } from "@capability-platform/client-sdk";
import type { ClientBlockError, ClientBlockSnapshot } from "../../contracts/src/index.js";

export type ClientBlockListener = () => void;

function mapError(reason: unknown): ClientBlockError {
  if (reason instanceof ClientSdkError) {
    const kind = reason.kind === "authentication" ? "authentication_required"
      : reason.kind === "capability_disabled" ? "capability_disabled"
        : reason.retryable ? "retryable" : "terminal";
    return Object.freeze({ kind, code: reason.code, message: reason.message, requestId: reason.requestId, retryable: reason.retryable });
  }
  return Object.freeze({ kind: "unknown", code: "unknown_error", message: reason instanceof Error ? reason.message : "The operation failed.", retryable: false });
}

export class ClientBlockController<T> {
  #snapshot: ClientBlockSnapshot<T> = Object.freeze({ state: "idle", revision: 0 });
  readonly #listeners = new Set<ClientBlockListener>();
  #operation = 0;
  #abort?: AbortController;

  getSnapshot = (): ClientBlockSnapshot<T> => this.#snapshot;
  subscribe = (listener: ClientBlockListener): (() => void) => {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  };

  disable(code = "capability_disabled", message = "This capability is disabled."): void {
    this.cancel();
    this.#set({ state: "disabled", error: { kind: "capability_disabled", code, message, retryable: false } });
  }

  reset(): void {
    this.cancel();
    this.#set({ state: "idle" });
  }

  cancel(): void {
    this.#operation += 1;
    this.#abort?.abort();
    this.#abort = undefined;
  }

  async load(loader: (signal: AbortSignal) => Promise<T>, empty: (value: T) => boolean = () => false): Promise<void> {
    await this.#run("loading", loader, (value) => empty(value) ? "empty" : "ready");
  }

  async submit(action: (signal: AbortSignal) => Promise<T>): Promise<void> {
    await this.#run("submitting", action, () => "success");
  }

  async #run(pending: "loading" | "submitting", operation: (signal: AbortSignal) => Promise<T>, success: (value: T) => "ready" | "empty" | "success"): Promise<void> {
    this.cancel();
    const id = this.#operation;
    const abort = new AbortController();
    this.#abort = abort;
    this.#set({ state: pending, data: this.#snapshot.data });
    try {
      const value = await operation(abort.signal);
      if (id !== this.#operation) return;
      this.#set({ state: success(value), data: value });
    } catch (reason) {
      if (id !== this.#operation || abort.signal.aborted) return;
      this.#set({ state: "failed", data: this.#snapshot.data, error: mapError(reason) });
    } finally {
      if (id === this.#operation) this.#abort = undefined;
    }
  }

  #set(next: Omit<ClientBlockSnapshot<T>, "revision">): void {
    this.#snapshot = Object.freeze({ ...next, revision: this.#snapshot.revision + 1 });
    for (const listener of this.#listeners) listener();
  }
}
