// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { appendFileSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const packageBudget = 300;
const testBudget = 30;

function cell(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll("|", "\\|")
    .replaceAll("`", "\\`")
    .replace(/[\r\n]/g, " ");
}

/** Ranks completed packages and tests, excluding replayed cached timings. */
export function summarizeGoTests(log, label) {
  const packages = new Map();
  for (const line of log.split(/\r?\n/).filter((line) => line.trim())) {
    const event = JSON.parse(line);
    if (!event.Package) continue;
    if (!packages.has(event.Package)) {
      packages.set(event.Package, {
        name: event.Package,
        cached: false,
        tests: [],
      });
    }
    const entry = packages.get(event.Package);
    if (
      event.Action === "output" &&
      !event.Test &&
      /^ok\s+.*\(cached\)/.test(event.Output ?? "")
    ) {
      entry.cached = true;
    }
    if (!["pass", "fail", "skip"].includes(event.Action)) continue;
    const result = {
      name: event.Test ?? event.Package,
      status: event.Action,
      seconds: event.Elapsed ?? 0,
    };
    if (event.Test) {
      if (event.Action !== "skip") entry.tests.push(result);
    } else {
      Object.assign(entry, result);
    }
  }
  const completed = [...packages.values()].filter((entry) => entry.status);
  if (!completed.length)
    throw new Error("Go test log contains no completed packages");
  const executed = completed.filter(
    (entry) => !entry.cached && entry.status !== "skip",
  );
  const tests = executed.flatMap((entry) =>
    entry.tests.map((test) => ({
      ...test,
      name: `${entry.name}: ${test.name}`,
    })),
  );
  const slowPackages = executed.filter(
    (entry) => entry.seconds > packageBudget,
  ).length;
  const slowTests = tests.filter((entry) => entry.seconds > testBudget).length;
  const table = (entries, limit = 10) =>
    [
      "| Name | Result | Seconds |",
      "| --- | --- | ---: |",
      ...entries
        .toSorted((a, b) => b.seconds - a.seconds)
        .slice(0, limit)
        .map(
          (entry) =>
            `| ${cell(entry.name)} | ${entry.status} | ${entry.seconds.toFixed(2)} |`,
        ),
      "",
    ].join("\n");
  const markdown = [
    `## Go test timings (${cell(label)})`,
    "",
    `${executed.length} packages executed; ${completed.filter((entry) => entry.cached).length} cached; ` +
      `${completed.filter((entry) => entry.status === "skip").length} skipped; ` +
      `${completed.filter((entry) => entry.status === "fail").length} failed.`,
    "",
    `Warning budgets: ${packageBudget}s per package and ${testBudget}s per test case. ` +
      `${slowPackages} packages and ${slowTests} tests exceeded these budgets.`,
    "",
    ...(completed.some((entry) => entry.status === "fail")
      ? [
          "### Failures",
          "",
          "The complete output is in the uploaded go-test-timings artifact.",
          "",
          table(
            [...completed, ...tests].filter((entry) => entry.status === "fail"),
            Infinity,
          ),
        ]
      : []),
    "### Slowest executed packages",
    "",
    table(executed),
    "### Slowest executed tests",
    "",
    "Parent and subtest timings overlap and should not be summed. Parallel subtests are ranked individually because their parent may report zero elapsed time. Cached replay timings are excluded.",
    "",
    table(tests),
  ].join("\n");
  return { markdown, slowPackages, slowTests };
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const [input, label = process.platform] = process.argv.slice(2);
  const summary = summarizeGoTests(readFileSync(input, "utf8"), label);
  console.log(summary.markdown);
  if (process.env.GITHUB_STEP_SUMMARY) {
    appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary.markdown + "\n");
  }
  if (summary.slowPackages || summary.slowTests) {
    console.log(
      `::warning title=Go test runtime budget::${summary.slowPackages} packages exceeded ${packageBudget}s; ` +
        `${summary.slowTests} test cases exceeded ${testBudget}s. See the job summary.`,
    );
  }
}
