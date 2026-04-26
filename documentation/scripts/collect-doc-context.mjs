import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const docsRoot = path.resolve(scriptDir, "..");
const repoRoot = path.resolve(docsRoot, "..");
const generatedDir = path.join(docsRoot, ".generated", "context");
const outputPath = path.join(generatedDir, "upbrr-doc-context.json");

function readRepoFile(relativePath) {
  return readFileSync(path.join(repoRoot, relativePath), "utf8");
}

function walkFiles(root, predicate = () => true) {
  const results = [];
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    const fullPath = path.join(root, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "node_modules" || entry.name === ".generated" || entry.name === "build") {
        continue;
      }
      results.push(...walkFiles(fullPath, predicate));
      continue;
    }
    if (predicate(fullPath)) {
      results.push(fullPath);
    }
  }
  return results;
}

function git(args) {
  try {
    return execFileSync("git", args, { cwd: repoRoot, encoding: "utf8" }).trim();
  } catch {
    return "";
  }
}

function collectCLIFlags() {
  const source = readRepoFile("cmd/upbrr/cli_options.go");
  const flags = [];
  const flagPattern =
    /fs\.(?<kind>BoolVar|StringVar|IntVar|DurationVar)\([^,]+,\s*"(?<name>[^"]+)",\s*(?<defaultValue>[^,]+),\s*"(?<help>[^"]*)"\)/g;

  for (const match of source.matchAll(flagPattern)) {
    flags.push({
      name: match.groups.name,
      kind: match.groups.kind.replace("Var", "").toLowerCase(),
      defaultValue: match.groups.defaultValue.trim(),
      help: match.groups.help,
    });
  }

  return flags.sort((left, right) => left.name.localeCompare(right.name));
}

function collectDocsPages() {
  return walkFiles(path.join(docsRoot, "docs"), (file) => /\.(md|mdx)$/.test(file)).map((file) => {
    const markdown = readFileSync(file, "utf8");
    const headings = [...markdown.matchAll(/^#{1,3}\s+(.+)$/gm)].map((match) => match[1].trim());
    const titleMatch = markdown.match(/^title:\s*(.+)$/m);
    return {
      path: path.relative(docsRoot, file).replaceAll("\\", "/"),
      title: titleMatch?.[1]?.trim() || headings[0] || path.basename(file),
      headings,
    };
  });
}

function collectConfigSections() {
  const config = readRepoFile("internal/config/defaults/example.yaml");
  return [...config.matchAll(/^([a-zA-Z0-9_]+):\s*$/gm)].map((match) => match[1]);
}

function excerpt(value, maxLength = 12000) {
  if (value.length <= maxLength) {
    return value;
  }
  return `${value.slice(0, maxLength)}\n\n[truncated]`;
}

function collectReferenceText() {
  const agents = readRepoFile("AGENTS.md");
  const safetyMatch = agents.match(/## Unattended Safety[\s\S]*?(?=\n## |\n# |$)/);

  return {
    readme: excerpt(readRepoFile("README.md")),
    cliOptions: excerpt(readRepoFile("cmd/upbrr/cli_options.go"), 18000),
    unattendedSafety: safetyMatch ? safetyMatch[0].trim() : "",
    existingCliDocs: excerpt(readFileSync(path.join(docsRoot, "docs", "usage", "cli.md"), "utf8")),
    existingAutomationDocs: excerpt(
      readFileSync(path.join(docsRoot, "docs", "maintenance", "automation.md"), "utf8"),
    ),
  };
}

function collectSourceInventory() {
  const groups = [
    "cmd/upbrr",
    "internal/core",
    "internal/config",
    "internal/trackers",
    "internal/webserver",
    "internal/guiapp",
    "pkg/api",
  ];

  return groups.map((group) => {
    const absolute = path.join(repoRoot, group);
    const files = statSync(absolute).isDirectory()
      ? walkFiles(absolute, (file) => file.endsWith(".go"))
      : [];
    return {
      path: group,
      goFiles: files.length,
    };
  });
}

const context = {
  generatedAt: new Date().toISOString(),
  git: {
    head: git(["rev-parse", "HEAD"]),
    branch: git(["branch", "--show-current"]),
  },
  repository: {
    name: "upbrr",
    url: "https://github.com/autobrr/upbrr",
    docsUrl: "https://upbrr.com/docs",
  },
  cliFlags: collectCLIFlags(),
  configSections: collectConfigSections(),
  docsPages: collectDocsPages(),
  references: collectReferenceText(),
  sourceInventory: collectSourceInventory(),
};

mkdirSync(generatedDir, { recursive: true });
writeFileSync(outputPath, `${JSON.stringify(context, null, 2)}\n`);
console.log(`Wrote ${path.relative(repoRoot, outputPath)}`);
