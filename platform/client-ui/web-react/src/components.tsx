import { useSyncExternalStore } from "react";
import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from "react";
import type { ClientBlockController } from "../../headless/src/index.js";

export function ClientButton({ busy = false, icon, children, disabled, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & {
  readonly busy?: boolean;
  readonly icon?: ReactNode;
}) {
  return <button {...props} className={`client-button ${props.className ?? ""}`} disabled={disabled || busy} aria-busy={busy}>
    {busy ? <span className="client-spinner" aria-hidden="true" /> : icon}
    <span>{children}</span>
  </button>;
}

export function ClientField({ label, error, inputProps }: {
  readonly label: string;
  readonly error?: string;
  readonly inputProps: InputHTMLAttributes<HTMLInputElement> & { id: string };
}) {
  const errorId = `${inputProps.id}-error`;
  return <div className="client-field">
    <label htmlFor={inputProps.id}>{label}</label>
    <input {...inputProps} aria-invalid={Boolean(error)} aria-describedby={error ? errorId : inputProps["aria-describedby"]} />
    {error && <span id={errorId} role="alert">{error}</span>}
  </div>;
}

export function ClientNotice({ tone = "neutral", children }: { readonly tone?: "neutral" | "success" | "danger"; readonly children: ReactNode }) {
  return <div className={`client-notice client-notice-${tone}`} role={tone === "danger" ? "alert" : "status"}>{children}</div>;
}

export function ClientAsyncBlock<T>({ controller, renderReady, messages, onRetry }: {
  readonly controller: ClientBlockController<T>;
  readonly renderReady: (value: T) => ReactNode;
  readonly messages: {
    readonly loading: string;
    readonly empty: string;
    readonly disabled: string;
    readonly success: string;
    readonly retry: string;
  };
  readonly onRetry?: () => void;
}) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  if ((snapshot.state === "ready" || snapshot.state === "success") && snapshot.data !== undefined) {
    return <>{renderReady(snapshot.data)}{snapshot.state === "success" && <span className="client-sr-only" role="status">{messages.success}</span>}</>;
  }
  if (snapshot.state === "loading" || snapshot.state === "submitting") return <div className="client-state" aria-busy="true"><span className="client-spinner" aria-hidden="true" />{messages.loading}</div>;
  if (snapshot.state === "empty") return <div className="client-state">{messages.empty}</div>;
  if (snapshot.state === "disabled") return <div className="client-state">{messages.disabled}</div>;
  if (snapshot.state === "failed") return <ClientNotice tone="danger"><span>{snapshot.error?.message}</span>{snapshot.error?.retryable && onRetry && <ClientButton type="button" onClick={onRetry}>{messages.retry}</ClientButton>}</ClientNotice>;
  return null;
}
