import { cp, mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { enforceSourceMapPolicy } from "./source-map-policy.mjs";

await mkdir(new URL("../dist/web-react/src/account/", import.meta.url), { recursive: true });
await cp(new URL("../web-react/src/styles.css", import.meta.url), new URL("../dist/web-react/src/styles.css", import.meta.url));
await cp(new URL("../web-react/src/account/account-blocks.css", import.meta.url), new URL("../dist/web-react/src/account/account-blocks.css", import.meta.url));
await enforceSourceMapPolicy(fileURLToPath(new URL("../dist/", import.meta.url)));
