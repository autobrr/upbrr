import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import Ajv from "ajv";
import addFormats from "ajv-formats";
import {
  contextPath,
  docsRoot,
  formatAjvErrors,
  readJsonFile,
  repoRoot,
  scriptDir,
} from "./doc-tooling.mjs";

const contextSchemaPath = path.join(
  docsRoot,
  "schemas",
  "doc-context.schema.json",
);

execFileSync("node", [path.join(scriptDir, "collect-doc-context.mjs")], {
  cwd: docsRoot,
  stdio: "inherit",
});

const context = readJsonFile(contextPath);
const ajv = new Ajv({ allErrors: true, strict: false });
addFormats(ajv);
const validateContext = ajv.compile(readJsonFile(contextSchemaPath));

if (!validateContext(context)) {
  console.error(
    `Docs context failed schema validation: ${formatAjvErrors(validateContext.errors)}`,
  );
  process.exit(1);
}

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
  return results.sort((left, right) => left.localeCompare(right));
}

function docsText() {
  return walkMarkdown(path.join(docsRoot, "docs"))
    .map((file) => readFileSync(file, "utf8").toLowerCase())
    .join("\n");
}

function collectDuplicateTitles(pages) {
  const titles = new Map();
  const duplicates = [];
  for (const page of pages) {
    const key = page.title.toLowerCase();
    if (titles.has(key)) {
      duplicates.push(`${page.title}: ${titles.get(key)} and ${page.path}`);
    }
    titles.set(key, page.path);
  }
  return duplicates;
}

function collectMalformedPages(pages) {
  const malformed = [];
  for (const page of pages) {
    if (page.frontMatter.__error) {
      malformed.push(`${page.path}: ${page.frontMatter.__error}`);
    }
    if (
      typeof page.frontMatter.title !== "string" ||
      page.frontMatter.title.trim() === ""
    ) {
      malformed.push(`${page.path}: missing non-empty front matter title`);
    }
    if (
      "sidebar_position" in page.frontMatter &&
      (typeof page.frontMatter.sidebar_position !== "number" ||
        !Number.isFinite(page.frontMatter.sidebar_position))
    ) {
      malformed.push(`${page.path}: sidebar_position must be a number`);
    }
  }
  return malformed;
}

function missingMentions(items, render) {
  const text = docsText();
  return items
    .filter((item) => !text.includes(render(item).toLowerCase()))
    .map(render);
}

function reportList(title, items, limit = 25) {
  if (items.length === 0) {
    return;
  }

  console.log(`${title}: ${items.slice(0, limit).join(", ")}`);
  if (items.length > limit) {
    console.log(`...and ${items.length - limit} more`);
  }
}

const duplicateTitles = collectDuplicateTitles(context.docsPages);
const malformedPages = collectMalformedPages(context.docsPages);

if (duplicateTitles.length > 0) {
  console.error("Duplicate documentation page titles:");
  for (const duplicate of duplicateTitles) {
    console.error(`  ${duplicate}`);
  }
  process.exit(1);
}

if (malformedPages.length > 0) {
  console.error("Malformed documentation metadata:");
  for (const page of malformedPages) {
    console.error(`  ${page}`);
  }
  process.exit(1);
}

const surfaces = context.documentableSurfaces;
const missingFlagMentions = missingMentions(
  surfaces.cliFlags.filter((flag) => flag.name.length > 2),
  (flag) => `--${flag.name}`,
);
const missingConfigSectionMentions = missingMentions(
  surfaces.configSections,
  (section) => section.name,
);
const missingTrackerMentions = missingMentions(surfaces.trackers, (tracker) =>
  tracker.name.toUpperCase(),
);

console.log(
  `Docs context: ${surfaces.cliFlags.length} CLI flags, ` +
    `${surfaces.configKeys.length} config keys, ${surfaces.trackers.length} tracker groups, ` +
    `${context.docsPages.length} docs pages`,
);

reportList("CLI flags not mentioned in docs yet", missingFlagMentions);
reportList(
  "Config sections not mentioned in docs yet",
  missingConfigSectionMentions,
);
reportList("Tracker groups not mentioned in docs yet", missingTrackerMentions);

const strictFailures = [
  ...missingFlagMentions.map((item) => `missing CLI flag mention: ${item}`),
  ...missingConfigSectionMentions.map(
    (item) => `missing config section mention: ${item}`,
  ),
  ...missingTrackerMentions.map((item) => `missing tracker mention: ${item}`),
];

if (process.env.DOCS_STRICT === "1" && strictFailures.length > 0) {
  console.error(
    "DOCS_STRICT=1 requires documentable surfaces to be mentioned in docs:",
  );
  for (const failure of strictFailures.slice(0, 100)) {
    console.error(`  ${failure}`);
  }
  if (strictFailures.length > 100) {
    console.error(`  ...and ${strictFailures.length - 100} more`);
  }
  process.exit(1);
}

console.log(
  `Generated context is ignored under ${path.relative(repoRoot, path.dirname(contextPath))}`,
);
