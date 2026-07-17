// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { UploadFacet } from "../../releaseSession/types";
import TrackerUploadPage from "./index";

afterEach(cleanup);

describe("TrackerUploadPage", () => {
  it("keeps dry run explicit", () => {
    const runDryRun = vi.fn(async () => true);
    const facet: UploadFacet = {
      view: {
        revision: 1,
        selectedTrackers: ["EXAMPLE"],
        eligibility: null,
        ignoredDupesFor: [],
        questionnaireAnswers: {},
        options: { noSeed: false, runLogLevel: "info" },
        dryRunStatus: "idle",
        dryRun: null,
        dryRunStaleReason: "Run dry run.",
        reviewStatus: "idle",
        review: null,
        reviewStaleReason: "Run review.",
        snapshot: null,
        error: "",
        transientError: "",
      },
      chooseTrackers: vi.fn(),
      answerQuestionnaire: vi.fn(),
      changeOptions: vi.fn(),
      runDryRun,
      review: vi.fn(async () => true),
      start: vi.fn(async () => true),
      cancel: vi.fn(async () => true),
      retry: vi.fn(async () => true),
    };
    render(
      <TrackerUploadPage
        facet={facet}
        trackerUploadItems={[{ name: "EXAMPLE", config: {} }]}
        trackerIconSrcByName={{}}
      />,
    );
    expect(screen.queryByText("Debug logging")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Log level")).toHaveValue("info");
    expect(screen.getByRole("option", { name: "debug" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    expect(runDryRun).toHaveBeenCalledOnce();
  });

  it("renders dry-run entries with nullable Go slice fields", () => {
    const facet: UploadFacet = {
      view: {
        revision: 1,
        selectedTrackers: ["EXAMPLE"],
        eligibility: null,
        ignoredDupesFor: [],
        questionnaireAnswers: {},
        options: { noSeed: false, runLogLevel: "info" },
        dryRunStatus: "ready",
        dryRun: {
          SourcePath: "C:\\media\\Example",
          Trackers: [
            {
              Tracker: "EXAMPLE",
              Status: "error",
              Message: "Preview unavailable.",
              Files: null,
              Questionnaire: { Tracker: "EXAMPLE", Fields: null },
            },
          ],
        } as unknown as NonNullable<UploadFacet["view"]["dryRun"]>,
        dryRunStaleReason: "",
        reviewStatus: "idle",
        review: null,
        reviewStaleReason: "Review required.",
        snapshot: null,
        error: "",
        transientError: "",
      },
      chooseTrackers: vi.fn(),
      answerQuestionnaire: vi.fn(),
      changeOptions: vi.fn(),
      runDryRun: vi.fn(async () => true),
      review: vi.fn(async () => true),
      start: vi.fn(async () => true),
      cancel: vi.fn(async () => true),
      retry: vi.fn(async () => true),
    };

    render(
      <TrackerUploadPage
        facet={facet}
        trackerUploadItems={[{ name: "EXAMPLE", config: {} }]}
        trackerIconSrcByName={{}}
      />,
    );

    expect(screen.getByRole("heading", { name: "EXAMPLE" })).toBeInTheDocument();
    expect(screen.getByText("0/0")).toBeInTheDocument();
    expect(screen.getByText("Preview unavailable.")).toBeInTheDocument();
  });
});
