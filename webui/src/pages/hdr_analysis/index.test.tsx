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
    phase: "",
    message: "",
    progress: 0,
    mutationBlockedReason: "",
    error: "",
  },
  generate: vi.fn(async () => true),
  retry: vi.fn(async () => true),
  cancel: vi.fn(async () => true),
  artifactURL: (id) => `/hdr/${id}`,
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
  });
  expect(screen.queryByRole("checkbox", { name: "Include in descriptions" })).toBeNull();
  expect(
    screen.getByText("Completed HDR plots are automatically included in generated descriptions."),
  ).toBeVisible();
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
