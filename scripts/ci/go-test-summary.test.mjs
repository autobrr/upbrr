// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import assert from "node:assert/strict";
import test from "node:test";
import { summarizeGoTests } from "./go-test-summary.mjs";

function log(...events) {
  return events.map((event) => JSON.stringify(event)).join("\r\n") + "\r\n";
}

test("excludes cached replay and skipped tests from runtime budgets", () => {
  const summary = summarizeGoTests(
    log(
      { Package: "cached", Action: "pass", Test: "TestReplayed", Elapsed: 900 },
      {
        Package: "cached",
        Action: "output",
        Output: "ok  \tcached\t(cached)\n",
      },
      { Package: "cached", Action: "pass", Elapsed: 901 },
      { Package: "empty", Action: "skip" },
      {
        Package: "executed",
        Action: "skip",
        Test: "TestUnavailable",
        Elapsed: 100,
      },
      { Package: "executed", Action: "pass", Test: "TestFast", Elapsed: 1 },
      { Package: "executed", Action: "pass", Elapsed: 2 },
    ),
    "Windows",
  );
  assert.equal(summary.slowPackages, 0);
  assert.equal(summary.slowTests, 0);
  assert.match(
    summary.markdown,
    /1 packages executed; 1 cached; 1 skipped; 0 failed/,
  );
  assert.match(summary.markdown, /executed: TestFast/);
  assert.doesNotMatch(summary.markdown, /TestReplayed|TestUnavailable/);
});

test("keeps failures and ranks parent and subtest durations", () => {
  const summary = summarizeGoTests(
    log(
      { Package: "fast", Action: "pass", Test: "TestFast", Elapsed: 1 },
      { Package: "fast", Action: "pass", Elapsed: 3 },
      {
        Package: "slow",
        Action: "fail",
        Test: "TestParent/child",
        Elapsed: 100,
      },
      { Package: "slow", Action: "fail", Test: "TestParent", Elapsed: 120 },
      { Package: "slow", Action: "fail", Elapsed: 301 },
      { Package: "build", Action: "fail" },
    ),
    "Linux",
  );
  assert.equal(summary.slowPackages, 1);
  assert.equal(summary.slowTests, 2);
  assert.match(summary.markdown, /2 failed/);
  assert.match(summary.markdown, /slow \| fail \| 301\.00/);
  assert.ok(
    summary.markdown.indexOf("slow | fail") <
      summary.markdown.indexOf("fast | pass"),
  );
  assert.match(summary.markdown, /TestParent\/child/);
});

test("reports expensive parallel children when the parent has zero elapsed time", () => {
  const summary = summarizeGoTests(
    log(
      {
        Package: "parallel",
        Action: "pass",
        Test: "TestMatrix/slow",
        Elapsed: 45,
      },
      { Package: "parallel", Action: "pass", Test: "TestMatrix", Elapsed: 0 },
      { Package: "parallel", Action: "pass", Elapsed: 46 },
    ),
    "Linux",
  );
  assert.equal(summary.slowTests, 1);
  assert.match(summary.markdown, /TestMatrix\/slow \| pass \| 45\.00/);
});

test("escapes table and workflow-command line breaks in names", () => {
  const summary = summarizeGoTests(
    log(
      {
        Package: "example|pkg",
        Action: "pass",
        Test: "Test<value>\n::warning::fake",
        Elapsed: 1,
      },
      { Package: "example|pkg", Action: "pass", Elapsed: 2 },
    ),
    "macOS",
  );
  assert.match(summary.markdown, /example\\\|pkg/);
  assert.match(summary.markdown, /Test&lt;value> ::warning::fake/);
  assert.doesNotMatch(summary.markdown, /^::warning::fake/m);
});

test("rejects malformed and incomplete logs", () => {
  assert.throws(() => summarizeGoTests("not JSON", "Linux"), SyntaxError);
  assert.throws(
    () =>
      summarizeGoTests(
        log({ Package: "unfinished", Action: "start" }),
        "Linux",
      ),
    /no completed packages/,
  );
});
