import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const scriptDir = path.dirname(fileURLToPath(import.meta.url));
export const docsRoot = path.resolve(scriptDir, "..");
export const repoRoot = path.resolve(docsRoot, "..");
export const draftsDir = path.join(docsRoot, ".generated", "drafts");
export const contextPath = path.join(
  docsRoot,
  ".generated",
  "context",
  "upbrr-doc-context.json",
);
export const draftSchemaPath = path.join(
  docsRoot,
  "schemas",
  "doc-draft.schema.json",
);

export function parseArgs(argv = process.argv.slice(2)) {
  const args = new Map();
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (!arg.startsWith("--")) {
      continue;
    }

    const key = arg.slice(2);
    const next = argv[index + 1];
    const value = next?.startsWith("--") ? "true" : next || "true";
    args.set(key, value);
    if (value !== "true") {
      index += 1;
    }
  }
  return args;
}

export function readJsonFile(filePath) {
  return JSON.parse(readFileSync(filePath, "utf8"));
}

export function formatAjvErrors(errors = []) {
  return errors
    .map((error) => {
      const location = error.instancePath || "/";
      return `${location} ${error.message}`;
    })
    .join("; ");
}

export function resolveDraftTarget(targetPath, options = {}) {
  if (typeof targetPath !== "string" || targetPath.trim() === "") {
    throw new Error("draft targetPath must be a non-empty string");
  }

  const root = options.docsRoot || docsRoot;
  const normalized = targetPath
    .trim()
    .replaceAll("\\", "/")
    .replace(/^\.\//, "");
  if (path.isAbsolute(normalized) || normalized.includes("\0")) {
    throw new Error(
      `draft targetPath must be repository-relative, got: ${targetPath}`,
    );
  }

  let docsRelativePath;
  if (normalized.startsWith("documentation/docs/")) {
    docsRelativePath = normalized.slice("documentation/".length);
  } else if (normalized.startsWith("docs/")) {
    docsRelativePath = normalized;
  } else {
    throw new Error(
      `draft targetPath must be inside documentation/docs, got: ${targetPath}`,
    );
  }

  if (!/\.(md|mdx)$/i.test(docsRelativePath)) {
    throw new Error(
      `draft targetPath must end with .md or .mdx, got: ${targetPath}`,
    );
  }

  const absolutePath = path.resolve(root, docsRelativePath);
  const docsDir = path.resolve(root, "docs");
  if (
    absolutePath !== docsDir &&
    !absolutePath.startsWith(`${docsDir}${path.sep}`)
  ) {
    throw new Error(
      `draft targetPath escapes documentation/docs: ${targetPath}`,
    );
  }

  return {
    absolutePath,
    docsRelativePath: docsRelativePath.replaceAll("\\", "/"),
    repoRelativePath: `documentation/${docsRelativePath}`.replaceAll("\\", "/"),
  };
}

export function validateDraftSemantics(draft) {
  const { repoRelativePath } = resolveDraftTarget(draft.targetPath);
  const combined = `${draft.title}\n${JSON.stringify(draft.frontMatter)}\n${draft.markdown}`;
  const bannedPatterns = [
    /\bProject Name\b/i,
    /\byourusername\b/i,
    /\brequirements\.txt\b/i,
    /\bMIT License\b/i,
    /\bupbrr\s+download\b/i,
    /\bupbrr\s+list\b/i,
    /\bcontent_id\b/i,
    /\b--auth-token\b/i,
    /\bdownload(s|ing)? content\b/i,
    /\bdownloading, parsing\b/i,
    /\bDownloadMetadata\b/i,
    /\bParseContent\b/i,
    /\bUpdateLocalCache\b/i,
    /\bservice stubs\b/i,
    /\bimplementation steps\b/i,
    /\binternal\/cli\/upbrr\.go\b/i,
    /\[Describe\b/i,
    /\[Prerequisite\b/i,
    /\bTODO\b/i,
  ];

  for (const pattern of bannedPatterns) {
    if (pattern.test(combined)) {
      throw new Error(
        `generated draft looks like a generic template: matched ${pattern}`,
      );
    }
  }

  if (!/\bupbrr\b/i.test(combined)) {
    throw new Error("generated draft does not mention upbrr");
  }

  return { ...draft, targetPath: repoRelativePath };
}

export function renderDraftMarkdown(draft) {
  const frontMatter = Object.entries(draft.frontMatter || {})
    .map(([key, value]) => `${key}: ${JSON.stringify(value)}`)
    .join("\n");

  return `---\n${frontMatter}\n---\n\n${draft.markdown.trim()}\n`;
}

export function draftSlug(title) {
  return title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "")
    .slice(0, 80);
}

export function writeDraftOutputs(draft, options = {}) {
  const dir = options.draftsDir || draftsDir;
  const stamp = options.stamp || new Date().toISOString().replace(/[:.]/g, "-");
  const slug = draftSlug(draft.title);

  mkdirSync(dir, { recursive: true });

  const jsonPath = path.join(dir, `${stamp}-${slug}.json`);
  const mdPath = path.join(dir, `${stamp}-${slug}.md`);

  writeFileSync(jsonPath, `${JSON.stringify(draft, null, 2)}\n`);
  writeFileSync(mdPath, renderDraftMarkdown(draft));

  return { jsonPath, mdPath };
}

export function applyDraft(draft, options = {}) {
  const allowWarnings = Boolean(options.allowWarnings);
  const force = Boolean(options.force);
  const target = resolveDraftTarget(draft.targetPath, options);

  if (
    !allowWarnings &&
    Array.isArray(draft.warnings) &&
    draft.warnings.length > 0
  ) {
    throw new Error(
      "refusing to apply draft with warnings; pass --allow-warnings to override",
    );
  }

  if (!force && existsSync(target.absolutePath)) {
    throw new Error(
      `refusing to overwrite existing docs page: ${target.repoRelativePath}`,
    );
  }

  mkdirSync(path.dirname(target.absolutePath), { recursive: true });
  writeFileSync(target.absolutePath, renderDraftMarkdown(draft));

  return target;
}
