// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DescriptionsFacet } from "../../releaseSession/types";
import DescriptionBuilderPage from "./index";

afterEach(cleanup);

const facet = (): DescriptionsFacet => ({
  view: {
    revision: 1,
    status: "ready",
    artifact: {
      id: "descriptions-1",
      workflowId: "workflow-1",
      revision: 7,
      release: { id: "release-1", revision: 2 },
      releaseRef: { SourcePath: "C:\\media\\Example", Generation: 1 },
      projectionSet: { id: "projections-1", revision: 4 },
      media: { id: "media-1", revision: 6 },
      inputFingerprint: "1".repeat(64),
      templateFingerprint: "2".repeat(64),
      descriptions: [
        {
          groupKey: "unit3d",
          trackerIds: ["EXAMPLE"],
          source: "raw",
          rendered: "<p>raw</p>",
          contentFingerprint: "3".repeat(64),
        },
      ],
      status: "completed",
      createdAt: "2026-07-21T00:00:00Z",
    },
    rawByGroup: { unit3d: "raw" },
    renderedByGroup: { unit3d: "<p>raw</p>" },
    dirtyGroups: [],
    staleReason: "",
    notice: "",
    error: "",
  },
  load: vi.fn(async () => true),
  edit: vi.fn(),
  render: vi.fn(async () => true),
  save: vi.fn(async () => true),
  reset: vi.fn(async () => true),
});

describe("DescriptionBuilderPage", () => {
  it("distinguishes actions and raw editors for two generated groups", () => {
    const base = facet();
    const artifact = base.view.artifact!;
    const descriptions: DescriptionsFacet = {
      ...base,
      view: {
        ...base.view,
        artifact: {
          ...artifact,
          descriptions: [
            ...artifact.descriptions,
            {
              ...artifact.descriptions[0],
              groupKey: "standalone",
              trackerIds: ["BTN"],
              source: "second raw",
              rendered: "<p>second raw</p>",
            },
          ],
        },
      },
    };
    render(
      <DescriptionBuilderPage
        facet={descriptions}
        sourcePath="C:\\media\\Example"
        trackerIconSrcByName={{}}
      />,
    );

    expect(screen.getByRole("button", { name: "Expand EXAMPLE" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Expand BTN" })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Expand EXAMPLE" }));
    fireEvent.click(screen.getByRole("button", { name: "Expand BTN" }));
    expect(screen.getByRole("textbox", { name: "Raw description for EXAMPLE" })).toBeVisible();
    expect(screen.getByRole("textbox", { name: "Raw description for BTN" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Render EXAMPLE" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Save group BTN" })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Reset group BTN" }));
    expect(descriptions.reset).toHaveBeenCalledWith("standalone");
  });

  it("forwards edits and explicit save through the facet", () => {
    const descriptions = facet();
    render(
      <DescriptionBuilderPage
        facet={descriptions}
        sourcePath="C:\\media\\Example"
        trackerIconSrcByName={{}}
      />,
    );
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Expand EXAMPLE" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Raw description for EXAMPLE" }), {
      target: { value: "changed" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save group EXAMPLE" }));
    expect(descriptions.edit).toHaveBeenCalledWith("unit3d", "changed");
    expect(descriptions.save).toHaveBeenCalledWith("unit3d");
  });

  it("hydrates the editor from the authoritative workflow description set", () => {
    const base = facet();
    const descriptions: DescriptionsFacet = {
      ...base,
      view: {
        ...base.view,
        artifact: {
          id: "descriptions-1",
          workflowId: "workflow-1",
          revision: 7,
          release: { id: "release-1", revision: 2 },
          releaseRef: { SourcePath: "C:\\media\\Example", Generation: 1 },
          projectionSet: { id: "projections-1", revision: 4 },
          media: { id: "media-1", revision: 6 },
          inputFingerprint: "1".repeat(64),
          templateFingerprint: "2".repeat(64),
          descriptions: [
            {
              groupKey: "unit3d",
              trackerIds: ["EXAMPLE"],
              source: "authoritative raw",
              rendered: "<p>authoritative rendered</p>",
              contentFingerprint: "3".repeat(64),
            },
          ],
          status: "completed",
          createdAt: "2026-07-21T00:00:00Z",
        },
        rawByGroup: {},
        renderedByGroup: {},
      },
    };
    render(
      <DescriptionBuilderPage
        facet={descriptions}
        sourcePath="C:\\media\\Example"
        trackerIconSrcByName={{}}
      />,
    );

    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Expand EXAMPLE" }));
    expect(screen.getByRole("textbox", { name: "Raw description for EXAMPLE" })).toHaveValue(
      "authoritative raw",
    );
    expect(screen.getByText("authoritative rendered")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Reset group EXAMPLE" }));
    expect(descriptions.reset).toHaveBeenCalledWith("unit3d");
  });
});
