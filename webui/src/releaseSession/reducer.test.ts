// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";
import type { MetadataPreview } from "../types";
import type { ReleaseWorkflowCurrent } from "../api/generated/release-workflow";
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

const current = (workflowID: string, revision: number): ReleaseWorkflowCurrent =>
  ({
    workflow: { id: workflowID, revision },
    continuation: { lifecycle: "ready", disposition: "none", refs: {} },
  }) as unknown as ReleaseWorkflowCurrent;

describe("sessionReducer upload intent", () => {
  it("retains description drafts for unchanged normalized tracker receipts", () => {
    let state = sessionReducer(initialSessionState(), {
      type: "trackers_received",
      trackers: ["BLU", "AITHER"],
    });
    state = sessionReducer(state, {
      type: "description_edited",
      groupKey: "unit3d",
      raw: "unsaved",
    });
    state = {
      ...state,
      descriptions: { ...state.descriptions, renderedByGroup: { unit3d: "<p>unsaved</p>" } },
    };
    const refreshed = sessionReducer(state, {
      type: "trackers_received",
      trackers: [" aither ", "BLU", "AITHER"],
    });
    expect(refreshed.descriptions).toBe(state.descriptions);
    const changed = sessionReducer(state, { type: "trackers_received", trackers: ["AITHER"] });
    expect(changed.descriptions.rawByGroup).toEqual({});
    expect(changed.descriptions.dirtyGroups).toEqual([]);
    expect(changed.descriptions.renderedByGroup).toEqual({});
  });

  it.each(["tracker", "generation", "input", "workflow", "unfinished workflow"])(
    "clears description drafts for a real %s change",
    (change) => {
      const sourcePath = "C:\\media\\Example.Release.2026.mkv";
      const initial = initialSessionState();
      const snapshot = {
        state: "active" as const,
        revision: 1,
        inputId: "input-one",
        sourceVersion: "source-one",
        current: current("workflow-one", 3),
      };
      const action = {
        type: "active_input_applied" as const,
        snapshot,
        status: "ready" as const,
        preview: preview(sourcePath, 1),
        intent: initial.preparationIntent,
        capturedInputEditRevision: 0,
        selectedTrackers: ["AITHER"],
      };
      let state = sessionReducer(initial, action);
      state = sessionReducer(state, {
        type: "description_edited",
        groupKey: "unit3d",
        raw: "unsaved",
      });
      const refreshed = sessionReducer(state, action);
      expect(refreshed.descriptions.rawByGroup.unit3d).toBe("unsaved");
      const nextSnapshot = change.includes("workflow")
        ? { ...snapshot, revision: 2, current: current("workflow-two", 1) }
        : snapshot;
      const changed = sessionReducer(state, {
        ...action,
        snapshot: nextSnapshot,
        ...(change === "unfinished workflow" ? { preview: null, status: "running" as const } : {}),
        ...(change === "tracker" ? { selectedTrackers: ["BLU"] } : {}),
        ...(change === "generation" ? { preview: preview(sourcePath, 2) } : {}),
        ...(change === "input"
          ? {
              snapshot: {
                ...snapshot,
                revision: 2,
                inputId: "input-two",
                sourceVersion: "source-two",
              },
            }
          : {}),
      });
      expect(changed.descriptions.rawByGroup).toEqual({});
      expect(changed.descriptions.dirtyGroups).toEqual([]);
      if (change === "unfinished workflow") {
        const completed = sessionReducer(changed, { ...action, snapshot: nextSnapshot });
        expect(completed.descriptions.rawByGroup).toEqual({});
      }
    },
  );

  it("invalidates at workflow generation publication, then preserves newer edits on reload", () => {
    const sourcePath = "C:\\media\\Example.Release.2026.mkv";
    const workflow = (generation: number): ReleaseWorkflowCurrent =>
      ({
        ...current("workflow-one", generation + 2),
        release: { release: { Source: { SourcePath: sourcePath }, Generation: generation } },
      }) as ReleaseWorkflowCurrent;
    const initial = initialSessionState();
    const action = {
      type: "active_input_applied" as const,
      snapshot: {
        state: "active" as const,
        revision: 1,
        inputId: "input-one",
        sourceVersion: "source-one",
        current: workflow(1),
      },
      status: "ready" as const,
      preview: preview(sourcePath, 1),
      intent: initial.preparationIntent,
      capturedInputEditRevision: 0,
      selectedTrackers: ["AITHER"],
    };
    let state = sessionReducer(initial, action);
    state = sessionReducer(state, {
      type: "description_edited",
      groupKey: "unit3d",
      raw: "old generation draft",
    });
    const priorSessionRevision = state.sessionRevision;
    const screenshotRevision = state.screenshots.revision + 1;
    state = sessionReducer(state, {
      type: "workflow_started",
      facet: "screenshots",
      sessionRevision: priorSessionRevision,
      revision: screenshotRevision,
    });
    state = sessionReducer(state, {
      type: "workflow_current_published",
      status: "ready",
      current: workflow(2),
    });
    expect(state.descriptions.rawByGroup).toEqual({});
    expect(state.sessionRevision).toBe(priorSessionRevision + 1);
    state = sessionReducer(state, {
      type: "screenshot_previewed",
      sessionRevision: priorSessionRevision,
      revision: screenshotRevision,
      image: "/stale-preview.png",
    });
    expect(state.screenshots.previewImage).toBe("");
    state = sessionReducer(state, {
      type: "description_edited",
      groupKey: "unit3d",
      raw: "new generation draft",
    });
    state = sessionReducer(state, {
      ...action,
      snapshot: { ...action.snapshot, current: workflow(2) },
      preview: preview(sourcePath, 2),
    });
    expect(state.descriptions.rawByGroup.unit3d).toBe("new generation draft");
    expect(state.sessionRevision).toBe(priorSessionRevision + 1);
  });

  it("keeps source-only edits independent of an authoritative tracker refresh", () => {
    const sourcePath = "C:\\media\\Example.Release.2026.mkv";
    const initial = initialSessionState();
    const action = {
      type: "active_input_applied" as const,
      snapshot: {
        state: "active" as const,
        revision: 1,
        inputId: "input-one",
        sourceVersion: "source-one",
        current: current("workflow-one", 3),
      },
      status: "ready" as const,
      preview: preview(sourcePath, 1),
      intent: initial.preparationIntent,
      capturedInputEditRevision: 0,
      selectedTrackers: ["AITHER"],
    };
    let state = sessionReducer(initial, action);
    state = sessionReducer(state, {
      type: "description_edited",
      groupKey: "unit3d",
      raw: "unsaved",
    });
    const capturedInputEditRevision = state.inputEditRevision;
    state = sessionReducer(state, {
      type: "draft_changed",
      value: "C:\\media\\Next.Release.2026.mkv",
    });
    state = sessionReducer(state, {
      ...action,
      capturedInputEditRevision,
      selectedTrackers: ["BLU"],
    });
    expect(state.sourceDraft).toBe("C:\\media\\Next.Release.2026.mkv");
    expect(state.selectedTrackers).toEqual(["BLU"]);
    expect(state.preparationDirty).toBe(false);
    expect(state.descriptions.rawByGroup).toEqual({});
  });

  it("retains an explicit source ID clear until preparation resolves the source again", () => {
    let state = initialSessionState();
    for (const value of ["123", ""]) {
      state = sessionReducer(state, {
        type: "tracker_source_id_changed",
        tracker: " aither ",
        value,
      });
      expect(state.preparationIntent.trackerSourceIDs).toEqual({ AITHER: value });
    }
  });
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

  it("clears the previous workflow when selecting a different source", () => {
    const previousSource = "C:\\media\\Previous.Release.2026-GRP.mkv";
    const nextSource = "C:\\media\\Next.Release.2026-GRP.mkv";
    const initial = initialSessionState();
    const previousCurrent = current("workflow-previous", 3);
    const active = sessionReducer(initial, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 1,
        inputId: "input-previous",
        sourceVersion: "source-previous-v1",
        current: {
          ...previousCurrent,
          workflow: { ...previousCurrent.workflow, status: "completed" },
        },
      },
      status: "ready",
      preview: preview(previousSource, 1),
      intent: initial.preparationIntent,
      capturedInputEditRevision: 0,
    });
    const failed = sessionReducer(active, {
      type: "workflow_view_failed",
      error: "Previous workflow failed.",
      failure: {
        Code: "invalid_source",
        Operation: "preparation",
        Message: "Previous workflow failed.",
        Recovery: "edit_input",
      },
    });

    expect(
      sessionReducer(failed, { type: "source_selected", sourcePath: previousSource }).workflowView,
    ).toBe(failed.workflowView);

    const switched = sessionReducer(failed, { type: "source_selected", sourcePath: nextSource });
    expect(switched.workflowView).toEqual({
      status: "idle",
      current: null,
      error: "",
      failure: null,
    });
    expect(switched.preview).toBeNull();
    expect(switched.release).toBeNull();
    expect(switched.screenshots.staleReason).toBe("Source changed.");
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

describe("sessionReducer active input snapshots", () => {
  it("accepts only correlated source verification progress and clears it on cancellation", () => {
    const sourcePath = "C:\\media\\Verify.Release.2026.mkv";
    let state = sessionReducer(initialSessionState(), {
      type: "preparation_started",
      sourcePath,
      commandRevision: 1,
      inputEditRevision: 0,
      correlationID: "prepare-current",
      intent: initialSessionState().preparationIntent,
    });
    const running = state;
    state = sessionReducer(state, {
      type: "source_verification_progressed",
      update: {
        correlationID: "prepare-stale",
        completedBytes: 50,
        totalBytes: 100,
        message: "Stale",
        status: "running",
      },
    });
    expect(state).toBe(running);

    state = sessionReducer(state, {
      type: "source_verification_progressed",
      update: {
        correlationID: "prepare-current",
        completedBytes: 50,
        totalBytes: 100,
        message: "Verifying source content.",
        status: "running",
      },
    });
    expect(state.sourceVerification).toMatchObject({ completedBytes: 50, totalBytes: 100 });

    state = sessionReducer(state, {
      type: "source_verification_progressed",
      update: {
        correlationID: "prepare-current",
        completedBytes: 0,
        totalBytes: 0,
        message: "Stage complete.",
        status: "completed",
      },
    });
    expect(state.sourceVerification).toMatchObject({
      completedBytes: 50,
      totalBytes: 100,
      status: "completed",
    });

    state = sessionReducer(state, {
      type: "preparation_cancelled",
      correlationID: "prepare-current",
    });
    expect(state.preparation.status).toBe("cancelled");
    expect(state.sourceVerification).toBeNull();
  });

  it("accepts the in-flight preparation failure after an empty-slot resync", () => {
    const sourcePath = "Z:\\missing\\Invalid.Release.2026.mkv";
    let state = sessionReducer(initialSessionState(), {
      type: "draft_changed",
      value: sourcePath,
    });
    state = sessionReducer(state, {
      type: "preparation_started",
      sourcePath,
      commandRevision: 1,
      inputEditRevision: state.inputEditRevision,
      correlationID: "prepare-invalid",
      intent: state.preparationIntent,
    });
    state = sessionReducer(state, {
      type: "source_verification_progressed",
      update: {
        correlationID: "prepare-invalid",
        completedBytes: 0,
        totalBytes: 0,
        message: "Verifying source content.",
        status: "running",
      },
    });

    state = sessionReducer(state, {
      type: "active_input_applied",
      snapshot: { state: "empty", revision: 2 },
      status: "ready",
      preview: null,
      intent: null,
      capturedInputEditRevision: state.inputEditRevision,
    });
    expect(state.selectedSource).toBe("");
    expect(state.preparation.status).toBe("running");
    expect(state.sourceVerification?.correlationID).toBe("prepare-invalid");

    state = sessionReducer(state, {
      type: "preparation_failed",
      sourcePath,
      commandRevision: 1,
      correlationID: "prepare-invalid",
      error: "The source path is unavailable. Recovery: edit input.",
      failure: {
        Code: "invalid_source",
        Operation: "preparation",
        Message: "The source path is unavailable.",
        Recovery: "edit_input",
      },
    });
    expect(state.preparation).toMatchObject({
      status: "error",
      error: "The source path is unavailable. Recovery: edit input.",
    });
    expect(state.sourceVerification).toBeNull();

    state = sessionReducer(state, {
      type: "active_input_applied",
      snapshot: { state: "empty", revision: 3 },
      status: "ready",
      preview: null,
      intent: null,
      capturedInputEditRevision: state.inputEditRevision,
    });
    expect(state.preparation).toMatchObject({
      status: "error",
      error: "The source path is unavailable. Recovery: edit input.",
    });
    expect(state.sourceDraft).toBe(sourcePath);
  });

  it("keeps a legacy recovery workflow while clearing unverified release authority", () => {
    const initial = initialSessionState();
    const sourcePath = "C:\\media\\Prior.Release.2026.mkv";
    const active = sessionReducer(initial, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 1,
        inputId: "input-prior",
        sourceVersion: "source-prior-v1",
        current: current("workflow-prior", 2),
      },
      status: "ready",
      preview: preview(sourcePath, 1),
      intent: initial.preparationIntent,
      capturedInputEditRevision: 0,
      selectedTrackers: ["AITHER"],
    });
    const recoveryCurrent = {
      ...current("workflow-recovery", 8),
      workflow: {
        ...current("workflow-recovery", 8).workflow,
        status: "blocked" as const,
        requiredActions: [
          {
            id: "action-reconcile",
            kind: "reconcile_submission",
            status: "pending" as const,
            workflowRevision: 8,
            prompt: "Verify the interrupted effect.",
            options: [{ value: "not_completed", label: "Confirmed not completed" }],
            createdAt: "2026-09-19T00:00:00Z",
          },
        ],
      },
    };
    const recovering = sessionReducer(active, {
      type: "active_input_applied",
      snapshot: {
        state: "recovering",
        revision: 2,
        current: recoveryCurrent,
      },
      status: "ready",
      preview: null,
      intent: null,
      capturedInputEditRevision: active.inputEditRevision,
    });

    expect(recovering.activeInput).toEqual({
      state: "recovering",
      revision: 2,
      inputID: "",
      sourceVersion: "",
      recoveryWorkflowIDs: [],
    });
    expect(recovering.workflowView.current?.workflow.id).toBe("workflow-recovery");
    expect(recovering.preview).toBeNull();
    expect(recovering.release).toBeNull();
    expect(recovering.selectedTrackers).toEqual([]);
  });

  it("applies workflow authority and its compatibility preview atomically", () => {
    const state = initialSessionState();
    const sourcePath = "C:\\media\\Atomic.Release.2026.mkv";
    const next = sessionReducer(state, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 4,
        inputId: "input-atomic",
        sourceVersion: "source-atomic-v1",
        current: current("workflow-atomic", 6),
      },
      status: "ready",
      preview: preview(sourcePath, 2),
      intent: state.preparationIntent,
      capturedInputEditRevision: 0,
      selectedTrackers: ["AITHER"],
    });

    expect(next.activeInput).toEqual({
      state: "active",
      revision: 4,
      inputID: "input-atomic",
      sourceVersion: "source-atomic-v1",
      recoveryWorkflowIDs: [],
    });
    expect(next.workflowView.current?.workflow.id).toBe("workflow-atomic");
    expect(next.preview?.Release).toEqual({ SourcePath: sourcePath, Generation: 2 });
    expect(next.release).toEqual({ SourcePath: sourcePath, Generation: 2 });
  });

  it("preserves a later draft for the same input version and clears it for another version", () => {
    const sourcePath = "C:\\media\\Draft.Release.2026.mkv";
    const initial = initialSessionState();
    let state = sessionReducer(initial, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 1,
        inputId: "input-draft",
        sourceVersion: "source-draft-v1",
        current: current("workflow-draft", 1),
      },
      status: "ready",
      preview: preview(sourcePath, 1),
      intent: initial.preparationIntent,
      capturedInputEditRevision: 0,
    });
    const capturedRevision = state.inputEditRevision;
    state = sessionReducer(state, {
      type: "metadata_changed",
      value: { Title: "Newer unsaved title" },
    });
    state = sessionReducer(state, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 2,
        inputId: "input-draft",
        sourceVersion: "source-draft-v1",
        current: current("workflow-draft", 2),
      },
      status: "ready",
      preview: preview(sourcePath, 2),
      intent: { ...initial.preparationIntent, metadata: { Title: "Server title" } },
      capturedInputEditRevision: capturedRevision,
    });
    expect(state.preparationIntent.metadata).toEqual({ Title: "Newer unsaved title" });
    expect(state.preparationDirty).toBe(true);

    state = sessionReducer(state, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 3,
        inputId: "input-draft",
        sourceVersion: "source-draft-v2",
        current: current("workflow-draft-v2", 1),
      },
      status: "ready",
      preview: preview(sourcePath, 1),
      intent: { ...initial.preparationIntent, metadata: { Title: "New source version" } },
      capturedInputEditRevision: state.inputEditRevision,
    });
    expect(state.preparationIntent.metadata).toEqual({ Title: "New source version" });
    expect(state.preparationDirty).toBe(false);
  });

  it("preserves an unaccepted correction during same-input resync and clears it on source switch", () => {
    const sourcePath = "C:\\media\\Draft.Release.2026.mkv";
    const initial = initialSessionState();
    let state = sessionReducer(initial, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 1,
        inputId: "input-draft",
        sourceVersion: "source-draft-v1",
        current: current("workflow-draft", 1),
      },
      status: "ready",
      preview: preview(sourcePath, 1),
      intent: { ...initial.preparationIntent, identity: { TMDBID: 777 } },
      capturedInputEditRevision: 0,
    });
    state = sessionReducer(state, {
      type: "identity_changed",
      value: { TMDBID: 0 },
    });
    const editRevision = state.inputEditRevision;

    state = sessionReducer(state, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 2,
        inputId: "input-draft",
        sourceVersion: "source-draft-v1",
        current: current("workflow-draft", 2),
      },
      status: "ready",
      preview: preview(sourcePath, 1),
      intent: { ...initial.preparationIntent, identity: { TMDBID: 777 } },
      capturedInputEditRevision: editRevision,
      preserveInputDraft: true,
    });
    expect(state.preparationIntent.identity).toEqual({ TMDBID: 0 });
    expect(state.correctionDirty).toBe(true);
    expect(state.inputEditRevision).toBe(editRevision);

    state = sessionReducer(state, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 3,
        inputId: "input-draft",
        sourceVersion: "source-draft-v2",
        current: current("workflow-draft-v2", 1),
      },
      status: "ready",
      preview: preview(sourcePath, 2),
      intent: { ...initial.preparationIntent, identity: { TMDBID: 888 } },
      capturedInputEditRevision: state.inputEditRevision,
      preserveInputDraft: true,
    });
    expect(state.preparationIntent.identity).toEqual({ TMDBID: 888 });
    expect(state.correctionDirty).toBe(false);
  });

  it("keeps locally chosen trackers during same-input resync and clears them for a new source version", () => {
    const sourcePath = "C:\\media\\Tracker.Choice.2026.mkv";
    const initial = initialSessionState();
    let state = sessionReducer(initial, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 1,
        inputId: "input-trackers",
        sourceVersion: "source-trackers-v1",
        current: current("workflow-trackers", 1),
      },
      status: "ready",
      preview: preview(sourcePath, 1),
      intent: initial.preparationIntent,
      capturedInputEditRevision: 0,
      selectedTrackers: ["HDS"],
    });
    state = sessionReducer(state, {
      type: "trackers_chosen",
      trackers: ["AITHER", "HDS"],
    });
    expect(state.preparationDirty).toBe(true);
    const editRevision = state.inputEditRevision;

    state = sessionReducer(state, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 2,
        inputId: "input-trackers",
        sourceVersion: "source-trackers-v1",
        current: current("workflow-trackers", 2),
      },
      status: "ready",
      preview: preview(sourcePath, 1),
      intent: initial.preparationIntent,
      capturedInputEditRevision: editRevision,
      selectedTrackers: ["HDS"],
      preserveInputDraft: true,
    });
    expect(state.selectedTrackers).toEqual(["AITHER", "HDS"]);
    expect(state.trackerSelectionTouched).toBe(true);

    state = sessionReducer(state, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 3,
        inputId: "input-trackers",
        sourceVersion: "source-trackers-v2",
        current: current("workflow-trackers-v2", 1),
      },
      status: "ready",
      preview: preview(sourcePath, 2),
      intent: initial.preparationIntent,
      capturedInputEditRevision: state.inputEditRevision,
      selectedTrackers: ["HDS"],
      preserveInputDraft: true,
    });
    expect(state.selectedTrackers).toEqual(["HDS"]);
    expect(state.trackerSelectionTouched).toBe(false);
  });

  it("rejects an older active-slot response after a newer source wins", () => {
    const initial = initialSessionState();
    const newer = sessionReducer(initial, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 5,
        inputId: "input-b",
        sourceVersion: "source-b-v1",
        current: current("workflow-b", 2),
      },
      status: "ready",
      preview: preview("C:\\media\\B.mkv", 1),
      intent: initial.preparationIntent,
      capturedInputEditRevision: 0,
    });
    const stale = sessionReducer(newer, {
      type: "active_input_applied",
      snapshot: {
        state: "active",
        revision: 4,
        inputId: "input-a",
        sourceVersion: "source-a-v1",
        current: current("workflow-a", 9),
      },
      status: "ready",
      preview: preview("C:\\media\\A.mkv", 9),
      intent: initial.preparationIntent,
      capturedInputEditRevision: 0,
    });

    expect(stale).toBe(newer);
    expect(stale.selectedSource).toBe("C:\\media\\B.mkv");
    expect(stale.workflowView.current?.workflow.id).toBe("workflow-b");
  });
});

describe("screenshot plan authority", () => {
  it.each(["publication", "active input"])(
    "fences projection-only stale completions from %s and retains media drafts",
    (receipt) => {
      const sourcePath = "C:\\media\\Example.Release.2026.mkv";
      const initial = initialSessionState();
      const workflow = {
        ...current("workflow-one", 4),
        workflow: {
          ...current("workflow-one", 4).workflow,
          trackerProjections: { id: "projections-one", revision: 1 },
        },
        release: { release: { Source: { SourcePath: sourcePath }, Generation: 1 } },
      } as ReleaseWorkflowCurrent;
      const applied = {
        type: "active_input_applied" as const,
        snapshot: {
          state: "active" as const,
          revision: 1,
          inputId: "input-one",
          sourceVersion: "source-one",
          current: workflow,
        },
        status: "ready" as const,
        preview: preview(sourcePath, 1),
        intent: initial.preparationIntent,
        capturedInputEditRevision: 0,
        selectedTrackers: ["AITHER"],
      };
      let state = sessionReducer(initial, applied);
      const plan = {
        SourcePath: sourcePath,
        SavedImagePlanID: "plan-before",
        DiscType: "",
        DurationSeconds: 120,
        FrameRate: 24,
        SuggestedSelections: [{ Index: 0, TimestampSeconds: 10, Frame: 240, Source: "auto" }],
        ExistingScreenshots: [],
        ExistingTrackerScreenshots: [],
        FinalSelections: [],
        TrackerImageLinks: [],
        PreviewImages: [],
        MetadataTimestamp: "2026-07-20T00:00:00Z",
        RequiresManualFrames: false,
      };
      state = sessionReducer(state, {
        type: "screenshots_loaded",
        sessionRevision: state.sessionRevision,
        revision: state.screenshots.revision,
        plan,
        reseedDrafts: true,
      });
      state = sessionReducer(state, {
        type: "screenshot_selection_changed",
        index: 0,
        value: { TimestampSeconds: 42, Frame: 1008 },
      });
      state = sessionReducer(state, {
        type: "screenshot_final_artifacts_changed",
        artifactIDs: ["retained-image"],
      });
      state = sessionReducer(state, {
        type: "screenshot_previewed",
        sessionRevision: state.sessionRevision,
        revision: state.screenshots.revision,
        image: "/retained-preview.png",
      });
      const before = state;
      const receive = (next: ReleaseWorkflowCurrent) =>
        receipt === "publication"
          ? { type: "workflow_current_published" as const, current: next, status: "ready" as const }
          : { ...applied, snapshot: { ...applied.snapshot, current: next } };
      state = sessionReducer(
        state,
        receive({ ...workflow, workflow: { ...workflow.workflow, revision: 5 } }),
      );
      expect(state.screenshots).toBe(before.screenshots);
      const sessionRevision = state.sessionRevision;
      const revision = state.screenshots.revision + 1;
      state = sessionReducer(state, {
        type: "workflow_started",
        facet: "screenshots",
        sessionRevision,
        revision,
      });
      state = sessionReducer(
        state,
        receive({
          ...workflow,
          workflow: {
            ...workflow.workflow,
            revision: 6,
            trackerProjections: { id: "projections-one", revision: 2 },
          },
        }),
      );
      expect(state.sessionRevision).toBe(sessionRevision);
      expect(state.screenshots.revision).toBeGreaterThan(revision);
      expect(state.screenshots.value).toBeNull();
      expect(state.screenshots.planStale).toBe(true);
      expect(state.screenshots.selections).toBe(before.screenshots.selections);
      expect(state.screenshots.finalSelectionArtifactIDs).toBe(
        before.screenshots.finalSelectionArtifactIDs,
      );
      expect(state.screenshots.previewImage).toBe(before.screenshots.previewImage);
      expect(state.menuImages).toBe(before.menuImages);
      expect(state.uploadedImages).toBe(before.uploadedImages);
      const refreshed = state;
      state = sessionReducer(state, {
        type: "screenshots_loaded",
        sessionRevision,
        revision,
        plan,
        reseedDrafts: true,
      });
      expect(state).toBe(refreshed);
    },
  );
});
