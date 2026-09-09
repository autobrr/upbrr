// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";
import type { MetadataPreview } from "../types";
import { emptyExternalIdentity } from "../utils/canonicalIdentity";
import { correctionValuesFor, initialSessionState, sessionReducer } from "./reducer";

const preview = (sourcePath: string, generation: number): MetadataPreview => ({
  SourcePath: sourcePath,
  TrackerName: "",
  ReleaseName: "Example.Release.2026.1080p-GRP",
  ReleaseNameOverrides: {},
  Release: { SourcePath: sourcePath, Generation: generation },
  Identity: { ...emptyExternalIdentity(sourcePath), Generation: generation },
  Display: { ReleaseName: "Example.Release.2026.1080p-GRP", Providers: [] },
  Bluray: null,
  Diagnostics: [],
  TrackerData: [],
});

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

  it("keeps automatic selections for discs omitted by a manual edit", () => {
    const suggestedSelections = [
      { DiscID: "disc-one", Index: 0, TimestampSeconds: 10, Frame: 240, Source: "auto" },
      { DiscID: "disc-two", Index: 1, TimestampSeconds: 20, Frame: 600, Source: "auto" },
    ];
    let state = initialSessionState();
    state = sessionReducer(state, {
      type: "screenshots_loaded",
      sessionRevision: 0,
      revision: 0,
      reseedDrafts: true,
      plan: {
        SourcePath: "C:\\media\\Example Collection",
        DiscType: "BDMV",
        DurationSeconds: 120,
        FrameRate: 24,
        SuggestedSelections: suggestedSelections,
        ExistingScreenshots: [],
        ExistingTrackerScreenshots: [],
        FinalSelections: [],
        TrackerImageLinks: [],
        PreviewImages: [],
        MetadataTimestamp: "2026-08-18T00:00:00Z",
        RequiresManualFrames: false,
      },
    });
    state = sessionReducer(state, {
      type: "screenshot_selection_changed",
      index: 0,
      value: { TimestampSeconds: 30, Frame: 720 },
    });

    expect(state.screenshots.selections).toEqual([
      { ...suggestedSelections[0], TimestampSeconds: 30, Frame: 720 },
      suggestedSelections[1],
    ]);
  });

  it("tracks only changed correction values and keeps unrelated confirmations", () => {
    let state = initialSessionState();
    state = sessionReducer(state, {
      type: "source_selected",
      sourcePath: "C:\\media\\Example.mkv",
    });
    state = sessionReducer(state, {
      type: "metadata_changed",
      value: { Title: "Saved title" },
    });
    state = sessionReducer(state, {
      type: "correction_confirmed",
      field: { field: "metadata.title" },
    });
    state = sessionReducer(state, {
      type: "metadata_changed",
      value: { Title: "Saved title", Genres: ["Drama", "Mystery"] },
    });

    expect(state.correctionConfirmFields).toEqual([{ field: "metadata.title" }]);
    expect(correctionValuesFor(state.preparationIntent, state.correctionValueFields)).toEqual({
      Identity: {},
      ReleaseName: {},
      Metadata: { Genres: ["Drama", "Mystery"] },
    });

    state = sessionReducer(state, {
      type: "correction_reset",
      field: { field: "metadata.genres" },
    });
    expect(state.preparationIntent.metadata).toEqual({ Title: "Saved title" });
    expect(state.correctionResetFields).toEqual([{ field: "metadata.genres" }]);
    expect(state.correctionConfirmFields).toEqual([{ field: "metadata.title" }]);
    expect(state.correctionValueFields).toEqual([]);
  });

  it("preserves a newer edit when an older preparation response succeeds", () => {
    const sourcePath = "C:\\media\\Example.mkv";
    let state = initialSessionState();
    state = sessionReducer(state, { type: "source_selected", sourcePath });
    state = sessionReducer(state, { type: "trackers_chosen", trackers: ["AITHER"] });
    state = sessionReducer(state, {
      type: "metadata_changed",
      value: { Title: "Submitted title" },
    });
    const submittedRevision = state.inputEditRevision;
    state = sessionReducer(state, {
      type: "preparation_started",
      sourcePath,
      commandRevision: 2,
      inputEditRevision: submittedRevision,
      correlationID: "prepare-2",
      intent: state.preparationIntent,
    });
    state = sessionReducer(state, {
      type: "metadata_changed",
      value: { Title: "Newer title" },
    });
    state = sessionReducer(state, {
      type: "preparation_succeeded",
      sourcePath,
      commandRevision: 2,
      correlationID: "prepare-2",
      preview: preview(sourcePath, 2),
      intent: {
        ...state.preparationIntent,
        metadata: { Title: "Submitted title" },
      },
    });

    expect(state.preparationIntent.metadata).toEqual({ Title: "Newer title" });
    expect(state.preparationDirty).toBe(true);
    expect(state.correctionDirty).toBe(true);
    expect(state.selectedTrackers).toEqual(["AITHER"]);
    expect(state.correctionValueFields).toEqual([{ field: "metadata.title" }]);
  });

  it("clears accepted tracker answers while retaining deselected drafts", () => {
    const sourcePath = "C:\\media\\Example.mkv";
    let state = initialSessionState();
    state = sessionReducer(state, { type: "source_selected", sourcePath });
    state = sessionReducer(state, { type: "trackers_chosen", trackers: ["AITHER"] });
    for (const tracker of ["AITHER", "PTP"]) {
      state = sessionReducer(state, {
        type: "tracker_input_answered",
        tracker,
        key: "choice",
        value: "yes",
      });
    }
    state = sessionReducer(state, {
      type: "preparation_started",
      sourcePath,
      commandRevision: 1,
      inputEditRevision: state.inputEditRevision,
      correlationID: "prepare-1",
      intent: state.preparationIntent,
    });
    state = sessionReducer(state, {
      type: "preparation_succeeded",
      sourcePath,
      commandRevision: 1,
      correlationID: "prepare-1",
      preview: preview(sourcePath, 1),
      intent: state.preparationIntent,
      selectedTrackers: ["AITHER"],
      trackerInputsAccepted: true,
    });
    expect(state.trackerInputAnswers).toEqual({ PTP: { choice: "yes" } });
  });

  it("accepts backend tracker selection only when no newer selection edit exists", () => {
    const sourcePath = "C:\\media\\Example.mkv";
    let state = initialSessionState();
    state = sessionReducer(state, { type: "source_selected", sourcePath });
    state = sessionReducer(state, { type: "trackers_chosen", trackers: ["AITHER"] });
    state = sessionReducer(state, {
      type: "preparation_started",
      sourcePath,
      commandRevision: 1,
      inputEditRevision: state.inputEditRevision,
      correlationID: "prepare-1",
      intent: state.preparationIntent,
    });
    state = sessionReducer(state, {
      type: "preparation_succeeded",
      sourcePath,
      commandRevision: 1,
      correlationID: "prepare-1",
      preview: preview(sourcePath, 1),
      intent: state.preparationIntent,
    });
    expect(state.selectedTrackers).toEqual(["AITHER"]);

    state = sessionReducer(state, {
      type: "preparation_started",
      sourcePath,
      commandRevision: 2,
      inputEditRevision: state.inputEditRevision,
      correlationID: "prepare-2",
      intent: state.preparationIntent,
    });
    state = sessionReducer(state, {
      type: "preparation_succeeded",
      sourcePath,
      commandRevision: 2,
      correlationID: "prepare-2",
      preview: preview(sourcePath, 2),
      intent: state.preparationIntent,
      selectedTrackers: ["BLU"],
    });
    expect(state.selectedTrackers).toEqual(["BLU"]);
  });
});
