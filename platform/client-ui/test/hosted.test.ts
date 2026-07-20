import { describe, expect, it } from "vitest";
import { parseHostedLaunch } from "../hosted-web/src/index.js";

describe("parseHostedLaunch", () => {
  it("accepts a versioned route with one opaque interaction", () => {
		expect(parseHostedLaunch("https://ui.example.test/ui/v1/account?interaction_id=hint_abcdefghijklmnopqrstuvwxyz")).toEqual({ routeId: "hosted.account", interactionId: "hint_abcdefghijklmnopqrstuvwxyz" });
  });

  it.each(["product_id", "tenant_id", "amount", "return_url", "access_token"])("rejects untrusted %s URL input", (name) => {
		expect(() => parseHostedLaunch(`https://ui.example.test/ui/v1/checkout?interaction_id=hint_abcdefghijklmnopqrstuvwxyz&${name}=forged`)).toThrow(`cannot contain ${name}`);
  });

  it("rejects unknown routes, duplicate interactions, fragments, and insecure remote origins", () => {
		expect(() => parseHostedLaunch("https://ui.example.test/ui/v1/unknown?interaction_id=hint_abcdefghijklmnopqrstuvwxyz")).toThrow("unsupported");
		expect(() => parseHostedLaunch("https://ui.example.test/ui/v1/auth?interaction_id=hint_abcdefghijklmnopqrstuvwxyz&interaction_id=hint_zyxwvutsrqponmlkjihgfedcba")).toThrow("one valid");
		expect(() => parseHostedLaunch("https://ui.example.test/ui/v1/auth?interaction_id=hint_abcdefghijklmnopqrstuvwxyz#token")).toThrow("fragment");
		expect(() => parseHostedLaunch("http://ui.example.test/ui/v1/auth?interaction_id=hint_abcdefghijklmnopqrstuvwxyz")).toThrow("HTTPS");
		expect(() => parseHostedLaunch("https://user:secret@ui.example.test/ui/v1/auth?interaction_id=hint_abcdefghijklmnopqrstuvwxyz")).toThrow("credentials");
  });

	it("rejects a legacy non-hint interaction identifier", () => {
		expect(() => parseHostedLaunch("https://ui.example.test/ui/v1/auth?interaction_id=interaction_123456789")).toThrow("one valid");
	});
});
