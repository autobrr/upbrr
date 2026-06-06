// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { DescriptionBuilderPreview } from "../../types";
import DescriptionBuilderPage from "./index";

describe("DescriptionBuilderPage", () => {
  afterEach(() => {
    cleanup();
  });

  it("does not decode sanitized attribute entities before rendering", () => {
    const builderPreview = {
      SourcePath: "C:/media/Movie.mkv",
      Groups: [
        {
          GroupKey: "unit3d",
          Trackers: ["BLU"],
          Description: "",
          DescriptionHTML: "",
          RawDescription: "raw",
          RawDescriptionHTML: `<img src="http://invalid.invalid/&#34; onerror=&#34;alert(1)" />`,
          HasOverride: false,
          ImageHost: { Status: "", SelectedHost: "", AllowedHosts: [], Warnings: [] },
        },
      ],
    } as unknown as DescriptionBuilderPreview;

    const { container } = render(
      <DescriptionBuilderPage
        path="C:/media/Movie.mkv"
        builderPreview={builderPreview}
        builderRawByGroup={{}}
        builderRenderedByGroup={{}}
        builderExpandedGroups={{ unit3d: true }}
        builderLoading={false}
        builderSaving={false}
        builderRenderLoading={false}
        builderRefreshing={false}
        builderProgressMessage=""
        builderError=""
        builderSaved=""
        refreshDescriptionBuilder={vi.fn()}
        setBuilderRawByGroup={vi.fn()}
        setBuilderDirtyByGroup={vi.fn()}
        setBuilderExpandedGroups={vi.fn()}
        resetBuilderDescription={vi.fn()}
        renderBuilderDescription={vi.fn()}
        saveBuilderDescription={vi.fn()}
      />,
    );

    const image = container.querySelector(".tracker-description.rendered img");
    expect(image).toBeInstanceOf(HTMLImageElement);
    expect(image?.getAttribute("onerror")).toBeNull();
    expect(image?.getAttribute("src")).toContain(`" onerror="alert(1)`);
  });

  it("renders final built content as the raw description preview", () => {
    const builderPreview = {
      SourcePath: "C:/media/Movie.mkv",
      Groups: [
        {
          GroupKey: "unit3d",
          Trackers: ["HHD"],
          Description: "[img]https://img.example/final.png[/img]",
          DescriptionHTML: `<div class="tracker-description"><img src="https://img.example/final.png" /></div>`,
          RawDescription: "[img]https://img.example/final.png[/img]",
          RawDescriptionHTML: `<div class="tracker-description"><img src="https://img.example/final.png" /></div>`,
          HasOverride: false,
          ImageHost: { Status: "", SelectedHost: "", AllowedHosts: [], Warnings: [] },
        },
      ],
    } as unknown as DescriptionBuilderPreview;

    const { container } = render(
      <DescriptionBuilderPage
        path="C:/media/Movie.mkv"
        builderPreview={builderPreview}
        builderRawByGroup={{}}
        builderRenderedByGroup={{}}
        builderExpandedGroups={{ unit3d: true }}
        builderLoading={false}
        builderSaving={false}
        builderRenderLoading={false}
        builderRefreshing={false}
        builderProgressMessage=""
        builderError=""
        builderSaved=""
        refreshDescriptionBuilder={vi.fn()}
        setBuilderRawByGroup={vi.fn()}
        setBuilderDirtyByGroup={vi.fn()}
        setBuilderExpandedGroups={vi.fn()}
        resetBuilderDescription={vi.fn()}
        renderBuilderDescription={vi.fn()}
        saveBuilderDescription={vi.fn()}
      />,
    );

    expect(screen.queryByText("Built Description Preview")).not.toBeInTheDocument();
    expect(
      screen.getByDisplayValue("[img]https://img.example/final.png[/img]"),
    ).toBeInTheDocument();
    expect(container.querySelector('img[src="https://img.example/final.png"]')).toBeInstanceOf(
      HTMLImageElement,
    );
  });

  it("shows build progress while descriptions are loading", () => {
    const builderPreview = {
      SourcePath: "C:/media/Movie.mkv",
      Groups: [],
    } as unknown as DescriptionBuilderPreview;

    render(
      <DescriptionBuilderPage
        path="C:/media/Movie.mkv"
        builderPreview={builderPreview}
        builderRawByGroup={{}}
        builderRenderedByGroup={{}}
        builderExpandedGroups={{}}
        builderLoading
        builderSaving={false}
        builderRenderLoading={false}
        builderRefreshing
        builderProgressMessage="Rehosting required comparison and description images..."
        builderError=""
        builderSaved=""
        refreshDescriptionBuilder={vi.fn()}
        setBuilderRawByGroup={vi.fn()}
        setBuilderDirtyByGroup={vi.fn()}
        setBuilderExpandedGroups={vi.fn()}
        resetBuilderDescription={vi.fn()}
        renderBuilderDescription={vi.fn()}
        saveBuilderDescription={vi.fn()}
      />,
    );

    expect(
      screen.getAllByText("Rehosting required comparison and description images...").length,
    ).toBeGreaterThan(0);
  });

  it("initializes rendered comparison controls in the builder preview", async () => {
    const builderPreview = {
      SourcePath: "C:/media/Movie.mkv",
      Groups: [
        {
          GroupKey: "unit3d",
          Trackers: ["HHD"],
          Description: "",
          DescriptionHTML: "",
          RawDescription: "raw",
          RawDescriptionHTML: `
            <div class="comparison">
              <details class="comparison__details">
                <summary class="comparison__button">Show</summary>
                <ul class="comparison__screenshots">
                  <li><ul class="comparison__row">
                    <li class="comparison__image-container"><figure><img class="comparison__image" src="https://img.example/a.png" /></figure></li>
                    <li class="comparison__image-container"><figure><img class="comparison__image" src="https://img.example/b.png" /></figure></li>
                    <li class="comparison__image-container"><figure><img class="comparison__image" src="https://img.example/c.png" /></figure></li>
                  </ul></li>
                </ul>
              </details>
            </div>`,
          HasOverride: false,
          ImageHost: { Status: "", SelectedHost: "", AllowedHosts: [], Warnings: [] },
        },
      ],
    } as unknown as DescriptionBuilderPreview;

    const { container } = render(
      <DescriptionBuilderPage
        path="C:/media/Movie.mkv"
        builderPreview={builderPreview}
        builderRawByGroup={{}}
        builderRenderedByGroup={{}}
        builderExpandedGroups={{ unit3d: true }}
        builderLoading={false}
        builderSaving={false}
        builderRenderLoading={false}
        builderRefreshing={false}
        builderProgressMessage=""
        builderError=""
        builderSaved=""
        refreshDescriptionBuilder={vi.fn()}
        setBuilderRawByGroup={vi.fn()}
        setBuilderDirtyByGroup={vi.fn()}
        setBuilderExpandedGroups={vi.fn()}
        resetBuilderDescription={vi.fn()}
        renderBuilderDescription={vi.fn()}
        saveBuilderDescription={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(container.querySelectorAll(".comparison__image-container--hidden")).toHaveLength(2);
    });

    const summary = container.querySelector(".comparison__button");
    expect(summary).toBeInstanceOf(HTMLElement);
    fireEvent.click(summary as HTMLElement);
    fireEvent.mouseMove(window, { clientX: 900 });

    await waitFor(() => {
      const cells = Array.from(
        container.querySelectorAll<HTMLElement>(".comparison__image-container"),
      );
      expect(cells[0].classList.contains("comparison__image-container--hidden")).toBe(true);
      expect(cells[2].classList.contains("comparison__image-container--hidden")).toBe(false);
    });
  });
});
