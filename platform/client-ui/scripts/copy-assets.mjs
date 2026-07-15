import { cp, mkdir } from "node:fs/promises";

await mkdir(new URL("../dist/web-react/src/", import.meta.url), { recursive: true });
await cp(new URL("../web-react/src/styles.css", import.meta.url), new URL("../dist/web-react/src/styles.css", import.meta.url));
