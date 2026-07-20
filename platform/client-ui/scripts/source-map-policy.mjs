import { basename, dirname, isAbsolute, join } from "node:path";
import { readdir, readFile, rm, writeFile } from "node:fs/promises";

export async function enforceSourceMapPolicy(distRoot) {
  const mapPaths = await collectFiles(distRoot, (name) => name.endsWith(".map"));
  if (mapPaths.length === 0) throw new Error("Client UI build produced no source maps");

  for (const mapPath of mapPaths) {
    let sourceMap;
    try {
      sourceMap = JSON.parse(await readFile(mapPath, "utf8"));
    } catch (error) {
      await handleInvalidMap(mapPath, error);
      continue;
    }
    const complete = Array.isArray(sourceMap.sources)
      && Array.isArray(sourceMap.sourcesContent)
      && sourceMap.sources.length === sourceMap.sourcesContent.length
      && sourceMap.sources.every((source) => typeof source === "string")
      && sourceMap.sourcesContent.every((content) => typeof content === "string");
    if (!complete) {
      await handleInvalidMap(mapPath);
      continue;
    }
    for (let index = 0; index < sourceMap.sources.length; index += 1) {
      const source = sourceMap.sources[index];
      const content = sourceMap.sourcesContent[index];
      if (isAbsolute(source) || /^[A-Za-z]:[\\/]/.test(source) || /^\\\\/.test(source) || /^file:/i.test(source)) {
        throw new Error(`Client UI source map contains an absolute source path: ${mapPath}`);
      }
      if (/-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/.test(content) || /postgres(?:ql)?:\/\/[^\s"']+:[^@\s"']+@/i.test(content)) {
        throw new Error(`Client UI source map contains secret material: ${mapPath}`);
      }
    }
  }

  const remainingMapPaths = await collectFiles(distRoot, (name) => name.endsWith(".map"));
  for (const outputPath of await collectFiles(distRoot, (name) => name.endsWith(".js") || name.endsWith(".d.ts"))) {
    const output = await readFile(outputPath, "utf8");
    const reference = output.match(/\/\/# sourceMappingURL=([^\r\n]+)/)?.[1];
    if (reference && !remainingMapPaths.includes(join(dirname(outputPath), reference))) throw new Error(`Client UI output contains a dangling source map reference: ${outputPath}`);
  }
}

async function handleInvalidMap(mapPath, cause) {
  if (!mapPath.endsWith(".d.ts.map")) throw new Error(`Client UI non-declaration source map is invalid: ${mapPath}`, { cause });
  const outputPath = mapPath.slice(0, -4);
  if (!outputPath.endsWith(".d.ts")) throw new Error(`Client UI declaration source map has no declaration output: ${mapPath}`, { cause });
  const output = await readFile(outputPath, "utf8");
  const reference = output.match(/\/\/# sourceMappingURL=([^\r\n]+)/)?.[1];
  if (reference !== basename(mapPath)) throw new Error(`Client UI declaration source map reference is not safe to remove: ${mapPath}`, { cause });
  await writeFile(outputPath, output.replace(/\r?\n?\/\/# sourceMappingURL=[^\r\n]+\s*$/, "\n"), "utf8");
  await rm(mapPath);
}

async function collectFiles(root, matches) {
  const result = [];
  for (const entry of await readdir(root, { withFileTypes: true })) {
    const path = join(root, entry.name);
    if (entry.isDirectory()) result.push(...await collectFiles(path, matches));
    else if (entry.isFile() && matches(entry.name)) result.push(path);
  }
  return result;
}
