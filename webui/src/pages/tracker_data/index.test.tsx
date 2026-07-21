// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { InputFacet } from "../../releaseSession/types";
import TrackerDataPage from "./index";

afterEach(cleanup);

describe("TrackerDataPage", () => {
  it("renders the input facet's immutable tracker-data view", () => {
    const facet = {
      view: {
        sourceDraft: "",
        selectedSource: "",
        status: "idle",
        error: "",
        preparationDirty: false,
        intent: { sourceLookupURL: "", identity: {}, releaseName: {}, trackers: [] },
        preview: null,
        trackerData: [],
      },
    } as unknown as InputFacet;
    render(
      <TrackerDataPage
        facet={facet}
        setLightboxImage={vi.fn()}
        setLightboxAlt={vi.fn()}
        trackerIconSrcByName={{}}
      />,
    );
    expect(screen.getByText("No tracker data available.")).toBeInTheDocument();
  });
});
