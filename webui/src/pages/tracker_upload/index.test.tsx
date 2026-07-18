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
        authorizedRulesByTracker: {},
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
      setRuleAuthorized: vi.fn(),
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
        authorizedRulesByTracker: {},
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
      setRuleAuthorized: vi.fn(),
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

  it("offers authorization only for waivable rule diagnostics", () => {
    const setRuleAuthorized = vi.fn();
    const facet: UploadFacet = {
      view: {
        revision: 1,
        selectedTrackers: ["EXAMPLE"],
        eligibility: null,
        ignoredDupesFor: [],
        authorizedRulesByTracker: {},
        questionnaireAnswers: {},
        options: { noSeed: false, runLogLevel: "info" },
        dryRunStatus: "ready",
        dryRun: {
          SourcePath: "C:\\media\\Example",
          Trackers: [
            {
              Tracker: "EXAMPLE",
              Status: "ready",
              Files: [],
              Diagnostics: {
                Duplicate: {
                  Tracker: "EXAMPLE",
                  Status: "completed",
                  HasDupes: true,
                  Filtered: [{ ID: "1", Name: "Example.Release.2026.1080p-GRP" }],
                },
                LiveEligibilityReasons: [],
                RuleDecisions: [
                  {
                    Rule: "resolution_required",
                    Reason: "missing resolution",
                    Disposition: "strict",
                    Authorized: false,
                  },
                  {
                    Rule: "container",
                    Reason: "container is not accepted",
                    Disposition: "waivable",
                    Authorized: false,
                  },
                  {
                    Rule: "recommended_id",
                    Reason: "IMDb ID recommended",
                    Disposition: "advisory",
                    Authorized: false,
                  },
                ],
              },
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
      setRuleAuthorized,
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

    expect(screen.queryByLabelText("Authorize resolution_required for EXAMPLE")).toBeNull();
    expect(screen.queryByLabelText("Authorize recommended_id for EXAMPLE")).toBeNull();
    expect(screen.getByText("Duplicate diagnostics")).toBeInTheDocument();
    expect(screen.getByText("Example.Release.2026.1080p-GRP")).toBeInTheDocument();
    fireEvent.click(screen.getByLabelText("Authorize container for EXAMPLE"));
    expect(setRuleAuthorized).toHaveBeenCalledWith("EXAMPLE", "container", true);
  });

  it("renders redacted dry-run payloads for every tracker", () => {
    const facet: UploadFacet = {
      view: {
        revision: 1,
        selectedTrackers: ["FIRST", "SECOND"],
        eligibility: null,
        ignoredDupesFor: [],
        authorizedRulesByTracker: {},
        questionnaireAnswers: {},
        options: { noSeed: false, runLogLevel: "debug" },
        dryRunStatus: "ready",
        dryRun: {
          SourcePath: "C:\\media\\Example",
          Trackers: [
            {
              Tracker: "FIRST",
              Status: "ready",
              Endpoint:
                "https://tracker.example/upload?api_key=fixture-secret&name=Example.Release.2026.1080p-GRP",
              Payload: {
                api_key: "fixture-secret",
                name: "first-payload",
              },
              Files: [
                {
                  Field: "torrent",
                  Path: "C:\\private\\Example.Release.2026.1080p-GRP.torrent",
                  Present: true,
                },
              ],
              Diagnostics: {},
            },
            {
              Tracker: "SECOND",
              Status: "ready",
              Payload: {
                name: "second-top-level-payload",
              },
              Files: [],
              DebugSections: [
                {
                  Title: "Tracker request",
                  Endpoint: "https://tracker.example/upload?token=fixture-secret",
                  Files: [],
                  Payload: {
                    description: "line 1\nline 2",
                    name: "second-debug-payload",
                  },
                },
              ],
              Diagnostics: {},
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
      setRuleAuthorized: vi.fn(),
      runDryRun: vi.fn(async () => true),
      review: vi.fn(async () => true),
      start: vi.fn(async () => true),
      cancel: vi.fn(async () => true),
      retry: vi.fn(async () => true),
    };

    render(
      <TrackerUploadPage
        facet={facet}
        trackerUploadItems={[
          { name: "FIRST", config: {} },
          { name: "SECOND", config: {} },
        ]}
        trackerIconSrcByName={{}}
      />,
    );

    expect(screen.getByRole("heading", { name: "FIRST" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "SECOND" })).toBeInTheDocument();
    expect(screen.getByText("first-payload")).toBeInTheDocument();
    expect(screen.getByText("second-top-level-payload")).toBeInTheDocument();
    expect(screen.getByText("second-debug-payload")).toBeInTheDocument();
    expect(screen.getByText("[13 bytes, 2 lines omitted]")).toBeInTheDocument();
    expect(screen.getByText("[local path]")).toBeInTheDocument();
    expect(screen.getAllByText("[REDACTED]").length).toBeGreaterThanOrEqual(1);

    const renderedText = document.body.textContent || "";
    expect(renderedText.includes("fixture-secret")).toBe(false);
    expect(renderedText.includes("C:\\private")).toBe(false);
  });
});
