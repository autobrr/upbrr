// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { HDRAnalysisFacet } from "../../releaseSession/types";
import HDRAnalysisPage from "./index";

afterEach(cleanup);
const first = "hdr_11111111111111111111111111111111";
const second = "hdr_22222222222222222222222222222222";
const facet = (): HDRAnalysisFacet => ({
  view: {
    available: true,
    status: "idle",
    releaseGeneration: 1,
    targets: [
      { id: first, label: "Video 1", selectionPolicy: "unique_hevc", supported: true },
      { id: second, label: "Video 2", selectionPolicy: "unique_hevc", supported: true },
    ],
    result: null,
    pendingRequest: null,
    comparison: null,
    includedArtifactIDs: [],
    phase: "",
    message: "",
    progress: 0,
    mutationBlockedReason: "",
    error: "",
  },
  generate: vi.fn(async () => true),
  setIncludedArtifacts: vi.fn(async () => true),
  selectComparison: vi.fn(async () => true),
  retryDecoded: vi.fn(async () => true),
  retry: vi.fn(async () => true),
  cancel: vi.fn(async () => true),
  artifactURL: (id) => `/hdr/${id}`,
});

it("restores interrupted request controls and resumes without a terminal result", () => {
  const original = facet();
  const value: HDRAnalysisFacet = {
    ...original,
    view: {
      ...original.view,
      pendingRequest: {
        release: { SourcePath: "Synthetic.HDR.mkv", Generation: 1 },
        targetIds: [second],
        peakSource: "max-scl-luminance",
        profileVersion: "hdr-analysis-v2",
        formats: "both",
        decoded: { sceneSync: true, activeArea: false },
      },
    },
  };
  render(<HDRAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  expect(screen.getByLabelText(/Video 2/)).toBeChecked();
  expect(screen.getByLabelText("Peak estimator")).toHaveValue("max-scl-luminance");
  expect(screen.getByLabelText("Metadata formats")).toHaveValue("both");
  expect(screen.getByLabelText("Scene synchronization")).toBeChecked();
  fireEvent.click(screen.getByRole("button", { name: "Resume" }));
  expect(value.retry).toHaveBeenCalledOnce();
  expect(value.generate).not.toHaveBeenCalled();
});

it.each([undefined, true])("restores retained automatic coverage sampled=%s", (sampled) => {
  const original = facet();
  const value: HDRAnalysisFacet = {
    ...original,
    view: {
      ...original.view,
      pendingRequest: {
        release: { SourcePath: "Synthetic.HDR.mkv", Generation: 1 },
        targetIds: [first],
        peakSource: "histogram",
        profileVersion: "hdr-analysis-v2",
        formats: "auto",
        decoded: {
          automatic: true,
          ...(sampled ? { sampled } : {}),
          sceneSync: false,
          activeArea: false,
        },
      },
    },
  };
  render(<HDRAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  const control = screen.getByRole("checkbox", { name: "Measure every video frame (slower)" });
  if (sampled) expect(control).not.toBeChecked();
  else expect(control).toBeChecked();
});

it("shows native formats independently and keeps decoded images unselected", () => {
  const original = facet();
  const format = {
    state: "complete",
    coverage: "complete",
    requested: true,
    advertised: true,
    trackId: "1",
    frames: 3,
    retained: 3,
    observed: 3,
    suppressed: 0,
    missing: 0,
    l1Samples: 3,
    scenes: 1,
    checks: [],
  };
  const image = { width: 3000, height: 1200, mime: "image/png", profile: "hdr-analysis-v2" };
  const value: HDRAnalysisFacet = {
    ...original,
    view: {
      ...original.view,
      includedArtifactIDs: ["hdr", "dv"],
      result: {
        id: "native",
        workflowId: "workflow",
        revision: 2,
        release: { SourcePath: "Synthetic.HDR.mkv", Generation: 1 },
        attemptId: "attempt",
        manifestFingerprint: "manifest",
        targetIds: [first],
        peakSource: "histogram",
        profileVersion: "hdr-analysis-v2",
        formats: "both",
        status: "completed",
        createdAt: "2026-10-12T00:00:00Z",
        completedAt: "2026-10-12T00:01:00Z",
        targets: [
          {
            targetId: first,
            label: "Video 1",
            status: "completed",
            frames: 0,
            scenes: 0,
            formats: [
              { ...format, format: "hdr10plus" },
              { ...format, format: "dolby_vision" },
            ],
            artifacts: [
              { ...image, id: "hdr", kind: "hdr10plus_plot", format: "hdr10plus" },
              { ...image, id: "dv", kind: "dolby_vision_l1_plot", format: "dolby_vision" },
              { ...image, id: "grade", kind: "grade_plot", width: 1200, height: 900 },
              {
                id: "report",
                kind: "structural_report",
                width: 0,
                height: 0,
                mime: "application/json",
                profile: "hdr-analysis-v2",
              },
            ],
          },
        ],
      },
    },
  };
  const setLightboxImage = vi.fn();
  render(
    <HDRAnalysisPage facet={value} setLightboxImage={setLightboxImage} setLightboxAlt={vi.fn()} />,
  );
  expect(screen.getByRole("heading", { name: "HDR10+" })).toBeVisible();
  expect(screen.getByRole("heading", { name: "Dolby Vision" })).toBeVisible();
  expect(screen.getByRole("link", { name: "Download structural report" })).toHaveAttribute(
    "href",
    "/hdr/report",
  );
  expect(
    screen.getByRole("checkbox", { name: "Include Video 1 Brightness grade in descriptions" }),
  ).not.toBeChecked();
  fireEvent.click(screen.getByRole("button", { name: "Preview Video 1 Dolby Vision plot" }));
  expect(setLightboxImage).toHaveBeenCalledWith("/hdr/dv");
  fireEvent.click(screen.getByRole("checkbox", { name: "Include Video 1 HDR10+ in descriptions" }));
  expect(value.setIncludedArtifacts).toHaveBeenCalledWith(["dv"]);
  expect(screen.queryByRole("checkbox", { name: /report/i })).toBeNull();
  fireEvent.change(screen.getByRole("combobox", { name: "Metadata formats" }), {
    target: { value: "dolby-vision" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Generate" }));
  expect(value.generate).toHaveBeenCalledWith({
    targetIDs: [first],
    peakSource: "histogram",
    formats: "dolby-vision",
  });
});

it("requires selected comparison authority and explicit mapping", () => {
  const base = facet();
  const value: HDRAnalysisFacet = {
    ...base,
    view: {
      ...base.view,
      comparison: {
        id: "source-b",
        fingerprint: "fingerprint-b",
        release: { SourcePath: "Synthetic.mkv", Generation: 1 },
        expiresAt: "2026-10-13T00:00:00Z",
      },
    },
  };
  render(<HDRAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  fireEvent.change(screen.getByRole("combobox", { name: "Analysis mode" }), {
    target: { value: "manual" },
  });
  fireEvent.click(screen.getByRole("checkbox", { name: /Video 1/ }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Brightness grade comparison" }));
  expect(screen.getByRole("button", { name: "Generate" })).toBeDisabled();
  fireEvent.change(screen.getByRole("spinbutton", { name: "Confirmed frame offset (B − A)" }), {
    target: { value: "0" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "Common crop: left,top,width,height" }), {
    target: { value: "0,12,192,88" },
  });
  fireEvent.click(screen.getByRole("checkbox", { name: "Scene synchronization" }));
  fireEvent.click(screen.getByRole("button", { name: "Generate" }));
  expect(value.generate).toHaveBeenCalledWith({
    targetIDs: [first],
    peakSource: "histogram",
    formats: "auto",
    decoded: {
      automatic: false,
      sceneSync: true,
      activeArea: false,
      comparison: {
        sourceId: "source-b",
        frameOffset: 0,
        crop: { left: 0, top: 12, width: 192, height: 88 },
        grade: true,
        colour: false,
      },
    },
  });
  expect(value.setIncludedArtifacts).not.toHaveBeenCalled();
});

it("requires explicit multi-target selection and submits source order with the selected estimator", () => {
  const value = facet();
  render(<HDRAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  expect(screen.getByRole("button", { name: "Generate" })).toBeDisabled();
  fireEvent.click(screen.getByRole("checkbox", { name: /Video 2/ }));
  fireEvent.click(screen.getByRole("checkbox", { name: /Video 1/ }));
  fireEvent.change(screen.getByRole("combobox", { name: "Peak estimator" }), {
    target: { value: "max-scl-luminance" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Generate" }));
  expect(value.generate).toHaveBeenCalledWith({
    targetIDs: [first, second],
    peakSource: "max-scl-luminance",
    formats: "auto",
    decoded: { automatic: false, audio: true, sceneSync: false, activeArea: false },
  });
  expect(screen.queryByRole("checkbox", { name: "Include in descriptions" })).toBeNull();
  expect(
    screen.getByText(
      "Native HDR plots, selected audio images, and amplitude statistics are included by default. Select picture-check images below to include them in descriptions.",
    ),
  ).toBeVisible();
});

it("defaults to metadata plots and primary audio and allows sampled or full-frame picture checks", () => {
  const value = facet();
  render(<HDRAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  const mode = screen.getByRole("combobox", { name: "Analysis mode" });
  expect(mode).toHaveValue("metadata_audio");
  fireEvent.click(screen.getByRole("checkbox", { name: /Video 1/ }));
  fireEvent.click(screen.getByRole("button", { name: "Generate" }));
  expect(value.generate).toHaveBeenLastCalledWith({
    targetIDs: [first],
    peakSource: "histogram",
    formats: "auto",
    decoded: { automatic: false, audio: true, sceneSync: false, activeArea: false },
  });
  expect(screen.queryByRole("checkbox", { name: "Measure every video frame (slower)" })).toBeNull();
  fireEvent.change(mode, { target: { value: "pictures_audio" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate" }));
  expect(value.generate).toHaveBeenLastCalledWith({
    targetIDs: [first],
    peakSource: "histogram",
    formats: "auto",
    decoded: { automatic: true, sampled: true, sceneSync: false, activeArea: false },
  });
  expect(
    screen.getByText(/optional HDR10 reference can provide additional grade and colour details/),
  ).toBeVisible();
  fireEvent.click(screen.getByRole("checkbox", { name: "Measure every video frame (slower)" }));
  fireEvent.click(screen.getByRole("button", { name: "Generate" }));
  expect(value.generate).toHaveBeenLastCalledWith({
    targetIDs: [first],
    peakSource: "histogram",
    formats: "auto",
    decoded: { automatic: true, sampled: false, sceneSync: false, activeArea: false },
  });
  fireEvent.change(mode, { target: { value: "metadata_audio" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate" }));
  expect(value.generate).toHaveBeenLastCalledWith({
    targetIDs: [first],
    peakSource: "histogram",
    formats: "auto",
    decoded: { automatic: false, audio: true, sceneSync: false, activeArea: false },
  });
  expect(screen.queryByRole("checkbox", { name: "Measure every video frame (slower)" })).toBeNull();
  fireEvent.change(mode, { target: { value: "manual" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate" }));
  expect(value.generate).toHaveBeenLastCalledWith({
    targetIDs: [first],
    peakSource: "histogram",
    formats: "auto",
  });
});

it("omits hidden manual checks in automatic mode and restores them when returning", () => {
  const value = facet();
  render(<HDRAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  const mode = screen.getByRole("combobox", { name: "Analysis mode" });
  fireEvent.click(screen.getByRole("checkbox", { name: /Video 1/ }));
  fireEvent.change(mode, { target: { value: "manual" } });
  fireEvent.click(screen.getByRole("checkbox", { name: "Scene synchronization" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Active-area borders" }));
  fireEvent.change(mode, { target: { value: "pictures_audio" } });
  expect(screen.queryByRole("checkbox", { name: "Scene synchronization" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Generate" }));
  expect(value.generate).toHaveBeenLastCalledWith(
    expect.objectContaining({
      decoded: { automatic: true, sampled: true, sceneSync: false, activeArea: false },
    }),
  );
  fireEvent.change(mode, { target: { value: "manual" } });
  fireEvent.click(screen.getByRole("button", { name: "Generate" }));
  expect(value.generate).toHaveBeenLastCalledWith(
    expect.objectContaining({
      decoded: { automatic: false, sceneSync: true, activeArea: true },
    }),
  );
});

it("retries failed measurements alongside successful targets and blocks retries while pending", () => {
  const original = facet();
  const value: HDRAnalysisFacet = {
    ...original,
    view: {
      ...original.view,
      result: {
        id: "analysis",
        workflowId: "workflow",
        revision: 2,
        release: { SourcePath: "Synthetic.HDR.mkv", Generation: 1 },
        manifestFingerprint: "manifest",
        attemptId: "attempt",
        targetIds: [first, second],
        peakSource: "histogram",
        profileVersion: "hdr-analysis-v2",
        decoded: { sceneSync: true, activeArea: false },
        status: "partial",
        createdAt: "2026-10-09T00:00:00Z",
        targets: [
          {
            targetId: first,
            label: "Video 1",
            status: "completed",
            frames: 100,
            scenes: 2,
            decoded: [
              {
                mode: "scene_sync",
                state: "complete",
                profile: "hdr-scene-v1",
                artifacts: [],
                selected: 100,
                measured: 100,
              },
            ],
          },
          {
            targetId: second,
            label: "Video 2",
            status: "partial",
            frames: 100,
            scenes: 2,
            decoded: [
              {
                mode: "scene_sync",
                state: "failed",
                profile: "hdr-scene-v1",
                artifacts: [],
                selected: 100,
                measured: 0,
              },
            ],
          },
        ],
      },
    },
  };
  const props = { facet: value, setLightboxImage: vi.fn(), setLightboxAlt: vi.fn() };
  const { rerender } = render(<HDRAnalysisPage {...props} />);
  const retry = screen.getByRole("button", { name: /Retry .* measurement/ });
  expect(retry).toBeEnabled();
  fireEvent.click(retry);
  expect(value.retryDecoded).toHaveBeenCalledWith(["scene_sync"]);
  rerender(
    <HDRAnalysisPage
      {...props}
      facet={{
        ...value,
        view: {
          ...value.view,
          pendingRequest: {
            release: value.view.result!.release,
            targetIds: [first, second],
            peakSource: "histogram",
            profileVersion: "hdr-analysis-v2",
            decoded: { sceneSync: true, activeArea: false },
          },
        },
      }}
    />,
  );
  expect(screen.getByRole("button", { name: /Retry .* measurement/ })).toBeDisabled();
});

it("blocks unconfirmed tracks and displays live extraction progress", () => {
  const value = facet();
  const props = { setLightboxImage: vi.fn(), setLightboxAlt: vi.fn() };
  const { rerender } = render(
    <HDRAnalysisPage
      {...props}
      facet={{
        ...value,
        view: {
          ...value.view,
          targets: [
            {
              ...value.view.targets[0]!,
              supported: false,
              reason: "MediaInfo must confirm HDR10+.",
            },
          ],
        },
      }}
    />,
  );
  expect(screen.getByRole("checkbox", { name: /Video 1/ })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Generate" })).toBeDisabled();
  rerender(
    <HDRAnalysisPage
      {...props}
      facet={{
        ...value,
        view: {
          ...value.view,
          status: "running",
          message: "Reading HDR10+ metadata: 1024 / 2048 MiB (50%)",
          progress: 52,
        },
      }}
    />,
  );
  expect(screen.getByRole("status")).toHaveTextContent(
    "Reading HDR10+ metadata: 1024 / 2048 MiB (50%) (52%)",
  );
  expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled();
});

it("keeps partial plots inspectable, blocks inclusion, and restores focus after cancellation", () => {
  const original = facet();
  const value: HDRAnalysisFacet = {
    ...original,
    view: {
      ...original.view,
      status: "running",
      phase: "reading_metadata",
      result: {
        id: "analysis",
        workflowId: "workflow",
        revision: 2,
        release: { SourcePath: "Synthetic.HDR.mkv", Generation: 1 },
        manifestFingerprint: "manifest",
        attemptId: "attempt",
        targetIds: [first, second],
        peakSource: "histogram",
        profileVersion: "hdr-analysis-v1",
        status: "partial",
        createdAt: "2026-10-09T00:00:00Z",
        completedAt: "2026-10-09T00:01:00Z",
        targets: [
          {
            targetId: first,
            label: "Video 1",
            status: "completed",
            frames: 100,
            scenes: 2,
            profile: "B",
            artifact: { id: "plot", width: 3000, height: 1200 },
          },
          {
            targetId: second,
            label: "Video 2",
            status: "failed",
            frames: 0,
            scenes: 0,
            failure: { code: "incomplete_extraction", message: "incomplete metadata" },
          },
        ],
      },
    },
  };
  const props = { facet: value, setLightboxImage: vi.fn(), setLightboxAlt: vi.fn() };
  const view = render(<HDRAnalysisPage {...props} />);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(value.cancel).toHaveBeenCalledOnce();
  const idle: HDRAnalysisFacet = {
    ...value,
    view: { ...value.view, status: "idle", phase: "", error: "Canceled" },
  };
  view.rerender(<HDRAnalysisPage {...props} facet={idle} />);
  expect(screen.getByRole("button", { name: "Generate" })).toHaveFocus();
  expect(screen.queryByRole("checkbox", { name: "Include in descriptions" })).toBeNull();
  expect(screen.getByRole("link", { name: "Download PNG" })).toHaveAttribute("href", "/hdr/plot");
  fireEvent.click(screen.getByRole("button", { name: "Preview Video 1 HDR10+ plot" }));
  expect(props.setLightboxImage).toHaveBeenCalledWith("/hdr/plot");
  expect(screen.queryByRole("button", { name: "Disable HDR inclusion" })).toBeNull();
});

it("shows video thread controls only when picture checks are selected", () => {
  const value = facet();
  render(<HDRAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  fireEvent.change(screen.getByRole("combobox", { name: "Analysis mode" }), {
    target: { value: "manual" },
  });
  expect(screen.queryByLabelText("Video decoder threads (0 uses the mode default)")).toBeNull();
  fireEvent.click(screen.getByLabelText("Scene synchronization"));
  expect(screen.getByLabelText("Video decoder threads (0 uses the mode default)")).toBeEnabled();
  fireEvent.click(screen.getByLabelText("Scene synchronization"));
  expect(screen.queryByLabelText("Video decoder threads (0 uses the mode default)")).toBeNull();
});

it("isolates bounded synchronization options and clears all tracks when audio is disabled", () => {
  const value = facet();
  render(<HDRAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  fireEvent.change(screen.getByRole("combobox", { name: "Analysis mode" }), {
    target: { value: "pictures_audio" },
  });
  fireEvent.click(screen.getByRole("checkbox", { name: /Video 1/ }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Brightness grade comparison" }));
  fireEvent.change(screen.getByRole("combobox", { name: "Analysis mode" }), {
    target: { value: "boundary" },
  });
  expect(screen.getByLabelText("Analyze audio")).toBeChecked();
  expect(screen.getByLabelText("Active-area borders")).not.toBeChecked();
  expect(screen.queryByLabelText("Scene synchronization")).toBeNull();
  fireEvent.click(screen.getByLabelText("Analyze all audio tracks (up to 64)"));
  fireEvent.change(screen.getByLabelText("Video decoder threads (0 uses the mode default)"), {
    target: { value: "8" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Generate" }));
  expect(value.generate).toHaveBeenLastCalledWith(
    expect.objectContaining({
      decoded: {
        automatic: false,
        boundarySync: true,
        audio: true,
        allAudioTracks: true,
        sceneSync: false,
        activeArea: false,
        videoDecoderThreads: 8,
      },
    }),
  );
  fireEvent.click(screen.getByLabelText("Analyze audio"));
  fireEvent.click(screen.getByRole("button", { name: "Generate" }));
  expect(value.generate).toHaveBeenLastCalledWith(
    expect.objectContaining({
      decoded: {
        automatic: false,
        boundarySync: true,
        sceneSync: false,
        activeArea: false,
        videoDecoderThreads: 8,
      },
    }),
  );
  fireEvent.click(screen.getByLabelText("Analyze audio"));
  expect(screen.getByLabelText("Analyze all audio tracks (up to 64)")).not.toBeChecked();
});

it("restores bounded mode, audio-off, active-area and decoder controls", () => {
  const base = facet();
  const value: HDRAnalysisFacet = {
    ...base,
    view: {
      ...base.view,
      pendingRequest: {
        release: { SourcePath: "Synthetic.HDR.mkv", Generation: 1 },
        targetIds: [first],
        peakSource: "histogram",
        profileVersion: "hdr-analysis-v3",
        formats: "auto",
        decoded: { boundarySync: true, activeArea: true, sceneSync: false, videoDecoderThreads: 4 },
      },
    },
  };
  render(<HDRAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  expect(screen.getByLabelText("Analysis mode")).toHaveValue("boundary");
  expect(screen.getByLabelText("Analyze audio")).not.toBeChecked();
  expect(screen.getByLabelText("Active-area borders")).toBeChecked();
  expect(screen.getByLabelText("Video decoder threads (0 uses the mode default)")).toHaveValue(4);
});
