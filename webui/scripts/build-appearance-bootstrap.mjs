// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import ts from "typescript";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = await readFile(path.join(root, "src", "themes", "appearance.ts"), "utf8");
const result = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2021,
  },
  reportDiagnostics: true,
});
if (result.diagnostics?.length) {
  throw new Error(
    ts.formatDiagnosticsWithColorAndContext(result.diagnostics, {
      getCanonicalFileName: (file) => file,
      getCurrentDirectory: () => root,
      getNewLine: () => "\n",
    }),
  );
}

const script = `/* Generated from src/themes/appearance.ts; do not edit. */\n(() => {\nconst exports = {};\n${result.outputText}\nexports.bootstrapAppearance();\n})();\n`;
await writeFile(path.join(root, "public", "appearance-bootstrap.js"), script);
