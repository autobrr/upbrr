// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";
import { initialSessionState, sessionReducer } from "./reducer";

describe("sessionReducer rule authorization", () => {
  it("keeps duplicate ignores and exact rule authorizations independent across upload retry state", () => {
    let state = initialSessionState();
    state = sessionReducer(state, {
      type: "rule_authorization_changed",
      tracker: " aither ",
      rule: "container",
      authorized: true,
    });
    state = sessionReducer(state, {
      type: "dupe_ignore_changed",
      tracker: "AITHER",
      ignored: true,
    });
    state = sessionReducer(state, { type: "job_command_started", kind: "upload" });
    state = sessionReducer(state, {
      type: "job_command_failed",
      kind: "upload",
      error: "retryable failure",
    });

    expect(state.ignoredDupesFor).toEqual(["AITHER"]);
    expect(state.authorizedRulesByTracker).toEqual({ AITHER: ["container"] });

    state = sessionReducer(state, {
      type: "rule_authorization_changed",
      tracker: "AITHER",
      rule: "container",
      authorized: false,
    });
    expect(state.ignoredDupesFor).toEqual(["AITHER"]);
    expect(state.authorizedRulesByTracker).toEqual({});
  });
});
