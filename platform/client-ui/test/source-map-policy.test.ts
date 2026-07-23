import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { enforceSourceMapPolicy } from "../scripts/source-map-policy.mjs";

const fixtureRoots: string[] = [];

afterEach(async () => {
  await Promise.all(fixtureRoots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

describe("Client UI source map release policy", () => {
  it("rejects an invalid JavaScript source map without deleting evidence", async () => {
    const root = await fixtureRoot();
    const output = join(root, "broken.js");
    const map = `${output}.map`;
    await writeFile(output, "export const broken = true;\n//# sourceMappingURL=broken.js.map\n", "utf8");
    await writeFile(map, JSON.stringify({ version: 3, sources: ["../src/broken.ts"], mappings: "" }), "utf8");

    await expect(enforceSourceMapPolicy(root)).rejects.toThrow("non-declaration source map is invalid");
    expect(await readFile(map, "utf8")).toContain("broken.ts");
    expect(await readFile(output, "utf8")).toContain("sourceMappingURL=broken.js.map");
  });

  it("removes only an invalid declaration map and its matching comment", async () => {
    const root = await fixtureRoot();
    const output = join(root, "public.d.ts");
    const map = `${output}.map`;
    await writeFile(output, "export interface PublicValue { value: string }\n//# sourceMappingURL=public.d.ts.map\n", "utf8");
    await writeFile(map, JSON.stringify({ version: 3, sources: ["../src/public.ts"], mappings: "" }), "utf8");

    await enforceSourceMapPolicy(root);
    await expect(readFile(map, "utf8")).rejects.toMatchObject({ code: "ENOENT" });
    expect(await readFile(output, "utf8")).not.toContain("sourceMappingURL");
  });
});

async function fixtureRoot(): Promise<string> {
  const runtime = resolve(process.cwd(), "../../.runtime/G2A-06/source-map-policy-tests");
  await mkdir(runtime, { recursive: true });
  const root = await mkdtemp(join(runtime, "fixture-"));
  fixtureRoots.push(root);
  return root;
}
