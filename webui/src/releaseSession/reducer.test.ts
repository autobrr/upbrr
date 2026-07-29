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

  it("retains confirmed tracker names until the source changes", () => {
    let state = initialSessionState();
    state = sessionReducer(state, {
      type: "release_name_confirmed",
      tracker: " ar ",
      value: "Example.Release.2026-GRP",
    });
    expect(state.releaseNameOverrides).toEqual({ AR: "Example.Release.2026-GRP" });

    state = sessionReducer(state, {
      type: "source_selected",
      sourcePath: "C:\\media\\Different.Release.2026",
    });
    expect(state.releaseNameOverrides).toEqual({});
  });

  it("selects newly published media without reselecting known cleared candidates", () => {
    const candidate = (artifactID: string, purpose: "final" | "menu") => ({
      image: {
        artifactID,
        index: 0,
        timestampSeconds: 0,
        purpose,
        width: 320,
        height: 180,
        sizeBytes: 128,
      },
      contentURL: `/media/${artifactID}`,
    });
    const screen = candidate("screen-1", "final");
    const menu = candidate("menu-1", "menu");
    let state = initialSessionState();

    state = sessionReducer(state, {
      type: "workflow_upload_candidates_changed",
      candidates: [screen],
    });
    expect(state.uploadedImages.selectedArtifactIDs).toEqual(["screen-1"]);

    state = sessionReducer(state, {
      type: "upload_image_selected",
      artifactID: "screen-1",
      selected: false,
    });
    state = sessionReducer(state, {
      type: "workflow_upload_candidates_changed",
      candidates: [screen, menu],
    });
    expect(state.uploadedImages.selectedArtifactIDs).toEqual(["menu-1"]);
  });
});
