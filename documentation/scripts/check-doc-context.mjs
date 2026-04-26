import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const docsRoot = path.resolve(scriptDir, "..");
const repoRoot = path.resolve(docsRoot, "..");
const contextPath = path.join(docsRoot, ".generated", "context", "upbrr-doc-context.json");

execFileSync("node", [path.join(scriptDir, "collect-doc-context.mjs")], {
  cwd: docsRoot,
  stdio: "inherit",
});

const context = JSON.parse(readFileSync(contextPath, "utf8"));

function walkMarkdown(root) {
  const results = [];
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    const fullPath = path.join(root, entry.name);
    if (entry.isDirectory()) {
      results.push(...walkMarkdown(fullPath));
    } else if (/\.(md|mdx)$/.test(entry.name)) {
      results.push(fullPath);
    }
  }
  return results;
}

const docsText = walkMarkdown(path.join(docsRoot, "docs"))
  .map((file) => readFileSync(file, "utf8").toLowerCase())
  .join("\n");

const titles = new Map();
const duplicateTitles = [];
for (const page of context.docsPages) {
  const key = page.title.toLowerCase();
  if (titles.has(key)) {
    duplicateTitles.push(`${page.title}: ${titles.get(key)} and ${page.path}`);
  }
  titles.set(key, page.path);
}

const missingFlagMentions = context.cliFlags
  .filter((flag) => flag.name.length > 2)
  .filter((flag) => !docsText.includes(`--${flag.name}`.toLowerCase()))
  .map((flag) => `--${flag.name}`);

if (duplicateTitles.length > 0) {
  console.error("Duplicate documentation page titles:");
  for (const duplicate of duplicateTitles) {
    console.error(`  ${duplicate}`);
  }
  process.exit(1);
}

console.log(`Docs context: ${context.cliFlags.length} CLI flags, ${context.docsPages.length} docs pages`);

if (missingFlagMentions.length > 0) {
  console.log(`CLI flags not mentioned in docs yet: ${missingFlagMentions.slice(0, 25).join(", ")}`);
  if (missingFlagMentions.length > 25) {
    console.log(`...and ${missingFlagMentions.length - 25} more`);
  }
}

if (process.env.DOCS_STRICT === "1" && missingFlagMentions.length > 0) {
  console.error("DOCS_STRICT=1 requires every non-alias CLI flag to be mentioned in docs.");
  process.exit(1);
}

console.log(`Generated context is ignored under ${path.relative(repoRoot, path.dirname(contextPath))}`);
