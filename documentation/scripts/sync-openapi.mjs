import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const docsDir = path.resolve(scriptDir, "..");
const repoRoot = path.resolve(docsDir, "..");
const sourcePath = path.join(repoRoot, "internal", "webserver", "openapi.yaml");
const targetPath = path.join(docsDir, "static", "openapi.yaml");

const spec = await readFile(sourcePath);

let current = null;
try {
  current = await readFile(targetPath);
} catch (error) {
  if (error.code !== "ENOENT") {
    throw error;
  }
}

if (!current || !current.equals(spec)) {
  await mkdir(path.dirname(targetPath), { recursive: true });
  await writeFile(targetPath, spec);
}

console.log(
  `Synced ${path.relative(repoRoot, targetPath)} from ${path.relative(repoRoot, sourcePath)}`,
);
