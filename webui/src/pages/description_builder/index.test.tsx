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
    preview: {
      SourcePath: "C:\\media\\Example",
      ContentFailures: [],
      Groups: [
        {
          GroupKey: "unit3d",
          Trackers: ["EXAMPLE"],
          RawDescription: "raw",
          RawDescriptionHTML: "<p>raw</p>",
          Description: "raw",
          DescriptionHTML: "<p>raw</p>",
          HasOverride: false,
          ImageHost: {
            Status: "ready",
            SelectedHost: "",
            AllowedHosts: [],
            Reuploaded: false,
            Message: "",
          },
        },
      ],
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
  it("forwards edits and explicit save through the facet", () => {
    const descriptions = facet();
    render(
      <DescriptionBuilderPage
        facet={descriptions}
        sourcePath="C:\\media\\Example"
        trackerIconSrcByName={{}}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Expand" }));
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "changed" } });
    fireEvent.click(screen.getByRole("button", { name: "Save group" }));
    expect(descriptions.edit).toHaveBeenCalledWith("unit3d", "changed");
    expect(descriptions.save).toHaveBeenCalledWith("unit3d");
  });
});
