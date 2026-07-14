import { describe, expect, it } from "vitest";
import { parseHostedLaunch } from "../hosted-web/src/index.js";

describe("parseHostedLaunch", () => {
  it("accepts a versioned route with one opaque interaction", () => {
    expect(parseHostedLaunch("https://ui.example.test/ui/v1/account?interaction_id=interaction_123456789")).toEqual({ routeId: "hosted.account", interactionId: "interaction_123456789" });
  });

  it.each(["product_id", "tenant_id", "amount", "return_url", "access_token"])("rejects untrusted %s URL input", (name) => {
    expect(() => parseHostedLaunch(`https://ui.example.test/ui/v1/checkout?interaction_id=interaction_123456789&${name}=forged`)).toThrow(`cannot contain ${name}`);
  });

  it("rejects unknown routes, duplicate interactions, fragments, and insecure remote origins", () => {
    expect(() => parseHostedLaunch("https://ui.example.test/ui/v1/unknown?interaction_id=interaction_123456789")).toThrow("unsupported");
    expect(() => parseHostedLaunch("https://ui.example.test/ui/v1/auth?interaction_id=interaction_123456789&interaction_id=interaction_987654321")).toThrow("one valid");
    expect(() => parseHostedLaunch("https://ui.example.test/ui/v1/auth?interaction_id=interaction_123456789#token")).toThrow("fragment");
    expect(() => parseHostedLaunch("http://ui.example.test/ui/v1/auth?interaction_id=interaction_123456789")).toThrow("HTTPS");
    expect(() => parseHostedLaunch("https://user:secret@ui.example.test/ui/v1/auth?interaction_id=interaction_123456789")).toThrow("credentials");
  });
});
