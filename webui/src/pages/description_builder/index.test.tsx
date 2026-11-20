// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DescriptionsFacet } from "../../releaseSession/types";
import DescriptionBuilderPage from "./index";

afterEach(cleanup);

const facet = (): DescriptionsFacet => ({
  view: {
    revision: 1,
    status: "ready",
    mainContext: "workflow-1:1",
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
  renderMain: vi.fn(async () => ""),
  saveMain: vi.fn(async () => true),
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
    expect(
      screen.queryByRole("textbox", { name: "Raw description for EXAMPLE" }),
    ).not.toBeInTheDocument();
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

    expect(
      screen.queryByRole("textbox", { name: "Raw description for EXAMPLE" }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Expand EXAMPLE" }));
    expect(screen.getByRole("textbox", { name: "Raw description for EXAMPLE" })).toHaveValue(
      "authoritative raw",
    );
    expect(screen.getByText("authoritative rendered")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Reset group EXAMPLE" }));
    expect(descriptions.reset).toHaveBeenCalledWith("unit3d");
  });

  it("formats only the selected multiline text by pointer and keyboard", async () => {
    const user = userEvent.setup();
    const descriptions: DescriptionsFacet = {
      ...facet(),
      renderMain: vi.fn(async () => "<strong>Rendered shared text</strong>"),
    };
    render(<DescriptionBuilderPage facet={descriptions} sourcePath="Example" />);
    const panel = within(screen.getByRole("region", { name: "Main Description" }));
    const input = panel.getByRole<HTMLTextAreaElement>("textbox", { name: "Raw main description" });
    fireEvent.change(input, { target: { value: "Before first\nsecond after" } });
    input.focus();
    input.setSelectionRange(7, 19, "backward");

    await user.click(panel.getByRole("button", { name: "Bold" }));
    expect(input).toHaveValue("Before [b]first\nsecond[/b] after");
    expect(input).toHaveFocus();
    expect([input.selectionStart, input.selectionEnd, input.selectionDirection]).toEqual([
      10,
      22,
      "backward",
    ]);

    await user.tab({ shift: true });
    expect(panel.getByRole("button", { name: "Code" })).toHaveFocus();
    await user.keyboard("[Space]");
    const wrapped = "Before [b][code]first\nsecond[/code][/b] after";
    expect(input).toHaveValue(wrapped);
    expect(input).toHaveFocus();

    const disclosure = panel.getByText("Rendered Main Preview", { exact: true });
    expect(panel.getByText("No rendered preview yet.")).not.toBeVisible();
    await user.click(panel.getByRole("button", { name: "Render main preview" }));
    expect(await panel.findByText("Rendered shared text")).toBeVisible();
    await user.click(disclosure);
    expect(panel.getByText("Rendered shared text")).not.toBeVisible();
    await user.click(disclosure);
    expect(panel.getByText("Rendered shared text")).toBeVisible();
    expect(descriptions.renderMain).toHaveBeenCalledTimes(1);
    await user.click(panel.getByRole("button", { name: "Save main description" }));
    expect(descriptions.saveMain).toHaveBeenCalledWith(wrapped);
  });

  it("keeps a failed save's draft but does not apply stale preview or reuse it in a different workflow", async () => {
    let resolvePreview!: (html: string) => void;
    const preview = new Promise<string>((resolve) => {
      resolvePreview = resolve;
    });
    let rejectPreview!: (error: Error) => void;
    const pendingFailure = new Promise<string>((_resolve, reject) => {
      rejectPreview = reject;
    });
    const base = facet();
    const descriptions: DescriptionsFacet = {
      ...base,
      renderMain: vi
        .fn()
        .mockReturnValueOnce(preview)
        .mockReturnValueOnce(pendingFailure)
        .mockRejectedValue(new Error("Synthetic render failure")),
      saveMain: vi.fn(async () => false),
    };
    const { rerender } = render(
      <DescriptionBuilderPage facet={descriptions} sourcePath="Example" />,
    );
    const input = screen.getByRole("textbox", { name: "Raw main description" });
    fireEvent.change(input, { target: { value: "First draft" } });
    fireEvent.click(screen.getByRole("button", { name: "Render main preview" }));
    fireEvent.change(input, { target: { value: "Changed draft" } });
    await act(async () => resolvePreview("<p>Obsolete preview</p>"));
    expect(screen.queryByText("Obsolete preview")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Render main preview" }));
    fireEvent.change(input, { target: { value: "Changed again" } });
    await act(async () => rejectPreview(new Error("Outdated preview request")));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Render main preview" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Main preview could not be rendered.",
    );
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Save main description" }));
    });
    expect(input).toHaveValue("Changed again");
    expect(screen.queryByRole("status")).not.toBeInTheDocument();

    rerender(
      <DescriptionBuilderPage
        facet={{ ...descriptions, view: { ...descriptions.view, artifact: null } }}
        sourcePath="Example"
      />,
    );
    expect(input).toHaveValue("Changed again");
    expect(screen.getByRole("button", { name: "Save main description" })).toBeDisabled();

    const next: DescriptionsFacet = {
      ...base,
      view: {
        ...base.view,
        mainContext: "workflow-2:1",
        artifact: {
          ...base.view.artifact!,
          workflowId: "workflow-2",
          mainSource: "Persisted shared description",
        },
      },
    };
    rerender(<DescriptionBuilderPage facet={next} sourcePath="Example" />);
    expect(screen.getByRole("textbox", { name: "Raw main description" })).toHaveValue(
      "Persisted shared description",
    );
  });
});
