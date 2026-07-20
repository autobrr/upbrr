// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DuplicatesFacet } from "../../releaseSession/types";
import DupeCheckPage from "./index";

afterEach(cleanup);

describe("DupeCheckPage", () => {
  it("forwards duplicate-check intent through the facet", () => {
    const run = vi.fn(async () => true);
    const facet: DuplicatesFacet = {
      view: {
        status: "idle",
        snapshot: null,
        eligibility: null,
        ignoredTrackers: [],
        selectedTrackers: ["EXAMPLE"],
        error: "",
        transientError: "",
      },
      run,
      cancel: vi.fn(async () => true),
      chooseTrackers: vi.fn(),
      setIgnored: vi.fn(),
    };
    render(
      <DupeCheckPage
        facet={facet}
        sourcePath="C:\\media\\Example"
        trackerUploadItems={[{ name: "EXAMPLE", config: {} }]}
        trackerIconSrcByName={{}}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    expect(run).toHaveBeenCalledOnce();
  });

  it("owns tracker selection and blocks execution while selection is empty", () => {
    const chooseTrackers = vi.fn();
    const facet: DuplicatesFacet = {
      view: {
        status: "idle",
        snapshot: null,
        eligibility: null,
        ignoredTrackers: [],
        selectedTrackers: [],
        error: "",
        transientError: "",
      },
      run: vi.fn(async () => false),
      cancel: vi.fn(async () => true),
      chooseTrackers,
      setIgnored: vi.fn(),
    };
    render(
      <DupeCheckPage
        facet={facet}
        sourcePath="C:\\media\\Example"
        trackerUploadItems={[{ name: "EXAMPLE", config: {} }]}
        trackerIconSrcByName={{}}
      />,
    );

    expect(screen.getByRole("button", { name: "Run dupe check" })).toBeDisabled();
    fireEvent.click(screen.getByRole("checkbox", { name: "EXAMPLE" }));
    expect(chooseTrackers).toHaveBeenCalledWith(["EXAMPLE"]);
  });

  it("shows tracker auth and count progress from a running job snapshot", () => {
    const facet: DuplicatesFacet = {
      view: {
        status: "running",
        snapshot: {
          jobID: "dupe-job",
          correlationID: "dupe-correlation",
          release: { SourcePath: "C:\\media\\Example", Generation: 1 },
          runtimeGeneration: 1,
          status: "running",
          trackers: [
            {
              tracker: "EXAMPLE",
              status: "running",
              message: "checking tracker auth",
              result: {} as never,
              startedAt: "2026-07-16T00:00:00Z",
              finishedAt: "",
            },
          ],
          completedCount: 1,
          totalCount: 3,
          summary: {
            SourcePath: "C:\\media\\Example",
            Results: [],
            Notes: [],
            Eligibility: {
              Release: { SourcePath: "C:\\media\\Example", Generation: 1 },
              Trackers: [],
              EligibleTrackers: [],
            },
          },
          startedAt: "2026-07-16T00:00:00Z",
          finishedAt: "",
        },
        eligibility: null,
        ignoredTrackers: [],
        selectedTrackers: ["EXAMPLE", "SECOND", "THIRD"],
        error: "",
        transientError: "",
      },
      run: vi.fn(async () => true),
      cancel: vi.fn(async () => true),
      chooseTrackers: vi.fn(),
      setIgnored: vi.fn(),
    };

    render(
      <DupeCheckPage
        facet={facet}
        sourcePath="C:\\media\\Example"
        trackerUploadItems={[
          { name: "EXAMPLE", config: {} },
          { name: "SECOND", config: {} },
          { name: "THIRD", config: {} },
        ]}
        trackerIconSrcByName={{}}
      />,
    );

    expect(screen.getByText("1/3 trackers complete")).toBeInTheDocument();
    expect(screen.getByText("checking tracker auth")).toBeInTheDocument();
    expect(screen.getByRole("progressbar", { name: "Duplicate check progress" })).toHaveAttribute(
      "aria-valuenow",
      "1",
    );
  });
});
