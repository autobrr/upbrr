// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render } from "@testing-library/react";
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
});
