import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";
import assert from "node:assert/strict";
import Ajv from "ajv";
import {
  applyDraft,
  formatAjvErrors,
  readJsonFile,
  renderDraftMarkdown,
  resolveDraftTarget,
} from "./doc-tooling.mjs";

function withTempDocsRoot(run) {
  const root = mkdtempSync(path.join(os.tmpdir(), "upbrr-docs-"));
  try {
    return run(root);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

function draft(overrides = {}) {
  return {
    targetPath: "documentation/docs/generated/example.md",
    title: "upbrr Generated Example",
    frontMatter: {
      sidebar_position: 1,
      title: "upbrr Generated Example",
    },
    markdown: "# upbrr Generated Example\n\nGenerated content.",
    sourceFiles: ["README.md"],
    warnings: [],
    ...overrides,
  };
}

test("resolveDraftTarget accepts docs-relative and repository-relative targets", () => {
  withTempDocsRoot((docsRoot) => {
    assert.equal(
      resolveDraftTarget("docs/generated/example.md", { docsRoot })
        .repoRelativePath,
      "documentation/docs/generated/example.md",
    );
    assert.equal(
      resolveDraftTarget("documentation/docs/generated/example.md", {
        docsRoot,
      }).docsRelativePath,
      "docs/generated/example.md",
    );
  });
});

test("resolveDraftTarget rejects path traversal and non-doc targets", () => {
  withTempDocsRoot((docsRoot) => {
    assert.throws(
      () => resolveDraftTarget("documentation/docs/../README.md", { docsRoot }),
      /escapes/,
    );
    assert.throws(
      () => resolveDraftTarget("README.md", { docsRoot }),
      /inside documentation\/docs/,
    );
    assert.throws(
      () =>
        resolveDraftTarget("documentation/docs/generated/example.txt", {
          docsRoot,
        }),
      /end with/,
    );
  });
});

test("applyDraft writes a validated Markdown page when warnings are absent", () => {
  withTempDocsRoot((docsRoot) => {
    const target = applyDraft(draft(), { docsRoot });
    assert.equal(
      target.repoRelativePath,
      "documentation/docs/generated/example.md",
    );
    assert.equal(
      readFileSync(target.absolutePath, "utf8"),
      renderDraftMarkdown(draft()),
    );
  });
});

test("applyDraft refuses warnings unless explicitly allowed", () => {
  withTempDocsRoot((docsRoot) => {
    assert.throws(
      () =>
        applyDraft(draft({ warnings: ["review source files"] }), { docsRoot }),
      /warnings/,
    );
    const target = applyDraft(draft({ warnings: ["review source files"] }), {
      docsRoot,
      allowWarnings: true,
    });
    assert.match(
      readFileSync(target.absolutePath, "utf8"),
      /Generated content/,
    );
  });
});

test("applyDraft refuses overwrites unless force is set", () => {
  withTempDocsRoot((docsRoot) => {
    const target = applyDraft(draft(), { docsRoot });
    writeFileSync(target.absolutePath, "existing\n");

    assert.throws(() => applyDraft(draft(), { docsRoot }), /overwrite/);
    applyDraft(draft({ markdown: "# upbrr Replacement\n\nReplacement." }), {
      docsRoot,
      force: true,
    });
    assert.match(readFileSync(target.absolutePath, "utf8"), /Replacement/);
  });
});

test("doc draft schema rejects incomplete drafts", () => {
  const schema = readJsonFile(path.resolve("schemas", "doc-draft.schema.json"));
  const ajv = new Ajv({ allErrors: true, strict: false });
  const validate = ajv.compile(schema);

  assert.equal(validate({ title: "Missing fields" }), false);
  assert.match(formatAjvErrors(validate.errors), /required/);
});

test("docs check passes in advisory and strict modes when surfaces are documented", () => {
  const advisory = spawnSync(
    process.execPath,
    ["scripts/check-doc-context.mjs"],
    {
      cwd: path.resolve("."),
      encoding: "utf8",
    },
  );
  assert.equal(advisory.status, 0, advisory.stderr);
  assert.match(advisory.stdout, /Docs context:/);

  const strict = spawnSync(
    process.execPath,
    ["scripts/check-doc-context.mjs"],
    {
      cwd: path.resolve("."),
      encoding: "utf8",
      env: {
        ...process.env,
        DOCS_STRICT: "1",
      },
    },
  );
  assert.equal(strict.status, 0, strict.stderr);
  assert.match(strict.stdout, /Docs context:/);
});
