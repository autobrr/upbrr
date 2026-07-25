// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";
import { initialSessionState, sessionReducer } from "./reducer";

describe("sessionReducer upload intent", () => {
  it("keeps duplicate decisions and questionnaire answers independent", () => {
    let state = initialSessionState();
    state = sessionReducer(state, {
      type: "questionnaire_answered",
      tracker: " example ",
      key: "edition",
      value: "Extended",
    });
    state = sessionReducer(state, {
      type: "dupe_ignore_changed",
      tracker: "EXAMPLE",
      ignored: true,
    });
    state = sessionReducer(state, { type: "job_command_started", kind: "upload" });
    state = sessionReducer(state, {
      type: "job_command_failed",
      kind: "upload",
      error: "retryable failure",
    });

    expect(state.ignoredDupesFor).toEqual(["EXAMPLE"]);
    expect(state.questionnaireAnswers).toEqual({ EXAMPLE: { edition: "Extended" } });
    expect(state.uploadError).toBe("retryable failure");
  });
});
