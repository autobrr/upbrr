// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { ComparisonPlan } from "../../api/generated/release-workflow";
import type { ComparisonsFacet } from "../../releaseSession/types";
import ComparisonPanel from "./ComparisonPanel";

afterEach(cleanup);

it("defaults new videos to B-frame matching and overlays without rewriting saved choices", async () => {
  const facet = comparisonFacet(null);
  const { unmount } = render(
    <ComparisonPanel facet={facet} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Create from videos" }));
  fireEvent.click(screen.getByRole("button", { name: "Add comparison video" }));
  for (const group of ["Main input · column 1", "Source 2 · column 2"]) {
    const column = within(screen.getByRole("group", { name: group }));
    expect(column.getByRole("checkbox", { name: "B" })).toBeChecked();
    expect(
      column.getByRole("checkbox", { name: "Comparison-only frame overlay and label" }),
    ).toBeChecked();
  }
  const main = within(screen.getByRole("group", { name: "Main input · column 1" }));
  fireEvent.click(main.getByRole("checkbox", { name: "B" }));
  fireEvent.click(main.getByRole("checkbox", { name: "I" }));
  fireEvent.click(main.getByRole("checkbox", { name: "Comparison-only frame overlay and label" }));
  fireEvent.click(screen.getByRole("button", { name: "Apply comparison setup" }));
  await waitFor(() => expect(facet.edit).toHaveBeenCalledTimes(1));
  const plan = vi.mocked(facet.edit).mock.calls[0]![0].plan;
  expect(plan.sources[0]).toMatchObject({
    preferBFrames: false,
    allowedFrameTypes: ["I"],
    overlay: false,
  });
  expect(plan.sources[1]).toMatchObject({
    preferBFrames: true,
    allowedFrameTypes: ["B"],
    overlay: true,
  });
  unmount();
  render(
    <ComparisonPanel
      facet={comparisonFacet(plan)}
      setLightboxImage={vi.fn()}
      setLightboxAlt={vi.fn()}
    />,
  );
  const saved = within(screen.getByRole("group", { name: "Main input · column 1" }));
  expect(saved.getByRole("checkbox", { name: "I" })).toBeChecked();
  expect(saved.getByRole("checkbox", { name: "Default B-frame preference" })).not.toBeChecked();
  expect(
    saved.getByRole("checkbox", { name: "Comparison-only frame overlay and label" }),
  ).not.toBeChecked();
});

it("returns to the entry actions after removing an unapplied setup", async () => {
  const facet = comparisonFacet(null);
  vi.mocked(facet.edit).mockResolvedValueOnce(false);
  render(<ComparisonPanel facet={facet} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "Import existing comparisons" }));
  fireEvent.click(screen.getByRole("button", { name: "Add mapped column" }));
  fireEvent.click(screen.getByRole("button", { name: "Remove comparisons" }));
  await waitFor(() => expect(facet.edit).toHaveBeenCalledTimes(1));
  expect(screen.getByRole("button", { name: "Add mapped column" })).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Remove comparisons" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Import existing comparisons" })).toBeVisible(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Create from videos" }));
  expect(screen.getByRole("group", { name: "Main input · column 1" })).toBeVisible();
});

const comparisonPlan = (imported = true): ComparisonPlan => ({
  imported,
  sources: ["Source", "Encode", "Filtered"].map((label, index) => ({
    id: imported ? String(index + 1) : index === 0 ? "main" : String(index + 1),
    label,
    imported,
    overlay: false,
    ...(imported
      ? {}
      : {
          fingerprint: `video-${index}`,
          frameRate: 24,
          durationSeconds: 100,
          width: 1920,
          height: 1080,
        }),
  })),
  mainSourceId: imported ? "2" : "main",
  reuseMainScreenshots: false,
  preferences: { count: 1, darkPercent: 0, motionPercent: 0 },
  fingerprint: "review-1",
  rows: [
    {
      id: "1",
      excluded: false,
      cells: [0, 1, 2].map((index) => ({
        sourceId: imported ? String(index + 1) : index === 0 ? "main" : String(index + 1),
        artifactId: `image-${index}`,
        contentSha256: String(index).repeat(64),
        ...(imported
          ? {}
          : { timestampSeconds: 12, frameNumber: 288, pictureType: "B", confidence: 0.96 }),
      })),
    },
  ],
});

const comparisonFacet = (plan: ComparisonPlan | null = comparisonPlan()): ComparisonsFacet => ({
  view: {
    workflowID: "workflow-1",
    plan,
    previews: {},
    previewPositions: {},
    artifacts: {
      id: "media-1",
      workflowId: "workflow-1",
      revision: 3,
      release: { id: "release-1", revision: 1 },
      releaseRef: { SourcePath: "example.mkv", Generation: 1 },
      projectionSet: { id: "projection-1", revision: 1 },
      captureFingerprint: "capture",
      requirementsFingerprint: "requirements",
      imageRequirementsPrepared: false,
      status: "completed",
      createdAt: "2026-11-13T00:00:00Z",
      artifacts: [0, 1, 2].map((index) => ({
        id: `image-${index}`,
        kind: "comparison",
        purpose: "comparison",
        selected: true,
        url: `/images/${index}`,
      })),
    },
    busy: false,
    blockedReason: "",
    error: "",
  },
  edit: vi.fn(async () => true),
  preview: vi.fn(async () => null),
  importImages: vi.fn(async () => true),
});

it("reviews exact retained images and invalidates approval when reuse choices change", () => {
  const facet = comparisonFacet();
  const openImage = vi.fn();
  const { rerender } = render(
    <ComparisonPanel facet={facet} setLightboxImage={openImage} setLightboxAlt={vi.fn()} />,
  );
  fireEvent.click(screen.getByRole("img", { name: "Encode, row 1, image 2" }));
  expect(openImage).toHaveBeenCalledWith("/images/1");
  const approve = screen.getByRole("button", { name: "Approve reviewed comparison images" });
  fireEvent.click(approve);
  expect(facet.edit).toHaveBeenCalledWith({
    action: "approve",
    plan: facet.view.plan,
    expectedFingerprint: "review-1",
  });
  rerender(
    <ComparisonPanel
      facet={{
        ...facet,
        view: { ...facet.view, plan: { ...facet.view.plan!, approvedFingerprint: "review-1" } },
      }}
      setLightboxImage={openImage}
      setLightboxAlt={vi.fn()}
    />,
  );
  expect(screen.getByText("These captured comparison images are approved.")).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Use the main column as normal screenshots" }),
  );
  expect(approve).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Apply comparison setup" }));
  expect(facet.edit).toHaveBeenLastCalledWith(
    expect.objectContaining({
      action: "configure",
      plan: expect.objectContaining({
        reuseMainScreenshots: true,
        approvedFingerprint: "",
        fingerprint: "",
      }),
    }),
  );
  expect(
    screen.getAllByText("Externally supplied image · timing, type and processing unknown."),
  ).toHaveLength(3);
});

it("retains source order and supports explicit import mapping and duplicate-image removal", () => {
  const facet = comparisonFacet();
  render(<ComparisonPanel facet={facet} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "Move Filtered left" }));
  const row = screen.getByRole("region", { name: "Comparison row 1" });
  expect(
    within(row)
      .getAllByRole("heading", { level: 4 })
      .map((heading) => heading.textContent),
  ).toEqual(["Source", "Filtered", "Encode"]);
  fireEvent.change(screen.getByLabelText("Source column for Source, row 1, image 1"), {
    target: { value: "3" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Remove Filtered, row 1, image 1 image" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Exclude row 1" }));
  fireEvent.click(screen.getByRole("button", { name: "Apply comparison setup" }));
  const instructions = vi.mocked(facet.edit).mock.lastCall![0];
  expect(instructions.plan.sources.map((source) => source.id)).toEqual(["1", "3", "2"]);
  expect(instructions.plan.rows[0]!.excluded).toBe(true);
  expect(instructions.plan.rows[0]!.cells).toHaveLength(2);
  expect(screen.getByRole("button", { name: "Approve reviewed comparison images" })).toBeDisabled();
});

it("imports prepared files without guessing the main column or flattening them into screenshots", () => {
  const facet = comparisonFacet(null);
  render(<ComparisonPanel facet={facet} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "Import existing comparisons" }));
  const files = [
    new File(["png"], "01-01-source.png", { type: "image/png" }),
    new File(["png"], "01-02-encode.png", { type: "image/png" }),
  ];
  fireEvent.change(screen.getByLabelText("Prepared comparison images"), { target: { files } });
  fireEvent.click(screen.getByRole("button", { name: "Import images" }));
  expect(facet.importImages).toHaveBeenCalledWith(
    files,
    expect.objectContaining({ imported: true, mainSourceId: "", rows: [], sources: [] }),
  );
  expect(facet.edit).not.toHaveBeenCalled();
});

it("keeps each duplicate-column image's row input attached to its own pixels", () => {
  const plan = comparisonPlan();
  const facet = comparisonFacet({
    ...plan,
    rows: plan.rows.map((row) => ({
      ...row,
      cells: row.cells.map((cell, index) => (index === 1 ? { ...cell, sourceId: "1" } : cell)),
    })),
  });
  render(<ComparisonPanel facet={facet} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  const file = new File(["png"], "replacement.png", { type: "image/png" });
  fireEvent.change(screen.getByLabelText("Replace Source, row 1, image 1"), {
    target: { files: [file] },
  });
  expect(facet.importImages).toHaveBeenCalledWith([file], facet.view.plan, {
    rowID: "1",
    sourceID: "1",
    artifactID: "image-0",
  });
  const first = screen.getByLabelText("Row for Source, row 1, image 1");
  fireEvent.change(first, { target: { value: "new-row" } });
  fireEvent.blur(first);
  const remaining = within(screen.getByRole("region", { name: "Comparison row 1" })).getByLabelText(
    "Row for Source, row 1, image 1",
  );
  expect(remaining).toHaveValue("1");
  fireEvent.blur(remaining);
  fireEvent.click(screen.getByRole("button", { name: "Apply comparison setup" }));
  const rows = vi.mocked(facet.edit).mock.lastCall![0].plan.rows;
  expect(rows.find((row) => row.id === "1")!.cells.map((cell) => cell.artifactId)).toContain(
    "image-1",
  );
  expect(rows.find((row) => row.id === "new-row")!.cells.map((cell) => cell.artifactId)).toEqual([
    "image-0",
  ]);
  const moved = within(
    screen.getByRole("region", { name: "Comparison row new-row" }),
  ).getByLabelText("Row for Source, row new-row, image 1");
  fireEvent.change(moved, { target: { value: "final-row" } });
  fireEvent.blur(moved);
  expect(screen.getByRole("region", { name: "Comparison row new-row" })).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Apply comparison setup" }));
  expect(
    vi.mocked(facet.edit).mock.lastCall![0].plan.rows.find((row) => row.id === "new-row")?.cells,
  ).toEqual([]);
});

it("keeps source previews independent, steps decoded frames and anchors displayed frames", async () => {
  const initial = comparisonFacet(comparisonPlan(false));
  const preview = (sourceId: string, timestampSeconds: number) => ({
    id: `preview-${sourceId}`,
    workflowId: "workflow-1",
    workflowRevision: 3,
    release: { id: "release-1", revision: 1 },
    comparisonSourceId: sourceId,
    timestampSeconds,
    pictureType: "B",
    contentUrl: `/previews/${sourceId}`,
    expiresAt: "2026-11-13T01:00:00Z",
    processing: {
      requestedRenderer: "libplacebo",
      renderer: "libplacebo",
      tonemapped: false,
      degraded: false,
    },
  });
  const facet: ComparisonsFacet = {
    ...initial,
    view: {
      ...initial.view,
      artifacts: {
        ...initial.view.artifacts!,
        artifacts: initial.view.artifacts!.artifacts.map((image, index) =>
          index === 1
            ? {
                ...image,
                processing: {
                  requestedRenderer: "software",
                  renderer: "software",
                  tonemapped: true,
                  degraded: false,
                },
              }
            : image,
        ),
      },
      previews: { main: preview("main", 10), "2": preview("2", 11.25) },
      previewPositions: { main: 10, "2": 11.25 },
    },
    preview: vi.fn(async () => preview("2", 20.13)),
  };
  render(<ComparisonPanel facet={facet} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  expect(screen.getByText("Renderer: software; tonemapping applied: yes.")).toBeVisible();
  expect(screen.getAllByText("Renderer: libplacebo; tonemapping applied: no.")).toHaveLength(2);
  fireEvent.change(screen.getByLabelText("Approximate frame seek · Encode"), {
    target: { value: "480" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Preview Encode" }));
  expect(facet.preview).toHaveBeenCalledWith("2", 20, undefined);
  await waitFor(() => expect(screen.getByLabelText("Preview seconds · Encode")).toHaveValue(20.13));
  const source = screen.getByRole("group", { name: "Encode · column 2" });
  fireEvent.click(within(source).getByRole("button", { name: "Next frame" }));
  expect(facet.preview).toHaveBeenLastCalledWith("2", 20.13, 1);
  fireEvent.click(within(source).getByRole("checkbox", { name: "I" }));
  fireEvent.click(within(source).getByRole("checkbox", { name: "B" }));
  fireEvent.click(
    within(source).getByRole("checkbox", { name: "Comparison-only frame overlay and label" }),
  );
  fireEvent.click(
    within(source).getByRole("button", { name: "Use displayed previews as an anchor" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Apply comparison setup" }));
  const instructions = vi.mocked(facet.edit).mock.lastCall![0];
  expect(instructions.plan.sources[1]).toEqual(
    expect.objectContaining({ id: "2", allowedFrameTypes: ["I", "B"], overlay: true }),
  );
  expect(instructions.plan.sources[0]!.allowedFrameTypes).toBeUndefined();
  expect(instructions.plan.anchors).toEqual([
    { sourceId: "2", mainSeconds: 10, sourceSeconds: 11.25 },
  ]);
  fireEvent.click(screen.getByRole("button", { name: "Add manual row from timeline positions" }));
  fireEvent.click(screen.getByRole("button", { name: "Apply comparison setup" }));
  expect(vi.mocked(facet.edit).mock.lastCall![0].plan.rows[1]!.cells).toEqual([
    { sourceId: "main", timestampSeconds: 10 },
    { sourceId: "2", timestampSeconds: 20.13 },
    { sourceId: "3", timestampSeconds: 0 },
  ]);
  const timestamp = screen.getByLabelText("Frame time for Source, row 1, image 1");
  timestamp.focus();
  fireEvent.change(timestamp, { target: { value: "12.3" } });
  expect(timestamp).toHaveFocus();
  expect(timestamp).toHaveValue(12.3);
});

it("removes an unwanted column without discarding remaining rows or anchors", () => {
  const initial = comparisonPlan(false);
  const plan = {
    ...initial,
    sources: initial.sources.map((source) =>
      source.id === "3" ? { ...source, id: "column-3" } : source,
    ),
    rows: initial.rows.map((row) => ({
      ...row,
      cells: row.cells.map((cell) =>
        cell.sourceId === "3" ? { ...cell, sourceId: "column-3" } : cell,
      ),
    })),
    anchors: [
      { sourceId: "2", mainSeconds: 1, sourceSeconds: 2 },
      { sourceId: "column-3", mainSeconds: 1, sourceSeconds: 3 },
    ],
  };
  const facet = comparisonFacet(plan);
  render(<ComparisonPanel facet={facet} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);
  fireEvent.change(screen.getByLabelText("Video path for Filtered"), {
    target: { value: "unwanted.mkv" },
  });
  expect(screen.getByRole("button", { name: "Remove Source column" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Remove Filtered column" }));
  fireEvent.click(screen.getByRole("button", { name: "Add comparison video" }));
  fireEvent.click(screen.getByRole("button", { name: "Remove Source 4 column" }));
  fireEvent.click(screen.getByRole("button", { name: "Apply comparison setup" }));
  const instructions = vi.mocked(facet.edit).mock.lastCall![0];
  expect(instructions.plan.sources.map((source) => source.id)).toEqual(["main", "2"]);
  expect(instructions.plan.rows[0]!.cells.map((cell) => cell.artifactId)).toEqual([
    "image-0",
    "image-1",
  ]);
  expect(instructions.plan.anchors).toEqual([plan.anchors[0]]);
  expect(instructions.videoSources).toEqual([]);
});
