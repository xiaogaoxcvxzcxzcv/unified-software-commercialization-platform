import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ClientSdk } from "@capability-platform/client-sdk";
import { ClientBlockController } from "../headless/src/index.js";
import { ClientAsyncBlock, ClientField, ClientUiProvider, useCapability, useClientUi } from "../web-react/src/index.js";

async function trustedContext() {
  const sdk = new ClientSdk({ baseUrl: "https://api.example.test", fetch: async () => new Response(JSON.stringify({
    client_session_token: "t".repeat(48), expires_at: "2099-07-14T00:00:00Z",
    product_context: { product_id: "prod-1", product_code: "product-one", environment: "production" },
    application_context: { product_id: "prod-1", environment: "production", application_id: "app-1", application_code: "web", platform: "web", distribution_channel: "official", client_id: "client-1", client_version: "1", release_track: "stable", context_version: 1 },
    tenant_context: { product_id: "prod-1", tenant_id: "tenant-1", tenant_type: "official", tenant_status: "active", resolved_by: "official_channel", context_version: 1 },
  }), { status: 201, headers: { "Cache-Control": "no-store", "Content-Type": "application/json" } }) });
  return sdk.establishSession({ clientId: "client-1", credentialId: "credential-1", clientVersion: "1", requestNonce: "nonce-1234567890123456", clientProof: { schema_version: 1, type: "hmac_sha256_v1", value: "p".repeat(64), timestamp: "2026-07-14T00:00:00Z" } });
}

describe("web-react foundation", () => {
  it("provides trusted context and capability membership without raw scope props", async () => {
    const context = await trustedContext();
    function Probe() {
      const client = useClientUi();
      return <span>{client.context.product.productCode}:{useCapability("account") ? "enabled" : "disabled"}</span>;
    }
    render(<ClientUiProvider context={context} enabledCapabilities={["account"]}><Probe /></ClientUiProvider>);
    expect(screen.getByText("product-one:enabled")).toBeInTheDocument();
  });

  it("renders accessible error and disabled states", async () => {
    const controller = new ClientBlockController<string>();
    controller.disable("capability_disabled", "Not available");
    const { rerender } = render(<ClientAsyncBlock controller={controller} renderReady={(value) => value} messages={{ loading: "Loading", empty: "Empty", disabled: "Disabled", success: "Saved", retry: "Retry" }} />);
    expect(screen.getByText("Disabled")).toBeInTheDocument();
    controller.reset();
    await controller.load(async () => { throw new Error("Failed safely"); });
    rerender(<ClientAsyncBlock controller={controller} renderReady={(value) => value} messages={{ loading: "Loading", empty: "Empty", disabled: "Disabled", success: "Saved", retry: "Retry" }} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Failed safely");
  });

  it("connects field errors to inputs", () => {
    render(<ClientField label="Email" error="Invalid email" inputProps={{ id: "email", name: "email" }} />);
    const input = screen.getByLabelText("Email");
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAccessibleDescription("Invalid email");
  });
});
