import { execFileSync } from "node:child_process";
import {
  mkdirSync,
  readFileSync,
  readdirSync,
  statSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { contextPath, docsRoot, repoRoot } from "./doc-tooling.mjs";

const generatedDir = path.dirname(contextPath);

function toRepoPath(filePath) {
  return path.relative(repoRoot, filePath).replaceAll("\\", "/");
}

function toDocsPath(filePath) {
  return path.relative(docsRoot, filePath).replaceAll("\\", "/");
}

function readRepoFile(relativePath) {
  return readFileSync(path.join(repoRoot, relativePath), "utf8");
}

function walkFiles(root, predicate = () => true) {
  const results = [];
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    const fullPath = path.join(root, entry.name);
    if (entry.isDirectory()) {
      if (
        ["node_modules", ".generated", "build", ".docusaurus"].includes(
          entry.name,
        )
      ) {
        continue;
      }
      results.push(...walkFiles(fullPath, predicate));
      continue;
    }
    if (predicate(fullPath)) {
      results.push(fullPath);
    }
  }
  return results.sort((left, right) => left.localeCompare(right));
}

function git(args) {
  try {
    return execFileSync("git", args, {
      cwd: repoRoot,
      encoding: "utf8",
    }).trim();
  } catch {
    return "";
  }
}

function parseFrontMatter(markdown) {
  if (!markdown.startsWith("---")) {
    return {};
  }

  const match = markdown.match(/^---\r?\n(?<body>[\s\S]*?)\r?\n---\r?\n?/);
  if (!match?.groups?.body) {
    return {
      __error: "front matter starts with --- but has no closing delimiter",
    };
  }

  const frontMatter = {};
  for (const line of match.groups.body.split(/\r?\n/)) {
    const keyValue = line.match(/^(?<key>[A-Za-z0-9_-]+):\s*(?<value>.*)$/);
    if (!keyValue?.groups) {
      continue;
    }
    const rawValue = keyValue.groups.value.trim();
    const numericValue = Number(rawValue);
    frontMatter[keyValue.groups.key] = Number.isFinite(numericValue)
      ? numericValue
      : rawValue.replace(/^["']|["']$/g, "");
  }

  return frontMatter;
}

function collectCLIFlags() {
  const sourceFile = "cmd/upbrr/cli_options.go";
  const source = readRepoFile(sourceFile);
  const flags = [];
  const flagPattern =
    /fs\.(?<kind>BoolVar|StringVar|IntVar|DurationVar)\([^,]+,\s*"(?<name>[^"]+)",\s*(?<defaultValue>[^,]+),\s*"(?<help>[^"]*)"\)/g;

  for (const match of source.matchAll(flagPattern)) {
    flags.push({
      name: match.groups.name,
      kind: match.groups.kind.replace("Var", "").toLowerCase(),
      defaultValue: match.groups.defaultValue.trim(),
      help: match.groups.help,
      sourceFile,
    });
  }

  return flags.sort((left, right) => left.name.localeCompare(right.name));
}

function collectDocsPages() {
  return walkFiles(path.join(docsRoot, "docs"), (file) =>
    /\.(md|mdx)$/.test(file),
  ).map((file) => {
    const markdown = readFileSync(file, "utf8");
    const frontMatter = parseFrontMatter(markdown);
    const headings = [...markdown.matchAll(/^#{1,3}\s+(.+)$/gm)].map((match) =>
      match[1].trim(),
    );
    const title =
      typeof frontMatter.title === "string" && frontMatter.title.trim() !== ""
        ? frontMatter.title.trim()
        : headings[0] || path.basename(file);

    return {
      path: toDocsPath(file),
      title,
      headings,
      frontMatter,
      sourceFile: toRepoPath(file),
    };
  });
}

function collectConfigSurfaces() {
  const sourceFile = "internal/config/defaults/example.yaml";
  const config = readRepoFile(sourceFile);
  const sections = [];
  const keys = [];
  const stack = [];

  for (const line of config.split(/\r?\n/)) {
    if (
      line.trim() === "" ||
      line.trimStart().startsWith("#") ||
      line.trimStart().startsWith("-")
    ) {
      continue;
    }

    const match = line.match(/^(?<indent>\s*)(?<key>[A-Za-z0-9_]+):(?:\s|$)/);
    if (!match?.groups) {
      continue;
    }

    const depth = Math.floor(match.groups.indent.length / 2);
    stack.length = depth;
    stack[depth] = match.groups.key;
    const keyPath = stack.slice(0, depth + 1).join(".");

    if (depth === 0) {
      sections.push({ name: match.groups.key, sourceFile });
    }
    keys.push({ path: keyPath, sourceFile });
  }

  return {
    configSections: sections,
    configKeys: keys,
  };
}

function collectTrackerInventory() {
  const implRoot = path.join(repoRoot, "internal", "trackers", "impl");
  return readdirSync(implRoot, { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => {
      const trackerRoot = path.join(implRoot, entry.name);
      const files = walkFiles(trackerRoot, (file) => file.endsWith(".go")).map(
        toRepoPath,
      );
      return {
        name: entry.name,
        path: toRepoPath(trackerRoot),
        files,
      };
    })
    .sort((left, right) => left.name.localeCompare(right.name));
}

function excerpt(value, maxLength = 12000) {
  if (value.length <= maxLength) {
    return value;
  }
  return `${value.slice(0, maxLength)}\n\n[truncated]`;
}

function reference(name, sourceFile, maxLength = 12000) {
  return {
    name,
    sourceFile,
    text: excerpt(readRepoFile(sourceFile), maxLength),
  };
}

function collectReferenceText() {
  const agents = readRepoFile("AGENTS.md");
  const safetyMatch = agents.match(
    /## Unattended Safety[\s\S]*?(?=\n## |\n# |$)/,
  );

  return [
    reference("README", "README.md"),
    reference("CLI options source", "cmd/upbrr/cli_options.go", 18000),
    {
      name: "Unattended safety policy",
      sourceFile: "AGENTS.md",
      text: safetyMatch ? safetyMatch[0].trim() : "",
    },
    {
      name: "Existing CLI docs",
      sourceFile: "documentation/docs/usage/cli.md",
      text: excerpt(
        readFileSync(path.join(docsRoot, "docs", "usage", "cli.md"), "utf8"),
      ),
    },
    {
      name: "Existing automation docs",
      sourceFile: "documentation/docs/maintenance/automation.md",
      text: excerpt(
        readFileSync(
          path.join(docsRoot, "docs", "maintenance", "automation.md"),
          "utf8",
        ),
      ),
    },
  ];
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
      ? walkFiles(absolute, (file) => file.endsWith(".go")).map(toRepoPath)
      : [];
    return {
      path: group,
      goFiles: files.length,
      files,
    };
  });
}

const configSurfaces = collectConfigSurfaces();

const context = {
  schemaVersion: 1,
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
  documentableSurfaces: {
    cliFlags: collectCLIFlags(),
    configSections: configSurfaces.configSections,
    configKeys: configSurfaces.configKeys,
    trackers: collectTrackerInventory(),
  },
  docsPages: collectDocsPages(),
  references: collectReferenceText(),
  sourceInventory: collectSourceInventory(),
};

mkdirSync(generatedDir, { recursive: true });
writeFileSync(contextPath, `${JSON.stringify(context, null, 2)}\n`);
console.log(`Wrote ${path.relative(repoRoot, contextPath)}`);
