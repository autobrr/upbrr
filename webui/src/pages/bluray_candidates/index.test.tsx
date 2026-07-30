// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { InputFacet } from "../../releaseSession/types";
import BlurayCandidatesPage from "./index";

afterEach(cleanup);

describe("BlurayCandidatesPage", () => {
  it("preserves candidate cards, cover preview, selection, and accepted state", () => {
    const selectCandidate = vi.fn<InputFacet["selectCandidate"]>();
    const setLightboxImage = vi.fn();
    const setLightboxAlt = vi.fn();
    const facet = {
      view: {
        status: "ready",
        error: "",
        preview: {
          Bluray: {
            BestScore: 97.5,
            Threshold: 80,
            AutoSelected: false,
            SelectedReleaseID: "accepted-candidate",
            Candidates: [
              {
                ReleaseID: "accepted-candidate",
                Accepted: true,
                Title: "Example Release 2026 Collector Edition",
                MovieTitle: "Example Release",
                MovieYear: 2026,
                Score: 97.5,
                Country: "Exampleland",
                Region: "A",
                Publisher: "Example Publisher",
                URL: "https://example.com/releases/accepted-candidate",
                CoverImages: [
                  { Kind: "Front", URL: "https://example.com/images/accepted-cover.jpg" },
                ],
              },
              {
                ReleaseID: "alternate-candidate",
                Accepted: false,
                Title: "Example Release 2026 Standard Edition",
                MovieTitle: "Example Release",
                MovieYear: 2026,
                Score: 91,
                Country: "Exampleland",
                Region: "B",
                Publisher: "Example Publisher",
                URL: "https://example.com/releases/alternate-candidate",
              },
            ],
          },
        },
      },
      selectCandidate,
    } as unknown as InputFacet;

    render(
      <BlurayCandidatesPage
        facet={facet}
        setLightboxImage={setLightboxImage}
        setLightboxAlt={setLightboxAlt}
      />,
    );

    expect(screen.getByText("Example Release 2026 Collector Edition")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Selected" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Select" }));
    expect(selectCandidate).toHaveBeenCalledWith("alternate-candidate");

    fireEvent.click(screen.getByRole("img", { name: "Front" }));
    expect(setLightboxImage).toHaveBeenCalledWith("https://example.com/images/accepted-cover.jpg");
    expect(setLightboxAlt).toHaveBeenCalledWith("Example Release 2026 Collector Edition Front");
  });
});
